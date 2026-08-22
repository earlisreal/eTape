// Package watchlist owns the user-pinned symbol list: membership, symbol
// normalization, the 400-symbol cap, JSON persistence through the store's
// existing config table, and the poller that pushes quota-free 3203 snapshots
// over the watchlist.rows topic. One global list, shared across all windows.
package watchlist

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

const (
	// configKey is the store config row holding the JSON array of symbols.
	configKey = "watchlist"
	// defaultCap is the 3203 single-request ceiling — one request per tick.
	defaultCap = 400
)

// ErrFull is returned by Add when the list is at its cap.
var ErrFull = errors.New("watchlist full")

// configStore is the store surface List needs (satisfied by *store.Store).
type configStore interface {
	GetConfig(key string) (string, bool, error)
	SetConfig(key, value string)
	Flush()
}

// List is the in-memory membership set with write-through persistence. Safe
// for concurrent Add/Remove (conn goroutine) + Symbols (poller goroutine) +
// Seed (demo boot).
type List struct {
	st       configStore
	mu       sync.Mutex
	syms     []string // insertion order; authoritative payload order
	cap      int
	revision uint64
}

// NewList loads config key "watchlist" (a JSON string array); an absent key
// yields an empty list.
func NewList(st configStore) (*List, error) {
	l := &List{st: st, cap: defaultCap}
	raw, ok, err := st.GetConfig(configKey)
	if err != nil {
		return nil, err
	}
	if ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &l.syms); err != nil {
			return nil, err
		}
	}
	return l, nil
}

// NewEmpty returns a List backed by st but seeded empty in memory, bypassing
// the initial config read entirely. Used as a fallback when NewList's config
// load fails (e.g. corrupt persisted JSON) — a corrupt watchlist config must
// never block engine boot. The list still persists correctly on the next
// mutation (Add/Remove/Seed), which overwrites the corrupt stored value.
func NewEmpty(st configStore) *List {
	return &List{st: st, cap: defaultCap}
}

// Normalize uppercases and ensures the US. prefix (US-only scope). A symbol
// that already carries a market prefix (contains ".") is only uppercased.
func Normalize(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if strings.Contains(s, ".") {
		return s
	}
	return "US." + s
}

// AddWithRevision normalizes and appends symbol, returning the exact resulting
// list revision while holding the same lock as the mutation.
func (l *List) AddWithRevision(symbol string) (bool, []string, uint64, error) {
	sym := Normalize(symbol)
	if sym == "" {
		syms, revision := l.Snapshot()
		return false, syms, revision, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.syms {
		if s == sym {
			return false, append([]string(nil), l.syms...), l.revision, nil
		}
	}
	if len(l.syms) >= l.cap {
		return false, append([]string(nil), l.syms...), l.revision, ErrFull
	}
	l.syms = append(l.syms, sym)
	l.revision++
	l.persistLocked()
	return true, append([]string(nil), l.syms...), l.revision, nil
}

// Add normalizes and appends symbol, returning added=false for a duplicate
// (harmless no-op) and ErrFull past the cap. Persists + Flushes on a real add.
func (l *List) Add(symbol string) (bool, error) {
	added, _, _, err := l.AddWithRevision(symbol)
	return added, err
}

// RemoveWithRevision deletes symbol if present and returns the exact resulting
// list revision while holding the same lock as the mutation.
func (l *List) RemoveWithRevision(symbol string) (bool, []string, uint64) {
	sym := Normalize(symbol)
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, s := range l.syms {
		if s == sym {
			l.syms = append(l.syms[:i], l.syms[i+1:]...)
			l.revision++
			l.persistLocked()
			return true, append([]string(nil), l.syms...), l.revision
		}
	}
	return false, append([]string(nil), l.syms...), l.revision
}

// Remove deletes symbol if present (idempotent); persists on a real removal.
func (l *List) Remove(symbol string) bool {
	removed, _, _ := l.RemoveWithRevision(symbol)
	return removed
}

// Snapshot returns a copy of the insertion-ordered membership and its exact
// revision from one lock acquisition.
func (l *List) Snapshot() ([]string, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.syms))
	copy(out, l.syms)
	return out, l.revision
}

// Symbols returns a copy in insertion order.
func (l *List) Symbols() []string {
	out, _ := l.Snapshot()
	return out
}

func (l *List) Revision() uint64 {
	_, revision := l.Snapshot()
	return revision
}

// Seed replaces the whole list (demo boot: trusted synth universe, no probe).
func (l *List) Seed(symbols []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syms = l.syms[:0]
	for _, s := range symbols {
		l.syms = append(l.syms, Normalize(s))
	}
	l.revision++
	l.persistLocked()
}

// persistLocked writes the JSON array through the store and forces a Flush so
// a mutation survives the demo flow's deliberate process re-exec. Mutations
// are a-few-per-day; Flush cost is irrelevant.
func (l *List) persistLocked() {
	b, _ := json.Marshal(l.syms)
	l.st.SetConfig(configKey, string(b))
	l.st.Flush()
}
