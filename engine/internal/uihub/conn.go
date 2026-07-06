package uihub

import (
	"context"
	"encoding/json"
	"sync"

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
	handle(name string, args json.RawMessage) wsmsg.AckMsg
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
}

func newConn(id uint64, ws wsSocket, h *Hub, cmd commandHandler, q queryHandler, outBuf int) *conn {
	if outBuf <= 0 {
		outBuf = 1024
	}
	return &conn{nid: id, ws: ws, hub: h, cmd: cmd, qry: q, out: make(chan []byte, outBuf), done: make(chan struct{})}
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
		_ = c.ws.Close(1000, "closing")
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
			if err := c.ws.Write(ctx, b); err != nil {
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
		ack := c.cmd.handle(head.Name, head.Args)
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
	_ = ctx
}

func (c *conn) enqueueJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.enqueue(b)
}
