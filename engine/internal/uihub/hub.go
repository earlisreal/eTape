package uihub

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// client is the hub's view of a connected UI socket (implemented by *conn, Task 7).
// ck is the outbound coalesce key: "" => the frame is lossless/ordered; a
// non-empty ck => a latest-wins delta the conn may supersede in place if the
// client is slow (see outbox). A false return means the lossless lane
// overflowed its hard cap (a genuinely pathological client); the hub then
// closes+drops it.
type client interface {
	id() uint64
	enqueue(b []byte, ck string) bool
	close()
}

type HubConfig struct {
	MDInterval       time.Duration
	AccountInterval  time.Duration
	PositionInterval time.Duration
	Buf              int // channel buffer depth for md/exec/pub inbound
}

type subReq struct {
	c     client
	topic wsmsg.Topic
}

type pub struct {
	topic   wsmsg.Topic
	key     string
	payload any
}

type ensureDemandReq struct {
	connID uint64
	d      feed.Demand
}

type releaseDemandReq struct {
	connID   uint64
	demandID string
}

// loadOlderReq is a pan-triggered deeper-history request marshaled onto
// Run's goroutine (see Hub.LoadOlder's doc comment for why).
type loadOlderReq struct {
	symbol string
	daily  bool
	done   func(added int, exhausted bool, err error)
}

// dropReport is how a conn's own goroutine (writeLoop, on a write timeout)
// tells Run a client is being dropped, so the resulting ui-drop sys.events
// frame is still built and emitted from Run's own single goroutine -- see
// ReportUIDrop and handleDrop.
type dropReport struct {
	id     uint64
	reason string
}

// backfillResult is how a spawned orch.Backfill goroutine reports its daily-
// fetch outcome back to Run's own goroutine, so backfilled/backfillInflight
// (Run-loop-owned) are only ever touched from Run -- see reportBackfill and
// handleBackfillDone.
type backfillResult struct {
	sym string
	ok  bool
}

// demandInfo is what the hub tracks per (connID, demandID): the symbol, and
// whether the demand is chart-capable (WantsHistory) so a reconnect re-arm
// (rearmBackfill) can tell a chart demand from a watchlist "interest" one.
type demandInfo struct {
	symbol       string
	wantsHistory bool
}

// feedBox lets the (single-write, many-read) feed reference live in an
// atomic.Pointer so SetFeed (called once at boot from main's goroutine) races
// safely with Validate reads in conn goroutines and Ensure/Release in Run.
type feedBox struct{ f Feed }

// backfillBox mirrors feedBox: SetBackfill is called once at boot from main's
// goroutine, after the Hub is already running, so the function pointer needs
// the same atomic-store/atomic-load discipline as the feed reference. The
// injected fn spawns the deep/daily backfill for sym and must call done(ok)
// exactly once when it finishes (ok=false on a failed daily fetch) via
// reportBackfill, so Run can decide whether the symbol needs a retry.
type backfillBox struct {
	fn func(sym string, done func(ok bool))
}

// loadOlderBox mirrors backfillBox/feedBox: SetLoadOlder is called once at
// boot from main's goroutine, after the Hub is already running.
type loadOlderBox struct {
	fn func(sym string, daily bool, done func(added int, exhausted bool, err error))
}

