package screener

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func testDate(raw string) time.Time {
	day, err := time.ParseInLocation("2006-01-02", raw, session.Loc())
	if err != nil {
		panic(err)
	}
	return day
}
func testFilters() wsmsg.ScreenerFilters {
	return wsmsg.ScreenerFilters{Lookback: "all-time", TradingDays: 126, Mode: "either", NearPct: 5, RequireClose: true}
}
func history(date time.Time, n int) (Instrument, []feed.Bar) {
	days := []time.Time{date}
	for len(days) < n {
		days = append(days, session.PreviousTradingDay(days[len(days)-1]))
	}
	slices.Reverse(days)
	bars := make([]feed.Bar, 0, n)
	for _, day := range days {
		bars = append(bars, feed.Bar{Symbol: "US.TEST", BucketMs: day.UnixMilli(), O: 95, H: 100, L: 90, C: 97, Volume: 1_000_000, Turnover: 97_000_000})
	}
	return Instrument{Symbol: "US.TEST", ListingDate: days[0], Shares: 10_000_000}, bars
}

func TestHighModesAndStrictBreakouts(t *testing.T) {
	date := testDate("2026-11-04")
	for _, tc := range []struct {
		name, mode            string
		high, close, near     float64
		confirm, match, isNew bool
	}{
		{"inclusive near", "near", 99, 95, 5, true, true, false},
		{"outside near", "near", 99, 94, 5, true, false, false},
		{"retest is not new", "new", 100, 99, 5, false, false, false},
		{"intraday breakout", "new", 101, 99, 5, false, true, true},
		{"faded close excluded", "new", 101, 99, 5, true, false, true},
		{"close exactly at prior record", "new", 101, 100, 5, true, false, true},
		{"confirmed close", "new", 102, 101, 5, true, true, true},
		{"either preserves near branch", "either", 101, 99, 5, true, true, true},
		{"either preserves far faded breakout", "either", 110, 91, 1, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stock, bars := history(date, 30)
			bars[len(bars)-1].H = tc.high
			bars[len(bars)-1].C = tc.close
			f := testFilters()
			f.Mode = tc.mode
			f.NearPct = tc.near
			f.RequireClose = tc.confirm
			row, match, err := Evaluate(f, stock, bars, date)
			if err != nil || match != tc.match || row.NewHigh != tc.isNew || row.PreviousHigh != 100 {
				t.Fatalf("row=%+v match=%v err=%v", row, match, err)
			}
		})
	}
}

func TestLookbacksExcludeSignalSessionAndRespectBoundary(t *testing.T) {
	date := testDate("2026-11-04")
	stock, bars := history(date, 300)
	bars[0].H = 200
	bars[len(bars)-1].H = 110
	bars[len(bars)-1].C = 105
	f := testFilters()
	f.Mode = "new"
	if _, match, err := Evaluate(f, stock, bars, date); err != nil || match {
		t.Fatalf("ATH match=%v err=%v", match, err)
	}
	for _, lookback := range []string{"6-month", "1-year", "trading-days"} {
		f.Lookback = lookback
		f.TradingDays = 90
		row, match, err := Evaluate(f, stock, bars, date)
		if err != nil || !match || row.PreviousHigh != 100 {
			t.Fatalf("%s row=%+v match=%v err=%v", lookback, row, match, err)
		}
	}
	f.Lookback = "trading-days"
	f.TradingDays = 5
	cutoff := lookbackStart(f, date)
	for i := range bars {
		if bars[i].BucketMs == cutoff.UnixMilli() {
			bars[i].H = 120
		}
	}
	if _, match, err := Evaluate(f, stock, bars, date); err != nil || match {
		t.Fatalf("inclusive oldest session match=%v err=%v", match, err)
	}
	if got := calendarMonthsBefore(testDate("2026-08-31"), 6); !got.Equal(testDate("2026-02-28")) {
		t.Fatal(got)
	}
}

func TestATHPreservesSessionsBeforeModernHolidayRules(t *testing.T) {
	date := testDate("1997-01-22")
	stock, bars := history(date, 5)
	for i := range bars {
		if bars[i].BucketMs == testDate("1997-01-20").UnixMilli() {
			bars[i].H = 200
		}
	}
	row, _, err := Evaluate(testFilters(), stock, bars, date)
	if err != nil || row.PreviousHigh != 200 {
		t.Fatalf("row=%+v err=%v", row, err)
	}
}

func TestHistoricalMarketClosureDoesNotInvalidateATHCoverage(t *testing.T) {
	date := testDate("1994-04-28")
	stock := Instrument{Symbol: "US.TEST", ListingDate: testDate("1994-04-25")}
	var bars []feed.Bar
	for _, raw := range []string{"1994-04-25", "1994-04-26", "1994-04-28"} {
		bars = append(bars, feed.Bar{Symbol: stock.Symbol, BucketMs: testDate(raw).UnixMilli(), O: 95, H: 100, L: 90, C: 97, Volume: 1000})
	}
	bars[2].H = 102
	bars[2].C = 101
	f := testFilters()
	f.Mode = "new"
	row, match, err := Evaluate(f, stock, bars, date)
	if err != nil || !match || row.PreviousHigh != 100 {
		t.Fatalf("row=%+v match=%v err=%v", row, match, err)
	}
}

