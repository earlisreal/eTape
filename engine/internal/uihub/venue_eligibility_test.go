package uihub

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/eligibility"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type venueEligibilityProviderSpy struct {
	result eligibility.Eligibility
	found  bool
	err    error
	calls  int
}

func (s *venueEligibilityProviderSpy) VenueInstrumentEligibility(context.Context, string) (eligibility.Eligibility, bool, error) {
	s.calls++
	return s.result, s.found, s.err
}

func TestVenueInstrumentEligibilityQueryRoutesAndCachesExactVenue(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	tradable, marginable, shortable := true, false, true
	provider := &venueEligibilityProviderSpy{
		result: eligibility.Eligibility{Tradable: &tradable, Marginable: &marginable, Shortable: &shortable},
		found:  true,
	}
	registry := eligibility.NewRegistry()
	registry.Register(exec.VenueID("alpaca-paper"), provider)
	q := NewVenueEligibilityQueriesForTest(&spyFills{}, clk, registry)

	query := json.RawMessage(`{"venue":"alpaca-paper","symbol":"US.AAPL"}`)
	got := q.handle("QueryVenueInstrumentEligibility", query).(wsmsg.VenueInstrumentEligibility)
	if !got.Supported || !got.Found || got.Tradable == nil || !*got.Tradable || got.Marginable == nil || *got.Marginable || got.Shortable == nil || !*got.Shortable {
		t.Fatalf("first eligibility = %#v", got)
	}
	if cached := q.handle("QueryVenueInstrumentEligibility", query).(wsmsg.VenueInstrumentEligibility); !cached.Found {
		t.Fatalf("cached eligibility = %#v", cached)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls before expiry = %d, want 1", provider.calls)
	}

	clk.Advance(59 * time.Second)
	q.handle("QueryVenueInstrumentEligibility", query)
	if provider.calls != 1 {
		t.Fatalf("provider calls before 60s = %d, want 1", provider.calls)
	}
	clk.Advance(time.Second)
	q.handle("QueryVenueInstrumentEligibility", query)
	if provider.calls != 2 {
		t.Fatalf("provider calls at expiry = %d, want 2", provider.calls)
	}

	unsupported := q.handle("QueryVenueInstrumentEligibility", json.RawMessage(`{"venue":"alpaca-live","symbol":"US.AAPL"}`)).(wsmsg.VenueInstrumentEligibility)
	if unsupported.Supported || unsupported.Found || unsupported.Tradable != nil || unsupported.Error == "" {
		t.Fatalf("unsupported eligibility = %#v", unsupported)
	}
}

func TestVenueInstrumentEligibilityQueryDoesNotCacheProviderErrors(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	provider := &venueEligibilityProviderSpy{found: true, err: errors.New("gateway unavailable")}
	registry := eligibility.NewRegistry()
	registry.Register(exec.VenueID("moomoo-paper"), provider)
	q := NewVenueEligibilityQueriesForTest(&spyFills{}, clk, registry)

	query := json.RawMessage(`{"venue":"moomoo-paper","symbol":"US.AAPL"}`)
	first := q.handle("QueryVenueInstrumentEligibility", query).(wsmsg.VenueInstrumentEligibility)
	second := q.handle("QueryVenueInstrumentEligibility", query).(wsmsg.VenueInstrumentEligibility)
	if first.Error != "gateway unavailable" || second.Error != "gateway unavailable" || first.Found || second.Found {
		t.Fatalf("error responses = %#v, %#v", first, second)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls after errors = %d, want 2", provider.calls)
	}
}

func TestFractionableSessionChangesBeforeAssetCacheExpires(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 27, 17, 59, 59, 0, time.UTC))
	yes, no := true, false
	provider := &venueEligibilityProviderSpy{found: true, result: eligibility.Eligibility{
		Fractionable: &yes, FractionalEHEnabled: &no,
	}}
	registry := eligibility.NewRegistry()
	registry.Register("paper", provider)
	q := NewVenueEligibilityQueriesForTest(&spyFills{}, clk, registry)
	query := json.RawMessage(`{"venue":"paper","symbol":"US.AAPL"}`)
	before := q.handle("QueryVenueInstrumentEligibility", query).(wsmsg.VenueInstrumentEligibility)
	clk.Advance(time.Second)
	after := q.handle("QueryVenueInstrumentEligibility", query).(wsmsg.VenueInstrumentEligibility)
	if before.Phase != "rth" || before.PhaseRefreshMs != 1001 || after.Phase != "post" ||
		after.Fractionable == nil || !*after.Fractionable || after.FractionalEHEnabled == nil || *after.FractionalEHEnabled || provider.calls != 1 {
		t.Fatalf("before=%+v after=%+v provider calls=%d", before, after, provider.calls)
	}
}

func TestLiveChartRiskPreviewUsesFractionalRoute(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 27, 17, 59, 59, 0, time.UTC))
	yes, no := true, false
	registry := eligibility.NewRegistry()
	registry.Register("alpaca-live", &venueEligibilityProviderSpy{found: true, result: eligibility.Eligibility{
		Tradable: &yes, Fractionable: &yes, FractionalEHEnabled: &no, FractionalStockOrders: true,
	}})
	q := NewVenueEligibilityQueriesForTest(&spyFills{}, clk, registry)
	args := json.RawMessage(`{"venue":"alpaca-live","symbol":"US.AAPL","riskEntry":true,"tif":"DAY","session":"EXTENDED"}`)
	got := q.handle("QueryStopLimitRoute", args).(wsmsg.StopLimitRoutePreview)
	if !got.FractionalRisk || got.EffectiveSession != "RTH" || got.DeadlineMs != clk.Now().Add(time.Second).UnixMilli() {
		t.Fatalf("regular-hours-only live preview: %+v", got)
	}
}
