package uihub

import (
	"encoding/json"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
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
	cd := newCommands(ex, &spyCfg{}, &spyInd{})
	ack := cd.handle("SubmitOrder", json.RawMessage(`{"venue":"sim","symbol":"US.AAPL","side":"SHORT","type":"STOP_LIMIT","tif":"GTC","qty":80,"limitPrice":3.55,"stopPrice":3.6}`))
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
	cd := newCommands(ex, &spyCfg{}, &spyInd{})
	ack := cd.handle("SubmitOrder", json.RawMessage(`{"venue":"sim","symbol":"US.AAPL","side":"BUY","type":"MARKET","tif":"DAY","qty":1}`))
	if ack.Status != "blocked" || ack.Reason != "R114 gate: max order value" {
		t.Fatalf("blocked reason must pass through verbatim: %+v", ack)
	}
}

func TestCommandsKillSwitchAllVenues(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{})
	cd.handle("KillSwitch", json.RawMessage(`{}`)) // no venue => all
	ks, ok := ex.last.(exec.KillSwitch)
	if !ok || ks.Venue != "" {
		t.Fatalf("KillSwitch{} => all venues (empty VenueID), got %T %+v", ex.last, ex.last)
	}
}

func TestCommandsArmMaster(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{})
	cd.handle("Arm", json.RawMessage(`{}`))
	if _, ok := ex.last.(exec.Arm); !ok {
		t.Fatalf("expected exec.Arm, got %T", ex.last)
	}
}

func TestCommandsGetSetConfig(t *testing.T) {
	cfg := &spyCfg{values: map[string]string{"theme": `"dark"`}}
	cd := newCommands(&spyExec{}, cfg, &spyInd{})
	get := cd.handle("GetConfig", json.RawMessage(`{"key":"theme"}`))
	if get.Status != "accepted" {
		t.Fatalf("GetConfig should accept: %+v", get)
	}
	raw, ok := get.Value.(json.RawMessage)
	if !ok || string(raw) != `"dark"` {
		t.Fatalf("GetConfig must return stored JSON value verbatim: %v", get.Value)
	}
	set := cd.handle("SetConfig", json.RawMessage(`{"key":"theme","value":"light"}`))
	if set.Status != "accepted" || cfg.got["theme"] != `"light"` {
		t.Fatalf("SetConfig must persist raw JSON value: %+v / %v", set, cfg.got)
	}
}

func TestCommandsIndicatorLifecycle(t *testing.T) {
	ind := &spyInd{}
	cd := newCommands(&spyExec{}, &spyCfg{}, ind)
	cd.handle("SubscribeIndicator", json.RawMessage(`{"instanceId":"i1","symbol":"US.AAPL","timeframe":"1m","type":"VWAP","params":{}}`))
	if ind.ensured != "i1" {
		t.Fatalf("SubscribeIndicator should EnsureIndicator, got %q", ind.ensured)
	}
	cd.handle("UnsubscribeIndicator", json.RawMessage(`{"instanceId":"i1"}`))
	if ind.released != "i1" {
		t.Fatalf("UnsubscribeIndicator should ReleaseIndicator, got %q", ind.released)
	}
}

func TestCommandsUnknown(t *testing.T) {
	cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{})
	ack := cd.handle("Nope", json.RawMessage(`{}`))
	if ack.Status != "blocked" {
		t.Fatalf("unknown command must block, got %+v", ack)
	}
}
