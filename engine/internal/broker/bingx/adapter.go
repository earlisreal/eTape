// Package bingx connects to the exchange's USDT perpetual trading APIs.
package bingx

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/broker/perps"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type environment struct {
	base, stream, broker, label, currency string
}

var (
	virtual = environment{"https://open-api-vst.bingx.com", "wss://vst-open-api-ws.bingx.com/swap-market", "bingx-vst", "BingX Virtual", "VST"}
	live    = environment{"https://open-api.bingx.com", "wss://open-api-swap.bingx.com/swap-market", "bingx-live", "BingX Live", "USDT"}
)

type Adapter struct {
	*perps.Adapter
	api *apiClient
}

type apiClient struct {
	env          environment
	http         *http.Client
	clock        clock.Clock
	creds        creds.Pair
	quotes       sync.Map
	catalogMu    sync.RWMutex
	catalog      map[string]contract
	catalogUntil time.Time
	orderReads   *netx.TokenBucket
	fillReads    *netx.TokenBucket
}

type contract struct {
	Symbol       string      `json:"symbol"`
	Asset        string      `json:"asset"`
	Currency     string      `json:"currency"`
	PriceDigits  *int        `json:"pricePrecision"`
	QtyDigits    *int        `json:"quantityPrecision"`
	Minimum      json.Number `json:"tradeMinQuantity"`
	MinimumValue json.Number `json:"tradeMinUSDT"`
	Fee          json.Number `json:"takerFeeRate"`
	Status       int         `json:"status"`
	Open         string      `json:"apiStateOpen"`
	Close        string      `json:"apiStateClose"`
}

func NewVirtual(venue exec.VenueID, cr creds.Pair, clk clock.Clock, path string) (*Adapter, error) {
	return newAdapter(virtual, venue, cr, clk, path)
}

func NewLive(venue exec.VenueID, cr creds.Pair, clk clock.Clock, path string) (*Adapter, error) {
	return newAdapter(live, venue, cr, clk, path)
}

func newAdapter(env environment, venue exec.VenueID, cr creds.Pair, clk clock.Clock, path string) (*Adapter, error) {
	if cr.KeyID == "" || cr.SecretKey == "" {
		return nil, fmt.Errorf("%s requires API credentials", env.label)
	}
	api := newAPI(env, cr, clk)
	a, err := perps.New(venue, clk, path, env.broker+":"+cr.KeyID, api)
	if err != nil {
		return nil, err
	}
	return &Adapter{Adapter: a, api: api}, nil
}

func newAPI(env environment, cr creds.Pair, clk clock.Clock) *apiClient {
	return &apiClient{env: env, clock: clk, creds: cr,
		orderReads: netx.NewTokenBucket(clock.System{}, 30, 1),
		fillReads:  netx.NewTokenBucket(clock.System{}, 5, 1),
		http: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}}
}

// VerifyVirtual checks account access and one-way mode with read-only VST requests.
func VerifyVirtual(ctx context.Context, cr creds.Pair, clk clock.Clock) error {
	_, err := newAPI(virtual, cr, clk).account(ctx)
	return err
}

// VerifyLive checks production account access and one-way mode using reads.
func VerifyLive(ctx context.Context, cr creds.Pair, clk clock.Clock) error {
	_, err := newAPI(live, cr, clk).account(ctx)
	return err
}

func (a *Adapter) SetFairQuote(q feed.Quote) {
	if id, ok := instrument.ParsePerpetual(q.Symbol); ok && id.Exchange == "BINGX" {
		a.api.quotes.Store(q.Symbol, q)
	}
}

type apiError struct {
	label string
	code  int64
}

func (e apiError) Error() string { return fmt.Sprintf("%s API code %d", e.label, e.code) }

func (a *apiClient) newRequest(ctx context.Context, method, path string, params url.Values, signed bool) (*http.Request, error) {
	if params == nil {
		params = make(url.Values)
	}
	if signed {
		params.Set("timestamp", strconv.FormatInt(a.clock.Now().UnixMilli(), 10))
		params.Set("recvWindow", "5000")
	}
	query := params.Encode()
	if signed {
		mac := hmac.New(sha256.New, []byte(a.creds.SecretKey))
		mac.Write([]byte(query))
		query += "&signature=" + hex.EncodeToString(mac.Sum(nil))
	}
	target := a.env.base + path
	if query != "" {
		target += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, fmt.Errorf("could not build %s request", a.env.label)
	}
	if signed {
		req.Header.Set("X-BX-APIKEY", a.creds.KeyID)
	}
	return req, nil
}

