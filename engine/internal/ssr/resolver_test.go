package ssr

import (
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

type fakeBars struct {
	mu    sync.Mutex
	bars  []feed.Bar
	err   error
	calls int
}

func (f *fakeBars) ReadRecentDailyBars(_ string, _ int) ([]feed.Bar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return append([]feed.Bar(nil), f.bars...), f.err
}

func (f *fakeBars) set(bars ...feed.Bar) {
	f.mu.Lock()
	f.bars = append([]feed.Bar(nil), bars...)
	f.err = nil
	f.mu.Unlock()
}

func (f *fakeBars) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func et(y int, m time.Month, d, hour, minute int) time.Time {
	return time.Date(y, m, d, hour, minute, 0, 0, session.Loc())
}

func daily(y int, m time.Month, d int, low, close float64) feed.Bar {
	return feed.Bar{
		Symbol:   "US.TEST",
		BucketMs: time.Date(y, m, d, 0, 0, 0, 0, session.Loc()).UnixMilli(),
		L:        low,
		C:        close,
	}
}

func liveLookup(r *Resolver, symbol string, now time.Time, low, priorClose float64) bool {
	return r.IsRestricted(symbol, now, now, low, priorClose)
}

func carryLookup(r *Resolver, symbol string, now time.Time, low, priorClose float64) bool {
	return r.IsRestricted(symbol, now, time.Time{}, low, priorClose)
}

func TestTriggersRule201Threshold(t *testing.T) {
	for _, tc := range []struct {
		name string
		low  float64
		want bool
	}{
		{"just above", 90.01, false},
		{"exactly ten percent", 90, true},
		{"below threshold", 89.99, true},
		{"invalid close", 90, false},
		{"invalid low", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			close := 100.0
			if tc.name == "invalid close" {
				close = 0
			}
			if got := triggersRule201(tc.low, close); got != tc.want {
				t.Fatalf("triggersRule201(%v, %v) = %v, want %v", tc.low, close, got, tc.want)
			}
		})
	}
}

func TestIsRestrictedUsesRegularLowAndSurvivesRecovery(t *testing.T) {
	r := New(nil)
	if liveLookup(r, "US.TEST", et(2026, time.July, 6, 8, 0), 89, 100) {
		t.Fatal("premarket low must not create a trigger")
	}
	if !liveLookup(r, "US.TEST", et(2026, time.July, 6, 10, 0), 89, 100) {
		t.Fatal("RTH low at the threshold must trigger")
	}
	if !liveLookup(r, "US.TEST", et(2026, time.July, 6, 17, 0), 95, 100) {
		t.Fatal("a recovered current price/low update must not clear the trigger")
	}
}

func TestFirstDayIPOHasNoDerivedTrigger(t *testing.T) {
	r := New(nil)
	if liveLookup(r, "US.IPO", et(2026, time.July, 6, 10, 0), 0, 0) {
		t.Fatal("missing prior close/low must not trigger")
	}
}

func TestPreviousTradingDayCarrySkipsHoliday(t *testing.T) {
	// July 3, 2026 is the observed Independence Day holiday, so Monday's
	// previous two sessions are Thursday July 2 and Wednesday July 1.
	bars := &fakeBars{}
	bars.set(
		daily(2026, time.July, 2, 90, 95),
		daily(2026, time.July, 1, 95, 100),
	)
	r := New(bars)
	if !carryLookup(r, "US.TEST", et(2026, time.July, 6, 8, 0), 0, 0) {
		t.Fatal("previous session trigger must carry across the holiday")
	}
}

func TestFridayTriggerCarriesToMonday(t *testing.T) {
	r := New(nil)
	if !liveLookup(r, "US.TEST", et(2026, time.July, 10, 10, 0), 89, 100) {
		t.Fatal("Friday should trigger")
	}
	if carryLookup(r, "US.TEST", et(2026, time.July, 11, 10, 0), 89, 100) ||
		carryLookup(r, "US.TEST", et(2026, time.July, 12, 10, 0), 89, 100) {
		t.Fatal("weekends must not be treated as trading days")
	}
	if !carryLookup(r, "US.TEST", et(2026, time.July, 13, 8, 0), 0, 0) {
		t.Fatal("Friday trigger must carry to Monday")
	}
}

