package instrument

import (
	"math"
	"math/big"
	"sort"
	"strconv"
)

// ContractRules describe a linear perpetual in native contracts and quote
// currency. Decimal ticks and multipliers retain the exchange's representation.
type ContractRules struct {
	Base           string     `json:"base"`
	Settlement     string     `json:"settlement"`
	Multiplier     string     `json:"multiplier"`
	PriceTick      string     `json:"priceTick"`
	QuantityStep   int64      `json:"quantityStep"`
	Minimum        int64      `json:"minimum"`
	Maximum        int64      `json:"maximum"`
	MinLeverage    int        `json:"minLeverage"`
	MaxLeverage    int        `json:"maxLeverage"`
	TakerFee       string     `json:"takerFee"`
	TradingEnabled bool       `json:"tradingEnabled"`
	RiskTiers      []RiskTier `json:"riskTiers,omitempty"`
}

// RiskTier contains isolated-margin terms for a position in native contracts.
type RiskTier struct {
	MaxContracts   int64   `json:"maxContracts"`
	MaxLeverage    int     `json:"maxLeverage"`
	Maintenance    float64 `json:"maintenance"`
	LiquidationFee float64 `json:"liquidationFee"`
}

func (t RiskTier) valid() bool {
	return t.MaxContracts > 0 && t.MaxLeverage > 0 && t.Maintenance > 0 &&
		t.LiquidationFee >= 0 && t.Maintenance+t.LiquidationFee < 1
}

// keepsStopAhead checks that isolated margin still covers maintenance and fees
// at a mark past the stop by 10 basis points of entry or one tick, whichever
// is larger. Estimate maintenance on the larger of the entry and that mark.
func (r ContractRules) keepsStopAhead(t RiskTier, entry, stop float64, leverage int, long bool) bool {
	if !t.valid() || leverage < r.MinLeverage || leverage > min(t.MaxLeverage, r.MaxLeverage) {
		return false
	}
	buffer := max(number(r.PriceTick), entry*0.001)
	mark := stop + buffer
	if long {
		mark = stop - buffer
	}
	rate := t.Maintenance + t.LiquidationFee
	return mark > 0 && (long && entry > stop || !long && entry < stop) && entry/float64(leverage) >
		math.Abs(entry-mark)+max(entry, mark)*rate+(entry+mark)*r.FeeRate()
}

// AutoRiskSize maximizes reviewed contracts first, then minimizes leverage.
// Every candidate is capped at the position size supported by its risk tier.
func (r ContractRules) AutoRiskSize(budget, collateral, entry, stop float64, cap int, maxQty int64, long bool) (int64, int) {
	var bestQty int64
	var bestLeverage int
	for _, tier := range r.RiskTiers {
		terms := r
		terms.Maximum = min(r.Maximum, tier.MaxContracts, maxQty)
		upper := min(cap, r.MaxLeverage, tier.MaxLeverage)
		if upper < r.MinLeverage || terms.Maximum < terms.Minimum {
			continue
		}
		// Stop clearance decreases with leverage, while affordable quantity
		// increases. Find the highest safe size, then the first rate for it.
		safeCount := sort.Search(upper-r.MinLeverage+1, func(i int) bool {
			return !r.keepsStopAhead(tier, entry, stop, r.MinLeverage+i, long)
		})
		if safeCount == 0 {
			continue
		}
		qty := terms.SizeRisk(budget, collateral, entry, stop, r.MinLeverage+safeCount-1)
		if qty == 0 || qty < bestQty {
			continue
		}
		leverage := r.MinLeverage + sort.Search(safeCount-1, func(i int) bool {
			return terms.SizeRisk(budget, collateral, entry, stop, r.MinLeverage+i) >= qty
		})
		if qty > bestQty || leverage < bestLeverage {
			bestQty, bestLeverage = qty, leverage
		}
	}
	return bestQty, bestLeverage
}

// AutoRiskAllowed also works on an actual fill VWAP, which can be off tick.
func (r ContractRules) AutoRiskAllowed(qty, entry, stop float64, leverage int, long bool) bool {
	for _, tier := range r.RiskTiers {
		if qty <= float64(tier.MaxContracts) {
			return r.keepsStopAhead(tier, entry, stop, leverage, long)
		}
	}
	return false
}

func (r ContractRules) Valid() bool {
	return r.Base != "" && r.Settlement == "USDT" && r.ContractSize() > 0 &&
		number(r.PriceTick) > 0 && r.QuantityStep > 0 && r.Minimum > 0 &&
		r.Maximum >= r.Minimum && r.MinLeverage > 0 && r.MaxLeverage >= r.MinLeverage &&
		number(r.TakerFee) >= 0
}

