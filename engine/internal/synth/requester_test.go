package synth

import (
	"context"
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/session"

	ownerplatepb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetownerplate"
	newspb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsearchnews"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
	staticpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetstaticinfo"
	tmrpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgettopmoversrank"
	rankpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetuspremarketrank"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
)

// newSteppedGenerator builds a Generator and advances it long enough for
// ticks/volume/quotes to be non-trivial, so tests exercise real generator
// state rather than the all-zero t=0 snapshot.
func newSteppedGenerator(seed int64) *Generator {
	start := int64(1_700_000_000_000)
	g := New(seed, clock.NewFake(timeMs(start)))
	now := start
	for i := 0; i < 500; i++ {
		now += 250
		g.StepTo(now)
	}
	return g
}

// codeFromRankRow mirrors scan.go's symbolOf: "US." + Security.GetCode() -
// the exact getter chain fetchPreMarket/fetchTopMovers/etc. use to recover
// eTape's internal symbol from a rank row's wire Security.
func codeFromRankRow(sec *qotcommon.Security) string {
	return "US." + sec.GetCode()
}

func TestRequester_PreMarketRank_UnmarshalsWithUniverseRows(t *testing.T) {
	g := newSteppedGenerator(5)
	r := NewRequester(g)
	fr, err := r.Request(context.Background(), opend.ProtoQotGetUSPreMarketRank,
		&rankpb.Request{}) // C2S paging ignored by the synth requester
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	var resp rankpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.GetRetType() != 0 {
		t.Fatalf("retType %d", resp.GetRetType())
	}
	rows := resp.GetS2C().GetDataList()
	if len(rows) == 0 {
		t.Fatal("no rank rows for synthetic universe")
	}

	uni := map[string]RankRow{}
	for _, rr := range g.RankRows() {
		uni[rr.Code] = rr
	}
	if len(rows) != len(uni) {
		t.Fatalf("got %d rank rows, want %d (one per universe symbol)", len(rows), len(uni))
	}
	for _, row := range rows {
		// Exact getter chain scan.go's fetchPreMarket uses (scan.go:319-322):
		// d.GetSecurity(), d.GetPreMarketChangeRatio(), d.GetPreMarketPrice(),
		// d.GetPreMarketVolume().
		code := codeFromRankRow(row.GetSecurity())
		want, ok := uni[code]
		if !ok {
			t.Fatalf("rank row for non-universe code %q", code)
		}
		if got := row.GetPreMarketChangeRatio(); got != want.PctChange {
			t.Errorf("%s: PreMarketChangeRatio = %v, want %v", code, got, want.PctChange)
		}
		if got := row.GetPreMarketPrice(); got != want.Last {
			t.Errorf("%s: PreMarketPrice = %v, want %v", code, got, want.Last)
		}
		if got := row.GetPreMarketVolume(); got != want.Volume {
			t.Errorf("%s: PreMarketVolume = %v, want %v", code, got, want.Volume)
		}
	}
}

// TestRequester_TopMoversRank_MatchesGeneratorRankRows cross-checks 3413
// against scan.go's fetchTopMovers getter chain (scan.go:342-344):
// d.GetSecurity(), d.GetChangeRatio(), d.GetCurPrice(), d.GetVolume().
func TestRequester_TopMoversRank_MatchesGeneratorRankRows(t *testing.T) {
	g := newSteppedGenerator(6)
	r := NewRequester(g)
	fr, err := r.Request(context.Background(), opend.ProtoQotGetTopMoversRank, &tmrpb.Request{})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	var resp tmrpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.GetRetType() != 0 {
		t.Fatalf("retType %d", resp.GetRetType())
	}

	uni := map[string]RankRow{}
	for _, rr := range g.RankRows() {
		uni[rr.Code] = rr
	}
	rows := resp.GetS2C().GetDataList()
	if len(rows) != len(uni) {
		t.Fatalf("got %d rows, want %d", len(rows), len(uni))
	}
	for _, row := range rows {
		code := codeFromRankRow(row.GetSecurity())
		want, ok := uni[code]
		if !ok {
			t.Fatalf("row for non-universe code %q", code)
		}
		if got := row.GetChangeRatio(); got != want.PctChange {
			t.Errorf("%s: ChangeRatio = %v, want %v", code, got, want.PctChange)
		}
		if got := row.GetCurPrice(); got != want.Last {
			t.Errorf("%s: CurPrice = %v, want %v", code, got, want.Last)
		}
		if got := row.GetVolume(); got != want.Volume {
			t.Errorf("%s: Volume = %v, want %v", code, got, want.Volume)
		}
	}
}

