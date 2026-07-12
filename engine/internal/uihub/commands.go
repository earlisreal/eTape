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
	"time"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
	"github.com/earlisreal/eTape/engine/internal/venueprobe"
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
	LoadOlder(symbol string, daily bool, done func(added int, exhausted bool, err error))
}

// venueAdmin is the file-only settings seam (satisfied by *venueadmin.Admin).
// It never touches the running gate/arm state — edits apply at next boot.
type venueAdmin interface {
	GetVenueSetup() (file, running config.VenueConfig, credKeys []string, moomooAttempted bool, err error)
	SetVenueSetup(vc config.VenueConfig) error
	PutCredential(name, keyID, secretKey string) error
	DeleteCredential(name string) error
}

// venueTester is the read-only credential-probe seam (satisfied by
// *venueprobe.Prober). Every implementation must be side-effect-free
// against the broker (GET-only) — see the plan's Global Constraints.
type venueTester interface {
	TestConnection(ctx context.Context, broker, env, credName, keyID, secretKey, accountID string) venueprobe.Result
}

// commands.tester holds the venueTester dependency; it is named "tester"
// rather than "probe" because *commands already has an unrelated probe
// method (symbol-existence validation for EnsureSymbol/FocusGroup) — a
// field and a method can't share a name on the same type.
type commands struct {
	ex     execDoer
	cfg    configStore
	ind    indicatorCtl
	dem    demandCtl
	va     venueAdmin
	feed   func() Feed
	tester venueTester
	// restart is set post-construction by uihub.New (not passed through
	// newCommands, to avoid touching every commands_test.go call site) — see
	// the "RestartEngine" case below. Nil in tests that don't set it, hence
	// the nil guard there.
	restart func()
	// startReplay/goLive/startDemo are set post-construction by uihub.New,
	// same pattern as restart above (see api.go) — kept out of newCommands'
	// param list so the many existing newCommands(...) call sites in
	// commands_test.go don't need updating. Unlike restart, they carry
	// arguments and can fail validation (bad day, negative speed), so each
	// call is expected to validate synchronously and return an error for a
	// blocked ack *before* scheduling any delayed side effect — see the
	// closures built in main.go.
	startReplay func(day string, speed float64) error
	goLive      func() error
	startDemo   func() error
}

func newCommands(ex execDoer, cfg configStore, ind indicatorCtl, dem demandCtl, va venueAdmin, feed func() Feed, tester venueTester) *commands {
	return &commands{ex: ex, cfg: cfg, ind: ind, dem: dem, va: va, feed: feed, tester: tester}
}

// restartAckFlushDelay defers the actual restart trigger past the moment
// this handler returns its "accepted" ack, so the ack has time to be
// written to the WS connection before boot's shutdown drain cancels the
// conn's context (which would otherwise make the write a no-op — see
// conn.go's write path).
const restartAckFlushDelay = 200 * time.Millisecond

func blocked(reason string) wsmsg.AckMsg { return wsmsg.AckMsg{Status: "blocked", Reason: reason} }

func ackFromCmd(a exec.CmdAck) wsmsg.AckMsg {
	status := wsmsg.AckStatus("accepted")
	if !a.Accepted {
		status = "blocked"
	}
	return wsmsg.AckMsg{Status: status, Reason: a.Reason, OrderID: a.OrderID}
}

