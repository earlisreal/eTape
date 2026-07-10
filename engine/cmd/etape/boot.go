package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/alpaca"
	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/broker/stub"
	"github.com/earlisreal/eTape/engine/internal/broker/tradezero"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	getglobalstate "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/getglobalstate"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"google.golang.org/protobuf/proto"
)

func buildGateConfig(g config.Gate) exec.GateConfig {
	vc := map[exec.VenueID]exec.VenueLimits{}
	for id, v := range g.Venue {
		vc[exec.VenueID(id)] = exec.VenueLimits{
			MaxOrderValue: v.MaxOrderValue, MaxPositionValue: v.MaxPositionValue,
			MaxPositionShares: v.MaxPositionShares, MaxOpenOrders: v.MaxOpenOrders,
		}
	}
	return exec.GateConfig{
		Global: exec.GlobalLimits{
			MaxDayLoss: g.Global.MaxDayLoss, MaxSymbolPositionValue: g.Global.MaxSymbolPositionValue,
			MaxSymbolPositionShares: g.Global.MaxSymbolPositionShares,
		},
		Venue: vc,
	}
}

func venueMetas(cfg config.Config) []uihub.VenueMeta {
	out := make([]uihub.VenueMeta, 0, len(cfg.Venues))
	for _, v := range cfg.Venues {
		gv := cfg.Gate.Venue[v.ID]
		note := ""
		if v.Broker == "moomoo" {
			note = "execution v1.x"
		}
		out = append(out, uihub.VenueMeta{
			ID: v.ID, Broker: v.Broker, Note: note,
			Gate: uihub.GateLimits{
				MaxOrderValue: gv.MaxOrderValue, MaxPositionValue: gv.MaxPositionValue,
				MaxPositionShares: gv.MaxPositionShares, MaxOpenOrders: gv.MaxOpenOrders,
			},
		})
	}
	return out
}

// startingBalances maps venue id -> the resolved starting balance for every
// sim venue (config.Venue.EffectiveStartingBalance, defaulting when unset).
// Non-sim venues are omitted; ResetBalance is structurally unsupported there.
func startingBalances(cfg config.Config) map[exec.VenueID]float64 {
	out := map[exec.VenueID]float64{}
	for _, v := range cfg.Venues {
		if v.Broker == "sim" {
			out[exec.VenueID(v.ID)] = v.EffectiveStartingBalance()
		}
	}
	return out
}

type venueBroker struct {
	ID     exec.VenueID
	Broker exec.Broker
	Run    func(ctx context.Context) // nil for sim; adapters' Run(ctx) returns no error (Plan 5)
}

// buildBrokers constructs one exec.Broker per configured venue. In replay mode
// every venue is a SimBroker (a recorded day has no live broker). In live mode it
// dispatches on Venue.Broker; moomoo is deferred to v1.x (error).
func buildBrokers(cfg config.Config, cr creds.File, clk clock.Clock, replay bool) ([]venueBroker, error) {
	out := make([]venueBroker, 0, len(cfg.Venues))
	for _, v := range cfg.Venues {
		id := exec.VenueID(v.ID)
		if replay {
			out = append(out, venueBroker{ID: id, Broker: sim.New(id, clk, v.EffectiveStartingBalance())})
			continue
		}
		switch v.Broker {
		case "sim":
			out = append(out, venueBroker{ID: id, Broker: sim.New(id, clk, v.EffectiveStartingBalance())})
		case "tradezero":
			pair, err := cr.Get(v.Credentials)
			if err != nil {
				return nil, fmt.Errorf("venue %s: %w", v.ID, err)
			}
			a, err := tradezero.New(tradezero.Config{Venue: id, AccountID: v.AccountID, Creds: pair, Clock: clk})
			if err != nil {
				return nil, fmt.Errorf("venue %s: %w", v.ID, err)
			}
			out = append(out, venueBroker{ID: id, Broker: a, Run: a.Run})
		case "alpaca":
			pair, err := cr.Get(v.Credentials)
			if err != nil {
				return nil, fmt.Errorf("venue %s: %w", v.ID, err)
			}
			a, err := alpaca.New(alpaca.Config{Venue: id, Env: v.Env, Creds: pair, Clock: clk})
			if err != nil {
				return nil, fmt.Errorf("venue %s: %w", v.ID, err)
			}
			out = append(out, venueBroker{ID: id, Broker: a, Run: a.Run})
		case "moomoo":
			// Stub venue: registers, never connects, rejects order placement.
			// The real moomoo trading adapter is execution v1.x; only this
			// case changes then. (Replay short-circuits to sim above.)
			out = append(out, venueBroker{ID: id, Broker: stub.New()})
		default:
			return nil, fmt.Errorf("venue %s: unknown broker %q", v.ID, v.Broker)
		}
	}
	return out, nil
}

