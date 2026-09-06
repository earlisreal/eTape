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

// OpenMs returns the opening fill time for the current (venue,symbol) trip.
// Zero means the position was seeded without an observed opening fill.
func (a *RoundTripAggregator) OpenMs(venue VenueID, symbol string) int64 {
	if t := a.trips[roundTripKey{Venue: venue, Symbol: symbol}]; t != nil {
		return t.openMs
	}
	return 0
}

// seedPosition seeds an existing broker position without inventing an opening
// time. It is used by the position-time tracker, not Trade History.
func (a *RoundTripAggregator) seedPosition(p Position) {
	if p.Qty == 0 {
		return
	}
	qty := math.Abs(p.Qty)
	a.trips[roundTripKey{Venue: p.Venue, Symbol: p.Symbol}] = &openTrip{
		isLong:       p.Qty > 0,
		openMs:       p.OpenedMs,
		openQty:      qty,
		openNotional: qty * p.AvgPrice,
		running:      p.Qty,
	}
}

// reconcilePositions keeps the tracker aligned with a broker's full snapshot.
// Existing same-direction rows retain their opening time; new or direction-
// changed rows start unknown unless the broker supplied an exact timestamp.
func (a *RoundTripAggregator) reconcilePositions(venue VenueID, ps []Position) {
	seen := map[roundTripKey]struct{}{}
	for _, p := range ps {
		if p.Qty == 0 {
			continue
		}
		key := roundTripKey{Venue: venue, Symbol: p.Symbol}
		p.Venue = venue
		seen[key] = struct{}{}
		t := a.trips[key]
		if t == nil || (t.running > 0) != (p.Qty > 0) {
			a.seedPosition(p)
			continue
		}
		t.running = p.Qty
		if t.openMs == 0 && p.OpenedMs > 0 {
			t.openMs = p.OpenedMs
		}
	}
	for key := range a.trips {
		if key.Venue != venue {
			continue
		}
		if _, ok := seen[key]; !ok {
			delete(a.trips, key)
		}
	}
}

// rebuildOpenPositions reconstructs active opening times from a final broker
// snapshot plus the persisted fills in chronological order. The reverse pass
// recovers the unknown starting quantity so carried positions stay unknown.
// ponytail: this assumes the queried fills cover the snapshot's changes; a
// native broker opening-time field is the upgrade path for unobserved fills.
func (a *RoundTripAggregator) rebuildOpenPositions(positions []Position, fills []Fill) {
	a.trips = map[roundTripKey]*openTrip{}
	current := map[roundTripKey]Position{}
	qty := map[roundTripKey]float64{}
	for _, p := range positions {
		if p.Qty == 0 {
			continue
		}
		key := roundTripKey{Venue: p.Venue, Symbol: p.Symbol}
		current[key] = p
		qty[key] = p.Qty
	}
	for i := len(fills) - 1; i >= 0; i-- {
		f := fills[i]
		key := roundTripKey{Venue: f.Venue, Symbol: f.Symbol}
		qty[key] -= signedFillQty(f)
	}
	for key, q := range qty {
		if q == 0 {
			continue
		}
		p := current[key]
		p.Venue, p.Symbol, p.Qty = key.Venue, key.Symbol, q
		a.seedPosition(p)
	}
	for _, f := range fills {
		a.Apply(f.Venue, f.Symbol, f.Side, f.Qty, f.Price, f.TsMs)
	}
}

func signedFillQty(f Fill) float64 {
	if longward(f.Side) {
		return f.Qty
	}
	return -f.Qty
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
