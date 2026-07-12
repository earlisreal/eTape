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
	booted := config.VenueConfig{Venues: []config.Venue{{ID: "boot", Broker: "sim", Env: "paper"}}}
	return New(cfgPath, credsPath, booted), cfgPath, credsPath
}

func TestSetVenueSetupValidatesBeforeWriting(t *testing.T) {
	a, cfgPath, _ := setup(t)
	// a bad env must be rejected AND leave the file untouched.
	bad := config.VenueConfig{Venues: []config.Venue{{ID: "x", Broker: "sim", Env: "demo"}}}
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
	vc := config.VenueConfig{Venues: []config.Venue{{ID: "alpaca-paper", Broker: "alpaca", Env: "paper", Credentials: "alpaca"}}}
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

func TestMoomooSeedStateFreshFile(t *testing.T) {
	a, _, _ := setup(t)
	attempted, venueExists, err := a.MoomooSeedState()
	if err != nil {
		t.Fatal(err)
	}
	if attempted || venueExists {
		t.Fatalf("fresh file: attempted=%v venueExists=%v, want false/false", attempted, venueExists)
	}
}

func TestMoomooSeedStateReportsExistingMoomooVenue(t *testing.T) {
	a, cfgPath, _ := setup(t)
	_ = os.WriteFile(cfgPath, []byte(`[[venue]]
id = "hand-added"
broker = "moomoo"
env = "live"
account_id = "555"
`), 0o644)
	attempted, venueExists, err := a.MoomooSeedState()
	if err != nil {
		t.Fatal(err)
	}
	if attempted {
		t.Fatalf("attempted should still be false: marker untouched by hand-added venue")
	}
	if !venueExists {
		t.Fatalf("venueExists should be true: a moomoo venue is present")
	}
}

func TestMarkMoomooSeedAttemptedSetsMarkerOnly(t *testing.T) {
	a, cfgPath, _ := setup(t)
	if err := a.MarkMoomooSeedAttempted(); err != nil {
		t.Fatalf("MarkMoomooSeedAttempted: %v", err)
	}
	attempted, venueExists, err := a.MoomooSeedState()
	if err != nil {
		t.Fatal(err)
	}
	if !attempted {
		t.Fatal("marker not set")
	}
	if venueExists {
		t.Fatal("no venue should have been added")
	}
	got, _ := config.ReadVenueConfig(cfgPath)
	if len(got.Venues) != 0 {
		t.Fatalf("venues should be untouched: %+v", got.Venues)
	}
}

func TestMarkMoomooSeedAttemptedIsIdempotent(t *testing.T) {
	a, cfgPath, _ := setup(t)
	if err := a.MarkMoomooSeedAttempted(); err != nil {
		t.Fatalf("1st MarkMoomooSeedAttempted: %v", err)
	}
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.MarkMoomooSeedAttempted(); err != nil {
		t.Fatalf("2nd MarkMoomooSeedAttempted: %v", err)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("second call should be a no-op write:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestSeedMoomooVenueCreatesVenueAndMarker(t *testing.T) {
	a, cfgPath, _ := setup(t)
	created, err := a.SeedMoomooVenue(123456)
	if err != nil {
		t.Fatalf("SeedMoomooVenue: %v", err)
	}
	if !created {
		t.Fatal("expected created=true on first seed")
	}
	got, err := config.ReadVenueConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Venues) != 1 {
		t.Fatalf("expected 1 venue, got %+v", got.Venues)
	}
	v := got.Venues[0]
	if v.ID != "moomoo" || v.Broker != "moomoo" || v.Env != "live" || v.AccountID != "123456" {
		t.Fatalf("seeded venue wrong: %+v", v)
	}
	attempted, _, err := a.MoomooSeedState()
	if err != nil {
		t.Fatal(err)
	}
	if !attempted {
		t.Fatal("marker not set after seed")
	}
}

func TestSeedMoomooVenueSkipsWhenMarkerAlreadySet(t *testing.T) {
	a, cfgPath, _ := setup(t)
	if err := a.MarkMoomooSeedAttempted(); err != nil {
		t.Fatal(err)
	}
	created, err := a.SeedMoomooVenue(999)
	if err != nil {
		t.Fatalf("SeedMoomooVenue: %v", err)
	}
	if created {
		t.Fatal("expected created=false when marker already set")
	}
	got, _ := config.ReadVenueConfig(cfgPath)
	if len(got.Venues) != 0 {
		t.Fatalf("no venue should be written: %+v", got.Venues)
	}
}

func TestSeedMoomooVenueSkipsWhenMoomooVenueAlreadyExists(t *testing.T) {
	a, cfgPath, _ := setup(t)
	_ = os.WriteFile(cfgPath, []byte(`[[venue]]
id = "hand-added"
broker = "moomoo"
env = "live"
account_id = "555"
`), 0o644)
	created, err := a.SeedMoomooVenue(999)
	if err != nil {
		t.Fatalf("SeedMoomooVenue: %v", err)
	}
	if created {
		t.Fatal("expected created=false when a moomoo venue already exists")
	}
	got, err := config.ReadVenueConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Venues) != 1 || got.Venues[0].ID != "hand-added" {
		t.Fatalf("existing venue must be untouched, no duplicate added: %+v", got.Venues)
	}
	attempted, _, err := a.MoomooSeedState()
	if err != nil {
		t.Fatal(err)
	}
	if !attempted {
		t.Fatal("marker should be set so the hand-added venue's later removal also sticks")
	}
}

func TestSeedMoomooVenueFailsValidationWritesNothing(t *testing.T) {
	a, cfgPath, _ := setup(t)
	// A non-moomoo venue already holds id "moomoo" -> the seeded venue would
	// collide (duplicate id), so ValidateVenueConfig must reject it.
	_ = os.WriteFile(cfgPath, []byte(`[[venue]]
id = "moomoo"
broker = "sim"
env = "paper"
`), 0o644)
	created, err := a.SeedMoomooVenue(123456)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if created {
		t.Fatal("expected created=false on validation failure")
	}
	got, err := config.ReadVenueConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Venues) != 1 || got.Venues[0].Broker != "sim" {
		t.Fatalf("file must be untouched: %+v", got.Venues)
	}
	attempted, _, err := a.MoomooSeedState()
	if err != nil {
		t.Fatal(err)
	}
	if attempted {
		t.Fatal("marker must NOT be set on validation failure")
	}
}
