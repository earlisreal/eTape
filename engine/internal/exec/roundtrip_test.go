package exec

import "testing"

func TestRoundTripLongTrip(t *testing.T) {
	a := NewRoundTripAggregator()
	if trades := a.Apply("sim-1", "AAPL", SideBuy, 10, 100, 1000); len(trades) != 0 {
		t.Fatalf("open leg should not emit, got %+v", trades)
	}
	trades := a.Apply("sim-1", "AAPL", SideSell, 10, 110, 2000)
	if len(trades) != 1 {
		t.Fatalf("close leg should emit exactly 1 trade, got %d: %+v", len(trades), trades)
	}
	tr := trades[0]
	if !tr.IsLong {
		t.Fatalf("expected long trip, got %+v", tr)
	}
	if tr.Venue != "sim-1" || tr.Symbol != "AAPL" {
		t.Fatalf("venue/symbol mismatch: %+v", tr)
	}
	if tr.Qty != 10 || tr.EntryPrice != 100 || tr.ExitPrice != 110 || tr.Realized != 100 {
		t.Fatalf("want qty=10 entry=100 exit=110 realized=100, got %+v", tr)
	}
	if tr.OpenMs != 1000 || tr.CloseMs != 2000 {
		t.Fatalf("want openMs=1000 closeMs=2000, got %+v", tr)
	}
	if tr.Seq != 1 {
		t.Fatalf("want seq=1, got %+v", tr)
	}
}

func TestRoundTripShortTrip(t *testing.T) {
	a := NewRoundTripAggregator()
	if trades := a.Apply("sim-1", "AAPL", SideShort, 10, 100, 1000); len(trades) != 0 {
		t.Fatalf("open leg should not emit, got %+v", trades)
	}
	trades := a.Apply("sim-1", "AAPL", SideCover, 10, 90, 2000)
	if len(trades) != 1 {
		t.Fatalf("close leg should emit exactly 1 trade, got %d: %+v", len(trades), trades)
	}
	tr := trades[0]
	if tr.IsLong {
		t.Fatalf("expected short trip, got %+v", tr)
	}
	if tr.Qty != 10 || tr.EntryPrice != 100 || tr.ExitPrice != 90 || tr.Realized != 100 {
		t.Fatalf("want qty=10 entry=100 exit=90 realized=100, got %+v", tr)
	}
	if tr.OpenMs != 1000 || tr.CloseMs != 2000 {
		t.Fatalf("want openMs=1000 closeMs=2000, got %+v", tr)
	}
}

func TestRoundTripScaleInThenFullExit(t *testing.T) {
	a := NewRoundTripAggregator()
	a.Apply("sim-1", "AAPL", SideBuy, 5, 100, 1000)
	if trades := a.Apply("sim-1", "AAPL", SideBuy, 5, 110, 1500); len(trades) != 0 {
		t.Fatalf("scale-in should not emit, got %+v", trades)
	}
	trades := a.Apply("sim-1", "AAPL", SideSell, 10, 120, 2000)
	if len(trades) != 1 {
		t.Fatalf("close leg should emit exactly 1 trade, got %d: %+v", len(trades), trades)
	}
	tr := trades[0]
	// Weighted avg entry: (5*100 + 5*110) / 10 = 105.
	if tr.Qty != 10 || tr.EntryPrice != 105 || tr.ExitPrice != 120 {
		t.Fatalf("want qty=10 entry=105 exit=120, got %+v", tr)
	}
	// Realized: proceeds 1200 - cost (500+550)=1050 -> 150.
	if tr.Realized != 150 {
		t.Fatalf("want realized=150, got %+v", tr)
	}
	if tr.OpenMs != 1000 {
		t.Fatalf("want openMs=1000 (first opening fill), got %+v", tr)
	}
}

func TestRoundTripPartialScaleOutThenFullExit(t *testing.T) {
	a := NewRoundTripAggregator()
	a.Apply("sim-1", "AAPL", SideBuy, 10, 100, 1000)
	if trades := a.Apply("sim-1", "AAPL", SideSell, 4, 110, 1500); len(trades) != 0 {
		t.Fatalf("partial scale-out should not emit, got %+v", trades)
	}
	trades := a.Apply("sim-1", "AAPL", SideSell, 6, 120, 2000)
	if len(trades) != 1 {
		t.Fatalf("full exit should emit exactly 1 trade, got %d: %+v", len(trades), trades)
	}
	tr := trades[0]
	if tr.Qty != 10 || tr.EntryPrice != 100 {
		t.Fatalf("want qty=10 entry=100, got %+v", tr)
	}
	// Weighted avg exit: (4*110 + 6*120) / 10 = 116.
	if tr.ExitPrice != 116 {
		t.Fatalf("want exit=116, got %+v", tr)
	}
	// Realized: proceeds (440+720)=1160 - cost 1000 -> 160.
	if tr.Realized != 160 {
		t.Fatalf("want realized=160, got %+v", tr)
	}
	if tr.OpenMs != 1000 || tr.CloseMs != 2000 {
		t.Fatalf("want openMs=1000 closeMs=2000, got %+v", tr)
	}
}

