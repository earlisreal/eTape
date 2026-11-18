package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/mexc"
	"github.com/earlisreal/eTape/engine/internal/instrument"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/uihub"
)

type perpetualFeedRouter struct {
	stocks   uihub.Feed
	mexcAPI  *mexc.Client
	mexc     *mexc.Stream
	archived func(string) bool
}

func (r perpetualFeedRouter) Validate(ctx context.Context, symbol string) error {
	if instrument.IsPerpetual(symbol) {
		err := r.mexcAPI.Validate(ctx, symbol)
		if errors.Is(err, feed.ErrFeedUnavailable) && r.archived(symbol) {
			return nil
		}
		return err
	}
	return r.stocks.Validate(ctx, symbol)
}

func (r perpetualFeedRouter) Ensure(d feed.Demand) {
	if instrument.IsPerpetual(d.Symbol) {
		r.stocks.Release(d.ID)
		r.mexc.Ensure(d)
	} else {
		r.mexc.Release(d.ID)
		r.stocks.Ensure(d)
	}
}

func (r perpetualFeedRouter) Release(id string) {
	r.stocks.Release(id)
	r.mexc.Release(id)
}

type historyWorker func(string, func(bool))

func perpetualHistory(ctx context.Context, api *mexc.Client, core *md.Core, st *store.Store, wg *sync.WaitGroup, stock historyWorker) historyWorker {
	return func(symbol string, done func(bool)) {
		if !instrument.IsPerpetual(symbol) {
			if stock != nil {
				stock(symbol, done)
			} else if done != nil {
				done(true)
			}
			return
		}
		wg.Go(func() {
			seed := func() bool {
				minute, readErr := st.ReadRecentBars1m(symbol, 2000)
				daily, dayReadErr := st.ReadRecentDailyBars(symbol, 2000)
				ten, tickReadErr := st.ReadRecentBars10s(symbol, 10000)
				if err := errors.Join(readErr, dayReadErr, tickReadErr); err != nil {
					slog.Warn("MEXC perpetual chart archive", "symbol", symbol, "err", err)
				}
				if len(minute)+len(daily)+len(ten) == 0 {
					return false
				}
				core.SeedChartHistory(symbol, daily, minute, ten)
				return true
			}
			ready := seed()
			if ready && done != nil {
				done(true)
			}
			now := time.Now().UnixMilli()
			minute, minuteErr := api.History(ctx, symbol, feed.Res1m)
			daily, dailyErr := api.History(ctx, symbol, feed.ResDay)
			if ctx.Err() != nil {
				return
			}
			for _, b := range minute {
				if b.BucketMs < session.ContinuousBucketStartMs(now, session.TF1m) {
					st.ArchiveBar1m(b)
				}
			}
			for _, b := range daily {
				if b.BucketMs < session.ContinuousBucketStartMs(now, session.TFDay) {
					st.ArchiveDaily(b)
				}
			}
			if err := errors.Join(minuteErr, dailyErr); err != nil {
				slog.Warn("MEXC perpetual chart history", "symbol", symbol, "err", err)
			}
			if len(minute)+len(daily) > 0 {
				st.Flush()
				refreshed := seed()
				if !ready && done != nil {
					done(refreshed)
				}
			} else if !ready && done != nil {
				done(false)
			}
		})
	}
}
