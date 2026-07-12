// Package moomoo -- this file (moomoo.go) assembles the pure-translation
// (mapping.go), transport (trd.go), and push-decode (normalize.go) layers into
// the exec.Broker interface, mirroring how alpaca/alpaca.go assembles the
// Alpaca adapter. Like Alpaca (and unlike TradeZero), moomoo has a NATIVE
// replace (trdClient.modifyOrder -> ModifyOrderOp_Normal) and no
// cancel-then-resubmit emulation, so a domain order's ClientOrderID is set once
// at submit time and never changes across a replace.
//
// The one structural way this adapter differs from Alpaca: opend.Client hands
// out TWO channels (State() and Pushes()) rather than invoking callbacks from
// its own read loop, so this Adapter owns the single goroutine that consumes
// both serially (Run's select loop). And reconcile is SIMPLER than Alpaca's:
// moomoo's getOrderList returns an unfiltered "today" blotter (open AND
// terminal orders, each with its real current status), so there is no
// Alpaca-style "this id vanished, go ask what happened to it" resolution
// branch -- every order to catch up on is already present with its authoritative
// status in one snapshot.
package moomoo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorder"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorderfill"
)

// errFlattenUnsupported / errResetBalanceUnsupported back Flatten/ResetBalance.
// Capabilities().FlattenAll and .ResetBalance are both false, so exec.Core
// never calls these in practice -- defense in depth, mirroring
// alpaca.Adapter.ResetBalance's pattern.
var (
	errFlattenUnsupported      = errors.New("moomoo: flatten unsupported (no native close-all)")
	errResetBalanceUnsupported = errors.New("moomoo: reset balance unsupported")
)

// Config configures a moomoo Adapter. Addr is the SAME OpenD host:port the feed
// connection dials -- the trade connection is a SECOND, independent
// opend.Client to the same gateway. Env is "paper" or "live" (anything other
// than "live" is treated as simulate by trdHeader/getAccList). Clock falls back
// to clock.System{}.
type Config struct {
	Venue     exec.VenueID
	AccountID uint64
	Env       string
	Addr      string
	Clock     clock.Clock
}

// Adapter is moomoo's exec.Broker implementation. It owns a trade-only
// opend.Client, the trdClient transport over it, and the pushDecoder that turns
// 2208/2218 pushes into domain events. It holds only the bookkeeping needed to
// bridge moomoo's numeric OrderID to eTape's domain ClientOrderID.
type Adapter struct {
	venue exec.VenueID
	clk   clock.Clock

	client *opend.Client
	tc     *trdClient
	push   *pushDecoder

	events chan exec.BrokerEvent

	mu sync.Mutex
	// orderIDByDomain maps a domain ClientOrderID (stable across a native
	// replace) to moomoo's numeric OrderID, which ReplaceOrder/CancelOrder must
	// target. A domain id missing here (a fresh process, or an order this
	// instance never saw the submission of) is resolved lazily via
	// resolveOrderID's orderByRemark fallback. Mirrors
	// alpaca.Adapter.brokerIDByClientID.
	orderIDByDomain map[string]uint64
	// connectedOnce distinguishes the very first connect (nothing could have
	// been missed yet: no StreamGap) from a reconnect. Mirrors alpaca's gate.
	connectedOnce bool
}

var _ exec.Broker = (*Adapter)(nil)

// New builds a moomoo Adapter. It builds the second opend.Client (ClientID
// "etape-trade") but does NOT start it -- Run(ctx) does, mirroring how
// alpaca.New builds a.ws without starting it.
func New(cfg Config) (*Adapter, error) {
	if cfg.Venue == "" {
		return nil, fmt.Errorf("moomoo: config missing venue")
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.System{}
	}
	client := opend.New(opend.Options{Addr: cfg.Addr, ClientID: "etape-trade", Clock: clk})
	return &Adapter{
		venue:           cfg.Venue,
		clk:             clk,
		client:          client,
		tc:              newTrdClient(client, cfg.AccountID, cfg.Env, clk),
		push:            newPushDecoder(),
		events:          make(chan exec.BrokerEvent, 256), // mirrors alpaca/tradezero buffer convention
		orderIDByDomain: map[string]uint64{},
	}, nil
}

