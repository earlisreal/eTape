// commands.go dispatches inbound WS "command" frames (SubmitOrder, Arm,
// GetConfig, SubscribeIndicator, ...) onto the exec.Core / store.Store /
// md.Core surfaces this package depends on only through the narrow execDoer,
// configStore, and indicatorCtl interfaces, so it stays testable with spies.
package uihub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type execDoer interface {
	Do(exec.Command) exec.CmdAck
}

type configStore interface {
	GetConfig(key string) (string, bool, error)
	SetConfig(key, value string)
}

type indicatorCtl interface {
	EnsureIndicator(id string, spec md.IndicatorSpec)
	ReleaseIndicator(id string)
}

// demandCtl is the hub surface the on-demand-subscription commands drive
// (satisfied by *Hub). EnsureDemand/ReleaseDemand are Run-loop-side; the
// blocking existence probe is done here in the conn goroutine via the feed
// getter before recording, so it never stalls the hub loop.
type demandCtl interface {
	EnsureDemand(connID uint64, d feed.Demand)
	ReleaseDemand(connID uint64, demandID string)
}

// venueAdmin is the file-only settings seam (satisfied by *venueadmin.Admin).
// It never touches the running gate/arm state — edits apply at next boot.
type venueAdmin interface {
	GetVenueSetup() (file, running config.VenueConfig, credKeys []string, err error)
	SetVenueSetup(vc config.VenueConfig) error
	PutCredential(name, keyID, secretKey string) error
	DeleteCredential(name string) error
}

type commands struct {
	ex   execDoer
	cfg  configStore
	ind  indicatorCtl
	dem  demandCtl
	va   venueAdmin
	feed func() Feed
}

func newCommands(ex execDoer, cfg configStore, ind indicatorCtl, dem demandCtl, va venueAdmin, feed func() Feed) *commands {
	return &commands{ex: ex, cfg: cfg, ind: ind, dem: dem, va: va, feed: feed}
}

func blocked(reason string) wsmsg.AckMsg { return wsmsg.AckMsg{Status: "blocked", Reason: reason} }

func ackFromCmd(a exec.CmdAck) wsmsg.AckMsg {
	status := wsmsg.AckStatus("accepted")
	if !a.Accepted {
		status = "blocked"
	}
	return wsmsg.AckMsg{Status: status, Reason: a.Reason, OrderID: a.OrderID}
}

