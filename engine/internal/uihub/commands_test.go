package uihub

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
	"github.com/earlisreal/eTape/engine/internal/venueprobe"
)

// mustJSON marshals v to a json.RawMessage, failing the test on error. Used
// by tests that build handle's args from a typed wsmsg struct rather than a
// hand-written JSON literal.
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

type spyExec struct {
	last exec.Command
	ack  exec.CmdAck
}

func (s *spyExec) Do(c exec.Command) exec.CmdAck { s.last = c; return s.ack }

type spyCfg struct {
	got     map[string]string
	values  map[string]string
	deleted string
}

func (s *spyCfg) GetConfig(k string) (string, bool, error) {
	v, ok := s.values[k]
	return v, ok, nil
}
func (s *spyCfg) SetConfig(k, v string) {
	if s.got == nil {
		s.got = map[string]string{}
	}
	s.got[k] = v
}
func (s *spyCfg) DeleteConfig(k string) { s.deleted = k }

type spyInd struct{ ensured, released string }

func (s *spyInd) EnsureIndicator(_ uint64, id string, _ md.IndicatorSpec) { s.ensured = id }
func (s *spyInd) ReleaseIndicator(_ uint64, id string)                    { s.released = id }

func TestCommandsSubmitOrderMapsEnums(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true, OrderID: "ET5"}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	ack, _ := cd.handle(context.Background(), "SubmitOrder", json.RawMessage(`{"venue":"sim","symbol":"US.AAPL","side":"SHORT","type":"STOP_LIMIT","tif":"GTC","session":"EXTENDED","qty":80,"limitPrice":3.55,"stopPrice":3.6}`), 0, func(wsmsg.AckMsg) {})
	if ack.Status != "accepted" || ack.OrderID != "ET5" {
		t.Fatalf("ack wrong: %+v", ack)
	}
	so, ok := ex.last.(exec.SubmitOrder)
	if !ok {
		t.Fatalf("expected exec.SubmitOrder, got %T", ex.last)
	}
	if so.Side != exec.SideShort || so.Type != exec.TypeStopLimit || so.TIF != exec.TIFGTC || so.Session != exec.SessionExtended {
		t.Fatalf("enum parse wrong: %+v", so)
	}
	if so.Qty != 80 || so.LimitPrice != 3.55 || so.StopPrice != 3.6 || string(so.Venue) != "sim" {
		t.Fatalf("field copy wrong: %+v", so)
	}
}

// TestCommandsSubmitOrderSessionDefaultsToAuto covers a legacy/absent `session`
// key on the wire (an older client, or a client that never sends it) — it must
// decode to SessionAuto, preserving today's clock-inferred submit behavior
// rather than failing closed to some other session.
func TestCommandsSubmitOrderSessionDefaultsToAuto(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true, OrderID: "ET6"}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.handle(context.Background(), "SubmitOrder", json.RawMessage(`{"venue":"sim","symbol":"US.AAPL","side":"BUY","type":"LIMIT","tif":"DAY","qty":10,"limitPrice":5}`), 0, func(wsmsg.AckMsg) {})
	so, ok := ex.last.(exec.SubmitOrder)
	if !ok || so.Session != exec.SessionAuto {
		t.Fatalf("absent session must default to SessionAuto, got %T %+v", ex.last, ex.last)
	}
}

func TestCommandsBlockedPassesReason(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: false, Reason: "R114 gate: max order value"}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	ack, _ := cd.handle(context.Background(), "SubmitOrder", json.RawMessage(`{"venue":"sim","symbol":"US.AAPL","side":"BUY","type":"MARKET","tif":"DAY","qty":1}`), 0, func(wsmsg.AckMsg) {})
	if ack.Status != "blocked" || ack.Reason != "R114 gate: max order value" {
		t.Fatalf("blocked reason must pass through verbatim: %+v", ack)
	}
}

