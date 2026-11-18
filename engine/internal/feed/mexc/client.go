// Package mexc connects eTape to the public MEXC Futures API.
package mexc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

const Exchange = "MEXC"

const (
	httpEndpoint = "https://api.mexc.com"
	wsEndpoint   = "wss://contract.mexc.com/edge"
)

type Contract struct {
	Symbol       string      `json:"symbol"`
	Base         string      `json:"baseCoin"`
	Quote        string      `json:"quoteCoin"`
	Settlement   string      `json:"settleCoin"`
	Product      int         `json:"futureType"`
	Multiplier   json.Number `json:"contractSize"`
	PriceTick    json.Number `json:"priceUnit"`
	QuantityStep json.Number `json:"volUnit"`
	Minimum      json.Number `json:"minVol"`
	Maximum      json.Number `json:"maxVol"`
	MinLeverage  int         `json:"minLeverage"`
	MaxLeverage  int         `json:"maxLeverage"`
	TakerFee     json.Number `json:"takerFeeRate"`
	State        int         `json:"state"`
	APIAllowed   bool        `json:"apiAllowed"`
}

// Rules returns fresh terms from the public catalog. Callers load the catalog
// on market demand, outside the execution writer.
func (c *Client) Rules(symbol string) (instrument.ContractRules, bool) {
	c.mu.Lock()
	entry := c.cache[symbol]
	c.mu.Unlock()
	item := entry.value
	step, _ := contracts(item.QuantityStep)
	minimum, _ := contracts(item.Minimum)
	maximum, _ := contracts(item.Maximum)
	rules := instrument.ContractRules{Base: item.Base, Settlement: item.Settlement, Multiplier: item.Multiplier.String(),
		PriceTick: item.PriceTick.String(), QuantityStep: step, Minimum: minimum, Maximum: maximum,
		MinLeverage: item.MinLeverage, MaxLeverage: item.MaxLeverage, TakerFee: item.TakerFee.String(),
		TradingEnabled: item.State == 0 && item.APIAllowed}
	return rules, time.Now().Before(entry.until) && rules.Valid()
}

type Client struct {
	http        *http.Client
	base        string
	stream      string
	mu          sync.Mutex
	cache       map[string]cachedContract
	funding     map[string]fundingTime
	nextRequest time.Time
}

type fundingTime struct {
	At      int64 `json:"nextSettleTime"`
	checked time.Time
}

type cachedContract struct {
	value Contract
	until time.Time
}

func NewClient() *Client {
	return &Client{
		http: &http.Client{Timeout: 8 * time.Second},
		base: httpEndpoint, stream: wsEndpoint, cache: make(map[string]cachedContract), funding: make(map[string]fundingTime),
	}
}

func (c *Client) nextFunding(ctx context.Context, native string) int64 {
	now := time.Now()
	c.mu.Lock()
	cached := c.funding[native]
	if now.Before(cached.checked) {
		c.mu.Unlock()
		return cached.At
	}
	cached.checked = now.Add(10 * time.Second)
	c.funding[native] = cached
	c.mu.Unlock()
	request, done := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer done()
	var next fundingTime
	if err := c.get(request, "/api/v1/contract/funding_rate/"+native, &next); err != nil || next.At <= now.UnixMilli() {
		return cached.At
	}
	next.checked = time.UnixMilli(next.At)
	c.mu.Lock()
	c.funding[native] = next
	c.mu.Unlock()
	return next.At
}

