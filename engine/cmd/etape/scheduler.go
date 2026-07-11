package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// The day-roll seal fires at 00:30 ET — inside the 20:00–04:00 ET US-market-
// closed window, so sealing the just-completed day serializes with a near-idle
// write queue. The day partition is by receive time, so no new rows for the
// prior day can arrive after midnight; the seal is safe.
const (
	sealHourET = 0
	sealMinET  = 30
)

// nextSealFire returns the next 00:30 ET instant strictly after now.
func nextSealFire(now time.Time) time.Time {
	et := now.In(session.Loc())
	fire := time.Date(et.Year(), et.Month(), et.Day(), sealHourET, sealMinET, 0, 0, session.Loc())
	if !fire.After(et) {
		fire = fire.AddDate(0, 0, 1)
	}
	return fire
}

// runSealScheduler enqueues a journal seal onto the store's writer goroutine at
// each 00:30 ET boundary, so an engine left running past midnight compresses the
// prior day without a restart. Returns when ctx is cancelled. It is a
// store-writing goroutine (RequestSeal) and must be joined via wg before
// st.Close() -- see the shutdown-ordering comment in main().
func runSealScheduler(ctx context.Context, wg *sync.WaitGroup, st *store.Store, clk clock.Clock, log *slog.Logger) {
	defer wg.Done()
	for {
		wait := nextSealFire(clk.Now()).Sub(clk.Now())
		select {
		case <-ctx.Done():
			return
		case <-clk.After(wait):
			st.RequestSeal()
			log.Info("day-roll seal requested")
		}
	}
}
