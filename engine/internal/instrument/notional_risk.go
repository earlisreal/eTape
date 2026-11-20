package instrument

import "math"

// NotionalRisk holds the exchange terms used to assess the entire isolated
// position at an adverse mark beyond the shared stop. Quantities are lots.
type NotionalRisk struct {
	Mark         float64        `json:"mark"`
	MinimumValue float64        `json:"minimumValue"`
	Tiers        []NotionalTier `json:"tiers"`
	Position     RiskPosition   `json:"position"`
}

type NotionalTier struct {
	Maximum   float64 `json:"maximum"`
	Rate      float64 `json:"rate"`
	Deduction float64 `json:"deduction"`
}

type RiskPosition struct {
	Qty              float64 `json:"qty"`
	AvgPrice         float64 `json:"avgPrice"`
	Margin           float64 `json:"margin"`
	LiquidationPrice float64 `json:"liquidationPrice"`
	Leverage         int     `json:"leverage"`
}

func (r ContractRules) LiquidationClearance(entry float64) float64 {
	return max(number(r.PriceTick), entry*0.001)
}

func (r ContractRules) StopClearsLiquidation(entry, stop, liquidation float64, long bool) bool {
	buffer := r.LiquidationClearance(entry)
	if !(entry > 0 && stop > 0 && number(r.PriceTick) > 0 && liquidation >= 0 && liquidation < math.Inf(1)) {
		return false
	}
	if long {
		return stop-buffer > liquidation
	}
	return liquidation > stop+buffer
}

func (n NotionalRisk) maintenance(value float64) (float64, bool) {
	for _, tier := range n.Tiers {
		if value <= tier.Maximum {
			return max(0, value*tier.Rate-tier.Deduction), true
		}
	}
	return 0, false
}

// SizeNotionalRisk prefers quantity first, then the highest supported
// leverage. The adapter must verify the exchange's opening capacity at the
// selected leverage before accepting the arm and again before submission.
func (r ContractRules) SizeNotionalRisk(n NotionalRisk, budget, cash, limit, stop float64,
	minLeverage, maxLeverage, currentLeverage int, currentCapacity, maxQty int64, long bool,
) (int64, int) {
	if !r.Valid() || !r.ValidPrice(limit) || !r.ValidPrice(stop) || long != (limit > stop) || limit == stop ||
		!(n.Mark > 0 && n.Mark < math.Inf(1) && n.MinimumValue >= 0 && n.MinimumValue < math.Inf(1)) || len(n.Tiers) == 0 {
		return 0, 0
	}
	var last NotionalTier
	for _, tier := range n.Tiers {
		if !(tier.Maximum > last.Maximum && tier.Maximum < math.Inf(1) && tier.Rate >= last.Rate && tier.Rate < 1 &&
			tier.Deduction >= 0 && tier.Deduction < math.Inf(1)) ||
			last.Maximum*tier.Rate-tier.Deduction+0.01 < last.Maximum*last.Rate-last.Deduction {
			return 0, 0
		}
		last = tier
	}
	position := n.Position
	if position.Qty != 0 {
		if long != (position.Qty > 0) || !(position.Margin > 0 && position.Margin < math.Inf(1)) ||
			!r.StopClearsLiquidation(position.AvgPrice, stop, position.LiquidationPrice, long) ||
			position.Leverage < minLeverage || position.Leverage > maxLeverage || position.Leverage != currentLeverage {
			return 0, 0
		}
		minLeverage, maxLeverage = position.Leverage, position.Leverage
	}
	mark := stop + r.LiquidationClearance(limit)
	direction := -1.0
	if long {
		mark, direction = stop-r.LiquidationClearance(limit), 1
	}
	if mark <= 0 {
		return 0, 0
	}
	unit, fee := r.ContractSize(), r.FeeRate()
	existingQty := math.Abs(position.Qty) * unit
	entryValue := max(n.Mark, limit)
	existingEquity := position.Margin + direction*(mark-position.AvgPrice)*existingQty
	step := r.QuantityStep
	desiredMin := max(float64(r.Minimum), math.Ceil(n.MinimumValue/(limit*unit)))
	if desiredMin > float64(maxQty) {
		return 0, 0
	}
	var bestQty int64
	bestLeverage := 0
	for leverage := min(maxLeverage, r.MaxLeverage); leverage >= max(minLeverage, r.MinLeverage); leverage-- {
		qty := min(maxQty, r.SizeRisk(budget, cash, limit, stop, leverage))
		if leverage == currentLeverage {
			qty = min(qty, currentCapacity)
		}
		affordable := cash / (unit * (entryValue/float64(leverage) + (limit+stop)*fee))
		if affordable < float64(qty) {
			qty = int64(max(0, affordable))
		}
		qty -= qty % step
		if qty <= bestQty || float64(qty) < desiredMin {
			continue
		}
		passes := func(lots int64) bool {
			base := float64(lots) * unit
			value := (existingQty + base) * mark
			maintenance, covered := n.maintenance(value)
			equity := existingEquity + base*(limit/float64(leverage)+direction*(mark-limit)-limit*fee)
			return covered && (existingQty+base)*entryValue <= last.Maximum && equity > maintenance+value*fee
		}
		if !passes(qty) {
			// The maintenance schedule increases with notional. Starting from a
			// protected position, find the largest lot count with clearance.
			lo, hi := int64(0), qty/step
			for lo < hi {
				mid := lo + (hi-lo+1)/2
				if passes(mid * step) {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			qty = lo * step
		}
		if qty > bestQty && float64(qty) >= desiredMin {
			bestQty, bestLeverage = qty, leverage
		}
	}
	return bestQty, bestLeverage
}
