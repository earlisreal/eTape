// Package moomoo is eTape's third execution-venue adapter, translating the
// broker-agnostic exec domain to/from moomoo OpenD's trade protobuf wire
// format (package trdcommon/common under feed/opend/pb). This file holds only
// pure translation: no I/O, no network calls -- the trdClient (a later task)
// calls these.
package moomoo

import (
	"fmt"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/common"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
	"github.com/earlisreal/eTape/engine/internal/session"
	"google.golang.org/protobuf/proto"
)

// wireSymbol strips eTape's "US." market-prefix convention so outbound
// requests carry the bare ticker moomoo's trade API expects (e.g. "AAPL",
// not "US.AAPL" -- the SDK itself strips "US." before sending `code`, per
// docs/2026-07-04-moomoo-trading-api.md).
func wireSymbol(symbol string) string {
	return strings.TrimPrefix(symbol, "US.")
}

// domainSymbol re-adds the "US." prefix to a bare ticker moomoo's trade API
// returns, so it matches the domain convention the rest of eTape keys
// Order/Position/Fill.Symbol by. moomoo is used here as a US-only execution
// venue (CLAUDE.md), so this is always correct.
func domainSymbol(symbol string) string {
	return "US." + symbol
}

// orderTypeWire maps eTape's domain OrderType to moomoo's trdcommon.OrderType.
// moomoo has no constant literally named "Limit" -- OrderType_OrderType_Normal
// (1) IS moomoo's limit order for US stocks.
func orderTypeWire(t exec.OrderType) (trdcommon.OrderType, error) {
	switch t {
	case exec.TypeMarket:
		return trdcommon.OrderType_OrderType_Market, nil
	case exec.TypeLimit:
		return trdcommon.OrderType_OrderType_Normal, nil
	case exec.TypeStop:
		return trdcommon.OrderType_OrderType_Stop, nil
	case exec.TypeStopLimit:
		return trdcommon.OrderType_OrderType_StopLimit, nil
	default:
		return trdcommon.OrderType_OrderType_Unknown, fmt.Errorf("moomoo: unsupported order type %v", t)
	}
}

// sideWire maps a trader action to moomoo's TrdSide, sending ONLY Buy or
// Sell -- moomoo's own proto comment is explicit that the client should
// never send SellShort/BuyBack ("客户端下单只传Buy或Sell即可"). This mirrors
// Alpaca's sideWire (alpaca/mapping.go), which also only ever sends
// "buy"/"sell" and lets the real Short/Cover distinction ride back in on the
// inbound side rather than the outbound one.
func sideWire(s exec.Side) trdcommon.TrdSide {
	switch s {
	case exec.SideBuy, exec.SideCover:
		return trdcommon.TrdSide_TrdSide_Buy
	default: // Sell, Short
		return trdcommon.TrdSide_TrdSide_Sell
	}
}

// sideDomain un-enriches a moomoo-reported TrdSide into eTape's domain Side.
// Unlike TradeZero's sideDomain (which needs a second openClose field) or
// Alpaca's sideDomain (which needs pre-fill position sign to infer
// Short/Cover), moomoo's server enriches Short/Cover into distinct wire
// values (SellShort=3/BuyBack=4) on US orders, so no extra context is
// needed here -- this is a direct, simpler un-enrichment.
func sideDomain(wireSide trdcommon.TrdSide) exec.Side {
	switch wireSide {
	case trdcommon.TrdSide_TrdSide_Buy:
		return exec.SideBuy
	case trdcommon.TrdSide_TrdSide_Sell:
		return exec.SideSell
	case trdcommon.TrdSide_TrdSide_SellShort:
		return exec.SideShort
	case trdcommon.TrdSide_TrdSide_BuyBack:
		return exec.SideCover
	default: // TrdSide_Unknown: not expected on an order side.
		return exec.SideBuy
	}
}

// tifWire maps eTape's domain TIF to moomoo's trdcommon.TimeInForce.
func tifWire(t exec.TIF) (trdcommon.TimeInForce, error) {
	switch t {
	case exec.TIFDay:
		return trdcommon.TimeInForce_TimeInForce_DAY, nil
	case exec.TIFGTC:
		return trdcommon.TimeInForce_TimeInForce_GTC, nil
	case exec.TIFIOC:
		// Documented (docs/2026-07-04-moomoo-trading-api.md): moomoo's IOC is
		// usable for crypto market orders only, not reliable for US stock
		// orders (eTape's only market, per CLAUDE.md). Reject here rather than
		// risk an order moomoo would bounce or mishandle -- mirrors Alpaca's
		// tifWire (alpaca/mapping.go), which rejects a TIF it knows the broker
		// will reject rather than send it and hope.
		return 0, fmt.Errorf("moomoo: TIF %v is documented for crypto market orders only, not reliable for US stock orders", t)
	case exec.TIFFOK:
		// moomoo's TimeInForce has no FOK equivalent at all (DAY/GTC/IOC/GTD only).
		return 0, fmt.Errorf("moomoo: unsupported TIF %v (no moomoo equivalent)", t)
	default:
		return 0, fmt.Errorf("moomoo: unsupported TIF %v", t)
	}
}

