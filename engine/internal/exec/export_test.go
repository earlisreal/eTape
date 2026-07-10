package exec

import (
	"testing"
	"time"
)

// export_test.go exposes Core's internal state to exec_test (the external test
// package) for whitebox assertions. It is compiled only for `go test` — never
// part of the production binary — and deliberately imports nothing beyond the
// standard library so it cannot introduce the broker/sim <-> exec import cycle
// that forced core_test.go (and core_lifecycle_test.go) into package exec_test.
//
// StateForTest returns Core's live *State. Callers must only read it, and only
// when no other goroutine is concurrently mutating it (e.g. right after Recover,
// before Run starts) to stay race-free.
func (c *Core) StateForTest() *State { return c.state }

func ms(iso string) int64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		panic(err)
	}
	return t.UnixMilli()
}

func TestResolveExportRangePresets(t *testing.T) {
	// 2026-07-08T18:00:00Z = 14:00 ET (EDT, UTC-4), a Wednesday. Same calendar
	// week/month as the vectors in session/session_test.go's TestBucketStartMs.
	now := time.UnixMilli(ms("2026-07-08T18:00:00Z"))
	cases := []struct {
		name       string
		preset     string
		wantFromMs int64
	}{
		{"today", "today", ms("2026-07-08T04:00:00Z")}, // ET wall-midnight
		{"week", "week", ms("2026-07-06T04:00:00Z")},   // Monday ET wall-midnight
		{"month", "month", ms("2026-07-01T04:00:00Z")}, // 1st-of-month ET wall-midnight
		{"all", "all", 0},
	}
	for _, c := range cases {
		fromMs, toMs, err := ResolveExportRange(c.preset, "", "", now)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if fromMs != c.wantFromMs {
			t.Errorf("%s: fromMs = %d, want %d", c.name, fromMs, c.wantFromMs)
		}
		if toMs != now.UnixMilli()+1 {
			t.Errorf("%s: toMs = %d, want now+1 = %d", c.name, toMs, now.UnixMilli()+1)
		}
	}
}

func TestResolveExportRangeCustom(t *testing.T) {
	now := time.UnixMilli(ms("2026-07-08T18:00:00Z"))
	fromMs, toMs, err := ResolveExportRange("custom", "2026-07-01", "2026-07-03", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := ms("2026-07-01T04:00:00Z"); fromMs != want { // ET midnight of "from"
		t.Errorf("fromMs = %d, want %d", fromMs, want)
	}
	if want := ms("2026-07-04T04:00:00Z"); toMs != want { // ET midnight of the day AFTER "to" (inclusive of "to")
		t.Errorf("toMs = %d, want %d", toMs, want)
	}
}

func TestResolveExportRangeCustomAcrossDST(t *testing.T) {
	// The "to" day (2026-03-08) is the day of the US spring-forward transition
	// (2am ET); the next-midnight upper bound must land on the correct EDT
	// wall-clock instant despite the offset change within the range.
	now := time.UnixMilli(ms("2026-03-10T00:00:00Z"))
	fromMs, toMs, err := ResolveExportRange("custom", "2026-03-06", "2026-03-08", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := ms("2026-03-06T05:00:00Z"); fromMs != want { // EST midnight, UTC-5
		t.Errorf("fromMs = %d, want %d", fromMs, want)
	}
	if want := ms("2026-03-09T04:00:00Z"); toMs != want { // EDT midnight, UTC-4 (DST already sprang forward)
		t.Errorf("toMs = %d, want %d", toMs, want)
	}
}

func TestResolveExportRangeCustomErrors(t *testing.T) {
	now := time.Now()
	if _, _, err := ResolveExportRange("custom", "not-a-date", "2026-07-03", now); err == nil {
		t.Error("expected error for unparseable from date")
	}
	if _, _, err := ResolveExportRange("custom", "2026-07-01", "nope", now); err == nil {
		t.Error("expected error for unparseable to date")
	}
	if _, _, err := ResolveExportRange("custom", "2026-07-05", "2026-07-01", now); err == nil {
		t.Error("expected error when from is after to")
	}
}

func TestResolveExportRangeUnknownPreset(t *testing.T) {
	if _, _, err := ResolveExportRange("bogus", "", "", time.Now()); err == nil {
		t.Error("expected error for unknown preset")
	}
}
