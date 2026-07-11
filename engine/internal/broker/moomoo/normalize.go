// Package moomoo -- this file (normalize.go) decodes moomoo's two trade PUSH
// protocols (Trd_UpdateOrder=2208, Trd_UpdateOrderFill=2218) into the
// broker-agnostic []exec.BrokerEvent shape. It holds no I/O, no
// exec.Broker/Adapter wiring, and no network calls -- a later task
// (moomoo.go) owns a *pushDecoder and calls its two decode methods from its
// push-consume loop. See pushDecoder's doc comment for the exact seam a
// future caller needs.
package moomoo

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/common"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorder"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorderfill"
)

// pushDecoder owns exactly the dedup/correlation/accumulation state needed to
// turn moomoo's two trade push protocols into domain events, and nothing
// else. It has no dependency on trdClient or any future exec.Broker
// Adapter -- moomoo.go (a later task) is expected to hold one instance as a
// field (e.g. `push *pushDecoder`) and call decodeOrderPush/decodeFillPush
// from its own push-consume loop, feeding it already-Unmarshaled Response
// values it read off the wire via opend.Client.
//
// Why this state exists at all: Trd_UpdateOrderFill's wire struct carries
// only ONE execution's own qty/price -- no cumulative fill quantity, no
// leaves quantity, no average price -- so those three aggregate values that
// exec.OrderFilled requires must be derived here, accumulated across
// however many fill pushes land for a given order. Trd_UpdateOrderFill also
// carries no domain order id (Remark) at all, only moomoo's own numeric
// OrderID -- so pushDecoder also has to learn, from earlier Trd_UpdateOrder
// pushes (which carry both), the mapping from that numeric OrderID to the
// domain order id and total order quantity.
type pushDecoder struct {
	mu sync.Mutex

	// lastKnownStatus is the last CONFIRMED (statusDomain ok=true) domain
	// status seen for a domain order id (== Remark). Used to suppress
	// re-emitting the same lifecycle event on every subsequent order push
	// that doesn't actually change status (e.g. repeated pushes while a
	// partial fill accumulates without an OrderStatus change).
	lastKnownStatus map[string]exec.OrderStatus

	// domainOIDByOrderID and totalQtyByOrderID are learned from ORDER pushes
	// (2208) keyed by moomoo's numeric OrderID, and consulted by FILL pushes
	// (2218), which carry that same numeric OrderID but no Remark.
	domainOIDByOrderID map[uint64]string
	totalQtyByOrderID  map[uint64]float64

	seenFillIDs          map[uint64]bool
	cumQtyByOrderID      map[uint64]float64 // running SUM of fill Qty seen so far, per numeric OrderID
	cumNotionalByOrderID map[uint64]float64 // running SUM of (fill Qty * fill Price), for VWAP
}

// newPushDecoder returns a pushDecoder ready to decode pushes for a single
// trade connection's lifetime. State is not persisted across restarts --
// process restart mid-flight is the known "fill arrives before any order
// push seen" race documented on decodeFillPush.
func newPushDecoder() *pushDecoder {
	return &pushDecoder{
		lastKnownStatus:      make(map[string]exec.OrderStatus),
		domainOIDByOrderID:   make(map[uint64]string),
		totalQtyByOrderID:    make(map[uint64]float64),
		seenFillIDs:          make(map[uint64]bool),
		cumQtyByOrderID:      make(map[uint64]float64),
		cumNotionalByOrderID: make(map[uint64]float64),
	}
}

// learnOrder records the correlation between moomoo's numeric orderID and
// eTape's domain order id/qty BEFORE any push has necessarily arrived --
// closes the race decodeFillPush discloses (a fill push arriving before the
// first order push is seen would otherwise be dropped, even though the Adapter
// already knows this correlation from its own SubmitOrder call). It seeds ONLY
// the correlation maps (domainOID/totalQty), never lastKnownStatus: the domain
// OrderAccepted still rides in on the 2208 order push (decodeOrderPush) or a
// reconcile, exactly as it does today -- learnOrder is a fill-correlation
// safety net, not a status source.
func (p *pushDecoder) learnOrder(orderID uint64, domainOID string, totalQty float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.domainOIDByOrderID[orderID] = domainOID
	p.totalQtyByOrderID[orderID] = totalQty
}

