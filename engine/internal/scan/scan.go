// Package scan is the session-aware rank scanner poller. It issues request/
// response protoIDs (3410/3413/3411/3412 per-session rank, 3202 static info,
// 3203 snapshot) through the OpenD client — no subscription quota — and
// publishes scanner.rank. Exchange type is resolved on demand (3202) to drop
// OTC/Pink codes before they rank (moomoo's US quote entitlement doesn't cover
// OTC — subscribing one fails at Qot_Sub). Float is resolved on demand for the
// surviving symbols (3203) and cached for the ET day; there is no low-float
// "universe" (3215 never echoes float). Rows carry normalized current
// observations, selected comparisons, and durable alert revisions; failed
// observations are unavailable rather than stale.
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

func snapshotObservationTime(basic *snappb.SnapshotBasicData) time.Time {
	ts := basic.GetUpdateTimestamp()
	if ts <= 0 || math.IsNaN(ts) || math.IsInf(ts, 0) {
		return time.Time{}
	}
	return time.Unix(int64(ts), 0)
}

func nonNegativeSnapshotVolume(value *int64) (int64, bool) {
	if value == nil || *value < 0 {
		return 0, false
	}
	return *value, true
}

func snapshotCumulativeVolume(basic *snappb.SnapshotBasicData, phase session.Phase) (int64, bool) {
	if basic == nil {
		return 0, false
	}
	pre := basic.PreMarket
	preVolume, ok := func() (int64, bool) {
		if pre == nil {
			return 0, false
		}
		return nonNegativeSnapshotVolume(pre.Volume)
	}()
	if !ok {
		return 0, false
	}
	switch phase {
	case session.PreMarket:
		return preVolume, true
	case session.RTH:
		regular, ok := nonNegativeSnapshotVolume(basic.Volume)
		if !ok {
			return 0, false
		}
		return addRelativeVolume(preVolume, regular)
	case session.PostMarket:
		regular, regularOK := nonNegativeSnapshotVolume(basic.Volume)
		after := basic.AfterMarket
		afterVolume, afterOK := func() (int64, bool) {
			if after == nil {
				return 0, false
			}
			return nonNegativeSnapshotVolume(after.Volume)
		}()
		if !regularOK || !afterOK {
			return 0, false
		}
		partial, ok := addRelativeVolume(preVolume, regular)
		if !ok {
			return 0, false
		}
		return addRelativeVolume(partial, afterVolume)
	default:
		return 0, false
	}
}

// rankItem is the poller-internal normalized form of one rank row (decoupled
// from the pb type so the transform is unit-testable without protobuf).
type rankItem struct {
	Symbol              string
	ChangePct           float64
	Last                float64
	Volume              int64
	RelativeVolume      *float64
	ShortSellRestricted bool
	cumulativeVolume    *int64
	cumulativePhase     session.Phase
	cumulativeDay       int64
	snapshotAttempted   bool
	snapshotUsable      bool
	observedPrice       *float64
	observedChange      *float64
	observedVolume      *int64
	changeStatus        string
	rankClosePrice      *float64
	closePrice          *float64
	closeCycle          int64
	alertSeq            int64
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
	key relativeVolumeCacheKey
	now time.Time
}

const (
	shortInterestFreshness = 24 * time.Hour
	shortInterestPace      = time.Second
	maxSafeInteger         = uint64(1<<53 - 1)
	scanRetryMin           = time.Second
	scanRetryMax           = 30 * time.Second
)

var relativeVolumeRetryDelays = [...]time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute}

type scanRetryBackoff struct {
	delay time.Duration
	until time.Time
}

func (b *scanRetryBackoff) active(now time.Time) bool {
	return !b.until.IsZero() && now.Before(b.until)
}

func (b *scanRetryBackoff) fail(now time.Time) {
	if b.delay < scanRetryMin {
		b.delay = scanRetryMin
	} else if b.delay < scanRetryMax {
		b.delay *= 2
		if b.delay > scanRetryMax {
			b.delay = scanRetryMax
		}
	}
	b.until = now.Add(b.delay)
}

