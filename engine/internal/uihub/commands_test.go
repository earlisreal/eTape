package uihub

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
	"github.com/earlisreal/eTape/engine/internal/venueprobe"
)

type spyExec struct {
	last exec.Command
	ack  exec.CmdAck
}

func (s *spyExec) Do(c exec.Command) exec.CmdAck { s.last = c; return s.ack }

type spyCfg struct {
	got    map[string]string
	values map[string]string
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

type spyInd struct{ ensured, released string }

func (s *spyInd) EnsureIndicator(id string, _ md.IndicatorSpec) { s.ensured = id }
func (s *spyInd) ReleaseIndicator(id string)                    { s.released = id }

func TestCommandsSubmitOrderMapsEnums(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true, OrderID: "ET5"}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	ack := cd.handle(context.Background(), "SubmitOrder", json.RawMessage(`{"venue":"sim","symbol":"US.AAPL","side":"SHORT","type":"STOP_LIMIT","tif":"GTC","qty":80,"limitPrice":3.55,"stopPrice":3.6}`), 0)
	if ack.Status != "accepted" || ack.OrderID != "ET5" {
		t.Fatalf("ack wrong: %+v", ack)
	}
	so, ok := ex.last.(exec.SubmitOrder)
	if !ok {
		t.Fatalf("expected exec.SubmitOrder, got %T", ex.last)
	}
	if so.Side != exec.SideShort || so.Type != exec.TypeStopLimit || so.TIF != exec.TIFGTC {
		t.Fatalf("enum parse wrong: %+v", so)
	}
	if so.Qty != 80 || so.LimitPrice != 3.55 || so.StopPrice != 3.6 || string(so.Venue) != "sim" {
		t.Fatalf("field copy wrong: %+v", so)
	}
}

func TestCommandsBlockedPassesReason(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: false, Reason: "R114 gate: max order value"}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	ack := cd.handle(context.Background(), "SubmitOrder", json.RawMessage(`{"venue":"sim","symbol":"US.AAPL","side":"BUY","type":"MARKET","tif":"DAY","qty":1}`), 0)
	if ack.Status != "blocked" || ack.Reason != "R114 gate: max order value" {
		t.Fatalf("blocked reason must pass through verbatim: %+v", ack)
	}
}

func TestCommandsKillSwitchAllVenues(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.handle(context.Background(), "KillSwitch", json.RawMessage(`{}`), 0) // no venue => all
	ks, ok := ex.last.(exec.KillSwitch)
	if !ok || ks.Venue != "" {
		t.Fatalf("KillSwitch{} => all venues (empty VenueID), got %T %+v", ex.last, ex.last)
	}
}

func TestCommandsArmMaster(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.handle(context.Background(), "Arm", json.RawMessage(`{}`), 0)
	if _, ok := ex.last.(exec.Arm); !ok {
		t.Fatalf("expected exec.Arm, got %T", ex.last)
	}
}

