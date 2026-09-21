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
	shortpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetshortinterest"
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
// *opend.OpenDFeed. A nil demandFeed disables the pool (tests/demo).
type demandFeed interface {
	Ensure(d feed.Demand)
	Release(id string)
}

type shortSellRestrictionResolver interface {
	IsRestricted(symbol string, now, snapshotAt time.Time, dayLow, priorClose float64) bool
}

func snapshotTimestamp(basic *snappb.SnapshotBasicData) time.Time {
	if basic == nil {
		return time.Time{}
	}
	ts := basic.GetUpdateTimestamp()
	if ts > 0 && !math.IsNaN(ts) && !math.IsInf(ts, 0) {
		return time.Unix(int64(ts), int64((ts-math.Trunc(ts))*float64(time.Second))).In(session.Loc())
	}
	return time.Time{}
}

func snapshotObservationTime(basic *snappb.SnapshotBasicData) time.Time {
	if observed := snapshotTimestamp(basic); !observed.IsZero() {
		return observed
	}
	if basic == nil {
		return time.Time{}
	}
	if raw := basic.GetUpdateTime(); raw != "" {
		if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", raw, session.Loc()); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func snapshotListingDate(basic *snappb.SnapshotBasicData) time.Time {
	if basic == nil || basic.GetListTime() == "" {
		return time.Time{}
	}
	listing, err := time.ParseInLocation("2006-01-02", basic.GetListTime(), session.Loc())
	if err != nil {
		return time.Time{}
	}
	return listing
}

// snapshotMetricDate requires an observation timestamp. Premarket timestamps
// may describe the current quote while the base total still represents the
// prior completed date; RTH and later phases must match the expected date.
func snapshotMetricDate(now time.Time, basic *snappb.SnapshotBasicData) (time.Time, bool) {
	expected, expectedOK := scannerMetricDate(now)
	if !expectedOK {
		return time.Time{}, false
	}
	observed := snapshotObservationTime(basic)
	if observed.IsZero() {
		return time.Time{}, false
	}
	et := observed.In(session.Loc())
	schedule := session.Schedule(et)
	observedDate := schedule.Date
	if !schedule.TradingDay {
		observedDate = session.PreviousTradingDay(schedule.Date)
	}
	phase := session.PhaseAt(now)
	if phase == session.PreMarket || (phase == session.Overnight && et.Hour() < 4) {
		// The completed base total can still be stamped with the current
		// premarket quote time; the represented total remains the prior date.
		if observedDate.Equal(expected) || observedDate.Equal(schedule.Date) {
			return expected, true
		}
		return time.Time{}, false
	}
	if observedDate.Equal(expected) {
		return expected, true
	}
	return time.Time{}, false
}

func nonNegativeSnapshotVolume(value *int64) (int64, bool) {
	if value == nil || *value < 0 {
		return 0, false
	}
	return *value, true
}

func sessionVolumeValue(value *int64) *int64 {
	v, ok := nonNegativeSnapshotVolume(value)
	if !ok {
		return nil
	}
	return &v
}

// rankItem is the poller-internal normalized form of one rank row (decoupled
// from the pb type so the transform is unit-testable without protobuf).
type rankItem struct {
	Symbol               string
	ChangePct            float64
	Last                 float64
	Volume               int64
	sessionVolume        *int64
	sessionVolumePhase   session.Phase
	sessionVolumePoolDay int64
	Turnover             *float64
	// dailyRequired marks rank values as discovery-only until the batched
	// snapshot supplies the base daily metrics used by Scanner filters.
	dailyRequired       bool
	dailyVolume         *int64
	dailyTurnover       *float64
	dailyDate           int64
	turnoverPhase       session.Phase
	turnoverPoolDay     int64
	RelativeVolume      *float64
	ShortSellRestricted bool
}

// floatEntry is a resolved float-cache entry. bad = definitively unresolvable
// this ET day (OTC error, zero float, no equity data); absent from the map =
// unknown (transient — a snapshot merely hasn't succeeded yet).
type floatEntry struct {
	shares float64
	bad    bool
}

type shortInterestEntry struct {
	shares    float64
	asOf      string
	available bool
	fetchedAt time.Time
}

type relativeVolumeCacheKey struct {
	symbol string
	day    int64
}

type relativeVolumeCacheEntry struct {
	profile     *relativeVolumeProfile
	complete    bool
	terminal    bool
	retryCount  int
	nextAttempt time.Time
}

type relativeVolumeRequest struct {
	key         relativeVolumeCacheKey
	now         time.Time
	listingDate time.Time
}

const (
	shortInterestFreshness = 24 * time.Hour
	shortInterestPace      = time.Second
	maxSafeInteger         = uint64(1<<53 - 1)
)

var relativeVolumeRetryDelays = [...]time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute}

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
	shortInterest         map[string]shortInterestEntry
	shortInterestPending  map[string]bool
	shortInterestQueue    []string
	shortInterestWake     chan struct{}
	relativeVolumeFetcher func(context.Context, string, time.Time, time.Time) ([]feed.Bar, error)
	relativeVolumeCache   map[relativeVolumeCacheKey]relativeVolumeCacheEntry
	relativeVolumePending map[relativeVolumeCacheKey]bool
	relativeVolumeQueue   []relativeVolumeRequest
	relativeVolumeWake    chan struct{}
	listingDates          map[string]time.Time
	relativeVolumeDay     int64
}

