// Package alpaca is a read-only client for Alpaca's historical market-data
// REST API (data.alpaca.markets), used as eTape's 1m-depth backfill fallback.
// It is deliberately separate from internal/broker/alpaca (the execution
// adapter): different base URL, no order surface, and it authenticates with
// the PAPER data key so live-account keys are never touched. It implements
// backfill.HistFetcher structurally.
package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

const defaultDataBase = "https://data.alpaca.markets"

// maxPages caps pagination as a runaway backstop (10k bars/page).
const maxPages = 50

// Client is the Alpaca historical-bars transport.
type Client struct {
	base   string
	keyID  string
	secret string
	feed   string
	hc     *http.Client
	clk    clock.Clock
	bucket *netx.TokenBucket
}

// New builds a Client. base defaults to the production data host; feedName
// defaults to "iex" (the free tier). Tests pass a mock server URL.
func New(base, keyID, secret, feedName string, clk clock.Clock) *Client {
	if base == "" {
		base = defaultDataBase
	}
	if feedName == "" {
		feedName = "iex"
	}
	return &Client{
		base: base, keyID: keyID, secret: secret, feed: feedName,
		hc:     netx.NewHTTPClient(15 * time.Second),
		clk:    clk,
		bucket: netx.NewTokenBucket(clk, 200.0/60.0, 5),
	}
}

func (c *Client) DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	return c.bars(ctx, symbol, "1Day", from, to)
}

func (c *Client) Intraday1m(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	return c.bars(ctx, symbol, "1Min", from, to)
}

type barJSON struct {
	T string  `json:"t"` // RFC3339 bar-start, UTC
	O float64 `json:"o"`
	H float64 `json:"h"`
	L float64 `json:"l"`
	C float64 `json:"c"`
	V int64   `json:"v"`
}

type barsResp struct {
	Bars          []barJSON `json:"bars"`
	NextPageToken *string   `json:"next_page_token"`
}

// bars pages through /v2/stocks/{sym}/bars, mapping each UTC bar-start to an
// epoch-ms bucket start. The symbol keeps its US. prefix on the returned bars
// (the rest of eTape keys by that string) but is stripped for the URL path.
func (c *Client) bars(ctx context.Context, symbol, timeframe string, from, to time.Time) ([]feed.Bar, error) {
	sym := strings.TrimPrefix(symbol, "US.")
	var out []feed.Bar
	pageToken := ""
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("timeframe", timeframe)
		q.Set("start", from.UTC().Format(time.RFC3339))
		q.Set("end", to.UTC().Format(time.RFC3339))
		q.Set("adjustment", "all") // split + dividend, closest to moomoo forward-rehab
		q.Set("feed", c.feed)
		q.Set("limit", "10000")
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		if err := c.bucket.Take(ctx); err != nil {
			return nil, err
		}
		reqURL := c.base + "/v2/stocks/" + url.PathEscape(sym) + "/bars?" + q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("APCA-API-KEY-ID", c.keyID)
		req.Header.Set("APCA-API-SECRET-KEY", c.secret)
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("alpaca data: status=%d body=%s", resp.StatusCode, body)
		}
		var br barsResp
		if err := json.Unmarshal(body, &br); err != nil {
			return nil, fmt.Errorf("alpaca data decode: %w", err)
		}
		for _, b := range br.Bars {
			ts, err := time.Parse(time.RFC3339, b.T)
			if err != nil {
				continue
			}
			out = append(out, feed.Bar{
				Symbol: symbol, BucketMs: ts.UnixMilli(),
				O: b.O, H: b.H, L: b.L, C: b.C, Volume: b.V,
			})
		}
		if br.NextPageToken == nil || *br.NextPageToken == "" {
			break
		}
		pageToken = *br.NextPageToken
	}
	return out, nil
}
