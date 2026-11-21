// Package eligibility owns the shared, read-only venue instrument capability.
package eligibility

import (
	"context"
	"sync"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

// Eligibility contains only the generic instrument permissions shown by the
// shell. A nil field means the venue did not provide that permission.
type Eligibility struct {
	FractionalPaper     bool
	Fractionable        *bool
	FractionalEHEnabled *bool
	Tradable            *bool
	Marginable          *bool
	Shortable           *bool
}

// Provider resolves one exact venue/account's instrument metadata.
type Provider interface {
	VenueInstrumentEligibility(context.Context, string) (Eligibility, bool, error)
}

// Registry keeps providers keyed by their exact configured venue.
type Registry struct {
	mu        sync.RWMutex
	providers map[exec.VenueID]Provider
}

func NewRegistry() *Registry { return &Registry{providers: map[exec.VenueID]Provider{}} }

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
	provider, ok := r.providers[venue]
	r.mu.RUnlock()
	return provider, ok && provider != nil
}