func TestCommandsResetBalanceDispatch(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.handle(context.Background(), "ResetBalance", json.RawMessage(`{"venue":"sim-1"}`), 0)
	rb, ok := ex.last.(exec.ResetBalance)
	if !ok || rb.Venue != "sim-1" {
		t.Fatalf("expected exec.ResetBalance{Venue: sim-1}, got %T %+v", ex.last, ex.last)
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

func TestCommandsGetSetConfig(t *testing.T) {
	cfg := &spyCfg{values: map[string]string{"theme": `"dark"`}}
	cd := newCommands(&spyExec{}, cfg, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	get := cd.handle(context.Background(), "GetConfig", json.RawMessage(`{"key":"theme"}`), 0)
	if get.Status != "accepted" {
		t.Fatalf("GetConfig should accept: %+v", get)
	}
	raw, ok := get.Value.(json.RawMessage)
	if !ok || string(raw) != `"dark"` {
		t.Fatalf("GetConfig must return stored JSON value verbatim: %v", get.Value)
	}
	set := cd.handle(context.Background(), "SetConfig", json.RawMessage(`{"key":"theme","value":"light"}`), 0)
	if set.Status != "accepted" || cfg.got["theme"] != `"light"` {
		t.Fatalf("SetConfig must persist raw JSON value: %+v / %v", set, cfg.got)
	}
}

func TestCommandsIndicatorLifecycle(t *testing.T) {
	ind := &spyInd{}
	cd := newCommands(&spyExec{}, &spyCfg{}, ind, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	cd.handle(context.Background(), "SubscribeIndicator", json.RawMessage(`{"instanceId":"i1","symbol":"US.AAPL","timeframe":"1m","type":"VWAP","params":{}}`), 0)
	if ind.ensured != "i1" {
		t.Fatalf("SubscribeIndicator should EnsureIndicator, got %q", ind.ensured)
	}
	cd.handle(context.Background(), "UnsubscribeIndicator", json.RawMessage(`{"instanceId":"i1"}`), 0)
	if ind.released != "i1" {
		t.Fatalf("UnsubscribeIndicator should ReleaseIndicator, got %q", ind.released)
	}
}

func TestCommandsUnknown(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, &spyVenueTester{})
	ack := cd.handle(context.Background(), "Nope", json.RawMessage(`{}`), 0)
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
	ack := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p1","symbol":"US.AAPL","profile":"watch"}`), 7)
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
	if !reflect.DeepEqual(got.d.Subs, []feed.SubType{feed.SubTicker, feed.SubKL1m}) {
		t.Fatalf("watch subs = %v", got.d.Subs)
	}
	if !got.d.WantsHistory {
		t.Fatalf("watch must want history (chart-capable, backs the deep-backfill trigger)")
	}
}

func TestEnsureSymbol_FocusedUSHasBook(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p2","symbol":"US.NVDA","profile":"focused"}`), 1)
	d := dem.ensured[0].d
	if !d.Focused {
		t.Fatal("focused flag missing")
	}
	if !reflect.DeepEqual(d.Subs, []feed.SubType{feed.SubQuote, feed.SubTicker, feed.SubKL1m, feed.SubBook}) {
		t.Fatalf("US focused subs = %v (want quote,ticker,kl1m,book)", d.Subs)
	}
	if !d.WantsHistory {
		t.Fatalf("focused must want history (chart-capable, backs the deep-backfill trigger)")
	}
}

func TestEnsureSymbol_FocusedHKNoBook(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p3","symbol":"HK.00700","profile":"focused"}`), 1)
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
		[]byte(`{"demandId":"p4","symbol":"US.T","profile":"interest"}`), 1)
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
	ack := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p5","symbol":"XX.FOO","profile":"watch"}`), 1)
	if ack.Status != "blocked" || len(dem.ensured) != 0 {
		t.Fatalf("want blocked+no-ensure, got %q ensured=%d", ack.Status, len(dem.ensured))
	}
}

func TestEnsureSymbol_UnknownSymbolReverts(t *testing.T) {
	cd, dem, _ := newCmdWith(t, feed.ErrUnknownSymbol, false)
	ack := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p6","symbol":"US.ZZZZQQ","profile":"watch"}`), 1)
	if ack.Status != "blocked" || len(dem.ensured) != 0 {
		t.Fatalf("unknown symbol must block and not ensure: %q ensured=%d", ack.Status, len(dem.ensured))
	}
	if ack.Reason == "" {
		t.Fatal("expected a reason mentioning the symbol")
	}
}

func TestEnsureSymbol_FeedUnavailableBlocks(t *testing.T) {
	cd, _, _ := newCmdWith(t, feed.ErrFeedUnavailable, false)
	ack := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p7","symbol":"US.AAPL","profile":"watch"}`), 1)
	if ack.Status != "blocked" {
		t.Fatalf("want blocked, got %q", ack.Status)
	}
}

func TestEnsureSymbol_NilFeedAcceptsNoProbe(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, true) // feed getter returns nil (replay)
	ack := cd.handle(context.Background(), "EnsureSymbol",
		[]byte(`{"demandId":"p8","symbol":"US.AAPL","profile":"watch"}`), 1)
	if ack.Status != "accepted" || len(dem.ensured) != 1 {
		t.Fatalf("replay must accept and still track: %q ensured=%d", ack.Status, len(dem.ensured))
	}
}

func TestReleaseSymbol_NamespacedAlwaysAccepted(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	ack := cd.handle(context.Background(), "ReleaseSymbol", []byte(`{"demandId":"p1"}`), 7)
	if ack.Status != "accepted" {
		t.Fatalf("release status = %q", ack.Status)
	}
	if len(dem.released) != 1 || dem.released[0].conn != 7 || dem.released[0].id != "dyn/7/p1" {
		t.Fatalf("release = %+v", dem.released)
	}
}

func TestFocusGroup_ProbesAndAcks(t *testing.T) {
	cd, _, _ := newCmdWith(t, nil, false)
	ack := cd.handle(context.Background(), "FocusGroup", []byte(`{"group":"blue","symbol":"US.AAPL"}`), 1)
	if ack.Status != "accepted" {
		t.Fatalf("focus ack = %q", ack.Status)
	}
	cd2, _, _ := newCmdWith(t, feed.ErrUnknownSymbol, false)
	if ack := cd2.handle(context.Background(), "FocusGroup", []byte(`{"group":"blue","symbol":"US.ZZZZQQ"}`), 1); ack.Status != "blocked" {
		t.Fatalf("bad focus symbol must block, got %q", ack.Status)
	}
}

type spyVenueAdmin struct {
	setCalled  bool
	putCalled  bool
	delErr     error
	setErr     error
	lastPutSec string
}

func (s *spyVenueAdmin) GetVenueSetup() (config.VenueConfig, config.VenueConfig, []string, error) {
	return config.VenueConfig{Venues: []config.Venue{{ID: "file-v", Broker: "sim", Env: "paper"}}},
		config.VenueConfig{Venues: []config.Venue{{ID: "run-v", Broker: "sim", Env: "paper"}}},
		[]string{"alpaca"}, nil
}
func (s *spyVenueAdmin) SetVenueSetup(config.VenueConfig) error { s.setCalled = true; return s.setErr }
func (s *spyVenueAdmin) PutCredential(_, _, sec string) error {
	s.putCalled = true
	s.lastPutSec = sec
	return nil
}
func (s *spyVenueAdmin) DeleteCredential(string) error { return s.delErr }

func TestGetVenueSetupResultHasNoSecrets(t *testing.T) {
	va := &spyVenueAdmin{}
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, va, func() Feed { return nil }, &spyVenueTester{})
	ack := cd.handle(context.Background(), "GetVenueSetup", json.RawMessage(`{}`), 0)
	if ack.Status != "accepted" {
		t.Fatalf("status %v", ack.Status)
	}
	b, _ := json.Marshal(ack)
	if strings.Contains(string(b), "secretKey") || strings.Contains(string(b), "keyId") {
		t.Fatalf("setup result leaked secret material: %s", b)
	}
}

