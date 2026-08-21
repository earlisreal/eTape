package md

import (
	"math"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func testLULDRegistry(tier luldTier, multiplier float64) luldRegistry {
	return luldRegistry{
		asOf:         time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		validThrough: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		symbols: map[string]luldRegistryEntry{
			"US.AAPL": {Tier: tier, Multiplier: multiplier, Provenance: "test"},
		},
	}
}

func etTime(t *testing.T, hour, minute int) time.Time {
	t.Helper()
	return time.Date(2026, 7, 6, hour, minute, 0, 0, session.Loc())
}

func luldQuote(prev float64, status feed.ProviderStatus, suspended bool) feed.Quote {
	return feed.Quote{Symbol: "US.AAPL", PrevClose: prev, Last: 100,
		ProviderStatus: status, ProviderSuspended: suspended}
}

func makeLULDPrint(at time.Time, price float64) feed.Tick {
	return feed.Tick{Symbol: "US.AAPL", TsMs: at.UnixMilli(), Price: price, LastEligible: true}
}

func warmLULD(t *testing.T, c *luldCalculator, now time.Time, prices ...float64) EstimatedLULD {
	t.Helper()
	c.onQuote(luldQuote(100, feed.ProviderStatusNormal, false), now)
	for i, price := range prices {
		c.onPrint(makeLULDPrint(now.Add(time.Duration(i)*time.Second), price), now.Add(time.Duration(i)*time.Second))
	}
	return c.advance(now.Add(5 * time.Minute))
}

func TestLULDBucketTableAndRounding(t *testing.T) {
	cases := []struct {
		name       string
		tier       luldTier
		prev       float64
		reference  float64
		now        time.Time
		multiplier float64
		wantLow    float64
		wantHigh   float64
	}{
		{"tier 1 above 3", luldTier1, 100, 100, etTime(t, 10, 0), 1, 95, 105},
		{"tier 2 above 3", luldTier2, 100, 100, etTime(t, 10, 0), 1, 90, 110},
		{"tier 2 at 3", luldTier2, 3, 100, etTime(t, 10, 0), 1, 80, 120},
		{"sub 75 cents", luldTier1, 0.5, 0.5, etTime(t, 10, 0), 1, 0.35, 0.65},
		{"final 25 minutes", luldTier1, 100, 100, etTime(t, 15, 40), 1, 90, 110},
		{"early close final 25", luldTier1, 100, 100, time.Date(2026, 11, 27, 12, 40, 0, 0, session.Loc()), 1, 90, 110},
		{"leveraged multiplier", luldTier2, 100, 100, etTime(t, 10, 0), 3, 70, 130},
		{"nearest cent", luldTier1, 100, 100.07, etTime(t, 10, 0), 1, 95.07, 105.07},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := testLULDRegistry(tc.tier, tc.multiplier)
			c := newLULDCalculator("US.AAPL", registry)
			c.onQuote(luldQuote(tc.prev, feed.ProviderStatusNormal, false), tc.now)
			c.onPrint(makeLULDPrint(tc.now, tc.reference), tc.now)
			got := c.advance(tc.now.Add(5 * time.Minute))
			if got.State != LULDEstimated || got.Lower != tc.wantLow || got.Upper != tc.wantHigh {
				t.Fatalf("band = %+v, want estimated %.2f-%.2f", got, tc.wantLow, tc.wantHigh)
			}
		})
	}
}

func TestLULDWarmupAndReferenceCadence(t *testing.T) {
	now := etTime(t, 10, 0)
	c := newLULDCalculator("US.AAPL", testLULDRegistry(luldTier1, 1))
	c.onQuote(luldQuote(100, feed.ProviderStatusNormal, false), now)
	c.onPrint(makeLULDPrint(now, 100), now)
	if got := c.advance(now.Add(4*time.Minute + 59*time.Second)); got.State != LULDWarming {
		t.Fatalf("before five minutes = %+v, want warming", got)
	}
	if got := c.advance(now.Add(5 * time.Minute)); got.State != LULDEstimated || got.Reference != 100 {
		t.Fatalf("warm result = %+v", got)
	}
	c.onPrint(makeLULDPrint(now.Add(5*time.Minute+1*time.Second), 103), now.Add(5*time.Minute+1*time.Second))
	if got := c.advance(now.Add(5*time.Minute + 29*time.Second)); got.Reference != 100 {
		t.Fatalf("reference changed before 30 seconds = %+v", got)
	}
	if got := c.advance(now.Add(5*time.Minute + 30*time.Second)); got.Reference != 103 {
		t.Fatalf("reference after cadence = %+v, want 103", got)
	}
}

