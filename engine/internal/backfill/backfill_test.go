package backfill

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func TestIntradayFromSkipsWeekends(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	if got, want := intradayFrom(now, 1), time.Date(2026, 7, 7, 0, 0, 0, 0, session.Loc()); !got.Equal(want) {
		t.Fatalf("intradayFrom(1) = %s, want %s", got, want)
	}
	if got, want := intradayFrom(now, 3), time.Date(2026, 7, 3, 0, 0, 0, 0, session.Loc()); !got.Equal(want) {
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
		t.Fatalf("calls = %d, want exactly 1 call of 1200", len(calls))
	}
	calls = nil
	seedUnlessCanceled(context.Background(), nil, func(b []feed.Bar) { calls = append(calls, b) })
	if len(calls) != 0 {
		t.Fatalf("empty input produced %d calls", len(calls))
	}
}

func TestSeedUnlessCanceledSkipsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	seedUnlessCanceled(ctx, []feed.Bar{{Symbol: "US.AAPL"}}, func(b []feed.Bar) { calls++ })
	if calls != 0 {
		t.Fatalf("seed calls with a pre-canceled ctx = %d, want 0", calls)
	}
}

// --- test doubles ---

type fakeFetcher struct {
	daily, m1  []feed.Bar
	dErr, mErr error
	m1Calls    atomic.Int32
	dCalls     atomic.Int32
}

func (f *fakeFetcher) DailyBars(_ context.Context, _ string, _, _ time.Time) ([]feed.Bar, error) {
	f.dCalls.Add(1)
	return f.daily, f.dErr
}
func (f *fakeFetcher) Intraday1m(_ context.Context, _ string, _, _ time.Time) ([]feed.Bar, error) {
	f.m1Calls.Add(1)
	return f.m1, f.mErr
}

type fakeTail struct {
	bars  []feed.Bar
	err   error
	calls atomic.Int32
}

func (t *fakeTail) Tail1m(_ context.Context, _ string) ([]feed.Bar, error) {
	t.calls.Add(1)
	return t.bars, t.err
}

type fakeSeeder struct {
	mu          sync.Mutex
	daily, hist []feed.Bar
	older       []feed.Bar
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
func (s *fakeSeeder) SeedOlder1m(_ string, b []feed.Bar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.older = append(s.older, b...)
	s.calls = append(s.calls, "older")
}

type fakeArchive struct {
	mu            sync.Mutex
	daily, m1     []feed.Bar
	ticks         []feed.Tick
	ticksErr      error
	archivedDaily []feed.Bar
	archived1m    []feed.Bar
}

func (a *fakeArchive) ReadDailyBars(_ string) ([]feed.Bar, error) { return a.daily, nil }

// ReadBars1m filters a.m1 by [fromMs, toMs], matching the real
// store.Store.ReadBars1m's ts >= fromMs AND ts <= toMs scan -- callers that
// stash a fixture with timestamps outside a query's actual window must not
// see it echoed back regardless of range.
func (a *fakeArchive) ReadBars1m(_ string, fromMs, toMs int64) ([]feed.Bar, error) {
	var out []feed.Bar
	for _, b := range a.m1 {
		if b.BucketMs >= fromMs && b.BucketMs <= toMs {
			out = append(out, b)
		}
	}
	return out, nil
}
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

func bar(ms int64) feed.Bar   { return feed.Bar{Symbol: "US.AAPL", BucketMs: ms, C: 1} }
func tick(ms int64) feed.Tick { return feed.Tick{Symbol: "US.AAPL", TsMs: ms, Price: 1} }

// chain wraps fetchers as a named Source chain for New.
func chain(fs ...HistFetcher) []Source {
	out := make([]Source, len(fs))
	for i, f := range fs {
		out[i] = Source{Name: fmt.Sprintf("f%d", i), HistFetcher: f}
	}
	return out
}

func fixedNow() time.Time { return time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc()) }

// --- warm-start (unchanged behavior; new New() signature) ---

