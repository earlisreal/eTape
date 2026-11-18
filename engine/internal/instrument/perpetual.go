// Package instrument defines identifiers shared by market data and execution.
package instrument

import "strings"

const PerpetualPrefix = "PERP."

func IsPerpetual(symbol string) bool {
	return strings.HasPrefix(symbol, PerpetualPrefix)
}

type Perpetual struct {
	Exchange string
	Native   string
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
