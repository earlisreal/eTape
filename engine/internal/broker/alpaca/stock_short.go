package alpaca

import (
	"context"
	"fmt"
	"math"
	"net/http"
)

// StockShortCapacity prices an estimated whole-share opening cap using the
// exact account and current borrow classification. Alpaca admits the order.
func (a *Adapter) StockShortCapacity(ctx context.Context, symbol string, valuation float64) (float64, error) {
	asset, err := a.rest.stockMarginAsset(ctx, assetCacheKey(symbol))
	if err != nil {
		return 0, fmt.Errorf("short eligibility unavailable from Alpaca: %w", err)
	}
	if asset.Tradable == nil || !*asset.Tradable || asset.Shortable == nil || !*asset.Shortable ||
		asset.BorrowStatus == nil || *asset.BorrowStatus != "easy_to_borrow" {
		return 0, fmt.Errorf("stock must be tradable and easy to borrow on Alpaca")
	}
	resp, err := a.rest.do(ctx, http.MethodGet, "/v2/account", nil)
	if err != nil {
		return 0, fmt.Errorf("short account data unavailable from Alpaca: %w", err)
	}
	account, err := a.rest.readAccountResponse(resp)
	if err != nil {
		return 0, fmt.Errorf("short account data unavailable from Alpaca: %w", err)
	}
	if !account.StockShortingEnabled {
		return 0, fmt.Errorf("short selling is disabled on this Alpaca account")
	}
	return math.Max(0, math.Floor(account.BuyingPower/valuation)), nil
}
