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

func TestOrderType_String_Stops(t *testing.T) {
	cases := map[OrderType]string{
		TypeMarket:    "MARKET",
		TypeLimit:     "LIMIT",
		TypeStop:      "STOP",
		TypeStopLimit: "STOP_LIMIT",
	}
	for ot, want := range cases {
		if got := ot.String(); got != want {
			t.Errorf("OrderType(%d).String() = %q, want %q", uint8(ot), got, want)
		}
	}
}

func TestOrderRequest_Validate_Stops(t *testing.T) {
	base := OrderRequest{Venue: "v", Symbol: "AAPL", Side: SideBuy, Qty: 10}
	tests := []struct {
		name    string
		mutate  func(*OrderRequest)
		wantErr bool
	}{
		{"stop without stop price", func(r *OrderRequest) { r.Type = TypeStop }, true},
		{"stop ok", func(r *OrderRequest) { r.Type = TypeStop; r.StopPrice = 5 }, false},
		{"stop-limit missing limit", func(r *OrderRequest) { r.Type = TypeStopLimit; r.StopPrice = 5 }, true},
		{"stop-limit missing stop", func(r *OrderRequest) { r.Type = TypeStopLimit; r.LimitPrice = 5 }, true},
		{"stop-limit ok", func(r *OrderRequest) { r.Type = TypeStopLimit; r.StopPrice = 5; r.LimitPrice = 6 }, false},
		{"limit still requires price", func(r *OrderRequest) { r.Type = TypeLimit }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.mutate(&r)
			if err := r.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
