// Package netx holds the network plumbing shared by broker adapters: jittered
// backoff, a clock-injected token-bucket rate limiter, and a keep-alive HTTP
// client factory. It imports only clock + stdlib and must never import exec or
// an adapter, so both adapters can depend on it without cycles.
package netx

import (
	"math/rand/v2"
	"time"
)

// Backoff yields full-jitter exponential delays in [Min, cur], cur doubling from
// Min up to Max. Mirrors the feed/opend reconnect policy.
type Backoff struct {
	Min, Max time.Duration
	cur      time.Duration
}

func (b *Backoff) Reset() { b.cur = 0 }

func (b *Backoff) Next() time.Duration {
	if b.cur == 0 {
		b.cur = b.Min
	} else {
		b.cur *= 2
		if b.cur > b.Max {
			b.cur = b.Max
		}
	}
	span := b.cur - b.Min
	if span <= 0 {
		return b.Min
	}
	return b.Min + rand.N(span)
}
