package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validVC() VenueConfig {
	return VenueConfig{
		Venues: []Venue{
			{ID: "alpaca-paper", Broker: "alpaca", Env: "paper", Credentials: "alpaca", AccountID: "", AutoArm: true},
			{ID: "tz-live", Broker: "tradezero", Env: "live", Credentials: "tradeZero", AccountID: "TZ123", AutoArm: false},
			{ID: "sim", Broker: "sim", Env: "paper", AutoArm: true},
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
		"empty id":                  func(vc *VenueConfig) { vc.Venues[0].ID = "" },
		"bad id chars":              func(vc *VenueConfig) { vc.Venues[0].ID = "Alpaca_Paper" },
		"duplicate id":              func(vc *VenueConfig) { vc.Venues[1].ID = "alpaca-paper" },
		"bad broker":                func(vc *VenueConfig) { vc.Venues[0].Broker = "etrade" },
		"bad env":                   func(vc *VenueConfig) { vc.Venues[0].Env = "demo" },
		"live auto-arm":             func(vc *VenueConfig) { vc.Venues[1].AutoArm = true },
		"missing cred key":          func(vc *VenueConfig) { vc.Venues[0].Credentials = "nope" },
		"tz missing account":        func(vc *VenueConfig) { vc.Venues[1].AccountID = "" },
		"negative gate cap":         func(vc *VenueConfig) { vc.Gate.Global.MaxDayLoss = -1 },
		"gate key unknown id":       func(vc *VenueConfig) { vc.Gate.Venue["ghost"] = GateVenue{} },
		"negative starting balance": func(vc *VenueConfig) { vc.Venues[2].StartingBalance = -1 },
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

func TestEffectiveStartingBalanceHonorsPositiveValue(t *testing.T) {
	v := Venue{ID: "sim", Broker: "sim", StartingBalance: 25_000}
	if got := v.EffectiveStartingBalance(); got != 25_000 {
		t.Fatalf("got %v, want 25000", got)
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
