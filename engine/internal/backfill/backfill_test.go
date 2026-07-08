package backfill

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
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

// fakeFetcher returns canned bars and records call ranges.
type fakeFetcher struct {
	daily, m1  []feed.Bar
	dErr, mErr error
	m1Calls    atomic.Int32
}

func (f *fakeFetcher) DailyBars(_ context.Context, _ string, _, _ time.Time) ([]feed.Bar, error) {
	return f.daily, f.dErr
}
func (f *fakeFetcher) Intraday1m(_ context.Context, _ string, _, _ time.Time) ([]feed.Bar, error) {
	f.m1Calls.Add(1)
	return f.m1, f.mErr
}

// fakeSeeder records seeded bars per method.
type fakeSeeder struct {
	mu          sync.Mutex
	daily, hist []feed.Bar
}

func (s *fakeSeeder) SeedDaily(_ string, b []feed.Bar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.daily = append(s.daily, b...)
}
func (s *fakeSeeder) SeedHistory1m(_ string, b []feed.Bar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hist = append(s.hist, b...)
}

// fakeArchive returns canned warm-start bars.
type fakeArchive struct {
	daily, m1 []feed.Bar
}

func (a *fakeArchive) ReadDailyBars(_ string) ([]feed.Bar, error)          { return a.daily, nil }
func (a *fakeArchive) ReadBars1m(_ string, _, _ int64) ([]feed.Bar, error) { return a.m1, nil }

func bar(ms int64) feed.Bar { return feed.Bar{Symbol: "US.AAPL", BucketMs: ms, C: 1} }

func TestBackfillWarmStartThenGapFill(t *testing.T) {
	primary := &fakeFetcher{
		daily: []feed.Bar{bar(1), bar(2)},
		m1:    []feed.Bar{bar(10), bar(11), bar(12)},
	}
	seeder := &fakeSeeder{}
	archive := &fakeArchive{
		daily: []feed.Bar{bar(0)}, // one warm-start daily bar
		m1:    []feed.Bar{bar(9)}, // one warm-start 1m bar
	}
	o := New(primary, nil, seeder, archive, clock.NewFake(time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())), Config{IntradayDays: 20, SeedChunk: 500})
	o.Backfill(context.Background(), "US.AAPL")

	// Daily: warm-start(1) + moomoo(2) = 3 seeded.
	if len(seeder.daily) != 3 {
		t.Fatalf("daily seeded = %d, want 3", len(seeder.daily))
	}
	// 1m: warm-start(1) + moomoo(3) = 4 seeded.
	if len(seeder.hist) != 4 {
		t.Fatalf("1m seeded = %d, want 4", len(seeder.hist))
	}
}

func TestBackfillPrimaryDailyErrorIsNonFatal(t *testing.T) {
	primary := &fakeFetcher{dErr: context.DeadlineExceeded, m1: []feed.Bar{bar(10)}}
	seeder := &fakeSeeder{}
	o := New(primary, nil, seeder, &fakeArchive{}, clock.NewFake(time.Now()), Config{IntradayDays: 20, SeedChunk: 500})
	o.Backfill(context.Background(), "US.AAPL") // must not panic
	if len(seeder.daily) != 0 {
		t.Fatalf("daily seeded on error = %d, want 0", len(seeder.daily))
	}
	if len(seeder.hist) != 1 {
		t.Fatalf("1m still seeded = %d, want 1", len(seeder.hist))
	}
}

func TestRunBoundedPoolCoversEverySymbol(t *testing.T) {
	primary := &fakeFetcher{m1: []feed.Bar{bar(10)}}
	seeder := &fakeSeeder{}
	o := New(primary, nil, seeder, &fakeArchive{}, clock.NewFake(time.Now()), Config{Concurrency: 2, IntradayDays: 20, SeedChunk: 500})
	o.Run(context.Background(), []string{"US.AAPL", "US.TSLA", "US.MSFT"})
	if got := primary.m1Calls.Load(); got != 3 {
		t.Fatalf("Intraday1m called %d times, want 3 (one per symbol)", got)
	}
}

