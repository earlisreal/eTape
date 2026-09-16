package opend

import (
	"context"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// requestGate paces request start times without reserving future slots while a
// caller is waiting. It therefore survives reconnects and a canceled waiter
// cannot delay the next live request.
type requestGate struct {
	mu      sync.Mutex
	clk     clock.Clock
	spacing time.Duration
	next    time.Time
}

func (g *requestGate) wait(ctx context.Context) error {
	for {
		if err := g.waitUntil(ctx); err != nil {
			return err
		}
		if g.take(g.clk.Now()) {
			return nil
		}
	}
}

// waitUntil waits for a currently available slot without reserving it. The
// caller must take the slot immediately before the operation it is pacing.
func (g *requestGate) waitUntil(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		now := g.clk.Now()
		g.mu.Lock()
		at := g.next
		g.mu.Unlock()
		if at.IsZero() || !now.Before(at) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-g.clk.After(at.Sub(now)):
		}
	}
}

// take reserves the next slot at now. It is intentionally separate from
// waitUntil so callers can acquire their send lock first without holding it
// while they wait.
func (g *requestGate) take(now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.next.IsZero() && now.Before(g.next) {
		return false
	}
	g.next = now.Add(g.spacing)
	return true
}
