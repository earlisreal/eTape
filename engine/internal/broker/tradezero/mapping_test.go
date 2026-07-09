package tradezero

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

func TestOrderTypeWire(t *testing.T) {
	cases := map[exec.OrderType]string{
		exec.TypeMarket: "Market", exec.TypeLimit: "Limit",
		exec.TypeStop: "Stop", exec.TypeStopLimit: "StopLimit",
	}
	for ot, want := range cases {
		got, err := orderTypeWire(ot)
		if err != nil || got != want {
			t.Errorf("orderTypeWire(%v) = %q,%v want %q", ot, got, err, want)
		}
	}
}

func TestSideWireAndBack(t *testing.T) {
	cases := []struct {
		s              exec.Side
		side, openClse string
	}{
		{exec.SideBuy, "Buy", "Open"},
		{exec.SideSell, "Sell", "Close"},
		{exec.SideShort, "Sell", "Open"},
		{exec.SideCover, "Buy", "Close"},
	}
	for _, c := range cases {
		gs, go_ := sideWire(c.s)
		if gs != c.side || go_ != c.openClse {
			t.Errorf("sideWire(%v) = %q/%q want %q/%q", c.s, gs, go_, c.side, c.openClse)
		}
		// round trip: responses enrich short to SellShort; derive via openClose.
		wire := gs
		if c.s == exec.SideShort {
			wire = "SellShort"
		}
		if back := sideDomain(wire, c.openClse); back != c.s {
			t.Errorf("sideDomain(%q,%q) = %v want %v", wire, c.openClse, back, c.s)
		}
	}
}

func TestTifWire_ExtendedHoursCoercion(t *testing.T) {
	if got := tifWire(exec.TIFDay, true, exec.TypeLimit); got != "Day_Plus" {
		t.Errorf("ext-hours Day limit -> %q want Day_Plus", got)
	}
	if got := tifWire(exec.TIFDay, false, exec.TypeLimit); got != "Day" {
		t.Errorf("RTH Day -> %q want Day", got)
	}
	if got := tifWire(exec.TIFGTC, true, exec.TypeLimit); got != "GTC_Plus" {
		t.Errorf("ext-hours GTC limit -> %q want GTC_Plus", got)
	}
	if got := tifWire(exec.TIFGTC, true, exec.TypeStopLimit); got != "GoodTillCancel" {
		t.Errorf("ext-hours GTC stop-limit keeps base TIF (_Plus is Limit-only) -> %q want GoodTillCancel", got)
	}
}

func TestIsExtendedHours(t *testing.T) {
	// 08:00 ET (pre-market) on a weekday.
	et := time.Date(2026, 7, 6, 8, 0, 0, 0, mustET(t))
	if !isExtendedHours(clock.NewFake(et)) {
		t.Fatal("08:00 ET should be extended hours")
	}
	// 10:00 ET (RTH).
	et = time.Date(2026, 7, 6, 10, 0, 0, 0, mustET(t))
	if isExtendedHours(clock.NewFake(et)) {
		t.Fatal("10:00 ET should not be extended hours")
	}
}

// TestExtendedHoursFor_ExplicitSessionOverridesClock covers the trader's
// explicit session choice taking priority over the clock-inferred
// (SessionAuto) default: RTH forces the base TIF path even during a
// pre-market clock, Extended forces the _Plus path even during an RTH clock,
// and Auto falls back to isExtendedHours (today's behavior) either way.
func TestExtendedHoursFor_ExplicitSessionOverridesClock(t *testing.T) {
	preMarket := clock.NewFake(time.Date(2026, 7, 6, 8, 0, 0, 0, mustET(t)))
	rth := clock.NewFake(time.Date(2026, 7, 6, 10, 0, 0, 0, mustET(t)))

	if got := extendedHoursFor(exec.SessionRTH, preMarket); got {
		t.Fatal("explicit RTH during pre-market clock must resolve to false")
	}
	if got := extendedHoursFor(exec.SessionExtended, rth); !got {
		t.Fatal("explicit Extended during RTH clock must resolve to true")
	}
	if got := extendedHoursFor(exec.SessionAuto, preMarket); !got {
		t.Fatal("Auto during pre-market clock must defer to isExtendedHours (true)")
	}
	if got := extendedHoursFor(exec.SessionAuto, rth); got {
		t.Fatal("Auto during RTH clock must defer to isExtendedHours (false)")
	}
}

func mustET(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	return loc
}
