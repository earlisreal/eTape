package feed

import "testing"

func TestDirectionString(t *testing.T) {
	for d, want := range map[Direction]string{Buy: "BUY", Sell: "SELL", Neutral: "NEUTRAL"} {
		if got := d.String(); got != want {
			t.Errorf("Direction(%d).String() = %q, want %q", d, got, want)
		}
	}
}

func TestDemandProfiles(t *testing.T) {
	w := WatchDemand("watch-AAPL", "US.AAPL")
	if w.Focused || len(w.Subs) != 2 {
		t.Fatalf("watch profile = %+v, want 2 subs, not Focused", w)
	}
	// Watch profile is exactly TICKER + K_1M (tape/10s/1m recording, no depth).
	if w.Subs[0] != SubTicker || w.Subs[1] != SubKL1m {
		t.Fatalf("watch subs = %v, want [SubTicker SubKL1m]", w.Subs)
	}
	c := ChartDemand("chart-AAPL", "US.AAPL")
	if !c.CachedDaily || !c.WantsHistory || len(c.Subs) != 3 || c.Subs[2] != SubKLDay {
		t.Fatalf("chart profile = %+v, want watch subs plus SubKLDay", c)
	}
}

// Compile-time exhaustiveness: every event type is part of the union.
var _ = []Event{
	TicksEvent{}, QuoteEvent{}, BookEvent{}, Bars1mEvent{},
	ConnUpEvent{}, ConnDownEvent{}, ResyncedEvent{},
}
