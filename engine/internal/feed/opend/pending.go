package opend

import "sync"

// pending correlates responses to in-flight requests by serial number.
// Every method removes its entry from the map under the lock before touching
// the channel, so failAll can never close a channel that resolve/cancel is
// about to use.
type pending struct {
	mu sync.Mutex
	m  map[uint32]chan Frame
}

func newPending() *pending { return &pending{m: make(map[uint32]chan Frame)} }

// register reserves a slot for serial and returns the (buffered) delivery channel.
func (p *pending) register(serial uint32) chan Frame {
	ch := make(chan Frame, 1)
	p.mu.Lock()
	p.m[serial] = ch
	p.mu.Unlock()
	return ch
}

// resolve delivers f to the waiter for serial. It returns false if no waiter is
// registered — the caller then treats f as a push.
func (p *pending) resolve(serial uint32, f Frame) bool {
	p.mu.Lock()
	ch, ok := p.m[serial]
	if ok {
		delete(p.m, serial)
	}
	p.mu.Unlock()
	if ok {
		ch <- f // cap-1 buffer: never blocks
	}
	return ok
}

// cancel drops a waiter (e.g. after a timeout). Idempotent.
func (p *pending) cancel(serial uint32) {
	p.mu.Lock()
	delete(p.m, serial)
	p.mu.Unlock()
}

// failAll closes every outstanding waiter — used on disconnect so blocked
// Request calls unblock and return ErrNotConnected.
func (p *pending) failAll() {
	p.mu.Lock()
	for s, ch := range p.m {
		close(ch)
		delete(p.m, s)
	}
	p.mu.Unlock()
}
