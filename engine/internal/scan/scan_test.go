package scan

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestRankRowsFloatUnitAndThresholds(t *testing.T) {
	cfg := config.Scan{MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000}
	// universe stores ACTUAL shares already converted from moomoo thousands.
	universe := map[string]float64{"US.LOWF": 20_000_000, "US.BIGF": 500_000_000}
	items := []rankItem{
		{Symbol: "US.LOWF", ChangePct: 12.5, Last: 4.2, Volume: 300_000}, // passes
		{Symbol: "US.BIGF", ChangePct: 20.0, Last: 8.0, Volume: 900_000}, // fails float cap
		{Symbol: "US.THIN", ChangePct: 30.0, Last: 1.0, Volume: 5_000},   // fails volume floor
		{Symbol: "US.FLAT", ChangePct: 1.0, Last: 2.0, Volume: 500_000},  // fails change threshold
	}
	rows := rankRows(items, universe, cfg)
	if len(rows) != 1 || rows[0].Symbol != "US.LOWF" {
		t.Fatalf("only US.LOWF should pass all thresholds, got %+v", rows)
	}
	if rows[0].FloatShares == nil || *rows[0].FloatShares != 20_000_000 {
		t.Fatalf("float should be actual shares from universe: %+v", rows[0])
	}
	if rows[0].ChangePct == nil || *rows[0].ChangePct != 12.5 {
		t.Fatalf("changePct wrong: %+v", rows[0])
	}
}

func TestRankRowsUnknownFloatIsNilNotZero(t *testing.T) {
	cfg := config.Scan{MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 0}
	rows := rankRows([]rankItem{{Symbol: "US.NEW", ChangePct: 9, Last: 2, Volume: 200_000}}, map[string]float64{}, cfg)
	// Unknown float: keep the row (can't disprove the cap) but floatShares must be nil, not 0.
	if len(rows) != 1 || rows[0].FloatShares != nil {
		t.Fatalf("unknown float must be nil and row retained: %+v", rows)
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
