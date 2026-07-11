package moomoo

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

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

const testAccID = uint64(123456789)

// newTestTrdClient dials a real opend.Client at m's address (System clock --
// this exercises the actual TCP framing/handshake, not a mock of
// opend.Client) and wraps it in a trdClient using clk for the trdClient's own
// session/rate-limit/timestamp logic. Decoupling the two clocks lets a test
// drive trdClient's rate limiter with a fake clock without also freezing the
// opend.Client's own request-timeout/keepalive timers.
func newTestTrdClient(t *testing.T, m *mockTrdOpenD, accID uint64, env string, clk clock.Clock) *trdClient {
	t.Helper()
	c := opend.New(opend.Options{Addr: m.addr(), Clock: clock.System{}, RequestTimeout: 2 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.ConnID() != 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if c.ConnID() == 0 {
		t.Fatal("client did not connect within 2s")
	}
	return newTrdClient(c, accID, env, clk)
}

func succeedPlaceOrder(orderID uint64) func(opend.Frame) proto.Message {
	return func(opend.Frame) proto.Message {
		return &trdplaceorder.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
			S2C:     &trdplaceorder.S2C{Header: trdHeader(testAccID, "paper"), OrderID: proto.Uint64(orderID)},
		}
	}
}

func succeedModify() func(opend.Frame) proto.Message {
	return func(opend.Frame) proto.Message {
		return &trdmodifyorder.Response{RetType: proto.Int32(int32(common.RetType_RetType_Succeed))}
	}
}

func TestTrdClient_PlaceOrder_Success(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdPlaceOrder, succeedPlaceOrder(555))
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	req := exec.OrderRequest{
		Symbol: "US.AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay,
		Qty: 10, LimitPrice: 123.45,
	}
	oid, err := tc.placeOrder(context.Background(), req, "clientOrder-1")
	if err != nil {
		t.Fatalf("placeOrder: %v", err)
	}
	if oid != 555 {
		t.Fatalf("orderID = %d, want 555", oid)
	}

	frames := m.requestsFor(opend.ProtoTrdPlaceOrder)
	if len(frames) != 1 {
		t.Fatalf("got %d place-order requests, want 1", len(frames))
	}
	var got trdplaceorder.Request
	if err := proto.Unmarshal(frames[0].Body, &got); err != nil {
		t.Fatal(err)
	}
	c2s := got.GetC2S()
	if c2s.GetCode() != "AAPL" {
		t.Errorf("code = %q, want AAPL (US. prefix must be stripped)", c2s.GetCode())
	}
	if c2s.GetQty() != 10 || c2s.GetPrice() != 123.45 {
		t.Errorf("qty/price = %v/%v, want 10/123.45", c2s.GetQty(), c2s.GetPrice())
	}
	if c2s.GetRemark() != "clientOrder-1" {
		t.Errorf("remark = %q, want clientOrder-1", c2s.GetRemark())
	}
	if c2s.GetPacketID().GetConnID() == 0 {
		t.Error("packetID.connID should be the live connection id, got 0")
	}
	if c2s.GetPacketID().GetSerialNo() == 0 {
		t.Error("packetID.serialNo should be nonzero after the first write")
	}
	if c2s.AuxPrice != nil {
		t.Errorf("auxPrice should be unset for a plain limit order, got %v", c2s.GetAuxPrice())
	}
}

func TestTrdClient_PlaceOrder_HardReject(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdPlaceOrder, func(opend.Frame) proto.Message {
		return &trdplaceorder.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Failed)),
			RetMsg:  proto.String("insufficient buying power"),
		}
	})
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	req := exec.OrderRequest{Symbol: "US.AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 10}
	oid, err := tc.placeOrder(context.Background(), req, "clientOrder-2")
	if err == nil {
		t.Fatal("expected error on hard reject, got nil")
	}
	if oid != 0 {
		t.Fatalf("orderID = %d on error, want 0 (never a silent false-success)", oid)
	}
}

