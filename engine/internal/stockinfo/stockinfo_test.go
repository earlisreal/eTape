package stockinfo

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	ownerplatepb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetownerplate"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
	staticpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetstaticinfo"
)

// ---- pure transform tests ----

func TestSnapshotToPayloadWithEquityExData(t *testing.T) {
	basic := &snappb.SnapshotBasicData{
		Name:                proto.String("Apple Inc."),
		CurPrice:            proto.Float64(210.0),
		LastClosePrice:      proto.Float64(200.0),
		Volume:              proto.Int64(123456),
		Highest52WeeksPrice: proto.Float64(250.0),
		Lowest52WeeksPrice:  proto.Float64(150.0),
	}
	ex := &snappb.EquitySnapshotExData{
		IssuedShares:         proto.Int64(1_000_000),
		IssuedMarketVal:      proto.Float64(210_000_000),
		OutstandingShares:    proto.Int64(900_000),
		OutstandingMarketVal: proto.Float64(189_000_000),
		PeRate:               proto.Float64(28.5),
		PeTTMRate:            proto.Float64(27.1),
		EarningsPershare:     proto.Float64(6.5),
	}
	got := snapshotToPayload(basic, ex, "Technology", "2026-07-09T00:00:00.000Z")

	if got.Name != "Apple Inc." || got.Industry != "Technology" || got.RefreshedAt != "2026-07-09T00:00:00.000Z" {
		t.Fatalf("basic/industry/refreshedAt wrong: %+v", got)
	}
	if got.Price == nil || *got.Price != 210.0 {
		t.Fatalf("Price wrong: %+v", got.Price)
	}
	if got.LastClose == nil || *got.LastClose != 200.0 {
		t.Fatalf("LastClose wrong: %+v", got.LastClose)
	}
	wantChangePct := (210.0 - 200.0) / 200.0 * 100
	if got.ChangePct == nil || *got.ChangePct != wantChangePct {
		t.Fatalf("ChangePct wrong: got %v want %v", got.ChangePct, wantChangePct)
	}
	if got.Volume != 123456 {
		t.Fatalf("Volume wrong: %v", got.Volume)
	}
	if got.High52 == nil || *got.High52 != 250.0 {
		t.Fatalf("High52 wrong: %+v", got.High52)
	}
	if got.Low52 == nil || *got.Low52 != 150.0 {
		t.Fatalf("Low52 wrong: %+v", got.Low52)
	}
	if got.MarketCap == nil || *got.MarketCap != 210_000_000 {
		t.Fatalf("MarketCap wrong: %+v", got.MarketCap)
	}
	if got.FloatMarketCap == nil || *got.FloatMarketCap != 189_000_000 {
		t.Fatalf("FloatMarketCap wrong: %+v", got.FloatMarketCap)
	}
	if got.SharesOutstanding == nil || *got.SharesOutstanding != 1_000_000 {
		t.Fatalf("SharesOutstanding wrong: %+v", got.SharesOutstanding)
	}
	if got.FloatShares == nil || *got.FloatShares != 900_000 {
		t.Fatalf("FloatShares wrong: %+v", got.FloatShares)
	}
	if got.Pe == nil || *got.Pe != 28.5 {
		t.Fatalf("Pe wrong: %+v", got.Pe)
	}
	if got.PeTTM == nil || *got.PeTTM != 27.1 {
		t.Fatalf("PeTTM wrong: %+v", got.PeTTM)
	}
	if got.Eps == nil || *got.Eps != 6.5 {
		t.Fatalf("Eps wrong: %+v", got.Eps)
	}
}

func TestSnapshotToPayloadZeroEquityValuesKeptNotNil(t *testing.T) {
	basic := &snappb.SnapshotBasicData{CurPrice: proto.Float64(1), LastClosePrice: proto.Float64(1)}
	ex := &snappb.EquitySnapshotExData{
		IssuedShares: proto.Int64(0), IssuedMarketVal: proto.Float64(0),
		OutstandingShares: proto.Int64(0), OutstandingMarketVal: proto.Float64(0),
		PeRate: proto.Float64(0), PeTTMRate: proto.Float64(0), EarningsPershare: proto.Float64(0),
	}
	got := snapshotToPayload(basic, ex, "", "t")
	if got.MarketCap == nil || *got.MarketCap != 0 {
		t.Fatalf("zero MarketCap should be kept as 0, not nil: %+v", got.MarketCap)
	}
	if got.Pe == nil || *got.Pe != 0 {
		t.Fatalf("zero Pe should be kept as 0, not nil: %+v", got.Pe)
	}
	if got.FloatShares == nil || *got.FloatShares != 0 {
		t.Fatalf("zero FloatShares should be kept as 0, not nil: %+v", got.FloatShares)
	}
}

func TestSnapshotToPayloadNoEquityExDataNilsEquityFields(t *testing.T) {
	basic := &snappb.SnapshotBasicData{
		Name: proto.String("SPY"), CurPrice: proto.Float64(500), LastClosePrice: proto.Float64(495),
		Volume: proto.Int64(1000), Highest52WeeksPrice: proto.Float64(520), Lowest52WeeksPrice: proto.Float64(400),
	}
	got := snapshotToPayload(basic, nil, "", "t")
	if got.Name != "SPY" {
		t.Fatalf("Name should still populate from Basic: %+v", got)
	}
	if got.Price == nil || *got.Price != 500 || got.LastClose == nil || *got.LastClose != 495 {
		t.Fatalf("Price/LastClose should still populate from Basic: %+v", got)
	}
	if got.Volume != 1000 {
		t.Fatalf("Volume should still populate from Basic: %+v", got)
	}
	if got.High52 == nil || *got.High52 != 520 || got.Low52 == nil || *got.Low52 != 400 {
		t.Fatalf("High52/Low52 should still populate from Basic: %+v", got)
	}
	if got.MarketCap != nil || got.FloatMarketCap != nil || got.SharesOutstanding != nil ||
		got.FloatShares != nil || got.Pe != nil || got.PeTTM != nil || got.Eps != nil {
		t.Fatalf("equity-derived fields must be nil when ex is nil: %+v", got)
	}
}

