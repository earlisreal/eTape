package exec

// ReconcileAccount overwrites a venue's account snapshot (broker is authoritative;
// eTape mirrors). Not persisted.
func (s *State) ReconcileAccount(a AccountSnapshot) {
	s.Venue(a.Venue).Account = a
}

// ReconcilePositions replaces a venue's positions with the broker's authoritative
// set (a full snapshot per push; absent symbols mean flat). Not persisted.
func (s *State) ReconcilePositions(v VenueID, ps []Position) {
	vs := s.Venue(v)
	vs.Positions = make(map[string]Position, len(ps))
	for _, p := range ps {
		vs.Positions[p.Symbol] = p
	}
}

// ReconcileOpenOrders adopts the broker's working-order set on boot/reconnect.
// Orders the broker reports that the log did not are inserted; log orders the
// broker no longer reports as working are left as-is (their terminal transition,
// if any, arrives as a synthesized reconcile event from the adapter — Plan 5).
func (s *State) ReconcileOpenOrders(v VenueID, orders []Order) {
	vs := s.Venue(v)
	for _, o := range orders {
		o.Venue = v
		vs.Orders[o.ID] = o
		s.orderIndex[o.ID] = v
	}
}

// SetMasterArmed flips the master switch. Not persisted — boot is always
// disarmed.
func (s *State) SetMasterArmed(on bool) { s.MasterArmed = on }

// IsArmed reports whether trading on a venue is permitted: master armed AND
// the venue is registered.
func (s *State) IsArmed(v VenueID) bool {
	_, ok := s.Venues[v]
	return s.MasterArmed && ok
}

// VenuePositionShares is the signed share position for a symbol on one venue.
func (s *State) VenuePositionShares(v VenueID, symbol string) float64 {
	vs, ok := s.Venues[v]
	if !ok {
		return 0
	}
	return vs.Positions[symbol].Qty
}

// SymbolNetShares is the signed net position for a symbol summed across venues.
func (s *State) SymbolNetShares(symbol string) float64 {
	var net float64
	for _, vs := range s.Venues {
		net += vs.Positions[symbol].Qty
	}
	return net
}

// sameDir reports whether a side increases a long (Buy/Cover) or a short
// (Sell/Short) position in the same direction as `ref`.
func sameDir(a, b Side) bool { return longward(a) == longward(b) }

func longward(sd Side) bool { return sd == SideBuy || sd == SideCover }

// VenueWorkingSameDir sums leaves-qty of working orders on a venue whose side
// pushes the position the same way as `side`.
func (s *State) VenueWorkingSameDir(v VenueID, symbol string, side Side) float64 {
	vs, ok := s.Venues[v]
	if !ok {
		return 0
	}
	var q float64
	for _, o := range vs.Orders {
		if o.Symbol == symbol && o.Working() && sameDir(o.Side, side) {
			q += o.LeavesQty
		}
	}
	return q
}

// SymbolWorkingSameDir is VenueWorkingSameDir summed across venues.
func (s *State) SymbolWorkingSameDir(symbol string, side Side) float64 {
	var q float64
	for v := range s.Venues {
		q += s.VenueWorkingSameDir(v, symbol, side)
	}
	return q
}

// TotalDayPnL sums each venue's authoritative day P&L (adapter-sourced).
func (s *State) TotalDayPnL() float64 {
	var t float64
	for _, vs := range s.Venues {
		t += vs.Account.DayPnL
	}
	return t
}
