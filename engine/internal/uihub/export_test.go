package uihub

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// NewHubForTest builds a Hub with a fresh mirror (no venues) and second-scale
// (test-friendly) intervals, exported so external test packages (uihub_test)
// that need a real Hub for server.go tests can construct one without reaching
// into unexported fields.
func NewHubForTest(clk clock.Clock) (*Hub, *mirror) {
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 200, 200, 500, 500)
	h := NewHub(clk, HubConfig{
		MDInterval: 20 * time.Millisecond, AccountInterval: 250 * time.Millisecond,
		PositionInterval: 100 * time.Millisecond, Buf: 256,
	}, m)
	return h, m
}

// NewCommandsForTest exposes newCommands to external test packages.
func NewCommandsForTest(ex execDoer, c configStore, i indicatorCtl, d demandCtl, f func() Feed) commandHandler {
	return newCommands(ex, c, i, d, f)
}

// NewQueriesForTest exposes newQueries to external test packages.
func NewQueriesForTest(f fillsQuerier) queryHandler { return newQueries(f) }