// VerifyAccount is venueprobe's read-only "test connection" helper for
// moomoo: a bare, synchronous, one-shot dial to OpenD's trade protocols,
// deliberately NOT built on Adapter/New/Run -- no long-lived connection, no
// push-consume loop, no Run goroutine kept alive after this returns. Mirrors
// alpaca.VerifyCredentials' exact role for its own broker (see
// alpaca/alpaca.go). Dials addr, waits for the InitConnect handshake via a
// throwaway *opend.Client, then calls Trd_GetAccList and returns the
// validated account (or the specific validation/dial/timeout error) -- never
// leaving a lingering goroutine or open TCP connection after it returns,
// success or failure.
func VerifyAccount(ctx context.Context, addr string, accountID uint64, env string, clk clock.Clock) (*trdcommon.TrdAcc, error) {
	pctx, cancel := context.WithCancel(ctx)
	defer cancel() // always tears down the throwaway connection/goroutine before returning

	if clk == nil {
		clk = clock.System{}
	}
	client := opend.New(opend.Options{Addr: addr, ClientID: "etape-trade-probe", Clock: clk})
	go client.Run(pctx)

	select {
	case st, ok := <-client.State():
		if !ok || st != opend.ConnUp {
			return nil, fmt.Errorf("moomoo: probe: connection failed")
		}
	case <-pctx.Done():
		return nil, pctx.Err()
	}

	tc := newTrdClient(client, accountID, env, clk)
	return tc.getAccList(ctx)
}

// ListAccounts is venueseed's discovery helper: a bare, synchronous, one-shot
// dial to OpenD's trade protocols returning the FULL account list, rather
// than validating one specific accID the way VerifyAccount/getAccList do.
// Same throwaway-connection contract as VerifyAccount -- dial, wait for the
// InitConnect handshake via a throwaway *opend.Client (clientID is the
// caller's choice, so a concurrent probe/seed/verify each get their own
// OpenD-visible client identity), one Trd_GetAccList round trip, then tear
// the connection down before returning, success or failure. Trd_GetAccList is
// read-only; like VerifyAccount, this NEVER sends Trd_UnlockTrade (2005) --
// eTape's standing rule that the trade password never touches eTape holds for
// discovery exactly as it does for validation.
func ListAccounts(ctx context.Context, addr, clientID string, clk clock.Clock) ([]*trdcommon.TrdAcc, error) {
	pctx, cancel := context.WithCancel(ctx)
	defer cancel() // always tears down the throwaway connection/goroutine before returning

	if clk == nil {
		clk = clock.System{}
	}
	client := opend.New(opend.Options{Addr: addr, ClientID: clientID, Clock: clk})
	go client.Run(pctx)

	select {
	case st, ok := <-client.State():
		if !ok || st != opend.ConnUp {
			return nil, fmt.Errorf("moomoo: list accounts: connection failed")
		}
	case <-pctx.Done():
		return nil, pctx.Err()
	}

	return fetchAccList(ctx, client)
}

// EligibleLiveUS reports whether acc can back eTape's live-only moomoo
// venue: real-money (TrdEnv_Real), not the Master account, not Disabled, and
// authorized to trade market US. It shares its Master/Disabled/US-authorized
// predicates with trdClient.getAccList's validation (trd.go) so venueseed's
// discovery filter and the adapter's boot-time validation can never drift
// apart -- only the TrdEnv check is specific to this function (live only;
// getAccList accepts either env, matching however the venue is configured).
func EligibleLiveUS(acc *trdcommon.TrdAcc) bool {
	if acc == nil {
		return false
	}
	if isMasterAcc(acc) || isDisabledAcc(acc) || !isUSAuthorized(acc) {
		return false
	}
	return trdcommon.TrdEnv(acc.GetTrdEnv()) == trdcommon.TrdEnv_TrdEnv_Real
}