// Hub is a single-goroutine event loop that owns the mirror, the connected-
// client set, and per-topic-class coalescing buffers. Every field below the
// channel declarations is touched only from within Run's goroutine; all other
// goroutines communicate with the hub exclusively via the channels, which is
// what makes the single-writer discipline verifiable with go test -race.
type Hub struct {
	clk clock.Clock
	cfg HubConfig
	m   *mirror

	register        chan client
	unregister      chan client
	subCh           chan subReq
	unsubCh         chan subReq
	ensureDemandCh  chan ensureDemandReq
	releaseDemandCh chan releaseDemandReq
	loadOlderCh     chan loadOlderReq
	demandSnapCh    chan chan []string
	mdCh            chan md.Update
	execCh          chan exec.Update
	pubCh           chan pub
	dropCh          chan dropReport     // conn goroutines -> Run: write-timeout drop reports
	backfillDoneCh  chan backfillResult // backfill goroutines -> Run: daily-fetch outcome
	syncCh          chan chan struct{}  // test barrier
	closed          chan struct{}       // closed when Run returns; unblocks stuck senders

	feedSlot      atomic.Pointer[feedBox]
	backfillSlot  atomic.Pointer[backfillBox]
	loadOlderSlot atomic.Pointer[loadOlderBox]

	// Run-loop-owned:
	clients          map[client]map[wsmsg.Topic]bool
	demands          map[uint64]map[string]demandInfo // connID -> demandID -> demandInfo
	demandLive       map[uint64]bool                  // connID currently registered
	backfilled       map[string]bool                  // symbol -> daily backfill has succeeded (process lifetime)
	backfillInflight map[string]bool                  // symbol -> a backfill spawn is currently running
	pendKeep         map[string]staged                // classMDKeep, flushed on md ticker
	tapePend         map[string][]wsmsg.Tick          // symbol -> accumulated ticks
	acctPend         map[string]staged                // venue -> latest account frame
	posLatest        staged
	posDirty         bool

	// sysEventSeq numbers ui-drop sys.events frames the Hub itself emits
	// (buildSysEvent). It is independent of health.Poller's own seq counter
	// (a drop is detected inside Hub.Run, not the health poller, and the two
	// never share state) -- see buildSysEvent's doc comment.
	sysEventSeq int64
}

func NewHub(clk clock.Clock, cfg HubConfig, m *mirror) *Hub {
	if cfg.Buf <= 0 {
		cfg.Buf = 1024
	}
	return &Hub{
		clk: clk, cfg: cfg, m: m,
		register:         make(chan client),
		unregister:       make(chan client),
		subCh:            make(chan subReq),
		unsubCh:          make(chan subReq),
		ensureDemandCh:   make(chan ensureDemandReq),
		releaseDemandCh:  make(chan releaseDemandReq),
		loadOlderCh:      make(chan loadOlderReq),
		demandSnapCh:     make(chan chan []string),
		mdCh:             make(chan md.Update, cfg.Buf),
		execCh:           make(chan exec.Update, cfg.Buf),
		pubCh:            make(chan pub, cfg.Buf),
		dropCh:           make(chan dropReport, cfg.Buf),
		backfillDoneCh:   make(chan backfillResult, cfg.Buf),
		syncCh:           make(chan chan struct{}),
		closed:           make(chan struct{}),
		clients:          map[client]map[wsmsg.Topic]bool{},
		demands:          map[uint64]map[string]demandInfo{},
		demandLive:       map[uint64]bool{},
		backfilled:       map[string]bool{},
		backfillInflight: map[string]bool{},
		pendKeep:         map[string]staged{},
		tapePend:         map[string][]wsmsg.Tick{},
		acctPend:         map[string]staged{},
	}
}

// Public entry points (safe from any goroutine; they only send on channels).
// Each select races the send against h.closed, which Run closes exactly once
// on the way out, so a call made during or after shutdown returns promptly
// instead of blocking forever on a channel nobody will ever receive from
// again.
func (h *Hub) Register(c client) {
	select {
	case h.register <- c:
	case <-h.closed:
	}
}

func (h *Hub) Unregister(c client) {
	select {
	case h.unregister <- c:
	case <-h.closed:
	}
}

func (h *Hub) Subscribe(c client, t wsmsg.Topic) {
	select {
	case h.subCh <- subReq{c, t}:
	case <-h.closed:
	}
}

func (h *Hub) Unsubscribe(c client, t wsmsg.Topic) {
	select {
	case h.unsubCh <- subReq{c, t}:
	case <-h.closed:
	}
}

