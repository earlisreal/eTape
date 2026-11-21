package alpaca

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

func TestFractionalStockOrderRoutes(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 20, 20, 59, 59, 0, time.UTC))
	yes, no := true, false
	a := &Adapter{paper: true, clk: clk, assetsBySymbol: map[string]AssetStatus{
		"AAPL": {Tradable: &yes, Fractionable: &yes, FractionalEHEnabled: &no},
	}}
	req := exec.OrderRequest{Symbol: "US.AAPL", Side: exec.SideBuy, Qty: .375, Type: exec.TypeLimit, TIF: exec.TIFDay}
	if reason := a.FractionalStockOrderReason(req); reason != "" {
		t.Fatal(reason)
	}
	clk.Advance(time.Second)
	if reason := a.FractionalStockOrderReason(req); reason == "" {
		t.Fatal("accepted after-hours without fractional extended-hours eligibility")
	}
	a.assetsBySymbol["AAPL"] = AssetStatus{Tradable: &yes, Fractionable: &yes, FractionalEHEnabled: &yes}
	if reason := a.FractionalStockOrderReason(req); reason != "" {
		t.Fatal(reason)
	}
	req.Side = exec.SideShort
	if reason := a.FractionalStockOrderReason(req); reason == "" {
		t.Fatal("accepted fractional short")
	}
	req.Side = exec.SideBuy
	clk.Advance(4 * time.Hour)
	if reason := a.FractionalStockOrderReason(req); reason == "" {
		t.Fatal("accepted overnight")
	}
}
