package scan

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/ssr"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
	staticpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetstaticinfo"
	tmrpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgettopmoversrank"
	ahpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusafterhoursrank"
	onpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusovernightrank"
	rankpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetuspremarketrank"
	filterpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotstockfilter"
)

func TestRankRowsThresholds(t *testing.T) {
	cfg := config.Scan{MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000}
	floats := map[string]floatEntry{"US.LOWF": {shares: 20_000_000}, "US.BIGF": {shares: 500_000_000}}
	items := []rankItem{
		{Symbol: "US.LOWF", ChangePct: 12.5, Last: 4.2, Volume: 300_000}, // passes
		{Symbol: "US.BIGF", ChangePct: 20.0, Last: 8.0, Volume: 900_000}, // fails float cap
		{Symbol: "US.THIN", ChangePct: 30.0, Last: 1.0, Volume: 5_000},   // fails volume floor
		{Symbol: "US.FLAT", ChangePct: 1.0, Last: 2.0, Volume: 500_000},  // fails change threshold
	}
	rows := rankRows(items, floats, cfg)
	if len(rows) != 1 || rows[0].Symbol != "US.LOWF" {
		t.Fatalf("only US.LOWF should pass all thresholds, got %+v", rows)
	}
	if rows[0].FloatShares == nil || *rows[0].FloatShares != 20_000_000 {
		t.Fatalf("float should be actual shares from cache: %+v", rows[0])
	}
	if rows[0].ChangePct == nil || *rows[0].ChangePct != 12.5 {
		t.Fatalf("changePct wrong: %+v", rows[0])
	}
}

func TestMostActiveIgnoresChangeThreshold(t *testing.T) {
	f := Defaults(config.Scan{})
	f.Mode, f.MinChangePct, f.MinVolume = "most_active", 50, 100
	rows := rankRowsFiltered([]rankItem{{Symbol: "US.A", ChangePct: 1, Volume: 101}, {Symbol: "US.B", ChangePct: 99, Volume: 99}}, nil, f)
	if len(rows) != 1 || rows[0].Symbol != "US.A" {
		t.Fatalf("got %+v", rows)
	}
}

type requesterFunc func(context.Context, uint32, proto.Message) (opend.Frame, error)

func (f requesterFunc) Request(ctx context.Context, id uint32, req proto.Message) (opend.Frame, error) {
	return f(ctx, id, req)
}

func TestMostActiveExtendedMergesDeduplicatesAndSorts(t *testing.T) {
	call := 0
	r := requesterFunc(func(_ context.Context, id uint32, req proto.Message) (opend.Frame, error) {
		if id != opend.ProtoQotGetUSPreMarketRank {
			t.Fatalf("proto=%d", id)
		}
		call++
		if call == 2 {
			return frameOf(&rankpb.Response{RetType: proto.Int32(0), S2C: &rankpb.S2C{DataList: []*rankpb.PreMarketRankItem{
				{Security: usSec("A"), PreMarketVolume: proto.Int64(300)}, {Security: usSec("C"), PreMarketVolume: proto.Int64(200)},
			}}}), nil
		}
		return frameOf(&rankpb.Response{RetType: proto.Int32(0), S2C: &rankpb.S2C{DataList: []*rankpb.PreMarketRankItem{
			{Security: usSec("A"), PreMarketVolume: proto.Int64(100)}, {Security: usSec("B"), PreMarketVolume: proto.Int64(400)},
		}}}), nil
	})
	p := New(config.Scan{}, r, nil, clock.System{}, nil, nil)
	got, err := p.fetchRank(context.Background(), session.PreMarket, "most_active")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Symbol != "US.B" || got[1].Symbol != "US.A" || got[1].Volume != 300 || got[2].Symbol != "US.C" {
		t.Fatalf("got %+v", got)
	}
}

func TestMostActiveExtendedRejectsHalfBoard(t *testing.T) {
	call := 0
	r := requesterFunc(func(_ context.Context, _ uint32, _ proto.Message) (opend.Frame, error) {
		call++
		if call == 2 {
			return opend.Frame{}, fmt.Errorf("losers failed")
		}
		return frameOf(&rankpb.Response{RetType: proto.Int32(0), S2C: &rankpb.S2C{}}), nil
	})
	p := New(config.Scan{}, r, nil, clock.System{}, nil, nil)
	if _, err := p.fetchRank(context.Background(), session.PreMarket, "most_active"); err == nil {
		t.Fatal("expected error")
	}
}