func (c *Client) get(ctx context.Context, path string, value any) error {
	c.mu.Lock()
	now := time.Now()
	start := now
	if c.nextRequest.After(start) {
		start = c.nextRequest
	}
	if deadline, ok := ctx.Deadline(); ok && !start.Before(deadline) {
		c.mu.Unlock()
		return context.DeadlineExceeded
	}
	c.nextRequest = start.Add(220 * time.Millisecond)
	c.mu.Unlock()
	if start.After(now) {
		timer := time.NewTimer(start.Sub(now))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-cache")
	response, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("MEXC Futures HTTP status %d", response.StatusCode)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Code    int             `json:"code"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.Success || envelope.Code != 0 {
		return fmt.Errorf("MEXC Futures code %d", envelope.Code)
	}
	return json.Unmarshal(envelope.Data, value)
}

func parseSymbol(symbol string) (instrument.Perpetual, bool) {
	id, ok := instrument.ParsePerpetual(symbol)
	return id, ok && id.Exchange == Exchange
}

func (c *Client) Contract(ctx context.Context, symbol string) (Contract, error) {
	id, ok := parseSymbol(symbol)
	if !ok {
		return Contract{}, feed.ErrUnknownSymbol
	}
	c.mu.Lock()
	cached := c.cache[symbol]
	c.mu.Unlock()
	if time.Now().Before(cached.until) {
		return cached.value, nil
	}
	// The endpoint can return a single object or a list.
	var payload json.RawMessage
	err := c.get(ctx, "/api/v1/contract/detail/country?symbol="+url.QueryEscape(id.Native), &payload)
	if err != nil {
		return Contract{}, errors.Join(feed.ErrFeedUnavailable, err)
	}
	var contracts []Contract
	if strings.HasPrefix(string(payload), "{") {
		contracts = make([]Contract, 1)
		err = json.Unmarshal(payload, &contracts[0])
	} else {
		err = json.Unmarshal(payload, &contracts)
	}
	if err != nil {
		return Contract{}, errors.Join(feed.ErrFeedUnavailable, err)
	}
	for _, item := range contracts {
		if item.Symbol == id.Native && item.Product == 1 && item.Quote == "USDT" && item.Settlement == "USDT" {
			c.mu.Lock()
			c.cache[symbol] = cachedContract{value: item, until: time.Now().Add(time.Minute)}
			c.mu.Unlock()
			return item, nil
		}
	}
	return Contract{}, feed.ErrUnknownSymbol
}

func (c *Client) Validate(ctx context.Context, symbol string) error {
	_, err := c.Contract(ctx, symbol)
	return err
}

func (c *Client) History(ctx context.Context, symbol string, resolution feed.Resolution) ([]feed.Bar, error) {
	id, ok := parseSymbol(symbol)
	if !ok {
		return nil, feed.ErrUnknownSymbol
	}
	interval := "Min1"
	if resolution == feed.ResDay {
		interval = "Day1"
	}
	var data struct {
		Time     []int64       `json:"time"`
		Open     []float64     `json:"open"`
		High     []float64     `json:"high"`
		Low      []float64     `json:"low"`
		Close    []float64     `json:"close"`
		Volume   []json.Number `json:"vol"`
		Turnover []float64     `json:"amount"`
	}
	if err := c.get(ctx, "/api/v1/contract/kline/"+id.Native+"?interval="+interval, &data); err != nil {
		return nil, err
	}
	n := len(data.Time)
	if len(data.Open) != n || len(data.High) != n || len(data.Low) != n || len(data.Close) != n || len(data.Volume) != n {
		return nil, errors.New("inconsistent MEXC Futures candle arrays")
	}
	bars := make([]feed.Bar, 0, n)
	for i, ts := range data.Time {
		volume, ok := contracts(data.Volume[i])
		if !ok || ts <= 0 {
			return nil, errors.New("invalid MEXC Futures candle")
		}
		b := feed.Bar{Symbol: symbol, BucketMs: ts * 1_000, O: data.Open[i], H: data.High[i], L: data.Low[i], C: data.Close[i], Volume: volume}
		if len(data.Turnover) == n {
			b.Turnover = data.Turnover[i]
		}
		bars = append(bars, b)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].BucketMs < bars[j].BucketMs })
	return bars, nil
}

// MEXC reports integer contract counts, sometimes written with a decimal point.
// Keep native counts exact through the integer market-data pipeline.
func contracts(n json.Number) (int64, bool) {
	raw := n.String()
	whole, fraction, decimal := strings.Cut(raw, ".")
	if decimal {
		if strings.Trim(fraction, "0") == "" {
			n = json.Number(whole)
		}
	}
	value, err := n.Int64()
	if err == nil {
		return value, value >= 0 && value < 1<<53
	}
	// Older daily candles use scientific notation for exact integer counts.
	if !strings.ContainsAny(raw, "eE") || len(raw) > 32 {
		return 0, false
	}
	power := strings.FieldsFunc(raw, func(r rune) bool { return r == 'e' || r == 'E' })
	if len(power) != 2 || len(strings.TrimLeft(power[1], "+-")) > 2 {
		return 0, false
	}
	var exact big.Rat
	if _, ok := exact.SetString(raw); !ok || !exact.IsInt() || !exact.Num().IsInt64() {
		return 0, false
	}
	value = exact.Num().Int64()
	return value, value >= 0 && value < 1<<53
}
