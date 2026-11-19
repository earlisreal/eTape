package linearperp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed/cryptoscanner"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type bookTicker struct {
	Symbol string      `json:"symbol"`
	Bid    json.Number `json:"bidPrice"`
	Ask    json.Number `json:"askPrice"`
	Time   int64       `json:"time"`
}

func (c *Client) CryptoScanner(ctx context.Context) (wsmsg.CryptoScannerSnapshot, error) {
	return c.scanner.Snapshot(ctx, c.PerpetualMarkets, c.scannerTickers)
}

func (c *Client) scannerTickers(ctx context.Context) ([]cryptoscanner.Ticker, error) {
	path := "/openApi/swap/v2/quote/ticker"
	if c.exchange != BingX {
		path = "/fapi/v1/ticker/24hr"
	}
	var response []struct {
		bookTicker
		Last      json.Number `json:"lastPrice"`
		Turnover  json.Number `json:"quoteVolume"`
		CloseTime int64       `json:"closeTime"`
	}
	if err := c.get(ctx, path, nil, &response); err != nil {
		return nil, err
	}
	books := make(map[string]bookTicker)
	if c.exchange != BingX {
		var quotes []bookTicker
		if err := c.get(ctx, "/fapi/v1/ticker/bookTicker", nil, &quotes); err == nil {
			cutoff := time.Now().Add(-20 * time.Second).UnixMilli()
			for _, quote := range quotes {
				if quote.Time >= cutoff {
					books[quote.Symbol] = quote
				}
			}
		}
	}
	tickers := make([]cryptoscanner.Ticker, 0, len(response))
	for _, row := range response {
		quote := row.bookTicker
		if c.exchange != BingX {
			quote = books[row.Symbol]
		}
		ticker := cryptoscanner.Ticker{
			Symbol: row.Symbol, Last: number(row.Last), Bid: number(quote.Bid), Ask: number(quote.Ask), Timestamp: row.CloseTime,
		}
		if amount, err := row.Turnover.Float64(); err == nil && amount >= 0 {
			ticker.Turnover = &amount
		}
		tickers = append(tickers, ticker)
	}
	return tickers, nil
}