// isMasterAcc, isDisabledAcc, and isUSAuthorized are the three env-independent
// eligibility predicates shared between EligibleLiveUS (discovery) and
// trdClient.getAccList (validation, trd.go) -- factored here so the two
// checks are always evaluated identically and cannot silently diverge.
func isMasterAcc(acc *trdcommon.TrdAcc) bool {
	return trdcommon.TrdAccRole(acc.GetAccRole()) == trdcommon.TrdAccRole_TrdAccRole_Master
}

func isDisabledAcc(acc *trdcommon.TrdAcc) bool {
	return trdcommon.TrdAccStatus(acc.GetAccStatus()) == trdcommon.TrdAccStatus_TrdAccStatus_Disabled
}

func isUSAuthorized(acc *trdcommon.TrdAcc) bool {
	for _, m := range acc.GetTrdMarketAuthList() {
		if trdcommon.TrdMarket(m) == trdcommon.TrdMarket_TrdMarket_US {
			return true
		}
	}
	return false
}

// now returns the current time in epoch milliseconds via the injected clock.
func (a *Adapter) now() int64 { return a.clk.Now().UnixMilli() }

// emit pushes a domain-visible event onto Events(). The channel is generously
// buffered for a single slow-ish consumer (exec.Core's writer loop); a blocking
// send is preferred over a lossy one, matching alpaca/tradezero/sim.
func (a *Adapter) emit(e exec.BrokerEvent) { a.events <- e }

func (a *Adapter) Events() <-chan exec.BrokerEvent { return a.events }

// Capabilities reports moomoo's native replace and overnight (OVERNIGHT
// session) support, but no native flatten-all (FlattenAll false: moomoo has no
// close-all-positions primitive, unlike Alpaca's DELETE /v2/positions).
func (a *Adapter) Capabilities() exec.Capabilities {
	return exec.Capabilities{NativeReplace: true, FlattenAll: false, OvernightSession: true}
}

// ProbeRTT times a lightweight, read-only, side-effect-free round trip
// (getAccList) for eTape's health poller -- the same reachability role
// alpaca.Adapter.ProbeRTT plays. Wall-clock time.Now() is used (not the
// injected clock) to match Alpaca's convention: a fake clock would make RTT
// meaningless. A getAccList validation failure surfaces here as a probe error,
// which is correct -- an account that no longer validates is not reachable in
// any useful sense.
func (a *Adapter) ProbeRTT(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	_, err := a.tc.getAccList(ctx)
	return time.Since(start), err
}

// SubmitOrder submits a new order. On ANY placeOrder error it probes
// orderByRemark(refreshCache=true) before deciding, mirroring Alpaca's
// SubmitOrder philosophy exactly: placeOrder's error covers both a definitive
// semantic reject (the order was never created) and a genuine transport
// ambiguity (timeout/drop mid-round-trip -- the order may or may not have
// landed). The probe resolves which: a truly-rejected order correctly resolves
// to "not found" (emit OrderRejected, no error return -- the same convention
// TradeZero/Alpaca use for a semantic reject), while a landed-despite-the-error
// order is treated as accepted.
func (a *Adapter) SubmitOrder(ctx context.Context, req exec.OrderRequest) (exec.OrderAck, error) {
	if err := req.Validate(); err != nil {
		return exec.OrderAck{}, err
	}
	orderID, err := a.tc.placeOrder(ctx, req, req.ClientOrderID)
	if err != nil {
		ord, found, probeErr := a.tc.orderByRemark(ctx, req.ClientOrderID, true)
		switch {
		case probeErr != nil:
			// Genuinely can't tell whether the order landed; surface the
			// transport error rather than guessing either way.
			return exec.OrderAck{}, fmt.Errorf("moomoo: submit %w (ambiguity probe also failed: %v)", err, probeErr)
		case found:
			orderID = ord.GetOrderID() // landed despite the error -- treat as accepted
		default:
			// Confirmed not created: no order push will ever arrive for a
			// ClientOrderID that was never placed, so this is the ONLY place
			// that can report the reject.
			a.emit(exec.OrderRejected{V: a.venue, OID: req.ClientOrderID, Reason: err.Error(), Ts: a.now()})
			return exec.OrderAck{OrderID: req.ClientOrderID, Accepted: false, Message: err.Error()}, nil
		}
	}

	a.mu.Lock()
	a.orderIDByDomain[req.ClientOrderID] = orderID
	a.mu.Unlock()
	// Seed the pushDecoder's correlation state now, before any push has
	// necessarily arrived: closes the "fill push before first order push" race
	// normalize.go discloses. pushDecoder has its own mutex -- deliberately a
	// separate lock from a.mu.
	a.push.learnOrder(orderID, req.ClientOrderID, req.Qty)

	return exec.OrderAck{OrderID: req.ClientOrderID, Accepted: true}, nil
}