// SetFeed injects the market-data control surface after the hub is running.
// Safe to call once from boot; nil until then (replay/tests never call it).
func (h *Hub) SetFeed(f Feed) { h.feedSlot.Store(&feedBox{f: f}) }

func (h *Hub) feed() Feed {
	if b := h.feedSlot.Load(); b != nil {
		return b.f
	}
	return nil
}

// SetBackfill injects the deep-history backfill trigger (spawns an
// orch.Backfill goroutine for a symbol, reporting its daily-fetch outcome via
// done) after the hub is running. Safe to call once from boot; nil until then
// (replay/tests/backfill-disabled never call it, in which case chart-open
// demands simply skip the deep backfill).
func (h *Hub) SetBackfill(fn func(sym string, done func(ok bool))) {
	h.backfillSlot.Store(&backfillBox{fn: fn})
}

func (h *Hub) backfill() func(sym string, done func(ok bool)) {
	if b := h.backfillSlot.Load(); b != nil {
		return b.fn
	}
	return nil
}

// SetLoadOlder injects the pan-triggered deeper-history fetch trigger after
// the hub is running. Safe to call once from boot; nil until then (replay
// without a fallback orchestrator, or tests, never call it) — LoadOlder then
// acks exhausted with no fetch attempted.
func (h *Hub) SetLoadOlder(fn func(sym string, daily bool, done func(added int, exhausted bool, err error))) {
	h.loadOlderSlot.Store(&loadOlderBox{fn: fn})
}

func (h *Hub) loadOlderFn() func(sym string, daily bool, done func(added int, exhausted bool, err error)) {
	if b := h.loadOlderSlot.Load(); b != nil {
		return b.fn
	}
	return nil
}

// reportBackfill lets a spawned orch.Backfill goroutine report its daily-
// fetch outcome back to Run's own goroutine -- the same cross-goroutine
// channel-send pattern as ReportUIDrop, so backfilled/backfillInflight stay
// Run-loop-owned (see handleBackfillDone).
func (h *Hub) reportBackfill(sym string, ok bool) {
	select {
	case h.backfillDoneCh <- backfillResult{sym: sym, ok: ok}:
	case <-h.closed:
	}
}

// EnsureDemand records a connection's demand and subscribes it (Run-loop side).
func (h *Hub) EnsureDemand(connID uint64, d feed.Demand) {
	select {
	case h.ensureDemandCh <- ensureDemandReq{connID: connID, d: d}:
	case <-h.closed:
	}
}

// ReleaseDemand forgets a connection's demand and unsubscribes it.
func (h *Hub) ReleaseDemand(connID uint64, demandID string) {
	select {
	case h.releaseDemandCh <- releaseDemandReq{connID: connID, demandID: demandID}:
	case <-h.closed:
	}
}

// LoadOlder marshals a pan-triggered deeper-history request onto Run's
// goroutine so the injected fetcher's backfillWG.Add (if any) always executes
// there — the same safety property triggerBackfill relies on for its own
// WaitGroup, needed here because commands.handle runs on a per-connection
// goroutine, not Run's.
func (h *Hub) LoadOlder(symbol string, daily bool, done func(added int, exhausted bool, err error)) {
	select {
	case h.loadOlderCh <- loadOlderReq{symbol: symbol, daily: daily, done: done}:
	case <-h.closed:
		done(0, true, nil)
	}
}

// ActiveDemandSymbols snapshots the deduped, sorted set of symbols under live
// demand across all connections (including interest demands with no subs).
// Used by the news poller to compose its rotation set.
func (h *Hub) ActiveDemandSymbols() []string {
	reply := make(chan []string, 1)
	select {
	case h.demandSnapCh <- reply:
	case <-h.closed:
		return nil
	}
	select {
	case out := <-reply:
		return out
	case <-h.closed:
		return nil
	}
}

func (h *Hub) PublishMD(u md.Update) {
	select {
	case h.mdCh <- u:
	case <-h.closed:
	}
}

func (h *Hub) PublishExec(u exec.Update) {
	select {
	case h.execCh <- u:
	case <-h.closed:
	}
}

