// Package alpaca is Alpaca's exec.Broker implementation: REST order
// entry/replace/cancel/flatten/snapshot (rest.go, Task 13), the
// trade_updates WebSocket client (ws.go, Task 14), wire mapping (mapping.go,
// Task 11), event normalization (normalize.go, Task 12), and this file's
// Adapter, which assembles all of the above into the exec.Broker interface
// (Task 15, mirroring how tradezero.go assembles the TradeZero adapter).
//
// The one structural way this adapter is SIMPLER than TradeZero's: Alpaca
// has a native PATCH replace (rest.go's replaceOrder) and a native
// DELETE-all-positions flatten (rest.go's flatten), so there is no
// cancel-then-resubmit emulation, no minted "-rN" id suffix, and no
// leg-conflation ambiguity to resolve in Snapshot/reconcile — a domain
// order's client_order_id is set once at submit time and never changes
// across a replace (trade_updates' "replaced" event echoes the SAME
// client_order_id with the updated qty/price fields; see normalize.go's
// normalizeUpdate and testdata/replaced.json). ReplaceOrder is therefore a
// thin PATCH wrapper: the domain-visible exec.OrderReplaced is produced
// entirely by the WS "replaced" event via normalizeUpdate, not emitted here.
package alpaca

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

// errResetBalanceUnsupported is returned by ResetBalance: a real Alpaca
// account can't be reset. Capabilities().ResetBalance is false so exec.Core
// is not expected to call it in practice — defense in depth.
var errResetBalanceUnsupported = errors.New("alpaca: reset balance unsupported")

// defaultPaperRESTBase, defaultLiveRESTBase, defaultPaperWSURL, and
// defaultLiveWSURL are Alpaca's documented production endpoints
// (docs/2026-07-03-alpaca-api.md): live and paper trading are SEPARATE base
// URLs (unlike TradeZero, where the same host serves both environments and a
// key selects one). Config overrides them for tests (mock servers).
const (
	defaultPaperRESTBase = "https://paper-api.alpaca.markets"
	defaultLiveRESTBase  = "https://api.alpaca.markets"
	defaultPaperWSURL    = "wss://paper-api.alpaca.markets/stream"
	defaultLiveWSURL     = "wss://api.alpaca.markets/stream"
)

// Config configures an Alpaca Adapter. RESTBase/WSURL default from Env
// ("paper" — the safe default, or "live") when left empty; tests supply mock
// server URLs instead.
type Config struct {
	Venue    exec.VenueID
	Env      string // "paper" (default) or "live"
	RESTBase string
	WSURL    string
	Creds    creds.Pair
	Clock    clock.Clock
}

