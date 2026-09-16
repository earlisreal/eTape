package main

import (
	"encoding/json"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/scan"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type scannerFilterConfigSpy struct {
	values map[string]string
	set    map[string]string
}

func (s *scannerFilterConfigSpy) GetConfig(key string) (string, bool, error) {
	value, ok := s.values[key]
	return value, ok, nil
}

func (s *scannerFilterConfigSpy) SetConfig(key, value string) {
	if s.set == nil {
		s.set = map[string]string{}
	}
	s.set[key] = value
}

func TestRestoreScannerFiltersV2WinsOverLegacyV1(t *testing.T) {
	defaults := scan.Defaults(config.Scan{})
	spy := &scannerFilterConfigSpy{values: map[string]string{
		"scanner.filters.v2": `{"mode":"losers","minChangePct":7,"maxFloatShares":1000000,"minVolume":2000,"minRelativeVolume":3.5,"floatUnit":"M","volumeUnit":"K"}`,
		"scanner.filters.v1": `{"mode":"gainers","minChangePct":1,"maxFloatShares":null,"minVolume":1,"minVolumeRatio":99,"floatUnit":"K","volumeUnit":"M"}`,
	}}
	got := restoreScannerFilters(spy, defaults)
	if got.Mode != "losers" || got.MinRelativeVolume != 3.5 || got.MinChangePct != 7 || got.MinVolume != 2000 {
		t.Fatalf("v2 was not authoritative: %+v", got)
	}
	if len(spy.set) != 0 {
		t.Fatalf("v2 load should not rewrite settings: %+v", spy.set)
	}
}

func TestRestoreScannerFiltersMigratesV1AndResetsThreshold(t *testing.T) {
	defaults := scan.Defaults(config.Scan{})
	spy := &scannerFilterConfigSpy{values: map[string]string{
		"scanner.filters.v1": `{"mode":"losers","minChangePct":7,"maxFloatShares":1000000,"minVolume":2000,"minVolumeRatio":3.5,"floatUnit":"M","volumeUnit":"K"}`,
	}}
	got := restoreScannerFilters(spy, defaults)
	if got.Mode != "losers" || got.MinChangePct != 7 || got.MaxFloatShares == nil || *got.MaxFloatShares != 1000000 || got.MinVolume != 2000 || got.MinRelativeVolume != 0 || got.FloatUnit != "M" || got.VolumeUnit != "K" {
		t.Fatalf("v1 migration changed unrelated filters: %+v", got)
	}
	var saved wsmsg.ScannerFilters
	if err := json.Unmarshal([]byte(spy.set["scanner.filters.v2"]), &saved); err != nil || saved.MinRelativeVolume != 0 {
		t.Fatalf("migrated v2=%q err=%v", spy.set["scanner.filters.v2"], err)
	}
}

func TestRestoreScannerFiltersMalformedDataFallsBackToDefaults(t *testing.T) {
	defaults := scan.Defaults(config.Scan{MinChangePct: 5})
	for name, values := range map[string]string{
		"bad v2": `{"mode":"gainers","minVolumeRatio":99}`,
		"bad v1": `{"mode":"gainers","minVolumeRatio":-1,"floatUnit":"M","volumeUnit":"K"}`,
	} {
		t.Run(name, func(t *testing.T) {
			spy := &scannerFilterConfigSpy{values: map[string]string{"scanner.filters.v2": values}}
			if name == "bad v1" {
				spy.values = map[string]string{"scanner.filters.v1": values}
			}
			if got := restoreScannerFilters(spy, defaults); got.MinChangePct != defaults.MinChangePct || got.MinRelativeVolume != 0 {
				t.Fatalf("got %+v, want defaults %+v", got, defaults)
			}
		})
	}
}