// resolveOrderID maps a domain order id to moomoo's numeric OrderID, first from
// the in-memory map (the common case), falling back to orderByRemark for an
// order this instance has no record of yet (a fresh process, or reconnect
// before the map was repopulated). Mirrors alpaca.Adapter.resolveBrokerID.
func (a *Adapter) resolveOrderID(ctx context.Context, domainOID string) (uint64, error) {
	a.mu.Lock()
	id, ok := a.orderIDByDomain[domainOID]
	a.mu.Unlock()
	if ok {
		return id, nil
	}
	ord, found, err := a.tc.orderByRemark(ctx, domainOID, true)
	if err != nil {
		return 0, fmt.Errorf("moomoo: resolve order id for %s: %w", domainOID, err)
	}
	if !found {
		return 0, fmt.Errorf("moomoo: unknown order %s", domainOID)
	}
	a.mu.Lock()
	a.orderIDByDomain[domainOID] = ord.GetOrderID()
	a.mu.Unlock()
	return ord.GetOrderID(), nil
}

// ReplaceOrder amends qty/limit/stop via moomoo's native modify. It emits
// exec.OrderReplaced synchronously on success ONLY: moomoo has no distinct
// "replaced" push status to key on (unlike Alpaca's WS "replaced" event), so
// the request/response IS the authoritative confirmation. On failure it just
// returns the error, no emit (mirroring Alpaca's plain error passthrough).
//
// A real correctness subtlety: state.go's OrderReplaced fold sets
// o.Qty = e.NewQty UNCONDITIONALLY (unlike NewLimit/NewStop, applied only if
// > 0). moomoo's modify response carries no resulting quantity, so a
// price-only replace (req.Qty == 0, "don't change qty") must NOT emit
// NewQty=0 -- that would corrupt the tracked quantity to zero. When qty is
// unspecified this queries the CURRENT quantity and emits that instead.
// NewLimit/NewStop need no such treatment: the fold applies them conditionally,
// so passing req.LimitPrice/req.StopPrice straight through (0 == "unspecified,
// leave alone") is already correct.
func (a *Adapter) ReplaceOrder(ctx context.Context, domainOID string, req exec.ReplaceRequest) error {
	orderID, err := a.resolveOrderID(ctx, domainOID)
	if err != nil {
		return fmt.Errorf("moomoo: replace: %w", err)
	}
	newQty := req.Qty
	if newQty <= 0 {
		ord, found, err := a.tc.orderByRemark(ctx, domainOID, true)
		if err != nil {
			return fmt.Errorf("moomoo: replace: resolve current qty: %w", err)
		}
		if !found {
			return fmt.Errorf("moomoo: replace: order %s not found while resolving current qty", domainOID)
		}
		newQty = ord.GetQty()
	}
	if err := a.tc.modifyOrder(ctx, orderID, req); err != nil {
		return err
	}
	a.emit(exec.OrderReplaced{
		V:        a.venue,
		OID:      domainOID,
		NewQty:   newQty,
		NewLimit: req.LimitPrice,
		NewStop:  req.StopPrice,
		Ts:       a.now(),
	})
	return nil
}