// sessionWire resolves req's session choice to moomoo's Common.Session wire
// enum. This is moomoo's analog of Alpaca's isExtendedHours/extendedHoursFor
// pair and TradeZero's isExtendedHours/extendedHoursFor pair (see both
// packages' mapping.go): the same clock-driven AUTO-resolution idea, but
// resolving to a 3-way enum choice (RTH/ETH/OVERNIGHT) instead of a single
// boolean extended_hours flag, because moomoo's wire session type is itself a
// 4-way (+NONE) enum rather than a bool. exec.ExtendedHoursFor's boolean
// signature doesn't fit this shape, so this is written directly rather than
// force-fit onto it.
//
// Session_Session_NONE and Session_Session_ALL are never produced here:
// NONE has no meaning for an explicit order and ALL (24-hour) has no
// equivalent in exec.OrderSession -- out of scope (task brief).
func sessionWire(s exec.OrderSession, clk clock.Clock) common.Session {
	switch s {
	case exec.SessionRTH:
		return common.Session_Session_RTH
	case exec.SessionExtended:
		return common.Session_Session_ETH
	case exec.SessionOvernight:
		// Reachable only when moomoo's Capabilities().OvernightSession is true
		// (a later task) -- Core.handleSubmit gates an explicit Overnight order
		// on that before this adapter is ever called.
		return common.Session_Session_OVERNIGHT
	default: // SessionAuto
		switch session.PhaseAt(clk.Now()) {
		case session.PreMarket, session.PostMarket:
			return common.Session_Session_ETH
		case session.Overnight:
			return common.Session_Session_OVERNIGHT
		default: // RTH, and Closed defensively
			return common.Session_Session_RTH
		}
	}
}

// statusDomain maps a moomoo OrderStatus to eTape's domain OrderStatus. ok
// reports whether the mapping is safe to trust: for
// OrderStatus_OrderStatus_TimeOut ("处理超时，结果未知" -- processing timed
// out, result unknown, must be reconciled by re-querying the order),
// OrderStatus_OrderStatus_FillCancelled (a rare fill-correction rollback),
// and OrderStatus_OrderStatus_Unknown, ok is false and the returned status is
// the zero value -- forcing any of these through a definite terminal domain
// status would assert something eTape hasn't actually confirmed. The caller
// (normalize.go, a later task) must special-case these three by inspecting
// the raw moomoo status directly rather than trusting this function blindly.
func statusDomain(s trdcommon.OrderStatus) (exec.OrderStatus, bool) {
	switch s {
	case trdcommon.OrderStatus_OrderStatus_Unsubmitted,
		trdcommon.OrderStatus_OrderStatus_WaitingSubmit,
		trdcommon.OrderStatus_OrderStatus_Submitting:
		return exec.StatusSubmitted, true
	case trdcommon.OrderStatus_OrderStatus_Submitted:
		return exec.StatusAccepted, true
	case trdcommon.OrderStatus_OrderStatus_Filled_Part,
		trdcommon.OrderStatus_OrderStatus_Cancelling_Part:
		// Cancelling_Part means a partial fill exists and the remainder is
		// being cancelled -- from the trader's point of view the order is
		// still (at least) partially filled, so it shares StatusPartiallyFilled
		// rather than jumping straight to Canceled.
		return exec.StatusPartiallyFilled, true
	case trdcommon.OrderStatus_OrderStatus_Filled_All:
		return exec.StatusFilled, true
	case trdcommon.OrderStatus_OrderStatus_Cancelling_All,
		trdcommon.OrderStatus_OrderStatus_Cancelled_Part,
		trdcommon.OrderStatus_OrderStatus_Cancelled_All,
		trdcommon.OrderStatus_OrderStatus_Deleted:
		// Cancelled_Part (partial fill, remainder cancelled) still lands on
		// Canceled: unlike Cancelling_Part, the cancellation has already
		// completed here, so there is no more working quantity -- Canceled is
		// the correct terminal status even though some quantity did fill.
		return exec.StatusCanceled, true
	case trdcommon.OrderStatus_OrderStatus_SubmitFailed,
		trdcommon.OrderStatus_OrderStatus_Failed,
		trdcommon.OrderStatus_OrderStatus_Disabled:
		return exec.StatusRejected, true
	default:
		return 0, false
	}
}

// trdHeader builds the TrdHeader every moomoo trade request carries. env is
// eTape's config string ("live" -> TrdEnv_Real, anything else ->
// TrdEnv_Simulate). accID is carried as uint64 end to end, never through a
// float (a documented gotcha -- float64 cannot losslessly represent all
// uint64 account IDs). TrdMarket is always US: moomoo is used here as a
// US-only execution venue (CLAUDE.md).
func trdHeader(accID uint64, env string) *trdcommon.TrdHeader {
	trdEnv := trdcommon.TrdEnv_TrdEnv_Simulate
	if env == "live" {
		trdEnv = trdcommon.TrdEnv_TrdEnv_Real
	}
	return &trdcommon.TrdHeader{
		TrdEnv:    proto.Int32(int32(trdEnv)),
		AccID:     proto.Uint64(accID),
		TrdMarket: proto.Int32(int32(trdcommon.TrdMarket_TrdMarket_US)),
	}
}

// secMarket is always TrdSecMarket_US: moomoo is used here as a US-only
// execution venue (CLAUDE.md).
func secMarket() trdcommon.TrdSecMarket {
	return trdcommon.TrdSecMarket_TrdSecMarket_US
}

// packetID builds the PacketID (connection ID + per-connection serial number)
// every moomoo request/response pair is correlated by.
func packetID(connID uint64, serialNo uint32) *common.PacketID {
	return &common.PacketID{
		ConnID:   proto.Uint64(connID),
		SerialNo: proto.Uint32(serialNo),
	}
}
