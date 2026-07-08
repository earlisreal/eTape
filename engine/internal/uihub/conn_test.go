package uihub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// fakeSocket is an in-memory wsSocket: reads pop from `in`, writes append to `out`.
type fakeSocket struct {
	in     chan []byte
	mu     sync.Mutex
	out    [][]byte
	closed bool
	block  bool // when true, Write blocks until its ctx is done (simulates a wedged peer)
}

func newFakeSocket() *fakeSocket { return &fakeSocket{in: make(chan []byte, 16)} }
func (s *fakeSocket) Read(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case b, ok := <-s.in:
		if !ok {
			return nil, errors.New("closed")
		}
		return b, nil
	}
}
func (s *fakeSocket) Write(ctx context.Context, b []byte) error {
	s.mu.Lock()
	block := s.block
	s.mu.Unlock()
	if block {
		<-ctx.Done() // never accepts the frame -- writeLoop's per-write timeout must fire
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out = append(s.out, append([]byte(nil), b...))
	return nil
}
func (s *fakeSocket) Close(code int, reason string) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}
func (s *fakeSocket) writes() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.out...)
}

type fakeCmd struct{ last string }

func (f *fakeCmd) handle(_ context.Context, name string, _ json.RawMessage, _ uint64) wsmsg.AckMsg {
	f.last = name
	return wsmsg.AckMsg{Kind: "ack", Status: "accepted", OrderID: "ET9"}
}

type fakeQuery struct{}

func (fakeQuery) handle(_ string, _ json.RawMessage) any { return []wsmsg.Fill{} }

func TestConnPingPong(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newFakeSocket()
	c := newConn(1, sock, h, &fakeCmd{}, fakeQuery{}, 8, time.Second)
	go c.run(ctx)

	sock.in <- []byte(`{"kind":"ping","t":123}`)
	waitFor(t, func() bool {
		for _, w := range sock.writes() {
			var m map[string]any
			_ = json.Unmarshal(w, &m)
			if m["kind"] == "pong" && m["t"] == float64(123) {
				return true
			}
		}
		return false
	})
}

func TestConnCommandProducesAck(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newFakeSocket()
	cmd := &fakeCmd{}
	c := newConn(1, sock, h, cmd, fakeQuery{}, 8, time.Second)
	go c.run(ctx)

	sock.in <- []byte(`{"kind":"command","corrId":"c1","name":"SubmitOrder","args":{}}`)
	waitFor(t, func() bool {
		for _, w := range sock.writes() {
			var m map[string]any
			_ = json.Unmarshal(w, &m)
			if m["kind"] == "ack" && m["corrId"] == "c1" && m["status"] == "accepted" && m["orderId"] == "ET9" {
				return true
			}
		}
		return false
	})
	if cmd.last != "SubmitOrder" {
		t.Fatalf("command not dispatched: %q", cmd.last)
	}
}

func TestConnSubscribeRoutesToHub(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10)
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, m)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newFakeSocket()
	c := newConn(1, sock, h, &fakeCmd{}, fakeQuery{}, 8, time.Second)
	h.Register(c)
	go c.run(ctx)
	sock.in <- []byte(`{"kind":"subscribe","topic":"exec.status"}`)
	h.sync()
	h.sync() // second barrier: subscribe processed after the reader forwards it
	// exec.status snapshot is always available (assembled aggregate) => a frame should be written
	waitFor(t, func() bool {
		for _, w := range sock.writes() {
			var mm map[string]any
			_ = json.Unmarshal(w, &mm)
			if mm["kind"] == "snapshot" && mm["topic"] == "exec.status" {
				return true
			}
		}
		return false
	})
}