func TestWarmStartSeedsSessionTicksBeforeDailyAnd1m(t *testing.T) {
	seeder := &fakeSeeder{}
	archive := &fakeArchive{ticks: []feed.Tick{tick(1)}, daily: []feed.Bar{bar(1)}, m1: []feed.Bar{bar(fixedNow().UnixMilli())}}
	o := New(nil, nil, nil, seeder, archive, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	o.warmStart(context.Background(), "US.AAPL", fixedNow().AddDate(0, 0, -20), fixedNow())
	if len(seeder.calls) < 3 || seeder.calls[0] != "ticks" {
		t.Fatalf("call order = %v, want session-ticks first", seeder.calls)
	}
}

func TestWarmStartTickReadErrorContinues(t *testing.T) {
	seeder := &fakeSeeder{}
	archive := &fakeArchive{ticksErr: context.DeadlineExceeded, daily: []feed.Bar{bar(1)}, m1: []feed.Bar{bar(fixedNow().UnixMilli())}}
	o := New(nil, nil, nil, seeder, archive, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	o.warmStart(context.Background(), "US.AAPL", fixedNow().AddDate(0, 0, -20), fixedNow())
	if len(seeder.daily) != 1 || len(seeder.hist) != 1 {
		t.Fatalf("daily=%d hist=%d, want 1 and 1", len(seeder.daily), len(seeder.hist))
	}
}

// --- dailyFrom 2016 floor ---

func TestDailyFromFloorsAt2016(t *testing.T) {
	floor := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
	o := New(nil, nil, nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{DailyYears: 0})
	if got := o.dailyFrom(fixedNow()); !got.Equal(floor) {
		t.Fatalf("dailyFrom(DailyYears=0) = %s, want 2016 floor", got)
	}
	o = New(nil, nil, nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{DailyYears: 100})
	if got := o.dailyFrom(fixedNow()); !got.Equal(floor) {
		t.Fatalf("dailyFrom(DailyYears=100) = %s, want clamp to 2016 floor", got)
	}
	o = New(nil, nil, nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{DailyYears: 1})
	if got, want := o.dailyFrom(fixedNow()), fixedNow().AddDate(-1, 0, 0); !got.Equal(want) {
		t.Fatalf("dailyFrom(DailyYears=1) = %s, want %s (not clamped)", got, want)
	}
}

// --- tail + deep 1m ---

// TestTailSeedsFirstThenDeep proves progressive seeding order (tail before
// deep) and daily-after-1m using a cold archive so warm-start seeds nothing.
func TestTailSeedsFirstThenDeep(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(100)}, daily: []feed.Bar{bar(1)}}
	tail := &fakeTail{bars: []feed.Bar{bar(1000)}}
	seeder := &fakeSeeder{}
	o := New(chain(deep), chain(deep), tail, seeder, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	_ = o.Backfill(context.Background(), "US.AAPL")

	if want := []string{"hist", "hist", "daily"}; fmt.Sprint(seeder.calls) != fmt.Sprint(want) {
		t.Fatalf("seed order = %v, want %v (tail 1m, deep 1m, then daily)", seeder.calls, want)
	}
	// tail bar (1000) seeded before deep bar (100).
	if len(seeder.hist) != 2 || seeder.hist[0].BucketMs != 1000 || seeder.hist[1].BucketMs != 100 {
		t.Fatalf("hist bars = %+v, want tail(1000) then deep(100)", seeder.hist)
	}
}

// TestTailWinsTrimsDeepOverlap: deep bars at/after the tail's oldest bar are
// dropped before seeding/archiving.
func TestTailWinsTrimsDeepOverlap(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(940), bar(1000), bar(1060)}}
	tail := &fakeTail{bars: []feed.Bar{bar(1000), bar(1060)}}
	seeder := &fakeSeeder{}
	archive := &fakeArchive{}
	o := New(chain(deep), chain(deep), tail, seeder, archive, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	_ = o.Backfill(context.Background(), "US.AAPL")

	// hist = tail(2) + deep-trimmed(1: only 940).
	if len(seeder.hist) != 3 {
		t.Fatalf("hist seeded = %d, want 3 (tail 2 + trimmed deep 1)", len(seeder.hist))
	}
	if last := seeder.hist[2]; last.BucketMs != 940 {
		t.Fatalf("trimmed deep bar = %d, want only 940 (strictly older than tail oldest 1000)", last.BucketMs)
	}
	if len(archive.archived1m) != 3 {
		t.Fatalf("archived 1m = %d, want 3", len(archive.archived1m))
	}
}

