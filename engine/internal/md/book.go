package md

import "github.com/earlisreal/eTape/engine/internal/feed"

type bookStore struct{ m map[string]feed.Book }

func newBookStore() *bookStore { return &bookStore{m: make(map[string]feed.Book)} }

// set replaces the symbol's book (full 10-level replace — cheaper than
// diffing at this depth) and returns it for emission.
func (s *bookStore) set(b feed.Book) feed.Book {
	s.m[b.Symbol] = b
	return b
}
