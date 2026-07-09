// Package stub provides a disconnected, reject-all execution venue for brokers
// whose live trading adapter is not yet implemented. It registers like any
// venue, never connects (Events() is closed), and rejects order placement;
// cancel and flatten are no-ops because exposure-reducing actions are never
// gated. moomoo trading is deferred to execution v1.x — when the real adapter
// lands, only boot.go's "moomoo" case changes.
package stub

import (
	"context"
	"errors"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

// Broker is the reject-all venue. Compile-time interface check below.
type Broker struct {
	events chan exec.BrokerEvent
}

var _ exec.Broker = (*Broker)(nil)

// New builds a stub broker whose Events() channel is already closed, so exec's
// pump goroutine exits immediately and the venue reports as never-connected.
func New() *Broker {
	ch := make(chan exec.BrokerEvent)
	close(ch)
	return &Broker{events: ch}
}

var errUnavailable = errors.New("venue unavailable: moomoo trading is deferred to execution v1.x")

func (b *Broker) Capabilities() exec.Capabilities { return exec.Capabilities{} }

func (b *Broker) SubmitOrder(context.Context, exec.OrderRequest) (exec.OrderAck, error) {
	return exec.OrderAck{}, errUnavailable
}

func (b *Broker) ReplaceOrder(context.Context, string, exec.ReplaceRequest) error {
	return errUnavailable
}

func (b *Broker) CancelOrder(context.Context, string) error { return nil }
func (b *Broker) CancelAll(context.Context, string) error   { return nil }
func (b *Broker) Flatten(context.Context) error             { return nil }

// ResetBalance errors, unlike the exposure-reducing no-ops above: fabricating
// account funds is never gated-safe to no-op, and moomoo trading isn't wired
// up yet regardless.
func (b *Broker) ResetBalance(context.Context, float64) error { return errUnavailable }

// Snapshot errors so exec.Recover logs "not available" and creates no account
// row — the Account panel then shows "—" for this venue rather than a fake $0.
func (b *Broker) Snapshot(context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	return exec.AccountSnapshot{}, nil, nil, errUnavailable
}

func (b *Broker) Events() <-chan exec.BrokerEvent { return b.events }
