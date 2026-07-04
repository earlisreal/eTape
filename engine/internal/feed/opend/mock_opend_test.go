package opend

import (
	"net"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/initconnect"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
)

// mockOpenD is an in-process OpenD server for client tests. Its handler decides,
// per inbound frame, what to reply (or to stay silent / close the conn).
type mockOpenD struct {
	ln      net.Listener
	handler func(m *mockOpenD, conn net.Conn, f Frame) // custom behavior per frame

	mu       sync.Mutex
	requests []Frame // every frame received (for assertions)
	dials    int
}

func newMockOpenD(t *testing.T) *mockOpenD {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &mockOpenD{ln: ln}
	m.handler = m.defaultHandler
	go m.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return m
}

func (m *mockOpenD) addr() string { return m.ln.Addr().String() }

func (m *mockOpenD) acceptLoop() {
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			return
		}
		m.mu.Lock()
		m.dials++
		m.mu.Unlock()
		go m.handleConn(conn)
	}
}

func (m *mockOpenD) handleConn(conn net.Conn) {
	defer conn.Close()
	fr := NewFrameReader(conn)
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			return
		}
		m.mu.Lock()
		m.requests = append(m.requests, f)
		m.mu.Unlock()
		m.handler(m, conn, f)
	}
}

// defaultHandler answers InitConnect and KeepAlive with success replies.
func (m *mockOpenD) defaultHandler(_ *mockOpenD, conn net.Conn, f Frame) {
	switch f.ProtoID {
	case ProtoInitConnect:
		resp := &initconnect.Response{
			RetType: proto.Int32(0),
			S2C: &initconnect.S2C{
				ServerVer:         proto.Int32(900),
				LoginUserID:       proto.Uint64(1),
				ConnID:            proto.Uint64(0xABCDEF),
				ConnAESKey:        proto.String("0000000000000000"),
				KeepAliveInterval: proto.Int32(10),
			},
		}
		m.reply(conn, f, resp)
	case ProtoKeepAlive:
		resp := &keepalive.Response{RetType: proto.Int32(0), S2C: &keepalive.S2C{Time: proto.Int64(1)}}
		m.reply(conn, f, resp)
	}
}

func (m *mockOpenD) reply(conn net.Conn, req Frame, msg proto.Message) {
	body, _ := proto.Marshal(msg)
	_, _ = conn.Write(Encode(req.ProtoID, req.SerialNo, body))
}

// push sends an unsolicited frame (serialNo 0 → no waiter → routed as a push).
func (m *mockOpenD) push(conn net.Conn, protoID uint32, msg proto.Message) {
	body, _ := proto.Marshal(msg)
	_, _ = conn.Write(Encode(protoID, 0, body))
}

func (m *mockOpenD) dialCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dials
}

// requestCount is a reserved assertion helper (no test in this task needs a
// total-request count yet; dialCount covers Task 11's reconnect tests).
//
//nolint:unused // kept for future opend tests
func (m *mockOpenD) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}