// Adapter is the Alpaca exec.Broker implementation. It owns the REST client
// (order entry/replace/cancel/flatten/snapshot) and the trade_updates
// WebSocket client (live order/fill pushes), and holds only the bookkeeping
// state needed to bridge Alpaca's wire shapes to the broker-agnostic
// exec.Broker contract — deliberately NOT a TradeZero-style replace-tracking
// state machine (replaceState/idByDomain/replacing), since Alpaca's native
// PATCH replace needs none of that (see the package doc).
type Adapter struct {
	venue exec.VenueID
	clk   clock.Clock

	rest *restClient
	ws   *wsClient

	events chan exec.BrokerEvent

	// runCtx is the context Run(ctx) was invoked with. It is only ever read
	// from the wsClient's callbacks, which are only ever invoked from the
	// single goroutine running ws.run(ctx) (itself started by Run on the same
	// goroutine that set runCtx) — no separate synchronization is needed,
	// mirroring tradezero.Adapter.runCtx.
	runCtx context.Context

	mu sync.Mutex
	// seenExecIDs dedups fill/partial_fill events on Alpaca's execution_id
	// (unlike TradeZero, which dedups on cumulative executed qty — Alpaca's
	// trade_updates gives a stable per-execution id directly). Owned by
	// normalizeUpdate (Task 12).
	seenExecIDs map[string]bool
	// sideByID remembers the domain Side of an order's original submission
	// (set by SubmitOrder below) so a later fill reports the side the ORDER
	// was submitted with, rather than re-deriving it from position_qty at
	// fill time. Owned by normalizeUpdate (Task 12), populated here.
	sideByID map[string]exec.Side
	// brokerIDByClientID maps a domain order id (== client_order_id, stable
	// across a native replace) to Alpaca's own order id (the wire "id"
	// field), which ReplaceOrder/CancelOrder must target — Alpaca's
	// PATCH/DELETE /v2/orders/{id} endpoints take Alpaca's id, not
	// client_order_id (only a dedicated GET endpoint, orderByClientID,
	// resolves by client_order_id). A domain id missing here (a fresh
	// process, or an order this adapter instance never saw the submission
	// of) is resolved lazily via resolveBrokerID's orderByClientID fallback
	// rather than requiring Snapshot/reconcile to prepopulate it — Alpaca's
	// domain Order shape (auOrder.domain()) doesn't carry the wire id, only
	// client_order_id, so there is nothing to prepopulate from a snapshot
	// row anyway.
	brokerIDByClientID map[string]string
	// lastKnownStatus is the last domain-visible OrderStatus per domain
	// order id, updated by both live WS pushes (handleUpdate) and
	// reconcile. reconcile uses it two ways: (1) a still-open order's status
	// is compared for logging/consistency only — the real catch-up signal is
	// lastKnownFilledQty, since Alpaca's open-orders snapshot has no
	// separate "did it advance" flag; (2) a domain id that was Working()
	// here but is ABSENT from a reconnect's fresh open-orders list is the
	// signal that it went terminal while disconnected (Alpaca's
	// status=open endpoint omits terminal orders entirely, unlike
	// TradeZero's un-filtered "today" blotter) — resolveMissingOrder then
	// asks orderByClientID which terminal state it actually reached.
	lastKnownStatus map[string]exec.OrderStatus
	// lastKnownFilledQty is the last cumulative filled_qty per domain order
	// id, used ONLY by reconcile's catch-up path (never by live WS fills,
	// which dedup on execution_id via seenExecIDs — a different mechanism
	// for a different purpose). A strictly-greater filled_qty on a later
	// reconcile is the only condition that synthesizes a catch-up
	// exec.OrderFilled; an unchanged value is a deliberate no-op so a
	// repeated reconcile on an unchanged snapshot never re-fires the same
	// fill.
	lastKnownFilledQty map[string]float64
	// connectedOnce distinguishes the very first WS connect (nothing could
	// have been missed yet: no gap diffing, no StreamGap) from a reconnect.
	connectedOnce bool
}

var _ exec.Broker = (*Adapter)(nil)

// New builds an Alpaca Adapter. RESTBase/WSURL fall back to Alpaca's
// documented paper/live endpoints (selected by Env; unset Env defaults to
// paper, the safe choice) when left empty; Clock falls back to
// clock.System{}.
func New(cfg Config) (*Adapter, error) {
	if cfg.Venue == "" {
		return nil, fmt.Errorf("alpaca: config missing venue")
	}
	live := cfg.Env == "live"

	base := cfg.RESTBase
	if base == "" {
		if live {
			base = defaultLiveRESTBase
		} else {
			base = defaultPaperRESTBase
		}
	}
	wsURL := cfg.WSURL
	if wsURL == "" {
		if live {
			wsURL = defaultLiveWSURL
		} else {
			wsURL = defaultPaperWSURL
		}
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.System{}
	}

	a := &Adapter{
		venue:              cfg.Venue,
		clk:                clk,
		events:             make(chan exec.BrokerEvent, 256),
		seenExecIDs:        map[string]bool{},
		sideByID:           map[string]exec.Side{},
		brokerIDByClientID: map[string]string{},
		lastKnownStatus:    map[string]exec.OrderStatus{},
		lastKnownFilledQty: map[string]float64{},
	}
	a.rest = newRESTClient(base, cfg.Creds.KeyID, cfg.Creds.SecretKey, clk)
	a.ws = newWSClient(wsURL, cfg.Creds.KeyID, cfg.Creds.SecretKey, clk, a.handleUpdate, a.handleConn)
	return a, nil
}

