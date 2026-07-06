package tradezero

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

// defaultRESTBase and defaultWSURL are TradeZero's production endpoints;
// Config overrides them for tests (mock servers) and, potentially, a future
// sandbox host.
const (
	defaultRESTBase = "https://webapi.tradezero.com"
	defaultWSURL    = "wss://webapi.tradezero.com/stream"
)

// defaultReplaceCancelTimeout bounds how long ReplaceOrder waits for the real
// terminal Canceled confirming the old leg before aborting the replace
// (per the multi-broker execution design's ~3s figure).
const defaultReplaceCancelTimeout = 3 * time.Second

// errUnsupported is returned by Flatten: TZ has no close-all primitive, and
// Capabilities().FlattenAll is false so exec.Core is not expected to call it
// in practice — this is defense in depth, not a path any config exercises.
var errUnsupported = errors.New("tradezero: flatten unsupported")

// reReplaceSuffix matches the "-rN" suffix the emulated-replace path (below)
// appends to a resubmitted leg's TZ client-order-id.
var reReplaceSuffix = regexp.MustCompile(`-r\d+$`)

// Config configures a TradeZero Adapter. RESTBase/WSURL/Route default to
// TradeZero's production values when left empty (tests supply mock-server
// URLs instead).
type Config struct {
	Venue     exec.VenueID
	AccountID string
	RESTBase  string
	WSURL     string
	Route     string
	Creds     creds.Pair
	Clock     clock.Clock
}

// replaceState tracks one in-flight emulated replace for a domain order id.
// oldTZID is the TZ client-order-id being canceled; confirmed is signaled
// exactly once (buffered so a signal that arrives before ReplaceOrder starts
// waiting isn't lost) by onCanceled when the real terminal Canceled for
// oldTZID (not any other leg) is observed.
type replaceState struct {
	oldTZID   string
	confirmed chan struct{}
}

// Adapter is the TradeZero exec.Broker implementation. It owns the REST
// client (order entry/cancel/snapshot), the Portfolio-WS client (live order +
// position pushes), and the emulated-replace state machine that presents
// TZ's cancel-then-resubmit reality as a single stable domain order id with a
// native-looking exec.OrderReplaced event
// (docs/superpowers/specs/2026-07-04-multi-broker-execution-design.md).
type Adapter struct {
	venue exec.VenueID
	route string // configured default route (e.g. "SMART")
	clk   clock.Clock

	rest *restClient
	ws   *wsClient

	events chan exec.BrokerEvent

	// replaceCancelTimeout overrides defaultReplaceCancelTimeout; tests set
	// this directly on the returned Adapter to exercise the abort path
	// without a real multi-second wait.
	replaceCancelTimeout time.Duration

	// runCtx is the context Run(ctx) was invoked with. It is only ever read
	// from the wsClient's callbacks, which are only ever invoked from the
	// single goroutine running ws.run(ctx) (itself started by Run on the same
	// goroutine that set runCtx) — no separate synchronization is needed.
	runCtx context.Context

	mu sync.Mutex
	// seenExecuted maps a TZ client-order-id (the raw wire id — a replace's
	// old and new legs are different TZ orders and get independent counters)
	// to the last cumulative `executed` quantity seen for it, so
	// normalizeOrder (and the reconcile fill-catch-up path) can dedup fills
	// on (id, executed) instead of re-emitting one per repeated frame.
	seenExecuted map[string]float64
	// tzIDByDomain maps a domain order id to the TZ client-order-id CURRENTLY
	// backing it: itself on first submit, "<id>-r1", "-r2", ... after each
	// successful replace.
	tzIDByDomain map[string]string
	// replaceSeq is the last replace suffix minted for a domain order id, so
	// consecutive replaces produce -r1, -r2, ... without reuse.
	replaceSeq map[string]int
	// replacing holds the in-flight replaceState for a domain order id, set
	// BEFORE the old leg is canceled and cleared once the replace resolves
	// (success or abort). Its presence is what makes onCanceled swallow the
	// old leg's terminal Canceled instead of surfacing it to the domain.
	replacing map[string]*replaceState
	// orderReq caches the last known full OrderRequest for a domain order
	// (symbol/side/type/TIF never change across a replace; only qty/prices
	// do), since exec.ReplaceRequest itself only carries the delta.
	orderReq map[string]exec.OrderRequest
	// positions mirrors the broker's per-symbol position state, updated by
	// both the startup/reconnect snapshot and live Portfolio-WS pushes, and
	// re-emitted in full (exec.Broker.Events wants a full BrokerPositions
	// slice, not a per-symbol delta).
	positions map[string]exec.Position
	// lastKnownStatus is the last domain-visible OrderStatus per domain order
	// id, used by reconcile to detect and synthesize any state transition
	// that happened while the WS connection was down.
	lastKnownStatus map[string]exec.OrderStatus
	// connectedOnce distinguishes the very first WS connect (no gap to
	// report) from a reconnect (StreamGap after the reconcile).
	connectedOnce bool
}

