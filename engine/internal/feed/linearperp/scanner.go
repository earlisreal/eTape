package linearperp

import (
	"context"
	"encoding/json"

	"github.com/earlisreal/eTape/engine/internal/feed/cryptoscanner"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func (c *Client) CryptoScanner(ctx context.Context) (wsmsg.CryptoScannerSnapshot, error) {
	return c.scanner.Snapshot(ctx, c.PerpetualMarkets, c.scannerTickers)
}

func (c *Client) scannerTickers(ctx context.Context) ([]cryptoscanner.Ticker, error) {
	path := "/openApi/swap/v2/quote/ticker"
	if c.exchange != BingX {
		path = "/fapi/v1/ticker/24hr"
	}
	var response []struct {
		Symbol    string      `json:"symbol"`
		Last      json.Number `json:"lastPrice"`
		ChangePct json.Number `json:"priceChangePercent"`
		Turnover  json.Number `json:"quoteVolume"`
		CloseTime int64       `json:"closeTime"`
	}
	if err := c.get(ctx, path, nil, &response); err != nil {
		return nil, err
	}
	tickers := make([]cryptoscanner.Ticker, 0, len(response))
	for _, row := range response {
		tickers = append(tickers, cryptoscanner.Ticker{
			Symbol: row.Symbol, Last: optionalTickerNumber(row.Last), ReportedChangePct: optionalTickerNumber(row.ChangePct),
			Turnover: optionalTickerNumber(row.Turnover), Timestamp: row.CloseTime,
		})
	}
	return tickers, nil
}

func optionalTickerNumber(raw json.Number) *float64 {
	value, err := raw.Float64()
	if err != nil {
		return nil
	}
	return &value
}
