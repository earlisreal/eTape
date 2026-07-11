// This file answers the real moomoo OpenD wire protocol (a handful of
// request/response protoIDs) from a synthetic *Generator's state, so the
// UNCHANGED scan/stockinfo/news pollers (engine/internal/scan/scan.go,
// .../stockinfo/stockinfo.go, .../news/news.go) work against demo data
// without knowing they aren't talking to a real OpenD connection.
//
// Field-fidelity: each build<Proto>Response helper below populates exactly
// the pb fields the corresponding real poller function dereferences
// (verified field-by-field against the three files above), plus whatever
// singular fields protoc-gen-go marked "req" (proto2 required) on a message
// this package chooses to populate at all - proto.Marshal (default
// AllowPartial=false) errors out on any unset required field even if no
// poller ever reads it back, so those get an explicit zero-value
// placeholder. That placeholder is wire-format filler, not fabricated data:
// it's never read by anything.
package synth

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/session"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	newspb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsearchnews"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
	staticpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetstaticinfo"
	tmrpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgettopmoversrank"
	ahpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusafterhoursrank"
	onpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusovernightrank"
	rankpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetuspremarketrank"
)

// unknownProtoIDBody is the wire-format encoding of {retType: -400} - the
// shared "field 1, required int32, default -400" RetType convention every
// generated moomoo Response type in this codebase uses. It's built once
// from an arbitrary already-imported Response type (rankpb's): the wire
// bytes for a single required int32 field don't depend on which generated
// Go type produced them, so the same bytes decode cleanly into any other
// Response type sharing the convention (verified: qotgetstaticinfo,
// qotgetsecuritysnapshot, qotgetsearchnews, and qotgetownerplate - the one
// protoID this package's tests exercise as "unrecognized" - all declare
// Default_Response_RetType = -400 on an identical req/varint/field-1
// RetType). Panics at init if that ever stops being true, so a future
// codegen drift fails loudly at test/startup time rather than silently
// shipping an unparsable fallback frame.
var unknownProtoIDBody = mustMarshal(&rankpb.Response{RetType: proto.Int32(rankpb.Default_Response_RetType)})

func mustMarshal(m proto.Message) []byte {
	b, err := proto.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("synth: precompute unknownProtoIDBody: %v", err))
	}
	return b
}

// Requester wraps a synthetic *Generator to answer OpenD's request/response
// protocols. It has no network connection and no request/response
// correlation to do - Request just builds the answer synchronously from
// current generator state.
type Requester struct {
	gen *Generator
}

// NewRequester wraps gen to answer OpenD's request/response protocols.
func NewRequester(gen *Generator) *Requester {
	return &Requester{gen: gen}
}

// Request builds the pb Response for protoID from generator state and
// returns it marshaled into an opend.Frame. Its signature mirrors
// *opend.Client.Request exactly, so it satisfies scan.go/stockinfo.go/
// news.go's local `requester` interfaces without any of those files
// changing. Every handled protoID's response reports RetType 0 (success).
//
// An unrecognized protoID (e.g. 3207/Qot_GetOwnerPlate, which stockinfo.go
// issues but this package doesn't implement - only the 7 protoIDs named in
// the task brief are handled) returns unknownProtoIDBody: valid wire bytes
// for "{retType: -400}", the shared required-field/default convention every
// moomoo Response type in this codebase uses for "no real value". A plain
// empty Body will not do here - google.golang.org/protobuf's proto.Unmarshal
// enforces required-field presence by default (same as Marshal), so decoding
// zero bytes into a Response whose RetType is marked required (proto2 "req")
// fails outright rather than silently zero-valuing. unknownProtoIDBody
// decodes cleanly into whatever Response type the caller actually expects,
// carrying that type's ordinary "no data" RetType, which the real pollers
// already treat as a non-fatal application failure (isolate/cache absent
// and move on) - never a crash, and no decode-error log repeating forever.
func (r *Requester) Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	if err := ctx.Err(); err != nil {
		return opend.Frame{}, err
	}

	var resp proto.Message
	switch protoID {
	case opend.ProtoQotGetUSPreMarketRank:
		resp = buildPreMarketRankResponse(r.gen)
	case opend.ProtoQotGetTopMoversRank:
		resp = buildTopMoversRankResponse(r.gen)
	case opend.ProtoQotGetUSAfterHoursRank:
		resp = buildAfterHoursRankResponse(r.gen)
	case opend.ProtoQotGetUSOvernightRank:
		resp = buildOvernightRankResponse(r.gen)
	case opend.ProtoQotGetStaticInfo:
		resp = buildStaticInfoResponse(r.gen, req)
	case opend.ProtoQotGetSecuritySnapshot:
		resp = buildSnapshotResponse(r.gen, req)
	case opend.ProtoQotGetSearchNews:
		resp = buildSearchNewsResponse(r.gen, req)
	default:
		return opend.Frame{ProtoID: protoID, FmtType: opend.FmtProtobuf, Body: unknownProtoIDBody}, nil
	}

	body, err := proto.Marshal(resp)
	if err != nil {
		return opend.Frame{}, fmt.Errorf("synth: marshal response for protoID %d: %w", protoID, err)
	}
	return opend.Frame{ProtoID: protoID, FmtType: opend.FmtProtobuf, Body: body}, nil
}

