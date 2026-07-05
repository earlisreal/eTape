package opend

import (
	"net"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/initconnect"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetbasicqot"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetkl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetorderbook"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetticker"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistorykl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistoryklquota"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotsub"
)

// mockOpenD is an in-process OpenD server for client tests. Its handler decides,
// per inbound frame, what to reply (or to stay silent / close the conn).
type mockOpenD struct {
	ln      net.Listener
	handler func(m *mockOpenD, conn net.Conn, f Frame) // custom behavior per frame

	mu          sync.Mutex
	requests    []Frame // every frame received (for assertions)
	dials       int
	data        map[string]*qotData // preloaded cache/history responses, keyed by "US.AAPL"-form symbol
	conns       []net.Conn          // live connections (for pushToAll / dropAllConns)
	quotaRemain int                 // Qot_RequestHistoryKLQuota remaining slots
}

// qotData lets tests preload canned cache/history responses per symbol
// (key = the "US.AAPL"-form symbol, i.e. formatSymbol of the request).
type qotData struct {
	bars1m  []*qotcommon.KLine // served by Qot_GetKL and (paged) Qot_RequestHistoryKL
	ticks   []*qotcommon.Ticker
	bids    []*qotcommon.OrderBook
	asks    []*qotcommon.OrderBook
	basic   *qotcommon.BasicQot
	pageLen int // history page size; 0 = everything in one page
}

func newMockOpenD(t *testing.T) *mockOpenD {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &mockOpenD{ln: ln, quotaRemain: 97}
	m.handler = m.defaultHandler
	go m.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return m
}

func (m *mockOpenD) setData(symbol string, d *qotData) {
	m.mu.Lock()
	if m.data == nil {
		m.data = make(map[string]*qotData)
	}
	m.data[symbol] = d
	m.mu.Unlock()
}

func (m *mockOpenD) dataFor(sec *qotcommon.Security) *qotData {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d := m.data[formatSymbol(sec)]; d != nil {
		return d
	}
	return &qotData{}
}

// snapshotRequests returns a mu-guarded copy of every frame received so far.
func (m *mockOpenD) snapshotRequests() []Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Frame(nil), m.requests...)
}

// pushToAll sends a push frame on every live connection.
func (m *mockOpenD) pushToAll(protoID, serialNo uint32, msg proto.Message) {
	m.mu.Lock()
	conns := append([]net.Conn(nil), m.conns...)
	m.mu.Unlock()
	for _, c := range conns {
		m.push(c, protoID, serialNo, msg)
	}
}

// dropAllConns severs every live connection (reconnect tests).
func (m *mockOpenD) dropAllConns() {
	m.mu.Lock()
	conns := append([]net.Conn(nil), m.conns...)
	m.conns = nil
	m.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
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
	m.mu.Lock()
	m.conns = append(m.conns, conn)
	m.mu.Unlock()
	defer func() {
		conn.Close()
		m.mu.Lock()
		for i, c := range m.conns {
			if c == conn {
				m.conns = append(m.conns[:i], m.conns[i+1:]...)
				break
			}
		}
		m.mu.Unlock()
	}()
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
	case ProtoQotSub:
		m.reply(conn, f, &qotsub.Response{RetType: proto.Int32(0), S2C: &qotsub.S2C{}})
	case ProtoQotGetKL:
		var req qotgetkl.Request
		_ = proto.Unmarshal(f.Body, &req)
		d := m.dataFor(req.GetC2S().GetSecurity())
		m.reply(conn, f, &qotgetkl.Response{RetType: proto.Int32(0),
			S2C: &qotgetkl.S2C{Security: req.GetC2S().GetSecurity(), KlList: d.bars1m}})
	case ProtoQotGetTicker:
		var req qotgetticker.Request
		_ = proto.Unmarshal(f.Body, &req)
		d := m.dataFor(req.GetC2S().GetSecurity())
		m.reply(conn, f, &qotgetticker.Response{RetType: proto.Int32(0),
			S2C: &qotgetticker.S2C{Security: req.GetC2S().GetSecurity(), TickerList: d.ticks}})
	case ProtoQotGetOrderBook:
		var req qotgetorderbook.Request
		_ = proto.Unmarshal(f.Body, &req)
		d := m.dataFor(req.GetC2S().GetSecurity())
		m.reply(conn, f, &qotgetorderbook.Response{RetType: proto.Int32(0),
			S2C: &qotgetorderbook.S2C{Security: req.GetC2S().GetSecurity(),
				OrderBookBidList: d.bids, OrderBookAskList: d.asks}})
	case ProtoQotGetBasicQot:
		var req qotgetbasicqot.Request
		_ = proto.Unmarshal(f.Body, &req)
		var list []*qotcommon.BasicQot
		if len(req.GetC2S().GetSecurityList()) > 0 {
			if d := m.dataFor(req.GetC2S().GetSecurityList()[0]); d.basic != nil {
				list = append(list, d.basic)
			}
		}
		m.reply(conn, f, &qotgetbasicqot.Response{RetType: proto.Int32(0),
			S2C: &qotgetbasicqot.S2C{BasicQotList: list}})
	case ProtoQotRequestHistoryKL:
		var req qotrequesthistorykl.Request
		_ = proto.Unmarshal(f.Body, &req)
		d := m.dataFor(req.GetC2S().GetSecurity())
		start := 0
		if key := req.GetC2S().GetNextReqKey(); len(key) == 1 { // offset byte
			start = int(key[0])
		}
		page := d.bars1m[start:]
		var next []byte
		if d.pageLen > 0 && len(page) > d.pageLen {
			page = page[:d.pageLen]
			next = []byte{byte(start + d.pageLen)}
		}
		m.reply(conn, f, &qotrequesthistorykl.Response{RetType: proto.Int32(0),
			S2C: &qotrequesthistorykl.S2C{Security: req.GetC2S().GetSecurity(),
				KlList: page, NextReqKey: next}})
	case ProtoQotRequestHistoryKLQuota:
		m.mu.Lock()
		remain := m.quotaRemain
		m.mu.Unlock()
		m.reply(conn, f, &qotrequesthistoryklquota.Response{RetType: proto.Int32(0),
			S2C: &qotrequesthistoryklquota.S2C{
				UsedQuota: proto.Int32(int32(100 - remain)), RemainQuota: proto.Int32(int32(remain))}})
	}
}

func (m *mockOpenD) reply(conn net.Conn, req Frame, msg proto.Message) {
	body, _ := proto.Marshal(msg)
	_, _ = conn.Write(Encode(req.ProtoID, req.SerialNo, body))
}

// push sends an unsolicited frame. Real OpenD pushes carry an independent
// nonzero server-side serial — the mock must too, or tests can't catch
// serial-collision bugs.
func (m *mockOpenD) push(conn net.Conn, protoID, serialNo uint32, msg proto.Message) {
	body, _ := proto.Marshal(msg)
	_, _ = conn.Write(Encode(protoID, serialNo, body))
}

func (m *mockOpenD) dialCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dials
}
