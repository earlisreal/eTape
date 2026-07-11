// Package moomoo -- this file (trd.go) is the trade transport layer: trdClient
// wraps a trade-only *opend.Client and turns every trdClient method into one
// request/response round trip over moomoo's Trd_* protobuf protocols,
// translating via mapping.go (Task 2) at the boundary. Structurally this plays
// the role alpaca/rest.go's restClient plays for Alpaca -- one struct wrapping
// the transport, one method per operation, never a silent false-success -- just
// over opend.Client.Request instead of net/http.
//
// This file holds ONLY the transport: no push-decode (normalize.go, a later
// task) and no exec.Broker Adapter/Run/reconcile wiring (moomoo.go, a later
// task).
package moomoo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/common"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdgetacclist"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdgetfunds"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdgetorderlist"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdgetpositionlist"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdmodifyorder"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdplaceorder"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdsubaccpush"
)

// trdClient is moomoo's trade transport: order entry/modify/cancel, account
// resolution/validation, and account/position/order snapshots, all over a
// trade-only *opend.Client. c.ConnID() supplies PacketID.ConnID on every
// write; serial is this adapter's OWN monotonic PacketID.SerialNo counter --
// a business-level anti-replay marker, deliberately distinct from
// opend.Client's private frame-correlation serial (serial.go), which this
// package cannot see and does not need to.
type trdClient struct {
	c     *opend.Client
	accID uint64
	env   string // "paper" or "live", as configured
	clk   clock.Clock

	serial atomic.Uint32

	// Documented per-venue token-bucket limits (plan research): PlaceOrder 15
	// req/30s, ModifyOrder (covers Normal/Cancel/CancelAll -- same wire
	// protocol) 20 req/30s. Burst is deliberately well below the window count,
	// mirroring alpaca/rest.go's restClient.bucket judgment call.
	//
	// A third bucket ("query bucket spent only when refreshCache=true") is
	// mentioned in the plan's research with no specific numeric rate found --
	// getFunds/getPositionList/getOrderList/orderByRemark deliberately have no
	// rate limiter here; a future task must supply the real number rather than
	// have this one fabricate it.
	placeBucket  *netx.TokenBucket
	modifyBucket *netx.TokenBucket
}

func newTrdClient(c *opend.Client, accID uint64, env string, clk clock.Clock) *trdClient {
	return &trdClient{
		c:            c,
		accID:        accID,
		env:          env,
		clk:          clk,
		placeBucket:  netx.NewTokenBucket(clk, 15.0/30.0, 3),
		modifyBucket: netx.NewTokenBucket(clk, 20.0/30.0, 3),
	}
}

// retOK reports whether a Trd_* response's retType indicates success. Every
// method below treats anything else as a hard error -- a response this
// package can't confirm as successful is always an error, never a
// default-accept (mirrors alpaca/rest.go's apiError philosophy).
func retOK(retType int32) bool {
	return retType == int32(common.RetType_RetType_Succeed)
}

func retErr(op string, retType int32, retMsg string) error {
	return fmt.Errorf("moomoo: %s rejected: retType=%d retMsg=%s", op, retType, retMsg)
}

