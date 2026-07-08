package session

import (
	"testing"
	"time"
)

func ms(iso string) int64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		panic(err)
	}
	return t.UnixMilli()
}

func TestBucketStartMs(t *testing.T) {
	cases := []struct {
		name string
		ts   string
		tf   Timeframe
		want string
	}{
		// EDT (UTC-4): 2026-07-06 13:30 UTC = 09:30 ET (same vectors as the UI mirror).
		{"10s floor", "2026-07-06T13:30:07Z", TF10s, "2026-07-06T13:30:00Z"},
		{"10s second bucket", "2026-07-06T13:30:12Z", TF10s, "2026-07-06T13:30:10Z"},
		{"1m floor", "2026-07-06T13:30:45Z", TF1m, "2026-07-06T13:30:00Z"},
		// 5m anchored at 09:30 ET.
		{"5m at open", "2026-07-06T13:32:00Z", TF5m, "2026-07-06T13:30:00Z"},
		{"5m second bucket", "2026-07-06T13:36:00Z", TF5m, "2026-07-06T13:35:00Z"},
		// Pre-market: negative offsets from the anchor must floor toward -inf.
		// 08:12 ET → 5m bucket 08:10 ET (12:10 UTC), 60m bucket 08:30 ET? No:
		// 08:12 is in [07:30,08:30) relative to the 09:30 anchor → 07:30 ET.
		{"5m pre-market", "2026-07-06T12:12:00Z", TF5m, "2026-07-06T12:10:00Z"},
		{"60m pre-market", "2026-07-06T12:12:00Z", TF60m, "2026-07-06T11:30:00Z"},
		{"60m RTH", "2026-07-06T14:45:00Z", TF60m, "2026-07-06T14:30:00Z"},
		// EST (UTC-5): 2026-01-06 14:30 UTC = 09:30 ET.
		{"1m in EST", "2026-01-06T14:31:30Z", TF1m, "2026-01-06T14:31:00Z"},
		{"30m in EST", "2026-01-06T15:10:00Z", TF30m, "2026-01-06T15:00:00Z"},
		// D/W/M: wall-midnight ET.
		{"D", "2026-07-06T18:00:00Z", TFDay, "2026-07-06T04:00:00Z"},
		{"W from Thursday", "2026-07-09T18:00:00Z", TFWeek, "2026-07-06T04:00:00Z"},
		{"W on Monday", "2026-07-06T18:00:00Z", TFWeek, "2026-07-06T04:00:00Z"},
		{"M mid-month", "2026-07-17T18:00:00Z", TFMonth, "2026-07-01T04:00:00Z"},
	}
	for _, c := range cases {
		if got := BucketStartMs(ms(c.ts), c.tf); got != ms(c.want) {
			t.Errorf("%s: BucketStartMs(%s, %s) = %d (%s), want %s",
				c.name, c.ts, c.tf, got, time.UnixMilli(got).UTC().Format(time.RFC3339), c.want)
		}
	}
}

func TestBucketStartMsCustomAnchor(t *testing.T) {
	// Anchor 09:00 ET: 09:20 ET falls in [09:00, 10:00).
	got := BucketStartMsAnchored(ms("2026-07-06T13:20:00Z"), TF60m, 9*3600)
	if want := ms("2026-07-06T13:00:00Z"); got != want {
		t.Fatalf("anchored bucket = %d, want %d", got, want)
	}
}

func TestPhaseAt(t *testing.T) {
	cases := []struct {
		ts   string
		want Phase
	}{
		{"2026-07-06T08:00:00Z", PreMarket},  // 04:00 ET Monday
		{"2026-07-06T13:29:59Z", PreMarket},  // 09:29:59 ET
		{"2026-07-06T13:30:00Z", RTH},        // 09:30 ET
		{"2026-07-06T19:59:59Z", RTH},        // 15:59:59 ET
		{"2026-07-06T20:00:00Z", PostMarket}, // 16:00 ET
		{"2026-07-07T00:00:00Z", Overnight},  // 20:00 ET (was Closed)
		{"2026-07-06T07:59:59Z", Overnight},  // 03:59:59 ET (was Closed)
		{"2026-07-04T15:00:00Z", Closed},     // Saturday
		{"2026-07-05T15:00:00Z", Closed},     // Sunday
		{"2026-01-06T14:30:00Z", RTH},        // EST regime
	}
	for _, c := range cases {
		if got := PhaseAt(time.UnixMilli(ms(c.ts))); got != c.want {
			t.Errorf("PhaseAt(%s) = %v, want %v", c.ts, got, c.want)
		}
	}
}

func TestPhaseAtOvernight(t *testing.T) {
	loc := Loc()
	et := func(h, m int) time.Time { return time.Date(2026, 7, 8, h, m, 0, 0, loc) } // Wed
	cases := []struct {
		name  string
		t     time.Time
		phase Phase
	}{
		{"post ends 19:59", et(19, 59), PostMarket},
		{"overnight starts 20:00", et(20, 0), Overnight},
		{"overnight 23:30", et(23, 30), Overnight},
		{"overnight 02:00", et(2, 0), Overnight},
		{"overnight 03:59", et(3, 59), Overnight},
		{"premarket starts 04:00", et(4, 0), PreMarket},
		{"weekend stays closed", time.Date(2026, 7, 11, 22, 0, 0, 0, loc), Closed}, // Sat 22:00
	}
	for _, c := range cases {
		if got := PhaseAt(c.t); got != c.phase {
			t.Errorf("%s: PhaseAt=%v want %v", c.name, got, c.phase)
		}
	}
	if Overnight.String() != "overnight" {
		t.Errorf("Overnight.String()=%q want %q", Overnight.String(), "overnight")
	}
}

func TestDayMs(t *testing.T) {
	if got, want := DayMs(ms("2026-07-06T18:00:00Z")), ms("2026-07-06T04:00:00Z"); got != want {
		t.Fatalf("DayMs = %d, want %d", got, want)
	}
}

func TestPoolDay(t *testing.T) {
	et := func(y int, mo time.Month, d, h, mi int) time.Time {
		return time.Date(y, mo, d, h, mi, 0, 0, Loc())
	}
	// The pool day anchored at 2026-07-07 20:00 ET spans 2026-07-07 20:00 ET
	// through 2026-07-08 20:00 ET (overnight -> pre-market -> RTH -> after-hours).
	anchor := PoolDay(et(2026, 7, 7, 20, 0))
	sameDay := []time.Time{
		et(2026, 7, 7, 20, 0),  // boundary start (overnight)
		et(2026, 7, 7, 23, 0),  // overnight, same calendar date
		et(2026, 7, 8, 3, 0),   // overnight, next calendar date
		et(2026, 7, 8, 9, 30),  // RTH open
		et(2026, 7, 8, 16, 0),  // RTH close
		et(2026, 7, 8, 19, 59), // last minute before the next boundary
	}
	for _, ts := range sameDay {
		if got := PoolDay(ts); got != anchor {
			t.Fatalf("PoolDay(%s)=%d, want %d (same pool day)", ts, got, anchor)
		}
	}
	// 20:00 ET on 2026-07-08 opens a NEW pool day.
	if next := PoolDay(et(2026, 7, 8, 20, 0)); next == anchor {
		t.Fatalf("PoolDay must roll over at 20:00 ET: got %d == %d", next, anchor)
	}
	// 19:59 vs 20:00 on the same date are different pool days.
	if PoolDay(et(2026, 7, 8, 19, 59)) == PoolDay(et(2026, 7, 8, 20, 0)) {
		t.Fatalf("PoolDay must differ across the 20:00 ET boundary")
	}
}
