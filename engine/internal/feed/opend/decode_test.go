package opend

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdatebasicqot"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdatekl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateorderbook"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
)

func sec(market int32, code string) *qotcommon.Security {
	return &qotcommon.Security{Market: proto.Int32(market), Code: proto.String(code)}
}

func TestSymbolRoundTrip(t *testing.T) {
	cases := []struct {
		in     string
		market int32
		code   string
		out    string
	}{
		{"US.AAPL", 11, "AAPL", "US.AAPL"},
		{"AAPL", 11, "AAPL", "US.AAPL"}, // bare defaults to US, canonicalizes
		{"HK.00700", 1, "00700", "HK.00700"},
		{"CC.BTC", 91, "BTC", "CC.BTC"},
	}
	for _, c := range cases {
		s, err := parseSymbol(c.in)
		if err != nil {
			t.Fatalf("parseSymbol(%q): %v", c.in, err)
		}
		if s.GetMarket() != c.market || s.GetCode() != c.code {
			t.Errorf("parseSymbol(%q) = (%d,%q), want (%d,%q)", c.in, s.GetMarket(), s.GetCode(), c.market, c.code)
		}
		if got := formatSymbol(s); got != c.out {
			t.Errorf("formatSymbol(parseSymbol(%q)) = %q, want %q", c.in, got, c.out)
		}
	}
	if _, err := parseSymbol("XX.FOO"); err == nil {
		t.Error("unknown market prefix must error")
	}
}

func TestDecodeKLineNormalizesEndLabel(t *testing.T) {
	// Verified live 2026-07-05: intraday K-lines label the bucket END
	// (US.AAPL RTH = 390 bars 09:31:00..16:00:00). 2026-07-02 16:00:00 ET
	// = 2026-07-02T20:00:00Z = epoch 1783022400 → the bar COVERS
	// 15:59–16:00, bucket start 15:59.
	k := &qotcommon.KLine{
		Timestamp: proto.Float64(1783022400), Time: proto.String("2026-07-02 16:00:00"),
		OpenPrice: proto.Float64(308.75), HighPrice: proto.Float64(309.2),
		LowPrice: proto.Float64(308.5), ClosePrice: proto.Float64(308.63),
		Volume: proto.Int64(12551689), Turnover: proto.Float64(3.87e9),
	}
	b, err := decodeKLine("US.AAPL", k, feed.Res1m)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(1783022400_000 - 60_000); b.BucketMs != want {
		t.Fatalf("1m BucketMs = %d, want %d (end label minus one span)", b.BucketMs, want)
	}
	if b.O != 308.75 || b.C != 308.63 || b.Volume != 12551689 {
		t.Fatalf("bar values wrong: %+v", b)
	}

	day, err := decodeKLine("US.AAPL", &qotcommon.KLine{
		Timestamp: proto.Float64(1782964800), // 2026-07-02 00:00:00 ET (= 04:00Z)
		OpenPrice: proto.Float64(1), HighPrice: proto.Float64(1), LowPrice: proto.Float64(1), ClosePrice: proto.Float64(1),
	}, feed.ResDay)
	if err != nil {
		t.Fatal(err)
	}
	if day.BucketMs != 1782964800_000 {
		t.Fatalf("daily BucketMs shifted: %d", day.BucketMs) // day-labeled: no shift
	}

	if _, err := decodeKLine("US.AAPL", &qotcommon.KLine{}, feed.Res1m); err == nil {
		t.Fatal("zero Timestamp must be a decode error, not a guess")
	}
}

