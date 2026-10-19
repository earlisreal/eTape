package pricealert

import (
	"context"
	"time"
)

// Feed never blocks execution's eligible-print bridge. Overflow invalidates
// queued input so recovery requires a subsequent fresh trade.
func (m *Manager) Feed(print Print) {
	if print.Gap {
		at := print.RecvTsMs
		if at <= 0 {
			at = m.clk.Now().UnixMilli()
		}
		m.pendingGap.Store(at)
		return
	}
	select {
	case m.input <- print:
	default:
		m.pendingGap.Store(m.clk.Now().UnixMilli())
	}
}

func (m *Manager) Run(ctx context.Context) {
	ticker := m.clk.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.demand != nil {
			for symbol := range m.demanded {
				m.demand.Release(symbol)
			}
		}
		clear(m.demanded)
	}()
	var last uint64
	initial := true
	flush := func() {
		m.mu.Lock()
		if !initial && last == m.revision {
			m.mu.Unlock()
			return
		}
		snapshot := m.snapshotLocked()
		last, initial = snapshot.Revision, false
		m.mu.Unlock()
		if m.onSnapshot != nil {
			m.onSnapshot(snapshot)
		}
	}
	flush()
	for {
		if gap := m.pendingGap.Swap(0); gap > 0 {
			_ = m.Ingest(Print{Gap: true, RecvTsMs: gap})
			flush()
		}
		select {
		case <-ctx.Done():
			return
		case print := <-m.input:
			if gap := m.pendingGap.Swap(0); gap > 0 {
				_ = m.Ingest(Print{Gap: true, RecvTsMs: gap})
			}
			_ = m.Ingest(print) // storage failures are retained and exposed in state
			flush()
		case <-ticker.C():
			m.Refresh()
			flush()
		}
	}
}
