package exec

import "context"

// Capabilities advertises what a venue's adapter supports natively so the Core
// and gate can adapt (e.g. TZ emulates replace; only Alpaca flattens).
type Capabilities struct {
	NativeReplace    bool // Alpaca PATCH, moomoo ModifyOrder-Normal; TZ false
	FlattenAll       bool // Alpaca DELETE /v2/positions only
	OvernightSession bool // Alpaca (Blue Ocean), moomoo (OVERNIGHT); TZ false
}

// Broker is the per-venue adapter contract. One instance per configured venue;
// implemented by broker/sim here and broker/tradezero, broker/alpaca in Plan 5.
type Broker interface {
	Capabilities() Capabilities
	SubmitOrder(ctx context.Context, req OrderRequest) (OrderAck, error)
	ReplaceOrder(ctx context.Context, orderID string, req ReplaceRequest) error
	CancelOrder(ctx context.Context, orderID string) error
	CancelAll(ctx context.Context, symbol string) error
	Snapshot(ctx context.Context) (AccountSnapshot, []Position, []Order, error)
	Events() <-chan BrokerEvent
}

// BrokerEvent is anything a Broker pushes: order-lifecycle events (which also
// satisfy Event and are persisted), connection transitions, and account/position
// reconcile snapshots (which are not persisted).
type BrokerEvent interface{ isBrokerEvent() }

// Order-lifecycle events are emitted by adapters AND persisted.
func (OrderAccepted) isBrokerEvent() {}
func (OrderRejected) isBrokerEvent() {}
func (OrderFilled) isBrokerEvent()   {}
func (OrderCanceled) isBrokerEvent() {}
func (OrderExpired) isBrokerEvent()  {}
func (OrderReplaced) isBrokerEvent() {}
func (StreamGap) isBrokerEvent()     {}

type BrokerConnUp struct{ V VenueID }
type BrokerConnDown struct{ V VenueID }
type BrokerAccount struct{ Account AccountSnapshot }
type BrokerPositions struct {
	V         VenueID
	Positions []Position
}

func (BrokerConnUp) isBrokerEvent()    {}
func (BrokerConnDown) isBrokerEvent()  {}
func (BrokerAccount) isBrokerEvent()   {}
func (BrokerPositions) isBrokerEvent() {}

// Mark is a last-trade price the gate values market orders against and the Core
// marks positions with. Its shape matches md.Mark; Plan 6 bridges the two.
type Mark struct {
	Symbol string
	Price  float64
	TsMs   int64
}

// MarkSource reads the latest trade price for a symbol.
type MarkSource interface {
	LastTrade(symbol string) (price float64, ok bool)
}

// EventStore is the persistence seam. Implemented by *store.Store (Task 5).
// AppendExecEvent is synchronous and error-returning: append failure blocks the
// order. ReadExecEventsSince returns events with TsMs >= fromMs, ordered by seq
// (the boot-replay input).
type EventStore interface {
	AppendExecEvent(env EventEnvelope, fill *FillRow) (seq int64, err error)
	ReadExecEventsSince(fromMs int64) ([]EventEnvelope, error)
}
