package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/alpaca"
	"github.com/earlisreal/eTape/engine/internal/broker/moomoo"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
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

// TestBuildBrokersMoomooConstructsAdapter verifies a moomoo venue with a
// valid numeric account_id builds a real *moomoo.Adapter with Run bound —
// the stub venue (reject-all, no Run) this replaces is gone; moomoo is no
// longer deferred to v1.x. Env is "live" — moomoo is a live-only venue now
// (see TestBuildBrokersMoomooPaperEnvErrors).
func TestBuildBrokersMoomooConstructsAdapter(t *testing.T) {
	cfg := config.Config{
		OpenD:  config.OpenD{Host: "127.0.0.1", Port: 11111},
		Venues: []config.Venue{{ID: "moomoo", Broker: "moomoo", AccountID: "123456", Env: "live"}},
	}
	vbs, err := buildBrokers(cfg, creds.File{}, clock.System{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vbs) != 1 || vbs[0].ID != "moomoo" {
		t.Fatalf("expected one moomoo venue, got %+v", vbs)
	}
	if vbs[0].Run == nil {
		t.Fatal("moomoo adapter must bind Run")
	}
	if _, ok := vbs[0].Broker.(*moomoo.Adapter); !ok {
		t.Fatalf("expected *moomoo.Adapter, got %T", vbs[0].Broker)
	}
}

// TestBuildBrokersMoomooNonNumericAccountIDErrors verifies buildBrokers
// defensively re-validates account_id at boot time (ValidateVenueConfig
// already rejects a non-numeric account_id when the settings UI writes
// config.toml, but a hand-edited file can skip that path entirely). Env is
// "live" so this exercises the account_id check specifically, not the
// live-only env check covered by TestBuildBrokersMoomooPaperEnvErrors.
func TestBuildBrokersMoomooNonNumericAccountIDErrors(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{{ID: "moomoo", Broker: "moomoo", AccountID: "not-a-number", Env: "live"}}}
	vbs, err := buildBrokers(cfg, creds.File{}, clock.System{}, false)
	if err == nil {
		t.Fatal("expected error for non-numeric moomoo account_id")
	}
	if len(vbs) != 0 {
		t.Fatalf("expected empty broker slice on error, got %d", len(vbs))
	}
}

// TestBuildBrokersMoomooPaperEnvErrors verifies buildBrokers fails loud on a
// hand-edited config.toml with a moomoo venue set to env = "paper" instead of
// silently relying on the moomoo adapter's own defense-in-depth
// simulate-default (env != "live" => TrdEnv_Simulate) — boot does not run
// configs through ValidateVenueConfig, so this is the only guard for that
// path. A real-money broker's env must never be silently coerced.
func TestBuildBrokersMoomooPaperEnvErrors(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{{ID: "moomoo", Broker: "moomoo", AccountID: "123456", Env: "paper"}}}
	vbs, err := buildBrokers(cfg, creds.File{}, clock.System{}, false)
	if err == nil {
		t.Fatal("expected error for moomoo venue with env: paper")
	}
	if !strings.Contains(err.Error(), "live-only") {
		t.Fatalf("expected live-only error, got: %v", err)
	}
	if len(vbs) != 0 {
		t.Fatalf("expected empty broker slice on error, got %d", len(vbs))
	}
}

