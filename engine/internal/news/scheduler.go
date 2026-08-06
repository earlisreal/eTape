package news

import (
	"sort"
	"time"
)

// SymbolPlan keeps active UI demand separate from scanner symbols. Active
// demand gets priority; refresh intervals are minimums, not guarantees. One
// 3.1s lane plus a sliding 10-per-30s limiter stays below Moomoo's quota.
type SymbolPlan struct{ Active, Scanner []string }

func (p SymbolPlan) All() []string {
	set := map[string]struct{}{}
	for _, g := range [][]string{p.Active, p.Scanner} {
		for _, s := range g {
			if s != "" {
				set[s] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

type scheduler struct{ attempts map[string]time.Time }

func newScheduler() *scheduler { return &scheduler{attempts: map[string]time.Time{}} }

func (s *scheduler) next(plan SymbolPlan, now time.Time, activeEvery, scannerEvery time.Duration) string {
	choose := func(symbols []string, every time.Duration) string {
		best := ""
		var oldest time.Time
		for _, sym := range symbols {
			last, tried := s.attempts[sym]
			if !tried {
				return sym
			}
			if now.Sub(last) < every {
				continue
			}
			if best == "" || last.Before(oldest) {
				best, oldest = sym, last
			}
		}
		return best
	}
	if sym := choose(plan.Active, activeEvery); sym != "" {
		return sym
	}
	if sym := choose(plan.Scanner, scannerEvery); sym != "" {
		return sym
	}
	s.prune(plan, now)
	return ""
}

func (s *scheduler) record(symbol string, now time.Time) { s.attempts[symbol] = now }

func (s *scheduler) prune(plan SymbolPlan, now time.Time) {
	keep := map[string]struct{}{}
	for _, sym := range plan.All() {
		keep[sym] = struct{}{}
	}
	for sym, at := range s.attempts {
		if _, ok := keep[sym]; !ok && now.Sub(at) > 10*time.Minute {
			delete(s.attempts, sym)
		}
	}
}

type limiter struct{ attempts []time.Time }

func (l *limiter) allow(now time.Time) bool {
	cutoff := now.Add(-30 * time.Second)
	keep := l.attempts[:0]
	for _, at := range l.attempts {
		if at.After(cutoff) {
			keep = append(keep, at)
		}
	}
	l.attempts = keep
	if len(l.attempts) >= 10 {
		return false
	}
	l.attempts = append(l.attempts, now)
	return true
}