// reconcileOrder is decodeOrderPush's snapshot-fed sibling: given one raw
// moomoo order from a trdClient.getOrderList snapshot (NOT a live push), it
// synthesizes whatever lifecycle/fill catch-up events are implied by comparing
// it against the last known tracked state -- used after a (re)connect to catch
// up on anything missed while disconnected.
//
// KNOWN RACE (bounded, not eliminated): moomoo.go's onConnUp calls
// subAccPush (live push subscription goes active) BEFORE calling reconcile
// (which is what eventually calls this method with a fresh getOrderList
// snapshot). OpenD's Trd_SubAccPush + Trd_GetOrderList have no transactional/
// atomic guarantee between them, so a fill that lands in that window can be
// observed TWICE: once here (this method sets cumQtyByOrderID directly from
// the snapshot's authoritative ExecutedQty) and again when the SAME fill's
// already-queued live 2218 push is processed by decodeFillPush after
// onConnUp returns (which adds additively on top of what this method just
// set). decodeFillPush now clamps cumQtyByOrderID to the order's known total
// qty, which bounds the damage to "at worst LeavesQty reads 0 slightly
// early" instead of an unbounded overcount corrupting state.go's applyFill
// (CumQty is taken verbatim from whatever the adapter reports). This is a
// mitigation, not a fix: a real fix would need either reordering onConnUp to
// snapshot-before-subscribe (trading this risk for a "missed fill until the
// next reconnect" risk instead) or a different reconciliation strategy
// entirely. Both are deferred -- out of scope here.
//
// It takes the RAW *trdcommon.Order (not an already-narrowed exec.Order),
// mirroring decodeOrderPush's own input shape (which decodes a
// trdupdateorder.Response wrapping the same trdcommon.Order): this keeps
// reconcileOrder a genuine sibling of decodeOrderPush -- both derive their
// domain view here, in the one place that owns the tracking maps. Taking the
// raw order is also what lets the reconcile-sourced terminal/fill events carry
// a real timestamp (raw.GetUpdateTimestamp, exactly as decodeOrderPush does)
// and a real reject reason (raw.GetLastErrMsg) instead of the zero values
// orderDomain deliberately leaves unset (see orderDomain's doc in trd.go).
func (p *pushDecoder) reconcileOrder(venue exec.VenueID, raw *trdcommon.Order) []exec.BrokerEvent {
	o := orderDomain(raw)
	oid := o.ID // == Remark, per orderDomain
	if oid == "" {
		return nil // not an eTape-placed order
	}
	moomooOrderID := raw.GetOrderID()
	ts := tsMs(raw.GetUpdateTimestamp())

	p.mu.Lock()
	defer p.mu.Unlock()

	p.domainOIDByOrderID[moomooOrderID] = oid
	p.totalQtyByOrderID[moomooOrderID] = o.Qty

	var out []exec.BrokerEvent

	// Missed fill catch-up: o.ExecutedQty is moomoo's authoritative cumulative
	// filled quantity. cumQtyByOrderID tracks the same thing from live 2218
	// pushes. A strictly greater o.ExecutedQty than what's tracked means at
	// least one fill happened while disconnected that this decoder never saw
	// live -- synthesize ONE catch-up OrderFilled for the whole missed delta
	// (a snapshot gives no per-fill granularity, only the aggregate;
	// o.AvgFillPrice is moomoo's own reported average across ALL fills to date,
	// which is what this one catch-up event's Price/AvgPrice reflect -- there
	// is no better per-fill price available from a snapshot). Setting
	// cumQtyByOrderID to o.ExecutedQty afterward (not adding a delta) keeps
	// future LIVE fills' accumulation correct going forward without
	// double-counting the catch-up amount.
	prevCum := p.cumQtyByOrderID[moomooOrderID]
	if o.ExecutedQty > prevCum {
		delta := o.ExecutedQty - prevCum
		p.cumQtyByOrderID[moomooOrderID] = o.ExecutedQty
		p.cumNotionalByOrderID[moomooOrderID] = o.ExecutedQty * o.AvgFillPrice // reset notional to stay consistent with the now-authoritative cum/avg pair
		out = append(out, exec.OrderFilled{
			F:         exec.Fill{Venue: venue, OrderID: oid, Symbol: o.Symbol, Side: o.Side, Qty: delta, Price: o.AvgFillPrice, TsMs: ts},
			CumQty:    o.ExecutedQty,
			LeavesQty: o.LeavesQty,
			AvgPrice:  o.AvgFillPrice,
		})
	}

	prevStatus, tracked := p.lastKnownStatus[oid]
	p.lastKnownStatus[oid] = o.Status
	if tracked && prevStatus == o.Status {
		return out // no NEW status transition; the fill catch-up above (if any) still applies
	}
	switch o.Status {
	case exec.StatusAccepted:
		out = append(out, exec.OrderAccepted{V: venue, OID: oid, BrokerOrderID: fmt.Sprint(moomooOrderID), Ts: ts})
	case exec.StatusCanceled:
		out = append(out, exec.OrderCanceled{V: venue, OID: oid, Ts: ts})
	case exec.StatusRejected:
		reason := raw.GetLastErrMsg()
		if reason == "" {
			reason = "rejected"
		}
		out = append(out, exec.OrderRejected{V: venue, OID: oid, Reason: reason, Ts: ts})
	}
	return out
}

