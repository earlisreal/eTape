# History Bars Multi-Provider Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route historical bars by resolution across Alpaca / Yahoo / a quota-free moomoo tail so `request_history_kline` (quota-spending) becomes a last resort, and cold charts render in <1 s.

**Architecture:** Replace the orchestrator's single `primary`/`fallback` pair with two ordered provider *chains* (daily = `[alpaca?, yahoo?, moomoo]`, 1m-deep = `[alpaca?, moomoo]`) plus a quota-free `TailFetcher` (moomoo `Qot_GetKL`, ≤1,000 recent 1m bars). Per-symbol sequence becomes warmStart → tail-seed → trimmed deep-1m → daily. A new zero-config Yahoo daily client and small Alpaca/opend/config changes feed the chains; wiring builds them in `main.go`.

**Tech Stack:** Go 1.x, `net/http` + `internal/broker/netx` (keep-alive client, token bucket, backoff), `encoding/json`, `httptest` for unit tests, existing `mock_opend_test.go` harness for the moomoo tail.

## Global Constraints

- **This is engine-only.** No UI, no `wsmsg`/`payloads.go`, no TS types. `make gen-ts-check` must show **no diff**.
- **No new credentials.** Yahoo is credential-free. Alpaca reuses the existing `resolveBackfillAlpacaCreds` (still refuses `alpaca-live`). No Venues/credentials UI change.
- **Intraday bars are unadjusted** (`adjustment=raw` / moomoo `RehabType_None`); **daily bars are adjusted** (`adjustment=all` / Yahoo `adjclose/close` scaling). This is load-bearing — see the `HistFetcher` doc comment in `backfill.go:21-31`.
- **Bars are ascending, bucket-START keyed.** `[]feed.Bar` fields: `Symbol, BucketMs, O, H, L, C, Volume` (+ `Turnover`, left 0 by new providers). A source with no data returns `(nil, nil)`.
- **Symbol convention:** strip `US.` for the outbound request, dots→dashes for share classes (`US.BRK.B`→`BRK-B`), re-add `US.` on returned bars (mirrors `hist/alpaca/alpaca.go:93`).
- **Never call `Date.now()`-equivalents in a way that breaks tests** — providers take a `clock.Clock`; use `c.clk.Now()`.
- Every task ends green: `cd engine && go build ./... && go vet ./... && go test ./...`.
- Spec: `docs/superpowers/specs/2026-07-10-history-bars-providers-design.md`. Do not deviate from its routing table, overlap rule (moomoo wins), or config semantics.

---

## File Structure

- **Create** `engine/internal/hist/yahoo/yahoo.go` — zero-config daily-only client (v8 chart), implements `HistFetcher` structurally (`Intraday1m` returns not-supported).
- **Create** `engine/internal/hist/yahoo/yahoo_test.go`.
- **Modify** `engine/internal/hist/alpaca/alpaca.go` — default feed `iex`→`sip`; `Intraday1m` 403-recent retry-with-clamp.
- **Modify** `engine/internal/hist/alpaca/alpaca_test.go` — add sip-default + 403-retry tests.
- **Modify** `engine/internal/config/config.go` — add `[backfill.yahoo]`; flip alpaca feed default to `sip`; document `daily_years` floor semantics.
- **Modify** `engine/internal/config/config_test.go` — assert new defaults + yahoo kill switch decode.
- **Modify** `engine/internal/feed/opend/opendfeed.go` — add public `Tail1m(ctx, symbol) ([]feed.Bar, error)`.
- **Modify** `engine/internal/feed/opend/opendfeed_test.go` (or `backfill_test.go`) — add `Tail1m` test.
- **Modify** `engine/internal/backfill/backfill.go` — chains + `Source` + `TailFetcher`; revised `Backfill` sequence; `dailyFrom` 2016 floor; delete `gapThresholdMs` + old `fill1m`/`fillDaily`.
- **Rewrite** `engine/internal/backfill/backfill_test.go` — new API + new-behavior tests.
- **Modify** `engine/cmd/etape/main.go` — build chains, wire `Tail1m`.

---

## Task 1: Yahoo daily provider

**Files:**
- Create: `engine/internal/hist/yahoo/yahoo.go`
- Test: `engine/internal/hist/yahoo/yahoo_test.go`