// rttProber is health.New's unexported prober interface, restated here so
// this package can select an rttProber out of the built brokers without
// importing health's internals. alpaca.Adapter.ProbeRTT satisfies it.
type rttProber interface {
	ProbeRTT(ctx context.Context) (time.Duration, error)
}

// firstAlpacaProber returns the first configured Alpaca adapter's ProbeRTT,
// for wiring the engine-alpaca health link. Only *alpaca.Adapter implements
// rttProber among the possible venueBroker.Broker types (sim/tradezero/
// stub/alpaca), so a type-assert cleanly picks it out; nil (no alpaca venue
// configured, or replay mode where every venue is sim) means the
// engine-alpaca link is omitted entirely rather than shown down — see
// buildHealth's hasAlpaca gate.
//
// A venue list with BOTH a paper and a live Alpaca venue only gets one
// link's worth of latency; this picks the first in config order. Per-venue
// latency (keyed by venue id) is a deferred generalization if that split
// ever matters day to day.
func firstAlpacaProber(vbs []venueBroker) rttProber {
	for _, vb := range vbs {
		if p, ok := vb.Broker.(rttProber); ok {
			return p
		}
	}
	return nil
}

// errAlpacaLiveCreds is returned by resolveBackfillAlpacaCreds when the
// explicit backfill.alpaca.creds_key names the live Alpaca key. Read-only
// historical backfill has no business touching a real-money credential, and
// this refusal never falls through to auto-resolving a different (paper)
// alpaca venue — an operator who explicitly names alpaca-live gets the
// refusal, not a silent substitution.
var errAlpacaLiveCreds = errors.New("refusing alpaca-live creds for read-only historical fallback")

// resolveBackfillAlpacaCreds resolves the Alpaca credential pair used by the
// deep-history backfill's optional 1m fallback (config.BackfillAlpaca). The
// credentials-store redesign hands out random key names on every Venues-UI
// edit (e.g. "key-a48b723d"), so a standalone creds_key literal in
// config.toml drifts out of sync with what's actually stored; this resolves
// against the configured Alpaca venues instead (mirroring firstAlpacaProber's
// "scan venues for alpaca" pattern), which the UI keeps in sync by
// construction.
//
// Resolution order:
//  1. cfg.Backfill.Alpaca.CredsKey, if non-empty: errAlpacaLiveCreds if it
//     names "alpaca-live" (never used for read-only backfill; this refusal
//     does NOT fall through to step 2); otherwise resolved via cr.Get and
//     returned if that succeeds. An unresolvable non-live key falls through
//     to step 2 (self-heals a stale/renamed key).
//  2. The first cfg.Venues entry with Broker == "alpaca" and Env != "live"
//     whose Credentials resolve via cr.Get. A live Alpaca venue is never
//     selected here.
//  3. An error if nothing above resolved.
//
// The returned label names what was used (the creds key, or the venue id
// plus its creds key) for logging only — never the secret pair itself.
func resolveBackfillAlpacaCreds(cfg config.Config, cr creds.File) (creds.Pair, string, error) {
	key := cfg.Backfill.Alpaca.CredsKey
	if key != "" {
		if key == "alpaca-live" {
			return creds.Pair{}, "", fmt.Errorf("%w (key %q)", errAlpacaLiveCreds, key)
		}
		if p, err := cr.Get(key); err == nil {
			return p, key, nil
		}
	}
	for _, v := range cfg.Venues {
		if v.Broker != "alpaca" || v.Env == "live" {
			continue
		}
		if p, err := cr.Get(v.Credentials); err == nil {
			return p, fmt.Sprintf("venue %s (%s)", v.ID, v.Credentials), nil
		}
	}
	return creds.Pair{}, "", fmt.Errorf("no resolvable alpaca creds for backfill fallback (creds_key %q, no usable non-live alpaca venue)", key)
}

// moomooProbe measures OpenD round-trip latency with a lightweight Qot_GetGlobalState.
type moomooProbe struct {
	c *opend.Client
}

func (p moomooProbe) ProbeRTT(ctx context.Context) (time.Duration, error) {
	if p.c == nil {
		return 0, errors.New("no opend client")
	}
	start := time.Now()
	// UserID is a required (deprecated) proto2 field — a zero C2S{} fails to marshal.
	_, err := p.c.Request(ctx, opend.ProtoGetGlobalState,
		&getglobalstate.Request{C2S: &getglobalstate.C2S{UserID: proto.Uint64(0)}})
	return time.Since(start), err
}
