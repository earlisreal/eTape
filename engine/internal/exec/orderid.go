package exec

import (
	"io"
	"sync"

	"github.com/oklog/ulid/v2"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// OrderIDGen mints "ET"+ULID client order IDs — 28 chars, lexicographically
// time-ordered, and collision-free across venues and restarts by construction.
// Callers are the single-writer Core; the mutex guards the monotonic entropy
// source so the type is nonetheless safe for incidental concurrent use.
type OrderIDGen struct {
	clk     clock.Clock
	mu      sync.Mutex
	entropy *ulid.MonotonicEntropy
}

// NewOrderIDGen seeds the generator. In production pass crypto/rand.Reader; tests
// pass a deterministic reader (e.g. math/rand) for reproducible IDs.
func NewOrderIDGen(clk clock.Clock, seed io.Reader) *OrderIDGen {
	return &OrderIDGen{clk: clk, entropy: ulid.Monotonic(seed, 0)}
}

// Next returns the next "ET"+ULID order ID.
func (g *OrderIDGen) Next() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	id := ulid.MustNew(ulid.Timestamp(g.clk.Now()), g.entropy)
	return "ET" + id.String()
}
