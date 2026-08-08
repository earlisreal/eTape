// Package ssr derives a best-effort US Rule 201 short-sale restriction from
// regular-session daily prices. It is an informational estimate, not the
// authoritative listing-market or SIP status.
package ssr

import (
	"strings"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

const (
	recentDailyLimit = 8
	negativeCarryTTL = time.Minute
)

// DailyBarReader is the bounded daily-history surface needed to reconstruct a
// previous-session derived Rule 201 trigger after a process restart.
type DailyBarReader interface {
	ReadRecentDailyBars(symbol string, limit int) ([]feed.Bar, error)
}

// Resolver derives shortSellRestricted from the current snapshot and recent
// completed daily bars. The in-memory trigger date preserves a trigger across
// archive lag and is intentionally bounded to the latest relevant date.
type Resolver struct {
	bars DailyBarReader

	mu       sync.Mutex
	triggers map[string]time.Time
	carry    map[string]carryEntry
}

type carryEntry struct {
	date       time.Time
	restricted bool
	checkedAt  time.Time
}

// New creates a derived SSR resolver. A nil reader is allowed; current-day
// observations still work, while restart/carryover reconstruction returns the
// best result available without panicking or making a network request.
func New(bars DailyBarReader) *Resolver {
	return &Resolver{
		bars:     bars,
		triggers: make(map[string]time.Time),
		carry:    make(map[string]carryEntry),
	}
}

// IsRestricted returns the derived Rule 201 estimate for symbol at now.
// snapshotAt is the provider's basic/latest-price update time; it does not
// establish the timestamps of dayLow or priorClose. A new live trigger is
// accepted only when snapshotAt reaches today's regular-session open.
// dayLow and priorClose must come from the regular-session snapshot fields;
// callers should not substitute current price or extended-hours prices.
func (r *Resolver) IsRestricted(symbol string, now, snapshotAt time.Time, dayLow, priorClose float64) bool {
	if r == nil || !strings.HasPrefix(symbol, "US.") {
		return false
	}

	etNow := now.In(session.Loc())
	schedule := session.Schedule(etNow)
	if !schedule.TradingDay {
		return false
	}
	today := schedule.Date
	previous := session.PreviousTradingDay(today)

	r.mu.Lock()
	defer r.mu.Unlock()

	if last, ok := r.triggers[symbol]; ok && last.Before(previous) {
		delete(r.triggers, symbol)
	}
	freshRTHSnapshot := !snapshotAt.IsZero() &&
		!snapshotAt.Before(schedule.Open) &&
		sameDate(snapshotAt, today)
	if !etNow.Before(schedule.Open) &&
		freshRTHSnapshot &&
		triggersRule201(dayLow, priorClose) {
		r.triggers[symbol] = today
	}
	if r.triggers[symbol].Equal(today) || r.triggers[symbol].Equal(previous) {
		return true
	}

	if entry, ok := r.carry[symbol]; ok && entry.date.Equal(today) {
		if entry.restricted || etNow.Sub(entry.checkedAt) < negativeCarryTTL {
			return entry.restricted
		}
	}
	if r.bars == nil {
		return false
	}

	archived, err := r.bars.ReadRecentDailyBars(symbol, recentDailyLimit)
	if err != nil {
		r.carry[symbol] = carryEntry{date: today, checkedAt: etNow}
		return false
	}
	p1, ok1 := barForDate(archived, previous)
	p2, ok2 := barForDate(archived, session.PreviousTradingDay(previous))
	if !ok1 || !ok2 {
		// Backfill may make an incomplete result derivable later; keep this
		// temporary negative result retryable without rereading every poll.
		r.carry[symbol] = carryEntry{date: today, checkedAt: etNow}
		return false
	}

	restricted := triggersRule201(p1.L, p2.C)
	r.carry[symbol] = carryEntry{date: today, restricted: restricted, checkedAt: etNow}
	return restricted
}

// triggersRule201 is the pure threshold comparison used by both live and
// archived calculations. Exactly 10% down is a trigger.
func triggersRule201(dayLow, priorClose float64) bool {
	return dayLow > 0 && priorClose > 0 && dayLow <= priorClose*0.90
}

func barForDate(bars []feed.Bar, date time.Time) (feed.Bar, bool) {
	for _, bar := range bars {
		if sameDate(time.UnixMilli(bar.BucketMs).In(session.Loc()), date) {
			return bar, true
		}
	}
	return feed.Bar{}, false
}

func sameDate(a, b time.Time) bool {
	ay, am, ad := a.In(session.Loc()).Date()
	by, bm, bd := b.In(session.Loc()).Date()
	return ay == by && am == bm && ad == bd
}
