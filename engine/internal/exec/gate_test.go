package exec

import "testing"

type fakeMarks map[string]float64

func (m fakeMarks) LastTrade(sym string) (float64, bool) { v, ok := m[sym]; return v, ok }

func baseCfg() GateConfig {
	return GateConfig{
		Global: GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionValue: 100000, MaxSymbolPositionShares: 1000},
		Venue: map[VenueID]VenueLimits{
			"sim-1": {MaxOrderValue: 5000, MaxPositionValue: 20000, MaxPositionShares: 200, MaxOpenOrders: 3},
			"sim-2": {MaxOrderValue: 5000, MaxPositionValue: 20000, MaxPositionShares: 200, MaxOpenOrders: 3},
		},
	}
}

func armedState() *State {
	s := NewState([]VenueID{"sim-1", "sim-2"})
	s.SetMasterArmed(true)
	return s
}

func buyReq(v VenueID, sym string, qty, limit float64, id string) OrderRequest {
	return OrderRequest{Venue: v, Symbol: sym, Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: qty, LimitPrice: limit, ClientOrderID: id}
}

func TestGateMasterArmAndUnknownVenue(t *testing.T) {
	cfg := baseCfg()
	marks := fakeMarks{"AAPL": 100}
	s := NewState([]VenueID{"sim-1"}) // disarmed
	if ok, reason := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 10, 100, "ET1"), marks); ok || reason != "master disarmed" {
		t.Fatalf("disarmed master should block, got ok=%v reason=%q", ok, reason)
	}
	s.SetMasterArmed(true) // master on, but the venue below was never registered
	if ok, reason := Evaluate(s, cfg, buyReq("sim-unregistered", "AAPL", 10, 100, "ET1"), marks); ok || reason != "unknown venue" {
		t.Fatalf("unregistered venue should block, got ok=%v reason=%q", ok, reason)
	}
}

func TestGateDuplicateID(t *testing.T) {
	cfg, marks := baseCfg(), fakeMarks{"AAPL": 100}
	s := armedState()
	s.Apply(submitEv("sim-1", "ETdup", "AAPL", SideBuy, 10, 100, 1000))
	if ok, reason := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 10, 100, "ETdup"), marks); ok {
		t.Fatalf("duplicate ID should block, reason=%q", reason)
	}
}

func TestGateMissingVenueConfigFailsClosed(t *testing.T) {
	cfg := baseCfg()
	delete(cfg.Venue, "sim-1") // simulate a config-wiring gap: venue registered but no gate entry
	marks := fakeMarks{"AAPL": 100}
	s := armedState()
	if ok, reason := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 1, 100, "ET1"), marks); ok || reason != "no gate config for venue" {
		t.Fatalf("missing venue gate config should fail closed, got ok=%v reason=%q", ok, reason)
	}
}

func TestGateMaxOrderValue(t *testing.T) {
	cfg, marks := baseCfg(), fakeMarks{"AAPL": 100}
	s := armedState()
	// 60 * 100 = 6000 > venue cap 5000
	if ok, _ := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 60, 100, "ET1"), marks); ok {
		t.Fatal("order value over cap should block")
	}
	// 40 * 100 = 4000 <= 5000
	if ok, reason := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 40, 100, "ET1"), marks); !ok {
		t.Fatalf("order value under cap should pass, reason=%q", reason)
	}
}

func TestGateMarketOrderValuationUsesMark(t *testing.T) {
	cfg := baseCfg()
	s := armedState()
	req := OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeMarket, Qty: 60, ClientOrderID: "ET1"}
	if ok, reason := Evaluate(s, cfg, req, fakeMarks{"AAPL": 100}); ok { // 60*100=6000 > 5000
		t.Fatalf("market order valued at mark over cap should block, reason=%q", reason)
	}
	if ok, reason := Evaluate(s, cfg, req, fakeMarks{}); ok || reason == "" { // no mark → cannot value → block
		t.Fatalf("market order without a mark should block, ok=%v reason=%q", ok, reason)
	}
}

func TestGateMaxResultingVenuePosition(t *testing.T) {
	cfg, marks := baseCfg(), fakeMarks{"AAPL": 100}
	s := armedState()
	s.ReconcilePositions("sim-1", []Position{{Venue: "sim-1", Symbol: "AAPL", Qty: 150}})
	// 150 held + 10 working (add below) + 50 new = 210 > 200 shares cap
	s.Apply(submitEv("sim-1", "ETw", "AAPL", SideBuy, 10, 100, 1000))
	if ok, _ := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 50, 100, "ET1"), marks); ok {
		t.Fatal("resulting venue position over shares cap should block")
	}
	// 150 + 10 + 30 = 190 <= 200
	if ok, reason := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 30, 100, "ET1"), marks); !ok {
		t.Fatalf("under shares cap should pass, reason=%q", reason)
	}
}

