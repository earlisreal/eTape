package moomoo

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/common"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdgetfunds"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdgetorderlist"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdgetpositionlist"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdplaceorder"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdsubaccpush"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorder"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdupdateorderfill"
)

// ---- test harness -----------------------------------------------------------

// buildAdapter constructs an Adapter directly (mirroring New) but with
// caller-supplied opend.Options so command tests can shorten RequestTimeout and
// reconnect tests can shorten the redial backoff. testVenue is defined in
// normalize_test.go.
func buildAdapter(m *mockTrdOpenD, accID uint64, env string, clk clock.Clock, opts opend.Options) *Adapter {
	opts.Addr = m.addr()
	opts.ClientID = "etape-trade"
	if opts.Clock == nil {
		opts.Clock = clock.System{}
	}
	client := opend.New(opts)
	return &Adapter{
		venue:           testVenue,
		clk:             clk,
		client:          client,
		tc:              newTrdClient(client, accID, env, clk),
		push:            newPushDecoder(),
		events:          make(chan exec.BrokerEvent, 256),
		orderIDByDomain: map[string]uint64{},
	}
}

func waitConnected(t *testing.T, c *opend.Client) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.ConnID() != 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("client did not connect within 2s")
}

// dialAdapter starts only the trade opend.Client (NOT the Adapter.Run select
// loop) and waits for connect -- for command-method tests (Submit/Replace/
// Cancel/Snapshot) that don't need push processing.
func dialAdapter(t *testing.T, m *mockTrdOpenD, env string) *Adapter {
	t.Helper()
	a := buildAdapter(m, testAccID, env, clock.System{}, opend.Options{RequestTimeout: 500 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = a.client.Run(ctx) }()
	waitConnected(t, a.client)
	return a
}

// runAdapter starts the full Adapter.Run select loop (which itself starts the
// client) -- for push/reconcile tests. Fast reconnect backoff keeps the
// reconnect test quick.
func runAdapter(t *testing.T, m *mockTrdOpenD, env string) *Adapter {
	t.Helper()
	a := buildAdapter(m, testAccID, env, clock.System{}, opend.Options{
		RequestTimeout: 2 * time.Second, ReconnectMin: 10 * time.Millisecond, ReconnectMax: 50 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go a.Run(ctx)
	return a
}

func nextEvent(t *testing.T, a *Adapter, timeout time.Duration) exec.BrokerEvent {
	t.Helper()
	select {
	case e := <-a.events:
		return e
	case <-time.After(timeout):
		t.Fatal("timed out waiting for broker event")
		return nil
	}
}

func assertNoEvent(t *testing.T, a *Adapter, within time.Duration) {
	t.Helper()
	select {
	case e := <-a.events:
		t.Fatalf("unexpected event: %T %+v", e, e)
	case <-time.After(within):
	}
}

// collectUntil drains events until one satisfies want (returning the full
// collected slice and the matching event) or the timeout fires.
func collectUntil(t *testing.T, a *Adapter, want func(exec.BrokerEvent) bool, timeout time.Duration) ([]exec.BrokerEvent, exec.BrokerEvent) {
	t.Helper()
	deadline := time.After(timeout)
	var all []exec.BrokerEvent
	for {
		select {
		case e := <-a.events:
			all = append(all, e)
			if want(e) {
				return all, e
			}
		case <-deadline:
			t.Fatalf("timed out; collected %d events: %+v", len(all), all)
			return all, nil
		}
	}
}

func containsStreamGap(evs []exec.BrokerEvent) bool {
	for _, e := range evs {
		if _, ok := e.(exec.StreamGap); ok {
			return true
		}
	}
	return false
}

// mutableOrders is a mutex-guarded order blotter the getOrderList responder
// reads, so a test can change the snapshot between (re)connects.
type mutableOrders struct {
	mu     sync.Mutex
	orders []*trdcommon.Order
}

func (mo *mutableOrders) set(os []*trdcommon.Order) { mo.mu.Lock(); mo.orders = os; mo.mu.Unlock() }
func (mo *mutableOrders) get() []*trdcommon.Order {
	mo.mu.Lock()
	defer mo.mu.Unlock()
	return mo.orders
}

// reconcileOrderFixture builds a *trdcommon.Order carrying the fill/timestamp
// fields the reconcile path reads (unlike orderFixture in trd_test.go, which
// omits them).
func reconcileOrderFixture(orderID uint64, remark, code string, status trdcommon.OrderStatus, qty, fillQty, fillAvg float64) *trdcommon.Order {
	return &trdcommon.Order{
		TrdSide:         proto.Int32(int32(trdcommon.TrdSide_TrdSide_Buy)),
		OrderType:       proto.Int32(int32(trdcommon.OrderType_OrderType_Normal)),
		OrderStatus:     proto.Int32(int32(status)),
		OrderID:         proto.Uint64(orderID),
		OrderIDEx:       proto.String("ex"),
		Code:            proto.String(code),
		Name:            proto.String(code),
		Qty:             proto.Float64(qty),
		FillQty:         proto.Float64(fillQty),
		FillAvgPrice:    proto.Float64(fillAvg),
		CreateTime:      proto.String("2026-07-11 09:30:00"),
		UpdateTime:      proto.String("2026-07-11 09:30:00"),
		UpdateTimestamp: proto.Float64(1_752_000_000), // non-zero epoch seconds
		Remark:          proto.String(remark),
	}
}

// installStdResponders wires the account/subscribe/funds/positions handlers a
// reconcile needs, plus a getOrderList handler backed by mo (positions empty).
func installStdResponders(m *mockTrdOpenD, env string, mo *mutableOrders) {
	accEnv := trdcommon.TrdEnv_TrdEnv_Simulate
	if env == "live" {
		accEnv = trdcommon.TrdEnv_TrdEnv_Real
	}
	acc := validTrdAcc(testAccID, accEnv)
	m.setRespond(opend.ProtoTrdGetAccList, func(opend.Frame) proto.Message { return accListResp(acc) })
	m.setRespond(opend.ProtoTrdSubAccPush, func(opend.Frame) proto.Message {
		return &trdsubaccpush.Response{RetType: proto.Int32(int32(common.RetType_RetType_Succeed))}
	})
	m.setRespond(opend.ProtoTrdGetFunds, func(opend.Frame) proto.Message {
		return &trdgetfunds.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
			S2C: &trdgetfunds.S2C{Header: trdHeader(testAccID, env), Funds: &trdcommon.Funds{
				Power: proto.Float64(50000), TotalAssets: proto.Float64(100000), Cash: proto.Float64(40000),
				MarketVal: proto.Float64(60000), FrozenCash: proto.Float64(0), DebtCash: proto.Float64(0),
				AvlWithdrawalCash: proto.Float64(40000), RealizedPL: proto.Float64(0),
			}},
		}
	})
	m.setRespond(opend.ProtoTrdGetPositionList, func(opend.Frame) proto.Message {
		return &trdgetpositionlist.Response{RetType: proto.Int32(int32(common.RetType_RetType_Succeed)), S2C: &trdgetpositionlist.S2C{Header: trdHeader(testAccID, env)}}
	})
	m.setRespond(opend.ProtoTrdGetOrderList, func(opend.Frame) proto.Message {
		return &trdgetorderlist.Response{RetType: proto.Int32(int32(common.RetType_RetType_Succeed)), S2C: &trdgetorderlist.S2C{Header: trdHeader(testAccID, env), OrderList: mo.get()}}
	})
}

// ---- Capabilities / unsupported ---------------------------------------------

func TestAdapter_Capabilities(t *testing.T) {
	a, err := New(Config{Venue: testVenue, Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	c := a.Capabilities()
	if !c.NativeReplace || c.FlattenAll || !c.OvernightSession {
		t.Fatalf("capabilities = %+v, want NativeReplace=true FlattenAll=false OvernightSession=true", c)
	}
	if err := a.Flatten(context.Background()); err == nil {
		t.Error("Flatten should return unsupported error")
	}
	if err := a.ResetBalance(context.Background(), 1000); err == nil {
		t.Error("ResetBalance should return unsupported error")
	}
}

func TestNew_MissingVenue(t *testing.T) {
	if _, err := New(Config{Addr: "127.0.0.1:0"}); err == nil {
		t.Fatal("New should reject a config with no venue")
	}
}

// ---- SubmitOrder ------------------------------------------------------------

func TestAdapter_SubmitOrder_Success(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdPlaceOrder, succeedPlaceOrder(555))
	a := dialAdapter(t, m, "paper")

	req := exec.OrderRequest{
		Venue: testVenue, Symbol: "US.AAPL", Side: exec.SideBuy, Type: exec.TypeLimit,
		TIF: exec.TIFDay, Qty: 10, LimitPrice: 123.45, ClientOrderID: "clientOrder-1",
	}
	ack, err := a.SubmitOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitOrder: %v", err)
	}
	if !ack.Accepted || ack.OrderID != "clientOrder-1" {
		t.Fatalf("ack = %+v, want accepted clientOrder-1", ack)
	}

	a.mu.Lock()
	gotID := a.orderIDByDomain["clientOrder-1"]
	a.mu.Unlock()
	if gotID != 555 {
		t.Fatalf("orderIDByDomain = %d, want 555", gotID)
	}

	// learnOrder must have seeded the pushDecoder correlation state.
	a.push.mu.Lock()
	gotOID := a.push.domainOIDByOrderID[555]
	gotQty := a.push.totalQtyByOrderID[555]
	a.push.mu.Unlock()
	if gotOID != "clientOrder-1" || gotQty != 10 {
		t.Fatalf("pushDecoder seed = (%q,%v), want (clientOrder-1,10)", gotOID, gotQty)
	}

	assertNoEvent(t, a, 50*time.Millisecond) // success emits nothing synchronously
}

func TestAdapter_SubmitOrder_AmbiguityProbeLandsAnyway(t *testing.T) {
	m := newMockTrdOpenD(t)
	// No PlaceOrder responder -> placeOrder times out (transport ambiguity).
	// But the order actually landed, so the probe (getOrderList) finds it.
	orders := []*trdcommon.Order{reconcileOrderFixture(777, "amb-oid", "AAPL", trdcommon.OrderStatus_OrderStatus_Submitted, 10, 0, 0)}
	m.setRespond(opend.ProtoTrdGetOrderList, func(opend.Frame) proto.Message {
		return &trdgetorderlist.Response{RetType: proto.Int32(int32(common.RetType_RetType_Succeed)), S2C: &trdgetorderlist.S2C{Header: trdHeader(testAccID, "paper"), OrderList: orders}}
	})
	a := dialAdapter(t, m, "paper")

	req := exec.OrderRequest{Venue: testVenue, Symbol: "US.AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 10, ClientOrderID: "amb-oid"}
	ack, err := a.SubmitOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitOrder (ambiguity-lands): %v", err)
	}
	if !ack.Accepted {
		t.Fatalf("ack = %+v, want accepted (order landed despite transport error)", ack)
	}
	a.mu.Lock()
	gotID := a.orderIDByDomain["amb-oid"]
	a.mu.Unlock()
	if gotID != 777 {
		t.Fatalf("orderIDByDomain = %d, want 777 (from the probe result)", gotID)
	}
	assertNoEvent(t, a, 50*time.Millisecond) // treated as accepted -> no OrderRejected
}

func TestAdapter_SubmitOrder_ConfirmedReject(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdPlaceOrder, func(opend.Frame) proto.Message {
		return &trdplaceorder.Response{
			RetType: proto.Int32(int32(common.RetType_RetType_Failed)),
			RetMsg:  proto.String("insufficient buying power"),
		}
	})
	// Probe returns an empty blotter -> not found -> confirmed reject.
	m.setRespond(opend.ProtoTrdGetOrderList, func(opend.Frame) proto.Message {
		return &trdgetorderlist.Response{RetType: proto.Int32(int32(common.RetType_RetType_Succeed)), S2C: &trdgetorderlist.S2C{Header: trdHeader(testAccID, "paper")}}
	})
	a := dialAdapter(t, m, "paper")

	req := exec.OrderRequest{Venue: testVenue, Symbol: "US.AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 10, ClientOrderID: "rej-oid"}
	ack, err := a.SubmitOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitOrder (reject) returned transport error, want nil with Accepted=false: %v", err)
	}
	if ack.Accepted {
		t.Fatalf("ack = %+v, want Accepted=false", ack)
	}
	e := nextEvent(t, a, time.Second)
	rej, ok := e.(exec.OrderRejected)
	if !ok || rej.OID != "rej-oid" || rej.V != testVenue {
		t.Fatalf("event = %T %+v, want OrderRejected for rej-oid", e, e)
	}
}

// ---- ReplaceOrder -----------------------------------------------------------

func TestAdapter_ReplaceOrder_QtyZeroResolvesCurrentQty(t *testing.T) {
	m := newMockTrdOpenD(t)
	// Current resting order carries Qty=100; a price-only replace (Qty unset)
	// must emit NewQty=100, NOT 0 (which state.go would fold into o.Qty=0).
	orders := []*trdcommon.Order{reconcileOrderFixture(42, "rep-oid", "AAPL", trdcommon.OrderStatus_OrderStatus_Submitted, 100, 0, 0)}
	m.setRespond(opend.ProtoTrdGetOrderList, func(opend.Frame) proto.Message {
		return &trdgetorderlist.Response{RetType: proto.Int32(int32(common.RetType_RetType_Succeed)), S2C: &trdgetorderlist.S2C{Header: trdHeader(testAccID, "paper"), OrderList: orders}}
	})
	m.setRespond(opend.ProtoTrdModifyOrder, succeedModify())
	a := dialAdapter(t, m, "paper")
	a.mu.Lock()
	a.orderIDByDomain["rep-oid"] = 42 // pre-seed so resolveOrderID hits the map
	a.mu.Unlock()

	if err := a.ReplaceOrder(context.Background(), "rep-oid", exec.ReplaceRequest{LimitPrice: 125.00}); err != nil {
		t.Fatalf("ReplaceOrder: %v", err)
	}
	e := nextEvent(t, a, time.Second)
	rep, ok := e.(exec.OrderReplaced)
	if !ok {
		t.Fatalf("event = %T, want OrderReplaced", e)
	}
	if rep.NewQty != 100 {
		t.Fatalf("NewQty = %v, want 100 (resolved current qty, not the unspecified 0)", rep.NewQty)
	}
	if rep.NewLimit != 125.00 {
		t.Fatalf("NewLimit = %v, want 125.00", rep.NewLimit)
	}
	if rep.NewStop != 0 {
		t.Fatalf("NewStop = %v, want 0 (unspecified)", rep.NewStop)
	}
}

func TestAdapter_ReplaceOrder_QtyGiven_NoQtyResolution(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdModifyOrder, succeedModify())
	a := dialAdapter(t, m, "paper")
	a.mu.Lock()
	a.orderIDByDomain["rep2"] = 7
	a.mu.Unlock()

	if err := a.ReplaceOrder(context.Background(), "rep2", exec.ReplaceRequest{Qty: 50, LimitPrice: 10}); err != nil {
		t.Fatalf("ReplaceOrder: %v", err)
	}
	// A specified qty must NOT trigger the current-qty getOrderList probe.
	if frames := m.requestsFor(opend.ProtoTrdGetOrderList); len(frames) != 0 {
		t.Fatalf("got %d getOrderList calls, want 0 (qty was specified)", len(frames))
	}
	e := nextEvent(t, a, time.Second)
	rep, ok := e.(exec.OrderReplaced)
	if !ok || rep.NewQty != 50 || rep.NewLimit != 10 {
		t.Fatalf("event = %T %+v, want OrderReplaced NewQty=50 NewLimit=10", e, e)
	}
}

