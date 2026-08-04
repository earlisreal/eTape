package exec

import "testing"

func TestCycleProjectionPartialCloseFlatAndFlip(t *testing.T) {
	c := newCycleProjection()
	c.bootstrap("v", 1, nil)
	c.applyFill(Fill{Venue: "v", Symbol: "A", Side: SideBuy, Qty: 10, Price: 100})
	c.applyFill(Fill{Venue: "v", Symbol: "A", Side: SideSell, Qty: 4, Price: 110})
	_, open, total, _ := c.account("v")
	if open != 40 || total != 40 {
		t.Fatalf("partial open=%v total=%v", open, total)
	}
	c.applyFill(Fill{Venue: "v", Symbol: "A", Side: SideSell, Qty: 6, Price: 90})
	_, open, total, _ = c.account("v")
	if open != 0 || total != -20 {
		t.Fatalf("flat open=%v total=%v", open, total)
	}
	c.applyFill(Fill{Venue: "v", Symbol: "S", Side: SideShort, Qty: 5, Price: 20})
	c.applyFill(Fill{Venue: "v", Symbol: "S", Side: SideCover, Qty: 7, Price: 18})
	p := c.position("v", "S")
	if p.Qty != 2 || p.Basis != 18 || p.Realized != 0 {
		t.Fatalf("flip = %+v", p)
	}
}
