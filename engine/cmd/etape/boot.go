package main

import (
	"context"
	"fmt"

	"github.com/earlisreal/eTape/engine/internal/broker/alpaca"
	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/broker/tradezero"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/uihub"
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
		out = append(out, uihub.VenueMeta{
			ID: v.ID, Broker: v.Broker,
			Gate: uihub.GateLimits{
				MaxOrderValue: gv.MaxOrderValue, MaxPositionValue: gv.MaxPositionValue,
				MaxPositionShares: gv.MaxPositionShares, MaxOpenOrders: gv.MaxOpenOrders,
			},
		})
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
			out = append(out, venueBroker{ID: id, Broker: sim.New(id, clk)})
			continue
		}
		switch v.Broker {
		case "sim":
			out = append(out, venueBroker{ID: id, Broker: sim.New(id, clk)})
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
			return nil, fmt.Errorf("venue %s: moomoo trading venue is deferred to v1.x", v.ID)
		default:
			return nil, fmt.Errorf("venue %s: unknown broker %q", v.ID, v.Broker)
		}
	}
	return out, nil
}