func TestDecodePushTicker(t *testing.T) {
	resp := &qotupdateticker.Response{
		RetType: proto.Int32(0),
		S2C: &qotupdateticker.S2C{
			Security: sec(11, "AAPL"),
			TickerList: []*qotcommon.Ticker{{
				Time:     proto.String("2026-07-02 12:33:20"),
				Sequence: proto.Int64(7001), Timestamp: proto.Float64(1782146000.123),
				Price: proto.Float64(309.10), Volume: proto.Int64(200), Turnover: proto.Float64(61820),
				Dir: proto.Int32(int32(qotcommon.TickerDirection_TickerDirection_Bid)),
			}, {
				Time:     proto.String("2026-07-02 12:33:20"),
				Sequence: proto.Int64(7002), Timestamp: proto.Float64(1782146000.500),
				Price: proto.Float64(309.05), Volume: proto.Int64(100), Turnover: proto.Float64(30905),
				Dir: proto.Int32(int32(qotcommon.TickerDirection_TickerDirection_Ask)),
			}},
		},
	}
	body, _ := proto.Marshal(resp)
	evs, err := DecodePush(Frame{ProtoID: ProtoQotUpdateTicker, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	te, ok := evs[0].(feed.TicksEvent)
	if !ok || len(te.Ticks) != 2 {
		t.Fatalf("got %#v, want TicksEvent with 2 ticks", evs)
	}
	t0 := te.Ticks[0]
	if t0.Symbol != "US.AAPL" || t0.Seq != 7001 || t0.TsMs != 1782146000123 || t0.Dir != feed.Buy {
		t.Fatalf("tick[0] = %+v", t0)
	}
	if te.Ticks[1].Dir != feed.Sell {
		t.Fatalf("tick[1].Dir = %v, want Sell", te.Ticks[1].Dir)
	}
}

// TestDecodePushTickerNoSecurity covers the case with an actually-reachable
// nil Security: s2c itself is "optional" at the Response level, so a
// malformed push can omit it entirely. (Security is "required" *within*
// S2C, so proto2 unmarshal itself rejects any wire message that has S2C
// present with a missing Security — that combination never reaches our
// code. The s2c-absent case is the real gap: before this fix, DecodePush
// silently returned (nil, nil) here instead of counting a decode failure.)
func TestDecodePushTickerNoSecurity(t *testing.T) {
	resp := &qotupdateticker.Response{RetType: proto.Int32(0)} // S2C omitted entirely
	body, _ := proto.Marshal(resp)
	evs, err := DecodePush(Frame{ProtoID: ProtoQotUpdateTicker, Body: body})
	if err == nil {
		t.Fatalf("ticker push without security must be a decode error, got evs=%v", evs)
	}
}

func TestDecodePushBookUsesTypoField(t *testing.T) {
	resp := &qotupdateorderbook.Response{
		RetType: proto.Int32(0),
		S2C: &qotupdateorderbook.S2C{
			Security: sec(11, "AAPL"),
			OrderBookBidList: []*qotcommon.OrderBook{
				{Price: proto.Float64(309.00), Volume: proto.Int64(500), OrederCount: proto.Int32(4)},
			},
			OrderBookAskList: []*qotcommon.OrderBook{
				{Price: proto.Float64(309.02), Volume: proto.Int64(300), OrederCount: proto.Int32(2)},
			},
			SvrRecvTimeBidTimestamp: proto.Float64(1782146001.0),
			SvrRecvTimeAskTimestamp: proto.Float64(1782146001.5),
		},
	}
	body, _ := proto.Marshal(resp)
	evs, err := DecodePush(Frame{ProtoID: ProtoQotUpdateOrderBook, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	be := evs[0].(feed.BookEvent)
	if be.Book.Bids[0].Orders != 4 || be.Book.Asks[0].Volume != 300 {
		t.Fatalf("book = %+v", be.Book)
	}
	if be.Book.TsMs != 1782146001500 { // max(bid, ask) server recv time
		t.Fatalf("book TsMs = %d", be.Book.TsMs)
	}
}

func TestDecodePushBasicQot(t *testing.T) {
	resp := &qotupdatebasicqot.Response{
		RetType: proto.Int32(0),
		S2C: &qotupdatebasicqot.S2C{
			BasicQotList: []*qotcommon.BasicQot{{
				Security:        sec(11, "AAPL"),
				IsSuspended:     proto.Bool(false),
				ListTime:        proto.String("1980-12-12"),
				PriceSpread:     proto.Float64(0.01),
				UpdateTime:      proto.String("2026-07-02 12:33:20"),
				UpdateTimestamp: proto.Float64(1782146000.0),
				HighPrice:       proto.Float64(310.0),
				OpenPrice:       proto.Float64(305.0),
				LowPrice:        proto.Float64(304.5),
				CurPrice:        proto.Float64(309.1),
				LastClosePrice:  proto.Float64(300.0),
				Volume:          proto.Int64(1_000_000),
				Turnover:        proto.Float64(3.09e8),
				TurnoverRate:    proto.Float64(1.2),
				Amplitude:       proto.Float64(1.8),
			}},
		},
	}
	body, _ := proto.Marshal(resp)
	evs, err := DecodePush(Frame{ProtoID: ProtoQotUpdateBasicQot, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	qe, ok := evs[0].(feed.QuoteEvent)
	if !ok {
		t.Fatalf("got %#v, want QuoteEvent", evs)
	}
	q := qe.Quote
	if q.Symbol != "US.AAPL" || q.TsMs != 1782146000000 || q.Last != 309.1 ||
		q.Open != 305.0 || q.High != 310.0 || q.Low != 304.5 ||
		q.PrevClose != 300.0 || q.Volume != 1_000_000 || q.Turnover != 3.09e8 {
		t.Fatalf("quote = %+v", q)
	}
}

func TestDecodeBasicQotNoSecurity(t *testing.T) {
	if _, err := decodeBasicQot(&qotcommon.BasicQot{}); err == nil {
		t.Fatal("BasicQot without Security must be a decode error")
	}
}

func TestDecodePushErrors(t *testing.T) {
	t.Run("non-zero RetType", func(t *testing.T) {
		resp := &qotupdateticker.Response{
			RetType: proto.Int32(1),
			RetMsg:  proto.String("some error"),
		}
		body, _ := proto.Marshal(resp)
		if _, err := DecodePush(Frame{ProtoID: ProtoQotUpdateTicker, Body: body}); err == nil {
			t.Fatal("non-zero RetType must return an error")
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		garbage := []byte{0xff, 0x00, 0xff, 0x00, 0xff}
		evs, err := DecodePush(Frame{ProtoID: ProtoQotUpdateTicker, Body: garbage})
		if err == nil {
			t.Fatalf("malformed body must return an error, got evs=%v", evs)
		}
	})
}

func TestDecodePushKLFiltersNon1m(t *testing.T) {
	mk := func(klType qotcommon.KLType) []byte {
		resp := &qotupdatekl.Response{
			RetType: proto.Int32(0),
			S2C: &qotupdatekl.S2C{
				RehabType: proto.Int32(int32(qotcommon.RehabType_RehabType_Forward)),
				KlType:    proto.Int32(int32(klType)), Security: sec(11, "AAPL"),
				KlList: []*qotcommon.KLine{{
					Time: proto.String("2026-07-02 12:41:00"), IsBlank: proto.Bool(false),
					Timestamp: proto.Float64(1782146460), OpenPrice: proto.Float64(1),
					HighPrice: proto.Float64(1), LowPrice: proto.Float64(1), ClosePrice: proto.Float64(1),
				}},
			},
		}
		b, _ := proto.Marshal(resp)
		return b
	}
	evs, err := DecodePush(Frame{ProtoID: ProtoQotUpdateKL, Body: mk(qotcommon.KLType_KLType_1Min)})
	if err != nil || len(evs) != 1 {
		t.Fatalf("1m push: evs=%v err=%v", evs, err)
	}
	evs, err = DecodePush(Frame{ProtoID: ProtoQotUpdateKL, Body: mk(qotcommon.KLType_KLType_Day)})
	if err != nil || len(evs) != 0 {
		t.Fatalf("non-1m KL push must be ignored: evs=%v err=%v", evs, err)
	}
	// Unknown push IDs and RT pushes are ignored, not errors.
	if evs, err := DecodePush(Frame{ProtoID: ProtoQotUpdateRT, Body: nil}); err != nil || evs != nil {
		t.Fatalf("RT push: evs=%v err=%v", evs, err)
	}
}