func New(cfg config.Scan, r requester, pub Publisher, clk clock.Clock, feed demandFeed, backfill func(string), relativeVolumeFetcher func(context.Context, string, time.Time, time.Time) ([]feed.Bar, error), ssr ...shortSellRestrictionResolver) *Poller {
	filters := Defaults(cfg)
	var resolver shortSellRestrictionResolver
	if len(ssr) > 0 {
		resolver = ssr[0]
	}
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk, feed: feed, backfill: backfill, relativeVolumeFetcher: relativeVolumeFetcher, ssr: resolver, pool: NewPool(),
		floats: map[string]floatEntry{}, otc: map[string]bool{}, seen: map[string]map[string]bool{}, filters: filters, baseline: true, poke: make(chan struct{}, 1),
		shortInterest: map[string]shortInterestEntry{}, shortInterestPending: map[string]bool{}, shortInterestWake: make(chan struct{}, 1),
		relativeVolumeCache: map[relativeVolumeCacheKey]relativeVolumeCacheEntry{}, relativeVolumePending: map[relativeVolumeCacheKey]bool{}, relativeVolumeWake: make(chan struct{}, 1), listingDates: map[string]time.Time{}}
}

func Defaults(cfg config.Scan) wsmsg.ScannerFilters {
	var cap *float64
	if cfg.MaxFloatShares > 0 {
		v := cfg.MaxFloatShares
		cap = &v
	}
	return wsmsg.ScannerFilters{Mode: "gainers", MinChangePct: cfg.MinChangePct, MaxFloatShares: cap, MinVolume: float64(cfg.MinVolume), MinSessionVolume: 0, MinTurnover: 0, MinRelativeVolume: 0, MinPrice: 0, MaxPrice: 0, FloatUnit: "M", VolumeUnit: "K"}
}