func TestCommandsKillSwitchAllVenues(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.handle(context.Background(), "KillSwitch", json.RawMessage(`{}`), 0, func(wsmsg.AckMsg) {}) // no venue => all
	ks, ok := ex.last.(exec.KillSwitch)
	if !ok || ks.Venue != "" {
		t.Fatalf("KillSwitch{} => all venues (empty VenueID), got %T %+v", ex.last, ex.last)
	}
}

func TestCommandsArmMaster(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.handle(context.Background(), "Arm", json.RawMessage(`{}`), 0, func(wsmsg.AckMsg) {})
	if _, ok := ex.last.(exec.Arm); !ok {
		t.Fatalf("expected exec.Arm, got %T", ex.last)
	}
}

func TestCommandsResetBalanceDispatch(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.handle(context.Background(), "ResetBalance", json.RawMessage(`{"venue":"sim-1"}`), 0, func(wsmsg.AckMsg) {})
	rb, ok := ex.last.(exec.ResetBalance)
	if !ok || rb.Venue != "sim-1" {
		t.Fatalf("expected exec.ResetBalance{Venue: sim-1}, got %T %+v", ex.last, ex.last)
	}
}

// TestCommandsRestartEngineBlockedWithNoHandler covers a commands value built
// via newCommands (restart left nil, as every other test in this file does) --
// the same shape uihub sees before api.New sets cmd.restart, and the shape any
// test double that never wires a restart handler will have.
func TestCommandsRestartEngineBlockedWithNoHandler(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	ack, deferred := cd.handle(context.Background(), "RestartEngine", json.RawMessage(`{}`), 0, func(wsmsg.AckMsg) {})
	if deferred {
		t.Fatal("RestartEngine must not be deferred")
	}
	if ack.Status != wsmsg.AckBlocked {
		t.Fatalf("RestartEngine with no handler: status = %q, want blocked", ack.Status)
	}
}

// TestCommandsRestartEngineAcksThenTriggers covers the real api.New wiring
// shape: cd.restart set post-construction (see api.go). The ack must come
// back accepted and non-deferred immediately; the actual restart trigger
// (cd.restart) fires only after restartAckFlushDelay, giving the ack time to
// reach the client before the caller cancels ctx.
func TestCommandsRestartEngineAcksThenTriggers(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	triggered := make(chan struct{})
	cd.restart = func() { close(triggered) }

	ack, deferred := cd.handle(context.Background(), "RestartEngine", json.RawMessage(`{}`), 0, func(wsmsg.AckMsg) {
		t.Fatal("RestartEngine's own ack must be synchronous, not delivered via reply")
	})
	if deferred {
		t.Fatal("RestartEngine must not be deferred")
	}
	if ack.Status != wsmsg.AckAccepted {
		t.Fatalf("RestartEngine: status = %q, want accepted", ack.Status)
	}

	select {
	case <-triggered:
	case <-time.After(2 * time.Second):
		t.Fatal("restart handler was never invoked")
	}
}

func TestVenueWireRoundTripsStartingBalance(t *testing.T) {
	v := config.Venue{ID: "sim-1", Broker: "sim", Env: "paper", StartingBalance: 25_000}
	wire := venueToWire(v)
	if wire.StartingBalance != 25_000 {
		t.Fatalf("venueToWire dropped StartingBalance: %+v", wire)
	}
	back := venueConfigFromWire([]wsmsg.Venue{wire}, wsmsg.Gate{Venue: map[string]wsmsg.GateLimitsView{}})
	if back.Venues[0].StartingBalance != 25_000 {
		t.Fatalf("venueConfigFromWire dropped StartingBalance: %+v", back.Venues[0])
	}
}