func TestSetVenueSetupBlocksOnError(t *testing.T) {
	va := &spyVenueAdmin{setErr: errors.New("venue \"x\": env \"demo\" must be paper or live")}
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, va, func() Feed { return nil }, &spyVenueTester{})
	ack := cd.handle(context.Background(), "SetVenueSetup", json.RawMessage(`{"venues":[],"gate":{"global":{},"venue":{}}}`), 0)
	if ack.Status != "blocked" || ack.Reason == "" {
		t.Fatalf("want blocked with reason, got %+v", ack)
	}
}

func TestPutCredentialRequiresAllFields(t *testing.T) {
	va := &spyVenueAdmin{}
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, va, func() Feed { return nil }, &spyVenueTester{})
	ack := cd.handle(context.Background(), "PutCredential", json.RawMessage(`{"name":"a","keyId":"","secretKey":"s"}`), 0)
	if ack.Status != "blocked" || va.putCalled {
		t.Fatalf("empty keyId must block before calling admin: %+v", ack)
	}
}

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

func TestTestConnectionBadArgsBlocksWithoutCallingProbe(t *testing.T) {
	vt := &spyVenueTester{}
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, vt)
	ack := cd.handle(context.Background(), "TestConnection", json.RawMessage(`not json`), 0)
	if ack.Status != "blocked" || ack.Reason != "bad args" {
		t.Fatalf("want blocked/\"bad args\", got %+v", ack)
	}
	if vt.called {
		t.Fatal("malformed args must never reach the probe")
	}
}

func TestTestConnectionAcceptsAndConvertsResult(t *testing.T) {
	vt := &spyVenueTester{result: venueprobe.Result{
		OK: true, Env: "live", AccountID: "2TZ1", AccountType: "Live", Message: "",
		Accounts: []venueprobe.Account{{AccountID: "2TZ2", AccountType: "Paper", Env: "paper"}},
	}}
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, vt)
	args := `{"broker":"tradezero","env":"live","credentials":"my-cred","keyId":"key-123","secretKey":"secret-456","accountId":"acct-789"}`
	ack := cd.handle(context.Background(), "TestConnection", json.RawMessage(args), 0)

	if ack.Status != "accepted" {
		t.Fatalf("want accepted, got %+v", ack)
	}
	if !vt.called {
		t.Fatal("valid args must reach the probe")
	}
	if vt.broker != "tradezero" || vt.env != "live" || vt.credName != "my-cred" ||
		vt.keyID != "key-123" || vt.secretKey != "secret-456" || vt.accountID != "acct-789" {
		t.Fatalf("probe args wrong: %+v", vt)
	}

	res, ok := ack.Value.(wsmsg.TestConnectionResult)
	if !ok {
		t.Fatalf("ack.Value must be wsmsg.TestConnectionResult, got %T", ack.Value)
	}
	if !res.OK || res.Env != "live" || res.AccountID != "2TZ1" || res.AccountType != "Live" || res.Message != "" {
		t.Fatalf("top-level fields not carried through: %+v", res)
	}
	if len(res.Accounts) != 1 {
		t.Fatalf("want 1 account, got %d: %+v", len(res.Accounts), res.Accounts)
	}
	acct := res.Accounts[0]
	if acct.AccountID != "2TZ2" || acct.AccountType != "Paper" || acct.Env != "paper" {
		t.Fatalf("nested account fields wrong: %+v", acct)
	}
}

func TestTestConnectionOKFalseStillAccepted(t *testing.T) {
	vt := &spyVenueTester{result: venueprobe.Result{OK: false, Message: "bad key"}}
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{}, &spyDemandCtl{}, &spyVenueAdmin{}, func() Feed { return nil }, vt)
	ack := cd.handle(context.Background(), "TestConnection", json.RawMessage(`{"broker":"alpaca","env":"paper"}`), 0)
	if ack.Status != "accepted" {
		t.Fatalf("a transport-successful probe with OK:false must still be accepted, got %+v", ack)
	}
	res, ok := ack.Value.(wsmsg.TestConnectionResult)
	if !ok || res.OK || res.Message != "bad key" {
		t.Fatalf("result must carry OK:false through: %+v (%T)", ack.Value, ack.Value)
	}
}
