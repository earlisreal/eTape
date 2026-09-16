package scan

import (
	"testing"
	"time"
)

func TestAlertEngineCrossingCooldownAndSuppressedEligibility(t *testing.T) {
	start := time.Unix(0, 0)
	e := newAlertEngine()
	value := 4.0
	if got := e.observe("US.A", "gainers", 5, &value, true, true, false, start); got != 0 {
		t.Fatalf("sub-threshold arrival emitted %d", got)
	}
	value = 5
	if got := e.observe("US.A", "gainers", 5, &value, false, false, false, start.Add(time.Second)); got != 0 {
		t.Fatalf("rejected crossing emitted %d", got)
	}
	value = 4
	e.observe("US.A", "gainers", 5, &value, true, false, false, start.Add(2*time.Second))
	value = 5
	if got := e.observe("US.A", "gainers", 5, &value, true, false, false, start.Add(3*time.Second)); got != 1 {
		t.Fatalf("crossing revision=%d, want 1", got)
	}
	value = 4
	e.observe("US.A", "gainers", 5, &value, true, false, false, start.Add(4*time.Second))
	value = 5
	if got := e.observe("US.A", "gainers", 5, &value, true, false, false, start.Add(59*time.Second)); got != 0 {
		t.Fatalf("cooldown crossing revision=%d", got)
	}
	value = 4
	e.observe("US.A", "gainers", 5, &value, true, false, false, start.Add(62*time.Second))
	value = 5
	if got := e.observe("US.A", "gainers", 5, &value, true, false, false, start.Add(63*time.Second)); got != 2 {
		t.Fatalf("post-cooldown revision=%d, want 2", got)
	}
	if got := e.observe("US.A", "gainers", 5, &value, true, false, false, start.Add(64*time.Second)); got != 0 {
		t.Fatalf("steady emission=%d, want no new emission", got)
	}
	if got := e.revision("US.A"); got != 2 {
		t.Fatalf("steady revision=%d, want last revision 2", got)
	}
	other := 6.0
	if got := e.observe("US.B", "gainers", 5, &other, true, true, false, start.Add(65*time.Second)); got != 1 {
		t.Fatalf("per-symbol first revision=%d, want 1", got)
	}
}

func TestAlertEngineUsesNewArrivalsForMostActiveAndZeroThreshold(t *testing.T) {
	start := time.Unix(0, 0)
	e := newAlertEngine()
	value := 0.0
	if got := e.observe("US.ACTIVE", "most_active", 0, nil, true, true, false, start); got != 0 {
		t.Fatalf("unavailable most-active arrival emitted %d", got)
	}
	if got := e.observe("US.ACTIVE", "most_active", 0, &value, true, false, false, start.Add(time.Second)); got != 1 {
		t.Fatalf("healthy most-active arrival revision=%d, want 1", got)
	}
	if got := e.observe("US.ZERO", "gainers", 0, &value, true, true, false, start); got != 1 {
		t.Fatalf("zero-threshold arrival revision=%d, want 1", got)
	}
	value = 4
	if got := e.observe("US.ZERO", "gainers", 0, &value, true, false, false, start.Add(time.Second)); got != 0 {
		t.Fatalf("zero-threshold update revision=%d, want no repeat", got)
	}
}
