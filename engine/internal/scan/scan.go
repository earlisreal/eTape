// Package scan is the pre-market/RTH rank scanner poller. It issues request/
// response protoIDs (3410/3413/3411/3412 per-session rank, 3202 static info,
// 3203 snapshot) through the OpenD client — no subscription quota — and
// publishes scanner.rank/scanner.hit. Exchange type is resolved on demand
// (3202) to drop OTC/Pink codes before they rank (moomoo's US quote
// entitlement doesn't cover OTC — subscribing one fails at Qot_Sub). Float
// is resolved on demand for the surviving symbols (3203) and cached for the
// ET day; there is no low-float "universe" (3215 never echoes float).
package scan

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
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
	staticpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetstaticinfo"
	tmrpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgettopmoversrank"
	ahpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusafterhoursrank"
	onpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetusovernightrank"
	rankpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetuspremarketrank"
	filterpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotstockfilter"
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

type shortSellRestrictionResolver interface {
	IsRestricted(symbol string, now, snapshotAt time.Time, dayLow, priorClose float64) bool
}

func snapshotObservationTime(basic *snappb.SnapshotBasicData) time.Time {
	ts := basic.GetUpdateTimestamp()
	if ts <= 0 || math.IsNaN(ts) || math.IsInf(ts, 0) {
		return time.Time{}
	}
	return time.Unix(int64(ts), 0)
}

// rankItem is the poller-internal normalized form of one rank row (decoupled
// from the pb type so the transform is unit-testable without protobuf).
type rankItem struct {
	Symbol              string
	ChangePct           float64
	Last                float64
	Volume              int64
	ShortSellRestricted bool
}

// floatEntry is a resolved float-cache entry. bad = definitively unresolvable
// this ET day (OTC error, zero float, no equity data); absent from the map =
// unknown (transient — a snapshot merely hasn't succeeded yet).
type floatEntry struct {
	shares float64
	bad    bool
}

type Poller struct {
	cfg                   config.Scan
	r                     requester
	pub                   Publisher
	clk                   clock.Clock
	feed                  demandFeed   // nil => pool disabled
	backfill              func(string) // async per-symbol deep-history seed; nil => no backfill
	pool                  *Pool
	poolSyms              atomic.Pointer[[]string]   // lock-free snapshot for the news set
	floats                map[string]floatEntry      // symbol -> resolved float; absent = unknown
	otc                   map[string]bool            // symbol -> resolved exchange type (true = OTC/Pink); absent = unknown
	seen                  map[string]map[string]bool // session -> symbol -> seen
	seenDay               int64                      // ET day of the current seen-sets + float cache
	mu                    sync.RWMutex
	filters               wsmsg.ScannerFilters
	baseline              bool
	poke                  chan struct{}
	lastStockFilter       time.Time
	board                 map[string]rankItem
	premarketBootstrapped bool
	lastPhase             session.Phase
	phaseSet              bool
	resetBoard            bool
	ssr                   shortSellRestrictionResolver
}

func New(cfg config.Scan, r requester, pub Publisher, clk clock.Clock, feed demandFeed, backfill func(string), ssr ...shortSellRestrictionResolver) *Poller {
	filters := Defaults(cfg)
	var resolver shortSellRestrictionResolver
	if len(ssr) > 0 {
		resolver = ssr[0]
	}
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk, feed: feed, backfill: backfill, ssr: resolver, pool: NewPool(),
		floats: map[string]floatEntry{}, otc: map[string]bool{}, seen: map[string]map[string]bool{}, filters: filters, baseline: true, poke: make(chan struct{}, 1)}
}

func Defaults(cfg config.Scan) wsmsg.ScannerFilters {
	var cap *float64
	if cfg.MaxFloatShares > 0 {
		v := cfg.MaxFloatShares
		cap = &v
	}
	return wsmsg.ScannerFilters{Mode: "gainers", MinChangePct: cfg.MinChangePct, MaxFloatShares: cap, MinVolume: float64(cfg.MinVolume), FloatUnit: "M", VolumeUnit: "K"}
}

