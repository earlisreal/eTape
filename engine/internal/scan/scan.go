// Package scan is the pre-market/RTH rank scanner poller. It issues request/
// response protoIDs (3410/3413/3411/3412 per-session rank, 3203 snapshot)
// through the OpenD client — no subscription quota — and publishes
// scanner.rank/scanner.hit. Float is
// resolved on demand for the symbols on the rank board (3203) and cached for
// the ET day; there is no low-float "universe" (3215 never echoes float).
package scan

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed"
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

type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

type requester interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)
}

// demandFeed is the subscription-control surface the pool drives. Satisfied by
// *opend.OpenDFeed. A nil demandFeed disables the pool (tests/replay).
type demandFeed interface {
	Ensure(d feed.Demand)
	Release(id string)
}

// rankItem is the poller-internal normalized form of one rank row (decoupled
// from the pb type so the transform is unit-testable without protobuf).
type rankItem struct {
	Symbol    string
	ChangePct float64
	Last      float64
	Volume    int64
}

// floatEntry is a resolved float-cache entry. bad = definitively unresolvable
// this ET day (OTC error, zero float, no equity data); absent from the map =
// unknown (transient — a snapshot merely hasn't succeeded yet).
type floatEntry struct {
	shares float64
	bad    bool
}

type Poller struct {
	cfg      config.Scan
	r        requester
	pub      Publisher
	clk      clock.Clock
	feed     demandFeed   // nil => pool disabled
	backfill func(string) // async per-symbol deep-history seed; nil => no backfill
	pool     *Pool
	poolSyms atomic.Pointer[[]string]   // lock-free snapshot for the news set
	floats   map[string]floatEntry      // symbol -> resolved float; absent = unknown
	seen     map[string]map[string]bool // session -> symbol -> seen
	seenDay  int64                      // ET day of the current seen-sets + float cache
}

func New(cfg config.Scan, r requester, pub Publisher, clk clock.Clock, feed demandFeed, backfill func(string)) *Poller {
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk, feed: feed, backfill: backfill, pool: NewPool(),
		floats: map[string]floatEntry{}, seen: map[string]map[string]bool{}}
}

func (p *Poller) Run(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	// Poll on a short base interval; the effective cadence is session-derived.
	base := p.clk.NewTicker(time.Duration(p.cfg.PremarketMs) * time.Millisecond)
	defer base.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-base.C():
			interval := p.pollInterval(now)
			if now.Sub(last) < interval {
				continue
			}
			last = now
			p.pollOnce(ctx, now)
		}
	}
}

func (p *Poller) pollInterval(now time.Time) time.Duration {
	if session.PhaseAt(now) == session.RTH {
		return time.Duration(p.cfg.RTHMs) * time.Millisecond
	}
	return time.Duration(p.cfg.PremarketMs) * time.Millisecond
}

// sessionKey maps a session phase to the scanner.rank message key. Closed
// (weekends/holidays) reuses the pre-market board.
func sessionKey(phase session.Phase) string {
	switch phase {
	case session.RTH:
		return "rth"
	case session.PostMarket:
		return "afterhours"
	case session.Overnight:
		return "overnight"
	default:
		return "premarket"
	}
}

func (p *Poller) pollOnce(ctx context.Context, now time.Time) {
	phase := session.PhaseAt(now)
	items, err := p.fetchRank(ctx, phase)
	if err != nil {
		slog.Warn("scan: rank fetch failed", "err", err)
		return // transient; next tick retries
	}
	p.resetIfNewDay(now)
	p.resolveFloats(ctx, items) // populate the float cache before filtering
	rows := rankRows(items, p.floats, p.cfg)
	p.updatePool(now, rows)
	sess := sessionKey(phase)
	p.pub.Publish(wsmsg.TopicScannerRank, sess, wsmsg.ScannerRankPayload{
		RefreshedAt: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Rows:        rows,
	})
	for _, sym := range p.newHits(sess, rows) {
		p.pub.Publish(wsmsg.TopicScannerHit, sess, wsmsg.ScanHitPayload{
			Symbol: sym, At: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		})
	}
}