var _ exec.Broker = (*Adapter)(nil)

// New builds a TradeZero Adapter. RESTBase, WSURL, and Route fall back to
// TradeZero's documented defaults when left empty; Clock falls back to
// clock.System{}.
func New(cfg Config) (*Adapter, error) {
	if cfg.Venue == "" {
		return nil, fmt.Errorf("tradezero: config missing venue")
	}
	if cfg.AccountID == "" {
		return nil, fmt.Errorf("tradezero: config missing accountID")
	}
	base := cfg.RESTBase
	if base == "" {
		base = defaultRESTBase
	}
	wsURL := cfg.WSURL
	if wsURL == "" {
		wsURL = defaultWSURL
	}
	route := cfg.Route
	if route == "" {
		route = defaultRoute
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.System{}
	}

	a := &Adapter{
		venue:                cfg.Venue,
		route:                route,
		clk:                  clk,
		events:               make(chan exec.BrokerEvent, 256),
		replaceCancelTimeout: defaultReplaceCancelTimeout,
		seenExecuted:         map[string]float64{},
		tzIDByDomain:         map[string]string{},
		replaceSeq:           map[string]int{},
		replacing:            map[string]*replaceState{},
		orderReq:             map[string]exec.OrderRequest{},
		positions:            map[string]exec.Position{},
		lastKnownStatus:      map[string]exec.OrderStatus{},
	}
	a.rest = newRESTClient(base, cfg.AccountID, cfg.Creds.KeyID, cfg.Creds.SecretKey, clk)
	a.ws = newWSClient(wsURL, cfg.AccountID, cfg.Creds.KeyID, cfg.Creds.SecretKey, clk, a.handleOrder, a.handlePosition, a.handleConn)
	return a, nil
}

// domainID recovers the stable domain order id from a TZ client-order-id by
// stripping a trailing "-rN" replace suffix (identity if there is none). This
// is a pure derivation with no map lookup, so the linkage between a replaced
// order's legs and its one domain id survives a process crash with no
// durable state: after a restart, any inbound frame for "oid-r2" still
// resolves to "oid".
func (a *Adapter) domainID(tzCID string) string {
	return reReplaceSuffix.ReplaceAllString(tzCID, "")
}

// now returns the current time in epoch milliseconds via the injected clock.
func (a *Adapter) now() int64 {
	if a.clk == nil {
		return clock.System{}.Now().UnixMilli()
	}
	return a.clk.Now().UnixMilli()
}

// onCanceled handles a TZ terminal "Canceled" status for tzCID (the raw wire
// id the status arrived for), which normalizes to domain order oid.
//
// If a replace is in flight for oid, this Canceled is not a real order death
// — it is the old leg being torn down so a new leg can be resubmitted under
// the same domain id — so it is swallowed (zero domain events) and instead
// signals the waiting ReplaceOrder goroutine via replaceState.confirmed, but
// only when tzCID actually matches the leg that replace is canceling (guards
// against a stale/duplicate Canceled for an already-superseded leg falsely
// confirming a later replace). Otherwise this is a genuine user/broker-
// initiated cancel and is surfaced as a real exec.OrderCanceled.
func (a *Adapter) onCanceled(venue exec.VenueID, oid, tzCID string, ts int64) []exec.BrokerEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.onCanceledLocked(venue, oid, tzCID, ts)
}

// onCanceledLocked is onCanceled's body, split out so reconcile (which
// already holds a.mu while diffing a snapshot) can call it without
// re-entering the lock.
func (a *Adapter) onCanceledLocked(venue exec.VenueID, oid, tzCID string, ts int64) []exec.BrokerEvent {
	rs, replacing := a.replacing[oid]
	if replacing {
		if rs.oldTZID == tzCID {
			select {
			case rs.confirmed <- struct{}{}:
			default: // already signaled (e.g. a duplicate frame); never block
			}
		}
		return nil
	}
	return []exec.BrokerEvent{exec.OrderCanceled{V: venue, OID: oid, Ts: ts}}
}

// emit pushes a domain-visible event onto Events(). The channel is generously
// buffered (matching broker/sim's convention) for a single slow-ish consumer
// (exec.Core's writer loop); a blocking send is preferred over a lossy one —
// dropping an order-lifecycle event silently would be far worse than a
// momentary backpressure stall.
func (a *Adapter) emit(e exec.BrokerEvent) {
	a.events <- e
}

