package linearperp

import (
	"context"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/instrument"
)

// DemoRules contains exchange filters in eTape lots. Both order types share
// the same lot quantum so account quantities and fills use one unit.
type DemoRules struct {
	instrument.ContractRules
	Market      instrument.ContractRules
	MinNotional float64
	MinPrice    float64
	MaxPrice    float64
}

func (c *Client) Refresh(ctx context.Context) error { return c.refreshMarkets(ctx) }

func (c *Client) DemoRules(symbol string) (DemoRules, bool) {
	id, ok := instrument.ParsePerpetual(symbol)
	if !ok || id.Exchange != instrument.BinanceDemo || c.exchange != id.Exchange {
		return DemoRules{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rules, known := c.demoRules[id.Native]
	return rules, known && time.Now().Before(c.until)
}

type binanceFilter struct {
	Type     string      `json:"filterType"`
	MinQty   json.Number `json:"minQty"`
	MaxQty   json.Number `json:"maxQty"`
	Step     json.Number `json:"stepSize"`
	Tick     json.Number `json:"tickSize"`
	MinPrice json.Number `json:"minPrice"`
	MaxPrice json.Number `json:"maxPrice"`
	Notional json.Number `json:"notional"`
}

func binanceRules(base string, trading bool, filters []binanceFilter) (DemoRules, bool) {
	var price, lot, market, notional binanceFilter
	for _, f := range filters {
		switch f.Type {
		case "PRICE_FILTER":
			price = f
		case "LOT_SIZE":
			lot = f
		case "MARKET_LOT_SIZE":
			market = f
		case "MIN_NOTIONAL":
			notional = f
		}
	}
	fractionDigits := func(n json.Number) int {
		_, fraction, _ := strings.Cut(n.String(), ".")
		return len(strings.TrimRight(fraction, "0"))
	}
	digits := max(fractionDigits(lot.Step), fractionDigits(market.Step))
	if digits > 8 || lot.Type == "" || market.Type == "" || price.Type == "" || notional.Type == "" {
		return DemoRules{}, false
	}
	quantum := "1"
	if digits > 0 {
		quantum = "0." + strings.Repeat("0", digits-1) + "1"
	}
	build := func(f binanceFilter) instrument.ContractRules {
		minimum, _ := DemoLots(f.MinQty, quantum)
		maximum, _ := DemoLots(f.MaxQty, quantum)
		step, _ := DemoLots(f.Step, quantum)
		return instrument.ContractRules{Base: base, Settlement: "USDT", Multiplier: quantum, PriceTick: price.Tick.String(),
			QuantityStep: step, Minimum: minimum, Maximum: maximum, MinLeverage: 1, MaxLeverage: 125,
			TakerFee: "0", TradingEnabled: trading}
	}
	r := DemoRules{ContractRules: build(lot), Market: build(market), MinNotional: number(notional.Notional),
		MinPrice: number(price.MinPrice), MaxPrice: number(price.MaxPrice)}
	return r, r.Valid() && r.Market.Valid() && r.MinNotional > 0 && r.MaxPrice >= r.MinPrice
}

// DemoLots converts an exchange decimal exactly. Integers stay lossless in
// the Go execution core, JSON contract and TypeScript UI.
func DemoLots(quantity json.Number, quantum string) (int64, bool) {
	value, ok := new(big.Rat).SetString(quantity.String())
	step, stepOK := new(big.Rat).SetString(quantum)
	if !ok || !stepOK || step.Sign() <= 0 {
		return 0, false
	}
	value.Quo(value, step)
	if !value.IsInt() || value.Num().BitLen() > 53 {
		return 0, false
	}
	return value.Num().Int64(), true
}

func DemoQuantity(lots int64, quantum string) string {
	if _, fraction, ok := strings.Cut(quantum, "."); ok {
		amount, _ := new(big.Rat).SetString(quantum)
		return amount.Mul(amount, big.NewRat(lots, 1)).FloatString(len(fraction))
	}
	return strconv.FormatInt(lots, 10)
}