func (h *Hub) Publish(t wsmsg.Topic, key string, p any) {
	select {
	case h.pubCh <- pub{t, key, p}:
	case <-h.closed:
	}
}

// ReportUIDrop lets a conn's own goroutine (writeLoop, on a write timeout)
// tell the Hub a client is being dropped, so the resulting ui-drop
// sys.events frame is still built and emitted from Run's own single
// goroutine -- the same single-writer discipline as every other piece of
// Run-loop-owned state (h.sysEventSeq, the mirror, h.clients). Unlike
// emitUIDrop (called directly by broadcast/sendSnapshot, which already run
// inside Run), this is an ordinary cross-goroutine channel send guarded by
// h.closed: it is not a self-send from inside Run, so none of Publish's
// self-send deadlock risk (see emitUIDrop's doc comment) applies here.
func (h *Hub) ReportUIDrop(id uint64, reason string) {
	select {
	case h.dropCh <- dropReport{id: id, reason: reason}:
	case <-h.closed:
	}
}

// sync is a test-only synchronous barrier: it blocks until the Run loop has
// drained and processed every message sent on the hub's channels before this
// call. It is unexported and used only by hub_test.go's syncHub helper. It
// also races against h.closed so a sync() call made after shutdown returns
// promptly instead of hanging.
func (h *Hub) sync() {
	done := make(chan struct{})
	select {
	case h.syncCh <- done:
	case <-h.closed:
		return
	}
	<-done
}

func (h *Hub) Run(ctx context.Context) error {
	defer close(h.closed)
	mdTick := h.clk.NewTicker(h.cfg.MDInterval)
	acctTick := h.clk.NewTicker(h.cfg.AccountInterval)
	posTick := h.clk.NewTicker(h.cfg.PositionInterval)
	defer mdTick.Stop()
	defer acctTick.Stop()
	defer posTick.Stop()

	for {
		select {
		case <-ctx.Done():
			for c := range h.clients {
				c.close()
			}
			return ctx.Err()
		case c := <-h.register:
			h.handleRegister(c)
		case c := <-h.unregister:
			h.handleUnregister(c)
		case r := <-h.subCh:
			h.handleSub(r)
		case r := <-h.unsubCh:
			h.handleUnsub(r)
		case r := <-h.ensureDemandCh:
			h.handleEnsureDemand(r)
		case r := <-h.releaseDemandCh:
			h.handleReleaseDemand(r)
		case r := <-h.loadOlderCh:
			h.handleLoadOlder(r)
		case reply := <-h.demandSnapCh:
			h.handleDemandSnapshot(reply)
		case u := <-h.mdCh:
			h.handleMD(u)
		case u := <-h.execCh:
			h.handleExec(u)
		case p := <-h.pubCh:
			h.handlePub(p)
		case r := <-h.dropCh:
			h.handleDrop(r)
		case r := <-h.backfillDoneCh:
			h.handleBackfillDone(r)
		case <-mdTick.C():
			h.flushMD()
		case <-acctTick.C():
			h.flushAcct()
		case <-posTick.C():
			if h.posDirty {
				h.broadcast(h.posLatest, false)
				h.posDirty = false
			}
		case done := <-h.syncCh:
			h.drain()
			close(done)
		}
	}
}