func scanDemandID(symbol string) string { return "scan:" + symbol }

// updatePool feeds the filtered top rows to the pool and executes the returned
// delta: Release evicted symbols, Ensure admitted symbols at watch tier, and
// trigger an async deep-history backfill on first admission. Release runs before
// Ensure so a symbol re-admitted on a pool-day reset ends up subscribed. A nil
// feed disables the pool entirely (tests/replay).
func (p *Poller) updatePool(now time.Time, rows []wsmsg.ScannerRow) {
	if p.feed == nil {
		return
	}
	syms := make([]string, len(rows))
	for i, r := range rows {
		syms[i] = r.Symbol
	}
	d := p.pool.Update(syms, now)
	for _, s := range d.Evicted {
		p.feed.Release(scanDemandID(s))
	}
	for _, s := range d.Admitted {
		p.feed.Ensure(feed.WatchDemand(scanDemandID(s), s))
	}
	if p.backfill != nil {
		for _, s := range d.Backfill {
			p.backfill(s)
		}
	}
	snap := p.pool.Symbols()
	p.poolSyms.Store(&snap)
}

// PoolSymbols returns a snapshot of the current pool members (sorted), or nil
// before the first poll / when the pool is disabled. Safe to call from another
// goroutine (the news poller).
func (p *Poller) PoolSymbols() []string {
	if s := p.poolSyms.Load(); s != nil {
		return *s
	}
	return nil
}

// rankRows is the pure transform: apply the float cache + client-side
// thresholds. Three-state float semantics (see the design's decision table):
//   - known & over cap (cap>0): drop
//   - known: include, float shown
//   - bad & cap>0: drop; bad & cap==0: include, float blank
//   - absent (transient): include, float blank
func rankRows(items []rankItem, floats map[string]floatEntry, cfg config.Scan) []wsmsg.ScannerRow {
	out := make([]wsmsg.ScannerRow, 0, len(items))
	for _, it := range items {
		if it.ChangePct < cfg.MinChangePct {
			continue
		}
		if cfg.MinVolume > 0 && it.Volume < cfg.MinVolume {
			continue
		}
		var floatPtr *float64
		if e, ok := floats[it.Symbol]; ok {
			if e.bad {
				if cfg.MaxFloatShares > 0 {
					continue // known-bad: drop when float screening is on
				}
			} else {
				if cfg.MaxFloatShares > 0 && e.shares > cfg.MaxFloatShares {
					continue // known float exceeds the cap
				}
				fv := e.shares
				floatPtr = &fv
			}
		}
		cp, lp := it.ChangePct, it.Last
		out = append(out, wsmsg.ScannerRow{
			Symbol: it.Symbol, ChangePct: &cp, Last: &lp, FloatShares: floatPtr, Volume: it.Volume,
		})
	}
	return out
}

// newHits returns symbols to force-flash. A session's first populated poll
// (empty seen-set) is a silent baseline: seed the set, emit nothing — this
// avoids a whole-board flash/chime storm at session rollover and daily reset.
// Genuinely-new symbols on later polls are returned as hits.
func (p *Poller) newHits(sess string, rows []wsmsg.ScannerRow) []string {
	s := p.seen[sess]
	baseline := len(s) == 0
	if s == nil {
		s = map[string]bool{}
		p.seen[sess] = s
	}
	var hits []string
	for _, r := range rows {
		if !s[r.Symbol] {
			s[r.Symbol] = true
			if !baseline {
				hits = append(hits, r.Symbol)
			}
		}
	}
	return hits
}

