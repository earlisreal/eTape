package uihub_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// minimal fakes so the server test doesn't need real cores.
type doerNoop struct{}

func (doerNoop) Do(exec.Command) exec.CmdAck { return exec.CmdAck{Accepted: true} }

type cfgNoop struct{}

func (cfgNoop) GetConfig(string) (string, bool, error) { return "", false, nil }
func (cfgNoop) SetConfig(string, string)               {}

type indNoop struct{}

func (indNoop) EnsureIndicator(string, md.IndicatorSpec) {}
func (indNoop) ReleaseIndicator(string)                  {}

type noopDemand struct{}

func (noopDemand) EnsureDemand(uint64, feed.Demand) {}
func (noopDemand) ReleaseDemand(uint64, string)     {}

func TestServerWSSubscribeSnapshot(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	// Build a hub with a mirror via the exported constructor path used by main.
	h, m := uihub.NewHubForTest(clk) // see note: a tiny test constructor exported in server_test-support
	_ = m
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	srv := uihub.NewServer(h,
		uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{}, noopDemand{}, nil, func() uihub.Feed { return nil }, nil),
		uihub.NewQueriesForTest(fillsNoop{}, clk),
		uihub.ServerConfig{OutBuf: 32})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// exec.status always has an assembled snapshot
	sub, _ := json.Marshal(wsmsg.SubscribeMsg{Kind: "subscribe", Topic: wsmsg.TopicExecStatus})
	if err := c.Write(ctx, websocket.MessageText, sub); err != nil {
		t.Fatal(err)
	}
	rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
	defer rcancel()
	_, data, err := c.Read(rctx)
	if err != nil {
		t.Fatal(err)
	}
	var mm map[string]any
	_ = json.Unmarshal(data, &mm)
	if mm["kind"] != "snapshot" || mm["topic"] != "exec.status" {
		t.Fatalf("expected exec.status snapshot, got %v", mm)
	}
}

type fillsNoop struct{}

func (fillsNoop) QueryFills(string, int64, int64) ([]exec.FillRow, error) { return nil, nil }

func (fillsNoop) ExportFills(context.Context, string, int64, int64) ([]exec.ExportFillRow, error) {
	return nil, nil
}

// tinySndBufListener wraps a net.Listener and pins every accepted TCP
// connection's kernel send buffer to a few KB. It's used to make a "slow
// client" (one that stops reading) back-pressure the server's ws.Write calls
// deterministically and fast, instead of depending on a platform's default
// socket buffer size (which can be hundreds of KB to a few MB and would make
// the overflow trigger slow/flaky).
type tinySndBufListener struct {
	net.Listener
}

func (l tinySndBufListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetWriteBuffer(2048)
	}
	return c, nil
}