func (b *scanRetryBackoff) clear() { b.delay, b.until = 0, time.Time{} }

func (p *Poller) rankRetryActive(phase session.Phase, now time.Time) bool {
	b := p.rankRetry[phase]
	return b.active(now)
}

func (p *Poller) failRank(phase session.Phase, now time.Time) {
	if p.rankRetry == nil {
		p.rankRetry = map[session.Phase]scanRetryBackoff{}
	}
	b := p.rankRetry[phase]
	b.fail(now)
	p.rankRetry[phase] = b
}

func (p *Poller) clearRank(phase session.Phase) {
	if b, ok := p.rankRetry[phase]; ok {
		b.clear()
		p.rankRetry[phase] = b
	}
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
	lastPhase             session.Phase
	phaseSet              bool
	resetBoard            bool
	bootstrap             map[int64]map[session.Phase]bool
	cycleCandidates       map[int64]map[string]rankItem
	lastCycle             int64
	currentCandidates     []rankItem
	currentCandidateCycle int64
	currentCandidatePhase session.Phase
	snapshotOffset        int
	history               map[string]*rollingHistory
	alerts                alertEngine
	feedEvents            chan bool
	feedResetPending      atomic.Bool
	feedGeneration        atomic.Uint64
	feedDown              atomic.Bool
	filtersChanged        atomic.Bool
	retry                 scanRetryBackoff
	rankRetry             map[session.Phase]scanRetryBackoff
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
}

func New(cfg config.Scan, r requester, pub Publisher, clk clock.Clock, feed demandFeed, backfill func(string), relativeVolumeFetcher func(context.Context, string, time.Time, time.Time) ([]feed.Bar, error), ssr ...shortSellRestrictionResolver) *Poller {
	filters := Defaults(cfg)
	var resolver shortSellRestrictionResolver
	if len(ssr) > 0 {
		resolver = ssr[0]
	}
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk, feed: feed, backfill: backfill, relativeVolumeFetcher: relativeVolumeFetcher, ssr: resolver, pool: NewPool(),
		floats: map[string]floatEntry{}, otc: map[string]bool{}, seen: map[string]map[string]bool{}, filters: filters, baseline: true, poke: make(chan struct{}, 1),
		history: map[string]*rollingHistory{}, bootstrap: map[int64]map[session.Phase]bool{}, cycleCandidates: map[int64]map[string]rankItem{}, alerts: newAlertEngine(), feedEvents: make(chan bool, 4),
		rankRetry:     map[session.Phase]scanRetryBackoff{},
		shortInterest: map[string]shortInterestEntry{}, shortInterestPending: map[string]bool{}, shortInterestWake: make(chan struct{}, 1),
		relativeVolumeCache: map[relativeVolumeCacheKey]relativeVolumeCacheEntry{}, relativeVolumePending: map[relativeVolumeCacheKey]bool{}, relativeVolumeWake: make(chan struct{}, 1)}
}

func Defaults(cfg config.Scan) wsmsg.ScannerFilters {
	var cap *float64
	if cfg.MaxFloatShares > 0 {
		v := cfg.MaxFloatShares
		cap = &v
	}
	return wsmsg.ScannerFilters{Mode: "gainers", ChangeBasis: "previous_close", MinChangePct: cfg.MinChangePct, MaxFloatShares: cap, MinVolume: float64(cfg.MinVolume), MinRelativeVolume: 0, FloatUnit: "M", VolumeUnit: "K"}
}

func normalizeFilters(f wsmsg.ScannerFilters) wsmsg.ScannerFilters {
	if f.ChangeBasis == "" {
		f.ChangeBasis = "previous_close"
	}
	return f
}

