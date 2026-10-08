package opend

import (
	"bytes"
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetkl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateorderbook"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
	"google.golang.org/protobuf/proto"
)

func TestCapturePreservesOriginalListOrderPrecisionAndUnknownFields(t *testing.T) {
	report := &qotcommon.Ticker{Time: proto.String("2026-10-08 04:06:12.123"), Sequence: proto.Int64(9007199254740993), Dir: proto.Int32(0), Price: proto.Float64(1.9001), Volume: proto.Int64(17), Turnover: proto.Float64(32.3), Timestamp: proto.Float64(1791446772.123), HpVolume: proto.Float64(17.25)}
	report.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x07})
	second := proto.Clone(report).(*qotcommon.Ticker)
	second.Sequence = proto.Int64(7)
	body, err := proto.Marshal(&qotupdateticker.Response{RetType: proto.Int32(0), S2C: &qotupdateticker.S2C{Security: sec(11, "AIXI"), TickerList: []*qotcommon.Ticker{report, second}}})
	if err != nil {
		t.Fatal(err)
	}
	source := feed.SourceRef{Run: "run", Connection: 2, Ingress: 77, ReceiptMs: 1791446772999}
	rows, err := DecodeRecording(feed.Recording{Kind: "source", Source: source, Body: body, Data: feed.SourceMessage{Protocol: ProtoQotUpdateTicker, Origin: "push"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || !bytes.Equal(rows[0].Body, body) {
		t.Fatalf("raw source and reports = %v", rows)
	}
	if rows[1].Sequence != 9007199254740993 || rows[1].Price != 1.9001 || rows[1].Source.Index != 0 || rows[2].Source.Index != 1 || rows[2].Sequence != 7 {
		t.Fatalf("indexed observations changed: %+v", rows)
	}
	data, err := json.Marshal(rows[1].Data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"hpVolume":17.25`)) || bytes.Contains(data, []byte(`"recvTime"`)) {
		t.Fatalf("optional presence changed: %s", data)
	}
}

type captureCounter struct {
	sources     atomic.Uint64
	routingLoss atomic.Uint64
}

func (*captureCounter) RunID() string { return "capture-fixture" }
func (c *captureCounter) Record(r feed.Recording) bool {
	if r.Kind == "source" {
		c.sources.Add(1)
	}
	return true
}
func (c *captureCounter) Lost(l feed.RecordingLane, _ feed.SourceRef, n uint64) {
	if l == feed.RecordingRouting {
		c.routingLoss.Add(n)
	}
}

func TestCapturePrecedesRawPushQueueDrop(t *testing.T) {
	m := newMockOpenD(t)
	sink := &captureCounter{}
	c := New(Options{Addr: m.addr(), Recorder: sink})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitForState(t, c, ConnUp)
	msg := &qotupdateticker.Response{RetType: proto.Int32(0), S2C: &qotupdateticker.S2C{Security: sec(11, "AIXI")}}
	for i := range 1100 {
		m.pushToAll(ProtoQotUpdateTicker, uint32(i), msg)
	}
	deadline := time.Now().Add(3 * time.Second)
	for sink.sources.Load() < 1100 || sink.routingLoss.Load() < 76 {
		if time.Now().After(deadline) {
			t.Fatalf("captured frames=%d", sink.sources.Load())
		}
		time.Sleep(time.Millisecond)
	}
	if sink.routingLoss.Load() != 76 {
		t.Fatalf("routing drops=%d; source frames=%d", sink.routingLoss.Load(), sink.sources.Load())
	}
}

func TestCaptureBBOSeparateSideTimesAndNoDepthBody(t *testing.T) {
	body, err := proto.Marshal(&qotupdateorderbook.Response{RetType: proto.Int32(0), S2C: &qotupdateorderbook.S2C{Security: sec(11, "AIXI"), SvrRecvTimeBidTimestamp: proto.Float64(123.5), OrderBookBidList: []*qotcommon.OrderBook{{Price: proto.Float64(2.37), Volume: proto.Int64(10), OrederCount: proto.Int32(0)}, {Price: proto.Float64(2.36), Volume: proto.Int64(99), OrederCount: proto.Int32(1)}}}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := DecodeRecording(feed.Recording{Kind: "source", Body: body, Data: feed.SourceMessage{Protocol: ProtoQotUpdateOrderBook}})
	if err != nil || len(rows) != 2 {
		t.Fatalf("BBO rows=%v %v", rows, err)
	}
	if rows[0].Body != nil || rows[1].Body != nil {
		t.Fatal("full depth body retained")
	}
	bbo, ok := rows[1].Data.(recordedBBO)
	if !ok || bbo.Ask != nil || bbo.AskServerTimestamp != nil || bbo.BidServerTimestamp == nil || *bbo.BidServerTimestamp != 123.5 || *bbo.Bid.Price != 2.37 || bbo.Bid.Orders == nil || *bbo.Bid.Orders != 0 {
		t.Fatalf("side presence/clock/top changed: %+v", bbo)
	}
}

func TestCaptureUnmatchedMinuteReplyRemainsUnclassified(t *testing.T) {
	body, err := proto.Marshal(&qotgetkl.Response{RetType: proto.Int32(0), S2C: &qotgetkl.S2C{Security: sec(11, "AIXI")}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := DecodeRecording(feed.Recording{Kind: "source", Body: body, Data: feed.SourceMessage{Protocol: ProtoQotGetKL, Origin: "unmatched"}})
	if err == nil || len(rows) != 1 || !bytes.Equal(rows[0].Body, body) {
		t.Fatalf("unmatched timeframe guessed: %v %v", rows, err)
	}
	rows, err = DecodeRecording(feed.Recording{Kind: "source", Body: body, Data: feed.SourceMessage{Protocol: ProtoQotGetKL, Matched: true, Timeframe: int32(qotcommon.KLType_KLType_Day)}})
	if err != nil || len(rows) != 0 {
		t.Fatalf("daily cache retained as minute: %v %v", rows, err)
	}
}
