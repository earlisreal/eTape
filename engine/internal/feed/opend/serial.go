package opend

import "sync/atomic"

// serialGen produces per-connection request serial numbers. A u32 that wraps at
// 2^32 is fine: correlation is by serialNo within a single live connection, and
// the in-flight window is tiny, so a wrap never collides in practice.
type serialGen struct{ n atomic.Uint32 }

func (g *serialGen) next() uint32 { return g.n.Add(1) }