func ValidateFilters(f wsmsg.ScannerFilters) error {
	if f.Mode != "gainers" && f.Mode != "losers" && f.Mode != "most_active" {
		return fmt.Errorf("invalid mode")
	}
	if (f.FloatUnit != "K" && f.FloatUnit != "M") || (f.VolumeUnit != "K" && f.VolumeUnit != "M") {
		return fmt.Errorf("invalid unit")
	}
	if math.IsNaN(f.MinChangePct) || math.IsInf(f.MinChangePct, 0) || f.MinChangePct < 0 || math.IsNaN(f.MinVolume) || math.IsInf(f.MinVolume, 0) || f.MinVolume < 0 || math.IsNaN(f.MinSessionVolume) || math.IsInf(f.MinSessionVolume, 0) || f.MinSessionVolume < 0 || math.IsNaN(f.MinTurnover) || math.IsInf(f.MinTurnover, 0) || f.MinTurnover < 0 || math.IsNaN(f.MinRelativeVolume) || math.IsInf(f.MinRelativeVolume, 0) || f.MinRelativeVolume < 0 || math.IsNaN(f.MinPrice) || math.IsInf(f.MinPrice, 0) || f.MinPrice < 0 || math.IsNaN(f.MaxPrice) || math.IsInf(f.MaxPrice, 0) || f.MaxPrice < 0 {
		return fmt.Errorf("invalid numeric filter")
	}
	if f.MinPrice > 0 && f.MaxPrice > 0 && f.MinPrice > f.MaxPrice {
		return fmt.Errorf("invalid price range")
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
	go p.runShortInterestWorker(ctx)
	if p.relativeVolumeFetcher != nil {
		go p.runRelativeVolumeWorker(ctx)
	} else {
		slog.Debug("scan: REL VOL unavailable", "reason", "no SIP history fetcher")
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
	poolDay := session.PoolDay(now)
	p.mu.Lock()
	if p.board == nil || p.resetBoard || (phase == session.PostMarket && p.phaseSet && p.lastPhase != session.PostMarket) {
		p.board = map[string]rankItem{}
		p.premarketBootstrapped = false
		p.resetBoard = false
	}
	p.lastPhase, p.phaseSet = phase, true
	bootstrapped := p.premarketBootstrapped
	metricDate, metricDateOK := scannerMetricDate(now)
	var metricDay int64
	if metricDateOK {
		metricDay = metricDate.UnixMilli()
	}
	for sym, item := range p.board {
		p.board[sym] = currentSessionItem(item, phase, poolDay)
		p.board[sym] = currentDailyItem(p.board[sym], metricDay)
	}
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
	for i := range items {
		if items[i].Turnover != nil {
			items[i].turnoverPoolDay = poolDay
		}
		if items[i].sessionVolume != nil {
			items[i].sessionVolumePoolDay = poolDay
		}
	}
	all := make(map[string]rankItem, len(p.board)+len(items))
	for sym, it := range p.board {
		all[sym] = it
	}
	for _, it := range items {
		if old, ok := all[it.Symbol]; ok {
			if it.sessionVolume != nil {
				old.sessionVolume = it.sessionVolume
				old.sessionVolumePhase = it.sessionVolumePhase
				old.sessionVolumePoolDay = it.sessionVolumePoolDay
			}
			if it.Turnover != nil {
				old.Turnover = it.Turnover
				old.turnoverPhase = it.turnoverPhase
				old.turnoverPoolDay = it.turnoverPoolDay
			}
			all[it.Symbol] = old
			continue
		}
		all[it.Symbol] = it
	}
	p.refreshSnapshots(ctx, phase, all, poolDay)
	// Snapshot enrichment is authoritative for the accumulated copy. Feed it
	// back into the current rank slice before filtering and pool warming so rank
	// volume can never masquerade as the daily metric.
	mergeRankItems(items, all)
	p.applyRelativeVolumes(now, all)
	mergeRankItems(items, all)
	for _, it := range items {
		it = currentSessionItem(all[it.Symbol], phase, poolDay)
		if len(rankRowsFiltered([]rankItem{it}, p.floats, filters)) != 0 {
			p.board[it.Symbol] = it
		}
	}
	rows := make([]wsmsg.ScannerRow, 0, len(p.board))
	for sym := range p.board {
		item := currentSessionItem(all[sym], phase, poolDay)
		p.board[sym] = item
		rows = append(rows, rankRowsFiltered([]rankItem{item}, p.floats, wsmsg.ScannerFilters{Mode: "most_active", FloatUnit: filters.FloatUnit, VolumeUnit: filters.VolumeUnit})...)
	}
	sort.Slice(rows, func(i, j int) bool {
		if filters.Mode == "most_active" {
			if rows[i].Volume == nil || rows[j].Volume == nil {
				return rows[i].Volume != nil
			}
			return *rows[i].Volume > *rows[j].Volume
		}
		if rows[i].ChangePct == nil || rows[j].ChangePct == nil {
			return rows[i].Symbol < rows[j].Symbol
		}
		if filters.Mode == "losers" {
			return *rows[i].ChangePct < *rows[j].ChangePct
		}
		return *rows[i].ChangePct > *rows[j].ChangePct
	})
	p.overlayShortInterest(rows, now)
	if !sameFilters(filters, p.Filters()) {
		return // SetFilters queued a fresh poll; never publish stale authoritative filters.
	}
	if bootstrapOK {
		p.mu.Lock()
		p.premarketBootstrapped = true
		p.mu.Unlock()
	}
	poolRows := rows
	if filters.MinRelativeVolume > 0 {
		poolFilters := filters
		poolFilters.MinRelativeVolume = 0
		poolRows = rankRowsFiltered(currentDailyItems(currentSessionItems(items, phase, poolDay), metricDay), p.floats, poolFilters)
	}
	p.updatePool(now, poolRows)
	p.enqueueRelativeVolumeForPool(now)
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
	if a.Mode != b.Mode || a.MinChangePct != b.MinChangePct || a.MinVolume != b.MinVolume || a.MinSessionVolume != b.MinSessionVolume || a.MinTurnover != b.MinTurnover || a.MinRelativeVolume != b.MinRelativeVolume || a.MinPrice != b.MinPrice || a.MaxPrice != b.MaxPrice || a.FloatUnit != b.FloatUnit || a.VolumeUnit != b.VolumeUnit {
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
// feed disables the pool entirely (tests/demo).
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

func validTurnover(value *float64) *float64 {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
		return nil
	}
	v := *value
	return &v
}

func currentSessionItem(it rankItem, phase session.Phase, poolDay int64) rankItem {
	if it.sessionVolume != nil && (it.sessionVolumePhase != phase || it.sessionVolumePoolDay != poolDay) {
		it.sessionVolume = nil
		it.sessionVolumePhase = session.Closed
		it.sessionVolumePoolDay = 0
	}
	if it.Turnover != nil && (it.turnoverPhase != phase || it.turnoverPoolDay != poolDay) {
		it.Turnover = nil
		it.turnoverPhase = session.Closed
		it.turnoverPoolDay = 0
	}
	return it
}

func currentSessionItems(items []rankItem, phase session.Phase, poolDay int64) []rankItem {
	out := make([]rankItem, len(items))
	for i, it := range items {
		out[i] = currentSessionItem(it, phase, poolDay)
	}
	return out
}

func currentDailyItem(it rankItem, metricDay int64) rankItem {
	if (it.dailyRequired || it.dailyVolume != nil || it.dailyTurnover != nil) && it.dailyDate != metricDay {
		it.dailyVolume = nil
		it.dailyTurnover = nil
		it.dailyDate = 0
	}
	return it
}

func currentDailyItems(items []rankItem, metricDay int64) []rankItem {
	out := make([]rankItem, len(items))
	for i, it := range items {
		out[i] = currentDailyItem(it, metricDay)
	}
	return out
}

func mergeRankItems(items []rankItem, all map[string]rankItem) {
	for i := range items {
		if enriched, ok := all[items[i].Symbol]; ok {
			items[i] = enriched
		}
	}
}

func snapshotTurnover(basic *snappb.SnapshotBasicData, phase session.Phase, poolDay int64) *float64 {
	if basic == nil {
		return nil
	}
	if observed := snapshotTimestamp(basic); !observed.IsZero() && (session.PhaseAt(observed) != phase || session.PoolDay(observed) != poolDay) {
		return nil
	}
	var value *float64
	switch phase {
	case session.RTH:
		value = basic.Turnover
	case session.PreMarket:
		if data := basic.GetPreMarket(); data != nil {
			value = data.Turnover
		}
	case session.PostMarket:
		if data := basic.GetAfterMarket(); data != nil {
			value = data.Turnover
		}
	case session.Overnight:
		if data := basic.GetOvernight(); data != nil {
			value = data.Turnover
		}
	}
	return validTurnover(value)
}

func snapshotSessionVolume(basic *snappb.SnapshotBasicData, phase session.Phase, poolDay int64) *int64 {
	if basic == nil {
		return nil
	}
	if observed := snapshotTimestamp(basic); !observed.IsZero() && (session.PhaseAt(observed) != phase || session.PoolDay(observed) != poolDay) {
		return nil
	}
	var value *int64
	switch phase {
	case session.RTH:
		value = basic.Volume
	case session.PreMarket:
		if data := basic.GetPreMarket(); data != nil {
			value = data.Volume
		}
	case session.PostMarket:
		if data := basic.GetAfterMarket(); data != nil {
			value = data.Volume
		}
	case session.Overnight:
		if data := basic.GetOvernight(); data != nil {
			value = data.Volume
		}
	}
	return sessionVolumeValue(value)
}

func snapshotDailyTurnover(basic *snappb.SnapshotBasicData) *float64 {
	if basic == nil {
		return nil
	}
	return validTurnover(basic.Turnover)
}

func turnoverMatches(value *float64, minimum float64) bool {
	return minimum <= 0 || value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= minimum
}

func rankRowsFiltered(items []rankItem, floats map[string]floatEntry, f wsmsg.ScannerFilters) []wsmsg.ScannerRow {
	out := make([]wsmsg.ScannerRow, 0, len(items))
	for _, it := range items {
		if (f.Mode == "gainers" && it.ChangePct < f.MinChangePct) || (f.Mode == "losers" && it.ChangePct > -f.MinChangePct) {
			continue
		}
		usesDaily := it.dailyRequired || it.dailyVolume != nil || it.dailyTurnover != nil
		volume := it.Volume
		volumeOK := true
		if usesDaily {
			volumeOK = it.dailyVolume != nil
			if volumeOK {
				volume = *it.dailyVolume
			}
		}
		if f.MinVolume > 0 && (!volumeOK || float64(volume) < f.MinVolume) {
			continue
		}
		if f.MinSessionVolume > 0 && (it.sessionVolume == nil || float64(*it.sessionVolume) < f.MinSessionVolume) {
			continue
		}
		turnoverValue := it.Turnover
		if usesDaily {
			turnoverValue = it.dailyTurnover
		}
		if !turnoverMatches(turnoverValue, f.MinTurnover) {
			continue
		}
		if f.MinRelativeVolume > 0 && (it.RelativeVolume == nil || *it.RelativeVolume < f.MinRelativeVolume) {
			continue
		}
		if f.MinPrice > 0 || f.MaxPrice > 0 {
			if !finitePositive(it.Last) || f.MinPrice > 0 && it.Last < f.MinPrice || f.MaxPrice > 0 && it.Last > f.MaxPrice {
				continue
			}
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
		var turnover *float64
		if turnoverValue != nil && !math.IsNaN(*turnoverValue) && !math.IsInf(*turnoverValue, 0) && *turnoverValue >= 0 {
			value := *turnoverValue
			turnover = &value
		}
		var volumePtr *int64
		if volumeOK {
			value := volume
			volumePtr = &value
		}
		var sessionVolumePtr *int64
		if it.sessionVolume != nil {
			value := *it.sessionVolume
			sessionVolumePtr = &value
		}
		out = append(out, wsmsg.ScannerRow{
			Symbol: it.Symbol, ShortSellRestricted: it.ShortSellRestricted,
			ChangePct: &cp, Last: &lp, FloatShares: floatPtr, Volume: volumePtr, SessionVolume: sessionVolumePtr, Turnover: turnover, RelativeVolume: it.RelativeVolume,
		})
	}
	return out
}

func finitePositive(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func relativeVolumeCacheDay(now time.Time) (int64, bool) {
	day, ok := scannerMetricDate(now)
	if !ok {
		return 0, false
	}
	return day.UnixMilli(), true
}

func sameRelativeVolumeProfile(a, b *relativeVolumeProfile) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.day != b.day || a.complete != b.complete || a.mean != b.mean || a.count != b.count {
		return false
	}
	return true
}

func (p *Poller) applyRelativeVolumes(now time.Time, items map[string]rankItem) {
	clockDay, clockDayOK := relativeVolumeCacheDay(now)
	for symbol, it := range items {
		it.RelativeVolume = nil
		usesDaily := it.dailyRequired || it.dailyVolume != nil || it.dailyTurnover != nil
		if clockDayOK && usesDaily && it.dailyVolume != nil && it.dailyDate == clockDay {
			key := relativeVolumeCacheKey{symbol: symbol, day: it.dailyDate}
			p.mu.RLock()
			entry := p.relativeVolumeCache[key]
			p.mu.RUnlock()
			it.RelativeVolume = relativeVolumeAtDate(entry.profile, it.dailyDate, *it.dailyVolume)
		}
		items[symbol] = it
	}
}

func (p *Poller) enqueueRelativeVolumeForPool(now time.Time) {
	if p.relativeVolumeFetcher == nil {
		return
	}
	for _, symbol := range p.pool.Symbols() {
		metricDay := int64(0)
		p.mu.RLock()
		if item, ok := p.board[symbol]; ok {
			metricDay = item.dailyDate
		}
		p.mu.RUnlock()
		p.enqueueRelativeVolumeAt(symbol, now, metricDay)
	}
}

func (p *Poller) enqueueRelativeVolumeAt(symbol string, now time.Time, metricDay int64) {
	if p.relativeVolumeFetcher == nil || symbol == "" {
		return
	}
	if metricDay == 0 {
		day, ok := relativeVolumeCacheDay(now)
		if !ok {
			return
		}
		metricDay = day
	}
	key := relativeVolumeCacheKey{symbol: symbol, day: metricDay}
	p.mu.Lock()
	entry := p.relativeVolumeCache[key]
	listingDate := p.listingDates[symbol]
	if entry.complete || entry.terminal || p.relativeVolumePending[key] || (!entry.nextAttempt.IsZero() && now.Before(entry.nextAttempt)) {
		p.mu.Unlock()
		return
	}
	p.relativeVolumeCache[key] = entry
	p.relativeVolumePending[key] = true
	requestNow := now
	if metricDay != 0 {
		et := time.UnixMilli(metricDay).In(session.Loc())
		requestNow = time.Date(et.Year(), et.Month(), et.Day(), 12, 0, 0, 0, session.Loc())
	}
	p.relativeVolumeQueue = append(p.relativeVolumeQueue, relativeVolumeRequest{key: key, now: requestNow, listingDate: listingDate})
	p.mu.Unlock()
	slog.Debug("scan: REL VOL history queued", "state", "queued", "symbol", symbol, "day", key.day)
	select {
	case p.relativeVolumeWake <- struct{}{}:
	default:
	}
}

func (p *Poller) nextRelativeVolume() (relativeVolumeRequest, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.relativeVolumeQueue) == 0 {
		return relativeVolumeRequest{}, false
	}
	request := p.relativeVolumeQueue[0]
	p.relativeVolumeQueue = p.relativeVolumeQueue[1:]
	return request, true
}

func (p *Poller) finishRelativeVolume(request relativeVolumeRequest, profile *relativeVolumeProfile, complete bool, err error) {
	p.mu.Lock()
	delete(p.relativeVolumePending, request.key)
	day, validDay := relativeVolumeCacheDay(p.clk.Now())
	if !validDay || day != request.key.day {
		p.mu.Unlock()
		return
	}
	old := p.relativeVolumeCache[request.key]
	if err != nil {
		retry := old.retryCount
		if retry >= len(relativeVolumeRetryDelays) {
			retry = len(relativeVolumeRetryDelays) - 1
		}
		old.retryCount++
		old.nextAttempt = p.clk.Now().Add(relativeVolumeRetryDelays[retry])
		p.relativeVolumeCache[request.key] = old
		nextAttempt := old.nextAttempt
		attempt := old.retryCount
		p.mu.Unlock()
		slog.Warn("scan: REL VOL history request failed", "state", "retrying", "symbol", request.key.symbol, "retryAt", nextAttempt, "attempt", attempt, "err", err)
		return
	}
	changed := !sameRelativeVolumeProfile(old.profile, profile) || old.complete != complete || old.terminal
	entry := relativeVolumeCacheEntry{profile: profile, complete: complete, terminal: false, retryCount: 0}
	if !complete {
		entry.retryCount = old.retryCount + 1
		retry := old.retryCount
		if retry >= len(relativeVolumeRetryDelays) {
			retry = len(relativeVolumeRetryDelays) - 1
		}
		entry.nextAttempt = p.clk.Now().Add(relativeVolumeRetryDelays[retry])
	}
	p.relativeVolumeCache[request.key] = entry
	p.mu.Unlock()
	if changed {
		if complete {
			slog.Debug("scan: REL VOL history ready", "state", "ready", "symbol", request.key.symbol, "day", request.key.day)
		} else {
			reason := "incomplete historical date"
			if profile != nil {
				reason = "zero baseline"
			}
			slog.Warn("scan: REL VOL unavailable", "state", "retrying", "symbol", request.key.symbol, "day", request.key.day, "reason", reason, "retryAt", entry.nextAttempt)
		}
		select {
		case p.poke <- struct{}{}:
		default:
		}
	}
}

func (p *Poller) runRelativeVolumeWorker(ctx context.Context) {
	for {
		request, ok := p.nextRelativeVolume()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-p.relativeVolumeWake:
				continue
			}
		}
		if ctx.Err() != nil {
			return
		}
		from, to, ok := relativeVolumeHistoryRange(request.now)
		if !ok {
			p.finishRelativeVolume(request, nil, false, nil)
			continue
		}
		bars, err := p.relativeVolumeFetcher(ctx, request.key.symbol, from, to)
		if err != nil {
			p.finishRelativeVolume(request, nil, false, err)
			continue
		}
		profile, valid := buildRelativeVolumeProfile(request.now, bars, request.listingDate)
		p.finishRelativeVolume(request, profile, valid && relativeVolumeProfileHasBaseline(profile), nil)
	}
}

func validShortInterestDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func shortInterestRecord(item *shortpb.UsShortInterestItem) (float64, string, bool) {
	if item == nil || item.SharesShort == nil || !validShortInterestDate(item.GetTimestampStr()) || item.GetSharesShort() > maxSafeInteger {
		return 0, "", false
	}
	return float64(item.GetSharesShort()), item.GetTimestampStr(), true
}

func (p *Poller) overlayShortInterest(rows []wsmsg.ScannerRow, now time.Time) {
	for i := range rows {
		p.mu.RLock()
		entry, ok := p.shortInterest[rows[i].Symbol]
		fresh := ok && now.Before(entry.fetchedAt.Add(shortInterestFreshness))
		p.mu.RUnlock()
		if ok && entry.available {
			value := entry.shares
			asOf := entry.asOf
			rows[i].ShortInterest = &value
			rows[i].ShortInterestAsOf = &asOf
		}
		if !fresh {
			p.enqueueShortInterest(rows[i].Symbol)
		}
	}
}

func (p *Poller) enqueueShortInterest(symbol string) {
	p.mu.Lock()
	if p.shortInterestPending[symbol] {
		p.mu.Unlock()
		return
	}
	p.shortInterestPending[symbol] = true
	p.shortInterestQueue = append(p.shortInterestQueue, symbol)
	p.mu.Unlock()
	select {
	case p.shortInterestWake <- struct{}{}:
	default:
	}
}

func (p *Poller) nextShortInterest() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.shortInterestQueue) == 0 {
		return "", false
	}
	symbol := p.shortInterestQueue[0]
	p.shortInterestQueue = p.shortInterestQueue[1:]
	return symbol, true
}

