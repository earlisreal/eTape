package md

import "github.com/earlisreal/eTape/engine/internal/feed"

type quoteStore struct{ m map[string]feed.Quote }

func newQuoteStore() *quoteStore { return &quoteStore{m: make(map[string]feed.Quote)} }

// set replaces the symbol's quote and returns it for emission.
func (s *quoteStore) set(q feed.Quote) feed.Quote {
	s.m[q.Symbol] = q
	return q
}
