package main

import "sort"

// newsSymbols composes the news poller's rotation set: config watchlist ∪ CLI
// --watch/--focus ∪ live UI demands (interest demands included), deduped and
// sorted. demand may be nil (no hub). Empty strings are dropped.
func newsSymbols(watchlist, watchCSV, focusCSV []string, demand func() []string) []string {
	set := map[string]struct{}{}
	add := func(ss []string) {
		for _, s := range ss {
			if s != "" {
				set[s] = struct{}{}
			}
		}
	}
	add(watchlist)
	add(watchCSV)
	add(focusCSV)
	if demand != nil {
		add(demand())
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