// ---- Cancel -----------------------------------------------------------------

func TestAdapter_CancelOrder_Delegates(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdModifyOrder, succeedModify())
	a := dialAdapter(t, m, "paper")
	a.mu.Lock()
	a.orderIDByDomain["c-oid"] = 99
	a.mu.Unlock()

	if err := a.CancelOrder(context.Background(), "c-oid"); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	frames := m.requestsFor(opend.ProtoTrdModifyOrder)
	if len(frames) != 1 {
		t.Fatalf("got %d modify frames, want 1", len(frames))
	}
}

func TestAdapter_CancelAll_Delegates(t *testing.T) {
	m := newMockTrdOpenD(t)
	m.setRespond(opend.ProtoTrdModifyOrder, succeedModify())
	a := dialAdapter(t, m, "live")

	if err := a.CancelAll(context.Background(), ""); err != nil {
		t.Fatalf("CancelAll: %v", err)
	}
	// live + no symbol -> one forAll modify (trd.go owns the branching).
	if frames := m.requestsFor(opend.ProtoTrdModifyOrder); len(frames) != 1 {
		t.Fatalf("got %d modify frames, want 1 forAll call", len(frames))
	}
}

// ---- Snapshot ---------------------------------------------------------------

func TestAdapter_Snapshot_StampsVenue(t *testing.T) {
	m := newMockTrdOpenD(t)
	mo := &mutableOrders{}
	mo.set([]*trdcommon.Order{reconcileOrderFixture(1, "snap-oid", "MSFT", trdcommon.OrderStatus_OrderStatus_Submitted, 10, 0, 0)})
	installStdResponders(m, "paper", mo)
	// One position, to prove positions get stamped too.
	m.setRespond(opend.ProtoTrdGetPositionList, func(opend.Frame) proto.Message {
		return &trdgetpositionlist.Response{RetType: proto.Int32(int32(common.RetType_RetType_Succeed)), S2C: &trdgetpositionlist.S2C{
			Header: trdHeader(testAccID, "paper"),
			PositionList: []*trdcommon.Position{{
				PositionID: proto.Uint64(1), PositionSide: proto.Int32(int32(trdcommon.PositionSide_PositionSide_Long)),
				Code: proto.String("AAPL"), Name: proto.String("Apple"), Qty: proto.Float64(10),
				CanSellQty: proto.Float64(10), Price: proto.Float64(150), Val: proto.Float64(1500),
				PlVal: proto.Float64(0), AverageCostPrice: proto.Float64(140),
			}},
		}}
	})
	a := dialAdapter(t, m, "paper")

	acct, positions, orders, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if acct.Venue != testVenue {
		t.Errorf("acct.Venue = %q, want %q", acct.Venue, testVenue)
	}
	if len(positions) != 1 || positions[0].Venue != testVenue {
		t.Errorf("positions venue not stamped: %+v", positions)
	}
	if len(orders) != 1 || orders[0].Venue != testVenue {
		t.Errorf("orders venue not stamped: %+v", orders)
	}
}

