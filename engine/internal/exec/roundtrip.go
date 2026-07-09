package exec

import "math"

// ClosedTrade is one completed round trip on a single (venue,symbol): a
// position that started flat, was opened (and possibly scaled in/out), and
// returned to flat. IsLong reports the trip's direction: true when it was
// opened by a longward fill (BUY), false when opened by a shortward fill
// (SHORT). EntryPrice/ExitPrice are quantity-weighted averages across every
// fill that contributed to the opening/closing side, respectively.
type ClosedTrade struct {
	Venue      VenueID
	Symbol     string
	IsLong     bool
	Qty        float64
	EntryPrice float64
	ExitPrice  float64
	Realized   float64
	OpenMs     int64
	CloseMs    int64
	Seq        int64
}

// roundTripKey identifies one independent position accumulator.
type roundTripKey struct {
	Venue  VenueID
	Symbol string
}

// openTrip is the in-progress accumulator for the current round trip on one
// (venue,symbol). It is discarded (the key removed from the aggregator's map)
// the instant the position returns to flat.
type openTrip struct {
	isLong        bool
	openMs        int64
	openQty       float64 // cumulative opening-side qty (entry weighting)
	openNotional  float64 // cumulative opening-side qty*price
	closeQty      float64 // cumulative closing-side qty (exit weighting)
	closeNotional float64 // cumulative closing-side qty*price
	cash          float64 // signed cash flow accumulated so far this trip
	running       float64 // signed position qty (positive long, negative short)
}

// RoundTripAggregator folds a stream of fills into ClosedTrade records, one
// per (venue,symbol) position that opens from flat and later returns to flat.
//
// Realized P&L for a round trip is exact net cash flow across the trip's
// life: BUY/COVER fills pay cash (outflow), SELL/SHORT fills receive cash
// (inflow). Because a round trip starts and ends flat, summing signed cash
// flow already nets out scale-ins and scale-outs correctly — no FIFO
// lot-matching is needed, and the formula is the same whether the trip is
// long or short.
type RoundTripAggregator struct {
	trips map[roundTripKey]*openTrip
	seq   int64
}

// NewRoundTripAggregator returns an aggregator with no open trips. Every
// (venue,symbol) key is assumed flat until its first fill.
func NewRoundTripAggregator() *RoundTripAggregator {
	return &RoundTripAggregator{trips: map[roundTripKey]*openTrip{}}
}

// cashSign is the fill's contribution sign to cash flow: SELL/SHORT receive
// cash (+1), BUY/COVER pay cash (-1).
func cashSign(side Side) float64 {
	if side == SideSell || side == SideShort {
		return 1
	}
	return -1
}

// Apply folds one fill into the aggregator's per-(venue,symbol) accumulator
// and returns any round trips the fill just closed. A fill closes at most one
// trip: either it fully flattens the running position (one emit), flips it
// (one emit for the old trip, plus a freshly opened new trip that is not yet
// closed), or neither (no emit).
func (a *RoundTripAggregator) Apply(venue VenueID, symbol string, side Side, qty, price float64, tsMs int64) []ClosedTrade {
	key := roundTripKey{Venue: venue, Symbol: symbol}
	t := a.trips[key]

	d := qty
	if !longward(side) {
		d = -qty
	}
	sign := cashSign(side)

	if t == nil {
		// Flat: open a new trip.
		a.trips[key] = &openTrip{
			isLong:       longward(side),
			openMs:       tsMs,
			openQty:      qty,
			openNotional: price * qty,
			cash:         sign * price * qty,
			running:      d,
		}
		return nil
	}

	if (t.running > 0) == (d > 0) {
		// Same sign as the running position: scale-in, accumulate opening side.
		t.openQty += qty
		t.openNotional += price * qty
		t.cash += sign * price * qty
		t.running += d
		return nil
	}

	// Opposite sign: this fill closes some or all of the running trip.
	closeQty := math.Min(qty, math.Abs(t.running))
	t.closeQty += closeQty
	t.closeNotional += price * closeQty
	t.cash += sign * price * closeQty
	newRunning := t.running + d

	if newRunning == 0 {
		trade := t.toClosedTrade(venue, symbol, tsMs, a.nextSeq())
		delete(a.trips, key)
		return []ClosedTrade{trade}
	}

	if (newRunning > 0) != (t.running > 0) {
		// Flip: close the old trip, then open a new one with the remainder at
		// the same fill price (the split is exact — both halves share price).
		trade := t.toClosedTrade(venue, symbol, tsMs, a.nextSeq())
		remainder := qty - closeQty
		a.trips[key] = &openTrip{
			isLong:       longward(side),
			openMs:       tsMs,
			openQty:      remainder,
			openNotional: price * remainder,
			cash:         sign * price * remainder,
			running:      newRunning,
		}
		return []ClosedTrade{trade}
	}

	// Partial close: same-signed position remains, trip stays open.
	t.running = newRunning
	return nil
}

// toClosedTrade builds the ClosedTrade for a trip whose position has just
// returned to (or flipped through) flat.
func (t *openTrip) toClosedTrade(venue VenueID, symbol string, closeMs, seq int64) ClosedTrade {
	return ClosedTrade{
		Venue:      venue,
		Symbol:     symbol,
		IsLong:     t.isLong,
		Qty:        t.openQty,
		EntryPrice: t.openNotional / t.openQty,
		ExitPrice:  t.closeNotional / t.closeQty,
		Realized:   t.cash,
		OpenMs:     t.openMs,
		CloseMs:    closeMs,
		Seq:        seq,
	}
}

// nextSeq is a single monotonic counter for the aggregator's whole lifetime
// (not per key) — later used as a cross-venue/cross-symbol dedup key.
func (a *RoundTripAggregator) nextSeq() int64 {
	a.seq++
	return a.seq
}