func (a *Adapter) Events() <-chan exec.BrokerEvent { return a.events }

func (a *Adapter) Capabilities() exec.Capabilities {
	return exec.Capabilities{NativeReplace: false, FlattenAll: false, OvernightSession: false}
}

// pickRoute prefers a route validated against TZ's /routes listing (fetched
// best-effort during reconcile) and falls back to the configured default
// otherwise (e.g. before the first successful fetchRoutes, or in tests whose
// mock server doesn't implement /routes at all).
func (a *Adapter) pickRoute() string {
	if r := a.rest.pickRoute("Stock"); r != "" {
		return r
	}
	return a.route
}

// SubmitOrder POSTs a new order and, since exec.Core's postSubmit only acts
// on a transport error (the OrderAck return is otherwise unused), is
// responsible for putting the domain-visible OrderAccepted/OrderRejected
// onto Events() itself. TZ's REST response already carries the semantic
// outcome (see rest.go's submitOrder), so this is emitted synchronously
// rather than waiting on the Portfolio-WS's own confirming push (which may
// also arrive and re-normalize to the same event — harmless, the fold is
// idempotent for a repeated OrderAccepted).
func (a *Adapter) SubmitOrder(ctx context.Context, req exec.OrderRequest) (exec.OrderAck, error) {
	if err := req.Validate(); err != nil {
		return exec.OrderAck{}, err
	}
	ok, rejText, err := a.rest.submitOrder(ctx, req, req.ClientOrderID, a.pickRoute())
	if err != nil {
		return exec.OrderAck{}, err
	}
	ts := a.now()
	a.mu.Lock()
	a.tzIDByDomain[req.ClientOrderID] = req.ClientOrderID
	a.orderReq[req.ClientOrderID] = req
	a.mu.Unlock()

	if !ok {
		a.emit(exec.OrderRejected{V: a.venue, OID: req.ClientOrderID, Reason: rejText, Ts: ts})
		return exec.OrderAck{OrderID: req.ClientOrderID, Accepted: false, Message: rejText}, nil
	}
	a.emit(exec.OrderAccepted{V: a.venue, OID: req.ClientOrderID, BrokerOrderID: req.ClientOrderID, Ts: ts})
	return exec.OrderAck{OrderID: req.ClientOrderID, Accepted: true}, nil
}

// ReplaceOrder emulates a broker-native replace: TZ has no modify endpoint
// and its client-order-ids are single-use forever (reuse -> R114), so a
// "replace" is cancel-old -> await its REAL terminal confirmation ->
// resubmit-with-a-derived-id, all while the DOMAIN order id the rest of the
// engine sees never changes. See the field docs on `replacing`/`tzIDByDomain`
// and onCanceled for how the swallow-and-signal half of this works.
func (a *Adapter) ReplaceOrder(ctx context.Context, domainOID string, req exec.ReplaceRequest) error {
	a.mu.Lock()
	if _, inFlight := a.replacing[domainOID]; inFlight {
		a.mu.Unlock()
		return fmt.Errorf("tradezero: replace already in flight for order %s", domainOID)
	}
	oldTZID, ok := a.tzIDByDomain[domainOID]
	if !ok {
		oldTZID = domainOID // never replaced before: TZ id == domain id
	}
	orig, ok := a.orderReq[domainOID]
	if !ok {
		a.mu.Unlock()
		return fmt.Errorf("tradezero: replace: unknown order %s", domainOID)
	}
	rs := &replaceState{oldTZID: oldTZID, confirmed: make(chan struct{}, 1)}
	// Mark as replacing BEFORE canceling: onCanceled must see this the
	// instant the old leg's terminal Canceled arrives, or that Canceled would
	// leak to the domain as if the order had simply died.
	a.replacing[domainOID] = rs
	a.mu.Unlock()

	clearReplacing := func() {
		a.mu.Lock()
		delete(a.replacing, domainOID)
		a.mu.Unlock()
	}

	if err := a.rest.cancelOrder(ctx, oldTZID); err != nil {
		clearReplacing()
		return fmt.Errorf("tradezero: replace: cancel of %s failed: %w", oldTZID, err)
	}

	timeout := a.replaceCancelTimeout
	if timeout <= 0 {
		timeout = defaultReplaceCancelTimeout
	}
	select {
	case <-rs.confirmed:
	case <-a.clk.After(timeout):
		clearReplacing()
		return fmt.Errorf("tradezero: replace: timed out waiting for cancel confirmation of %s; order %s left working under its current id", oldTZID, domainOID)
	case <-ctx.Done():
		clearReplacing()
		return fmt.Errorf("tradezero: replace: %w waiting for cancel confirmation of %s", ctx.Err(), oldTZID)
	}

	a.mu.Lock()
	n := a.replaceSeq[domainOID] + 1
	a.replaceSeq[domainOID] = n
	newTZID := fmt.Sprintf("%s-r%d", domainOID, n)
	newReq := orig
	if req.Qty > 0 {
		newReq.Qty = req.Qty
	}
	if req.LimitPrice > 0 {
		newReq.LimitPrice = req.LimitPrice
	}
	if req.StopPrice > 0 {
		newReq.StopPrice = req.StopPrice
	}
	newReq.ClientOrderID = newTZID
	a.mu.Unlock()

	ok2, rejText, err := a.rest.submitOrder(ctx, newReq, newTZID, a.pickRoute())
	if err != nil || !ok2 {
		// The old leg is genuinely gone (its cancel was just confirmed) and
		// the new leg never got accepted: the order is truly dead at the
		// broker, not merely "still working under a stale id". Surface a
		// real terminal cancel rather than leaving the domain's fold
		// believing an id it can no longer act on is still live.
		clearReplacing()
		msg := rejText
		if err != nil {
			msg = err.Error()
		}
		a.emit(exec.OrderCanceled{V: a.venue, OID: domainOID, Ts: a.now()})
		return fmt.Errorf("tradezero: replace: resubmit of %s failed after old leg %s was canceled (order now dead): %s", newTZID, oldTZID, msg)
	}

	a.mu.Lock()
	a.tzIDByDomain[domainOID] = newTZID
	a.orderReq[domainOID] = newReq
	delete(a.replacing, domainOID)
	a.mu.Unlock()

	a.emit(exec.OrderReplaced{
		V: a.venue, OID: domainOID,
		NewQty: newReq.Qty, NewLimit: newReq.LimitPrice, NewStop: newReq.StopPrice,
		Ts: a.now(),
	})
	return nil
}