// now returns the current time in epoch milliseconds via the injected clock.
func (a *Adapter) now() int64 {
	if a.clk == nil {
		return clock.System{}.Now().UnixMilli()
	}
	return a.clk.Now().UnixMilli()
}

// emit pushes a domain-visible event onto Events(). The channel is generously
// buffered (matching broker/sim's and broker/tradezero's convention) for a
// single slow-ish consumer (exec.Core's writer loop); a blocking send is
// preferred over a lossy one.
func (a *Adapter) emit(e exec.BrokerEvent) { a.events <- e }

func (a *Adapter) Events() <-chan exec.BrokerEvent { return a.events }

// ProbeRTT times a lightweight read-only GET /v2/clock round trip, giving
// eTape's health poller a reachability RTT for Alpaca — the same role
// moomooProbe.ProbeRTT (boot.go) plays for the moomoo OpenD link. This is
// deliberately a control-plane reachability probe, not an order ack/fill
// latency measurement (which only exists by placing real orders; see
// docs/2026-07-06-venue-latency-benchmark.md for that one-off measurement).
// Wall-clock time.Now() is used (not the injected clock) to match
// moomooProbe's convention, since a fake clock would make RTT meaningless.
func (a *Adapter) ProbeRTT(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	err := a.rest.ping(ctx)
	return time.Since(start), err
}

// Capabilities reports Alpaca's native replace, native flatten-all, and
// overnight (Blue Ocean ATS) session support — all true, unlike TradeZero's
// all-false Capabilities.
func (a *Adapter) Capabilities() exec.Capabilities {
	return exec.Capabilities{NativeReplace: true, FlattenAll: true, OvernightSession: true}
}

// SubmitOrder POSTs a new order. Unlike TradeZero (whose REST response IS the
// semantic accept/reject, emitted synchronously), Alpaca's REST submit only
// tells the caller whether the ORDER WAS CREATED; the domain-visible
// OrderAccepted is produced asynchronously by the WS "new" trade_updates
// event via normalizeUpdate/handleUpdate. This method's own job is
// bookkeeping (sideByID, brokerIDByClientID, lastKnownStatus) plus resolving
// the one real ambiguity REST leaves: submitOrder returning an error could
// mean either (a) Alpaca genuinely rejected the order (a structured >=400,
// synchronous — no trade_updates event will EVER follow, since the order was
// never created) or (b) a transport failure lost the response after the
// order actually landed. orderByClientID (Task 13) answers which case this
// is, so a lost-response order is never falsely reported as rejected, and
// (with a lost or spurious network failure) a genuinely rejected order is
// reported as an exec.OrderRejected — the same non-error-return convention
// TradeZero's Adapter uses for a semantic reject — rather than surfacing a
// bare transport error to the caller for a case that will never resolve any
// other way.
func (a *Adapter) SubmitOrder(ctx context.Context, req exec.OrderRequest) (exec.OrderAck, error) {
	if err := req.Validate(); err != nil {
		return exec.OrderAck{}, err
	}

	brokerID, err := a.rest.submitOrder(ctx, req, req.ClientOrderID)
	if err != nil {
		ord, found, lookupErr := a.rest.orderByClientID(ctx, req.ClientOrderID)
		switch {
		case lookupErr != nil:
			// Genuinely can't tell whether the order landed or not; surface
			// the transport error rather than guessing either way.
			return exec.OrderAck{}, fmt.Errorf("alpaca: submit %w (ambiguity probe also failed: %v)", err, lookupErr)
		case found:
			// The submit response was lost but the order landed anyway —
			// exactly the ambiguity orderByClientID exists to resolve.
			// Treat it as accepted; the WS "new" event still carries the
			// domain OrderAccepted.
			brokerID = ord.ID
		default:
			// Confirmed: the order never landed, whether because Alpaca's
			// synchronous validation rejected it or because the request
			// truly never reached Alpaca. No trade_updates event will ever
			// arrive for a client_order_id that was never created, so this
			// is the ONLY place that can report it.
			a.emit(exec.OrderRejected{V: a.venue, OID: req.ClientOrderID, Reason: err.Error(), Ts: a.now()})
			return exec.OrderAck{OrderID: req.ClientOrderID, Accepted: false, Message: err.Error()}, nil
		}
	}

	a.mu.Lock()
	a.sideByID[req.ClientOrderID] = req.Side
	a.brokerIDByClientID[req.ClientOrderID] = brokerID
	a.lastKnownStatus[req.ClientOrderID] = exec.StatusAccepted
	a.lastKnownFilledQty[req.ClientOrderID] = 0
	a.mu.Unlock()

	return exec.OrderAck{OrderID: req.ClientOrderID, Accepted: true}, nil
}

