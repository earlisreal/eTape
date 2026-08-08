package locates

import (
	"context"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

type testProvider struct{}

func (testProvider) LocateEligibility(string) (Eligibility, bool) { return Eligibility{}, true }
func (testProvider) QuoteLocates(context.Context, []string) (QuoteResult, error) {
	return QuoteResult{}, nil
}
func (testProvider) CreateLocate(context.Context, Request) (Record, error) { return Record{}, nil }
func (testProvider) ListLocates(context.Context, ListFilter) (Page, error) { return Page{}, nil }
func (testProvider) GetLocate(context.Context, string) (Record, error)     { return Record{}, nil }

func TestRequestValidateEnforcesLocateInvariants(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want bool
	}{
		{name: "valid 100", req: Request{Symbol: "US.AAPL", Qty: 100, LimitPrice: "0.0123", IdempotencyKey: "k"}, want: true},
		{name: "valid 500", req: Request{Symbol: "US.AAPL", Qty: 500, LimitPrice: "1.0000", IdempotencyKey: "k"}, want: true},
		{name: "zero quantity", req: Request{Symbol: "US.AAPL", Qty: 0, LimitPrice: "1", IdempotencyKey: "k"}},
		{name: "non multiple", req: Request{Symbol: "US.AAPL", Qty: 50, LimitPrice: "1", IdempotencyKey: "k"}},
		{name: "missing price", req: Request{Symbol: "US.AAPL", Qty: 100, IdempotencyKey: "k"}},
		{name: "scientific price", req: Request{Symbol: "US.AAPL", Qty: 100, LimitPrice: "1e-3", IdempotencyKey: "k"}},
		{name: "missing idempotency key", req: Request{Symbol: "US.AAPL", Qty: 100, LimitPrice: "1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.req.Validate() == nil; got != tc.want {
				t.Fatalf("Validate() success = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRegistryRoutesExactVenueOnly(t *testing.T) {
	r := NewRegistry()
	paper := testProvider{}
	live := testProvider{}
	r.Register(exec.VenueID("alpaca-paper"), paper)
	r.Register(exec.VenueID("alpaca-live"), live)

	got, ok := r.ProviderFor("alpaca-paper")
	if !ok || got == nil {
		t.Fatal("paper provider not registered")
	}
	if _, ok := r.ProviderFor("alpaca-live"); !ok {
		t.Fatal("live provider not registered")
	}
	for _, venue := range []exec.VenueID{"sim", "tradezero", "moomoo"} {
		if _, ok := r.ProviderFor(venue); ok {
			t.Fatalf("%s must not inherit an Alpaca locate provider", venue)
		}
	}
	if _, ok := r.ProviderFor("alpaca"); ok {
		t.Fatal("bare alpaca must not match an account-specific venue")
	}
}
