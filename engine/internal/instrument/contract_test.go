package instrument

import "testing"

func TestAutoRiskSize(t *testing.T) {
	rules := ContractRules{
		Base: "BTC", Settlement: "USDT", Multiplier: "0.0001", PriceTick: "0.1", QuantityStep: 1,
		Minimum: 1, Maximum: 1000, MinLeverage: 1, MaxLeverage: 100, TakerFee: "0",
		RiskTiers: []RiskTier{
			{MaxContracts: 80, MaxLeverage: 100, Maintenance: 0.001, LiquidationFee: 0.001},
			{MaxContracts: 1000, MaxLeverage: 10, Maintenance: 0.005, LiquidationFee: 0.001},
		},
	}
	for _, tc := range []struct {
		name             string
		collateral, stop float64
		cap              int
		reviewed, qty    int64
		leverage         int
	}{
		{"long", 120, 99000, 100, 1000, 100, 9},
		{"short", 120, 101000, 100, 1000, 100, 9},
		{"cap reduces size", 120, 99000, 5, 1000, 60, 5},
		{"reviewed quantity limits leverage", 120, 99000, 100, 36, 36, 3},
		{"tier limits quantity", 70, 99000, 100, 1000, 80, 12},
		{"liquidation limits quantity", 10, 99000, 100, 1000, 76, 76},
		{"below minimum", 0.1, 99000, 100, 1000, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			qty, leverage := rules.AutoRiskSize(10, tc.collateral, 100000, tc.stop, tc.cap, tc.reviewed, tc.stop < 100000)
			if qty != tc.qty || leverage != tc.leverage {
				t.Fatalf("got %d contracts at %dx, want %d at %dx", qty, leverage, tc.qty, tc.leverage)
			}
		})
	}
}
