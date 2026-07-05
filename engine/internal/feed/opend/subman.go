package opend

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotsub"
)

// rpc is the request seam (satisfied by *Client) so the manager and backfill
// are testable without a socket.
type rpc interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (Frame, error)
}

func pbSubType(s feed.SubType) int32 {
	switch s {
	case feed.SubQuote:
		return int32(qotcommon.SubType_SubType_Basic) // 1
	case feed.SubBook:
		return int32(qotcommon.SubType_SubType_OrderBook) // 2
	case feed.SubTicker:
		return int32(qotcommon.SubType_SubType_Ticker) // 4
	case feed.SubKL1m:
		return int32(qotcommon.SubType_SubType_KL_1Min) // 11
	}
	return 0
}

type subOptions struct {
	Budget       int           // quota slots (default 100)
	MinHold      time.Duration // default 60s  (moomoo rule)
	Hysteresis   time.Duration // default 5m   (release delay)
	ExtendedTime bool          // default true (US pre/post)
}

type subKey struct {
	Symbol string
	Sub    feed.SubType
}

type subState struct {
	subscribedAt time.Time
	droppedAt    time.Time // zero while still desired
}

type demandState struct {
	d          feed.Demand
	lastEnsure time.Time
}

// subManager owns the moomoo subscription quota: it is the ONLY component
// that issues Qot_Sub. Consumers declare demands; live subscriptions are the
// union of demands, capped by the slot budget (focused symbols first, then
// most-recently-demanded). Unsubscribes are delayed by MinHold (moomoo's 60 s
// rule) and Hysteresis (symbol-flipping must not churn quota).
type subManager struct {
	rpc rpc
	clk clock.Clock
	opt subOptions

	mu      sync.Mutex
	demands map[string]*demandState
	active  map[subKey]*subState
	starved map[string]bool
	kick    chan struct{}
}

func newSubManager(r rpc, clk clock.Clock, o subOptions) *subManager {
	if o.Budget == 0 {
		o.Budget = 100
	}
	if o.MinHold == 0 {
		o.MinHold = time.Minute
	}
	if o.Hysteresis == 0 {
		o.Hysteresis = 5 * time.Minute
	}
	return &subManager{
		rpc: r, clk: clk, opt: o,
		demands: make(map[string]*demandState),
		active:  make(map[subKey]*subState),
		starved: make(map[string]bool),
		kick:    make(chan struct{}, 1),
	}
}

// Run is the worker loop: reconcile on every kick and once per second (so
// hysteresis deadlines fire without kicks).
func (m *subManager) Run(ctx context.Context) {
	tick := m.clk.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.kick:
		case <-tick.C():
		}
		m.pass(ctx)
	}
}

func (m *subManager) Ensure(d feed.Demand) {
	m.mu.Lock()
	m.demands[d.ID] = &demandState{d: d, lastEnsure: m.clk.Now()}
	m.mu.Unlock()
	m.kickWorker()
}

func (m *subManager) Release(id string) {
	m.mu.Lock()
	delete(m.demands, id)
	m.mu.Unlock()
	m.kickWorker()
}

func (m *subManager) kickWorker() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// desired computes the target set capped to capSlots (the budget minus
// slots pinned by moomoo's min-hold rule). Caller holds m.mu.
func (m *subManager) desired(capSlots int) (map[subKey]bool, []string) {
	type symDemand struct {
		symbol  string
		focused bool
		latest  time.Time
		subs    map[feed.SubType]bool
	}
	bySym := make(map[string]*symDemand)
	for _, ds := range m.demands {
		sd := bySym[ds.d.Symbol]
		if sd == nil {
			sd = &symDemand{symbol: ds.d.Symbol, subs: make(map[feed.SubType]bool)}
			bySym[ds.d.Symbol] = sd
		}
		for _, s := range ds.d.Subs {
			sd.subs[s] = true
		}
		sd.focused = sd.focused || ds.d.Focused
		if ds.lastEnsure.After(sd.latest) {
			sd.latest = ds.lastEnsure
		}
	}
	order := make([]*symDemand, 0, len(bySym))
	for _, sd := range bySym {
		order = append(order, sd)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].focused != order[j].focused {
			return order[i].focused
		}
		if !order[i].latest.Equal(order[j].latest) {
			return order[i].latest.After(order[j].latest)
		}
		return order[i].symbol < order[j].symbol // deterministic tiebreak
	})
	want := make(map[subKey]bool)
	var starved []string
	used := 0
	for _, sd := range order {
		if used+len(sd.subs) > capSlots {
			starved = append(starved, sd.symbol)
			continue
		}
		used += len(sd.subs)
		for s := range sd.subs {
			want[subKey{Symbol: sd.symbol, Sub: s}] = true
		}
	}
	sort.Strings(starved)
	return want, starved
}