// TestRequester_StaticInfo_ExchTypeAndSecurity cross-checks 3202 against the
// exact getter chain scan.go's staticInfoBatch success path uses
// (scan.go:478-482): info.GetBasic(), basic.GetSecurity() (via symbolOf),
// basic.GetExchType(). The synth universe is never OTC/Pink, so ExchType
// must resolve to a real (non-Pink) exchange for every requested symbol,
// matching scan.go's dropOTC never dropping a synthetic symbol.
func TestRequester_StaticInfo_ExchTypeAndSecurity(t *testing.T) {
	g := newSteppedGenerator(7)
	r := NewRequester(g)
	codes := g.Symbols()
	if len(codes) == 0 {
		t.Fatal("empty universe")
	}

	// Build the request exactly as scan.go's staticInfoBatch does: bare
	// wire codes (codeOf strips "US."), Market = US.
	secs := make([]*qotcommon.Security, 0, len(codes))
	for _, c := range codes {
		secs = append(secs, &qotcommon.Security{
			Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
			Code:   proto.String(wireCode(c)),
		})
	}
	fr, err := r.Request(context.Background(), opend.ProtoQotGetStaticInfo,
		&staticpb.Request{C2S: &staticpb.C2S{SecurityList: secs}})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	var resp staticpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.GetRetType() != 0 {
		t.Fatalf("retType %d", resp.GetRetType())
	}

	infos := resp.GetS2C().GetStaticInfoList()
	if len(infos) != len(codes) {
		t.Fatalf("got %d static infos, want %d (one per requested symbol)", len(infos), len(codes))
	}
	seen := map[string]bool{}
	for _, info := range infos {
		basic := info.GetBasic()
		code := codeFromRankRow(basic.GetSecurity())
		if !g.Has(code) {
			t.Fatalf("static info for non-universe code %q", code)
		}
		seen[code] = true
		if qotcommon.ExchType(basic.GetExchType()) == qotcommon.ExchType_ExchType_US_Pink {
			t.Errorf("%s: ExchType resolved to OTC/Pink - synth universe should never be OTC", code)
		}
		if basic.GetExchType() == 0 {
			t.Errorf("%s: ExchType unset (0), want a real exchange", code)
		}
	}
	for _, c := range codes {
		if !seen[c] {
			t.Errorf("missing static info for requested symbol %s", c)
		}
	}
}