// resolveBrokerID maps a domain order id to Alpaca's own order id, first
// from the in-memory map (the common case: this adapter instance submitted
// or has already reconciled the order), falling back to orderByClientID for
// an order this instance has no record of yet (a fresh process, or an order
// placed outside eTape) — the same lookup SubmitOrder uses for its own
// ambiguity, reused here for a different purpose.
func (a *Adapter) resolveBrokerID(ctx context.Context, domainOID string) (string, error) {
	a.mu.Lock()
	brokerID, ok := a.brokerIDByClientID[domainOID]
	a.mu.Unlock()
	if ok {
		return brokerID, nil
	}

	ord, found, err := a.rest.orderByClientID(ctx, domainOID)
	if err != nil {
		return "", fmt.Errorf("alpaca: resolve broker id for %s: %w", domainOID, err)
	}
	if !found {
		return "", fmt.Errorf("alpaca: unknown order %s", domainOID)
	}
	a.mu.Lock()
	a.brokerIDByClientID[domainOID] = ord.ID
	a.mu.Unlock()
	return ord.ID, nil
}

// ReplaceOrder PATCHes qty/limit/stop — Alpaca's native replace. It does NOT
// emit an exec.OrderReplaced itself: the domain event is produced entirely
// by the WS "replaced" trade_updates event via normalizeUpdate/handleUpdate,
// under the SAME domain order id (see the package doc). This deliberately
// mirrors nothing from TradeZero's ReplaceOrder — there is no cancel, no
// await-confirmation, no resubmit, no replaceState — because none of that
// emulation is needed when the broker itself guarantees atomicity.
//
// domainOID (== the order's original client_order_id, per the package doc)
// is passed through to restClient.replaceOrder so the PATCH body explicitly
// re-sends it: Alpaca's documented PATCH behavior is to AUTO-GENERATE a new
// client_order_id for the replaced order when the field is omitted, which
// would silently mint a fresh id, breaking brokerIDByClientID, the WS
// "replaced" event's correlation back to this domain order, and every
// assumption this whole adapter makes about client_order_id being stable
// across a replace.
func (a *Adapter) ReplaceOrder(ctx context.Context, domainOID string, req exec.ReplaceRequest) error {
	brokerID, err := a.resolveBrokerID(ctx, domainOID)
	if err != nil {
		return fmt.Errorf("alpaca: replace: %w", err)
	}
	return a.rest.replaceOrder(ctx, brokerID, domainOID, req)
}

// CancelOrder DELETEs the order currently backing domainOID.
func (a *Adapter) CancelOrder(ctx context.Context, domainOID string) error {
	brokerID, err := a.resolveBrokerID(ctx, domainOID)
	if err != nil {
		return fmt.Errorf("alpaca: cancel: %w", err)
	}
	return a.rest.cancelOrder(ctx, brokerID)
}

func (a *Adapter) CancelAll(ctx context.Context, symbol string) error {
	return a.rest.cancelAll(ctx, symbol)
}

// Flatten closes every open position via Alpaca's native DELETE
// /v2/positions — unlike TradeZero, which has no close-all primitive at all
// (Capabilities().FlattenAll is true here, false there).
func (a *Adapter) Flatten(ctx context.Context) error {
	return a.rest.flatten(ctx)
}

