package locates

import (
	"context"
	"sync"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

type Provider interface {
	LocateEligibility(symbol string) (Eligibility, bool)
	QuoteLocates(ctx context.Context, symbols []string) (QuoteResult, error)
	CreateLocate(ctx context.Context, req Request) (Record, error)
	ListLocates(ctx context.Context, filter ListFilter) (Page, error)
	GetLocate(ctx context.Context, id string) (Record, error)
}

// Registry keeps account-specific locate providers keyed by their exact venue.
// It is populated during boot and read by UIHub handlers concurrently.
type Registry struct {
	mu        sync.RWMutex
	providers map[exec.VenueID]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: map[exec.VenueID]Provider{}}
}

func (r *Registry) Register(venue exec.VenueID, provider Provider) {
	if r == nil || venue == "" || provider == nil {
		return
	}
	r.mu.Lock()
	if r.providers == nil {
		r.providers = map[exec.VenueID]Provider{}
	}
	r.providers[venue] = provider
	r.mu.Unlock()
}

func (r *Registry) ProviderFor(venue exec.VenueID) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	p, ok := r.providers[venue]
	r.mu.RUnlock()
	return p, ok
}
