package wsmsg

import "encoding/json"

// ---- market-data payloads (timestamps are ISO-8601 UTC strings) ----

type Quote struct {
	Symbol string  `json:"symbol"`
	Bid    float64 `json:"bid"`
	Ask    float64 `json:"ask"`
	Last   float64 `json:"last"`
	Ts     string  `json:"ts"`
}

type BookLevel struct {
	Price float64 `json:"price"`
	Size  int64   `json:"size"`
}

type Book struct {
	Symbol string      `json:"symbol"`
	Bids   []BookLevel `json:"bids"`
	Asks   []BookLevel `json:"asks"`
	Ts     string      `json:"ts"`
}

type Tick struct {
	Symbol    string        `json:"symbol"`
	Price     float64       `json:"price"`
	Size      int64         `json:"size"`
	Direction TickDirection `json:"direction"`
	Ts        string        `json:"ts"`
}

type Bar struct {
	Symbol      string  `json:"symbol"`
	Timeframe   string  `json:"timeframe"`
	BucketStart string  `json:"bucketStart"`
	O           float64 `json:"o"`
	H           float64 `json:"h"`
	L           float64 `json:"l"`
	C           float64 `json:"c"`
	V           int64   `json:"v"`
	InProgress  bool    `json:"inProgress"`
	Gap         bool    `json:"gap,omitempty"`
}

type IndicatorPoint struct {
	TimeMs int64   `json:"timeMs"`
	Value  float64 `json:"value"`
}

// ---- execution payloads (timestamps are epoch-ms numbers) ----

type Order struct {
	Venue        string      `json:"venue"`
	ID           string      `json:"id"`
	Symbol       string      `json:"symbol"`
	Side         Side        `json:"side"`
	Type         OrderType   `json:"type"`
	TIF          TIF         `json:"tif"`
	Qty          float64     `json:"qty"`
	LimitPrice   float64     `json:"limitPrice"`
	StopPrice    float64     `json:"stopPrice"`
	Status       OrderStatus `json:"status"`
	ExecutedQty  float64     `json:"executedQty"`
	LeavesQty    float64     `json:"leavesQty"`
	AvgFillPrice float64     `json:"avgFillPrice"`
	RejectReason string      `json:"rejectReason"`
	ReplacesID   string      `json:"replacesId"`
	CreatedMs    int64       `json:"createdMs"`
	UpdatedMs    int64       `json:"updatedMs"`
}

type Fill struct {
	Venue   string  `json:"venue"`
	OrderID string  `json:"orderId"`
	Symbol  string  `json:"symbol"`
	Side    Side    `json:"side"`
	Qty     float64 `json:"qty"`
	Price   float64 `json:"price"`
	TsMs    int64   `json:"tsMs"`
}

// PositionRow.Venue is a pointer so a cross-venue net row serializes venue:null.
// tstype forces tygo to emit a literal `| null` union instead of `venue?:`.
type PositionRow struct {
	Venue         *string `json:"venue" tstype:"string | null,required"`
	Symbol        string  `json:"symbol"`
	Qty           float64 `json:"qty"`
	AvgPrice      float64 `json:"avgPrice"`
	UnrealizedPnl float64 `json:"unrealizedPnl"`
}

type AccountRow struct {
	Venue         string  `json:"venue"`
	Equity        float64 `json:"equity"`
	BuyingPower   float64 `json:"buyingPower"`
	AvailableCash float64 `json:"availableCash"`
	SodEquity     float64 `json:"sodEquity"`
	Realized      float64 `json:"realized"`
	DayPnl        float64 `json:"dayPnl"`
	Leverage      float64 `json:"leverage"`
	TsMs          int64   `json:"tsMs"`
}

type GateLimitsView struct {
	MaxOrderValue     float64 `json:"maxOrderValue"`
	MaxPositionValue  float64 `json:"maxPositionValue"`
	MaxPositionShares float64 `json:"maxPositionShares"`
	MaxOpenOrders     int     `json:"maxOpenOrders"`
}

type GlobalLimitsView struct {
	MaxDayLoss              float64 `json:"maxDayLoss"`
	MaxSymbolPositionValue  float64 `json:"maxSymbolPositionValue"`
	MaxSymbolPositionShares float64 `json:"maxSymbolPositionShares"`
}

type VenueStatus struct {
	Venue            string         `json:"venue"`
	Broker           Broker         `json:"broker"`
	Connected        bool           `json:"connected"`
	VenueArmed       bool           `json:"venueArmed"`
	ReconcilePending bool           `json:"reconcilePending"`
	Note             string         `json:"note"`
	LastReconcileMs  *int64         `json:"lastReconcileMs" tstype:"number | null,required"`
	Gate             GateLimitsView `json:"gate"`
}

type ExecStatus struct {
	MasterArmed bool             `json:"masterArmed"`
	Global      GlobalLimitsView `json:"global"`
	Venues      []VenueStatus    `json:"venues"`
}

// ---- scanner / news / health payloads ----

type ScannerRow struct {
	Symbol      string   `json:"symbol"`
	ChangePct   *float64 `json:"changePct" tstype:"number | null,required"`   // null = no print yet
	Last        *float64 `json:"last" tstype:"number | null,required"`        // null = no print yet
	FloatShares *float64 `json:"floatShares" tstype:"number | null,required"` // ACTUAL shares (engine converts moomoo thousands); null = unknown
	Volume      int64    `json:"volume"`                                      // 0 is legitimate
}

type ScannerRankPayload struct {
	RefreshedAt string       `json:"refreshedAt"`
	Rows        []ScannerRow `json:"rows"`
}

type ScanHitPayload struct {
	Symbol string `json:"symbol"`
	At     string `json:"at"`
}

type NewsItem struct {
	Symbol   string `json:"symbol"`
	Headline string `json:"headline"`
	Source   string `json:"source"`
	URL      string `json:"url"`
	SeenAt   string `json:"seen_at"`
}

// LinkName and LinkStatus (HealthLink's typed enums) live in wsmsg.go
// alongside the other wire enum types, not here — see the note there.

type HealthLink struct {
	Link   LinkName   `json:"link"`
	Ms     *float64   `json:"ms" tstype:"number | null,required"`
	Min    *float64   `json:"min" tstype:"number | null,required"`
	Avg    *float64   `json:"avg" tstype:"number | null,required"`
	Max    *float64   `json:"max" tstype:"number | null,required"`
	Status LinkStatus `json:"status"`
}

type HealthSnapshot struct {
	Links []HealthLink `json:"links"`
}

type SysEvent struct {
	Seq    int64  `json:"seq"`
	Ts     string `json:"ts"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// ---- command / query argument DTOs (moved from wsmsg.go so tygo can still
// generate them while wsmsg.go itself is excluded — see tygo.yaml) ----

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
