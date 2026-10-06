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
		p.Venue = v
		vs.Positions[p.Symbol] = p
	}
}

// ReconcilePosition applies one absolute symbol update without clearing sibling positions.
func (s *State) ReconcilePosition(p Position) {
	vs := s.Venue(p.Venue)
	if p.Qty == 0 {
		delete(vs.Positions, p.Symbol)
	} else {
		vs.Positions[p.Symbol] = p
	}
}

func (s *State) SetPositionsReady(v VenueID, ready bool) { s.Venue(v).PositionsReady = ready }

func (s *State) PositionsReady(v VenueID) bool {
	vs, ok := s.Venues[v]
	return ok && vs.PositionsReady
}

// ReconcileOpenOrders adopts the broker's working-order set on boot/reconnect.
// Orders the broker reports that the log did not are inserted; log orders the
// broker no longer reports as working are left as-is (their terminal transition,
// if any, arrives as a synthesized reconcile event from the adapter — Plan 5).
func (s *State) ReconcileOpenOrders(v VenueID, orders []Order) {
	vs := s.Venue(v)
	for _, o := range orders {
		o.Venue = v
		if parent, ok := vs.Orders[o.ID]; ok && parent.Held != nil {
			o.RiskEntry, o.RiskEntryID = parent.RiskEntry, parent.RiskEntryID
			o.Type, o.StopPrice, o.Held = parent.Type, parent.StopPrice, parent.Held
			o.DeferredPositionPct = parent.DeferredPositionPct
			if parent.Held.ResolvedQty > 0 {
				o.Qty = parent.Qty
			}
		}
		if parent, ok := vs.Orders[o.ID]; ok && parent.Action != nil {
			o.Action = parent.Action
		}
		vs.Orders[o.ID] = o
		s.orderIndex[o.ID] = v
	}
}

func (s *State) ReconcileExternalOpenOrders(v VenueID, orders []Order) {
	vs := s.Venue(v)
	vs.ExternalOrders = make(map[string]Order, len(orders))
	for _, o := range orders {
		if o.ID == "" || !o.Working() {
			continue
		}
		o.Venue = v
		vs.ExternalOrders[o.ID] = o
	}
}

func (s *State) SetExternalOrder(v VenueID, order Order, working bool) {
	vs := s.Venue(v)
	if order.ID == "" {
		return
	}
	if !working {
		delete(vs.ExternalOrders, order.ID)
		return
	}
	order.Venue = v
	vs.ExternalOrders[order.ID] = order
}

func (s *State) SetFlattenPending(v VenueID, pending bool) { s.Venue(v).FlattenPending = pending }

// SetMasterArmed flips the master switch. Not persisted — boot is always
// disarmed.
func (s *State) SetMasterArmed(on bool) { s.MasterArmed = on }

func (s *State) SetActiveVenue(v VenueID) { s.ActiveVenue = v }

func (s *State) SetAccountFresh(v VenueID, fresh bool) {
	if s.AccountFresh == nil {
		s.AccountFresh = map[VenueID]bool{}
	}
	s.AccountFresh[v] = fresh
}

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

// VenueSellCommittedShares sums broker-bound SELL leaves; a local held parent
// does not reserve shares until it has an activating child.
func (s *State) VenueSellCommittedShares(v VenueID, symbol, excludeID string) float64 {
	vs, ok := s.Venues[v]
	if !ok {
		return 0
	}
	var qty float64
	for id, o := range vs.Orders {
		if id != excludeID && o.Symbol == symbol {
			qty += sellCommitmentLeaves(o)
		}
	}
	for _, o := range vs.ExternalOrders {
		if o.Symbol == symbol {
			qty += sellCommitmentLeaves(o)
		}
	}
	return qty
}

func sellCommitmentLeaves(o Order) float64 {
	if o.Side != SideSell || !o.Working() {
		return 0
	}
	if o.Held != nil && o.Held.ChildClientID == "" &&
		(o.Held.Phase == HeldWaiting || o.Held.Phase == HeldArmed || o.Held.Phase == HeldPaused) {
		return 0
	}
	leaves := o.LeavesQty
	if o.Action != nil && o.Action.Kind == ActionReplace &&
		(o.Action.Phase == ActionRequested || o.Action.Phase == ActionUnknown) {
		if requested := o.Action.RequestedQty - o.ExecutedQty; requested > leaves {
			leaves = requested
		}
	}
	if leaves > 0 {
		return leaves
	}
	return 0
}

func (s *State) VenueHasSellCommitments(v VenueID) bool {
	vs, ok := s.Venues[v]
	if !ok {
		return false
	}
	for _, o := range vs.Orders {
		if sellCommitmentLeaves(o) > 0 {
			return true
		}
	}
	for _, o := range vs.ExternalOrders {
		if sellCommitmentLeaves(o) > 0 {
			return true
		}
	}
	return false
}

func (s *State) FlattenReconciled(v VenueID) bool {
	vs, ok := s.Venues[v]
	if !ok || !vs.PositionsReady {
		return false
	}
	for _, p := range vs.Positions {
		if p.Qty > 1e-9 || p.Qty < -1e-9 {
			return false
		}
	}
	return true
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