// TestVenueWireRoundTripsSlippageAndFillLatency is the regression test for
// the final-review finding that venueToWire/venueConfigFromWire (and the
// wsmsg.Venue wire struct itself) never learned about the two sim realism
// knobs added alongside StartingBalance: config.Venue.SlippageBps and
// config.Venue.FillLatencyMs. Without carrying them through the wire struct,
// any settings-UI save (venueConfigFromWire -> venueadmin.SetVenueSetup ->
// config.WriteVenueConfig) silently zeroes fields a user hand-set directly
// in config.toml, even when the save was for an unrelated venue field.
func TestVenueWireRoundTripsSlippageAndFillLatency(t *testing.T) {
	v := config.Venue{ID: "sim-1", Broker: "sim", Env: "paper", SlippageBps: 7.5, FillLatencyMs: 250}
	wire := venueToWire(v)
	if wire.SlippageBps != 7.5 {
		t.Fatalf("venueToWire dropped SlippageBps: %+v", wire)
	}
	if wire.FillLatencyMs != 250 {
		t.Fatalf("venueToWire dropped FillLatencyMs: %+v", wire)
	}
	back := venueConfigFromWire([]wsmsg.Venue{wire}, wsmsg.Gate{Venue: map[string]wsmsg.GateLimitsView{}})
	if back.Venues[0].SlippageBps != 7.5 {
		t.Fatalf("venueConfigFromWire dropped SlippageBps: %+v", back.Venues[0])
	}
	if back.Venues[0].FillLatencyMs != 250 {
		t.Fatalf("venueConfigFromWire dropped FillLatencyMs: %+v", back.Venues[0])
	}
}