// CancelOrder cancels the working order backing domainOID.
func (a *Adapter) CancelOrder(ctx context.Context, domainOID string) error {
	orderID, err := a.resolveOrderID(ctx, domainOID)
	if err != nil {
		return fmt.Errorf("moomoo: cancel: %w", err)
	}
	return a.tc.cancelOrder(ctx, orderID)
}

// CancelAll cancels every open order, optionally scoped to symbol. trdClient
// (Task 3) already owns all the branching logic (live forAll vs. paper/symbol
// iterate-and-join).
func (a *Adapter) CancelAll(ctx context.Context, symbol string) error {
	return a.tc.cancelAll(ctx, symbol)
}

// Flatten is unsupported: moomoo has no native close-all primitive.
// Capabilities().FlattenAll is false so exec.Core never calls this in practice.
func (a *Adapter) Flatten(context.Context) error { return errFlattenUnsupported }

// ResetBalance is unsupported: a real moomoo account can't be reset.
// Capabilities().ResetBalance is false so exec.Core never calls this in
// practice.
func (a *Adapter) ResetBalance(context.Context, float64) error { return errResetBalanceUnsupported }

// Snapshot fetches the trade-authoritative account/positions/orders view and
// stamps venue on every returned struct (a gap Task 3's snapshot deliberately
// left for this layer to close).
func (a *Adapter) Snapshot(ctx context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	acct, positions, orders, err := a.tc.snapshot(ctx)
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

// Run starts the trade opend.Client in its own goroutine and then owns the
// single loop that consumes BOTH its State() (connection transitions) and
// Pushes() (trade push frames) channels serially. It blocks until ctx is done.
//
// Concurrency model: this loop is the ONLY goroutine that touches connectedOnce
// (via reconcile) and drives onConnUp -- ctx is passed explicitly down that
// call chain (no shared runCtx field is needed; unlike Alpaca, whose reconcile
// runs from a ctx-less ws callback, this Adapter's onConnUp/reconcile are called
// directly from here with ctx in scope). State that COMMAND methods
// (SubmitOrder/ReplaceOrder/CancelOrder) touch concurrently from OTHER
// goroutines (Core's writer loop) is safe because it is independently
// mutex-guarded: a.mu for orderIDByDomain, and pushDecoder's own mu for its
// correlation/accumulation maps -- not because of anything about this loop.
func (a *Adapter) Run(ctx context.Context) {
	go func() { _ = a.client.Run(ctx) }()
	for {
		select {
		case <-ctx.Done():
			return
		case st, ok := <-a.client.State():
			if !ok {
				return
			}
			switch st {
			case opend.ConnUp:
				a.emit(exec.BrokerConnUp{V: a.venue})
				a.onConnUp(ctx)
			case opend.ConnDown:
				a.emit(exec.BrokerConnDown{V: a.venue})
			}
		case f, ok := <-a.client.Pushes():
			if !ok {
				return
			}
			a.handlePush(f)
		}
	}
}

// handlePush decodes one trade push frame into domain events and emits them.
// The default case surfaces ANY unrecognized push loudly rather than silently
// dropping it -- most notably the "trade unlock needed" notification, whose
// exact protocol ID the plan's research did not confirm. Per the standing rule
// eTape NEVER auto-unlocks; a human acts in the OpenD GUI. This catch-all
// surfaces whatever the real notify ID turns out to be without this code having
// to guess it.
func (a *Adapter) handlePush(f opend.Frame) {
	switch f.ProtoID {
	case opend.ProtoTrdUpdateOrder:
		var resp trdupdateorder.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			slog.Warn("moomoo: decode order push", "err", err)
			return
		}
		for _, e := range a.push.decodeOrderPush(a.venue, &resp) {
			a.emit(e)
		}
	case opend.ProtoTrdUpdateOrderFill:
		var resp trdupdateorderfill.Response
		if err := proto.Unmarshal(f.Body, &resp); err != nil {
			slog.Warn("moomoo: decode fill push", "err", err)
			return
		}
		for _, e := range a.push.decodeFillPush(a.venue, &resp) {
			a.emit(e)
		}
	default:
		slog.Warn("moomoo: unrecognized trade push", "protoID", f.ProtoID, "bodyLen", len(f.Body))
	}
}