// ResetBalance is unsupported: a real Alpaca account can't be reset.
// Capabilities().ResetBalance is false so exec.Core never calls this in
// practice.
func (a *Adapter) ResetBalance(context.Context, float64) error {
	return errResetBalanceUnsupported
}

// Snapshot fetches the REST-authoritative account/positions/open-orders view
// and stamps venue on every returned struct. Unlike TradeZero's Snapshot,
// there is no replace-suffix stripping or leg-conflation to resolve here:
// Alpaca's client_order_id is stable across a replace, so a domain order
// never appears under more than one id in the first place.
func (a *Adapter) Snapshot(ctx context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	acct, positions, orders, err := a.rest.snapshot(ctx)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, err
	}
	acct.Venue = a.venue
	for i := range positions {
		positions[i].Venue = a.venue
	}
	for i := range orders {
		orders[i].Venue = a.venue
	}
	return acct, positions, orders, nil
}

// Run starts the trade_updates WebSocket client, which drives connect/
// reconnect and invokes handleConn/handleUpdate. It blocks until ctx is done.
func (a *Adapter) Run(ctx context.Context) {
	a.runCtx = ctx
	a.ws.run(ctx)
}

// handleConn is the wsClient onConn callback. On connect it runs reconcile()
// BEFORE the wsClient's read loop starts reading this connection's frames
// (wsClient.session calls onConn(true) synchronously, then readLoop) — any
// frame the server pushes while reconcile's REST calls are in flight simply
// queues on the OS socket buffer instead of being read yet, which is the
// "buffer -> snapshot -> replay" sequence with no separate application-level
// buffer needed: the frames are "replayed" by the read loop's normal
// processing immediately after reconcile returns. Mirrors
// tradezero.Adapter.handleConn.
func (a *Adapter) handleConn(up bool) {
	if up {
		a.emit(exec.BrokerConnUp{V: a.venue})
		a.reconcile()
	} else {
		a.emit(exec.BrokerConnDown{V: a.venue})
	}
}

// handleUpdate is the wsClient onUpdate callback: normalize, update
// bookkeeping, and emit. Bookkeeping runs BEFORE emit so a consumer that
// reacts to an emitted event by immediately calling ReplaceOrder/CancelOrder
// always sees brokerIDByClientID already populated.
func (a *Adapter) handleUpdate(tu tradeUpdate) {
	evs := a.normalizeUpdate(a.venue, tu)

	if oid := tu.Order.ClientOrderID; oid != "" {
		a.mu.Lock()
		if tu.Order.ID != "" {
			a.brokerIDByClientID[oid] = tu.Order.ID
		}
		a.lastKnownStatus[oid] = restOrderStatusDomain(tu.Order.Status)
		if fq := float64(tu.Order.FilledQty); fq > a.lastKnownFilledQty[oid] {
			a.lastKnownFilledQty[oid] = fq
		}
		a.mu.Unlock()
	}

	for _, e := range evs {
		a.emit(e)
	}
}