// drain non-blockingly services every message currently queued on the
// inbound channels, in an arbitrary but exhaustive order, before a pending
// sync() reply is closed. A channel send happens-before the corresponding
// receive becomes possible, so by the time a test goroutine has returned
// from e.g. PublishMD() and gone on to call sync(), its message is already
// sitting in the buffered channel (or, for the unbuffered register/subCh/
// etc., already being served by drain's own receive). Draining everything
// pending at the moment sync()'s send is serviced therefore guarantees
// "everything published before this sync() call has been applied" even
// though select would otherwise be free to service syncCh first.
func (h *Hub) drain() {
	for {
		select {
		case c := <-h.register:
			h.handleRegister(c)
		case c := <-h.unregister:
			h.handleUnregister(c)
		case r := <-h.subCh:
			h.handleSub(r)
		case r := <-h.unsubCh:
			h.handleUnsub(r)
		case r := <-h.ensureDemandCh:
			h.handleEnsureDemand(r)
		case r := <-h.releaseDemandCh:
			h.handleReleaseDemand(r)
		case r := <-h.loadOlderCh:
			h.handleLoadOlder(r)
		case u := <-h.mdCh:
			h.handleMD(u)
		case u := <-h.execCh:
			h.handleExec(u)
		case p := <-h.pubCh:
			h.handlePub(p)
		case r := <-h.dropCh:
			h.handleDrop(r)
		case r := <-h.backfillDoneCh:
			h.handleBackfillDone(r)
		default:
			return
		}
	}
}

func (h *Hub) handleRegister(c client) {
	h.clients[c] = map[wsmsg.Topic]bool{}
	h.demandLive[c.id()] = true
}

func (h *Hub) handleUnregister(c client) {
	id := c.id()
	if m := h.demands[id]; m != nil {
		if f := h.feed(); f != nil {
			for did := range m {
				f.Release(did)
			}
		}
		delete(h.demands, id)
	}
	delete(h.demandLive, id)
	delete(h.clients, c)
	c.close()
}

func (h *Hub) handleEnsureDemand(r ensureDemandReq) {
	if !h.demandLive[r.connID] {
		return // conn already gone; drop so it can never leak quota
	}
	m := h.demands[r.connID]
	if m == nil {
		m = map[string]demandInfo{}
		h.demands[r.connID] = m
	}
	m[r.d.ID] = demandInfo{symbol: r.d.Symbol, wantsHistory: r.d.WantsHistory}
	if f := h.feed(); f != nil {
		f.Ensure(r.d)
	}
	// Deep-backfill (deep intraday + full daily history) any chart-capable
	// demand whose daily fetch hasn't yet succeeded, so a symbol opened on a
	// chart gets full daily history even if it was never a scanner-pool
	// admission. backfilled is set only once the daily fetch actually
	// succeeds (handleBackfillDone) -- a failed attempt (e.g. OpenD was down)
	// leaves it unset so a later demand, or the reconnect re-arm below
	// (rearmBackfill), retries it. backfillInflight prevents spawning a
	// second goroutine for a symbol still being backfilled. The scan poller
	// runs its own independent, pool-day-scoped dedup for the same underlying
	// orch.Backfill; the two can each fire once for the same symbol, but
	// OpenDFeed.HistoryBars coalesces concurrent same-symbol fetches
	// (singleflight) and the 30-day dedup window covers sequential ones, so
	// a second call spends no additional history quota.
	h.triggerBackfill(r.d.Symbol, r.d.WantsHistory)
}

// triggerBackfill spawns the injected backfill fn for sym if it is a chart-
// capable (wantsHistory) demand whose daily fetch hasn't already succeeded
// and isn't already in flight. Called from handleEnsureDemand (a fresh
// demand) and rearmBackfill (an OpenD reconnect re-arm) -- always from Run's
// own goroutine, so backfilled/backfillInflight need no extra locking.
func (h *Hub) triggerBackfill(sym string, wantsHistory bool) {
	fn := h.backfill()
	if fn == nil || !wantsHistory || h.backfilled[sym] || h.backfillInflight[sym] {
		return
	}
	h.backfillInflight[sym] = true
	fn(sym, func(ok bool) { h.reportBackfill(sym, ok) })
}

// handleLoadOlder runs on Run's own goroutine, so the injected fn's
// backfillWG.Add (main.go's loadOlderFn) is race-safe against the shutdown
// sequence's backfillWG.Wait(), exactly like triggerBackfill's fn.
func (h *Hub) handleLoadOlder(r loadOlderReq) {
	fn := h.loadOlderFn()
	if fn == nil {
		r.done(0, true, nil) // no fetch surface injected — nothing older to serve
		return
	}
	fn(r.symbol, r.daily, r.done)
}

