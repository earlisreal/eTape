package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataSettingsWritePreservesOtherConfigAndRejectsStaleOrInvalidEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := `[scan]
premarket_ms = 2000
rth_ms = 3000
min_change_pct = 0
max_float_shares = 0
min_volume = 123

[md]
session_anchor = "09:30"

[seed]
moomoo_attempted = true

[[venue]]
id = "sim-paper"
broker = "sim"
env = "paper"

[gate.global]
max_day_loss = 500
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Scan.MinChangePct != 0 || cfg.Scan.MaxFloatShares != 0 || cfg.Scan.MinVolume != 123 {
		t.Fatalf("legacy zero values lost: %+v", cfg.Scan)
	}
	expected := cfg.DataSettings()
	next := expected
	next.ScannerRTHMs, next.IntradayDays, next.YahooNews = 1500, 2, true
	if err := WriteDataSettings(path, expected, next, nil); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DataSettings() != next || got.Gate.Global.MaxDayLoss != 500 || !got.Seed.MoomooAttempted || len(got.Venues) != 1 {
		t.Fatalf("round trip lost settings or unrelated data: %+v", got)
	}
	if got.Scan.LegacyMinChangePct == nil || *got.Scan.LegacyMinChangePct != 0 || got.Scan.LegacyMaxFloatShares == nil || *got.Scan.LegacyMaxFloatShares != 0 {
		t.Fatalf("unmigrated legacy zeros did not survive: %+v", got.Scan)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil || string(backup) != original {
		t.Fatalf("backup = %q, err %v", backup, err)
	}
	if err := WriteDataSettings(path, expected, next, nil); err == nil || !strings.Contains(err.Error(), "changed elsewhere") {
		t.Fatalf("stale save err = %v", err)
	}
	invalid := next
	invalid.RecordingMaxBytes = 1
	if err := WriteDataSettings(path, next, invalid, nil); err == nil {
		t.Fatal("invalid archive limit accepted")
	}
	after, err := Load(path)
	if err != nil || after.DataSettings() != next {
		t.Fatalf("failed save changed settings: %+v, err %v", after.DataSettings(), err)
	}
}

func TestMigratedScanKeysAreRemovedOnlyWhenUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[scan]\nmin_change_pct = 0\nmax_float_shares = 1000000\nmin_volume = 23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// An edit made after the boot migration must survive the next UI save.
	if err := os.WriteFile(path, []byte("[scan]\nmin_change_pct = 0\nmax_float_shares = 2000000\nmin_volume = 23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteVenueConfigMigrated(path, VenueConfig{}, &cfg.Scan); err != nil {
		t.Fatal(err)
	}
	changed, err := Load(path)
	if err != nil || changed.Scan.MaxFloatShares != 2000000 {
		t.Fatalf("new legacy value lost: %+v, err %v", changed.Scan, err)
	}
	if err := WriteVenueConfigMigrated(path, VenueConfig{}, &changed.Scan); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"min_change_pct", "max_float_shares", "min_volume"} {
		if strings.Contains(string(data), key) {
			t.Fatalf("migrated key %q remains in %s", key, data)
		}
	}
}

func TestRetiredFieldsDoNotChangeConfigOrBlockLoading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[scan]\nrank_pages = 9\n[news]\ncatalyst_min_score = 999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("retired fields blocked loading: %v", err)
	}
}