// ---- Run / reconcile --------------------------------------------------------

func TestAdapter_Run_FirstConnect_NoStreamGap(t *testing.T) {
	m := newMockTrdOpenD(t)
	mo := &mutableOrders{}
	mo.set([]*trdcommon.Order{reconcileOrderFixture(101, "first-oid", "AAPL", trdcommon.OrderStatus_OrderStatus_Submitted, 100, 0, 0)})
	installStdResponders(m, "paper", mo)
	a := runAdapter(t, m, "paper")

	// Deterministic first-connect order: ConnUp, Account, Positions, then the
	// working order's OrderAccepted.
	if _, ok := nextEvent(t, a, 2*time.Second).(exec.BrokerConnUp); !ok {
		t.Fatal("want BrokerConnUp first")
	}
	if _, ok := nextEvent(t, a, time.Second).(exec.BrokerAccount); !ok {
		t.Fatal("want BrokerAccount")
	}
	if _, ok := nextEvent(t, a, time.Second).(exec.BrokerPositions); !ok {
		t.Fatal("want BrokerPositions")
	}
	acc, ok := nextEvent(t, a, time.Second).(exec.OrderAccepted)
	if !ok || acc.OID != "first-oid" {
		t.Fatalf("want OrderAccepted for first-oid, got %+v", acc)
	}
	// No StreamGap on the very first connect.
	assertNoEvent(t, a, 150*time.Millisecond)
}

