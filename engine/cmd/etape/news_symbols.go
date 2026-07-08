package main

import "sort"

// newsSymbols composes the news poller's rotation set: current scanner-pool
// members ∪ live UI demands (interest demands included), deduped and sorted.
// Empty strings are dropped.
func newsSymbols(pool, liveDemands []string) []string {
	set := map[string]struct{}{}
	add := func(ss []string) {
		for _, s := range ss {
			if s != "" {
				set[s] = struct{}{}
			}
		}
	}
	add(pool)
	add(liveDemands)
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
