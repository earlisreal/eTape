// Package linearperp supplies public Binance USDⓈ-M and BingX swap data.
package linearperp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/cryptoscanner"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

const (
	Binance = "BINANCE"
	BingX   = "BINGX"
)

type Client struct {
	exchange  string
	base      string
	http      *http.Client
	marketWS  string
	bookWS    string
	mu        sync.Mutex
	markets   map[string]instrument.PerpetualMarket
	until     time.Time
	scanner   cryptoscanner.Cache
	demoRules map[string]DemoRules
}

func NewBinance() *Client {
	return &Client{exchange: Binance, base: "https://fapi.binance.com", http: &http.Client{Timeout: 7 * time.Second},
		marketWS: "wss://fstream.binance.com/market/stream", bookWS: "wss://fstream.binance.com/public/stream"}
}

const BinanceDemoREST = "https://demo-fapi.binance.com"

func NewBinanceDemo() *Client {
	return &Client{exchange: instrument.BinanceDemo, base: BinanceDemoREST, http: &http.Client{Timeout: 7 * time.Second},
		marketWS: "wss://demo-fstream.binance.com/market/stream", bookWS: "wss://demo-fstream.binance.com/public/stream"}
}

func NewBingX() *Client {
	return &Client{exchange: BingX, base: "https://open-api.bingx.com", http: &http.Client{Timeout: 7 * time.Second},
		marketWS: "wss://open-api-swap.bingx.com/swap-market"}
}

