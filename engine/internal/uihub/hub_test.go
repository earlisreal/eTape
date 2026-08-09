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
	"github.com/earlisreal/eTape/engine/internal/session"
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

// enqueue records the frame regardless of ck: outbound coalescing is the real
// *conn's outbox job (exercised in conn_test.go via a real conn + blockable
// socket), not the hub's -- the hub only decides the ck and hands identical
// bytes to every subscribed client, which is what these hub-level tests assert.
func (c *fakeClient) enqueue(b []byte, _ string) bool {
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
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 200, 200, 500, 500, 500)
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

func TestHubIndicatorReadyBroadcastsExactInstanceID(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	c := &fakeClient{nid: 1}
	h.Register(c)
	h.Subscribe(c, wsmsg.TopicSysEvents)
	syncHub(h)
	h.PublishMD(md.IndicatorReadyUpdate{Symbol: "US.AAPL", InstanceID: "panel-a:VWAP-0"})
	syncHub(h)

	for _, frame := range c.got() {
		var msg struct {
			Kind    string          `json:"kind"`
			Topic   string          `json:"topic"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(frame, &msg); err != nil || msg.Topic != string(wsmsg.TopicSysEvents) || msg.Kind != "delta" {
			continue
		}
		var event wsmsg.SysEvent
		if err := json.Unmarshal(msg.Payload, &event); err == nil && event.Kind == "indicator-ready" {
			if event.Detail != "panel-a:VWAP-0" {
				t.Fatalf("indicator-ready detail = %q, want exact instance ID", event.Detail)
			}
			return
		}
	}
	t.Fatal("subscribed sys.events consumer received no indicator-ready event")
}

func TestHubQueryChartWindowSkipBarsReturnsOnlyIndicators(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	for _, bucket := range []int64{1_000, 2_000, 3_000} {
		h.PublishMD(md.BarUpdate{Bar: md.Bar{
			Symbol: "US.AAPL", TF: session.TF1m, BucketMs: bucket, C: float64(bucket),
		}})
	}
	h.PublishMD(md.IndicatorUpdate{
		InstanceID: "panel-a:VWAP-0", SeriesKey: "panel-a:VWAP-0", Snapshot: true,
		Points: []md.Point{{TimeMs: 1_000, Value: 10}, {TimeMs: 2_000, Value: 11}, {TimeMs: 3_000, Value: 12}},
	})
	syncHub(h)

	args := wsmsg.QueryChartWindowArgs{
		Symbol: "US.AAPL", Timeframe: "1m", FromMs: 1_000, ToMs: 3_000,
		TailBars: 0, IndicatorSeriesKeys: []string{"panel-a:VWAP-0"}, SkipBars: true,
	}
	hydrated := h.QueryChartWindow(args)
	if len(hydrated.Bars) != 0 {
		t.Fatalf("skipBars result returned %d bars, want none", len(hydrated.Bars))
	}
	if hydrated.FromMs != args.FromMs || hydrated.ToMs != args.ToMs {
		t.Fatalf("skipBars range = [%d,%d), want [%d,%d)", hydrated.FromMs, hydrated.ToMs, args.FromMs, args.ToMs)
	}
	if len(hydrated.Indicators) != 1 || len(hydrated.Indicators[0].Points) != 2 {
		t.Fatalf("skipBars indicators = %+v, want one range-filtered series", hydrated.Indicators)
	}

	args.SkipBars = false
	withBars := h.QueryChartWindow(args)
	if len(withBars.Bars) != 2 {
		t.Fatalf("normal chart-window result returned %d bars, want 2", len(withBars.Bars))
	}
}

// TestHubBarSnapshotDoesNotLeakOntoBarsTopic verifies history snapshots remain
// command responses and never leak onto the live md.bars broadcast topic.
func TestHubBarSnapshotDoesNotLeakOntoBarsTopic(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	c := &fakeClient{nid: 1}
	h.Register(c)
	h.Subscribe(c, wsmsg.TopicBars)
	syncHub(h)
	before := len(c.got())

	const n = 500
	bars := make([]md.Bar, n)
	for i := range bars {
		bars[i] = md.Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: int64(i) * 60_000, C: float64(i)}
	}
	h.PublishMD(md.BarSnapshot{Symbol: "US.AAPL", TF: session.TF1m, Bars: bars})
	syncHub(h) // no mdTick.Advance: a coalesced delta would NOT appear yet

	after := c.got()
	if len(after) != before {
		t.Fatalf("history snapshot leaked onto md.bars topic: before=%d after=%d", before, len(after))
	}
}

// TestHubBarPrependDoesNotLeakOntoBarsTopic verifies older-history chunks
// remain command responses and never enter live broadcast staging.
func TestHubBarPrependDoesNotLeakOntoBarsTopic(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	c := &fakeClient{nid: 1}
	h.Register(c)
	h.Subscribe(c, wsmsg.TopicBars)
	syncHub(h)
	before := len(c.got())

	// The pan-triggered older-history chunk: bars strictly older than
	// anything already cached (mirrors SeedOlder1m's BarPrepend shape).
	older := []md.Bar{
		{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 1_000_000},
		{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 1_060_000},
	}
	h.PublishMD(md.BarPrepend{Symbol: "US.AAPL", TF: session.TF1m, Bars: older})
	syncHub(h) // no mdTick.Advance: a coalesced delta would NOT appear yet

	afterBatch := c.got()
	if len(afterBatch) != before {
		t.Fatalf("history prepend leaked onto md.bars topic: before=%d after=%d", before, len(afterBatch))
	}
	if len(h.pendKeep) != 0 {
		t.Fatalf("suppressed prepend entered pending broadcasts: %d", len(h.pendKeep))
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

// TestOutboundCoalesceKeyRouting verifies every topic routes to the correct
// outbound lane: latest-wins topics get a non-empty coalesce key at the right
// granularity (per-symbol/venue/session/single-slot), every event topic stays
// lossless (""), and a snapshot of ANY topic is always lossless.
func TestOutboundCoalesceKeyRouting(t *testing.T) {
	tests := []struct {
		name string
		s    staged
		snap bool
		want string
	}{
		{"quote delta -> per symbol", staged{Topic: wsmsg.TopicQuote, Payload: wsmsg.Quote{Symbol: "US.AAPL"}}, false, "d|q|US.AAPL"},
		{"book delta -> per symbol", staged{Topic: wsmsg.TopicBook, Payload: wsmsg.Book{Symbol: "US.AAPL"}}, false, "d|b|US.AAPL"},
		{"bar delta -> per symbol+tf+bucket", staged{Topic: wsmsg.TopicBars, Payload: wsmsg.Bar{Symbol: "US.AAPL", Timeframe: "1m", BucketStart: "T0"}}, false, "d|bar|US.AAPL|1m|T0"},
		{"account delta -> per venue", staged{Topic: wsmsg.TopicExecAccount, Payload: wsmsg.AccountRow{Venue: "alpaca"}}, false, "d|acct|alpaca"},
		{"positions delta -> single slot", staged{Topic: wsmsg.TopicExecPositions}, false, "d|exec.positions"},
		{"scanner.rank delta -> per session", staged{Topic: wsmsg.TopicScannerRank, Key: "sess1"}, false, "d|scanner.rank|sess1"},
		{"stock.detail delta -> per symbol", staged{Topic: wsmsg.TopicStockDetail, Key: "US.AAPL"}, false, "d|stock.detail|US.AAPL"},
		{"sys.health delta -> single slot", staged{Topic: wsmsg.TopicSysHealth}, false, "d|sys.health"},
		{"tape delta -> lossless", staged{Topic: wsmsg.TopicTape}, false, ""},
		{"orders delta -> lossless", staged{Topic: wsmsg.TopicExecOrders}, false, ""},
		{"fills delta -> lossless", staged{Topic: wsmsg.TopicExecFills}, false, ""},
		{"status delta -> lossless", staged{Topic: wsmsg.TopicExecStatus}, false, ""},
		{"sys.events delta -> lossless", staged{Topic: wsmsg.TopicSysEvents}, false, ""},
		{"news delta -> lossless", staged{Topic: wsmsg.TopicNews}, false, ""},
		{"scanner.hit delta -> lossless", staged{Topic: wsmsg.TopicScannerHit}, false, ""},
		{"config delta -> lossless", staged{Topic: wsmsg.TopicConfig}, false, ""},
		{"indicator delta -> lossless", staged{Topic: wsmsg.TopicIndicator}, false, ""},
		{"snapshot of a coalesceable topic -> lossless", staged{Topic: wsmsg.TopicQuote, Payload: wsmsg.Quote{Symbol: "US.AAPL"}}, true, ""},
		{"snapshot of scanner.rank -> lossless", staged{Topic: wsmsg.TopicScannerRank, Key: "sess1"}, true, ""},
		{"snapshot of stock.detail -> lossless", staged{Topic: wsmsg.TopicStockDetail, Key: "US.AAPL"}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := outboundCoalesceKey(tt.s, tt.snap); got != tt.want {
				t.Fatalf("outboundCoalesceKey(%s, snap=%v) = %q, want %q", tt.s.Topic, tt.snap, got, tt.want)
			}
		})
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
	if !strings.Contains(detail, "dropped UI client 1:") || !strings.Contains(detail, "overflow") {
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
	if !strings.Contains(detail, "dropped UI client 2:") || !strings.Contains(detail, "overflow") {
		t.Fatalf("expected detail to identify client 2 and an overflow reason, got %q", detail)
	}
}

// TestHubWriteTimeoutDropEmitsUIDropSysEvent is the end-to-end case for the
// write-timeout drop path: conn_test.go's TestConnWriteTimeoutClosesConn
// already pins that a wedged peer's write times out and tears the conn down,
// but only asserts the conn closes -- not that the Hub actually turns it into
// a ui-drop sys.events frame for survivors. Unlike the overflow paths above
// (broadcast/sendSnapshot call emitUIDrop directly, from Run's own
// goroutine), a write timeout is detected in the conn's own writeLoop
// goroutine and crosses into Run via ReportUIDrop -> dropCh -> handleDrop --
// a genuinely different path that needs its own coverage. This drives a real
// conn through an actual write timeout and asserts a survivor subscribed to
// sys.events receives the resulting frame.
func TestHubWriteTimeoutDropEmitsUIDropSysEvent(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	survivor := &fakeClient{nid: 2}
	h.Register(survivor)
	h.Subscribe(survivor, wsmsg.TopicSysEvents)
	syncHub(h)

	sock := newFakeSocket()
	sock.block = true // every Write blocks until its ctx is done -- a wedged peer
	wedged := newConn(1, sock, h, &fakeCmd{}, fakeQuery{}, 8, 20*time.Millisecond)
	h.Register(wedged)
	go wedged.run(ctx)

	if !wedged.enqueue([]byte(`{"kind":"ping","t":1}`), "") {
		t.Fatal("enqueue should succeed immediately; the queue itself isn't full")
	}

	waitFor(t, func() bool { return connDone(wedged) })
	syncHub(h) // barrier: ReportUIDrop's dropCh send is drained and handleDrop applied

	detail, ok := findUIDropDetail(t, survivor.got())
	if !ok {
		t.Fatal("expected a ui-drop sys.events delta to reach the surviving subscribed client")
	}
	if !strings.Contains(detail, "dropped UI client 1:") || !strings.Contains(detail, "write timeout") {
		t.Fatalf("expected detail to identify client 1 and a write-timeout reason, got %q", detail)
	}
}