func TestBuildBrokersUnknownErrors(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{{ID: "x", Broker: "nope"}}}
	if _, err := buildBrokers(cfg, creds.File{}, clock.System{}, false); err == nil {
		t.Fatal("unknown broker must error")
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

// TestBuildBrokersSimSeedsConfiguredStartingBalance guards against the
// boot-balance regression: buildBrokers used to construct every sim broker
// unfunded (New took no starting cash), so a fresh boot always reconciled
// $0 equity/buying power into Core.State regardless of starting_balance —
// only the separate, manually-triggered ResetBalance command ever funded the
// account. Covers both an explicit config value and the unset/default case,
// and both the "sim" broker branch and the replay-forces-sim branch.
func TestBuildBrokersSimSeedsConfiguredStartingBalance(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{
		{ID: "sim-1", Broker: "sim", StartingBalance: 25_000},
		{ID: "sim-2", Broker: "sim"}, // unset => default
	}}
	for _, replay := range []bool{false, true} {
		vbs, err := buildBrokers(cfg, creds.File{}, clock.System{}, replay)
		if err != nil {
			t.Fatalf("replay=%v: %v", replay, err)
		}
		want := map[exec.VenueID]float64{"sim-1": 25_000, "sim-2": config.DefaultSimStartingBalance}
		for _, vb := range vbs {
			acct, _, _, err := vb.Broker.Snapshot(context.Background())
			if err != nil {
				t.Fatalf("replay=%v: snapshot %s: %v", replay, vb.ID, err)
			}
			if acct.Equity != want[vb.ID] {
				t.Fatalf("replay=%v: %s equity = %v, want %v", replay, vb.ID, acct.Equity, want[vb.ID])
			}
		}
	}
}

// TestBuildBrokersSimAppliesConfiguredSlippage is Task 4's boot-wiring guard,
// the SlippageBps analog of TestBuildBrokersSimSeedsConfiguredStartingBalance
// above: a venue's configured slippage_bps must actually reach the
// constructed sim broker via sim.Options, not just default to off. Verified
// indirectly through a fill's price (Broker exposes no slippage getter), for
// both the "sim" broker branch and the replay-forces-sim branch.
func TestBuildBrokersSimAppliesConfiguredSlippage(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{{ID: "sim", Broker: "sim", SlippageBps: 100}}} // 1%
	for _, replay := range []bool{false, true} {
		vbs, err := buildBrokers(cfg, creds.File{}, clock.System{}, replay)
		if err != nil {
			t.Fatalf("replay=%v: %v", replay, err)
		}
		b := vbs[0].Broker
		sink, ok := b.(simSink)
		if !ok {
			t.Fatalf("replay=%v: sim broker should implement simSink (SetBook)", replay)
		}
		sink.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
		if _, err := b.SubmitOrder(context.Background(), exec.OrderRequest{
			Venue: "sim", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1",
		}); err != nil {
			t.Fatalf("replay=%v: submit: %v", replay, err)
		}
		var filled bool
	drain:
		for {
			select {
			case ev := <-b.Events():
				if f, ok := ev.(exec.OrderFilled); ok {
					filled = true
					if f.F.Price <= 100 {
						t.Fatalf("replay=%v: fill price %v should reflect the configured 100bps slippage (must be > raw ask 100)", replay, f.F.Price)
					}
				}
			case <-time.After(100 * time.Millisecond):
				break drain
			}
		}
		if !filled {
			t.Fatalf("replay=%v: expected a fill", replay)
		}
	}
}

