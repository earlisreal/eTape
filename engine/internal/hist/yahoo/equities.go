package yahoo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// EquityQuote holds the listing metadata and quote fields used by Screener discovery.
type EquityQuote struct {
	Symbol           string  `json:"symbol"`
	Name             string  `json:"shortName"`
	Type             string  `json:"quoteType"`
	Exchange         string  `json:"exchange"`
	FirstTradeMs     int64   `json:"firstTradeDateMilliseconds"`
	MarketCap        float64 `json:"marketCap"`
	Price            float64 `json:"regularMarketPrice"`
	MarketTime       int64   `json:"regularMarketTime"`
	DayHigh          float64 `json:"regularMarketDayHigh"`
	FiftyTwoWeekHigh float64 `json:"fiftyTwoWeekHigh"`
}

type equityPage struct {
	Start  int           `json:"start"`
	Total  int           `json:"total"`
	Count  int           `json:"count"`
	Quotes []EquityQuote `json:"quotes"`
}

// ListedUSEquities uses the Yahoo Finance equity screener. Reject an incomplete
// catalog so a failed page cannot silently narrow the screen's universe.
func (c *Client) ListedUSEquities(ctx context.Context) ([]EquityQuote, error) {
	var quotes []EquityQuote
	seen := make(map[string]bool)
	total := 0
	for len(quotes) == 0 || len(quotes) < total {
		page, err := c.equityPage(ctx, len(quotes))
		if err != nil {
			return nil, err
		}
		if total == 0 {
			total = page.Total
		}
		if total <= 0 || total > 20_000 || page.Total != total || page.Start != len(quotes) ||
			page.Count != len(page.Quotes) || page.Count == 0 || len(quotes)+page.Count > total {
			return nil, errors.New("stock discovery: Yahoo returned an incomplete catalog")
		}
		for _, quote := range page.Quotes {
			if quote.Symbol == "" || seen[quote.Symbol] {
				return nil, errors.New("stock discovery: Yahoo returned missing or repeated symbols")
			}
			seen[quote.Symbol] = true
			quotes = append(quotes, quote)
		}
	}
	return quotes, nil
}

func (c *Client) equityPage(ctx context.Context, offset int) (equityPage, error) {
	body := fmt.Appendf(nil, `{"size":250,"offset":%d,"sortField":"ticker","sortType":"ASC","quoteType":"EQUITY",
	"query":{"operator":"AND","operands":[{"operator":"EQ","operands":["region","us"]},
	{"operator":"OR","operands":[{"operator":"EQ","operands":["exchange","ASE"]},
	{"operator":"EQ","operands":["exchange","NAS"]},{"operator":"EQ","operands":["exchange","NCM"]},
	{"operator":"EQ","operands":["exchange","NGM"]},{"operator":"EQ","operands":["exchange","NMS"]},
	{"operator":"EQ","operands":["exchange","NYQ"]}]}]}}`, offset)
	for attempt := 0; ; attempt++ {
		if err := c.bucket.Take(ctx); err != nil {
			return equityPage{}, err
		}
		cookie, crumb, err := c.session.Credentials(ctx)
		if err != nil {
			return equityPage{}, err
		}
		query := url.Values{"crumb": {crumb}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/finance/screener?"+query.Encode(), bytes.NewReader(body))
		if err != nil {
			return equityPage{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", browserUA)
		req.Header.Set("Cookie", cookie)
		resp, err := c.hc.Do(req)
		if err != nil {
			// http.Client errors include the request URL, which contains the crumb.
			if transportErr, ok := errors.AsType[*url.Error](err); ok {
				err = transportErr.Err
			}
			return equityPage{}, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
				c.session.Invalidate(cookie, crumb)
				continue
			}
			return equityPage{}, fmt.Errorf("stock discovery: Yahoo returned HTTP %d", resp.StatusCode)
		}
		var response struct {
			Finance struct {
				Result []equityPage    `json:"result"`
				Error  json.RawMessage `json:"error"`
			} `json:"finance"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&response)
		_ = resp.Body.Close()
		if err != nil {
			return equityPage{}, fmt.Errorf("stock discovery: decode Yahoo response: %w", err)
		}
		if len(response.Finance.Result) != 1 || (len(response.Finance.Error) != 0 && string(response.Finance.Error) != "null") {
			return equityPage{}, errors.New("stock discovery: Yahoo returned no catalog page")
		}
		return response.Finance.Result[0], nil
	}
}