func ValidateFilters(f wsmsg.ScannerFilters) error {
	f = normalizeFilters(f)
	if f.Mode != "gainers" && f.Mode != "losers" && f.Mode != "most_active" {
		return fmt.Errorf("invalid mode")
	}
	if f.ChangeBasis != "previous_close" && f.ChangeBasis != "1m" && f.ChangeBasis != "5m" && f.ChangeBasis != "1h" {
		return fmt.Errorf("invalid change basis")
	}
	if (f.FloatUnit != "K" && f.FloatUnit != "M") || (f.VolumeUnit != "K" && f.VolumeUnit != "M") {
		return fmt.Errorf("invalid unit")
	}
	if math.IsNaN(f.MinChangePct) || math.IsInf(f.MinChangePct, 0) || f.MinChangePct < 0 || math.IsNaN(f.MinVolume) || math.IsInf(f.MinVolume, 0) || f.MinVolume < 0 || math.IsNaN(f.MinRelativeVolume) || math.IsInf(f.MinRelativeVolume, 0) || f.MinRelativeVolume < 0 {
		return fmt.Errorf("invalid numeric filter")
	}
	if f.MaxFloatShares != nil && (math.IsNaN(*f.MaxFloatShares) || math.IsInf(*f.MaxFloatShares, 0) || *f.MaxFloatShares < 0) {
		return fmt.Errorf("invalid float cap")
	}
	return nil
}

func (p *Poller) Filters() wsmsg.ScannerFilters { p.mu.RLock(); defer p.mu.RUnlock(); return p.filters }
func (p *Poller) SetFilters(f wsmsg.ScannerFilters) error {
	f = normalizeFilters(f)
	if err := ValidateFilters(f); err != nil {
		return err
	}
	p.mu.Lock()
	p.filters = f
	p.baseline = true
	p.resetBoard = true
	p.mu.Unlock()
	p.filtersChanged.Store(true)
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
	next := p.clk.Now()
	for {
		p.handleFeedEvents()
		now := p.clk.Now()
		if !next.After(now) {
			p.pollOnce(ctx, now)
			next = p.clk.Now().Add(p.pollInterval(p.clk.Now()))
			continue
		}
		wait := p.clk.After(next.Sub(now))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
			continue
		case <-p.poke:
			p.pollOnce(ctx, p.clk.Now())
			next = p.clk.Now().Add(p.pollInterval(p.clk.Now()))
		case <-p.feedEvents:
			p.handleFeedEvents()
		}
	}
}

func (p *Poller) pollInterval(now time.Time) time.Duration {
	interval := time.Duration(p.cfg.PremarketMs) * time.Millisecond
	if session.PhaseAt(now) == session.RTH {
		interval = time.Duration(p.cfg.RTHMs) * time.Millisecond
	}
	if interval <= 0 {
		return time.Second
	}
	return interval
}

// OnFeedState is called by the Hub's MD event loop. It only changes atomics
// and queues owner-thread cleanup; the poller's maps remain single-writer.
func (p *Poller) OnFeedState(up bool) {
	if !up {
		p.feedGeneration.Add(1)
		p.feedDown.Store(true)
		p.feedResetPending.Store(true)
	} else {
		p.feedDown.Store(false)
	}
	select {
	case p.feedEvents <- up:
	default:
	}
	if up {
		select {
		case p.poke <- struct{}{}:
		default:
		}
	}
}

