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
	TopicStockDetail Topic = "stock.detail"

	TopicExecAccount   Topic = "exec.account"
	TopicExecPositions Topic = "exec.positions"
	TopicExecOrders    Topic = "exec.orders"
	TopicExecFills     Topic = "exec.fills"
	TopicExecStatus    Topic = "exec.status"
	TopicExecTrades    Topic = "exec.trades"

	TopicSysHealth  Topic = "sys.health"
	TopicSysSession Topic = "sys.session"
	TopicSysEvents  Topic = "sys.events"
	TopicConfig     Topic = "config"
)

// AllTopics is the set a client may subscribe to (server-side allow-list).
var AllTopics = map[Topic]bool{
	TopicQuote: true, TopicBook: true, TopicTape: true, TopicBars: true, TopicIndicator: true,
	TopicScannerRank: true, TopicScannerHit: true, TopicNews: true, TopicStockDetail: true,
	TopicExecAccount: true, TopicExecPositions: true, TopicExecOrders: true,
	TopicExecFills: true, TopicExecStatus: true, TopicExecTrades: true,
	TopicSysHealth: true, TopicSysSession: true, TopicSysEvents: true, TopicConfig: true,
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

// OrderSession mirrors exec.OrderSession on the wire.
type OrderSession string

const (
	SessionAuto      OrderSession = "AUTO"
	SessionRTH       OrderSession = "RTH"
	SessionExtended  OrderSession = "EXTENDED"
	SessionOvernight OrderSession = "OVERNIGHT"
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

// AckStatus is AckMsg.Status's typed enum (kept narrow so tygo emits a
// literal union instead of `string`).
type AckStatus string

const (
	AckAccepted AckStatus = "accepted"
	AckBlocked  AckStatus = "blocked"
)

// LinkName identifies a monitored engine<->peer link (typed so tygo would
// emit a literal union instead of `string`, if this file weren't excluded
// from tygo generation — see tygo.yaml frontmatter for the hand-declared
// TS equivalent).
type LinkName string

const (
	LinkUIEngine     LinkName = "ui-engine"
	LinkEngineMoomoo LinkName = "engine-moomoo"
	LinkEngineTZ     LinkName = "engine-tz"
	LinkEngineAlpaca LinkName = "engine-alpaca"
)

// LinkStatus is HealthLink.Status's typed enum.
type LinkStatus string

const (
	LinkOK       LinkStatus = "ok"
	LinkDegraded LinkStatus = "degraded"
	LinkDown     LinkStatus = "down"
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
	Kind    string    `json:"kind"` // always "ack"
	CorrID  string    `json:"corrId"`
	Status  AckStatus `json:"status"`
	Reason  string    `json:"reason,omitempty"`
	OrderID string    `json:"orderId,omitempty"`
	Value   any       `json:"value,omitempty"`
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

// Command/query argument DTOs live in payloads.go, not here — see the note
// in tygo.yaml: wsmsg.go is excluded from tygo generation (its envelope
// `kind` discriminants and enum consts are hand-declared as TS literal
// unions in tygo.yaml's frontmatter instead), so any type that needs to be
// tygo-generated must live outside this file.
