package opend

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func (m *subManager) ordinaryWantedLocked(key subKey) bool {
	for _, d := range m.demands {
		if d.d.Symbol == key.Symbol && containsSub(d.d.Subs, key.Sub) {
			return true
		}
	}
	return false
}

func (m *subManager) pendingOrdinaryLocked() bool {
	for _, d := range m.demands {
		for _, sub := range d.d.Subs {
			key := subKey{d.d.Symbol, sub}
			if _, ok := m.active[key]; !ok && !m.quarantine[key] {
				return true
			}
		}
	}
	return false
}

func (m *subManager) kickOptional() {
	if !m.opt.OptionalBook {
		return
	}
	select {
	case m.optionalWake <- struct{}{}:
	default:
	}
}

func (m *subManager) recordCoverageLocked(key subKey, state, owner string) {
	if m.opt.Recorder == nil {
		return
	}
	m.opt.Recorder.Record(feed.Recording{Kind: "coverage", Symbol: key.Symbol, Source: feed.SourceRef{ReceiptMs: m.clk.Now().UnixMilli()}, Data: struct {
		Subtype           feed.SubType
		State, Owner      string
		ManagerGeneration uint64
	}{key.Sub, state, owner, m.connectionGeneration}})
}

func (m *subManager) bookCoverageLocked(symbol, state string) {
	if m.bookCoverage[symbol] == state {
		return
	}
	m.bookCoverage[symbol] = state
	m.recordCoverageLocked(subKey{symbol, feed.SubBook}, state, "recorder")
}

func (m *subManager) optionalFreshLocked(now time.Time) bool {
	return !m.connectionDown && m.quotaHolds == 0 && (!m.opt.RequireQuota || m.quotaKnown && now.Sub(m.quotaAt) <= 75*time.Second)
}

func (m *subManager) runOptionalBooks(ctx context.Context) {
	tick := m.clk.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.optionalWake:
		case <-tick.C():
		}
		m.optionalBookPass(ctx)
	}
}

// A separate worker keeps BOOK-only RPCs and seeds off ordinary admission.
// Account-wide reservations remain shared; no released quota is invented here.
func (m *subManager) optionalBookPass(ctx context.Context) {
	for range 16 {
		if ctx.Err() != nil {
			return
		}
		now := m.clk.Now()
		m.mu.Lock()
		fresh := m.optionalFreshLocked(now)
		pending := m.pendingOrdinaryLocked()
		adjusted := m.quotaRemain - m.quotaSpentSinceRead - m.quotaPending
		pressure := pending || !fresh || len(m.active) > m.opt.Budget || m.opt.RequireQuota && adjusted < m.opt.QuotaHeadroom
		var releases []subKey
		for key := range m.optional {
			if m.ordinaryWantedLocked(key) {
				delete(m.optional, key)
				m.bookCoverageLocked(key.Symbol, "shared")
				continue
			}
			st, active := m.active[key]
			_, tickerActive := m.active[subKey{key.Symbol, feed.SubTicker}]
			if !active {
				delete(m.optional, key)
				continue
			}
			if pressure || !tickerActive {
				if now.Sub(st.subscribedAt) >= m.opt.MinHold {
					releases = append(releases, key)
				} else {
					m.bookCoverageLocked(key.Symbol, "release_waiting_min_hold")
				}
			}
		}
		sort.Slice(releases, func(i, j int) bool { return releases[i].Symbol < releases[j].Symbol })
		if len(releases) > 0 {
			key := releases[0]
			generation := m.connectionGeneration
			requestCtx, cancel := context.WithCancel(ctx)
			m.optionalCancel = cancel
			m.optionalRelease = key
			m.mu.Unlock()
			err := m.qotSub(WithBackgroundRequest(requestCtx), []string{key.Symbol}, []feed.SubType{feed.SubBook}, false)
			cancel()
			m.mu.Lock()
			m.optionalCancel = nil
			m.optionalRelease = subKey{}
			// A DOM demand may have arrived while the unsubscribe was in flight.
			// Remove the acknowledged server subscription and let ordinary
			// priority re-admit it; never pretend the old BOOK remains active.
			if err == nil && generation == m.connectionGeneration {
				delete(m.active, key)
				delete(m.optional, key)
				m.bookCoverageLocked(key.Symbol, "released")
			} else if err != nil && generation == m.connectionGeneration {
				var biz *subBizError
				if !errors.As(err, &biz) {
					delete(m.active, key)
					delete(m.optional, key)
					m.quotaKnown = false
					m.bookCoverageLocked(key.Symbol, "release_outcome_unknown")
				}
			}
			m.mu.Unlock()
			if m.quotaRefresh != nil {
				m.quotaRefresh()
			}
			m.kickWorker()
			if err != nil {
				return
			}
			continue
		}
		var candidates []string
		for key := range m.active {
			if key.Sub == feed.SubTicker && (strings.HasPrefix(key.Symbol, "US.") || !strings.Contains(key.Symbol, ".")) {
				candidates = append(candidates, key.Symbol)
			}
		}
		sort.Strings(candidates)
		var candidate string
		for _, symbol := range candidates {
			key := subKey{symbol, feed.SubBook}
			if _, active := m.active[key]; active {
				if m.ordinaryWantedLocked(key) {
					m.bookCoverageLocked(symbol, "shared")
				} else if !pressure {
					m.optional[key] = true
					m.bookCoverageLocked(symbol, "active")
				}
				continue
			}
			if pressure || m.quotaPending > 0 || len(m.active) >= m.opt.Budget || m.opt.RequireQuota && adjusted <= m.opt.QuotaHeadroom || m.optionalFailed[key] >= subQuarantineThreshold {
				m.bookCoverageLocked(symbol, "unavailable")
				continue
			}
			candidate = symbol
			break
		}
		if candidate == "" {
			m.mu.Unlock()
			return
		}
		key := subKey{candidate, feed.SubBook}
		generation := m.connectionGeneration
		requestCtx, cancel := context.WithCancel(ctx)
		m.optionalCancel = cancel
		m.reserveQuotaAdmissionsLocked(1)
		m.mu.Unlock()
		err := m.qotSub(WithBackgroundRequest(requestCtx), []string{candidate}, []feed.SubType{feed.SubBook}, true)
		cancel()
		m.mu.Lock()
		m.optionalCancel = nil
		admitted := err == nil && generation == m.connectionGeneration
		if admitted {
			m.active[key] = &subState{subscribedAt: m.clk.Now()}
			m.optional[key] = true
			delete(m.optionalFailed, key)
			m.bookCoverageLocked(candidate, "active")
		} else {
			var biz *subBizError
			if errors.As(err, &biz) {
				m.optionalFailed[key]++
			} else if generation == m.connectionGeneration {
				m.quotaKnown = false
			}
			m.bookCoverageLocked(candidate, "unavailable")
		}
		m.mu.Unlock()
		m.finishQuotaAdmissions([]subKey{key})
		if admitted && m.optionalActivated != nil {
			m.optionalActivated(candidate)
		}
		if err != nil {
			if m.quotaRefresh != nil {
				m.quotaRefresh()
			}
			return
		}
	}
}