func ValidateFilters(f wsmsg.ScannerFilters) error {
	if f.Mode != "gainers" && f.Mode != "losers" && f.Mode != "most_active" {
		return fmt.Errorf("invalid mode")
	}
	if (f.FloatUnit != "K" && f.FloatUnit != "M") || (f.VolumeUnit != "K" && f.VolumeUnit != "M") {
		return fmt.Errorf("invalid unit")
	}
	if math.IsNaN(f.MinChangePct) || math.IsInf(f.MinChangePct, 0) || f.MinChangePct < 0 || math.IsNaN(f.MinVolume) || math.IsInf(f.MinVolume, 0) || f.MinVolume < 0 {
		return fmt.Errorf("invalid numeric filter")
	}
	if f.MaxFloatShares != nil && (math.IsNaN(*f.MaxFloatShares) || math.IsInf(*f.MaxFloatShares, 0) || *f.MaxFloatShares < 0) {
		return fmt.Errorf("invalid float cap")
	}
	return nil
}

func (p *Poller) Filters() wsmsg.ScannerFilters { p.mu.RLock(); defer p.mu.RUnlock(); return p.filters }
func (p *Poller) SetFilters(f wsmsg.ScannerFilters) error {
	if err := ValidateFilters(f); err != nil {
		return err
	}
	p.mu.Lock()
	p.filters = f
	p.baseline = true
	p.resetBoard = true
	p.mu.Unlock()
	select {
	case p.poke <- struct{}{}:
	default:
	}
	return nil
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
		case <-p.poke:
			p.pollOnce(ctx, p.clk.Now())
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
	filters := p.Filters()
	phase := session.PhaseAt(now)
	p.mu.Lock()
	if p.board == nil || p.resetBoard || (phase == session.PostMarket && p.phaseSet && p.lastPhase != session.PostMarket) {
		p.board = map[string]rankItem{}
		p.premarketBootstrapped = false
		p.resetBoard = false
	}
	p.lastPhase, p.phaseSet = phase, true
	bootstrapped := p.premarketBootstrapped
	p.mu.Unlock()

	var items []rankItem
	bootstrapOK := false
	if phase == session.RTH && !bootstrapped {
		pre, err := p.fetchRank(ctx, session.PreMarket, filters.Mode)
		if err != nil {
			slog.Warn("scan: premarket bootstrap failed", "err", err)
		} else {
			items = append(items, pre...)
			bootstrapOK = true
		}
	}
	current, err := p.fetchRank(ctx, phase, filters.Mode)
	if err != nil {
		slog.Warn("scan: rank fetch failed", "err", err)
		if len(items) == 0 {
			return // transient; next tick retries
		}
	} else {
		items = append(items, current...)
		if phase == session.PreMarket {
			bootstrapOK = true
		}
	}
	p.resetIfNewDay(now)
	p.resolveExch(ctx, items) // populate the exchange-type cache before dropping OTC
	items = dropOTC(items, p.otc)
	all := make(map[string]rankItem, len(p.board)+len(items))
	for sym, it := range p.board {
		all[sym] = it
	}
	for _, it := range items {
		if _, retained := all[it.Symbol]; !retained {
			all[it.Symbol] = it
		}
	}
	p.refreshSnapshots(ctx, phase, all)
	for _, it := range items {
		it = all[it.Symbol]
		if len(rankRowsFiltered([]rankItem{it}, p.floats, filters)) != 0 {
			p.board[it.Symbol] = it
		}
	}
	rows := make([]wsmsg.ScannerRow, 0, len(p.board))
	for sym := range p.board {
		p.board[sym] = all[sym]
		rows = append(rows, rankRowsFiltered([]rankItem{all[sym]}, p.floats, wsmsg.ScannerFilters{Mode: "most_active", FloatUnit: filters.FloatUnit, VolumeUnit: filters.VolumeUnit})...)
	}
	sort.Slice(rows, func(i, j int) bool {
		if filters.Mode == "most_active" {
			return rows[i].Volume > rows[j].Volume
		}
		if rows[i].ChangePct == nil || rows[j].ChangePct == nil {
			return rows[i].Symbol < rows[j].Symbol
		}
		if filters.Mode == "losers" {
			return *rows[i].ChangePct < *rows[j].ChangePct
		}
		return *rows[i].ChangePct > *rows[j].ChangePct
	})
	if !sameFilters(filters, p.Filters()) {
		return // SetFilters queued a fresh poll; never publish stale authoritative filters.
	}
	if bootstrapOK {
		p.mu.Lock()
		p.premarketBootstrapped = true
		p.mu.Unlock()
	}
	p.updatePool(now, rows)
	sess := sessionKey(phase)
	p.mu.Lock()
	baseline := p.baseline
	p.baseline = false
	p.mu.Unlock()
	p.pub.Publish(wsmsg.TopicScannerRank, sess, wsmsg.ScannerRankPayload{
		RefreshedAt: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Rows:        rows, Filters: filters, Baseline: baseline,
	})
	for _, sym := range p.newHits(sess, rows) {
		p.pub.Publish(wsmsg.TopicScannerHit, sess, wsmsg.ScanHitPayload{
			Symbol: sym, At: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		})
	}
}