func TestSnapshotToPayloadLastCloseZeroLeavesChangePctNil(t *testing.T) {
	basic := &snappb.SnapshotBasicData{CurPrice: proto.Float64(10), LastClosePrice: proto.Float64(0)}
	got := snapshotToPayload(basic, nil, "", "t")
	if got.ChangePct != nil {
		t.Fatalf("ChangePct should be nil when lastClose is 0, got %v", *got.ChangePct)
	}
}

func TestIndustryFromPlatesPicksIndustryType(t *testing.T) {
	plates := []*qotcommon.PlateInfo{
		{Name: proto.String("Consumer Tech"), PlateType: proto.Int32(int32(qotcommon.PlateSetType_PlateSetType_Concept))},
		{Name: proto.String("Technology"), PlateType: proto.Int32(int32(qotcommon.PlateSetType_PlateSetType_Industry))},
		{Name: proto.String("Americas"), PlateType: proto.Int32(int32(qotcommon.PlateSetType_PlateSetType_Region))},
	}
	if got := industryFromPlates(plates); got != "Technology" {
		t.Fatalf("want Technology, got %q", got)
	}
}

func TestIndustryFromPlatesNoIndustryReturnsEmpty(t *testing.T) {
	plates := []*qotcommon.PlateInfo{
		{Name: proto.String("Americas"), PlateType: proto.Int32(int32(qotcommon.PlateSetType_PlateSetType_Region))},
	}
	if got := industryFromPlates(plates); got != "" {
		t.Fatalf("want empty string, got %q", got)
	}
}

// ---- fake requester / publisher for Run()-level tests ----

// fakeRequester dispatches canned responses by protoID and counts calls per
// protoID so tests can assert cache behavior (e.g. no 3207 call on a cache
// hit). snapshotFn/ownerPlateFn/staticInfoFn, when set, take priority over
// the static snapshot/ownerPlate/staticInfo fields and are handed the bare
// moomoo codes (e.g. "AAPL", not "US.AAPL") present in that specific
// request, so a test can simulate "this code poisons the whole batch, these
// others succeed" — exercising the binary-split isolation path.
// snapshotCodeCalls/ownerPlateCodeCalls/staticInfoCodeCalls record the codes
// requested on every call, in order, so a test can assert a specific code
// was (or was not) re-requested.
type fakeRequester struct {
	snapshot            *snappb.Response
	ownerPlate          *ownerplatepb.Response
	staticInfo          *staticpb.Response
	snapshotFn          func(codes []string) *snappb.Response
	ownerPlateFn        func(codes []string) *ownerplatepb.Response
	staticInfoFn        func(codes []string) *staticpb.Response
	calls               map[uint32]int
	snapshotCodeCalls   [][]string
	ownerPlateCodeCalls [][]string
	staticInfoCodeCalls [][]string
}

func newFakeRequester() *fakeRequester {
	return &fakeRequester{calls: map[uint32]int{}}
}

// codesFromSecurityList extracts the bare moomoo codes from a SecurityList,
// shared by both protocols since they take the same []*qotcommon.Security shape.
func codesFromSecurityList(secs []*qotcommon.Security) []string {
	out := make([]string, 0, len(secs))
	for _, s := range secs {
		out = append(out, s.GetCode())
	}
	return out
}

func (f *fakeRequester) Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	f.calls[protoID]++
	switch protoID {
	case opend.ProtoQotGetSecuritySnapshot:
		codes := codesFromSecurityList(req.(*snappb.Request).GetC2S().GetSecurityList())
		f.snapshotCodeCalls = append(f.snapshotCodeCalls, codes)
		resp := f.snapshot
		if f.snapshotFn != nil {
			resp = f.snapshotFn(codes)
		}
		b, _ := proto.Marshal(resp)
		return opend.Frame{ProtoID: protoID, Body: b}, nil
	case opend.ProtoQotGetOwnerPlate:
		codes := codesFromSecurityList(req.(*ownerplatepb.Request).GetC2S().GetSecurityList())
		f.ownerPlateCodeCalls = append(f.ownerPlateCodeCalls, codes)
		resp := f.ownerPlate
		if f.ownerPlateFn != nil {
			resp = f.ownerPlateFn(codes)
		}
		b, _ := proto.Marshal(resp)
		return opend.Frame{ProtoID: protoID, Body: b}, nil
	case opend.ProtoQotGetStaticInfo:
		codes := codesFromSecurityList(req.(*staticpb.Request).GetC2S().GetSecurityList())
		f.staticInfoCodeCalls = append(f.staticInfoCodeCalls, codes)
		resp := f.staticInfo
		if f.staticInfoFn != nil {
			resp = f.staticInfoFn(codes)
		}
		b, _ := proto.Marshal(resp)
		return opend.Frame{ProtoID: protoID, Body: b}, nil
	default:
		return opend.Frame{}, nil
	}
}

