package scan

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestRankRowsThresholds(t *testing.T) {
	cfg := config.Scan{MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000}
	floats := map[string]floatEntry{"US.LOWF": {shares: 20_000_000}, "US.BIGF": {shares: 500_000_000}}
	items := []rankItem{
		{Symbol: "US.LOWF", ChangePct: 12.5, Last: 4.2, Volume: 300_000}, // passes
		{Symbol: "US.BIGF", ChangePct: 20.0, Last: 8.0, Volume: 900_000}, // fails float cap
		{Symbol: "US.THIN", ChangePct: 30.0, Last: 1.0, Volume: 5_000},   // fails volume floor
		{Symbol: "US.FLAT", ChangePct: 1.0, Last: 2.0, Volume: 500_000},  // fails change threshold
	}
	rows := rankRows(items, floats, cfg)
	if len(rows) != 1 || rows[0].Symbol != "US.LOWF" {
		t.Fatalf("only US.LOWF should pass all thresholds, got %+v", rows)
	}
	if rows[0].FloatShares == nil || *rows[0].FloatShares != 20_000_000 {
		t.Fatalf("float should be actual shares from cache: %+v", rows[0])
	}
	if rows[0].ChangePct == nil || *rows[0].ChangePct != 12.5 {
		t.Fatalf("changePct wrong: %+v", rows[0])
	}
}

func TestRankRowsThreeStateFloat(t *testing.T) {
	floats := map[string]floatEntry{
		"US.UNDER": {shares: 20_000_000},
		"US.OVER":  {shares: 500_000_000},
		"US.BAD":   {bad: true},
		// US.ABSENT intentionally not in the cache.
	}
	items := []rankItem{
		{Symbol: "US.UNDER", ChangePct: 12, Last: 4, Volume: 300_000},
		{Symbol: "US.OVER", ChangePct: 20, Last: 8, Volume: 900_000},
		{Symbol: "US.BAD", ChangePct: 15, Last: 3, Volume: 400_000},
		{Symbol: "US.ABSENT", ChangePct: 11, Last: 2, Volume: 250_000},
	}

	// Cap ON: OVER (known over cap) and BAD dropped; UNDER shows float; ABSENT kept, blank.
	withCap := rankRows(items, floats, config.Scan{MinChangePct: 5, MaxFloatShares: 50_000_000})
	gotCap := map[string]*float64{}
	for _, r := range withCap {
		gotCap[r.Symbol] = r.FloatShares
	}
	if len(withCap) != 2 {
		t.Fatalf("cap on: want 2 rows (UNDER, ABSENT), got %d: %+v", len(withCap), withCap)
	}
	if f := gotCap["US.UNDER"]; f == nil || *f != 20_000_000 {
		t.Fatalf("UNDER float wrong: %+v", gotCap["US.UNDER"])
	}
	if f, ok := gotCap["US.ABSENT"]; !ok || f != nil {
		t.Fatalf("ABSENT must be present with nil float: ok=%v f=%v", ok, f)
	}

	// Cap OFF: nothing dropped for float; BAD shown blank, OVER shown with its float.
	noCap := rankRows(items, floats, config.Scan{MinChangePct: 5, MaxFloatShares: 0})
	got := map[string]*float64{}
	for _, r := range noCap {
		got[r.Symbol] = r.FloatShares
	}
	if len(noCap) != 4 {
		t.Fatalf("cap off: want all 4 rows, got %d: %+v", len(noCap), noCap)
	}
	if f := got["US.OVER"]; f == nil || *f != 500_000_000 {
		t.Fatalf("OVER float should show when cap off: %+v", got["US.OVER"])
	}
	if got["US.BAD"] != nil {
		t.Fatalf("BAD float must be blank (nil): %+v", got["US.BAD"])
	}
}

func TestResetIfNewDayClearsFloatCacheAndSeen(t *testing.T) {
	p := &Poller{
		floats:  map[string]floatEntry{"US.A": {shares: 1}},
		seen:    map[string]map[string]bool{"premarket": {"US.A": true}},
		seenDay: session.DayMs(time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC).UnixMilli()),
	}
	p.resetIfNewDay(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)) // different ET day
	if len(p.floats) != 0 {
		t.Fatalf("float cache should clear on new day: %+v", p.floats)
	}
	if len(p.seen) != 0 {
		t.Fatalf("seen-sets should clear on new day: %+v", p.seen)
	}
}

func TestNewHitsSeenSet(t *testing.T) {
	p := &Poller{seen: map[string]map[string]bool{}}
	first := p.newHits("premarket", []wsmsg.ScannerRow{{Symbol: "US.A"}, {Symbol: "US.B"}})
	if len(first) != 2 {
		t.Fatalf("first pass: both are new hits, got %v", first)
	}
	second := p.newHits("premarket", []wsmsg.ScannerRow{{Symbol: "US.A"}, {Symbol: "US.C"}})
	if len(second) != 1 || second[0] != "US.C" {
		t.Fatalf("second pass: only US.C is new, got %v", second)
	}
}