// TestTrdClient_PlaceOrder_BadTIF_NeverSendsOnWire verifies a translation
// error (tifWire rejects IOC for US stocks, per mapping.go) short-circuits
// before any frame reaches the transport -- never a malformed request on the
// wire.
func TestTrdClient_PlaceOrder_BadTIF_NeverSendsOnWire(t *testing.T) {
	m := newMockTrdOpenD(t)
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	req := exec.OrderRequest{Symbol: "US.AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFIOC, Qty: 10}
	if _, err := tc.placeOrder(context.Background(), req, "clientOrder-3"); err == nil {
		t.Fatal("expected TIF translation error")
	}
	if frames := m.requestsFor(opend.ProtoTrdPlaceOrder); len(frames) != 0 {
		t.Fatalf("got %d place-order requests, want 0 (malformed request must never be sent)", len(frames))
	}
}

func TestTrdClient_PlaceOrder_StopLimit_SetsPriceAndAuxPrice(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdPlaceOrder, succeedPlaceOrder(1))
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	req := exec.OrderRequest{
		Symbol: "US.AAPL", Side: exec.SideSell, Type: exec.TypeStopLimit, TIF: exec.TIFGTC,
		Qty: 5, LimitPrice: 100, StopPrice: 101,
	}
	if _, err := tc.placeOrder(context.Background(), req, "clientOrder-4"); err != nil {
		t.Fatalf("placeOrder: %v", err)
	}
	frames := m.requestsFor(opend.ProtoTrdPlaceOrder)
	var got trdplaceorder.Request
	if err := proto.Unmarshal(frames[len(frames)-1].Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.GetC2S().GetPrice() != 100 || got.GetC2S().GetAuxPrice() != 101 {
		t.Errorf("price/auxPrice = %v/%v, want 100/101", got.GetC2S().GetPrice(), got.GetC2S().GetAuxPrice())
	}
}

func TestTrdClient_ModifyOrder_OnlyNonZeroFieldsSent(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdModifyOrder, succeedModify())
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	if err := tc.modifyOrder(context.Background(), 42, exec.ReplaceRequest{Qty: 20}); err != nil {
		t.Fatalf("modifyOrder: %v", err)
	}
	frames := m.requestsFor(opend.ProtoTrdModifyOrder)
	if len(frames) != 1 {
		t.Fatalf("got %d modify requests, want 1", len(frames))
	}
	var got trdmodifyorder.Request
	if err := proto.Unmarshal(frames[0].Body, &got); err != nil {
		t.Fatal(err)
	}
	c2s := got.GetC2S()
	if trdcommon.ModifyOrderOp(c2s.GetModifyOrderOp()) != trdcommon.ModifyOrderOp_ModifyOrderOp_Normal {
		t.Errorf("modifyOrderOp = %v, want Normal", c2s.GetModifyOrderOp())
	}
	if c2s.GetOrderID() != 42 {
		t.Errorf("orderID = %d, want 42", c2s.GetOrderID())
	}
	if c2s.Qty == nil || c2s.GetQty() != 20 {
		t.Errorf("qty = %v, want 20 set", c2s.Qty)
	}
	if c2s.Price != nil {
		t.Errorf("price should be unset (rr.LimitPrice was zero), got %v", c2s.GetPrice())
	}
}

func TestTrdClient_CancelOrder_Success(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdModifyOrder, succeedModify())
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	if err := tc.cancelOrder(context.Background(), 99); err != nil {
		t.Fatalf("cancelOrder: %v", err)
	}
	frames := m.requestsFor(opend.ProtoTrdModifyOrder)
	if len(frames) != 1 {
		t.Fatalf("got %d modify requests, want 1", len(frames))
	}
	var got trdmodifyorder.Request
	if err := proto.Unmarshal(frames[0].Body, &got); err != nil {
		t.Fatal(err)
	}
	c2s := got.GetC2S()
	if trdcommon.ModifyOrderOp(c2s.GetModifyOrderOp()) != trdcommon.ModifyOrderOp_ModifyOrderOp_Cancel {
		t.Errorf("modifyOrderOp = %v, want Cancel", c2s.GetModifyOrderOp())
	}
	if c2s.GetOrderID() != 99 {
		t.Errorf("orderID = %d, want 99", c2s.GetOrderID())
	}
	if c2s.Qty == nil || c2s.GetQty() != 0 {
		t.Errorf("qty should be explicitly zeroed (not nil), got %v", c2s.Qty)
	}
	if c2s.Price == nil || c2s.GetPrice() != 0 {
		t.Errorf("price should be explicitly zeroed (not nil), got %v", c2s.Price)
	}
}

