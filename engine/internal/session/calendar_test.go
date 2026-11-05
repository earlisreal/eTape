package session

import (
	"testing"
	"time"
)

func TestHistoricalTradingDates(t *testing.T) {
	for _, tc := range []struct {
		date    string
		trading bool
	}{
		{"1972-11-07", false}, {"1976-11-02", false}, {"1980-11-04", false}, {"1984-11-06", true},
		{"1972-12-28", false}, {"1973-01-25", false}, {"1977-07-14", false}, {"1985-09-27", false}, {"1994-04-27", false},
		{"1997-01-20", true}, {"1998-01-19", false},
		{"2021-12-31", true}, {"2022-01-03", true}, {"2023-01-02", false},
	} {
		t.Run(tc.date, func(t *testing.T) {
			day, err := time.ParseInLocation("2006-01-02", tc.date, Loc())
			if err != nil {
				t.Fatal(err)
			}
			if got := IsTradingDay(day); got != tc.trading {
				t.Fatalf("IsTradingDay=%v want %v", got, tc.trading)
			}
		})
	}
}