func (cd *commands) handle(ctx context.Context, name string, args json.RawMessage, connID uint64) wsmsg.AckMsg {
	switch name {
	case "SubmitOrder":
		var a wsmsg.SubmitOrderArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		return ackFromCmd(cd.ex.Do(exec.SubmitOrder{
			Venue: exec.VenueID(a.Venue), Symbol: a.Symbol,
			Side: sideFromWire(a.Side), Type: orderTypeFromWire(a.Type), TIF: tifFromWire(a.TIF),
			Qty: a.Qty, LimitPrice: a.LimitPrice, StopPrice: a.StopPrice,
		}))
	case "CancelOrder":
		var a wsmsg.CancelOrderArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		return ackFromCmd(cd.ex.Do(exec.CancelOrder{Venue: exec.VenueID(a.Venue), OrderID: a.OrderID}))
	case "ReplaceOrder":
		var a wsmsg.ReplaceOrderArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		return ackFromCmd(cd.ex.Do(exec.ReplaceOrder{
			Venue: exec.VenueID(a.Venue), OrderID: a.OrderID,
			Qty: a.Qty, LimitPrice: a.LimitPrice, StopPrice: a.StopPrice,
		}))
	case "Flatten":
		var a wsmsg.FlattenArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		return ackFromCmd(cd.ex.Do(exec.Flatten{Venue: exec.VenueID(a.Venue)}))
	case "KillSwitch":
		var a wsmsg.KillSwitchArgs
		_ = json.Unmarshal(args, &a) // empty ok => all venues
		return ackFromCmd(cd.ex.Do(exec.KillSwitch{Venue: exec.VenueID(a.Venue)}))
	case "Arm":
		var a wsmsg.ArmArgs
		_ = json.Unmarshal(args, &a)
		return ackFromCmd(cd.ex.Do(exec.Arm{Venue: exec.VenueID(a.Venue)}))
	case "Disarm":
		var a wsmsg.ArmArgs
		_ = json.Unmarshal(args, &a)
		return ackFromCmd(cd.ex.Do(exec.Disarm{Venue: exec.VenueID(a.Venue)}))
	case "GetConfig":
		var a wsmsg.GetConfigArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		v, ok, err := cd.cfg.GetConfig(a.Key)
		if err != nil {
			return blocked("config read error")
		}
		if !ok {
			return wsmsg.AckMsg{Status: "accepted"} // absent key => accepted with no value
		}
		return wsmsg.AckMsg{Status: "accepted", Value: json.RawMessage(v)}
	case "SetConfig":
		var a wsmsg.SetConfigArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		cd.cfg.SetConfig(a.Key, string(a.Value))
		return wsmsg.AckMsg{Status: "accepted"}
	case "SubscribeIndicator":
		var a struct {
			InstanceID string             `json:"instanceId"`
			Symbol     string             `json:"symbol"`
			Timeframe  string             `json:"timeframe"`
			Type       string             `json:"type"`
			Params     map[string]float64 `json:"params"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.InstanceID == "" {
			return blocked("bad args")
		}
		cd.ind.EnsureIndicator(a.InstanceID, md.IndicatorSpec{
			Symbol: a.Symbol, TF: session.Timeframe(a.Timeframe),
			Type: md.IndicatorType(a.Type), Params: a.Params,
		})
		return wsmsg.AckMsg{Status: "accepted"}
	case "UnsubscribeIndicator":
		var a struct {
			InstanceID string `json:"instanceId"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.InstanceID == "" {
			return blocked("bad args")
		}
		cd.ind.ReleaseIndicator(a.InstanceID)
		return wsmsg.AckMsg{Status: "accepted"}
	case "EnsureSymbol":
		var a wsmsg.EnsureSymbolArgs
		if err := json.Unmarshal(args, &a); err != nil || a.DemandID == "" {
			return blocked("bad args")
		}
		if !supportedMarket(a.Symbol) {
			return blocked("unsupported market")
		}
		if reason := cd.probe(ctx, a.Symbol); reason != "" {
			return blocked(reason)
		}
		d, ok := demandForProfile(fmt.Sprintf("dyn/%d/%s", connID, a.DemandID), a.Symbol, a.Profile)
		if !ok {
			return blocked("bad profile")
		}
		cd.dem.EnsureDemand(connID, d)
		return wsmsg.AckMsg{Status: "accepted"}
	case "ReleaseSymbol":
		var a wsmsg.ReleaseSymbolArgs
		if err := json.Unmarshal(args, &a); err != nil || a.DemandID == "" {
			return blocked("bad args")
		}
		cd.dem.ReleaseDemand(connID, fmt.Sprintf("dyn/%d/%s", connID, a.DemandID))
		return wsmsg.AckMsg{Status: "accepted"}
	case "FocusGroup":
		var a wsmsg.FocusGroupArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		if !supportedMarket(a.Symbol) {
			return blocked("unsupported market")
		}
		if reason := cd.probe(ctx, a.Symbol); reason != "" {
			return blocked(reason)
		}
		// Registers no demand — demands arrive from member panels as they follow.
		return wsmsg.AckMsg{Status: "accepted"}
	case "GetVenueSetup":
		file, running, keys, err := cd.va.GetVenueSetup()
		if err != nil {
			return blocked("venue read error")
		}
		return wsmsg.AckMsg{Status: "accepted", Value: wsmsg.VenueSetup{
			File: venueConfigToWire(file), Running: venueConfigToWire(running), CredKeys: keys,
		}}
	case "SetVenueSetup":
		var a wsmsg.SetVenueSetupArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		if err := cd.va.SetVenueSetup(venueConfigFromWire(a.Venues, a.Gate)); err != nil {
			return blocked(err.Error())
		}
		return wsmsg.AckMsg{Status: "accepted"}
	case "PutCredential":
		var a wsmsg.PutCredentialArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		if a.Name == "" || a.KeyID == "" || a.SecretKey == "" {
			return blocked("name, keyId, and secretKey are required")
		}
		if err := cd.va.PutCredential(a.Name, a.KeyID, a.SecretKey); err != nil {
			return blocked(err.Error())
		}
		return wsmsg.AckMsg{Status: "accepted"}
	case "DeleteCredential":
		var a wsmsg.DeleteCredentialArgs
		if err := json.Unmarshal(args, &a); err != nil || a.Name == "" {
			return blocked("bad args")
		}
		if err := cd.va.DeleteCredential(a.Name); err != nil {
			return blocked(err.Error())
		}
		return wsmsg.AckMsg{Status: "accepted"}
	default:
		return blocked("unknown command: " + name)
	}
}

// probe validates a symbol exists; returns "" to accept, else a block reason.
// Skipped when the feed is nil (replay/tests) so those paths accept.
func (cd *commands) probe(ctx context.Context, symbol string) string {
	f := cd.feed()
	if f == nil {
		return ""
	}
	err := f.Validate(ctx, symbol)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, feed.ErrUnknownSymbol):
		return "unknown symbol " + symbol
	default:
		return "feed unavailable"
	}
}