// TestConnWriteTimeoutClosesConn is the RED case for Task 1's write-deadline
// requirement: a socket that never accepts a write (genuinely wedged, not
// just slow) must not block writeLoop forever -- the per-write timeout must
// fire and tear the connection down via the existing c.close() path.
func TestConnWriteTimeoutClosesConn(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newFakeSocket()
	sock.block = true // every Write blocks until its ctx is done
	c := newConn(1, sock, h, &fakeCmd{}, fakeQuery{}, 8, 20*time.Millisecond)
	h.Register(c)
	go c.run(ctx)

	if !c.enqueue([]byte(`{"kind":"ping","t":1}`), "") {
		t.Fatal("enqueue should succeed immediately; the queue itself isn't full")
	}

	waitFor(t, func() bool {
		select {
		case <-c.done:
			return true
		default:
			return false
		}
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

// gatedSocket is a wsSocket whose Write blocks until the test opens the gate.
// Holding the gate keeps writeLoop parked mid-write so frames enqueued after it
// pile up (and coalesce) in the outbox; opening the gate lets them drain so the
// test can observe exactly what got written. `entered` receives a token each
// time a Write begins waiting on the gate, letting a test await "the writer is
// now parked" deterministically instead of sleeping.
type gatedSocket struct {
	mu      sync.Mutex
	out     [][]byte
	closed  bool
	release chan struct{}
	entered chan struct{}
}

func newGatedSocket() *gatedSocket {
	return &gatedSocket{release: make(chan struct{}), entered: make(chan struct{}, 4096)}
}

func (s *gatedSocket) Read(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *gatedSocket) Write(ctx context.Context, b []byte) error {
	select {
	case <-s.release:
		// gate already open: record immediately
	default:
		select {
		case s.entered <- struct{}{}:
		default:
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out = append(s.out, append([]byte(nil), b...))
	return nil
}

func (s *gatedSocket) Close(int, string) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *gatedSocket) openGate() { close(s.release) }

func (s *gatedSocket) writes() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.out...)
}

func connDone(c *conn) bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// TestConnOutboxCoalescesWhileBlocked is the core root-cause-fix case: while the
// writer is blocked on a slow peer, a flood of latest-wins deltas for one key
// must coalesce to a single frame (the newest value) instead of overflowing and
// dropping the connection, while every lossless frame is still delivered in
// order. Exercises real concurrent enqueue (this goroutine) vs. pop/writeLoop
// (the writer goroutine) through a blockable socket -- not a call sequence.
func TestConnOutboxCoalescesWhileBlocked(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newGatedSocket()
	c := newConn(1, sock, h, &fakeCmd{}, fakeQuery{}, 1024, 5*time.Second)
	go c.writeLoop(ctx)

	// Park the writer inside a gated Write on a lossless primer so nothing is
	// popped until the gate opens -- making the coalescing below deterministic.
	if !c.enqueue([]byte("L1"), "") {
		t.Fatal("primer enqueue should succeed")
	}
	<-sock.entered // writer is now blocked in Write("L1")

	const quoteKey = "d|q|US.AAPL"
	for i := 1; i <= 100; i++ {
		if !c.enqueue([]byte(fmt.Sprintf("Q%d", i)), quoteKey) {
			t.Fatalf("quote enqueue %d should succeed (coalesced items never count toward the cap)", i)
		}
		if i < 100 {
			if !c.enqueue([]byte(fmt.Sprintf("L%d", i+1)), "") {
				t.Fatalf("lossless enqueue L%d should succeed", i+1)
			}
		}
	}

	sock.openGate()

	// 100 lossless + exactly 1 coalesced quote = 101 frames.
	waitFor(t, func() bool { return len(sock.writes()) == 101 })

	var quotes, lossless []string
	for _, f := range sock.writes() {
		if bytes.HasPrefix(f, []byte("Q")) {
			quotes = append(quotes, string(f))
		} else {
			lossless = append(lossless, string(f))
		}
	}
	if len(quotes) != 1 || quotes[0] != "Q100" {
		t.Fatalf("expected exactly one quote frame (the last enqueued, Q100), got %v", quotes)
	}
	want := make([]string, 100)
	for i := range want {
		want[i] = fmt.Sprintf("L%d", i+1)
	}
	if !reflect.DeepEqual(lossless, want) {
		t.Fatalf("lossless frames out of order or missing:\n got %v\nwant %v", lossless, want)
	}
	if connDone(c) {
		t.Fatal("conn must not be closed -- coalescing absorbs the backpressure")
	}
}

// TestConnOutboxHardCapOverflowDropsConn proves the one remaining drop path:
// coalesceable deltas never count toward the cap (a slow client sheds them
// forever), but the lossless/ordered lane is bounded, and the first lossless
// frame past the cap returns false and tears the conn down. (The hub turns that
// false into a ui-drop sys.events frame; see hub_test.go's Task 1 coverage.)
func TestConnOutboxHardCapOverflowDropsConn(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	const capN = 4
	sock := newGatedSocket()
	c := newConn(1, sock, h, &fakeCmd{}, fakeQuery{}, capN, 5*time.Second)
	go c.writeLoop(ctx)

	// Park the writer so nothing drains, then fill the lossless lane to exactly
	// cap.
	if !c.enqueue([]byte("primer"), "") {
		t.Fatal("primer enqueue should succeed")
	}
	<-sock.entered
	for i := 0; i < capN; i++ {
		if !c.enqueue([]byte(fmt.Sprintf("L%d", i)), "") {
			t.Fatalf("lossless enqueue %d within cap should succeed", i)
		}
	}
	// Coalesceable deltas do NOT count toward the lossless cap: they must all
	// succeed even with the lossless lane full.
	for i := 0; i < 20; i++ {
		if !c.enqueue([]byte("Q"), "d|q|US.AAPL") {
			t.Fatalf("coalesceable delta %d must not be capped", i)
		}
	}
	// The first lossless frame past the cap overflows -> false -> conn dropped.
	if c.enqueue([]byte("Lover"), "") {
		t.Fatal("enqueue past the hard cap must return false")
	}
	waitFor(t, func() bool { return connDone(c) })

	// Once closed, every enqueue returns false (a late frame can't leak in).
	if c.enqueue([]byte("Q"), "d|q|US.AAPL") {
		t.Fatal("enqueue after close must return false")
	}
}

// TestConnOutboxSnapshotPrecedesLaterDeltas pins the snapshot-before-delta
// invariant: a snapshot (always lossless, ck "") enqueued before later
// coalesceable deltas for the same topic/key must still be written before any
// of them, so the client seeds its store before applying deltas onto it.
func TestConnOutboxSnapshotPrecedesLaterDeltas(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newGatedSocket()
	c := newConn(1, sock, h, &fakeCmd{}, fakeQuery{}, 1024, 5*time.Second)
	go c.writeLoop(ctx)

	if !c.enqueue([]byte("primer"), "") {
		t.Fatal("primer enqueue should succeed")
	}
	<-sock.entered
	if !c.enqueue([]byte("SNAP"), "") { // a snapshot frame: always lossless
		t.Fatal("snapshot enqueue should succeed")
	}
	for i := 1; i <= 50; i++ {
		if !c.enqueue([]byte(fmt.Sprintf("D%d", i)), "d|q|US.AAPL") {
			t.Fatalf("delta enqueue %d should succeed", i)
		}
	}
	sock.openGate()

	// primer, SNAP, and the single coalesced delta (D50) = 3 frames.
	waitFor(t, func() bool { return len(sock.writes()) == 3 })

	snapIdx, deltaIdx := -1, -1
	for i, f := range sock.writes() {
		switch {
		case bytes.Equal(f, []byte("SNAP")):
			snapIdx = i
		case bytes.HasPrefix(f, []byte("D")):
			deltaIdx = i
		}
	}
	if snapIdx == -1 || deltaIdx == -1 {
		t.Fatalf("expected both a SNAP and a delta frame, got %v", sock.writes())
	}
	if snapIdx >= deltaIdx {
		t.Fatalf("snapshot must precede the delta; got snap@%d delta@%d", snapIdx, deltaIdx)
	}
}

func drainOutbox(o *outbox) []string {
	var got []string
	for {
		b, ok := o.pop()
		if !ok {
			return got
		}
		got = append(got, string(b))
	}
}

// TestOutboxCoalesceKeepsPositionAndLatest is a focused unit test of the outbox
// invariant: superseding a key overwrites its value in place at its ORIGINAL
// queue position (not the back), and yields the newest value on drain.
func TestOutboxCoalesceKeepsPositionAndLatest(t *testing.T) {
	o := newOutbox(1024)
	o.enqueue([]byte("A1"), "a")
	o.enqueue([]byte("B1"), "b")
	o.enqueue([]byte("A2"), "a") // supersede A in place -- keeps position 0
	if got, want := drainOutbox(o), []string{"A2", "B1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestOutboxCompactsWhenDrained proves q is reset (not grown unboundedly via
// naked head advancement) once fully drained, and is reusable afterward.
func TestOutboxCompactsWhenDrained(t *testing.T) {
	o := newOutbox(1024)
	for i := 0; i < 5; i++ {
		if !o.enqueue([]byte("x"), "") {
			t.Fatalf("enqueue %d should succeed", i)
		}
	}
	for i := 0; i < 5; i++ {
		if _, ok := o.pop(); !ok {
			t.Fatalf("expected item %d", i)
		}
	}
	if _, ok := o.pop(); ok {
		t.Fatal("expected drained outbox")
	}
	o.mu.Lock()
	l, head, events := len(o.q), o.head, o.events
	o.mu.Unlock()
	if l != 0 || head != 0 || events != 0 {
		t.Fatalf("outbox not compacted after drain: len=%d head=%d events=%d", l, head, events)
	}
	if !o.enqueue([]byte("y"), "") {
		t.Fatal("enqueue after compaction should succeed")
	}
	if got := drainOutbox(o); !reflect.DeepEqual(got, []string{"y"}) {
		t.Fatalf("post-compaction drain got %v, want [y]", got)
	}
}
