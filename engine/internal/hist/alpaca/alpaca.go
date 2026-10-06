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
	"strconv"
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
	base     string
	keyID    string
	secret   string
	feed     string
	hc       *http.Client
	clk      clock.Clock
	bucket   *netx.TokenBucket
	readyAt  time.Time
	cooldown *netx.RestartCooldown
}

// New builds a Client. base defaults to the production data host; feedName
// defaults to "sip" (free tier serves SIP historical bars). Tests pass a mock
// server URL.
func New(base, keyID, secret, feedName string, clk clock.Clock) *Client {
	if base == "" {
		base = defaultDataBase
	}
	if feedName == "" {
		feedName = "sip"
	}
	client := &Client{
		base: base, keyID: keyID, secret: secret, feed: feedName,
		hc:  netx.NewHTTPClient(15 * time.Second),
		clk: clk,
		// Two tokens let Scanner history spend one while reserving one for
		// foreground chart history through TakeWithReserve(ctx, 1).
		bucket: netx.NewTokenBucket(clk, 150.0/60.0, 2),
	}
	if base == defaultDataBase {
		client.readyAt = clk.Now().Add(time.Minute)
	}
	return client
}

// SetRestartCooldown must be called before the client is used.
func (c *Client) SetRestartCooldown(cooldown *netx.RestartCooldown) {
	c.cooldown = cooldown
	c.readyAt = cooldown.ReadyAt()
}

func (c *Client) DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	if cutoff := c.clk.Now().Add(-24 * time.Hour); to.After(cutoff) {
		to = cutoff
	}
	if !to.After(from) {
		return nil, nil
	}
	return c.bars(ctx, symbol, "1Day", "all", from, to, false)
}

// ScannerDailyBars returns raw daily volumes for Scanner's REL VOL baseline.
// Chart history keeps the adjusted DailyBars path above; Scanner must preserve
// provider-reported share counts across splits.
func (c *Client) ScannerDailyBars(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	if !to.After(from) {
		return nil, nil
	}
	return c.bars(ctx, symbol, "1Day", "raw", from, to, true)
}

// recentSIPClampBuffer backs off the 1m window end when Alpaca returns a 403
// for too-recent SIP data. The free SIP feed's 15-minute recency rule is
// normally enforced by silent server-side clamping (HTTP 200, last bar at
// now−16m), but this defensive retry covers a return to the old 403 behavior.
const recentSIPClampBuffer = 16 * time.Minute

func (c *Client) Intraday1m(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	if cutoff := c.clk.Now().Add(-recentSIPClampBuffer); to.After(cutoff) {
		to = cutoff
	}
	if !to.After(from) {
		return nil, nil
	}
	return c.bars(ctx, symbol, "1Min", "raw", from, to, false)
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
//
// adjustment: DailyBars passes "all" (split + dividend, closest to moomoo
// forward-rehab) — daily stays adjusted for continuous official prices.
// Intraday1m passes "raw" (unadjusted) so 1m/cascaded 5m/15m/etc. history
// matches the raw scale of the live tick/quote feed; forward adjustment on
// intraday would scale pre-split bars up by the split ratio, corrupting
// anything computed over a window straddling a reverse split (moomoo mirrors
// this split — see opend/backfill.go's historyBars).
func (c *Client) waitStartup(ctx context.Context) error {
	if delay := c.readyAt.Sub(c.clk.Now()); delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.clk.After(delay):
		}
	}
	return ctx.Err()
}

func (c *Client) bars(ctx context.Context, symbol, timeframe, adjustment string, from, to time.Time, background bool) ([]feed.Bar, error) {
	if err := c.waitStartup(ctx); err != nil {
		return nil, err
	}
	sym := strings.TrimPrefix(symbol, "US.")
	var out []feed.Bar
	pageToken := ""
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("timeframe", timeframe)
		q.Set("start", from.UTC().Format(time.RFC3339))
		q.Set("end", to.UTC().Format(time.RFC3339))
		q.Set("adjustment", adjustment)
		q.Set("feed", c.feed)
		q.Set("limit", "10000")
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		var err error
		if background {
			err = c.bucket.TakeWithReserve(ctx, 1)
		} else {
			err = c.bucket.Take(ctx)
		}
		if err != nil {
			return nil, err
		}
		reqURL := c.base + "/v2/stocks/" + url.PathEscape(sym) + "/bars?" + q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("APCA-API-KEY-ID", c.keyID)
		req.Header.Set("APCA-API-SECRET-KEY", c.secret)
		if c.cooldown != nil {
			if err := c.cooldown.Record(); err != nil {
				return nil, err
			}
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		budget := alpacaResponseBudget(resp.Header, resp.StatusCode, c.clk.Now())
		if budget.remaining >= 0 && !budget.resetAt.IsZero() {
			c.bucket.ObserveBudget(budget.remaining, budget.resetAt)
		}
		if !budget.deferUntil.IsZero() {
			c.bucket.DeferUntil(budget.deferUntil)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if c.cooldown != nil && !budget.deferUntil.IsZero() {
			if err := c.cooldown.DeferUntil(budget.deferUntil); err != nil {
				return nil, err
			}
		}
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
		if page == maxPages-1 {
			return nil, fmt.Errorf("alpaca data: pagination exceeded %d pages", maxPages)
		}
		pageToken = *br.NextPageToken
	}
	return out, nil
}

type alpacaBudget struct {
	remaining  int
	resetAt    time.Time
	deferUntil time.Time
}

func alpacaResponseBudget(headers http.Header, status int, now time.Time) alpacaBudget {
	budget := alpacaBudget{remaining: -1}
	if remaining, err := strconv.Atoi(headers.Get("X-RateLimit-Remaining")); err == nil && remaining >= 0 {
		budget.remaining = remaining
	}
	budget.resetAt = alpacaResetAt(headers.Get("X-RateLimit-Reset"))
	if budget.remaining == 0 {
		budget.deferUntil = budget.resetAt
	}
	if status == http.StatusTooManyRequests {
		budget.deferUntil = alpacaRetryAt(headers.Get("Retry-After"), now)
		if budget.resetAt.After(budget.deferUntil) {
			budget.deferUntil = budget.resetAt
		}
		if budget.deferUntil.IsZero() {
			budget.deferUntil = now.Add(time.Second)
		}
	}
	return budget
}

func alpacaResetAt(value string) time.Time {
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	whole := int64(seconds)
	nanos := int64((seconds - float64(whole)) * float64(time.Second))
	return time.Unix(whole, nanos)
}

func alpacaRetryAt(value string, now time.Time) time.Time {
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(now) {
		return retryAt
	}
	return time.Time{}
}