func TestMostActiveRTHUsesVolumeSortedStockFilter(t *testing.T) {
	r := requesterFunc(func(_ context.Context, id uint32, msg proto.Message) (opend.Frame, error) {
		if id != opend.ProtoQotStockFilter {
			t.Fatalf("proto=%d", id)
		}
		c := msg.(*filterpb.Request).GetC2S()
		if c.GetBegin() != 0 || c.GetNum() != 200 || len(c.GetAccumulateFilterList()) != 2 {
			t.Fatalf("request=%+v", c)
		}
		if v := c.GetAccumulateFilterList()[0]; v.GetFieldName() != int32(filterpb.AccumulateField_AccumulateField_Volume) || v.GetDays() != 1 || v.GetSortDir() != int32(filterpb.SortDir_SortDir_Descend) {
			t.Fatalf("volume filter=%+v", v)
		}
		return frameOf(&filterpb.Response{RetType: proto.Int32(0), S2C: &filterpb.S2C{LastPage: proto.Bool(true), AllCount: proto.Int32(1), DataList: []*filterpb.StockData{{
			Security: usSec("A"), Name: proto.String("A"), BaseDataList: []*filterpb.BaseData{{FieldName: proto.Int32(int32(filterpb.StockField_StockField_CurPrice)), Value: proto.Float64(12.5)}},
			AccumulateDataList: []*filterpb.AccumulateData{{FieldName: proto.Int32(int32(filterpb.AccumulateField_AccumulateField_Volume)), Value: proto.Float64(1234), Days: proto.Int32(1)}, {FieldName: proto.Int32(int32(filterpb.AccumulateField_AccumulateField_ChangeRate)), Value: proto.Float64(4.5), Days: proto.Int32(1)}},
		}}}}), nil
	})
	p := New(config.Scan{}, r, nil, clock.System{}, nil, nil)
	got, err := p.fetchRank(context.Background(), session.RTH, "most_active")
	if err != nil || len(got) != 1 || got[0] != (rankItem{Symbol: "US.A", Last: 12.5, ChangePct: 4.5, Volume: 1234}) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestRankRowsThreeStateFloat(t *testing.T) {
	floats := map[string]floatEntry{
		"US.UNDER": {shares: 20_000_000},
		"US.OVER":  {shares: 500_000_000},
		"US.BAD":   {bad: true},
		// US.ABSENT intentionally not in the cache.
	}
	items := []rankItem{
		{Symbol: "US.UNDER", ChangePct: 12, Last: 4, Volume: 300_000},
		{Symbol: "US.OVER", ChangePct: 20, Last: 8, Volume: 900_000},
		{Symbol: "US.BAD", ChangePct: 15, Last: 3, Volume: 400_000},
		{Symbol: "US.ABSENT", ChangePct: 11, Last: 2, Volume: 250_000},
	}

	// Cap ON: OVER (known over cap) and BAD dropped; UNDER shows float; ABSENT kept, blank.
	withCap := rankRows(items, floats, config.Scan{MinChangePct: 5, MaxFloatShares: 50_000_000})
	gotCap := map[string]*float64{}
	for _, r := range withCap {
		gotCap[r.Symbol] = r.FloatShares
	}
	if len(withCap) != 2 {
		t.Fatalf("cap on: want 2 rows (UNDER, ABSENT), got %d: %+v", len(withCap), withCap)
	}
	if f := gotCap["US.UNDER"]; f == nil || *f != 20_000_000 {
		t.Fatalf("UNDER float wrong: %+v", gotCap["US.UNDER"])
	}
	if f, ok := gotCap["US.ABSENT"]; !ok || f != nil {
		t.Fatalf("ABSENT must be present with nil float: ok=%v f=%v", ok, f)
	}

	// Cap OFF: nothing dropped for float; BAD shown blank, OVER shown with its float.
	noCap := rankRows(items, floats, config.Scan{MinChangePct: 5, MaxFloatShares: 0})
	got := map[string]*float64{}
	for _, r := range noCap {
		got[r.Symbol] = r.FloatShares
	}
	if len(noCap) != 4 {
		t.Fatalf("cap off: want all 4 rows, got %d: %+v", len(noCap), noCap)
	}
	if f := got["US.OVER"]; f == nil || *f != 500_000_000 {
		t.Fatalf("OVER float should show when cap off: %+v", got["US.OVER"])
	}
	if got["US.BAD"] != nil {
		t.Fatalf("BAD float must be blank (nil): %+v", got["US.BAD"])
	}
}

func TestDropOTC(t *testing.T) {
	otc := map[string]bool{"US.PINK": true, "US.LISTED": false}
	items := []rankItem{
		{Symbol: "US.PINK"},   // confirmed OTC: dropped
		{Symbol: "US.LISTED"}, // confirmed listed: kept
		{Symbol: "US.NEW"},    // unresolved (absent): kept, not assumed OTC
	}
	out := dropOTC(items, otc)
	var syms []string
	for _, it := range out {
		syms = append(syms, it.Symbol)
	}
	if !reflect.DeepEqual(syms, []string{"US.LISTED", "US.NEW"}) {
		t.Fatalf("dropOTC = %v, want [US.LISTED US.NEW] (only confirmed OTC dropped)", syms)
	}
}

func TestResetIfNewDayClearsFloatCacheAndSeen(t *testing.T) {
	p := &Poller{
		floats:  map[string]floatEntry{"US.A": {shares: 1}},
		otc:     map[string]bool{"US.A": false},
		seen:    map[string]map[string]bool{"premarket": {"US.A": true}},
		seenDay: session.DayMs(time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC).UnixMilli()),
	}
	p.resetIfNewDay(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)) // different ET day
	if len(p.floats) != 0 {
		t.Fatalf("float cache should clear on new day: %+v", p.floats)
	}
	if len(p.otc) != 0 {
		t.Fatalf("exchange-type cache should clear on new day: %+v", p.otc)
	}
	if len(p.seen) != 0 {
		t.Fatalf("seen-sets should clear on new day: %+v", p.seen)
	}
}

func TestNewHitsSeenSet(t *testing.T) {
	p := &Poller{seen: map[string]map[string]bool{}}
	// First populated poll for a session is a silent baseline: no hits, seed only.
	first := p.newHits("premarket", []wsmsg.ScannerRow{{Symbol: "US.A"}, {Symbol: "US.B"}})
	if len(first) != 0 {
		t.Fatalf("first poll is a silent baseline, want 0 hits, got %v", first)
	}
	// Genuinely-new symbols after the baseline do fire.
	second := p.newHits("premarket", []wsmsg.ScannerRow{{Symbol: "US.A"}, {Symbol: "US.C"}})
	if len(second) != 1 || second[0] != "US.C" {
		t.Fatalf("second pass: only US.C is new, got %v", second)
	}
}

// fakeReq implements the scan.requester interface with canned responses.
type fakeReq struct {
	rankResp     *rankpb.Response // 3410 pre-market
	topMoversRsp *tmrpb.Response  // 3413 RTH
	afterHrsRsp  *ahpb.Response   // 3411 post-market
	overnightRsp *onpb.Response   // 3412 overnight
	rankErr      error
	topErr       error
	preCalls     int
	topCalls     int
	snap         func(codes []string) (*snappb.Response, error)
	snapCalls    int
	// staticInfo answers 3202 (exchange type). nil => every code resolves as
	// listed (empty response), matching tests that don't care about OTC.
	staticInfo  func(codes []string) (*staticpb.Response, error)
	staticCalls int
}

func (f *fakeReq) Request(_ context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	switch protoID {
	case opend.ProtoQotGetUSPreMarketRank:
		f.preCalls++
		if f.rankErr != nil {
			return opend.Frame{}, f.rankErr
		}
		return frameOf(f.rankResp), nil
	case opend.ProtoQotGetTopMoversRank:
		f.topCalls++
		if f.topErr != nil {
			return opend.Frame{}, f.topErr
		}
		return frameOf(f.topMoversRsp), nil
	case opend.ProtoQotGetUSAfterHoursRank:
		return frameOf(f.afterHrsRsp), nil
	case opend.ProtoQotGetUSOvernightRank:
		return frameOf(f.overnightRsp), nil
	case opend.ProtoQotGetStaticInfo:
		f.staticCalls++
		if f.staticInfo == nil {
			return frameOf(&staticpb.Response{RetType: proto.Int32(0), S2C: &staticpb.S2C{}}), nil
		}
		var codes []string
		for _, s := range req.(*staticpb.Request).GetC2S().GetSecurityList() {
			codes = append(codes, s.GetCode())
		}
		resp, err := f.staticInfo(codes)
		if err != nil {
			return opend.Frame{}, err
		}
		return frameOf(resp), nil
	case opend.ProtoQotGetSecuritySnapshot:
		f.snapCalls++
		var codes []string
		for _, s := range req.(*snappb.Request).GetC2S().GetSecurityList() {
			codes = append(codes, s.GetCode())
		}
		resp, err := f.snap(codes)
		if err != nil {
			return opend.Frame{}, err
		}
		return frameOf(resp), nil
	default:
		return opend.Frame{}, fmt.Errorf("unexpected protoID %d", protoID)
	}
}