func supportedMarket(sym string) bool {
	return strings.HasPrefix(sym, "US.") || strings.HasPrefix(sym, "HK.")
}

// demandForProfile builds the feed.Demand for a profile. focused adds SubBook
// only for US symbols (HK is LV1: a book sub retries forever). Returns ok=false
// for an unknown profile.
func demandForProfile(id, symbol, profile string) (feed.Demand, bool) {
	switch profile {
	case "watch":
		return feed.WatchDemand(id, symbol), true
	case "focused":
		subs := []feed.SubType{feed.SubQuote, feed.SubTicker, feed.SubKL1m}
		if strings.HasPrefix(symbol, "US.") {
			subs = append(subs, feed.SubBook)
		}
		return feed.Demand{ID: id, Symbol: symbol, Subs: subs, Focused: true, WantsHistory: true}, true
	case "interest":
		return feed.Demand{ID: id, Symbol: symbol}, true
	default:
		return feed.Demand{}, false
	}
}

func sideFromWire(s wsmsg.Side) exec.Side {
	switch s {
	case wsmsg.SideSell:
		return exec.SideSell
	case wsmsg.SideShort:
		return exec.SideShort
	case wsmsg.SideCover:
		return exec.SideCover
	default:
		return exec.SideBuy
	}
}

func orderTypeFromWire(t wsmsg.OrderType) exec.OrderType {
	switch t {
	case wsmsg.OrderLimit:
		return exec.TypeLimit
	case wsmsg.OrderStop:
		return exec.TypeStop
	case wsmsg.OrderStopLimit:
		return exec.TypeStopLimit
	default:
		return exec.TypeMarket
	}
}

func tifFromWire(t wsmsg.TIF) exec.TIF {
	switch t {
	case wsmsg.TIFGTC:
		return exec.TIFGTC
	case wsmsg.TIFIOC:
		return exec.TIFIOC
	case wsmsg.TIFFOK:
		return exec.TIFFOK
	default:
		return exec.TIFDay
	}
}

func venueToWire(v config.Venue) wsmsg.Venue {
	return wsmsg.Venue{ID: v.ID, Broker: v.Broker, Env: v.Env, Credentials: v.Credentials, AccountID: v.AccountID, AutoArm: v.AutoArm}
}

func gateToWire(g config.Gate) wsmsg.Gate {
	vm := map[string]wsmsg.GateLimitsView{}
	for id, gv := range g.Venue {
		vm[id] = wsmsg.GateLimitsView{MaxOrderValue: gv.MaxOrderValue, MaxPositionValue: gv.MaxPositionValue, MaxPositionShares: gv.MaxPositionShares, MaxOpenOrders: gv.MaxOpenOrders}
	}
	return wsmsg.Gate{
		Global: wsmsg.GlobalLimitsView{MaxDayLoss: g.Global.MaxDayLoss, MaxSymbolPositionValue: g.Global.MaxSymbolPositionValue, MaxSymbolPositionShares: g.Global.MaxSymbolPositionShares},
		Venue:  vm,
	}
}

func venueConfigToWire(vc config.VenueConfig) wsmsg.VenueConfig {
	vs := make([]wsmsg.Venue, 0, len(vc.Venues))
	for _, v := range vc.Venues {
		vs = append(vs, venueToWire(v))
	}
	return wsmsg.VenueConfig{Venues: vs, Gate: gateToWire(vc.Gate)}
}

func venueConfigFromWire(venues []wsmsg.Venue, gate wsmsg.Gate) config.VenueConfig {
	vs := make([]config.Venue, 0, len(venues))
	for _, v := range venues {
		vs = append(vs, config.Venue{ID: v.ID, Broker: v.Broker, Env: v.Env, Credentials: v.Credentials, AccountID: v.AccountID, AutoArm: v.AutoArm})
	}
	vm := map[string]config.GateVenue{}
	for id, gv := range gate.Venue {
		vm[id] = config.GateVenue{MaxOrderValue: gv.MaxOrderValue, MaxPositionValue: gv.MaxPositionValue, MaxPositionShares: gv.MaxPositionShares, MaxOpenOrders: gv.MaxOpenOrders}
	}
	return config.VenueConfig{
		Venues: vs,
		Gate:   config.Gate{Global: config.GateGlobal{MaxDayLoss: gate.Global.MaxDayLoss, MaxSymbolPositionValue: gate.Global.MaxSymbolPositionValue, MaxSymbolPositionShares: gate.Global.MaxSymbolPositionShares}, Venue: vm},
	}
}