// TestTailFailUsesDeepUntrimmed: a tail error ⇒ the deep set is seeded whole.
func TestTailFailUsesDeepUntrimmed(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(940), bar(1000)}}
	tail := &fakeTail{err: errors.New("not subscribed")}
	seeder := &fakeSeeder{}
	o := New(chain(deep), chain(deep), tail, seeder, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	_ = o.Backfill(context.Background(), "US.AAPL")

	if tail.calls.Load() != 1 {
		t.Fatalf("tail calls = %d, want 1", tail.calls.Load())
	}
	if len(seeder.hist) != 2 {
		t.Fatalf("hist seeded = %d, want 2 (deep untrimmed, no tail)", len(seeder.hist))
	}
}

// TestNilTailSkipsTailStep: replay/demo (no OpenD) — tail nil, deep untrimmed.
func TestNilTailSkipsTailStep(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(940), bar(1000)}}
	seeder := &fakeSeeder{}
	o := New(chain(deep), chain(deep), nil, seeder, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	_ = o.Backfill(context.Background(), "US.AAPL")
	if len(seeder.hist) != 2 {
		t.Fatalf("hist seeded = %d, want 2 (nil tail skipped)", len(seeder.hist))
	}
}

// --- chain-walk ---

func TestChainWalkAdvancesOnErrorThenEmptyThenServes(t *testing.T) {
	errF := &fakeFetcher{dErr: context.DeadlineExceeded}
	emptyF := &fakeFetcher{} // (nil, nil)
	goodF := &fakeFetcher{daily: []feed.Bar{bar(1), bar(2)}}
	seeder := &fakeSeeder{}
	o := New([]Source{
		{Name: "err", HistFetcher: errF},
		{Name: "empty", HistFetcher: emptyF},
		{Name: "good", HistFetcher: goodF},
	}, nil, nil, seeder, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	if err := o.Backfill(context.Background(), "US.AAPL"); err != nil {
		t.Fatalf("Backfill err = %v, want nil (good served)", err)
	}
	if len(seeder.daily) != 2 {
		t.Fatalf("daily seeded = %d, want 2 (from the 3rd provider)", len(seeder.daily))
	}
	if errF.dCalls.Load() != 1 || emptyF.dCalls.Load() != 1 || goodF.dCalls.Load() != 1 {
		t.Fatalf("chain not walked in order: err=%d empty=%d good=%d", errF.dCalls.Load(), emptyF.dCalls.Load(), goodF.dCalls.Load())
	}
}

// TestDailyChainAllErrorReturnsError pins the uihub re-arm signal: a daily
// error from every provider (e.g. moomoo ErrHistoryQuotaExhausted last resort)
// surfaces so the hub retries on reconnect.
func TestDailyChainAllErrorReturnsError(t *testing.T) {
	a := &fakeFetcher{dErr: context.DeadlineExceeded}
	b := &fakeFetcher{dErr: errors.New("quota exhausted")}
	o := New(chain(a, b), nil, nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	if err := o.Backfill(context.Background(), "US.AAPL"); err == nil {
		t.Fatal("Backfill err = nil, want the last daily error")
	}
}

func TestDailyAllEmptyReturnsNil(t *testing.T) {
	o := New(chain(&fakeFetcher{}, &fakeFetcher{}), nil, nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	if err := o.Backfill(context.Background(), "US.AAPL"); err != nil {
		t.Fatalf("Backfill err = %v, want nil (no data is not a failure)", err)
	}
}

func TestBackfillArchivesFreshFetch(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(10), bar(11)}}
	daily := &fakeFetcher{daily: []feed.Bar{bar(1), bar(2)}}
	archive := &fakeArchive{}
	o := New(chain(daily), chain(deep), nil, &fakeSeeder{}, archive, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	_ = o.Backfill(context.Background(), "US.AAPL")
	if len(archive.archivedDaily) != 2 || len(archive.archived1m) != 2 {
		t.Fatalf("archived daily=%d 1m=%d, want 2 and 2", len(archive.archivedDaily), len(archive.archived1m))
	}
}

func TestRunBoundedPoolCoversEverySymbol(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(10)}}
	o := New(nil, chain(deep), nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{Concurrency: 2, IntradayDays: 20})
	o.Run(context.Background(), []string{"US.AAPL", "US.TSLA", "US.MSFT"})
	if got := deep.m1Calls.Load(); got != 3 {
		t.Fatalf("Intraday1m called %d times, want 3", got)
	}
}
