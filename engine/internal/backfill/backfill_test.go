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

func TestSeedUnlessCanceledCallsSeedOnce(t *testing.T) {
	bars := make([]feed.Bar, 1200)
	for i := range bars {
		bars[i] = feed.Bar{Symbol: "US.AAPL", BucketMs: int64(i)}
	}
	var calls [][]feed.Bar
	seedUnlessCanceled(context.Background(), bars, func(b []feed.Bar) {
		calls = append(calls, append([]feed.Bar(nil), b...))
	})
	if len(calls) != 1 || len(calls[0]) != 1200 {
		t.Fatalf("calls = %d (sizes %v), want exactly 1 call of 1200", len(calls), calls)
	}
	for i := range calls[0] {
		if calls[0][i].BucketMs != int64(i) {
			t.Fatalf("order broken at %d: %d", i, calls[0][i].BucketMs)
		}
	}
	// Empty input => no call.
	calls = nil
	seedUnlessCanceled(context.Background(), nil, func(b []feed.Bar) { calls = append(calls, b) })
	if len(calls) != 0 {
		t.Fatalf("empty input produced %d calls", len(calls))
	}
}

func TestSeedUnlessCanceledSkipsOnCanceledContext(t *testing.T) {
	bars := []feed.Bar{{Symbol: "US.AAPL", BucketMs: 0}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before seeding starts
	var calls int
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { calls++ })
	if calls != 0 {
		t.Fatalf("seed calls with a pre-canceled ctx = %d, want 0", calls)
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

// fakeSeeder records seeded bars/ticks per method, plus an ordered call-tag
// log so tests can assert relative ordering across methods (e.g.
// SeedSessionTicks before SeedDaily/SeedHistory1m).
type fakeSeeder struct {
	mu          sync.Mutex
	daily, hist []feed.Bar
	ticks       []feed.Tick
	calls       []string
}

func (s *fakeSeeder) SeedSessionTicks(_ string, t []feed.Tick) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ticks = append(s.ticks, t...)
	s.calls = append(s.calls, "ticks")
}
func (s *fakeSeeder) SeedDaily(_ string, b []feed.Bar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.daily = append(s.daily, b...)
	s.calls = append(s.calls, "daily")
}
func (s *fakeSeeder) SeedHistory1m(_ string, b []feed.Bar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hist = append(s.hist, b...)
	s.calls = append(s.calls, "hist")
}

// fakeArchive returns canned warm-start bars and records what gets archived
// (ArchiveBar1m/ArchiveDaily), so tests can assert freshly-fetched history is
// persisted at the source now that it no longer rides the per-bar BarUpdate
// emit path (see the Archive interface's doc comment in backfill.go).
type fakeArchive struct {
	mu            sync.Mutex
	daily, m1     []feed.Bar
	ticks         []feed.Tick
	ticksErr      error
	archivedDaily []feed.Bar
	archived1m    []feed.Bar
}

func (a *fakeArchive) ReadDailyBars(_ string) ([]feed.Bar, error)          { return a.daily, nil }
func (a *fakeArchive) ReadBars1m(_ string, _, _ int64) ([]feed.Bar, error) { return a.m1, nil }
func (a *fakeArchive) ReadJournalTicks(_ string, _ int64) ([]feed.Tick, error) {
	return a.ticks, a.ticksErr
}
func (a *fakeArchive) ArchiveBar1m(b feed.Bar) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.archived1m = append(a.archived1m, b)
}
func (a *fakeArchive) ArchiveDaily(b feed.Bar) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.archivedDaily = append(a.archivedDaily, b)
}

func bar(ms int64) feed.Bar { return feed.Bar{Symbol: "US.AAPL", BucketMs: ms, C: 1} }

func tick(tsMs int64) feed.Tick { return feed.Tick{Symbol: "US.AAPL", TsMs: tsMs, Price: 1} }

func TestWarmStartSeedsSessionTicksFromJournal(t *testing.T) {
	seeder := &fakeSeeder{}
	archive := &fakeArchive{ticks: []feed.Tick{tick(1), tick(2)}}
	o := New(&fakeFetcher{}, nil, seeder, archive, clock.NewFake(time.Now()), Config{IntradayDays: 20})
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	o.warmStart(context.Background(), "US.AAPL", now.AddDate(0, 0, -20), now)

	if len(seeder.ticks) != 2 || seeder.ticks[0].TsMs != 1 || seeder.ticks[1].TsMs != 2 {
		t.Fatalf("session ticks seeded = %+v, want the 2 ticks ReadJournalTicks returned", seeder.ticks)
	}
}