// fakeBars is a dailyBarReader test double. barsFor, when set, overrides the
// static bars map for a given symbol; err, when set, is returned for every
// symbol (simulating a store-read failure). readCalls counts ReadDailyBars
// calls per symbol so a test can assert the once-per-day cache is honored.
type fakeBars struct {
	bars      map[string][]feed.Bar
	err       error
	readCalls map[string]int
}

func newFakeBars() *fakeBars {
	return &fakeBars{bars: map[string][]feed.Bar{}, readCalls: map[string]int{}}
}

func (f *fakeBars) ReadDailyBars(symbol string) ([]feed.Bar, error) {
	if f.readCalls != nil {
		f.readCalls[symbol]++
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.bars[symbol], nil
}

// closesBars builds n daily bars with the given closes (index 0 = oldest),
// enough to exercise md.EMA's period gate.
func closesBars(symbol string, closes ...float64) []feed.Bar {
	out := make([]feed.Bar, len(closes))
	for i, c := range closes {
		out[i] = feed.Bar{Symbol: symbol, BucketMs: int64(i) * 86400_000, C: c}
	}
	return out
}

// fakePublisher records every Publish call. Guarded by a mutex because
// TestRunTicksAndStopsOnContextCancel reads calls from the test goroutine
// while Poller.Run's goroutine (via fetchTick) writes to it concurrently;
// every other test in this file calls fetchTick synchronously and never
// needs the lock, but taking it unconditionally keeps the type usable
// either way.
type fakePublisher struct {
	mu    sync.Mutex
	calls []pubCall
}

type pubCall struct {
	topic   wsmsg.Topic
	key     string
	payload any
}

func (f *fakePublisher) Publish(topic wsmsg.Topic, key string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, pubCall{topic: topic, key: key, payload: payload})
}

// snapshot returns a race-safe copy of the recorded calls.
func (f *fakePublisher) snapshotCalls() []pubCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]pubCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func sec(code string) *qotcommon.Security {
	return &qotcommon.Security{Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)), Code: proto.String(code)}
}

// snapshotFor builds a Snapshot with every proto2 `req` field on
// SnapshotBasicData (and, if hasEquity, EquitySnapshotExData) populated so
// proto.Marshal succeeds through the fake requester's wire round-trip. Only
// the fields the tests actually assert on carry meaningful values; the rest
// are present-but-arbitrary to satisfy required-ness.
func snapshotFor(code string, hasEquity bool) *snappb.Snapshot {
	sn := &snappb.Snapshot{
		Basic: &snappb.SnapshotBasicData{
			Security: sec(code), Name: proto.String(code + " Inc."),
			Type: proto.Int32(0), IsSuspend: proto.Bool(false),
			ListTime: proto.String("1980-01-01"), LotSize: proto.Int32(100),
			PriceSpread: proto.Float64(0.01), UpdateTime: proto.String("2026-07-09 09:30:00"),
			HighPrice: proto.Float64(105), OpenPrice: proto.Float64(95), LowPrice: proto.Float64(94),
			CurPrice: proto.Float64(100), LastClosePrice: proto.Float64(90),
			Volume: proto.Int64(500), Turnover: proto.Float64(50000), TurnoverRate: proto.Float64(1),
		},
	}
	if hasEquity {
		sn.EquityExData = &snappb.EquitySnapshotExData{
			IssuedShares: proto.Int64(1000), IssuedMarketVal: proto.Float64(100000),
			NetAsset: proto.Float64(0), NetProfit: proto.Float64(0), NetAssetPershare: proto.Float64(0),
			EyRate: proto.Float64(0), PbRate: proto.Float64(0),
			OutstandingShares: proto.Int64(900), OutstandingMarketVal: proto.Float64(90000),
			PeRate: proto.Float64(10), PeTTMRate: proto.Float64(9), EarningsPershare: proto.Float64(1),
		}
	}
	return sn
}

// plateInfo builds a valid Qot_Common.PlateInfo (Plate and Name are proto2
// `req` fields on this message).
func plateInfo(name string, plateType qotcommon.PlateSetType) *qotcommon.PlateInfo {
	return &qotcommon.PlateInfo{
		Plate:     sec("PLATE"),
		Name:      proto.String(name),
		PlateType: proto.Int32(int32(plateType)),
	}
}

// staticInfoFor builds a SecurityStaticInfo with every proto2 `req` field on
// SecurityStaticBasic populated (mirrors snapshotFor's required-ness
// convention) plus the ExchType this test cares about.
func staticInfoFor(code string, exchType qotcommon.ExchType) *qotcommon.SecurityStaticInfo {
	return &qotcommon.SecurityStaticInfo{
		Basic: &qotcommon.SecurityStaticBasic{
			Security: sec(code), Id: proto.Int64(1), LotSize: proto.Int32(100),
			SecType: proto.Int32(0), Name: proto.String(code + " Inc."),
			ListTime: proto.String("1980-01-01"), ExchType: proto.Int32(int32(exchType)),
		},
	}
}

// ---- Run() / fetchTick integration tests ----

