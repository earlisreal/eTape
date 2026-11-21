package exec

import (
	"context"
	"math"
	"time"
)

type stockShortBroker interface {
	StockShortCapacity(ctx context.Context, symbol string, referencePrice float64) (float64, error)
}

func (c *Core) stockShortCapacity(ctx context.Context, venue VenueID, symbol, exclude string, limit, trigger float64) (float64, string) {
	vs := c.state.Venue(venue)
	switch broker := c.brokers[venue].(type) {
	case interface{ SimulatesStockShorts() }:
		funds := vs.Account.Equity
		for _, p := range vs.Positions {
			price := c.marks[p.Symbol]
			if !finitePositive(price) {
				price = p.AvgPrice
			}
			funds -= math.Abs(p.Qty) * price
		}
		for _, orders := range []map[string]Order{vs.Orders, vs.ExternalOrders} {
			for _, order := range orders {
				req := OrderRequest{Venue: venue, Symbol: order.Symbol, Side: order.Side}
				if order.ID == exclude || !order.Working() || order.Side != SideBuy && order.Side != SideShort || reducesPosition(c.state, req) {
					continue
				}
				price := math.Max(order.StopPrice, order.LimitPrice)
				if !finitePositive(price) {
					price = c.marks[order.Symbol]
					if !finitePositive(price) {
						return 0, "price unavailable for existing simulated entry"
					}
				}
				funds -= order.LeavesQty * price
			}
		}
		return math.Max(0, math.Floor(funds/math.Max(limit, trigger))), ""
	case stockShortBroker:
		// Use the trigger as the reference for an estimate before the book is
		// available. A current ask gives a more conservative valuation.
		reference := trigger
		book := c.trailBooks[symbol]
		if age := c.now() - book.askReceiptMs; age >= 0 && age <= 1000 && book.askReceiptMs > 0 && !book.clockInvalid && finitePositive(book.Ask) {
			reference = math.Max(reference, book.Ask)
		}
		request, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		capacity, err := broker.StockShortCapacity(request, symbol, math.Max(limit, reference*1.03))
		if err != nil {
			return 0, err.Error()
		}
		return math.Floor(capacity), ""
	default:
		return 0, "select Simulator or an Alpaca account for stock Chart Risk Entry shorts"
	}
}

func (c *Core) sizeStockRisk(ctx context.Context, venue VenueID, symbol, exclude string, side Side, budget, entry, stop, trigger, funds, reviewed float64) (float64, string) {
	qty := riskShares(budget, entry, stop, funds)
	if side == SideShort {
		capacity, reason := c.stockShortCapacity(ctx, venue, symbol, exclude, entry, trigger)
		if reason != "" {
			return 0, reason
		}
		distance := math.Round((stop-entry)*10000) / 10000
		if distance <= 0 {
			return 0, "invalid short risk distance"
		}
		qty = math.Min(capacity, math.Floor(budget/distance+1e-9))
	}
	qty = math.Min(qty, math.Floor(reviewed))
	if !finitePositive(qty) {
		return 0, "risk or funding budget rounds to zero shares"
	}
	return qty, ""
}

func (c *Core) checkStockShortTrigger(ctx context.Context, entry Order) string {
	vs := c.state.Venue(entry.Venue)
	if !c.state.PositionsReady(entry.Venue) || vs.FlattenPending {
		return "position data unavailable or flatten pending"
	}
	owner, _, reason := c.riskAddProtection(entry.Venue, entry.Symbol, entry.Side)
	if reason != "" {
		return reason
	}
	if owner.ID != entry.RiskEntry.SharedEntryID {
		return "position or shared protection changed"
	}
	for _, orders := range []map[string]Order{vs.Orders, vs.ExternalOrders} {
		for _, order := range orders {
			if order.Symbol == entry.Symbol && order.Working() && order.ID != entry.ID && order.ID != entry.RiskEntry.StopID {
				return "another order is working for this symbol"
			}
		}
	}
	capacity, reason := c.stockShortCapacity(ctx, entry.Venue, entry.Symbol, entry.ID, entry.LimitPrice, entry.StopPrice)
	if reason != "" {
		return reason
	}
	if c.now() >= entry.Held.DeadlineMs {
		return "entry session expired during short eligibility check"
	}
	if capacity < entry.LeavesQty {
		return "short opening capacity is below the armed share quantity"
	}
	return ""
}
