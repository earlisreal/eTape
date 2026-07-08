package opend

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistorykl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
)

func kl(tsSec float64, c float64, v int64) *qotcommon.KLine {
	return &qotcommon.KLine{
		// Time and IsBlank are proto2 "required" fields: the mock round-trips
		// through a real Marshal/Unmarshal over the wire, so leaving them
		// unset makes proto.Unmarshal fail on the client side.
		Time:      proto.String(time.Unix(int64(tsSec), 0).UTC().Format("2006-01-02 15:04:05")),
		IsBlank:   proto.Bool(false),
		Timestamp: proto.Float64(tsSec), OpenPrice: proto.Float64(c), HighPrice: proto.Float64(c),
		LowPrice: proto.Float64(c), ClosePrice: proto.Float64(c), Volume: proto.Int64(v),
	}
}

// liveClient dials the mock and returns a connected client (helper shared
// with Task 8's tests; reuse the ConnUp-waiting pattern from client_test.go).
func liveClient(t *testing.T, m *mockOpenD) *Client {
	t.Helper()
	c := New(Options{Addr: m.addr()})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.Run(ctx) }()
	waitForState(t, c, ConnUp)
	return c
}

func TestCachedBars1mDecodesAndClamps(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{bars1m: []*qotcommon.KLine{
		kl(1782146460, 309.1, 1000), // end-labeled: bucket = timestamp − 60 s
		kl(1782146520, 309.2, 1100),
	}})
	b := newBackfill(liveClient(t, m))
	bars, err := b.cachedBars1m(context.Background(), "US.AAPL", 5000) // clamped to 1000
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 || bars[0].BucketMs != 1782146460_000-60_000 {
		t.Fatalf("bars = %+v", bars)
	}
}

func TestRecentTicksDecodesAndClamps(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{ticks: []*qotcommon.Ticker{
		{Time: proto.String("2026-07-05 09:31:00"), Sequence: proto.Int64(1), Timestamp: proto.Float64(1782146460), Price: proto.Float64(309.1),
			Volume: proto.Int64(100), Turnover: proto.Float64(30910), Dir: proto.Int32(int32(qotcommon.TickerDirection_TickerDirection_Bid))},
	}})
	b := newBackfill(liveClient(t, m))
	ticks, err := b.recentTicks(context.Background(), "US.AAPL", 5000) // clamped to 1000
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 1 || ticks[0].Dir != feed.Buy || ticks[0].Price != 309.1 {
		t.Fatalf("ticks = %+v", ticks)
	}
}

func TestBookSnapshotDecodes(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{
		bids: []*qotcommon.OrderBook{{Price: proto.Float64(100), Volume: proto.Int64(10), OrederCount: proto.Int32(1)}},
		asks: []*qotcommon.OrderBook{{Price: proto.Float64(101), Volume: proto.Int64(20), OrederCount: proto.Int32(2)}},
	})
	b := newBackfill(liveClient(t, m))
	book, err := b.bookSnapshot(context.Background(), "US.AAPL")
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Bids) != 1 || len(book.Asks) != 1 || book.Bids[0].Price != 100 || book.Asks[0].Price != 101 {
		t.Fatalf("book = %+v", book)
	}
}

func TestQuoteSnapshotDecodes(t *testing.T) {
	m := newMockOpenD(t)
	sec := &qotcommon.Security{Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)), Code: proto.String("AAPL")}
	m.setData("US.AAPL", &qotData{basic: &qotcommon.BasicQot{
		Security: sec, IsSuspended: proto.Bool(false), ListTime: proto.String("1980-12-12"),
		PriceSpread: proto.Float64(0.01), UpdateTime: proto.String("2026-07-05 09:31:00"),
		HighPrice: proto.Float64(310), OpenPrice: proto.Float64(308), LowPrice: proto.Float64(307),
		CurPrice: proto.Float64(309.5), LastClosePrice: proto.Float64(305), Volume: proto.Int64(1_000_000),
		Turnover: proto.Float64(300_000_000), TurnoverRate: proto.Float64(1.5), Amplitude: proto.Float64(2.1),
		UpdateTimestamp: proto.Float64(1782146460),
	}})
	b := newBackfill(liveClient(t, m))
	q, err := b.quoteSnapshot(context.Background(), "US.AAPL")
	if err != nil {
		t.Fatal(err)
	}
	if q.Last != 309.5 || q.Symbol != "US.AAPL" {
		t.Fatalf("quote = %+v", q)
	}
}

