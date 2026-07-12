package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validVC() VenueConfig {
	return VenueConfig{
		Venues: []Venue{
			{ID: "alpaca-paper", Broker: "alpaca", Env: "paper", Credentials: "alpaca", AccountID: ""},
			{ID: "tz-live", Broker: "tradezero", Env: "live", Credentials: "tradeZero", AccountID: "TZ123"},
			{ID: "sim", Broker: "sim", Env: "paper", SlippageBps: 7.5, FillLatencyMs: 250},
		},
		Gate: Gate{
			Global: GateGlobal{MaxDayLoss: 1000},
			Venue:  map[string]GateVenue{"alpaca-paper": {MaxOrderValue: 5000, MaxOpenOrders: 3}},
		},
	}
}

func TestValidateVenueConfigAccepts(t *testing.T) {
	if err := ValidateVenueConfig(validVC(), []string{"alpaca", "tradeZero"}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateVenueConfigRejects(t *testing.T) {
	keys := []string{"alpaca", "tradeZero"}
	cases := map[string]func(vc *VenueConfig){
		"empty id":           func(vc *VenueConfig) { vc.Venues[0].ID = "" },
		"bad id chars":       func(vc *VenueConfig) { vc.Venues[0].ID = "Alpaca_Paper" },
		"duplicate id":       func(vc *VenueConfig) { vc.Venues[1].ID = "alpaca-paper" },
		"bad broker":         func(vc *VenueConfig) { vc.Venues[0].Broker = "etrade" },
		"bad env":            func(vc *VenueConfig) { vc.Venues[0].Env = "demo" },
		"missing cred key":   func(vc *VenueConfig) { vc.Venues[0].Credentials = "nope" },
		"tz missing account": func(vc *VenueConfig) { vc.Venues[1].AccountID = "" },
		"moomoo missing account": func(vc *VenueConfig) {
			vc.Venues = append(vc.Venues, Venue{ID: "moomoo-live", Broker: "moomoo", Env: "live", AccountID: ""})
		},
		"moomoo non-numeric account": func(vc *VenueConfig) {
			vc.Venues = append(vc.Venues, Venue{ID: "moomoo-live", Broker: "moomoo", Env: "live", AccountID: "not-a-number"})
		},
		"moomoo paper env": func(vc *VenueConfig) {
			vc.Venues = append(vc.Venues, Venue{ID: "moomoo-paper", Broker: "moomoo", Env: "paper", AccountID: "12345678"})
		},
		"negative gate cap":         func(vc *VenueConfig) { vc.Gate.Global.MaxDayLoss = -1 },
		"gate key unknown id":       func(vc *VenueConfig) { vc.Gate.Venue["ghost"] = GateVenue{} },
		"negative starting balance": func(vc *VenueConfig) { vc.Venues[2].StartingBalance = -1 },
		"negative slippage bps":     func(vc *VenueConfig) { vc.Venues[2].SlippageBps = -1 },
		"negative fill latency ms":  func(vc *VenueConfig) { vc.Venues[2].FillLatencyMs = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			vc := validVC()
			mutate(&vc)
			if err := ValidateVenueConfig(vc, keys); err == nil {
				t.Fatalf("expected rejection for %q", name)
			}
		})
	}
}

func TestEffectiveStartingBalanceDefaultsWhenUnsetOrZero(t *testing.T) {
	for _, sb := range []float64{0, -5} {
		v := Venue{ID: "sim", Broker: "sim", StartingBalance: sb}
		if got := v.EffectiveStartingBalance(); got != DefaultSimStartingBalance {
			t.Fatalf("StartingBalance=%v: got %v, want default %v", sb, got, DefaultSimStartingBalance)
		}
	}
}

// TestValidateVenueConfigAcceptsMoomooNumericAccountID covers the moomoo
// account_id branch added alongside the tradezero one (ValidateVenueConfig):
// a moomoo venue whose account_id parses as a numeric string must pass,
// mirroring the tradezero "tz missing account" reject-case coverage above
// with the accept-side moomoo case.
func TestValidateVenueConfigAcceptsMoomooNumericAccountID(t *testing.T) {
	vc := validVC()
	vc.Venues = append(vc.Venues, Venue{ID: "moomoo-live", Broker: "moomoo", Env: "live", AccountID: "12345678"})
	if err := ValidateVenueConfig(vc, []string{"alpaca", "tradeZero"}); err != nil {
		t.Fatalf("valid moomoo venue rejected: %v", err)
	}
}

func TestValidateVenueConfigAcceptsPositiveSlippageBps(t *testing.T) {
	vc := validVC()
	vc.Venues[2].SlippageBps = 5
	if err := ValidateVenueConfig(vc, []string{"alpaca", "tradeZero"}); err != nil {
		t.Fatalf("positive slippage_bps rejected: %v", err)
	}
}

func TestValidateVenueConfigAcceptsPositiveFillLatencyMs(t *testing.T) {
	vc := validVC()
	vc.Venues[2].FillLatencyMs = 250
	if err := ValidateVenueConfig(vc, []string{"alpaca", "tradeZero"}); err != nil {
		t.Fatalf("positive fill_latency_ms rejected: %v", err)
	}
}

func TestEffectiveStartingBalanceHonorsPositiveValue(t *testing.T) {
	v := Venue{ID: "sim", Broker: "sim", StartingBalance: 25_000}
	if got := v.EffectiveStartingBalance(); got != 25_000 {
		t.Fatalf("got %v, want 25000", got)
	}
}

func TestSeedConfigRoundTripThroughWriteVenueConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	// Create a config file with the [seed] marker and an initial venue.
	original := `[md]
session_anchor = "09:30"

[seed]
moomoo_attempted = true

[[venue]]
id = "old"
broker = "sim"
env = "paper"
`
	if err := os.WriteFile(p, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify that the marker is present before WriteVenueConfig.
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load before WriteVenueConfig: %v", err)
	}
	if !cfg.Seed.MoomooAttempted {
		t.Fatalf("marker not loaded: got %v, want true", cfg.Seed.MoomooAttempted)
	}

	// Call WriteVenueConfig with a different venue set (simulating a settings-UI save).
	vc := validVC()
	if err := WriteVenueConfig(p, vc); err != nil {
		t.Fatalf("WriteVenueConfig: %v", err)
	}

	// Re-load and verify the marker survived the round-trip.
	cfg, err = Load(p)
	if err != nil {
		t.Fatalf("Load after WriteVenueConfig: %v", err)
	}
	if !cfg.Seed.MoomooAttempted {
		t.Fatalf("marker lost after WriteVenueConfig: got %v, want true", cfg.Seed.MoomooAttempted)
	}

	// Also verify the new venues took effect.
	if len(cfg.Venues) != 3 || cfg.Venues[1].ID != "tz-live" {
		t.Fatalf("venues not updated: %+v", cfg.Venues)
	}
}

