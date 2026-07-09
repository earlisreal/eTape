package exec

// Update is a typed change the Core emits for uihub (Plan 6 maps these to the
// exec.* WS topics and owns coalescing). Sealed union.
type Update interface{ isExecUpdate() }

type OrderUpdate struct{ Order Order }
type FillUpdate struct{ Fill Fill }
type AccountUpdate struct {
	Account     AccountSnapshot
	MasterArmed bool
}
type PositionUpdate struct{ Position Position }
type StatusUpdate struct {
	Venue       VenueID
	Connected   bool
	MasterArmed bool
	Note        string
}
type TradeUpdate struct{ Trade ClosedTrade }

func (OrderUpdate) isExecUpdate()    {}
func (FillUpdate) isExecUpdate()     {}
func (AccountUpdate) isExecUpdate()  {}
func (PositionUpdate) isExecUpdate() {}
func (StatusUpdate) isExecUpdate()   {}
func (TradeUpdate) isExecUpdate()    {}