func (p *Poller) handleFeedEvents() {
	for {
		select {
		case <-p.feedEvents:
		default:
			if p.feedResetPending.Swap(false) {
				for _, h := range p.history {
					h.invalidate()
				}
				p.alerts.reset(true)
				p.baseline = true
			}
			return
		}
	}
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
	p.handleFeedEvents()
	if p.feedDown.Load() || ctx.Err() != nil {
		return
	}
	generation := p.feedGeneration.Load()
	filters := p.Filters()
	filtersReset := p.filtersChanged.Load()
	phase := session.PhaseAt(now)
	cycle := session.TradingCycleStart(now).UnixMilli()
	if p.retry.active(p.clk.Now()) {
		return
	}

	type stageResult struct {
		phase session.Phase
		items []rankItem
	}
	results := make([]stageResult, 0, len(discoveryStages(phase)))
	for _, stage := range discoveryStages(phase) {
		if !filtersReset && stage != phase && p.stageBootstrapped(cycle, stage) {
			continue
		}
		if !filtersReset && p.rankRetryActive(stage, p.clk.Now()) {
			continue
		}
		stageItems, err := p.fetchRank(ctx, stage, filters.Mode)
		if err != nil {
			if ctx.Err() == nil {
				p.failRank(stage, p.clk.Now())
			}
			slog.Warn("scan: rank fetch failed", "phase", stage.String(), "err", err)
			continue
		}
		p.clearRank(stage)
		if p.pollStale(ctx, generation, cycle, phase, filters) {
			return
		}
		results = append(results, stageResult{phase: stage, items: stageItems})
	}
	if p.pollStale(ctx, generation, cycle, phase, filters) {
		return
	}
	filtersReset = filtersReset || p.filtersChanged.Swap(false)
	if filtersReset {
		p.alerts.reset(true)
		p.bootstrap = map[int64]map[session.Phase]bool{}
		p.cycleCandidates = map[int64]map[string]rankItem{}
		p.currentCandidates = nil
		p.currentCandidateCycle = 0
	}
	p.pruneCycleState(cycle)
	if p.phaseSet && p.lastPhase != phase && p.lastCycle == cycle && p.currentCandidateCycle == cycle && p.currentCandidatePhase == p.lastPhase {
		p.rememberCycleCandidates(cycle, p.currentCandidates)
	}
	p.mu.Lock()
	if p.board == nil || p.resetBoard || (phase == session.PostMarket && p.phaseSet && p.lastPhase != session.PostMarket) {
		p.board = map[string]rankItem{}
		if phase == session.PostMarket && p.phaseSet && p.lastPhase != session.PostMarket {
			p.alerts.reset(true)
			p.baseline = true
		}
		p.resetBoard = false
	}
	p.lastPhase, p.phaseSet = phase, true
	p.lastCycle = cycle
	p.mu.Unlock()

	var currentItems []rankItem
	for _, result := range results {
		if result.phase == phase {
			currentItems = append(currentItems, result.items...)
			p.replaceCurrentCandidates(cycle, phase, result.items, !filtersReset)
		} else {
			p.rememberCycleCandidates(cycle, result.items)
			p.markStageBootstrapped(cycle, result.phase)
		}
	}
	items := make([]rankItem, 0, len(p.cycleCandidates[cycle])+len(currentItems))
	for _, it := range p.cycleCandidates[cycle] {
		items = append(items, it)
	}
	items = append(items, currentItems...)
	if p.pollStale(ctx, generation, cycle, phase, filters) {
		return
	}
	p.resetIfNewDay(p.clk.Now())
	if !p.resolveExch(ctx, items) { // populate the exchange-type cache before dropping OTC
		return
	}
	if p.pollStale(ctx, generation, cycle, phase, filters) {
		return
	}
	items = dropOTC(items, p.otc)
	all := make(map[string]rankItem, len(p.board)+len(items))
	for sym, it := range p.board {
		all[sym] = it
	}
	for _, it := range items {
		all[it.Symbol] = it // later stages override earlier bootstrap values
	}
	snapshotNow := p.clk.Now()
	p.refreshSnapshotsAt(ctx, phase, all, snapshotNow)
	if p.pollStale(ctx, generation, cycle, phase, filters) {
		return
	}
	observationNow := p.clk.Now()
	if p.pollStale(ctx, generation, cycle, phase, filters) {
		return
	}
	p.updateObservations(observationNow, phase, all, filters)
	if p.pollStale(ctx, generation, cycle, phase, filters) {
		return
	}
	p.applyRelativeVolumes(observationNow, all)
	wasBoard := make(map[string]bool, len(p.board))
	for sym := range p.board {
		wasBoard[sym] = true
	}
	for _, it := range items {
		it = all[it.Symbol]
		if len(rankRowsFiltered([]rankItem{it}, p.floats, filters)) != 0 {
			p.board[it.Symbol] = it
		}
	}
	p.retireCandidates(items, observationNow)
	if p.pollStale(ctx, generation, cycle, phase, filters) {
		return
	}
	p.evaluateAlerts(observationNow, filters, all, wasBoard)
	rows := make([]wsmsg.ScannerRow, 0, len(p.board))
	for sym := range p.board {
		it, ok := all[sym]
		if !ok {
			continue
		}
		p.board[sym] = it
		rows = append(rows, rankRowsFiltered([]rankItem{it}, p.floats, wsmsg.ScannerFilters{Mode: "most_active", ChangeBasis: filters.ChangeBasis, FloatUnit: filters.FloatUnit, VolumeUnit: filters.VolumeUnit})...)
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
	p.overlayShortInterest(rows, observationNow)
	if p.pollStale(ctx, generation, cycle, phase, filters) || !sameFilters(filters, p.Filters()) {
		return // SetFilters queued a fresh poll; never publish stale authoritative filters.
	}
	poolRows := rows
	if filters.MinRelativeVolume > 0 {
		poolFilters := filters
		poolFilters.MinRelativeVolume = 0
		poolRows = rankRowsFiltered(items, p.floats, poolFilters)
	}
	p.updatePool(observationNow, poolRows)
	p.enqueueRelativeVolumeForPool(observationNow)
	if p.pollStale(ctx, generation, cycle, phase, filters) {
		return
	}
	sess := sessionKey(phase)
	p.mu.Lock()
	baseline := p.baseline
	p.baseline = false
	p.mu.Unlock()
	p.pub.Publish(wsmsg.TopicScannerRank, sess, wsmsg.ScannerRankPayload{
		RefreshedAt: observationNow.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Rows:        rows, Filters: filters, Baseline: baseline, WarmingCount: p.warmingCount(all, filters),
	})
}

func (p *Poller) pollStale(ctx context.Context, generation uint64, cycle int64, phase session.Phase, filters wsmsg.ScannerFilters) bool {
	if ctx.Err() != nil || p.feedDown.Load() || generation != p.feedGeneration.Load() {
		return true
	}
	now := p.clk.Now()
	return session.PhaseAt(now) != phase || session.TradingCycleStart(now).UnixMilli() != cycle || !sameFilters(filters, p.Filters())
}

func sameFilters(a, b wsmsg.ScannerFilters) bool {
	a, b = normalizeFilters(a), normalizeFilters(b)
	if a.Mode != b.Mode || a.MinChangePct != b.MinChangePct || a.MinVolume != b.MinVolume || a.MinRelativeVolume != b.MinRelativeVolume || a.FloatUnit != b.FloatUnit || a.VolumeUnit != b.VolumeUnit {
		return false
	}
	if a.ChangeBasis != b.ChangeBasis {
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

func rankRowsFiltered(items []rankItem, floats map[string]floatEntry, f wsmsg.ScannerFilters) []wsmsg.ScannerRow {
	out := make([]wsmsg.ScannerRow, 0, len(items))
	for _, it := range items {
		change, status := it.changeValue()
		if f.Mode == "gainers" && (change == nil || *change < f.MinChangePct) {
			continue
		}
		if f.Mode == "losers" && (change == nil || *change > -f.MinChangePct) {
			continue
		}
		volume := it.Volume
		if it.snapshotAttempted {
			volume = it.rowVolume()
		}
		if f.MinVolume > 0 && float64(volume) < f.MinVolume {
			continue
		}
		if f.MinRelativeVolume > 0 && (it.RelativeVolume == nil || *it.RelativeVolume < f.MinRelativeVolume) {
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
		var cp *float64
		if change != nil {
			v := *change
			cp = &v
		}
		lp := it.rowLast()
		if !it.snapshotAttempted {
			v := it.Last
			lp = &v
		}
		out = append(out, wsmsg.ScannerRow{
			Symbol: it.Symbol, ShortSellRestricted: it.ShortSellRestricted,
			ChangePct: cp, ChangeStatus: status, AlertSeq: it.alertSeq, Last: lp, FloatShares: floatPtr, Volume: volume, RelativeVolume: it.RelativeVolume,
		})
	}
	return out
}

func relativeVolumeCacheDay(now time.Time) (int64, bool) {
	et := now.In(session.Loc())
	s := session.Schedule(et)
	if !s.TradingDay || !relativeVolumePhase(session.PhaseAt(et)) {
		return 0, false
	}
	return s.Date.UnixMilli(), true
}

func sameRelativeVolumeProfile(a, b *relativeVolumeProfile) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.day != b.day || a.complete != b.complete || a.counts != b.counts {
		return false
	}
	return a.means == b.means
}

func (p *Poller) applyRelativeVolumes(now time.Time, items map[string]rankItem) {
	phase := session.PhaseAt(now)
	day, validDay := relativeVolumeCacheDay(now)
	for symbol, it := range items {
		it.RelativeVolume = nil
		if validDay && it.cumulativeVolume != nil && it.cumulativePhase == phase && it.cumulativeDay == day {
			key := relativeVolumeCacheKey{symbol: symbol, day: day}
			p.mu.RLock()
			entry := p.relativeVolumeCache[key]
			p.mu.RUnlock()
			it.RelativeVolume = relativeVolumeAt(entry.profile, now, *it.cumulativeVolume)
		}
		items[symbol] = it
	}
}

func (p *Poller) enqueueRelativeVolumeForPool(now time.Time) {
	if p.relativeVolumeFetcher == nil {
		return
	}
	for _, symbol := range p.pool.Symbols() {
		p.enqueueRelativeVolume(symbol, now)
	}
}

func (p *Poller) enqueueRelativeVolume(symbol string, now time.Time) {
	if p.relativeVolumeFetcher == nil || symbol == "" {
		return
	}
	day, ok := relativeVolumeCacheDay(now)
	if !ok {
		return
	}
	key := relativeVolumeCacheKey{symbol: symbol, day: day}
	p.mu.Lock()
	entry := p.relativeVolumeCache[key]
	if entry.complete || entry.terminal || p.relativeVolumePending[key] || (!entry.nextAttempt.IsZero() && now.Before(entry.nextAttempt)) {
		p.mu.Unlock()
		return
	}
	p.relativeVolumeCache[key] = entry
	p.relativeVolumePending[key] = true
	p.relativeVolumeQueue = append(p.relativeVolumeQueue, relativeVolumeRequest{key: key, now: now})
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
	day, validDay := relativeVolumeCacheDay(p.clk.Now())
	p.mu.Lock()
	delete(p.relativeVolumePending, request.key)
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
	changed := false
	changed = !sameRelativeVolumeProfile(old.profile, profile) || old.complete != complete || old.terminal != !complete
	p.relativeVolumeCache[request.key] = relativeVolumeCacheEntry{profile: profile, complete: complete, terminal: !complete, retryCount: 0}
	p.mu.Unlock()
	if changed {
		if complete {
			slog.Debug("scan: REL VOL history ready", "state", "ready", "symbol", request.key.symbol, "day", request.key.day)
		} else {
			reason := "incomplete historical date"
			if profile != nil {
				reason = "zero baseline"
			}
			slog.Warn("scan: REL VOL unavailable", "state", "terminal-unavailable", "symbol", request.key.symbol, "day", request.key.day, "reason", reason)
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
		profile, valid := buildRelativeVolumeProfile(request.now, bars)
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
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()),
			ChangePct: d.GetPreMarketChangeRatio(), Last: d.GetPreMarketPrice(), Volume: d.GetPreMarketVolume(), rankClosePrice: closePricePtr(d.GetClosePrice())})
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
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()),
			ChangePct: d.GetAfterHoursChangeRatio(), Last: d.GetAfterHoursPrice(), Volume: d.GetAfterHoursVolume(), rankClosePrice: closePricePtr(d.GetClosePrice())})
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
		out = append(out, rankItem{Symbol: symbolOf(d.GetSecurity()),
			ChangePct: d.GetOvernightChangeRatio(), Last: d.GetOvernightPrice(), Volume: d.GetOvernightVolume(), rankClosePrice: closePricePtr(d.GetClosePrice())})
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
func (p *Poller) resolveExch(ctx context.Context, items []rankItem) bool {
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
		if !p.staticInfoBatch(ctx, missing[start:end], &reqs) {
			if p.retry.active(p.clk.Now()) {
				return false
			}
			break // request budget exhausted or context canceled
		}
	}
	return true
}

