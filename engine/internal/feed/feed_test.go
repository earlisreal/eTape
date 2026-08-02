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
	if w.Focused || len(w.Subs) != 1 || w.HistoryDays != 2 {
		t.Fatalf("watch profile = %+v, want ticker-only 2-day warm", w)
	}
	if w.Subs[0] != SubTicker {
		t.Fatalf("watch subs = %v, want [SubTicker]", w.Subs)
	}
	c := ChartDemand("chart-AAPL", "US.AAPL")
	if !c.CachedDaily || c.HistoryDays != 70 || len(c.Subs) != 3 || c.Subs[2] != SubKLDay {
		t.Fatalf("chart profile = %+v, want watch subs plus SubKLDay", c)
	}
}

// Compile-time exhaustiveness: every event type is part of the union.
var _ = []Event{
	TicksEvent{}, QuoteEvent{}, BookEvent{}, Bars1mEvent{},
	ConnUpEvent{}, ConnDownEvent{}, ResyncedEvent{},
}
