package wsmsg

// ScreenerFilters always evaluates the latest completed regular session.
// Calendar lookbacks are distinct from a custom number of trading sessions.
type ScreenerFilters struct {
	Lookback        string   `json:"lookback"` // all-time, 6-month, 1-year, trading-days
	TradingDays     int      `json:"tradingDays"`
	Mode            string   `json:"mode"` // near, new, either
	NearPct         float64  `json:"nearPct"`
	RequireClose    bool     `json:"requireClose"`
	MinPrice        float64  `json:"minPrice"`
	MaxPrice        float64  `json:"maxPrice"`
	MinDollarVolume float64  `json:"minDollarVolume"`
	MinMarketCap    float64  `json:"minMarketCap"`
	MaxMarketCap    float64  `json:"maxMarketCap"`
	AboveSMA20      bool     `json:"aboveSma20"`
	AboveSMA50      bool     `json:"aboveSma50"`
	AboveSMA200     bool     `json:"aboveSma200"`
	ExtensionSMA    int      `json:"extensionSma"`
	MaxExtensionPct float64  `json:"maxExtensionPct"`
	MinChangePct    *float64 `json:"minChangePct,omitempty"`
	MaxChangePct    *float64 `json:"maxChangePct,omitempty"`
	MinATRPct       float64  `json:"minAtrPct"`
	MaxATRPct       float64  `json:"maxAtrPct"`
}

type RunScreenerArgs struct {
	PanelID string          `json:"panelId"`
	Filters ScreenerFilters `json:"filters"`
}

type CancelScreenerArgs struct {
	PanelID string `json:"panelId"`
}

type ScreenerRow struct {
	Symbol         string   `json:"symbol"`
	Name           string   `json:"name"`
	Close          float64  `json:"close"`
	High           float64  `json:"high"`
	PreviousHigh   float64  `json:"previousHigh"`
	DistancePct    float64  `json:"distancePct"`
	NewHigh        bool     `json:"newHigh"`
	CloseConfirmed bool     `json:"closeConfirmed"`
	DollarVolume   *float64 `json:"dollarVolume,omitempty"`
	MarketCap      *float64 `json:"marketCap,omitempty"`
	ChangePct      *float64 `json:"changePct,omitempty"`
	ATRPct         *float64 `json:"atrPct,omitempty"`
	HistoryStart   string   `json:"historyStart"`
}

type ScreenerState struct {
	PanelID     string          `json:"panelId"`
	RunID       uint64          `json:"runId"`
	Status      string          `json:"status"` // queued, discovering, running, complete, canceled, error, removed
	AsOf        string          `json:"asOf"`
	StartedAt   string          `json:"startedAt"`
	FinishedAt  string          `json:"finishedAt"`
	Filters     ScreenerFilters `json:"filters"`
	Universe    int             `json:"universe"`
	Candidates  int             `json:"candidates"`
	Checked     int             `json:"checked"`
	Unavailable int             `json:"unavailable"`
	Warning     string          `json:"warning"`
	Error       string          `json:"error"`
	Rows        []ScreenerRow   `json:"rows"`
}