func TestCommandsGetSetConfig(t *testing.T) {
	cfg := &spyCfg{values: map[string]string{"theme": `"dark"`}}
	cd := newCommands(&spyExec{}, cfg, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	get, _ := cd.handle(context.Background(), "GetConfig", json.RawMessage(`{"key":"theme"}`), 0, func(wsmsg.AckMsg) {})
	if get.Status != "accepted" {
		t.Fatalf("GetConfig should accept: %+v", get)
	}
	raw, ok := get.Value.(json.RawMessage)
	if !ok || string(raw) != `"dark"` {
		t.Fatalf("GetConfig must return stored JSON value verbatim: %v", get.Value)
	}
	set, _ := cd.handle(context.Background(), "SetConfig", json.RawMessage(`{"key":"theme","value":"light"}`), 0, func(wsmsg.AckMsg) {})
	if set.Status != "accepted" || cfg.got["theme"] != `"light"` {
		t.Fatalf("SetConfig must persist raw JSON value: %+v / %v", set, cfg.got)
	}
}

func TestCommandsSetConfigNotifiesRuntimeAfterPersisting(t *testing.T) {
	cfg := &spyCfg{}
	cd := newCommands(&spyExec{}, cfg, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	called := false
	cd.onConfigSet = func(key, value string) {
		called = true
		if cfg.got[key] != value {
			t.Fatalf("observer saw config before persistence: %q != %q", cfg.got[key], value)
		}
	}
	ack, _ := cd.handle(context.Background(), "SetConfig", json.RawMessage(`{"key":"orderConfig","value":{"activeVenue":"alpaca-paper"}}`), 0, func(wsmsg.AckMsg) {})
	if ack.Status != "accepted" || !called {
		t.Fatalf("SetConfig ack/observer = %+v called=%v", ack, called)
	}
}

func TestCommandsDeleteConfig(t *testing.T) {
	cfg := &spyCfg{}
	cd := newCommands(&spyExec{}, cfg, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	ack, _ := cd.handle(context.Background(), "DeleteConfig", json.RawMessage(`{"key":"workspace.dead"}`), 0, func(wsmsg.AckMsg) {})
	if ack.Status != wsmsg.AckAccepted || cfg.deleted != "workspace.dead" {
		t.Fatalf("DeleteConfig: %+v deleted=%q", ack, cfg.deleted)
	}
	bad, _ := cd.handle(context.Background(), "DeleteConfig", json.RawMessage(`{"key":`), 0, func(wsmsg.AckMsg) {})
	if bad.Status != wsmsg.AckBlocked {
		t.Fatalf("malformed DeleteConfig accepted: %+v", bad)
	}
}

func TestCommandsIndicatorLifecycle(t *testing.T) {
	ind := &spyInd{}
	cd := newCommands(&spyExec{}, &spyCfg{}, ind, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.handle(context.Background(), "SubscribeIndicator", json.RawMessage(`{"instanceId":"i1","symbol":"US.AAPL","timeframe":"1m","type":"VWAP","params":{}}`), 0, func(wsmsg.AckMsg) {})
	if ind.ensured != "i1" {
		t.Fatalf("SubscribeIndicator should EnsureIndicator, got %q", ind.ensured)
	}
	cd.handle(context.Background(), "UnsubscribeIndicator", json.RawMessage(`{"instanceId":"i1"}`), 0, func(wsmsg.AckMsg) {})
	if ind.released != "i1" {
		t.Fatalf("UnsubscribeIndicator should ReleaseIndicator, got %q", ind.released)
	}
}

// TestCommandsAllReturnNonDeferred is the regression test for Task 6's
// mechanical sweep across handle's ~49 return sites: every existing command
// name (plus the unknown/default branch) must still report deferred=false
// and its usual ack status now that handle's signature grew a reply callback
// and a second (bool) return value. reply is also asserted unused -- no
// existing command is deferred yet; Task 7's LoadOlderBars will be the first
// real caller of the callback.
func TestCommandsAllReturnNonDeferred(t *testing.T) {
	tests := []struct {
		name       string
		args       string
		wantStatus wsmsg.AckStatus
	}{
		{"SubmitOrder", `{"venue":"sim","symbol":"US.AAPL","side":"BUY","type":"MARKET","tif":"DAY","qty":1}`, wsmsg.AckAccepted},
		{"CancelOrder", `{"venue":"sim","orderId":"o1"}`, wsmsg.AckAccepted},
		{"ReplaceOrder", `{"venue":"sim","orderId":"o1","qty":1}`, wsmsg.AckAccepted},
		{"Flatten", `{"venue":"sim"}`, wsmsg.AckAccepted},
		{"ResetBalance", `{"venue":"sim"}`, wsmsg.AckAccepted},
		{"KillSwitch", `{}`, wsmsg.AckAccepted},
		{"Arm", `{}`, wsmsg.AckAccepted},
		{"Disarm", `{}`, wsmsg.AckAccepted},
		{"GetConfig", `{"key":"theme"}`, wsmsg.AckAccepted},
		{"SetConfig", `{"key":"theme","value":"light"}`, wsmsg.AckAccepted},
		{"SubscribeIndicator", `{"instanceId":"i1","symbol":"US.AAPL","timeframe":"1m","type":"VWAP","params":{}}`, wsmsg.AckAccepted},
		{"UnsubscribeIndicator", `{"instanceId":"i1"}`, wsmsg.AckAccepted},
		{"EnsureSymbol", `{"demandId":"p1","symbol":"US.AAPL","profile":"watch"}`, wsmsg.AckAccepted},
		{"ReleaseSymbol", `{"demandId":"p1"}`, wsmsg.AckAccepted},
		{"FocusGroup", `{"group":"blue","symbol":"US.AAPL"}`, wsmsg.AckAccepted},
		{"Nope", `{}`, wsmsg.AckBlocked}, // unknown command => default branch
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ex := &spyExec{ack: exec.CmdAck{Accepted: true, OrderID: "ET1"}}
			cfg := &spyCfg{values: map[string]string{"theme": `"dark"`}}
			cd := newCommands(ex, cfg, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
			ack, deferred := cd.handle(context.Background(), tc.name, json.RawMessage(tc.args), 1, func(wsmsg.AckMsg) {
				t.Fatalf("%s: reply callback invoked, but no existing command is deferred", tc.name)
			})
			if deferred {
				t.Fatalf("%s: deferred = true, want false (mechanical sweep must not change any existing command's synchronous behavior)", tc.name)
			}
			if ack.Status != tc.wantStatus {
				t.Fatalf("%s: status = %q, want %q (reason=%q)", tc.name, ack.Status, tc.wantStatus, ack.Reason)
			}
		})
	}
}

func TestCommandsUnknown(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	ack, _ := cd.handle(context.Background(), "Nope", json.RawMessage(`{}`), 0, func(wsmsg.AckMsg) {})
	if ack.Status != "blocked" {
		t.Fatalf("unknown command must block, got %+v", ack)
	}
}

type spyCmdFeed struct{ err error }

func (s *spyCmdFeed) Validate(context.Context, string) error { return s.err }
func (s *spyCmdFeed) Ensure(feed.Demand)                     {}
func (s *spyCmdFeed) Release(string)                         {}

type spyDemandCtl struct {
	ensured []struct {
		conn uint64
		d    feed.Demand
	}
	released []struct {
		conn uint64
		id   string
	}
	loadOlderFn func(symbol string, daily bool, requiredStartMs, requiredEndMs int64, done func(added int, exhausted bool, err error))
}

func (s *spyDemandCtl) EnsureDemand(conn uint64, d feed.Demand) {
	s.ensured = append(s.ensured, struct {
		conn uint64
		d    feed.Demand
	}{conn, d})
}
func (s *spyDemandCtl) ReleaseDemand(conn uint64, id string) {
	s.released = append(s.released, struct {
		conn uint64
		id   string
	}{conn, id})
}

func (s *spyDemandCtl) LoadOlder(symbol string, daily bool, requiredStartMs, requiredEndMs int64, done func(added int, exhausted bool, err error)) {
	if s.loadOlderFn != nil {
		s.loadOlderFn(symbol, daily, requiredStartMs, requiredEndMs, done)
		return
	}
	done(0, true, nil) // default stub: nothing to fetch (mirrors the Hub's nil-slot fallback)
}

func newCmdWith(t *testing.T, feedErr error, feedNil bool) (*commands, *spyDemandCtl, *spyCmdFeed) {
	t.Helper()
	dem := &spyDemandCtl{}
	sf := &spyCmdFeed{err: feedErr}
	getter := func() Feed { return sf }
	if feedNil {
		getter = func() Feed { return nil }
	}
	return newCommands(nil, nil, nil, dem, &spyVenueAdmin{}, getter, &spyVenueTester{}), dem, sf
}

func TestEnsureSymbol_AcceptsAndMapsWatch(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	ack, _ := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p1","symbol":"US.AAPL","profile":"watch"}`), 7, func(wsmsg.AckMsg) {})
	if ack.Status != "accepted" {
		t.Fatalf("status = %q reason=%q", ack.Status, ack.Reason)
	}
	if len(dem.ensured) != 1 {
		t.Fatalf("EnsureDemand calls = %d", len(dem.ensured))
	}
	got := dem.ensured[0]
	if got.conn != 7 || got.d.ID != "dyn/7/p1" || got.d.Symbol != "US.AAPL" {
		t.Fatalf("demand = %+v", got)
	}
	if got.d.Focused {
		t.Fatalf("watch must not be focused")
	}
	if got.d.BackgroundSeed {
		t.Fatalf("UI watch must use foreground seed lane")
	}
	if !reflect.DeepEqual(got.d.Subs, []feed.SubType{feed.SubTicker}) {
		t.Fatalf("watch subs = %v", got.d.Subs)
	}
	if !got.d.WantsHistory {
		t.Fatal("watch must request archive history")
	}
}

func TestEnsureSymbol_ChartAddsCachedDailySubscription(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	ack, _ := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"chart1","symbol":"US.AAPL","profile":"chart"}`), 7, func(wsmsg.AckMsg) {})
	if ack.Status != "accepted" {
		t.Fatalf("ack = %+v", ack)
	}
	d := dem.ensured[0].d
	if !d.CachedDaily || !reflect.DeepEqual(d.Subs, []feed.SubType{feed.SubTicker, feed.SubKL1m, feed.SubKLDay}) {
		t.Fatalf("chart demand = %+v", d)
	}
}

func TestEnsureSymbol_FocusedUSHasBook(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p2","symbol":"US.NVDA","profile":"focused"}`), 1, func(wsmsg.AckMsg) {})
	d := dem.ensured[0].d
	if !d.Focused {
		t.Fatal("focused flag missing")
	}
	if d.BackgroundSeed {
		t.Fatal("UI focused demand must use foreground seed lane")
	}
	if !reflect.DeepEqual(d.Subs, []feed.SubType{feed.SubQuote, feed.SubTicker, feed.SubKL1m, feed.SubKLDay, feed.SubBook}) {
		t.Fatalf("US focused subs = %v (want quote,ticker,kl1m,klday,book)", d.Subs)
	}
	if !d.CachedDaily {
		t.Fatal("focused demand must request immediate cached daily bars")
	}
	if !d.WantsHistory {
		t.Fatal("focused demand must request history")
	}
}