// CancelOrder cancels the TZ leg currently backing domainOID (its original id
// if never replaced, else the latest "-rN" leg).
func (a *Adapter) CancelOrder(ctx context.Context, domainOID string) error {
	a.mu.Lock()
	tzID, ok := a.tzIDByDomain[domainOID]
	a.mu.Unlock()
	if !ok {
		tzID = domainOID
	}
	return a.rest.cancelOrder(ctx, tzID)
}

func (a *Adapter) CancelAll(ctx context.Context, symbol string) error {
	return a.rest.cancelAll(ctx, symbol)
}

// Flatten is unsupported: TZ has no close-all primitive. Capabilities()
// reports FlattenAll:false so exec.Core never calls this in practice; this
// exists as defense in depth against a future caller that doesn't check.
func (a *Adapter) Flatten(context.Context) error {
	return errUnsupported
}

// Snapshot fetches the REST-authoritative account/positions/orders view,
// stamping venue and stripping any "-rN" replace suffix from order ids so a
// replaced order still reports under its one stable domain id.
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
		orders[i].ID = a.domainID(orders[i].ID)
	}
	return acct, positions, orders, nil
}

// Run starts the Portfolio-WS client, which drives connect/reconnect and
// invokes handleConn/handleOrder/handlePosition. It blocks until ctx is done.
func (a *Adapter) Run(ctx context.Context) {
	a.runCtx = ctx
	a.ws.run(ctx)
}

// handleConn is the wsClient onConn callback. On connect it runs reconcile()
// BEFORE the wsClient's read loop starts reading this connection's frames
// (wsClient.session calls onConn(true) synchronously, then read loop) — any
// frame the server pushes while reconcile's REST snapshot call is in flight
// simply queues on the OS socket buffer instead of being read, which is the
// "buffer -> snapshot -> replay" sequence with no separate application-level
// buffer needed: the frames are "replayed" by the read loop's normal
// processing immediately after reconcile returns.
func (a *Adapter) handleConn(up bool) {
	if up {
		a.emit(exec.BrokerConnUp{V: a.venue})
		a.reconcile()
	} else {
		a.emit(exec.BrokerConnDown{V: a.venue})
	}
}

// handleOrder is the wsClient onOrder callback: normalize and emit.
func (a *Adapter) handleOrder(o tzOrder) {
	for _, e := range a.normalizeOrder(a.venue, o) {
		a.emit(e)
	}
}