func (p *Poller) fetchShortInterest(ctx context.Context, symbol string) (float64, string, bool, error) {
	num := int32(1)
	fr, err := p.r.Request(ctx, opend.ProtoQotGetShortInterest, &shortpb.Request{C2S: &shortpb.C2S{
		Security: &qotcommon.Security{
			Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)),
			Code:   proto.String(codeOf(symbol)),
		},
		Num: &num,
	}})
	if err != nil {
		return 0, "", false, err
	}
	var resp shortpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return 0, "", false, err
	}
	if resp.GetRetType() != 0 {
		return 0, "", false, fmt.Errorf("short interest retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	items := resp.GetS2C().GetUsItemList()
	if len(items) == 0 {
		return 0, "", false, nil
	}
	newest := items[0]
	for _, item := range items[1:] {
		if item != nil && (newest == nil || item.GetTimestampStr() > newest.GetTimestampStr()) {
			newest = item
		}
	}
	shares, asOf, ok := shortInterestRecord(newest)
	if !ok {
		return 0, "", false, fmt.Errorf("short interest record is missing a safe share count or ISO report date")
	}
	return shares, asOf, true, nil
}

func (p *Poller) finishShortInterest(symbol string, shares float64, asOf string, available bool, err error) {
	p.mu.Lock()
	delete(p.shortInterestPending, symbol)
	changed := false
	if err == nil {
		old, hadOld := p.shortInterest[symbol]
		switch {
		case available:
			changed = !hadOld || !old.available || old.shares != shares || old.asOf != asOf
			p.shortInterest[symbol] = shortInterestEntry{shares: shares, asOf: asOf, available: true, fetchedAt: p.clk.Now()}
		case !hadOld || !old.available:
			p.shortInterest[symbol] = shortInterestEntry{fetchedAt: p.clk.Now()}
		}
	}
	p.mu.Unlock()
	if changed {
		select {
		case p.poke <- struct{}{}:
		default:
		}
	}
}

func (p *Poller) runShortInterestWorker(ctx context.Context) {
	var lastRequest time.Time
	for {
		symbol, ok := p.nextShortInterest()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-p.shortInterestWake:
				continue
			}
		}
		if !lastRequest.IsZero() {
			wait := shortInterestPace - p.clk.Now().Sub(lastRequest)
			if wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-p.clk.After(wait):
				}
			}
		}
		lastRequest = p.clk.Now()
		shares, asOf, available, err := p.fetchShortInterest(ctx, symbol)
		p.finishShortInterest(symbol, shares, asOf, available, err)
	}
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
	if metricDay, ok := relativeVolumeCacheDay(now); ok && metricDay != p.relativeVolumeDay {
		p.relativeVolumeDay = metricDay
		p.mu.Lock()
		p.relativeVolumeCache = map[relativeVolumeCacheKey]relativeVolumeCacheEntry{}
		p.relativeVolumePending = map[relativeVolumeCacheKey]bool{}
		p.relativeVolumeQueue = nil
		p.mu.Unlock()
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
		it := rankItem{Symbol: symbolOf(d.GetSecurity()), dailyRequired: true}
		for _, v := range d.GetBaseDataList() {
			if v.GetFieldName() == price {
				it.Last = v.GetValue()
			}
		}
		for _, v := range d.GetAccumulateDataList() {
			switch v.GetFieldName() {
			case volume:
				it.Volume = int64(v.GetValue())
				value := int64(v.GetValue())
				if it.sessionVolume = sessionVolumeValue(&value); it.sessionVolume != nil {
					it.sessionVolumePhase = session.RTH
				}
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
		&rankpb.Request{C2S: &rankpb.C2S{SortDir: proto.Int32(dir), Offset: proto.Int32(0), Count: proto.Int32(100)}})
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
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()), dailyRequired: true,
			ChangePct: d.GetPreMarketChangeRatio(), Last: d.GetPreMarketPrice(), Volume: d.GetPreMarketVolume(),
			sessionVolume: sessionVolumeValue(d.PreMarketVolume), sessionVolumePhase: session.PreMarket,
			Turnover: validTurnover(d.PreMarketTurnover), turnoverPhase: session.PreMarket})
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
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()), dailyRequired: true,
			ChangePct: d.GetChangeRatio(), Last: d.GetCurPrice(), Volume: d.GetVolume(),
			sessionVolume: sessionVolumeValue(d.Volume), sessionVolumePhase: session.RTH,
			Turnover: validTurnover(d.Turnover), turnoverPhase: session.RTH})
	}
	return out, nil
}