func sameFilters(a, b wsmsg.ScannerFilters) bool {
	if a.Mode != b.Mode || a.MinChangePct != b.MinChangePct || a.MinVolume != b.MinVolume || a.FloatUnit != b.FloatUnit || a.VolumeUnit != b.VolumeUnit {
		return false
	}
	if a.MaxFloatShares == nil || b.MaxFloatShares == nil {
		return a.MaxFloatShares == nil && b.MaxFloatShares == nil
	}
	return *a.MaxFloatShares == *b.MaxFloatShares
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
		demand := feed.WatchDemand(scanDemandID(s), s)
		demand.BackgroundSeed = true
		p.feed.Ensure(demand)
	}
	if p.backfill != nil {
		for i, s := range d.Backfill {
			sym := s
			delay := time.Duration(i) * 300 * time.Millisecond
			if delay == 0 {
				p.backfill(sym)
			} else {
				go func() {
					time.Sleep(delay)
					p.backfill(sym)
				}()
			}
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

// dropOTC is the pure transform: drop items confirmed OTC/Pink (otc[sym] ==
// true). Anything else — resolved not-OTC, or still unresolved (transient
// transport error or budget-exhausted; retried next poll) — is kept, not
// dropped. If an actual OTC code ever slips through unflagged, subman's
// quarantine is the backstop.
func dropOTC(items []rankItem, otc map[string]bool) []rankItem {
	out := make([]rankItem, 0, len(items))
	for _, it := range items {
		if otc[it.Symbol] {
			continue
		}
		out = append(out, it)
	}
	return out
}

// rankRows is the pure transform: apply the float cache + client-side
// thresholds. Three-state float semantics (see the design's decision table):
//   - known & over cap (cap>0): drop
//   - known: include, float shown
//   - bad & cap>0: drop; bad & cap==0: include, float blank
//   - absent (transient): include, float blank
func rankRows(items []rankItem, floats map[string]floatEntry, cfg config.Scan) []wsmsg.ScannerRow {
	return rankRowsFiltered(items, floats, Defaults(cfg))
}

func rankRowsFiltered(items []rankItem, floats map[string]floatEntry, f wsmsg.ScannerFilters) []wsmsg.ScannerRow {
	out := make([]wsmsg.ScannerRow, 0, len(items))
	for _, it := range items {
		if (f.Mode == "gainers" && it.ChangePct < f.MinChangePct) || (f.Mode == "losers" && it.ChangePct > -f.MinChangePct) {
			continue
		}
		if f.MinVolume > 0 && float64(it.Volume) < f.MinVolume {
			continue
		}
		var floatPtr *float64
		if e, ok := floats[it.Symbol]; ok {
			if e.bad {
				if f.MaxFloatShares != nil && *f.MaxFloatShares > 0 {
					continue // known-bad: drop when float screening is on
				}
			} else {
				if f.MaxFloatShares != nil && *f.MaxFloatShares > 0 && e.shares > *f.MaxFloatShares {
					continue // known float exceeds the cap
				}
				fv := e.shares
				floatPtr = &fv
			}
		}
		cp, lp := it.ChangePct, it.Last
		out = append(out, wsmsg.ScannerRow{
			Symbol: it.Symbol, ShortSellRestricted: it.ShortSellRestricted,
			ChangePct: &cp, Last: &lp, FloatShares: floatPtr, Volume: it.Volume,
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

// resetIfNewDay clears the seen-sets AND the float/exchange-type caches on
// the ET-day boundary, so overnight splits/offerings/re-listings are
// re-resolved and bad-marks last at most one ET day.
func (p *Poller) resetIfNewDay(now time.Time) {
	day := session.DayMs(now.UnixMilli())
	if day != p.seenDay {
		p.seenDay = day
		p.seen = map[string]map[string]bool{}
		p.floats = map[string]floatEntry{}
		p.otc = map[string]bool{}
	}
}

// fetchRank issues the rank request for the given session phase and normalizes
// the response to []rankItem (gainers-only, SortDir descending). Each session
// uses its native change ratio (spec: "vs most-recent close").
func (p *Poller) fetchRank(ctx context.Context, phase session.Phase, modes ...string) ([]rankItem, error) {
	mode := "gainers"
	if len(modes) > 0 {
		mode = modes[0]
	}
	if mode == "most_active" {
		if phase == session.RTH {
			return p.fetchMostActiveRTH(ctx)
		}
		return p.fetchMostActiveExtended(ctx, phase)
	}
	dir := int32(0)
	if mode == "losers" {
		dir = 1
	}
	switch phase {
	case session.RTH:
		return p.fetchTopMovers(ctx, dir)
	case session.PostMarket:
		return p.fetchAfterHours(ctx, dir)
	case session.Overnight:
		return p.fetchOvernight(ctx, dir)
	default: // PreMarket + Closed
		return p.fetchPreMarket(ctx, dir)
	}
}

func (p *Poller) fetchMostActiveExtended(ctx context.Context, phase session.Phase) ([]rankItem, error) {
	gainers, err := p.fetchRank(ctx, phase, "gainers")
	if err != nil {
		return nil, err
	}
	losers, err := p.fetchRank(ctx, phase, "losers")
	if err != nil {
		return nil, err
	}
	bySymbol := make(map[string]rankItem, len(gainers)+len(losers))
	for _, it := range append(gainers, losers...) {
		if old, ok := bySymbol[it.Symbol]; !ok || it.Volume > old.Volume {
			bySymbol[it.Symbol] = it
		}
	}
	out := make([]rankItem, 0, len(bySymbol))
	for _, it := range bySymbol {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Volume > out[j].Volume })
	return out, nil
}

func (p *Poller) fetchMostActiveRTH(ctx context.Context) ([]rankItem, error) {
	if wait := 3100*time.Millisecond - p.clk.Now().Sub(p.lastStockFilter); !p.lastStockFilter.IsZero() && wait > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.clk.After(wait):
		}
	}
	p.lastStockFilter = p.clk.Now()
	volume := int32(filterpb.AccumulateField_AccumulateField_Volume)
	desc := int32(filterpb.SortDir_SortDir_Descend)
	change := int32(filterpb.AccumulateField_AccumulateField_ChangeRate)
	price := int32(filterpb.StockField_StockField_CurPrice)
	one := int32(1)
	fr, err := p.r.Request(ctx, opend.ProtoQotStockFilter, &filterpb.Request{C2S: &filterpb.C2S{
		Begin: proto.Int32(0), Num: proto.Int32(200), Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
		BaseFilterList:       []*filterpb.BaseFilter{{FieldName: &price, IsNoFilter: proto.Bool(true)}},
		AccumulateFilterList: []*filterpb.AccumulateFilter{{FieldName: &volume, IsNoFilter: proto.Bool(true), SortDir: &desc, Days: &one}, {FieldName: &change, IsNoFilter: proto.Bool(true), Days: &one}},
	}})
	if err != nil {
		return nil, err
	}
	var resp filterpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 {
		return nil, fmt.Errorf("stock filter retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	out := make([]rankItem, 0, len(resp.GetS2C().GetDataList()))
	for _, d := range resp.GetS2C().GetDataList() {
		it := rankItem{Symbol: symbolOf(d.GetSecurity())}
		for _, v := range d.GetBaseDataList() {
			if v.GetFieldName() == price {
				it.Last = v.GetValue()
			}
		}
		for _, v := range d.GetAccumulateDataList() {
			switch v.GetFieldName() {
			case volume:
				it.Volume = int64(v.GetValue())
			case change:
				it.ChangePct = v.GetValue()
			}
		}
		out = append(out, it)
	}
	return out, nil
}

func (p *Poller) fetchPreMarket(ctx context.Context, dir int32) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSPreMarketRank,
		&rankpb.Request{C2S: &rankpb.C2S{SortDir: proto.Int32(dir), Offset: proto.Int32(0), Count: proto.Int32(35)}})
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

func (p *Poller) fetchTopMovers(ctx context.Context, dir int32) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetTopMoversRank,
		&tmrpb.Request{C2S: &tmrpb.C2S{
			Market:  proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)), // required field
			SortDir: proto.Int32(dir), Offset: proto.Int32(0), Count: proto.Int32(100)}})
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

func (p *Poller) fetchAfterHours(ctx context.Context, dir int32) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSAfterHoursRank,
		&ahpb.Request{C2S: &ahpb.C2S{SortDir: proto.Int32(dir), Offset: proto.Int32(0), Count: proto.Int32(35)}})
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

