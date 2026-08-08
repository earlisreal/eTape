package news

import (
	"regexp"
	"sort"
	"strings"
)

func normalizeMoomooSecurity(raw string) (string, bool) {
	p := strings.Split(strings.ToUpper(strings.TrimSpace(raw)), ".")
	if len(p) != 2 || p[0] == "" || p[1] == "" {
		return "", false
	}
	var ticker string
	switch {
	case p[0] == "US":
		ticker = p[1]
	case p[1] == "US":
		ticker = p[0]
	default:
		return "", false
	}
	for _, r := range ticker {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '.' {
			return "", false
		}
	}
	return "US." + ticker, true
}

func normalizeRelatedSecurities(raw []string) []string {
	set := map[string]struct{}{}
	for _, s := range raw {
		if n, ok := normalizeMoomooSecurity(s); ok {
			set[n] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func trackedSymbols(plan SymbolPlan) map[string]struct{} {
	set := map[string]struct{}{}
	for _, group := range [][]string{plan.Active, plan.Scanner} {
		for _, raw := range group {
			if s, ok := normalizeMoomooSecurity(raw); ok {
				set[s] = struct{}{}
			}
		}
	}
	return set
}

func relatedTracked(raw []string, tracked map[string]struct{}) []string {
	out := make([]string, 0)
	for _, s := range normalizeRelatedSecurities(raw) {
		if _, ok := tracked[s]; ok {
			out = append(out, s)
		}
	}
	return out
}

func headlineHasTicker(headline, symbol string) bool {
	ticker := strings.TrimPrefix(symbol, "US.")
	if ticker == "" {
		return false
	}
	prefix := "(?i)"
	if len(ticker) <= 2 {
		prefix = ""
	} // avoid lower-case English words such as "on"
	return regexp.MustCompile(prefix + `(^|[^A-Za-z0-9])` + regexp.QuoteMeta(ticker) + `($|[^A-Za-z0-9])`).MatchString(headline)
}
