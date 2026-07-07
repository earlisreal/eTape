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

func TestStoreDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Store.RetentionDays != 30 {
		t.Fatalf("RetentionDays default = %d, want 30", cfg.Store.RetentionDays)
	}
	if cfg.Store.FlushMs != 250 {
		t.Fatalf("FlushMs default = %d, want 250", cfg.Store.FlushMs)
	}
	if cfg.Store.DBPath != "" {
		t.Fatalf("DBPath default = %q, want empty (resolved by main)", cfg.Store.DBPath)
	}
}

func TestStoreOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[store]\ndb_path = \"/tmp/x.db\"\nretention_days = 7\nflush_ms = 100\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.DBPath != "/tmp/x.db" || cfg.Store.RetentionDays != 7 || cfg.Store.FlushMs != 100 {
		t.Fatalf("store override not applied: %+v", cfg.Store)
	}
}

func TestVenueAndGateParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
[[venue]]
id = "alpaca-paper"
broker = "alpaca"
env = "paper"
credentials = "alpaca"

[[venue]]
id = "tz-live"
broker = "tradezero"
env = "live"
credentials = "tradeZero"
account_id = "ACC123"

[gate.global]
max_day_loss = 1000.0
max_symbol_position_value = 100000.0
max_symbol_position_shares = 1000.0

[gate.venue.alpaca-paper]
max_order_value = 5000.0
max_position_value = 20000.0
max_position_shares = 200.0
max_open_orders = 3
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Venues) != 2 || cfg.Venues[0].ID != "alpaca-paper" || cfg.Venues[1].Broker != "tradezero" || cfg.Venues[1].AccountID != "ACC123" {
		t.Fatalf("venues wrong: %+v", cfg.Venues)
	}
	if cfg.Gate.Global.MaxDayLoss != 1000 {
		t.Fatalf("gate global wrong: %+v", cfg.Gate.Global)
	}
	gv, ok := cfg.Gate.Venue["alpaca-paper"]
	if !ok || gv.MaxOrderValue != 5000 || gv.MaxOpenOrders != 3 {
		t.Fatalf("gate venue wrong: %+v ok=%v", gv, ok)
	}
}

func TestVenueDefaultsEmpty(t *testing.T) {
	cfg := Default()
	if len(cfg.Venues) != 0 {
		t.Fatalf("default venues should be empty, got %+v", cfg.Venues)
	}
	if len(cfg.Gate.Venue) != 0 {
		t.Fatalf("default gate venue map should be empty, got %+v", cfg.Gate.Venue)
	}
}

func TestDefaultHasUIHubAndPollerSections(t *testing.T) {
	c := Default()
	if got := c.UIHub.Addr(); got != "127.0.0.1:8686" {
		t.Fatalf("UIHub.Addr() = %q, want 127.0.0.1:8686", got)
	}
	if c.UIHub.OutboundQueue != 1024 {
		t.Fatalf("UIHub.OutboundQueue = %d, want 1024", c.UIHub.OutboundQueue)
	}
	if c.UIHub.MDRateHz != 30 || c.UIHub.AccountRateHz != 4 || c.UIHub.PositionMs != 100 {
		t.Fatalf("UIHub rates = %v/%v/%v, want 30/4/100", c.UIHub.MDRateHz, c.UIHub.AccountRateHz, c.UIHub.PositionMs)
	}
	if !c.Scan.Enabled || c.Scan.PremarketMs != 2000 || c.Scan.MaxFloatShares != 50_000_000 {
		t.Fatalf("Scan defaults wrong: %+v", c.Scan)
	}
	if !c.News.Enabled || c.News.FocusedMs != 20000 {
		t.Fatalf("News defaults wrong: %+v", c.News)
	}
	if !c.Health.Enabled || c.Health.ProbeMs != 5000 {
		t.Fatalf("Health defaults wrong: %+v", c.Health)
	}
}

func TestLoadOverridesUIHubSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := "[uihub]\nport = 9000\nmd_rate_hz = 15.0\n\n[scan]\nmin_change_pct = 8.0\n"
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.UIHub.Port != 9000 || c.UIHub.MDRateHz != 15 {
		t.Fatalf("override failed: port=%d rate=%v", c.UIHub.Port, c.UIHub.MDRateHz)
	}
	// Unset fields in a present section still fall back to Default() (Load merges onto Default()).
	if c.UIHub.OutboundQueue != 1024 {
		t.Fatalf("OutboundQueue lost its default: %d", c.UIHub.OutboundQueue)
	}
	if c.Scan.MinChangePct != 8 {
		t.Fatalf("scan override failed: %v", c.Scan.MinChangePct)
	}
}

func TestBackfillDefaultsAndOverride(t *testing.T) {
	// Defaults when absent.
	cfg, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Backfill.Enabled || cfg.Backfill.IntradayDays != 20 ||
		cfg.Backfill.DailyYears != 0 || cfg.Backfill.Concurrency != 3 || cfg.Backfill.SeedChunk != 500 {
		t.Fatalf("backfill defaults = %+v", cfg.Backfill)
	}
	// Overrides parse.
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[backfill]\nenabled = false\nintraday_days = 5\nconcurrency = 8\nseed_chunk = 250\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backfill.Enabled || cfg.Backfill.IntradayDays != 5 || cfg.Backfill.Concurrency != 8 || cfg.Backfill.SeedChunk != 250 {
		t.Fatalf("backfill override = %+v", cfg.Backfill)
	}
}