// placeOrder submits req and returns moomoo's broker-assigned numeric order
// id. remark carries eTape's domain ClientOrderID -- moomoo's order
// pushes/lists carry it back in Remark, not a native lookup key (see
// orderByRemark). ot/tif translation errors are returned immediately, before
// any rate-limit token is spent or any request sent -- never a malformed
// request on the wire.
func (tc *trdClient) placeOrder(ctx context.Context, req exec.OrderRequest, remark string) (uint64, error) {
	ot, err := orderTypeWire(req.Type)
	if err != nil {
		return 0, err
	}
	tif, err := tifWire(req.TIF)
	if err != nil {
		return 0, err
	}

	c2s := &trdplaceorder.C2S{
		PacketID:    packetID(tc.c.ConnID(), tc.serial.Add(1)),
		Header:      trdHeader(tc.accID, tc.env),
		TrdSide:     proto.Int32(int32(sideWire(req.Side))),
		OrderType:   proto.Int32(int32(ot)),
		Code:        proto.String(wireSymbol(req.Symbol)),
		Qty:         proto.Float64(req.Qty),
		SecMarket:   proto.Int32(int32(secMarket())),
		Remark:      proto.String(remark),
		TimeInForce: proto.Int32(int32(tif)),
		Session:     proto.Int32(int32(sessionWire(req.Session, tc.clk))),
	}
	if req.Type == exec.TypeLimit || req.Type == exec.TypeStopLimit {
		c2s.Price = proto.Float64(req.LimitPrice)
	}
	if req.Type == exec.TypeStop || req.Type == exec.TypeStopLimit {
		c2s.AuxPrice = proto.Float64(req.StopPrice)
	}

	if err := tc.placeBucket.Take(ctx); err != nil {
		return 0, err
	}
	fr, err := tc.c.Request(ctx, opend.ProtoTrdPlaceOrder, &trdplaceorder.Request{C2S: c2s})
	if err != nil {
		return 0, fmt.Errorf("moomoo: place order transport: %w", err)
	}
	var resp trdplaceorder.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return 0, fmt.Errorf("moomoo: decode place order response: %w", err)
	}
	if !retOK(resp.GetRetType()) {
		return 0, retErr("place order", resp.GetRetType(), resp.GetRetMsg())
	}

	s2c := resp.GetS2C()
	if s2c != nil && s2c.OrderID != nil {
		return s2c.GetOrderID(), nil
	}
	if s2c != nil && s2c.OrderIDEx != nil && s2c.GetOrderIDEx() != "" {
		id, err := strconv.ParseUint(s2c.GetOrderIDEx(), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("moomoo: place order response orderIDEx %q is not a valid uint64: %w", s2c.GetOrderIDEx(), err)
		}
		return id, nil
	}
	return 0, fmt.Errorf("moomoo: place order response missing both orderID and orderIDEx")
}

// sendModify sends c2s over Trd_ModifyOrder and checks retType. Shared by
// modifyOrder, cancelOrder, and cancelAll's forAll branch -- all three are the
// same wire protocol, differing only in which fields are set.
func (tc *trdClient) sendModify(ctx context.Context, c2s *trdmodifyorder.C2S) error {
	if err := tc.modifyBucket.Take(ctx); err != nil {
		return err
	}
	fr, err := tc.c.Request(ctx, opend.ProtoTrdModifyOrder, &trdmodifyorder.Request{C2S: c2s})
	if err != nil {
		return fmt.Errorf("moomoo: modify order transport: %w", err)
	}
	var resp trdmodifyorder.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return fmt.Errorf("moomoo: decode modify order response: %w", err)
	}
	if !retOK(resp.GetRetType()) {
		return retErr("modify order", resp.GetRetType(), resp.GetRetMsg())
	}
	return nil
}

// modifyOrder is moomoo's native amend: ModifyOrderOp_Normal. Only non-zero
// fields of rr are sent, mirroring Alpaca's replaceOrder exactly -- rr's
// fields are absolute NEW values when set, and zero means "don't change this
// field," not "set to zero." AuxPrice (StopPrice) is included on the same
// only-if-nonzero basis as Qty/Price for completeness on stop-bearing order
// types; moomoo's own docs mark it (like Qty/Price) as meaningful only for
// ModifyOrderOp_Normal on a single order, which this always is.
func (tc *trdClient) modifyOrder(ctx context.Context, orderID uint64, rr exec.ReplaceRequest) error {
	c2s := &trdmodifyorder.C2S{
		PacketID:      packetID(tc.c.ConnID(), tc.serial.Add(1)),
		Header:        trdHeader(tc.accID, tc.env),
		OrderID:       proto.Uint64(orderID),
		ModifyOrderOp: proto.Int32(int32(trdcommon.ModifyOrderOp_ModifyOrderOp_Normal)),
	}
	if rr.Qty > 0 {
		c2s.Qty = proto.Float64(rr.Qty)
	}
	if rr.LimitPrice > 0 {
		c2s.Price = proto.Float64(rr.LimitPrice)
	}
	if rr.StopPrice > 0 {
		c2s.AuxPrice = proto.Float64(rr.StopPrice)
	}
	return tc.sendModify(ctx, c2s)
}

