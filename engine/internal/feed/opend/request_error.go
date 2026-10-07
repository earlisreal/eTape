package opend

import "strings"

// IsSymbolSpecificRequestError is deliberately allow-list based: batch
// splitting multiplies provider calls, so only a response that identifies a
// bad symbol may trigger it. Quota, permission, rate, transport and unknown
// failures stay whole and retry on their normal cadence.
func IsSymbolSpecificRequestError(message string) bool {
	m := strings.ToLower(message)
	for _, marker := range []string{"invalid security", "invalid stock", "invalid code", "unknown security", "security not found", "not available for", "get stock's sector interface does not support etfs type"} {
		if strings.Contains(m, marker) {
			return true
		}
	}
	return false
}