**Interfaces:**
- Produces: `yahoo.New(base string, clk clock.Clock) *Client`; `(*Client).DailyBars(ctx, symbol string, from, to time.Time) ([]feed.Bar, error)`; `(*Client).Intraday1m(...) ([]feed.Bar, error)` returning `yahoo.ErrIntradayUnsupported`. Structurally satisfies `backfill.HistFetcher` (do NOT add an explicit `var _ backfill.HistFetcher` — that would create an import cycle; alpaca doesn't either).

- [ ] **Step 1: Write the failing test**

```go
package yahoo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// oneDayResp is a minimal v8 chart body: 2016-01-04 (a Monday). timestamp is
// the session-open instant (14:30Z summer / here 14:30Z is fine for the test);
// adjclose < close so the scaling factor is observable.
const oneDayResp = `{"chart":{"result":[{"timestamp":[1451919600],` +
	`"indicators":{"quote":[{"open":[10],"high":[11],"low":[9],"close":[10],"volume":[1000]}],` +
	`"adjclose":[{"adjclose":[5]}]}}],"error":null}}`

func TestDailyBarsScalesAndBucketsToETMidnight(t *testing.T) {
	var gotPath, gotInterval, gotRange, gotUA string
	mux := http.NewServeMux()
	mux.HandleFunc("/v8/finance/chart/AAPL", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotInterval = r.URL.Query().Get("interval")
		gotRange = r.URL.Query().Get("range")
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(oneDayResp))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, clock.NewFake(time.UnixMilli(0)))
	bars, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(2_000_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v8/finance/chart/AAPL" || gotInterval != "1d" || gotRange != "" {
		t.Fatalf("request = path %q interval %q range %q (range must be empty — never use range=)", gotPath, gotInterval, gotRange)
	}
	if !strings.Contains(strings.ToLower(gotUA), "mozilla") {
		t.Fatalf("User-Agent = %q, want a browser UA", gotUA)
	}
	if len(bars) != 1 {
		t.Fatalf("bars = %d, want 1", len(bars))
	}
	b := bars[0]
	// adjclose/close = 5/10 = 0.5 scaling on every OHLC; volume unscaled.
	if b.Symbol != "US.AAPL" || b.O != 5 || b.H != 5.5 || b.L != 4.5 || b.C != 5 || b.Volume != 1000 {
		t.Fatalf("scaled bar = %+v", b)
	}
	// Bucket normalized to ET-midnight of 2016-01-04.
	wantMid := time.Date(2016, 1, 4, 0, 0, 0, 0, session.Loc()).UnixMilli()
	if b.BucketMs != wantMid {
		t.Fatalf("BucketMs = %d, want ET-midnight %d", b.BucketMs, wantMid)
	}
}

func TestDailyBarsSkipsNullRows(t *testing.T) {
	body := `{"chart":{"result":[{"timestamp":[1451919600,1452006000],` +
		`"indicators":{"quote":[{"open":[10,null],"high":[11,null],"low":[9,null],"close":[10,null],"volume":[1000,null]}],` +
		`"adjclose":[{"adjclose":[10,null]}]}}],"error":null}}`
	mux := http.NewServeMux()
	mux.HandleFunc("/v8/finance/chart/AAPL", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, clock.NewFake(time.UnixMilli(0)))
	bars, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(2_000_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 1 {
		t.Fatalf("bars = %d, want 1 (the all-null second row is skipped)", len(bars))
	}
}

func TestDailyBarsMapsShareClassSymbol(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/v8/finance/chart/BRK-B", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(oneDayResp))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, clock.NewFake(time.UnixMilli(0)))
	bars, err := c.DailyBars(context.Background(), "US.BRK.B", time.UnixMilli(0), time.UnixMilli(2_000_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v8/finance/chart/BRK-B" {
		t.Fatalf("path = %q, want /v8/finance/chart/BRK-B (dots→dashes)", gotPath)
	}
	if len(bars) != 1 || bars[0].Symbol != "US.BRK.B" {
		t.Fatalf("returned symbol = %+v, want US.BRK.B preserved", bars)
	}
}

func TestDailyBars429RetriesThenErrors(t *testing.T) {
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/v8/finance/chart/AAPL", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// System clock so the single backoff sleep actually elapses (~500ms).
	c := New(srv.URL, clock.System{})
	_, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(2_000_000_000_000))
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want a 429 error after retry", err)
	}
	if calls != 2 {
		t.Fatalf("http calls = %d, want 2 (one retry then give up)", calls)
	}
}

func TestIntraday1mUnsupported(t *testing.T) {
	c := New("", clock.NewFake(time.UnixMilli(0)))
	if _, err := c.Intraday1m(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(1)); err != ErrIntradayUnsupported {
		t.Fatalf("Intraday1m err = %v, want ErrIntradayUnsupported", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/hist/yahoo/...`
Expected: FAIL — package/`New` not defined.

- [ ] **Step 3: Write the implementation**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/hist/yahoo/...`
Expected: PASS (all 5 tests). The 429 test takes ~0.5 s.

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/hist/yahoo/ && git commit -m "feat(hist): add zero-config Yahoo daily-bars provider"
```

---

## Task 2: Alpaca provider — sip default + 403-recent retry

**Files:**
- Modify: `engine/internal/hist/alpaca/alpaca.go` (`New` default feed; `Intraday1m`)
- Test: `engine/internal/hist/alpaca/alpaca_test.go`

**Interfaces:**
- Consumes: existing `alpaca.New(base, keyID, secret, feedName string, clk clock.Clock) *Client` (unchanged signature).
- Produces: `New("", ...)` now defaults `feed=sip`; `Intraday1m` retries once with `end = now−16m` on a 403 mentioning recent SIP data.

- [ ] **Step 1: Write the failing tests** (append to `alpaca_test.go`)

```go
func TestNewDefaultsToSIP(t *testing.T) {
	var gotFeed string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, r *http.Request) {
		gotFeed = r.URL.Query().Get("feed")
		_, _ = w.Write([]byte(`{"bars":[],"next_page_token":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "K", "S", "", clock.NewFake(time.UnixMilli(0))) // empty feed => default
	if _, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(1<<40)); err != nil {
		t.Fatal(err)
	}
	if gotFeed != "sip" {
		t.Fatalf("default feed = %q, want sip", gotFeed)
	}
}

func TestIntraday1mRetriesOnRecentSIP403(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	var calls int
	var secondEnd string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"message":"subscription does not permit querying recent SIP data"}`))
			return
		}
		secondEnd = r.URL.Query().Get("end")
		_, _ = w.Write([]byte(`{"bars":[{"t":"2026-07-08T15:00:00Z","o":1,"h":1,"l":1,"c":1,"v":1}],"next_page_token":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "K", "S", "sip", clock.NewFake(now))
	bars, err := c.Intraday1m(context.Background(), "US.AAPL", time.UnixMilli(0), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("err = %v, want the clamped retry to succeed", err)
	}
	if calls != 2 || len(bars) != 1 {
		t.Fatalf("calls = %d bars = %d, want 2 and 1", calls, len(bars))
	}
	// Retry clamps end to now−16m = 2026-07-08T11:44:00Z.
	wantEnd := now.Add(-16 * time.Minute).UTC().Format(time.RFC3339)
	if secondEnd != wantEnd {
		t.Fatalf("retry end = %q, want %q", secondEnd, wantEnd)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/hist/alpaca/... -run 'SIP|DefaultsToSIP'`
Expected: FAIL — `TestNewDefaultsToSIP` sees `iex`; `TestIntraday1mRetriesOnRecentSIP403` gets a 403 error (no retry).

- [ ] **Step 3: Edit `alpaca.go`**

In `New`, change the default and its doc line:

```go
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
	// ... unchanged ...
}
```

Replace `Intraday1m` and add helpers below it:

```go
// recentSIPClampBuffer backs off the 1m window end when Alpaca returns a 403
// for too-recent SIP data. The free SIP feed's 15-minute recency rule is
// normally enforced by silent server-side clamping (HTTP 200, last bar at
// now−16m), but this defensive retry covers a return to the old 403 behavior.
const recentSIPClampBuffer = 16 * time.Minute