// cancelOrder cancels a single working order. Qty/Price are explicitly zeroed
// (rather than left nil) to match the documented/verified Python SDK
// behavior for a single-order cancel (docs/2026-07-04-moomoo-trading-api.md).
func (tc *trdClient) cancelOrder(ctx context.Context, orderID uint64) error {
	c2s := &trdmodifyorder.C2S{
		PacketID:      packetID(tc.c.ConnID(), tc.serial.Add(1)),
		Header:        trdHeader(tc.accID, tc.env),
		OrderID:       proto.Uint64(orderID),
		ModifyOrderOp: proto.Int32(int32(trdcommon.ModifyOrderOp_ModifyOrderOp_Cancel)),
		Qty:           proto.Float64(0),
		Price:         proto.Float64(0),
	}
	return tc.sendModify(ctx, c2s)
}

// cancelAll cancels every open order, optionally scoped to symbol. moomoo has
// no symbol-scoped forAll (per the plan's protocol research), so the single
// wire-call forAll path only applies with env=="live" AND no symbol filter;
// every other combination (paper, or live with a symbol) lists working orders
// via getOrderList and cancels each individually, joining all errors rather
// than stopping at the first failure (mirrors alpaca/rest.go's cancelAll).
//
// Orders whose statusDomain is ambiguous (ok=false: TimeOut/FillCancelled/
// Unknown) are treated conservatively -- still attempted for cancel rather
// than skipped, since this package cannot confirm they are already terminal.
func (tc *trdClient) cancelAll(ctx context.Context, symbol string) error {
	if tc.env == "live" && symbol == "" {
		c2s := &trdmodifyorder.C2S{
			PacketID:      packetID(tc.c.ConnID(), tc.serial.Add(1)),
			Header:        trdHeader(tc.accID, tc.env),
			OrderID:       proto.Uint64(0),
			ModifyOrderOp: proto.Int32(int32(trdcommon.ModifyOrderOp_ModifyOrderOp_Cancel)),
			ForAll:        proto.Bool(true),
			TrdMarket:     proto.Int32(int32(trdcommon.TrdMarket_TrdMarket_US)),
		}
		return tc.sendModify(ctx, c2s)
	}

	orders, err := tc.getOrderList(ctx, true)
	if err != nil {
		return fmt.Errorf("moomoo: cancel-all list orders: %w", err)
	}
	wireSym := ""
	if symbol != "" {
		wireSym = wireSymbol(symbol)
	}
	var errs []error
	for _, o := range orders {
		if wireSym != "" && o.GetCode() != wireSym {
			continue
		}
		if !orderStillWorking(o) {
			continue
		}
		if err := tc.cancelOrder(ctx, o.GetOrderID()); err != nil {
			errs = append(errs, fmt.Errorf("cancel order %d: %w", o.GetOrderID(), err))
		}
	}
	return errors.Join(errs...)
}

// orderStillWorking reports whether o should be included in a cancelAll
// sweep: a confirmed working status (Submitted/Accepted/PartiallyFilled), OR
// an ambiguous status (statusDomain ok=false) that this package cannot
// confirm is already terminal -- attempt the cancel rather than silently
// skip it.
func orderStillWorking(o *trdcommon.Order) bool {
	status, ok := statusDomain(trdcommon.OrderStatus(o.GetOrderStatus()))
	if !ok {
		return true
	}
	switch status {
	case exec.StatusSubmitted, exec.StatusAccepted, exec.StatusPartiallyFilled:
		return true
	default:
		return false
	}
}