// TestAdapter_Run_Reconnect_StreamGapAndFillCatchUp is the end-to-end
// concurrency/reconcile test: a first connect with a resting order, a forced
// disconnect, a reconnect whose snapshot shows a partial fill that happened
// while disconnected (delta'd into ONE catch-up OrderFilled + a StreamGap), and
// finally a LIVE fill push for the remainder that must accumulate on top of the
// catch-up baseline WITHOUT double-counting.
func TestAdapter_Run_Reconnect_StreamGapAndFillCatchUp(t *testing.T) {
	const (
		oid     = "recon-oid"
		orderID = uint64(620193847)
	)
	m := newMockTrdOpenD(t)
	mo := &mutableOrders{}
	// First connect: resting, unfilled, qty 100.
	mo.set([]*trdcommon.Order{reconcileOrderFixture(orderID, oid, "AAPL", trdcommon.OrderStatus_OrderStatus_Submitted, 100, 0, 0)})
	installStdResponders(m, "paper", mo)
	a := runAdapter(t, m, "paper")

	// Drain the first-connect events up to the working order's Accept.
	first, _ := collectUntil(t, a, func(e exec.BrokerEvent) bool {
		acc, ok := e.(exec.OrderAccepted)
		return ok && acc.OID == oid
	}, 2*time.Second)
	if containsStreamGap(first) {
		t.Fatalf("first connect must not emit StreamGap: %+v", first)
	}
	assertNoEvent(t, a, 100*time.Millisecond)

	// While "disconnected", 40 of 100 fill at avg 150.10.
	mo.set([]*trdcommon.Order{reconcileOrderFixture(orderID, oid, "AAPL", trdcommon.OrderStatus_OrderStatus_Filled_Part, 100, 40, 150.10)})
	m.closeConns()

	// Second connect: expect a StreamGap, and somewhere before it the catch-up
	// OrderFilled for the missed 40.
	second, _ := collectUntil(t, a, func(e exec.BrokerEvent) bool {
		_, ok := e.(exec.StreamGap)
		return ok
	}, 3*time.Second)

	var catchUp *exec.OrderFilled
	for _, e := range second {
		if f, ok := e.(exec.OrderFilled); ok {
			ff := f
			catchUp = &ff
			break
		}
	}
	if catchUp == nil {
		t.Fatalf("reconnect missing catch-up OrderFilled: %+v", second)
	}
	if catchUp.F.Qty != 40 || catchUp.CumQty != 40 || catchUp.LeavesQty != 60 {
		t.Fatalf("catch-up fill = qty%v cum%v leaves%v, want 40/40/60", catchUp.F.Qty, catchUp.CumQty, catchUp.LeavesQty)
	}
	if math.Abs(catchUp.AvgPrice-150.10) > 1e-9 {
		t.Fatalf("catch-up AvgPrice = %v, want 150.10", catchUp.AvgPrice)
	}
	if catchUp.F.TsMs == 0 {
		t.Fatal("catch-up fill TsMs should be the order's real UpdateTimestamp, not 0")
	}

	// LIVE fill push for the remaining 60 @ 150.40 -- must accumulate on top of
	// the reconcile baseline (cum 40 -> 100), NOT re-count the 40.
	fill := &trdupdateorderfill.Response{
		RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
		S2C: &trdupdateorderfill.S2C{Header: trdHeader(testAccID, "paper"), OrderFill: &trdcommon.OrderFill{
			TrdSide: proto.Int32(int32(trdcommon.TrdSide_TrdSide_Buy)), FillID: proto.Uint64(999001),
			FillIDEx: proto.String("999001"), OrderID: proto.Uint64(orderID), OrderIDEx: proto.String("620193847"),
			Code: proto.String("AAPL"), Name: proto.String("AAPL"), Qty: proto.Float64(60),
			Price: proto.Float64(150.40), CreateTime: proto.String("2026-07-11 09:31:40"),
			SecMarket: proto.Int32(int32(trdcommon.TrdSecMarket_TrdSecMarket_US)), CreateTimestamp: proto.Float64(1_752_000_100),
		}},
	}
	m.push(opend.ProtoTrdUpdateOrderFill, fill)

	_, liveEv := collectUntil(t, a, func(e exec.BrokerEvent) bool {
		_, ok := e.(exec.OrderFilled)
		return ok
	}, 2*time.Second)
	live := liveEv.(exec.OrderFilled)
	if live.F.Qty != 60 {
		t.Fatalf("live fill Qty = %v, want 60", live.F.Qty)
	}
	if live.CumQty != 100 {
		t.Fatalf("live fill CumQty = %v, want 100 (no double-count of the reconcile'd 40)", live.CumQty)
	}
	if live.LeavesQty != 0 {
		t.Fatalf("live fill LeavesQty = %v, want 0", live.LeavesQty)
	}
	wantAvg := (40*150.10 + 60*150.40) / 100.0 // 150.28
	if math.Abs(live.AvgPrice-wantAvg) > 1e-9 {
		t.Fatalf("live fill AvgPrice = %v, want %v", live.AvgPrice, wantAvg)
	}
}