// ProbeRTT satisfies health.prober so the demo's "moomoo" health link
// reports a small, constant, healthy RTT - there's no real network hop to
// measure.
func (r *Requester) ProbeRTT(ctx context.Context) (time.Duration, error) {
	return 2 * time.Millisecond, nil
}

// usSecurity builds a qotcommon.Security for genCode (the Generator's own
// "US.<code>" key), stripping the "US." prefix for the wire value: real
// moomoo Security.Code values never carry a country prefix - "US." is
// purely eTape's own symbolOf/codeOf convention, applied by the real
// pollers themselves on both sides of the wire (scan.go/stockinfo.go strip
// it via codeOf before building a request Security, and reconstruct it via
// symbolOf after reading a response Security).
func usSecurity(genCode string) *qotcommon.Security {
	return &qotcommon.Security{
		Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
		Code:   proto.String(wireCode(genCode)),
	}
}

// wireCode strips the Generator's "US." prefix for a wire Security.Code.
func wireCode(genCode string) string {
	return strings.TrimPrefix(genCode, "US.")
}

// genCodeOf re-adds the "US." prefix the real pollers strip (via their own
// codeOf helper) before building a wire Security.Code, so the result can be
// looked up in the Generator's "US.<code>" key space. Idempotent: a symbol
// that already carries the prefix (e.g. news.go's Keyword, which main.go
// feeds it straight from the scanner pool / active demand set in "US.<code>"
// form and is never round-tripped through codeOf/symbolOf) passes through
// unchanged.
func genCodeOf(wire string) string {
	if strings.HasPrefix(wire, "US.") {
		return wire
	}
	return "US." + wire
}

// --- rank protocols: 3410 pre-market, 3413 top movers/RTH, 3411 after
// hours, 3412 overnight. All four share the same shape: per RankRow,
// Security + this session's change-ratio/price/volume triple. Rank rows
// never carry float/name/turnover (scan.go gets float separately via 3203).

func buildPreMarketRankResponse(g *Generator) *rankpb.Response {
	rows := g.RankRows()
	items := make([]*rankpb.PreMarketRankItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, &rankpb.PreMarketRankItem{
			Security:             usSecurity(row.Code),
			PreMarketChangeRatio: proto.Float64(row.PctChange),
			PreMarketPrice:       proto.Float64(row.Last),
			PreMarketVolume:      proto.Int64(row.Volume),
		})
	}
	return &rankpb.Response{RetType: proto.Int32(0), S2C: &rankpb.S2C{DataList: items}}
}

func buildTopMoversRankResponse(g *Generator) *tmrpb.Response {
	rows := g.RankRows()
	items := make([]*tmrpb.TopMoversRankItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, &tmrpb.TopMoversRankItem{
			Security:    usSecurity(row.Code),
			ChangeRatio: proto.Float64(row.PctChange),
			CurPrice:    proto.Float64(row.Last),
			Volume:      proto.Int64(row.Volume),
		})
	}
	return &tmrpb.Response{RetType: proto.Int32(0), S2C: &tmrpb.S2C{DataList: items}}
}

func buildAfterHoursRankResponse(g *Generator) *ahpb.Response {
	rows := g.RankRows()
	items := make([]*ahpb.AfterHoursRankItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, &ahpb.AfterHoursRankItem{
			Security:              usSecurity(row.Code),
			AfterHoursChangeRatio: proto.Float64(row.PctChange),
			AfterHoursPrice:       proto.Float64(row.Last),
			AfterHoursVolume:      proto.Int64(row.Volume),
		})
	}
	return &ahpb.Response{RetType: proto.Int32(0), S2C: &ahpb.S2C{DataList: items}}
}

