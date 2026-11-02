package main

import (
	"errors"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/config"
)

type scannerMigrationFake struct {
	values  map[string]string
	pending map[string]string
	fail    bool
}

func (s *scannerMigrationFake) GetConfig(key string) (string, bool, error) {
	if s.fail {
		return "", false, errors.New("database unavailable")
	}
	v, ok := s.values[key]
	return v, ok, nil
}
func (s *scannerMigrationFake) SetConfig(key, value string) {
	if s.pending == nil {
		s.pending = map[string]string{}
	}
	s.pending[key] = value
}
func (s *scannerMigrationFake) Flush() {
	if s.fail {
		return
	}
	for key, value := range s.pending {
		s.values[key] = value
	}
}

func TestScannerDefaultsMigrateAndSurviveRemovingTOMLKeys(t *testing.T) {
	st := &scannerMigrationFake{values: map[string]string{}}
	cfg := config.Default().Scan
	zero, cap, volume := float64(0), float64(2500000), int64(321)
	cfg.MinChangePct, cfg.MaxFloatShares, cfg.MinVolume = zero, cap, volume
	cfg.LegacyMinChangePct, cfg.LegacyMaxFloatShares, cfg.LegacyMinVolume = &zero, &cap, &volume
	first, migrated, err := scannerDefaultsAtBoot(st, cfg, false)
	if err != nil || migrated == nil || first.MinChangePct != 0 || first.MaxFloatShares == nil || *first.MaxFloatShares != cap || first.MinVolume != float64(volume) {
		t.Fatalf("migration = %+v, %v, %v", first, migrated, err)
	}
	restored, migratedAgain, err := scannerDefaultsAtBoot(st, config.Default().Scan, false)
	if err != nil || migratedAgain != nil || restored.MinChangePct != 0 || restored.MaxFloatShares == nil || *restored.MaxFloatShares != cap || restored.MinVolume != float64(volume) {
		t.Fatalf("restored = %+v, %v, %v", restored, migratedAgain, err)
	}
	// An existing user-selected filter remains authoritative.
	st.values["scanner.filters.v2"] = `{"mode":"losers","minChangePct":7,"maxFloatShares":null,"minVolume":5,"minRelativeVolume":0,"floatUnit":"M","volumeUnit":"K","sessionVolumeUnit":"K"}`
	if got := restoreScannerFilters(st, restored); got.Mode != "losers" || got.MinChangePct != 7 {
		t.Fatalf("saved filter lost: %+v", got)
	}
}

func TestScannerDefaultsMigrationFailureAndDemoPreserveLegacyFields(t *testing.T) {
	cfg := config.Default().Scan
	value := float64(8)
	cfg.MinChangePct, cfg.LegacyMinChangePct = value, &value
	st := &scannerMigrationFake{values: map[string]string{}, fail: true}
	if _, migrated, err := scannerDefaultsAtBoot(st, cfg, false); err == nil || migrated != nil {
		t.Fatalf("failed migration allowed cleanup: migrated=%v err=%v", migrated, err)
	}
	if _, migrated, err := scannerDefaultsAtBoot(st, cfg, true); err != nil || migrated != nil {
		t.Fatalf("demo attempted migration: migrated=%v err=%v", migrated, err)
	}
}