// Intraday1m requests "raw" (unadjusted) bars up to `to` and accepts the
// server clamp. On a 403 mentioning recent SIP data it retries once with
// end = now−16m — see the adjustment comment on bars() for raw-vs-all.
func (c *Client) Intraday1m(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	bars, err := c.bars(ctx, symbol, "1Min", "raw", from, to)
	if err != nil && isRecentSIPForbidden(err) {
		clampedTo := c.clk.Now().Add(-recentSIPClampBuffer)
		if clampedTo.After(from) {
			return c.bars(ctx, symbol, "1Min", "raw", from, clampedTo)
		}
	}
	return bars, err
}

// isRecentSIPForbidden reports whether err is Alpaca's 403 for requesting SIP
// data inside the free-tier 15-minute recency window.
func isRecentSIPForbidden(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "status=403") && (strings.Contains(s, "recent") || strings.Contains(s, "subscription"))
}
```

- [ ] **Step 4: Run the full alpaca test package**

Run: `cd engine && go test ./internal/hist/alpaca/...`
Expected: PASS (new tests + the existing iex-explicit tests still pass — they pass `"iex"` literally).

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/hist/alpaca/ && git commit -m "feat(hist): alpaca defaults to sip + retries recent-SIP 403 with clamp"
```

---

## Task 3: Config — yahoo section + sip default

**Files:**
- Modify: `engine/internal/config/config.go`
- Test: `engine/internal/config/config_test.go`

**Interfaces:**
- Produces: `config.BackfillYahoo{Enabled bool}` at `config.Config.Backfill.Yahoo`; `Default().Backfill.Alpaca.Feed == "sip"`; `Default().Backfill.Yahoo.Enabled == true`.

- [ ] **Step 1: Write the failing test** (append to `config_test.go`)

```go
func TestBackfillDefaultsAndYahooKillSwitch(t *testing.T) {
	d := Default()
	if d.Backfill.Alpaca.Feed != "sip" {
		t.Fatalf("default alpaca feed = %q, want sip", d.Backfill.Alpaca.Feed)
	}
	if !d.Backfill.Yahoo.Enabled {
		t.Fatalf("default yahoo enabled = false, want true")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[backfill.yahoo]\nenabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Backfill.Yahoo.Enabled {
		t.Fatalf("yahoo.enabled = true after kill switch, want false")
	}
	// Alpaca feed default survives a file that doesn't mention it.
	if c.Backfill.Alpaca.Feed != "sip" {
		t.Fatalf("alpaca feed = %q after partial file, want sip default preserved", c.Backfill.Alpaca.Feed)
	}
}
```

(Ensure `config_test.go` imports `os`, `path/filepath` — add if missing.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd engine && go test ./internal/config/... -run TestBackfillDefaultsAndYahooKillSwitch`
Expected: FAIL — `Backfill.Yahoo` undefined / feed is `iex`.

- [ ] **Step 3: Edit `config.go`**

Add the struct after `BackfillAlpaca`:

```go
// BackfillYahoo is the [backfill.yahoo] section: the zero-config daily-history
// fallback (no credentials). A kill switch for when Yahoo's unofficial
// endpoint misbehaves.
type BackfillYahoo struct {
	Enabled bool `toml:"enabled"`
}
```

Add the field to `Backfill` and fix the `DailyYears` comment:

```go
type Backfill struct {
	Enabled      bool           `toml:"enabled"`
	IntradayDays int            `toml:"intraday_days"` // trading days of 1m history to backfill
	DailyYears   int            `toml:"daily_years"`   // 0 = since the 2016-01-01 provider floor; >0 = max(now−daily_years, 2016-01-01)
	Concurrency  int            `toml:"concurrency"`   // bounded boot worker pool
	SeedChunk    int            `toml:"seed_chunk"`    // vestigial: no longer read (see backfill.Config.SeedChunk); kept so an existing config.toml's seed_chunk key doesn't need editing
	Alpaca       BackfillAlpaca `toml:"alpaca"`
	Yahoo        BackfillYahoo  `toml:"yahoo"`
}
```

In `Default()`, update the `Backfill` block:

```go
Backfill: Backfill{Enabled: true, IntradayDays: 20, DailyYears: 0, Concurrency: 3, SeedChunk: 500,
	Alpaca: BackfillAlpaca{Enabled: true, CredsKey: "", Feed: "sip"},
	Yahoo:  BackfillYahoo{Enabled: true},
},
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd engine && go test ./internal/config/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/config/config.go internal/config/config_test.go && git commit -m "feat(config): add [backfill.yahoo] kill switch, default alpaca feed to sip"
```

---

## Task 4: OpenD `Tail1m` — quota-free moomoo tail as a public method

**Files:**
- Modify: `engine/internal/feed/opend/opendfeed.go`
- Test: `engine/internal/feed/opend/opendfeed_test.go`

**Interfaces:**
- Produces: `(*OpenDFeed).Tail1m(ctx context.Context, symbol string) ([]feed.Bar, error)` — `Qot_GetKL`, ≤1,000 recent 1m bars, quota-free. Satisfies `backfill.TailFetcher` (Task 5). Errors/empties are the moomoo response's own (no active K_1M subscription ⇒ error) — callers treat that as "skip tail".

- [ ] **Step 1: Write the failing test** (append to `opendfeed_test.go`)

```go
func TestTail1mReturnsCachedBars(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{bars1m: []*qotcommon.KLine{
		kl(1782146460, 309.1, 1000), // end-labeled: bucket = timestamp − 60 s
		kl(1782146520, 309.2, 1100),
	}})
	fd := NewOpenDFeed(liveClient(t, m), FeedOptions{})
	bars, err := fd.Tail1m(context.Background(), "US.AAPL")
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 || bars[0].BucketMs != 1782146460_000-60_000 {
		t.Fatalf("Tail1m bars = %+v", bars)
	}
}
```

(`opendfeed_test.go` already imports `qotcommon`; `kl`/`newMockOpenD`/`liveClient`/`qotData` are shared test helpers in the package.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd engine && go test ./internal/feed/opend/... -run TestTail1mReturnsCachedBars`
Expected: FAIL — `fd.Tail1m` undefined.

