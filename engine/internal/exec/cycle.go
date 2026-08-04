package exec

import "math"

// CycleCheckpoint is the small durable projection needed to recover the
// account panel's close-to-close P&L without changing broker risk values.
type CycleCheckpoint struct {
	Venue     VenueID                  `json:"venue"`
	StartMs   int64                    `json:"startMs"`
	Realized  float64                  `json:"realized"`
	Positions map[string]CyclePosition `json:"positions"`
}

type CyclePosition struct {
	Qty      float64 `json:"qty"`
	Basis    float64 `json:"basis"`
	Realized float64 `json:"realized"`
	Carried  float64 `json:"carried"`
}

type cycleProjection struct {
	byVenue map[VenueID]*CycleCheckpoint
	marks   map[string]float64
}

func newCycleProjection() *cycleProjection {
	return &cycleProjection{byVenue: map[VenueID]*CycleCheckpoint{}, marks: map[string]float64{}}
}

func (c *cycleProjection) bootstrap(venue VenueID, startMs int64, positions []Position) {
	cp := &CycleCheckpoint{Venue: venue, StartMs: startMs, Positions: map[string]CyclePosition{}}
	for _, p := range positions {
		if p.Venue == venue && p.Qty != 0 {
			basis := p.AvgPrice
			if m := c.marks[p.Symbol]; m != 0 {
				basis = m
			}
			cp.Positions[p.Symbol] = CyclePosition{Qty: p.Qty, Basis: basis, Carried: p.Qty}
		}
	}
	c.byVenue[venue] = cp
}

func (c *cycleProjection) restore(cp CycleCheckpoint) {
	if cp.Positions == nil {
		cp.Positions = map[string]CyclePosition{}
	}
	c.byVenue[cp.Venue] = &cp
}

func (c *cycleProjection) applyFill(f Fill) {
	cp := c.byVenue[f.Venue]
	if cp == nil {
		return
	}
	p := cp.Positions[f.Symbol]
	delta := f.Qty
	if !longward(f.Side) {
		delta = -delta
	}
	if p.Qty == 0 {
		cp.Positions[f.Symbol] = CyclePosition{Qty: delta, Basis: f.Price}
		return
	}
	if (p.Qty > 0) == (delta > 0) {
		p.Basis = (p.Basis*math.Abs(p.Qty) + f.Price*f.Qty) / (math.Abs(p.Qty) + f.Qty)
		p.Qty += delta
		cp.Positions[f.Symbol] = p
		return
	}
	closed := math.Min(math.Abs(p.Qty), f.Qty)
	realized := (f.Price - p.Basis) * closed
	if p.Qty < 0 {
		realized = -realized
	}
	cp.Realized += realized
	p.Realized += realized
	next := p.Qty + delta
	if next == 0 {
		delete(cp.Positions, f.Symbol)
	} else if (next > 0) != (p.Qty > 0) {
		cp.Positions[f.Symbol] = CyclePosition{Qty: next, Basis: f.Price}
	} else {
		p.Qty = next
		cp.Positions[f.Symbol] = p
	}
}

func (c *cycleProjection) mark(symbol string, price float64) {
	if price > 0 {
		c.marks[symbol] = price
	}
}

func (c *cycleProjection) account(venue VenueID) (start int64, openRealized, cycleRealized, day float64) {
	cp := c.byVenue[venue]
	if cp == nil {
		return
	}
	start, cycleRealized, day = cp.StartMs, cp.Realized, cp.Realized
	for symbol, p := range cp.Positions {
		openRealized += p.Realized
		if mark := c.marks[symbol]; mark != 0 {
			day += (mark - p.Basis) * p.Qty
		}
	}
	return
}

func (c *cycleProjection) position(venue VenueID, symbol string) CyclePosition {
	if cp := c.byVenue[venue]; cp != nil {
		return cp.Positions[symbol]
	}
	return CyclePosition{}
}

func (c *cycleProjection) reset(venue VenueID, startMs int64, positions []Position) {
	c.bootstrap(venue, startMs, positions)
}