// handleBackfillDone applies a backfill goroutine's reported outcome
// (reportBackfill) on Run's own goroutine: backfilled is set only on
// success, so a failed attempt stays retryable (a later chart-open demand or
// OpenD-reconnect re-arm will try again).
func (h *Hub) handleBackfillDone(r backfillResult) {
	delete(h.backfillInflight, r.sym)
	if r.ok {
		h.backfilled[r.sym] = true
	}
}

// forEachDemand calls fn once for every currently-tracked demandInfo across
// all connections. It does not dedup by symbol -- a symbol can appear more
// than once (e.g. an interest-only watchlist demand and a chart demand for
// the same symbol under different demand IDs); callers that need a unique
// symbol set do their own dedup (see handleDemandSnapshot, rearmBackfill).
func (h *Hub) forEachDemand(fn func(demandInfo)) {
	for _, m := range h.demands {
		for _, info := range m {
			fn(info)
		}
	}
}

// rearmBackfill re-triggers the deep/daily backfill for every symbol
// currently under a chart-capable (wantsHistory) demand that hasn't
// succeeded yet. Called from handleMD on the md.ResyncedUpdate transition --
// i.e. once OpenD reconnects and resubscribes -- so a symbol whose backfill
// failed while OpenD was down (or never fired because the app opened before
// OpenD did) gets a fresh attempt without requiring a UI refresh.
//
// Symbols are collected into a set by ORing wantsHistory across every demand
// for that symbol, not by picking whichever demandInfo a map iteration
// visits first: a symbol can carry both an interest-only demand
// (wantsHistory=false, e.g. a watchlist row) and a chart demand
// (wantsHistory=true) at the same time under different demand IDs, and Go's
// randomized map iteration order must never decide whether the chart demand
// gets re-armed.
func (h *Hub) rearmBackfill() {
	if h.backfill() == nil {
		return // no backfill trigger injected (replay / backfill disabled)
	}
	chartSymbols := map[string]struct{}{}
	h.forEachDemand(func(info demandInfo) {
		if info.wantsHistory {
			chartSymbols[info.symbol] = struct{}{}
		}
	})
	for sym := range chartSymbols {
		h.triggerBackfill(sym, true)
	}
}

func (h *Hub) handleReleaseDemand(r releaseDemandReq) {
	if m := h.demands[r.connID]; m != nil {
		delete(m, r.demandID)
	}
	if f := h.feed(); f != nil {
		f.Release(r.demandID)
	}
}

func (h *Hub) handleDemandSnapshot(reply chan []string) {
	set := map[string]struct{}{}
	h.forEachDemand(func(info demandInfo) { set[info.symbol] = struct{}{} })
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	reply <- out
}

func (h *Hub) handleSub(r subReq) {
	if subs, ok := h.clients[r.c]; ok {
		subs[r.topic] = true
		h.sendSnapshot(r.c, r.topic)
	}
}

func (h *Hub) handleUnsub(r subReq) {
	if subs, ok := h.clients[r.c]; ok {
		delete(subs, r.topic)
	}
}

func (h *Hub) handleMD(u md.Update) {
	for _, s := range h.m.applyMD(u) {
		h.stageMD(s)
	}
	// md.ResyncedUpdate fires once per OpenD reconnect cycle, only after
	// ResubscribeAll succeeds (see opend.OpenDFeed's stateLoop) -- it is
	// naturally edge-triggered (not per keepalive), so re-arming here needs
	// no extra debounce. See rearmBackfill's doc comment for why this is the
	// fix for daily bars not appearing until a reconnect + refresh.
	if _, ok := u.(md.ResyncedUpdate); ok {
		h.rearmBackfill()
	}
}

func (h *Hub) handleExec(u exec.Update) {
	for _, s := range h.m.applyExec(u) {
		h.stageExec(s)
	}
}