// TestRequester_Snapshot_MatchesGeneratorFundamentalsAndQuote cross-checks
// 3203 against the exact getter chains stockinfo.go's snapshotToPayload and
// scan.go's snapshotBatch use: GetBasic().{GetName,GetCurPrice,
// GetLastClosePrice,GetVolume,GetHighest52WeeksPrice,GetLowest52WeeksPrice},
// GetEquityExData().{GetIssuedShares,GetOutstandingShares,GetIssuedMarketVal,
// GetOutstandingMarketVal,GetPeRate,GetPeTTMRate,GetEarningsPershare}.
func TestRequester_Snapshot_MatchesGeneratorFundamentalsAndQuote(t *testing.T) {
	g := newSteppedGenerator(8)
	r := NewRequester(g)
	code := g.Symbols()[0]

	fr, err := r.Request(context.Background(), opend.ProtoQotGetSecuritySnapshot,
		&snappb.Request{C2S: &snappb.C2S{SecurityList: []*qotcommon.Security{{
			Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
			Code:   proto.String(wireCode(code)),
		}}}})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	var resp snappb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.GetRetType() != 0 {
		t.Fatalf("retType %d", resp.GetRetType())
	}

	snaps := resp.GetS2C().GetSnapshotList()
	if len(snaps) != 1 {
		t.Fatalf("got %d snapshots, want exactly 1 for the single requested symbol", len(snaps))
	}
	sn := snaps[0]
	basic := sn.GetBasic()
	if got := codeFromRankRow(basic.GetSecurity()); got != code {
		t.Fatalf("snapshot for %q, want %q", got, code)
	}

	wantQ, ok := g.QuoteOf(code)
	if !ok {
		t.Fatalf("QuoteOf(%s): not found", code)
	}
	wantF, ok := g.Fundamentals(code)
	if !ok {
		t.Fatalf("Fundamentals(%s): not found", code)
	}

	if got := basic.GetName(); got == "" {
		t.Error("Name is empty")
	}
	if got := basic.GetCurPrice(); got != wantQ.Last {
		t.Errorf("CurPrice = %v, want %v", got, wantQ.Last)
	}
	if got := basic.GetLastClosePrice(); got != wantQ.PrevClose {
		t.Errorf("LastClosePrice = %v, want %v", got, wantQ.PrevClose)
	}
	if got := basic.GetVolume(); got != wantQ.Volume {
		t.Errorf("Volume = %v, want %v", got, wantQ.Volume)
	}
	if got := basic.GetHighest52WeeksPrice(); got != wantF.High52Wk {
		t.Errorf("Highest52WeeksPrice = %v, want %v", got, wantF.High52Wk)
	}
	if got := basic.GetLowest52WeeksPrice(); got != wantF.Low52Wk {
		t.Errorf("Lowest52WeeksPrice = %v, want %v", got, wantF.Low52Wk)
	}

	ex := sn.GetEquityExData()
	if ex == nil {
		t.Fatal("EquityExData is nil - scan.go's float resolution would mark this symbol bad")
	}
	if got := ex.GetOutstandingShares(); got != wantF.FloatShares {
		t.Errorf("OutstandingShares (float) = %v, want %v", got, wantF.FloatShares)
	}
	if got := ex.GetIssuedShares(); got != wantF.SharesOut {
		t.Errorf("IssuedShares = %v, want %v", got, wantF.SharesOut)
	}
	if got, want := ex.GetIssuedMarketVal(), float64(wantF.SharesOut)*wantQ.Last; got != want {
		t.Errorf("IssuedMarketVal = %v, want %v", got, want)
	}
	if got, want := ex.GetOutstandingMarketVal(), float64(wantF.FloatShares)*wantQ.Last; got != want {
		t.Errorf("OutstandingMarketVal = %v, want %v", got, want)
	}
	// PE/PE-TTM/EPS: the generator models no valuation data, and
	// stockinfo.go's own doc comment treats an exact-0.0 ex-data field as a
	// legitimate value it takes as-is (not a null it special-cases), so 0
	// here is a real, poller-anticipated value.
	if got := ex.GetPeRate(); got != 0 {
		t.Errorf("PeRate = %v, want 0 (unmodeled)", got)
	}
	if got := ex.GetPeTTMRate(); got != 0 {
		t.Errorf("PeTTMRate = %v, want 0 (unmodeled)", got)
	}
	if got := ex.GetEarningsPershare(); got != 0 {
		t.Errorf("EarningsPershare = %v, want 0 (unmodeled)", got)
	}

	// scan.go's snapshotBatch bad-float guard (scan.go:570): a synthetic
	// symbol must never trip "ex == nil || OutstandingShares <= 0".
	if ex.GetOutstandingShares() <= 0 {
		t.Error("OutstandingShares <= 0 - scan.go would mark this symbol's float unresolvable")
	}
}