// staticInfoBatch resolves one batch of symbols via a single 3202 request,
// recursing with a binary split when OpenD errors the whole batch — the same
// "one bad code fails the batch" isolation as snapshotBatch. *reqs tracks the
// per-poll request budget across chunks and recursion.
func (p *Poller) staticInfoBatch(ctx context.Context, syms []string, reqs *int) bool {
	if len(syms) == 0 {
		return true
	}
	if p.retry.active(p.clk.Now()) {
		return false
	}
	if *reqs >= maxStaticInfoReqs {
		return false // budget exhausted; leave the rest unresolved for the next poll
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
		if ctx.Err() == nil {
			p.retry.fail(p.clk.Now())
		}
		slog.Warn("scan: static info transport failed", "err", err, "n", len(syms))
		return false
	}
	var resp staticpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		p.retry.fail(p.clk.Now())
		slog.Warn("scan: static info decode failed", "err", err)
		return false
	}
	if resp.GetRetType() != 0 {
		// Only a response that names one of the requested symbols is safe to
		// isolate. Unknown and account-wide failures terminate this refresh.
		if !opend.SymbolSpecificFailure(resp.GetRetMsg(), syms) {
			p.retry.fail(p.clk.Now())
			return false
		}
		if len(syms) == 1 {
			p.otc[syms[0]] = false
			p.retry.clear()
			slog.Info("scan: exchange type unresolvable", "symbol", syms[0], "reason", resp.GetRetMsg())
			return true
		}
		mid := len(syms) / 2
		return p.staticInfoBatch(ctx, syms[:mid], reqs) && p.staticInfoBatch(ctx, syms[mid:], reqs)
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
	p.retry.clear()
	return true
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
		if !p.snapshotBatch(ctx, session.Closed, missing[start:end], &reqs, nil) {
			break
		}
	}
}