func (p *Poller) fetchOvernight(ctx context.Context, dir int32) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSOvernightRank,
		&onpb.Request{C2S: &onpb.C2S{SortDir: proto.Int32(dir), Offset: proto.Int32(0), Count: proto.Int32(35)}})
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
	// maxStaticInfoReqs/staticInfoChunkSize mirror the 3203 budget below.
	// 3202's own rate limit isn't documented in
	// .claude/skills/moomooapi/docs/API_LIMITS.md — re-verify against live
	// OpenD; these are a conservative starting assumption, not a measured
	// limit.
	maxStaticInfoReqs   = 8   // per-poll 3202 request budget (backstop for the empty-cache day-reset case)
	staticInfoChunkSize = 400 // assumed 3202 codes-per-request cap
)

// resolveExch resolves exchange type (3202) for rank symbols not already in
// the otc cache, so dropOTC can drop confirmed OTC/Pink codes before they
// rank or consume a 3203 float call. Bounded to maxStaticInfoReqs requests
// per poll; symbols left unresolved stay absent and are retried on the next
// poll. Steady state is zero requests (board symbols persist cached
// poll-to-poll).
func (p *Poller) resolveExch(ctx context.Context, items []rankItem) {
	var missing []string
	for _, it := range items {
		if _, ok := p.otc[it.Symbol]; !ok {
			missing = append(missing, it.Symbol)
		}
	}
	reqs := 0
	for start := 0; start < len(missing); start += staticInfoChunkSize {
		end := start + staticInfoChunkSize
		if end > len(missing) {
			end = len(missing)
		}
		p.staticInfoBatch(ctx, missing[start:end], &reqs)
	}
}

