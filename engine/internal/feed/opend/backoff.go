package opend

import (
	"math/rand/v2"
	"time"
)

// backoff yields jittered exponential delays capped at max, per the engine
// spec's OpenD-disconnect policy (1 s → 30 s, jittered).
type backoff struct {
	min, max time.Duration
	cur      time.Duration
}

func newBackoff(min, max time.Duration) *backoff { return &backoff{min: min, max: max} }

func (b *backoff) reset() { b.cur = 0 }

func (b *backoff) next() time.Duration {
	if b.cur == 0 {
		b.cur = b.min
	} else {
		b.cur *= 2
		if b.cur > b.max {
			b.cur = b.max
		}
	}
	// full jitter within [min, cur]
	span := b.cur - b.min
	if span <= 0 {
		return b.min
	}
	return b.min + rand.N(span)
}
