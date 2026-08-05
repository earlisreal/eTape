package backfill

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// intradayFrom returns local midnight `days` calendar days before now.
func intradayFrom(now time.Time, days int) time.Time {
	if days < 1 {
		days = 1
	}
	et := now.In(session.Loc())
	return time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, session.Loc()).AddDate(0, 0, -days)
}

// tenSecondFrom returns the configured calendar-day floor. Zero keeps only
// the current trading cycle, which starts at the latest NYSE close (the start
// of that trading day's post-market session).
func tenSecondFrom(now time.Time, days int) time.Time {
	if days <= 0 {
		return session.TradingCycleStart(now)
	}
	et := now.In(session.Loc())
	return time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, session.Loc()).AddDate(0, 0, -days)
}