func TestTrdClient_CancelAll_LiveNoSymbol_UsesForAll(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdModifyOrder, succeedModify())
	tc := newTestTrdClient(t, m, testAccID, "live", clock.System{})

	if err := tc.cancelAll(context.Background(), ""); err != nil {
		t.Fatalf("cancelAll: %v", err)
	}
	if frames := m.requestsFor(opend.ProtoTrdGetOrderList); len(frames) != 0 {
		t.Fatalf("live+no-symbol must not list orders, got %d getOrderList calls", len(frames))
	}
	frames := m.requestsFor(opend.ProtoTrdModifyOrder)
	if len(frames) != 1 {
		t.Fatalf("got %d modify requests, want exactly 1 forAll call", len(frames))
	}
	var got trdmodifyorder.Request
	if err := proto.Unmarshal(frames[0].Body, &got); err != nil {
		t.Fatal(err)
	}
	c2s := got.GetC2S()
	if !c2s.GetForAll() {
		t.Error("forAll should be true")
	}
	if trdcommon.ModifyOrderOp(c2s.GetModifyOrderOp()) != trdcommon.ModifyOrderOp_ModifyOrderOp_Cancel {
		t.Errorf("modifyOrderOp = %v, want Cancel", c2s.GetModifyOrderOp())
	}
	if c2s.GetOrderID() != 0 {
		t.Errorf("orderID = %d, want 0 for forAll", c2s.GetOrderID())
	}
}

// orderFixture builds a minimal *trdcommon.Order for cancelAll list-response
// tests.
func orderFixture(orderID uint64, code string, status trdcommon.OrderStatus) *trdcommon.Order {
	return &trdcommon.Order{
		TrdSide:     proto.Int32(int32(trdcommon.TrdSide_TrdSide_Buy)),
		OrderType:   proto.Int32(int32(trdcommon.OrderType_OrderType_Normal)),
		OrderStatus: proto.Int32(int32(status)),
		OrderID:     proto.Uint64(orderID),
		OrderIDEx:   proto.String("ex"),
		Code:        proto.String(code),
		Name:        proto.String(code),
		Qty:         proto.Float64(1),
		CreateTime:  proto.String("2026-07-11 09:30:00"),
		UpdateTime:  proto.String("2026-07-11 09:30:00"),
		Remark:      proto.String("remark-" + code),
	}
}

func TestTrdClient_CancelAll_LiveWithSymbol_IteratesFilteredBySymbol(t *testing.T) {
	m := newMockTrdOpenD(t)
	orders := []*trdcommon.Order{
		orderFixture(1, "AAPL", trdcommon.OrderStatus_OrderStatus_Submitted),  // matches symbol, working -> cancel
		orderFixture(2, "AAPL", trdcommon.OrderStatus_OrderStatus_Filled_All), // matches symbol, terminal -> skip
		orderFixture(3, "MSFT", trdcommon.OrderStatus_OrderStatus_Submitted),  // different symbol -> skip
	}
	m.setRespond(opend.ProtoTrdGetOrderList, func(opend.Frame) proto.Message {
		return &trdgetorderlist.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
			S2C:     &trdgetorderlist.S2C{Header: trdHeader(testAccID, "paper"), OrderList: orders},
		}
	})
	m.setRespond(opend.ProtoTrdModifyOrder, succeedModify())
	tc := newTestTrdClient(t, m, testAccID, "live", clock.System{})

	if err := tc.cancelAll(context.Background(), "US.AAPL"); err != nil {
		t.Fatalf("cancelAll: %v", err)
	}
	frames := m.requestsFor(opend.ProtoTrdModifyOrder)
	if len(frames) != 1 {
		t.Fatalf("got %d cancel requests, want 1 (only order 1 matches symbol+working)", len(frames))
	}
	var got trdmodifyorder.Request
	if err := proto.Unmarshal(frames[0].Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.GetC2S().GetOrderID() != 1 {
		t.Errorf("cancelled orderID = %d, want 1", got.GetC2S().GetOrderID())
	}
}

