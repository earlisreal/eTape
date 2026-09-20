package scan

import (
	"math"
	"sort"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

const (
	relativeVolumeLookback = 50
)

// relativeVolumeProfile is a compact full-day baseline for one represented
// snapshot date.
type relativeVolumeProfile struct {
	day      int64
	complete bool
	mean     float64
	count    int
}

// relativeVolumePhase identifies periods where a base daily snapshot can be
// displayed. Overnight belongs to the preceding exchange date for the base
// daily total, but remains valid for the scanner cache.
func relativeVolumePhase(phase session.Phase) bool {
	return phase == session.PreMarket || phase == session.RTH || phase == session.PostMarket || phase == session.Overnight
}

// scannerMetricDate returns the ET trading date represented by the base
// snapshot. Before the regular open, Moomoo can still report the prior day's
// completed daily total; after-hours and overnight keep the completed current
// trading date until the next premarket rollover.
func scannerMetricDate(now time.Time) (time.Time, bool) {
	et := now.In(session.Loc())
	s := session.Schedule(et)
	phase := session.PhaseAt(et)
	if !s.TradingDay {
		return session.PreviousTradingDay(s.Date), true
	}
	if !relativeVolumePhase(phase) {
		return time.Time{}, false
	}
	if phase == session.PreMarket || (phase == session.Overnight && et.Hour() < 4) {
		return session.PreviousTradingDay(s.Date), true
	}
	return s.Date, true
}

func relativeVolumeDays(now time.Time) []time.Time {
	metric, ok := scannerMetricDate(now)
	if !ok {
		return nil
	}
	days := make([]time.Time, relativeVolumeLookback)
	day := session.PreviousTradingDay(metric)
	for i := len(days) - 1; i >= 0; i-- {
		days[i] = day
		day = session.PreviousTradingDay(day)
	}
	return days
}

func relativeVolumeHistoryRange(now time.Time) (from, to time.Time, ok bool) {
	days := relativeVolumeDays(now)
	if len(days) == 0 {
		return time.Time{}, time.Time{}, false
	}
	from = days[0].In(session.Loc())
	to = session.NextTradingDay(days[len(days)-1]).In(session.Loc())
	return from, to, true
}

func firstTradingDayOnOrAfter(day time.Time) time.Time {
	day = day.In(session.Loc())
	for {
		if schedule := session.Schedule(day); schedule.TradingDay {
			return schedule.Date
		}
		day = day.AddDate(0, 0, 1)
	}
}

func addRelativeVolume(a, b int64) (int64, bool) {
	if a < 0 || b < 0 || a > int64(1<<63-1)-b {
		return 0, false
	}
	return a + b, true
}

func dateKey(t time.Time) int64 {
	et := t.In(session.Loc())
	return time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, session.Loc()).UnixMilli()
}

// buildRelativeVolumeProfile accepts bars for the requested completed trading
// dates. A contiguous suffix is valid for genuinely short listing histories;
// missing dates inside that suffix are unavailable and retryable.
func buildRelativeVolumeProfile(now time.Time, bars []feed.Bar, listingDates ...time.Time) (*relativeVolumeProfile, bool) {
	metric, ok := scannerMetricDate(now)
	if !ok {
		return nil, false
	}
	days := relativeVolumeDays(now)
	if len(days) == 0 {
		return nil, false
	}
	wanted := make(map[int64]bool, len(days))
	for _, day := range days {
		wanted[dateKey(day)] = true
	}
	var listingDate time.Time
	if len(listingDates) > 0 {
		listingDate = listingDates[0].In(session.Loc())
		if !listingDate.IsZero() && listingDate.After(metric) {
			return nil, false
		}
	}
	volumes := make(map[int64]int64, len(days))
	for _, bar := range bars {
		key := dateKey(time.UnixMilli(bar.BucketMs))
		if !wanted[key] {
			continue
		}
		if bar.Volume < 0 {
			return nil, false
		}
		if _, duplicate := volumes[key]; duplicate {
			return nil, false
		}
		volumes[key] = bar.Volume
	}
	if len(volumes) == 0 {
		return nil, false
	}
	// A complete response must include the newest requested date. A shorter
	// response is valid only when snapshot listing metadata proves that the
	// symbol was listed after the oldest requested date.
	newest := dateKey(days[len(days)-1])
	if _, ok := volumes[newest]; !ok {
		return nil, false
	}
	keys := make([]int64, 0, len(volumes))
	for key := range volumes {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for i := 1; i < len(keys); i++ {
		if dateKey(session.NextTradingDay(time.UnixMilli(keys[i-1]))) != keys[i] {
			return nil, false
		}
	}
	firstExpected := days[0]
	if !listingDate.IsZero() {
		firstExpected = firstTradingDayOnOrAfter(listingDate)
		if firstExpected.Before(days[0]) {
			firstExpected = days[0]
		}
	}
	if len(keys) < len(days) {
		if listingDate.IsZero() || dateKey(firstExpected) != keys[0] {
			return nil, false
		}
		expectedCount := 0
		for day := firstExpected; !day.After(days[len(days)-1]); day = session.NextTradingDay(day) {
			expectedCount++
		}
		if expectedCount != len(keys) {
			return nil, false
		}
	} else if len(keys) != len(days) || dateKey(firstExpected) != keys[0] {
		return nil, false
	}
	var sum int64
	for _, key := range keys {
		var addOK bool
		sum, addOK = addRelativeVolume(sum, volumes[key])
		if !addOK {
			return nil, false
		}
	}
	profile := &relativeVolumeProfile{
		day:      metric.UnixMilli(),
		complete: true,
		count:    len(keys),
		mean:     float64(sum) / float64(len(keys)),
	}
	if profile.mean <= 0 || math.IsNaN(profile.mean) || math.IsInf(profile.mean, 0) {
		profile.mean = 0
	}
	return profile, true
}

func relativeVolumeAt(profile *relativeVolumeProfile, now time.Time, currentVolume int64) *float64 {
	if profile == nil || currentVolume < 0 {
		return nil
	}
	metric, ok := scannerMetricDate(now)
	if !ok {
		return nil
	}
	return relativeVolumeAtDate(profile, metric.UnixMilli(), currentVolume)
}

func relativeVolumeAtDate(profile *relativeVolumeProfile, metricDay int64, currentVolume int64) *float64 {
	if profile == nil || currentVolume < 0 || metricDay == 0 || profile.day != metricDay {
		return nil
	}
	mean := profile.mean
	if mean <= 0 || math.IsNaN(mean) || math.IsInf(mean, 0) {
		return nil
	}
	value := float64(currentVolume) / mean
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func relativeVolumeProfileHasBaseline(profile *relativeVolumeProfile) bool {
	if profile == nil {
		return false
	}
	return profile.count > 0 && profile.mean > 0 && !math.IsNaN(profile.mean) && !math.IsInf(profile.mean, 0)
}