// TestServerOverflowDropDoesNotStallHub is a regression test for the review
// finding that conn.close() called ws.Close() synchronously: with the real
// coder/websocket adapter, Close performs a graceful close handshake that can
// block for several seconds on an unresponsive peer (it needs the same read
// lock the peer's own blocked reader may be holding). Hub.broadcast and
// Hub.sendSnapshot call a dead client's close() directly from Hub.Run's
// single event-loop goroutine on queue overflow, so an unpatched synchronous
// close() would stall every other client's traffic for the duration of that
// handshake. This test proves a second, healthy client is served promptly
// while a first, stuck client's queue is overflowing and being torn down.
func TestServerOverflowDropDoesNotStallHub(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h, _ := uihub.NewHubForTest(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	srv := uihub.NewServer(h,
		uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{}, noopDemand{}, nil, func() uihub.Feed { return nil }, nil),
		uihub.NewQueriesForTest(fillsNoop{}, clk),
		uihub.ServerConfig{OutBuf: 2}) // tiny outbound queue: a handful of updates overflow it

	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.Listener = tinySndBufListener{Listener: ts.Listener}
	ts.Start()
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	// Client A: subscribes once, then never reads again -- simulating a
	// stuck/slow client. With the server's send buffer pinned to 2KB (above)
	// and A never draining its receive window, the server's ws.Write calls to
	// A start blocking after only a handful of frames.
	connA, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connA.CloseNow() }()

	subA, _ := json.Marshal(wsmsg.SubscribeMsg{Kind: "subscribe", Topic: wsmsg.TopicSysEvents})
	if err := connA.Write(ctx, websocket.MessageText, subA); err != nil {
		t.Fatal(err)
	}
	// Read exactly once, to confirm the subscribe was processed (the
	// sys.events snapshot frame) -- then A never reads again.
	ackCtx, ackCancel := context.WithTimeout(ctx, 2*time.Second)
	if _, _, err := connA.Read(ackCtx); err != nil {
		ackCancel()
		t.Fatalf("client A did not get its subscribe snapshot: %v", err)
	}
	ackCancel()

	// Flood sys.events: handlePub broadcasts unconditionally (no coalescing),
	// so every call produces one frame to A. Once A's connection backs up,
	// the Hub's overflow-drop path (conn.enqueue's `default: c.close()`)
	// fires for A synchronously, on the Hub's single event-loop goroutine.
	floodDone := make(chan struct{})
	go func() {
		defer close(floodDone)
		for i := 0; i < 500; i++ {
			h.Publish(wsmsg.TopicSysEvents, "", wsmsg.SysEvent{
				Seq: int64(i), Kind: "test", Detail: strings.Repeat("x", 256),
			})
		}
	}()
	defer func() { <-floodDone }()

	// While the flood (and A's overflow-triggered teardown) is in flight,
	// client B must still be served promptly -- proving the Hub's single
	// goroutine isn't stalled inside conn.close()'s ws.Close() handshake.
	connB, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connB.CloseNow() }()

	subB, _ := json.Marshal(wsmsg.SubscribeMsg{Kind: "subscribe", Topic: wsmsg.TopicExecStatus})
	if err := connB.Write(ctx, websocket.MessageText, subB); err != nil {
		t.Fatal(err)
	}
	bCtx, bCancel := context.WithTimeout(ctx, 2*time.Second)
	defer bCancel()
	_, data, err := connB.Read(bCtx)
	if err != nil {
		t.Fatalf("client B was blocked/timed out waiting for its snapshot (Hub stalled?): %v", err)
	}
	var mm map[string]any
	_ = json.Unmarshal(data, &mm)
	if mm["kind"] != "snapshot" || mm["topic"] != "exec.status" {
		t.Fatalf("expected exec.status snapshot for client B, got %v", mm)
	}
}

func TestServerStaticFileServing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>etape</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.UnixMilli(0))
	h, _ := uihub.NewHubForTest(clk)
	srv := uihub.NewServer(h,
		uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{}, noopDemand{}, nil, func() uihub.Feed { return nil }, nil),
		uihub.NewQueriesForTest(fillsNoop{}, clk),
		uihub.ServerConfig{DistDir: dir, OutBuf: 32})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("static index should 200, got %d", resp.StatusCode)
	}
	// SPA fallback: an unknown non-file path also returns index.html
	resp2, err := http.Get(ts.URL + "/trading")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("SPA fallback should serve index.html for /trading, got %d", resp2.StatusCode)
	}
}

// slowConfig is a configStore fake whose SetConfig blocks on release until the
// test lets it proceed, so the test can force a window where a connection's
// dispatch() call is genuinely in flight -- reproducing the shutdown race the
// final review finding describes (a client's SetConfig command still being
// processed by conn.run()'s readLoop after the top-level ctx has fired).
type slowConfig struct {
	calls   atomic.Int32
	active  atomic.Bool   // true only while a SetConfig call is executing
	started chan struct{} // closed once SetConfig is entered
	release chan struct{} // test closes this to let SetConfig return
}

func (c *slowConfig) GetConfig(string) (string, bool, error) { return "", false, nil }

func (c *slowConfig) SetConfig(string, string) {
	c.active.Store(true)
	c.calls.Add(1)
	close(c.started)
	<-c.release
	c.active.Store(false)
}