func (p *Poller) fetchAfterHours(ctx context.Context, dir int32) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSAfterHoursRank,
		&ahpb.Request{C2S: &ahpb.C2S{SortDir: proto.Int32(dir), Offset: proto.Int32(0), Count: proto.Int32(100)}})
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
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()), dailyRequired: true,
			ChangePct: d.GetAfterHoursChangeRatio(), Last: d.GetAfterHoursPrice(), Volume: d.GetAfterHoursVolume(),
			sessionVolume: sessionVolumeValue(d.AfterHoursVolume), sessionVolumePhase: session.PostMarket,
			Turnover: validTurnover(d.AfterHoursTurnover), turnoverPhase: session.PostMarket})
	}
	return out, nil
}

func (p *Poller) fetchOvernight(ctx context.Context, dir int32) ([]rankItem, error) {
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSOvernightRank,
		&onpb.Request{C2S: &onpb.C2S{SortDir: proto.Int32(dir), Offset: proto.Int32(0), Count: proto.Int32(100)}})
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
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()), dailyRequired: true,
			ChangePct: d.GetOvernightChangeRatio(), Last: d.GetOvernightPrice(), Volume: d.GetOvernightVolume(),
			sessionVolume: sessionVolumeValue(d.OvernightVolume), sessionVolumePhase: session.Overnight,
			Turnover: validTurnover(d.OvernightTurnover), turnoverPhase: session.Overnight})
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
		p.snapshotBatch(ctx, session.Closed, missing[start:end], &reqs, nil, session.PoolDay(p.clk.Now()))
	}
}

