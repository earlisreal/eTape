package exec

import (
	"encoding/json"
	"fmt"
)

type Source uint8

const (
	SrcLocal Source = iota
	SrcWS
	SrcREST
	SrcReconcile
)

func (s Source) String() string {
	switch s {
	case SrcLocal:
		return "local"
	case SrcWS:
		return "ws"
	case SrcREST:
		return "rest"
	case SrcReconcile:
		return "reconcile"
	default:
		return fmt.Sprintf("Source(%d)", uint8(s))
	}
}

// Event is a persisted execution-log event. The single AUTOINCREMENT seq the
// store assigns gives the total order the fold replays; each event carries the
// fields the fold needs plus enough for the store envelope.
type Event interface {
	isExecEvent()
	Kind() string
	Venue() VenueID
	OrderID() string
	TsMs() int64
}

type OrderSubmitted struct{ Order Order }
type OrderAccepted struct {
	V             VenueID
	OID           string
	BrokerOrderID string
	Ts            int64
}
type OrderRejected struct {
	V      VenueID
	OID    string
	Reason string
	Ts     int64
}
type OrderBlocked struct {
	V      VenueID
	OID    string
	Req    OrderRequest
	Reason string
	Ts     int64
}
type OrderFilled struct {
	F         Fill
	CumQty    float64
	LeavesQty float64
	AvgPrice  float64
}
type OrderCanceled struct {
	V   VenueID
	OID string
	Ts  int64
}
type OrderExpired struct {
	V   VenueID
	OID string
	Ts  int64
}
type OrderReplaced struct {
	V        VenueID
	OID      string
	NewQty   float64
	NewLimit float64
	NewStop  float64
	Ts       int64
}
type StreamGap struct {
	V  VenueID
	Ts int64
}

func (OrderSubmitted) isExecEvent() {}
func (OrderAccepted) isExecEvent()  {}
func (OrderRejected) isExecEvent()  {}
func (OrderBlocked) isExecEvent()   {}
func (OrderFilled) isExecEvent()    {}
func (OrderCanceled) isExecEvent()  {}
func (OrderExpired) isExecEvent()   {}
func (OrderReplaced) isExecEvent()  {}
func (StreamGap) isExecEvent()      {}

func (OrderSubmitted) Kind() string { return "order_submitted" }
func (OrderAccepted) Kind() string  { return "order_accepted" }
func (OrderRejected) Kind() string  { return "order_rejected" }
func (OrderBlocked) Kind() string   { return "order_blocked" }
func (OrderFilled) Kind() string    { return "order_filled" }
func (OrderCanceled) Kind() string  { return "order_canceled" }
func (OrderExpired) Kind() string   { return "order_expired" }
func (OrderReplaced) Kind() string  { return "order_replaced" }
func (StreamGap) Kind() string      { return "stream_gap" }

func (e OrderSubmitted) Venue() VenueID { return e.Order.Venue }
func (e OrderAccepted) Venue() VenueID  { return e.V }
func (e OrderRejected) Venue() VenueID  { return e.V }
func (e OrderBlocked) Venue() VenueID   { return e.V }
func (e OrderFilled) Venue() VenueID    { return e.F.Venue }
func (e OrderCanceled) Venue() VenueID  { return e.V }
func (e OrderExpired) Venue() VenueID   { return e.V }
func (e OrderReplaced) Venue() VenueID  { return e.V }
func (e StreamGap) Venue() VenueID      { return e.V }

func (e OrderSubmitted) OrderID() string { return e.Order.ID }
func (e OrderAccepted) OrderID() string  { return e.OID }
func (e OrderRejected) OrderID() string  { return e.OID }
func (e OrderBlocked) OrderID() string   { return e.OID }
func (e OrderFilled) OrderID() string    { return e.F.OrderID }
func (e OrderCanceled) OrderID() string  { return e.OID }
func (e OrderExpired) OrderID() string   { return e.OID }
func (e OrderReplaced) OrderID() string  { return e.OID }
func (e StreamGap) OrderID() string      { return "" }

func (e OrderSubmitted) TsMs() int64 { return e.Order.UpdatedMs }
func (e OrderAccepted) TsMs() int64  { return e.Ts }
func (e OrderRejected) TsMs() int64  { return e.Ts }
func (e OrderBlocked) TsMs() int64   { return e.Ts }
func (e OrderFilled) TsMs() int64    { return e.F.TsMs }
func (e OrderCanceled) TsMs() int64  { return e.Ts }
func (e OrderExpired) TsMs() int64   { return e.Ts }
func (e OrderReplaced) TsMs() int64  { return e.Ts }
func (e StreamGap) TsMs() int64      { return e.Ts }

// EncodeEvent serializes an event to its kind + JSON payload (the concrete
// struct; sealed unions carry no struct tags, matching the feed/md convention).
func EncodeEvent(ev Event) (string, []byte, error) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return "", nil, fmt.Errorf("exec: encode %s: %w", ev.Kind(), err)
	}
	return ev.Kind(), payload, nil
}

// DecodeEvent reconstructs a typed event from its kind + JSON payload.
func DecodeEvent(kind string, payload []byte) (Event, error) {
	switch kind {
	case "order_submitted":
		var v OrderSubmitted
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("exec: decode %s: %w", kind, err)
		}
		return v, nil
	case "order_accepted":
		var v OrderAccepted
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("exec: decode %s: %w", kind, err)
		}
		return v, nil
	case "order_rejected":
		var v OrderRejected
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("exec: decode %s: %w", kind, err)
		}
		return v, nil
	case "order_blocked":
		var v OrderBlocked
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("exec: decode %s: %w", kind, err)
		}
		return v, nil
	case "order_filled":
		var v OrderFilled
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("exec: decode %s: %w", kind, err)
		}
		return v, nil
	case "order_canceled":
		var v OrderCanceled
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("exec: decode %s: %w", kind, err)
		}
		return v, nil
	case "order_expired":
		var v OrderExpired
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("exec: decode %s: %w", kind, err)
		}
		return v, nil
	case "order_replaced":
		var v OrderReplaced
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("exec: decode %s: %w", kind, err)
		}
		return v, nil
	case "stream_gap":
		var v StreamGap
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("exec: decode %s: %w", kind, err)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("exec: unknown event kind %q", kind)
	}
}

// EnvelopeOf builds the store envelope for an event (payload encoded inline).
func EnvelopeOf(ev Event, src Source, seq int64) EventEnvelope {
	kind, payload, err := EncodeEvent(ev)
	if err != nil {
		// A domain event that cannot encode is a programmer error; store a marker
		// rather than silently dropping (the coordinator treats an encode failure
		// as an append failure and blocks the order).
		payload = []byte(fmt.Sprintf("{\"encodeError\":%q}", err.Error()))
		kind = ev.Kind()
	}
	return EventEnvelope{
		Seq:     seq,
		TsMs:    ev.TsMs(),
		Source:  src.String(),
		Venue:   string(ev.Venue()),
		OrderID: ev.OrderID(),
		Kind:    kind,
		Payload: payload,
	}
}

// FillRowOf extracts the fills-projection row for OrderFilled events.
func FillRowOf(ev Event) (*FillRow, bool) {
	f, ok := ev.(OrderFilled)
	if !ok {
		return nil, false
	}
	return &FillRow{
		OrderID: f.F.OrderID,
		Symbol:  f.F.Symbol,
		Side:    f.F.Side.String(),
		Qty:     f.F.Qty,
		Price:   f.F.Price,
		TsMs:    f.F.TsMs,
		Venue:   string(f.F.Venue),
	}, true
}
