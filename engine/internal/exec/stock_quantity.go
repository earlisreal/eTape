package exec

import (
	"math"
	"strings"
)

const stockQuantityScale = 1_000_000_000

func normalizedStockQuantity(symbol string, qty float64) float64 {
	if strings.HasPrefix(symbol, "US.") {
		return math.Round(qty*stockQuantityScale) / stockQuantityScale
	}
	return qty
}

func floorStockQuantity(qty float64) float64 {
	return math.Floor(qty*stockQuantityScale) / stockQuantityScale
}

type fractionalStockBroker interface {
	FractionalStockOrderReason(OrderRequest) string
}

func (c *Core) stockFractionalReason(req OrderRequest) string {
	if !strings.HasPrefix(req.Symbol, "US.") || math.Trunc(req.Qty) == req.Qty {
		return ""
	}
	return c.stockFractionalRouteReason(req)
}

func (c *Core) stockFractionalRouteReason(req OrderRequest) string {
	if !strings.HasPrefix(req.Symbol, "US.") {
		return "fractional stock sizing requires a US stock"
	}
	if req.Qty != normalizedStockQuantity(req.Symbol, req.Qty) {
		return "fractional stock quantity supports up to nine decimal places"
	}
	if req.Side != SideBuy && req.Side != SideSell {
		return "stock shorts and covers require whole shares"
	}
	if req.Side == SideBuy && c.state.VenuePositionShares(req.Venue, req.Symbol) < 0 {
		return "cover the short position before opening a fractional long"
	}
	broker, ok := c.brokers[req.Venue].(fractionalStockBroker)
	if !ok {
		return "selected venue requires whole stock shares"
	}
	return broker.FractionalStockOrderReason(req)
}

func (c *Core) sizePositionShares(req OrderRequest, shares, pct float64) float64 {
	shares = math.Abs(shares)
	qty := shares * pct / 100
	if pct == 100 {
		qty = shares
	}
	req.Qty = floorStockQuantity(qty)
	if pct == 100 && req.Side == SideSell && shares != math.Trunc(shares) {
		// Pass the full confirmed quantity to the route guard, even when that
		// route is unavailable. A full close must never sell only whole shares.
		return shares
	}
	if req.Side == SideSell && c.stockFractionalRouteReason(req) == "" {
		return req.Qty
	}
	return math.Floor(qty)
}
