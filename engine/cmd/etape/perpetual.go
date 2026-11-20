package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/instrument"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type perpetualAPI interface {
	Validate(context.Context, string) error
	PerpetualMarkets(context.Context) ([]instrument.PerpetualMarket, error)
	History(context.Context, string, feed.Resolution) ([]feed.Bar, error)
}

type perpetualStream interface {
	Ensure(feed.Demand)
	Release(string)
	Run(context.Context)
	Events() <-chan feed.Event
}

type perpetualSource struct {
	exchange string
	api      perpetualAPI
	stream   perpetualStream
}

type perpetualFeedRouter struct {
	stocks   uihub.Feed
	sources  []perpetualSource
	archived func(string) bool
}

func (r perpetualFeedRouter) source(symbol string) int {
	id, ok := instrument.ParsePerpetual(symbol)
	if !ok {
		return -1
	}
	return slices.IndexFunc(r.sources, func(source perpetualSource) bool { return source.exchange == id.Exchange })
}

func (r perpetualFeedRouter) PerpetualMarkets(ctx context.Context, exchange string) ([]instrument.PerpetualMarket, error) {
	if exchange != "" {
		for _, source := range r.sources {
			if source.exchange == exchange {
				return source.api.PerpetualMarkets(ctx)
			}
		}
		return nil, fmt.Errorf("unsupported crypto exchange %q", exchange)
	}
	var group sync.WaitGroup
	result := make([][]instrument.PerpetualMarket, len(r.sources))
	failures := make([]error, len(r.sources))
	for i, source := range r.sources {
		group.Go(func() { result[i], failures[i] = source.api.PerpetualMarkets(ctx) })
	}
	group.Wait()
	var markets []instrument.PerpetualMarket
	for _, rows := range result {
		markets = append(markets, rows...)
	}
	if len(markets) == 0 {
		return nil, errors.Join(failures...)
	}
	return markets, nil
}

func (r perpetualFeedRouter) CryptoScanner(ctx context.Context, exchange string) (wsmsg.CryptoScannerSnapshot, error) {
	for _, source := range r.sources {
		if source.exchange == exchange {
			scanner, ok := source.api.(interface {
				CryptoScanner(context.Context) (wsmsg.CryptoScannerSnapshot, error)
			})
			if ok {
				return scanner.CryptoScanner(ctx)
			}
		}
	}
	return wsmsg.CryptoScannerSnapshot{}, fmt.Errorf("unsupported crypto exchange %q", exchange)
}

func (r perpetualFeedRouter) Validate(ctx context.Context, symbol string) error {
	if instrument.IsPerpetual(symbol) {
		i := r.source(symbol)
		if i < 0 {
			return feed.ErrUnknownSymbol
		}
		err := r.sources[i].api.Validate(ctx, symbol)
		if errors.Is(err, feed.ErrFeedUnavailable) && r.archived(symbol) {
			return nil
		}
		return err
	}
	return r.stocks.Validate(ctx, symbol)
}

func (r perpetualFeedRouter) Ensure(d feed.Demand) {
	selected := r.source(d.Symbol)
	for i, source := range r.sources {
		if i != selected {
			source.stream.Release(d.ID)
		}
	}
	switch {
	case selected >= 0:
		r.stocks.Release(d.ID)
		r.sources[selected].stream.Ensure(d)
	case !instrument.IsPerpetual(d.Symbol):
		r.stocks.Ensure(d)
	}
}

func (r perpetualFeedRouter) Release(id string) {
	r.stocks.Release(id)
	for _, source := range r.sources {
		source.stream.Release(id)
	}
}

type historyWorker func(string, func(bool))

func perpetualHistory(ctx context.Context, router perpetualFeedRouter, core *md.Core, st *store.Store, wg *sync.WaitGroup, stock historyWorker) historyWorker {
	return func(symbol string, done func(bool)) {
		if !instrument.IsPerpetual(symbol) {
			if stock != nil {
				stock(symbol, done)
			} else if done != nil {
				done(true)
			}
			return
		}
		i := router.source(symbol)
		if i < 0 {
			if done != nil {
				done(false)
			}
			return
		}
		api := router.sources[i].api
		wg.Go(func() {
			seed := func() bool {
				minute, readErr := st.ReadRecentBars1m(symbol, 2000)
				daily, dayReadErr := st.ReadRecentDailyBars(symbol, 2000)
				ten, tickReadErr := st.ReadRecentBars10s(symbol, 10000)
				if err := errors.Join(readErr, dayReadErr, tickReadErr); err != nil {
					slog.Warn("perpetual chart archive", "symbol", symbol, "err", err)
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
				slog.Warn("perpetual chart history", "symbol", symbol, "err", err)
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