func TestTrdClient_CancelAll_Paper_IteratesEvenWithoutSymbol(t *testing.T) {
	m := newMockTrdOpenD(t)
	orders := []*trdcommon.Order{
		orderFixture(10, "AAPL", trdcommon.OrderStatus_OrderStatus_Submitted),
		orderFixture(11, "MSFT", trdcommon.OrderStatus_OrderStatus_Submitting),
		orderFixture(12, "TSLA", trdcommon.OrderStatus_OrderStatus_Cancelled_All), // terminal -> skip
	}
	m.setRespond(opend.ProtoTrdGetOrderList, func(opend.Frame) proto.Message {
		return &trdgetorderlist.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
			S2C:     &trdgetorderlist.S2C{Header: trdHeader(testAccID, "paper"), OrderList: orders},
		}
	})
	m.setRespond(opend.ProtoTrdModifyOrder, succeedModify())
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	if err := tc.cancelAll(context.Background(), ""); err != nil {
		t.Fatalf("cancelAll: %v", err)
	}
	frames := m.requestsFor(opend.ProtoTrdModifyOrder)
	if len(frames) != 2 {
		t.Fatalf("got %d cancel requests, want 2 (paper env must iterate, never forAll)", len(frames))
	}
}

// TestTrdClient_CancelAll_AttemptsAmbiguousStatus verifies an order whose
// statusDomain is ambiguous (ok=false: TimeOut) is still attempted for
// cancel, per the brief's conservative-by-default rule.
func TestTrdClient_CancelAll_AttemptsAmbiguousStatus(t *testing.T) {
	m := newMockTrdOpenD(t)
	orders := []*trdcommon.Order{
		orderFixture(20, "AAPL", trdcommon.OrderStatus_OrderStatus_TimeOut),
	}
	m.setRespond(opend.ProtoTrdGetOrderList, func(opend.Frame) proto.Message {
		return &trdgetorderlist.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
			S2C:     &trdgetorderlist.S2C{Header: trdHeader(testAccID, "paper"), OrderList: orders},
		}
	})
	m.setRespond(opend.ProtoTrdModifyOrder, succeedModify())
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	if err := tc.cancelAll(context.Background(), ""); err != nil {
		t.Fatalf("cancelAll: %v", err)
	}
	if frames := m.requestsFor(opend.ProtoTrdModifyOrder); len(frames) != 1 {
		t.Fatalf("got %d cancel requests, want 1 (ambiguous status must still be attempted)", len(frames))
	}
}

func accListResp(accs ...*trdcommon.TrdAcc) proto.Message {
	return &trdgetacclist.Response{
		RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
		S2C:     &trdgetacclist.S2C{AccList: accs},
	}
}

func validTrdAcc(accID uint64, env trdcommon.TrdEnv) *trdcommon.TrdAcc {
	return &trdcommon.TrdAcc{
		TrdEnv:            proto.Int32(int32(env)),
		AccID:             proto.Uint64(accID),
		TrdMarketAuthList: []int32{int32(trdcommon.TrdMarket_TrdMarket_US)},
		AccStatus:         proto.Int32(int32(trdcommon.TrdAccStatus_TrdAccStatus_Active)),
		AccRole:           proto.Int32(int32(trdcommon.TrdAccRole_TrdAccRole_Normal)),
	}
}

func TestTrdClient_GetAccList_Success(t *testing.T) {
	m := newMockTrdOpenD(t)
	acc := validTrdAcc(testAccID, trdcommon.TrdEnv_TrdEnv_Simulate)
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(acc) })
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	got, err := tc.getAccList(context.Background())
	if err != nil {
		t.Fatalf("getAccList: %v", err)
	}
	if got.GetAccID() != testAccID {
		t.Fatalf("accID = %d, want %d", got.GetAccID(), testAccID)
	}

	// Also verify the required-but-deprecated UserID field was actually sent
	// as 0, not omitted (a nil pointer on a required proto2 field can fail to
	// marshal -- this is the exact gotcha the brief calls out).
	frames := m.requestsFor(opend.ProtoTrdGetAccList)
	var req trdgetacclist.Request
	if err := proto.Unmarshal(frames[0].Body, &req); err != nil {
		t.Fatal(err)
	}
	if req.GetC2S().UserID == nil {
		t.Error("userID must be explicitly set (proto2 required field), got nil")
	}
}

