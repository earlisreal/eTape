package opend

import "strings"

// SymbolSpecificFailure is conservative by design: only an explicit US.<code>
// token naming one requested security may justify binary isolation. A bare
// ticker is ambiguous in provider prose and therefore never triggers retries.
func SymbolSpecificFailure(message string, symbols []string) bool {
	words := strings.FieldsFunc(strings.ToLower(message), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	for _, symbol := range symbols {
		code := strings.ToLower(strings.TrimPrefix(symbol, "US."))
		if code == "" {
			continue
		}
		for i, word := range words {
			if word == "us" && i+1 < len(words) && words[i+1] == code {
				return true
			}
		}
	}
	return false
}
