package main

import (
	"sort"

	"github.com/earlisreal/eTape/engine/internal/news"
)

// newsSymbolPlan preserves active demand priority while keeping scanner polling
// available in the same rate-limited lane.
func newsSymbolPlan(pool, liveDemands []string) news.SymbolPlan {
	clean := func(ss []string) []string {
		set := map[string]struct{}{}
		for _, s := range ss {
			if s != "" {
				set[s] = struct{}{}
			}
		}
		out := make([]string, 0, len(set))
		for s := range set {
			out = append(out, s)
		}
		sort.Strings(out)
		return out
	}
	active := clean(liveDemands)
	activeSet := make(map[string]struct{}, len(active))
	for _, s := range active {
		activeSet[s] = struct{}{}
	}
	scanner := make([]string, 0, len(pool))
	for _, s := range clean(pool) {
		if _, active := activeSet[s]; !active {
			scanner = append(scanner, s)
		}
	}
	return news.SymbolPlan{Active: active, Scanner: scanner}
}