// TestServerWaitBlocksUntilConnectionDrains is a regression test for the final
// whole-branch review finding: store.SetConfig sends unconditionally on
// s.writes, and store.Close() closes s.writes, so a client's SetConfig command
// still being dispatched after the top-level ctx fires can panic the process.
// Nothing previously joined conn.run() goroutines before main.go called
// st.Close(), because Hub.Run's <-ctx.Done() branch only asks connections to
// close (asynchronously) -- it doesn't wait for them to actually finish
// tearing down, and http.Server.Shutdown does not wait on hijacked WebSocket
// connections either.
//
// This test proves Server.Wait() closes that gap: it (a) genuinely blocks
// while a connection's dispatch() call is still executing, even after the
// hub's ctx has already been cancelled, and (b) returns promptly once that
// connection's conn.run() goroutine has actually returned. Because dispatch()
// is only ever called synchronously from within conn.run()'s own readLoop (one
// goroutine per connection, no concurrent dispatch calls), the readLoop cannot
// have advanced past that in-flight call while conn.run() is still running --
// so by the time Wait() unblocks, no dispatch()/SetConfig call for that
// connection can still be executing (asserted directly via sc.active below).
func TestServerWaitBlocksUntilConnectionDrains(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h, _ := uihub.NewHubForTest(clk)
	// hubCtx mirrors main.go's top-level ctx: only Hub.Run observes it, exactly
	// as in production (a websocket connection's own r.Context() is tied to
	// the individual HTTP request, not to this ctx -- see server.go's Wait doc).
	hubCtx, cancelHub := context.WithCancel(context.Background())
	defer cancelHub()
	hubDone := make(chan struct{})
	go func() { defer close(hubDone); _ = h.Run(hubCtx) }()

	sc := &slowConfig{started: make(chan struct{}), release: make(chan struct{})}
	srv := uihub.NewServer(h,
		uihub.NewCommandsForTest(doerNoop{}, sc, indNoop{}, noopDemand{}, nil, func() uihub.Feed { return nil }, nil),
		uihub.NewQueriesForTest(fillsNoop{}, clk),
		uihub.ServerConfig{OutBuf: 32})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	c, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()

	// Subscribe and wait for the snapshot reply: this round-trip guarantees the
	// server has already run past hub.Register + srv.connWG.Add for this
	// connection (readLoop had to be running to receive and answer it), so the
	// connection is unambiguously "tracked" and "registered with the hub"
	// before we proceed.
	sub, _ := json.Marshal(wsmsg.SubscribeMsg{Kind: "subscribe", Topic: wsmsg.TopicExecStatus})
	if err := c.Write(dialCtx, websocket.MessageText, sub); err != nil {
		t.Fatal(err)
	}
	snapCtx, snapCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if _, _, err := c.Read(snapCtx); err != nil {
		snapCancel()
		t.Fatalf("did not get subscribe snapshot: %v", err)
	}
	snapCancel()

	// Send a SetConfig command; the server's dispatch() call for this
	// connection will now block inside sc.SetConfig until the test releases it.
	setCfg, _ := json.Marshal(wsmsg.CommandMsg{
		Kind: "command", CorrID: "c1", Name: "SetConfig",
		Args: json.RawMessage(`{"key":"k","value":"1"}`),
	})
	if err := c.Write(dialCtx, websocket.MessageText, setCfg); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sc.started:
	case <-time.After(2 * time.Second):
		t.Fatal("SetConfig was never invoked")
	}

	// Simulate main.go's shutdown signal firing: cancel the top-level ctx.
	// Hub.Run's <-ctx.Done() branch now calls c.close() on the connection
	// (spawning the real ws.Close() handshake asynchronously per Task 10's
	// fix) -- but the connection's readLoop is not selecting on c.done right
	// now; it is synchronously blocked inside dispatch() -> SetConfig, so
	// conn.run() cannot observe the close yet and has not returned.
	cancelHub()
	select {
	case <-hubDone:
	case <-time.After(2 * time.Second):
		t.Fatal("hub.Run did not return after ctx cancel")
	}

	waitDone := make(chan struct{})
	go func() { srv.Wait(); close(waitDone) }()

	// (a) Wait() must genuinely block: the connection is still open and its
	// dispatch() call has not returned, even though the hub's ctx already
	// fired.
	select {
	case <-waitDone:
		t.Fatal("Server.Wait() returned while a connection's SetConfig dispatch was still in flight")
	case <-time.After(200 * time.Millisecond):
	}
	if !sc.active.Load() {
		t.Fatal("test setup bug: SetConfig should still be active at this point")
	}

	// Let SetConfig (and therefore dispatch(), and therefore conn.run()'s
	// readLoop iteration) finish.
	close(sc.release)

	// (b) Wait() must return promptly once conn.run() has actually returned.
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Server.Wait() did not return after the connection's conn.run() finished")
	}

	// No dispatch()/SetConfig call for this connection can still be in flight:
	// dispatch() only ever runs synchronously inside conn.run()'s own readLoop
	// goroutine, and Wait() only returned because that goroutine returned.
	if sc.active.Load() {
		t.Fatal("SetConfig still marked active after Server.Wait() returned")
	}
	if got := sc.calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 SetConfig call, got %d", got)
	}
}

