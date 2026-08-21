package md

import (
	"testing"
	"time"
)

func TestLULDRegistryLookupIsDatedAllowlist(t *testing.T) {
	r, err := loadLULDRegistry([]byte(`{
  "as_of":"2026-07-01",
  "valid_through":"2026-10-01",
  "sources":["https://example.test/registry"],
  "symbols":{
    "US.AAPL":{"tier":"T1","provenance":"S&P 500"},
    "US.TQQQ":{"tier":"T2","multiplier":2,"provenance":"leveraged ETP override"}
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r.lookup("US.AAPL", time.Date(2026, 10, 1, 23, 59, 0, 0, time.UTC)); !ok || got.Tier != luldTier1 {
		t.Fatalf("valid lookup = (%+v, %v)", got, ok)
	}
	if _, ok := r.lookup("US.AAPL", time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)); ok {
		t.Fatal("expired registry record must not be returned")
	}
	if _, ok := r.lookup("US.AAPL", time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)); !ok {
		t.Fatal("valid-through date must use the ET calendar date")
	}
	if _, ok := r.lookup("US.UNKNOWN", time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)); ok {
		t.Fatal("unknown symbol must not fall back to Tier 2")
	}
	if got, ok := r.lookup("US.TQQQ", time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)); !ok || got.Multiplier != 2 {
		t.Fatalf("leveraged override = (%+v, %v)", got, ok)
	}
}

func TestLULDRegistryValidation(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing source", `{"as_of":"2026-07-01","valid_through":"2026-10-01","sources":[],"symbols":{"US.AAPL":{"tier":"T1","provenance":"x"}}}`},
		{"bad symbol", `{"as_of":"2026-07-01","valid_through":"2026-10-01","sources":["https://example.test"],"symbols":{"US aapl":{"tier":"T1","provenance":"x"}}}`},
		{"bad tier", `{"as_of":"2026-07-01","valid_through":"2026-10-01","sources":["https://example.test"],"symbols":{"US.AAPL":{"tier":"T3","provenance":"x"}}}`},
		{"bad multiplier", `{"as_of":"2026-07-01","valid_through":"2026-10-01","sources":["https://example.test"],"symbols":{"US.AAPL":{"tier":"T1","multiplier":0.5,"provenance":"x"}}}`},
		{"missing provenance", `{"as_of":"2026-07-01","valid_through":"2026-10-01","sources":["https://example.test"],"symbols":{"US.AAPL":{"tier":"T1"}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadLULDRegistry([]byte(tc.json)); err == nil {
				t.Fatal("invalid registry unexpectedly accepted")
			}
		})
	}
}

func TestEmbeddedLULDRegistryIsValid(t *testing.T) {
	if len(defaultLULDRegistry.symbols) == 0 {
		t.Fatal("embedded LULD registry is empty")
	}
	if _, ok := defaultLULDRegistry.lookup("US.AAPL", time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)); !ok {
		t.Fatal("embedded registry must cover US.AAPL")
	}
}