func TestLULDQuietInputRetainsReference(t *testing.T) {
	now := etTime(t, 10, 0)
	c := newLULDCalculator("US.AAPL", testLULDRegistry(luldTier1, 1))
	warmLULD(t, c, now, 100)
	got := c.advance(now.Add(11 * time.Minute))
	if got.State != LULDEstimated || got.Reference != 100 || got.Lower != 95 || got.Upper != 105 {
		t.Fatalf("quiet result = %+v, want retained estimate", got)
	}
}

func TestLULDUnavailableReasons(t *testing.T) {
	now := etTime(t, 10, 0)
	c := newLULDCalculator("US.UNKNOWN", testLULDRegistry(luldTier1, 1))
	c.onQuote(feed.Quote{Symbol: "US.UNKNOWN", PrevClose: 100}, now)
	if got := c.advance(now); got.State != LULDUnavailable || got.Tier != "UNKNOWN" {
		t.Fatalf("unknown symbol = %+v", got)
	}

	c = newLULDCalculator("US.AAPL", testLULDRegistry(luldTier1, 1))
	c.onQuote(luldQuote(math.NaN(), feed.ProviderStatusNormal, false), now)
	c.onPrint(makeLULDPrint(now, 100), now)
	if got := c.advance(now.Add(5 * time.Minute)); got.State != LULDUnavailable || got.Reason != LULDReasonPreviousClose {
		t.Fatalf("missing previous close = %+v", got)
	}

	if got := c.advance(etTime(t, 8, 0)); got.State != LULDUnavailable || got.Reason != LULDReasonOutsideRTH {
		t.Fatalf("outside RTH = %+v", got)
	}
}

func TestLULDProviderAndTransportFreezeRecovery(t *testing.T) {
	now := etTime(t, 10, 0)
	c := newLULDCalculator("US.AAPL", testLULDRegistry(luldTier1, 1))
	warmLULD(t, c, now, 100)
	c.onQuote(luldQuote(100, feed.ProviderStatusNonnormal, false), now.Add(6*time.Minute))
	if got := c.advance(now.Add(6 * time.Minute)); got.State != LULDFrozen || got.Lower != 95 || got.Reason != LULDReasonProviderStatus {
		t.Fatalf("provider freeze = %+v", got)
	}
	c.onQuote(luldQuote(100, feed.ProviderStatusUnknown, false), now.Add(6*time.Minute+time.Second))
	if got := c.advance(now.Add(6*time.Minute + time.Second)); got.State != LULDFrozen {
		t.Fatalf("neutral status cleared provider freeze = %+v", got)
	}
	c.onQuote(luldQuote(100, feed.ProviderStatusNormal, false), now.Add(6*time.Minute+2*time.Second))
	if got := c.advance(now.Add(6*time.Minute + 2*time.Second)); got.State != LULDWarming {
		t.Fatalf("normal recovery = %+v, want warming", got)
	}
	c.onPrint(makeLULDPrint(now.Add(6*time.Minute+3*time.Second), 100), now.Add(6*time.Minute+3*time.Second))
	c.onTransport(true, now.Add(6*time.Minute+4*time.Second))
	if got := c.advance(now.Add(6*time.Minute + 4*time.Second)); got.State != LULDFrozen {
		t.Fatalf("transport freeze = %+v", got)
	}
	c.onTransport(false, now.Add(6*time.Minute+5*time.Second))
	if got := c.advance(now.Add(6*time.Minute + 5*time.Second)); got.State != LULDWarming {
		t.Fatalf("transport recovery = %+v, want warming", got)
	}
}