// staticInfoBatch resolves one batch of symbols via a single 3202 request,
// recursing with a binary split when OpenD errors the whole batch — the same
// "one bad code fails the batch" isolation as snapshotBatch. *reqs tracks the
// per-poll request budget across chunks and recursion.
func (p *Poller) staticInfoBatch(ctx context.Context, syms []string, reqs *int) {
	if len(syms) == 0 {
		return
	}
	if *reqs >= maxStaticInfoReqs {
		return // budget exhausted; leave the rest unresolved for the next poll
	}
	*reqs++

	secs := make([]*qotcommon.Security, 0, len(syms))
	for _, s := range syms {
		secs = append(secs, &qotcommon.Security{
			Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
			Code:   proto.String(codeOf(s)),
		})
	}
	fr, err := p.r.Request(ctx, opend.ProtoQotGetStaticInfo,
		&staticpb.Request{C2S: &staticpb.C2S{SecurityList: secs}})
	if err != nil {
		// Transport/context error: leave symbols unresolved; the next poll retries.
		slog.Warn("scan: static info transport failed", "err", err, "n", len(syms))
		return
	}
	var resp staticpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		slog.Warn("scan: static info decode failed", "err", err)
		return
	}
	if resp.GetRetType() != 0 {
		// Application error — the whole batch failed. Isolate the offending
		// code by binary split; a code that fails in isolation is cached
		// not-OTC (never assumed OTC — the error may be unrelated), so it
		// stays visible to the scanner but, matching snapshotBatch's bad-mark
		// convention, isn't re-requested every poll (steady state stays zero
		// requests). dropOTC's absent-symbol case and subman's quarantine
		// remain the backstop if it's actually OTC.
		if len(syms) == 1 {
			p.otc[syms[0]] = false
			slog.Info("scan: exchange type unresolvable", "symbol", syms[0], "reason", resp.GetRetMsg())
			return
		}
		mid := len(syms) / 2
		p.staticInfoBatch(ctx, syms[:mid], reqs)
		p.staticInfoBatch(ctx, syms[mid:], reqs)
		return
	}
	// Success: record each returned security's exchange type. Anything
	// requested-but-omitted from the response is cached not-OTC for the same
	// reason (avoid re-requesting a code OpenD won't ever answer for).
	got := make(map[string]bool, len(syms))
	for _, info := range resp.GetS2C().GetStaticInfoList() {
		basic := info.GetBasic()
		sym := symbolOf(basic.GetSecurity())
		got[sym] = true
		p.otc[sym] = basic.GetExchType() == int32(qotcommon.ExchType_ExchType_US_Pink)
	}
	for _, s := range syms {
		if !got[s] {
			p.otc[s] = false
			slog.Info("scan: exchange type unresolvable", "symbol", s, "reason", "omitted from static info response")
		}
	}
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
		p.snapshotBatch(ctx, session.Closed, missing[start:end], &reqs, nil)
	}
}

