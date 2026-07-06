package tradezero

import (
	"sync"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

// Adapter is the TradeZero exec.Broker implementation. Task 7 declares only
// the skeleton needed to compile normalizeOrder (this task) and its tests:
// fill-dedup state plus the three Task-10 hook methods below. Tasks 8-10 add
// the REST client, the Portfolio-WS client, replace-chain tracking, and the
// real constructor — every field/method here is provisional until Task 10
// lands and should be read as "just enough for Task 7", not the final shape.
type Adapter struct {
	venue exec.VenueID

	mu sync.Mutex
	// seenExecuted maps a TZ client-order-id to the last cumulative `executed`
	// quantity seen for it, so normalizeOrder can dedup fills on (id, executed)
	// instead of re-emitting one per repeated/duplicate order-update frame.
	seenExecuted map[string]float64

	// clk is the injected clock for deterministic tests (Task 10 TODO: wire
	// this through the real constructor). nil falls back to clock.System.
	clk clock.Clock
}

// domainID recovers the domain order id from a TZ client-order-id.
//
// Task 10 TODO: strip the "-rN" replace suffix that the emulated-replace path
// (docs/2026-07-04-multi-broker-execution-design.md) appends to the resent
// leg's client-order-id, so a full replace chain (cancel old leg, submit new
// leg with a derived id) still reports as a single domain order to the rest
// of the engine. Task 7 has no replace logic yet, so this is the identity
// function.
func (a *Adapter) domainID(tzCID string) string {
	return tzCID
}

// now returns the current time in epoch milliseconds.
//
// Task 10 TODO: this already routes through the injected clock.Clock (the
// convention used elsewhere in the engine for deterministic tests) so Task 10
// only needs to set a.clk in the real constructor; no signature change
// expected. Task 7's own tests don't assert on wall-clock values, so the
// clock.System fallback (when a.clk is left nil, as in newTestAdapter) is
// enough for now.
func (a *Adapter) now() int64 {
	if a.clk == nil {
		return clock.System{}.Now().UnixMilli()
	}
	return a.clk.Now().UnixMilli()
}

// onCanceled handles a TZ terminal "Canceled" status for an order.
//
// Task 10 TODO: this is the emulated-replace "swallow during replace" hook —
// when eTape itself canceled the old leg of a replace (TZ has no native
// modify, per the multi-broker execution design), the terminal Canceled for
// that old leg must NOT surface as a normal domain OrderCanceled (the order
// isn't actually done; a new leg is about to be submitted). Instead it should
// trigger the resubmit and emit whatever event the replace protocol needs.
// Task 7 has no replace tracking yet, so every cancellation is a no-op here
// (produces zero events) rather than risking an incorrect terminal signal.
func (a *Adapter) onCanceled(venue exec.VenueID, oid string, ts int64) []exec.BrokerEvent {
	return nil
}