// getAccList resolves and validates tc.accID against moomoo's account list.
// Every validation failure names exactly which check failed -- never a
// generic "invalid account" -- since a later task (the exec.Broker Adapter)
// needs to surface these to Earl at boot time.
func (tc *trdClient) getAccList(ctx context.Context) (*trdcommon.TrdAcc, error) {
	req := &trdgetacclist.Request{C2S: &trdgetacclist.C2S{UserID: proto.Uint64(0)}} // required-but-deprecated proto2 field, see boot.go's ProbeRTT precedent
	fr, err := tc.c.Request(ctx, opend.ProtoTrdGetAccList, req)
	if err != nil {
		return nil, fmt.Errorf("moomoo: get acc list transport: %w", err)
	}
	var resp trdgetacclist.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, fmt.Errorf("moomoo: decode get acc list response: %w", err)
	}
	if !retOK(resp.GetRetType()) {
		return nil, retErr("get acc list", resp.GetRetType(), resp.GetRetMsg())
	}

	var acc *trdcommon.TrdAcc
	for _, a := range resp.GetS2C().GetAccList() {
		if a.GetAccID() == tc.accID {
			acc = a
			break
		}
	}
	if acc == nil {
		return nil, fmt.Errorf("moomoo: accID %d not found in account list", tc.accID)
	}
	if trdcommon.TrdAccRole(acc.GetAccRole()) == trdcommon.TrdAccRole_TrdAccRole_Master {
		return nil, fmt.Errorf("moomoo: accID %d is a MASTER account -- moomoo does not allow a master account to place orders", tc.accID)
	}
	if trdcommon.TrdAccStatus(acc.GetAccStatus()) == trdcommon.TrdAccStatus_TrdAccStatus_Disabled {
		return nil, fmt.Errorf("moomoo: accID %d is disabled (accStatus=Disabled)", tc.accID)
	}
	usAuthorized := false
	for _, m := range acc.GetTrdMarketAuthList() {
		if trdcommon.TrdMarket(m) == trdcommon.TrdMarket_TrdMarket_US {
			usAuthorized = true
			break
		}
	}
	if !usAuthorized {
		return nil, fmt.Errorf("moomoo: accID %d is not authorized to trade market US (trdMarketAuthList=%v)", tc.accID, acc.GetTrdMarketAuthList())
	}
	wantEnv := trdcommon.TrdEnv_TrdEnv_Simulate
	if tc.env == "live" {
		wantEnv = trdcommon.TrdEnv_TrdEnv_Real
	}
	if gotEnv := trdcommon.TrdEnv(acc.GetTrdEnv()); gotEnv != wantEnv {
		return nil, fmt.Errorf("moomoo: accID %d trdEnv=%v does not match configured env %q (want %v)", tc.accID, gotEnv, tc.env, wantEnv)
	}
	return acc, nil
}

// getFunds fetches account funds. A response that decodes successfully but
// carries no Funds payload is treated as an error -- this feeds
// exec.AccountSnapshot, and a nil Funds silently zeroing every balance field
// would be indistinguishable from a genuinely empty account.
func (tc *trdClient) getFunds(ctx context.Context) (*trdcommon.Funds, error) {
	req := &trdgetfunds.Request{C2S: &trdgetfunds.C2S{Header: trdHeader(tc.accID, tc.env)}}
	fr, err := tc.c.Request(ctx, opend.ProtoTrdGetFunds, req)
	if err != nil {
		return nil, fmt.Errorf("moomoo: get funds transport: %w", err)
	}
	var resp trdgetfunds.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, fmt.Errorf("moomoo: decode get funds response: %w", err)
	}
	if !retOK(resp.GetRetType()) {
		return nil, retErr("get funds", resp.GetRetType(), resp.GetRetMsg())
	}
	funds := resp.GetS2C().GetFunds()
	if funds == nil {
		return nil, fmt.Errorf("moomoo: get funds response missing funds payload")
	}
	return funds, nil
}

func (tc *trdClient) getPositionList(ctx context.Context) ([]*trdcommon.Position, error) {
	req := &trdgetpositionlist.Request{C2S: &trdgetpositionlist.C2S{Header: trdHeader(tc.accID, tc.env)}}
	fr, err := tc.c.Request(ctx, opend.ProtoTrdGetPositionList, req)
	if err != nil {
		return nil, fmt.Errorf("moomoo: get position list transport: %w", err)
	}
	var resp trdgetpositionlist.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, fmt.Errorf("moomoo: decode get position list response: %w", err)
	}
	if !retOK(resp.GetRetType()) {
		return nil, retErr("get position list", resp.GetRetType(), resp.GetRetMsg())
	}
	return resp.GetS2C().GetPositionList(), nil
}