// refreshSnapshots refreshes every accumulated row in the same quota-free
// batch used for float enrichment. Failed or omitted symbols keep their prior
// rank values.
func (p *Poller) refreshSnapshots(ctx context.Context, phase session.Phase, items map[string]rankItem, poolDays ...int64) {
	syms := make([]string, 0, len(items))
	for sym := range items {
		syms = append(syms, sym)
	}
	reqs := 0
	poolDay := session.PoolDay(p.clk.Now())
	if len(poolDays) > 0 {
		poolDay = poolDays[0]
	}
	for start := 0; start < len(syms); start += snapshotChunkSize {
		end := start + snapshotChunkSize
		if end > len(syms) {
			end = len(syms)
		}
		p.snapshotBatch(ctx, phase, syms[start:end], &reqs, items, poolDay)
	}
}

// snapshotBatch resolves one batch of symbols via a single 3203 request,
// recursing with a binary split when OpenD errors the whole batch (the "one
// bad code fails the batch" case — e.g. an OTC code without quote rights).
// *reqs tracks the per-poll request budget across chunks and recursion.
func (p *Poller) snapshotBatch(ctx context.Context, phase session.Phase, syms []string, reqs *int, items map[string]rankItem, poolDay int64) {
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
		p.snapshotBatch(ctx, phase, syms[:mid], reqs, items, poolDay)
		p.snapshotBatch(ctx, phase, syms[mid:], reqs, items, poolDay)
		return
	}
	// Success: record each returned security; anything requested-but-absent is bad.
	got := make(map[string]bool, len(syms))
	for _, sn := range resp.GetS2C().GetSnapshotList() {
		basic := sn.GetBasic()
		sym := symbolOf(basic.GetSecurity())
		got[sym] = true
		if listingDate := snapshotListingDate(basic); !listingDate.IsZero() {
			p.mu.Lock()
			p.listingDates[sym] = listingDate
			p.mu.Unlock()
		}
		if it, ok := items[sym]; items != nil && ok {
			it.RelativeVolume = nil
			metricDate, metricOK := snapshotMetricDate(p.clk.Now(), basic)
			metricDay := int64(0)
			if metricOK {
				metricDay = metricDate.UnixMilli()
			}
			if metricDay != 0 {
				it.dailyDate = metricDay
				if dailyVolume, ok := nonNegativeSnapshotVolume(func() *int64 {
					if basic == nil {
						return nil
					}
					return basic.Volume
				}()); ok {
					it.dailyVolume = &dailyVolume
				} else {
					it.dailyVolume = nil
				}
				if dailyTurnover := snapshotDailyTurnover(basic); dailyTurnover != nil {
					it.dailyTurnover = dailyTurnover
				} else {
					it.dailyTurnover = nil
				}
			}
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
				}
			}
			if turnover := snapshotTurnover(basic, phase, poolDay); turnover != nil {
				it.Turnover = turnover
				it.turnoverPhase = phase
				it.turnoverPoolDay = poolDay
			}
			if sessionVolume := snapshotSessionVolume(basic, phase, poolDay); sessionVolume != nil {
				it.sessionVolume = sessionVolume
				it.sessionVolumePhase = phase
				it.sessionVolumePoolDay = poolDay
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
