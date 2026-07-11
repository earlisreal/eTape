package moomoo

import (
	"net"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/initconnect"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
)

// mockTrdOpenD is an in-process OpenD trade server for trdClient tests. It
// implements the same 44-byte framing as the real gateway (reusing
// opend.Frame/Encode/NewFrameReader directly, since those are exported for
// exactly this purpose) and answers InitConnect/KeepAlive the same way
// feed/opend/mock_opend_test.go's mockOpenD does. Every Trd_* protocol's
// response is per-test configurable via setRespond, so this is a genuine
// integration test of the wire encode/decode -- not a mock of trdClient
// itself.
type mockTrdOpenD struct {
	ln net.Listener

	mu       sync.Mutex
	requests []opend.Frame
	conns    []net.Conn
	respond  map[uint32]func(opend.Frame) proto.Message
}

func newMockTrdOpenD(t *testing.T) *mockTrdOpenD {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &mockTrdOpenD{ln: ln, respond: make(map[uint32]func(opend.Frame) proto.Message)}
	go m.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return m
}

func (m *mockTrdOpenD) addr() string { return m.ln.Addr().String() }

func (m *mockTrdOpenD) acceptLoop() {
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			return
		}
		m.mu.Lock()
		m.conns = append(m.conns, conn)
		m.mu.Unlock()
		go m.handleConn(conn)
	}
}

func (m *mockTrdOpenD) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	fr := opend.NewFrameReader(conn)
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			return
		}
		m.mu.Lock()
		m.requests = append(m.requests, f)
		m.mu.Unlock()
		m.handle(conn, f)
	}
}

// handle answers InitConnect/KeepAlive unconditionally (every test needs a
// live connection) and otherwise dispatches to a per-test respond function
// keyed by protoID. No configured respond function means the mock stays
// silent for that frame -- useful for asserting a request was never sent
// (e.g. a translation error that must short-circuit before the wire).
func (m *mockTrdOpenD) handle(conn net.Conn, f opend.Frame) {
	switch f.ProtoID {
	case opend.ProtoInitConnect:
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
		return
	case opend.ProtoKeepAlive:
		m.reply(conn, f, &keepalive.Response{RetType: proto.Int32(0), S2C: &keepalive.S2C{Time: proto.Int64(1)}})
		return
	}

	m.mu.Lock()
	fn := m.respond[f.ProtoID]
	m.mu.Unlock()
	if fn == nil {
		return // no configured handler: stay silent (simulates a request timeout)
	}
	if msg := fn(f); msg != nil {
		m.reply(conn, f, msg)
	}
}

func (m *mockTrdOpenD) reply(conn net.Conn, req opend.Frame, msg proto.Message) {
	body, _ := proto.Marshal(msg)
	_, _ = conn.Write(opend.Encode(req.ProtoID, req.SerialNo, body))
}

// setRespond registers fn as the response builder for protoID, replacing any
// prior registration. fn receives the raw request frame (so a test can
// decode the C2S body and assert on its fields) and returns the response
// message to encode and send back; a nil return suppresses the reply.
func (m *mockTrdOpenD) setRespond(protoID uint32, fn func(opend.Frame) proto.Message) {
	m.mu.Lock()
	m.respond[protoID] = fn
	m.mu.Unlock()
}

// requestsFor returns a mu-guarded copy of every received frame matching
// protoID, in arrival order.
func (m *mockTrdOpenD) requestsFor(protoID uint32) []opend.Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []opend.Frame
	for _, f := range m.requests {
		if f.ProtoID == protoID {
			out = append(out, f)
		}
	}
	return out
}