// pass reconciles active against desired. Subscribes are batched per
// subtype-group; unsubscribes wait for MinHold+Hysteresis — but hysteresis
// holds a slot only while it's free: under budget pressure, lingering slots
// past MinHold are evicted (oldest droppedAt first, per-slot) to make room.
// MinHold is never waived (moomoo rejects early unsubscribes); demands that
// can't fit while slots are pinned stay starved and retry as holds expire.
// Exposed as a method (not inlined in Run) so tests drive passes synchronously.
func (m *subManager) pass(ctx context.Context) {
	now := m.clk.Now()

	m.mu.Lock()
	// Rule 2: stamp droppedAt on the first pass that sees an entry undesired.
	rawWant := make(map[subKey]bool)
	for _, ds := range m.demands {
		for _, s := range ds.d.Subs {
			rawWant[subKey{Symbol: ds.d.Symbol, Sub: s}] = true
		}
	}
	pinned := 0 // undesired but inside MinHold: nothing can free these yet
	for k, st := range m.active {
		if rawWant[k] {
			st.droppedAt = time.Time{} // re-desired: cancel pending unsubscribe
			continue
		}
		if st.droppedAt.IsZero() {
			st.droppedAt = now
		}
		if now.Sub(st.subscribedAt) < m.opt.MinHold {
			pinned++
		}
	}

	want, starved := m.desired(m.opt.Budget - pinned)
	newStarved := make(map[string]bool, len(starved))
	for _, s := range starved { // log starvation transitions once per change
		newStarved[s] = true
		if !m.starved[s] {
			slog.Warn("subscription quota pressure: symbol starved", "symbol", s, "budget", m.opt.Budget)
		}
	}
	m.starved = newStarved

	var adds []subKey
	for k := range want {
		if _, ok := m.active[k]; !ok {
			adds = append(adds, k)
		}
	}
	var removes []subKey
	removed := make(map[subKey]bool)
	for k, st := range m.active {
		if want[k] || st.droppedAt.IsZero() {
			continue
		}
		if now.Sub(st.subscribedAt) >= m.opt.MinHold && now.Sub(st.droppedAt) >= m.opt.Hysteresis {
			removes = append(removes, k)
			removed[k] = true
		}
	}
	// Rule 3: pressure eviction — free enough hysteresis-held slots for adds.
	if projected := len(m.active) - len(removes) + len(adds); projected > m.opt.Budget {
		var lingering []subKey
		for k, st := range m.active {
			if want[k] || removed[k] || st.droppedAt.IsZero() {
				continue
			}
			if now.Sub(st.subscribedAt) >= m.opt.MinHold {
				lingering = append(lingering, k)
			}
		}
		sort.Slice(lingering, func(i, j int) bool {
			a, b := m.active[lingering[i]], m.active[lingering[j]]
			if !a.droppedAt.Equal(b.droppedAt) {
				return a.droppedAt.Before(b.droppedAt)
			}
			if lingering[i].Symbol != lingering[j].Symbol { // deterministic
				return lingering[i].Symbol < lingering[j].Symbol
			}
			return lingering[i].Sub < lingering[j].Sub
		})
		for _, k := range lingering {
			if projected <= m.opt.Budget {
				break
			}
			removes = append(removes, k)
			removed[k] = true
			projected--
		}
	}
	m.mu.Unlock()

	for _, group := range groupBySubTypeSet(adds) {
		if err := m.qotSub(ctx, group.symbols, group.subs, true); err != nil {
			slog.Warn("subscribe failed; will retry next pass", "symbols", group.symbols, "err", err)
			continue
		}
		m.mu.Lock()
		for _, k := range group.keys {
			m.active[k] = &subState{subscribedAt: now}
		}
		m.mu.Unlock()
	}
	for _, group := range groupBySubTypeSet(removes) {
		if err := m.qotSub(ctx, group.symbols, group.subs, false); err != nil {
			slog.Warn("unsubscribe failed; will retry next pass", "symbols", group.symbols, "err", err)
			continue
		}
		m.mu.Lock()
		for _, k := range group.keys {
			delete(m.active, k)
		}
		m.mu.Unlock()
	}
}

