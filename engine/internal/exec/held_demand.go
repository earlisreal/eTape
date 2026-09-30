package exec

import "context"

// HeldDemandController admits and releases execution-grade ticker leases. The
// execution domain stays independent of a particular feed implementation.
type HeldDemandController interface {
	Acquire(ctx context.Context, orderID, symbol string) error
	Release(orderID string)
}