func TestGateMaxOpenOrders(t *testing.T) {
	cfg, marks := baseCfg(), fakeMarks{"AAPL": 100}
	s := armedState()
	s.Apply(submitEv("sim-1", "ET1", "AAPL", SideBuy, 1, 100, 1000))
	s.Apply(submitEv("sim-1", "ET2", "AAPL", SideBuy, 1, 100, 1001))
	s.Apply(submitEv("sim-1", "ET3", "AAPL", SideBuy, 1, 100, 1002))
	// 3 working + this = 4 > cap 3
	if ok, _ := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 1, 100, "ET4"), marks); ok {
		t.Fatal("exceeding max open orders should block")
	}
}

func TestGateGlobalSymbolPosition(t *testing.T) {
	cfg, marks := baseCfg(), fakeMarks{"AAPL": 100}
	cfg.Global.MaxSymbolPositionShares = 250
	s := armedState()
	s.ReconcilePositions("sim-1", []Position{{Venue: "sim-1", Symbol: "AAPL", Qty: 150}})
	s.ReconcilePositions("sim-2", []Position{{Venue: "sim-2", Symbol: "AAPL", Qty: 80}})
	// global net 230 + 40 new on sim-1 = 270 > 250 global cap (per-venue caps allow it)
	if ok, _ := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 40, 100, "ET1"), marks); ok {
		t.Fatal("resulting cross-venue symbol position over global cap should block")
	}
}

func TestBreachedDayLoss(t *testing.T) {
	cfg := baseCfg()
	s := armedState()
	s.ReconcileAccount(AccountSnapshot{Venue: "sim-1", DayPnL: -600})
	s.ReconcileAccount(AccountSnapshot{Venue: "sim-2", DayPnL: -500}) // total -1100 <= -1000
	if !BreachedDayLoss(s, cfg) {
		t.Fatal("summed day loss over cap should be a breach")
	}
	s.ReconcileAccount(AccountSnapshot{Venue: "sim-2", DayPnL: -100}) // total -700
	if BreachedDayLoss(s, cfg) {
		t.Fatal("under cap should not breach")
	}
}

func TestGateDayLossBreachBlocksAfterRearm(t *testing.T) {
	cfg := baseCfg()
	marks := fakeMarks{"AAPL": 100}
	s := armedState()
	s.ReconcileAccount(AccountSnapshot{Venue: "sim-1", DayPnL: -1100}) // breaches MaxDayLoss=1000
	// Simulate what Core does on breach (auto-disarm), then an explicit
	// human re-arm with no fresh account update in between.
	s.SetMasterArmed(false)
	s.SetMasterArmed(true)
	if ok, reason := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 1, 100, "ET1"), marks); ok || reason != "day-loss breached" {
		t.Fatalf("re-arm after day-loss breach should still block, got ok=%v reason=%q", ok, reason)
	}
}

func TestGate_ValuesStopAtStopPrice_NoMarkNeeded(t *testing.T) {
	s := armedState()
	cfg := GateConfig{Venue: map[VenueID]VenueLimits{"sim-1": {MaxOrderValue: 1000}}}
	marks := fakeMarks{} // no marks at all
	// 10 * 90 = 900 <= 1000 -> allowed; a market order here would be blocked ("no mark").
	ok, reason := Evaluate(s, cfg, OrderRequest{
		Venue: "sim-1", Symbol: "AAPL", Side: SideSell, Type: TypeStop,
		Qty: 10, StopPrice: 90, ClientOrderID: "ET-stop",
	}, marks)
	if !ok {
		t.Fatalf("stop order should value at stop price without a mark: %q", reason)
	}
	// 20 * 90 = 1800 > 1000 -> blocked on venue value cap.
	if ok, _ := Evaluate(s, cfg, OrderRequest{
		Venue: "sim-1", Symbol: "AAPL", Side: SideSell, Type: TypeStop,
		Qty: 20, StopPrice: 90, ClientOrderID: "ET-stop2",
	}, marks); ok {
		t.Fatalf("20*90 should exceed the 1000 venue cap")
	}
}

func TestGate_ValuesStopLimitAtLimitPrice(t *testing.T) {
	s := armedState()
	cfg := GateConfig{Venue: map[VenueID]VenueLimits{"sim-1": {MaxOrderValue: 1000}}}
	marks := fakeMarks{}
	// stop-limit valued at limit (101), 10*101 = 1010 > 1000 -> blocked.
	if ok, _ := Evaluate(s, cfg, OrderRequest{
		Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeStopLimit,
		Qty: 10, StopPrice: 100, LimitPrice: 101, ClientOrderID: "ET-sl",
	}, marks); ok {
		t.Fatalf("stop-limit should value at limit price (10*101 > 1000)")
	}
}