// refreshSnapshots refreshes every accumulated row in the same quota-free
// batch used for float enrichment. Failed or omitted symbols are explicitly
// unavailable in the passed map; callers may still publish that state.
func (p *Poller) refreshSnapshots(ctx context.Context, phase session.Phase, items map[string]rankItem) {
	p.refreshSnapshotsAt(ctx, phase, items, p.clk.Now())
}

func (p *Poller) refreshSnapshotsAt(ctx context.Context, phase session.Phase, items map[string]rankItem, now time.Time) bool {
	syms := make([]string, 0, len(items))
	for sym, it := range items {
		syms = append(syms, sym)
		it.snapshotAttempted = true
		it.snapshotUsable = false
		it.observedPrice = nil
		it.observedChange = nil
		it.observedVolume = nil
		it.changeStatus = "unavailable"
		it.closePrice = nil
		it.closeCycle = 0
		it.RelativeVolume = nil
		it.cumulativeVolume = nil
		it.cumulativePhase = session.Closed
		it.cumulativeDay = 0
		items[sym] = it
	}
	sort.Strings(syms)
	if len(syms) == 0 {
		return true
	}
	limit := len(syms)
	maxSymbols := maxSnapshotReqs * snapshotChunkSize
	if limit > maxSymbols {
		limit = maxSymbols
	}
	total := len(syms)
	start := 0
	if len(syms) > maxSymbols {
		start = p.snapshotOffset % total
		ordered := make([]string, 0, limit)
		for i := 0; i < limit; i++ {
			ordered = append(ordered, syms[(start+i)%total])
		}
		syms = ordered
		p.snapshotOffset = (start + limit) % total
	} else {
		p.snapshotOffset = 0
	}
	reqs := 0
	for start := 0; start < len(syms); start += snapshotChunkSize {
		end := start + snapshotChunkSize
		if end > len(syms) {
			end = len(syms)
		}
		if !p.snapshotBatchAt(ctx, phase, syms[start:end], &reqs, items, now) {
			if p.retry.active(p.clk.Now()) {
				return false
			}
			break // request budget exhausted or context canceled
		}
	}
	return true
}

