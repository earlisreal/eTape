package opend

import "github.com/earlisreal/eTape/engine/internal/feed"

// TickerAvailability reports acknowledged live coverage separately from quota
// starvation. A disconnect or a rejected subscription is not usable coverage.
func (m *subManager) TickerAvailability(symbol string) (active, capacity bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := subKey{Symbol: symbol, Sub: feed.SubTicker}
	if m.connectionDown || m.quarantine[key] {
		return false, false
	}
	_, active = m.active[key]
	return active, !active && m.starved[symbol]
}

func (f *OpenDFeed) TickerAvailability(symbol string) (active, capacity bool) {
	return f.sub.TickerAvailability(symbol)
}