// reconcile is the startup/reconnect sequence: REST snapshot -> seed/diff
// state -> emit BrokerAccount + BrokerPositions (+ any transitions missed
// while disconnected, + StreamGap on a reconnect specifically, never on the
// very first connect). Runs synchronously from handleConn(true).
//
// Alpaca's restClient.snapshot lists OPEN orders only
// (`GET /v2/orders?status=open`) — unlike TradeZero's unfiltered "today"
// blotter, a terminal order (filled/canceled/rejected/expired) simply
// disappears from this list rather than appearing with its terminal status.
// That shapes the diff into two independent halves:
//
//  1. An order still present in the open list: only a strictly-greater
//     filled_qty than lastKnownFilledQty implies a missed partial fill
//     (diffOpenOrderLocked). An unchanged filled_qty is a deliberate no-op —
//     this is the property that keeps a repeated reconcile on an unchanged
//     snapshot from re-firing anything.
//  2. A domain id that WAS tracked as Working() before this reconnect but is
//     ABSENT from the fresh open list: its disappearance IS the signal that
//     it went terminal while disconnected, but not which terminal state —
//     resolveMissingOrder asks orderByClientID directly. Once resolved,
//     lastKnownStatus[oid] is no longer Working(), so a later reconcile
//     never re-checks (and can never re-emit for) that id again.
func (a *Adapter) reconcile() {
	ctx := a.runCtx
	if ctx == nil {
		ctx = context.Background()
	}

	acct, positions, orders, err := a.rest.snapshot(ctx)
	if err != nil {
		slog.Warn("alpaca: reconcile: snapshot failed", "err", err)
		return
	}
	acct.Venue = a.venue
	for i := range positions {
		positions[i].Venue = a.venue
	}
	for i := range orders {
		orders[i].Venue = a.venue
	}

	a.mu.Lock()
	reconnect := a.connectedOnce
	a.connectedOnce = true

	openNow := make(map[string]bool, len(orders))
	var gapEvents []exec.BrokerEvent
	var missingTracked []string
	if reconnect {
		for _, o := range orders {
			openNow[o.ID] = true
			gapEvents = append(gapEvents, a.diffOpenOrderLocked(o)...)
		}
		for oid, st := range a.lastKnownStatus {
			if !isWorkingStatus(st) || openNow[oid] {
				continue
			}
			missingTracked = append(missingTracked, oid)
		}
	}

	// Seed/refresh lastKnownFilledQty UNCONDITIONALLY (not just when it grew)
	// and AFTER diffOpenOrderLocked already read the prior value above: a
	// resting order sitting at filled_qty=0 must still get a map entry the
	// first time reconcile sees it (marking it "tracked"), or the NEXT
	// reconcile's diff would treat its first real partial fill as
	// "never tracked" and silently drop the catch-up event entirely.
	for _, o := range orders {
		a.lastKnownStatus[o.ID] = o.Status
		a.lastKnownFilledQty[o.ID] = o.ExecutedQty
	}
	a.mu.Unlock()

	a.emit(exec.BrokerAccount{Account: acct})
	a.emit(exec.BrokerPositions{V: a.venue, Positions: positions})
	for _, e := range gapEvents {
		a.emit(e)
	}
	if reconnect {
		for _, oid := range missingTracked {
			for _, e := range a.resolveMissingOrder(ctx, oid) {
				a.emit(e)
			}
		}
		a.emit(exec.StreamGap{V: a.venue, Ts: a.now()})
	}
}

// isWorkingStatus reports whether an OrderStatus implies the order could
// still be sitting in Alpaca's open-orders list — exec.Order.Working() would
// do the same check on a full Order, but reconcile only has the bare status
// for a tracked-but-now-missing id.
//
// exec.StatusReplaced MUST be included here even though exec.Order.Working()
// itself doesn't special-case it: Order.Working() never actually needs to,
// because state.go's OrderReplaced fold rewrites a replaced order's Status to
// StatusAccepted (see internal/exec/state.go), so a *domain* Order's Status
// field is never literally StatusReplaced. But lastKnownStatus here is
// populated straight from the wire via restOrderStatusDomain (handleUpdate
// sets it to restOrderStatusDomain(tu.Order.Status) for every WS event,
// including "replaced", and resolveMissingOrder below can also observe it via
// orderByClientID) — a RAW wire status, not a folded domain Status. Treating
// StatusReplaced as NOT working here would exclude a just-replaced order from
// every future reconcile permanently: it would never be re-checked against
// the open-orders list, so any fill/cancel/reject that happens after a
// replace while disconnected would be silently dropped forever, and the id
// would stay wrongly "not working" on every subsequent reconcile too.
func isWorkingStatus(s exec.OrderStatus) bool {
	return s == exec.StatusSubmitted || s == exec.StatusAccepted ||
		s == exec.StatusPartiallyFilled || s == exec.StatusReplaced
}