func TestQuoteSnapshotErrorsWhenEmpty(t *testing.T) {
	m := newMockOpenD(t)
	b := newBackfill(liveClient(t, m))
	if _, err := b.quoteSnapshot(context.Background(), "US.AAPL"); err == nil {
		t.Fatal("expected error for missing basic quote")
	}
}

func TestHistoryBarsPaginates(t *testing.T) {
	m := newMockOpenD(t)
	var kls []*qotcommon.KLine
	for i := 0; i < 5; i++ {
		kls = append(kls, kl(1782146460+float64(60*i), 300+float64(i), 100))
	}
	m.setData("US.AAPL", &qotData{bars1m: kls, pageLen: 2}) // 3 pages: 2+2+1
	b := newBackfill(liveClient(t, m))
	from := time.UnixMilli(1782140000_000)
	to := time.UnixMilli(1782150000_000)
	bars, err := b.historyBars(context.Background(), "US.AAPL", feed.Res1m, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 5 {
		t.Fatalf("got %d bars across pages, want 5", len(bars))
	}
	// Three history requests were made, page 2 and 3 carrying NextReqKey.
	var histReqs []*qotrequesthistorykl.C2S
	for _, f := range m.snapshotRequests() {
		if f.ProtoID == ProtoQotRequestHistoryKL {
			var r qotrequesthistorykl.Request
			if err := proto.Unmarshal(f.Body, &r); err != nil {
				t.Fatal(err)
			}
			histReqs = append(histReqs, r.GetC2S())
		}
	}
	if len(histReqs) != 3 {
		t.Fatalf("history requests = %d, want 3", len(histReqs))
	}
	if histReqs[0].GetNextReqKey() != nil || histReqs[1].GetNextReqKey() == nil {
		t.Fatal("NextReqKey must be absent on page 1, present on page 2")
	}
	if !histReqs[0].GetExtendedTime() {
		t.Fatal("Res1m history must set ExtendedTime")
	}
	// Intraday (1m) history is unadjusted so it matches the raw tick/quote scale —
	// forward adjustment would scale pre-split bars up by the split ratio, corrupting
	// anything computed over a window straddling a reverse split (e.g. EMA 200).
	if got := qotcommon.RehabType(histReqs[0].GetRehabType()); got != qotcommon.RehabType_RehabType_None {
		t.Fatalf("RehabType = %v, want None (unadjusted)", got)
	}
}

// TestHistoryBarsDayResolutionSkipsExtendedTime verifies ResDay uses the bare
// date format and does not set ExtendedTime (API only supports it ≤60m).
func TestHistoryBarsDayResolutionSkipsExtendedTime(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{bars1m: []*qotcommon.KLine{
		kl(1782146460, 309.1, 1000),
	}})
	b := newBackfill(liveClient(t, m))
	from := time.UnixMilli(1782140000_000)
	to := time.UnixMilli(1782150000_000)
	if _, err := b.historyBars(context.Background(), "US.AAPL", feed.ResDay, from, to); err != nil {
		t.Fatal(err)
	}
	var histReqs []*qotrequesthistorykl.C2S
	for _, f := range m.snapshotRequests() {
		if f.ProtoID == ProtoQotRequestHistoryKL {
			var r qotrequesthistorykl.Request
			if err := proto.Unmarshal(f.Body, &r); err != nil {
				t.Fatal(err)
			}
			histReqs = append(histReqs, r.GetC2S())
		}
	}
	if len(histReqs) != 1 {
		t.Fatalf("history requests = %d, want 1", len(histReqs))
	}
	if histReqs[0].GetExtendedTime() {
		t.Fatal("ResDay history must not set ExtendedTime")
	}
	if _, err := time.Parse("2006-01-02", histReqs[0].GetBeginTime()); err != nil {
		t.Fatalf("BeginTime %q not in bare-date format: %v", histReqs[0].GetBeginTime(), err)
	}
	// Daily stays forward-adjusted (official auction prices, continuous across
	// splits/dividends) — only intraday drops rehab, see TestHistoryBarsPaginates.
	if got := qotcommon.RehabType(histReqs[0].GetRehabType()); got != qotcommon.RehabType_RehabType_Forward {
		t.Fatalf("RehabType = %v, want Forward", got)
	}
}

