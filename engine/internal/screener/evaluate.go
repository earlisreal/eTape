// Package screener evaluates end-of-day swing screens independently of live Scanner boards.
package screener

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func Validate(f wsmsg.ScreenerFilters) error {
	if f.Lookback != "all-time" && f.Lookback != "6-month" && f.Lookback != "1-year" && f.Lookback != "trading-days" {
		return errors.New("choose a valid high lookback")
	}
	if f.Mode != "near" && f.Mode != "new" && f.Mode != "either" {
		return errors.New("choose Near, New, or Either")
	}
	if f.TradingDays < 1 || f.TradingDays > 2520 {
		return errors.New("trading days must be between 1 and 2520")
	}
	if !nonnegative(f.NearPct) || f.NearPct > 100 {
		return errors.New("distance from high must be between 0 and 100%")
	}
	for _, v := range []float64{f.MinPrice, f.MaxPrice, f.MinDollarVolume, f.MinMarketCap, f.MaxMarketCap, f.MaxExtensionPct, f.MinATRPct, f.MaxATRPct} {
		if !nonnegative(v) {
			return errors.New("filter values must be finite and non-negative")
		}
	}
	if (f.MaxPrice > 0 && f.MinPrice > f.MaxPrice) || (f.MaxMarketCap > 0 && f.MinMarketCap > f.MaxMarketCap) || (f.MaxATRPct > 0 && f.MinATRPct > f.MaxATRPct) {
		return errors.New("minimum must not exceed maximum")
	}
	for _, v := range []*float64{f.MinChangePct, f.MaxChangePct} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return errors.New("momentum bounds must be finite")
		}
	}
	if f.MinChangePct != nil && f.MaxChangePct != nil && *f.MinChangePct > *f.MaxChangePct {
		return errors.New("minimum momentum must not exceed maximum")
	}
	if f.ExtensionSMA != 0 && f.ExtensionSMA != 20 && f.ExtensionSMA != 50 && f.ExtensionSMA != 200 {
		return errors.New("extension average must be 20, 50, or 200 days")
	}
	if f.MaxExtensionPct > 0 && f.ExtensionSMA == 0 {
		return errors.New("choose a moving average for the extension limit")
	}
	return nil
}

func nonnegative(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
func positive(v float64) bool    { return v > 0 && nonnegative(v) }

// CompletedDate honors holidays, half days, and exchange time, including DST.
func CompletedDate(now time.Time) time.Time {
	s := session.Schedule(now)
	if s.TradingDay && !now.Before(s.Close) {
		return s.Date
	}
	return session.PreviousTradingDay(now)
}

func lookbackStart(f wsmsg.ScreenerFilters, date time.Time) time.Time {
	switch f.Lookback {
	case "6-month":
		return calendarMonthsBefore(date, 6)
	case "1-year":
		return calendarMonthsBefore(date, 12)
	case "trading-days":
		for i := 0; i < f.TradingDays; i++ {
			date = session.PreviousTradingDay(date)
		}
		return date
	default:
		return time.Date(1900, 1, 1, 0, 0, 0, 0, session.Loc())
	}
}

func calendarMonthsBefore(date time.Time, months int) time.Time {
	first := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, session.Loc()).AddDate(0, -months, 0)
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(date.Day(), last)-1)
}

type Instrument struct {
	Symbol      string
	Name        string
	ListingDate time.Time
	Shares      float64
}

