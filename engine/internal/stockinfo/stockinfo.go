// Package stockinfo is the poll-only stock-fundamentals + industry fetcher
// for the focused-symbol Stock Info panel. It combines two request/response
// moomoo protocols — Qot_GetSecuritySnapshot (3203: price, market cap,
// float, PE, EPS, 52-week range) and Qot_GetOwnerPlate (3207: industry/
// sector plate lookup) — and publishes one wsmsg.StockDetailPayload per
// symbol per tick.
//
// Rate-limit note: one 3203 request per RefreshMs tick per MaxPerReq-sized
// chunk of symbols (fundamentals refresh every tick, no caching); 3207
// fires only for symbols not yet in the industry cache, so in steady state
// it is issued at most once per symbol for the life of the process. Like
// scan.go and news.go, no explicit rate limiter is implemented here — tick
// cadence plus the industry cache keep both protocols well under moomoo's
// documented limits (60 req/30s for 3203; 3207's limit isn't documented
// anywhere in this repo, but the once-per-symbol cache makes any reasonable
// limit a non-issue).
package stockinfo

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	ownerplatepb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetownerplate"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
)

type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

type requester interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)
}

// Poller ticks on cfg.RefreshMs, refetching fundamentals for every symbol in
// symbols() every tick and industry for symbols not yet cached. industry is
// only ever touched from the Run goroutine, so it needs no mutex.
type Poller struct {
	cfg      config.StockInfo
	r        requester
	pub      Publisher
	clk      clock.Clock
	symbols  func() []string   // focused + watchlist symbols to refresh
	industry map[string]string // symbol -> resolved industry name; "" = known-absent
}

func New(cfg config.StockInfo, r requester, pub Publisher, clk clock.Clock, symbols func() []string) *Poller {
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk, symbols: symbols, industry: map[string]string{}}
}

func (p *Poller) Run(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	tick := p.clk.NewTicker(time.Duration(p.cfg.RefreshMs) * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			p.fetchTick(ctx)
		}
	}
}

// fetchTick performs the two-step fetch (snapshot fundamentals for every
// requested symbol, industry lookup only for symbols not yet cached) and
// publishes one wsmsg.StockDetailPayload per symbol that a snapshot was
// returned for.
func (p *Poller) fetchTick(ctx context.Context) {
	syms := p.symbols()
	if len(syms) == 0 {
		return
	}
	refreshedAt := p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	snapshots := p.fetchSnapshots(ctx, syms)
	p.resolveIndustries(ctx, syms)
	for _, sym := range syms {
		snap, ok := snapshots[sym]
		if !ok {
			continue // no data this tick (transport/batch failure); retried next tick
		}
		payload := snapshotToPayload(snap.GetBasic(), snap.GetEquityExData(), p.industry[sym], refreshedAt)
		payload.Symbol = sym
		p.pub.Publish(wsmsg.TopicStockDetail, sym, payload)
	}
}

// fetchSnapshots issues one Qot_GetSecuritySnapshot request per MaxPerReq
// chunk of syms and returns symbol -> Snapshot for whatever the response
// contained. A transport error or decode error is chunk-wide and unrelated
// to any single symbol's data, so it is logged and that chunk is skipped for
// this tick (retried whole on the next tick). A whole-batch application
// failure (RetType != 0) is isolated via the same binary-split retry as
// scan.go's snapshotBatch (see snapshotChunk): one bad/unentitled code no
// longer blanks out every other symbol that happened to share its chunk. A
// symbol isolated down to size 1 that still fails simply gets no entry in
// out this tick — fetchSnapshots has no cache (fundamentals refresh every
// tick by design), so there is nothing to "give up" on; it is retried fresh
// next tick like any other missing symbol.
func (p *Poller) fetchSnapshots(ctx context.Context, syms []string) map[string]*snappb.Snapshot {
	out := make(map[string]*snappb.Snapshot, len(syms))
	for _, ch := range chunk(syms, p.maxPerReq()) {
		p.snapshotChunk(ctx, ch, out)
	}
	return out
}