- [ ] **Step 3: Add the method to `opendfeed.go`** (next to `CachedBars1m`)

```go
// Tail1m returns the quota-free recent 1m window (≤1,000 bars) from moomoo's
// Qot_GetKL cache. It requires an active K_1M subscription; OpenD rejects the
// read otherwise, surfacing as an error the backfill orchestrator treats as
// "skip the tail step". Implements backfill.TailFetcher.
func (f *OpenDFeed) Tail1m(ctx context.Context, symbol string) ([]feed.Bar, error) {
	return f.bf.cachedBars1m(ctx, symbol, maxAPIRows)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd engine && go test ./internal/feed/opend/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/feed/opend/opendfeed.go internal/feed/opend/opendfeed_test.go && git commit -m "feat(opend): expose quota-free Tail1m (Qot_GetKL) for backfill"
```

---

## Task 5: Orchestrator provider-chains + revised sequence + wiring

This task changes `backfill.New`'s signature; `main.go` must land in the **same task** so `go build ./...` stays green. It depends on Tasks 1–4.

**Files:**
- Modify: `engine/internal/backfill/backfill.go`
- Rewrite: `engine/internal/backfill/backfill_test.go`
- Modify: `engine/cmd/etape/main.go`

**Interfaces:**
- Consumes: `yahoo.New`, `alpaca.New`, `(*OpenDFeed).Tail1m`, `config.Backfill.Yahoo.Enabled`, `config.Backfill.Alpaca.Feed`.
- Produces:
  - `backfill.Source{ Name string; HistFetcher }`
  - `backfill.TailFetcher interface { Tail1m(ctx context.Context, symbol string) ([]feed.Bar, error) }`
  - `backfill.New(daily, intraday []Source, tail TailFetcher, seeder Seeder, archive Archive, clk clock.Clock, cfg Config) *Orchestrator`
  - `MoomooFetcher(fd feed.Feed) HistFetcher` (unchanged).

- [ ] **Step 1: Rewrite `backfill_test.go` to the new API + new behaviors**

Replace the whole file with:

```go
package backfill

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func TestIntradayFromSkipsWeekends(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	if got, want := intradayFrom(now, 1), time.Date(2026, 7, 7, 0, 0, 0, 0, session.Loc()); !got.Equal(want) {
		t.Fatalf("intradayFrom(1) = %s, want %s", got, want)
	}
	if got, want := intradayFrom(now, 3), time.Date(2026, 7, 3, 0, 0, 0, 0, session.Loc()); !got.Equal(want) {
		t.Fatalf("intradayFrom(3) = %s, want %s", got, want)
	}
}

func TestSeedUnlessCanceledCallsSeedOnce(t *testing.T) {
	bars := make([]feed.Bar, 1200)
	for i := range bars {
		bars[i] = feed.Bar{Symbol: "US.AAPL", BucketMs: int64(i)}
	}
	var calls [][]feed.Bar
	seedUnlessCanceled(context.Background(), bars, func(b []feed.Bar) {
		calls = append(calls, append([]feed.Bar(nil), b...))
	})
	if len(calls) != 1 || len(calls[0]) != 1200 {
		t.Fatalf("calls = %d, want exactly 1 call of 1200", len(calls))
	}
	calls = nil
	seedUnlessCanceled(context.Background(), nil, func(b []feed.Bar) { calls = append(calls, b) })
	if len(calls) != 0 {
		t.Fatalf("empty input produced %d calls", len(calls))
	}
}

func TestSeedUnlessCanceledSkipsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	seedUnlessCanceled(ctx, []feed.Bar{{Symbol: "US.AAPL"}}, func(b []feed.Bar) { calls++ })
	if calls != 0 {
		t.Fatalf("seed calls with a pre-canceled ctx = %d, want 0", calls)
	}
}

// --- test doubles ---

type fakeFetcher struct {
	daily, m1  []feed.Bar
	dErr, mErr error
	m1Calls    atomic.Int32
	dCalls     atomic.Int32
}

func (f *fakeFetcher) DailyBars(_ context.Context, _ string, _, _ time.Time) ([]feed.Bar, error) {
	f.dCalls.Add(1)
	return f.daily, f.dErr
}
func (f *fakeFetcher) Intraday1m(_ context.Context, _ string, _, _ time.Time) ([]feed.Bar, error) {
	f.m1Calls.Add(1)
	return f.m1, f.mErr
}

type fakeTail struct {
	bars  []feed.Bar
	err   error
	calls atomic.Int32
}

func (t *fakeTail) Tail1m(_ context.Context, _ string) ([]feed.Bar, error) {
	t.calls.Add(1)
	return t.bars, t.err
}

type fakeSeeder struct {
	mu          sync.Mutex
	daily, hist []feed.Bar
	ticks       []feed.Tick
	calls       []string
}

func (s *fakeSeeder) SeedSessionTicks(_ string, t []feed.Tick) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ticks = append(s.ticks, t...)
	s.calls = append(s.calls, "ticks")
}
func (s *fakeSeeder) SeedDaily(_ string, b []feed.Bar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.daily = append(s.daily, b...)
	s.calls = append(s.calls, "daily")
}
func (s *fakeSeeder) SeedHistory1m(_ string, b []feed.Bar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hist = append(s.hist, b...)
	s.calls = append(s.calls, "hist")
}

type fakeArchive struct {
	mu            sync.Mutex
	daily, m1     []feed.Bar
	ticks         []feed.Tick
	ticksErr      error
	archivedDaily []feed.Bar
	archived1m    []feed.Bar
}

func (a *fakeArchive) ReadDailyBars(_ string) ([]feed.Bar, error)          { return a.daily, nil }
func (a *fakeArchive) ReadBars1m(_ string, _, _ int64) ([]feed.Bar, error) { return a.m1, nil }
func (a *fakeArchive) ReadJournalTicks(_ string, _ int64) ([]feed.Tick, error) {
	return a.ticks, a.ticksErr
}
func (a *fakeArchive) ArchiveBar1m(b feed.Bar) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.archived1m = append(a.archived1m, b)
}
func (a *fakeArchive) ArchiveDaily(b feed.Bar) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.archivedDaily = append(a.archivedDaily, b)
}

func bar(ms int64) feed.Bar  { return feed.Bar{Symbol: "US.AAPL", BucketMs: ms, C: 1} }
func tick(ms int64) feed.Tick { return feed.Tick{Symbol: "US.AAPL", TsMs: ms, Price: 1} }

// chain wraps fetchers as a named Source chain for New.
func chain(fs ...HistFetcher) []Source {
	out := make([]Source, len(fs))
	for i, f := range fs {
		out[i] = Source{Name: fmt.Sprintf("f%d", i), HistFetcher: f}
	}
	return out
}

func fixedNow() time.Time { return time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc()) }

// --- warm-start (unchanged behavior; new New() signature) ---

func TestWarmStartSeedsSessionTicksBeforeDailyAnd1m(t *testing.T) {
	seeder := &fakeSeeder{}
	archive := &fakeArchive{ticks: []feed.Tick{tick(1)}, daily: []feed.Bar{bar(1)}, m1: []feed.Bar{bar(2)}}
	o := New(nil, nil, nil, seeder, archive, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	o.warmStart(context.Background(), "US.AAPL", fixedNow().AddDate(0, 0, -20), fixedNow())
	if len(seeder.calls) < 3 || seeder.calls[0] != "ticks" {
		t.Fatalf("call order = %v, want session-ticks first", seeder.calls)
	}
}

func TestWarmStartTickReadErrorContinues(t *testing.T) {
	seeder := &fakeSeeder{}
	archive := &fakeArchive{ticksErr: context.DeadlineExceeded, daily: []feed.Bar{bar(1)}, m1: []feed.Bar{bar(2)}}
	o := New(nil, nil, nil, seeder, archive, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	o.warmStart(context.Background(), "US.AAPL", fixedNow().AddDate(0, 0, -20), fixedNow())
	if len(seeder.daily) != 1 || len(seeder.hist) != 1 {
		t.Fatalf("daily=%d hist=%d, want 1 and 1", len(seeder.daily), len(seeder.hist))
	}
}

// --- dailyFrom 2016 floor ---

func TestDailyFromFloorsAt2016(t *testing.T) {
	floor := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
	o := New(nil, nil, nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{DailyYears: 0})
	if got := o.dailyFrom(fixedNow()); !got.Equal(floor) {
		t.Fatalf("dailyFrom(DailyYears=0) = %s, want 2016 floor", got)
	}
	o = New(nil, nil, nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{DailyYears: 100})
	if got := o.dailyFrom(fixedNow()); !got.Equal(floor) {
		t.Fatalf("dailyFrom(DailyYears=100) = %s, want clamp to 2016 floor", got)
	}
	o = New(nil, nil, nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{DailyYears: 1})
	if got, want := o.dailyFrom(fixedNow()), fixedNow().AddDate(-1, 0, 0); !got.Equal(want) {
		t.Fatalf("dailyFrom(DailyYears=1) = %s, want %s (not clamped)", got, want)
	}
}

// --- tail + deep 1m ---

// TestTailSeedsFirstThenDeep proves progressive seeding order (tail before
// deep) and daily-after-1m using a cold archive so warm-start seeds nothing.
func TestTailSeedsFirstThenDeep(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(100)}, daily: []feed.Bar{bar(1)}}
	tail := &fakeTail{bars: []feed.Bar{bar(1000)}}
	seeder := &fakeSeeder{}
	o := New(chain(deep), chain(deep), tail, seeder, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	_ = o.Backfill(context.Background(), "US.AAPL")

	if want := []string{"hist", "hist", "daily"}; fmt.Sprint(seeder.calls) != fmt.Sprint(want) {
		t.Fatalf("seed order = %v, want %v (tail 1m, deep 1m, then daily)", seeder.calls, want)
	}
	// tail bar (1000) seeded before deep bar (100).
	if len(seeder.hist) != 2 || seeder.hist[0].BucketMs != 1000 || seeder.hist[1].BucketMs != 100 {
		t.Fatalf("hist bars = %+v, want tail(1000) then deep(100)", seeder.hist)
	}
}

// TestTailWinsTrimsDeepOverlap: deep bars at/after the tail's oldest bar are
// dropped before seeding/archiving.
func TestTailWinsTrimsDeepOverlap(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(940), bar(1000), bar(1060)}}
	tail := &fakeTail{bars: []feed.Bar{bar(1000), bar(1060)}}
	seeder := &fakeSeeder{}
	archive := &fakeArchive{}
	o := New(chain(deep), chain(deep), tail, seeder, archive, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	_ = o.Backfill(context.Background(), "US.AAPL")

	// hist = tail(2) + deep-trimmed(1: only 940).
	if len(seeder.hist) != 3 {
		t.Fatalf("hist seeded = %d, want 3 (tail 2 + trimmed deep 1)", len(seeder.hist))
	}
	if last := seeder.hist[2]; last.BucketMs != 940 {
		t.Fatalf("trimmed deep bar = %d, want only 940 (strictly older than tail oldest 1000)", last.BucketMs)
	}
	if len(archive.archived1m) != 3 {
		t.Fatalf("archived 1m = %d, want 3", len(archive.archived1m))
	}
}

// TestTailFailUsesDeepUntrimmed: a tail error ⇒ the deep set is seeded whole.
func TestTailFailUsesDeepUntrimmed(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(940), bar(1000)}}
	tail := &fakeTail{err: errors.New("not subscribed")}
	seeder := &fakeSeeder{}
	o := New(chain(deep), chain(deep), tail, seeder, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	_ = o.Backfill(context.Background(), "US.AAPL")

	if tail.calls.Load() != 1 {
		t.Fatalf("tail calls = %d, want 1", tail.calls.Load())
	}
	if len(seeder.hist) != 2 {
		t.Fatalf("hist seeded = %d, want 2 (deep untrimmed, no tail)", len(seeder.hist))
	}
}

// TestNilTailSkipsTailStep: replay/demo (no OpenD) — tail nil, deep untrimmed.
func TestNilTailSkipsTailStep(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(940), bar(1000)}}
	seeder := &fakeSeeder{}
	o := New(chain(deep), chain(deep), nil, seeder, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	_ = o.Backfill(context.Background(), "US.AAPL")
	if len(seeder.hist) != 2 {
		t.Fatalf("hist seeded = %d, want 2 (nil tail skipped)", len(seeder.hist))
	}
}

// --- chain-walk ---

func TestChainWalkAdvancesOnErrorThenEmptyThenServes(t *testing.T) {
	errF := &fakeFetcher{dErr: context.DeadlineExceeded}
	emptyF := &fakeFetcher{} // (nil, nil)
	goodF := &fakeFetcher{daily: []feed.Bar{bar(1), bar(2)}}
	seeder := &fakeSeeder{}
	o := New([]Source{
		{Name: "err", HistFetcher: errF},
		{Name: "empty", HistFetcher: emptyF},
		{Name: "good", HistFetcher: goodF},
	}, nil, nil, seeder, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	if err := o.Backfill(context.Background(), "US.AAPL"); err != nil {
		t.Fatalf("Backfill err = %v, want nil (good served)", err)
	}
	if len(seeder.daily) != 2 {
		t.Fatalf("daily seeded = %d, want 2 (from the 3rd provider)", len(seeder.daily))
	}
	if errF.dCalls.Load() != 1 || emptyF.dCalls.Load() != 1 || goodF.dCalls.Load() != 1 {
		t.Fatalf("chain not walked in order: err=%d empty=%d good=%d", errF.dCalls.Load(), emptyF.dCalls.Load(), goodF.dCalls.Load())
	}
}

// TestDailyChainAllErrorReturnsError pins the uihub re-arm signal: a daily
// error from every provider (e.g. moomoo ErrHistoryQuotaExhausted last resort)
// surfaces so the hub retries on reconnect.
func TestDailyChainAllErrorReturnsError(t *testing.T) {
	a := &fakeFetcher{dErr: context.DeadlineExceeded}
	b := &fakeFetcher{dErr: errors.New("quota exhausted")}
	o := New(chain(a, b), nil, nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	if err := o.Backfill(context.Background(), "US.AAPL"); err == nil {
		t.Fatal("Backfill err = nil, want the last daily error")
	}
}

func TestDailyAllEmptyReturnsNil(t *testing.T) {
	o := New(chain(&fakeFetcher{}, &fakeFetcher{}), nil, nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	if err := o.Backfill(context.Background(), "US.AAPL"); err != nil {
		t.Fatalf("Backfill err = %v, want nil (no data is not a failure)", err)
	}
}

func TestBackfillArchivesFreshFetch(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(10), bar(11)}}
	daily := &fakeFetcher{daily: []feed.Bar{bar(1), bar(2)}}
	archive := &fakeArchive{}
	o := New(chain(daily), chain(deep), nil, &fakeSeeder{}, archive, clock.NewFake(fixedNow()), Config{IntradayDays: 20})
	_ = o.Backfill(context.Background(), "US.AAPL")
	if len(archive.archivedDaily) != 2 || len(archive.archived1m) != 2 {
		t.Fatalf("archived daily=%d 1m=%d, want 2 and 2", len(archive.archivedDaily), len(archive.archived1m))
	}
}

func TestRunBoundedPoolCoversEverySymbol(t *testing.T) {
	deep := &fakeFetcher{m1: []feed.Bar{bar(10)}}
	o := New(nil, chain(deep), nil, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(fixedNow()), Config{Concurrency: 2, IntradayDays: 20})
	o.Run(context.Background(), []string{"US.AAPL", "US.TSLA", "US.MSFT"})
	if got := deep.m1Calls.Load(); got != 3 {
		t.Fatalf("Intraday1m called %d times, want 3", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails (compile error)**

Run: `cd engine && go test ./internal/backfill/...`
Expected: FAIL to compile — `Source`, `TailFetcher`, new `New` signature not defined.

- [ ] **Step 3: Rewrite the changed parts of `backfill.go`**

Replace the `HistFetcher`-through-`fill1m`/`MoomooFetcher` region as follows. Keep the package doc comment, imports (add `context` is already there), `Seeder`, `Archive`, `seedUnlessCanceled`, `Config`, `Run`, `warmStart`, `archive1m`, `archiveDailyBars`, `MoomooFetcher`/`moomooFetcher` **unchanged**. Delete `gapThresholdMs` and the old `fillDaily`/`fill1m`.

Add after the `HistFetcher` interface:

```go
// Source pairs a HistFetcher with a short label naming which provider served,
// for logging. The orchestrator walks a chain of Sources in order.
type Source struct {
	Name string
	HistFetcher
}