// subGroup is a set of symbols sharing an identical subtype set — Qot_Sub
// subscribes the cross product SecurityList x SubTypeList, so only symbols
// with the same subtype set can share a call.
type subGroup struct {
	symbols []string
	subs    []feed.SubType
	keys    []subKey
}

func groupBySubTypeSet(keys []subKey) []subGroup {
	bySym := make(map[string][]feed.SubType)
	for _, k := range keys {
		bySym[k.Symbol] = append(bySym[k.Symbol], k.Sub)
	}
	bySig := make(map[string]*subGroup)
	for sym, subs := range bySym {
		sort.Slice(subs, func(i, j int) bool { return subs[i] < subs[j] })
		var sig strings.Builder
		for _, s := range subs {
			fmt.Fprintf(&sig, "%d,", s)
		}
		g, ok := bySig[sig.String()]
		if !ok {
			g = &subGroup{subs: subs}
			bySig[sig.String()] = g
		}
		g.symbols = append(g.symbols, sym)
		for _, s := range subs {
			g.keys = append(g.keys, subKey{Symbol: sym, Sub: s})
		}
	}
	out := make([]subGroup, 0, len(bySig))
	for _, g := range bySig {
		sort.Strings(g.symbols) // deterministic call contents
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].symbols[0] < out[j].symbols[0] })
	return out
}

func (m *subManager) qotSub(ctx context.Context, symbols []string, subs []feed.SubType, subscribe bool) error {
	secs := make([]*qotcommon.Security, 0, len(symbols))
	for _, s := range symbols {
		sec, err := parseSymbol(s)
		if err != nil {
			return err
		}
		secs = append(secs, sec)
	}
	subTypes := make([]int32, 0, len(subs))
	for _, s := range subs {
		subTypes = append(subTypes, pbSubType(s))
	}
	// RegPushRehabTypeList is deliberately unset — the official Python SDK
	// never sets it either (quote_query.py pack_sub_or_unsub_req, verified
	// 2026-07-05) and K_1M pushes flow fine (2026-07-03 benchmark).
	req := &qotsub.Request{C2S: &qotsub.C2S{
		SecurityList:     secs,
		SubTypeList:      subTypes,
		IsSubOrUnSub:     proto.Bool(subscribe),
		IsRegOrUnRegPush: proto.Bool(subscribe),
		IsFirstPush:      proto.Bool(subscribe),
		ExtendedTime:     proto.Bool(m.opt.ExtendedTime),
	}}
	f, err := m.rpc.Request(ctx, ProtoQotSub, req)
	if err != nil {
		return err
	}
	var resp qotsub.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		return fmt.Errorf("qot_sub decode: %w", err)
	}
	if resp.GetRetType() != 0 {
		return fmt.Errorf("qot_sub retType=%d msg=%q", resp.GetRetType(), resp.GetRetMsg())
	}
	return nil
}

// ResubscribeAll reissues the full active set (reconnect path) and refreshes
// subscribedAt so MinHold restarts on the new session.
func (m *subManager) ResubscribeAll(ctx context.Context) error {
	m.mu.Lock()
	keys := make([]subKey, 0, len(m.active))
	for k := range m.active {
		keys = append(keys, k)
	}
	m.mu.Unlock()
	now := m.clk.Now()
	for _, group := range groupBySubTypeSet(keys) {
		if err := m.qotSub(ctx, group.symbols, group.subs, true); err != nil {
			return err
		}
	}
	m.mu.Lock()
	for _, st := range m.active {
		st.subscribedAt = now
	}
	m.mu.Unlock()
	return nil
}

// ActiveSymbols returns the live subscription map (symbol → subtypes),
// used by the reconnect reseed.
func (m *subManager) ActiveSymbols() map[string][]feed.SubType {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]feed.SubType)
	for k := range m.active {
		out[k.Symbol] = append(out[k.Symbol], k.Sub)
	}
	for _, subs := range out {
		sort.Slice(subs, func(i, j int) bool { return subs[i] < subs[j] })
	}
	return out
}

func (m *subManager) Slots() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}

func (m *subManager) Starved() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.starved))
	for s := range m.starved {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
