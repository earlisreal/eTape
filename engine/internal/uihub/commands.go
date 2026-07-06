// commands.go dispatches inbound WS "command" frames (SubmitOrder, Arm,
// GetConfig, SubscribeIndicator, ...) onto the exec.Core / store.Store /
// md.Core surfaces this package depends on only through the narrow execDoer,
// configStore, and indicatorCtl interfaces, so it stays testable with spies.
package uihub

import (
	"encoding/json"

	"github.com/earlisreal/eTape/engine/internal/exec"
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

type commands struct {
	ex  execDoer
	cfg configStore
	ind indicatorCtl
}

func newCommands(ex execDoer, cfg configStore, ind indicatorCtl) *commands {
	return &commands{ex: ex, cfg: cfg, ind: ind}
}

func blocked(reason string) wsmsg.AckMsg { return wsmsg.AckMsg{Status: "blocked", Reason: reason} }

func ackFromCmd(a exec.CmdAck) wsmsg.AckMsg {
	status := wsmsg.AckStatus("accepted")
	if !a.Accepted {
		status = "blocked"
	}
	return wsmsg.AckMsg{Status: status, Reason: a.Reason, OrderID: a.OrderID}
}

func (cd *commands) handle(name string, args json.RawMessage) wsmsg.AckMsg {
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
	case "FocusGroup":
		// Link-group focus is UI-local (BroadcastChannel); the engine acks and no-ops.
		return wsmsg.AckMsg{Status: "accepted"}
	default:
		return blocked("unknown command: " + name)
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