func (r ContractRules) ContractSize() float64 { return number(r.Multiplier) }
func (r ContractRules) FeeRate() float64      { return number(r.TakerFee) }

func (r ContractRules) ValidQuantity(qty float64) bool {
	return qty >= float64(r.Minimum) && qty <= float64(r.Maximum) && qty == math.Trunc(qty) &&
		r.QuantityStep > 0 && int64(qty)%r.QuantityStep == 0
}

func (r ContractRules) ValidPrice(price float64) bool {
	p, ok := exactFloat(price)
	step, stepOK := new(big.Rat).SetString(r.PriceTick)
	return ok && stepOK && p.Sign() > 0 && step.Sign() > 0 && p.Quo(p, step).IsInt()
}

// EntryLimit snaps the slippage cushion to the exchange tick. Trigger and
// protective prices must themselves already be valid ticks.
func (r ContractRules) EntryLimit(trigger, cushion float64, percent, long bool) (float64, bool) {
	if !r.ValidPrice(trigger) || cushion < 0 {
		return 0, false
	}
	target, _ := exactFloat(trigger)
	distance, ok := exactFloat(cushion)
	if !ok {
		return 0, false
	}
	if percent {
		distance.Mul(distance, target).Quo(distance, big.NewRat(100, 1))
	}
	if long {
		target.Add(target, distance)
	} else {
		target.Sub(target, distance)
	}
	if target.Sign() <= 0 {
		return 0, false
	}
	step, _ := new(big.Rat).SetString(r.PriceTick)
	target.Quo(target, step)
	ticks, remainder := new(big.Int), new(big.Int)
	ticks.QuoRem(target.Num(), target.Denom(), remainder)
	if long && remainder.Sign() != 0 {
		ticks.Add(ticks, big.NewInt(1))
	}
	price, _ := target.SetInt(ticks).Mul(target, step).Float64()
	return price, r.ValidPrice(price)
}

// MoveEntryLimit preserves the exact tick distance for orders created before
// the cushion recipe was saved with the entry.
func (r ContractRules) MoveEntryLimit(trigger, oldTrigger, oldLimit float64) (float64, bool) {
	if !r.ValidPrice(trigger) || !r.ValidPrice(oldTrigger) || !r.ValidPrice(oldLimit) {
		return 0, false
	}
	target, _ := exactFloat(trigger)
	from, _ := exactFloat(oldTrigger)
	limit, _ := exactFloat(oldLimit)
	price, _ := target.Add(target, limit.Sub(limit, from)).Float64()
	return price, r.ValidPrice(price)
}

func (r ContractRules) SizeRisk(budget, collateral, limit, stop float64, leverage int) int64 {
	if !r.Valid() || !r.ValidPrice(limit) || !r.ValidPrice(stop) || limit == stop ||
		leverage < r.MinLeverage || leverage > r.MaxLeverage {
		return 0
	}
	b, okB := exactFloat(budget)
	c, okC := exactFloat(collateral)
	p, _ := exactFloat(limit)
	s, _ := exactFloat(stop)
	multiplier, okM := new(big.Rat).SetString(r.Multiplier)
	fee, okF := new(big.Rat).SetString(r.TakerFee)
	if !okB || !okC || !okM || !okF || b.Sign() <= 0 || c.Sign() <= 0 {
		return 0
	}
	roundTripFees := new(big.Rat).Add(p, s)
	roundTripFees.Mul(roundTripFees, multiplier).Mul(roundTripFees, fee)
	loss := new(big.Rat).Sub(p, s)
	loss.Abs(loss).Mul(loss, multiplier).Add(loss, roundTripFees)
	margin := new(big.Rat).Mul(p, multiplier)
	margin.Quo(margin, big.NewRat(int64(leverage), 1)).Add(margin, roundTripFees)
	if loss.Sign() <= 0 || margin.Sign() <= 0 {
		return 0
	}
	maximum := big.NewRat(r.Maximum, 1)
	b.Quo(b, loss)
	c.Quo(c, margin)
	for _, cap := range []*big.Rat{b, c} {
		if cap.Cmp(maximum) < 0 {
			maximum = cap
		}
	}
	contracts := new(big.Int).Quo(maximum.Num(), maximum.Denom()).Int64()
	contracts -= contracts % r.QuantityStep
	if contracts < r.Minimum {
		return 0
	}
	return contracts
}

func number(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return -1
	}
	return v
}

func exactFloat(v float64) (*big.Rat, bool) {
	return new(big.Rat).SetString(strconv.FormatFloat(v, 'f', -1, 64))
}
