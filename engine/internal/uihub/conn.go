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
	handle(ctx context.Context, name string, args json.RawMessage, connID uint64) wsmsg.AckMsg
}

type queryHandler interface {
	handle(name string, args json.RawMessage) any
}

type conn struct {
	nid  uint64
	ws   wsSocket
	hub  *Hub
	cmd  commandHandler
	qry  queryHandler
	out  chan []byte
	once sync.Once
	done chan struct{}

	// writeTimeout bounds a single ws.Write call (see writeLoop). A peer that
	// can't accept even one already-queued frame within this window is wedged
	// (not just slow -- "slow" is what the outbound queue itself absorbs), so
	// the write is aborted and the connection torn down instead of blocking
	// writeLoop, the connection's single writer goroutine, indefinitely.
	writeTimeout time.Duration
}

func newConn(id uint64, ws wsSocket, h *Hub, cmd commandHandler, q queryHandler, outBuf int, writeTimeout time.Duration) *conn {
	if outBuf <= 0 {
		outBuf = 1024
	}
	if writeTimeout <= 0 {
		writeTimeout = 5 * time.Second
	}
	return &conn{
		nid: id, ws: ws, hub: h, cmd: cmd, qry: q,
		out: make(chan []byte, outBuf), done: make(chan struct{}),
		writeTimeout: writeTimeout,
	}
}

func (c *conn) id() uint64 { return c.nid }

// enqueue is called by the hub loop (broadcast/snapshot) AND by this conn's own
// reader (ack/result/pong). Non-blocking: on a full queue it tears the conn down
// and returns false so the hub drops it.
func (c *conn) enqueue(b []byte) bool {
	select {
	case c.out <- b:
		return true
	case <-c.done:
		return false
	default:
		c.close()
		return false
	}
}

func (c *conn) close() {
	c.once.Do(func() {
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
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case b := <-c.out:
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
		ack := c.cmd.handle(ctx, head.Name, head.Args, c.nid)
		ack.Kind = "ack"
		ack.CorrID = head.CorrID
		c.enqueueJSON(ack)
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
	c.enqueue(b)
}
