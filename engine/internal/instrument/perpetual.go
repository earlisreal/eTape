// Package instrument defines identifiers shared by market data and execution.
package instrument

import "strings"

const PerpetualPrefix = "PERP."

// The Binance and BingX feeds archive integer units of 10⁻⁸ base assets.
// The fixed scale keeps older recordings readable after venue rule changes.
const BaseQuantityScale = 100_000_000

func DisplayQuantity(symbol string, raw int64) float64 {
	if strings.HasPrefix(symbol, "PERP.BINANCE.") || strings.HasPrefix(symbol, "PERP.BINGX.") {
		return float64(raw) / BaseQuantityScale
	}
	return float64(raw)
}

func IsPerpetual(symbol string) bool {
	return strings.HasPrefix(symbol, PerpetualPrefix)
}

type Perpetual struct {
	Exchange string
	Native   string
}

type PerpetualMarket struct {
	ID             Perpetual
	Base           string
	Settlement     string
	TradingEnabled bool
}

func (p Perpetual) String() string {
	return PerpetualPrefix + p.Exchange + "." + p.Native
}

func ParsePerpetual(symbol string) (Perpetual, bool) {
	value, ok := strings.CutPrefix(symbol, PerpetualPrefix)
	exchange, native, found := strings.Cut(value, ".")
	if !ok || !found || exchange == "" || native == "" {
		return Perpetual{}, false
	}
	for _, s := range []string{exchange, native} {
		for _, r := range s {
			if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
				return Perpetual{}, false
			}
		}
	}
	return Perpetual{Exchange: exchange, Native: native}, true
}
