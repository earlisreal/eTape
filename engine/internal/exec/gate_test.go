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
	s.SetVenueArmed("sim-1", true)
	s.SetVenueArmed("sim-2", true)
	return s
}

func buyReq(v VenueID, sym string, qty, limit float64, id string) OrderRequest {
	return OrderRequest{Venue: v, Symbol: sym, Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: qty, LimitPrice: limit, ClientOrderID: id}
}

func TestGateMasterAndVenueArm(t *testing.T) {
	cfg := baseCfg()
	marks := fakeMarks{"AAPL": 100}
	s := NewState([]VenueID{"sim-1"}) // disarmed
	if ok, reason := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 10, 100, "ET1"), marks); ok || reason == "" {
		t.Fatalf("disarmed master should block, got ok=%v reason=%q", ok, reason)
	}
	s.SetMasterArmed(true) // venue still off
	if ok, _ := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 10, 100, "ET1"), marks); ok {
		t.Fatal("venue disarmed should block")
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
