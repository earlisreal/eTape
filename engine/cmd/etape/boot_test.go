package main

import (
	"context"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/broker/alpaca"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/uihub"
)

func TestBuildGateConfigMapsVenueAndGlobal(t *testing.T) {
	g := config.Gate{
		Global: config.GateGlobal{MaxDayLoss: 500, MaxSymbolPositionValue: 10000, MaxSymbolPositionShares: 5000},
		Venue:  map[string]config.GateVenue{"sim": {MaxOrderValue: 1000, MaxOpenOrders: 10}},
	}
	gc := buildGateConfig(g)
	if gc.Global.MaxDayLoss != 500 || gc.Global.MaxSymbolPositionValue != 10000 {
		t.Fatalf("global map wrong: %+v", gc.Global)
	}
	vl, ok := gc.Venue["sim"]
	if !ok || vl.MaxOrderValue != 1000 || vl.MaxOpenOrders != 10 {
		t.Fatalf("venue map wrong: %+v", gc.Venue)
	}
}

func TestBuildBrokersReplayIsAllSim(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{
		{ID: "tz", Broker: "tradezero"}, {ID: "al", Broker: "alpaca"},
	}}
	vbs, err := buildBrokers(cfg, creds.File{}, clock.System{}, true) // replay => sim regardless of Broker
	if err != nil {
		t.Fatal(err)
	}
	if len(vbs) != 2 {
		t.Fatalf("want 2 venue brokers, got %d", len(vbs))
	}
	for _, vb := range vbs {
		if vb.Run != nil {
			t.Fatalf("replay sim brokers need no Run goroutine: %s", vb.ID)
		}
		if !vb.Broker.Capabilities().FlattenAll { // sim reports FlattenAll=true
			t.Fatalf("expected sim broker for %s", vb.ID)
		}
	}
}

func TestBuildBrokersMoomooRegistersStub(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{{ID: "moomoo", Broker: "moomoo"}}}
	vbs, err := buildBrokers(cfg, creds.File{}, clock.System{}, false)
	if err != nil {
		t.Fatalf("moomoo venue should register a stub, not error: %v", err)
	}
	if len(vbs) != 1 || vbs[0].ID != "moomoo" {
		t.Fatalf("expected one moomoo venue, got %+v", vbs)
	}
	if vbs[0].Run != nil {
		t.Fatal("stub venue has no Run loop")
	}
	if _, err := vbs[0].Broker.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "moomoo"}); err == nil {
		t.Fatal("moomoo stub must reject submits")
	}
}

func TestBuildBrokersUnknownErrors(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{{ID: "x", Broker: "nope"}}}
	if _, err := buildBrokers(cfg, creds.File{}, clock.System{}, false); err == nil {
		t.Fatal("unknown broker must error")
	}
}

func TestAutoArmVenues(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{
		{ID: "alpaca-paper", Broker: "alpaca", AutoArm: true},
		{ID: "alpaca-live", Broker: "alpaca"},
		{ID: "moomoo", Broker: "moomoo", AutoArm: true},
	}}
	got := autoArmVenues(cfg)
	if !got["alpaca-paper"] || !got["moomoo"] {
		t.Fatalf("auto-arm venues missing: %+v", got)
	}
	if got["alpaca-live"] {
		t.Fatalf("live venue must not auto-arm: %+v", got)
	}
}

func TestStartingBalances(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{
		{ID: "sim-1", Broker: "sim", StartingBalance: 25_000},
		{ID: "sim-2", Broker: "sim"},                                 // unset => default
		{ID: "alpaca-paper", Broker: "alpaca", StartingBalance: 999}, // non-sim: ignored
	}}
	got := startingBalances(cfg)
	if got["sim-1"] != 25_000 {
		t.Fatalf("sim-1 starting balance = %v, want 25000", got["sim-1"])
	}
	if got["sim-2"] != config.DefaultSimStartingBalance {
		t.Fatalf("sim-2 starting balance = %v, want default %v", got["sim-2"], config.DefaultSimStartingBalance)
	}
	if _, ok := got["alpaca-paper"]; ok {
		t.Fatal("non-sim venues must not appear in the starting-balance map")
	}
}

func TestBuildBrokersLiveSim(t *testing.T) {
	vbs, err := buildBrokers(config.Config{Venues: []config.Venue{{ID: "sim", Broker: "sim"}}}, creds.File{}, clock.System{}, false)
	if err != nil || len(vbs) != 1 || vbs[0].Run != nil {
		t.Fatalf("live sim venue should build a sim broker with no Run: %v / %+v", err, vbs)
	}
}

func TestVenueMetasMapsConfiguredBrokerAndGate(t *testing.T) {
	cfg := config.Config{
		Venues: []config.Venue{{ID: "sim", Broker: "alpaca"}},
		Gate: config.Gate{Venue: map[string]config.GateVenue{
			"sim": {MaxOrderValue: 1000, MaxPositionValue: 2000, MaxPositionShares: 300, MaxOpenOrders: 10},
		}},
	}
	vms := venueMetas(cfg)
	if len(vms) != 1 {
		t.Fatalf("want 1 venue meta, got %d", len(vms))
	}
	vm := vms[0]
	if vm.ID != "sim" || vm.Broker != "alpaca" {
		t.Fatalf("venue identity wrong: %+v", vm)
	}
	if vm.Gate.MaxOrderValue != 1000 || vm.Gate.MaxPositionValue != 2000 ||
		vm.Gate.MaxPositionShares != 300 || vm.Gate.MaxOpenOrders != 10 {
		t.Fatalf("gate limits wrong: %+v", vm.Gate)
	}
}

