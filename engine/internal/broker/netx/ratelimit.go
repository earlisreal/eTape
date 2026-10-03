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
	clk          clock.Clock
	rate         float64 // tokens per second
	baseRate     float64
	burst        float64
	mu           sync.Mutex
	tokens       float64
	last         time.Time
	limitedUntil time.Time
	blockedUntil time.Time
}

func NewTokenBucket(clk clock.Clock, ratePerSec float64, burst int) *TokenBucket {
	return &TokenBucket{clk: clk, rate: ratePerSec, baseRate: ratePerSec, burst: float64(burst), tokens: float64(burst), last: clk.Now()}
}

func (tb *TokenBucket) refillLocked() {
	now := tb.clk.Now()
	if !tb.limitedUntil.IsZero() && !now.Before(tb.limitedUntil) {
		tb.rate = tb.baseRate
		tb.limitedUntil = time.Time{}
	}
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
	wait := time.Duration(0)
	if tb.blockedUntil.After(tb.clk.Now()) {
		wait = tb.blockedUntil.Sub(tb.clk.Now())
	}
	if tb.tokens >= 1 {
		return wait
	}
	need := 1 - tb.tokens
	refill := time.Duration(need / tb.rate * float64(time.Second))
	if refill > wait {
		return refill
	}
	return wait
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

// TakeWithReserve admits background work only when reserve whole tokens remain
// for foreground requests. The history client uses it for Scanner REL VOL.
func (tb *TokenBucket) TakeWithReserve(ctx context.Context, reserve int) error {
	if reserve < 0 {
		reserve = 0
	}
	for {
		tb.mu.Lock()
		tb.refillLocked()
		wait := time.Duration(0)
		if tb.blockedUntil.After(tb.clk.Now()) {
			wait = tb.blockedUntil.Sub(tb.clk.Now())
		}
		need := float64(reserve+1) - tb.tokens
		if need <= 0 && wait == 0 {
			tb.tokens--
			tb.mu.Unlock()
			return nil
		}
		if need > 0 {
			refill := time.Duration(need / tb.rate * float64(time.Second))
			if refill > wait {
				wait = refill
			}
		}
		tb.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tb.clk.After(wait):
		}
	}
}

// ObserveBudget applies a lower provider-reported allowance until reset.
func (tb *TokenBucket) ObserveBudget(remaining int, resetAt time.Time) {
	now := tb.clk.Now()
	if remaining < 0 || !resetAt.After(now) {
		return
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refillLocked()
	if remaining == 0 {
		if resetAt.After(tb.blockedUntil) {
			tb.blockedUntil = resetAt
		}
		return
	}
	rate := float64(remaining) / resetAt.Sub(now).Seconds() * 0.9
	if rate <= 0 || rate >= tb.rate {
		return
	}
	if !tb.limitedUntil.IsZero() && tb.limitedUntil.After(resetAt) {
		resetAt = tb.limitedUntil
	}
	tb.rate = rate
	tb.limitedUntil = resetAt
}

// DeferUntil pauses new sends through a provider reset or Retry-After time.
func (tb *TokenBucket) DeferUntil(until time.Time) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if until.After(tb.blockedUntil) {
		tb.blockedUntil = until
	}
}