func frameOf(m proto.Message) opend.Frame {
	b, _ := proto.Marshal(m)
	return opend.Frame{Body: b}
}

// capturePub records published scanner payloads.
type capturePub struct {
	ranks []wsmsg.ScannerRankPayload
	hits  []wsmsg.ScanHitPayload
}

func (c *capturePub) Publish(topic wsmsg.Topic, _ string, payload any) {
	switch topic {
	case wsmsg.TopicScannerRank:
		c.ranks = append(c.ranks, payload.(wsmsg.ScannerRankPayload))
	case wsmsg.TopicScannerHit:
		c.hits = append(c.hits, payload.(wsmsg.ScanHitPayload))
	}
}

func usSec(code string) *qotcommon.Security {
	return &qotcommon.Security{
		Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
		Code:   proto.String(code),
	}
}

// snapshotBasic fills every required SnapshotBasicData field (dummy values).
func snapshotBasic(code string) *snappb.SnapshotBasicData {
	return &snappb.SnapshotBasicData{
		Security:       usSec(code),
		Type:           proto.Int32(3),
		IsSuspend:      proto.Bool(false),
		ListTime:       proto.String("2020-01-01"),
		LotSize:        proto.Int32(1),
		PriceSpread:    proto.Float64(0.01),
		UpdateTime:     proto.String("2026-07-08 04:00:00"),
		HighPrice:      proto.Float64(1),
		OpenPrice:      proto.Float64(1),
		LowPrice:       proto.Float64(1),
		LastClosePrice: proto.Float64(1),
		CurPrice:       proto.Float64(1),
		Volume:         proto.Int64(0),
		Turnover:       proto.Float64(0),
		TurnoverRate:   proto.Float64(0),
	}
}

// equityEx fills every required EquitySnapshotExData field; only
// OutstandingShares carries meaning.
func equityEx(outstanding int64) *snappb.EquitySnapshotExData {
	return &snappb.EquitySnapshotExData{
		IssuedShares:         proto.Int64(outstanding * 2),
		IssuedMarketVal:      proto.Float64(0),
		NetAsset:             proto.Float64(0),
		NetProfit:            proto.Float64(0),
		EarningsPershare:     proto.Float64(0),
		OutstandingShares:    proto.Int64(outstanding),
		OutstandingMarketVal: proto.Float64(0),
		NetAssetPershare:     proto.Float64(0),
		EyRate:               proto.Float64(0),
		PeRate:               proto.Float64(0),
		PbRate:               proto.Float64(0),
		PeTTMRate:            proto.Float64(0),
	}
}

// snap builds a Snapshot. equity=false => no EquityExData (ETF/preferred);
// outstanding<=0 with equity=true => zero-float. Both are "bad".
func snap(code string, outstanding int64, equity bool) *snappb.Snapshot {
	s := &snappb.Snapshot{Basic: snapshotBasic(code)}
	if equity {
		s.EquityExData = equityEx(outstanding)
	}
	return s
}

func marketSnap(code string, outstanding int64, last, close float64, volume int64) *snappb.Snapshot {
	s := snap(code, outstanding, true)
	s.Basic.CurPrice = proto.Float64(last)
	s.Basic.LastClosePrice = proto.Float64(close)
	s.Basic.Volume = proto.Int64(volume)
	return s
}

func extendedSnap(code string, phase session.Phase, price, change float64, volume int64) *snappb.Snapshot {
	s := marketSnap(code, 1, 90, 80, 999)
	d := &qotcommon.PreAfterMarketData{Price: proto.Float64(price), ChangeRate: proto.Float64(change), Volume: proto.Int64(volume)}
	switch phase {
	case session.PostMarket:
		s.Basic.AfterMarket = d
	case session.Overnight:
		s.Basic.Overnight = d
	default:
		s.Basic.PreMarket = d
	}
	return s
}

func snapResp(snaps ...*snappb.Snapshot) *snappb.Response {
	return &snappb.Response{RetType: proto.Int32(0), S2C: &snappb.S2C{SnapshotList: snaps}}
}

func snapErrResp(msg string) *snappb.Response {
	return &snappb.Response{RetType: proto.Int32(1), RetMsg: proto.String(msg)}
}

// staticBasic fills every required SecurityStaticBasic field (dummy values);
// exchType is the field under test (ExchType_ExchType_US_Pink = OTC).
func staticBasic(code string, exchType int32) *qotcommon.SecurityStaticBasic {
	return &qotcommon.SecurityStaticBasic{
		Security: usSec(code),
		Id:       proto.Int64(1),
		LotSize:  proto.Int32(1),
		SecType:  proto.Int32(int32(qotcommon.SecurityType_SecurityType_Eqty)),
		Name:     proto.String(code),
		ListTime: proto.String("2020-01-01"),
		ExchType: proto.Int32(exchType),
	}
}

func staticInfoOf(code string, exchType int32) *qotcommon.SecurityStaticInfo {
	return &qotcommon.SecurityStaticInfo{Basic: staticBasic(code, exchType)}
}

func staticInfoResp(infos ...*qotcommon.SecurityStaticInfo) *staticpb.Response {
	return &staticpb.Response{RetType: proto.Int32(0), S2C: &staticpb.S2C{StaticInfoList: infos}}
}

func staticErrResp(msg string) *staticpb.Response {
	return &staticpb.Response{RetType: proto.Int32(1), RetMsg: proto.String(msg)}
}

