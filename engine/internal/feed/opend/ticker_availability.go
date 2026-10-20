package opend

import "github.com/earlisreal/eTape/engine/internal/feed"

// TickerAvailability reports acknowledged live coverage separately from quota
// starvation. A disconnect or a rejected subscription is not usable coverage.
func (m *subManager) TickerAvailability(symbol string) (active, capacity bool) {
	return m.SubscriptionAvailability(symbol, feed.SubTicker)
}

func (m *subManager) SubscriptionAvailability(symbol string, sub feed.SubType) (active, capacity bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := subKey{Symbol: symbol, Sub: sub}
	if m.connectionDown || m.quarantine[key] {
		return false, false
	}
	_, active = m.active[key]
	return active, !active && m.starved[symbol]
}

func (f *OpenDFeed) TickerAvailability(symbol string) (active, capacity bool) {
	return f.sub.TickerAvailability(symbol)
}

func (m *subManager) ExecutionCoverage(symbol string, sub feed.SubType) feed.Coverage {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := subKey{Symbol: symbol, Sub: sub}
	s := m.active[k]
	if m.connectionDown || m.quarantine[k] || s == nil {
		return feed.Coverage{}
	}
	return feed.Coverage{Verified: true, Revision: s.revision, SinceMs: s.subscribedAt.UnixMilli()}
}