func TestEnsureSymbol_FocusedHKNoBook(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p3","symbol":"HK.00700","profile":"focused"}`), 1, func(wsmsg.AckMsg) {})
	d := dem.ensured[0].d
	for _, s := range d.Subs {
		if s == feed.SubBook {
			t.Fatal("HK focused must NOT include SubBook (LV1 entitlement)")
		}
	}
}

func TestEnsureSymbol_InterestNoSubs(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p4","symbol":"US.T","profile":"interest"}`), 1, func(wsmsg.AckMsg) {})
	d := dem.ensured[0].d
	if len(d.Subs) != 0 {
		t.Fatalf("interest must have no subs, got %v", d.Subs)
	}
	if d.WantsHistory {
		t.Fatalf("interest must not want history (lightweight news-rotation profile)")
	}
}

func TestEnsureSymbol_RejectsBadMarket(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	ack, _ := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p5","symbol":"XX.FOO","profile":"watch"}`), 1, func(wsmsg.AckMsg) {})
	if ack.Status != "blocked" || len(dem.ensured) != 0 {
		t.Fatalf("want blocked+no-ensure, got %q ensured=%d", ack.Status, len(dem.ensured))
	}
}

func TestEnsureSymbol_UnknownSymbolReverts(t *testing.T) {
	cd, dem, _ := newCmdWith(t, feed.ErrUnknownSymbol, false)
	ack, _ := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p6","symbol":"US.ZZZZQQ","profile":"watch"}`), 1, func(wsmsg.AckMsg) {})
	if ack.Status != "blocked" || len(dem.ensured) != 0 {
		t.Fatalf("unknown symbol must block and not ensure: %q ensured=%d", ack.Status, len(dem.ensured))
	}
	if ack.Reason == "" {
		t.Fatal("expected a reason mentioning the symbol")
	}
}