// snapshotChunk resolves one chunk of syms via a single 3203 request, adding
// each returned Snapshot to out. On a whole-batch RetType != 0 failure (the
// "one bad code fails the batch" case — e.g. an OTC code without quote
// rights) it recurses with a binary split instead of dropping the whole
// chunk, exactly like scan.go's snapshotBatch: split at the midpoint, retry
// each half, and keep splitting until either a half succeeds or is narrowed
// to the single offending symbol.
func (p *Poller) snapshotChunk(ctx context.Context, ch []string, out map[string]*snappb.Snapshot) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetSecuritySnapshot,
		&snappb.Request{C2S: &snappb.C2S{SecurityList: securitiesFor(ch)}})
	if err != nil {
		slog.Warn("stockinfo: snapshot transport failed", "err", err, "n", len(ch))
		return
	}
	var resp snappb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		slog.Warn("stockinfo: snapshot decode failed", "err", err)
		return
	}
	if resp.GetRetType() != 0 {
		if len(ch) == 1 {
			slog.Info("stockinfo: snapshot unresolvable this tick", "symbol", ch[0], "reason", resp.GetRetMsg())
			return
		}
		mid := len(ch) / 2
		p.snapshotChunk(ctx, ch[:mid], out)
		p.snapshotChunk(ctx, ch[mid:], out)
		return
	}
	for _, sn := range resp.GetS2C().GetSnapshotList() {
		out[symbolOf(sn.GetBasic().GetSecurity())] = sn
	}
}

// resolveIndustries fetches Qot_GetOwnerPlate for symbols not yet in the
// industry cache and records the result — caching "" when a symbol has no
// industry plate (or was omitted from an otherwise-successful response), or
// when it is isolated down to a single symbol that still fails on its own
// (see ownerPlateChunk) — so it is never re-requested for the life of the
// process. A transport error or decode error is chunk-wide and left
// uncached, so it is retried on the next tick. A whole-batch application
// failure (RetType != 0) is isolated via the same binary-split retry as
// fetchSnapshots/scan.go's snapshotBatch, so one bad/unentitled code no
// longer leaves every other symbol in its chunk permanently unresolved.
func (p *Poller) resolveIndustries(ctx context.Context, syms []string) {
	var missing []string
	for _, s := range syms {
		if _, ok := p.industry[s]; !ok {
			missing = append(missing, s)
		}
	}
	for _, ch := range chunk(missing, p.maxPerReq()) {
		p.ownerPlateChunk(ctx, ch)
	}
}

// ownerPlateChunk resolves one chunk of syms via a single 3207 request,
// caching each returned industry (or "" when a symbol succeeded but had no
// row in the response). On a whole-batch RetType != 0 failure it recurses
// with a binary split, same shape as snapshotChunk. Once the split narrows
// the failure down to exactly one symbol that still fails alone, the
// ambiguity that justifies leaving a batch uncached is gone — that symbol is
// definitively the bad one, so unlike the top-level "whole-batch failure
// stays uncached" rule, it is cached as "" here and never retried again,
// converging to the same steady state as a symbol that resolves successfully
// with no industry.
func (p *Poller) ownerPlateChunk(ctx context.Context, ch []string) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetOwnerPlate,
		&ownerplatepb.Request{C2S: &ownerplatepb.C2S{SecurityList: securitiesFor(ch)}})
	if err != nil {
		slog.Warn("stockinfo: owner-plate transport failed", "err", err, "n", len(ch))
		return
	}
	var resp ownerplatepb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		slog.Warn("stockinfo: owner-plate decode failed", "err", err)
		return
	}
	if resp.GetRetType() != 0 {
		if len(ch) == 1 {
			p.industry[ch[0]] = ""
			slog.Info("stockinfo: owner-plate unresolvable, caching absent industry", "symbol", ch[0], "reason", resp.GetRetMsg())
			return
		}
		mid := len(ch) / 2
		p.ownerPlateChunk(ctx, ch[:mid])
		p.ownerPlateChunk(ctx, ch[mid:])
		return
	}
	got := make(map[string]bool, len(ch))
	for _, op := range resp.GetS2C().GetOwnerPlateList() {
		sym := symbolOf(op.GetSecurity())
		got[sym] = true
		p.industry[sym] = industryFromPlates(op.GetPlateInfoList())
	}
	for _, s := range ch {
		if !got[s] {
			p.industry[s] = "" // succeeded but no row for this symbol: cache absent
		}
	}
}