// TestRequester_SearchNews_MatchesGeneratorQuote cross-checks 3263 against
// the exact getter chain news.go's pollSymbol uses (news.go:89-93):
// n.GetTitle(), n.GetSource(), n.GetUrl(), n.GetNewsSubType(),
// n.GetPublishTime(), n.GetViewCount(). PublishTime must parse with the same
// format news.go's parsePublishTime expects
// ("2006-01-02 15:04:05" in session.Loc()), or the published timestamp is
// silently dropped upstream.
func TestRequester_SearchNews_MatchesGeneratorQuote(t *testing.T) {
	g := newSteppedGenerator(9)
	r := NewRequester(g)
	code := g.Symbols()[0] // "US.<code>", exactly what news.go's Keyword carries

	fr, err := r.Request(context.Background(), opend.ProtoQotGetSearchNews,
		&newspb.Request{C2S: &newspb.C2S{Keyword: proto.String(code), MaxCount: proto.Int32(10)}})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	var resp newspb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.GetRetType() != 0 {
		t.Fatalf("retType %d", resp.GetRetType())
	}

	items := resp.GetS2C().GetSearchNewsList()
	if len(items) == 0 {
		t.Fatal("no news items for a valid universe symbol")
	}

	wantQ, ok := g.QuoteOf(code)
	if !ok {
		t.Fatalf("QuoteOf(%s): not found", code)
	}

	for _, n := range items {
		if n.GetTitle() == "" {
			t.Error("Title is empty")
		}
		if n.GetSource() == "" {
			t.Error("Source is empty")
		}
		if n.GetUrl() == "" {
			t.Error("Url is empty")
		}
		if got := n.GetViewCount(); got != wantQ.Volume {
			t.Errorf("ViewCount = %v, want %v (generator volume)", got, wantQ.Volume)
		}
		// Exact parse news.go's parsePublishTime performs (news.go:133-139).
		pt, err := time.ParseInLocation("2006-01-02 15:04:05", n.GetPublishTime(), session.Loc())
		if err != nil {
			t.Errorf("PublishTime %q does not parse as news.go expects: %v", n.GetPublishTime(), err)
		}
		if math.Abs(float64(pt.UnixMilli()-wantQ.TsMs)) > 1000 {
			t.Errorf("PublishTime %v too far from generator time %v", pt, time.UnixMilli(wantQ.TsMs))
		}
	}
}

// TestRequester_UnknownProtoID_DegradesQuietly checks that a protoID this
// package doesn't implement (3207/Qot_GetOwnerPlate, which stockinfo.go's
// resolveIndustries issues but this task's brief doesn't cover) returns an
// error-free frame - not ErrRequestTimeout - whose Body decodes cleanly (no
// decode error: a genuinely empty Body would fail proto.Unmarshal's
// required-field check, since RetType is proto2 "req") into the real
// response type as that type's ordinary "no data" RetType (-400), which
// stockinfo.go's ownerPlateChunk already treats as an ordinary, non-fatal
// isolate-and-cache-absent case (RetType != 0 branch), not a transport/decode
// failure that retries forever.
func TestRequester_UnknownProtoID_DegradesQuietly(t *testing.T) {
	g := newSteppedGenerator(10)
	r := NewRequester(g)

	fr, err := r.Request(context.Background(), opend.ProtoQotGetOwnerPlate, &ownerplatepb.Request{})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if fr.ProtoID != opend.ProtoQotGetOwnerPlate {
		t.Errorf("ProtoID = %d, want %d", fr.ProtoID, opend.ProtoQotGetOwnerPlate)
	}

	var resp ownerplatepb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		t.Fatalf("unmarshal fallback body: %v", err)
	}
	if resp.GetRetType() == 0 {
		t.Error("fallback body decoded RetType 0 (success) - expected the type's default failure sentinel")
	}
	if resp.GetS2C().GetOwnerPlateList() != nil {
		t.Error("fallback body decoded a non-nil OwnerPlateList - expected no data")
	}
}

func TestRequester_ProbeRTT(t *testing.T) {
	g := newSteppedGenerator(11)
	r := NewRequester(g)
	d, err := r.ProbeRTT(context.Background())
	if err != nil {
		t.Fatalf("ProbeRTT: %v", err)
	}
	if d != 2*time.Millisecond {
		t.Errorf("ProbeRTT = %v, want 2ms", d)
	}
}