func (tc *trdClient) getOrderList(ctx context.Context, refreshCache bool) ([]*trdcommon.Order, error) {
	req := &trdgetorderlist.Request{C2S: &trdgetorderlist.C2S{
		Header:       trdHeader(tc.accID, tc.env),
		RefreshCache: proto.Bool(refreshCache),
	}}
	fr, err := tc.c.Request(ctx, opend.ProtoTrdGetOrderList, req)
	if err != nil {
		return nil, fmt.Errorf("moomoo: get order list transport: %w", err)
	}
	var resp trdgetorderlist.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, fmt.Errorf("moomoo: decode get order list response: %w", err)
	}
	if !retOK(resp.GetRetType()) {
		return nil, retErr("get order list", resp.GetRetType(), resp.GetRetMsg())
	}
	return resp.GetS2C().GetOrderList(), nil
}

// orderByRemark resolves the domain ClientOrderID -> moomoo order by linear
// scan over getOrderList: moomoo's order pushes/lists carry the domain id in
// Remark, not as a native lookup key. This is moomoo's analog of Alpaca's
// orderByClientID (alpaca/rest.go). found=false (nil, false, nil) means the
// list came back clean with no match; a transport/decode error is a real
// error, never masked as "not found."
func (tc *trdClient) orderByRemark(ctx context.Context, remark string, refreshCache bool) (*trdcommon.Order, bool, error) {
	orders, err := tc.getOrderList(ctx, refreshCache)
	if err != nil {
		return nil, false, err
	}
	for _, o := range orders {
		if o.GetRemark() == remark {
			return o, true, nil
		}
	}
	return nil, false, nil
}

// subAccPush registers accIDList to receive Trd_UpdateOrder/Trd_UpdateOrderFill
// pushes on this connection. accIDList is a full replacement list, not an
// increment (per moomoo's docs), which is the caller's responsibility to
// respect.
func (tc *trdClient) subAccPush(ctx context.Context, accIDList []uint64) error {
	req := &trdsubaccpush.Request{C2S: &trdsubaccpush.C2S{AccIDList: accIDList}}
	fr, err := tc.c.Request(ctx, opend.ProtoTrdSubAccPush, req)
	if err != nil {
		return fmt.Errorf("moomoo: sub acc push transport: %w", err)
	}
	var resp trdsubaccpush.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return fmt.Errorf("moomoo: decode sub acc push response: %w", err)
	}
	if !retOK(resp.GetRetType()) {
		return retErr("sub acc push", resp.GetRetType(), resp.GetRetMsg())
	}
	return nil
}

// orderTypeDomain reverses orderTypeWire for decoding a moomoo order snapshot
// back to eTape's domain OrderType. Lives here, not mapping.go, mirroring
// Alpaca's own orderTypeDomain precedent (alpaca/rest.go): the decode
// direction lives with its consumer, not the (already reviewed/approved)
// pure-translation file.
func orderTypeDomain(t trdcommon.OrderType) exec.OrderType {
	switch t {
	case trdcommon.OrderType_OrderType_Normal:
		return exec.TypeLimit
	case trdcommon.OrderType_OrderType_Stop:
		return exec.TypeStop
	case trdcommon.OrderType_OrderType_StopLimit:
		return exec.TypeStopLimit
	default: // Market, and any order type eTape never itself sends.
		return exec.TypeMarket
	}
}

// positionQtyDomain signs a moomoo position's quantity by PositionSide,
// mirroring Alpaca's positionQtyDomain sign-handling pattern (alpaca/rest.go)
// but keyed on the enum moomoo actually reports rather than a side string.
func positionQtyDomain(p *trdcommon.Position) float64 {
	qty := p.GetQty()
	if trdcommon.PositionSide(p.GetPositionSide()) == trdcommon.PositionSide_PositionSide_Short {
		return -qty
	}
	return qty
}

