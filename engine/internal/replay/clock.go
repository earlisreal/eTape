// Package replay is a feed.Feed + clock.Clock that reconstructs a recorded
// session from the store journal. It is an adapter (like feed/opend): it may
// import store and the domain packages, never the other way around.
package replay

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// Clock is a simulated clock driven by journal event timestamps. It wraps a
// clock.Fake (whose Advance fires due timers/tickers in chronological order)
// and exposes absolute-time stepping via AdvanceTo.
type Clock struct{ f *clock.Fake }

// NewClock returns a Clock frozen at start (the first event's timestamp).
func NewClock(start time.Time) *Clock { return &Clock{f: clock.NewFake(start)} }

func (c *Clock) Now() time.Time                         { return c.f.Now() }
func (c *Clock) After(d time.Duration) <-chan time.Time { return c.f.After(d) }
func (c *Clock) NewTicker(d time.Duration) clock.Ticker { return c.f.NewTicker(d) }

// AdvanceTo moves simulated time forward to t, firing any due timers/tickers.
// Forward-only: an earlier or equal t is a no-op (journal ts_exch can be flat
// or, for conn events, reuse a neighbor's recv time).
func (c *Clock) AdvanceTo(t time.Time) {
	if d := t.Sub(c.f.Now()); d > 0 {
		c.f.Advance(d)
	}
}

var _ clock.Clock = (*Clock)(nil)