// Evaluate distinguishes a strict new record from a retest. Missing coverage
// is unavailable, never a negative match or a shorter substitute for ATH.
func Evaluate(f wsmsg.ScreenerFilters, stock Instrument, input []feed.Bar, date time.Time) (wsmsg.ScreenerRow, bool, error) {
	row := wsmsg.ScreenerRow{Symbol: stock.Symbol, Name: stock.Name}
	bars := slices.Clone(input)
	slices.SortFunc(bars, func(a, b feed.Bar) int {
		if a.BucketMs < b.BucketMs {
			return -1
		}
		if a.BucketMs > b.BucketMs {
			return 1
		}
		return 0
	})
	clean := bars[:0]
	for _, b := range bars {
		day := session.Schedule(time.UnixMilli(b.BucketMs)).Date
		if day.After(date) {
			continue
		}
		if !positive(b.O) || !positive(b.H) || !positive(b.L) || !positive(b.C) || b.H < max(b.O, b.C) || b.L > min(b.O, b.C) || b.L > b.H || b.Volume < 0 || !nonnegative(b.Turnover) {
			return row, false, errors.New("invalid daily history")
		}
		if len(clean) > 0 && clean[len(clean)-1].BucketMs == day.UnixMilli() {
			return row, false, errors.New("duplicate daily history")
		}
		b.BucketMs = day.UnixMilli()
		clean = append(clean, b)
	}
	bars = clean
	if len(bars) < 2 || bars[len(bars)-1].BucketMs != date.UnixMilli() {
		return row, false, errors.New("completed session is unavailable")
	}
	start := lookbackStart(f, date)
	if f.Lookback == "all-time" && stock.ListingDate.IsZero() {
		return row, false, errors.New("listing date is unavailable for ATH verification")
	}
	if !stock.ListingDate.IsZero() && stock.ListingDate.After(start) {
		start = stock.ListingDate
	}
	coverageStart := start
	if !session.IsTradingDay(coverageStart) {
		coverageStart = session.NextTradingDay(coverageStart)
	}
	if bars[0].BucketMs > coverageStart.UnixMilli() {
		return row, false, errors.New("daily history does not cover the selected high lookback")
	}
	byDate := make(map[int64]feed.Bar, len(bars))
	for _, b := range bars {
		byDate[b.BucketMs] = b
	}
	for day := coverageStart; day.Before(date); day = session.NextTradingDay(day) {
		_, ok := byDate[day.UnixMilli()]
		if !ok {
			return row, false, fmt.Errorf("daily history is missing %s", day.Format("2006-01-02"))
		}
	}
	// Preserve older provider sessions even when the modern offline calendar
	// applies a holiday that did not exist then (for example pre-1998 MLK Day).
	previous := 0.0
	for _, b := range bars {
		if b.BucketMs >= start.UnixMilli() && b.BucketMs < date.UnixMilli() {
			previous = max(previous, b.H)
		}
	}
	if !positive(previous) {
		return row, false, errors.New("previous high is unavailable")
	}
	last := bars[len(bars)-1]
	row.Close, row.PreviousHigh, row.High = last.C, previous, max(previous, last.H)
	row.DistancePct = max(0, (row.High-row.Close)/row.High*100)
	row.NewHigh = last.H > previous
	row.CloseConfirmed = last.C > previous
	row.HistoryStart = time.UnixMilli(bars[0].BucketMs).In(session.Loc()).Format("2006-01-02")
	if positive(stock.Shares) {
		v := stock.Shares * last.C
		if positive(v) {
			row.MarketCap = &v
		}
	}
	// Means require consecutive completed sessions; sparse input cannot make a
	// short history appear to be a 20/50/200-session measure.
	recent := make([]feed.Bar, 0, 201)
	for day := date; ; day = session.PreviousTradingDay(day) {
		b, ok := byDate[day.UnixMilli()]
		if !ok {
			break
		}
		recent = append(recent, b)
		if len(recent) >= 201 {
			break
		}
	}
	sma := func(n int) float64 {
		if len(recent) < n {
			return 0
		}
		total := 0.0
		for _, b := range recent[:n] {
			total += b.C
		}
		return total / float64(n)
	}
	if len(recent) >= 20 {
		total := 0.0
		for _, b := range recent[:20] {
			total += b.C * float64(b.Volume)
		}
		v := total / 20
		if nonnegative(v) {
			row.DollarVolume = &v
		}
	}
	if len(recent) >= 22 {
		v := (last.C/recent[21].C - 1) * 100
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			row.ChangePct = &v
		}
	}
	if len(recent) >= 15 {
		total := 0.0
		for i := 0; i < 14; i++ {
			b := recent[i]
			prev := recent[i+1].C
			total += max(b.H-b.L, math.Abs(b.H-prev), math.Abs(b.L-prev))
		}
		v := total / 14 / last.C * 100
		if nonnegative(v) {
			row.ATRPct = &v
		}
	}
	near := row.DistancePct <= f.NearPct
	breakout := row.NewHigh && (!f.RequireClose || row.CloseConfirmed)
	if (f.Mode == "near" && !near) || (f.Mode == "new" && !breakout) || (f.Mode == "either" && !near && !breakout) {
		return row, false, nil
	}
	if last.C < f.MinPrice || (f.MaxPrice > 0 && last.C > f.MaxPrice) {
		return row, false, nil
	}
	if f.MinDollarVolume > 0 && (row.DollarVolume == nil || *row.DollarVolume < f.MinDollarVolume) {
		return row, false, nil
	}
	if (f.MinMarketCap > 0 || f.MaxMarketCap > 0) && (row.MarketCap == nil || *row.MarketCap < f.MinMarketCap || (f.MaxMarketCap > 0 && *row.MarketCap > f.MaxMarketCap)) {
		return row, false, nil
	}
	for _, rule := range []struct {
		enabled bool
		n       int
	}{{f.AboveSMA20, 20}, {f.AboveSMA50, 50}, {f.AboveSMA200, 200}} {
		if rule.enabled && (sma(rule.n) == 0 || last.C <= sma(rule.n)) {
			return row, false, nil
		}
	}
	if f.MaxExtensionPct > 0 {
		avg := sma(f.ExtensionSMA)
		if avg == 0 || (last.C/avg-1)*100 > f.MaxExtensionPct {
			return row, false, nil
		}
	}
	if (f.MinChangePct != nil || f.MaxChangePct != nil) && (row.ChangePct == nil || (f.MinChangePct != nil && *row.ChangePct < *f.MinChangePct) || (f.MaxChangePct != nil && *row.ChangePct > *f.MaxChangePct)) {
		return row, false, nil
	}
	if (f.MinATRPct > 0 || f.MaxATRPct > 0) && (row.ATRPct == nil || *row.ATRPct < f.MinATRPct || (f.MaxATRPct > 0 && *row.ATRPct > f.MaxATRPct)) {
		return row, false, nil
	}
	return row, true, nil
}
