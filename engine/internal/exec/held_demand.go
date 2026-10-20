package exec

import "context"

// HeldDemandController admits and releases execution-grade ticker leases. The
// execution domain stays independent of a particular feed implementation.
type HeldDemandController interface {
	Acquire(ctx context.Context, orderID, symbol string) error
	Release(orderID string)
}

type TrailDemandController interface {
	AcquireTrail(ctx context.Context, orderID, symbol string, config TrailConfig) error
}

// BookCoverageController verifies that cached evidence belongs to the currently
// acknowledged lease. Zero means unavailable, even if a cached book exists.
type BookCoverageController interface{ TrailBookCoverage(symbol string) uint64 }

// Optional corroboration must never block admission of required ticker data.
type ProtectionBookDemandController interface {
	EnsureProtectionBook(orderID, symbol string)
}
