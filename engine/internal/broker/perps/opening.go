package perps

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

const OpeningSnapshotMaxAge = 15 * time.Second

// OpeningCache holds signed, account-specific settings for recently viewed or
// armed contracts. Its local read never waits for an exchange response.
type OpeningCache struct {
	mu      sync.Mutex
	entries map[string]openingSnapshot
}

type openingSnapshot struct {
	terms   exec.PerpetualOpeningTerms
	err     error
	at      time.Time
	loading bool
}

func (c *OpeningCache) Current(symbol string, now time.Time) (exec.PerpetualOpeningTerms, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.entries[symbol]
	if s.err != nil {
		return exec.PerpetualOpeningTerms{}, s.err
	}
	if age := now.Sub(s.at); age < 0 || age >= OpeningSnapshotMaxAge {
		return exec.PerpetualOpeningTerms{}, errors.New("open the linked chart and wait for fresh perpetual opening terms")
	}
	return s.terms, nil
}

func (c *OpeningCache) Refresh(ctx context.Context, symbol string, now time.Time, load func(context.Context, string) (exec.PerpetualOpeningTerms, error)) {
	c.mu.Lock()
	if s, ok := c.entries[symbol]; ok && (s.loading || now.Sub(s.at) < 5*time.Second) {
		c.mu.Unlock()
		return
	}
	if c.entries == nil {
		c.entries = make(map[string]openingSnapshot)
	}
	for key, s := range c.entries {
		if !s.loading && now.Sub(s.at) >= time.Minute {
			delete(c.entries, key)
		}
	}
	previous := c.entries[symbol]
	previous.loading = true
	c.entries[symbol] = previous
	c.mu.Unlock()
	terms, err := load(ctx, symbol)
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		previous.loading = false
		c.entries[symbol] = previous
		return
	}
	c.entries[symbol] = openingSnapshot{terms: terms, err: err, at: now}
}