// decodeOrderPush turns one Trd_UpdateOrder (2208) push into zero or one
// domain lifecycle events. It ALWAYS records the numeric-OrderID ->
// (domainOID, totalQty) correlation first (even when no event results),
// since a later fill push depends on it having been learned. This protocol
// is responsible ONLY for Accepted/Canceled/Rejected -- Filled/
// PartiallyFilled are signaled exclusively by decodeFillPush, and
// StatusExpired/StatusBlocked/StatusReplaced are currently unreachable via
// statusDomain (see mapping.go) -- handled defensively below, not
// fabricated.
func (p *pushDecoder) decodeOrderPush(venue exec.VenueID, resp *trdupdateorder.Response) []exec.BrokerEvent {
	if resp.GetRetType() != int32(common.RetType_RetType_Succeed) {
		slog.Warn("moomoo: Trd_UpdateOrder push carried a non-success retType (unexpected)",
			"retType", resp.GetRetType(), "retMsg", resp.GetRetMsg())
		return nil
	}
	o := resp.GetS2C().GetOrder()
	if o == nil {
		slog.Warn("moomoo: Trd_UpdateOrder push missing order payload")
		return nil
	}
	oid := o.GetRemark()
	if oid == "" {
		// Not placed by eTape (e.g. via the moomoo app or another client) --
		// no domain correlation, nothing to do.
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Always record: a later fill push needs this even if this particular
	// status transition doesn't itself produce an event.
	p.domainOIDByOrderID[o.GetOrderID()] = oid
	p.totalQtyByOrderID[o.GetOrderID()] = o.GetQty()

	raw := trdcommon.OrderStatus(o.GetOrderStatus())
	if raw == trdcommon.OrderStatus_OrderStatus_TimeOut {
		// "处理超时，结果未知" -- processing timed out, result unknown. Leave
		// the last CONFIRMED status untouched; a later push (once the
		// timeout resolves) is still treated as a fresh transition against
		// that last known-good status.
		return nil
	}

	status, ok := statusDomain(raw)
	if !ok {
		// FillCancelled or Unknown (TimeOut already handled above): rare,
		// documented-as-needing-reconciliation cases. Do not fabricate a
		// domain event here -- a later reconcile path handles them.
		slog.Warn("moomoo: Trd_UpdateOrder push carries an unmappable OrderStatus",
			"oid", oid, "orderID", o.GetOrderID(), "rawStatus", raw)
		return nil
	}

	prev, tracked := p.lastKnownStatus[oid]
	p.lastKnownStatus[oid] = status
	if tracked && prev == status {
		return nil // not a new transition
	}

	ts := tsMs(o.GetUpdateTimestamp())
	switch status {
	case exec.StatusAccepted:
		return []exec.BrokerEvent{exec.OrderAccepted{
			V: venue, OID: oid, BrokerOrderID: fmt.Sprint(o.GetOrderID()), Ts: ts,
		}}
	case exec.StatusCanceled:
		return []exec.BrokerEvent{exec.OrderCanceled{V: venue, OID: oid, Ts: ts}}
	case exec.StatusRejected:
		reason := o.GetLastErrMsg()
		if reason == "" {
			reason = "rejected"
		}
		return []exec.BrokerEvent{exec.OrderRejected{V: venue, OID: oid, Reason: reason, Ts: ts}}
	case exec.StatusSubmitted, exec.StatusPartiallyFilled, exec.StatusFilled:
		// Submitted has no one-shot BrokerEvent; Filled/PartiallyFilled are
		// signaled exclusively by decodeFillPush (2218), never here.
		return nil
	default:
		// StatusExpired/StatusBlocked/StatusReplaced: currently unreachable
		// given statusDomain's actual mapping (no moomoo OrderStatus value
		// maps to any of these today) -- handled defensively in case a
		// future statusDomain change ever produces one, not fabricated here.
		slog.Warn("moomoo: Trd_UpdateOrder push transitioned to a domain status this decoder does not emit an event for",
			"oid", oid, "status", status)
		return nil
	}
}

// decodeFillPush turns one Trd_UpdateOrderFill (2218) push into zero or one
// exec.OrderFilled events. Every field OrderFilled needs beyond this one
// fill's own qty/price (CumQty, LeavesQty, AvgPrice) is derived from state
// accumulated here across however many fill pushes have landed for this
// order, plus the total qty learned from an earlier order push.
//
// KNOWN LIMITATION: if a fill push arrives before this decoder has ever seen
// an order push for that numeric OrderID (domainOIDByOrderID has no entry --
// should be rare since moomoo always order-pushes an Accepted transition
// before any fill can occur, but is a real possible race, e.g. a process
// restart mid-flight), the fill is logged and DROPPED rather than emitted
// with a fabricated or wrong domain order id. A future task could have the
// caller (moomoo.go's Adapter, which owns a *trdClient) fall back to
// trdClient.getOrderList/orderByRemark-style resolution by scanning for the
// numeric OrderID when this happens -- out of this task's scope, since that
// would require pushDecoder to reach back into trdClient.
func (p *pushDecoder) decodeFillPush(venue exec.VenueID, resp *trdupdateorderfill.Response) []exec.BrokerEvent {
	if resp.GetRetType() != int32(common.RetType_RetType_Succeed) {
		slog.Warn("moomoo: Trd_UpdateOrderFill push carried a non-success retType (unexpected)",
			"retType", resp.GetRetType(), "retMsg", resp.GetRetMsg())
		return nil
	}
	f := resp.GetS2C().GetOrderFill()
	if f == nil {
		slog.Warn("moomoo: Trd_UpdateOrderFill push missing orderFill payload")
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	fillID := f.GetFillID()
	if p.seenFillIDs[fillID] {
		return nil // dedup: moomoo docs note fills can theoretically redeliver
	}
	p.seenFillIDs[fillID] = true

	orderID := f.GetOrderID()
	if orderID == 0 {
		slog.Warn("moomoo: Trd_UpdateOrderFill push has no OrderID; cannot correlate", "fillID", fillID)
		return nil
	}

	domainOID, known := p.domainOIDByOrderID[orderID]
	if !known {
		slog.Warn("moomoo: Trd_UpdateOrderFill push arrived before any Trd_UpdateOrder push for this OrderID; dropping fill (cannot correlate to a domain order id)",
			"orderID", orderID, "fillID", fillID)
		return nil
	}

	qty, price := f.GetQty(), f.GetPrice()
	p.cumQtyByOrderID[orderID] += qty
	p.cumNotionalByOrderID[orderID] += qty * price

	// Reconnect-boundary double-count mitigation (BOUNDING, not eliminating --
	// see the fuller race writeup on reconcileOrder above and onConnUp in
	// moomoo.go): moomoo.go's onConnUp subscribes this account to live pushes
	// (subAccPush) BEFORE reconcile reads its getOrderList snapshot, so a fill
	// landing in that window can be counted TWICE -- once via reconcileOrder's
	// catch-up path (which sets cumQtyByOrderID directly from the snapshot's
	// authoritative ExecutedQty) and again HERE when the same fill's queued
	// live push is processed afterward (which adds additively, with no way to
	// know the snapshot already accounted for it). Clamping to the order's
	// known total turns "an arbitrarily inflated CumQty" into "at worst,
	// LeavesQty reads 0 slightly early" -- state.go's applyFill takes CumQty
	// verbatim, so an unclamped overcount would otherwise corrupt the order's
	// tracked ExecutedQty (and the fills ledger feeding P&L/trade export).
	// This does NOT close the race itself: cumNotionalByOrderID is left
	// unclamped (so AvgPrice can still be mildly perturbed in this same rare
	// window), and a full fix would require either reordering onConnUp
	// (snapshot-before-subscribe, which trades this risk for a "missed fill
	// until the next reconnect" risk) or a different reconciliation strategy
	// entirely -- both are deferred, out of scope for this bounding mitigation.
	if total, knownTotal := p.totalQtyByOrderID[orderID]; knownTotal && p.cumQtyByOrderID[orderID] > total {
		p.cumQtyByOrderID[orderID] = total
	}

	cumQty := p.cumQtyByOrderID[orderID]
	avgPrice := 0.0
	if cumQty > 0 {
		avgPrice = p.cumNotionalByOrderID[orderID] / cumQty
	}

	leavesQty := 0.0
	if total, knownTotal := p.totalQtyByOrderID[orderID]; knownTotal {
		leavesQty = total - cumQty
		if leavesQty < 0 {
			leavesQty = 0
		}
	}
	// If the total qty was never learned, leavesQty stays 0 rather than a
	// fabricated number -- corrected by a later reconcile/snapshot.

	ts := tsMs(f.GetCreateTimestamp())
	return []exec.BrokerEvent{exec.OrderFilled{
		F: exec.Fill{
			Venue: venue, OrderID: domainOID, Symbol: domainSymbol(f.GetCode()),
			Side: sideDomain(trdcommon.TrdSide(f.GetTrdSide())), Qty: qty, Price: price, TsMs: ts,
		},
		CumQty:    cumQty,
		LeavesQty: leavesQty,
		AvgPrice:  avgPrice,
	}}
}

// tsMs converts one of moomoo's epoch-seconds float64 timestamps (e.g.
// Order.UpdateTimestamp, OrderFill.CreateTimestamp) to epoch milliseconds,
// defaulting to 0 when missing/zero rather than fabricating "now" --
// mirrors alpaca/normalize.go's parseTs convention exactly, keeping
// pushDecoder free of any clock dependency.
func tsMs(epochSeconds float64) int64 {
	if epochSeconds == 0 {
		return 0
	}
	return int64(epochSeconds * 1000)
}
