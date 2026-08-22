package wailsruntime

import (
	"context"
	"errors"
	"sync"
)

var ErrStopping = errors.New("wails runtime is stopping")

// Gate is the application-owned admission boundary shared by bindings and
// Stream handlers. Wails cancellation is a work signal; this gate is the
// shutdown ordering signal.
type Gate struct {
	mu       sync.Mutex
	stopping bool
	inFlight int
	wait     sync.WaitGroup
}

func NewGate() *Gate { return &Gate{} }

func (g *Gate) Enter(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	g.mu.Lock()
	if g.stopping {
		g.mu.Unlock()
		return nil, ErrStopping
	}
	if err := ctx.Err(); err != nil {
		g.mu.Unlock()
		return nil, err
	}
	g.inFlight++
	g.wait.Add(1)
	g.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.inFlight--
			g.mu.Unlock()
			g.wait.Done()
		})
	}, nil
}

func (g *Gate) BeginStop() {
	g.mu.Lock()
	g.stopping = true
	g.mu.Unlock()
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