func buildOvernightRankResponse(g *Generator) *onpb.Response {
	rows := g.RankRows()
	items := make([]*onpb.OvernightRankItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, &onpb.OvernightRankItem{
			Security:             usSecurity(row.Code),
			OvernightChangeRatio: proto.Float64(row.PctChange),
			OvernightPrice:       proto.Float64(row.Last),
			OvernightVolume:      proto.Int64(row.Volume),
		})
	}
	return &onpb.Response{RetType: proto.Int32(0), S2C: &onpb.S2C{DataList: items}}
}

// --- static info: 3202 ---------------------------------------------------

// requestedCodes returns the Generator codes ("US.<code>") a 3202/3203
// request actually asked about, translated from the request's bare wire
// Security.Code list via genCodeOf and filtered to symbols the Generator
// actually has. An empty/absent SecurityList (or a req of a type this
// function doesn't recognize) falls back to the whole universe, which is
// harmless in practice: scan.go/stockinfo.go never send an empty
// SecurityList (they early-return before building the request), and they
// only ever look up the symbols they asked for, ignoring anything extra in
// the response.
func requestedCodes(g *Generator, req proto.Message) []string {
	var secs []*qotcommon.Security
	switch r := req.(type) {
	case *staticpb.Request:
		secs = r.GetC2S().GetSecurityList()
	case *snappb.Request:
		secs = r.GetC2S().GetSecurityList()
	}
	if len(secs) == 0 {
		return g.Symbols()
	}
	codes := make([]string, 0, len(secs))
	for _, s := range secs {
		code := genCodeOf(s.GetCode())
		if g.Has(code) {
			codes = append(codes, code)
		}
	}
	return codes
}

// buildStaticInfoResponse populates the two fields scan.go's
// resolveExch/staticInfoBatch reads (Basic.Security, Basic.ExchType) plus
// SecurityStaticBasic's other proto2-required singular fields (Id, LotSize,
// SecType, Name, ListTime), which go unread but still need a value for
// proto.Marshal to succeed. ExchType is always NASDAQ: the synth universe
// never contains an OTC/Pink code, so dropOTC never has anything to drop.
func buildStaticInfoResponse(g *Generator, req proto.Message) *staticpb.Response {
	codes := requestedCodes(g, req)
	infos := make([]*qotcommon.SecurityStaticInfo, 0, len(codes))
	for _, code := range codes {
		infos = append(infos, &qotcommon.SecurityStaticInfo{
			Basic: &qotcommon.SecurityStaticBasic{
				Security: usSecurity(code),
				ExchType: proto.Int32(int32(qotcommon.ExchType_ExchType_US_Nasdaq)),
				// Required-but-unread wire-format filler (see doc comment above).
				Id:       proto.Int64(0),
				LotSize:  proto.Int32(0),
				SecType:  proto.Int32(int32(qotcommon.SecurityType_SecurityType_Eqty)),
				Name:     proto.String(wireCode(code)),
				ListTime: proto.String(""),
			},
		})
	}
	return &staticpb.Response{RetType: proto.Int32(0), S2C: &staticpb.S2C{StaticInfoList: infos}}
}

// --- security snapshot: 3203 --------------------------------------------

func buildSnapshotResponse(g *Generator, req proto.Message) *snappb.Response {
	codes := requestedCodes(g, req)
	snaps := make([]*snappb.Snapshot, 0, len(codes))
	for _, code := range codes {
		q, ok := g.QuoteOf(code)
		if !ok {
			continue
		}
		f, ok := g.Fundamentals(code)
		if !ok {
			continue
		}
		snaps = append(snaps, &snappb.Snapshot{
			Basic:        buildSnapshotBasic(code, q, f),
			EquityExData: buildEquityExData(q, f),
		})
	}
	return &snappb.Response{RetType: proto.Int32(0), S2C: &snappb.S2C{SnapshotList: snaps}}
}

// buildSnapshotBasic populates the fields stockinfo.go's snapshotToPayload
// and scan.go's snapshotBatch actually read (Security, Name, CurPrice,
// LastClosePrice, Volume, Highest52WeeksPrice, Lowest52WeeksPrice) plus the
// proto2-required singular fields that go unread but still need a value for
// proto.Marshal to succeed (Type, IsSuspend, ListTime, LotSize, PriceSpread,
// UpdateTime, HighPrice, OpenPrice, LowPrice, Turnover, TurnoverRate) - all
// zero-valued wire-format filler, not fabricated data.
func buildSnapshotBasic(code string, q feed.Quote, f Fundamentals) *snappb.SnapshotBasicData {
	return &snappb.SnapshotBasicData{
		Security:            usSecurity(code),
		Name:                proto.String(wireCode(code)),
		CurPrice:            proto.Float64(q.Last),
		LastClosePrice:      proto.Float64(q.PrevClose),
		Volume:              proto.Int64(q.Volume),
		Highest52WeeksPrice: proto.Float64(f.High52Wk),
		Lowest52WeeksPrice:  proto.Float64(f.Low52Wk),

		// Required-but-unread wire-format filler (see doc comment above).
		Type:         proto.Int32(int32(qotcommon.SecurityType_SecurityType_Eqty)),
		IsSuspend:    proto.Bool(false),
		ListTime:     proto.String(""),
		LotSize:      proto.Int32(0),
		PriceSpread:  proto.Float64(0),
		UpdateTime:   proto.String(""),
		HighPrice:    proto.Float64(0),
		OpenPrice:    proto.Float64(0),
		LowPrice:     proto.Float64(0),
		Turnover:     proto.Float64(0),
		TurnoverRate: proto.Float64(0),
	}
}

