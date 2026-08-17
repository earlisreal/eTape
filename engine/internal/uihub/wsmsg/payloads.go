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
	Symbol          string                   `json:"symbol"`
	Price           float64                  `json:"price"`
	Size            int64                    `json:"size"`
	Direction       TickDirection            `json:"direction"`
	TransactionType TickTransactionType      `json:"transactionType"`
	Significance    SignificanceLevel        `json:"significance"`
	Condition       TickTradeReportCondition `json:"condition"`
	RawType         int32                    `json:"rawType"`
	RawTypeSign     int32                    `json:"rawTypeSign"`
	DeliverySource  TickDeliverySource       `json:"deliverySource"`
	RangeEligible   bool                     `json:"rangeEligible"`
	LastEligible    bool                     `json:"lastEligible"`
	VolumeEligible  bool                     `json:"volumeEligible"`
	Ts              string                   `json:"ts"`
}

type SignificanceStatus struct {
	Symbol               string            `json:"symbol"`
	Pool                 SignificancePool  `json:"pool"`
	BaselineCount        int               `json:"baselineCount"`
	LargeAvailable       bool              `json:"largeAvailable"`
	LargeThreshold       int64             `json:"largeThreshold"`
	ExceptionalAvailable bool              `json:"exceptionalAvailable"`
	ExceptionalThreshold int64             `json:"exceptionalThreshold"`
	Provisional          bool              `json:"provisional"`
	Full                 bool              `json:"full"`
	State                SignificanceState `json:"state"`
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
	VolumeOnly  bool    `json:"volumeOnly,omitempty"`
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

// ClosedOrder is a read-only historical order-leg projection. ID is the
// projection row key; it may differ from the live domain order ID for a
// replaced leg.
type ClosedOrder struct {
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
	DayBasis      float64 `json:"dayBasis"`
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
	CycleStartMs  int64   `json:"cycleStartMs"`
	CycleRealized float64 `json:"cycleRealized"`
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

// LocateEligibility is sourced from Alpaca's startup active-assets cache.
// A nil field means Alpaca did not provide that piece of metadata.
type LocateEligibility struct {
	Supported    bool    `json:"supported"`
	Found        bool    `json:"found"`
	BorrowStatus *string `json:"borrowStatus" tstype:"string | null,required"`
	Shortable    *bool   `json:"shortable" tstype:"boolean | null,required"`
	Marginable   *bool   `json:"marginable" tstype:"boolean | null,required"`
	Tradable     *bool   `json:"tradable" tstype:"boolean | null,required"`
	Error        string  `json:"error"`
}

type LocateQuote struct {
	Symbol       string `json:"symbol"`
	AvailableQty int64  `json:"availableQty"`
	Price        string `json:"price"`
	QuotedAt     string `json:"quotedAt"`
}

type LocateQuoteError struct {
	Symbol  string `json:"symbol"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type LocateQuoteResult struct {
	Quotes []LocateQuote      `json:"quotes"`
	Errors []LocateQuoteError `json:"errors"`
	Error  string             `json:"error"`
}

type LocateRecord struct {
	ID           string `json:"id"`
	Symbol       string `json:"symbol"`
	RequestedQty int64  `json:"requestedQty"`
	LimitPrice   string `json:"limitPrice"`
	AllOrNone    bool   `json:"allOrNone"`
	Status       string `json:"status"`
	CreatedAt    string `json:"createdAt"`
	LocatedQty   int64  `json:"locatedQty"`
	LocatedPrice string `json:"locatedPrice"`
	TotalFee     string `json:"totalFee"`
	ExpiresAt    string `json:"expiresAt"`
}

type LocateListResult struct {
	Locates       []LocateRecord `json:"locates"`
	NextPageToken string         `json:"nextPageToken"`
	Error         string         `json:"error"`
}

// ---- scanner / news / health payloads ----

type ScannerRow struct {
	Symbol              string   `json:"symbol"`
	ShortSellRestricted bool     `json:"shortSellRestricted"`
	ChangePct           *float64 `json:"changePct" tstype:"number | null,required"`   // null = no print yet
	Last                *float64 `json:"last" tstype:"number | null,required"`        // null = no print yet
	FloatShares         *float64 `json:"floatShares" tstype:"number | null,required"` // ACTUAL shares (engine converts moomoo thousands); null = unknown
	Volume              int64    `json:"volume"`                                      // 0 is legitimate
}

type ScannerRankPayload struct {
	RefreshedAt string         `json:"refreshedAt"`
	Rows        []ScannerRow   `json:"rows"`
	Filters     ScannerFilters `json:"filters,omitempty"`
	Baseline    bool           `json:"baseline,omitempty"`
}

type ScannerFilters struct {
	Mode           string   `json:"mode" tstype:"\"gainers\" | \"losers\" | \"most_active\""`
	MinChangePct   float64  `json:"minChangePct"`
	MaxFloatShares *float64 `json:"maxFloatShares" tstype:"number | null,required"`
	MinVolume      float64  `json:"minVolume"`
	FloatUnit      string   `json:"floatUnit" tstype:"\"K\" | \"M\""`
	VolumeUnit     string   `json:"volumeUnit" tstype:"\"K\" | \"M\""`
}

type ScanHitPayload struct {
	Symbol string `json:"symbol"`
	At     string `json:"at"`
}

// WatchlistRow is one row of the user-pinned watchlist. Last/ChangePct are
// nil until the first successful snapshot for that symbol (ScannerRow
// convention). Not reusing ScannerRow: it carries floatShares (scanner-only),
// and coupling the types drags either's evolution onto the other.
type WatchlistRow struct {
	Symbol    string   `json:"symbol"`
	Last      *float64 `json:"last" tstype:"number | null,required"`
	ChangePct *float64 `json:"changePct" tstype:"number | null,required"`
	Volume    int64    `json:"volume"`
}

// WatchlistRowsPayload is the full-snapshot push on topic watchlist.rows.
// Symbols is the authoritative membership + order (always current); Rows may
// lag Symbols by up to one poll (mutation push / failed poll) and is keyed by
// Symbol — the panel renders dashes for a Symbol absent from Rows.
type WatchlistRowsPayload struct {
	RefreshedAt *string        `json:"refreshedAt" tstype:"string | null,required"`
	Symbols     []string       `json:"symbols"`
	Rows        []WatchlistRow `json:"rows"`
}

type WatchlistAddArgs struct {
	Symbol string `json:"symbol"`
}

type WatchlistRemoveArgs struct {
	Symbol string `json:"symbol"`
}

// StockDetailPayload is the snapshot for the stock.detail topic: fundamentals
// for the Stock Info panel. Nullable numerics follow the ScannerRow
// convention (`*float64` + tstype) — null means moomoo hasn't returned a
// value for that field yet (e.g. no snapshot fetched, or the field is
// genuinely absent for this instrument type). Alpaca asset fields are
// informational only: false means Alpaca explicitly reported false, while
// null means no Alpaca source, an unavailable field, or a failed request.
type StockDetailPayload struct {
	Symbol              string   `json:"symbol"`
	Name                string   `json:"name"`
	Industry            string   `json:"industry"`
	Country             string   `json:"country"`  // best-effort profile country; empty = unavailable
	Sector              string   `json:"sector"`   // best-effort profile sector; empty = unavailable
	Exchange            string   `json:"exchange"` // NASDAQ/NYSE/AMEX/OTC via moomoo ExchType; "" = unresolved/unknown
	Price               *float64 `json:"price" tstype:"number | null,required"`
	LastClose           *float64 `json:"lastClose" tstype:"number | null,required"`
	ChangePct           *float64 `json:"changePct" tstype:"number | null,required"`
	MarketCap           *float64 `json:"marketCap" tstype:"number | null,required"`         // moomoo IssuedMarketVal
	FloatMarketCap      *float64 `json:"floatMarketCap" tstype:"number | null,required"`    // moomoo OutstandingMarketVal
	SharesOutstanding   *float64 `json:"sharesOutstanding" tstype:"number | null,required"` // moomoo IssuedShares, raw share count
	FloatShares         *float64 `json:"floatShares" tstype:"number | null,required"`       // moomoo OutstandingShares, raw share count
	Pe                  *float64 `json:"pe" tstype:"number | null,required"`                // moomoo PeRate
	PeTTM               *float64 `json:"peTTM" tstype:"number | null,required"`             // moomoo PeTTMRate
	Eps                 *float64 `json:"eps" tstype:"number | null,required"`               // moomoo EarningsPershare
	High52              *float64 `json:"high52" tstype:"number | null,required"`
	Low52               *float64 `json:"low52" tstype:"number | null,required"`
	Ema200              *float64 `json:"ema200" tstype:"number | null,required"`       // 200-day EMA of daily closes; nil until 200 daily bars are backfilled
	Volume              int64    `json:"volume"`                                       // 0 is legitimate
	BorrowStatus        *string  `json:"borrowStatus" tstype:"string | null,required"` // informational; HTB still needs a future locate workflow
	Shortable           *bool    `json:"shortable" tstype:"boolean | null,required"`   // asset-level flag, not short-order authorization
	Marginable          *bool    `json:"marginable" tstype:"boolean | null,required"`  // asset-level flag, not account margin authorization
	Tradable            *bool    `json:"tradable" tstype:"boolean | null,required"`    // asset-level flag, not execution authorization
	ShortSellRestricted bool     `json:"shortSellRestricted"`
	RefreshedAt         string   `json:"refreshedAt"`
}

type NewsItem struct {
	ID                 string   `json:"id"`
	Symbols            []string `json:"symbols"`
	Headline           string   `json:"headline"`
	Source             string   `json:"source"`
	URL                string   `json:"url"`
	SeenAt             string   `json:"seen_at"`
	PublishedAt        string   `json:"published_at"`
	PublishedPrecision string   `json:"published_precision"`
	ViewCount          int64    `json:"view_count"`
	Type               string   `json:"type"` // "news" | "notice" | "rating"
	CatalystCategory   string   `json:"catalyst_category"`
	CatalystScore      int      `json:"catalyst_score"`
	CatalystReasons    []string `json:"catalyst_reasons"`
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

// QuotaInfo is the account-wide moomoo quota snapshot embedded in
// HealthSnapshot when the quota poller has a reading. State is one of
// "ok"|"foreign"|"low"|"exhausted"; HistState is "ok"|"low" (see
// internal/quota). subForeign is subscription slots used by *other* OpenD
// clients on the same account (contention).
type QuotaInfo struct {
	SubUsed    int    `json:"subUsed"`
	SubRemain  int    `json:"subRemain"`
	SubOwn     int    `json:"subOwn"`
	SubForeign int    `json:"subForeign"`
	HistUsed   int    `json:"histUsed"`
	HistRemain int    `json:"histRemain"`
	State      string `json:"state"`
	HistState  string `json:"histState"`
}

type HealthSnapshot struct {
	Links []HealthLink `json:"links"`
	Quota *QuotaInfo   `json:"quota,omitempty"`
}

// SessionSnapshot is the static sys.session topic: which mode the engine
// booted in. Mode is "live" or "demo".
type SessionSnapshot struct {
	Mode string `json:"mode"`
}

// BootStatus is the sys.boot snapshot: the engine's current boot phase, so the
// UI shows a neutral connecting banner during boot instead of the red
// feed-disconnected strip. Snapshot-bearing (like SessionSnapshot):
// re-delivered to every new subscriber, also pushed as a delta on each
// transition. Phase is one of "connecting" | "ready".
type BootStatus struct {
	Phase string `json:"phase"`
}

type SysEvent struct {
	Seq    int64  `json:"seq"`
	Ts     string `json:"ts"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	// Level is "info"|"warn"|"danger"; empty/absent = info. Warn/danger drive
	// UI toasts (see ui/src/data/quotaToasts.ts). Existing events omit it.
	Level string `json:"level,omitempty"`
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
	Venue string `json:"venue,omitempty"` // omitted/empty => all venues; any scope disarms the global master
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

type QueryLocateEligibilityArgs struct {
	Venue  string `json:"venue"`
	Symbol string `json:"symbol"`
}

type QueryLocateQuotesArgs struct {
	Venue   string   `json:"venue"`
	Symbols []string `json:"symbols"`
}

type QueryLocatesArgs struct {
	Venue     string `json:"venue"`
	Status    string `json:"status"`
	Symbol    string `json:"symbol"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Limit     int    `json:"limit"`
	PageToken string `json:"pageToken"`
}

type QueryLocateArgs struct {
	Venue    string `json:"venue"`
	LocateID string `json:"locateId"`
}

type RequestLocateArgs struct {
	Venue          string `json:"venue"`
	Symbol         string `json:"symbol"`
	Qty            int64  `json:"qty"`
	LimitPrice     string `json:"limitPrice"`
	AllOrNone      bool   `json:"allOrNone"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type QueryCycleFillsArgs struct {
	Venue string `json:"venue"`
}
type CarriedPosition struct {
	Symbol string  `json:"symbol"`
	Qty    float64 `json:"qty"`
}
type QueryCycleFillsResult struct {
	CycleStartMs int64             `json:"cycleStartMs"`
	Carried      []CarriedPosition `json:"carried"`
	Fills        []Fill            `json:"fills"`
}

// ExportFillsArgs selects one venue's fills for the trade-export CSV.
// Preset is one of "today"|"week"|"month"|"all"|"custom"; From/To are
// "YYYY-MM-DD" ET calendar dates, used only when Preset is "custom".
type ExportFillsArgs struct {
	Venue  string `json:"venue"`
	Preset string `json:"preset"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
}

// ExportFillsResult carries the generated CSV (engine is the content source
// of truth) plus a row count for a UI empty-state/toast check.
type ExportFillsResult struct {
	CSV   string `json:"csv"`
	Count int    `json:"count"`
}

type GetConfigArgs struct {
	Key string `json:"key"`
}

type SetConfigArgs struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

type DeleteConfigArgs struct {
	Key string `json:"key"`
}

type SetScannerFiltersArgs struct {
	Filters ScannerFilters `json:"filters"`
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

// QueryChartWindowArgs selects either an exact half-open UTC range or latest
// TailBars bars. Exactly one mode must be supplied.
type QueryChartWindowArgs struct {
	Symbol              string   `json:"symbol"`
	Timeframe           string   `json:"timeframe"`
	FromMs              int64    `json:"fromMs"`
	ToMs                int64    `json:"toMs"`
	TailBars            int      `json:"tailBars"`
	IndicatorSeriesKeys []string `json:"indicatorSeriesKeys"`
	SkipBars            bool     `json:"skipBars,omitempty"`
}

type IndicatorSeriesWindow struct {
	SeriesKey string           `json:"seriesKey"`
	Points    []IndicatorPoint `json:"points"`
}

type QueryChartWindowResult struct {
	Symbol          string                  `json:"symbol"`
	Timeframe       string                  `json:"timeframe"`
	FromMs          int64                   `json:"fromMs"`
	ToMs            int64                   `json:"toMs"`
	Bars            []Bar                   `json:"bars"`
	Indicators      []IndicatorSeriesWindow `json:"indicators"`
	HistoryRevision int64                   `json:"historyRevision"`
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
	SlippageBps     float64 `json:"slippageBps"`     // sim only; <=0 => off
	FillLatencyMs   int     `json:"fillLatencyMs"`   // sim only; <=0 => off
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

// SeedView mirrors config.SeedConfig for the settings UI: whether the
// one-shot moomoo auto-config has produced a definitive outcome.
type SeedView struct {
	MoomooAttempted bool `json:"moomooAttempted"`
}

// VenueSetup is the GetVenueSetup result. file = parsed from config.toml,
// running = what the engine booted with; the restart banner shows when they
// differ. credKeys = credential NAMES only. seed = the file's [seed] marker.
type VenueSetup struct {
	File     VenueConfig `json:"file"`
	Running  VenueConfig `json:"running"`
	CredKeys []string    `json:"credKeys"`
	Seed     SeedView    `json:"seed"`
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

// ---- test-connection probe (settings "Venues & credentials" Test button) ----

// TestConnectionArgs carries the (possibly not-yet-saved) credential under
// test. KeyID/SecretKey are the typed-but-unsaved values from the UI form
// when non-empty; when both are empty the engine falls back to the saved
// credential named by Credentials.
type TestConnectionArgs struct {
	Broker      string `json:"broker"`
	Env         string `json:"env"`
	Credentials string `json:"credentials"`
	KeyID       string `json:"keyId"`
	SecretKey   string `json:"secretKey"`
	AccountID   string `json:"accountId"`
}

// TestAccount is one candidate account a probe discovered (TradeZero can
// return more than one; the UI offers a picker when len(Accounts) > 1).
type TestAccount struct {
	AccountID   string `json:"accountId"`
	AccountType string `json:"accountType"`
	Env         string `json:"env"`
}

// TestConnectionResult is the TestConnection command's AckMsg.Value payload.
// OK is the auth outcome (distinct from AckMsg.Status, which is the
// transport-level accepted/blocked outcome — a malformed-args request is
// "blocked" at the transport level; a bad API key is a transport-level
// "accepted" ack carrying OK:false here).
type TestConnectionResult struct {
	OK          bool          `json:"ok"`
	Env         string        `json:"env"`
	AccountID   string        `json:"accountId"`
	AccountType string        `json:"accountType"`
	Message     string        `json:"message"`
	Accounts    []TestAccount `json:"accounts"`
}

// ---- demo control ----

// StartDemoArgs is intentionally empty (kept as a named type for tygo
// stability). A UI-triggered demo relaunch takes no knobs — just -demo.
type StartDemoArgs struct{}