func TestFetchTickPublishesOnePayloadPerSymbolKeyedBySymbol(t *testing.T) {
	syms := []string{"US.AAPL", "US.TSLA"}
	fr := newFakeRequester()
	fr.snapshot = &snappb.Response{
		RetType: proto.Int32(0),
		S2C: &snappb.S2C{SnapshotList: []*snappb.Snapshot{
			snapshotFor("AAPL", true),
			snapshotFor("TSLA", true),
		}},
	}
	fr.ownerPlate = &ownerplatepb.Response{
		RetType: proto.Int32(0),
		S2C: &ownerplatepb.S2C{OwnerPlateList: []*ownerplatepb.SecurityOwnerPlate{
			{Security: sec("AAPL"), PlateInfoList: []*qotcommon.PlateInfo{
				plateInfo("Technology", qotcommon.PlateSetType_PlateSetType_Industry),
			}},
			{Security: sec("TSLA"), PlateInfoList: []*qotcommon.PlateInfo{
				plateInfo("Auto", qotcommon.PlateSetType_PlateSetType_Industry),
			}},
		}},
	}
	fr.staticInfo = &staticpb.Response{
		RetType: proto.Int32(0),
		S2C: &staticpb.S2C{StaticInfoList: []*qotcommon.SecurityStaticInfo{
			staticInfoFor("AAPL", qotcommon.ExchType_ExchType_US_Nasdaq),
			staticInfoFor("TSLA", qotcommon.ExchType_ExchType_US_Nasdaq),
		}},
	}
	pub := &fakePublisher{}
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, pub, clk, func() []string { return syms }, newFakeBars())

	p.fetchTick(context.Background())

	if len(pub.calls) != 2 {
		t.Fatalf("want 2 publish calls (one per symbol), got %d: %+v", len(pub.calls), pub.calls)
	}
	seen := map[string]bool{}
	for _, c := range pub.calls {
		if c.topic != wsmsg.TopicStockDetail {
			t.Fatalf("wrong topic: %v", c.topic)
		}
		payload, ok := c.payload.(wsmsg.StockDetailPayload)
		if !ok {
			t.Fatalf("payload is not a wsmsg.StockDetailPayload (got %T)", c.payload)
		}
		if payload.Symbol != c.key {
			t.Fatalf("payload.Symbol %q != key %q", payload.Symbol, c.key)
		}
		seen[c.key] = true
	}
	if !seen["US.AAPL"] || !seen["US.TSLA"] {
		t.Fatalf("expected publishes keyed by US.AAPL and US.TSLA, got %+v", pub.calls)
	}
}

func TestFetchTickETFGateNilsEquityFieldsButStillPublishes(t *testing.T) {
	syms := []string{"US.SPY"}
	fr := newFakeRequester()
	fr.snapshot = &snappb.Response{
		RetType: proto.Int32(0),
		S2C:     &snappb.S2C{SnapshotList: []*snappb.Snapshot{snapshotFor("SPY", false)}},
	}
	fr.ownerPlate = &ownerplatepb.Response{RetType: proto.Int32(0), S2C: &ownerplatepb.S2C{}}
	fr.staticInfo = &staticpb.Response{RetType: proto.Int32(0), S2C: &staticpb.S2C{}}
	pub := &fakePublisher{}
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, pub, clk, func() []string { return syms }, newFakeBars())

	p.fetchTick(context.Background())

	if len(pub.calls) != 1 {
		t.Fatalf("want 1 publish call, got %d", len(pub.calls))
	}
	payload := pub.calls[0].payload.(wsmsg.StockDetailPayload)
	if payload.Name != "SPY Inc." || payload.Price == nil || *payload.Price != 100 {
		t.Fatalf("basic-derived fields should still be set: %+v", payload)
	}
	if payload.MarketCap != nil || payload.FloatMarketCap != nil || payload.SharesOutstanding != nil ||
		payload.FloatShares != nil || payload.Pe != nil || payload.PeTTM != nil || payload.Eps != nil {
		t.Fatalf("equity-derived fields must be nil for a no-EquityExData instrument: %+v", payload)
	}
}

func TestFetchTickIndustryCachedAcrossTicksNoRepeatOwnerPlateRequest(t *testing.T) {
	syms := []string{"US.AAPL"}
	fr := newFakeRequester()
	fr.snapshot = &snappb.Response{
		RetType: proto.Int32(0),
		S2C:     &snappb.S2C{SnapshotList: []*snappb.Snapshot{snapshotFor("AAPL", true)}},
	}
	fr.ownerPlate = &ownerplatepb.Response{
		RetType: proto.Int32(0),
		S2C: &ownerplatepb.S2C{OwnerPlateList: []*ownerplatepb.SecurityOwnerPlate{
			{Security: sec("AAPL"), PlateInfoList: []*qotcommon.PlateInfo{
				plateInfo("Technology", qotcommon.PlateSetType_PlateSetType_Industry),
			}},
		}},
	}
	pub := &fakePublisher{}
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, pub, clk, func() []string { return syms }, newFakeBars())

	p.fetchTick(context.Background())
	if fr.calls[opend.ProtoQotGetOwnerPlate] != 1 {
		t.Fatalf("first tick should issue exactly 1 owner-plate request, got %d", fr.calls[opend.ProtoQotGetOwnerPlate])
	}
	if fr.calls[opend.ProtoQotGetSecuritySnapshot] != 1 {
		t.Fatalf("first tick should issue exactly 1 snapshot request, got %d", fr.calls[opend.ProtoQotGetSecuritySnapshot])
	}

	p.fetchTick(context.Background())
	if fr.calls[opend.ProtoQotGetOwnerPlate] != 1 {
		t.Fatalf("second tick should issue zero additional owner-plate requests (cached), got total %d", fr.calls[opend.ProtoQotGetOwnerPlate])
	}
	if fr.calls[opend.ProtoQotGetSecuritySnapshot] != 2 {
		t.Fatalf("second tick should still refresh fundamentals, got total snapshot calls %d", fr.calls[opend.ProtoQotGetSecuritySnapshot])
	}
	if len(pub.calls) != 2 {
		t.Fatalf("want 2 total publishes (one per tick), got %d", len(pub.calls))
	}
	payload := pub.calls[1].payload.(wsmsg.StockDetailPayload)
	if payload.Industry != "Technology" {
		t.Fatalf("second-tick payload should still carry the cached industry: %+v", payload)
	}
}

