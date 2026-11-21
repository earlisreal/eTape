package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

const stockMarginTTL = 15 * time.Minute
const stockMarginRetry = time.Minute

var stockMarginDecimal = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// StockMargin holds Alpaca's reported percentages in percentage points.
type StockMargin struct {
	Long, Short *string
	FetchedAt   time.Time
	NextCheck   time.Time
	Stale       bool
}

type stockMarginCheck struct {
	value StockMargin
	done  chan struct{}
}

func marginPercent(value *string) *string {
	if value == nil || !stockMarginDecimal.MatchString(*value) {
		return nil
	}
	return value
}

func (asset assetResponse) stockMargin(at time.Time) StockMargin {
	return StockMargin{
		Long: marginPercent(asset.MarginLong), Short: marginPercent(asset.MarginShort),
		FetchedAt: at, NextCheck: at.Add(stockMarginTTL),
	}
}

func (a *Adapter) stockMarginLocked(symbol string) StockMargin {
	if check := a.marginChecks[symbol]; check != nil && !check.value.NextCheck.IsZero() {
		return check.value
	}
	return a.assetsBySymbol[symbol].margin
}

// StockMargin returns the cached value, optionally revalidating a due symbol.
// The refresh cache is separate from eligibility and locate metadata.
func (a *Adapter) StockMargin(ctx context.Context, symbol string, refresh bool) StockMargin {
	symbol = assetCacheKey(symbol)
	a.assetMu.Lock()
	current := a.stockMarginLocked(symbol)
	if !refresh || a.clk.Now().Before(current.NextCheck) {
		a.assetMu.Unlock()
		return current
	}
	if check := a.marginChecks[symbol]; check != nil && check.done != nil {
		done := check.done
		a.assetMu.Unlock()
		select {
		case <-done:
			return a.StockMargin(ctx, symbol, false)
		case <-ctx.Done():
			return current
		}
	}
	if a.marginChecks == nil {
		a.marginChecks = make(map[string]*stockMarginCheck)
	}
	check := &stockMarginCheck{value: current, done: make(chan struct{})}
	a.marginChecks[symbol] = check
	a.assetMu.Unlock()

	request, cancel := context.WithTimeout(ctx, 5*time.Second)
	asset, err := a.rest.stockMarginAsset(request, symbol)
	cancel()

	a.assetMu.Lock()
	defer a.assetMu.Unlock()
	now := a.clk.Now()
	if err == nil {
		check.value = asset.stockMargin(now)
	} else {
		seed := a.assetsBySymbol[symbol].margin
		if seed.FetchedAt.After(current.FetchedAt) {
			current = seed
		}
		current.Stale = current.Long != nil || current.Short != nil
		current.NextCheck = now.Add(stockMarginRetry)
		check.value = current
	}
	close(check.done)
	check.done = nil
	return check.value
}

func (rc *restClient) stockMarginAsset(ctx context.Context, symbol string) (assetResponse, error) {
	resp, err := rc.do(ctx, http.MethodGet, "/v2/assets/"+url.PathEscape(symbol), nil)
	if err != nil {
		return assetResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return assetResponse{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return assetResponse{}, fmt.Errorf("alpaca asset lookup: %s", resp.Status)
	}
	var asset assetResponse
	if err := json.NewDecoder(resp.Body).Decode(&asset); err != nil {
		return assetResponse{}, err
	}
	if assetCacheKey(asset.Symbol) != symbol {
		return assetResponse{}, fmt.Errorf("alpaca asset lookup returned a different symbol")
	}
	return asset, nil
}
