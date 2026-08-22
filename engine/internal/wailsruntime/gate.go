package wailsruntime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var ErrStopping = errors.New("wails runtime is stopping")

// Gate is the application-owned admission boundary shared by bindings and
// Stream handlers. Wails cancellation is a work signal; this gate is the
// shutdown ordering signal.
type Gate struct {
	mu       sync.Mutex
	stopping bool
	inFlight int
	nextID   uint64
	active   map[uint64]context.CancelFunc
	wait     sync.WaitGroup
}

func NewGate() *Gate { return &Gate{active: make(map[uint64]context.CancelFunc)} }

func (g *Gate) Enter(ctx context.Context) (func(), error) {
	_, release, err := g.EnterContext(ctx)
	return release, err
}

// EnterContext admits work and returns a child context canceled when the gate
// begins stopping. Callers that do long work should use the returned context.
func (g *Gate) EnterContext(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	workCtx, cancel := context.WithCancel(ctx)

	g.mu.Lock()
	if g.stopping {
		g.mu.Unlock()
		cancel()
		return nil, nil, ErrStopping
	}
	if err := ctx.Err(); err != nil {
		g.mu.Unlock()
		cancel()
		return nil, nil, err
	}
	id := g.nextID
	g.nextID++
	g.inFlight++
	g.active[id] = cancel
	g.wait.Add(1)
	g.mu.Unlock()

	var once atomic.Bool
	release := func() {
		if !once.CompareAndSwap(false, true) {
			return
		}
		g.mu.Lock()
		delete(g.active, id)
		g.inFlight--
		g.mu.Unlock()
		cancel()
		g.wait.Done()
	}
	return workCtx, release, nil
}

func (g *Gate) BeginStop() {
	g.mu.Lock()
	if g.stopping {
		g.mu.Unlock()
		return
	}
	g.stopping = true
	cancellers := make([]context.CancelFunc, 0, len(g.active))
	for _, cancel := range g.active {
		cancellers = append(cancellers, cancel)
	}
	g.mu.Unlock()
	for _, cancel := range cancellers {
		cancel()
	}
}

func (g *Gate) Stop(ctx context.Context) error {
	g.BeginStop()
	if ctx == nil {
		ctx = context.Background()
	}

	done := make(chan struct{})
	go func() {
		g.wait.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *Gate) InFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inFlight
}

func (g *Gate) Stopping() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopping
}
