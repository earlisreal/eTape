package exec

import "testing"

func TestReconcileOverwrites(t *testing.T) {
	s := NewState([]VenueID{"sim-1"})
	s.ReconcilePositions("sim-1", []Position{{Venue: "sim-1", Symbol: "AAPL", Qty: 100, AvgPrice: 50}})
	if s.VenuePositionShares("sim-1", "AAPL") != 100 {
		t.Fatalf("got %v", s.VenuePositionShares("sim-1", "AAPL"))
	}
	// A later push is authoritative and REPLACES (not accumulates).
	s.ReconcilePositions("sim-1", []Position{{Venue: "sim-1", Symbol: "AAPL", Qty: 40, AvgPrice: 51}})
	if s.VenuePositionShares("sim-1", "AAPL") != 40 {
		t.Fatalf("overwrite failed: %v", s.VenuePositionShares("sim-1", "AAPL"))
	}
	s.ReconcileAccount(AccountSnapshot{Venue: "sim-1", Equity: 10000, DayPnL: -250})
	if s.Venue("sim-1").Account.DayPnL != -250 {
		t.Fatalf("account not reconciled: %+v", s.Venue("sim-1").Account)
	}
}

func TestArming(t *testing.T) {
	s := NewState([]VenueID{"sim-1", "sim-2"})
	if s.IsArmed("sim-1") {
		t.Fatal("boot should be disarmed")
	}
	if s.IsArmed("sim-unregistered") {
		t.Fatal("an unregistered venue should never report armed, even with master on")
	}
	s.SetMasterArmed(true)
	if !s.IsArmed("sim-1") {
		t.Fatal("master on + registered venue → armed")
	}
	if !s.IsArmed("sim-2") {
		t.Fatal("master arm covers every registered venue, not just sim-1")
	}
	if s.IsArmed("sim-unregistered") {
		t.Fatal("master on but venue never registered → still not armed")
	}
}

func TestCrossVenueAggregates(t *testing.T) {
	s := NewState([]VenueID{"sim-1", "sim-2"})
	s.ReconcilePositions("sim-1", []Position{{Venue: "sim-1", Symbol: "AAPL", Qty: 100}})
	s.ReconcilePositions("sim-2", []Position{{Venue: "sim-2", Symbol: "AAPL", Qty: -30}})
	if got := s.SymbolNetShares("AAPL"); got != 70 {
		t.Fatalf("net = %v, want 70", got)
	}
	// working same-direction buy exposure across venues
	s.Apply(submitEv("sim-1", "ET1", "AAPL", SideBuy, 10, 100, 1000))
	s.Apply(submitEv("sim-2", "ET2", "AAPL", SideBuy, 5, 100, 1001))
	s.Apply(submitEv("sim-1", "ET3", "AAPL", SideSell, 4, 200, 1002)) // opposite dir, excluded
	if got := s.SymbolWorkingSameDir("AAPL", SideBuy); got != 15 {
		t.Fatalf("working buy = %v, want 15", got)
	}
	if got := s.VenueWorkingSameDir("sim-1", "AAPL", SideBuy); got != 10 {
		t.Fatalf("venue working buy = %v, want 10", got)
	}
	s.ReconcileAccount(AccountSnapshot{Venue: "sim-1", DayPnL: -100})
	s.ReconcileAccount(AccountSnapshot{Venue: "sim-2", DayPnL: -50})
	if got := s.TotalDayPnL(); got != -150 {
		t.Fatalf("total day pnl = %v, want -150", got)
	}
}
