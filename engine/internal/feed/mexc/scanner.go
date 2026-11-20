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
		Last      *float64 `json:"lastPrice"`
		Change    *float64 `json:"riseFallRate"`
		Turnover  *float64 `json:"amount24"`
		Timestamp int64    `json:"timestamp"`
	}
	if err := c.get(ctx, "/api/v1/contract/ticker", &response); err != nil {
		return nil, err
	}
	tickers := make([]cryptoscanner.Ticker, 0, len(response))
	for _, row := range response {
		var changePct *float64
		if row.Change != nil {
			value := *row.Change * 100
			changePct = &value
		}
		tickers = append(tickers, cryptoscanner.Ticker{
			Symbol: row.Symbol, Last: row.Last, ReportedChangePct: changePct, Turnover: row.Turnover, Timestamp: row.Timestamp,
		})
	}
	return tickers, nil
}
