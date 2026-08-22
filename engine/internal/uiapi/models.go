package uiapi

// The types in this package are the Wails query contract. Keep them explicit
// rather than aliasing the Stream DTOs: the generated bindings and the Go
// service methods must have one obvious source of truth.

type QueryChartWindowArgs struct {
	Symbol              string   `json:"symbol"`
	Timeframe           string   `json:"timeframe"`
	FromMs              int64    `json:"fromMs"`
	ToMs                int64    `json:"toMs"`
	TailBars            int      `json:"tailBars"`
	IndicatorSeriesKeys []string `json:"indicatorSeriesKeys"`
	SkipBars            bool     `json:"skipBars,omitempty"`
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

type Side string

const (
	SideBuy   Side = "BUY"
	SideSell  Side = "SELL"
	SideShort Side = "SHORT"
	SideCover Side = "COVER"
)

type Fill struct {
	Venue   string  `json:"venue"`
	OrderID string  `json:"orderId"`
	Symbol  string  `json:"symbol"`
	Side    Side    `json:"side"`
	Qty     float64 `json:"qty"`
	Price   float64 `json:"price"`
	TsMs    int64   `json:"tsMs"`
}

type QueryFillsArgs struct {
	Symbol string `json:"symbol"`
	FromMs int64  `json:"fromMs"`
	ToMs   int64  `json:"toMs"`
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

type ExportFillsArgs struct {
	Venue  string `json:"venue"`
	Preset string `json:"preset"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
}

type ExportFillsResult struct {
	CSV   string `json:"csv"`
	Count int    `json:"count"`
	Error string `json:"error,omitempty"`
}

type QueryLocateEligibilityArgs struct {
	Venue  string `json:"venue"`
	Symbol string `json:"symbol"`
}

type LocateEligibility struct {
	Supported    bool    `json:"supported"`
	Found        bool    `json:"found"`
	BorrowStatus *string `json:"borrowStatus"`
	Shortable    *bool   `json:"shortable"`
	Marginable   *bool   `json:"marginable"`
	Tradable     *bool   `json:"tradable"`
	Error        string  `json:"error"`
}

type QueryLocateQuotesArgs struct {
	Venue   string   `json:"venue"`
	Symbols []string `json:"symbols"`
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

type QueryLocatesArgs struct {
	Venue     string `json:"venue"`
	Status    string `json:"status"`
	Symbol    string `json:"symbol"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Limit     int    `json:"limit"`
	PageToken string `json:"pageToken"`
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
	Error        string `json:"error,omitempty"`
}

type LocateListResult struct {
	Locates       []LocateRecord `json:"locates"`
	NextPageToken string         `json:"nextPageToken"`
	Error         string         `json:"error"`
}

type QueryLocateArgs struct {
	Venue    string `json:"venue"`
	LocateID string `json:"locateId"`
}
