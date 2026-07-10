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
	"github.com/earlisreal/eTape/engine/internal/broker/netx"
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

// TestWS_PingTimeoutTriggersReconnect covers the liveness timer: Alpaca's
// trade_updates stream has no server ping/pong, so a connection that
// silently stops delivering frames — and stops reading entirely, so it can
// never auto-pong — is only detected by the pinger goroutine failing to get
// a pong back within pongTimeout. This is a distinct code path from
// TestWS_DeadConnectionReconnectsWithBackoff, which triggers via an abrupt
// close and produces an immediate read error rather than a ping timeout.
// The test shortens pingInterval/pongTimeout to keep this fast, and bounds
// its own wait so a regression that makes the client hang can't hang the
// test suite forever.
func TestWS_PingTimeoutTriggersReconnect(t *testing.T) {
	srv, dialCount := mockWSServer(func(ctx context.Context, c *websocket.Conn, dialN int) {
		_, _, _ = c.Read(ctx) // auth
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"authorization","data":{"status":"authorized"}}`))
		_, _, _ = c.Read(ctx) // listen
		if dialN == 1 {
			// Go silent past the client's shortened pong timeout instead of
			// sending anything — no close, no error frame, no more writes,
			// and (unlike TestWS_IdleButAlivePingsSucceed) no more reads
			// either, so the client's pings are never acknowledged: a
			// coder/websocket pong is only ever consumed by the peer's own
			// Read machinery, and this peer stops calling Read here.
			<-ctx.Done()
			return
		}
		// Second connection: prove the client reconnected and is talking
		// normally again, not just retrying the dial in a broken state.
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"trade_updates","data":{"event":"new","order":{"client_order_id":"ET-c","symbol":"AAPL","side":"buy","order_type":"limit","qty":"1","status":"new"}}}`))
		<-ctx.Done()
	})
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	var mu sync.Mutex
	var connEvents []bool
	ws := newWSClient(wsURL, "K", "S", clock.System{},
		func(tradeUpdate) {},
		func(up bool) { mu.Lock(); connEvents = append(connEvents, up); mu.Unlock() })
	ws.pingInterval = 50 * time.Millisecond
	ws.pongTimeout = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go ws.run(ctx)

	// Safety bound for this test's own wait: the run() backoff between
	// sessions starts at 1s (netx.Backoff{Min: time.Second}), so allow
	// generous headroom above pingInterval+pongTimeout + Min backoff without
	// letting a genuine hang stall the suite.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(dialCount) >= 2 {
			mu.Lock()
			evs := append([]bool(nil), connEvents...)
			mu.Unlock()
			sawDisconnect := false
			for _, up := range evs {
				if !up {
					sawDisconnect = true
					break
				}
			}
			if !sawDisconnect {
				t.Fatalf("expected onConn(false) after the ping timeout, got %v", evs)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("client did not reconnect after the ping timeout within the safety bound")
}