// handlePosition is the wsClient onPosition callback. Positions are
// broker-reconciled, not event-sourced, so a live push updates the cached
// per-symbol map and re-emits the full snapshot (BrokerPositions carries a
// full slice, not a delta).
func (a *Adapter) handlePosition(p tzPosition) {
	qty := p.Shares
	if p.Side == "Short" {
		qty = -qty
	}
	a.mu.Lock()
	a.positions[p.Symbol] = exec.Position{Venue: a.venue, Symbol: p.Symbol, Qty: qty, AvgPrice: p.PriceAvg}
	snapshot := make([]exec.Position, 0, len(a.positions))
	for _, pos := range a.positions {
		snapshot = append(snapshot, pos)
	}
	a.mu.Unlock()
	a.emit(exec.BrokerPositions{V: a.venue, Positions: snapshot})
}

// reconcile is the startup/reconnect sequence: REST snapshot -> seed/diff
// state -> emit BrokerAccount + BrokerPositions (+ any order transitions
// missed while disconnected, + StreamGap on a reconnect specifically, never
// on the very first connect). Runs synchronously from handleConn(true).
func (a *Adapter) reconcile() {
	ctx := a.runCtx
	if ctx == nil {
		ctx = context.Background()
	}

	// Best-effort: populates rc.routes for pickRoute. A mock/test server that
	// doesn't implement /routes at all just leaves pickRoute falling back to
	// the configured default, which is fine — this is not on the critical
	// path for order entry.
	if _, err := a.rest.fetchRoutes(ctx); err != nil {
		slog.Debug("tradezero: reconcile: fetch routes failed (non-fatal)", "err", err)
	}

	acct, positions, orders, err := a.rest.snapshot(ctx)
	if err != nil {
		slog.Warn("tradezero: reconcile: snapshot failed", "err", err)
		return
	}
	acct.Venue = a.venue
	for i := range positions {
		positions[i].Venue = a.venue
	}

	a.mu.Lock()
	reconnect := a.connectedOnce
	a.connectedOnce = true

	posMap := make(map[string]exec.Position, len(positions))
	for _, p := range positions {
		posMap[p.Symbol] = p
	}
	a.positions = posMap

	var gapEvents []exec.BrokerEvent
	for _, o := range orders {
		domainOID := a.domainID(o.ID)
		prevStatus, seen := a.lastKnownStatus[domainOID]
		a.lastKnownStatus[domainOID] = o.Status
		if reconnect && seen && prevStatus != o.Status {
			gapEvents = append(gapEvents, a.synthesizeTransitionLocked(domainOID, o)...)
		}
		if o.ExecutedQty > 0 && o.ExecutedQty > a.seenExecuted[o.ID] {
			a.seenExecuted[o.ID] = o.ExecutedQty
		}
	}
	a.mu.Unlock()

	a.emit(exec.BrokerAccount{Account: acct})
	a.emit(exec.BrokerPositions{V: a.venue, Positions: positions})
	for _, e := range gapEvents {
		a.emit(e)
	}
	if reconnect {
		a.emit(exec.StreamGap{V: a.venue, Ts: a.now()})
	}
}

// synthesizeTransitionLocked builds the domain event(s) implied by an order
// whose broker-reported state moved while the WS connection was down (caller
// holds a.mu). Fill derivation reuses the same seenExecuted dedup map as
// normalizeOrder, keyed by the same raw TZ id, so a fill already observed
// live is never double-counted here.
func (a *Adapter) synthesizeTransitionLocked(domainOID string, o exec.Order) []exec.BrokerEvent {
	var out []exec.BrokerEvent
	ts := a.now()

	if o.ExecutedQty > 0 {
		prevExec := a.seenExecuted[o.ID]
		if o.ExecutedQty > prevExec {
			out = append(out, exec.OrderFilled{
				F: exec.Fill{
					Venue: a.venue, OrderID: domainOID, Symbol: o.Symbol, Side: o.Side,
					Qty: o.ExecutedQty - prevExec, Price: o.AvgFillPrice, TsMs: ts,
				},
				CumQty: o.ExecutedQty, LeavesQty: o.LeavesQty, AvgPrice: o.AvgFillPrice,
			})
		}
	}
	switch o.Status {
	case exec.StatusCanceled:
		out = append(out, a.onCanceledLocked(a.venue, domainOID, o.ID, ts)...)
	case exec.StatusRejected:
		out = append(out, exec.OrderRejected{V: a.venue, OID: domainOID, Reason: rejectText(o.RejectReason), Ts: ts})
	case exec.StatusExpired:
		out = append(out, exec.OrderExpired{V: a.venue, OID: domainOID, Ts: ts})
	}
	return out
}
