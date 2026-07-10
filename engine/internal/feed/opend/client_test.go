package opend

import (
	"context"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/initconnect"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetbasicqot"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotsub"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
)

func dialClient(t *testing.T, m *mockOpenD) (*Client, net.Conn) {
	t.Helper()
	c := New(Options{Addr: m.addr(), Clock: clock.System{}, RequestTimeout: 200 * time.Millisecond})
	conn, err := net.Dial("tcp", m.addr())
	if err != nil {
		t.Fatal(err)
	}
	c.setConn(conn)
	// Start the reader/dispatch loop by hand (serveConn's inner goroutine) so we
	// can test Request/push without the full lifecycle (Tasks 10-11).
	fr := NewFrameReader(conn)
	go func() {
		for {
			f, err := fr.ReadFrame()
			if err != nil {
				return
			}
			if IsPushProtoID(f.ProtoID) || !c.pending.resolve(f) {
				c.pushes <- f
			}
		}
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return c, conn
}

func TestRequestGetsCorrelatedResponse(t *testing.T) {
	m := newMockOpenD(t)
	c, _ := dialClient(t, m)

	f, err := c.Request(context.Background(), ProtoKeepAlive,
		&keepalive.Request{C2S: &keepalive.C2S{Time: proto.Int64(1)}})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	var resp keepalive.Response
	if err := proto.Unmarshal(f.Body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.GetRetType() != 0 {
		t.Fatalf("retType = %d, want 0", resp.GetRetType())
	}
}

func TestRequestTimesOutWhenServerSilent(t *testing.T) {
	m := newMockOpenD(t)
	m.handler = func(_ *mockOpenD, _ net.Conn, _ Frame) {} // never reply
	c, _ := dialClient(t, m)

	_, err := c.Request(context.Background(), ProtoKeepAlive,
		&keepalive.Request{C2S: &keepalive.C2S{Time: proto.Int64(1)}})
	if err != ErrRequestTimeout {
		t.Fatalf("err = %v, want ErrRequestTimeout", err)
	}
}

func TestUnsolicitedFrameRoutesToPushes(t *testing.T) {
	m := newMockOpenD(t)
	// Reply to the request AND emit a push on the same conn.
	m.handler = func(mm *mockOpenD, conn net.Conn, f Frame) {
		mm.defaultHandler(mm, conn, f)
		mm.push(conn, ProtoQotUpdateTicker, 1500, &qotsub.Response{RetType: proto.Int32(0)})
	}
	c, _ := dialClient(t, m)

	if _, err := c.Request(context.Background(), ProtoKeepAlive,
		&keepalive.Request{C2S: &keepalive.C2S{Time: proto.Int64(1)}}); err != nil {
		t.Fatalf("Request: %v", err)
	}
	select {
	case p := <-c.Pushes():
		if p.ProtoID != ProtoQotUpdateTicker {
			t.Fatalf("push protoID = %d, want %d", p.ProtoID, ProtoQotUpdateTicker)
		}
	case <-time.After(time.Second):
		t.Fatal("no push routed within 1s")
	}
}

func TestInitConnectStoresConnID(t *testing.T) {
	m := newMockOpenD(t)
	c, _ := dialClient(t, m)
	if err := c.initConnect(context.Background()); err != nil {
		t.Fatalf("initConnect: %v", err)
	}
	if c.ConnID() != 0xABCDEF {
		t.Fatalf("connID = %#x, want 0xABCDEF", c.ConnID())
	}
	if c.ServerVer() != 900 {
		t.Fatalf("serverVer = %d, want 900", c.ServerVer())
	}
	if c.kaInt != 10*time.Second {
		t.Fatalf("keepAlive interval = %v, want 10s", c.kaInt)
	}
}

func TestInitConnectFailsOnNonZeroRetType(t *testing.T) {
	m := newMockOpenD(t)
	m.handler = func(_ *mockOpenD, conn net.Conn, f Frame) {
		if f.ProtoID == ProtoInitConnect {
			resp := &initconnect.Response{RetType: proto.Int32(-1), RetMsg: proto.String("nope")}
			m.reply(conn, f, resp)
		}
	}
	c, _ := dialClient(t, m)
	if err := c.initConnect(context.Background()); err == nil {
		t.Fatal("expected error on retType=-1")
	}
}

func TestKeepAliveLoopSendsHeartbeats(t *testing.T) {
	m := newMockOpenD(t)
	c, _ := dialClient(t, m)
	if err := c.initConnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.kaInt = 20 * time.Millisecond // fast heartbeat for the test
	errc := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go c.keepAliveLoop(ctx, errc)
	defer cancel()

	// Expect several KeepAlive requests to arrive at the mock within ~200ms.
	deadline := time.After(500 * time.Millisecond)
	for {
		kaCount := 0
		m.mu.Lock()
		for _, r := range m.requests {
			if r.ProtoID == ProtoKeepAlive {
				kaCount++
			}
		}
		m.mu.Unlock()
		if kaCount >= 3 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("only %d keepalives seen, want >=3", kaCount)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestRunConnectsAndSignalsUp(t *testing.T) {
	m := newMockOpenD(t)
	c := New(Options{
		Addr: m.addr(), Clock: clock.System{},
		RequestTimeout: 200 * time.Millisecond, ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	select {
	case st := <-c.State():
		if st != ConnUp {
			t.Fatalf("first state = %v, want ConnUp", st)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ConnUp within 2s")
	}
	if c.ConnID() != 0xABCDEF {
		t.Fatalf("connID = %#x after connect", c.ConnID())
	}
}

func TestRunReconnectsAfterDrop(t *testing.T) {
	m := newMockOpenD(t)
	// Drop the connection right after the InitConnect reply, forcing reconnects.
	m.handler = func(mm *mockOpenD, conn net.Conn, f Frame) {
		if f.ProtoID == ProtoInitConnect {
			mm.defaultHandler(mm, conn, f)
			_ = conn.Close()
		}
	}
	c := New(Options{
		Addr: m.addr(), Clock: clock.System{},
		RequestTimeout: 100 * time.Millisecond, ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	// The client should dial repeatedly as the server keeps dropping it.
	deadline := time.After(2 * time.Second)
	for {
		if m.dialCount() >= 3 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("only %d dials, want >=3 (reconnect not happening)", m.dialCount())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestRunReconnectsAfterKeepAliveTimeout drives the kaErr exit path: a wedged
// OpenD that answers InitConnect and the first heartbeat, then goes silent
// WITHOUT closing the TCP socket. The client must (1) time out the next
// KeepAlive and reconnect, and (2) close the dead socket so the reader goroutine
// unblocks and exits. Before serveConn closed the conn on exit, the reader
// stayed blocked in ReadFrame (io.ReadFull, no deadline) on the still-open
// socket, leaking the goroutine + fd on every wedged session.
func TestRunReconnectsAfterKeepAliveTimeout(t *testing.T) {
	m := newMockOpenD(t)
	var kaSeen atomic.Int32
	m.handler = func(mm *mockOpenD, conn net.Conn, f Frame) {
		switch f.ProtoID {
		case ProtoInitConnect:
			// Full success reply (ServerVer etc. are proto2-required) with a
			// fast 1s keepalive interval so the heartbeat loop ticks quickly.
			resp := &initconnect.Response{
				RetType: proto.Int32(0),
				S2C: &initconnect.S2C{
					ServerVer:         proto.Int32(900),
					LoginUserID:       proto.Uint64(1),
					ConnID:            proto.Uint64(0xABCDEF),
					ConnAESKey:        proto.String("0000000000000000"),
					KeepAliveInterval: proto.Int32(1),
				},
			}
			mm.reply(conn, f, resp)
		case ProtoKeepAlive:
			if kaSeen.Add(1) <= 1 {
				mm.defaultHandler(mm, conn, f) // answer the first heartbeat only
			}
			// otherwise: stay silent, keep the socket open (the wedge scenario)
		}
	}

	base := runtime.NumGoroutine()

	c := New(Options{
		Addr: m.addr(), Clock: clock.System{},
		RequestTimeout: 50 * time.Millisecond,
		ReconnectMin:   time.Millisecond, ReconnectMax: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()

	// (1) dialCount>=2 proves the keepalive timeout ended the wedged session and
	// the client redialed on a socket the server never closed.
	deadline := time.After(4 * time.Second)
	for m.dialCount() < 2 {
		select {
		case <-deadline:
			cancel()
			t.Fatalf("only %d dials, want >=2 (keepalive-timeout reconnect not happening)", m.dialCount())
		case <-time.After(10 * time.Millisecond):
		}
	}

	// (2) Stop Run and assert the wedged session's reader goroutine was cleaned
	// up. Without serveConn closing the socket on the kaErr path, each wedged
	// connection leaks a reader blocked in ReadFrame; goroutine count would stay
	// elevated instead of settling back toward the pre-Run baseline.
	cancel()
	<-done
	leakDeadline := time.After(2 * time.Second)
	for {
		if runtime.NumGoroutine() <= base+2 {
			return
		}
		select {
		case <-leakDeadline:
			t.Fatalf("goroutines did not settle after cancel: base=%d now=%d (leaked reader on kaErr path?)",
				base, runtime.NumGoroutine())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	m := newMockOpenD(t)
	c := New(Options{
		Addr: m.addr(), Clock: clock.System{},
		ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// waitForState blocks until the client reports the wanted state (draining
// intermediate transitions) or fails the test after 3s.
func waitForState(t *testing.T, c *Client, want ConnState) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case st := <-c.State():
			if st == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for connection state %v", want)
		}
	}
}

// TestPushSerialCollisionDoesNotHijackRequest reproduces the live observation
// that OpenD pushes carry their own serial numbers: a push whose serial equals
// an in-flight request's serial must NOT resolve that request.
func TestPushSerialCollisionDoesNotHijackRequest(t *testing.T) {
	m := newMockOpenD(t)
	m.handler = func(m *mockOpenD, conn net.Conn, f Frame) {
		switch f.ProtoID {
		case ProtoInitConnect, ProtoKeepAlive:
			m.defaultHandler(m, conn, f)
		case ProtoQotGetBasicQot:
			// Adversarial ordering: first a ticker push reusing the request's
			// serial, then the real response.
			m.push(conn, ProtoQotUpdateTicker, f.SerialNo, &qotupdateticker.Response{
				RetType: proto.Int32(0),
				S2C:     &qotupdateticker.S2C{Security: &qotcommon.Security{Market: proto.Int32(11), Code: proto.String("AAPL")}},
			})
			m.reply(conn, f, &qotgetbasicqot.Response{RetType: proto.Int32(0), S2C: &qotgetbasicqot.S2C{}})
		}
	}

	c := New(Options{Addr: m.addr()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	waitForState(t, c, ConnUp)

	f, err := c.Request(ctx, ProtoQotGetBasicQot, &qotgetbasicqot.Request{C2S: &qotgetbasicqot.C2S{}})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if f.ProtoID != ProtoQotGetBasicQot {
		t.Fatalf("request resolved with protoID %d (hijacked by push), want %d", f.ProtoID, ProtoQotGetBasicQot)
	}
	select {
	case p := <-c.Pushes():
		if p.ProtoID != ProtoQotUpdateTicker {
			t.Fatalf("push protoID = %d, want %d", p.ProtoID, ProtoQotUpdateTicker)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("push frame never delivered to Pushes()")
	}
}
