package alpaca

import (
	"fmt"
	"math"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// wireSymbol strips eTape's "US." market-prefix convention so outbound
// requests carry the bare ticker Alpaca's API expects (e.g. "AAPL", not
// "US.AAPL" — Alpaca rejects the latter as an unknown symbol).
func wireSymbol(symbol string) string {
	return strings.TrimPrefix(symbol, "US.")
}

// domainSymbol re-adds the "US." prefix to a bare ticker Alpaca's API
// returns, so it matches the domain convention the rest of eTape keys
// Order/Position/Fill.Symbol by. Alpaca is a US-only venue (CLAUDE.md), so
// this is always correct.
func domainSymbol(symbol string) string {
	return "US." + symbol
}

func orderTypeWire(t exec.OrderType) (string, error) {
	switch t {
	case exec.TypeMarket:
		return "market", nil
	case exec.TypeLimit:
		return "limit", nil
	case exec.TypeStop:
		return "stop", nil
	case exec.TypeStopLimit:
		return "stop_limit", nil
	default:
		return "", fmt.Errorf("alpaca: unsupported order type %v", t)
	}
}

func sideWire(s exec.Side) string {
	switch s {
	case exec.SideBuy, exec.SideCover:
		return "buy"
	default: // Sell, Short
		return "sell"
	}
}

func tifWire(t exec.TIF) (string, error) {
	switch t {
	case exec.TIFDay:
		return "day", nil
	case exec.TIFGTC:
		return "gtc", nil
	case exec.TIFFOK:
		return "fok", nil
	case exec.TIFIOC:
		// Verified 2026-07-06 (docs/2026-07-03-alpaca-api.md): IOC is rejected
		// (42210000) outside market hours on a standard account -- a session-time
		// gate, not an Elite-tier restriction (FOK/OPG/CLS are all accepted). This
		// function has no session-time context, so IOC is rejected here rather
		// than risking an order Alpaca would bounce anyway.
		return "", fmt.Errorf("alpaca: TIF %v is accepted by Alpaca only during market hours; submit during RTH", t)
	default:
		return "", fmt.Errorf("alpaca: unsupported TIF %v", t)
	}
}

// isExtendedHours reports whether clk's current time falls outside RTH on
// Alpaca's terms. Unlike TradeZero's equivalent (tradezero/mapping.go), this
// includes Overnight: Alpaca uniquely supports the 20:00-04:00 ET Blue Ocean
// ATS session (Capabilities().OvernightSession is true) and requires the same
// extended_hours flag for overnight limit orders as for pre/post-market ones.
func isExtendedHours(clk clock.Clock) bool {
	switch session.PhaseAt(clk.Now()) {
	case session.PreMarket, session.PostMarket, session.Overnight:
		return true
	default:
		return false
	}
}

// extendedHoursFor resolves whether Alpaca's extended_hours flag should be set
// for req's explicit session choice: exec.ExtendedHoursFor (shared with
// TradeZero — see its mapping.go) applied to Alpaca's own clock-derived AUTO
// resolution. Alpaca has one flag for all non-RTH sessions (its Blue Ocean
// ATS overnight window shares the flag with pre/post-market). An explicit
// OVERNIGHT only reaches an adapter whose Capabilities().OvernightSession is
// true (Core.handleSubmit gates it), so this never sets extended_hours=true
// for an overnight order on a venue that can't work it.
func extendedHoursFor(s exec.OrderSession, clk clock.Clock) bool {
	return exec.ExtendedHoursFor(s, isExtendedHours(clk))
}

// roundPrice applies Alpaca's sub-penny rule: >= $1 -> 2 dp, < $1 -> 4 dp.
func roundPrice(p float64) float64 {
	if p >= 1 {
		return math.Round(p*100) / 100
	}
	return math.Round(p*10000) / 10000
}

func sideDomain(alpacaSide string, positionQtyBefore float64) exec.Side {
	if alpacaSide == "buy" {
		if positionQtyBefore < 0 {
			return exec.SideCover
		}
		return exec.SideBuy
	}
	if positionQtyBefore > 0 {
		return exec.SideSell
	}
	return exec.SideShort
}
