package opend

import (
	"sync"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// pending correlates responses to in-flight requests by serial number AND
// protoID. Every method removes its entry from the map under the lock before
// touching the channel, so failAll can never close a channel that
// resolve/cancel is about to use.
type pending struct {
	mu sync.Mutex
	m  map[uint32]waiter
}

type waiter struct {
	descriptor feed.SourceMessage
	protoID    uint32
	ch         chan Frame
}

func newPending() *pending { return &pending{m: make(map[uint32]waiter)} }

// register reserves a slot for serial and returns the (buffered) delivery channel.
func (p *pending) register(serial, protoID uint32) chan Frame {
	return p.registerSource(serial, protoID, feed.SourceMessage{})
}

func (p *pending) registerSource(serial, protoID uint32, descriptor feed.SourceMessage) chan Frame {
	ch := make(chan Frame, 1)
	p.mu.Lock()
	p.m[serial] = waiter{protoID: protoID, ch: ch, descriptor: descriptor}
	p.mu.Unlock()
	return ch
}

func (p *pending) descriptor(f Frame) feed.SourceMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	if w, ok := p.m[f.SerialNo]; ok && w.protoID == f.ProtoID {
		return w.descriptor
	}
	return feed.SourceMessage{Protocol: f.ProtoID, Origin: "unmatched", Matched: false}
}

// resolve delivers f to the waiter for f.SerialNo, but only when the waiter's
// protoID also matches. It returns false if no matching waiter is registered —
// the caller then treats f as a push.
func (p *pending) resolve(f Frame) bool {
	p.mu.Lock()
	w, ok := p.m[f.SerialNo]
	if ok && w.protoID == f.ProtoID {
		delete(p.m, f.SerialNo)
		p.mu.Unlock()
		w.ch <- f // cap-1 buffer: never blocks
		return true
	}
	p.mu.Unlock()
	return false
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
	for s, w := range p.m {
		close(w.ch)
		delete(p.m, s)
	}
	p.mu.Unlock()
}