// TestBuildBrokersSimAppliesConfiguredFillLatency is Task 5's boot-wiring
// guard, the FillLatencyMs analog of TestBuildBrokersSimAppliesConfiguredSlippage
// above: a venue's configured fill_latency_ms must actually reach the
// constructed sim broker via sim.Options, not just default to off. Uses a
// fake clock (clock.NewFake, advanced explicitly) rather than a wall-clock
// sleep to prove the fill is genuinely deferred by event time, for both the
// "sim" broker branch and the replay-forces-sim branch.
func TestBuildBrokersSimAppliesConfiguredFillLatency(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{{ID: "sim", Broker: "sim", FillLatencyMs: 500}}}
	for _, replay := range []bool{false, true} {
		clk := clock.NewFake(time.UnixMilli(1000))
		vbs, err := buildBrokers(cfg, creds.File{}, clk, replay)
		if err != nil {
			t.Fatalf("replay=%v: %v", replay, err)
		}
		b := vbs[0].Broker
		sink, ok := b.(simSink)
		if !ok {
			t.Fatalf("replay=%v: sim broker should implement simSink (SetBook)", replay)
		}
		sink.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
		if _, err := b.SubmitOrder(context.Background(), exec.OrderRequest{
			Venue: "sim", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1",
		}); err != nil {
			t.Fatalf("replay=%v: submit: %v", replay, err)
		}
		select {
		case ev := <-b.Events():
			if _, ok := ev.(exec.OrderAccepted); !ok {
				t.Fatalf("replay=%v: expected only OrderAccepted before the latency deadline, got %+v", replay, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("replay=%v: timed out waiting for OrderAccepted", replay)
		}
		select {
		case ev := <-b.Events():
			t.Fatalf("replay=%v: order should not fill before its 500ms eligibility deadline, got %+v", replay, ev)
		case <-time.After(50 * time.Millisecond):
		}

		clk.Advance(500 * time.Millisecond)
		sink.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
		select {
		case ev := <-b.Events():
			if f, ok := ev.(exec.OrderFilled); !ok || f.F.Qty != 10 {
				t.Fatalf("replay=%v: expected a full fill once eligible, got %+v", replay, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("replay=%v: timed out waiting for the deferred fill", replay)
		}
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

// TestResolveBackfillAlpacaCredsExplicitKeyWins verifies an explicit, resolvable
// backfill.alpaca.creds_key is used as-is, even when a configured alpaca venue
// (with different credentials) would also resolve — the explicit override
// wins over venue auto-resolution.
func TestResolveBackfillAlpacaCredsExplicitKeyWins(t *testing.T) {
	cr := creds.File{
		"explicit-key": {KeyID: "EK", SecretKey: "ES"},
		"key-a48b723d": {KeyID: "VK", SecretKey: "VS"},
	}
	cfg := config.Config{
		Backfill: config.Backfill{Alpaca: config.BackfillAlpaca{CredsKey: "explicit-key"}},
		Venues:   []config.Venue{{ID: "alpaca-paper", Broker: "alpaca", Env: "paper", Credentials: "key-a48b723d"}},
	}
	p, label, err := resolveBackfillAlpacaCreds(cfg, cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.KeyID != "EK" || p.SecretKey != "ES" {
		t.Fatalf("expected explicit creds pair, got %+v", p)
	}
	if !strings.Contains(label, "explicit-key") {
		t.Fatalf("label = %q, want it to mention the explicit key", label)
	}
}

// TestResolveBackfillAlpacaCredsStaleKeyFallsBackToVenue verifies a stale or
// empty creds_key (the credentials-store redesign regenerates key names, so a
// literal like "alpaca" can drift out of sync) falls through to the first
// non-live alpaca venue whose credentials do resolve — the production bug
// this task fixes.
func TestResolveBackfillAlpacaCredsStaleKeyFallsBackToVenue(t *testing.T) {
	cr := creds.File{"key-a48b723d": {KeyID: "VK", SecretKey: "VS"}}
	for _, staleKey := range []string{"alpaca", ""} {
		cfg := config.Config{
			Backfill: config.Backfill{Alpaca: config.BackfillAlpaca{CredsKey: staleKey}},
			Venues: []config.Venue{
				{ID: "alpaca-paper", Broker: "alpaca", Env: "paper", Credentials: "key-a48b723d"},
			},
		}
		p, label, err := resolveBackfillAlpacaCreds(cfg, cr)
		if err != nil {
			t.Fatalf("creds_key=%q: unexpected error: %v", staleKey, err)
		}
		if p.KeyID != "VK" || p.SecretKey != "VS" {
			t.Fatalf("creds_key=%q: expected venue creds pair, got %+v", staleKey, p)
		}
		if !strings.Contains(label, "alpaca-paper") {
			t.Fatalf("creds_key=%q: label = %q, want it to mention the resolved venue", staleKey, label)
		}
	}
}

// TestResolveBackfillAlpacaCredsRefusesAlpacaLive verifies an explicit
// creds_key of "alpaca-live" is refused outright (preserving the existing
// live-key guard) and does NOT fall through to auto-resolving a different,
// non-live alpaca venue — explicitly naming the live key must never result in
// a silent substitution.
func TestResolveBackfillAlpacaCredsRefusesAlpacaLive(t *testing.T) {
	cr := creds.File{
		"alpaca-live":  {KeyID: "LK", SecretKey: "LS"},
		"key-a48b723d": {KeyID: "VK", SecretKey: "VS"},
	}
	cfg := config.Config{
		Backfill: config.Backfill{Alpaca: config.BackfillAlpaca{CredsKey: "alpaca-live"}},
		Venues: []config.Venue{
			{ID: "alpaca-paper", Broker: "alpaca", Env: "paper", Credentials: "key-a48b723d"},
		},
	}
	_, _, err := resolveBackfillAlpacaCreds(cfg, cr)
	if err == nil {
		t.Fatal("expected an error refusing the alpaca-live creds key")
	}
	if !errors.Is(err, errAlpacaLiveCreds) {
		t.Fatalf("expected errAlpacaLiveCreds sentinel, got %v", err)
	}
}

// TestResolveBackfillAlpacaCredsErrorsWhenOnlyLiveVenue verifies that when no
// explicit creds_key is set and every configured alpaca venue is live, the
// helper errors rather than ever selecting a live venue's credentials for the
// read-only backfill fallback.
func TestResolveBackfillAlpacaCredsErrorsWhenOnlyLiveVenue(t *testing.T) {
	cr := creds.File{"live-creds": {KeyID: "LK", SecretKey: "LS"}}
	cfg := config.Config{
		Venues: []config.Venue{{ID: "alpaca-live-venue", Broker: "alpaca", Env: "live", Credentials: "live-creds"}},
	}
	if _, _, err := resolveBackfillAlpacaCreds(cfg, cr); err == nil {
		t.Fatal("expected an error when only a live alpaca venue is configured")
	}
}

// TestResolveBackfillAlpacaCredsErrorsWhenNothingResolves verifies the no-op
// case: no explicit key, no alpaca venues at all.
func TestResolveBackfillAlpacaCredsErrorsWhenNothingResolves(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{{ID: "tz", Broker: "tradezero", Credentials: "tz_creds"}}}
	if _, _, err := resolveBackfillAlpacaCreds(cfg, creds.File{}); err == nil {
		t.Fatal("expected an error when nothing resolves")
	}
}

// TestLiveMoomooDayLossGap verifies the pure predicate backing the DEC3
// boot-time warning: moomoo/trd.go's snapshot() hardcodes AccountSnapshot.
// DayPnL to 0 (Trd_GetFunds has no day-P&L field), so the global MaxDayLoss
// circuit breaker (exec/gate.go's BreachedDayLoss, which sums every venue's
// DayPnL) is blind to moomoo-originated losses whenever a moomoo venue is
// configured alongside a non-zero MaxDayLoss. The predicate must require
// BOTH conditions — either alone is not a gap.
func TestLiveMoomooDayLossGap(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{
			name: "moomoo venue and MaxDayLoss>0",
			cfg: config.Config{
				Gate:   config.Gate{Global: config.GateGlobal{MaxDayLoss: 500}},
				Venues: []config.Venue{{ID: "moomoo", Broker: "moomoo", AccountID: "123456", Env: "live"}},
			},
			want: true,
		},
		{
			name: "moomoo venue but MaxDayLoss==0",
			cfg: config.Config{
				Gate:   config.Gate{Global: config.GateGlobal{MaxDayLoss: 0}},
				Venues: []config.Venue{{ID: "moomoo", Broker: "moomoo", AccountID: "123456", Env: "live"}},
			},
			want: false,
		},
		{
			name: "no moomoo venue but MaxDayLoss>0",
			cfg: config.Config{
				Gate:   config.Gate{Global: config.GateGlobal{MaxDayLoss: 500}},
				Venues: []config.Venue{{ID: "tz", Broker: "tradezero"}},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := liveMoomooDayLossGap(tt.cfg); got != tt.want {
				t.Fatalf("liveMoomooDayLossGap() = %v, want %v", got, tt.want)
			}
		})
	}
}