func TestFetchTickCachesEmptyIndustryToAvoidRerequest(t *testing.T) {
	syms := []string{"US.NOIND"}
	fr := newFakeRequester()
	fr.snapshot = &snappb.Response{
		RetType: proto.Int32(0),
		S2C:     &snappb.S2C{SnapshotList: []*snappb.Snapshot{snapshotFor("NOIND", true)}},
	}
	// Owner-plate succeeds but returns no row for this symbol at all.
	fr.ownerPlate = &ownerplatepb.Response{RetType: proto.Int32(0), S2C: &ownerplatepb.S2C{}}
	fr.staticInfo = &staticpb.Response{RetType: proto.Int32(0), S2C: &staticpb.S2C{}}
	pub := &fakePublisher{}
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, pub, clk, func() []string { return syms }, newFakeBars())

	p.fetchTick(context.Background())
	p.fetchTick(context.Background())

	if fr.calls[opend.ProtoQotGetOwnerPlate] != 1 {
		t.Fatalf("empty-industry result should still be cached, want 1 owner-plate call total, got %d", fr.calls[opend.ProtoQotGetOwnerPlate])
	}
	for _, c := range pub.calls {
		payload := c.payload.(wsmsg.StockDetailPayload)
		if payload.Industry != "" {
			t.Fatalf("expected cached-absent industry to render as empty string: %+v", payload)
		}
	}
}

// batchOrPoison returns a snapshot response builder for use as
// fakeRequester.snapshotFn: any request containing poisonCode fails the
// whole batch (RetType != 0), simulating moomoo's documented "one bad code
// fails the batch" behavior for 3203; any other request succeeds with real
// snapshot data for every code present.
func batchOrPoison(poisonCode string) func(codes []string) *snappb.Response {
	return func(codes []string) *snappb.Response {
		for _, c := range codes {
			if c == poisonCode {
				return &snappb.Response{RetType: proto.Int32(-1), RetMsg: proto.String("no quote rights: " + poisonCode)}
			}
		}
		list := make([]*snappb.Snapshot, 0, len(codes))
		for _, c := range codes {
			list = append(list, snapshotFor(c, true))
		}
		return &snappb.Response{RetType: proto.Int32(0), S2C: &snappb.S2C{SnapshotList: list}}
	}
}

// ownerPlateBatchOrPoison is batchOrPoison's 3207 counterpart: any request
// containing poisonCode fails the whole batch; any other request succeeds
// with a resolved industry for every code present.
func ownerPlateBatchOrPoison(poisonCode string) func(codes []string) *ownerplatepb.Response {
	return func(codes []string) *ownerplatepb.Response {
		for _, c := range codes {
			if c == poisonCode {
				return &ownerplatepb.Response{RetType: proto.Int32(-1), RetMsg: proto.String("no quote rights: " + poisonCode)}
			}
		}
		list := make([]*ownerplatepb.SecurityOwnerPlate, 0, len(codes))
		for _, c := range codes {
			list = append(list, &ownerplatepb.SecurityOwnerPlate{
				Security:      sec(c),
				PlateInfoList: []*qotcommon.PlateInfo{plateInfo(c+" Industry", qotcommon.PlateSetType_PlateSetType_Industry)},
			})
		}
		return &ownerplatepb.Response{RetType: proto.Int32(0), S2C: &ownerplatepb.S2C{OwnerPlateList: list}}
	}
}

// codeCallCount counts how many recorded per-call code lists mention code.
func codeCallCount(callLists [][]string, code string) int {
	n := 0
	for _, codes := range callLists {
		for _, c := range codes {
			if c == code {
				n++
			}
		}
	}
	return n
}

func TestFetchSnapshotsIsolatesBadSymbolViaBinarySplit(t *testing.T) {
	syms := []string{"US.AAA", "US.BBB", "US.CCC"}
	fr := newFakeRequester()
	fr.snapshotFn = batchOrPoison("BBB")
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, &fakePublisher{}, clock.NewFake(time.Now()), func() []string { return syms }, newFakeBars())

	out := p.fetchSnapshots(context.Background(), syms)

	if _, ok := out["US.AAA"]; !ok {
		t.Fatalf("US.AAA should have been resolved despite US.BBB poisoning the initial batch: %+v", out)
	}
	if _, ok := out["US.CCC"]; !ok {
		t.Fatalf("US.CCC should have been resolved despite US.BBB poisoning the initial batch: %+v", out)
	}
	if _, ok := out["US.BBB"]; ok {
		t.Fatalf("US.BBB should not appear in the result once isolated as the unresolvable symbol: %+v", out)
	}
}

