package backfill

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func TestIntradayFromSkipsWeekends(t *testing.T) {
	// Wednesday 2026-07-08 12:00 ET.
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	// 1 trading day back = Tuesday 2026-07-07 00:00 ET.
	got := intradayFrom(now, 1)
	want := time.Date(2026, 7, 7, 0, 0, 0, 0, session.Loc())
	if !got.Equal(want) {
		t.Fatalf("intradayFrom(1) = %s, want %s", got, want)
	}
	// 3 trading days back from Wed spans the weekend: Tue, Mon, Fri 2026-07-03.
	got = intradayFrom(now, 3)
	want = time.Date(2026, 7, 3, 0, 0, 0, 0, session.Loc())
	if !got.Equal(want) {
		t.Fatalf("intradayFrom(3) = %s, want %s", got, want)
	}
}

func TestSeedChunkedSplitsAndPreservesOrder(t *testing.T) {
	bars := make([]feed.Bar, 1200)
	for i := range bars {
		bars[i] = feed.Bar{Symbol: "US.AAPL", BucketMs: int64(i)}
	}
	var calls [][]feed.Bar
	seedChunked(500, bars, func(b []feed.Bar) {
		calls = append(calls, append([]feed.Bar(nil), b...))
	})
	if len(calls) != 3 || len(calls[0]) != 500 || len(calls[1]) != 500 || len(calls[2]) != 200 {
		t.Fatalf("chunk sizes = %d,%d,%d (want 500,500,200)", len(calls[0]), len(calls[1]), len(calls[2]))
	}
	// Order preserved end-to-end.
	var flat []feed.Bar
	for _, c := range calls {
		flat = append(flat, c...)
	}
	for i := range flat {
		if flat[i].BucketMs != int64(i) {
			t.Fatalf("order broken at %d: %d", i, flat[i].BucketMs)
		}
	}
	// Empty input => no calls.
	calls = nil
	seedChunked(500, nil, func(b []feed.Bar) { calls = append(calls, b) })
	if len(calls) != 0 {
		t.Fatalf("empty input produced %d calls", len(calls))
	}
}
