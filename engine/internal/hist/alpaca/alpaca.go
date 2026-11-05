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
	"github.com/earlisreal/eTape/engine/internal/session"
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

// ScreenerDailyBatch reads split-adjusted SIP bars for several symbols at once.
// Daily OHLC excludes extended-hours trades. Volume is Alpaca's reported volume.
func (c *Client) ScreenerDailyBatch(ctx context.Context, symbols []string, from, date time.Time) (map[string][]feed.Bar, error) {
	cutoff := c.clk.Now().Add(-recentSIPClampBuffer)
	if cutoff.Before(session.Schedule(date).Close) {
		return nil, fmt.Errorf("the regular session is still within Alpaca's SIP delay")
	}
	if err := c.waitStartup(ctx); err != nil {
		return nil, err
	}
	to := date.AddDate(0, 0, 1).Add(-time.Second)
	if cutoff.Before(to) {
		to = cutoff
	}
	codes := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		codes = append(codes, strings.TrimPrefix(symbol, "US."))
	}
	q := c.barQuery("1Day", "split", from, to)
	q.Set("feed", "sip")
	q.Set("symbols", strings.Join(codes, ","))
	out := make(map[string][]feed.Bar, len(symbols))
	for range maxPages {
		var page struct {
			Bars          map[string][]barJSON `json:"bars"`
			NextPageToken string               `json:"next_page_token"`
		}
		if err := c.barPage(ctx, "/v2/stocks/bars", q, true, &page); err != nil {
			return nil, err
		}
		for code, bars := range page.Bars {
			symbol := "US." + code
			out[symbol] = appendBars(out[symbol], symbol, bars)
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		if page.NextPageToken == q.Get("page_token") {
			return nil, fmt.Errorf("alpaca data: pagination did not advance")
		}
		q.Set("page_token", page.NextPageToken)
	}
	return nil, fmt.Errorf("alpaca data: pagination exceeded %d pages", maxPages)
}

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

// bars keeps the US. prefix on returned bars, stripping it for the URL path.
// Chart daily bars use all adjustments, while minute bars stay on the raw
// price scale of live ticks. Scanner daily volume also uses raw bars.
func (c *Client) bars(ctx context.Context, symbol, timeframe, adjustment string, from, to time.Time, background bool) ([]feed.Bar, error) {
	if err := c.waitStartup(ctx); err != nil {
		return nil, err
	}
	sym := strings.TrimPrefix(symbol, "US.")
	path := "/v2/stocks/" + url.PathEscape(sym) + "/bars"
	q := c.barQuery(timeframe, adjustment, from, to)
	var out []feed.Bar
	for range maxPages {
		var br barsResp
		if err := c.barPage(ctx, path, q, background, &br); err != nil {
			return nil, err
		}
		out = appendBars(out, symbol, br.Bars)
		if br.NextPageToken == nil || *br.NextPageToken == "" {
			return out, nil
		}
		q.Set("page_token", *br.NextPageToken)
	}
	return nil, fmt.Errorf("alpaca data: pagination exceeded %d pages", maxPages)
}

func (c *Client) barQuery(timeframe, adjustment string, from, to time.Time) url.Values {
	return url.Values{
		"timeframe": {timeframe}, "adjustment": {adjustment}, "feed": {c.feed}, "limit": {"10000"},
		"start": {from.UTC().Format(time.RFC3339)}, "end": {to.UTC().Format(time.RFC3339)},
	}
}

func appendBars(out []feed.Bar, symbol string, bars []barJSON) []feed.Bar {
	for _, b := range bars {
		ts, err := time.Parse(time.RFC3339, b.T)
		if err != nil {
			continue
		}
		out = append(out, feed.Bar{
			Symbol: symbol, BucketMs: ts.UnixMilli(),
			O: b.O, H: b.H, L: b.L, C: b.C, Volume: b.V,
		})
	}
	return out
}

func (c *Client) barPage(ctx context.Context, path string, query url.Values, background bool, out any) error {
	var err error
	if background {
		err = c.bucket.TakeWithReserve(ctx, 1)
	} else {
		err = c.bucket.Take(ctx)
	}
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("APCA-API-KEY-ID", c.keyID)
	req.Header.Set("APCA-API-SECRET-KEY", c.secret)
	if c.cooldown != nil {
		if err := c.cooldown.Record(); err != nil {
			return err
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	budget := alpacaResponseBudget(resp.Header, resp.StatusCode, c.clk.Now())
	if budget.remaining >= 0 && !budget.resetAt.IsZero() {
		c.bucket.ObserveBudget(budget.remaining, budget.resetAt)
	}
	if !budget.deferUntil.IsZero() {
		c.bucket.DeferUntil(budget.deferUntil)
		if c.cooldown != nil {
			if err := c.cooldown.DeferUntil(budget.deferUntil); err != nil {
				return err
			}
		}
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("alpaca data: status=%d body=%s", resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("alpaca data decode: %w", err)
	}
	return nil
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
