package moomoo

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/common"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/trdcommon"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// TestWireSymbol_StripsUSPrefix covers the BRK.B gotcha: a US ticker can
// itself contain a dot, so stripping must match the literal "US." prefix,
// not trim on the first dot found anywhere in the string.
func TestWireSymbol_StripsUSPrefix(t *testing.T) {
	cases := map[string]string{
		"US.AAPL": "AAPL", "US.BRK.B": "BRK.B", "AAPL": "AAPL",
	}
	for in, want := range cases {
		if got := wireSymbol(in); got != want {
			t.Errorf("wireSymbol(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDomainSymbol_AddsUSPrefix(t *testing.T) {
	if got := domainSymbol("AAPL"); got != "US.AAPL" {
		t.Fatalf("domainSymbol(AAPL) = %q, want US.AAPL", got)
	}
	if got := domainSymbol("BRK.B"); got != "US.BRK.B" {
		t.Fatalf("domainSymbol(BRK.B) = %q, want US.BRK.B", got)
	}
}

func TestOrderTypeWire(t *testing.T) {
	cases := map[exec.OrderType]trdcommon.OrderType{
		exec.TypeMarket:    trdcommon.OrderType_OrderType_Market,
		exec.TypeLimit:     trdcommon.OrderType_OrderType_Normal,
		exec.TypeStop:      trdcommon.OrderType_OrderType_Stop,
		exec.TypeStopLimit: trdcommon.OrderType_OrderType_StopLimit,
	}
	for ot, want := range cases {
		got, err := orderTypeWire(ot)
		if err != nil || got != want {
			t.Errorf("orderTypeWire(%v) = %v,%v want %v", ot, got, err, want)
		}
	}
	if _, err := orderTypeWire(exec.OrderType(99)); err == nil {
		t.Fatal("unsupported order type should error")
	}
}

// TestSideWire covers moomoo's rule that the client only ever sends Buy or
// Sell (never SellShort/BuyBack) -- the real Short/Cover distinction rides
// back in on the inbound side, unlike Alpaca/TradeZero which need extra
// context (position sign / openClose) to recover it.
func TestSideWire(t *testing.T) {
	if got := sideWire(exec.SideBuy); got != trdcommon.TrdSide_TrdSide_Buy {
		t.Errorf("SideBuy -> %v, want Buy", got)
	}
	if got := sideWire(exec.SideCover); got != trdcommon.TrdSide_TrdSide_Buy {
		t.Errorf("SideCover -> %v, want Buy", got)
	}
	if got := sideWire(exec.SideSell); got != trdcommon.TrdSide_TrdSide_Sell {
		t.Errorf("SideSell -> %v, want Sell", got)
	}
	if got := sideWire(exec.SideShort); got != trdcommon.TrdSide_TrdSide_Sell {
		t.Errorf("SideShort -> %v, want Sell", got)
	}
}

// TestSideDomain covers un-enriching all four possible inbound TrdSide
// values moomoo's server may report on a US order -- no second field (like
// TradeZero's openClose) is needed because moomoo enriches Short/Cover into
// distinct wire values on the way back.
func TestSideDomain(t *testing.T) {
	cases := map[trdcommon.TrdSide]exec.Side{
		trdcommon.TrdSide_TrdSide_Buy:       exec.SideBuy,
		trdcommon.TrdSide_TrdSide_Sell:      exec.SideSell,
		trdcommon.TrdSide_TrdSide_SellShort: exec.SideShort,
		trdcommon.TrdSide_TrdSide_BuyBack:   exec.SideCover,
	}
	for wire, want := range cases {
		if got := sideDomain(wire); got != want {
			t.Errorf("sideDomain(%v) = %v, want %v", wire, got, want)
		}
	}
}

func TestTifWire(t *testing.T) {
	if got, err := tifWire(exec.TIFDay); err != nil || got != trdcommon.TimeInForce_TimeInForce_DAY {
		t.Fatalf("TIFDay -> %v, %v; want DAY, nil", got, err)
	}
	if got, err := tifWire(exec.TIFGTC); err != nil || got != trdcommon.TimeInForce_TimeInForce_GTC {
		t.Fatalf("TIFGTC -> %v, %v; want GTC, nil", got, err)
	}
	// Documented (docs/2026-07-04-moomoo-trading-api.md): moomoo's IOC is
	// usable for crypto market orders only -- not reliable for US stock
	// orders, eTape's only market -- so IOC must still error.
	if _, err := tifWire(exec.TIFIOC); err == nil {
		t.Fatal("TIFIOC should error (crypto-only, unsupported for US stocks)")
	}
	// FOK has no moomoo equivalent at all.
	if _, err := tifWire(exec.TIFFOK); err == nil {
		t.Fatal("TIFFOK should error (no moomoo equivalent)")
	}
}

// TestSessionWire_ExplicitSessionOverridesClock covers the trader's explicit
// session choice taking priority over the clock, mirroring
// TestExtendedHoursFor_ExplicitSessionOverridesClock in the TradeZero/Alpaca
// mapping tests -- except sessionWire resolves to moomoo's 3-way wire enum
// instead of a boolean.
func TestSessionWire_ExplicitSessionOverridesClock(t *testing.T) {
	rth := clock.NewFake(time.Date(2026, 7, 6, 10, 0, 0, 0, session.Loc())) // Monday RTH

	cases := []struct {
		name string
		s    exec.OrderSession
		want common.Session
	}{
		{"explicit RTH", exec.SessionRTH, common.Session_Session_RTH},
		{"explicit Extended", exec.SessionExtended, common.Session_Session_ETH},
		{"explicit Overnight", exec.SessionOvernight, common.Session_Session_OVERNIGHT},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionWire(tc.s, rth); got != tc.want {
				t.Errorf("sessionWire(%v, RTH clock) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// TestSessionWire_AutoResolvesViaClock covers SessionAuto's clock-driven
// resolution across all four session.Phase values it can land in.
func TestSessionWire_AutoResolvesViaClock(t *testing.T) {
	et := func(hour, min int) time.Time {
		return time.Date(2026, 7, 6, hour, min, 0, 0, session.Loc()) // Monday
	}
	cases := []struct {
		name string
		t    time.Time
		want common.Session
	}{
		{"pre-market 08:00 ET", et(8, 0), common.Session_Session_ETH},
		{"RTH 10:00 ET", et(10, 0), common.Session_Session_RTH},
		{"post-market 18:00 ET", et(18, 0), common.Session_Session_ETH},
		{"overnight 22:00 ET", et(22, 0), common.Session_Session_OVERNIGHT},
		{"overnight 02:00 ET", et(2, 0), common.Session_Session_OVERNIGHT},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionWire(exec.SessionAuto, clock.NewFake(tc.t))
			if got != tc.want {
				t.Errorf("sessionWire(Auto, %v) = %v, want %v", tc.t, got, tc.want)
			}
		})
	}
}

func TestStatusDomain(t *testing.T) {
	trusted := map[trdcommon.OrderStatus]exec.OrderStatus{
		trdcommon.OrderStatus_OrderStatus_Unsubmitted:     exec.StatusSubmitted,
		trdcommon.OrderStatus_OrderStatus_WaitingSubmit:   exec.StatusSubmitted,
		trdcommon.OrderStatus_OrderStatus_Submitting:      exec.StatusSubmitted,
		trdcommon.OrderStatus_OrderStatus_Submitted:       exec.StatusAccepted,
		trdcommon.OrderStatus_OrderStatus_Filled_Part:     exec.StatusPartiallyFilled,
		trdcommon.OrderStatus_OrderStatus_Cancelling_Part: exec.StatusPartiallyFilled,
		trdcommon.OrderStatus_OrderStatus_Filled_All:      exec.StatusFilled,
		trdcommon.OrderStatus_OrderStatus_Cancelling_All:  exec.StatusCanceled,
		trdcommon.OrderStatus_OrderStatus_Cancelled_Part:  exec.StatusCanceled,
		trdcommon.OrderStatus_OrderStatus_Cancelled_All:   exec.StatusCanceled,
		trdcommon.OrderStatus_OrderStatus_Deleted:         exec.StatusCanceled,
		trdcommon.OrderStatus_OrderStatus_SubmitFailed:    exec.StatusRejected,
		trdcommon.OrderStatus_OrderStatus_Failed:          exec.StatusRejected,
		trdcommon.OrderStatus_OrderStatus_Disabled:        exec.StatusRejected,
	}
	for wire, want := range trusted {
		got, ok := statusDomain(wire)
		if !ok {
			t.Errorf("statusDomain(%v) ok = false, want true", wire)
		}
		if got != want {
			t.Errorf("statusDomain(%v) = %v, want %v", wire, got, want)
		}
	}

	// TimeOut ("result unknown"), FillCancelled (rare fill-correction), and
	// Unknown must NOT be forced through this mapping -- ok must be false so
	// a later caller (normalize.go) knows to special-case them.
	untrusted := []trdcommon.OrderStatus{
		trdcommon.OrderStatus_OrderStatus_TimeOut,
		trdcommon.OrderStatus_OrderStatus_FillCancelled,
		trdcommon.OrderStatus_OrderStatus_Unknown,
	}
	for _, wire := range untrusted {
		if _, ok := statusDomain(wire); ok {
			t.Errorf("statusDomain(%v) ok = true, want false (needs special-case handling)", wire)
		}
	}
}

func TestTrdHeader(t *testing.T) {
	h := trdHeader(12345, "live")
	if h.GetAccID() != 12345 {
		t.Errorf("AccID = %v, want 12345", h.GetAccID())
	}
	if h.GetTrdEnv() != int32(trdcommon.TrdEnv_TrdEnv_Real) {
		t.Errorf("TrdEnv = %v, want Real", h.GetTrdEnv())
	}
	if h.GetTrdMarket() != int32(trdcommon.TrdMarket_TrdMarket_US) {
		t.Errorf("TrdMarket = %v, want US", h.GetTrdMarket())
	}

	h = trdHeader(999, "paper")
	if h.GetTrdEnv() != int32(trdcommon.TrdEnv_TrdEnv_Simulate) {
		t.Errorf("TrdEnv = %v, want Simulate for non-live env", h.GetTrdEnv())
	}
}

func TestSecMarket(t *testing.T) {
	if got := secMarket(); got != trdcommon.TrdSecMarket_TrdSecMarket_US {
		t.Errorf("secMarket() = %v, want US", got)
	}
}

func TestPacketID(t *testing.T) {
	p := packetID(42, 7)
	if p.GetConnID() != 42 {
		t.Errorf("ConnID = %v, want 42", p.GetConnID())
	}
	if p.GetSerialNo() != 7 {
		t.Errorf("SerialNo = %v, want 7", p.GetSerialNo())
	}
}