// onConnUp runs the startup/reconnect sequence: validate the account, subscribe
// this account to order/fill pushes, then reconcile a fresh snapshot. Called
// synchronously from Run's select loop on every ConnUp (first connect AND every
// reconnect).
//
// KNOWN RACE (bounded, not eliminated -- see normalize.go's reconcileOrder doc
// comment for the full writeup): subAccPush below runs BEFORE reconcile's
// getOrderList snapshot, so a fill landing in that window can be counted both
// by reconcile's catch-up path AND by the same fill's already-queued live push
// once handlePush processes it afterward. decodeFillPush clamps its cumulative
// quantity to the order's known total to bound the damage. A full fix would
// require either reordering this sequence (subscribe-after-snapshot instead,
// which trades this risk for a "missed fill until the next reconnect" risk)
// or a different reconciliation strategy -- both deferred, out of scope here.
func (a *Adapter) onConnUp(ctx context.Context) {
	if _, err := a.tc.getAccList(ctx); err != nil {
		// A validation failure (wrong account role/status/market/env) is a real
		// misconfiguration, not a transient error -- continuing with a
		// possibly-wrong account would be actively dangerous. Log loudly and
		// stop; a future TCP reconnect re-runs this check. There is no
		// retry-with-backoff here (a disclosed limitation of this task's scope).
		slog.Warn("moomoo: getAccList validation failed on connect", "err", err)
		return
	}
	if err := a.tc.subAccPush(ctx, []uint64{a.tc.accID}); err != nil {
		// Unlike getAccList, log loudly but DO proceed to reconcile: a
		// stale-but-present initial account/position/order view (from the
		// snapshot) is strictly better than nothing, even without live pushes
		// subscribed.
		slog.Warn("moomoo: subAccPush failed", "err", err)
	}
	a.reconcile(ctx)
}

// reconcile is the snapshot-driven catch-up. Because moomoo's getOrderList
// returns an unfiltered "today" blotter (every order with its authoritative
// status), reconcileOrder synthesizes any missed lifecycle/fill events directly
// from each raw order -- no Alpaca-style "resolve a vanished order" branch.
//
// It obtains the (translated account/positions) from its own Snapshot method
// (which already stamps Venue) and, separately, the RAW []*trdcommon.Order from
// trdClient.getOrderList so each order's numeric OrderID is available to
// reconcileOrder (Snapshot's translated exec.Order carries only the domain
// Remark, not moomoo's numeric id). This costs one extra getOrderList round
// trip at (re)connect time -- an accepted, rare-path cost, chosen over
// reopening Task 3's already-approved trdClient.snapshot signature.
func (a *Adapter) reconcile(ctx context.Context) {
	acct, positions, _, err := a.Snapshot(ctx)
	if err != nil {
		slog.Warn("moomoo: reconcile: snapshot failed", "err", err)
		return
	}
	rawOrders, err := a.tc.getOrderList(ctx, true)
	if err != nil {
		slog.Warn("moomoo: reconcile: order list failed", "err", err)
		return
	}

	a.mu.Lock()
	reconnect := a.connectedOnce
	a.connectedOnce = true
	// Repopulate the domain->numeric map from the fresh blotter so a
	// Replace/Cancel arriving after a reconnect resolves from memory rather than
	// a per-call orderByRemark scan. Only eTape-placed orders (non-empty Remark)
	// are mapped; the numeric OrderID is moomoo's own.
	for _, o := range rawOrders {
		if remark := o.GetRemark(); remark != "" {
			a.orderIDByDomain[remark] = o.GetOrderID()
		}
	}
	a.mu.Unlock()

	a.emit(exec.BrokerAccount{Account: acct})
	a.emit(exec.BrokerPositions{V: a.venue, Positions: positions})

	for _, o := range rawOrders {
		for _, e := range a.push.reconcileOrder(a.venue, o) {
			a.emit(e)
		}
	}

	if reconnect {
		a.emit(exec.StreamGap{V: a.venue, Ts: a.now()})
	}
}