func rankResp(items ...rankItem) *rankpb.Response {
	var data []*rankpb.PreMarketRankItem
	for _, it := range items {
		data = append(data, &rankpb.PreMarketRankItem{
			Security:             usSec(codeOf(it.Symbol)),
			PreMarketChangeRatio: proto.Float64(it.ChangePct),
			PreMarketPrice:       proto.Float64(it.Last),
			PreMarketVolume:      proto.Int64(it.Volume),
		})
	}
	return &rankpb.Response{RetType: proto.Int32(0), S2C: &rankpb.S2C{DataList: data}}
}

func topResp(items ...rankItem) *tmrpb.Response {
	data := make([]*tmrpb.TopMoversRankItem, 0, len(items))
	for _, it := range items {
		data = append(data, &tmrpb.TopMoversRankItem{Security: usSec(codeOf(it.Symbol)), ChangeRatio: proto.Float64(it.ChangePct), CurPrice: proto.Float64(it.Last), Volume: proto.Int64(it.Volume)})
	}
	return &tmrpb.Response{RetType: proto.Int32(0), S2C: &tmrpb.S2C{DataList: data}}
}

func newTestPoller(cfg config.Scan, fr *fakeReq, pub *capturePub) *Poller {
	return New(cfg, fr, pub, clock.NewFake(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)), nil, nil)
}

func TestResolveFloatsClassifiesKnownAndBad(t *testing.T) {
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		// KNOWN -> real float; NOEQ -> no equity data; ZERO -> zero float;
		// OMIT -> requested but absent from the response.
		return snapResp(
			snap("KNOWN", 15_000_000, true),
			snap("NOEQ", 0, false),
			snap("ZERO", 0, true),
		), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	items := []rankItem{{Symbol: "US.KNOWN"}, {Symbol: "US.NOEQ"}, {Symbol: "US.ZERO"}, {Symbol: "US.OMIT"}}
	p.resolveFloats(context.Background(), items)
	if e := p.floats["US.KNOWN"]; e.bad || e.shares != 15_000_000 {
		t.Fatalf("KNOWN should resolve to 15M: %+v", e)
	}
	for _, s := range []string{"US.NOEQ", "US.ZERO", "US.OMIT"} {
		if e, ok := p.floats[s]; !ok || !e.bad {
			t.Fatalf("%s should be marked bad: %+v ok=%v", s, e, ok)
		}
	}
}

func TestResolveFloatsTransportErrorLeavesAbsent(t *testing.T) {
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		return nil, fmt.Errorf("dial tcp: connection refused")
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	p.resolveFloats(context.Background(), []rankItem{{Symbol: "US.A"}})
	if _, ok := p.floats["US.A"]; ok {
		t.Fatalf("transport error must leave the symbol absent, not cached")
	}
}

func TestResolveFloatsSplitRetryIsolatesBadCode(t *testing.T) {
	// Any batch containing BAD errors as a whole until BAD is alone.
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		for _, c := range codes {
			if c == "BAD" {
				return snapErrResp("US OTC market quote is not available"), nil
			}
		}
		snaps := make([]*snappb.Snapshot, 0, len(codes))
		for _, c := range codes {
			snaps = append(snaps, snap(c, 10_000_000, true))
		}
		return snapResp(snaps...), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	items := []rankItem{{Symbol: "US.A"}, {Symbol: "US.B"}, {Symbol: "US.BAD"}, {Symbol: "US.C"}}
	p.resolveFloats(context.Background(), items)
	if e := p.floats["US.BAD"]; !e.bad {
		t.Fatalf("US.BAD should be isolated and marked bad: %+v", e)
	}
	for _, s := range []string{"US.A", "US.B", "US.C"} {
		if e := p.floats[s]; e.bad || e.shares != 10_000_000 {
			t.Fatalf("%s should resolve to 10M: %+v", s, e)
		}
	}
}

func TestResolveFloatsRequestCap(t *testing.T) {
	// Every batch fails as a whole -> pathological split explosion; must stop
	// at maxSnapshotReqs requests, leaving the rest absent.
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		return snapErrResp("all bad"), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	var items []rankItem
	for i := 0; i < 35; i++ {
		items = append(items, rankItem{Symbol: fmt.Sprintf("US.S%d", i)})
	}
	p.resolveFloats(context.Background(), items)
	if fr.snapCalls != maxSnapshotReqs {
		t.Fatalf("snapshot requests = %d, want cap %d", fr.snapCalls, maxSnapshotReqs)
	}
}

func TestResolveFloatsChunksAtCap(t *testing.T) {
	var maxBatch int
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		if len(codes) > maxBatch {
			maxBatch = len(codes)
		}
		snaps := make([]*snappb.Snapshot, 0, len(codes))
		for _, c := range codes {
			snaps = append(snaps, snap(c, 1_000_000, true))
		}
		return snapResp(snaps...), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	var items []rankItem
	for i := 0; i < 900; i++ { // 3 chunks of 400/400/100, all succeed (<8 reqs)
		items = append(items, rankItem{Symbol: fmt.Sprintf("US.S%d", i)})
	}
	p.resolveFloats(context.Background(), items)
	if maxBatch > snapshotChunkSize {
		t.Fatalf("a batch of %d exceeds the chunk cap %d", maxBatch, snapshotChunkSize)
	}
}