// diffOpenOrderLocked compares a currently-open order's snapshot state
// against lastKnownFilledQty and synthesizes the fill catch-up event implied
// by a filled_qty increase observed while disconnected. Only a strictly
// greater filled_qty produces an event — an order whose filled_qty matches
// what was already recorded (including one this adapter instance has never
// tracked before, where the zero-value default would otherwise wrongly
// replay the order's ENTIRE fill history as one synthetic event) is a
// no-op, not a duplicate. Caller holds a.mu.
func (a *Adapter) diffOpenOrderLocked(o exec.Order) []exec.BrokerEvent {
	prevFilled, tracked := a.lastKnownFilledQty[o.ID]
	if !tracked || o.ExecutedQty <= prevFilled {
		return nil
	}
	return []exec.BrokerEvent{exec.OrderFilled{
		F: exec.Fill{
			Venue: a.venue, OrderID: o.ID, Symbol: o.Symbol, Side: o.Side,
			Qty: o.ExecutedQty - prevFilled, Price: o.AvgFillPrice, TsMs: a.now(),
		},
		CumQty: o.ExecutedQty, LeavesQty: o.LeavesQty, AvgPrice: o.AvgFillPrice,
	}}
}

// resolveMissingOrder looks up the definitive terminal state of a domain
// order that was tracked as still-working before this reconnect but is no
// longer present in the open-orders snapshot. Only orderByClientID (a single
// full REST order fetch) can say which terminal state it reached; a lookup
// or decode failure is logged and produces no event rather than guessing —
// the domain order is left exactly as it last was and will be re-examined on
// the next reconnect.
func (a *Adapter) resolveMissingOrder(ctx context.Context, oid string) []exec.BrokerEvent {
	ord, found, err := a.rest.orderByClientID(ctx, oid)
	if err != nil {
		slog.Warn("alpaca: reconcile: resolve missing order failed", "oid", oid, "err", err)
		return nil
	}
	if !found {
		slog.Warn("alpaca: reconcile: tracked order vanished from the open list with no record at all", "oid", oid)
		return nil
	}
	o := ord.domain()
	ts := a.now()

	a.mu.Lock()
	prevFilled := a.lastKnownFilledQty[oid]
	a.lastKnownStatus[oid] = o.Status
	if o.ExecutedQty > prevFilled {
		a.lastKnownFilledQty[oid] = o.ExecutedQty
	}
	if ord.ID != "" {
		a.brokerIDByClientID[oid] = ord.ID
	}
	a.mu.Unlock()

	var out []exec.BrokerEvent
	if o.ExecutedQty > prevFilled {
		out = append(out, exec.OrderFilled{
			F: exec.Fill{
				Venue: a.venue, OrderID: oid, Symbol: o.Symbol, Side: o.Side,
				Qty: o.ExecutedQty - prevFilled, Price: o.AvgFillPrice, TsMs: ts,
			},
			CumQty: o.ExecutedQty, LeavesQty: o.LeavesQty, AvgPrice: o.AvgFillPrice,
		})
	}
	switch o.Status {
	case exec.StatusCanceled:
		out = append(out, exec.OrderCanceled{V: a.venue, OID: oid, Ts: ts})
	case exec.StatusRejected:
		reason := o.RejectReason
		if reason == "" {
			reason = "rejected"
		}
		out = append(out, exec.OrderRejected{V: a.venue, OID: oid, Reason: reason, Ts: ts})
	case exec.StatusExpired:
		out = append(out, exec.OrderExpired{V: a.venue, OID: oid, Ts: ts})
	case exec.StatusReplaced:
		// orderByClientID caught the order mid-replace: Alpaca answered with
		// the object that just got replaced (status "replaced"/
		// "pending_replace"), not yet a definitive terminal state. Emitting
		// nothing here is deliberate, NOT a drop: lastKnownStatus[oid] was
		// already set to o.Status (StatusReplaced) above, and isWorkingStatus
		// now treats StatusReplaced as working, so this id stays eligible for
		// missingTracked and gets re-examined on the NEXT reconcile instead of
		// being abandoned — the same bug isWorkingStatus's doc comment
		// describes, guarded against here too in case a future edit narrows
		// isWorkingStatus again without touching this switch.
		// StatusFilled: the OrderFilled synthesized above (LeavesQty==0) IS
		// the terminal signal, matching sim's and TradeZero's convention —
		// there is no separate "filled" domain event type.
	}
	return out
}
