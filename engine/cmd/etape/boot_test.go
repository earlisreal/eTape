package main

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
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

func TestBuildBrokersMoomooAndUnknownError(t *testing.T) {
	if _, err := buildBrokers(config.Config{Venues: []config.Venue{{ID: "mm", Broker: "moomoo"}}}, creds.File{}, clock.System{}, false); err == nil {
		t.Fatal("moomoo venue must error (deferred to v1.x)")
	}
	if _, err := buildBrokers(config.Config{Venues: []config.Venue{{ID: "x", Broker: "bogus"}}}, creds.File{}, clock.System{}, false); err == nil {
		t.Fatal("unknown broker must error")
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
