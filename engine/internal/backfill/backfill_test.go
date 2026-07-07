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
