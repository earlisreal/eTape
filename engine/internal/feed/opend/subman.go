package opend

import (
	"context"
	"errors"
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
	case feed.SubKLDay:
		return int32(qotcommon.SubType_SubType_KL_Day) // 6
	}
	return 0
}

// WaitActive blocks until a successful subscription ack made key active.
func (m *subManager) WaitActive(ctx context.Context, key subKey) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		_, ok := m.active[key]
		quarantined := m.quarantine[key]
		m.mu.Unlock()
		if ok {
			return nil
		}
		if quarantined {
			return fmt.Errorf("subscription unavailable: %s", key.Symbol)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type subOptions struct {
	Budget        int // quota slots (default 100)
	QuotaHeadroom int
	RequireQuota  bool
	MinHold       time.Duration // default 60s  (moomoo rule)
	Hysteresis    time.Duration // default 5m   (release delay)
	ExtendedTime  bool          // default true (US pre/post)
}

// subQuarantineThreshold is the number of consecutive hard (business-level)
// Qot_Sub rejections a subKey must accumulate before it is quarantined. A
// single rejection can be a transient server-side condition (quota, momentary
// entitlement hiccup) and must still retry-and-recover; a symbol that keeps
// failing this many passes in a row (e.g. an OTC/Pink code with no quote
// entitlement) is permanently unsubscribable and must stop being retried.
const subQuarantineThreshold = 3

// subBizError marks a Qot_Sub rejection that came back as a well-formed
// response with a non-zero RetType (a moomoo business-level rejection, e.g.
// "US OTC market quote is not available for X"), as opposed to a transport
// or decode failure. Only business errors count toward quarantine — a
// transport hiccup is genuinely transient and must keep retrying every pass.
type subBizError struct {
	retType int32
	msg     string
}

func (e *subBizError) Error() string {
	return fmt.Sprintf("qot_sub retType=%d msg=%q", e.retType, e.msg)
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

	mu                   sync.Mutex
	demands              map[string]*demandState
	active               map[subKey]*subState
	starved              map[string]bool
	quarantine           map[subKey]bool // hard-failed keys excluded from desired(); cleared on reconnect
	subFail              map[subKey]int  // consecutive business-error count, toward subQuarantineThreshold
	quotaKnown           bool
	quotaRemain          int
	quotaAt              time.Time
	quotaSpentSinceRead  int
	quotaPending         int
	quotaHolds           int
	quotaPendingDone     chan struct{}
	quotaChanged         chan struct{}
	connectionGeneration uint64
	connectionDown       bool
	connectionCtx        context.Context
	connectionCancel     context.CancelFunc
	kick                 chan struct{}
	activated            func(context.Context, feed.Demand) // queues cache seeds after acknowledgement
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
	if o.QuotaHeadroom < 0 {
		o.QuotaHeadroom = 0
	}
	connectionCtx, connectionCancel := context.WithCancel(context.Background())
	return &subManager{
		rpc: r, clk: clk, opt: o,
		demands:       make(map[string]*demandState),
		active:        make(map[subKey]*subState),
		starved:       make(map[string]bool),
		quarantine:    make(map[subKey]bool),
		subFail:       make(map[subKey]int),
		quotaChanged:  make(chan struct{}),
		connectionCtx: connectionCtx, connectionCancel: connectionCancel,
		kick: make(chan struct{}, 1),
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

// Ensure registers demand and returns its already-acknowledged subtypes under
// the same lock. Later acknowledgements are delivered through activated.
func (m *subManager) Ensure(d feed.Demand) []feed.SubType {
	m.mu.Lock()
	m.demands[d.ID] = &demandState{d: d, lastEnsure: m.clk.Now()}
	var active []feed.SubType
	for _, sub := range d.Subs {
		if _, ok := m.active[subKey{Symbol: d.Symbol, Sub: sub}]; ok {
			active = append(active, sub)
		}
	}
	m.mu.Unlock()
	m.kickWorker()
	return active
}

func (m *subManager) Release(id string) {
	m.mu.Lock()
	delete(m.demands, id)
	m.mu.Unlock()
	m.kickWorker()
}

func (m *subManager) SetSubscriptionQuota(remain int, observedAt time.Time) {
	if remain < 0 || observedAt.IsZero() {
		return
	}
	m.mu.Lock()
	m.quotaKnown, m.quotaRemain, m.quotaAt = true, remain, observedAt
	// The normal quota poll brackets this update with Begin/End below, which
	// waits for in-flight admissions. Preserve local spend if a caller updates
	// the counter without that barrier while requests are pending.
	if m.quotaPending == 0 {
		m.quotaSpentSinceRead = 0
	}
	m.signalQuotaChangeLocked()
	m.mu.Unlock()
	m.kickWorker()
}

// BeginSubscriptionQuotaRefresh pauses new admissions and waits for every
// reserved Qot_Sub batch to settle before GetSubInfo is sent. This keeps a
// counter response from racing an admission and erasing its local debit.
func (m *subManager) BeginSubscriptionQuotaRefresh(ctx context.Context) error {
	m.mu.Lock()
	m.quotaHolds++
	for m.quotaPending > 0 {
		done := m.quotaPendingDone
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			m.EndSubscriptionQuotaRefresh()
			return ctx.Err()
		case <-done:
		}
		m.mu.Lock()
	}
	m.mu.Unlock()
	return nil
}

func (m *subManager) EndSubscriptionQuotaRefresh() {
	m.mu.Lock()
	if m.quotaHolds > 0 {
		m.quotaHolds--
	}
	m.signalQuotaChangeLocked()
	m.mu.Unlock()
	m.kickWorker()
}

func (m *subManager) signalQuotaChangeLocked() {
	if m.quotaChanged != nil {
		close(m.quotaChanged)
	}
	m.quotaChanged = make(chan struct{})
}

func (m *subManager) reserveQuotaAdmissionsLocked(count int) {
	if count <= 0 {
		return
	}
	if m.quotaPending == 0 {
		m.quotaPendingDone = make(chan struct{})
	}
	m.quotaPending += count
}

func (m *subManager) finishQuotaAdmissions(keys []subKey) {
	m.mu.Lock()
	for _, key := range keys {
		if _, active := m.active[key]; active && m.opt.RequireQuota {
			m.quotaSpentSinceRead++
		}
	}
	m.quotaPending -= len(keys)
	if m.quotaPending < 0 {
		m.quotaPending = 0
	}
	if m.quotaPending == 0 && m.quotaPendingDone != nil {
		close(m.quotaPendingDone)
		m.quotaPendingDone = nil
	}
	m.signalQuotaChangeLocked()
	m.mu.Unlock()
}

// ConnectionDown marks retained active slots for guarded replay. Demands remain
// intact, but the manager refuses new admissions until a fresh account-wide
// quota read arrives on the next connection.
func (m *subManager) ConnectionDown() {
	m.mu.Lock()
	if m.connectionCancel != nil {
		m.connectionCancel()
	}
	m.connectionCtx, m.connectionCancel = context.WithCancel(context.Background())
	m.connectionGeneration++
	m.connectionDown = true
	m.quotaKnown = false
	m.quotaSpentSinceRead = 0
	m.signalQuotaChangeLocked()
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
			if m.quarantine[subKey{Symbol: ds.d.Symbol, Sub: s}] {
				continue // hard-failed: excluded from the target set (and its budget slot)
			}
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

	quotaFree := m.opt.Budget
	if m.opt.RequireQuota {
		quotaFree = 0
		if m.quotaKnown && now.Sub(m.quotaAt) <= 75*time.Second {
			quotaFree = m.quotaRemain - m.opt.QuotaHeadroom - m.quotaSpentSinceRead - m.quotaPending
			if quotaFree < 0 {
				quotaFree = 0
			}
		}
	}
	if m.quotaHolds > 0 || m.connectionDown {
		quotaFree = 0
	}
	capSlots := len(m.active) + quotaFree
	if capSlots > m.opt.Budget {
		capSlots = m.opt.Budget
	}
	capSlots -= pinned
	if capSlots < 0 {
		capSlots = 0
	}
	want, starved := m.desired(capSlots)

	var adds []subKey
	for k := range want {
		if _, ok := m.active[k]; !ok {
			adds = append(adds, k)
		}
	}
	type priority struct {
		focused bool
		latest  time.Time
	}
	bySymbol := map[string]priority{}
	for _, ds := range m.demands {
		current := bySymbol[ds.d.Symbol]
		current.focused = current.focused || ds.d.Focused
		if ds.lastEnsure.After(current.latest) {
			current.latest = ds.lastEnsure
		}
		bySymbol[ds.d.Symbol] = current
	}
	sort.Slice(adds, func(i, j int) bool {
		a, b := bySymbol[adds[i].Symbol], bySymbol[adds[j].Symbol]
		if a.focused != b.focused {
			return a.focused
		}
		if !a.latest.Equal(b.latest) {
			return a.latest.After(b.latest)
		}
		if adds[i].Symbol != adds[j].Symbol {
			return adds[i].Symbol < adds[j].Symbol
		}
		return adds[i].Sub < adds[j].Sub
	})
	if m.opt.RequireQuota && len(adds) > quotaFree {
		for _, deferred := range adds[quotaFree:] {
			starved = append(starved, deferred.Symbol)
		}
		adds = adds[:quotaFree]
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
	newStarved := make(map[string]bool, len(starved))
	for _, symbol := range starved {
		newStarved[symbol] = true
		if !m.starved[symbol] {
			slog.Warn("subscription quota pressure: symbol starved", "symbol", symbol, "budget", m.opt.Budget)
		}
	}
	m.starved = newStarved
	generation := m.connectionGeneration
	m.reserveQuotaAdmissionsLocked(len(adds))
	m.mu.Unlock()

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
	for _, group := range groupBySubTypeSet(adds) {
		m.mu.Lock()
		room := !m.opt.RequireQuota || len(m.active)+len(group.keys) <= m.opt.Budget
		m.mu.Unlock()
		if room {
			m.trySubscribe(ctx, group.symbols, group.subs, now, generation)
		}
		m.finishQuotaAdmissions(group.keys)
		if m.activated != nil {
			admitted := make(map[subKey]bool, len(group.keys))
			var seeds []feed.Demand
			m.mu.Lock()
			for _, key := range group.keys {
				_, admitted[key] = m.active[key]
			}
			for _, demand := range m.demands {
				seed := demand.d
				seed.Subs = nil
				for _, sub := range demand.d.Subs {
					if admitted[subKey{Symbol: seed.Symbol, Sub: sub}] {
						seed.Subs = append(seed.Subs, sub)
					}
				}
				if len(seed.Subs) > 0 {
					seeds = append(seeds, seed)
				}
			}
			m.mu.Unlock()
			for _, seed := range seeds {
				m.activated(ctx, seed)
			}
		}
	}
}

// trySubscribe issues Qot_Sub for symbols (all sharing subs, the subtype
// set). On a business rejection (subBizError) it binary-splits the batch to
// isolate the offending symbol(s) — mirroring scan.go's snapshotBatch "one
// bad code fails the batch" idiom — so good symbols in the same batch still
// subscribe on this same pass instead of waiting on the bad one. A symbol
// isolated to a single-element batch that keeps failing accrues subFail;
// once it reaches subQuarantineThreshold it is quarantined (excluded from
// desired(), so it stops being retried) and logged once. A transport/decode
// error is treated as transient: no split, no count, just retry next pass.
func (m *subManager) trySubscribe(ctx context.Context, symbols []string, subs []feed.SubType, now time.Time, generation uint64) {
	if len(symbols) == 0 {
		return
	}
	m.mu.Lock()
	if generation != m.connectionGeneration {
		m.mu.Unlock()
		return
	}
	connectionCtx := m.connectionCtx
	m.mu.Unlock()
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(connectionCtx, cancel)
	defer func() {
		stop()
		cancel()
	}()
	if err := requestCtx.Err(); err != nil {
		return
	}
	err := m.qotSub(requestCtx, symbols, subs, true)
	if err == nil {
		m.mu.Lock()
		if generation == m.connectionGeneration {
			for _, s := range symbols {
				for _, sub := range subs {
					k := subKey{Symbol: s, Sub: sub}
					m.active[k] = &subState{subscribedAt: now}
					delete(m.subFail, k)
				}
			}
		}
		m.mu.Unlock()
		return
	}

	var biz *subBizError
	if !errors.As(err, &biz) {
		slog.Warn("subscribe failed; will retry next pass", "symbols", symbols, "err", err)
		return
	}
	if !IsSymbolSpecificRequestError(biz.msg) {
		slog.Warn("subscribe deferred after provider rejection", "symbols", symbols, "retType", biz.retType, "reason", biz.msg)
		return
	}
	if len(symbols) > 1 {
		mid := len(symbols) / 2
		m.trySubscribe(ctx, symbols[:mid], subs, now, generation)
		m.trySubscribe(ctx, symbols[mid:], subs, now, generation)
		return
	}

	// Isolated to one symbol: count the hard failure toward quarantine.
	sym := symbols[0]
	m.mu.Lock()
	quarantined := false
	for _, sub := range subs {
		k := subKey{Symbol: sym, Sub: sub}
		m.subFail[k]++
		if m.subFail[k] >= subQuarantineThreshold {
			m.quarantine[k] = true
			quarantined = true
		}
	}
	m.mu.Unlock()
	if quarantined {
		slog.Warn("subscribe permanently failing; quarantined until reconnect", "symbol", sym, "err", err)
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
	// RegPushRehabTypeList: only effective for registered K-line-type pushes
	// (K_1M here); every other subscription type ignores it. Left unset, moomoo
	// defaults registered K-line push to forward-adjusted, which diverges from
	// the raw tick/quote scale on a reverse-split symbol (see historyBars' rehab
	// comment) — set it explicitly to unadjusted so live K_1M pushes match the
	// rest of the intraday pipeline. Deviates from the official Python SDK,
	// which never sets this field (quote_query.py pack_sub_or_unsub_req,
	// verified 2026-07-05) and so silently rides the forward-adjusted default;
	// K_1M pushes were confirmed still flowing with this field set (2026-07-03
	// benchmark covered the unset case only — re-verify against live OpenD).
	req := &qotsub.Request{C2S: &qotsub.C2S{
		SecurityList:         secs,
		SubTypeList:          subTypes,
		IsSubOrUnSub:         proto.Bool(subscribe),
		IsRegOrUnRegPush:     proto.Bool(subscribe),
		IsFirstPush:          proto.Bool(subscribe),
		ExtendedTime:         proto.Bool(m.opt.ExtendedTime),
		RegPushRehabTypeList: []int32{int32(qotcommon.RehabType_RehabType_None)},
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
		return &subBizError{retType: resp.GetRetType(), msg: resp.GetRetMsg()}
	}
	return nil
}

// ResubscribeAll waits for a post-reconnect account-wide quota reading, then
// replays the highest-priority retained subscriptions that fit while preserving
// configured headroom. Remaining demand is left to the normal priority pass.
func (m *subManager) ResubscribeAll(ctx context.Context) error {
	for {
		m.mu.Lock()
		wanted := make(map[subKey]bool)
		for _, demand := range m.demands {
			for _, sub := range demand.d.Subs {
				wanted[subKey{Symbol: demand.d.Symbol, Sub: sub}] = true
			}
		}
		var keys []subKey
		for key := range m.active {
			if wanted[key] {
				keys = append(keys, key)
			}
		}
		hasDemand := len(wanted) > 0
		fresh := !m.opt.RequireQuota || m.quotaKnown && m.quotaHolds == 0 && m.clk.Now().Sub(m.quotaAt) <= 75*time.Second
		available := m.opt.Budget
		if m.opt.RequireQuota {
			available = m.quotaRemain - m.opt.QuotaHeadroom - m.quotaSpentSinceRead - m.quotaPending
		}
		if available < 0 {
			available = 0
		}
		if !hasDemand {
			m.active = make(map[subKey]*subState)
			m.connectionDown = false
			m.quarantine = make(map[subKey]bool)
			m.subFail = make(map[subKey]int)
			m.signalQuotaChangeLocked()
			m.mu.Unlock()
			return nil
		}
		if fresh {
			if m.opt.RequireQuota && len(keys) > 0 {
				selected, _ := m.desired(available)
				kept := keys[:0]
				for _, key := range keys {
					if selected[key] {
						kept = append(kept, key)
					}
				}
				keys = kept
			}
			m.quarantine = make(map[subKey]bool)
			m.subFail = make(map[subKey]int)
			if len(keys) == 0 {
				m.active = make(map[subKey]*subState)
				m.connectionDown = false
				m.signalQuotaChangeLocked()
				m.mu.Unlock()
				m.pass(ctx)
				return nil
			}
			m.quotaHolds++
			m.reserveQuotaAdmissionsLocked(len(keys))
			m.active = make(map[subKey]*subState)
			generation := m.connectionGeneration
			m.mu.Unlock()

			now := m.clk.Now()
			for _, group := range groupBySubTypeSet(keys) {
				m.trySubscribe(ctx, group.symbols, group.subs, now, generation)
				m.finishQuotaAdmissions(group.keys)
			}
			m.mu.Lock()
			stillCurrent := generation == m.connectionGeneration
			if stillCurrent {
				m.connectionDown = false
			}
			m.quotaHolds--
			m.signalQuotaChangeLocked()
			m.mu.Unlock()
			if !stillCurrent {
				return context.Canceled
			}
			m.kickWorker()
			m.pass(ctx) // use any remaining budget for demands added while reconnecting
			return nil
		}
		changed := m.quotaChanged
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
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

// DesiredSymbols returns the retained demand union, independent of whether
// the current connection has enough account quota to activate every entry.
func (m *subManager) DesiredSymbols() map[string][]feed.SubType {
	m.mu.Lock()
	defer m.mu.Unlock()
	bySymbol := make(map[string]map[feed.SubType]bool)
	for _, demand := range m.demands {
		for _, sub := range demand.d.Subs {
			key := subKey{Symbol: demand.d.Symbol, Sub: sub}
			if m.quarantine[key] {
				continue
			}
			if bySymbol[demand.d.Symbol] == nil {
				bySymbol[demand.d.Symbol] = make(map[feed.SubType]bool)
			}
			bySymbol[demand.d.Symbol][sub] = true
		}
	}
	out := make(map[string][]feed.SubType, len(bySymbol))
	for symbol, set := range bySymbol {
		for sub := range set {
			out[symbol] = append(out[symbol], sub)
		}
		sort.Slice(out[symbol], func(i, j int) bool { return out[symbol][i] < out[symbol][j] })
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

// Quarantined returns the symbols with at least one hard-failed (permanently
// rejected) subscription key, deduplicated across subtypes.
func (m *subManager) Quarantined() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[string]bool, len(m.quarantine))
	for k := range m.quarantine {
		seen[k.Symbol] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