// TestAdapter_UnrecognizedPush_NoPanic pushes an unknown protoID and a
// malformed 2208 body, then a valid 2208 order push -- proving handlePush's
// default/decode-error branches neither panic nor kill the loop.
func TestAdapter_UnrecognizedPush_NoPanic(t *testing.T) {
	m := newMockTrdOpenD(t)
	mo := &mutableOrders{} // empty blotter
	installStdResponders(m, "paper", mo)
	a := runAdapter(t, m, "paper")

	// Settle the first connect (ConnUp, Account, Positions; no orders).
	collectUntil(t, a, func(e exec.BrokerEvent) bool { _, ok := e.(exec.BrokerPositions); return ok }, 2*time.Second)

	m.pushRaw(9999, []byte{0x01, 0x02, 0x03})          // unknown protoID -> default branch
	m.pushRaw(opend.ProtoTrdUpdateOrder, []byte{0x08}) // malformed 2208 body -> decode-error branch

	// A valid order push still processes -> proves the loop survived.
	order := &trdupdateorder.Response{
		RetType: proto.Int32(int32(common.RetType_RetType_Succeed)),
		S2C: &trdupdateorder.S2C{Header: trdHeader(testAccID, "paper"), Order: reconcileOrderFixture(
			888, "survivor", "AAPL", trdcommon.OrderStatus_OrderStatus_Submitted, 5, 0, 0)},
	}
	m.push(opend.ProtoTrdUpdateOrder, order)

	_, e := collectUntil(t, a, func(e exec.BrokerEvent) bool {
		acc, ok := e.(exec.OrderAccepted)
		return ok && acc.OID == "survivor"
	}, 2*time.Second)
	if acc, ok := e.(exec.OrderAccepted); !ok || acc.OID != "survivor" {
		t.Fatalf("want OrderAccepted for survivor after unrecognized pushes, got %+v", e)
	}
}
