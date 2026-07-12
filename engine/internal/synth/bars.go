// This file rolls printed ticks (Task 4) into 1-minute OHLCV bars and
// assembles the basic quote struct the feed layer publishes alongside them.
// barAgg tracks exactly one in-progress minute bucket at a time; the caller
// is responsible for handing its symbol back in when finalizing (barAgg
// itself carries no Symbol field, matching Task 4's sessionAgg).
package synth

import (
	"github.com/earlisreal/eTape/engine/internal/feed"
)

// barMs is the bucket width (1 minute) bars are aggregated at.
const barMs = 60_000

// barAgg accumulates ticks into the current in-progress 1-minute bar.
// bucketMs is the bucket's start (floor to minute); open is false before the
// first tick has ever arrived, distinguishing "no bar yet" from a real print
// whose o/h/l/c could otherwise look like a zero-value bar.
type barAgg struct {
	bucketMs   int64
	o, h, l, c float64
	vol        int64
	turn       float64
	open       bool
}

// add folds tk into ba's current minute bucket. If ba has no open bucket yet,
// it starts one at tk's bucket and returns nil. If tk falls in the same
// bucket as ba's current one, it updates o/h/l/c/vol/turn in place and
// returns nil. If tk's bucket differs (i.e. a new minute has started), it
// finalizes and returns the just-closed bar, then starts a fresh bucket at
// tk's bucket with tk as its first print.
func (ba *barAgg) add(tk feed.Tick) (closed *feed.Bar) {
	bucket := tk.TsMs - tk.TsMs%barMs

	if !ba.open {
		ba.start(bucket, tk)
		return nil
	}

	if bucket != ba.bucketMs {
		closed = ba.finalize(tk.Symbol)
		ba.start(bucket, tk)
		return closed
	}

	if tk.Price > ba.h {
		ba.h = tk.Price
	}
	if tk.Price < ba.l {
		ba.l = tk.Price
	}
	ba.c = tk.Price
	ba.vol += tk.Volume
	ba.turn += tk.Turnover
	return nil
}

// start resets ba to a fresh bucket seeded by tk (tk's price opens and
// closes the new bar; its volume/turnover are the bar's first contribution).
func (ba *barAgg) start(bucket int64, tk feed.Tick) {
	ba.bucketMs = bucket
	ba.o = tk.Price
	ba.h = tk.Price
	ba.l = tk.Price
	ba.c = tk.Price
	ba.vol = tk.Volume
	ba.turn = tk.Turnover
	ba.open = true
}

// finalize builds the feed.Bar for ba's current bucket, keyed by symbol.
func (ba *barAgg) finalize(symbol string) *feed.Bar {
	return &feed.Bar{
		Symbol:   symbol,
		BucketMs: ba.bucketMs,
		O:        ba.o,
		H:        ba.h,
		L:        ba.l,
		C:        ba.c,
		Volume:   ba.vol,
		Turnover: ba.turn,
	}
}

// inProgress returns the live, not-yet-closed bar for symbol — used for
// in-minute chart updates before the bucket rolls over and add returns the
// finalized bar. ok is false if no tick has arrived yet (ba is unopened).
func (ba *barAgg) inProgress(symbol string) (feed.Bar, bool) {
	if !ba.open {
		return feed.Bar{}, false
	}
	return *ba.finalize(symbol), true
}

// buildQuote assembles the basic feed.Quote for symbol from sess's running
// session aggregate (Task 4), prevClose (the prior session's official close,
// supplied by the caller — sessionAgg has no notion of "previous session"),
// and tsMs (the quote's timestamp — the triggering tick/tick-batch's time,
// supplied by the caller since sessionAgg does not track one).
func buildQuote(symbol string, sess *sessionAgg, prevClose float64, tsMs int64) feed.Quote {
	return feed.Quote{
		Symbol:    symbol,
		TsMs:      tsMs,
		Last:      sess.Last,
		Open:      sess.Open,
		High:      sess.High,
		Low:       sess.Low,
		PrevClose: prevClose,
		Volume:    sess.Vol,
		Turnover:  sess.Turnover,
	}
}