func TestEnsureSymbol_ArchivedSymbolSkipsLiveProbe(t *testing.T) {
	cd, _, sf := newCmdWith(t, nil, false)
	sf.err = feed.ErrFeedUnavailable
	cd.knownSymbol.Store(&knownSymbolBox{fn: func(symbol string) bool { return symbol == "US.NVDA" }})
	ack, _ := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p1","symbol":"US.NVDA","profile":"watch"}`), 1, func(wsmsg.AckMsg) {})
	if ack.Status != wsmsg.AckAccepted {
		t.Fatalf("archived symbol should skip unavailable live probe: %+v", ack)
	}
}

func TestEnsureSymbol_FeedUnavailableBlocks(t *testing.T) {
	cd, _, _ := newCmdWith(t, feed.ErrFeedUnavailable, false)
	ack, _ := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p7","symbol":"US.AAPL","profile":"watch"}`), 1, func(wsmsg.AckMsg) {})
	if ack.Status != "blocked" {
		t.Fatalf("want blocked, got %q", ack.Status)
	}
}

func TestEnsureSymbol_NilFeedAcceptsNoProbe(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, true) // feed getter returns nil (replay)
	ack, _ := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p8","symbol":"US.AAPL","profile":"watch"}`), 1, func(wsmsg.AckMsg) {})
	if ack.Status != "accepted" || len(dem.ensured) != 1 {
		t.Fatalf("replay must accept and still track: %q ensured=%d", ack.Status, len(dem.ensured))
	}
}

func TestReleaseSymbol_NamespacedAlwaysAccepted(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	ack, _ := cd.handle(context.Background(), "ReleaseSymbol", []byte(`{"demandId":"p1"}`), 7, func(wsmsg.AckMsg) {})
	if ack.Status != "accepted" {
		t.Fatalf("release status = %q", ack.Status)
	}
	if len(dem.released) != 1 || dem.released[0].conn != 7 || dem.released[0].id != "dyn/7/p1" {
		t.Fatalf("release = %+v", dem.released)
	}
}

