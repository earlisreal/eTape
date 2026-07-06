package alpaca

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

func TestOrderTypeWire(t *testing.T) {
	cases := map[exec.OrderType]string{
		exec.TypeMarket: "market", exec.TypeLimit: "limit",
		exec.TypeStop: "stop", exec.TypeStopLimit: "stop_limit",
	}
	for ot, want := range cases {
		if got, err := orderTypeWire(ot); err != nil || got != want {
			t.Errorf("orderTypeWire(%v)=%q,%v want %q", ot, got, err, want)
		}
	}
}

func TestSideWire(t *testing.T) {
	if sideWire(exec.SideBuy) != "buy" || sideWire(exec.SideCover) != "buy" {
		t.Fatal("buy/cover -> buy")
	}
	if sideWire(exec.SideSell) != "sell" || sideWire(exec.SideShort) != "sell" {
		t.Fatal("sell/short -> sell")
	}
}

func TestRoundPrice_SubPenny(t *testing.T) {
	if roundPrice(190.5049) != 190.50 {
		t.Fatalf("got %v", roundPrice(190.5049))
	}
	if roundPrice(0.12345) != 0.1235 { // sub-$1 -> 4dp
		t.Fatalf("got %v", roundPrice(0.12345))
	}
}

func TestTifWire(t *testing.T) {
	// Verified 2026-07-06 against a real paper account: FOK is accepted on a
	// standard account (not Elite-only); IOC is rejected outside market hours
	// (a session-time gate this pure function can't evaluate), so it stays
	// rejected here, but for the correct reason.
	if got, err := tifWire(exec.TIFFOK); err != nil || got != "fok" {
		t.Fatalf("FOK -> %q, %v; want \"fok\", nil (FOK is accepted on a standard account)", got, err)
	}
	if _, err := tifWire(exec.TIFIOC); err == nil {
		t.Fatal("IOC should still error (market-hours-only, not evaluable here)")
	}
	if got, _ := tifWire(exec.TIFDay); got != "day" {
		t.Fatalf("day -> %q", got)
	}
	if got, _ := tifWire(exec.TIFGTC); got != "gtc" {
		t.Fatalf("gtc -> %q", got)
	}
}

func TestSideDomain(t *testing.T) {
	cases := []struct {
		name              string
		alpacaSide        string
		positionQtyBefore float64
		want              exec.Side
	}{
		{
			name:              "buy with negative position -> Cover",
			alpacaSide:        "buy",
			positionQtyBefore: -10,
			want:              exec.SideCover,
		},
		{
			name:              "buy with zero position -> Buy",
			alpacaSide:        "buy",
			positionQtyBefore: 0,
			want:              exec.SideBuy,
		},
		{
			name:              "buy with positive position -> Buy",
			alpacaSide:        "buy",
			positionQtyBefore: 10,
			want:              exec.SideBuy,
		},
		{
			name:              "sell with positive position -> Sell",
			alpacaSide:        "sell",
			positionQtyBefore: 10,
			want:              exec.SideSell,
		},
		{
			name:              "sell with zero position -> Short",
			alpacaSide:        "sell",
			positionQtyBefore: 0,
			want:              exec.SideShort,
		},
		{
			name:              "sell with negative position -> Short",
			alpacaSide:        "sell",
			positionQtyBefore: -10,
			want:              exec.SideShort,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sideDomain(tc.alpacaSide, tc.positionQtyBefore)
			if got != tc.want {
				t.Errorf("sideDomain(%q, %v) = %v, want %v", tc.alpacaSide, tc.positionQtyBefore, got, tc.want)
			}
		})
	}
}