func (a *apiClient) request(ctx context.Context, method, path string, params url.Values, signed bool, into any) error {
	var budget *netx.TokenBucket
	if method == http.MethodGet {
		switch path {
		case "/openApi/swap/v2/trade/order":
			budget = a.orderReads
		case "/openApi/swap/v2/trade/allFillOrders":
			budget = a.fillReads
		}
	}
	if budget != nil {
		if err := budget.Take(ctx); err != nil {
			return err
		}
	}
	req, err := a.newRequest(ctx, method, path, params, signed)
	if err != nil {
		return err
	}
	res, err := a.http.Do(req)
	if err != nil {
		// net/http errors can include the signed URL.
		return fmt.Errorf("%s connection failed", a.env.label)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		if budget != nil && res.StatusCode == http.StatusTooManyRequests {
			a.deferRateLimited(budget, res.Header.Get("Retry-After"))
		}
		return fmt.Errorf("%s returned HTTP %d", a.env.label, res.StatusCode)
	}
	var response struct {
		Code json.Number     `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&response); err != nil {
		return fmt.Errorf("invalid %s response", a.env.label)
	}
	code, err := response.Code.Int64()
	if err != nil {
		return fmt.Errorf("invalid %s response status", a.env.label)
	}
	if code != 0 {
		if budget != nil && code == 100410 {
			a.deferRateLimited(budget, res.Header.Get("Retry-After"))
		}
		return apiError{a.env.label, code}
	}
	if len(response.Data) == 0 || bytes.Equal(response.Data, []byte("null")) || json.Unmarshal(response.Data, into) != nil {
		return fmt.Errorf("invalid %s data", a.env.label)
	}
	return nil
}

func (a *apiClient) deferRateLimited(budget *netx.TokenBucket, retryAfter string) {
	now := time.Now()
	until := now.Add(time.Second)
	if seconds, err := strconv.ParseInt(retryAfter, 10, 32); err == nil && seconds > 1 {
		until = now.Add(time.Duration(seconds) * time.Second)
	} else if reset, err := http.ParseTime(retryAfter); err == nil && reset.After(until) {
		until = reset
	}
	budget.DeferUntil(until)
}

func (a *apiClient) refreshCatalog(ctx context.Context) error {
	a.catalogMu.RLock()
	fresh := a.clock.Now().Before(a.catalogUntil)
	a.catalogMu.RUnlock()
	if fresh {
		return nil
	}
	var items []contract
	if err := a.request(ctx, http.MethodGet, "/openApi/swap/v2/quote/contracts", nil, false, &items); err != nil {
		return err
	}
	catalog := make(map[string]contract)
	for _, item := range items {
		id, ok := instrument.ParsePerpetual("PERP.BINGX." + item.Symbol)
		if ok && strings.HasSuffix(id.Native, "-USDT") && item.Currency == "USDT" && item.Asset != "" &&
			item.QtyDigits != nil && *item.QtyDigits >= 0 && *item.QtyDigits <= 12 &&
			item.PriceDigits != nil && *item.PriceDigits >= 0 && *item.PriceDigits <= 12 {
			catalog[id.Native] = item
		}
	}
	if len(catalog) == 0 {
		return fmt.Errorf("no %s USDT contracts", a.env.label)
	}
	a.catalogMu.Lock()
	a.catalog, a.catalogUntil = catalog, a.clock.Now().Add(2*time.Minute)
	a.catalogMu.Unlock()
	return nil
}

func tick(precision int) string {
	if precision == 0 {
		return "1"
	}
	return "0." + strings.Repeat("0", precision-1) + "1"
}

func lots(raw json.Number, digits int) (float64, bool) {
	value, ok := new(big.Rat).SetString(raw.String())
	if !ok {
		return 0, false
	}
	unit, _ := new(big.Rat).SetString(tick(digits))
	value.Quo(value, unit)
	if !value.IsInt() || value.Num().BitLen() > 53 {
		return 0, false
	}
	return float64(value.Num().Int64()), true
}

func (a *apiClient) contract(native string) (contract, bool) {
	a.catalogMu.RLock()
	defer a.catalogMu.RUnlock()
	item, ok := a.catalog[native]
	return item, ok && a.clock.Now().Before(a.catalogUntil)
}

func (a *apiClient) Rules(symbol string) (instrument.ContractRules, bool) {
	id, ok := instrument.ParsePerpetual(symbol)
	if !ok || id.Exchange != "BINGX" {
		return instrument.ContractRules{}, false
	}
	item, ok := a.contract(id.Native)
	if !ok {
		return instrument.ContractRules{}, false
	}
	minimum, valid := lots(item.Minimum, *item.QtyDigits)
	if !valid || minimum < 0 {
		return instrument.ContractRules{}, false
	}
	// Actual opening size and leverage limits are account- and side-specific.
	rules := instrument.ContractRules{Base: item.Asset, Settlement: "USDT", Multiplier: tick(*item.QtyDigits),
		PriceTick: tick(*item.PriceDigits), QuantityStep: 1, Minimum: int64(max(1, minimum)), Maximum: 1<<53 - 1,
		MinLeverage: 1, MaxLeverage: 500, TakerFee: item.Fee.String(), TradingEnabled: item.Status == 1 && item.Open == "true"}
	return rules, rules.Valid()
}

func (a *apiClient) FairPrice(symbol string) (float64, bool) {
	value, ok := a.quotes.Load(symbol)
	if !ok {
		return 0, false
	}
	q := value.(feed.Quote)
	age := a.clock.Now().Sub(time.UnixMilli(q.TsMs))
	return q.Mark, q.Mark > 0 && age >= -2*time.Second && age < 5*time.Second
}