// TestHistoryBarsPageCapStopsAtMaxHistoryPages exercises the runaway-
// pagination backstop: a mock that always returns a NextReqKey must not be
// paged forever.
func TestHistoryBarsPageCapStopsAtMaxHistoryPages(t *testing.T) {
	m := newMockOpenD(t)
	m.handler = func(m *mockOpenD, conn net.Conn, f Frame) {
		switch f.ProtoID {
		case ProtoInitConnect, ProtoKeepAlive:
			m.defaultHandler(m, conn, f)
		case ProtoQotRequestHistoryKL:
			var req qotrequesthistorykl.Request
			_ = proto.Unmarshal(f.Body, &req)
			m.reply(conn, f, &qotrequesthistorykl.Response{RetType: proto.Int32(0),
				S2C: &qotrequesthistorykl.S2C{
					Security:   req.GetC2S().GetSecurity(),
					KlList:     []*qotcommon.KLine{kl(1782146460, 300, 100)},
					NextReqKey: []byte{1}, // always more pages available
				}})
		}
	}
	b := newBackfill(liveClient(t, m))
	from := time.UnixMilli(1782140000_000)
	to := time.UnixMilli(1782150000_000)
	bars, err := b.historyBars(context.Background(), "US.AAPL", feed.Res1m, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != maxHistoryPages {
		t.Fatalf("bars = %d, want %d (page cap)", len(bars), maxHistoryPages)
	}
}

func TestHistoryBarsRetTypeError(t *testing.T) {
	m := newMockOpenD(t)
	m.handler = func(m *mockOpenD, conn net.Conn, f Frame) {
		switch f.ProtoID {
		case ProtoInitConnect, ProtoKeepAlive:
			m.defaultHandler(m, conn, f)
		case ProtoQotRequestHistoryKL:
			m.reply(conn, f, &qotrequesthistorykl.Response{RetType: proto.Int32(-1), RetMsg: proto.String("boom")})
		}
	}
	b := newBackfill(liveClient(t, m))
	from := time.UnixMilli(1782140000_000)
	to := time.UnixMilli(1782150000_000)
	if _, err := b.historyBars(context.Background(), "US.AAPL", feed.Res1m, from, to); err == nil {
		t.Fatal("expected error on non-zero RetType")
	}
}

// TestMockPushToAllDeliversToLiveConn exercises the mock's conns-tracking
// infrastructure (Step 1 of this task): pushToAll fans out to every live
// connection recorded in handleConn.
func TestMockPushToAllDeliversToLiveConn(t *testing.T) {
	m := newMockOpenD(t)
	c := liveClient(t, m)
	sec := &qotcommon.Security{Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)), Code: proto.String("AAPL")}
	resp := &qotupdateticker.Response{RetType: proto.Int32(0), S2C: &qotupdateticker.S2C{Security: sec}}
	m.pushToAll(ProtoQotUpdateTicker, 999, resp)
	select {
	case f := <-c.Pushes():
		if f.ProtoID != ProtoQotUpdateTicker {
			t.Fatalf("protoID = %d, want %d", f.ProtoID, ProtoQotUpdateTicker)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("push not delivered within 2s")
	}
}

// TestMockDropAllConnsSeversConnection exercises dropAllConns (reconnect-test
// infrastructure staged for Task 8): every tracked connection is closed and
// the live client observes ConnDown.
func TestMockDropAllConnsSeversConnection(t *testing.T) {
	m := newMockOpenD(t)
	c := liveClient(t, m)
	m.dropAllConns()
	waitForState(t, c, ConnDown)
}

func TestHistoryQuota(t *testing.T) {
	m := newMockOpenD(t)
	b := newBackfill(liveClient(t, m))
	used, remain, err := b.historyQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if used != 3 || remain != 97 {
		t.Fatalf("quota = (%d,%d), want (3,97)", used, remain)
	}
}