// TailFetcher pulls the quota-free recent 1m window (moomoo Qot_GetKL, ≤1,000
// bars) for a symbol with an active K_1M subscription. Implemented by
// *opend.OpenDFeed; nil in replay/demo (no OpenD), where the tail step is
// skipped.
type TailFetcher interface {
	Tail1m(ctx context.Context, symbol string) ([]feed.Bar, error)
}
```

Replace the `Orchestrator` struct + `New`:

```go
// Orchestrator runs the per-symbol backfill sequence over ordered provider
// chains: daily = [alpaca?, yahoo?, moomoo-last-resort], intraday (1m deep) =
// [alpaca?, moomoo-last-resort], plus the moomoo quota-free 1m tail. In normal
// operation the moomoo entries never fire, so historical quota spend is ~0.
type Orchestrator struct {
	daily    []Source
	intraday []Source
	tail     TailFetcher
	seeder   Seeder
	archive  Archive
	clk      clock.Clock
	cfg      Config
}

func New(daily, intraday []Source, tail TailFetcher, seeder Seeder, archive Archive, clk clock.Clock, cfg Config) *Orchestrator {
	if cfg.IntradayDays <= 0 {
		cfg.IntradayDays = 20
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 3
	}
	return &Orchestrator{daily: daily, intraday: intraday, tail: tail, seeder: seeder, archive: archive, clk: clk, cfg: cfg}
}
```

Replace `Backfill` + `dailyFrom`:

```go
// Backfill runs warm-start → quota-free tail seed → deep 1m (trimmed so the
// tail wins overlaps) → daily, for one symbol. Every step is best-effort: a
// failure is logged and later steps still run. The tail seeds first so a cold
// symbol's chart is interactive in <1 s; daily runs last so its (up to ~3 s)
// latency never delays the intraday chart. Returns the daily-fetch outcome
// (nil once any daily provider served) so a caller can re-arm on failure (the
// uihub retries a failed daily backfill once OpenD reconnects).
func (o *Orchestrator) Backfill(ctx context.Context, symbol string) error {
	now := o.clk.Now()
	from1m := intradayFrom(now, o.cfg.IntradayDays)
	o.warmStart(ctx, symbol, from1m, now)
	tailOldestMs, tailOK := o.tail1m(ctx, symbol)
	o.fill1m(ctx, symbol, from1m, now, tailOldestMs, tailOK)
	return o.fillDaily(ctx, symbol, o.dailyFrom(now), now)
}

