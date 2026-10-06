package netx

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/atomicfile"
	"github.com/earlisreal/eTape/engine/internal/clock"
)

// RestartCooldown preserves the last limited request before sending it. A new
// process waits only for the previous request window's remainder; unknown state
// retains the full conservative cooldown. It does not track other API clients.
type RestartCooldown struct {
	path, scope string
	clk         clock.Clock
	readyAt     time.Time
	mu          sync.Mutex
	record      cooldownRecord
}

type cooldownRecord struct {
	Scope       string    `json:"scope"`
	LastAttempt time.Time `json:"lastAttempt"`
	NotBefore   time.Time `json:"notBefore"`
}

func NewRestartCooldown(path, scope string, window time.Duration, clk clock.Clock) *RestartCooldown {
	now := clk.Now()
	c := &RestartCooldown{path: path, scope: scope, clk: clk, readyAt: now.Add(window)}
	data, err := os.ReadFile(path)
	var record cooldownRecord
	if err == nil && json.Unmarshal(data, &record) == nil && record.Scope == scope &&
		!record.LastAttempt.IsZero() && !record.LastAttempt.After(now) {
		c.readyAt = record.LastAttempt.Add(window)
		if record.NotBefore.After(c.readyAt) {
			c.readyAt = record.NotBefore
		}
		c.record = record
	}
	return c
}

func (c *RestartCooldown) ReadyAt() time.Time { return c.readyAt }

// Record must succeed before a limited provider request is sent. Atomic,
// synchronous persistence also covers crashes and ambiguous request failures.
func (c *RestartCooldown) Record() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.record.Scope = c.scope
	c.record.LastAttempt = c.clk.Now()
	return c.writeLocked()
}

// DeferUntil preserves an observed provider reset or Retry-After across restart.
func (c *RestartCooldown) DeferUntil(until time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !until.After(c.record.NotBefore) {
		return nil
	}
	c.record.NotBefore = until
	return c.writeLocked()
}

func (c *RestartCooldown) writeLocked() error {
	data, err := json.Marshal(c.record)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(c.path, data, 0o600); err != nil {
		return fmt.Errorf("record provider request cooldown: %w", err)
	}
	return nil
}