func (p *Poller) maxPerReq() int {
	if p.cfg.MaxPerReq <= 0 {
		return 400
	}
	return p.cfg.MaxPerReq
}

// chunk splits syms into groups of at most size (size<=0 means "one chunk").
func chunk(syms []string, size int) [][]string {
	if len(syms) == 0 {
		return nil
	}
	if size <= 0 {
		size = len(syms)
	}
	out := make([][]string, 0, (len(syms)+size-1)/size)
	for start := 0; start < len(syms); start += size {
		end := start + size
		if end > len(syms) {
			end = len(syms)
		}
		out = append(out, syms[start:end])
	}
	return out
}

// securitiesFor builds the qotcommon.Security list shared by 3203 and 3207
// requests (both take the same SecurityList shape).
func securitiesFor(syms []string) []*qotcommon.Security {
	secs := make([]*qotcommon.Security, 0, len(syms))
	for _, s := range syms {
		secs = append(secs, &qotcommon.Security{
			Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
			Code:   proto.String(codeOf(s)),
		})
	}
	return secs
}

// snapshotToPayload maps decoded snapshot pb sub-messages to the wire
// payload. It is a pure function so the mapping/nullability/changePct math
// is unit-testable without a fake network.
//
// ex may be nil (ETFs and other non-equity instruments lack EquityExData);
// when nil, every equity-derived field stays nil rather than 0, so the UI
// can distinguish "no data" from a genuine 0.0 value. When ex is non-nil its
// fields are taken as-is even if exactly 0.0 — moomoo genuinely returns 0
// for some fields on thin/new listings.
func snapshotToPayload(basic *snappb.SnapshotBasicData, ex *snappb.EquitySnapshotExData, industry string, refreshedAt string) wsmsg.StockDetailPayload {
	out := wsmsg.StockDetailPayload{
		Industry:    industry,
		RefreshedAt: refreshedAt,
	}
	if basic != nil {
		out.Name = basic.GetName()
		price, lastClose := basic.GetCurPrice(), basic.GetLastClosePrice()
		out.Price = &price
		out.LastClose = &lastClose
		if lastClose != 0 {
			cp := (price - lastClose) / lastClose * 100
			out.ChangePct = &cp
		}
		out.Volume = basic.GetVolume()
		h52, l52 := basic.GetHighest52WeeksPrice(), basic.GetLowest52WeeksPrice()
		out.High52 = &h52
		out.Low52 = &l52
	}
	if ex != nil {
		marketCap, floatMarketCap := ex.GetIssuedMarketVal(), ex.GetOutstandingMarketVal()
		sharesOut, floatShares := float64(ex.GetIssuedShares()), float64(ex.GetOutstandingShares())
		pe, peTTM, eps := ex.GetPeRate(), ex.GetPeTTMRate(), ex.GetEarningsPershare()
		out.MarketCap = &marketCap
		out.FloatMarketCap = &floatMarketCap
		out.SharesOutstanding = &sharesOut
		out.FloatShares = &floatShares
		out.Pe = &pe
		out.PeTTM = &peTTM
		out.Eps = &eps
	}
	return out
}

// industryFromPlates scans a Qot_GetOwnerPlate result's plate list for the
// Industry-type entry and returns its name, or "" if none is present.
func industryFromPlates(plates []*qotcommon.PlateInfo) string {
	for _, pi := range plates {
		if pi.GetPlateType() == int32(qotcommon.PlateSetType_PlateSetType_Industry) {
			return pi.GetName()
		}
	}
	return ""
}

// codeOf is symbolOf's inverse: eTape "US.<code>" -> the bare moomoo code.
// US-only scope (CLAUDE.md), so the prefix is always "US.". Replicated
// locally from scan.go rather than exported from package scan, to avoid
// coupling two unrelated pollers over two one-line helpers.
func codeOf(symbol string) string {
	return strings.TrimPrefix(symbol, "US.")
}

// symbolOf renders a moomoo Security as eTape's "US.<code>" convention.
func symbolOf(s *qotcommon.Security) string {
	if s == nil {
		return ""
	}
	return "US." + s.GetCode()
}
