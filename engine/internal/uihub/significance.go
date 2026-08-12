package uihub

import (
	"sort"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

const (
	significanceWindow        = 2_000
	significanceSeenWindow    = significanceWindow * 2
	significanceLargeWarmup   = 200
	significanceRareWarmup    = 1_000
	significanceRecalcCadence = 64
)

type significancePool uint8

const (
	significanceNoPool significancePool = iota
	significanceRTH
	significanceExtended
)

type significancePoolState struct {
	sizes                []int64
	largeThreshold       int64
	exceptionalThreshold int64
	largeAvailable       bool
	exceptionalAvailable bool
	sinceRecalc          int
}

func (p *significancePoolState) classify(size int64) wsmsg.SignificanceLevel {
	if p.exceptionalAvailable && size >= p.exceptionalThreshold {
		return wsmsg.SignificanceExceptional
	}
	if p.largeAvailable && size >= p.largeThreshold {
		return wsmsg.SignificanceLarge
	}
	return wsmsg.SignificanceNone
}

func (p *significancePoolState) insert(size int64) bool {
	p.sizes = append(p.sizes, size)
	if len(p.sizes) > significanceWindow {
		p.sizes = p.sizes[len(p.sizes)-significanceWindow:]
	}
	p.sinceRecalc++
	if len(p.sizes) == significanceLargeWarmup || len(p.sizes) == significanceRareWarmup ||
		(len(p.sizes) >= significanceLargeWarmup && p.sinceRecalc >= significanceRecalcCadence) {
		p.recalculate()
		p.sinceRecalc = 0
		return true
	}
	return false
}

func (p *significancePoolState) recalculate() {
	ordered := append([]int64(nil), p.sizes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	if len(ordered) < significanceLargeWarmup {
		p.largeAvailable = false
		p.exceptionalAvailable = false
		p.largeThreshold = 0
		p.exceptionalThreshold = 0
		return
	}
	median := nearestRank(ordered, 50, 100)
	p.largeAvailable = true
	p.largeThreshold = maxInt64(nearestRank(ordered, 95, 100), multiplyInt64(median, 3))
	if len(ordered) >= significanceRareWarmup {
		p.exceptionalAvailable = true
		p.exceptionalThreshold = maxInt64(nearestRank(ordered, 99, 100), multiplyInt64(median, 8))
	}
}

// nearestRank returns the item at ceil(numerator/denominator*N), using the
// one-based order-statistic position from the feature contract.
func nearestRank(ordered []int64, numerator, denominator int) int64 {
	position := (numerator*len(ordered) + denominator - 1) / denominator
	if position < 1 {
		position = 1
	}
	return ordered[position-1]
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func multiplyInt64(v int64, n int64) int64 {
	const max = int64(^uint64(0) >> 1)
	if v > max/n {
		return max
	}
	return v * n
}

func poolForPhase(p session.Phase) significancePool {
	switch p {
	case session.RTH:
		return significanceRTH
	case session.PreMarket, session.PostMarket, session.Overnight:
		return significanceExtended
	default:
		return significanceNoPool
	}
}

func poolToWire(p significancePool) wsmsg.SignificancePool {
	switch p {
	case significanceRTH:
		return wsmsg.SignificancePoolRTH
	case significanceExtended:
		return wsmsg.SignificancePoolExtended
	default:
		return ""
	}
}

type significanceSeenKey struct {
	day  int64
	seq  int64
	pool significancePool
}

type significanceSymbolState struct {
	symbol         string
	cycle          int64
	initialized    bool
	activePool     significancePool
	lastState      wsmsg.SignificanceState
	pools          [3]significancePoolState
	seen           map[significanceSeenKey]wsmsg.SignificanceLevel
	seenOrder      []significanceSeenKey
	nextTransition time.Time
	published      wsmsg.SignificanceStatus
	publishedSet   bool
}

func newSignificanceSymbol(symbol string, cycle int64) *significanceSymbolState {
	return &significanceSymbolState{
		symbol: symbol,
		cycle:  cycle,
		seen:   make(map[significanceSeenKey]wsmsg.SignificanceLevel),
	}
}

func (s *significanceSymbolState) reset(cycle int64) {
	s.cycle = cycle
	s.initialized = false
	s.activePool = significanceNoPool
	s.lastState = ""
	s.pools = [3]significancePoolState{}
	s.seen = make(map[significanceSeenKey]wsmsg.SignificanceLevel)
	s.seenOrder = nil
	s.nextTransition = time.Time{}
}

func (s *significanceSymbolState) remember(key significanceSeenKey, level wsmsg.SignificanceLevel) {
	if _, exists := s.seen[key]; exists {
		return
	}
	s.seen[key] = level
	s.seenOrder = append(s.seenOrder, key)
	if len(s.seenOrder) <= significanceSeenWindow {
		return
	}
	delete(s.seen, s.seenOrder[0])
	s.seenOrder = s.seenOrder[1:]
}

func (s *significanceSymbolState) status(phase session.Phase) wsmsg.SignificanceStatus {
	pool := s.activePool
	if current := poolForPhase(phase); current != significanceNoPool {
		pool = current
	}
	// Closed has no active phase, but the wire status still identifies the
	// pool whose thresholds are being displayed. A symbol first seen while the
	// calendar is closed has no prior pool, so use the cycle's Extended pool.
	if pool == significanceNoPool {
		pool = significanceExtended
	}
	p := &s.pools[pool]
	return wsmsg.SignificanceStatus{
		Symbol:               s.symbol,
		Pool:                 poolToWire(pool),
		BaselineCount:        len(p.sizes),
		LargeAvailable:       p.largeAvailable,
		LargeThreshold:       p.largeThreshold,
		ExceptionalAvailable: p.exceptionalAvailable,
		ExceptionalThreshold: p.exceptionalThreshold,
		Provisional:          len(p.sizes) < significanceWindow,
		Full:                 len(p.sizes) >= significanceWindow,
		State:                s.stateFor(phase),
	}
}

func (s *significanceSymbolState) stateFor(phase session.Phase) wsmsg.SignificanceState {
	if phase == session.Closed {
		return wsmsg.SignificanceStateClosed
	}
	pool := poolForPhase(phase)
	if pool == significanceNoPool || len(s.pools[pool].sizes) == 0 {
		return wsmsg.SignificanceStateWarming
	}
	return wsmsg.SignificanceStateActive
}

// syncTime updates the symbol's cycle/session state and reports whether the
// low-frequency status read model needs a publication.
func (s *significanceSymbolState) syncTime(at time.Time) bool {
	cycle := session.PoolDay(at)
	reset := s.cycle != cycle
	if reset {
		s.reset(cycle)
	}
	phase := session.PhaseAt(at)
	pool := poolForPhase(phase)
	poolChanged := pool != significanceNoPool && pool != s.activePool
	if pool != significanceNoPool {
		s.activePool = pool
	}
	desired := s.stateFor(phase)
	stateChanged := !s.initialized || desired != s.lastState
	s.initialized = true
	s.lastState = desired
	if s.nextTransition.IsZero() || reset || poolChanged || stateChanged || !at.Before(s.nextTransition) {
		s.nextTransition = nextSignificanceTransition(at)
	}
	return reset || poolChanged || stateChanged
}

// nextSignificanceTransition returns the next exchange-time boundary that can
// change a symbol's cycle, pool, or published state. The hub calls advance at
// a high rate for other market-data work, so status advancement should sleep
// until one of these low-frequency boundaries is actually due.
func nextSignificanceTransition(at time.Time) time.Time {
	et := at.In(session.Loc())
	start := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, session.Loc())
	var next time.Time
	consider := func(candidate time.Time) {
		if !candidate.After(at) || (!next.IsZero() && !candidate.Before(next)) {
			return
		}
		next = candidate
	}
	for offset := 0; offset < 14; offset++ {
		day := start.AddDate(0, 0, offset)
		// Overnight changes to Closed at midnight when the new calendar date is
		// a weekend or holiday.
		consider(day)
		// PoolDay resets at 20:00 even on weekends and holidays.
		consider(time.Date(day.Year(), day.Month(), day.Day(), 20, 0, 0, 0, session.Loc()))
		sched := session.Schedule(day)
		if !sched.TradingDay {
			continue
		}
		consider(time.Date(day.Year(), day.Month(), day.Day(), 4, 0, 0, 0, session.Loc()))
		consider(time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, session.Loc()))
		consider(sched.Close)
		consider(sched.DataClose)
	}
	if next.IsZero() {
		return at.Add(24 * time.Hour)
	}
	return next
}

func (s *significanceSymbolState) publish(phase session.Phase) wsmsg.SignificanceStatus {
	s.published = s.status(phase)
	s.publishedSet = true
	return s.published
}

type significanceEngine struct {
	symbols map[string]*significanceSymbolState
}

func newSignificanceEngine() significanceEngine {
	return significanceEngine{symbols: make(map[string]*significanceSymbolState)}
}

func (e *significanceEngine) symbol(symbol string, at time.Time) *significanceSymbolState {
	cycle := session.PoolDay(at)
	s := e.symbols[symbol]
	if s == nil {
		s = newSignificanceSymbol(symbol, cycle)
		e.symbols[symbol] = s
	}
	return s
}

func (e *significanceEngine) classify(t feed.Tick) (wsmsg.SignificanceLevel, *wsmsg.SignificanceStatus) {
	at := time.UnixMilli(t.TsMs)
	s := e.symbol(t.Symbol, at)
	phase := session.PhaseAt(at)
	statusChanged := s.syncTime(at)
	pool := poolForPhase(phase)
	level := wsmsg.SignificanceNone
	key := significanceSeenKey{day: session.DayMs(t.TsMs), seq: t.Seq, pool: pool}
	if t.Seq != 0 {
		if prior, ok := s.seen[key]; ok {
			if statusChanged {
				status := s.publish(phase)
				return prior, &status
			}
			return prior, nil
		}
	}

	if pool != significanceNoPool && t.Volume > 0 && scoresForSignificance(t.Type) {
		p := &s.pools[pool]
		level = p.classify(t.Volume)
		if learnsSignificance(t.Type) {
			wasProvisional := len(p.sizes) < significanceWindow
			if p.insert(t.Volume) || (wasProvisional && len(p.sizes) == significanceWindow) {
				statusChanged = true
			}
		}
	}
	if t.Seq != 0 {
		s.remember(key, level)
	}
	if pool != significanceNoPool {
		desired := s.stateFor(phase)
		if desired != s.lastState {
			s.lastState = desired
			statusChanged = true
		}
	}
	if !statusChanged {
		return level, nil
	}
	status := s.publish(phase)
	return level, &status
}

func scoresForSignificance(t feed.TransactionType) bool {
	switch t {
	case feed.TransactionRegular, feed.TransactionOddLot,
		feed.TransactionIntermarketSweep, feed.TransactionIntermarketSweepOddLot:
		return true
	default:
		return false
	}
}

func learnsSignificance(t feed.TransactionType) bool {
	return t == feed.TransactionRegular || t == feed.TransactionOddLot
}

func (e *significanceEngine) advance(now time.Time) []wsmsg.SignificanceStatus {
	statuses := make([]wsmsg.SignificanceStatus, 0)
	for _, s := range e.symbols {
		if !s.nextTransition.IsZero() && now.Before(s.nextTransition) {
			continue
		}
		phase := session.PhaseAt(now)
		if s.syncTime(now) {
			statuses = append(statuses, s.publish(phase))
		}
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Symbol < statuses[j].Symbol })
	return statuses
}

func (e *significanceEngine) snapshot() []wsmsg.SignificanceStatus {
	statuses := make([]wsmsg.SignificanceStatus, 0, len(e.symbols))
	for _, s := range e.symbols {
		if s.publishedSet {
			statuses = append(statuses, s.published)
		}
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Symbol < statuses[j].Symbol })
	return statuses
}
