package uihub

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// wsSocket is the minimal surface over github.com/coder/websocket the conn needs
// (server.go adapts a *websocket.Conn to it; tests supply an in-memory fake).
type wsSocket interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, b []byte) error
	Close(code int, reason string) error
}

type commandHandler interface {
	handle(ctx context.Context, name string, args json.RawMessage, connID uint64, reply func(wsmsg.AckMsg)) (wsmsg.AckMsg, bool)
}

type queryHandler interface {
	handle(name string, args json.RawMessage) any
}

// qitem is one queued outbound frame. ck is its coalesce key: "" means the
// frame is lossless/ordered (an event or a snapshot -- never superseded), and a
// non-empty ck means the frame is a latest-wins delta whose newest value may
// overwrite an older one still queued under the same key (see outbox.enqueue).
type qitem struct {
	b  []byte
	ck string
}

// outbox is the conn's mutex-guarded outbound queue. It replaces the plain
// `chan []byte` with a topic-aware buffer so a slow client sheds stale
// latest-wins values (quotes/book/bars/account/positions/scanner.rank/health)
// by superseding them in place, while event/ordered frames stay a lossless
// FIFO bounded only by a hard cap that -- if ever exceeded -- still drops the
// connection (a genuinely pathological client). This is the root-cause fix for
// the "whole-app refresh" reconnect: normal busy-market backpressure now
// resolves by coalescing instead of dropping the socket.
//
// Concurrency: enqueue is called by many producers (the Hub's Run goroutine via
// broadcast/sendSnapshot, and this conn's own reader via enqueueJSON); pop is
// called by exactly one consumer (writeLoop, still the sole caller of ws.Write,
// so the single-writer discipline is unchanged). Every field is guarded by mu.
// notify is a buffered(1) wake channel: a successful enqueue does a
// non-blocking send so it never blocks a producer, and writeLoop drains fully
// on each wake, so a dropped (redundant) wake token can never strand a queued
// frame.
type outbox struct {
	mu     sync.Mutex
	q      []*qitem          // insertion-ordered; both lanes share one queue
	head   int               // consume index; q is compacted when fully drained
	slots  map[string]*qitem // coalesce key -> its currently-queued item
	events int               // count of live (queued, not-yet-popped) ck=="" items
	cap    int               // hard cap on the lossless lane (from ServerConfig.OutBuf)
	notify chan struct{}     // buffered(1); wakes writeLoop
	closed bool
}

func newOutbox(capHint int) *outbox {
	if capHint <= 0 {
		capHint = 1024
	}
	return &outbox{
		slots:  map[string]*qitem{},
		cap:    capHint,
		notify: make(chan struct{}, 1),
	}
}

// enqueue adds b to the outbox. It never blocks and does bounded work, so it is
// safe to call from the Hub's single Run goroutine. Returns false only when the
// outbox is closed, or when a lossless frame would exceed the hard cap (the one
// remaining overflow/drop condition -- the caller then drops the client and
// Task 1's ui-drop instrumentation records it).
func (o *outbox) enqueue(b []byte, ck string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return false
	}
	if ck != "" {
		if it := o.slots[ck]; it != nil {
			// Supersede in place: keep the key's original queue position (set by
			// its first enqueue) and just replace the payload with the newest
			// value. This is what preserves per-topic FIFO for coalesced keys --
			// a superseded value is never a second entry in q, only an
			// overwritten field on the one entry already there.
			it.b = b
			o.wake()
			return true
		}
		it := &qitem{b: b, ck: ck}
		o.q = append(o.q, it)
		o.slots[ck] = it
		o.wake()
		return true
	}
	// Lossless/ordered lane: bounded by the hard cap. This is the ONLY remaining
	// path that drops a connection under backpressure.
	if o.events >= o.cap {
		return false
	}
	o.q = append(o.q, &qitem{b: b})
	o.events++
	o.wake()
	return true
}

// wake signals writeLoop without ever blocking a producer: notify is
// buffered(1), so a send that finds a token already pending is simply dropped
// (that pending wake will make writeLoop drain everything queued so far
// anyway). Must be called with mu held.
func (o *outbox) wake() {
	select {
	case o.notify <- struct{}{}:
	default:
	}
}

// pop returns the next queued frame in insertion order, or ok=false when the
// outbox is fully drained. On drain it compacts q back to zero length (reusing
// the backing array) so a long-lived connection never grows q unboundedly via
// naked head advancement.
func (o *outbox) pop() ([]byte, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.head >= len(o.q) {
		o.q = o.q[:0]
		o.head = 0
		return nil, false
	}
	it := o.q[o.head]
	o.q[o.head] = nil // release the *qitem (and its []byte) for GC
	o.head++
	if it.ck == "" {
		o.events--
	} else if o.slots[it.ck] == it {
		// Only clear the slot if it still points at the item we just consumed.
		// Coalescing overwrites b in place at the same position, so this is
		// always the single live entry for the key -- the guard is cheap
		// insurance, not a correctness dependency.
		delete(o.slots, it.ck)
	}
	return it.b, true
}

// markClosed makes every later enqueue return false so a frame handed to the
// outbox after the conn is torn down can never leak into it forever.
func (o *outbox) markClosed() {
	o.mu.Lock()
	o.closed = true
	o.mu.Unlock()
}