// snapshotBatch resolves one batch of symbols via a single 3203 request,
// recursing with a binary split when OpenD errors the whole batch (the "one
// bad code fails the batch" case — e.g. an OTC code without quote rights).
// *reqs tracks the per-poll request budget across chunks and recursion.
func (p *Poller) snapshotBatch(ctx context.Context, phase session.Phase, syms []string, reqs *int, items map[string]rankItem) bool {
	return p.snapshotBatchAt(ctx, phase, syms, reqs, items, p.clk.Now())
}

func (p *Poller) snapshotBatchAt(ctx context.Context, phase session.Phase, syms []string, reqs *int, items map[string]rankItem, now time.Time) bool {
	if len(syms) == 0 {
		return true
	}
	if p.retry.active(p.clk.Now()) {
		return false
	}
	if *reqs >= maxSnapshotReqs {
		return false // budget exhausted; leave the rest absent for the next poll
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
		if ctx.Err() == nil {
			p.retry.fail(p.clk.Now())
		}
		slog.Warn("scan: snapshot transport failed", "err", err, "n", len(syms))
		return false
	}
	var resp snappb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		p.retry.fail(p.clk.Now())
		slog.Warn("scan: snapshot decode failed", "err", err)
		return false
	}
	if resp.GetRetType() != 0 {
		if !opend.SymbolSpecificFailure(resp.GetRetMsg(), syms) {
			p.retry.fail(p.clk.Now())
			return false
		}
		if len(syms) == 1 {
			p.floats[syms[0]] = floatEntry{bad: true}
			p.retry.clear()
			return true
		}
		mid := len(syms) / 2
		return p.snapshotBatchAt(ctx, phase, syms[:mid], reqs, items, now) && p.snapshotBatchAt(ctx, phase, syms[mid:], reqs, items, now)
	}
	// Success: record each returned security; anything requested-but-absent is bad.
	got := make(map[string]bool, len(syms))
	for _, sn := range resp.GetS2C().GetSnapshotList() {
		basic := sn.GetBasic()
		if basic == nil {
			continue
		}
		sym := symbolOf(basic.GetSecurity())
		got[sym] = true
		if it, ok := items[sym]; items != nil && ok {
			values := normalizeSnapshotWithClose(basic, phase, now, it.rankClosePrice)
			it.RelativeVolume = nil
			it.cumulativeVolume = nil
			it.cumulativePhase = phase
			it.cumulativeDay = session.DayMs(now.UnixMilli())
			it.snapshotUsable = values.usable
			if values.usable {
				it.Last = values.price
				it.observedPrice = &values.price
				if values.hasVolume {
					it.Volume = values.volume
					it.observedVolume = &values.volume
				}
				if values.hasChange {
					it.ChangePct = values.change
					it.observedChange = &values.change
				}
			}
			if values.hasClose {
				it.closePrice = &values.close
				it.closeCycle = session.TradingCycleStart(now).UnixMilli()
			}
			if values.hasChange {
				it.changeStatus = "ready"
			}
			if cumulative, ok := snapshotCumulativeVolume(basic, phase); ok {
				it.cumulativeVolume = &cumulative
			}
			if p.ssr != nil {
				it.ShortSellRestricted = p.ssr.IsRestricted(sym, now, snapshotObservationTime(basic), basic.GetLowPrice(), basic.GetLastClosePrice())
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
			if it, ok := items[s]; items != nil && ok {
				it.snapshotUsable = false
				it.observedPrice, it.observedChange, it.observedVolume = nil, nil, nil
				it.changeStatus = "unavailable"
				items[s] = it
			}
			p.floats[s] = floatEntry{bad: true}
			slog.Debug("scan: float unresolvable", "symbol", s, "reason", "omitted from snapshot response")
		}
	}
	p.retry.clear()
	return true
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