func TestFocusGroup_ProbesAndAcks(t *testing.T) {
	cd, _, _ := newCmdWith(t, nil, false)
	ack, _ := cd.handle(context.Background(), "FocusGroup", []byte(`{"group":"blue","symbol":"US.AAPL"}`), 1, func(wsmsg.AckMsg) {})
	if ack.Status != "accepted" {
		t.Fatalf("focus ack = %q", ack.Status)
	}
	cd2, _, _ := newCmdWith(t, feed.ErrUnknownSymbol, false)
	if ack, _ := cd2.handle(context.Background(), "FocusGroup", []byte(`{"group":"blue","symbol":"US.ZZZZQQ"}`), 1, func(wsmsg.AckMsg) {}); ack.Status != "blocked" {
		t.Fatalf("bad focus symbol must block, got %q", ack.Status)
	}
}

type spyVenueAdmin struct {
	setCalled       bool
	putCalled       bool
	delErr          error
	setErr          error
	lastPutSec      string
	moomooAttempted bool
}

func (s *spyVenueAdmin) GetVenueSetup() (config.VenueConfig, config.VenueConfig, []string, bool, error) {
	return config.VenueConfig{Venues: []config.Venue{{ID: "file-v", Broker: "sim", Env: "paper"}}},
		config.VenueConfig{Venues: []config.Venue{{ID: "run-v", Broker: "sim", Env: "paper"}}},
		[]string{"alpaca"}, s.moomooAttempted, nil
}
func (s *spyVenueAdmin) SetVenueSetup(config.VenueConfig) error { s.setCalled = true; return s.setErr }
func (s *spyVenueAdmin) PutCredential(_, _, sec string) error {
	s.putCalled = true
	s.lastPutSec = sec
	return nil
}
func (s *spyVenueAdmin) DeleteCredential(string) error { return s.delErr }

// spyVenueTester is the venueTester test double: it records whether it was
// called and with what args, and returns a caller-configured Result.
type spyVenueTester struct {
	result                                             venueprobe.Result
	called                                             bool
	broker, env, credName, keyID, secretKey, accountID string
}

func (s *spyVenueTester) TestConnection(_ context.Context, broker, env, credName, keyID, secretKey, accountID string) venueprobe.Result {
	s.called = true
	s.broker, s.env, s.credName, s.keyID, s.secretKey, s.accountID = broker, env, credName, keyID, secretKey, accountID
	return s.result
}

func TestCommandsStartDemoBlockedWithNoHandler(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	ack, deferred := cd.handle(context.Background(), "StartDemo", mustJSON(t, wsmsg.StartDemoArgs{}), 0, func(wsmsg.AckMsg) {})
	if deferred {
		t.Fatal("StartDemo must not be deferred")
	}
	if ack.Status != wsmsg.AckBlocked {
		t.Fatalf("StartDemo with no handler: status = %q, want blocked", ack.Status)
	}
}

func TestCommandsStartDemoDispatchesAndAcks(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	hit := false
	cd.startDemo = func() error { hit = true; return nil }
	ack, _ := cd.handle(context.Background(), "StartDemo", mustJSON(t, wsmsg.StartDemoArgs{}), 0, func(wsmsg.AckMsg) {})
	if !hit || ack.Status != wsmsg.AckAccepted {
		t.Fatalf("StartDemo not dispatched: hit=%v ack=%+v", hit, ack)
	}
}

func TestCommandsStartDemoBlockedOnHandlerError(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.startDemo = func() error { return errors.New("create demo temp dir: boom") }
	ack, _ := cd.handle(context.Background(), "StartDemo", mustJSON(t, wsmsg.StartDemoArgs{}), 0, func(wsmsg.AckMsg) {})
	if ack.Status != wsmsg.AckBlocked {
		t.Fatalf("want blocked, got %+v", ack)
	}
}
