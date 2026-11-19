// Package cryptoscanner keeps rolling Scanner snapshots for one exchange.
package cryptoscanner

import (
	"context"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/instrument"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

const (
	scannerInterval  = 5 * time.Second
	scannerWindow    = 5 * time.Minute
	scannerTolerance = 20 * time.Second
)

type Ticker struct {
	Symbol    string
	Last      float64
	Bid       float64
	Ask       float64
	Turnover  *float64
	Timestamp int64
}

type scannerPrice struct {
	at   time.Time
	last float64
}

type Cache struct {
	mu       sync.Mutex
	next     time.Time
	snapshot wsmsg.CryptoScannerSnapshot
	err      error
	history  map[string][]scannerPrice
	started  time.Time
}

// Snapshot shares one paced snapshot and price history between all windows.
func (cache *Cache) Snapshot(ctx context.Context, getMarkets func(context.Context) ([]instrument.PerpetualMarket, error), getTickers func(context.Context) ([]Ticker, error)) (wsmsg.CryptoScannerSnapshot, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if time.Now().Before(cache.next) {
		return cache.snapshot, cache.err
	}
	cache.err = cache.refresh(ctx, getMarkets, getTickers)
	cache.next = time.Now().Add(scannerInterval)
	return cache.snapshot, cache.err
}

func (cache *Cache) refresh(ctx context.Context, getMarkets func(context.Context) ([]instrument.PerpetualMarket, error), getTickers func(context.Context) ([]Ticker, error)) error {
	markets, err := getMarkets(ctx)
	if err != nil {
		return err
	}
	tickers, err := getTickers(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	if cache.history == nil {
		cache.history = make(map[string][]scannerPrice)
		cache.started = now
	}
	for symbol, history := range cache.history {
		if now.Sub(history[len(history)-1].at) > scannerWindow+scannerTolerance {
			delete(cache.history, symbol)
		}
	}
	type listing struct{ symbol, base string }
	eligible := make(map[string]listing, len(markets))
	for _, market := range markets {
		if market.CryptoUnderlying {
			eligible[market.ID.Native] = listing{market.ID.String(), market.Base}
		}
	}
	rows := make([]wsmsg.CryptoScannerRow, 0, len(eligible))
	for _, ticker := range tickers {
		market, ok := eligible[ticker.Symbol]
		at := time.UnixMilli(ticker.Timestamp)
		age := now.Sub(at)
		if !ok || ticker.Last <= 0 || age > scannerTolerance || age < -scannerInterval {
			continue
		}
		history, change := scannerChange(cache.history[market.symbol], scannerPrice{at: at, last: ticker.Last})
		cache.history[market.symbol] = history
		row := wsmsg.CryptoScannerRow{Symbol: market.symbol, Base: market.base, Last: ticker.Last, ChangePct: change, Turnover: ticker.Turnover}
		if row.Turnover != nil && *row.Turnover < 0 {
			row.Turnover = nil
		}
		if ticker.Bid > 0 && ticker.Ask >= ticker.Bid {
			spread := 10_000 * ((ticker.Ask - ticker.Bid) / (ticker.Ask/2 + ticker.Bid/2))
			row.SpreadBps = &spread
		}
		rows = append(rows, row)
	}
	cache.snapshot = wsmsg.CryptoScannerSnapshot{AsOfMs: now.UnixMilli(), Warming: now.Sub(cache.started) < scannerWindow, Rows: rows}
	return nil
}

func scannerChange(history []scannerPrice, current scannerPrice) ([]scannerPrice, *float64) {
	if len(history) > 0 && current.at.Before(history[len(history)-1].at) {
		return history, nil
	}
	target := current.at.Add(-scannerWindow)
	for len(history) > 0 && history[0].at.Before(target.Add(-scannerTolerance)) {
		history = history[1:]
	}
	var change *float64
	for _, point := range history {
		if point.at.After(target) {
			break
		}
		value := 100 * (current.last/point.last - 1)
		change = &value
	}
	if len(history) == 0 || current.at.After(history[len(history)-1].at) {
		history = append(history, current)
	}
	return history, change
}
