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
	st   configStore
	mu   sync.Mutex
	syms []string // insertion order; authoritative payload order
	cap  int
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

// Add normalizes and appends symbol, returning added=false for a duplicate
// (harmless no-op) and ErrFull past the cap. Persists + Flushes on a real add.
func (l *List) Add(symbol string) (bool, error) {
	sym := Normalize(symbol)
	if sym == "" {
		return false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.syms {
		if s == sym {
			return false, nil
		}
	}
	if len(l.syms) >= l.cap {
		return false, ErrFull
	}
	l.syms = append(l.syms, sym)
	l.persistLocked()
	return true, nil
}

// Remove deletes symbol if present (idempotent); persists on a real removal.
func (l *List) Remove(symbol string) bool {
	sym := Normalize(symbol)
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, s := range l.syms {
		if s == sym {
			l.syms = append(l.syms[:i], l.syms[i+1:]...)
			l.persistLocked()
			return true
		}
	}
	return false
}

// Symbols returns a copy in insertion order.
func (l *List) Symbols() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.syms))
	copy(out, l.syms)
	return out
}

// Seed replaces the whole list (demo boot: trusted synth universe, no probe).
func (l *List) Seed(symbols []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syms = l.syms[:0]
	for _, s := range symbols {
		l.syms = append(l.syms, Normalize(s))
	}
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
