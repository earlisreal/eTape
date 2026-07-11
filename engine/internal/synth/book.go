// This file implements the simulated L2 order book: a fixed 10-level ladder
// per side that centers on the price walk's mid, consumes liquidity as
// synthetic trades sweep the touch, and replenishes back toward target
// depth between prints.
package synth

import (
	"math"
	"math/rand"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// bookDepth is the number of levels newBook/replenish maintain per side.
const bookDepth = 10

// roundLot is used to derive a level's synthetic order count from its size.
const roundLot = 100

// level is one price level of one side of the simulated book. bids are
// ordered high->low, asks low->high (best/touch first on both sides).
type level struct {
	Price  float64
	Size   int64
	Orders int32
}

// bookState is the mutable per-symbol L2 book. bids and asks are kept sorted
// best-first at all times (bids descending, asks ascending) by every
// mutating method.
type bookState struct {
	bids []level
	asks []level
}

// newBook builds a fresh 10-level-per-side book centered on mid, using
// spec's spread and size distribution.
func newBook(rng *rand.Rand, spec SymbolSpec, mid float64) *bookState {
	b := &bookState{}
	b.rebuildAround(rng, spec, mid, false)
	return b
}

// rebuildAround re-centers b on mid, discarding the previous ladder and
// drawing bookDepth fresh levels per side. halfSpread widens by
// spec.Spread.FlushMult when flush is set (a volatility-flush moment).
func (b *bookState) rebuildAround(rng *rand.Rand, spec SymbolSpec, mid float64, flush bool) {
	halfSpread := math.Max(0.01, float64(spec.Spread.MinCents)/100)
	if flush {
		halfSpread *= spec.Spread.FlushMult
	}

	bestBid := round2(mid - halfSpread)
	if bestBid < priceFloor {
		bestBid = priceFloor
	}
	bestAsk := round2(mid + halfSpread)
	if bestAsk < bestBid+0.01 {
		bestAsk = bestBid + 0.01
	}

	b.bids = buildLevels(rng, spec, bestBid, false)
	b.asks = buildLevels(rng, spec, bestAsk, true)
}

// priceFloor is the minimum tradable price a level may sit at ($0.01 —
// matching stepPrice's own Mid floor in price.go).
const priceFloor = 0.01

// buildLevels draws up to bookDepth levels starting at start and walking
// away from the touch by tickStep increments — up for asks (ascending),
// down for bids (descending). The walk stops early, short of bookDepth,
// rather than push a bid below priceFloor.
func buildLevels(rng *rand.Rand, spec SymbolSpec, start float64, ascending bool) []level {
	if !ascending && start < priceFloor {
		start = priceFloor
	}
	levels := make([]level, 0, bookDepth)
	price := start
	for i := 0; i < bookDepth; i++ {
		levels = append(levels, drawLevel(rng, spec, price))
		var next float64
		if ascending {
			next = round2(price + tickStep(rng))
		} else {
			next = round2(price - tickStep(rng))
			if next < priceFloor {
				break
			}
		}
		price = next
	}
	return levels
}

// consume walks the touch on the side implied by dir (Buy consumes asks,
// Sell consumes bids), decrementing/promoting levels as qty is filled, and
// returns the volume-weighted average execution price and the filled
// quantity (always qty, since a deep sweep synthesizes worse levels rather
// than running out of liquidity).
func (b *bookState) consume(dir feed.Direction, qty int64) (execPrice float64, filled int64) {
	side := &b.asks
	if dir == feed.Sell {
		side = &b.bids
	}

	remaining := qty
	var notional float64
	lastPrice := 0.0
	if len(*side) > 0 {
		lastPrice = (*side)[0].Price
	}
	for remaining > 0 {
		if len(*side) == 0 {
			extendSide(side, dir, lastPrice)
		}
		lv := &(*side)[0]
		take := remaining
		if take > lv.Size {
			take = lv.Size
		}
		notional += lv.Price * float64(take)
		lv.Size -= take
		remaining -= take
		filled += take
		lastPrice = lv.Price

		if lv.Size <= 0 {
			*side = (*side)[1:]
		}
	}

	// Postcondition: never leave the touched side empty. The loop above
	// only extends when it finds the side empty *before* taking from it;
	// if the very last unit of qty happens to exactly drain the side's
	// last remaining level, the loop exits with remaining == 0 without
	// ever re-checking — leaving *side at length 0. Guard against that
	// here unconditionally, regardless of how the loop above exited.
	if len(*side) == 0 {
		extendSide(side, dir, lastPrice)
	}

	if filled == 0 {
		return 0, 0
	}
	return notional / float64(filled), filled
}

// extendSide appends one synthetic level one tick worse than lastPrice (the
// price of the level that was just fully consumed), so a deep sweep never
// runs the book dry. Buy direction extends the ask side upward, uncapped;
// sell direction extends the bid side downward, floored at priceFloor.
func extendSide(side *[]level, dir feed.Direction, lastPrice float64) {
	const step = 0.01
	price := lastPrice
	if dir == feed.Buy {
		price = round2(price + step)
	} else {
		price = round2(price - step)
		if price < priceFloor {
			price = priceFloor
		}
	}
	size := int64(500)
	*side = append(*side, level{Price: price, Size: size, Orders: ordersFor(size)})
}

// replenish tops each side back toward bookDepth levels centered on mid,
// occasionally planting a larger wall at the nearest round number, and
// re-asserts best-first ordering and a non-crossed touch.
func (b *bookState) replenish(rng *rand.Rand, spec SymbolSpec, mid float64) {
	halfSpread := math.Max(0.01, float64(spec.Spread.MinCents)/100)

	b.bids = topUp(rng, spec, b.bids, round2(mid-halfSpread), true)
	b.asks = topUp(rng, spec, b.asks, round2(mid+halfSpread), false)

	// occasionally plant a round-number wall on whichever side it falls on
	if rng.Float64() < 0.1 {
		plantRoundWall(rng, spec, &b.bids, &b.asks)
	}

	b.fixCrossed()
}

// topUp extends side (already best-first, sorted, may be short after a
// sweep) back out to bookDepth levels, walking away from anchor by
// tickStep increments. desc selects the walk direction (true = bids,
// descending; false = asks, ascending). Like buildLevels, the walk stops
// early rather than push a bid below priceFloor.
func topUp(rng *rand.Rand, spec SymbolSpec, side []level, anchor float64, desc bool) []level {
	if len(side) == 0 {
		if desc && anchor < priceFloor {
			anchor = priceFloor
		}
		side = append(side, drawLevel(rng, spec, anchor))
	}
	for len(side) < bookDepth {
		last := side[len(side)-1]
		var price float64
		if desc {
			price = round2(last.Price - tickStep(rng))
			if price < priceFloor {
				break
			}
		} else {
			price = round2(last.Price + tickStep(rng))
		}
		side = append(side, drawLevel(rng, spec, price))
	}
	return side
}

// plantRoundWall finds the round-number price ($ or $0.50) nearest each
// side's own touch and, if it falls within that side's existing ladder,
// boosts that level's size to simulate a resting institutional order. bids
// and asks are checked against their own round-number target — under a wide
// (e.g. flushed) spread the two touches can straddle a $0.50 boundary
// asymmetrically, so a single shared target computed from one side's touch
// would misplace the other side's wall.
func plantRoundWall(rng *rand.Rand, spec SymbolSpec, bids, asks *[]level) {
	wallSize := int64(spec.BookMeanSize * between(rng, 3, 6))
	if wallSize < 1 {
		wallSize = 1
	}

	if len(*bids) > 0 {
		round := math.Round((*bids)[0].Price*2) / 2 // nearest $0.50
		for i := range *bids {
			if math.Abs((*bids)[i].Price-round) < 0.005 {
				(*bids)[i].Size += wallSize
				(*bids)[i].Orders = ordersFor((*bids)[i].Size)
				return
			}
		}
	}
	if len(*asks) > 0 {
		round := math.Round((*asks)[0].Price*2) / 2 // nearest $0.50
		for i := range *asks {
			if math.Abs((*asks)[i].Price-round) < 0.005 {
				(*asks)[i].Size += wallSize
				(*asks)[i].Orders = ordersFor((*asks)[i].Size)
				return
			}
		}
	}
}

// fixCrossed nudges the touch apart by a cent if a deep sweep left the book
// crossed or locked (bestBid >= bestAsk).
func (b *bookState) fixCrossed() {
	if len(b.bids) == 0 || len(b.asks) == 0 {
		return
	}
	if b.bids[0].Price >= b.asks[0].Price {
		b.asks[0].Price = round2(b.bids[0].Price + 0.01)
	}
}

// best returns the current touch prices.
func (b *bookState) best() (bid, ask float64) {
	if len(b.bids) > 0 {
		bid = b.bids[0].Price
	}
	if len(b.asks) > 0 {
		ask = b.asks[0].Price
	}
	return bid, ask
}

// snapshot copies b into a feed.Book replacement snapshot for symbol at
// tsMs. feed.BookLevel names its size field Volume, not Size.
func (b *bookState) snapshot(symbol string, tsMs int64) feed.Book {
	return feed.Book{
		Symbol: symbol,
		TsMs:   tsMs,
		Bids:   toFeedLevels(b.bids),
		Asks:   toFeedLevels(b.asks),
	}
}

func toFeedLevels(levels []level) []feed.BookLevel {
	out := make([]feed.BookLevel, len(levels))
	for i, lv := range levels {
		out[i] = feed.BookLevel{Price: lv.Price, Volume: lv.Size, Orders: lv.Orders}
	}
	return out
}

// drawLevel builds one level at price with a lognormal size drawn from
// spec's book-size distribution.
func drawLevel(rng *rand.Rand, spec SymbolSpec, price float64) level {
	size := lognormalSize(rng, spec.BookMeanSize, spec.BookSizeSigma)
	return level{Price: price, Size: size, Orders: ordersFor(size)}
}

// lognormalSize draws a level size from a lognormal distribution with the
// given mean and sigma, floored at 1 share.
func lognormalSize(rng *rand.Rand, mean, sigma float64) int64 {
	if mean <= 0 {
		mean = 1
	}
	sigmaFrac := 0.5
	if mean > 0 {
		sigmaFrac = sigma / mean
	}
	size := int64(math.Exp(rng.NormFloat64()*sigmaFrac) * mean)
	if size < 1 {
		size = 1
	}
	return size
}

// ordersFor derives a plausible synthetic order count for a level of the
// given size: roughly size/roundLot, with a floor of 1.
func ordersFor(size int64) int32 {
	n := int32(size / roundLot)
	if n < 1 {
		n = 1
	}
	return n
}

// tickStep draws the price gap (in dollars) to the next level out from the
// touch: a fraction of a cent to a few cents.
func tickStep(rng *rand.Rand) float64 {
	return round2(between(rng, 0.01, 0.03))
}

// round2 rounds px to the nearest cent.
func round2(px float64) float64 {
	return math.Round(px*100) / 100
}
