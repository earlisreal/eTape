package backfill

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// intradayFrom returns ET midnight `tradingDays` NYSE sessions before now.
func intradayFrom(now time.Time, tradingDays int) time.Time {
	if tradingDays < 1 {
		tradingDays = 1
	}
	et := now.In(session.Loc())
	d := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, session.Loc())
	for tradingDays > 0 {
		d = d.AddDate(0, 0, -1)
		if session.IsTradingDay(d) {
			tradingDays--
		}
	}
	return d
}
