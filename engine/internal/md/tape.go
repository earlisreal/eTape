package md

import "github.com/earlisreal/eTape/engine/internal/feed"

// ring is a fixed-capacity tick ring. Appends overwrite the oldest entry;
// snapshot returns chronological order.
type ring struct {
	buf  []feed.Tick
	head int // next write position
	n    int // valid entries
}

func newRing(capacity int) *ring { return &ring{buf: make([]feed.Tick, capacity)} }

func (r *ring) append(t feed.Tick) {
	r.buf[r.head] = t
	r.head = (r.head + 1) % len(r.buf)
	if r.n < len(r.buf) {
		r.n++
	}
}

func (r *ring) snapshot() []feed.Tick {
	out := make([]feed.Tick, 0, r.n)
	start := (r.head - r.n + len(r.buf)) % len(r.buf)
	for i := 0; i < r.n; i++ {
		out = append(out, r.buf[(start+i)%len(r.buf)])
	}
	return out
}
