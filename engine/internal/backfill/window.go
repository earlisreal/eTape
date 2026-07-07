package backfill

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// intradayFrom returns ET midnight `tradingDays` weekdays before now. Weekends
// are skipped; US market holidays are not modeled (a holiday counts as a
// trading day here), matching the session package's documented v1 stance — the
// result is only a lower bound for the history query, so over-counting a
// holiday just widens the window harmlessly.
func intradayFrom(now time.Time, tradingDays int) time.Time {
	if tradingDays < 1 {
		tradingDays = 1
	}
	et := now.In(session.Loc())
	d := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, session.Loc())
	for tradingDays > 0 {
		d = d.AddDate(0, 0, -1)
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			tradingDays--
		}
	}
	return d
}