// refreshSnapshots refreshes every accumulated row in the same quota-free
// batch used for float enrichment. Failed or omitted symbols keep their prior
// rank values.
func (p *Poller) refreshSnapshots(ctx context.Context, phase session.Phase, items map[string]rankItem) {
	syms := make([]string, 0, len(items))
	for sym := range items {
		syms = append(syms, sym)
	}
	reqs := 0
	for start := 0; start < len(syms); start += snapshotChunkSize {
		end := start + snapshotChunkSize
		if end > len(syms) {
			end = len(syms)
		}
		p.snapshotBatch(ctx, phase, syms[start:end], &reqs, items)
	}
}

// snapshotBatch resolves one batch of symbols via a single 3203 request,
// recursing with a binary split when OpenD errors the whole batch (the "one
// bad code fails the batch" case — e.g. an OTC code without quote rights).
// *reqs tracks the per-poll request budget across chunks and recursion.
func (p *Poller) snapshotBatch(ctx context.Context, phase session.Phase, syms []string, reqs *int, items map[string]rankItem) {
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
			return
		}
		mid := len(syms) / 2
		p.snapshotBatch(ctx, phase, syms[:mid], reqs, items)
		p.snapshotBatch(ctx, phase, syms[mid:], reqs, items)
		return
	}
	// Success: record each returned security; anything requested-but-absent is bad.
	got := make(map[string]bool, len(syms))
	for _, sn := range resp.GetS2C().GetSnapshotList() {
		basic := sn.GetBasic()
		sym := symbolOf(basic.GetSecurity())
		got[sym] = true
		if it, ok := items[sym]; items != nil && ok {
			if phase == session.RTH {
				if basic.GetCurPrice() > 0 {
					it.Last = basic.GetCurPrice()
					if basic.GetLastClosePrice() > 0 {
						it.ChangePct = (it.Last - basic.GetLastClosePrice()) / basic.GetLastClosePrice() * 100
					}
				}
				if basic.Volume != nil {
					it.Volume = basic.GetVolume()
				}
			} else {
				var extended *qotcommon.PreAfterMarketData
				switch phase {
				case session.PostMarket:
					extended = basic.GetAfterMarket()
				case session.Overnight:
					extended = basic.GetOvernight()
				default:
					extended = basic.GetPreMarket()
				}
				if extended != nil {
					it.Last = extended.GetPrice()
					it.ChangePct = extended.GetChangeRate()
					it.Volume = extended.GetVolume()
				}
			}
			if p.ssr != nil {
				it.ShortSellRestricted = p.ssr.IsRestricted(sym, p.clk.Now(), snapshotObservationTime(basic), basic.GetLowPrice(), basic.GetLastClosePrice())
			}
			items[sym] = it
		}
		ex := sn.GetEquityExData()
		if ex == nil || ex.GetOutstandingShares() <= 0 {
			p.floats[sym] = floatEntry{bad: true}
			continue
		}
		p.floats[sym] = floatEntry{shares: float64(ex.GetOutstandingShares())}
	}
	for _, s := range syms {
		if !got[s] {
			p.floats[s] = floatEntry{bad: true}
			slog.Debug("scan: float unresolvable", "symbol", s, "reason", "omitted from snapshot response")
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