// TestServerWaitBoundedByBaseContextAfterHubExit is a regression test for a
// residual gap found in re-review of the fix TestServerWaitBlocksUntilConnectionDrains
// covers: Server.Wait()'s boundedness depended entirely on Hub.Run's
// <-ctx.Done() branch calling c.close() on every registered client. But a
// connection accepted (and hub.Register'd) AFTER Hub.Run has already returned
// races into Hub.Register's own <-h.closed branch and silently no-ops -- the
// connection never lands in h.clients, so Hub.Run's teardown loop (which
// already returned anyway) can never tell it to close either. Before this fix,
// that connection's readLoop would block forever in ws.Read(r.Context())
// because r.Context() was tied only to the individual HTTP request, not to any
// cancellable top-level context -- so Server.Wait() (no timeout) would hang.
//
// This test reproduces exactly that ordering -- Hub.Run exits FIRST, then a
// new connection is accepted and registered against the already-exited hub --
// and proves the fix (httpSrv.BaseContext tying r.Context() to a top-level
// ctx, wired in cmd/etape/main.go) bounds it: Server.Wait() blocks while the
// connection's read is pending, and returns promptly once the top-level ctx
// (not any Hub state) is cancelled.
func TestServerWaitBoundedByBaseContextAfterHubExit(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h, _ := uihub.NewHubForTest(clk)

	// Run and immediately exit the hub -- simulating Hub.Run having already
	// returned by the time the phantom connection is accepted.
	hubCtx, cancelHub := context.WithCancel(context.Background())
	hubDone := make(chan struct{})
	go func() { defer close(hubDone); _ = h.Run(hubCtx) }()
	cancelHub()
	select {
	case <-hubDone:
	case <-time.After(2 * time.Second):
		t.Fatal("hub.Run did not exit")
	}

	srv := uihub.NewServer(h,
		uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{}, noopDemand{}, nil, func() uihub.Feed { return nil }, nil),
		uihub.NewQueriesForTest(fillsNoop{}, clk),
		uihub.ServerConfig{OutBuf: 32})

	// topCtx stands in for main.go's top-level shutdown ctx. Wiring it via
	// BaseContext (as cmd/etape/main.go now does) is the fix under test: it
	// must be what unblocks the phantom connection's Read, not anything Hub-
	// related.
	topCtx, cancelTop := context.WithCancel(context.Background())
	defer cancelTop()

	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.Config.BaseContext = func(net.Listener) context.Context { return topCtx }
	ts.Start()
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	c, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()

	// Round-trip a ping/pong before touching srv.Wait(): dispatch()'s "ping"
	// case answers entirely inside conn.run()'s own readLoop/writeLoop, with no
	// hub involvement (unlike "subscribe", which would go nowhere here -- the
	// hub already exited, so hub.Subscribe would just race into its own
	// <-h.closed no-op branch and never reply). Receiving the pong back
	// guarantees serveWS has already executed connWG.Add(1) + hub.Register and
	// started conn.run() for this connection, establishing the happens-before
	// edge srv.Wait() (called next) needs: sync.WaitGroup requires any Add(1)
	// racing a zero counter to happen-before the Wait call it's paired with,
	// and a bare successful Dial (the handshake response is written before
	// serveWS ever reaches connWG.Add) does not by itself provide that.
	ping, _ := json.Marshal(map[string]any{"kind": "ping", "t": 42})
	if err := c.Write(dialCtx, websocket.MessageText, ping); err != nil {
		t.Fatal(err)
	}
	pongCtx, pongCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if _, _, err := c.Read(pongCtx); err != nil {
		pongCancel()
		t.Fatalf("did not get pong reply: %v", err)
	}
	pongCancel()

	// The client never sends or disconnects again, so with the old
	// (per-request) r.Context(), readLoop's next ws.Read would block forever --
	// there is no Hub state left to ever close this connection (Hub.Run
	// already returned, and this connection's hub.Register call raced into the
	// <-h.closed no-op branch, so it was never added to h.clients).
	waitDone := make(chan struct{})
	go func() { srv.Wait(); close(waitDone) }()

	select {
	case <-waitDone:
		t.Fatal("Server.Wait() returned before the phantom connection's read was unblocked")
	case <-time.After(200 * time.Millisecond):
	}

	// Cancel the top-level ctx (not any Hub-related ctx) -- this is the only
	// thing that can unblock the phantom connection now.
	cancelTop()

	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Server.Wait() did not return after the top-level ctx was cancelled")
	}
}
