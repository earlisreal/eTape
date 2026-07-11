package backfill

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestLoadOlderErrorsWithoutWatermark(t *testing.T) {
	o := New(nil, nil, &fakeTail{}, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{})
	_, _, err := o.LoadOlder(context.Background(), "US.AAPL")
	if err == nil {
		t.Fatalf("want error when no watermark exists")
	}
}

func TestLoadOlderArchiveFirstServesWithoutChain(t *testing.T) {
	watermark := fixedNow().AddDate(0, 0, -20)
	from := intradayFrom(watermark, 20)
	// Archive's earliest bar sits right at `from` -- squarely within the
	// archive-coverage slack, so this exercises the genuine archive-first-wins
	// path (contrast with TestLoadOlderArchiveFirstFallsThroughOnSparseSlice,
	// which exercises the fall-through path for a slice that does NOT cover
	// the window).
	arch := &fakeArchive{m1: []feed.Bar{bar(from.UnixMilli()), bar(from.Add(time.Minute).UnixMilli())}}
	seed := &fakeSeeder{}
	// nil intraday chain => archive-only; walkChain over nil returns (nil,"",nil).
	o := New(nil, nil, &fakeTail{}, seed, arch, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	o.noteBackfilled("US.AAPL", watermark)
	added, exhausted, err := o.LoadOlder(context.Background(), "US.AAPL")
	if err != nil || added == 0 || exhausted {
		t.Fatalf("archive-first should serve: added=%d exhausted=%v err=%v", added, exhausted, err)
	}
	if len(seed.older) == 0 {
		t.Fatalf("SeedOlder1m not called")
	}
}

// TestLoadOlderArchiveFirstFallsThroughOnSparseSlice proves a stray/gappy
// archive slice that does NOT reach back near `from` is not mistaken for
// coverage of the requested window: LoadOlder falls through to the provider
// chain, and the CHAIN's bars -- not the stray archive slice -- are what get
// seeded and used to advance the watermark. This is the Critical bug's
// regression case: the archive is written by 3 independent, uncoordinated
// call sites (tail1m, fill1m, loadOlder), so a sparse/stale slice inside a
// requested window is realistic, not contrived.
func TestLoadOlderArchiveFirstFallsThroughOnSparseSlice(t *testing.T) {
	watermark := fixedNow().AddDate(0, 0, -20)
	from := intradayFrom(watermark, 20)

	// Stray bar sits just below `cur` (watermark), nowhere near `from` -- well
	// outside the archive-coverage slack, so it must not count as coverage.
	strayMs := watermark.Add(-time.Minute).UnixMilli()
	arch := &fakeArchive{m1: []feed.Bar{bar(strayMs)}}

	chainBars := []feed.Bar{bar(from.UnixMilli()), bar(from.Add(time.Minute).UnixMilli())}
	fetcher := &fakeFetcher{m1: chainBars}
	seed := &fakeSeeder{}
	o := New(nil, chain(fetcher), &fakeTail{}, seed, arch, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	o.noteBackfilled("US.AAPL", watermark)

	added, exhausted, err := o.LoadOlder(context.Background(), "US.AAPL")
	if err != nil {
		t.Fatalf("LoadOlder err = %v, want nil", err)
	}
	if exhausted {
		t.Fatalf("exhausted = true, want false (from is above the 2016 floor)")
	}
	if added != len(chainBars) {
		t.Fatalf("added = %d, want %d (chain bars, not the stray archive slice)", added, len(chainBars))
	}
	if fetcher.m1Calls.Load() != 1 {
		t.Fatalf("chain fetcher calls = %d, want 1 (fallthrough must reach the provider)", fetcher.m1Calls.Load())
	}
	if len(seed.older) != len(chainBars) || seed.older[0].BucketMs != chainBars[0].BucketMs {
		t.Fatalf("seeded older bars = %+v, want the chain's bars %+v", seed.older, chainBars)
	}
	if len(arch.archived1m) != len(chainBars) {
		t.Fatalf("archived 1m = %d, want %d (the chain's bars, not the stray slice re-archived)", len(arch.archived1m), len(chainBars))
	}
	o.mu.Lock()
	got := o.oldest1m["US.AAPL"]
	o.mu.Unlock()
	if got != from.UnixMilli() {
		t.Fatalf("watermark = %d, want %d (advanced to the chain's from, not the stray archive slice)", got, from.UnixMilli())
	}
}

// TestLoadOlderProviderErrorDoesNotAdvanceWatermark proves a genuine
// every-provider-errored failure (transient, not "no more history exists")
// returns a non-nil error, does not mark exhausted, and -- critically --
// does not advance the watermark, so a retry naturally re-attempts the exact
// same [from, to) window instead of silently skipping past it.
func TestLoadOlderProviderErrorDoesNotAdvanceWatermark(t *testing.T) {
	watermark := fixedNow().AddDate(0, 0, -20)
	failing := &fakeFetcher{mErr: errors.New("transient failure")}
	o := New(nil, chain(failing), &fakeTail{}, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	o.noteBackfilled("US.AAPL", watermark)

	added, exhausted, err := o.LoadOlder(context.Background(), "US.AAPL")
	if err == nil {
		t.Fatalf("want a non-nil error when every provider fails")
	}
	if exhausted {
		t.Fatalf("exhausted = true, want false on a genuine provider error")
	}
	if added != 0 {
		t.Fatalf("added = %d, want 0", added)
	}
	o.mu.Lock()
	got := o.oldest1m["US.AAPL"]
	o.mu.Unlock()
	if got != watermark.UnixMilli() {
		t.Fatalf("watermark = %d, want unchanged %d (must not advance past a window a retry still needs to cover)", got, watermark.UnixMilli())
	}

	// A subsequent call recomputes from the unchanged watermark, so it
	// re-attempts the identical window -- proven here by the fetcher being
	// asked again (same deterministic from/to derived from the watermark).
	added2, exhausted2, err2 := o.LoadOlder(context.Background(), "US.AAPL")
	if err2 == nil || exhausted2 || added2 != 0 {
		t.Fatalf("retry = added=%d exhausted=%v err=%v, want the same failing outcome", added2, exhausted2, err2)
	}
	if failing.m1Calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2 (retry re-attempted the same window)", failing.m1Calls.Load())
	}
}

func TestLoadOlderExhaustsAtFloor(t *testing.T) {
	o := New(nil, nil, &fakeTail{}, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{})
	o.noteBackfilled("US.AAPL", dailyFloor) // watermark already at 2016 floor
	_, exhausted, err := o.LoadOlder(context.Background(), "US.AAPL")
	if err != nil || !exhausted {
		t.Fatalf("want exhausted at floor, got exhausted=%v err=%v", exhausted, err)
	}
}

func TestLoadOlderExhaustsWhenArchiveAndChainEmpty(t *testing.T) {
	watermark := fixedNow().AddDate(0, 0, -20)
	o := New(nil, nil, &fakeTail{}, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	o.noteBackfilled("US.AAPL", watermark) // empty archive + nil chain
	_, exhausted, err := o.LoadOlder(context.Background(), "US.AAPL")
	if err != nil || !exhausted {
		t.Fatalf("want exhausted (pre-listing), got exhausted=%v err=%v", exhausted, err)
	}
}

func TestLoadOlderDailyOneShot(t *testing.T) {
	pre2016 := []feed.Bar{bar(dailyFloor.AddDate(-1, 0, 0).UnixMilli())}
	src := &fakeFetcher{daily: pre2016}
	seed := &fakeSeeder{}
	o := New(chain(src), nil, &fakeTail{}, seed, &fakeArchive{}, clock.NewFake(fixedNow()), Config{})
	o.noteBackfilled("US.KO", fixedNow().AddDate(0, 0, -20))
	added, exhausted, err := o.LoadOlderDaily(context.Background(), "US.KO")
	if err != nil || added == 0 || !exhausted {
		t.Fatalf("daily one-shot: added=%d exhausted=%v err=%v", added, exhausted, err)
	}
	// Second call must be a no-op exhausted (one-shot) -- src.daily must NOT be re-fetched.
	added2, exhausted2, _ := o.LoadOlderDaily(context.Background(), "US.KO")
	if added2 != 0 || !exhausted2 {
		t.Fatalf("second daily call should be exhausted no-op")
	}
}