// dailyFloor is the earliest daily-history start requested. Alpaca's free tier
// hard-floors at 2016-01-04; Yahoo goes deeper, but the extra depth is below
// the indicator-relevance threshold (spec's indicator-depth rationale: only a
// monthly 200-period indicator wants more, an accepted casualty). Clamping
// here keeps depth consistent regardless of which provider served.
var dailyFloor = time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)

// dailyFrom is DailyYears ago clamped to dailyFloor, or dailyFloor when
// DailyYears<=0.
func (o *Orchestrator) dailyFrom(now time.Time) time.Time {
	if o.cfg.DailyYears <= 0 {
		return dailyFloor
	}
	from := now.AddDate(-o.cfg.DailyYears, 0, 0)
	if from.Before(dailyFloor) {
		return dailyFloor
	}
	return from
}
```

Add the new step helpers (replacing the deleted `fillDaily`/`fill1m`):

```go
// tail1m fetches the quota-free ≤1,000-bar recent 1m window, archives + seeds
// it, and returns the oldest bar's BucketMs so fill1m can trim the deep set to
// strictly-older bars (moomoo wins overlaps). ok is false when the tail is
// unavailable (no OpenD, not subscribed, empty, or error) — fill1m then uses
// the deep set untrimmed.
func (o *Orchestrator) tail1m(ctx context.Context, symbol string) (oldestMs int64, ok bool) {
	if o.tail == nil {
		return 0, false
	}
	bars, err := o.tail.Tail1m(ctx, symbol)
	if err != nil {
		slog.Warn("backfill: tail 1m failed", "symbol", symbol, "err", err)
		return 0, false
	}
	if len(bars) == 0 {
		return 0, false
	}
	o.archive1m(bars)
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	return bars[0].BucketMs, true // ascending → [0] is oldest
}

// fill1m walks the 1m chain for the deep window, trims to bars strictly older
// than the tail's oldest bar (when a tail seeded), then archives + seeds.
func (o *Orchestrator) fill1m(ctx context.Context, symbol string, from, to time.Time, tailOldestMs int64, tailOK bool) {
	bars, served, err := walkChain(ctx, symbol, from, to, o.intraday, intraday1m)
	if len(bars) == 0 {
		if err != nil {
			slog.Warn("backfill: deep 1m unavailable", "symbol", symbol, "err", err)
		}
		return
	}
	if tailOK {
		bars = trimOlderThan(bars, tailOldestMs)
	}
	if len(bars) == 0 {
		return
	}
	o.archive1m(bars)
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	slog.Info("backfill: deep 1m served", "symbol", symbol, "provider", served, "bars", len(bars))
}

// fillDaily walks the daily chain and seeds the first non-empty result. It
// returns nil once any provider served (even with zero bars — no data is not a
// failure), otherwise the last error, so the uihub knows whether to re-arm.
func (o *Orchestrator) fillDaily(ctx context.Context, symbol string, from, to time.Time) error {
	bars, served, err := walkChain(ctx, symbol, from, to, o.daily, dailyBars)
	if len(bars) == 0 {
		return err
	}
	o.archiveDailyBars(bars)
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
	slog.Info("backfill: daily served", "symbol", symbol, "provider", served, "bars", len(bars))
	return nil
}

// fetchFunc selects DailyBars or Intraday1m off a Source for walkChain.
type fetchFunc func(Source) func(context.Context, string, time.Time, time.Time) ([]feed.Bar, error)

func dailyBars(s Source) func(context.Context, string, time.Time, time.Time) ([]feed.Bar, error) {
	return s.DailyBars
}
func intraday1m(s Source) func(context.Context, string, time.Time, time.Time) ([]feed.Bar, error) {
	return s.Intraday1m
}

// walkChain tries each source in order, returning the first non-empty result
// and the serving source's name. A source error is logged and the walk
// advances; an empty (nil, nil) result also advances. If every source errored,
// the last error is returned (bars nil); if every source returned empty with
// no error, (nil, "", nil).
func walkChain(ctx context.Context, symbol string, from, to time.Time, chain []Source, pick fetchFunc) ([]feed.Bar, string, error) {
	var lastErr error
	for _, s := range chain {
		bars, err := pick(s)(ctx, symbol, from, to)
		if err != nil {
			slog.Warn("backfill: provider failed", "symbol", symbol, "provider", s.Name, "err", err)
			lastErr = err
			continue
		}
		if len(bars) > 0 {
			return bars, s.Name, nil
		}
	}
	return nil, "", lastErr
}