func TestResolveIndustriesIsolatesBadSymbolAndCachesEmptyOnPersistentFailure(t *testing.T) {
	syms := []string{"US.AAA", "US.BBB", "US.CCC"}
	fr := newFakeRequester()
	fr.ownerPlateFn = ownerPlateBatchOrPoison("BBB")
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, &fakePublisher{}, clock.NewFake(time.Now()), func() []string { return syms }, newFakeBars())

	p.resolveIndustries(context.Background(), syms)

	if got := p.industry["US.AAA"]; got != "AAA Industry" {
		t.Fatalf("US.AAA should resolve its real industry despite US.BBB poisoning the initial batch, got %q", got)
	}
	if got := p.industry["US.CCC"]; got != "CCC Industry" {
		t.Fatalf("US.CCC should resolve its real industry despite US.BBB poisoning the initial batch, got %q", got)
	}
	got, ok := p.industry["US.BBB"]
	if !ok {
		t.Fatalf("US.BBB should be cached (as absent) once isolated as the persistently-failing symbol, got no entry at all")
	}
	if got != "" {
		t.Fatalf("US.BBB should cache as empty-industry once isolated alone, got %q", got)
	}

	bbbCallsBefore := codeCallCount(fr.ownerPlateCodeCalls, "BBB")
	totalCallsBefore := fr.calls[opend.ProtoQotGetOwnerPlate]

	// Second tick: all three symbols (including the isolated BBB) are now
	// cached, so resolveIndustries should issue zero further 3207 requests —
	// proving BBB converged to a steady state instead of being retried
	// forever every tick.
	p.resolveIndustries(context.Background(), syms)

	if got := fr.calls[opend.ProtoQotGetOwnerPlate]; got != totalCallsBefore {
		t.Fatalf("second resolveIndustries call should issue zero further owner-plate requests (all cached), total went from %d to %d", totalCallsBefore, got)
	}
	if got := codeCallCount(fr.ownerPlateCodeCalls, "BBB"); got != bbbCallsBefore {
		t.Fatalf("US.BBB should not be re-requested after being cached empty, call count went from %d to %d", bbbCallsBefore, got)
	}
}

func TestFetchTickPublishesGoodSymbolsWhenOneSnapshotBatchIsPoisoned(t *testing.T) {
	syms := []string{"US.AAA", "US.BBB", "US.CCC"}
	fr := newFakeRequester()
	fr.snapshotFn = batchOrPoison("BBB")
	fr.ownerPlateFn = ownerPlateBatchOrPoison("BBB")
	pub := &fakePublisher{}
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, pub, clk, func() []string { return syms }, newFakeBars())

	p.fetchTick(context.Background())

	if len(pub.calls) != 2 {
		t.Fatalf("want 2 publishes (US.AAA, US.CCC; US.BBB isolated-unresolvable), got %d: %+v", len(pub.calls), pub.calls)
	}
	seen := map[string]bool{}
	for _, c := range pub.calls {
		seen[c.key] = true
	}
	if !seen["US.AAA"] || !seen["US.CCC"] {
		t.Fatalf("expected US.AAA and US.CCC published despite US.BBB poisoning the batch, got %+v", pub.calls)
	}
	if seen["US.BBB"] {
		t.Fatalf("US.BBB should not be published this tick, got %+v", pub.calls)
	}
}

func TestFetchTickSkipsWhenSymbolsEmpty(t *testing.T) {
	fr := newFakeRequester()
	pub := &fakePublisher{}
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, pub, clk, func() []string { return nil }, newFakeBars())

	p.fetchTick(context.Background())

	if len(pub.calls) != 0 {
		t.Fatalf("want no publishes when symbols() is empty, got %d", len(pub.calls))
	}
	if fr.calls[opend.ProtoQotGetSecuritySnapshot] != 0 || fr.calls[opend.ProtoQotGetOwnerPlate] != 0 {
		t.Fatalf("want no requests when symbols() is empty, got %+v", fr.calls)
	}
}

// TestRunTicksAndStopsOnContextCancel uses a real clock with a very short
// interval (rather than clock.Fake) to sidestep the inherent goroutine-start
// race between "the ticker under test registers with the fake clock" and
// "the test calls Advance" — with a real ticker, registration happens near
// -instantly at goroutine start, long before the short interval elapses.
func TestRunTicksAndStopsOnContextCancel(t *testing.T) {
	syms := []string{"US.AAPL"}
	fr := newFakeRequester()
	fr.snapshot = &snappb.Response{
		RetType: proto.Int32(0),
		S2C:     &snappb.S2C{SnapshotList: []*snappb.Snapshot{snapshotFor("AAPL", true)}},
	}
	fr.ownerPlate = &ownerplatepb.Response{RetType: proto.Int32(0), S2C: &ownerplatepb.S2C{}}
	fr.staticInfo = &staticpb.Response{RetType: proto.Int32(0), S2C: &staticpb.S2C{}}
	pub := &fakePublisher{}
	p := New(config.StockInfo{Enabled: true, RefreshMs: 5, MaxPerReq: 400}, fr, pub, clock.System{}, func() []string { return syms }, newFakeBars())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for len(pub.snapshotCalls()) == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for a publish after at least one tick")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func TestRunDisabledReturnsImmediately(t *testing.T) {
	fr := newFakeRequester()
	pub := &fakePublisher{}
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: false, RefreshMs: 1000}, fr, pub, clk, func() []string { return []string{"US.AAPL"} }, newFakeBars())

	done := make(chan error, 1)
	go func() { done <- p.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("want nil error when disabled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return immediately when disabled")
	}
}

func TestChunkSplitsIntoSizedGroups(t *testing.T) {
	syms := []string{"a", "b", "c", "d", "e"}
	got := chunk(syms, 2)
	want := [][]string{{"a", "b"}, {"c", "d"}, {"e"}}
	if len(got) != len(want) {
		t.Fatalf("want %d chunks, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("chunk %d length mismatch: got %v want %v", i, got[i], want[i])
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("chunk %d mismatch: got %v want %v", i, got[i], want[i])
			}
		}
	}
}