func TestConsecutiveDayRetriggerExtendsRestriction(t *testing.T) {
	bars := &fakeBars{}
	bars.set(
		daily(2026, time.July, 10, 89, 100),
		daily(2026, time.July, 13, 84, 95),
	)
	r := New(bars)
	if !liveLookup(r, "US.TEST", et(2026, time.July, 13, 10, 0), 89, 100) {
		t.Fatal("Monday should trigger")
	}
	if !carryLookup(r, "US.TEST", et(2026, time.July, 14, 8, 0), 0, 0) {
		t.Fatal("Tuesday should carry Monday's trigger")
	}
	if !liveLookup(r, "US.TEST", et(2026, time.July, 14, 10, 0), 84, 95) {
		t.Fatal("Tuesday retrigger should be recognized")
	}
	if !carryLookup(r, "US.TEST", et(2026, time.July, 15, 8, 0), 0, 0) {
		t.Fatal("Tuesday retrigger should carry to Wednesday")
	}
}

func TestNoRetriggerExpiresAfterFollowingTradingDay(t *testing.T) {
	bars := &fakeBars{}
	bars.set(
		daily(2026, time.July, 10, 100, 100),
		daily(2026, time.July, 13, 90, 100),
		daily(2026, time.July, 14, 95, 95),
	)
	r := New(bars)
	if !liveLookup(r, "US.TEST", et(2026, time.July, 13, 10, 0), 90, 100) {
		t.Fatal("Monday should trigger")
	}
	if !carryLookup(r, "US.TEST", et(2026, time.July, 14, 8, 0), 0, 0) {
		t.Fatal("Tuesday should carry Monday's trigger")
	}
	if !liveLookup(r, "US.TEST", et(2026, time.July, 14, 10, 0), 95, 100) {
		t.Fatal("Tuesday remains restricted even without a retrigger")
	}
	if carryLookup(r, "US.TEST", et(2026, time.July, 15, 8, 0), 0, 0) {
		t.Fatal("Monday's trigger must not carry beyond Tuesday")
	}
}

func TestMissingArchiveIsTemporarilyThrottledButRetries(t *testing.T) {
	bars := &fakeBars{}
	r := New(bars)
	when := et(2026, time.July, 7, 8, 0)
	if carryLookup(r, "US.TEST", when, 0, 0) {
		t.Fatal("missing history must not create a false positive")
	}
	if got := bars.callCount(); got != 1 {
		t.Fatalf("initial missing-history calls = %d, want 1", got)
	}
	if carryLookup(r, "US.TEST", when.Add(negativeCarryTTL-time.Second), 0, 0) {
		t.Fatal("missing history should remain temporarily unrestricted")
	}
	if got := bars.callCount(); got != 1 {
		t.Fatalf("pre-TTL missing-history calls = %d, want 1", got)
	}

	bars.set(daily(2026, time.July, 6, 90, 95), daily(2026, time.July, 2, 95, 100))
	if !carryLookup(r, "US.TEST", when.Add(negativeCarryTTL+time.Second), 0, 0) {
		t.Fatal("resolver should retry after history arrives")
	}
	if got := bars.callCount(); got != 2 {
		t.Fatalf("post-TTL missing-history calls = %d, want 2", got)
	}
}

func TestLiveTriggerBypassesMissingHistoryCache(t *testing.T) {
	bars := &fakeBars{}
	r := New(bars)
	premarket := et(2026, time.July, 7, 8, 0)
	if carryLookup(r, "US.TEST", premarket, 0, 0) {
		t.Fatal("missing history must not create a false positive")
	}
	if !r.IsRestricted("US.TEST", et(2026, time.July, 7, 10, 0), et(2026, time.July, 7, 10, 0), 89, 100) {
		t.Fatal("fresh live trigger must bypass the missing-history cache")
	}
	if got := bars.callCount(); got != 1 {
		t.Fatalf("live trigger should not reread history, calls = %d, want 1", got)
	}
}

func TestNegativeCarryCacheRetriesAfterArchivedDailyBarIsCorrected(t *testing.T) {
	bars := &fakeBars{}
	bars.set(daily(2026, time.July, 6, 95, 95), daily(2026, time.July, 2, 95, 100))
	r := New(bars)
	when := et(2026, time.July, 7, 8, 0)

	if carryLookup(r, "US.TEST", when, 0, 0) {
		t.Fatal("initial stale archive should be unrestricted")
	}
	if got := bars.callCount(); got != 1 {
		t.Fatalf("initial carry lookup calls = %d, want 1", got)
	}

	bars.set(daily(2026, time.July, 6, 89, 95), daily(2026, time.July, 2, 95, 100))
	if carryLookup(r, "US.TEST", when.Add(negativeCarryTTL-time.Second), 0, 0) {
		t.Fatal("negative carry result should remain cached before TTL")
	}
	if got := bars.callCount(); got != 1 {
		t.Fatalf("pre-TTL carry lookup calls = %d, want 1", got)
	}

	if !carryLookup(r, "US.TEST", when.Add(negativeCarryTTL+time.Second), 0, 0) {
		t.Fatal("corrected archived low should produce carry after TTL")
	}
	if got := bars.callCount(); got != 2 {
		t.Fatalf("post-TTL carry lookup calls = %d, want 2", got)
	}
}

