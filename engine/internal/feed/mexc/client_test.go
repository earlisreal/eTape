package mexc

import (
	"encoding/json"
	"testing"
)

func TestIsolatedRiskTiers(t *testing.T) {
	const payload = `{
		"positionOpenType":3,"minLeverage":1,"maxLeverage":500,"countryConfigContractMaxLeverage":150,
		"liquidationFeeRate":0.0004,"riskLimitType":"BY_VOLUME","riskLimitMode":"CUSTOM","feeRateMode":"NORMAL",
		"riskLimitCustom":[{"maxVol":120000,"mmr":0.004,"maxLeverage":100},{"maxVol":50000,"mmr":0.001,"maxLeverage":500}]
	}`
	var contract Contract
	if err := json.Unmarshal([]byte(payload), &contract); err != nil {
		t.Fatal(err)
	}
	tiers := contract.isolatedRiskTiers()
	if len(tiers) != 2 || tiers[0].MaxContracts != 50000 || tiers[0].MaxLeverage != 150 ||
		tiers[0].Maintenance != 0.001 || tiers[0].LiquidationFee != 0.0004 || tiers[1].MaxLeverage != 100 {
		t.Fatalf("unexpected isolated tiers: %+v", tiers)
	}
	contract.RiskLimitType = "BY_VALUE"
	if tiers := contract.isolatedRiskTiers(); len(tiers) != 0 {
		t.Fatalf("interpreted a value limit as a contract count: %+v", tiers)
	}
}
