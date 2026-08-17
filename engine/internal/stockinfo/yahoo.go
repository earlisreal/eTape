package stockinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

const (
	yahooMetadataBase      = "https://query1.finance.yahoo.com"
	yahooMetadataCookieURL = "https://fc.yahoo.com"
	yahooMetadataTTL       = 24 * time.Hour
	yahooMetadataRetry     = time.Hour
	yahooMetadataUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120 Safari/537.36"
)

type yahooMetadata struct {
	Country  string
	Sector   string
	Industry string
}

type yahooMetadataEntry struct {
	value      yahooMetadata
	fetchedAt  time.Time
	retryAfter time.Time
	pending    bool
}

type yahooMetadataCache struct {
	base      string
	cookieURL string
	hc        *http.Client
	clk       clock.Clock
	bucket    *netx.TokenBucket
	mu        sync.Mutex
	sessionMu sync.Mutex
	cookie    string
	crumb     string
	entries   map[string]yahooMetadataEntry
}

func newYahooMetadataCache(base string, clk clock.Clock, hc *http.Client) *yahooMetadataCache {
	cookieURL := yahooMetadataCookieURL
	if base == "" {
		base = yahooMetadataBase
	} else {
		base = strings.TrimRight(base, "/")
		cookieURL = base + "/fc.yahoo.com"
	}
	if hc == nil {
		hc = netx.NewHTTPClient(8 * time.Second)
	}
	return &yahooMetadataCache{
		base: base, cookieURL: cookieURL, hc: hc, clk: clk,
		bucket:  netx.NewTokenBucket(clk, 30.0/60.0, 5),
		entries: map[string]yahooMetadataEntry{},
	}
}

// get returns the last cached value immediately and starts one background
// refresh for a missing or expired symbol. Metadata is deliberately eventual:
// a slow or unavailable Yahoo endpoint must not delay Moomoo fundamentals.
func (c *yahooMetadataCache) get(ctx context.Context, symbol string) yahooMetadata {
	if !strings.HasPrefix(symbol, "US.") {
		return yahooMetadata{}
	}
	now := c.clk.Now()
	c.mu.Lock()
	entry := c.entries[symbol]
	value := entry.value
	ready := entry.pending ||
		(!entry.fetchedAt.IsZero() && now.Before(entry.fetchedAt.Add(yahooMetadataTTL))) ||
		(entry.fetchedAt.IsZero() && now.Before(entry.retryAfter))
	if !ready {
		entry.pending = true
		c.entries[symbol] = entry
	}
	c.mu.Unlock()
	if !ready {
		go c.refresh(ctx, symbol)
	}
	return value
}

func (c *yahooMetadataCache) refresh(ctx context.Context, symbol string) {
	requestCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	value, err := c.fetch(requestCtx, symbol)
	now := c.clk.Now()
	c.mu.Lock()
	entry := c.entries[symbol]
	entry.pending = false
	if err == nil {
		entry.value = value
		entry.fetchedAt = now
		entry.retryAfter = time.Time{}
	} else {
		entry.retryAfter = now.Add(yahooMetadataRetry)
		slog.Warn("stockinfo: Yahoo profile metadata unavailable", "symbol", symbol, "err", err)
	}
	c.entries[symbol] = entry
	c.mu.Unlock()
}

