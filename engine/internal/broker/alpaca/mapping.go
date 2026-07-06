package alpaca

import (
	"fmt"
	"math"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

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
	default:
		return "", fmt.Errorf("alpaca: TIF %v requires an Elite account (standard is day/gtc)", t)
	}
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
