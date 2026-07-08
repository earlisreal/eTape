package scan

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
	tmrpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgettopmoversrank"
	ahpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusafterhoursrank"
	onpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusovernightrank"
	rankpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetuspremarketrank"
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

func TestResetIfNewDayClearsFloatCacheAndSeen(t *testing.T) {
	p := &Poller{
		floats:  map[string]floatEntry{"US.A": {shares: 1}},
		seen:    map[string]map[string]bool{"premarket": {"US.A": true}},
		seenDay: session.DayMs(time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC).UnixMilli()),
	}
	p.resetIfNewDay(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)) // different ET day
	if len(p.floats) != 0 {
		t.Fatalf("float cache should clear on new day: %+v", p.floats)
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
	snap         func(codes []string) (*snappb.Response, error)
	snapCalls    int
}

func (f *fakeReq) Request(_ context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	switch protoID {
	case opend.ProtoQotGetUSPreMarketRank:
		if f.rankErr != nil {
			return opend.Frame{}, f.rankErr
		}
		return frameOf(f.rankResp), nil
	case opend.ProtoQotGetTopMoversRank:
		return frameOf(f.topMoversRsp), nil
	case opend.ProtoQotGetUSAfterHoursRank:
		return frameOf(f.afterHrsRsp), nil
	case opend.ProtoQotGetUSOvernightRank:
		return frameOf(f.overnightRsp), nil
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

func snapResp(snaps ...*snappb.Snapshot) *snappb.Response {
	return &snappb.Response{RetType: proto.Int32(0), S2C: &snappb.S2C{SnapshotList: snaps}}
}

func snapErrResp(msg string) *snappb.Response {
	return &snappb.Response{RetType: proto.Int32(1), RetMsg: proto.String(msg)}
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

func newTestPoller(cfg config.Scan, fr *fakeReq, pub *capturePub) *Poller {
	return New(cfg, fr, pub, clock.NewFake(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)))
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

func TestPollOnceEndToEnd(t *testing.T) {
	fr := &fakeReq{
		rankResp: rankResp(
			rankItem{Symbol: "US.LOWF", ChangePct: 12, Last: 4, Volume: 300_000},  // passes
			rankItem{Symbol: "US.BIGF", ChangePct: 20, Last: 8, Volume: 900_000},  // over float cap
			rankItem{Symbol: "US.THIN", ChangePct: 30, Last: 1, Volume: 5_000},    // under volume floor
		),
		snap: func(codes []string) (*snappb.Response, error) {
			return snapResp(
				snap("LOWF", 20_000_000, true),
				snap("BIGF", 500_000_000, true),
				snap("THIN", 1_000_000, true),
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
	if fr.snapCalls != 1 {
		t.Fatalf("float cache should make the second poll issue zero snapshots: snapCalls=%d", fr.snapCalls)
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
