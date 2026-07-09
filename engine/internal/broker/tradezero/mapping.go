package tradezero

import (
	"fmt"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// wireSymbol strips eTape's "US." market-prefix convention so outbound
// requests carry the bare ticker TradeZero's API expects (e.g. "AAPL", not
// "US.AAPL").
func wireSymbol(symbol string) string {
	return strings.TrimPrefix(symbol, "US.")
}

// domainSymbol re-adds the "US." prefix to a bare ticker TradeZero's API
// returns, so it matches the domain convention the rest of eTape keys
// Order/Position/Fill.Symbol by. TradeZero is a US-only venue (CLAUDE.md), so
// this is always correct.
func domainSymbol(symbol string) string {
	return "US." + symbol
}

func orderTypeWire(t exec.OrderType) (string, error) {
	switch t {
	case exec.TypeMarket:
		return "Market", nil
	case exec.TypeLimit:
		return "Limit", nil
	case exec.TypeStop:
		return "Stop", nil
	case exec.TypeStopLimit:
		return "StopLimit", nil
	default:
		return "", fmt.Errorf("tradezero: unsupported order type %v", t)
	}
}

// sideWire maps a trader action to TZ side+openClose. Never sends "SellShort".
func sideWire(s exec.Side) (side, openClose string) {
	switch s {
	case exec.SideBuy:
		return "Buy", "Open"
	case exec.SideSell:
		return "Sell", "Close"
	case exec.SideShort:
		return "Sell", "Open"
	case exec.SideCover:
		return "Buy", "Close"
	default:
		return "Buy", "Open"
	}
}

// sideDomain un-enriches a TZ response side (which may be "SellShort") using
// openClose. Buy/Open=Buy, Sell/Close=Sell, Sell(or SellShort)/Open=Short,
// Buy/Close=Cover.
func sideDomain(wireSide, openClose string) exec.Side {
	buyish := wireSide == "Buy"
	if buyish {
		if openClose == "Close" {
			return exec.SideCover
		}
		return exec.SideBuy
	}
	if openClose == "Open" {
		return exec.SideShort
	}
	return exec.SideSell
}

// tifWire maps domain TIF to TZ, coercing Day/GTC to their _Plus variants for
// extended-hours limit/stop-limit orders (avoids TZ rejecting a plain Day limit
// placed outside RTH).
func tifWire(t exec.TIF, extendedHours bool, ot exec.OrderType) string {
	// TZ's _Plus TIFs are documented Limit-ONLY (docs/2026-07-03-tradezero-api.md).
	// Extended-hours stop-limit behaviour on TZ is unverified (Monday-live item);
	// keep a stop-limit's base TIF rather than sending a Limit-only _Plus it may
	// reject.
	limitish := ot == exec.TypeLimit
	switch t {
	case exec.TIFDay:
		if extendedHours && limitish {
			return "Day_Plus"
		}
		return "Day"
	case exec.TIFGTC:
		if extendedHours && limitish {
			return "GTC_Plus"
		}
		return "GoodTillCancel"
	case exec.TIFIOC:
		return "ImmediateOrCancel"
	case exec.TIFFOK:
		return "FillOrKill"
	default:
		return "Day"
	}
}

func isExtendedHours(clk clock.Clock) bool {
	switch session.PhaseAt(clk.Now()) {
	case session.PreMarket, session.PostMarket:
		return true
	default:
		return false
	}
}

// extendedHoursFor resolves whether TZ's _Plus TIF variants should be used
// for req's explicit session choice: exec.ExtendedHoursFor (shared with
// Alpaca — see its mapping.go) applied to TZ's own clock-derived AUTO
// resolution. OVERNIGHT cannot actually reach TZ — its
// Capabilities().OvernightSession is false, so Core.handleSubmit blocks an
// explicit Overnight order before any adapter call — but exec.ExtendedHoursFor
// maps it to the extended path defensively rather than silently reverting to
// plain RTH TIF if that gate is ever bypassed.
func extendedHoursFor(s exec.OrderSession, clk clock.Clock) bool {
	return exec.ExtendedHoursFor(s, isExtendedHours(clk))
}