func TestSeedConfigZeroValueDefault(t *testing.T) {
	// Test that Default() has the marker false.
	cfg := Default()
	if cfg.Seed.MoomooAttempted {
		t.Fatalf("Default() marker should be false, got %v", cfg.Seed.MoomooAttempted)
	}

	// Test that a config file with no [seed] table loads with false.
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	content := `[md]
session_anchor = "09:30"

[[venue]]
id = "sim"
broker = "sim"
env = "paper"
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Seed.MoomooAttempted {
		t.Fatalf("config with no [seed] table should have marker false, got %v", cfg.Seed.MoomooAttempted)
	}
}

func TestWriteVenueConfigRoundTripAndBak(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	original := `# hand-written, keep me
[md]
session_anchor = "09:30"

[[venue]]
id = "old"
broker = "sim"
env = "paper"
`
	if err := os.WriteFile(p, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	vc := validVC()
	if err := WriteVenueConfig(p, vc); err != nil {
		t.Fatalf("WriteVenueConfig: %v", err)
	}

	// .bak holds the ORIGINAL bytes (comments intact).
	bak, err := os.ReadFile(p + ".bak")
	if err != nil || string(bak) != original {
		t.Fatalf(".bak not the original: err=%v\n%s", err, bak)
	}

	// Reloading the rewritten file yields the venues+gate we set, and the
	// non-venue section value survives.
	got, err := ReadVenueConfig(p)
	if err != nil {
		t.Fatalf("ReadVenueConfig: %v", err)
	}
	if len(got.Venues) != 3 || got.Venues[1].ID != "tz-live" || got.Venues[1].AccountID != "TZ123" {
		t.Fatalf("venues not round-tripped: %+v", got.Venues)
	}
	// The sim realism knobs (Task 4/5) round-trip through the TOML struct
	// tags exactly like StartingBalance — this is the layer BELOW the
	// uihub wire-mapping bug (venueToWire/venueConfigFromWire), which is
	// covered separately in internal/uihub/commands_test.go.
	if got.Venues[2].SlippageBps != 7.5 || got.Venues[2].FillLatencyMs != 250 {
		t.Fatalf("sim realism knobs not round-tripped: %+v", got.Venues[2])
	}
	if got.Gate.Venue["alpaca-paper"].MaxOrderValue != 5000 {
		t.Fatalf("gate not round-tripped: %+v", got.Gate)
	}
	full, err := Load(p)
	if err != nil || full.MD.SessionAnchor != "09:30" {
		t.Fatalf("non-venue section lost: anchor=%q err=%v", full.MD.SessionAnchor, err)
	}

	// Second write does NOT overwrite .bak.
	vc.Venues = vc.Venues[:1]
	if err := WriteVenueConfig(p, vc); err != nil {
		t.Fatal(err)
	}
	bak2, _ := os.ReadFile(p + ".bak")
	if string(bak2) != original {
		t.Fatalf(".bak overwritten on second save")
	}
}
