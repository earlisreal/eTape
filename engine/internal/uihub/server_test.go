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
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
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

func TestServerWSSubscribeSnapshot(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	// Build a hub with a mirror via the exported constructor path used by main.
	h, m := uihub.NewHubForTest(clk) // see note: a tiny test constructor exported in server_test-support
	_ = m
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	srv := uihub.NewServer(h,
		uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{}),
		uihub.NewQueriesForTest(fillsNoop{}),
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
		uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{}),
		uihub.NewQueriesForTest(fillsNoop{}),
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
		uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{}),
		uihub.NewQueriesForTest(fillsNoop{}),
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
