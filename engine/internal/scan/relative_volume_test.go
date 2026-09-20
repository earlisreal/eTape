package scan

import (
	"math"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func relativeVolumeET(raw string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, session.Loc())
	if err != nil {
		panic(err)
	}
	return t
}

func dailyBars(days []time.Time, volumes ...int64) []feed.Bar {
	bars := make([]feed.Bar, 0, len(volumes))
	for i, volume := range volumes {
		day := days[len(days)-len(volumes)+i]
		bars = append(bars, feed.Bar{Symbol: "US.TEST", BucketMs: day.Add(12 * time.Hour).UnixMilli(), Volume: volume})
	}
	return bars
}

func TestRelativeVolumeUsesFiftyCompletedDays(t *testing.T) {
	now := relativeVolumeET("2026-07-08 10:00:00")
	days := relativeVolumeDays(now)
	if len(days) != 50 {
		t.Fatalf("got %d dates, want 50", len(days))
	}
	if !days[len(days)-1].Equal(session.PreviousTradingDay(session.Schedule(now).Date)) {
		t.Fatalf("newest denominator date=%v", days[len(days)-1])
	}
	from, to, ok := relativeVolumeHistoryRange(now)
	if !ok || !from.Equal(days[0]) || !to.Equal(session.NextTradingDay(days[len(days)-1])) {
		t.Fatalf("range=%v..%v ok=%v", from, to, ok)
	}

	volumes := make([]int64, len(days))
	for i := range volumes {
		volumes[i] = int64(i + 1)
	}
	bars := dailyBars(days, volumes...)
	// This current-day bar must be ignored because the numerator date is not
	// part of its own denominator.
	bars = append(bars, feed.Bar{BucketMs: session.Schedule(now).Date.Add(12 * time.Hour).UnixMilli(), Volume: 999999})
	profile, ok := buildRelativeVolumeProfile(now, bars)
	if !ok || profile.count != 50 || profile.mean != 25.5 {
		t.Fatalf("profile=%+v ok=%v", profile, ok)
	}
	got := relativeVolumeAt(profile, now, 255)
	if got == nil || math.Abs(*got-10) > 1e-12 {
		t.Fatalf("relative volume=%v, want 10", got)
	}
}

func TestRelativeVolumeAcceptsContiguousShortHistory(t *testing.T) {
	now := relativeVolumeET("2026-07-08 10:00:00")
	days := relativeVolumeDays(now)
	bars := dailyBars(days[len(days)-3:], 100, 200, 300)
	if _, ok := buildRelativeVolumeProfile(now, bars); ok {
		t.Fatal("short history without listing metadata accepted")
	}
	profile, ok := buildRelativeVolumeProfile(now, bars, days[len(days)-3])
	if !ok || profile.count != 3 || profile.mean != 200 {
		t.Fatalf("short profile=%+v ok=%v", profile, ok)
	}

	gap := dailyBars([]time.Time{days[len(days)-3], days[len(days)-1]}, 100, 300)
	if _, ok := buildRelativeVolumeProfile(now, gap, days[len(days)-3]); ok {
		t.Fatal("missing date inside returned history accepted")
	}
	if _, ok := buildRelativeVolumeProfile(now, []feed.Bar{{BucketMs: days[len(days)-1].Add(12 * time.Hour).UnixMilli(), Volume: 0}}, days[len(days)-1]); !ok {
		t.Fatal("single zero-volume listing day should be accepted")
	} else if relativeVolumeProfileHasBaseline(&relativeVolumeProfile{count: 1, mean: 0}) {
		t.Fatal("zero mean should have no baseline")
	}
}

func TestRelativeVolumeRejectsInvalidOrStaleHistory(t *testing.T) {
	now := relativeVolumeET("2026-07-08 10:00:00")
	days := relativeVolumeDays(now)
	valid := dailyBars(days[len(days)-2:], 10, 20)
	duplicate := append(append([]feed.Bar(nil), valid...), valid[0])
	if _, ok := buildRelativeVolumeProfile(now, duplicate); ok {
		t.Fatal("duplicate daily bucket accepted")
	}
	negative := dailyBars(days[len(days)-1:], -1)
	if _, ok := buildRelativeVolumeProfile(now, negative); ok {
		t.Fatal("negative daily volume accepted")
	}
	profile, ok := buildRelativeVolumeProfile(now, valid, days[len(days)-2])
	if !ok {
		t.Fatal("valid profile unavailable")
	}
	if relativeVolumeAt(profile, relativeVolumeET("2026-07-07 10:00:00"), 10) != nil {
		t.Fatal("profile used for a different represented date")
	}
}

func TestRelativeVolumeDateMappingUsesPriorDayBeforeOpen(t *testing.T) {
	premarket := relativeVolumeET("2026-07-08 08:00:00")
	got, ok := scannerMetricDate(premarket)
	if !ok || !got.Equal(session.PreviousTradingDay(session.Schedule(premarket).Date)) {
		t.Fatalf("premarket metric date=%v ok=%v", got, ok)
	}
	rth, ok := scannerMetricDate(relativeVolumeET("2026-07-08 10:00:00"))
	if !ok || !rth.Equal(session.Schedule(premarket).Date) {
		t.Fatalf("RTH metric date=%v ok=%v", rth, ok)
	}
	weekendNow := relativeVolumeET("2026-07-11 12:00:00")
	weekend, ok := scannerMetricDate(weekendNow)
	if !ok || !weekend.Equal(session.PreviousTradingDay(weekendNow)) {
		t.Fatalf("weekend metric date=%v ok=%v", weekend, ok)
	}
}