func TestWarmStartSeedsSessionTicksBeforeDailyAnd1m(t *testing.T) {
	seeder := &fakeSeeder{}
	archive := &fakeArchive{
		ticks: []feed.Tick{tick(1)},
		daily: []feed.Bar{bar(1)},
		m1:    []feed.Bar{bar(2)},
	}
	o := New(&fakeFetcher{}, nil, seeder, archive, clock.NewFake(time.Now()), Config{IntradayDays: 20})
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	o.warmStart(context.Background(), "US.AAPL", now.AddDate(0, 0, -20), now)

	if len(seeder.calls) < 3 || seeder.calls[0] != "ticks" {
		t.Fatalf("call order = %v, want session-ticks seed first", seeder.calls)
	}
}

func TestWarmStartNoTicksSkipsSeedSessionTicks(t *testing.T) {
	seeder := &fakeSeeder{}
	archive := &fakeArchive{} // ReadJournalTicks returns (nil, nil) -- cold symbol
	o := New(&fakeFetcher{}, nil, seeder, archive, clock.NewFake(time.Now()), Config{IntradayDays: 20})
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	o.warmStart(context.Background(), "US.AAPL", now.AddDate(0, 0, -20), now)

	if len(seeder.ticks) != 0 {
		t.Fatalf("session ticks seeded = %+v, want none", seeder.ticks)
	}
	for _, c := range seeder.calls {
		if c == "ticks" {
			t.Fatalf("SeedSessionTicks called despite no journaled ticks, calls=%v", seeder.calls)
		}
	}
}

func TestWarmStartTickReadErrorContinuesToDailyAnd1m(t *testing.T) {
	seeder := &fakeSeeder{}
	archive := &fakeArchive{
		ticksErr: context.DeadlineExceeded,
		daily:    []feed.Bar{bar(1)},
		m1:       []feed.Bar{bar(2)},
	}
	o := New(&fakeFetcher{}, nil, seeder, archive, clock.NewFake(time.Now()), Config{IntradayDays: 20})
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	o.warmStart(context.Background(), "US.AAPL", now.AddDate(0, 0, -20), now)

	if len(seeder.daily) != 1 || len(seeder.hist) != 1 {
		t.Fatalf("daily=%d hist=%d, want 1 and 1 -- a tick-read failure must not abort warm-start", len(seeder.daily), len(seeder.hist))
	}
	if len(seeder.ticks) != 0 {
		t.Fatalf("session ticks seeded despite read error: %+v", seeder.ticks)
	}
}

func TestWarmStartSkipsSessionTicksOnCanceledContext(t *testing.T) {
	seeder := &fakeSeeder{}
	archive := &fakeArchive{ticks: []feed.Tick{tick(1)}}
	o := New(&fakeFetcher{}, nil, seeder, archive, clock.NewFake(time.Now()), Config{IntradayDays: 20})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before warmStart runs
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	o.warmStart(ctx, "US.AAPL", now.AddDate(0, 0, -20), now)

	if len(seeder.ticks) != 0 {
		t.Fatalf("session ticks seeded on canceled ctx = %+v, want none", seeder.ticks)
	}
}

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
	_ = o.Backfill(context.Background(), "US.AAPL")

	// Daily: warm-start(1) + moomoo(2) = 3 seeded.
	if len(seeder.daily) != 3 {
		t.Fatalf("daily seeded = %d, want 3", len(seeder.daily))
	}
	// 1m: warm-start(1) + moomoo(3) = 4 seeded.
	if len(seeder.hist) != 4 {
		t.Fatalf("1m seeded = %d, want 4", len(seeder.hist))
	}
}

// TestBackfillArchivesFreshFetchNotWarmStart verifies the source-side
// archiving added alongside the BarSnapshot fix: freshly-fetched (primary)
// history is persisted via Archive*, while warm-start bars (already read
// FROM that same archive) are not redundantly re-archived.
func TestBackfillArchivesFreshFetchNotWarmStart(t *testing.T) {
	primary := &fakeFetcher{
		daily: []feed.Bar{bar(1), bar(2)},
		m1:    []feed.Bar{bar(10), bar(11)},
	}
	seeder := &fakeSeeder{}
	archive := &fakeArchive{
		daily: []feed.Bar{bar(0)}, // warm-start only
		m1:    []feed.Bar{bar(9)}, // warm-start only
	}
	o := New(primary, nil, seeder, archive, clock.NewFake(time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())), Config{IntradayDays: 20})
	_ = o.Backfill(context.Background(), "US.AAPL")

	if len(archive.archivedDaily) != 2 || archive.archivedDaily[0].BucketMs != 1 || archive.archivedDaily[1].BucketMs != 2 {
		t.Fatalf("archived daily = %+v, want the 2 fresh-fetch bars only", archive.archivedDaily)
	}
	if len(archive.archived1m) != 2 || archive.archived1m[0].BucketMs != 10 || archive.archived1m[1].BucketMs != 11 {
		t.Fatalf("archived 1m = %+v, want the 2 fresh-fetch bars only", archive.archived1m)
	}
}

