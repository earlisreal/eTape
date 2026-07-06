package exec

import "testing"

func TestSideString(t *testing.T) {
	for s, want := range map[Side]string{SideBuy: "BUY", SideSell: "SELL", SideShort: "SHORT", SideCover: "COVER"} {
		if got := s.String(); got != want {
			t.Errorf("Side(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestStatusString(t *testing.T) {
	if StatusPartiallyFilled.String() != "PARTIALLY_FILLED" {
		t.Errorf("got %q", StatusPartiallyFilled.String())
	}
	if StatusBlocked.String() != "BLOCKED" {
		t.Errorf("got %q", StatusBlocked.String())
	}
}

func TestOrderRequestValidate(t *testing.T) {
	good := OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 100}
	if err := good.Validate(); err != nil {
		t.Fatalf("good.Validate() = %v, want nil", err)
	}
	bad := []OrderRequest{
		{Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 100},                // no venue
		{Venue: "sim-1", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 100},                // no symbol
		{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 0, LimitPrice: 100}, // qty 0
		{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 0},  // limit 0 on a limit order
	}
	for i, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("bad[%d].Validate() = nil, want error", i)
		}
	}
}
