// Package wsmsg is the eTape engine<->UI WebSocket contract: pure DTO structs
// with explicit json tags. It imports stdlib only so tygo can compile it into
// ui/src/gen without pulling domain types. Domain->wire mappers live in the
// parent uihub package, never here.
package wsmsg

import "encoding/json"

// Topic is the logical channel a snapshot/delta belongs to.
type Topic string

const (
	TopicQuote     Topic = "md.quote"
	TopicBook      Topic = "md.book"
	TopicTape      Topic = "md.tape"
	TopicBars      Topic = "md.bars"
	TopicIndicator Topic = "md.indicator"

	TopicScannerRank Topic = "scanner.rank"
	TopicScannerHit  Topic = "scanner.hit"
	TopicNews        Topic = "news.item"

	TopicExecAccount   Topic = "exec.account"
	TopicExecPositions Topic = "exec.positions"
	TopicExecOrders    Topic = "exec.orders"
	TopicExecFills     Topic = "exec.fills"
	TopicExecStatus    Topic = "exec.status"

	TopicSysHealth Topic = "sys.health"
	TopicSysEvents Topic = "sys.events"
	TopicConfig    Topic = "config"
)

// AllTopics is the set a client may subscribe to (server-side allow-list).
var AllTopics = map[Topic]bool{
	TopicQuote: true, TopicBook: true, TopicTape: true, TopicBars: true, TopicIndicator: true,
	TopicScannerRank: true, TopicScannerHit: true, TopicNews: true,
	TopicExecAccount: true, TopicExecPositions: true, TopicExecOrders: true,
	TopicExecFills: true, TopicExecStatus: true,
	TopicSysHealth: true, TopicSysEvents: true, TopicConfig: true,
}

// Wire enum types (string literals matching ui/src/wire/contract.ts).
type Side string

const (
	SideBuy   Side = "BUY"
	SideSell  Side = "SELL"
	SideShort Side = "SHORT"
	SideCover Side = "COVER"
)

type OrderType string

const (
	OrderMarket    OrderType = "MARKET"
	OrderLimit     OrderType = "LIMIT"
	OrderStop      OrderType = "STOP"
	OrderStopLimit OrderType = "STOP_LIMIT"
)

type TIF string

const (
	TIFDay TIF = "DAY"
	TIFGTC TIF = "GTC"
	TIFIOC TIF = "IOC"
	TIFFOK TIF = "FOK"
)

type OrderStatus string

const (
	StatusSubmitted       OrderStatus = "SUBMITTED"
	StatusAccepted        OrderStatus = "ACCEPTED"
	StatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	StatusFilled          OrderStatus = "FILLED"
	StatusCanceled        OrderStatus = "CANCELED"
	StatusRejected        OrderStatus = "REJECTED"
	StatusExpired         OrderStatus = "EXPIRED"
	StatusBlocked         OrderStatus = "BLOCKED"
	StatusReplaced        OrderStatus = "REPLACED"
)

type TickDirection string

const (
	DirBuy     TickDirection = "BUY"
	DirSell    TickDirection = "SELL"
	DirNeutral TickDirection = "NEUTRAL"
)

type Broker string

const (
	BrokerTradeZero Broker = "tradezero"
	BrokerAlpaca    Broker = "alpaca"
	BrokerMoomoo    Broker = "moomoo"
)

// ---- server -> client frames ----
// Struct names carry the "Msg" suffix to match ui/src/wire/contract.ts exactly
// (SnapshotMsg/DeltaMsg/AckMsg/PongMsg/ResultMsg) so the tygo output is a
// drop-in for the interim hand-authored contract.

type SnapshotMsg struct {
	Kind    string `json:"kind"` // always "snapshot"
	Topic   Topic  `json:"topic"`
	Key     string `json:"key,omitempty"`
	Payload any    `json:"payload"`
}

type DeltaMsg struct {
	Kind    string `json:"kind"` // always "delta"
	Topic   Topic  `json:"topic"`
	Key     string `json:"key,omitempty"`
	Payload any    `json:"payload"`
}

type AckMsg struct {
	Kind    string `json:"kind"` // always "ack"
	CorrID  string `json:"corrId"`
	Status  string `json:"status"` // "accepted" | "blocked"
	Reason  string `json:"reason,omitempty"`
	OrderID string `json:"orderId,omitempty"`
	Value   any    `json:"value,omitempty"`
}

type PongMsg struct {
	Kind string `json:"kind"` // always "pong"
	T    int64  `json:"t"`
}

type ResultMsg struct {
	Kind    string `json:"kind"` // always "result"
	CorrID  string `json:"corrId"`
	Payload any    `json:"payload"`
}

// ---- client -> server frames ----

type SubscribeMsg struct {
	Kind  string `json:"kind"` // "subscribe"
	Topic Topic  `json:"topic"`
}

type UnsubscribeMsg struct {
	Kind  string `json:"kind"` // "unsubscribe"
	Topic Topic  `json:"topic"`
}

type CommandMsg struct {
	Kind   string          `json:"kind"` // "command"
	CorrID string          `json:"corrId"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
}

type QueryMsg struct {
	Kind   string          `json:"kind"` // "query"
	CorrID string          `json:"corrId"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
}

type PingMsg struct {
	Kind string `json:"kind"` // "ping"
	T    int64  `json:"t"`
}

// ---- command / query argument DTOs ----

type SubmitOrderArgs struct {
	Venue      string    `json:"venue"`
	Symbol     string    `json:"symbol"`
	Side       Side      `json:"side"`
	Type       OrderType `json:"type"`
	TIF        TIF       `json:"tif"`
	Qty        float64   `json:"qty"`
	LimitPrice float64   `json:"limitPrice"`
	StopPrice  float64   `json:"stopPrice"`
}

type CancelOrderArgs struct {
	Venue   string `json:"venue"`
	OrderID string `json:"orderId"`
}

type ReplaceOrderArgs struct {
	Venue      string  `json:"venue"`
	OrderID    string  `json:"orderId"`
	Qty        float64 `json:"qty"`
	LimitPrice float64 `json:"limitPrice"`
	StopPrice  float64 `json:"stopPrice"`
}

type FlattenArgs struct {
	Venue string `json:"venue"`
}

type KillSwitchArgs struct {
	Venue string `json:"venue,omitempty"` // omitted/empty => all venues
}

type ArmArgs struct {
	Venue string `json:"venue,omitempty"` // omitted/empty => master
}

type QueryFillsArgs struct {
	Symbol string `json:"symbol"`
	FromMs int64  `json:"fromMs"`
	ToMs   int64  `json:"toMs"`
}

type GetConfigArgs struct {
	Key string `json:"key"`
}

type SetConfigArgs struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}