func TestBackfillPrimaryDailyErrorIsNonFatal(t *testing.T) {
	primary := &fakeFetcher{dErr: context.DeadlineExceeded, m1: []feed.Bar{bar(10)}}
	seeder := &fakeSeeder{}
	o := New(primary, nil, seeder, &fakeArchive{}, clock.NewFake(time.Now()), Config{IntradayDays: 20, SeedChunk: 500})
	_ = o.Backfill(context.Background(), "US.AAPL") // must not panic
	if len(seeder.daily) != 0 {
		t.Fatalf("daily seeded on error = %d, want 0", len(seeder.daily))
	}
	if len(seeder.hist) != 1 {
		t.Fatalf("1m still seeded = %d, want 1", len(seeder.hist))
	}
}

// TestBackfillReturnsDailyError pins the outcome Backfill reports back to a
// caller (the uihub uses this to decide whether a symbol's daily backfill
// must be retried): nil once daily bars were seeded from either source,
// otherwise the last error hit trying.
func TestBackfillReturnsDailyError(t *testing.T) {
	t.Run("primary fails, no fallback -> error, 1m still runs", func(t *testing.T) {
		primary := &fakeFetcher{dErr: context.DeadlineExceeded, m1: []feed.Bar{bar(10)}}
		seeder := &fakeSeeder{}
		o := New(primary, nil, seeder, &fakeArchive{}, clock.NewFake(time.Now()), Config{IntradayDays: 20, SeedChunk: 500})
		err := o.Backfill(context.Background(), "US.AAPL")
		if err == nil {
			t.Fatal("Backfill err = nil, want the primary daily error")
		}
		if primary.m1Calls.Load() != 1 {
			t.Fatalf("m1Calls = %d, want 1 -- fill1m must still run despite the daily error", primary.m1Calls.Load())
		}
	})

	t.Run("primary fails, fallback fails -> error", func(t *testing.T) {
		primary := &fakeFetcher{dErr: context.DeadlineExceeded}
		fallback := &fakeFetcher{dErr: context.DeadlineExceeded}
		seeder := &fakeSeeder{}
		o := New(primary, fallback, seeder, &fakeArchive{}, clock.NewFake(time.Now()), Config{IntradayDays: 20})
		if err := o.Backfill(context.Background(), "US.AAPL"); err == nil {
			t.Fatal("Backfill err = nil, want the fallback daily error")
		}
	})

	t.Run("primary fails, fallback succeeds -> nil", func(t *testing.T) {
		primary := &fakeFetcher{dErr: context.DeadlineExceeded}
		fallback := &fakeFetcher{daily: []feed.Bar{bar(1)}}
		seeder := &fakeSeeder{}
		o := New(primary, fallback, seeder, &fakeArchive{}, clock.NewFake(time.Now()), Config{IntradayDays: 20})
		if err := o.Backfill(context.Background(), "US.AAPL"); err != nil {
			t.Fatalf("Backfill err = %v, want nil (fallback seeded daily bars)", err)
		}
	})

	t.Run("primary succeeds -> nil", func(t *testing.T) {
		primary := &fakeFetcher{daily: []feed.Bar{bar(1)}}
		seeder := &fakeSeeder{}
		o := New(primary, nil, seeder, &fakeArchive{}, clock.NewFake(time.Now()), Config{IntradayDays: 20})
		if err := o.Backfill(context.Background(), "US.AAPL"); err != nil {
			t.Fatalf("Backfill err = %v, want nil", err)
		}
	})
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

// recordFallback lets a test give the primary and fallback different data and
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
	_ = o.Backfill(context.Background(), "US.AAPL")

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
	_ = o.Backfill(context.Background(), "US.AAPL")
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
	_ = o.Backfill(context.Background(), "US.AAPL")

	// 1m: fallback asked for the whole [from, now] window.
	if fb.m1Calls.Load() != 1 || fb.m1From.Load() != from.UnixMilli() || fb.m1To.Load() != now.UnixMilli() {
		t.Fatalf("fallback 1m range = [%d,%d) calls=%d", fb.m1From.Load(), fb.m1To.Load(), fb.m1Calls.Load())
	}
	// Daily: primary errored, fallback daily used (2 bars).
	if fb.dailyCall.Load() != 1 || len(seeder.daily) != 2 {
		t.Fatalf("daily fallback calls=%d seeded=%d", fb.dailyCall.Load(), len(seeder.daily))
	}
}
