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
	Venue        string       `json:"venue"`
	ID           string       `json:"id"`
	Symbol       string       `json:"symbol"`
	Side         Side         `json:"side"`
	Type         OrderType    `json:"type"`
	TIF          TIF          `json:"tif"`
	Session      OrderSession `json:"session"`
	Qty          float64      `json:"qty"`
	LimitPrice   float64      `json:"limitPrice"`
	StopPrice    float64      `json:"stopPrice"`
	Status       OrderStatus  `json:"status"`
	ExecutedQty  float64      `json:"executedQty"`
	LeavesQty    float64      `json:"leavesQty"`
	AvgFillPrice float64      `json:"avgFillPrice"`
	RejectReason string       `json:"rejectReason"`
	ReplacesID   string       `json:"replacesId"`
	CreatedMs    int64        `json:"createdMs"`
	UpdatedMs    int64        `json:"updatedMs"`
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

// ClosedTradeRow is one completed round trip: a position that opened from flat
// and returned to flat, with weighted-average entry/exit and net realized P&L.
type ClosedTradeRow struct {
	Venue      string  `json:"venue"`
	Symbol     string  `json:"symbol"`
	IsLong     bool    `json:"isLong"`
	Qty        float64 `json:"qty"`
	EntryPrice float64 `json:"entryPrice"`
	ExitPrice  float64 `json:"exitPrice"`
	Realized   float64 `json:"realized"`
	OpenMs     int64   `json:"openMs"`
	CloseMs    int64   `json:"closeMs"`
	Seq        int64   `json:"seq"`
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

// StockDetailPayload is the snapshot for the stock.detail topic: fundamentals
// for the Stock Info panel. Nullable numerics follow the ScannerRow
// convention (`*float64` + tstype) — null means moomoo hasn't returned a
// value for that field yet (e.g. no snapshot fetched, or the field is
// genuinely absent for this instrument type).
type StockDetailPayload struct {
	Symbol            string   `json:"symbol"`
	Name              string   `json:"name"`
	Industry          string   `json:"industry"`
	Price             *float64 `json:"price" tstype:"number | null,required"`
	LastClose         *float64 `json:"lastClose" tstype:"number | null,required"`
	ChangePct         *float64 `json:"changePct" tstype:"number | null,required"`
	MarketCap         *float64 `json:"marketCap" tstype:"number | null,required"`         // moomoo IssuedMarketVal
	FloatMarketCap    *float64 `json:"floatMarketCap" tstype:"number | null,required"`    // moomoo OutstandingMarketVal
	SharesOutstanding *float64 `json:"sharesOutstanding" tstype:"number | null,required"` // moomoo IssuedShares, raw share count
	FloatShares       *float64 `json:"floatShares" tstype:"number | null,required"`       // moomoo OutstandingShares, raw share count
	Pe                *float64 `json:"pe" tstype:"number | null,required"`                // moomoo PeRate
	PeTTM             *float64 `json:"peTTM" tstype:"number | null,required"`             // moomoo PeTTMRate
	Eps               *float64 `json:"eps" tstype:"number | null,required"`               // moomoo EarningsPershare
	High52            *float64 `json:"high52" tstype:"number | null,required"`
	Low52             *float64 `json:"low52" tstype:"number | null,required"`
	Volume            int64    `json:"volume"` // 0 is legitimate
	RefreshedAt       string   `json:"refreshedAt"`
}

type NewsItem struct {
	Symbol      string `json:"symbol"`
	Headline    string `json:"headline"`
	Source      string `json:"source"`
	URL         string `json:"url"`
	SeenAt      string `json:"seen_at"`
	PublishedAt string `json:"published_at"`
	ViewCount   int64  `json:"view_count"`
	Type        string `json:"type"` // "news" | "notice" | "rating"
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
	Venue      string       `json:"venue"`
	Symbol     string       `json:"symbol"`
	Side       Side         `json:"side"`
	Type       OrderType    `json:"type"`
	TIF        TIF          `json:"tif"`
	Session    OrderSession `json:"session"`
	Qty        float64      `json:"qty"`
	LimitPrice float64      `json:"limitPrice"`
	StopPrice  float64      `json:"stopPrice"`
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

type ResetBalanceArgs struct {
	Venue string `json:"venue"`
}

type KillSwitchArgs struct {
	Venue string `json:"venue,omitempty"` // omitted/empty => all venues
}

// ArmArgs is intentionally empty: Arm/Disarm are master-only commands with no
// arguments. Kept as a named type so the command dispatch has something to
// unmarshal into (and tygo has a stable type to regenerate).
type ArmArgs struct{}

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

// EnsureSymbolArgs subscribes a panel's symbol on demand. profile is one of
// "watch" | "focused" | "interest". demandId is the UI panel instance id.
type EnsureSymbolArgs struct {
	DemandID string `json:"demandId"`
	Symbol   string `json:"symbol"`
	Profile  string `json:"profile"`
}

// ReleaseSymbolArgs drops a panel's on-demand subscription.
type ReleaseSymbolArgs struct {
	DemandID string `json:"demandId"`
}

// FocusGroupArgs carries a link-group focus change for engine-side existence
// validation (the demand itself arrives from the member panels).
type FocusGroupArgs struct {
	Group  string `json:"group"`
	Symbol string `json:"symbol"`
}

// ---- venue & credentials config DTOs (settings "Venues & credentials") ----

// Venue mirrors config.Venue (no secret material — Credentials is a key NAME).
type Venue struct {
	ID              string  `json:"id"`
	Broker          string  `json:"broker"`
	Env             string  `json:"env"`
	Credentials     string  `json:"credentials"`
	AccountID       string  `json:"accountId"`
	StartingBalance float64 `json:"startingBalance"` // sim only; <=0 => engine default
}

// Gate mirrors config.Gate; reuses the existing limit-view shapes.
type Gate struct {
	Global GlobalLimitsView          `json:"global"`
	Venue  map[string]GateLimitsView `json:"venue"`
}

type VenueConfig struct {
	Venues []Venue `json:"venues"`
	Gate   Gate    `json:"gate"`
}

// VenueSetup is the GetVenueSetup result. file = parsed from config.toml,
// running = what the engine booted with; the restart banner shows when they
// differ. credKeys = credential NAMES only.
type VenueSetup struct {
	File     VenueConfig `json:"file"`
	Running  VenueConfig `json:"running"`
	CredKeys []string    `json:"credKeys"`
}

type SetVenueSetupArgs struct {
	Venues []Venue `json:"venues"`
	Gate   Gate    `json:"gate"`
}

type PutCredentialArgs struct {
	Name      string `json:"name"`
	KeyID     string `json:"keyId"`
	SecretKey string `json:"secretKey"`
}

type DeleteCredentialArgs struct {
	Name string `json:"name"`
}
