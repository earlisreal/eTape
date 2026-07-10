// Package yahoo is a read-only client for Yahoo Finance's v8 chart API
// (query1.finance.yahoo.com), eTape's zero-config daily-history fallback when
// Alpaca is not configured. Daily only — Intraday1m returns
// ErrIntradayUnsupported (the orchestrator never routes 1m to it). It
// implements backfill.HistFetcher structurally (no explicit assertion, to
// avoid an import cycle — see hist/alpaca).
//
// Adjusted OHLC: each bar's O/H/L/C is scaled by adjclose/close, matching
// Alpaca adjustment=all (current price real, past scaled for splits +
// dividends). Daily bucket keys are normalized to ET-midnight so they align
// with Alpaca's daily t=00:00-ET convention and upsert cleanly on a provider
// switch instead of duplicating the day.
package yahoo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

const defaultBase = "https://query1.finance.yahoo.com"

// browserUA is required: the v8 chart endpoint 403s a default Go User-Agent.
const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// ErrIntradayUnsupported is returned by Intraday1m: Yahoo is daily-only and is
// never placed in the orchestrator's 1m chain.
var ErrIntradayUnsupported = errors.New("yahoo: intraday 1m not supported")

// Client is the Yahoo daily-bars transport.
type Client struct {
	base   string
	hc     *http.Client
	clk    clock.Clock
	bucket *netx.TokenBucket
}

// New builds a Client. base defaults to the production host; tests pass a mock
// server URL. Conservative rate limit (~30 req/60 s, burst 5) since this is an
// unofficial endpoint.
func New(base string, clk clock.Clock) *Client {
	if base == "" {
		base = defaultBase
	}
	return &Client{
		base:   base,
		hc:     netx.NewHTTPClient(15 * time.Second),
		clk:    clk,
		bucket: netx.NewTokenBucket(clk, 30.0/60.0, 5),
	}
}

// Intraday1m is unsupported — Yahoo serves only daily here.
func (c *Client) Intraday1m(context.Context, string, time.Time, time.Time) ([]feed.Bar, error) {
	return nil, ErrIntradayUnsupported
}

// chartResp mirrors the v8 chart JSON. Quote/adjclose arrays are pointer
// slices so Yahoo's occasional null entries decode as nil and are skipped.
type chartResp struct {
	Chart struct {
		Result []struct {
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Close  []*float64 `json:"close"`
					Volume []*int64   `json:"volume"`
				} `json:"quote"`
				AdjClose []struct {
					AdjClose []*float64 `json:"adjclose"`
				} `json:"adjclose"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

// DailyBars fetches adjusted daily bars for [from, to]. It always uses explicit
// period1/period2 epoch seconds + interval=1d and never range= (range=
// silently coarsens to weekly and truncates — see the spec).
func (c *Client) DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	q := url.Values{}
	q.Set("period1", strconv.FormatInt(from.Unix(), 10))
	q.Set("period2", strconv.FormatInt(to.Unix(), 10))
	q.Set("interval", "1d")
	q.Set("includeAdjustedClose", "true")
	reqURL := c.base + "/v8/finance/chart/" + url.PathEscape(yahooSymbol(symbol)) + "?" + q.Encode()

	body, err := c.get(ctx, reqURL)
	if err != nil {
		return nil, err
	}
	var cr chartResp
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("yahoo decode: %w", err)
	}
	if cr.Chart.Error != nil {
		return nil, fmt.Errorf("yahoo chart error: %s %s", cr.Chart.Error.Code, cr.Chart.Error.Description)
	}
	if len(cr.Chart.Result) == 0 {
		return nil, nil
	}
	res := cr.Chart.Result[0]
	if len(res.Indicators.Quote) == 0 || len(res.Indicators.AdjClose) == 0 {
		return nil, nil
	}
	qv := res.Indicators.Quote[0]
	adj := res.Indicators.AdjClose[0].AdjClose
	out := make([]feed.Bar, 0, len(res.Timestamp))
	for i, ts := range res.Timestamp {
		if i >= len(qv.Open) || i >= len(qv.High) || i >= len(qv.Low) || i >= len(qv.Close) || i >= len(adj) {
			continue
		}
		if qv.Open[i] == nil || qv.High[i] == nil || qv.Low[i] == nil || qv.Close[i] == nil || adj[i] == nil {
			continue // Yahoo emits occasional all-null rows
		}
		closeP := *qv.Close[i]
		if closeP == 0 {
			continue
		}
		scale := *adj[i] / closeP
		var vol int64
		if i < len(qv.Volume) && qv.Volume[i] != nil {
			vol = *qv.Volume[i]
		}
		out = append(out, feed.Bar{
			Symbol:   symbol,
			BucketMs: etMidnightMs(ts),
			O:        *qv.Open[i] * scale,
			H:        *qv.High[i] * scale,
			L:        *qv.Low[i] * scale,
			C:        closeP * scale,
			Volume:   vol,
		})
	}
	return out, nil
}

// get issues the request with the required browser UA and rate limiting. On a
// single 429 it waits one backoff interval and retries once, then errors so the
// orchestrator chain advances.
func (c *Client) get(ctx context.Context, reqURL string) ([]byte, error) {
	bo := netx.Backoff{Min: 500 * time.Millisecond, Max: 2 * time.Second}
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.bucket.Take(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", browserUA)
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			select {
			case <-c.clk.After(bo.Next()):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("yahoo: status=%d body=%s", resp.StatusCode, body)
		}
		return body, nil
	}
	return nil, fmt.Errorf("yahoo: status=429 after one retry")
}

// yahooSymbol maps eTape's US.-prefixed symbol to Yahoo's ticker: strip US.,
// dots→dashes for share classes (US.BRK.B → BRK-B).
func yahooSymbol(symbol string) string {
	return strings.ReplaceAll(strings.TrimPrefix(symbol, "US."), ".", "-")
}

// etMidnightMs normalizes a Yahoo daily timestamp (session-open instant) to
// ET-midnight of that calendar day, matching Alpaca's daily t=00:00-ET bucket.
func etMidnightMs(sec int64) int64 {
	et := time.Unix(sec, 0).In(session.Loc())
	return time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, session.Loc()).UnixMilli()
}