func (h *Hub) handlePub(p pub) {
	s := staged{Topic: p.topic, Key: p.key, Payload: p.payload}
	h.m.applyPub(s)
	h.broadcast(s, false)
}

// handleDrop services a dropReport that arrived via dropCh (from a conn's own
// goroutine, e.g. writeLoop's write-timeout path) by emitting its ui-drop
// sys.events frame here on Run's own goroutine.
func (h *Hub) handleDrop(r dropReport) {
	h.emitUIDrop(r.id, r.reason)
}

// buildSysEvent returns a staged sys.events value with the next sequence
// number and current timestamp, in the same shape health.Poller.Event
// produces (Seq/Ts/Kind/Detail) -- but Hub-owned, since a drop is detected
// inside Hub.Run itself, not the health poller. h.sysEventSeq is a separate
// counter from the health poller's own seq field; the two never share state,
// so their sequence numbers are independent (each only numbers events from
// its own source).
func (h *Hub) buildSysEvent(kind, detail string) staged {
	h.sysEventSeq++
	return staged{Topic: wsmsg.TopicSysEvents, Payload: wsmsg.SysEvent{
		Seq: h.sysEventSeq, Ts: h.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Kind: kind, Detail: detail,
	}}
}

// emitUIDrop applies and delivers a single "ui-drop" sys.events frame naming
// clientID and reason. It does what handlePub does (apply to the mirror, then
// deliver to subscribed survivors) but is invoked directly rather than via
// Publish/pubCh: Publish sends on h.pubCh, which Run itself drains, so a
// self-send from inside Run (broadcast/sendSnapshot both run on Run's own
// goroutine) would risk deadlock if pubCh were ever full and Run were blocked
// trying to send to the very channel only it can drain.
//
// Delivery goes through deliverRaw, not broadcast: broadcast is what detects
// drops and calls emitUIDrop in the first place, so calling it again here
// would let a chain of always-failing clients recurse indefinitely (client A
// drops -> emit -> delivering the frame fails for client B -> emit -> ...).
// deliverRaw silently tears down any client that can't even accept the drop
// notification instead of feeding it back into another emission. This is the
// "collect all drops first, emit at most once per broadcast/sendSnapshot call
// for the batch" reentrancy guard: broadcast/sendSnapshot collect their own
// (primary) drops before calling emitUIDrop at all, and nothing downstream of
// emitUIDrop ever calls it again.
//
// Must only be called from Run's own goroutine (directly by broadcast/
// sendSnapshot, or via handleDrop for a channel-reported drop): it touches
// h.sysEventSeq and the mirror without synchronization, like every other
// piece of Run-loop-owned state.
func (h *Hub) emitUIDrop(clientID uint64, reason string) {
	s := h.buildSysEvent("ui-drop", fmt.Sprintf("dropped UI client %d: %s", clientID, reason))
	h.m.applyPub(s)
	if b, err := json.Marshal(wsmsg.DeltaMsg{Kind: "delta", Topic: s.Topic, Key: s.Key, Payload: s.Payload}); err == nil {
		h.deliverRaw(s.Topic, b)
	}
}

// deliverRaw writes b to every currently-registered client subscribed to
// topic, closing and forgetting (but not further instrumenting -- see
// emitUIDrop's doc comment) any whose enqueue fails. sys.events (the only
// topic delivered here) is lossless/ordered, so ck is always "".
func (h *Hub) deliverRaw(topic wsmsg.Topic, b []byte) {
	var dead []client
	for c, subs := range h.clients {
		if subs[topic] {
			if !c.enqueue(b, "") {
				dead = append(dead, c)
			}
		}
	}
	for _, c := range dead {
		delete(h.clients, c)
		c.close()
	}
}

