package md

import (
	"context"
	"errors"
	"math"
	"runtime"
	"slices"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestVolumeProfileConservesCapturedVolumeAndExpandsValueArea(t *testing.T) {
	const start int64 = 1791540000000
	ticks := []feed.Tick{
		{Symbol: "US.TEST", Seq: 1, TsMs: start, Price: 10, Volume: 5, Condition: feed.TradeConditionAutomaticMatch},
		{Symbol: "US.TEST", Seq: 2, TsMs: start + 1, Price: 11.1, Volume: 25, Condition: feed.TradeConditionOddLot},
		{Symbol: "US.TEST", Seq: 3, TsMs: start + 2, Price: 12.1, Volume: 30, Condition: feed.TradeConditionAutomaticMatch},
		{Symbol: "US.TEST", Seq: 4, TsMs: start + 3, Price: 14, Volume: 10, Condition: feed.TradeConditionAutomaticMatch},
	}
	duplicate := ticks[2]
	duplicate.Dir = feed.Buy
	ticks = append(ticks, duplicate, feed.Tick{Symbol: "US.TEST", Seq: 5, TsMs: start + 4, Price: 100, Volume: 999, Condition: feed.TradeConditionCorrectedComprehensiveLatePrice})
	got, err := CalculateVolumeProfile(context.Background(), ticks, start, start+10, 4, 70)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 70 || len(got.Rows) != 4 || got.Rows[0].Volume != 5 || got.Rows[1].Volume != 25 || got.Rows[2].Volume != 30 || got.Rows[3].Volume != 10 {
		t.Fatalf("captured volume: %+v", got)
	}
	if got.POC == nil || *got.POC != 12.5 || *got.VAL != 11 || *got.VAH != 13 {
		t.Fatalf("levels: %+v", got)
	}
}

func TestVolumeProfileMaximumWorkingSet(t *testing.T) {
	if testing.Short() {
		t.Skip("capacity measurement")
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	const start int64 = 1791540000000
	ticks := make([]feed.Tick, VolumeProfileMaxPrints)
	for i := range ticks {
		ticks[i] = feed.Tick{Symbol: "US.BUSY", Seq: int64(i + 1), TsMs: start, Price: 1.2345, Volume: 1, Condition: feed.TradeConditionOddLot}
	}
	got, err := CalculateVolumeProfile(context.Background(), ticks, start, start+1, 200, 70)
	runtime.ReadMemStats(&after)
	if err != nil || got.Total != VolumeProfileMaxPrints {
		t.Fatalf("maximum calculation: %+v %v", got, err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated > 64<<20 {
		t.Fatalf("working-data allocation exceeded 64 MiB: %d", allocated)
	}
	t.Logf("96,000 reports plus deduplication/calculation allocated=%d bytes (SQLite cache capped separately at 1 MiB)", allocated)
}

func TestVolumeProfileBoundariesConflictsAndFlatPrice(t *testing.T) {
	const start int64 = 1791540000000
	base := feed.Tick{Symbol: "US.TEST", Seq: 9007199254740993, TsMs: start, Price: 1.2345, Volume: 17, Condition: feed.TradeConditionOddLot}
	end := base
	end.Seq++
	end.TsMs = start + 100
	got, err := CalculateVolumeProfile(context.Background(), []feed.Tick{base, base, end}, start, start+100, 100, 100)
	if err != nil || got.Total != 17 || len(got.Rows) != 1 || *got.POC != base.Price || *got.VAL != base.Price || *got.VAH != base.Price {
		t.Fatalf("flat half-open profile: %+v %v", got, err)
	}
	conflict := base
	conflict.TsMs = start - 1
	missing := base
	missing.Seq = 0
	invalid := base
	invalid.Seq = 2
	invalid.Price = math.NaN()
	got, err = CalculateVolumeProfile(context.Background(), []feed.Tick{base, conflict, missing, invalid}, start, start+100, 100, 70)
	if err != nil || got.Total != 0 || got.POC != nil || len(got.Rows) != 0 || !slices.Contains(got.Reasons, "conflicting_reports") || !slices.Contains(got.Reasons, "missing_sequence") || !slices.Contains(got.Reasons, "invalid_report") {
		t.Fatalf("exclusions: %+v %v", got, err)
	}
}

func TestVolumeProfileTiesZeroRowsAndCancellation(t *testing.T) {
	const start int64 = 1791540000000
	var ticks []feed.Tick
	for i, price := range []float64{10, 11.1, 12.1, 14} {
		ticks = append(ticks, feed.Tick{Symbol: "US.TEST", Seq: int64(i + 1), TsMs: start, Price: price, Volume: 10, Condition: feed.TradeConditionAutomaticMatch})
	}
	got, err := CalculateVolumeProfile(context.Background(), ticks, start, start+1, 4, 50)
	if err != nil || *got.POC != 11.5 || *got.VAL != 10 || *got.VAH != 12 {
		t.Fatalf("POC and adjacent ties: %+v %v", got, err)
	}
	got, err = CalculateVolumeProfile(context.Background(), []feed.Tick{ticks[0], ticks[3]}, start, start+1, 4, 100)
	if err != nil || got.Rows[1].Volume != 0 || got.Rows[2].Volume != 0 || *got.VAL != 10 || *got.VAH != 14 {
		t.Fatalf("zero rows and 100%%: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = CalculateVolumeProfile(ctx, nil, start, start+1, 1, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err = CalculateVolumeProfile(context.Background(), make([]feed.Tick, VolumeProfileMaxPrints+1), start, start+1, 1, 1); !errors.Is(err, ErrVolumeProfileTooLarge) {
		t.Fatalf("bound: %v", err)
	}
}

func TestVolumeProfilePOCTieUsesLowerRowDespiteFloatingPointRounding(t *testing.T) {
	const start int64 = 1791540000000
	var ticks []feed.Tick
	for i, price := range []float64{10.01, 10.01495, 10.01505, 10.02} {
		volume := int64(10)
		if i == 0 || i == 3 {
			volume = 1
		}
		ticks = append(ticks, feed.Tick{Symbol: "US.TEST", Seq: int64(i + 1), TsMs: start, Price: price, Volume: volume, Condition: feed.TradeConditionAutomaticMatch})
	}
	got, err := CalculateVolumeProfile(context.Background(), ticks, start, start+1, 100, 70)
	if err != nil {
		t.Fatal(err)
	}
	want := got.Rows[49].Lower + (got.Rows[49].Upper-got.Rows[49].Lower)/2
	if *got.POC != want {
		t.Fatalf("equal midpoint-distance tie: got %.17g want %.17g", *got.POC, want)
	}
}
