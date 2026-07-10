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
	"github.com/earlisreal/eTape/engine/internal/broker/netx"
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

// TestWS_PingTimeoutTriggersReconnect covers the liveness timer: TZ's
// Portfolio WS has no server ping/pong, so a connection that silently stops
// delivering frames — and stops reading entirely, so it can never auto-pong —
// is only detected by the pinger goroutine failing to get a pong back within
// pongTimeout. This is a distinct code path from an abrupt close (immediate
// read error), which the handshake/dispatch tests above already exercise via
// the mock's <-ctx.Done() shape. The test shortens pingInterval/pongTimeout
// to keep this fast, and bounds its own wait so a regression that makes the
// client hang can't hang the test suite forever.
func TestWS_PingTimeoutTriggersReconnect(t *testing.T) {
	srv, dialCount := mockTZServer(func(ctx context.Context, c *websocket.Conn, dialN int) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`))
		_, auth, _ := c.Read(ctx)
		if !json.Valid(auth) {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`))
		_, _, _ = c.Read(ctx) // subscribe
		if dialN == 1 {
			// Go silent past the client's shortened pong timeout instead of
			// sending anything — no close, no error frame, no more writes,
			// and no more reads either, so the client's pings are never
			// acknowledged: a coder/websocket pong is only ever consumed by
			// the peer's own Read machinery, and this peer stops calling
			// Read here.
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
				t.Fatalf("expected onConn(false) after the ping timeout, got %v", evs)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("client did not reconnect after the ping timeout within the safety bound")
}

// TestWS_IdleButAlivePingsSucceed is the actual regression test for this
// task. TZ's Portfolio WS sends nothing at all whenever there is no
// order/position activity — the normal case, not a failure — and before this
// fix, readFrame wrapped *every* Read in a fresh staleTimeout deadline, so
// any idle gap longer than that deadline reconnected the session regardless
// of whether the socket was actually healthy. Here the mock keeps its own
// Read loop running the whole time (so the library auto-pongs every ping it
// receives) but never writes a single data frame, for a window spanning many
// pingInterval cycles. A client that still judged liveness by "did Read get
// a frame recently" would reconnect repeatedly during this window; the fixed
// client, judging liveness by ping/pong instead, must not reconnect at all.
func TestWS_IdleButAlivePingsSucceed(t *testing.T) {
	srv, dialCount := mockTZServer(func(ctx context.Context, c *websocket.Conn, _ int) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`))
		_, auth, _ := c.Read(ctx)
		if !json.Valid(auth) {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`))
		_, _, _ = c.Read(ctx) // subscribe
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

	var connMu sync.Mutex
	var connEvents []bool
	ws := newWSClient(wsURL, "2TZ00001", "K", "S", clock.System{},
		func(tzOrder) {}, func(tzPosition) {},
		func(up bool) { connMu.Lock(); connEvents = append(connEvents, up); connMu.Unlock() })
	ws.pingInterval = 30 * time.Millisecond
	ws.pongTimeout = 200 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ws.run(ctx)

	// This window comfortably spans well over a dozen pingInterval cycles of
	// pure silence — old code's default 30s staleTimeout is gone, but what
	// matters is the shape: many idle cycles with zero data frames, which is
	// exactly what used to fire the old per-read deadline.
	time.Sleep(500 * time.Millisecond)

	if n := atomic.LoadInt32(dialCount); n != 1 {
		t.Fatalf("dial count = %d, want exactly 1 (idle-but-alive must not trigger a reconnect)", n)
	}
	connMu.Lock()
	evs := append([]bool(nil), connEvents...)
	connMu.Unlock()
	if len(evs) == 0 {
		t.Fatal("client never reported connected")
	}
	for _, up := range evs {
		if !up {
			t.Fatalf("expected no disconnect while idle-but-alive, got connEvents=%v", evs)
		}
	}
}

// TestWS_LateFailedAuthNeverReconnects covers the errFailedAuth invariant
// this task must preserve, in the specific shape the new errgroup/pinger
// structure introduces: unlike TestWS_FailedAuthNeverReconnects (FAILED_AUTH
// during the bounded handshake phase, before onConn(true) or the errgroup
// ever start), here FAILED_AUTH arrives from the server *after* subscribe,
// i.e. from readLoop while it runs concurrently with pinger inside session's
// errgroup. readLoop returning errFailedAuth must still propagate out of
// g.Wait() unchanged (not wrapped, not masked by a concurrent pinger error)
// so run()'s errors.Is(err, errFailedAuth) check still fires and ends the
// client for good — no reconnect.
func TestWS_LateFailedAuthNeverReconnects(t *testing.T) {
	srv, dialCount := mockTZServer(func(ctx context.Context, c *websocket.Conn, _ int) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`))
		_, auth, _ := c.Read(ctx)
		if !json.Valid(auth) {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`))
		_, _, _ = c.Read(ctx) // subscribe
		// Unlike TestWS_FailedAuthNeverReconnects, FAILED_AUTH here arrives
		// post-handshake, on the steady-state connection readLoop owns.
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
		t.Fatal("run(ctx) did not return after a late FAILED_AUTH")
	}

	// Give any errant reconnect attempt a moment to land before checking —
	// run() has already returned, but this guards against a regression that
	// spawns a stray dial from elsewhere.
	time.Sleep(200 * time.Millisecond)
	if n := atomic.LoadInt32(dialCount); n != 1 {
		t.Fatalf("dial count = %d, want 1 (must never reconnect after a late FAILED_AUTH)", n)
	}
}

// TestResetBackoffIfHealthy_LongSessionResets is the backoff-reset
// regression test: a session that lasted at least bo.Max was a genuinely
// healthy, actively-pinged connection (not a connect-then-drop loop), so the
// next reconnect delay must go back to Min instead of continuing to climb.
// This exercises resetBackoffIfHealthy directly against the same
// netx.Backoff run() uses, rather than driving a real WS session end to end
// on a wall clock — asserting an exact reconnect delay via real timing would
// be flaky (scheduling noise is comparable to the delays involved), whereas
// netx.Backoff.Next() is deterministic immediately after Reset(): cur starts
// at 0, so the first Next() call after a reset returns exactly Min with no
// jitter at all (see netx.Backoff.Next()'s span<=0 case).
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
// threshold is real: a session shorter than bo.Max (still flapping) must not
// reset, so backoff keeps climbing across a genuine reconnect storm instead
// of a single healthy blip masking it. If resetBackoffIfHealthy ignored the
// threshold and always reset, Next() here would return exactly bo.Min
// deterministically (100% of the time, per the same span<=0 case above)
// instead of a full-jittered value strictly inside (Min, 2*Min) — so the
// boundary checks below reliably catch an "always resets" bug rather than
// depending on a coincidental timing race.
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
