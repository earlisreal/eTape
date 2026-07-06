package exec

import "math"

// GlobalLimits caps aggregate risk across all venues. A zero value means "cap
// not set — do not enforce".
type GlobalLimits struct {
	MaxDayLoss              float64
	MaxSymbolPositionValue  float64
	MaxSymbolPositionShares float64
}

// VenueLimits caps one venue's risk. Zero values mean "cap not set".
type VenueLimits struct {
	MaxOrderValue     float64
	MaxPositionValue  float64
	MaxPositionShares float64
	MaxOpenOrders     int
}

type GateConfig struct {
	Global GlobalLimits
	Venue  map[VenueID]VenueLimits
}

// signedQty returns the request's signed effect on a position (long +, short -).
func signedQty(req OrderRequest) float64 {
	if longward(req.Side) {
		return req.Qty
	}
	return -req.Qty
}

// orderValue values an order for the max-order-value / position-value checks:
//   Limit      -> limit price
//   StopLimit  -> limit price (it triggers into a limit at that price)
//   Stop       -> stop price (triggers into a market ~at the stop; always priced)
//   Market     -> last-trade mark (ok=false when there is no mark -> must block)
func orderValue(req OrderRequest, marks MarkSource) (float64, bool) {
	switch req.Type {
	case TypeMarket:
		m, ok := marks.LastTrade(req.Symbol)
		if !ok {
			return 0, false
		}
		return req.Qty * m, true
	case TypeStop:
		return req.Qty * req.StopPrice, true
	default: // Limit, StopLimit
		return req.Qty * req.LimitPrice, true
	}
}

// markOr returns the last-trade mark for resulting-position valuation, falling
// back to the order's own price when no mark exists (limit/stop-limit -> limit
// price; bare stop -> stop price). A market order always has a mark here (the
// order-value check above already blocked it otherwise).
func markOr(req OrderRequest, marks MarkSource) float64 {
	if m, ok := marks.LastTrade(req.Symbol); ok {
		return m
	}
	if req.LimitPrice > 0 {
		return req.LimitPrice
	}
	return req.StopPrice
}

// Evaluate runs the two-layer gate. Returns (true, "") to allow, or (false,
// reason) at the first failing rule. Pure — the Core calls it in-loop.
func Evaluate(s *State, cfg GateConfig, req OrderRequest, marks MarkSource) (bool, string) {
	// 1. master armed
	if !s.MasterArmed {
		return false, "master disarmed"
	}
	// 2. venue armed
	if vs, ok := s.Venues[req.Venue]; !ok || !vs.Armed {
		return false, "venue disarmed"
	}
	// 3. duplicate ID (global — one event log)
	if _, dup := s.orderIndex[req.ClientOrderID]; dup {
		return false, "duplicate order id"
	}

	// 3.5. global day-loss breach — checked here (not just reactively via
	// auto-disarm) so a re-arm after breach, or the window before the first
	// account refresh, cannot bypass it. Auto-disarm (Core.handleBrokerEvent)
	// remains the reactive layer on top of this authoritative submit-time check.
	if BreachedDayLoss(s, cfg) {
		return false, "day-loss breached"
	}

	vl, hasVenueConfig := cfg.Venue[req.Venue]
	if !hasVenueConfig {
		return false, "no gate config for venue"
	}

	// 4a. per-venue max order value
	val, ok := orderValue(req, marks)
	if !ok {
		return false, "no mark to value market order"
	}
	if vl.MaxOrderValue > 0 && val > vl.MaxOrderValue {
		return false, "order value exceeds venue cap"
	}

	// 4b. per-venue max resulting position (shares + value)
	mark := markOr(req, marks)
	venueResult := s.VenuePositionShares(req.Venue, req.Symbol) +
		directional(s.VenueWorkingSameDir(req.Venue, req.Symbol, req.Side), req.Side) +
		signedQty(req)
	if vl.MaxPositionShares > 0 && math.Abs(venueResult) > vl.MaxPositionShares {
		return false, "resulting venue position exceeds share cap"
	}
	if vl.MaxPositionValue > 0 && math.Abs(venueResult)*mark > vl.MaxPositionValue {
		return false, "resulting venue position exceeds value cap"
	}

	// 4c. per-venue max open orders
	if vl.MaxOpenOrders > 0 && workingCount(s.Venue(req.Venue), req.Symbol) >= vl.MaxOpenOrders {
		return false, "max open orders on venue"
	}

	// 5. global max resulting per-symbol position (shares + value) across venues
	globalResult := s.SymbolNetShares(req.Symbol) +
		directional(s.SymbolWorkingSameDir(req.Symbol, req.Side), req.Side) +
		signedQty(req)
	if cfg.Global.MaxSymbolPositionShares > 0 && math.Abs(globalResult) > cfg.Global.MaxSymbolPositionShares {
		return false, "resulting symbol position exceeds global share cap"
	}
	if cfg.Global.MaxSymbolPositionValue > 0 && math.Abs(globalResult)*mark > cfg.Global.MaxSymbolPositionValue {
		return false, "resulting symbol position exceeds global value cap"
	}
	return true, ""
}

// directional signs a same-direction working-exposure magnitude by side.
func directional(mag float64, side Side) float64 {
	if longward(side) {
		return mag
	}
	return -mag
}

// workingCount counts working orders (any symbol) on a venue — the max-open-
// orders cap is a venue-wide working-order count.
func workingCount(vs *VenueState, _ string) int {
	n := 0
	for _, o := range vs.Orders {
		if o.Working() {
			n++
		}
	}
	return n
}

// BreachedDayLoss reports whether the summed venue day P&L has breached the
// global max-day-loss cap. The Core calls this on each account refresh and
// auto-disarms the master switch on breach.
func BreachedDayLoss(s *State, cfg GateConfig) bool {
	if cfg.Global.MaxDayLoss <= 0 {
		return false
	}
	return s.TotalDayPnL() <= -cfg.Global.MaxDayLoss
}
