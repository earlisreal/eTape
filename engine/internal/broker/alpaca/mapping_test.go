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

func TestTifWire_RejectsElite(t *testing.T) {
	if _, err := tifWire(exec.TIFIOC); err == nil {
		t.Fatal("IOC should error on a standard account")
	}
	if got, _ := tifWire(exec.TIFDay); got != "day" {
		t.Fatalf("day -> %q", got)
	}
}
