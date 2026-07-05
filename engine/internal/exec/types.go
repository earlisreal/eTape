// Package exec is eTape's broker-agnostic execution domain: venue-keyed orders,
// fills, positions, and accounts; the append-only event log and its fold; the
// two-layer safety gate; and the Broker/EventStore interfaces adapters and the
// store satisfy. It imports only clock and session (domain siblings) — never
// store, md, uihub, broker adapters, or feed/opend.
package exec

import (
	"errors"
	"fmt"
)

type VenueID string

type Side uint8

const (
	SideBuy Side = iota
	SideSell
	SideShort
	SideCover
)

func (s Side) String() string {
	switch s {
	case SideBuy:
		return "BUY"
	case SideSell:
		return "SELL"
	case SideShort:
		return "SHORT"
	case SideCover:
		return "COVER"
	default:
		return fmt.Sprintf("Side(%d)", uint8(s))
	}
}

type OrderType uint8

const (
	TypeMarket OrderType = iota
	TypeLimit
)

func (t OrderType) String() string {
	switch t {
	case TypeMarket:
		return "MARKET"
	case TypeLimit:
		return "LIMIT"
	default:
		return fmt.Sprintf("OrderType(%d)", uint8(t))
	}
}

type TIF uint8

const (
	TIFDay TIF = iota
	TIFGTC
	TIFIOC
	TIFFOK
)

func (t TIF) String() string {
	switch t {
	case TIFDay:
		return "DAY"
	case TIFGTC:
		return "GTC"
	case TIFIOC:
		return "IOC"
	case TIFFOK:
		return "FOK"
	default:
		return fmt.Sprintf("TIF(%d)", uint8(t))
	}
}

type OrderStatus uint8

const (
	StatusSubmitted OrderStatus = iota
	StatusAccepted
	StatusPartiallyFilled
	StatusFilled
	StatusCanceled
	StatusRejected
	StatusExpired
	StatusBlocked
	StatusReplaced
)

func (s OrderStatus) String() string {
	switch s {
	case StatusSubmitted:
		return "SUBMITTED"
	case StatusAccepted:
		return "ACCEPTED"
	case StatusPartiallyFilled:
		return "PARTIALLY_FILLED"
	case StatusFilled:
		return "FILLED"
	case StatusCanceled:
		return "CANCELED"
	case StatusRejected:
		return "REJECTED"
	case StatusExpired:
		return "EXPIRED"
	case StatusBlocked:
		return "BLOCKED"
	case StatusReplaced:
		return "REPLACED"
	default:
		return fmt.Sprintf("OrderStatus(%d)", uint8(s))
	}
}

// Order is one order's full lifecycle state. Working = Status in
// {Submitted, Accepted, PartiallyFilled}.
type Order struct {
	Venue        VenueID
	ID           string
	Symbol       string
	Side         Side
	Type         OrderType
	TIF          TIF
	Qty          float64
	LimitPrice   float64
	StopPrice    float64
	Status       OrderStatus
	ExecutedQty  float64
	LeavesQty    float64
	AvgFillPrice float64
	RejectReason string
	ReplacesID   string
	CreatedMs    int64
	UpdatedMs    int64
}

// Working reports whether the order can still fill or be canceled.
func (o Order) Working() bool {
	return o.Status == StatusSubmitted || o.Status == StatusAccepted || o.Status == StatusPartiallyFilled
}

type Fill struct {
	Venue   VenueID
	OrderID string
	Symbol  string
	Side    Side
	Qty     float64
	Price   float64
	TsMs    int64
}

// Position mirrors the broker's authoritative per-symbol position; Qty is signed
// (positive long, negative short).
type Position struct {
	Venue    VenueID
	Symbol   string
	Qty      float64
	AvgPrice float64
}

type AccountSnapshot struct {
	Venue         VenueID
	Equity        float64
	BuyingPower   float64
	AvailableCash float64
	SodEquity     float64
	Realized      float64
	DayPnL        float64
	Leverage      float64
	TsMs          int64
}

// OrderRequest is a fully-specified order to one venue. ClientOrderID is set by
// the Core before the gate runs; adapters echo it back on order events.
type OrderRequest struct {
	Venue         VenueID
	Symbol        string
	Side          Side
	Type          OrderType
	TIF           TIF
	Qty           float64
	LimitPrice    float64
	StopPrice     float64
	ClientOrderID string
}

// Validate enforces the "a request without a valid venue is malformed" rule and
// basic field sanity. The gate performs the risk checks; this is structural.
func (r OrderRequest) Validate() error {
	if r.Venue == "" {
		return errors.New("exec: order request missing venue")
	}
	if r.Symbol == "" {
		return errors.New("exec: order request missing symbol")
	}
	if r.Qty <= 0 {
		return fmt.Errorf("exec: order request qty %v must be > 0", r.Qty)
	}
	if r.Type == TypeLimit && r.LimitPrice <= 0 {
		return fmt.Errorf("exec: limit order missing limit price")
	}
	return nil
}

type ReplaceRequest struct {
	Qty        float64
	LimitPrice float64
	StopPrice  float64
}

type OrderAck struct {
	OrderID  string
	Accepted bool
	Message  string
}

// EventEnvelope is the persisted form of an Event: the indexed columns plus the
// JSON payload. Defined in exec so the store imports exec (never the reverse).
type EventEnvelope struct {
	Seq     int64
	TsMs    int64
	Source  string
	Venue   string
	OrderID string
	Kind    string
	Payload []byte
}

// FillRow is the fills-projection row written in the same transaction as an
// OrderFilled event.
type FillRow struct {
	OrderID string
	Symbol  string
	Side    string
	Qty     float64
	Price   float64
	TsMs    int64
	Venue   string
}