func TestRoundTripLongToShortFlipInOneFill(t *testing.T) {
	a := NewRoundTripAggregator()
	a.Apply("sim-1", "AAPL", SideBuy, 10, 100, 1000)

	// A single 15-share sell flips the 10-share long into a 5-share short.
	trades := a.Apply("sim-1", "AAPL", SideSell, 15, 105, 2000)
	if len(trades) != 1 {
		t.Fatalf("flip fill should emit exactly 1 trade (the closed long leg), got %d: %+v", len(trades), trades)
	}
	first := trades[0]
	if !first.IsLong || first.Qty != 10 || first.EntryPrice != 100 || first.ExitPrice != 105 {
		t.Fatalf("want closed long qty=10 entry=100 exit=105, got %+v", first)
	}
	// Realized: proceeds on the 10 closing shares (1050) - cost (1000) = 50.
	if first.Realized != 50 {
		t.Fatalf("want realized=50 on the closed leg, got %+v", first)
	}
	if first.OpenMs != 1000 || first.CloseMs != 2000 {
		t.Fatalf("want openMs=1000 closeMs=2000, got %+v", first)
	}

	// Close out the new 5-share short leftover from the flip.
	trades2 := a.Apply("sim-1", "AAPL", SideCover, 5, 110, 3000)
	if len(trades2) != 1 {
		t.Fatalf("closing the flip remainder should emit exactly 1 trade, got %d: %+v", len(trades2), trades2)
	}
	second := trades2[0]
	if second.IsLong {
		t.Fatalf("expected the flip remainder to be a short trip, got %+v", second)
	}
	if second.Qty != 5 || second.EntryPrice != 105 || second.ExitPrice != 110 {
		t.Fatalf("want qty=5 entry=105 (flip price) exit=110, got %+v", second)
	}
	// Realized: proceeds on open (5*105=525) minus cover cost (5*110=550) -> -25.
	if second.Realized != -25 {
		t.Fatalf("want realized=-25, got %+v", second)
	}
	// The key flip assertion: the new trip's open timestamp is the flip fill's ts.
	if second.OpenMs != 2000 {
		t.Fatalf("want 2nd trip openMs == flip ts (2000), got %+v", second)
	}
	if second.CloseMs != 3000 {
		t.Fatalf("want closeMs=3000, got %+v", second)
	}
	if second.Seq <= first.Seq {
		t.Fatalf("seq must be monotonically increasing across trips: first=%d second=%d", first.Seq, second.Seq)
	}
}

func TestRoundTripFlatStartAssumption(t *testing.T) {
	// The aggregator assumes it observes every fill from flat; the very first
	// fill for a fresh (venue,symbol) key always opens a new trip regardless of
	// side (buy opens long, short opens short) and never emits on its own.
	a := NewRoundTripAggregator()
	if trades := a.Apply("sim-1", "MSFT", SideShort, 3, 50, 1000); len(trades) != 0 {
		t.Fatalf("first fill from flat should never emit, got %+v", trades)
	}
	// A second, unrelated (venue,symbol) key is independent and also starts flat.
	if trades := a.Apply("sim-2", "MSFT", SideBuy, 1, 50, 1000); len(trades) != 0 {
		t.Fatalf("independent key should also start flat and not emit, got %+v", trades)
	}
	// Finish closing both so the test leaves no dangling state surprises.
	trades := a.Apply("sim-1", "MSFT", SideCover, 3, 45, 2000)
	if len(trades) != 1 || trades[0].Realized != 15 {
		t.Fatalf("want 1 trade realized=15, got %+v", trades)
	}
}

func TestRoundTripSeqMonotonicAcrossKeys(t *testing.T) {
	a := NewRoundTripAggregator()
	a.Apply("sim-1", "AAPL", SideBuy, 1, 100, 1000)
	a.Apply("sim-2", "MSFT", SideBuy, 1, 200, 1000)
	t1 := a.Apply("sim-1", "AAPL", SideSell, 1, 101, 2000)
	t2 := a.Apply("sim-2", "MSFT", SideSell, 1, 201, 2000)
	if len(t1) != 1 || len(t2) != 1 {
		t.Fatalf("expected both to emit, got %+v %+v", t1, t2)
	}
	if t2[0].Seq != t1[0].Seq+1 {
		t.Fatalf("seq should be a single monotonic counter across (venue,symbol) keys: t1=%d t2=%d", t1[0].Seq, t2[0].Seq)
	}
}