// trimOlderThan returns the ascending prefix of bars with BucketMs strictly
// less than tsMs (the tail's oldest bar), so the deep 1m set never overwrites a
// moomoo tail bar within a run.
func trimOlderThan(bars []feed.Bar, tsMs int64) []feed.Bar {
	for i, b := range bars {
		if b.BucketMs >= tsMs {
			return bars[:i]
		}
	}
	return bars
}
```

Update the package doc comment's first paragraph to reflect chains (replace the "gap-fills from moomoo … Alpaca 1m-depth fallback" sentence with the chain description). Keep the BarSnapshot paragraph.

- [ ] **Step 4: Run the backfill package tests**

Run: `cd engine && go test ./internal/backfill/...`
Expected: PASS. (`go build ./cmd/...` still broken until Step 5 — expected.)

- [ ] **Step 5: Wire the chains in `main.go`**

Add the import (with the other `hist` import, alphabetized):

```go
histyahoo "github.com/earlisreal/eTape/engine/internal/hist/yahoo"
```

Replace the `if cfg.Backfill.Enabled { … }` block (main.go ~376–401, through the `orch := backfill.New(...)`), keeping the `hubBackfill`/`backfillOne` closures below it unchanged:

```go
var hubBackfill func(sym string, done func(ok bool))
if cfg.Backfill.Enabled {
	var alpacaSrc *histalpaca.Client
	if cfg.Backfill.Alpaca.Enabled {
		if p, label, err := resolveBackfillAlpacaCreds(cfg, credsFile); err == nil {
			alpacaSrc = histalpaca.New("", p.KeyID, p.SecretKey, cfg.Backfill.Alpaca.Feed, clock.System{})
			log.Info("backfill: alpaca provider resolved", "from", label, "feed", cfg.Backfill.Alpaca.Feed)
		} else if errors.Is(err, errAlpacaLiveCreds) {
			log.Warn("backfill: refusing alpaca-live creds for read-only historical provider", "key", cfg.Backfill.Alpaca.CredsKey)
		} else {
			log.Warn("backfill: alpaca provider disabled (no creds)", "key", cfg.Backfill.Alpaca.CredsKey, "err", err)
		}
	}
	moomoo := backfill.MoomooFetcher(fd)
	var dailyChain, intradayChain []backfill.Source
	if alpacaSrc != nil {
		dailyChain = append(dailyChain, backfill.Source{Name: "alpaca", HistFetcher: alpacaSrc})
		intradayChain = append(intradayChain, backfill.Source{Name: "alpaca", HistFetcher: alpacaSrc})
	}
	if cfg.Backfill.Yahoo.Enabled {
		dailyChain = append(dailyChain, backfill.Source{Name: "yahoo", HistFetcher: histyahoo.New("", clock.System{})})
	}
	// moomoo request_history_kline is the quota-guarded last resort in both chains.
	dailyChain = append(dailyChain, backfill.Source{Name: "moomoo", HistFetcher: moomoo})
	intradayChain = append(intradayChain, backfill.Source{Name: "moomoo", HistFetcher: moomoo})

	orch := backfill.New(
		dailyChain,
		intradayChain,
		fd, // TailFetcher: OpenDFeed.Tail1m (quota-free Qot_GetKL)
		core,
		st,
		clock.System{},
		backfill.Config{
			IntradayDays: cfg.Backfill.IntradayDays,
			DailyYears:   cfg.Backfill.DailyYears,
			Concurrency:  cfg.Backfill.Concurrency,
			SeedChunk:    cfg.Backfill.SeedChunk,
		},
	)
	hubBackfill = func(sym string, done func(ok bool)) {
		backfillWG.Add(1)
		go func() {
			defer backfillWG.Done()
			err := orch.Backfill(ctx, sym)
			if done != nil {
				done(err == nil)
			}
		}()
	}
}
```

- [ ] **Step 6: Verify the whole engine builds, vets, and tests green**

Run: `cd engine && go build ./... && go vet ./... && go test ./...`
Expected: PASS across all packages. Also confirm no other caller broke:
Run: `cd engine && rg -n 'backfill\.New\(|\.primary|\.fallback' cmd/ internal/`
Expected: only `cmd/etape/main.go`'s new call; no stray `primary`/`fallback` references.

- [ ] **Step 7: Commit**

```bash
cd engine && git add internal/backfill/ cmd/etape/main.go && git commit -m "feat(backfill): route history via provider chains + quota-free moomoo tail"
```

---

## Verification (end-to-end)

**Static / unit (no OpenD needed):**
```bash
cd engine
go build ./... && go vet ./...
go test ./...
make gen-ts-check   # MUST be a no-op (engine-only change; expect no wsmsg.ts diff)
```

**Live smoke (OpenD + browser)** — from the spec's live-verify checklist. Requires OpenD logged in (US LV3) and a built UI. Watch the engine log for the new `backfill: … served … provider=…` lines:

1. **Alpaca configured, cold symbol:** open a fresh symbol's chart. Chart interactive in <1 s (tail seed), then deepens ~4 s later (Alpaca deep 1m as a larger `BarSnapshot`). Logs name the serving provider per fill (`provider=alpaca` for daily + deep 1m, tail from moomoo). `request_history_kline` quota unchanged before/after (compare `moomooapi` `get_history_kl_quota`).
2. **Alpaca disabled** (`[backfill.alpaca] enabled=false`): daily served by `provider=yahoo`; deep 1m falls to `provider=moomoo` last resort (one quota slot, loud `ErrHistoryQuotaExhausted`-guarded fetch logged); tail still seeds.
3. **Qot_GetKL warm-up (flagged risk):** on a freshly subscribed cold symbol, confirm `Tail1m` returns pre-subscribe bars promptly. If the chart's first paint is empty for >1–2 s, the moomoo cache needs warm-up — add one `~2 s` retry inside `tail1m` (note it here; not implemented preemptively).
4. **Symbol mapping:** load `BRK.B`, one preferred, one OTC name — confirm Alpaca + Yahoo both return bars (path `BRK-B`, returned symbol `US.BRK.B`).
5. **Steady state:** after a session of scanner churn + chart opens, `get_history_kl_quota` used ≈ 0.

Live findings that need a fix (e.g. #3 warm-up) get folded back as a follow-up task, not silently skipped.

---

## Execution notes (auto mode)

- **Isolation:** execute in a **git worktree** off **local `main`** (this repo runs many parallel sessions; `origin/main` lags local `main` by 15–20+ commits — set the worktree base to local `main`, not origin). Do not `git pull`.
- **Copy this plan** to `docs/superpowers/plans/2026-07-10-history-bars-providers-implementation.md` at execution start (first commit) so it's tracked with the work.
- **Driver:** subagent-driven-development — fresh subagent per task, two-stage review between tasks; the loop runs autonomously without pausing for per-task human approval. If a task reviewer flags a plan-mandated coverage gap, add the missing test immediately (standing policy).
- **On completion:** run the full static verification above, then merge the worktree branch to local `main` (verify safe via `git merge-tree` first, per repo convention). **Do not push** — Earl pushes himself.
- No UI/tygo changes expected; if `make gen-ts-check` shows a diff, stop — something is wrong.