func (cd *commands) handle(ctx context.Context, name string, args json.RawMessage, connID uint64, reply func(wsmsg.AckMsg)) (wsmsg.AckMsg, bool) {
	switch name {
	case "SubmitOrder":
		var a wsmsg.SubmitOrderArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		return ackFromCmd(cd.ex.Do(exec.SubmitOrder{
			Venue: exec.VenueID(a.Venue), Symbol: a.Symbol,
			Side: sideFromWire(a.Side), Type: orderTypeFromWire(a.Type), TIF: tifFromWire(a.TIF),
			Session: sessionFromWire(a.Session),
			Qty:     a.Qty, LimitPrice: a.LimitPrice, StopPrice: a.StopPrice,
		})), false
	case "CancelOrder":
		var a wsmsg.CancelOrderArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		return ackFromCmd(cd.ex.Do(exec.CancelOrder{Venue: exec.VenueID(a.Venue), OrderID: a.OrderID})), false
	case "ReplaceOrder":
		var a wsmsg.ReplaceOrderArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		return ackFromCmd(cd.ex.Do(exec.ReplaceOrder{
			Venue: exec.VenueID(a.Venue), OrderID: a.OrderID,
			Qty: a.Qty, LimitPrice: a.LimitPrice, StopPrice: a.StopPrice,
		})), false
	case "Flatten":
		var a wsmsg.FlattenArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		return ackFromCmd(cd.ex.Do(exec.Flatten{Venue: exec.VenueID(a.Venue)})), false
	case "ResetBalance":
		var a wsmsg.ResetBalanceArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		return ackFromCmd(cd.ex.Do(exec.ResetBalance{Venue: exec.VenueID(a.Venue)})), false
	case "KillSwitch":
		var a wsmsg.KillSwitchArgs
		_ = json.Unmarshal(args, &a) // empty ok => all venues
		return ackFromCmd(cd.ex.Do(exec.KillSwitch{Venue: exec.VenueID(a.Venue)})), false
	case "Arm":
		return ackFromCmd(cd.ex.Do(exec.Arm{})), false
	case "Disarm":
		return ackFromCmd(cd.ex.Do(exec.Disarm{})), false
	case "GetConfig":
		var a wsmsg.GetConfigArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		v, ok, err := cd.cfg.GetConfig(a.Key)
		if err != nil {
			return blocked("config read error"), false
		}
		if !ok {
			return wsmsg.AckMsg{Status: "accepted"}, false // absent key => accepted with no value
		}
		return wsmsg.AckMsg{Status: "accepted", Value: json.RawMessage(v)}, false
	case "SetConfig":
		var a wsmsg.SetConfigArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		cd.cfg.SetConfig(a.Key, string(a.Value))
		return wsmsg.AckMsg{Status: "accepted"}, false
	case "SubscribeIndicator":
		var a struct {
			InstanceID string             `json:"instanceId"`
			Symbol     string             `json:"symbol"`
			Timeframe  string             `json:"timeframe"`
			Type       string             `json:"type"`
			Params     map[string]float64 `json:"params"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.InstanceID == "" {
			return blocked("bad args"), false
		}
		cd.ind.EnsureIndicator(a.InstanceID, md.IndicatorSpec{
			Symbol: a.Symbol, TF: session.Timeframe(a.Timeframe),
			Type: md.IndicatorType(a.Type), Params: a.Params,
		})
		return wsmsg.AckMsg{Status: "accepted"}, false
	case "UnsubscribeIndicator":
		var a struct {
			InstanceID string `json:"instanceId"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.InstanceID == "" {
			return blocked("bad args"), false
		}
		cd.ind.ReleaseIndicator(a.InstanceID)
		return wsmsg.AckMsg{Status: "accepted"}, false
	case "EnsureSymbol":
		var a wsmsg.EnsureSymbolArgs
		if err := json.Unmarshal(args, &a); err != nil || a.DemandID == "" {
			return blocked("bad args"), false
		}
		if !supportedMarket(a.Symbol) {
			return blocked("unsupported market"), false
		}
		if reason := cd.probe(ctx, a.Symbol); reason != "" {
			return blocked(reason), false
		}
		d, ok := demandForProfile(fmt.Sprintf("dyn/%d/%s", connID, a.DemandID), a.Symbol, a.Profile)
		if !ok {
			return blocked("bad profile"), false
		}
		cd.dem.EnsureDemand(connID, d)
		return wsmsg.AckMsg{Status: "accepted"}, false
	case "ReleaseSymbol":
		var a wsmsg.ReleaseSymbolArgs
		if err := json.Unmarshal(args, &a); err != nil || a.DemandID == "" {
			return blocked("bad args"), false
		}
		cd.dem.ReleaseDemand(connID, fmt.Sprintf("dyn/%d/%s", connID, a.DemandID))
		return wsmsg.AckMsg{Status: "accepted"}, false
	case "FocusGroup":
		var a wsmsg.FocusGroupArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		if !supportedMarket(a.Symbol) {
			return blocked("unsupported market"), false
		}
		if reason := cd.probe(ctx, a.Symbol); reason != "" {
			return blocked(reason), false
		}
		// Registers no demand — demands arrive from member panels as they follow.
		return wsmsg.AckMsg{Status: "accepted"}, false
	case "LoadOlderBars":
		var a wsmsg.LoadOlderBarsArgs
		if err := json.Unmarshal(args, &a); err != nil || a.Symbol == "" {
			return blocked("bad args"), false
		}
		cd.dem.LoadOlder(a.Symbol, a.Daily, func(added int, exhausted bool, err error) {
			if err != nil {
				reply(wsmsg.AckMsg{Status: "blocked", Reason: err.Error()})
				return
			}
			reply(wsmsg.AckMsg{Status: "accepted", Value: wsmsg.LoadOlderResult{Added: added, Exhausted: exhausted}})
		})
		return wsmsg.AckMsg{}, true // deferred
	case "GetVenueSetup":
		file, running, keys, moomooAttempted, err := cd.va.GetVenueSetup()
		if err != nil {
			return blocked("venue read error"), false
		}
		return wsmsg.AckMsg{Status: "accepted", Value: wsmsg.VenueSetup{
			File: venueConfigToWire(file), Running: venueConfigToWire(running), CredKeys: keys,
			Seed: wsmsg.SeedView{MoomooAttempted: moomooAttempted},
		}}, false
	case "SetVenueSetup":
		var a wsmsg.SetVenueSetupArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		if err := cd.va.SetVenueSetup(venueConfigFromWire(a.Venues, a.Gate)); err != nil {
			return blocked(err.Error()), false
		}
		return wsmsg.AckMsg{Status: "accepted"}, false
	case "PutCredential":
		var a wsmsg.PutCredentialArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		if a.Name == "" || a.KeyID == "" || a.SecretKey == "" {
			return blocked("name, keyId, and secretKey are required"), false
		}
		if err := cd.va.PutCredential(a.Name, a.KeyID, a.SecretKey); err != nil {
			return blocked(err.Error()), false
		}
		return wsmsg.AckMsg{Status: "accepted"}, false
	case "DeleteCredential":
		var a wsmsg.DeleteCredentialArgs
		if err := json.Unmarshal(args, &a); err != nil || a.Name == "" {
			return blocked("bad args"), false
		}
		if err := cd.va.DeleteCredential(a.Name); err != nil {
			return blocked(err.Error()), false
		}
		return wsmsg.AckMsg{Status: "accepted"}, false
	case "TestConnection":
		var a wsmsg.TestConnectionArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		r := cd.tester.TestConnection(pctx, a.Broker, a.Env, a.Credentials, a.KeyID, a.SecretKey, a.AccountID)
		return wsmsg.AckMsg{Status: "accepted", Value: resultToWire(r)}, false
	case "RestartEngine":
		if cd.restart == nil {
			return blocked("restart not supported"), false
		}
		time.AfterFunc(restartAckFlushDelay, cd.restart)
		return wsmsg.AckMsg{Status: "accepted"}, false
	case "StartReplay":
		var a wsmsg.StartReplayArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args"), false
		}
		if a.Speed < 0 {
			return blocked("speed must be >= 0"), false
		}
		if cd.startReplay == nil {
			return blocked("replay switching not supported"), false
		}
		if err := cd.startReplay(a.Day, a.Speed); err != nil {
			return blocked(err.Error()), false
		}
		return wsmsg.AckMsg{Status: wsmsg.AckAccepted}, false
	case "GoLive":
		if cd.goLive == nil {
			return blocked("replay switching not supported"), false
		}
		if err := cd.goLive(); err != nil {
			return blocked(err.Error()), false
		}
		return wsmsg.AckMsg{Status: wsmsg.AckAccepted}, false
	case "StartDemo":
		if cd.startDemo == nil {
			return blocked("demo switching not supported"), false
		}
		if err := cd.startDemo(); err != nil {
			return blocked(err.Error()), false
		}
		return wsmsg.AckMsg{Status: wsmsg.AckAccepted}, false
	default:
		return blocked("unknown command: " + name), false
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

// sessionFromWire defaults an absent/unrecognized value to SessionAuto — the
// safe, clock-inferred behavior — so a legacy client that never sends
// `session` (or a stale one) keeps today's submit behavior unchanged.
func sessionFromWire(s wsmsg.OrderSession) exec.OrderSession {
	switch s {
	case wsmsg.SessionRTH:
		return exec.SessionRTH
	case wsmsg.SessionExtended:
		return exec.SessionExtended
	case wsmsg.SessionOvernight:
		return exec.SessionOvernight
	default:
		return exec.SessionAuto
	}
}

func venueToWire(v config.Venue) wsmsg.Venue {
	return wsmsg.Venue{
		ID: v.ID, Broker: v.Broker, Env: v.Env, Credentials: v.Credentials, AccountID: v.AccountID,
		StartingBalance: v.StartingBalance,
		SlippageBps:     v.SlippageBps,
		FillLatencyMs:   v.FillLatencyMs,
	}
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

// resultToWire converts a venueprobe.Result into the TestConnection command's
// AckMsg.Value payload.
func resultToWire(r venueprobe.Result) wsmsg.TestConnectionResult {
	accts := make([]wsmsg.TestAccount, 0, len(r.Accounts))
	for _, a := range r.Accounts {
		accts = append(accts, wsmsg.TestAccount{AccountID: a.AccountID, AccountType: a.AccountType, Env: a.Env})
	}
	return wsmsg.TestConnectionResult{
		OK: r.OK, Env: r.Env, AccountID: r.AccountID, AccountType: r.AccountType,
		Message: r.Message, Accounts: accts,
	}
}

func venueConfigFromWire(venues []wsmsg.Venue, gate wsmsg.Gate) config.VenueConfig {
	vs := make([]config.Venue, 0, len(venues))
	for _, v := range venues {
		vs = append(vs, config.Venue{
			ID: v.ID, Broker: v.Broker, Env: v.Env, Credentials: v.Credentials, AccountID: v.AccountID,
			StartingBalance: v.StartingBalance,
			SlippageBps:     v.SlippageBps,
			FillLatencyMs:   v.FillLatencyMs,
		})
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
