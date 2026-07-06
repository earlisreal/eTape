package uihub

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type fakeClient struct {
	mu     sync.Mutex
	nid    uint64
	frames [][]byte
	full   bool
	closed bool
}

func (c *fakeClient) id() uint64 { return c.nid }
func (c *fakeClient) enqueue(b []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.full {
		return false
	}
	c.frames = append(c.frames, append([]byte(nil), b...))
	return true
}
func (c *fakeClient) close() { c.mu.Lock(); c.closed = true; c.mu.Unlock() }
func (c *fakeClient) got() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.frames...)
}

func decodeKindTopic(t *testing.T, b []byte) (kind string, topic string) {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	k, _ := m["kind"].(string)
	tp, _ := m["topic"].(string)
	return k, tp
}

func newTestHub(clk clock.Clock) *Hub {
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 200, 200, 500, 500)
	return NewHub(clk, HubConfig{
		MDInterval: 33 * time.Millisecond, AccountInterval: 250 * time.Millisecond,
		PositionInterval: 100 * time.Millisecond, Buf: 64,
	}, m)
}

// syncHub is a test-only barrier: it blocks until the hub's Run loop has
// drained and processed every message sent before this call, letting tests
// assert on hub-side effects deterministically instead of sleeping.
func syncHub(h *Hub) { h.sync() }

func TestHubSubscribeSendsSnapshotThenCoalescedDelta(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	c := &fakeClient{nid: 1}
	h.Register(c)
	// seed a quote before subscribe so the snapshot is non-empty
	h.PublishMD(md.QuoteUpdate{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.47, TsMs: 1}})
	syncHub(h) // barrier: ensure the publish was applied (see helper note)
	h.Subscribe(c, wsmsg.TopicQuote)
	syncHub(h)

	// snapshot should have arrived (kind:snapshot, topic md.quote)
	frames := c.got()
	if len(frames) == 0 {
		t.Fatal("expected a snapshot frame after subscribe")
	}
	k, tp := decodeKindTopic(t, frames[0])
	if k != "snapshot" || tp != "md.quote" {
		t.Fatalf("first frame should be md.quote snapshot, got %s/%s", k, tp)
	}

	// a new quote should NOT broadcast until the md ticker fires (keep-latest coalescing)
	h.PublishMD(md.QuoteUpdate{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.50, TsMs: 2}})
	syncHub(h)
	before := len(c.got())
	clk.Advance(33 * time.Millisecond) // fire md ticker
	syncHub(h)
	after := c.got()
	if len(after) <= before {
		t.Fatalf("expected a coalesced delta after md tick; before=%d after=%d", before, len(after))
	}
	k, tp = decodeKindTopic(t, after[len(after)-1])
	if k != "delta" || tp != "md.quote" {
		t.Fatalf("last frame should be md.quote delta, got %s/%s", k, tp)
	}
}

func TestHubExecOrdersBroadcastImmediately(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	c := &fakeClient{nid: 1}
	h.Register(c)
	h.Subscribe(c, wsmsg.TopicExecOrders)
	syncHub(h)
	base := len(c.got())
	h.PublishExec(exec.OrderUpdate{Order: exec.Order{Venue: "sim", ID: "ET1", Symbol: "US.AAPL", Status: exec.StatusSubmitted}})
	syncHub(h)
	// event-driven: no ticker advance needed
	frames := c.got()
	if len(frames) <= base {
		t.Fatalf("exec.orders must broadcast immediately, got %d frames", len(frames))
	}
	k, tp := decodeKindTopic(t, frames[len(frames)-1])
	if k != "delta" || tp != "exec.orders" {
		t.Fatalf("expected exec.orders delta, got %s/%s", k, tp)
	}
}

func TestHubOverflowClosesClient(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	c := &fakeClient{nid: 1, full: true} // every enqueue fails
	h.Register(c)
	h.Subscribe(c, wsmsg.TopicExecOrders)
	syncHub(h)
	h.PublishExec(exec.OrderUpdate{Order: exec.Order{Venue: "sim", ID: "ET1", Status: exec.StatusSubmitted}})
	syncHub(h)
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		t.Fatal("a client whose queue is always full must be closed and dropped")
	}
}
