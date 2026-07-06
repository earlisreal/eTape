package alpaca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/clock"
)

// TestWS_DecodesBinaryFramePayload proves the client decodes a trade_updates
// payload regardless of WS opcode: Alpaca's paper endpoint sends BINARY
// frames, live sends TEXT frames, but the payload is JSON either way. A
// client that only handled one opcode would silently miss every frame on
// whichever environment sends the other one.
func TestWS_DecodesBinaryFramePayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		_, _, _ = c.Read(ctx) // auth
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"authorization","data":{"status":"authorized"}}`))
		_, _, _ = c.Read(ctx) // listen
		// paper sends BINARY frames; payload is still JSON.
		_ = c.Write(ctx, websocket.MessageBinary, []byte(`{"stream":"trade_updates","data":{"event":"new","order":{"client_order_id":"ET-z","symbol":"AAPL","side":"buy","order_type":"limit","qty":"1","status":"new"}}}`))
		<-ctx.Done()
	}))
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	var mu sync.Mutex
	var got []tradeUpdate
	ws := newWSClient(wsURL, "K", "S", clock.System{}, func(tu tradeUpdate) { mu.Lock(); got = append(got, tu); mu.Unlock() }, func(bool) {})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ws.run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 1 {
			if got[0].Event != "new" {
				t.Fatalf("event = %q", got[0].Event)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("binary-frame trade update not decoded")
}

// TestWS_DecodesTextFramePayload is the mirror of the binary test: Alpaca's
// live endpoint sends TEXT frames. Both opcodes must decode identically —
// this is the counterpart proof that the client isn't just accidentally
// binary-only (or text-only) but genuinely opcode-agnostic.
func TestWS_DecodesTextFramePayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		_, _, _ = c.Read(ctx) // auth
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"authorization","data":{"status":"authorized"}}`))
		_, _, _ = c.Read(ctx) // listen
		// live sends TEXT frames.
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"trade_updates","data":{"event":"fill","execution_id":"exec-1","price":"190.48","qty":"1","position_qty":"1","order":{"client_order_id":"ET-y","symbol":"MSFT","side":"buy","order_type":"market","qty":"1","filled_qty":"1","filled_avg_price":"190.48","status":"filled"}}}`))
		<-ctx.Done()
	}))
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	var mu sync.Mutex
	var got []tradeUpdate
	ws := newWSClient(wsURL, "K", "S", clock.System{}, func(tu tradeUpdate) { mu.Lock(); got = append(got, tu); mu.Unlock() }, func(bool) {})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ws.run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 1 {
			if got[0].Event != "fill" || got[0].Order.Symbol != "MSFT" {
				t.Fatalf("update = %+v", got[0])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("text-frame trade update not decoded")
}

// mockWSServer builds an httptest.Server whose per-connection behavior is
// driven by handle, and returns a dial counter incremented once per accepted
// WS connection. Tests use the counter to assert reconnect behavior directly
// rather than inferring it from timing alone.
func mockWSServer(handle func(ctx context.Context, c *websocket.Conn, dialN int)) (*httptest.Server, *int32) {
	var dialCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		n := atomic.AddInt32(&dialCount, 1)
		handle(r.Context(), c, int(n))
	}))
	return srv, &dialCount
}

// TestWS_HandshakeSequence covers the auth -> listen handshake order itself:
// the server only advances past auth once it has read a decodable auth frame,
// and only advances past listen once it has read a decodable listen frame
// naming the trade_updates stream. A client that skipped or reordered a step
// would stall the mock server's reads and this test would time out.
func TestWS_HandshakeSequence(t *testing.T) {
	type frame struct {
		Action string         `json:"action"`
		Data   map[string]any `json:"data"`
	}

	srv, _ := mockWSServer(func(ctx context.Context, c *websocket.Conn, _ int) {
		_, authRaw, err := c.Read(ctx)
		if err != nil {
			return
		}
		var af frame
		if err := json.Unmarshal(authRaw, &af); err != nil || af.Action != "auth" {
			t.Errorf("expected action=auth first, got %s", authRaw)
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"authorization","data":{"status":"authorized"}}`))

		_, listenRaw, err := c.Read(ctx)
		if err != nil {
			return
		}
		var lf frame
		if err := json.Unmarshal(listenRaw, &lf); err != nil || lf.Action != "listen" {
			t.Errorf("expected action=listen second, got %s", listenRaw)
			return
		}
		streams, _ := lf.Data["streams"].([]any)
		if len(streams) != 1 || streams[0] != "trade_updates" {
			t.Errorf("expected listen streams=[trade_updates], got %v", lf.Data)
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"listening","data":{"streams":["trade_updates"]}}`))
		<-ctx.Done()
	})
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	var connMu sync.Mutex
	var connUp bool
	ws := newWSClient(wsURL, "K", "S", clock.System{}, func(tradeUpdate) {}, func(up bool) {
		connMu.Lock()
		connUp = up
		connMu.Unlock()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ws.run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		connMu.Lock()
		up := connUp
		connMu.Unlock()
		if up {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("client never completed the auth->listen handshake (onConn(true) not observed)")
}

// TestWS_ErrorFrameTriggersReconnect proves an error frame during the
// trade_updates stream (Alpaca sends {"stream":"error",...} or
// {"action":"error",...} on a mid-session problem) tears the connection down
// and reconnects, rather than hanging the read loop forever waiting on a dead
// stream.
func TestWS_ErrorFrameTriggersReconnect(t *testing.T) {
	srv, dialCount := mockWSServer(func(ctx context.Context, c *websocket.Conn, dialN int) {
		_, _, _ = c.Read(ctx) // auth
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"authorization","data":{"status":"authorized"}}`))
		_, _, _ = c.Read(ctx) // listen
		if dialN == 1 {
			_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"error","data":{"msg":"connection limit exceeded","code":406}}`))
			<-ctx.Done()
			return
		}
		// Second connection: prove the client reconnected and is talking
		// normally again, not just retrying the dial in a broken state.
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"trade_updates","data":{"event":"new","order":{"client_order_id":"ET-a","symbol":"AAPL","side":"buy","order_type":"limit","qty":"1","status":"new"}}}`))
		<-ctx.Done()
	})
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	var mu sync.Mutex
	var got []tradeUpdate
	var connEvents []bool
	ws := newWSClient(wsURL, "K", "S", clock.System{},
		func(tu tradeUpdate) { mu.Lock(); got = append(got, tu); mu.Unlock() },
		func(up bool) { mu.Lock(); connEvents = append(connEvents, up); mu.Unlock() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go ws.run(ctx)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		evs := append([]bool(nil), connEvents...)
		mu.Unlock()
		if n == 1 {
			if atomic.LoadInt32(dialCount) < 2 {
				t.Fatalf("dial count = %d, want >= 2 (error frame must trigger a reconnect)", atomic.LoadInt32(dialCount))
			}
			sawDisconnect := false
			for _, up := range evs {
				if !up {
					sawDisconnect = true
					break
				}
			}
			if !sawDisconnect {
				t.Fatalf("expected onConn(false) after the error frame, got %v", evs)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("client did not reconnect after the error frame within the safety bound")
}

// TestWS_DeadConnectionReconnectsWithBackoff proves the client reconnects
// (using netx.Backoff, Task 5) after the underlying connection dies outright
// (server closes without warning) rather than hanging run(ctx) forever.
func TestWS_DeadConnectionReconnectsWithBackoff(t *testing.T) {
	srv, dialCount := mockWSServer(func(ctx context.Context, c *websocket.Conn, dialN int) {
		_, _, _ = c.Read(ctx) // auth
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"authorization","data":{"status":"authorized"}}`))
		_, _, _ = c.Read(ctx) // listen
		if dialN == 1 {
			// Close abruptly instead of responding further: simulates a dead
			// connection with no explicit error frame at all.
			_ = c.Close(websocket.StatusAbnormalClosure, "simulated drop")
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"trade_updates","data":{"event":"new","order":{"client_order_id":"ET-b","symbol":"TSLA","side":"buy","order_type":"limit","qty":"1","status":"new"}}}`))
		<-ctx.Done()
	})
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	var mu sync.Mutex
	var got []tradeUpdate
	ws := newWSClient(wsURL, "K", "S", clock.System{},
		func(tu tradeUpdate) { mu.Lock(); got = append(got, tu); mu.Unlock() }, func(bool) {})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go ws.run(ctx)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 1 && atomic.LoadInt32(dialCount) >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("client did not reconnect after the dead connection within the safety bound")
}
