// Package cryptoscanner caches exchange-reported perpetual market quotes.
package cryptoscanner

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/instrument"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

const (
	scannerInterval  = 5 * time.Second
	scannerTolerance = 20 * time.Second
)

type Ticker struct {
	Symbol            string
	Last              *float64
	ReportedChangePct *float64
	Turnover          *float64
	Timestamp         int64
}

type Cache struct {
	mu       sync.Mutex
	next     time.Time
	snapshot wsmsg.CryptoScannerSnapshot
	err      error
}

// Snapshot shares one paced quote snapshot between all windows.
func (cache *Cache) Snapshot(ctx context.Context, getMarkets func(context.Context) ([]instrument.PerpetualMarket, error), getTickers func(context.Context) ([]Ticker, error)) (wsmsg.CryptoScannerSnapshot, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if time.Now().Before(cache.next) {
		return cache.snapshot, cache.err
	}
	cache.snapshot, cache.err = loadSnapshot(ctx, getMarkets, getTickers)
	cache.next = time.Now().Add(scannerInterval)
	return cache.snapshot, cache.err
}

func loadSnapshot(ctx context.Context, getMarkets func(context.Context) ([]instrument.PerpetualMarket, error), getTickers func(context.Context) ([]Ticker, error)) (wsmsg.CryptoScannerSnapshot, error) {
	markets, err := getMarkets(ctx)
	if err != nil {
		return wsmsg.CryptoScannerSnapshot{}, err
	}
	tickers, err := getTickers(ctx)
	if err != nil {
		return wsmsg.CryptoScannerSnapshot{}, err
	}
	return snapshotRows(time.Now(), markets, tickers), nil
}

func snapshotRows(now time.Time, markets []instrument.PerpetualMarket, tickers []Ticker) wsmsg.CryptoScannerSnapshot {
	eligible := make(map[string]string, len(markets))
	for _, market := range markets {
		eligible[market.ID.Native] = market.ID.String()
	}
	rows := make([]wsmsg.CryptoScannerRow, 0, len(eligible))
	for _, ticker := range tickers {
		symbol, ok := eligible[ticker.Symbol]
		age := now.Sub(time.UnixMilli(ticker.Timestamp))
		if !ok || age > scannerTolerance || age < -scannerInterval {
			continue
		}
		row := wsmsg.CryptoScannerRow{Symbol: symbol, Last: finite(ticker.Last),
			ReportedChangePct: finite(ticker.ReportedChangePct), Turnover: finite(ticker.Turnover)}
		if row.Last != nil && *row.Last <= 0 {
			row.Last = nil
		}
		if row.Turnover != nil && *row.Turnover < 0 {
			row.Turnover = nil
		}
		if row.Last != nil || row.ReportedChangePct != nil || row.Turnover != nil {
			rows = append(rows, row)
		}
	}
	return wsmsg.CryptoScannerSnapshot{AsOfMs: now.UnixMilli(), Rows: rows}
}

func finite(value *float64) *float64 {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return nil
	}
	return value
}
