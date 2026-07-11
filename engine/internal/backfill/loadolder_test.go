package backfill

import (
	"context"
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
	older := watermark.AddDate(0, 0, -20)
	arch := &fakeArchive{m1: []feed.Bar{bar(older.UnixMilli()), bar(older.Add(time.Minute).UnixMilli())}}
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