func TestTrdClient_GetAccList_MasterRejected(t *testing.T) {
	m := newMockTrdOpenD(t)
	acc := validTrdAcc(testAccID, trdcommon.TrdEnv_TrdEnv_Simulate)
	acc.AccRole = proto.Int32(int32(trdcommon.TrdAccRole_TrdAccRole_Master))
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(acc) })
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	if _, err := tc.getAccList(context.Background()); err == nil {
		t.Fatal("expected error for a MASTER account")
	}
}

func TestTrdClient_GetAccList_DisabledRejected(t *testing.T) {
	m := newMockTrdOpenD(t)
	acc := validTrdAcc(testAccID, trdcommon.TrdEnv_TrdEnv_Simulate)
	acc.AccStatus = proto.Int32(int32(trdcommon.TrdAccStatus_TrdAccStatus_Disabled))
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(acc) })
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	if _, err := tc.getAccList(context.Background()); err == nil {
		t.Fatal("expected error for a disabled account")
	}
}

func TestTrdClient_GetAccList_WrongMarketRejected(t *testing.T) {
	m := newMockTrdOpenD(t)
	acc := validTrdAcc(testAccID, trdcommon.TrdEnv_TrdEnv_Simulate)
	acc.TrdMarketAuthList = []int32{int32(trdcommon.TrdMarket_TrdMarket_HK)}
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(acc) })
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	if _, err := tc.getAccList(context.Background()); err == nil {
		t.Fatal("expected error for an account not authorized for US")
	}
}

func TestTrdClient_GetAccList_NotFound(t *testing.T) {
	m := newMockTrdOpenD(t)
	other := validTrdAcc(testAccID+1, trdcommon.TrdEnv_TrdEnv_Simulate)
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(other) })
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	if _, err := tc.getAccList(context.Background()); err == nil {
		t.Fatal("expected error when accID is absent from the list")
	}
}

func TestTrdClient_GetAccList_WrongEnvRejected(t *testing.T) {
	m := newMockTrdOpenD(t)
	acc := validTrdAcc(testAccID, trdcommon.TrdEnv_TrdEnv_Real) // account is LIVE
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(acc) })
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{}) // but configured as paper

	if _, err := tc.getAccList(context.Background()); err == nil {
		t.Fatal("expected error when account trdEnv does not match configured env")
	}
}

