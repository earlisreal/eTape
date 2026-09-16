package scan

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func (it rankItem) changeValue() (*float64, string) {
	if !it.snapshotAttempted {
		v := it.ChangePct
		return &v, "ready"
	}
	if !it.snapshotUsable || it.observedChange == nil {
		if it.snapshotUsable && it.changeStatus == "warming" {
			return nil, "warming"
		}
		return nil, "unavailable"
	}
	return it.observedChange, "ready"
}

func (it rankItem) rowVolume() int64 {
	if !it.snapshotAttempted || !it.snapshotUsable || it.observedVolume == nil {
		return 0
	}
	return *it.observedVolume
}

func (it rankItem) rowLast() *float64 {
	if !it.snapshotAttempted || !it.snapshotUsable || it.observedPrice == nil {
		return nil
	}
	return it.observedPrice
}

func basisDuration(basis string) (time.Duration, bool) {
	switch basis {
	case "1m":
		return time.Minute, true
	case "5m":
		return 5 * time.Minute, true
	case "1h":
		return time.Hour, true
	default:
		return 0, false
	}
}

func (p *Poller) updateObservations(now time.Time, phase session.Phase, items map[string]rankItem, filters wsmsg.ScannerFilters) {
	for symbol, it := range items {
		if !it.snapshotAttempted {
			continue
		}
		if !it.snapshotUsable {
			it.changeStatus = "unavailable"
			if h := p.history[symbol]; h != nil && !h.last.IsZero() && now.Sub(h.last) > rollingSampleGap {
				h.invalidate()
				p.alerts.resetSymbol(symbol, true)
			}
			items[symbol] = it
			continue
		}
		if it.observedPrice != nil {
			h := p.history[symbol]
			if h == nil {
				h = &rollingHistory{silent: true}
				p.history[symbol] = h
			}
			if h.observe(now, *it.observedPrice) {
				p.alerts.resetSymbol(symbol, true)
			}
		}
		if filters.ChangeBasis == "previous_close" {
			cycle := session.TradingCycleStart(now).UnixMilli()
			if it.observedPrice != nil && it.closePrice != nil && it.closeCycle == cycle && *it.closePrice > 0 {
				v := (*it.observedPrice - *it.closePrice) / *it.closePrice * 100
				it.observedChange, it.changeStatus = &v, "ready"
			} else {
				it.observedChange, it.changeStatus = nil, "unavailable"
			}
		} else if duration, ok := basisDuration(filters.ChangeBasis); ok {
			if h := p.history[symbol]; h != nil {
				if v, ready := h.pct(now, duration); ready {
					h.consumeSilent()
					it.observedChange, it.changeStatus = &v, "ready"
				} else {
					it.observedChange, it.changeStatus = nil, "warming"
				}
			} else {
				it.observedChange, it.changeStatus = nil, "warming"
			}
		}
		items[symbol] = it
	}
}

func (p *Poller) evaluateAlerts(now time.Time, filters wsmsg.ScannerFilters, items map[string]rankItem, wasBoard map[string]bool) {
	p.mu.RLock()
	baseline := p.baseline
	p.mu.RUnlock()
	for symbol, it := range items {
		value, _ := it.changeValue()
		if filters.Mode == "most_active" && value == nil && it.snapshotUsable && it.observedPrice != nil {
			zero := 0.0
			value = &zero
		}
		eligible := len(rankRowsFiltered([]rankItem{it}, p.floats, filters)) != 0
		newArrival := !wasBoard[symbol]
		quiet := baseline
		p.alerts.observe(symbol, filters.Mode, filters.MinChangePct, value, eligible, newArrival, quiet, now)
		it.alertSeq = p.alerts.revision(symbol)
		items[symbol] = it
	}
}

func (p *Poller) retireCandidates(items []rankItem, now time.Time) {
	current := make(map[string]bool, len(items))
	for _, it := range items {
		current[it.Symbol] = true
	}
	for symbol, h := range p.history {
		if current[symbol] {
			continue
		}
		if _, retained := p.board[symbol]; !retained && (h.last.IsZero() || now.Sub(h.last) > rollingKeep) {
			delete(p.history, symbol)
		}
	}
}

func (p *Poller) warmingCount(items map[string]rankItem, filters wsmsg.ScannerFilters) int {
	if filters.ChangeBasis == "previous_close" {
		return 0
	}
	count := 0
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.Symbol] {
			continue
		}
		seen[it.Symbol] = true
		if _, retained := p.board[it.Symbol]; retained {
			continue
		}
		if _, status := it.changeValue(); status == "warming" {
			count++
		}
	}
	return count
}
