package mexc

import (
	"context"

	"github.com/earlisreal/eTape/engine/internal/feed/cryptoscanner"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func (c *Client) CryptoScanner(ctx context.Context) (wsmsg.CryptoScannerSnapshot, error) {
	return c.scanner.Snapshot(ctx, c.PerpetualMarkets, c.scannerTickers)
}

func (c *Client) scannerTickers(ctx context.Context) ([]cryptoscanner.Ticker, error) {
	var response []struct {
		Symbol    string   `json:"symbol"`
		Last      float64  `json:"lastPrice"`
		Bid       float64  `json:"bid1"`
		Ask       float64  `json:"ask1"`
		Turnover  *float64 `json:"amount24"`
		Timestamp int64    `json:"timestamp"`
	}
	if err := c.get(ctx, "/api/v1/contract/ticker", &response); err != nil {
		return nil, err
	}
	tickers := make([]cryptoscanner.Ticker, 0, len(response))
	for _, row := range response {
		tickers = append(tickers, cryptoscanner.Ticker{
			Symbol: row.Symbol, Last: row.Last, Bid: row.Bid, Ask: row.Ask, Turnover: row.Turnover, Timestamp: row.Timestamp,
		})
	}
	return tickers, nil
}