func TestCodeOfAndSymbolOf(t *testing.T) {
	if got := codeOf("US.AAPL"); got != "AAPL" {
		t.Fatalf("codeOf wrong: %q", got)
	}
	if got := symbolOf(sec("AAPL")); got != "US.AAPL" {
		t.Fatalf("symbolOf wrong: %q", got)
	}
	if got := symbolOf(nil); got != "" {
		t.Fatalf("symbolOf(nil) should be empty, got %q", got)
	}
}

// ---- exchLabel (pure) ----

func TestExchLabelMapsKnownUSVenues(t *testing.T) {
	cases := []struct {
		t    qotcommon.ExchType
		want string
	}{
		{qotcommon.ExchType_ExchType_US_NYSE, "NYSE"},
		{qotcommon.ExchType_ExchType_US_Nasdaq, "NASDAQ"},
		{qotcommon.ExchType_ExchType_US_AMEX, "AMEX"},
		{qotcommon.ExchType_ExchType_US_Pink, "OTC"},
		{qotcommon.ExchType_ExchType_US_Option, ""}, // not a US equity venue this panel names
		{0, ""}, // unset/unknown
	}
	for _, c := range cases {
		if got := exchLabel(int32(c.t)); got != c.want {
			t.Fatalf("exchLabel(%v) = %q, want %q", c.t, got, c.want)
		}
	}
}

// ---- resolveExchanges / staticInfoChunk ----

// staticInfoBatchOrPoison is batchOrPoison's 3202 counterpart: any request
// containing poisonCode fails the whole batch; any other request succeeds
// with a resolved NASDAQ exchange for every code present.
func staticInfoBatchOrPoison(poisonCode string) func(codes []string) *staticpb.Response {
	return func(codes []string) *staticpb.Response {
		for _, c := range codes {
			if c == poisonCode {
				return &staticpb.Response{RetType: proto.Int32(-1), RetMsg: proto.String("no static info: " + poisonCode)}
			}
		}
		list := make([]*qotcommon.SecurityStaticInfo, 0, len(codes))
		for _, c := range codes {
			list = append(list, staticInfoFor(c, qotcommon.ExchType_ExchType_US_Nasdaq))
		}
		return &staticpb.Response{RetType: proto.Int32(0), S2C: &staticpb.S2C{StaticInfoList: list}}
	}
}

func TestFetchTickSetsExchangeFromStaticInfo(t *testing.T) {
	syms := []string{"US.AAPL", "US.KO"}
	fr := newFakeRequester()
	fr.snapshot = &snappb.Response{
		RetType: proto.Int32(0),
		S2C: &snappb.S2C{SnapshotList: []*snappb.Snapshot{
			snapshotFor("AAPL", true),
			snapshotFor("KO", true),
		}},
	}
	fr.ownerPlate = &ownerplatepb.Response{RetType: proto.Int32(0), S2C: &ownerplatepb.S2C{}}
	fr.staticInfo = &staticpb.Response{
		RetType: proto.Int32(0),
		S2C: &staticpb.S2C{StaticInfoList: []*qotcommon.SecurityStaticInfo{
			staticInfoFor("AAPL", qotcommon.ExchType_ExchType_US_Nasdaq),
			staticInfoFor("KO", qotcommon.ExchType_ExchType_US_NYSE),
		}},
	}
	pub := &fakePublisher{}
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, pub, clk, func() []string { return syms }, newFakeBars())

	p.fetchTick(context.Background())

	got := map[string]string{}
	for _, c := range pub.calls {
		got[c.key] = c.payload.(wsmsg.StockDetailPayload).Exchange
	}
	if got["US.AAPL"] != "NASDAQ" {
		t.Fatalf("US.AAPL exchange = %q, want NASDAQ", got["US.AAPL"])
	}
	if got["US.KO"] != "NYSE" {
		t.Fatalf("US.KO exchange = %q, want NYSE", got["US.KO"])
	}
}

func TestFetchTickExchangeCachedAcrossTicksNoRepeatStaticInfoRequest(t *testing.T) {
	syms := []string{"US.AAPL"}
	fr := newFakeRequester()
	fr.snapshot = &snappb.Response{
		RetType: proto.Int32(0),
		S2C:     &snappb.S2C{SnapshotList: []*snappb.Snapshot{snapshotFor("AAPL", true)}},
	}
	fr.ownerPlate = &ownerplatepb.Response{RetType: proto.Int32(0), S2C: &ownerplatepb.S2C{}}
	fr.staticInfo = &staticpb.Response{
		RetType: proto.Int32(0),
		S2C: &staticpb.S2C{StaticInfoList: []*qotcommon.SecurityStaticInfo{
			staticInfoFor("AAPL", qotcommon.ExchType_ExchType_US_Nasdaq),
		}},
	}
	pub := &fakePublisher{}
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, pub, clk, func() []string { return syms }, newFakeBars())

	p.fetchTick(context.Background())
	if fr.calls[opend.ProtoQotGetStaticInfo] != 1 {
		t.Fatalf("first tick should issue exactly 1 static-info request, got %d", fr.calls[opend.ProtoQotGetStaticInfo])
	}

	p.fetchTick(context.Background())
	if fr.calls[opend.ProtoQotGetStaticInfo] != 1 {
		t.Fatalf("second tick should issue zero additional static-info requests (cached), got total %d", fr.calls[opend.ProtoQotGetStaticInfo])
	}
	payload := pub.calls[1].payload.(wsmsg.StockDetailPayload)
	if payload.Exchange != "NASDAQ" {
		t.Fatalf("second-tick payload should still carry the cached exchange: %+v", payload)
	}
}

