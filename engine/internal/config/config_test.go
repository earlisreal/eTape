package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if got := cfg.OpenD.Addr(); got != "127.0.0.1:11111" {
		t.Fatalf("default OpenD addr = %q, want 127.0.0.1:11111", got)
	}
}

func TestLoadOverridesOpenD(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[opend]\nhost = \"10.0.0.5\"\nport = 22222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.OpenD.Addr(); got != "10.0.0.5:22222" {
		t.Fatalf("OpenD addr = %q, want 10.0.0.5:22222", got)
	}
}

func TestLoadMalformedFileErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(p, []byte("[opend\nhost = "), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("Load: expected error for malformed TOML, got nil")
	}
}

func TestFeedAndMDSectionsWithDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[feed]
watchlist = ["US.AAPL", "US.TSLA"]
quota_slots = 300

[md]
session_anchor = "09:00"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Feed.Watchlist) != 2 || cfg.Feed.QuotaSlots != 300 {
		t.Fatalf("feed = %+v", cfg.Feed)
	}
	if !cfg.Feed.ExtendedTime || cfg.Feed.UnsubHysteresisSecs != 300 {
		t.Fatalf("feed defaults not preserved: %+v", cfg.Feed)
	}
	if cfg.MD.TapeRing != 65536 {
		t.Fatalf("md defaults not preserved: %+v", cfg.MD)
	}
	secs, err := cfg.MD.AnchorSecs()
	if err != nil || secs != 9*3600 {
		t.Fatalf("AnchorSecs = %d, %v; want 32400", secs, err)
	}
}

func TestAnchorSecsRejectsGarbage(t *testing.T) {
	m := MD{SessionAnchor: "9am"}
	if _, err := m.AnchorSecs(); err == nil {
		t.Fatal("want parse error for '9am'")
	}
}
