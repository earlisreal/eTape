package opend

import (
	"sync"
	"testing"
)

func TestSerialGenMonotonic(t *testing.T) {
	var g serialGen
	a, b, c := g.next(), g.next(), g.next()
	if a != 1 || b != 2 || c != 3 {
		t.Fatalf("serials = %d,%d,%d want 1,2,3", a, b, c)
	}
}

func TestPendingResolveDeliversToWaiter(t *testing.T) {
	p := newPending()
	ch := p.register(7, 1001)
	if ok := p.resolve(Frame{ProtoID: 1001, SerialNo: 7}); !ok {
		t.Fatal("resolve returned false for a registered serial with matching protoID")
	}
	f := <-ch
	if f.SerialNo != 7 {
		t.Fatalf("delivered serial = %d, want 7", f.SerialNo)
	}
}

func TestPendingResolveUnknownIsPush(t *testing.T) {
	p := newPending()
	if ok := p.resolve(Frame{ProtoID: 1001, SerialNo: 99}); ok {
		t.Fatal("resolve of unregistered serial returned true (should be treated as push)")
	}
}

func TestPendingResolveMismatchedProtoIDIsPush(t *testing.T) {
	p := newPending()
	_ = p.register(7, 1004) // register with protoID 1004
	// Try to resolve with a different protoID (3011)
	if ok := p.resolve(Frame{ProtoID: 3011, SerialNo: 7}); ok {
		t.Fatal("resolve of mismatched protoID returned true (should be treated as push)")
	}
}

func TestPendingCancelThenResolveIsPush(t *testing.T) {
	p := newPending()
	_ = p.register(5, 1001)
	p.cancel(5)
	if ok := p.resolve(Frame{ProtoID: 1001, SerialNo: 5}); ok {
		t.Fatal("resolve after cancel returned true")
	}
	p.cancel(5) // idempotent, must not panic
}

func TestPendingFailAllClosesWaiters(t *testing.T) {
	p := newPending()
	ch := p.register(3, 1001)
	p.failAll()
	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed after failAll")
	}
}

func TestPendingConcurrentResolveCancel(t *testing.T) {
	// -race must stay clean under concurrent register/resolve/cancel/failAll.
	p := newPending()
	var wg sync.WaitGroup
	for i := uint32(1); i <= 200; i++ {
		wg.Add(1)
		go func(s uint32) {
			defer wg.Done()
			ch := p.register(s, 1001)
			go p.resolve(Frame{SerialNo: s, ProtoID: 1001})
			select {
			case <-ch:
			default:
			}
			p.cancel(s)
		}(i)
	}
	wg.Wait()
	p.failAll()
}
