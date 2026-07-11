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

// push writes an unsolicited push frame (serialNo 0) to every connected
// client -- the Adapter tests use it to drive the live 2208/2218 push path.
// 2208/2218 are push proto IDs (opend.IsPushProtoID true), so opend.Client's
// reader routes them to Pushes() regardless of serial; any other protoID with
// no in-flight matching request routes there too (exercising handlePush's
// unrecognized-push branch).
func (m *mockTrdOpenD) push(protoID uint32, msg proto.Message) {
	body, _ := proto.Marshal(msg)
	m.pushRaw(protoID, body)
}

// pushRaw writes protoID + body verbatim (no marshal) so a test can inject a
// deliberately malformed push body and exercise handlePush's decode-error
// branch.
func (m *mockTrdOpenD) pushRaw(protoID uint32, body []byte) {
	frame := opend.Encode(protoID, 0, body)
	m.mu.Lock()
	conns := append([]net.Conn(nil), m.conns...)
	m.mu.Unlock()
	for _, c := range conns {
		_, _ = c.Write(frame)
	}
}

// closeConns closes and forgets every server-side connection, simulating a
// mid-session drop; the Adapter's opend.Client then redials and the acceptLoop
// records the fresh connection. Clearing m.conns ensures a later push targets
// only the reconnected socket.
func (m *mockTrdOpenD) closeConns() {
	m.mu.Lock()
	conns := append([]net.Conn(nil), m.conns...)
	m.conns = nil
	m.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// connCount returns the number of TCP connections currently tracked by this
// mock server. Tests use it as a cheap "dial count" proxy when closeConns()
// is never called in between: VerifyAccount's contract is to dial exactly
// once and tear the connection down before returning, so N back-to-back
// calls (with no intervening closeConns()) should grow connCount by exactly
// N, with no extra (leaked reconnect) accepts in between.
func (m *mockTrdOpenD) connCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.conns)
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
