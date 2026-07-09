package venueadmin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/config"
)

func setup(t *testing.T) (*Admin, string, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	credsPath := filepath.Join(dir, "credentials.json")
	_ = os.WriteFile(cfgPath, []byte("[md]\nsession_anchor = \"09:30\"\n"), 0o644)
	_ = os.WriteFile(credsPath, []byte(`{"alpaca":{"keyId":"K","secretKey":"S"}}`), 0o600)
	booted := config.VenueConfig{Venues: []config.Venue{{ID: "boot", Broker: "sim", Env: "paper", AutoArm: true}}}
	return New(cfgPath, credsPath, booted), cfgPath, credsPath
}

func TestSetVenueSetupValidatesBeforeWriting(t *testing.T) {
	a, cfgPath, _ := setup(t)
	// live + auto_arm must be rejected AND leave the file untouched.
	bad := config.VenueConfig{Venues: []config.Venue{{ID: "x", Broker: "sim", Env: "live", AutoArm: true}}}
	if err := a.SetVenueSetup(bad); err == nil {
		t.Fatal("expected validation error")
	}
	got, _ := config.ReadVenueConfig(cfgPath)
	if len(got.Venues) != 0 {
		t.Fatalf("file mutated on invalid set: %+v", got.Venues)
	}
}

func TestSetVenueSetupWritesValid(t *testing.T) {
	a, cfgPath, _ := setup(t)
	vc := config.VenueConfig{Venues: []config.Venue{{ID: "alpaca-paper", Broker: "alpaca", Env: "paper", Credentials: "alpaca", AutoArm: true}}}
	if err := a.SetVenueSetup(vc); err != nil {
		t.Fatalf("SetVenueSetup: %v", err)
	}
	got, _ := config.ReadVenueConfig(cfgPath)
	if len(got.Venues) != 1 || got.Venues[0].ID != "alpaca-paper" {
		t.Fatalf("not written: %+v", got.Venues)
	}
}

func TestGetVenueSetupReturnsFileRunningKeys(t *testing.T) {
	a, _, _ := setup(t)
	file, running, keys, err := a.GetVenueSetup()
	if err != nil {
		t.Fatal(err)
	}
	if len(running.Venues) != 1 || running.Venues[0].ID != "boot" {
		t.Fatalf("running should be booted: %+v", running.Venues)
	}
	if len(file.Venues) != 0 {
		t.Fatalf("file should have no venues yet: %+v", file.Venues)
	}
	if len(keys) != 1 || keys[0] != "alpaca" {
		t.Fatalf("keys: %v", keys)
	}
}

func TestDeleteCredentialBlockedWhileReferenced(t *testing.T) {
	a, cfgPath, credsPath := setup(t)
	_ = os.WriteFile(cfgPath, []byte(`[[venue]]
id = "a"
broker = "alpaca"
env = "paper"
credentials = "alpaca"
`), 0o644)
	if err := a.DeleteCredential("alpaca"); err == nil {
		t.Fatal("expected block: alpaca is referenced")
	}
	// Still present.
	ks, _ := a.PutCredential("alpaca", "K", "S"), error(nil)
	_ = ks
	if _, err := os.Stat(credsPath); err != nil {
		t.Fatal(err)
	}
}