type conn struct {
	nid  uint64
	ws   wsSocket
	hub  *Hub
	cmd  commandHandler
	qry  queryHandler
	out  *outbox
	once sync.Once
	done chan struct{}

	// writeTimeout bounds a single ws.Write call (see writeLoop). A peer that
	// can't accept even one already-queued frame within this window is wedged
	// (not just slow -- "slow" is what the outbox itself absorbs by coalescing),
	// so the write is aborted and the connection torn down instead of blocking
	// writeLoop, the connection's single writer goroutine, indefinitely.
	writeTimeout time.Duration
}

func newConn(id uint64, ws wsSocket, h *Hub, cmd commandHandler, q queryHandler, outBuf int, writeTimeout time.Duration) *conn {
	if writeTimeout <= 0 {
		writeTimeout = 5 * time.Second
	}
	return &conn{
		nid: id, ws: ws, hub: h, cmd: cmd, qry: q,
		out: newOutbox(outBuf), done: make(chan struct{}),
		writeTimeout: writeTimeout,
	}
}

func (c *conn) id() uint64 { return c.nid }

// enqueue is called by the hub loop (broadcast/snapshot) AND by this conn's own
// reader (ack/result/pong). Non-blocking. ck is the outbound coalesce key ("" =>
// lossless/ordered; non-empty => latest-wins). On a false return (lossless lane
// over the hard cap, or an already-closed outbox) it tears the conn down so the
// hub drops it; close() is idempotent, so the hub calling close() again on its
// own drop path is harmless.
func (c *conn) enqueue(b []byte, ck string) bool {
	if c.out.enqueue(b, ck) {
		return true
	}
	c.close()
	return false
}

func (c *conn) close() {
	c.once.Do(func() {
		// Close the outbox first so any enqueue racing this teardown (e.g. a
		// hub broadcast in flight) returns false instead of buffering a frame
		// nobody will ever pop.
		c.out.markClosed()
		close(c.done)
		// ws.Close performs a graceful close handshake that can block for
		// several seconds on an unresponsive peer (it needs the same read
		// lock our own blocked reader may be holding). Run it off the
		// caller's goroutine so a client we're forcibly dropping (e.g. via
		// the Hub's overflow-drop path, which calls close() synchronously
		// from Hub.Run's single event-loop goroutine) never stalls the Hub.
		go func() { _ = c.ws.Close(1000, "closing") }()
	})
}

// run starts the writer and reader; returns when either ends. Callers Register
// the conn with the hub before calling run (so snapshots can be delivered).
func (c *conn) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); c.writeLoop(ctx) }()
	c.readLoop(ctx) // blocks
	c.close()
	c.hub.Unregister(c) // clean up hub-side subscription state
	cancel()
	wg.Wait()
}

func (c *conn) writeLoop(ctx context.Context) {
	for {
		// Drain everything currently queued before sleeping again. notify is
		// buffered(1), so a burst enqueued while we were writing the previous
		// frame is covered by a single wake -- popping until empty is what makes
		// that correct (one-frame-per-wake would strand the rest).
		for {
			b, ok := c.out.pop()
			if !ok {
				break
			}
			wctx, cancel := context.WithTimeout(ctx, c.writeTimeout)
			err := c.ws.Write(wctx, b)
			cancel()
			if err != nil {
				// Distinguish "this write's own deadline elapsed" (wedged
				// peer -- ctx derives from the parent, so errors.Is only
				// matches DeadlineExceeded when writeTimeout, not the parent
				// ctx's cancellation/shutdown, is what actually fired) from
				// every other write error (e.g. the peer closed normally):
				// only the former is the "outbound path failed" case Task 1
				// makes visible via sys.events -- an ordinary disconnect
				// isn't a diagnosis-relevant drop.
				if errors.Is(err, context.DeadlineExceeded) {
					c.hub.ReportUIDrop(c.nid, "write timeout")
				}
				c.close()
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-c.out.notify:
		}
	}
}

func (c *conn) readLoop(ctx context.Context) {
	for {
		b, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		c.dispatch(ctx, b)
		select {
		case <-c.done:
			return
		default:
		}
	}
}

func (c *conn) dispatch(ctx context.Context, b []byte) {
	var head struct {
		Kind   string          `json:"kind"`
		Topic  wsmsg.Topic     `json:"topic"`
		CorrID string          `json:"corrId"`
		Name   string          `json:"name"`
		Args   json.RawMessage `json:"args"`
		T      int64           `json:"t"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return // drop malformed frames silently (matches the UI codec's drop-and-count)
	}
	switch head.Kind {
	case "subscribe":
		if wsmsg.AllTopics[head.Topic] {
			c.hub.Subscribe(c, head.Topic)
		}
	case "unsubscribe":
		if wsmsg.AllTopics[head.Topic] {
			c.hub.Unsubscribe(c, head.Topic)
		}
	case "command":
		send := func(ack wsmsg.AckMsg) {
			ack.Kind = "ack"
			ack.CorrID = head.CorrID
			c.enqueueJSON(ack)
		}
		ack, deferred := c.cmd.handle(ctx, head.Name, head.Args, c.nid, send)
		if !deferred {
			send(ack)
		}
	case "query":
		payload := c.qry.handle(head.Name, head.Args)
		c.enqueueJSON(wsmsg.ResultMsg{Kind: "result", CorrID: head.CorrID, Payload: payload})
	case "ping":
		c.enqueueJSON(wsmsg.PongMsg{Kind: "pong", T: head.T})
	default:
		// unknown kind: ignore
	}
}

func (c *conn) enqueueJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.enqueue(b, "") // ack/result/pong replies are lossless/ordered
}
