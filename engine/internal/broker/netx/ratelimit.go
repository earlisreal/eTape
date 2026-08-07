package netx

import (
	"context"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// TokenBucket is a clock-injected token bucket. Allow() is non-blocking;
// Take(ctx) blocks (via clk.After) until a token frees or ctx ends.
type TokenBucket struct {
	clk    clock.Clock
	rate   float64 // tokens per second
	burst  float64
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func NewTokenBucket(clk clock.Clock, ratePerSec float64, burst int) *TokenBucket {
	return &TokenBucket{clk: clk, rate: ratePerSec, burst: float64(burst), tokens: float64(burst), last: clk.Now()}
}

func (tb *TokenBucket) refillLocked() {
	now := tb.clk.Now()
	if elapsed := now.Sub(tb.last).Seconds(); elapsed > 0 {
		tb.tokens += elapsed * tb.rate
		if tb.tokens > tb.burst {
			tb.tokens = tb.burst
		}
		tb.last = now
	}
}

func (tb *TokenBucket) Allow() bool {
	return tb.AllowWithReserve(0)
}

// AllowWithReserve consumes one token only when reserve whole tokens would
// remain. It is for low-priority callers that must never drain the capacity
// needed by a higher-priority operation; unlike Take, it never waits.
func (tb *TokenBucket) AllowWithReserve(reserve int) bool {
	if reserve < 0 {
		reserve = 0
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refillLocked()
	if tb.tokens >= float64(reserve+1) {
		tb.tokens--
		return true
	}
	return false
}

// Refund returns one token after a caller admits a request from this bucket
// but cannot complete a later admission step. It is for compound, non-blocking
// reservations only; normal callers should use Allow or Take.
func (tb *TokenBucket) Refund() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refillLocked()
	if tb.tokens < tb.burst {
		tb.tokens++
		if tb.tokens > tb.burst {
			tb.tokens = tb.burst
		}
	}
}

// waitLocked returns how long until the next whole token; 0 if one is ready.
func (tb *TokenBucket) waitLocked() time.Duration {
	tb.refillLocked()
	if tb.tokens >= 1 {
		return 0
	}
	need := 1 - tb.tokens
	return time.Duration(need / tb.rate * float64(time.Second))
}

func (tb *TokenBucket) Take(ctx context.Context) error {
	for {
		tb.mu.Lock()
		wait := tb.waitLocked()
		if wait == 0 {
			tb.tokens--
			tb.mu.Unlock()
			return nil
		}
		tb.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tb.clk.After(wait):
		}
	}
}