// resetIfNewDay clears the seen-sets AND the float cache on the ET-day
// boundary, so overnight splits/offerings are re-resolved and bad-marks last
// at most one ET day.
func (p *Poller) resetIfNewDay(now time.Time) {
	day := session.DayMs(now.UnixMilli())
	if day != p.seenDay {
		p.seenDay = day
		p.seen = map[string]map[string]bool{}
		p.floats = map[string]floatEntry{}
	}
}

// fetchRank issues the rank request for the given session phase and normalizes
// the response to []rankItem (gainers-only, SortDir descending). Each session
// uses its native change ratio (spec: "vs most-recent close").
func (p *Poller) fetchRank(ctx context.Context, phase session.Phase) ([]rankItem, error) {
	switch phase {
	case session.RTH:
		return p.fetchTopMovers(ctx)
	case session.PostMarket:
		return p.fetchAfterHours(ctx)
	case session.Overnight:
		return p.fetchOvernight(ctx)
	default: // PreMarket + Closed
		return p.fetchPreMarket(ctx)
	}
}

func (p *Poller) fetchPreMarket(ctx context.Context) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSPreMarketRank,
		&rankpb.Request{C2S: &rankpb.C2S{SortDir: proto.Int32(0), Offset: proto.Int32(0), Count: proto.Int32(35)}})
	if err != nil {
		return nil, err
	}
	var resp rankpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 {
		return nil, fmt.Errorf("premarket rank retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	var out []rankItem
	for _, d := range resp.GetS2C().GetDataList() {
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()),
			ChangePct: d.GetPreMarketChangeRatio(), Last: d.GetPreMarketPrice(), Volume: d.GetPreMarketVolume()})
	}
	return out, nil
}

func (p *Poller) fetchTopMovers(ctx context.Context) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetTopMoversRank,
		&tmrpb.Request{C2S: &tmrpb.C2S{
			Market:  proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)), // required field
			SortDir: proto.Int32(0), Offset: proto.Int32(0), Count: proto.Int32(35)}})
	if err != nil {
		return nil, err
	}
	var resp tmrpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 {
		return nil, fmt.Errorf("topmovers rank retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	var out []rankItem
	for _, d := range resp.GetS2C().GetDataList() {
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()),
			ChangePct: d.GetChangeRatio(), Last: d.GetCurPrice(), Volume: d.GetVolume()})
	}
	return out, nil
}

func (p *Poller) fetchAfterHours(ctx context.Context) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSAfterHoursRank,
		&ahpb.Request{C2S: &ahpb.C2S{SortDir: proto.Int32(0), Offset: proto.Int32(0), Count: proto.Int32(35)}})
	if err != nil {
		return nil, err
	}
	var resp ahpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 {
		return nil, fmt.Errorf("afterhours rank retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	var out []rankItem
	for _, d := range resp.GetS2C().GetDataList() {
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()),
			ChangePct: d.GetAfterHoursChangeRatio(), Last: d.GetAfterHoursPrice(), Volume: d.GetAfterHoursVolume()})
	}
	return out, nil
}

func (p *Poller) fetchOvernight(ctx context.Context) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSOvernightRank,
		&onpb.Request{C2S: &onpb.C2S{SortDir: proto.Int32(0), Offset: proto.Int32(0), Count: proto.Int32(35)}})
	if err != nil {
		return nil, err
	}
	var resp onpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 {
		return nil, fmt.Errorf("overnight rank retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	var out []rankItem
	for _, d := range resp.GetS2C().GetDataList() {
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()),
			ChangePct: d.GetOvernightChangeRatio(), Last: d.GetOvernightPrice(), Volume: d.GetOvernightVolume()})
	}
	return out, nil
}

const (
	maxSnapshotReqs   = 8   // per-poll 3203 request budget (backstop for the empty-cache day-reset case)
	snapshotChunkSize = 400 // 3203 codes-per-request cap
)