func (h *Hub) stageMD(s staged) {
	switch classify(s.Topic) {
	case classTape:
		ticks, _ := s.Payload.([]wsmsg.Tick)
		sym := ""
		if len(ticks) > 0 {
			sym = ticks[0].Symbol
		}
		h.tapePend[sym] = append(h.tapePend[sym], ticks...)
	case classMDKeep:
		if s.Snap {
			// A bars full-series snapshot (history-seed replacement, see
			// mirror.applyMD's md.BarSnapshot case): broadcast now, on the
			// lossless/ordered lane (outboundCoalesceKey short-circuits to ""
			// for any Snap frame) -- coalescing it into pendKeep like an
			// ordinary keep-latest quote/book/bar delta would let a later
			// dedup-keyed write silently replace it before the next md tick
			// flushes, dropping the whole seeded series.
			h.broadcast(s, true)
			return
		}
		if s.Batch {
			// Batch prepend: broadcast now as a delta on the lossless lane.
			// Keep-latest coalescing would let a later single-bar delta drop it.
			h.broadcast(s, false)
			return
		}
		h.pendKeep[dedupOf(s)] = s
	default: // indicator: immediate; Snap decides snapshot vs delta
		h.broadcast(s, s.Snap)
	}
}

func (h *Hub) stageExec(s staged) {
	switch classify(s.Topic) {
	case classAccount:
		h.acctPend[dedupOf(s)] = s
	case classPositions:
		h.posLatest = s
		h.posDirty = true
	default: // orders, fills, status
		h.broadcast(s, false)
	}
}

func (h *Hub) flushMD() {
	for k, s := range h.pendKeep {
		h.broadcast(s, false)
		delete(h.pendKeep, k)
	}
	for sym, ticks := range h.tapePend {
		if len(ticks) == 0 {
			continue
		}
		h.broadcast(staged{Topic: wsmsg.TopicTape, Payload: ticks}, false)
		delete(h.tapePend, sym)
	}
}

func (h *Hub) flushAcct() {
	for k, s := range h.acctPend {
		h.broadcast(s, false)
		delete(h.acctPend, k)
	}
}

func (h *Hub) broadcast(s staged, snap bool) {
	var b []byte
	var err error
	if snap {
		b, err = json.Marshal(wsmsg.SnapshotMsg{Kind: "snapshot", Topic: s.Topic, Key: s.Key, Payload: s.Payload})
	} else {
		b, err = json.Marshal(wsmsg.DeltaMsg{Kind: "delta", Topic: s.Topic, Key: s.Key, Payload: s.Payload})
	}
	if err != nil {
		return
	}
	// The frame bytes are identical for every subscribed client, so its
	// outbound coalesce key is too -- compute it once. "" => lossless/ordered
	// (every event topic, and every snapshot); non-empty => a latest-wins delta
	// a slow client may supersede in place instead of overflowing (see outbox).
	ck := outboundCoalesceKey(s, snap)
	// Collect every drop from this pass before emitting any ui-drop event
	// (the reentrancy guard emitUIDrop's doc comment describes): iterating
	// h.clients to completion first means `dead` is the complete original
	// batch, so the emit loop below can't itself add to it.
	var dead []client
	for c, subs := range h.clients {
		if subs[s.Topic] {
			if !c.enqueue(b, ck) {
				dead = append(dead, c)
			}
		}
	}
	for _, c := range dead {
		delete(h.clients, c)
		c.close()
		h.emitUIDrop(c.id(), "outbound queue overflow")
	}
}

func (h *Hub) sendSnapshot(c client, topic wsmsg.Topic) {
	for _, fr := range h.m.snapshotFrames(topic) {
		b, err := json.Marshal(wsmsg.SnapshotMsg{Kind: "snapshot", Topic: fr.Topic, Key: fr.Key, Payload: fr.Payload})
		if err != nil {
			continue
		}
		// Every snapshot is lossless/ordered (ck ""): it is the seed a topic's
		// client-side store applies later deltas onto, so it must never be
		// coalesced away or reordered behind a delta.
		if !c.enqueue(b, "") {
			delete(h.clients, c)
			c.close()
			h.emitUIDrop(c.id(), "outbound queue overflow")
			return
		}
	}
}
