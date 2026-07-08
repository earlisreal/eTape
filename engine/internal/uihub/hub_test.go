package uihub

import (
	"context"
	"encoding/json"
	"strings"
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

// TestHubSyncBarrierOrdering reproduces the sync-ordering gap from review
// finding 1: PublishMD/PublishExec/Publish enqueue into buffered channels and
// return immediately, so if sync()'s barrier isn't airtight, Run's select
// could service the (also-ready) syncCh case before draining an
// already-queued publish, and a caller's post-sync() assertion would flake.
// Repeated under -race -count=N this pins the fix (drain-before-close) in
// place; without it this test is expected to flake under load.
func TestHubSyncBarrierOrdering(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	c := &fakeClient{nid: 1}
	h.Register(c)
	h.Subscribe(c, wsmsg.TopicExecOrders)
	syncHub(h)

	for i := 0; i < 200; i++ {
		base := len(c.got())
		h.PublishExec(exec.OrderUpdate{Order: exec.Order{
			Venue: "sim", ID: "ETsync", Symbol: "US.AAPL", Status: exec.StatusSubmitted,
		}})
		// No sleep, no yield: sync() must itself guarantee the publish above
		// has already been applied by the time it returns.
		syncHub(h)
		frames := c.got()
		if len(frames) <= base {
			t.Fatalf("iteration %d: publish immediately before sync() must be visible after it returns; base=%d got=%d", i, base, len(frames))
		}
	}
}

func TestHubPublicMethodsReturnPromptlyAfterShutdown(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = h.Run(ctx) }()

	c := &fakeClient{nid: 1}
	h.Register(c)
	h.Subscribe(c, wsmsg.TopicExecOrders)
	syncHub(h)

	cancel() // trigger Run's ctx.Done() path, which returns without servicing other channels
	// give Run a moment to actually observe ctx.Done() and close h.closed
	<-h.closed

	calls := map[string]func(){
		"Register":    func() { h.Register(&fakeClient{nid: 2}) },
		"Unregister":  func() { h.Unregister(c) },
		"Subscribe":   func() { h.Subscribe(c, wsmsg.TopicQuote) },
		"Unsubscribe": func() { h.Unsubscribe(c, wsmsg.TopicExecOrders) },
		"PublishMD":   func() { h.PublishMD(md.QuoteUpdate{Quote: feed.Quote{Symbol: "US.AAPL", Last: 1, TsMs: 1}}) },
		"PublishExec": func() {
			h.PublishExec(exec.OrderUpdate{Order: exec.Order{Venue: "sim", ID: "ETshutdown", Status: exec.StatusSubmitted}})
		},
		"Publish": func() { h.Publish(wsmsg.TopicQuote, "US.AAPL", map[string]any{}) },
		"sync":    func() { h.sync() },
	}

	for name, call := range calls {
		done := make(chan struct{})
		go func() {
			call()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s did not return within 1s after shutdown; goroutine leaked/blocked", name)
		}
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

// findUIDropDetail scans frames for a sys.events entry whose Kind is
// "ui-drop", returning its Detail string (and ok=true) if found. It checks
// both shapes a sys.events frame can take: a live delta (single SysEvent
// payload, emitUIDrop's own delivery) and a snapshot taken after the fact
// (payload is the accumulated []SysEvent, mirror.snapshotFrames' shape) --
// either is proof the event was recorded and is visible to this client.
func findUIDropDetail(t *testing.T, frames [][]byte) (string, bool) {
	t.Helper()
	for _, fr := range frames {
		var m struct {
			Kind    string          `json:"kind"`
			Topic   string          `json:"topic"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(fr, &m); err != nil || m.Topic != "sys.events" {
			continue
		}
		switch m.Kind {
		case "delta":
			var e wsmsg.SysEvent
			if err := json.Unmarshal(m.Payload, &e); err == nil && e.Kind == "ui-drop" {
				return e.Detail, true
			}
		case "snapshot":
			var events []wsmsg.SysEvent
			if err := json.Unmarshal(m.Payload, &events); err == nil {
				for _, e := range events {
					if e.Kind == "ui-drop" {
						return e.Detail, true
					}
				}
			}
		}
	}
	return "", false
}

// TestHubBroadcastOverflowEmitsUIDropSysEvent is the RED case for Task 1's
// broadcast() drop path: when enqueue fails for a subscribed client inside
// broadcast, the hub must still close+drop it (unchanged behavior) AND emit a
// ui-drop sys.events frame that reaches every other client subscribed to
// sys.events -- so a live session can confirm drops are happening without
// needing to reproduce a full-app reconnect first.
func TestHubBroadcastOverflowEmitsUIDropSysEvent(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	// dead starts healthy so its subscribe-triggered snapshot (exec.orders
	// always has one, even if empty) succeeds -- only flip it to always-fail
	// afterward, so the drop this test is isolating is unambiguously
	// broadcast()'s overflow path, not sendSnapshot()'s (covered separately
	// by TestHubSendSnapshotOverflowEmitsUIDropSysEvent).
	dead := &fakeClient{nid: 1}
	survivor := &fakeClient{nid: 2}
	h.Register(dead)
	h.Register(survivor)
	h.Subscribe(dead, wsmsg.TopicExecOrders)
	h.Subscribe(survivor, wsmsg.TopicSysEvents)
	syncHub(h)

	dead.mu.Lock()
	dead.full = true // every enqueue fails from now on
	dead.mu.Unlock()

	h.PublishExec(exec.OrderUpdate{Order: exec.Order{Venue: "sim", ID: "ET1", Status: exec.StatusSubmitted}})
	syncHub(h)

	dead.mu.Lock()
	closed := dead.closed
	dead.mu.Unlock()
	if !closed {
		t.Fatal("overflowing client must still be closed and dropped (unchanged behavior)")
	}

	detail, ok := findUIDropDetail(t, survivor.got())
	if !ok {
		t.Fatal("expected a ui-drop sys.events delta to reach the surviving subscribed client")
	}
	if !strings.Contains(detail, "1") || !strings.Contains(detail, "overflow") {
		t.Fatalf("expected detail to identify client 1 and an overflow reason, got %q", detail)
	}
}

// TestHubSendSnapshotOverflowEmitsUIDropSysEvent is the RED case for Task 1's
// sendSnapshot() drop path -- distinct code from broadcast(): a client whose
// very first snapshot frame can't be enqueued must be dropped (unchanged
// behavior) and also produce a ui-drop sys.events frame for survivors.
func TestHubSendSnapshotOverflowEmitsUIDropSysEvent(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	survivor := &fakeClient{nid: 1}
	h.Register(survivor)
	h.Subscribe(survivor, wsmsg.TopicSysEvents)
	syncHub(h)

	dead := &fakeClient{nid: 2, full: true} // every enqueue fails, including the snapshot frame
	h.Register(dead)
	syncHub(h)
	h.Subscribe(dead, wsmsg.TopicExecStatus) // exec.status always has an assembled snapshot
	syncHub(h)

	dead.mu.Lock()
	closed := dead.closed
	dead.mu.Unlock()
	if !closed {
		t.Fatal("a client whose snapshot enqueue fails must be closed and dropped (unchanged behavior)")
	}

	detail, ok := findUIDropDetail(t, survivor.got())
	if !ok {
		t.Fatal("expected a ui-drop sys.events delta to reach the surviving subscribed client")
	}
	if !strings.Contains(detail, "2") || !strings.Contains(detail, "overflow") {
		t.Fatalf("expected detail to identify client 2 and an overflow reason, got %q", detail)
	}
}