func TestPositiveCarryCacheDoesNotReread(t *testing.T) {
	bars := &fakeBars{}
	bars.set(daily(2026, time.July, 6, 89, 95), daily(2026, time.July, 2, 95, 100))
	r := New(bars)
	when := et(2026, time.July, 7, 8, 0)

	if !carryLookup(r, "US.TEST", when, 0, 0) {
		t.Fatal("positive carry should be restricted")
	}
	if !carryLookup(r, "US.TEST", when.Add(negativeCarryTTL+time.Second), 0, 0) {
		t.Fatal("positive carry should remain restricted")
	}
	if got := bars.callCount(); got != 1 {
		t.Fatalf("positive carry lookup calls = %d, want 1", got)
	}
}

func TestRestartReconstructsPreviousDayCarry(t *testing.T) {
	bars := &fakeBars{}
	bars.set(daily(2026, time.July, 6, 90, 95), daily(2026, time.July, 2, 95, 100))
	if !carryLookup(New(bars), "US.TEST", et(2026, time.July, 7, 8, 0), 0, 0) {
		t.Fatal("fresh resolver should reconstruct yesterday's trigger")
	}
}

func TestNonUSSymbolNeverTriggersRule201(t *testing.T) {
	if liveLookup(New(nil), "HK.00700", et(2026, time.July, 6, 10, 0), 80, 100) {
		t.Fatal("non-US symbol must not receive derived Rule 201 status")
	}
}

func TestStalePreviousDaySnapshotAfterOpenCannotRetriggerToday(t *testing.T) {
	r := New(nil)
	monday := et(2026, time.July, 6, 10, 0)
	tuesday := et(2026, time.July, 7, 10, 0)
	if !r.IsRestricted("US.TEST", monday, monday, 89, 100) {
		t.Fatal("Monday should trigger")
	}
	if !r.IsRestricted("US.TEST", tuesday, monday, 89, 100) {
		t.Fatal("Monday carry should restrict Tuesday")
	}
	if r.IsRestricted("US.TEST", et(2026, time.July, 8, 8, 0), time.Time{}, 0, 0) {
		t.Fatal("stale Monday snapshot must not extend restriction into Wednesday")
	}
}

func TestCurrentDaySnapshotCanRetriggerCarryDay(t *testing.T) {
	r := New(nil)
	monday := et(2026, time.July, 6, 10, 0)
	tuesday := et(2026, time.July, 7, 10, 0)
	if !r.IsRestricted("US.TEST", monday, monday, 89, 100) {
		t.Fatal("Monday should trigger")
	}
	if !carryLookup(r, "US.TEST", et(2026, time.July, 7, 8, 0), 0, 0) {
		t.Fatal("Monday carry should restrict Tuesday")
	}
	if !r.IsRestricted("US.TEST", tuesday, tuesday, 84, 95) {
		t.Fatal("fresh Tuesday snapshot should retrigger")
	}
	if !carryLookup(r, "US.TEST", et(2026, time.July, 8, 8, 0), 0, 0) {
		t.Fatal("Tuesday retrigger should carry into Wednesday")
	}
}

func TestMissingSnapshotTimestampCannotRetrigger(t *testing.T) {
	r := New(nil)
	tuesday := et(2026, time.July, 7, 10, 0)
	if r.IsRestricted("US.TEST", tuesday, time.Time{}, 89, 100) {
		t.Fatal("missing snapshot timestamp must not create a current-day trigger")
	}
	if carryLookup(r, "US.TEST", et(2026, time.July, 8, 8, 0), 0, 0) {
		t.Fatal("missing timestamp trigger must not carry into Wednesday")
	}
}

func TestResolverConcurrentCalls(t *testing.T) {
	r := New(nil)
	when := et(2026, time.July, 6, 10, 0)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !liveLookup(r, "US.TEST", when, 89, 100) {
				t.Errorf("concurrent lookup returned unrestricted")
			}
		}()
	}
	wg.Wait()
}