func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	address := c.base + path
	if query := params.Encode(); query != "" {
		address += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s market data HTTP %d", c.exchange, res.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(res.Body, 8<<20))
	if c.exchange == BingX {
		var env struct {
			Code *int            `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if err := decoder.Decode(&env); err != nil {
			return err
		}
		if env.Code == nil || *env.Code != 0 {
			return errors.New("BingX market data request rejected")
		}
		return json.Unmarshal(env.Data, out)
	}
	return decoder.Decode(out)
}

func (c *Client) PerpetualMarkets(ctx context.Context) ([]instrument.PerpetualMarket, error) {
	if err := c.refreshMarkets(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rows := make([]instrument.PerpetualMarket, 0, len(c.markets))
	for _, row := range c.markets {
		if c.exchange != instrument.BinanceDemo || row.TradingEnabled {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID.Native < rows[j].ID.Native })
	return rows, nil
}

func (c *Client) refreshMarkets(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.until) {
		return nil
	}
	markets, err := c.loadMarkets(ctx)
	if err == nil {
		c.markets, c.until = markets, time.Now().Add(5*time.Minute)
	}
	return err
}

func (c *Client) loadMarkets(ctx context.Context) (map[string]instrument.PerpetualMarket, error) {
	markets := map[string]instrument.PerpetualMarket{}
	add := func(symbol, base string, trading bool) {
		id := instrument.Perpetual{Exchange: c.exchange, Native: symbol}
		if _, ok := instrument.ParsePerpetual(id.String()); !ok || base == "" {
			return
		}
		markets[symbol] = instrument.PerpetualMarket{ID: id, Base: base, Settlement: "USDT", CryptoUnderlying: true, TradingEnabled: trading}
	}
	if c.exchange != BingX {
		var data struct {
			Symbols []struct {
				Symbol     string          `json:"symbol"`
				Type       string          `json:"contractType"`
				Status     string          `json:"status"`
				Base       string          `json:"baseAsset"`
				Quote      string          `json:"quoteAsset"`
				Margin     string          `json:"marginAsset"`
				Underlying string          `json:"underlyingType"`
				Filters    []binanceFilter `json:"filters"`
			} `json:"symbols"`
		}
		if err := c.get(ctx, "/fapi/v1/exchangeInfo", nil, &data); err != nil {
			return nil, err
		}
		rules := make(map[string]DemoRules)
		for _, row := range data.Symbols {
			if row.Type != "PERPETUAL" || row.Quote != "USDT" || row.Margin != "USDT" || row.Underlying != "COIN" {
				continue
			}
			if c.exchange == instrument.BinanceDemo {
				if terms, ok := binanceRules(row.Base, row.Status == "TRADING", row.Filters); ok {
					rules[row.Symbol] = terms
					add(row.Symbol, row.Base, terms.TradingEnabled)
				}
			} else if row.Status == "TRADING" {
				add(row.Symbol, row.Base, false)
			}
		}
		if c.exchange == instrument.BinanceDemo && len(rules) == 0 {
			return nil, errors.New("no Binance Futures Demo contract rules")
		}
		c.demoRules = rules
	} else {
		var rows []struct {
			Symbol   string `json:"symbol"`
			Base     string `json:"asset"`
			Currency string `json:"currency"`
			Status   int    `json:"status"`
		}
		if err := c.get(ctx, "/openApi/swap/v2/quote/contracts", nil, &rows); err != nil {
			return nil, err
		}
		var scheduled []struct {
			Symbol string `json:"symbol"`
		}
		if err := c.get(ctx, "/openApi/swap/v1/quote/tradingSchedule", nil, &scheduled); err != nil {
			return nil, err
		}
		nonCrypto := make(map[string]bool, len(scheduled))
		for _, row := range scheduled {
			nonCrypto[row.Symbol] = true
		}
		for _, row := range rows {
			if row.Status == 1 && row.Currency == "USDT" && row.Symbol == row.Base+"-USDT" &&
				!nonCrypto[row.Symbol] {
				add(row.Symbol, row.Base, false)
			}
		}
	}
	return markets, nil
}

func (c *Client) Validate(ctx context.Context, symbol string) error {
	id, ok := instrument.ParsePerpetual(symbol)
	if !ok || id.Exchange != c.exchange {
		return feed.ErrUnknownSymbol
	}
	if err := c.refreshMarkets(ctx); err != nil {
		return errors.Join(feed.ErrFeedUnavailable, err)
	}
	c.mu.Lock()
	_, active := c.markets[id.Native]
	c.mu.Unlock()
	if !active {
		return feed.ErrUnknownSymbol
	}
	return nil
}

type candle struct {
	Time   int64       `json:"time"`
	Open   json.Number `json:"open"`
	High   json.Number `json:"high"`
	Low    json.Number `json:"low"`
	Close  json.Number `json:"close"`
	Volume json.Number `json:"volume"`
}

func (c *Client) History(ctx context.Context, symbol string, res feed.Resolution) ([]feed.Bar, error) {
	id, ok := instrument.ParsePerpetual(symbol)
	if !ok || id.Exchange != c.exchange {
		return nil, feed.ErrUnknownSymbol
	}
	interval := "1m"
	if res == feed.ResDay {
		interval = "1d"
	}
	params := url.Values{"symbol": {id.Native}, "interval": {interval}, "limit": {"1000"}}
	var result []feed.Bar
	if c.exchange != BingX {
		var rows [][]json.Number
		if err := c.get(ctx, "/fapi/v1/klines", params, &rows); err != nil {
			return nil, err
		}
		for _, p := range rows {
			if len(p) < 8 {
				continue
			}
			ts, _ := p[0].Int64()
			if b, valid := toBar(symbol, ts, p[1], p[2], p[3], p[4], p[5]); valid {
				b.Turnover = number(p[7])
				result = append(result, b)
			}
		}
	} else {
		var rows []candle
		if err := c.get(ctx, "/openApi/swap/v3/quote/klines", params, &rows); err != nil {
			return nil, err
		}
		for _, p := range rows {
			if b, valid := toBar(symbol, p.Time, p.Open, p.High, p.Low, p.Close, p.Volume); valid {
				result = append(result, b)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].BucketMs < result[j].BucketMs })
	return result, nil
}

func toBar(symbol string, ts int64, open, high, low, close, volume json.Number) (feed.Bar, bool) {
	qty, ok := scaled(volume)
	b := feed.Bar{Symbol: symbol, BucketMs: ts, O: number(open), H: number(high), L: number(low), C: number(close), Volume: qty}
	return b, ok && ts > 0 && b.O > 0 && b.H > 0 && b.L > 0 && b.C > 0
}

func number(raw json.Number) float64 {
	n, err := raw.Float64()
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	return n
}

func scaled(raw json.Number) (int64, bool) {
	whole, fraction, _ := strings.Cut(raw.String(), ".")
	fraction = strings.TrimRight(fraction, "0")
	if len(fraction) > 8 || whole == "" || strings.ContainsAny(whole, "-+") {
		return 0, false
	}
	n, err := strconv.ParseInt(whole+fraction+strings.Repeat("0", 8-len(fraction)), 10, 64)
	return n, err == nil
}
