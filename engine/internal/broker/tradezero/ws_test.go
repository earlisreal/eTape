package tradezero

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

// mockTZ serves the 3-step handshake then pushes one order update.
func mockTZ(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`))
		_, auth, _ := c.Read(ctx) // {"key":..,"secret":..}
		if !json.Valid(auth) {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`))
		_, _, _ = c.Read(ctx) // subscribe payload
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"action":"update","userOrderId":"2TZ00001:ETx","symbol":"AAPL","orderStatus":"New","orderQuantity":10,"executed":0,"orderType":"Limit","side":"Buy","openClose":"Open"}`))
		<-ctx.Done()
	}))
}

func TestWS_HandshakeAndOrderDispatch(t *testing.T) {
	srv := mockTZ(t)
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):] // http->ws

	var mu sync.Mutex
	var got []tzOrder
	ws := newWSClient(wsURL, "2TZ00001", "K", "S", clock.System{},
		func(o tzOrder) { mu.Lock(); got = append(got, o); mu.Unlock() },
		func(tzPosition) {}, func(bool) {})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ws.run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 1 {
			if got[0].Symbol != "AAPL" {
				t.Fatalf("order symbol = %q", got[0].Symbol)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("did not receive the order update within timeout")
}

// mockTZServer builds an httptest.Server whose per-connection behavior is
// driven by handle, and returns a pointer to a dial counter incremented once
// per accepted WS connection. Tests use the counter to assert reconnect (or
// no-reconnect) behavior directly, instead of inferring it from timing.
func mockTZServer(handle func(ctx context.Context, c *websocket.Conn, dialN int)) (*httptest.Server, *int32) {
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

// TestWS_FailedAuthNeverReconnects covers the named safety requirement: a
// FAILED_AUTH response must permanently stop run(ctx), never triggering a
// second dial. Retrying with the same bad key/secret would just fail forever
// and burn the backoff loop (or, worse, risk account lockout on a real
// broker that penalizes repeated bad-auth attempts).
func TestWS_FailedAuthNeverReconnects(t *testing.T) {
	srv, dialCount := mockTZServer(func(ctx context.Context, c *websocket.Conn, _ int) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`))
		_, auth, _ := c.Read(ctx) // {"key":..,"secret":..}
		if !json.Valid(auth) {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"FAILED_AUTH"}`))
		<-ctx.Done()
	})
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	ws := newWSClient(wsURL, "2TZ00001", "K", "S", clock.System{},
		func(tzOrder) {}, func(tzPosition) {}, func(bool) {})

	done := make(chan struct{})
	go func() {
		ws.run(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("run(ctx) did not return after FAILED_AUTH")
	}

	// Give any errant reconnect attempt a moment to land before checking —
	// run() has already returned, but this guards against a regression that
	// spawns a stray dial from elsewhere.
	time.Sleep(200 * time.Millisecond)
	if n := atomic.LoadInt32(dialCount); n != 1 {
		t.Fatalf("dial count = %d, want 1 (must never reconnect after FAILED_AUTH)", n)
	}
}

// TestWS_TerminatedResubscribesSameConnection covers the non-fatal status
// path: TERMINATED (and, by the same code path, INVALID_DATA) must not tear
// down the socket. The client is expected to resend the subscribe payload
// and keep reading on the SAME connection.
func TestWS_TerminatedResubscribesSameConnection(t *testing.T) {
	var subscribeCount int32
	srv, dialCount := mockTZServer(func(ctx context.Context, c *websocket.Conn, _ int) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`))
		_, auth, _ := c.Read(ctx)
		if !json.Valid(auth) {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`))
		_, _, _ = c.Read(ctx) // 1st subscribe
		atomic.AddInt32(&subscribeCount, 1)
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"TERMINATED"}`))
		_, _, _ = c.Read(ctx) // 2nd subscribe, sent after TERMINATED
		atomic.AddInt32(&subscribeCount, 1)
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"action":"update","userOrderId":"2TZ00001:ETx","symbol":"MSFT","orderStatus":"New","orderQuantity":5,"executed":0,"orderType":"Limit","side":"Buy","openClose":"Open"}`))
		<-ctx.Done()
	})
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	var mu sync.Mutex
	var got []tzOrder
	ws := newWSClient(wsURL, "2TZ00001", "K", "S", clock.System{},
		func(o tzOrder) { mu.Lock(); got = append(got, o); mu.Unlock() },
		func(tzPosition) {}, func(bool) {})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ws.run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 1 {
			if got[0].Symbol != "MSFT" {
				t.Fatalf("order symbol = %q", got[0].Symbol)
			}
			if sc := atomic.LoadInt32(&subscribeCount); sc != 2 {
				t.Fatalf("subscribe count = %d, want 2 (resubscribe after TERMINATED)", sc)
			}
			if dc := atomic.LoadInt32(dialCount); dc != 1 {
				t.Fatalf("dial count = %d, want 1 (TERMINATED must not tear down the socket)", dc)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("client goroutine did not dispatch the post-TERMINATED order update")
}

// TestWS_PositionDispatch covers the position-frame discriminant: an update
// frame with no userOrderId field is a position push and must be routed to
// onPosition, not onOrder.
func TestWS_PositionDispatch(t *testing.T) {
	srv, _ := mockTZServer(func(ctx context.Context, c *websocket.Conn, _ int) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`))
		_, auth, _ := c.Read(ctx)
		if !json.Valid(auth) {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`))
		_, _, _ = c.Read(ctx) // subscribe
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"action":"update","symbol":"TSLA","side":"Long","shares":25,"priceAvg":210.5}`))
		<-ctx.Done()
	})
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	var mu sync.Mutex
	var gotOrders []tzOrder
	var gotPositions []tzPosition
	ws := newWSClient(wsURL, "2TZ00001", "K", "S", clock.System{},
		func(o tzOrder) { mu.Lock(); gotOrders = append(gotOrders, o); mu.Unlock() },
		func(p tzPosition) { mu.Lock(); gotPositions = append(gotPositions, p); mu.Unlock() },
		func(bool) {})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ws.run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(gotPositions)
		mu.Unlock()
		if n == 1 {
			mu.Lock()
			p := gotPositions[0]
			orderCount := len(gotOrders)
			mu.Unlock()
			if p.Symbol != "TSLA" || p.Side != "Long" || p.Shares != 25 || p.PriceAvg != 210.5 {
				t.Fatalf("position = %+v", p)
			}
			if orderCount != 0 {
				t.Fatalf("expected no order dispatch for a position frame, got %d", orderCount)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("did not receive the position update within timeout")
}

// TestWS_StaleReadTriggersReconnect covers the liveness timer: TZ's Portfolio
// WS has no ping/pong, so a connection that silently stops delivering frames
// is only detected by readFrame's deadline. The test shortens staleTimeout
// (default 30s) to keep this fast, and bounds its own wait so a regression
// that makes the client hang can't hang the test suite forever.
func TestWS_StaleReadTriggersReconnect(t *testing.T) {
	srv, dialCount := mockTZServer(func(ctx context.Context, c *websocket.Conn, dialN int) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`))
		_, auth, _ := c.Read(ctx)
		if !json.Valid(auth) {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`))
		_, _, _ = c.Read(ctx) // subscribe
		if dialN == 1 {
			// Go silent past the client's shortened stale timeout instead of
			// sending anything — the client must notice via its own read
			// deadline, since this protocol has no server ping/pong.
			<-ctx.Done()
			return
		}
		// Second connection: prove the client reconnected and is talking
		// normally again, not just retrying the dial in a broken state.
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"action":"update","userOrderId":"2TZ00001:ETx","symbol":"AAPL","orderStatus":"New","orderQuantity":1,"executed":0,"orderType":"Limit","side":"Buy","openClose":"Open"}`))
		<-ctx.Done()
	})
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	var connMu sync.Mutex
	var connEvents []bool
	ws := newWSClient(wsURL, "2TZ00001", "K", "S", clock.System{},
		func(tzOrder) {}, func(tzPosition) {},
		func(up bool) { connMu.Lock(); connEvents = append(connEvents, up); connMu.Unlock() })
	ws.staleTimeout = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go ws.run(ctx)

	// Safety bound for this test's own wait: the run() backoff between
	// sessions starts at 1s (netx.Backoff{Min: time.Second}), so allow
	// generous headroom above staleTimeout + Min backoff without letting a
	// genuine hang stall the suite.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(dialCount) >= 2 {
			connMu.Lock()
			evs := append([]bool(nil), connEvents...)
			connMu.Unlock()
			sawDisconnect := false
			for _, up := range evs {
				if !up {
					sawDisconnect = true
					break
				}
			}
			if !sawDisconnect {
				t.Fatalf("expected onConn(false) after the stale-read timeout, got %v", evs)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("client did not reconnect after the stale-read timeout within the safety bound")
}
