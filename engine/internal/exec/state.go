package exec

// VenueState is one venue's execution state. Orders + Fills are event-sourced
// (this file); Positions + Account are broker-reconciled (reconcile.go). Arming
// is master-only now (State.MasterArmed) — no per-venue arm switch.
type VenueState struct {
	Orders    map[string]Order
	Fills     []Fill
	Positions map[string]Position
	Account   AccountSnapshot
}

func newVenueState() *VenueState {
	return &VenueState{Orders: map[string]Order{}, Positions: map[string]Position{}}
}

// State is the whole multi-venue execution state, owned by the single Core
// writer goroutine. orderIndex maps every order ID ever seen (working, terminal,
// or blocked) to its venue — the global duplicate-ID check and broker-event
// routing both read it.
type State struct {
	MasterArmed bool
	Venues      map[VenueID]*VenueState
	orderIndex  map[string]VenueID
}

// NewState builds empty state for a fixed venue set (venues come from config;
// unknown-venue events are ignored defensively).
func NewState(venues []VenueID) *State {
	s := &State{Venues: map[VenueID]*VenueState{}, orderIndex: map[string]VenueID{}}
	for _, v := range venues {
		s.Venues[v] = newVenueState()
	}
	return s
}

// Venue returns the venue state, creating it if the venue was not pre-registered
// (keeps the fold total for logs that predate a config change).
func (s *State) Venue(v VenueID) *VenueState {
	vs, ok := s.Venues[v]
	if !ok {
		vs = newVenueState()
		s.Venues[v] = vs
	}
	return vs
}

// OrderVenue reports which venue owns an order ID.
func (s *State) OrderVenue(orderID string) (VenueID, bool) {
	v, ok := s.orderIndex[orderID]
	return v, ok
}

// Apply folds one persisted event into state. Deterministic and I/O-free — the
// basis of replay(log) == state. Account/position events are handled by
// ApplyReconcile (reconcile.go), not here.
func (s *State) Apply(ev Event) {
	switch e := ev.(type) {
	case OrderSubmitted:
		o := e.Order
		if o.LeavesQty == 0 && o.ExecutedQty == 0 {
			o.LeavesQty = o.Qty
		}
		s.Venue(o.Venue).Orders[o.ID] = o
		s.orderIndex[o.ID] = o.Venue
	case OrderBlocked:
		// Recorded for the duplicate-ID defense; terminal, never working.
		vs := s.Venue(e.V)
		vs.Orders[e.OID] = Order{Venue: e.V, ID: e.OID, Symbol: e.Req.Symbol, Side: e.Req.Side,
			Type: e.Req.Type, TIF: e.Req.TIF, Qty: e.Req.Qty, LimitPrice: e.Req.LimitPrice,
			StopPrice: e.Req.StopPrice, Status: StatusBlocked, RejectReason: e.Reason,
			CreatedMs: e.Ts, UpdatedMs: e.Ts}
		s.orderIndex[e.OID] = e.V
	case OrderAccepted:
		s.mutate(e.V, e.OID, e.Ts, func(o *Order) { o.Status = StatusAccepted })
	case OrderRejected:
		s.mutate(e.V, e.OID, e.Ts, func(o *Order) { o.Status = StatusRejected; o.RejectReason = e.Reason })
	case OrderFilled:
		s.applyFill(e)
	case OrderCanceled:
		s.mutate(e.V, e.OID, e.Ts, func(o *Order) {
			if o.Working() {
				o.Status = StatusCanceled
			}
		})
	case OrderExpired:
		s.mutate(e.V, e.OID, e.Ts, func(o *Order) {
			if o.Working() {
				o.Status = StatusExpired
			}
		})
	case OrderReplaced:
		s.mutate(e.V, e.OID, e.Ts, func(o *Order) {
			if !o.Working() {
				return
			}
			o.Qty = e.NewQty
			if e.NewLimit > 0 {
				o.LimitPrice = e.NewLimit
			}
			if e.NewStop > 0 {
				o.StopPrice = e.NewStop
			}
			o.LeavesQty = e.NewQty - o.ExecutedQty
			o.Status = StatusAccepted
		})
	case StreamGap:
		// A gap marker: reconcile (Core) resolves state against a fresh snapshot.
		// The fold records nothing here; the marker exists for audit + replay.
	}
}

// mutate applies fn to an existing order and stamps UpdatedMs; no-op if unknown.
func (s *State) mutate(v VenueID, id string, ts int64, fn func(*Order)) {
	vs := s.Venue(v)
	o, ok := vs.Orders[id]
	if !ok {
		return
	}
	fn(&o)
	o.UpdatedMs = ts
	vs.Orders[id] = o
}

func (s *State) applyFill(e OrderFilled) {
	vs := s.Venue(e.F.Venue)
	vs.Fills = append(vs.Fills, e.F)
	o, ok := vs.Orders[e.F.OrderID]
	if !ok {
		// Fill for an order we never saw submitted (reconcile gap): index it.
		o = Order{Venue: e.F.Venue, ID: e.F.OrderID, Symbol: e.F.Symbol, Side: e.F.Side, CreatedMs: e.F.TsMs}
		s.orderIndex[e.F.OrderID] = e.F.Venue
	}
	o.ExecutedQty = e.CumQty
	o.LeavesQty = e.LeavesQty
	o.AvgFillPrice = e.AvgPrice
	if e.LeavesQty <= 0 {
		o.Status = StatusFilled
	} else {
		o.Status = StatusPartiallyFilled
	}
	o.UpdatedMs = e.F.TsMs
	vs.Orders[o.ID] = o
}

// Replay folds a full log from empty state — the boot path and the property-test
// entry point.
func Replay(events []Event, venues []VenueID) *State {
	s := NewState(venues)
	for _, ev := range events {
		s.Apply(ev)
	}
	return s
}