type yahooProfileResponse struct {
	QuoteSummary struct {
		Result []struct {
			AssetProfile yahooProfile `json:"assetProfile"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"quoteSummary"`
	Finance struct {
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"finance"`
}

type yahooProfile struct {
	Country  string `json:"country"`
	Sector   string `json:"sector"`
	Industry string `json:"industry"`
}

func (c *yahooMetadataCache) fetch(ctx context.Context, symbol string) (yahooMetadata, error) {
	if err := c.bucket.Take(ctx); err != nil {
		return yahooMetadata{}, err
	}
	if err := c.ensureSession(ctx); err != nil {
		return yahooMetadata{}, err
	}
	ticker := strings.ReplaceAll(strings.TrimPrefix(symbol, "US."), ".", "-")
	if ticker == "" {
		return yahooMetadata{}, fmt.Errorf("empty Yahoo ticker for %q", symbol)
	}
	cookie, crumb := c.session()
	value, err := c.fetchProfile(ctx, ticker, cookie, crumb)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "invalid crumb") {
		return value, err
	}
	c.clearSession()
	if err := c.ensureSession(ctx); err != nil {
		return yahooMetadata{}, err
	}
	cookie, crumb = c.session()
	return c.fetchProfile(ctx, ticker, cookie, crumb)
}

func (c *yahooMetadataCache) fetchProfile(ctx context.Context, ticker, cookie, crumb string) (yahooMetadata, error) {
	requestURL := c.base + "/v10/finance/quoteSummary/" + url.PathEscape(ticker)
	u, err := url.Parse(requestURL)
	if err != nil {
		return yahooMetadata{}, err
	}
	query := u.Query()
	query.Set("modules", "assetProfile")
	query.Set("crumb", crumb)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return yahooMetadata{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", yahooMetadataUA)
	req.Header.Set("Cookie", cookie)
	resp, err := c.hc.Do(req)
	if err != nil {
		return yahooMetadata{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return yahooMetadata{}, yahooHTTPError{status: resp.StatusCode, body: string(body)}
	}
	var body yahooProfileResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return yahooMetadata{}, err
	}
	if body.QuoteSummary.Error != nil {
		return yahooMetadata{}, fmt.Errorf("yahoo profile: %s %s", body.QuoteSummary.Error.Code, body.QuoteSummary.Error.Description)
	}
	if body.Finance.Error != nil {
		return yahooMetadata{}, fmt.Errorf("yahoo profile: %s %s", body.Finance.Error.Code, body.Finance.Error.Description)
	}
	if len(body.QuoteSummary.Result) == 0 {
		return yahooMetadata{}, nil
	}
	profile := body.QuoteSummary.Result[0].AssetProfile
	return yahooMetadata{
		Country:  strings.TrimSpace(profile.Country),
		Sector:   strings.TrimSpace(profile.Sector),
		Industry: strings.TrimSpace(profile.Industry),
	}, nil
}

type yahooHTTPError struct {
	status int
	body   string
}

func (e yahooHTTPError) Error() string {
	return fmt.Sprintf("yahoo profile: status=%d body=%s", e.status, e.body)
}

func (c *yahooMetadataCache) ensureSession(ctx context.Context) error {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if cookie, crumb := c.session(); cookie != "" && crumb != "" {
		return nil
	}
	cookie, err := c.fetchCookie(ctx)
	if err != nil {
		return err
	}
	crumb, err := c.fetchCrumb(ctx, cookie)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.cookie, c.crumb = cookie, crumb
	c.mu.Unlock()
	return nil
}

func (c *yahooMetadataCache) session() (cookie, crumb string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cookie, c.crumb
}

func (c *yahooMetadataCache) clearSession() {
	c.mu.Lock()
	c.cookie, c.crumb = "", ""
	c.mu.Unlock()
}

func (c *yahooMetadataCache) fetchCookie(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cookieURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", yahooMetadataUA)
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	cookies := resp.Cookies()
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		values = append(values, cookie.Name+"="+cookie.Value)
	}
	if len(values) == 0 {
		return "", fmt.Errorf("yahoo session: no cookie from %s", c.cookieURL)
	}
	return strings.Join(values, "; "), nil
}

func (c *yahooMetadataCache) fetchCrumb(ctx context.Context, cookie string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/test/getcrumb", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", yahooMetadataUA)
	req.Header.Set("Cookie", cookie)
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	crumb := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK || crumb == "" {
		return "", fmt.Errorf("yahoo session: crumb status=%d body=%s", resp.StatusCode, body)
	}
	return crumb, nil
}

func applyYahooMetadata(payload *wsmsg.StockDetailPayload, value yahooMetadata) {
	payload.Country = value.Country
	payload.Sector = value.Sector
	if strings.TrimSpace(payload.Industry) == "" {
		payload.Industry = value.Industry
	}
}