// buildEquityExData populates the fields scan.go (float, via
// GetOutstandingShares) and stockinfo.go's snapshotToPayload (market cap,
// float market cap, shares outstanding/float, PE, PE-TTM, EPS) read, plus
// the proto2-required singular fields that go unread (NetAsset, NetProfit,
// NetAssetPershare, EyRate, PbRate). PeRate/PeTTMRate/EarningsPershare are
// always 0: the Generator models no valuation/earnings data, and
// stockinfo.go's own doc comment already treats an exact-0.0 ex-data field
// as a legitimate "moomoo genuinely returns 0 for some fields on thin/new
// listings" value, not a null it needs to special-case - so 0 here is a
// real, poller-anticipated value, not filler standing in for one.
func buildEquityExData(q feed.Quote, f Fundamentals) *snappb.EquitySnapshotExData {
	return &snappb.EquitySnapshotExData{
		IssuedShares:         proto.Int64(f.SharesOut),
		OutstandingShares:    proto.Int64(f.FloatShares),
		IssuedMarketVal:      proto.Float64(float64(f.SharesOut) * q.Last),
		OutstandingMarketVal: proto.Float64(float64(f.FloatShares) * q.Last),
		PeRate:               proto.Float64(0),
		PeTTMRate:            proto.Float64(0),
		EarningsPershare:     proto.Float64(0),

		// Required-but-unread wire-format filler (see doc comment above).
		NetAsset:         proto.Float64(0),
		NetProfit:        proto.Float64(0),
		NetAssetPershare: proto.Float64(0),
		EyRate:           proto.Float64(0),
		PbRate:           proto.Float64(0),
	}
}

// --- search news: 3263 ---------------------------------------------------

// buildSearchNewsResponse synthesizes exactly one news item for the
// requested symbol's current generator state. news.go reads Title, Source,
// Url, NewsSubType, PublishTime, and ViewCount (news.go:75-94); this
// package has no other source of headline text, so the item's copy is
// derived deterministically from the same quote data (price, direction,
// magnitude) the rest of the demo already shows for this symbol, rather
// than being arbitrary. The URL embeds the generator's current step time
// (ms) so a later poll - once the price has moved - produces a distinct,
// undeduped item instead of news.go's own URL-based dedup silently
// swallowing every poll after the first for a symbol whose news never
// changes.
func buildSearchNewsResponse(g *Generator, req proto.Message) *newspb.Response {
	var keyword string
	if r, ok := req.(*newspb.Request); ok {
		keyword = r.GetC2S().GetKeyword()
	}
	code := genCodeOf(keyword)
	q, ok := g.QuoteOf(code)
	if !ok {
		return &newspb.Response{RetType: proto.Int32(0), S2C: &newspb.S2C{}}
	}

	var pct float64
	if q.PrevClose != 0 {
		pct = (q.Last - q.PrevClose) / q.PrevClose * 100
	}
	dir := "rises"
	if pct < 0 {
		dir = "falls"
	}
	publishTime := time.UnixMilli(q.TsMs).In(session.Loc()).Format("2006-01-02 15:04:05")
	sym := wireCode(code)

	item := &newspb.SearchNews{
		Title:       proto.String(fmt.Sprintf("%s %s %.2f%% to $%.2f", sym, dir, math.Abs(pct), q.Last)),
		Source:      proto.String("Synth Wire"),
		Url:         proto.String(fmt.Sprintf("https://synth.etape.local/news/%s/%d", sym, q.TsMs)),
		NewsSubType: proto.Int32(int32(newspb.NewsSubType_NewsSubType_NEWS)),
		PublishTime: proto.String(publishTime),
		ViewCount:   proto.Int64(q.Volume),
	}
	return &newspb.Response{RetType: proto.Int32(0), S2C: &newspb.S2C{SearchNewsList: []*newspb.SearchNews{item}}}
}
