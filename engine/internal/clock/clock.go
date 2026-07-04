// Package clock abstracts wall-clock time behind an interface so time-dependent
// components (keepalive tickers, request timeouts, pollers, coalescing, replay)
// are deterministic under test. It is deliberately dependency-free so every
// domain and adapter package can import it without cycles.
//
// Spec note: go-engine-design lists Clock under feed/; it is hoisted here to its
// own package so time-dependent packages need not import the feed event types.
package clock

import "time"

// Ticker abstracts *time.Ticker.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Clock abstracts the parts of the time package the engine uses.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	NewTicker(d time.Duration) Ticker
}

// System is the real-time Clock.
type System struct{}

func (System) Now() time.Time                         { return time.Now() }
func (System) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (System) NewTicker(d time.Duration) Ticker       { return sysTicker{time.NewTicker(d)} }

type sysTicker struct{ t *time.Ticker }

func (s sysTicker) C() <-chan time.Time { return s.t.C }
func (s sysTicker) Stop()               { s.t.Stop() }