func TestResolveExchangesIsolatesBadSymbolAndCachesEmptyOnPersistentFailure(t *testing.T) {
	syms := []string{"US.AAA", "US.BBB", "US.CCC"}
	fr := newFakeRequester()
	fr.staticInfoFn = staticInfoBatchOrPoison("BBB")
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, &fakePublisher{}, clock.NewFake(time.Now()), func() []string { return syms }, newFakeBars())

	p.resolveExchanges(context.Background(), syms)

	if got := p.exch["US.AAA"]; got != "NASDAQ" {
		t.Fatalf("US.AAA should resolve its real exchange despite US.BBB poisoning the initial batch, got %q", got)
	}
	if got := p.exch["US.CCC"]; got != "NASDAQ" {
		t.Fatalf("US.CCC should resolve its real exchange despite US.BBB poisoning the initial batch, got %q", got)
	}
	got, ok := p.exch["US.BBB"]
	if !ok {
		t.Fatalf("US.BBB should be cached (as absent) once isolated as the persistently-failing symbol, got no entry at all")
	}
	if got != "" {
		t.Fatalf("US.BBB should cache as empty-exchange once isolated alone, got %q", got)
	}

	totalCallsBefore := fr.calls[opend.ProtoQotGetStaticInfo]
	p.resolveExchanges(context.Background(), syms)
	if got := fr.calls[opend.ProtoQotGetStaticInfo]; got != totalCallsBefore {
		t.Fatalf("second resolveExchanges call should issue zero further static-info requests (all cached), total went from %d to %d", totalCallsBefore, got)
	}
}

// ---- ema200For ----

func TestEma200ForReturnsNilWhenFewerThanPeriodBars(t *testing.T) {
	bars := newFakeBars()
	closes := make([]float64, ema200Period-1)
	for i := range closes {
		closes[i] = 100 + float64(i)
	}
	bars.bars["US.IPO"] = closesBars("US.IPO", closes...)
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000}, newFakeRequester(), &fakePublisher{}, clk, func() []string { return nil }, bars)

	if got := p.ema200For("US.IPO"); got != nil {
		t.Fatalf("want nil EMA-200 with only %d bars, got %v", len(closes), *got)
	}
}

func TestEma200ForMatchesMdEMAWhenEnoughBars(t *testing.T) {
	bars := newFakeBars()
	closes := make([]float64, ema200Period+10)
	for i := range closes {
		closes[i] = 100 + float64(i%7) // some variation, not monotonic
	}
	bars.bars["US.AAPL"] = closesBars("US.AAPL", closes...)
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000}, newFakeRequester(), &fakePublisher{}, clk, func() []string { return nil }, bars)

	want, ok := md.EMA(closes, ema200Period)
	if !ok {
		t.Fatalf("md.EMA should have succeeded with %d closes", len(closes))
	}
	got := p.ema200For("US.AAPL")
	if got == nil || *got != want {
		t.Fatalf("ema200For = %v, want %v", got, want)
	}
}

func TestEma200ForCachedPerDayNoRepeatReadDailyBars(t *testing.T) {
	bars := newFakeBars()
	closes := make([]float64, ema200Period)
	for i := range closes {
		closes[i] = 100 + float64(i)
	}
	bars.bars["US.AAPL"] = closesBars("US.AAPL", closes...)
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000}, newFakeRequester(), &fakePublisher{}, clk, func() []string { return nil }, bars)

	p.ema200For("US.AAPL")
	if bars.readCalls["US.AAPL"] != 1 {
		t.Fatalf("first call should read the archive once, got %d reads", bars.readCalls["US.AAPL"])
	}

	clk.Advance(time.Hour) // same UTC day
	p.ema200For("US.AAPL")
	if bars.readCalls["US.AAPL"] != 1 {
		t.Fatalf("same-day call should be served from cache, got %d reads", bars.readCalls["US.AAPL"])
	}

	clk.Advance(24 * time.Hour) // crosses into the next UTC day
	p.ema200For("US.AAPL")
	if bars.readCalls["US.AAPL"] != 2 {
		t.Fatalf("next-day call should re-read the archive, got %d reads", bars.readCalls["US.AAPL"])
	}
}

func TestFetchTickSetsEma200Field(t *testing.T) {
	syms := []string{"US.AAPL"}
	fr := newFakeRequester()
	fr.snapshot = &snappb.Response{
		RetType: proto.Int32(0),
		S2C:     &snappb.S2C{SnapshotList: []*snappb.Snapshot{snapshotFor("AAPL", true)}},
	}
	fr.ownerPlate = &ownerplatepb.Response{RetType: proto.Int32(0), S2C: &ownerplatepb.S2C{}}
	fr.staticInfo = &staticpb.Response{RetType: proto.Int32(0), S2C: &staticpb.S2C{}}
	bars := newFakeBars()
	closes := make([]float64, ema200Period)
	for i := range closes {
		closes[i] = 100 + float64(i)
	}
	bars.bars["US.AAPL"] = closesBars("US.AAPL", closes...)
	pub := &fakePublisher{}
	clk := clock.NewFake(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	p := New(config.StockInfo{Enabled: true, RefreshMs: 1000, MaxPerReq: 400}, fr, pub, clk, func() []string { return syms }, bars)

	p.fetchTick(context.Background())

	want, _ := md.EMA(closes, ema200Period)
	payload := pub.calls[0].payload.(wsmsg.StockDetailPayload)
	if payload.Ema200 == nil || *payload.Ema200 != want {
		t.Fatalf("payload.Ema200 = %v, want %v", payload.Ema200, want)
	}
}