// resolveFloats snapshots (3203) the rank symbols not already in the float
// cache and records the results, so rankRows filters against fresh data. It
// is bounded to maxSnapshotReqs requests per poll; symbols left unresolved
// stay absent and are retried on the next poll. Steady state is zero requests
// (board symbols persist cached poll-to-poll).
func (p *Poller) resolveFloats(ctx context.Context, items []rankItem) {
	var missing []string
	for _, it := range items {
		if _, ok := p.floats[it.Symbol]; !ok {
			missing = append(missing, it.Symbol)
		}
	}
	reqs := 0
	for start := 0; start < len(missing); start += snapshotChunkSize {
		end := start + snapshotChunkSize
		if end > len(missing) {
			end = len(missing)
		}
		p.snapshotBatch(ctx, missing[start:end], &reqs)
	}
}

// snapshotBatch resolves one batch of symbols via a single 3203 request,
// recursing with a binary split when OpenD errors the whole batch (the "one
// bad code fails the batch" case — e.g. an OTC code without quote rights).
// *reqs tracks the per-poll request budget across chunks and recursion.
func (p *Poller) snapshotBatch(ctx context.Context, syms []string, reqs *int) {
	if len(syms) == 0 {
		return
	}
	if *reqs >= maxSnapshotReqs {
		return // budget exhausted; leave the rest absent for the next poll
	}
	*reqs++

	secs := make([]*qotcommon.Security, 0, len(syms))
	for _, s := range syms {
		secs = append(secs, &qotcommon.Security{
			Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
			Code:   proto.String(codeOf(s)),
		})
	}
	fr, err := p.r.Request(ctx, opend.ProtoQotGetSecuritySnapshot,
		&snappb.Request{C2S: &snappb.C2S{SecurityList: secs}})
	if err != nil {
		// Transport/context error: leave symbols absent; the next poll retries.
		slog.Warn("scan: snapshot transport failed", "err", err, "n", len(syms))
		return
	}
	var resp snappb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		slog.Warn("scan: snapshot decode failed", "err", err)
		return
	}
	if resp.GetRetType() != 0 {
		// Application error — the whole batch failed. Isolate the offending
		// code by binary split; a single failing code is marked bad.
		if len(syms) == 1 {
			p.floats[syms[0]] = floatEntry{bad: true}
			slog.Info("scan: float unresolvable", "symbol", syms[0], "reason", resp.GetRetMsg())
			return
		}
		mid := len(syms) / 2
		p.snapshotBatch(ctx, syms[:mid], reqs)
		p.snapshotBatch(ctx, syms[mid:], reqs)
		return
	}
	// Success: record each returned security; anything requested-but-absent is bad.
	got := make(map[string]bool, len(syms))
	for _, sn := range resp.GetS2C().GetSnapshotList() {
		sym := symbolOf(sn.GetBasic().GetSecurity())
		got[sym] = true
		ex := sn.GetEquityExData()
		if ex == nil || ex.GetOutstandingShares() <= 0 {
			p.floats[sym] = floatEntry{bad: true}
			slog.Info("scan: float unresolvable", "symbol", sym, "reason", "no equity float data")
			continue
		}
		p.floats[sym] = floatEntry{shares: float64(ex.GetOutstandingShares())}
	}
	for _, s := range syms {
		if !got[s] {
			p.floats[s] = floatEntry{bad: true}
			slog.Info("scan: float unresolvable", "symbol", s, "reason", "omitted from snapshot response")
		}
	}
}

// codeOf is symbolOf's inverse: eTape "US.<code>" -> the bare moomoo code.
// US-only scope (CLAUDE.md), so the prefix is always "US.".
func codeOf(symbol string) string {
	return strings.TrimPrefix(symbol, "US.")
}

// symbolOf renders a moomoo Security as eTape's "US.<code>" convention.
func symbolOf(s *qotcommon.Security) string {
	if s == nil {
		return ""
	}
	return "US." + s.GetCode() // US-only scope (CLAUDE.md); Market is always QotMarket_US here
}