func TestTrdClient_OrderByRemark(t *testing.T) {
	m := newMockTrdOpenD(t)
	orders := []*trdcommon.Order{
		orderFixture(1, "AAPL", trdcommon.OrderStatus_OrderStatus_Submitted),
		orderFixture(2, "MSFT", trdcommon.OrderStatus_OrderStatus_Submitted),
	}
	m.setRespond(opend.ProtoTrdGetOrderList, func(opend.Frame) proto.Message {
		return &trdgetorderlist.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
			S2C:     &trdgetorderlist.S2C{Header: trdHeader(testAccID, "paper"), OrderList: orders},
		}
	})
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	got, ok, err := tc.orderByRemark(context.Background(), "remark-MSFT", true)
	if err != nil || !ok {
		t.Fatalf("orderByRemark = %v,%v,%v want found", got, ok, err)
	}
	if got.GetOrderID() != 2 {
		t.Fatalf("orderID = %d, want 2", got.GetOrderID())
	}

	_, ok, err = tc.orderByRemark(context.Background(), "no-such-remark", true)
	if err != nil || ok {
		t.Fatalf("orderByRemark(missing) = ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

func TestTrdClient_SubAccPush_Success(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdSubAccPush, func(opend.Frame) proto.Message {
		return &trdsubaccpush.Response{RetType: proto.Int32(int32(common.RetType_RetType_Succeed))}
	})
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	if err := tc.subAccPush(context.Background(), []uint64{testAccID}); err != nil {
		t.Fatalf("subAccPush: %v", err)
	}
	frames := m.requestsFor(opend.ProtoTrdSubAccPush)
	var got trdsubaccpush.Request
	if err := proto.Unmarshal(frames[0].Body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.GetC2S().GetAccIDList()) != 1 || got.GetC2S().GetAccIDList()[0] != testAccID {
		t.Errorf("accIDList = %v, want [%d]", got.GetC2S().GetAccIDList(), testAccID)
	}
}

func TestTrdClient_Snapshot_Composition(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdGetFunds, func(opend.Frame) proto.Message {
		return &trdgetfunds.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
			S2C: &trdgetfunds.S2C{Header: trdHeader(testAccID, "paper"), Funds: &trdcommon.Funds{
				Power: proto.Float64(50000), TotalAssets: proto.Float64(100000),
				Cash: proto.Float64(40000), MarketVal: proto.Float64(60000),
				FrozenCash: proto.Float64(0), DebtCash: proto.Float64(0),
				AvlWithdrawalCash: proto.Float64(40000), RealizedPL: proto.Float64(1234.5),
			}},
		}
	})
	positions := []*trdcommon.Position{
		{
			PositionID: proto.Uint64(1), PositionSide: proto.Int32(int32(trdcommon.PositionSide_PositionSide_Long)),
			Code: proto.String("AAPL"), Name: proto.String("Apple"), Qty: proto.Float64(10),
			CanSellQty: proto.Float64(10), Price: proto.Float64(150), Val: proto.Float64(1500), PlVal: proto.Float64(0),
			AverageCostPrice: proto.Float64(140),
		},
		{
			PositionID: proto.Uint64(2), PositionSide: proto.Int32(int32(trdcommon.PositionSide_PositionSide_Short)),
			Code: proto.String("TSLA"), Name: proto.String("Tesla"), Qty: proto.Float64(5),
			CanSellQty: proto.Float64(5), Price: proto.Float64(200), Val: proto.Float64(1000), PlVal: proto.Float64(0),
			AverageCostPrice: proto.Float64(210),
		},
	}
	m.setRespond(opend.ProtoTrdGetPositionList, func(opend.Frame) proto.Message {
		return &trdgetpositionlist.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
			S2C:     &trdgetpositionlist.S2C{Header: trdHeader(testAccID, "paper"), PositionList: positions},
		}
	})
	orders := []*trdcommon.Order{orderFixture(1, "MSFT", trdcommon.OrderStatus_OrderStatus_Submitted)}
	m.setRespond(opend.ProtoTrdGetOrderList, func(opend.Frame) proto.Message {
		return &trdgetorderlist.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
			S2C:     &trdgetorderlist.S2C{Header: trdHeader(testAccID, "paper"), OrderList: orders},
		}
	})

	fake := clock.NewFake(time.UnixMilli(1_752_000_000_000))
	tc := newTestTrdClient(t, m, testAccID, "paper", fake)

	acct, pos, ords, err := tc.snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if acct.Equity != 100000 || acct.BuyingPower != 50000 || acct.AvailableCash != 40000 || acct.Realized != 1234.5 {
		t.Errorf("account snapshot = %+v, unexpected values", acct)
	}
	if acct.TsMs != fake.Now().UnixMilli() {
		t.Errorf("TsMs = %d, want %d (injected clock)", acct.TsMs, fake.Now().UnixMilli())
	}

	if len(pos) != 2 {
		t.Fatalf("got %d positions, want 2", len(pos))
	}
	if pos[0].Symbol != "US.AAPL" || pos[0].Qty != 10 || pos[0].AvgPrice != 140 {
		t.Errorf("long position = %+v, want US.AAPL qty=10 avgPrice=140", pos[0])
	}
	if pos[1].Symbol != "US.TSLA" || pos[1].Qty != -5 || pos[1].AvgPrice != 210 {
		t.Errorf("short position = %+v, want US.TSLA qty=-5 avgPrice=210 (sign flipped)", pos[1])
	}

	if len(ords) != 1 {
		t.Fatalf("got %d orders, want 1", len(ords))
	}
	if ords[0].ID != "remark-MSFT" {
		t.Errorf("order.ID = %q, want remark-MSFT (Remark, not OrderID)", ords[0].ID)
	}
	if ords[0].Symbol != "US.MSFT" {
		t.Errorf("order.Symbol = %q, want US.MSFT", ords[0].Symbol)
	}
	if ords[0].Status != exec.StatusAccepted {
		t.Errorf("order.Status = %v, want StatusAccepted (moomoo Submitted)", ords[0].Status)
	}
}

