package instrument

import "testing"

func TestSizeNotionalRisk(t *testing.T) {
	rules := ContractRules{Base: "BTC", Settlement: "USDT", Multiplier: "0.001", PriceTick: "1", QuantityStep: 1, Minimum: 1,
		Maximum: 1000, MinLeverage: 1, MaxLeverage: 125, TakerFee: "0.0005", TradingEnabled: true}
	n := NotionalRisk{Mark: 100_100, MinimumValue: 2, Tiers: []NotionalTier{{Maximum: 1_000_000, Rate: 0.005}}}
	qty, leverage := rules.SizeNotionalRisk(n, 5, 1000, 100_000, 99_000, 1, 125, 5, 100, 100, true)
	if qty != 4 || leverage != 59 {
		t.Fatalf("Auto must keep the risk-sized quantity and choose the highest leverage clearing liquidation: %d at %d×", qty, leverage)
	}
	n.Tiers[0].Maximum = 300
	qty, leverage = rules.SizeNotionalRisk(n, 5, 1000, 100_000, 99_000, 1, 20, 5, 100, 100, true)
	if qty != 2 || leverage != 20 {
		t.Fatalf("tier must cover notional at the current mark as well as at the stop: %d at %d×", qty, leverage)
	}
	n.Tiers[0].Maximum = 1_000_000
	n.Position = RiskPosition{Qty: 4, AvgPrice: 100_000, Margin: 80, LiquidationPrice: 85_000, Leverage: 5}
	qty, leverage = rules.SizeNotionalRisk(n, 5, 1000, 100_000, 99_000, 1, 125, 5, 1, 100, true)
	if qty != 1 || leverage != 5 {
		t.Fatalf("add must use the current leverage and remaining capacity: %d at %d×", qty, leverage)
	}
	n.Position.LiquidationPrice = 98_950
	if qty, _ := rules.SizeNotionalRisk(n, 5, 1000, 100_000, 99_000, 1, 125, 5, 100, 100, true); qty != 0 {
		t.Fatal("add accepted when the shared stop lacks clearance from the existing position's liquidation price")
	}
}
