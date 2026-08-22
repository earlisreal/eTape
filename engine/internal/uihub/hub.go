package uihub

import (
	"context"
	"encoding/json"
	"errors"
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

// ErrRestarting marks a self-restart so the UI reconnects to the replacement
// engine instead of entering the terminal stopped state.
var ErrRestarting = errors.New("engine restarting")

// client is the hub's view of a connected UI socket (implemented by *conn, Task 7).
// ck is the outbound coalesce key: "" => the frame is lossless/ordered; a
// non-empty ck => a latest-wins delta the conn may supersede in place if the
// client is slow (see outbox). A false return means an explicit outbound bound
// was reached; the hub then closes+drops it rather than hiding loss.
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

// MarketClockSample is a validated estimate of the OpenD upstream market
// clock relative to the engine clock. SampledAt is the engine time at which
// the request completed; the Hub derives the current sample age when it sends
// a pong. The source is optional so demo/replay and older feeds keep the local
// browser-clock fallback.
type MarketClockSample struct {
	OffsetMs  int64
	SampledAt time.Time
	RTT       time.Duration
}

// MarketClockSource supplies the latest OpenD clock estimate. Implementations
// must retain the last valid sample when a later probe fails. The now argument
// is supplied for deterministic tests and future source-specific age checks.
type MarketClockSource interface {
	LatestMarketClock(now time.Time) (MarketClockSample, bool)
}

type marketClockBox struct{ source MarketClockSource }

type subReq struct {
	c     client
	topic wsmsg.Topic
}

type pub struct {
	topic   wsmsg.Topic
	key     string
	payload any
}

type workspaceNotification struct {
	workspaceID string
	revision    int64
	kind        string
}

type ensureDemandReq struct {
	connID uint64
	d      feed.Demand
}

type releaseDemandReq struct {
	connID   uint64
	demandID string
}

type ensureIndicatorReq struct {
	connID uint64
	id     string
	spec   md.IndicatorSpec
}

type releaseIndicatorReq struct {
	connID uint64
	id     string
}

type chartWindowReq struct {
	args  wsmsg.QueryChartWindowArgs
	reply chan wsmsg.QueryChartWindowResult
}

// dropReport is how a conn's own goroutine (writeLoop, on a write timeout)
// tells Run a client is being dropped, so the resulting ui-drop sys.events
// frame is still built and emitted from Run's own single goroutine -- see
// ReportUIDrop and handleDrop.
type dropReport struct {
	id     uint64
	reason string
}

// backfillResult reports focused preparation or archive warming completion
// back to Run's own goroutine.
type backfillResult struct {
	sym     string
	focused bool
	ok      bool
}

// demandInfo tracks the history role so reconnect can re-arm warming.
type demandInfo struct {
	symbol       string
	wantsHistory bool
	focused      bool
}

// feedBox lets the (single-write, many-read) feed reference live in an
// atomic.Pointer so SetFeed (called once at boot from main's goroutine) races
// safely with Validate reads in conn goroutines and Ensure/Release in Run.
type feedBox struct{ f Feed }

// backfillBox mirrors feedBox: its explicit focused and archive callbacks are
// installed after the Hub is running, so the pointers use atomic publication.
type backfillBox struct {
	prepare func(sym string, done func(ok bool))
	warm    func(sym string, done func(ok bool))
}

type cachedDailyBox struct{ fn func(sym string) }

// Hub is a single-goroutine event loop that owns the mirror, the connected-
// client set, and per-topic-class coalescing buffers. Every field below the
// channel declarations is touched only from within Run's goroutine; all other
// goroutines communicate with the hub exclusively via the channels, which is
// what makes the single-writer discipline verifiable with go test -race.
type Hub struct {
	clk clock.Clock
	cfg HubConfig
	m   *mirror
	// cmd is a back-reference to the commands value New builds alongside this
	// Hub, set exactly once in New before Run (or any conn goroutine) starts —
	// see New's `h.cmd = cmd` and the command handler setup. Never reassigned
	// after that, so reading it from the connection goroutines is race-free.
	cmd *commands

	register           chan client
	unregister         chan client
	subCh              chan subReq
	unsubCh            chan subReq
	ensureDemandCh     chan ensureDemandReq
	releaseDemandCh    chan releaseDemandReq
	ensureIndicatorCh  chan ensureIndicatorReq
	releaseIndicatorCh chan releaseIndicatorReq
	chartWindowCh      chan chartWindowReq
	demandSnapCh       chan chan []string
	mdCh               chan md.Update
	execCh             chan exec.Update
	pubCh              chan pub
	workspaceCh        chan workspaceNotification
	dropCh             chan dropReport     // conn goroutines -> Run: write-timeout drop reports
	backfillDoneCh     chan backfillResult // backfill goroutines -> Run: daily-fetch outcome
	syncCh             chan chan struct{}  // test barrier
	closed             chan struct{}       // closed when Run returns; unblocks stuck senders

	feedSlot        atomic.Pointer[feedBox]
	backfillSlot    atomic.Pointer[backfillBox]
	cachedDailySlot atomic.Pointer[cachedDailyBox]
	marketClockSlot atomic.Pointer[marketClockBox]

	// ind is the md.Core surface EnsureIndicator/ReleaseIndicator forward to.
	// Unlike feedSlot/backfillSlot (injected asynchronously,
	// after Run is already goroutine-scheduled, hence the atomic.Pointer
	// boxes), ind is set once via SetIndicators inside uihub.New -- before the
	// caller ever starts Run's goroutine -- and is read only from Run's own
	// goroutine (handleEnsureIndicator/handleReleaseIndicator/
	// handleUnregister), so a plain field is race-safe here.
	ind Indicators

	// Run-loop-owned:
	clients         map[client]map[wsmsg.Topic]bool
	demands         map[uint64]map[string]demandInfo // connID -> demandID -> demandInfo
	demandLive      map[uint64]bool                  // connID currently registered
	indicators      map[uint64]map[string]bool       // connID -> instanceIDs owned by that connection
	warmed          map[string]bool                  // symbol -> archive warm succeeded
	warmInflight    map[string]bool                  // symbol -> archive warm currently running
	prepareInflight map[string]bool                  // symbol -> focused preparation currently running
	pendKeep        map[string]staged                // classMDKeep, flushed on md ticker
	tapePend        map[string][]wsmsg.Tick          // symbol -> accumulated ticks
	acctPend        map[string]staged                // venue -> latest account frame
	posLatest       staged
	posDirty        bool

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
		register:           make(chan client),
		unregister:         make(chan client),
		subCh:              make(chan subReq),
		unsubCh:            make(chan subReq),
		ensureDemandCh:     make(chan ensureDemandReq),
		releaseDemandCh:    make(chan releaseDemandReq),
		ensureIndicatorCh:  make(chan ensureIndicatorReq),
		releaseIndicatorCh: make(chan releaseIndicatorReq),
		chartWindowCh:      make(chan chartWindowReq),
		demandSnapCh:       make(chan chan []string),
		mdCh:               make(chan md.Update, cfg.Buf),
		execCh:             make(chan exec.Update, cfg.Buf),
		pubCh:              make(chan pub, cfg.Buf),
		workspaceCh:        make(chan workspaceNotification, cfg.Buf),
		dropCh:             make(chan dropReport, cfg.Buf),
		backfillDoneCh:     make(chan backfillResult, cfg.Buf),
		syncCh:             make(chan chan struct{}),
		closed:             make(chan struct{}),
		clients:            map[client]map[wsmsg.Topic]bool{},
		demands:            map[uint64]map[string]demandInfo{},
		demandLive:         map[uint64]bool{},
		indicators:         map[uint64]map[string]bool{},
		warmed:             map[string]bool{},
		warmInflight:       map[string]bool{},
		prepareInflight:    map[string]bool{},
		pendKeep:           map[string]staged{},
		tapePend:           map[string][]wsmsg.Tick{},
		acctPend:           map[string]staged{},
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

func (h *Hub) QueryChartWindow(a wsmsg.QueryChartWindowArgs) wsmsg.QueryChartWindowResult {
	reply := make(chan wsmsg.QueryChartWindowResult, 1)
	select {
	case h.chartWindowCh <- chartWindowReq{args: a, reply: reply}:
	case <-h.closed:
		return wsmsg.QueryChartWindowResult{Symbol: a.Symbol, Timeframe: a.Timeframe, Bars: []wsmsg.Bar{}, Indicators: []wsmsg.IndicatorSeriesWindow{}}
	}
	select {
	case out := <-reply:
		return out
	case <-h.closed:
		return wsmsg.QueryChartWindowResult{Symbol: a.Symbol, Timeframe: a.Timeframe, Bars: []wsmsg.Bar{}, Indicators: []wsmsg.IndicatorSeriesWindow{}}
	}
}

// SetIndicators injects the md.Core surface EnsureIndicator/ReleaseIndicator
// forward to. Unlike SetFeed (called after the hub is already running), this
// is called synchronously inside uihub.New -- before the caller starts Run's
// goroutine -- so h.ind needs no atomic box (see the Hub struct's doc comment
// on the field). Every forwarding call nil-guards h.ind so tests that never
// call this (NewHubForTest leaves it nil) still exercise the bookkeeping.
func (h *Hub) SetIndicators(i Indicators) { h.ind = i }

// SetFeed injects the market-data control surface after the hub is running.
// Safe to call once from boot; nil until then (replay/tests never call it).
func (h *Hub) SetFeed(f Feed) { h.feedSlot.Store(&feedBox{f: f}) }

// SetMarketClockSource publishes the optional upstream-clock source. It is
// installed after the Hub is already running in live mode, so the atomic slot
// keeps concurrent ping dispatches race-free. Passing nil restores local-clock
// fallback for demo/replay or after a feed teardown.
func (h *Hub) SetMarketClockSource(source MarketClockSource) {
	if source == nil {
		h.marketClockSlot.Store(nil)
		return
	}
	h.marketClockSlot.Store(&marketClockBox{source: source})
}

func (h *Hub) pong(t int64) wsmsg.PongMsg {
	msg := wsmsg.PongMsg{Kind: "pong", T: t}
	box := h.marketClockSlot.Load()
	if box == nil || box.source == nil {
		return msg
	}
	now := h.clk.Now()
	sample, ok := box.source.LatestMarketClock(now)
	if !ok || sample.SampledAt.IsZero() {
		return msg
	}
	engineMs := now.UnixMilli()
	offsetMs := sample.OffsetMs
	ageMs := now.Sub(sample.SampledAt).Milliseconds()
	if ageMs < 0 {
		ageMs = 0
	}
	rttMs := sample.RTT.Milliseconds()
	if rttMs < 0 {
		rttMs = 0
	}
	msg.EngineTimeMs = &engineMs
	msg.MarketOffsetMs = &offsetMs
	msg.MarketSampleAgeMs = &ageMs
	msg.MarketSampleRttMs = &rttMs
	return msg
}

// SetKnownSymbol installs a positive-only local archive lookup used to avoid
// blocking chart switches on OpenD validation for previously seen symbols.
func (h *Hub) SetKnownSymbol(fn func(string) bool) {
	if fn != nil {
		h.cmd.knownSymbol.Store(&knownSymbolBox{fn: fn})
	}
}

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
	if fn == nil {
		h.backfillSlot.Store(nil)
		return
	}
	h.backfillSlot.Store(&backfillBox{
		prepare: fn,
		warm:    fn,
	})
}

// SetHistoryWarm installs explicit focused preparation and archive-only roles.
func (h *Hub) SetHistoryWarm(
	prepare func(sym string, done func(ok bool)),
	warm func(sym string, done func(ok bool)),
) {
	if prepare == nil && warm == nil {
		h.backfillSlot.Store(nil)
		return
	}
	h.backfillSlot.Store(&backfillBox{prepare: prepare, warm: warm})
}

// SetCachedDaily injects chart-only, memory-only daily cache seeding.
func (h *Hub) SetCachedDaily(fn func(sym string)) {
	if fn == nil {
		h.cachedDailySlot.Store(nil)
		return
	}
	h.cachedDailySlot.Store(&cachedDailyBox{fn: fn})
}

func (h *Hub) cachedDaily() func(string) {
	if b := h.cachedDailySlot.Load(); b != nil {
		return b.fn
	}
	return nil
}

func (h *Hub) backfill() *backfillBox { return h.backfillSlot.Load() }

func (h *Hub) ValidateSymbol(ctx context.Context, symbol string) error {
	if h == nil || h.cmd == nil {
		return nil
	}
	return h.cmd.validateSymbol(ctx, symbol)
}

// reportBackfill returns worker completion to Run's own goroutine.
func (h *Hub) reportBackfill(sym string, focused, ok bool) {
	select {
	case h.backfillDoneCh <- backfillResult{sym: sym, focused: focused, ok: ok}:
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

// EnsureIndicator records connID's ownership of instance id and forwards to
// the injected md.Core (Run-loop side) -- the Indicators-interface mirror of
// EnsureDemand, so a connection's indicator instances are swept on disconnect
// the same way its market-data demands already are (handleUnregister).
func (h *Hub) EnsureIndicator(connID uint64, id string, spec md.IndicatorSpec) {
	select {
	case h.ensureIndicatorCh <- ensureIndicatorReq{connID: connID, id: id, spec: spec}:
	case <-h.closed:
	}
}

// ReleaseIndicator forgets connID's ownership of instance id and forwards the
// release to the injected md.Core.
func (h *Hub) ReleaseIndicator(connID uint64, id string) {
	select {
	case h.releaseIndicatorCh <- releaseIndicatorReq{connID: connID, id: id}:
	case <-h.closed:
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

// NotifyWorkspace sends a revision hint to the owning Wails Workspace Stream.
// An empty workspaceID is the catalog broadcast; both are low-rate lossless
// frames and never travel through ordinary Wails events.
func (h *Hub) NotifyWorkspace(workspaceID string, revision int64, kind string) {
	select {
	case h.workspaceCh <- workspaceNotification{workspaceID: workspaceID, revision: revision, kind: kind}:
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
			closeCode, closeReason := 1001, "engine stopped"
			if errors.Is(context.Cause(ctx), ErrRestarting) {
				closeCode, closeReason = 1000, "restarting"
			}
			for c := range h.clients {
				if clean, ok := c.(interface{ closeWith(int, string) }); ok {
					clean.closeWith(closeCode, closeReason)
				} else {
					c.close()
				}
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
		case r := <-h.ensureIndicatorCh:
			h.handleEnsureIndicator(r)
		case r := <-h.releaseIndicatorCh:
			h.handleReleaseIndicator(r)
		case r := <-h.chartWindowCh:
			h.handleChartWindow(r)
		case reply := <-h.demandSnapCh:
			h.handleDemandSnapshot(reply)
		case u := <-h.mdCh:
			h.handleMD(u)
		case u := <-h.execCh:
			h.handleExec(u)
		case p := <-h.pubCh:
			h.handlePub(p)
		case n := <-h.workspaceCh:
			h.handleWorkspaceNotification(n)
		case r := <-h.dropCh:
			h.handleDrop(r)
		case r := <-h.backfillDoneCh:
			h.handleBackfillDone(r)
		case <-mdTick.C():
			for _, s := range h.m.advanceSignificance(h.clk.Now()) {
				h.stageMD(s)
			}
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
		case r := <-h.ensureIndicatorCh:
			h.handleEnsureIndicator(r)
		case r := <-h.releaseIndicatorCh:
			h.handleReleaseIndicator(r)
		case r := <-h.chartWindowCh:
			h.handleChartWindow(r)
		case u := <-h.mdCh:
			h.handleMD(u)
		case u := <-h.execCh:
			h.handleExec(u)
		case p := <-h.pubCh:
			h.handlePub(p)
		case n := <-h.workspaceCh:
			h.handleWorkspaceNotification(n)
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
	if m := h.indicators[id]; m != nil {
		if h.ind != nil {
			for iid := range m {
				h.ind.ReleaseIndicator(id, iid)
			}
		}
		delete(h.indicators, id)
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
	m[r.d.ID] = demandInfo{symbol: r.d.Symbol, wantsHistory: r.d.WantsHistory, focused: r.d.Focused}
	if f := h.feed(); f != nil {
		f.Ensure(r.d)
	}
	if r.d.CachedDaily {
		if fn := h.cachedDaily(); fn != nil {
			fn(r.d.Symbol)
		}
	}
	// Focused demands prepare one chart snapshot; watch/scanner demands warm
	// only the archive. The role comes from Demand.Focused, never day counts.
	h.triggerBackfill(r.d.Symbol, r.d.WantsHistory, r.d.Focused)
}

// triggerBackfill dispatches an explicit focused or archive-only worker.
func (h *Hub) triggerBackfill(sym string, wantsHistory, focused bool) {
	worker := h.backfill()
	if focused {
		if worker == nil || worker.prepare == nil {
			event := h.buildSysEvent("chart-ready", sym)
			h.m.applyPub(event)
			h.broadcast(event, false)
			return
		}
		if h.prepareInflight[sym] {
			return
		}
		h.prepareInflight[sym] = true
		worker.prepare(sym, func(ok bool) { h.reportBackfill(sym, true, ok) })
		return
	}
	if worker == nil || worker.warm == nil || !wantsHistory || h.warmed[sym] || h.warmInflight[sym] {
		return
	}
	h.warmInflight[sym] = true
	worker.warm(sym, func(ok bool) { h.reportBackfill(sym, false, ok) })
}

func (h *Hub) handleChartWindow(r chartWindowReq) {
	a := r.args
	result := wsmsg.QueryChartWindowResult{
		Symbol: a.Symbol, Timeframe: a.Timeframe, FromMs: a.FromMs, ToMs: a.ToMs,
		Bars: []wsmsg.Bar{}, Indicators: []wsmsg.IndicatorSeriesWindow{}, HistoryRevision: h.m.historyRevision,
	}
	all := h.m.bars[barKey(a.Symbol, a.Timeframe)]
	from, to := a.FromMs, a.ToMs
	if !a.SkipBars {
		if a.TailBars > 0 {
			start := len(all) - a.TailBars
			if start < 0 {
				start = 0
			}
			result.Bars = append(result.Bars, all[start:]...)
			if len(result.Bars) > 0 {
				from = wireBarMs(result.Bars[0])
				to = wireBarMs(result.Bars[len(result.Bars)-1]) + 1
				result.FromMs, result.ToMs = from, to
			}
		} else if from < to {
			lo := sort.Search(len(all), func(i int) bool { return wireBarMs(all[i]) >= from })
			hi := sort.Search(len(all), func(i int) bool { return wireBarMs(all[i]) >= to })
			result.Bars = append(result.Bars, all[lo:hi]...)
		}
	}
	for _, key := range a.IndicatorSeriesKeys {
		points := h.m.indicators[key]
		lo := sort.Search(len(points), func(i int) bool { return points[i].TimeMs >= from })
		hi := sort.Search(len(points), func(i int) bool { return points[i].TimeMs >= to })
		window := make([]wsmsg.IndicatorPoint, 0, hi-lo)
		window = append(window, points[lo:hi]...)
		result.Indicators = append(result.Indicators, wsmsg.IndicatorSeriesWindow{SeriesKey: key, Points: window})
	}
	r.reply <- result
}

func wireBarMs(b wsmsg.Bar) int64 {
	t, err := time.Parse(time.RFC3339Nano, b.BucketStart)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// handleBackfillDone clears focused in-flight state or records archive success.
func (h *Hub) handleBackfillDone(r backfillResult) {
	if r.focused {
		delete(h.prepareInflight, r.sym)
		return
	}
	delete(h.warmInflight, r.sym)
	if r.ok {
		h.warmed[r.sym] = true
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

// rearmBackfill re-triggers history warming for every symbol
// currently under a history-bearing demand that hasn't
// succeeded yet. Called from handleMD on the md.ResyncedUpdate transition --
// i.e. once OpenD reconnects and resubscribes -- so a symbol whose backfill
// failed while OpenD was down (or never fired because the app opened before
// OpenD did) gets a fresh attempt without requiring a UI refresh.
//
// Focused demands win over archive-only watch demands for a shared symbol.
func (h *Hub) rearmBackfill() {
	if h.backfill() == nil {
		return // no backfill trigger injected (replay / backfill disabled)
	}
	targets := map[string]demandInfo{}
	h.forEachDemand(func(info demandInfo) {
		current := targets[info.symbol]
		if info.focused || (!current.focused && info.wantsHistory) {
			targets[info.symbol] = info
		}
	})
	for sym, target := range targets {
		h.triggerBackfill(sym, target.wantsHistory, target.focused)
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

// handleEnsureIndicator mirrors handleEnsureDemand: it reuses the existing
// demandLive liveness tracker (rather than a parallel one) so a late ensure
// for an already-unregistered connection is dropped the same way a late
// demand ensure is, then records r.id under h.indicators[r.connID] and
// forwards to the injected md.Core so ownership is tracked on both sides --
// the Hub only needs to know WHICH ids to release on disconnect; md.Core's
// owner set (indicator.go) handles the actual idempotency/correctness.
func (h *Hub) handleEnsureIndicator(r ensureIndicatorReq) {
	if !h.demandLive[r.connID] {
		return // conn already gone; drop so it can never leak the instance
	}
	m := h.indicators[r.connID]
	if m == nil {
		m = map[string]bool{}
		h.indicators[r.connID] = m
	}
	m[r.id] = true
	if h.ind != nil {
		h.ind.EnsureIndicator(r.connID, r.id, r.spec)
	}
}

// handleReleaseIndicator mirrors handleReleaseDemand.
func (h *Hub) handleReleaseIndicator(r releaseIndicatorReq) {
	if m := h.indicators[r.connID]; m != nil {
		delete(m, r.id)
	}
	if h.ind != nil {
		h.ind.ReleaseIndicator(r.connID, r.id)
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
	if conn, ok := u.(md.ConnUpdate); ok {
		kind := "feed-down"
		detail := "moomoo OpenD feed disconnected"
		if conn.Up {
			kind = "feed-up"
			detail = "moomoo OpenD feed connected"
		}
		event := h.buildSysEvent(kind, detail)
		h.m.applyPub(event)
		h.broadcast(event, false)
	}
	if ready, ok := u.(md.HistoryReadyUpdate); ok {
		kind := "history-ready"
		if ready.Prepared {
			kind = "chart-ready"
		}
		event := h.buildSysEvent(kind, ready.Symbol)
		h.m.applyPub(event)
		h.broadcast(event, false)
	}
	if ready, ok := u.(md.IndicatorReadyUpdate); ok {
		event := h.buildSysEvent("indicator-ready", ready.InstanceID)
		h.m.applyPub(event)
		h.broadcast(event, false)
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

func (h *Hub) handleWorkspaceNotification(n workspaceNotification) {
	b, err := json.Marshal(wsmsg.DeltaMsg{
		Kind:  "delta",
		Topic: wsmsg.TopicWorkspace,
		Key:   n.workspaceID,
		Payload: wsmsg.WorkspaceInvalidation{
			WorkspaceID: n.workspaceID,
			Kind:        n.kind,
			Revision:    n.revision,
		},
	})
	if err != nil {
		return
	}
	var dead []client
	for c, subs := range h.clients {
		if !subs[wsmsg.TopicWorkspace] {
			continue
		}
		if n.workspaceID != "" {
			owner, ok := c.(interface{ workspaceID() string })
			if !ok || owner.workspaceID() != n.workspaceID {
				continue
			}
		}
		if !c.enqueue(b, "") {
			dead = append(dead, c)
		}
	}
	for _, c := range dead {
		delete(h.clients, c)
		c.close()
		h.emitUIDrop(c.id(), "outbound queue overflow")
	}
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
	// Seed/reseed batches update mirror only. Chart clients pull exact windows;
	// only live single-point deltas travel through topic broadcasts.
	if (s.Topic == wsmsg.TopicBars && (s.Snap || s.Batch)) || (s.Topic == wsmsg.TopicIndicator && s.Snap) {
		return
	}
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
	if topic == wsmsg.TopicBars || topic == wsmsg.TopicIndicator {
		return
	}
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
