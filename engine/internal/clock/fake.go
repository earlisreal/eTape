package clock

import (
	"sort"
	"sync"
	"time"
)

// Fake is a deterministic Clock for tests: time moves only when Advance is
// called, and every due timer/ticker fires in chronological order during the
// Advance call. Channels have capacity 1, matching time.Ticker semantics —
// an unconsumed tick is dropped, never queued.
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	wakers []*fakeWaker
}

type fakeWaker struct {
	at       time.Time
	interval time.Duration // 0 = one-shot After
	ch       chan time.Time
	stopped  bool
}

// NewFake returns a Fake clock frozen at start.
func NewFake(start time.Time) *Fake { return &Fake{now: start} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := &fakeWaker{at: f.now.Add(d), ch: make(chan time.Time, 1)}
	f.wakers = append(f.wakers, w)
	return w.ch
}

func (f *Fake) NewTicker(d time.Duration) Ticker {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := &fakeWaker{at: f.now.Add(d), interval: d, ch: make(chan time.Time, 1)}
	f.wakers = append(f.wakers, w)
	return fakeTicker{f: f, w: w}
}

// Advance moves the clock forward by d, firing due wakers in time order.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	target := f.now.Add(d)
	for {
		var next *fakeWaker
		for _, w := range f.wakers {
			if w.stopped || w.at.After(target) {
				continue
			}
			if next == nil || w.at.Before(next.at) {
				next = w
			}
		}
		if next == nil {
			break
		}
		f.now = next.at
		select {
		case next.ch <- next.at:
		default: // undelivered tick: drop, like time.Ticker
		}
		if next.interval > 0 {
			next.at = next.at.Add(next.interval)
		} else {
			next.stopped = true
		}
	}
	f.now = target
	// Compact stopped one-shots so long tests don't accumulate garbage.
	live := f.wakers[:0]
	for _, w := range f.wakers {
		if !w.stopped {
			live = append(live, w)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].at.Before(live[j].at) })
	f.wakers = live
}

type fakeTicker struct {
	f *Fake
	w *fakeWaker
}

func (t fakeTicker) C() <-chan time.Time { return t.w.ch }
func (t fakeTicker) Stop() {
	t.f.mu.Lock()
	t.w.stopped = true
	t.f.mu.Unlock()
}