func TestResolveFloatsSteadyStateNoRequests(t *testing.T) {
	fr := &fakeReq{snap: func(codes []string) (*snappb.Response, error) {
		snaps := make([]*snappb.Snapshot, 0, len(codes))
		for _, c := range codes {
			snaps = append(snaps, snap(c, 10_000_000, true))
		}
		return snapResp(snaps...), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	items := []rankItem{{Symbol: "US.A"}, {Symbol: "US.B"}}
	p.resolveFloats(context.Background(), items)
	first := fr.snapCalls
	if first == 0 {
		t.Fatalf("first resolve should have issued at least one request")
	}
	p.resolveFloats(context.Background(), items) // all cached now
	if fr.snapCalls != first {
		t.Fatalf("second resolve should issue no new requests: %d -> %d", first, fr.snapCalls)
	}
}

func TestResolveExchClassifiesListedAndOTC(t *testing.T) {
	fr := &fakeReq{staticInfo: func(codes []string) (*staticpb.Response, error) {
		return staticInfoResp(
			staticInfoOf("LISTED", int32(qotcommon.ExchType_ExchType_US_Nasdaq)),
			staticInfoOf("PINK", int32(qotcommon.ExchType_ExchType_US_Pink)),
		), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	items := []rankItem{{Symbol: "US.LISTED"}, {Symbol: "US.PINK"}, {Symbol: "US.OMIT"}}
	p.resolveExch(context.Background(), items)
	if p.otc["US.LISTED"] {
		t.Fatalf("US.LISTED should resolve as not-OTC: %+v", p.otc)
	}
	if !p.otc["US.PINK"] {
		t.Fatalf("US.PINK should resolve as OTC: %+v", p.otc)
	}
	if v, ok := p.otc["US.OMIT"]; !ok || v {
		t.Fatalf("US.OMIT was omitted from the response and must be cached not-OTC (never re-requested): ok=%v v=%v", ok, v)
	}
}

func TestResolveExchTransportErrorLeavesAbsent(t *testing.T) {
	fr := &fakeReq{staticInfo: func(codes []string) (*staticpb.Response, error) {
		return nil, fmt.Errorf("dial tcp: connection refused")
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	p.resolveExch(context.Background(), []rankItem{{Symbol: "US.A"}})
	if _, ok := p.otc["US.A"]; ok {
		t.Fatalf("transport error must leave the symbol unresolved, not cached")
	}
}

func TestResolveExchSplitRetryIsolatesBadCode(t *testing.T) {
	// Any batch containing BAD errors as a whole until BAD is alone; BAD's own
	// isolated error must cache it not-OTC (never assumed OTC), same
	// conservative default snapshotBatch uses for its bad-mark.
	fr := &fakeReq{staticInfo: func(codes []string) (*staticpb.Response, error) {
		for _, c := range codes {
			if c == "BAD" {
				return staticErrResp("no permission"), nil
			}
		}
		var infos []*qotcommon.SecurityStaticInfo
		for _, c := range codes {
			infos = append(infos, staticInfoOf(c, int32(qotcommon.ExchType_ExchType_US_Nasdaq)))
		}
		return staticInfoResp(infos...), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	items := []rankItem{{Symbol: "US.A"}, {Symbol: "US.B"}, {Symbol: "US.BAD"}, {Symbol: "US.C"}}
	p.resolveExch(context.Background(), items)
	for _, s := range []string{"US.A", "US.B", "US.C", "US.BAD"} {
		if v, ok := p.otc[s]; !ok || v {
			t.Fatalf("%s should resolve as not-OTC (cached false): ok=%v v=%v", s, ok, v)
		}
	}
}

// TestResolveExchIsolatedFailureCachedNoRepeatedRequests guards the fix for
// staticInfoBatch's isolated-failure/omission caching: a code that keeps
// failing 3202 in isolation must be cached (steady state stays zero
// requests), not re-queried every poll — mirroring resolveFloats'
// established bad-mark convention.
func TestResolveExchIsolatedFailureCachedNoRepeatedRequests(t *testing.T) {
	fr := &fakeReq{staticInfo: func(codes []string) (*staticpb.Response, error) {
		for _, c := range codes {
			if c == "BAD" {
				return staticErrResp("no permission"), nil
			}
		}
		return staticInfoResp(staticInfoOf("A", int32(qotcommon.ExchType_ExchType_US_Nasdaq))), nil
	}}
	p := newTestPoller(config.Scan{}, fr, &capturePub{})
	items := []rankItem{{Symbol: "US.A"}, {Symbol: "US.BAD"}}
	p.resolveExch(context.Background(), items)
	first := fr.staticCalls
	if first == 0 {
		t.Fatalf("first resolve should have issued at least one request")
	}
	p.resolveExch(context.Background(), items) // both cached now, including BAD
	if fr.staticCalls != first {
		t.Fatalf("second resolve should issue no new requests: %d -> %d", first, fr.staticCalls)
	}
}

// TestPollOnceDropsOTCBeforeFloatResolveAndPool covers the end-to-end wiring:
// an OTC/Pink symbol on the rank board is resolved via 3202, dropped before
// it ever reaches the 3203 float call, rankRows, or the pool/subscription
// feed — it never appears in the published rows and never gets Ensure'd.
func TestPollOnceDropsOTCBeforeFloatResolveAndPool(t *testing.T) {
	fr := &fakeReq{
		rankResp: rankResp(
			rankItem{Symbol: "US.LOWF", ChangePct: 12, Last: 4, Volume: 300_000}, // listed, passes
			rankItem{Symbol: "US.OTC1", ChangePct: 20, Last: 1, Volume: 400_000}, // OTC, would otherwise pass
		),
		staticInfo: func(codes []string) (*staticpb.Response, error) {
			return staticInfoResp(
				staticInfoOf("LOWF", int32(qotcommon.ExchType_ExchType_US_Nasdaq)),
				staticInfoOf("OTC1", int32(qotcommon.ExchType_ExchType_US_Pink)),
			), nil
		},
		snap: func(codes []string) (*snappb.Response, error) {
			for _, c := range codes {
				if c == "OTC1" {
					t.Fatalf("OTC1 must be dropped before the 3203 float call, got codes=%v", codes)
				}
			}
			return snapResp(marketSnap("LOWF", 20_000_000, 4, 3.5, 300_000)), nil
		},
	}
	sf := &spyFeed{}
	pub := &capturePub{}
	p := New(config.Scan{Enabled: true, MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000},
		fr, pub, clock.NewFake(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)), sf, nil)

	p.pollOnce(context.Background(), p.clk.Now())

	rows := pub.ranks[0].Rows
	if len(rows) != 1 || rows[0].Symbol != "US.LOWF" {
		t.Fatalf("only US.LOWF should survive (OTC1 dropped): %+v", rows)
	}
	if len(sf.ensured) != 1 || sf.ensured[0].ID != "scan:US.LOWF" {
		t.Fatalf("only US.LOWF should be Ensure'd into the pool: %+v", sf.ensured)
	}
}

func TestPollOnceEndToEnd(t *testing.T) {
	fr := &fakeReq{
		rankResp: rankResp(
			rankItem{Symbol: "US.LOWF", ChangePct: 12, Last: 4, Volume: 300_000}, // passes
			rankItem{Symbol: "US.BIGF", ChangePct: 20, Last: 8, Volume: 900_000}, // over float cap
			rankItem{Symbol: "US.THIN", ChangePct: 30, Last: 1, Volume: 5_000},   // under volume floor
		),
		snap: func(codes []string) (*snappb.Response, error) {
			return snapResp(
				marketSnap("LOWF", 20_000_000, 4, 3.5, 300_000),
				marketSnap("BIGF", 500_000_000, 8, 6, 900_000),
				marketSnap("THIN", 1_000_000, 1, .75, 5_000),
			), nil
		},
	}
	pub := &capturePub{}
	p := newTestPoller(config.Scan{Enabled: true, MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000}, fr, pub)

	p.pollOnce(context.Background(), p.clk.Now())

	if len(pub.ranks) != 1 {
		t.Fatalf("want exactly one rank publish, got %d", len(pub.ranks))
	}
	rows := pub.ranks[0].Rows
	if len(rows) != 1 || rows[0].Symbol != "US.LOWF" {
		t.Fatalf("only US.LOWF should survive (BIGF over cap, THIN under volume): %+v", rows)
	}
	if rows[0].FloatShares == nil || *rows[0].FloatShares != 20_000_000 {
		t.Fatalf("US.LOWF float should be resolved via 3203: %+v", rows[0])
	}
	if len(pub.hits) != 0 {
		t.Fatalf("first poll is a silent baseline -> no hits: %+v", pub.hits)
	}

	// Second poll, same board: still a rank publish, no hits (baseline already seeded).
	p.pollOnce(context.Background(), p.clk.Now())
	if len(pub.ranks) != 2 {
		t.Fatalf("want a second rank publish, got %d", len(pub.ranks))
	}
	if len(pub.hits) != 0 {
		t.Fatalf("baseline seeded, US.LOWF already seen -> no hits on second poll: %+v", pub.hits)
	}
	if fr.snapCalls != 2 {
		t.Fatalf("each poll should refresh accumulated rows: snapCalls=%d", fr.snapCalls)
	}
}

func TestRTHBootstrapIsStickyAndRunsOnce(t *testing.T) {
	fr := &fakeReq{
		rankResp:     rankResp(rankItem{Symbol: "US.PRE", ChangePct: 8, Last: 2, Volume: 10}),
		topMoversRsp: topResp(rankItem{Symbol: "US.RTH", ChangePct: 6, Last: 3, Volume: 20}),
		snap: func(codes []string) (*snappb.Response, error) {
			out := make([]*snappb.Snapshot, 0, len(codes))
			for _, code := range codes {
				out = append(out, marketSnap(code, 1_000_000, 2, 1, 100))
			}
			return snapResp(out...), nil
		},
	}
	pub := &capturePub{}
	clk := clock.NewFake(et(2026, 7, 8, 10, 0))
	p := New(config.Scan{Enabled: true}, fr, pub, clk, nil, nil)
	p.pollOnce(context.Background(), clk.Now())
	p.pollOnce(context.Background(), clk.Now())
	if fr.preCalls != 1 || fr.topCalls != 2 {
		t.Fatalf("calls pre=%d rth=%d", fr.preCalls, fr.topCalls)
	}
	if got := len(pub.ranks[1].Rows); got != 2 {
		t.Fatalf("sticky combined rows=%d: %+v", got, pub.ranks[1].Rows)
	}
}

func TestRTHBootstrapFailureRetriesWithoutBlocking(t *testing.T) {
	fr := &fakeReq{rankErr: fmt.Errorf("temporary"), rankResp: rankResp(rankItem{Symbol: "US.PRE", ChangePct: 8}), topMoversRsp: topResp(rankItem{Symbol: "US.RTH", ChangePct: 6}), snap: func(codes []string) (*snappb.Response, error) {
		out := make([]*snappb.Snapshot, 0, len(codes))
		for _, code := range codes {
			out = append(out, marketSnap(code, 1, 2, 1, 100))
		}
		return snapResp(out...), nil
	}}
	pub := &capturePub{}
	clk := clock.NewFake(et(2026, 7, 8, 10, 0))
	p := New(config.Scan{Enabled: true}, fr, pub, clk, nil, nil)
	p.pollOnce(context.Background(), clk.Now())
	if len(pub.ranks) != 1 || len(pub.ranks[0].Rows) != 1 {
		t.Fatalf("RTH must publish despite bootstrap failure: %+v", pub.ranks)
	}
	fr.rankErr = nil
	p.pollOnce(context.Background(), clk.Now())
	p.pollOnce(context.Background(), clk.Now())
	if fr.preCalls != 2 || len(pub.ranks[2].Rows) != 2 {
		t.Fatalf("bootstrap retry/stop failed: calls=%d rows=%+v", fr.preCalls, pub.ranks[2].Rows)
	}
}

func TestRTHFilterResetRepeatsBootstrap(t *testing.T) {
	fr := &fakeReq{rankResp: rankResp(rankItem{Symbol: "US.PRE", ChangePct: 8}), topMoversRsp: topResp(rankItem{Symbol: "US.RTH", ChangePct: 6}), snap: func(codes []string) (*snappb.Response, error) {
		out := make([]*snappb.Snapshot, 0, len(codes))
		for _, code := range codes {
			out = append(out, marketSnap(code, 1, 2, 1, 100))
		}
		return snapResp(out...), nil
	}}
	clk := clock.NewFake(et(2026, 7, 8, 10, 0))
	p := New(config.Scan{Enabled: true}, fr, &capturePub{}, clk, nil, nil)
	p.pollOnce(context.Background(), clk.Now())
	f := p.Filters()
	f.MinVolume = 1
	if err := p.SetFilters(f); err != nil {
		t.Fatal(err)
	}
	p.pollOnce(context.Background(), clk.Now())
	if fr.preCalls != 2 {
		t.Fatalf("filter reset premarket calls=%d", fr.preCalls)
	}
}

func TestAccumulatedRowsRefreshAndSurviveSnapshotFailure(t *testing.T) {
	fail := false
	fr := &fakeReq{rankResp: rankResp(rankItem{Symbol: "US.A", ChangePct: 5, Last: 1, Volume: 1}), snap: func(codes []string) (*snappb.Response, error) {
		if fail {
			return nil, fmt.Errorf("temporary")
		}
		return snapResp(extendedSnap("A", session.PreMarket, 12, 20, 321)), nil
	}}
	pub := &capturePub{}
	clk := clock.NewFake(et(2026, 7, 8, 8, 0))
	p := New(config.Scan{Enabled: true}, fr, pub, clk, nil, nil)
	p.pollOnce(context.Background(), clk.Now())
	fail = true
	p.pollOnce(context.Background(), clk.Now())
	for i := range pub.ranks {
		r := pub.ranks[i].Rows[0]
		if r.Last == nil || *r.Last != 12 || r.ChangePct == nil || *r.ChangePct != 20 || r.Volume != 321 {
			t.Fatalf("poll %d did not preserve refreshed values: %+v", i, r)
		}
	}
}

func TestSnapshotRefreshUsesActiveSessionData(t *testing.T) {
	for _, tc := range []struct {
		name   string
		phase  session.Phase
		want   rankItem
		makeSn func() *snappb.Snapshot
	}{
		{"premarket", session.PreMarket, rankItem{Last: 101, ChangePct: 1, Volume: 11}, func() *snappb.Snapshot { return extendedSnap("A", session.PreMarket, 101, 1, 11) }},
		{"after-hours", session.PostMarket, rankItem{Last: 102, ChangePct: 2, Volume: 22}, func() *snappb.Snapshot { return extendedSnap("A", session.PostMarket, 102, 2, 22) }},
		{"overnight", session.Overnight, rankItem{Last: 103, ChangePct: 3, Volume: 33}, func() *snappb.Snapshot { return extendedSnap("A", session.Overnight, 103, 3, 33) }},
		{"rth", session.RTH, rankItem{Last: 90, ChangePct: 12.5, Volume: 999}, func() *snappb.Snapshot { return marketSnap("A", 1, 90, 80, 999) }},
		{"missing extended data", session.PreMarket, rankItem{Last: 7, ChangePct: 6, Volume: 5}, func() *snappb.Snapshot { return marketSnap("A", 1, 90, 80, 999) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := &fakeReq{snap: func([]string) (*snappb.Response, error) { return snapResp(tc.makeSn()), nil }}
			p := newTestPoller(config.Scan{}, fr, &capturePub{})
			items := map[string]rankItem{"US.A": {Symbol: "US.A", Last: 7, ChangePct: 6, Volume: 5}}
			p.refreshSnapshots(context.Background(), tc.phase, items)
			got := items["US.A"]
			if got.Last != tc.want.Last || got.ChangePct != tc.want.ChangePct || got.Volume != tc.want.Volume {
				t.Fatalf("got %+v, want market values %+v", got, tc.want)
			}
		})
	}
}

func TestSnapshotRefreshEnrichesDerivedSSRWithoutChangingCanonicalSymbol(t *testing.T) {
	fr := &fakeReq{snap: func([]string) (*snappb.Response, error) {
		s := marketSnap("A", 1, 105, 100, 999)
		s.Basic.LowPrice = proto.Float64(89)
		return snapResp(s), nil
	}}
	clk := clock.NewFake(et(2026, 7, 8, 10, 0))
	p := New(config.Scan{}, fr, &capturePub{}, clk, nil, nil, ssr.New(nil))
	items := map[string]rankItem{"US.A": {Symbol: "US.A"}}
	p.refreshSnapshots(context.Background(), session.RTH, items)

	got := items["US.A"]
	if got.Symbol != "US.A" {
		t.Fatalf("snapshot enrichment changed canonical symbol: %+v", got)
	}
	if got.Last != 105 || !got.ShortSellRestricted {
		t.Fatalf("SSR should use the crossed low while preserving recovered current price: %+v", got)
	}
	rows := rankRowsFiltered([]rankItem{got}, nil, wsmsg.ScannerFilters{Mode: "most_active"})
	if len(rows) != 1 || !rows[0].ShortSellRestricted || rows[0].Symbol != "US.A" {
		t.Fatalf("SSR did not reach ScannerRow: %+v", rows)
	}
	if fr.snapCalls != 1 {
		t.Fatalf("SSR enrichment added a snapshot request: %d", fr.snapCalls)
	}
}

func TestBoardSurvivesCycleUntilPostMarketTransition(t *testing.T) {
	fr := &fakeReq{
		overnightRsp: &onpb.Response{RetType: proto.Int32(0), S2C: &onpb.S2C{DataList: []*onpb.OvernightRankItem{{Security: usSec("ON"), OvernightChangeRatio: proto.Float64(5), OvernightPrice: proto.Float64(2), OvernightVolume: proto.Int64(10)}}}},
		rankResp:     rankResp(rankItem{Symbol: "US.PRE", ChangePct: 6}),
		topMoversRsp: topResp(rankItem{Symbol: "US.RTH", ChangePct: 7}),
		afterHrsRsp:  &ahpb.Response{RetType: proto.Int32(0), S2C: &ahpb.S2C{DataList: []*ahpb.AfterHoursRankItem{{Security: usSec("POST"), AfterHoursChangeRatio: proto.Float64(8), AfterHoursPrice: proto.Float64(2), AfterHoursVolume: proto.Int64(10)}}}},
		snap: func(codes []string) (*snappb.Response, error) {
			out := make([]*snappb.Snapshot, 0, len(codes))
			for _, code := range codes {
				out = append(out, marketSnap(code, 1, 2, 1, 100))
			}
			return snapResp(out...), nil
		},
	}
	pub := &capturePub{}
	clk := clock.NewFake(et(2026, 7, 8, 2, 0))
	p := New(config.Scan{Enabled: true}, fr, pub, clk, nil, nil)
	p.pollOnce(context.Background(), et(2026, 7, 8, 2, 0))
	p.pollOnce(context.Background(), et(2026, 7, 8, 10, 0))
	if got := len(pub.ranks[1].Rows); got != 3 {
		t.Fatalf("overnight/pre/RTH board rows=%d: %+v", got, pub.ranks[1].Rows)
	}
	p.pollOnce(context.Background(), et(2026, 7, 8, 16, 0))
	if got := pub.ranks[2].Rows; len(got) != 1 || got[0].Symbol != "US.POST" {
		t.Fatalf("post-market reset rows=%+v", got)
	}
}

func TestFetchRankSelectsSessionAPI(t *testing.T) {
	fr := &fakeReq{
		topMoversRsp: &tmrpb.Response{RetType: proto.Int32(0), S2C: &tmrpb.S2C{DataList: []*tmrpb.TopMoversRankItem{
			{Security: usSec("RTHX"), ChangeRatio: proto.Float64(7.5), CurPrice: proto.Float64(3.3), Volume: proto.Int64(11)}}}},
		afterHrsRsp: &ahpb.Response{RetType: proto.Int32(0), S2C: &ahpb.S2C{DataList: []*ahpb.AfterHoursRankItem{
			{Security: usSec("AHX"), AfterHoursChangeRatio: proto.Float64(4.2), AfterHoursPrice: proto.Float64(2.2), AfterHoursVolume: proto.Int64(22)}}}},
		overnightRsp: &onpb.Response{RetType: proto.Int32(0), S2C: &onpb.S2C{DataList: []*onpb.OvernightRankItem{
			{Security: usSec("ONX"), OvernightChangeRatio: proto.Float64(9.1), OvernightPrice: proto.Float64(1.1), OvernightVolume: proto.Int64(33)}}}},
		rankResp: rankResp(rankItem{Symbol: "US.PMX", ChangePct: 5.5, Last: 4.4, Volume: 44}),
	}
	p := newTestPoller(config.Scan{Enabled: true}, fr, &capturePub{})

	cases := []struct {
		phase  session.Phase
		symbol string
		pct    float64
	}{
		{session.RTH, "US.RTHX", 7.5},
		{session.PostMarket, "US.AHX", 4.2},
		{session.Overnight, "US.ONX", 9.1},
		{session.PreMarket, "US.PMX", 5.5},
		{session.Closed, "US.PMX", 5.5}, // Closed falls back to the pre-market board
	}
	for _, c := range cases {
		items, err := p.fetchRank(context.Background(), c.phase)
		if err != nil {
			t.Fatalf("phase %v: %v", c.phase, err)
		}
		if len(items) != 1 || items[0].Symbol != c.symbol || items[0].ChangePct != c.pct {
			t.Fatalf("phase %v: got %+v", c.phase, items)
		}
	}
}

func TestSessionKey(t *testing.T) {
	for phase, want := range map[session.Phase]string{
		session.RTH: "rth", session.PostMarket: "afterhours",
		session.Overnight: "overnight", session.PreMarket: "premarket", session.Closed: "premarket",
	} {
		if got := sessionKey(phase); got != want {
			t.Errorf("sessionKey(%v)=%q want %q", phase, got, want)
		}
	}
}

// spyFeed records Ensure/Release calls for pool-delta assertions.
type spyFeed struct {
	ensured  []feed.Demand
	released []string
}

func (s *spyFeed) Ensure(d feed.Demand) { s.ensured = append(s.ensured, d) }
func (s *spyFeed) Release(id string)    { s.released = append(s.released, id) }

func rows(syms ...string) []wsmsg.ScannerRow {
	out := make([]wsmsg.ScannerRow, len(syms))
	for i, s := range syms {
		out[i] = wsmsg.ScannerRow{Symbol: s}
	}
	return out
}

func TestUpdatePoolEnsuresWatchDemandsAndBackfills(t *testing.T) {
	sf := &spyFeed{}
	backfillCh := make(chan string, 2)
	clk := clock.NewFake(et(2026, 7, 8, 14, 0)) // RTH, well inside a pool day
	p := New(config.Scan{}, &fakeReq{}, &capturePub{}, clk, sf, func(s string) { backfillCh <- s })

	p.updatePool(clk.Now(), rows("US.A", "US.B"))

	backfilled := make([]string, 0, 2)
	for len(backfilled) < 2 {
		select {
		case symbol := <-backfillCh:
			backfilled = append(backfilled, symbol)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for backfills; got %v", backfilled)
		}
	}

	if len(sf.ensured) != 2 {
		t.Fatalf("want 2 Ensure calls, got %+v", sf.ensured)
	}
	if sf.ensured[0].ID != "scan:US.A" || sf.ensured[0].Symbol != "US.A" {
		t.Fatalf("Ensure[0]=%+v, want id scan:US.A", sf.ensured[0])
	}
	if len(sf.ensured[0].Subs) != 1 || sf.ensured[0].Subs[0] != feed.SubTicker ||
		sf.ensured[0].Focused || !sf.ensured[0].WantsHistory {
		t.Fatalf("pool must use ticker-only archive-warm watch shape: %+v", sf.ensured[0])
	}
	if !sf.ensured[0].BackgroundSeed {
		t.Fatalf("scanner demand must use background seed lane: %+v", sf.ensured[0])
	}
	if !reflect.DeepEqual(backfilled, []string{"US.A", "US.B"}) {
		t.Fatalf("backfilled=%v, want [US.A US.B]", backfilled)
	}
	if !reflect.DeepEqual(p.PoolSymbols(), []string{"US.A", "US.B"}) {
		t.Fatalf("PoolSymbols()=%v, want [US.A US.B]", p.PoolSymbols())
	}
}

func TestUpdatePoolReleasesOnDayReset(t *testing.T) {
	sf := &spyFeed{}
	clk := clock.NewFake(et(2026, 7, 8, 19, 0))
	p := New(config.Scan{}, &fakeReq{}, &capturePub{}, clk, sf, nil) // nil backfill tolerated

	p.updatePool(et(2026, 7, 8, 19, 0), rows("US.A", "US.B")) // pool day D
	p.updatePool(et(2026, 7, 8, 20, 0), rows("US.C"))         // crosses 20:00 ET -> day D+1

	sort.Strings(sf.released)
	if !reflect.DeepEqual(sf.released, []string{"scan:US.A", "scan:US.B"}) {
		t.Fatalf("released=%v, want [scan:US.A scan:US.B]", sf.released)
	}
}

func TestUpdatePoolNilFeedInert(t *testing.T) {
	clk := clock.NewFake(et(2026, 7, 8, 14, 0))
	p := New(config.Scan{}, &fakeReq{}, &capturePub{}, clk, nil, nil)
	p.updatePool(clk.Now(), rows("US.A")) // must not panic
	if p.PoolSymbols() != nil {
		t.Fatalf("nil feed must disable the pool: PoolSymbols()=%v", p.PoolSymbols())
	}
}

func TestPollOnceDrivesPool(t *testing.T) {
	fr := &fakeReq{
		rankResp: rankResp(rankItem{Symbol: "US.LOWF", ChangePct: 12.5, Last: 4.2, Volume: 300_000}),
		snap: func(codes []string) (*snappb.Response, error) {
			return snapResp(marketSnap("LOWF", 20_000_000, 4.2, 3.5, 300_000)), nil
		},
	}
	sf := &spyFeed{}
	clk := clock.NewFake(et(2026, 7, 8, 8, 0)) // pre-market
	p := New(config.Scan{Enabled: true, MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000},
		fr, &capturePub{}, clk, sf, nil)

	p.pollOnce(context.Background(), clk.Now())

	if len(sf.ensured) != 1 || sf.ensured[0].ID != "scan:US.LOWF" {
		t.Fatalf("pollOnce should Ensure the filtered top row via the pool: %+v", sf.ensured)
	}
}