// splitFetcher lets a test give the primary and fallback different data and
// record what range the fallback was asked for.
type recordFallback struct {
	m1        []feed.Bar
	daily     []feed.Bar
	m1From    atomic.Int64
	m1To      atomic.Int64
	m1Calls   atomic.Int32
	dailyCall atomic.Int32
}

func (r *recordFallback) DailyBars(_ context.Context, _ string, _, _ time.Time) ([]feed.Bar, error) {
	r.dailyCall.Add(1)
	return r.daily, nil
}
func (r *recordFallback) Intraday1m(_ context.Context, _ string, from, to time.Time) ([]feed.Bar, error) {
	r.m1Calls.Add(1)
	r.m1From.Store(from.UnixMilli())
	r.m1To.Store(to.UnixMilli())
	return r.m1, nil
}

func TestFallbackFillsShallowGap(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	from := intradayFrom(now, 20)
	// Primary returns only recent bars: oldest is 5 days after `from` — a wide gap.
	oldestMs := from.UnixMilli() + 5*24*3600*1000
	primary := &fakeFetcher{m1: []feed.Bar{bar(oldestMs), bar(oldestMs + 60000)}}
	fb := &recordFallback{m1: []feed.Bar{bar(from.UnixMilli())}}
	seeder := &fakeSeeder{}
	o := New(primary, fb, seeder, &fakeArchive{}, clock.NewFake(now), Config{IntradayDays: 20, SeedChunk: 500})
	o.Backfill(context.Background(), "US.AAPL")

	if fb.m1Calls.Load() != 1 {
		t.Fatalf("fallback 1m calls = %d, want 1", fb.m1Calls.Load())
	}
	// Fallback asked for [from, oldest).
	if fb.m1From.Load() != from.UnixMilli() || fb.m1To.Load() != oldestMs {
		t.Fatalf("fallback range = [%d,%d), want [%d,%d)", fb.m1From.Load(), fb.m1To.Load(), from.UnixMilli(), oldestMs)
	}
	// Seeded = primary(2) + fallback(1).
	if len(seeder.hist) != 3 {
		t.Fatalf("1m seeded = %d, want 3", len(seeder.hist))
	}
}

func TestFallbackSkippedWhenPrimaryDeepEnough(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	from := intradayFrom(now, 20)
	// Primary's oldest is right at `from` — full depth, no gap.
	primary := &fakeFetcher{m1: []feed.Bar{bar(from.UnixMilli()), bar(from.UnixMilli() + 60000)}}
	fb := &recordFallback{m1: []feed.Bar{bar(0)}}
	o := New(primary, fb, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(now), Config{IntradayDays: 20, SeedChunk: 500})
	o.Backfill(context.Background(), "US.AAPL")
	if fb.m1Calls.Load() != 0 {
		t.Fatalf("fallback called %d times, want 0 (primary deep enough)", fb.m1Calls.Load())
	}
}

func TestFallbackFillsWholeWindowOnPrimaryError(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	from := intradayFrom(now, 20)
	primary := &fakeFetcher{mErr: context.DeadlineExceeded, dErr: context.DeadlineExceeded}
	fb := &recordFallback{m1: []feed.Bar{bar(from.UnixMilli())}, daily: []feed.Bar{bar(1), bar(2)}}
	seeder := &fakeSeeder{}
	o := New(primary, fb, seeder, &fakeArchive{}, clock.NewFake(now), Config{IntradayDays: 20, SeedChunk: 500})
	o.Backfill(context.Background(), "US.AAPL")

	// 1m: fallback asked for the whole [from, now] window.
	if fb.m1Calls.Load() != 1 || fb.m1From.Load() != from.UnixMilli() || fb.m1To.Load() != now.UnixMilli() {
		t.Fatalf("fallback 1m range = [%d,%d) calls=%d", fb.m1From.Load(), fb.m1To.Load(), fb.m1Calls.Load())
	}
	// Daily: primary errored, fallback daily used (2 bars).
	if fb.dailyCall.Load() != 1 || len(seeder.daily) != 2 {
		t.Fatalf("daily fallback calls=%d seeded=%d", fb.dailyCall.Load(), len(seeder.daily))
	}
}