// TestTrdClient_Snapshot_AmbiguousOrderStatus_FallsBackToSubmitted verifies
// the documented ok=false fallback: an order whose statusDomain cannot be
// trusted (TimeOut) still decodes to a valid domain Order, using
// StatusSubmitted rather than asserting a status this package can't back up.
func TestTrdClient_Snapshot_AmbiguousOrderStatus_FallsBackToSubmitted(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdGetFunds, func(opend.Frame) proto.Message {
		return &trdgetfunds.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
			S2C: &trdgetfunds.S2C{Header: trdHeader(testAccID, "paper"), Funds: &trdcommon.Funds{
				Power: proto.Float64(0), TotalAssets: proto.Float64(0), Cash: proto.Float64(0),
				MarketVal: proto.Float64(0), FrozenCash: proto.Float64(0), DebtCash: proto.Float64(0),
				AvlWithdrawalCash: proto.Float64(0),
			}},
		}
	})
	m.setRespond(opend.ProtoTrdGetPositionList, func(opend.Frame) proto.Message {
		return &trdgetpositionlist.Response{RetType: proto.Int32(int32(common.RetType_RetType_Succeed)), S2C: &trdgetpositionlist.S2C{Header: trdHeader(testAccID, "paper")}}
	})
	orders := []*trdcommon.Order{orderFixture(1, "MSFT", trdcommon.OrderStatus_OrderStatus_TimeOut)}
	m.setRespond(opend.ProtoTrdGetOrderList, func(opend.Frame) proto.Message {
		return &trdgetorderlist.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
			S2C:     &trdgetorderlist.S2C{Header: trdHeader(testAccID, "paper"), OrderList: orders},
		}
	})
	tc := newTestTrdClient(t, m, testAccID, "paper", clock.System{})

	_, _, ords, err := tc.snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(ords) != 1 || ords[0].Status != exec.StatusSubmitted {
		t.Fatalf("orders = %+v, want one order with StatusSubmitted fallback", ords)
	}
}

// TestTrdClient_RateLimiter_BlocksBurst drains placeBucket's burst (3 tokens
// at 15/30s = 0.5/s) with three placeOrder calls, then verifies a fourth
// blocks until the injected fake clock is advanced enough to refill one
// token -- never a real 30s sleep.
func TestTrdClient_RateLimiter_BlocksBurst(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdPlaceOrder, succeedPlaceOrder(1))
	fake := clock.NewFake(time.UnixMilli(0))
	tc := newTestTrdClient(t, m, testAccID, "paper", fake)

	req := exec.OrderRequest{Symbol: "US.AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 1}
	for i := 0; i < 3; i++ {
		if _, err := tc.placeOrder(context.Background(), req, "burst"); err != nil {
			t.Fatalf("placeOrder #%d: %v", i, err)
		}
	}

	done := make(chan error, 1)
	go func() {
		_, err := tc.placeOrder(context.Background(), req, "burst-4th")
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("4th placeOrder returned early (err=%v); bucket should have been empty", err)
	case <-time.After(100 * time.Millisecond):
		// expected: still blocked
	}

	// Refill needs 2s at 0.5 tokens/sec for one token. Poll-advance the fake
	// clock in small steps (real sleeps give the blocked goroutine a chance
	// to register its waker), mirroring netx/ratelimit_test.go's pattern.
	const (
		maxIterations = 500
		stepAdvance   = 10 * time.Millisecond
	)
	for i := 0; i < maxIterations; i++ {
		time.Sleep(time.Millisecond)
		fake.Advance(stepAdvance)
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("4th placeOrder = %v, want nil after refill", err)
			}
			return
		default:
		}
	}
	t.Fatal("4th placeOrder did not unblock after simulated refill window")
}
