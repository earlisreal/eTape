package session

import "time"

// DaySchedule describes one NYSE trading date in exchange time.
type DaySchedule struct {
	Date       time.Time
	Open       time.Time
	Close      time.Time
	DataClose  time.Time
	TradingDay bool
}

// Schedule returns the offline NYSE schedule for the ET date containing t.
func Schedule(t time.Time) DaySchedule {
	et := t.In(loc)
	d := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, loc)
	s := DaySchedule{Date: d}
	if !isTradingDate(d) {
		return s
	}
	s.TradingDay = true
	s.Open = d.Add(9*time.Hour + 30*time.Minute)
	s.Close = d.Add(16 * time.Hour)
	s.DataClose = d.Add(20 * time.Hour)
	if isEarlyClose(d) {
		s.Close = d.Add(13 * time.Hour)
		s.DataClose = d.Add(17 * time.Hour)
	}
	return s
}

func IsTradingDay(t time.Time) bool { return Schedule(t).TradingDay }

func PreviousTradingDay(t time.Time) time.Time {
	d := midnightET(t).AddDate(0, 0, -1)
	for !IsTradingDay(d) {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

func NextTradingDay(t time.Time) time.Time {
	d := midnightET(t).AddDate(0, 0, 1)
	for !IsTradingDay(d) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// TradingCycleStart returns the scheduled NYSE close that starts the account
// trading cycle containing t. At the close itself the new cycle has begun.
func TradingCycleStart(t time.Time) time.Time {
	et := t.In(loc)
	if s := Schedule(et); s.TradingDay && !et.Before(s.Close) {
		return s.Close
	}
	return Schedule(PreviousTradingDay(et)).Close
}

// NextTradingCycleStart returns the first scheduled close after t.
func NextTradingCycleStart(t time.Time) time.Time {
	et := t.In(loc)
	if s := Schedule(et); s.TradingDay && et.Before(s.Close) {
		return s.Close
	}
	return Schedule(NextTradingDay(et)).Close
}

func midnightET(t time.Time) time.Time {
	et := t.In(loc)
	return time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, loc)
}

func isTradingDate(d time.Time) bool {
	wd := d.Weekday()
	if wd == time.Saturday || wd == time.Sunday || exceptionalClosure(d) {
		return false
	}
	y, m, day := d.Date()
	if sameDate(d, observed(y, time.January, 1)) || sameDate(d, observed(y+1, time.January, 1)) ||
		(m == time.January && wd == time.Monday && day >= 15 && day <= 21) ||
		(m == time.February && wd == time.Monday && day >= 15 && day <= 21) ||
		sameDate(d, goodFriday(y)) ||
		(m == time.May && wd == time.Monday && day+7 > daysInMonth(y, m)) ||
		(y >= 2022 && sameDate(d, observed(y, time.June, 19))) ||
		sameDate(d, observed(y, time.July, 4)) ||
		(m == time.September && wd == time.Monday && day <= 7) ||
		(m == time.November && wd == time.Thursday && day >= 22 && day <= 28) ||
		sameDate(d, observed(y, time.December, 25)) {
		return false
	}
	return true
}

func isEarlyClose(d time.Time) bool {
	_, m, day := d.Date()
	// Friday after Thanksgiving, Christmas Eve, and the last trading day
	// before Independence Day are the recurring NYSE 13:00 closes.
	if m == time.November && d.Weekday() == time.Friday && day >= 23 && day <= 29 {
		return true
	}
	if m == time.December && day == 24 {
		return true
	}
	if m == time.July {
		holiday := observed(d.Year(), time.July, 4)
		p := holiday.AddDate(0, 0, -1)
		for !isTradingDate(p) {
			p = p.AddDate(0, 0, -1)
		}
		if sameDate(d, p) {
			return true
		}
	}
	return false
}

func observed(y int, m time.Month, day int) time.Time {
	d := time.Date(y, m, day, 0, 0, 0, 0, loc)
	switch d.Weekday() {
	case time.Saturday:
		return d.AddDate(0, 0, -1)
	case time.Sunday:
		return d.AddDate(0, 0, 1)
	}
	return d
}

func exceptionalClosure(d time.Time) bool {
	k := d.Format("2006-01-02")
	switch k {
	case "2001-09-11", "2001-09-12", "2001-09-13", "2001-09-14",
		"2004-06-11", "2007-01-02", "2012-10-29", "2012-10-30",
		"2018-12-05", "2025-01-09":
		return true
	}
	return false
}

func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
func daysInMonth(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, loc).Day() }

func goodFriday(y int) time.Time {
	// Anonymous Gregorian computus; valid throughout the supported range.
	a, b, c := y%19, y/100, y%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(y, time.Month(month), day, 0, 0, 0, 0, loc).AddDate(0, 0, -2)
}