func TestVenueMetasMissingGateEntryIsZeroLimits(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{{ID: "unconfigured", Broker: "tradezero"}}}
	vms := venueMetas(cfg)
	if len(vms) != 1 || vms[0].Gate != (uihub.GateLimits{}) {
		t.Fatalf("expected zero-value gate limits for venue w/o gate config: %+v", vms)
	}
}

func TestBuildBrokersLiveMissingCredsErrors(t *testing.T) {
	// When replay=false with a tradezero/alpaca venue but empty creds.File,
	// buildBrokers should return an error (no partial broker slice).
	cfg := config.Config{Venues: []config.Venue{
		{ID: "tz", Broker: "tradezero", Credentials: "mykey", AccountID: "acct1"},
	}}
	vbs, err := buildBrokers(cfg, creds.File{}, clock.System{}, false)
	if err == nil {
		t.Fatal("expected error for missing tradezero credentials")
	}
	if len(vbs) != 0 {
		t.Fatalf("expected empty broker slice on error, got %d", len(vbs))
	}

	// Same test for alpaca.
	cfg = config.Config{Venues: []config.Venue{
		{ID: "al", Broker: "alpaca", Credentials: "otherkey", Env: "paper"},
	}}
	vbs, err = buildBrokers(cfg, creds.File{}, clock.System{}, false)
	if err == nil {
		t.Fatal("expected error for missing alpaca credentials")
	}
	if len(vbs) != 0 {
		t.Fatalf("expected empty broker slice on error, got %d", len(vbs))
	}
}

func TestBuildBrokersLiveTradezeroAndAlpacaBindRun(t *testing.T) {
	// When replay=false with tradezero/alpaca venues and valid creds,
	// buildBrokers should construct real adapters with Run bound (not nil).
	cr := creds.File{
		"tz_creds": {KeyID: "tzkey", SecretKey: "tzsecret"},
		"al_creds": {KeyID: "alkey", SecretKey: "alsecret"},
	}
	cfg := config.Config{Venues: []config.Venue{
		{ID: "tz", Broker: "tradezero", Credentials: "tz_creds", AccountID: "acct1"},
		{ID: "al", Broker: "alpaca", Credentials: "al_creds", Env: "paper"},
	}}
	vbs, err := buildBrokers(cfg, cr, clock.System{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vbs) != 2 {
		t.Fatalf("expected 2 brokers, got %d", len(vbs))
	}
	// Verify first broker (tradezero) has Run bound.
	if vbs[0].ID != "tz" || vbs[0].Broker == nil || vbs[0].Run == nil {
		t.Fatalf("tradezero broker not properly constructed: ID=%s, Broker=%v, Run=nil", vbs[0].ID, vbs[0].Broker)
	}
	// Verify second broker (alpaca) has Run bound.
	if vbs[1].ID != "al" || vbs[1].Broker == nil || vbs[1].Run == nil {
		t.Fatalf("alpaca broker not properly constructed: ID=%s, Broker=%v, Run=nil", vbs[1].ID, vbs[1].Broker)
	}
}

// TestFirstAlpacaProberFindsAlpacaAdapter verifies firstAlpacaProber picks out
// the Alpaca adapter (which implements ProbeRTT) from a mixed venue list, for
// wiring into health.New's alpaca-link probe.
func TestFirstAlpacaProberFindsAlpacaAdapter(t *testing.T) {
	cr := creds.File{
		"tz_creds": {KeyID: "tzkey", SecretKey: "tzsecret"},
		"al_creds": {KeyID: "alkey", SecretKey: "alsecret"},
	}
	cfg := config.Config{Venues: []config.Venue{
		{ID: "tz", Broker: "tradezero", Credentials: "tz_creds", AccountID: "acct1"},
		{ID: "al", Broker: "alpaca", Credentials: "al_creds", Env: "paper"},
	}}
	vbs, err := buildBrokers(cfg, cr, clock.System{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p := firstAlpacaProber(vbs)
	if p == nil {
		t.Fatal("expected a non-nil prober when an alpaca venue is configured")
	}
	if _, ok := p.(*alpaca.Adapter); !ok {
		t.Fatalf("expected the alpaca.Adapter itself, got %T", p)
	}
}

// TestFirstAlpacaProberNilWithoutAlpaca verifies no alpaca venue configured
// means no prober — the engine-alpaca link must not appear at all (not just
// down) when nothing exists to probe.
func TestFirstAlpacaProberNilWithoutAlpaca(t *testing.T) {
	cr := creds.File{"tz_creds": {KeyID: "tzkey", SecretKey: "tzsecret"}}
	cfg := config.Config{Venues: []config.Venue{
		{ID: "tz", Broker: "tradezero", Credentials: "tz_creds", AccountID: "acct1"},
		{ID: "sim", Broker: "sim"},
	}}
	vbs, err := buildBrokers(cfg, cr, clock.System{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p := firstAlpacaProber(vbs); p != nil {
		t.Fatalf("expected nil prober with no alpaca venue, got %T", p)
	}
}