// TestWS_IdleButAlivePingsSucceed is the actual regression test for this
// task. Alpaca's trade_updates stream sends nothing at all whenever the
// account is idle (no order activity) — the normal case, not a failure —
// and before this fix, readFrame wrapped *every* Read in a fresh
// staleTimeout deadline, so any idle gap longer than that deadline
// reconnected the session regardless of whether the socket was actually
// healthy. Here the mock keeps its own Read loop running the whole time (so
// the library auto-pongs every ping it receives) but never writes a single
// data frame, for a window spanning many pingInterval cycles. A client that
// still judged liveness by "did Read get a frame recently" would reconnect
// repeatedly during this window; the fixed client, judging liveness by
// ping/pong instead, must not reconnect at all.
func TestWS_IdleButAlivePingsSucceed(t *testing.T) {
	srv, dialCount := mockWSServer(func(ctx context.Context, c *websocket.Conn, dialN int) {
		_, _, _ = c.Read(ctx) // auth
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"authorization","data":{"status":"authorized"}}`))
		_, _, _ = c.Read(ctx) // listen
		// Block on a single Read for the life of the connection: any ping
		// the client sends arrives while this call is pending and gets
		// auto-ponged by the library's read machinery internally (control
		// frames don't make Read return), but no data frame is ever written
		// back, so this call only returns when ctx ends. Note a *short*
		// per-call read deadline here would be self-defeating: coder/websocket
		// closes the connection on any error from any method, "context
		// expirations as well" included (see (*Conn).Read's doc comment) —
		// exactly the false-positive-staleness bug this task fixes on the
		// client side, so the mock must not reintroduce it on the server side.
		_, _, _ = c.Read(ctx)
	})
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	var mu sync.Mutex
	var connEvents []bool
	ws := newWSClient(wsURL, "K", "S", clock.System{},
		func(tradeUpdate) {},
		func(up bool) { mu.Lock(); connEvents = append(connEvents, up); mu.Unlock() })
	ws.pingInterval = 30 * time.Millisecond
	ws.pongTimeout = 200 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ws.run(ctx)

	// This window comfortably spans well over a dozen pingInterval cycles
	// of pure silence — old code's default 30s staleTimeout is gone, but
	// what matters is the shape: many idle cycles with zero data frames,
	// which is exactly what used to fire the old per-read deadline.
	time.Sleep(500 * time.Millisecond)

	if n := atomic.LoadInt32(dialCount); n != 1 {
		t.Fatalf("dial count = %d, want exactly 1 (idle-but-alive must not trigger a reconnect)", n)
	}
	mu.Lock()
	evs := append([]bool(nil), connEvents...)
	mu.Unlock()
	if len(evs) == 0 {
		t.Fatal("client never reported connected")
	}
	for _, up := range evs {
		if !up {
			t.Fatalf("expected no disconnect while idle-but-alive, got connEvents=%v", evs)
		}
	}
}

// TestResetBackoffIfHealthy_LongSessionResets is the backoff-reset
// regression test: a session that lasted at least bo.Max was a genuinely
// healthy, actively-pinged connection (not a connect-then-drop loop), so
// the next reconnect delay must go back to Min instead of continuing to
// climb. This exercises resetBackoffIfHealthy directly against the same
// netx.Backoff run() uses, rather than driving a real WS session end to end
// on a wall clock — asserting an exact reconnect delay via real timing
// would be flaky (scheduling noise is comparable to the delays involved),
// whereas netx.Backoff.Next() is deterministic immediately after Reset():
// cur starts at 0, so the first Next() call after a reset returns exactly
// Min with no jitter at all (see netx.Backoff.Next()'s span<=0 case).
func TestResetBackoffIfHealthy_LongSessionResets(t *testing.T) {
	bo := netx.Backoff{Min: 10 * time.Millisecond, Max: 100 * time.Millisecond}
	// Simulate a prior flapping period: two quick failures already climbed
	// the backoff away from Min.
	bo.Next()
	bo.Next()
	if got := bo.Next(); got == bo.Min {
		t.Fatalf("test setup: expected backoff to have climbed past Min before the reset check, got %v", got)
	}

	resetBackoffIfHealthy(&bo, 150*time.Millisecond) // >= Max: healthy long session

	if got := bo.Next(); got != bo.Min {
		t.Fatalf("post-reset backoff = %v, want exactly Min (%v)", got, bo.Min)
	}
}

// TestResetBackoffIfHealthy_ShortSessionKeepsClimbing proves the duration
// threshold is real: a session shorter than bo.Max (still flapping) must
// not reset, so backoff keeps climbing across a genuine reconnect storm
// instead of a single healthy blip masking it. If resetBackoffIfHealthy
// ignored the threshold and always reset, Next() here would return exactly
// bo.Min deterministically (100% of the time, per the same span<=0 case
// above) instead of a full-jittered value strictly inside (Min, 2*Min) —
// so the boundary checks below reliably catch an "always resets" bug rather
// than depending on a coincidental timing race.
func TestResetBackoffIfHealthy_ShortSessionKeepsClimbing(t *testing.T) {
	bo := netx.Backoff{Min: 10 * time.Millisecond, Max: 100 * time.Millisecond}
	bo.Next() // cur = Min

	resetBackoffIfHealthy(&bo, 5*time.Millisecond) // < Max: still flapping

	got := bo.Next()
	if got <= bo.Min {
		t.Fatalf("backoff after short (unhealthy) session = %v, want > Min (%v) -- looks reset when it should still be climbing", got, bo.Min)
	}
	if got >= 2*bo.Min {
		t.Fatalf("backoff after short session = %v, want < 2*Min (%v)", got, 2*bo.Min)
	}
}