func TestMissingHistoryFailsClosed(t *testing.T) {
	date := testDate("2026-11-04")
	for _, tc := range []string{"truncated", "gap", "latest missing", "listing missing", "invalid", "duplicate"} {
		t.Run(tc, func(t *testing.T) {
			stock, bars := history(date, 30)
			switch tc {
			case "truncated":
				bars = bars[1:]
			case "gap":
				bars = append(bars[:10], bars[11:]...)
			case "latest missing":
				bars = bars[:len(bars)-1]
			case "listing missing":
				stock.ListingDate = time.Time{}
			case "invalid":
				bars[0].H = math.NaN()
			case "duplicate":
				bars = append(bars, bars[10])
			}
			if _, match, err := Evaluate(testFilters(), stock, bars, date); err == nil || match {
				t.Fatalf("match=%v err=%v", match, err)
			}
		})
	}
}

func TestFiltersAndDailyStatistics(t *testing.T) {
	date := testDate("2026-11-04")
	stock, bars := history(date, 220)
	row, match, err := Evaluate(testFilters(), stock, bars, date)
	if err != nil || !match || row.DollarVolume == nil || *row.DollarVolume != 97_000_000 || row.ChangePct == nil || *row.ChangePct != 0 || row.MarketCap == nil || *row.MarketCap != 970_000_000 || row.ATRPct == nil {
		t.Fatalf("row=%+v match=%v err=%v", row, match, err)
	}
	for _, change := range []func(*wsmsg.ScreenerFilters){
		func(f *wsmsg.ScreenerFilters) { f.MinPrice = 98 }, func(f *wsmsg.ScreenerFilters) { f.MaxPrice = 96 },
		func(f *wsmsg.ScreenerFilters) { f.MinDollarVolume = 98_000_000 }, func(f *wsmsg.ScreenerFilters) { f.MinMarketCap = 1e9 },
		func(f *wsmsg.ScreenerFilters) { f.AboveSMA20 = true }, func(f *wsmsg.ScreenerFilters) { f.AboveSMA50 = true }, func(f *wsmsg.ScreenerFilters) { f.AboveSMA200 = true },
		func(f *wsmsg.ScreenerFilters) { f.MaxATRPct = 1 }, func(f *wsmsg.ScreenerFilters) { v := 1.0; f.MinChangePct = &v },
	} {
		f := testFilters()
		change(&f)
		if _, match, err := Evaluate(f, stock, bars, date); err != nil || match {
			t.Fatalf("filters=%+v match=%v err=%v", f, match, err)
		}
	}
	stock.Shares = 0
	f := testFilters()
	f.MinMarketCap = 1
	if _, match, err := Evaluate(f, stock, bars, date); err != nil || match {
		t.Fatalf("missing cap match=%v err=%v", match, err)
	}
	// An open-session candle cannot overwrite the completed close.
	bars = append(bars, feed.Bar{BucketMs: session.NextTradingDay(date).UnixMilli(), O: 200, H: 220, L: 190, C: 210})
	row, match, err = Evaluate(testFilters(), stock, bars, date)
	if err != nil || !match || row.Close != 97 {
		t.Fatalf("future bar: %+v %v %v", row, match, err)
	}
}

func TestCompletedSessionCalendar(t *testing.T) {
	for _, tc := range []struct{ now, want string }{
		{"2026-11-04 15:59", "2026-11-03"}, {"2026-11-04 16:00", "2026-11-04"},
		{"2026-11-07 17:00", "2026-11-06"}, {"2026-11-26 17:00", "2026-11-25"},
		{"2026-11-27 12:59", "2026-11-25"}, {"2026-11-27 13:00", "2026-11-27"},
	} {
		now, _ := time.ParseInLocation("2006-01-02 15:04", tc.now, session.Loc())
		if got := CompletedDate(now).Format("2006-01-02"); got != tc.want {
			t.Fatalf("%s = %s want %s", tc.now, got, tc.want)
		}
	}
}

func TestValidateRejectsBadInputs(t *testing.T) {
	if err := Validate(testFilters()); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*wsmsg.ScreenerFilters){func(f *wsmsg.ScreenerFilters) { f.Lookback = "52-weeks" }, func(f *wsmsg.ScreenerFilters) { f.Mode = "bad" }, func(f *wsmsg.ScreenerFilters) { f.TradingDays = 0 }, func(f *wsmsg.ScreenerFilters) { f.MinPrice = math.NaN() }, func(f *wsmsg.ScreenerFilters) { f.NearPct = 101 }, func(f *wsmsg.ScreenerFilters) { f.MinPrice = 10; f.MaxPrice = 5 }, func(f *wsmsg.ScreenerFilters) { f.MaxExtensionPct = 10 }} {
		f := testFilters()
		change(&f)
		if Validate(f) == nil {
			t.Fatalf("accepted %+v", f)
		}
	}
}
