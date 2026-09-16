package scan

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

const (
	rollingSampleGap = 5 * time.Second
	rollingKeep      = time.Hour + rollingSampleGap
)

type priceSample struct {
	at    time.Time
	price float64
}

// rollingHistory is deliberately small: one symbol keeps only the last hour
// plus the lookup tolerance. A gap starts a new segment by dropping the old
// samples; no interpolation can bridge it.
type rollingHistory struct {
	samples []priceSample
	last    time.Time
	silent  bool
}

func (h *rollingHistory) observe(at time.Time, price float64) (gap bool) {
	if price <= 0 || at.IsZero() {
		return false
	}
	if !h.last.IsZero() && at.Sub(h.last) > rollingSampleGap {
		h.samples = nil
		h.silent = true
		gap = true
	}
	h.samples = append(h.samples, priceSample{at: at, price: price})
	h.last = at
	cutoff := at.Add(-rollingKeep)
	first := 0
	for first < len(h.samples) && h.samples[first].at.Before(cutoff) {
		first++
	}
	if first > 0 {
		h.samples = h.samples[first:]
	}
	return gap
}

func (h *rollingHistory) invalidate() {
	h.samples = nil
	h.last = time.Time{}
	h.silent = true
}

func (h *rollingHistory) pct(now time.Time, duration time.Duration) (float64, bool) {
	if len(h.samples) == 0 || h.last.IsZero() || now.Sub(h.last) > rollingSampleGap {
		return 0, false
	}
	target := now.Add(-duration)
	var base priceSample
	found := false
	for _, sample := range h.samples {
		if sample.at.After(target) {
			break
		}
		base, found = sample, true
	}
	if !found || target.Sub(base.at) > rollingSampleGap || base.price <= 0 {
		return 0, false
	}
	return (h.lastPrice()/base.price - 1) * 100, true
}

func (h *rollingHistory) lastPrice() float64 {
	if len(h.samples) == 0 {
		return 0
	}
	return h.samples[len(h.samples)-1].price
}

func (h *rollingHistory) consumeSilent() bool {
	if !h.silent {
		return false
	}
	h.silent = false
	return true
}

func discoveryStages(phase session.Phase) []session.Phase {
	switch phase {
	case session.PostMarket:
		return []session.Phase{session.PostMarket}
	case session.Overnight:
		return []session.Phase{session.PostMarket, session.Overnight}
	case session.PreMarket:
		return []session.Phase{session.PostMarket, session.Overnight, session.PreMarket}
	case session.RTH:
		return []session.Phase{session.PostMarket, session.Overnight, session.PreMarket, session.RTH}
	default:
		return []session.Phase{session.PreMarket}
	}
}

func (p *Poller) stageBootstrapped(cycle int64, phase session.Phase) bool {
	return p.bootstrap[cycle] != nil && p.bootstrap[cycle][phase]
}

func (p *Poller) markStageBootstrapped(cycle int64, phase session.Phase) {
	stages := p.bootstrap[cycle]
	if stages == nil {
		stages = map[session.Phase]bool{}
		p.bootstrap[cycle] = stages
	}
	stages[phase] = true
	p.pruneCycleState(cycle)
}

func (p *Poller) rememberCycleCandidates(cycle int64, items []rankItem) {
	p.pruneCycleState(cycle)
	candidates := p.cycleCandidates[cycle]
	if candidates == nil {
		candidates = map[string]rankItem{}
		p.cycleCandidates[cycle] = candidates
	}
	for _, it := range items {
		if it.Symbol != "" {
			candidates[it.Symbol] = it
		}
	}
}

func (p *Poller) pruneCycleState(cycle int64) {
	cutoff := cycle - 3*24*time.Hour.Milliseconds()
	for key := range p.bootstrap {
		if key < cutoff {
			delete(p.bootstrap, key)
		}
	}
	for key := range p.cycleCandidates {
		if key < cutoff {
			delete(p.cycleCandidates, key)
		}
	}
}

func (p *Poller) replaceCurrentCandidates(cycle int64, phase session.Phase, items []rankItem, retire bool) {
	if retire && p.currentCandidateCycle == cycle && p.currentCandidatePhase == phase {
		current := make(map[string]bool, len(items))
		for _, it := range items {
			current[it.Symbol] = true
		}
		for _, old := range p.currentCandidates {
			_, retained := p.board[old.Symbol]
			if current[old.Symbol] || retained || p.cycleCandidates[cycle][old.Symbol].Symbol != "" {
				continue
			}
			delete(p.history, old.Symbol)
			p.alerts.resetSymbol(old.Symbol, true)
		}
	}
	p.currentCandidates = append(p.currentCandidates[:0], items...)
	p.currentCandidateCycle, p.currentCandidatePhase = cycle, phase
}