// orderDomain converts one moomoo order snapshot entry into the
// broker-agnostic exec.Order shape used by snapshot. ID is Remark (the
// domain ClientOrderID eTape minted), NOT OrderID/OrderIDEx (moomoo's own
// numeric ids) -- see orderByRemark's doc comment for why. TIF, Session,
// RejectReason, CreatedMs, UpdatedMs are deliberately left at zero value,
// mirroring Alpaca's own auOrder.domain() precedent (alpaca/rest.go), which
// leaves the same fields unset in its own snapshot-decode path -- not a gap
// unique to moomoo.
//
// Status: when statusDomain's ok is false (TimeOut/FillCancelled/Unknown --
// see mapping.go), this uses exec.StatusSubmitted as a conservative
// placeholder rather than asserting a status it cannot back up. There is no
// "needs reconciliation" value in the domain OrderStatus enum;
// StatusSubmitted under-claims certainty rather than over-claiming it,
// mirroring Alpaca's own precedent of never asserting a status it can't
// confirm.
func orderDomain(o *trdcommon.Order) exec.Order {
	status, ok := statusDomain(trdcommon.OrderStatus(o.GetOrderStatus()))
	if !ok {
		status = exec.StatusSubmitted
	}
	return exec.Order{
		ID:           o.GetRemark(),
		Symbol:       domainSymbol(o.GetCode()),
		Side:         sideDomain(trdcommon.TrdSide(o.GetTrdSide())),
		Type:         orderTypeDomain(trdcommon.OrderType(o.GetOrderType())),
		Qty:          o.GetQty(),
		LimitPrice:   o.GetPrice(),
		StopPrice:    o.GetAuxPrice(),
		Status:       status,
		ExecutedQty:  o.GetFillQty(),
		LeavesQty:    o.GetQty() - o.GetFillQty(),
		AvgFillPrice: o.GetFillAvgPrice(),
	}
}

// snapshot composes getFunds + getPositionList + getOrderList(refreshCache=
// true -- a full reconcile always wants fresh data, mirroring Alpaca's own
// snapshot) into the broker-agnostic (AccountSnapshot, []Position, []Order)
// shape. DayPnL, SodEquity, and Leverage are deliberately left at zero value:
// moomoo's Funds message has no day-P&L field and no field that clearly
// corresponds to the other two -- a later architectural decision (out of
// this task's scope) decides how moomoo's day-loss gets computed from
// eTape's own ledger instead, rather than this function guessing/fabricating
// a mapping.
//
// IMPORTANT -- MaxDayLoss circuit breaker gap: because AccountSnapshot.DayPnL
// is always 0 here (Trd_GetFunds has no day-P&L field, and no ledger-derived
// alternative has been built), exec.Core's global MaxDayLoss circuit breaker
// (gate.go's BreachedDayLoss, which sums every venue's DayPnL via state.go)
// does NOT see moomoo's contribution to the day's aggregate loss. Do NOT
// live-arm a moomoo venue as your PRIMARY or ONLY venue without either
// (a) building a ledger-derived day-loss computation for moomoo, or
// (b) explicitly accepting that MaxDayLoss provides no protection for
// moomoo-originated losses. See docs/2026-07-04-moomoo-trading-api.md's
// status section for the tracked, unresolved state of this gap.
//
// AvgPrice uses AverageCostPrice per
// docs/2026-07-04-moomoo-trading-api.md's guidance (never CostPrice/
// DilutedCostPrice, which are documented diluted/stale) -- if unset on a
// paper account, falling through to zero is the accepted degraded paper
// behavior, not a bug to route around.
func (tc *trdClient) snapshot(ctx context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	funds, err := tc.getFunds(ctx)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, err
	}
	positions, err := tc.getPositionList(ctx)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, err
	}
	orders, err := tc.getOrderList(ctx, true)
	if err != nil {
		return exec.AccountSnapshot{}, nil, nil, err
	}

	acct := exec.AccountSnapshot{
		Equity:        funds.GetTotalAssets(),
		BuyingPower:   funds.GetPower(),
		AvailableCash: funds.GetCash(),
		Realized:      funds.GetRealizedPL(),
		TsMs:          tc.clk.Now().UnixMilli(),
	}

	pos := make([]exec.Position, 0, len(positions))
	for _, p := range positions {
		pos = append(pos, exec.Position{
			Symbol:   domainSymbol(p.GetCode()),
			Qty:      positionQtyDomain(p),
			AvgPrice: p.GetAverageCostPrice(),
		})
	}

	ords := make([]exec.Order, 0, len(orders))
	for _, o := range orders {
		ords = append(ords, orderDomain(o))
	}

	return acct, pos, ords, nil
}
