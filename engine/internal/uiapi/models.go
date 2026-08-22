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

// WorkspaceStatus is a business outcome. Invalid input, stale revisions,
// reserved identities, and an already-open Workspace are returned as blocked
// values; only unavailable storage or bridge failures reject the binding.
type WorkspaceStatus string

const (
	WorkspaceAccepted WorkspaceStatus = "accepted"
	WorkspaceBlocked  WorkspaceStatus = "blocked"
)

type WorkspaceIDArgs struct {
	WorkspaceID string `json:"workspaceId"`
}

type WorkspaceCloseArgs struct {
	WorkspaceID string `json:"workspaceId"`
	RequestID   string `json:"requestId"`
}

type CreateWorkspaceArgs struct {
	WorkspaceID             string             `json:"workspaceId"`
	Name                    string             `json:"name"`
	Document                *WorkspaceDocument `json:"document,omitempty"`
	ExpectedCatalogRevision int64              `json:"expectedCatalogRevision,omitempty"`
}

type RenameWorkspaceArgs struct {
	WorkspaceID             string `json:"workspaceId"`
	Name                    string `json:"name"`
	ExpectedCatalogRevision int64  `json:"expectedCatalogRevision,omitempty"`
}

type DeleteWorkspaceArgs struct {
	WorkspaceID             string `json:"workspaceId"`
	ExpectedCatalogRevision int64  `json:"expectedCatalogRevision,omitempty"`
}

type SaveWorkspaceArgs struct {
	WorkspaceID      string            `json:"workspaceId"`
	Document         WorkspaceDocument `json:"document"`
	ExpectedRevision int64             `json:"expectedRevision,omitempty"`
}

type WorkspacePanel struct {
	ID       string         `json:"id"`
	PanelID  string         `json:"panelId"`
	Group    *string        `json:"group"`
	Settings map[string]any `json:"settings"`
}

type WorkspaceScannerSync struct {
	Enabled           bool   `json:"enabled"`
	SourceWorkspaceID string `json:"sourceWorkspaceId,omitempty"`
	SourcePanelID     string `json:"sourcePanelId,omitempty"`
}

// WorkspaceDocument keeps Dockview's layout opaque. Go validates its bounded
// JSON envelope and identity but never interprets Panel Group layout data.
type WorkspaceDocument struct {
	Name          string                `json:"name"`
	LayoutVersion int                   `json:"layoutVersion"`
	Panels        []WorkspacePanel      `json:"panels"`
	Layout        any                   `json:"layout"`
	Groups        map[string]string     `json:"groups,omitempty"`
	LinkVenues    map[string]string     `json:"linkVenues,omitempty"`
	ScannerSync   *WorkspaceScannerSync `json:"scannerSync,omitempty"`
}

type WorkspaceCatalogEntry struct {
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Open        bool   `json:"open"`
}

type WorkspaceCatalogResult struct {
	Status           WorkspaceStatus         `json:"status"`
	Reason           string                  `json:"reason,omitempty"`
	Revision         int64                   `json:"revision"`
	Entries          []WorkspaceCatalogEntry `json:"entries"`
	OpenWorkspaceIDs []string                `json:"openWorkspaceIds"`
}

type WorkspaceDocumentResult struct {
	Status      WorkspaceStatus    `json:"status"`
	Reason      string             `json:"reason,omitempty"`
	WorkspaceID string             `json:"workspaceId"`
	Revision    int64              `json:"revision"`
	Document    *WorkspaceDocument `json:"document,omitempty"`
}

type WorkspaceMutationResult struct {
	Status           WorkspaceStatus         `json:"status"`
	Reason           string                  `json:"reason,omitempty"`
	WorkspaceID      string                  `json:"workspaceId,omitempty"`
	Revision         int64                   `json:"revision"` // document revision; window actions return the current open-set revision
	CatalogRevision  int64                   `json:"catalogRevision"`
	Entries          []WorkspaceCatalogEntry `json:"entries,omitempty"`
	OpenWorkspaceIDs []string                `json:"openWorkspaceIds,omitempty"`
}

type WorkspaceFlushResult struct {
	Status WorkspaceStatus `json:"status"`
	Reason string          `json:"reason,omitempty"`
}

type MutationStatus string

const (
	MutationAccepted MutationStatus = "accepted"
	MutationBlocked  MutationStatus = "blocked"
)

type MutationResult struct {
	Status   MutationStatus `json:"status"`
	Reason   string         `json:"reason"`
	Revision uint64         `json:"revision"`
}

type ScannerFilters struct {
	Mode           string   `json:"mode"`
	MinChangePct   float64  `json:"minChangePct"`
	MaxFloatShares *float64 `json:"maxFloatShares"`
	MinVolume      float64  `json:"minVolume"`
	MinVolumeRatio float64  `json:"minVolumeRatio"`
	FloatUnit      string   `json:"floatUnit"`
	VolumeUnit     string   `json:"volumeUnit"`
}

type ScannerFiltersView struct {
	Filters  ScannerFilters `json:"filters"`
	Revision uint64         `json:"revision"`
}

type SetScannerFiltersArgs struct {
	Filters ScannerFilters `json:"filters"`
}

type ScannerFiltersMutationResult struct {
	Status   MutationStatus `json:"status"`
	Reason   string         `json:"reason"`
	Filters  ScannerFilters `json:"filters"`
	Revision uint64         `json:"revision"`
}

type WatchlistMutationArgs struct {
	Symbol string `json:"symbol"`
}

type WatchlistMutationResult struct {
	Status   MutationStatus `json:"status"`
	Reason   string         `json:"reason"`
	Symbols  []string       `json:"symbols"`
	Revision uint64         `json:"revision"`
}

type GlobalLimitsView struct {
	MaxDayLoss              float64 `json:"maxDayLoss"`
	MaxSymbolPositionValue  float64 `json:"maxSymbolPositionValue"`
	MaxSymbolPositionShares float64 `json:"maxSymbolPositionShares"`
}

type GateLimitsView struct {
	MaxOrderValue     float64 `json:"maxOrderValue"`
	MaxPositionValue  float64 `json:"maxPositionValue"`
	MaxPositionShares float64 `json:"maxPositionShares"`
	MaxOpenOrders     int     `json:"maxOpenOrders"`
}

type Venue struct {
	ID              string  `json:"id"`
	Broker          string  `json:"broker"`
	Env             string  `json:"env"`
	Credentials     string  `json:"credentials"`
	AccountID       string  `json:"accountId"`
	StartingBalance float64 `json:"startingBalance"`
	SlippageBps     float64 `json:"slippageBps"`
	FillLatencyMs   int     `json:"fillLatencyMs"`
}

type Gate struct {
	Global GlobalLimitsView          `json:"global"`
	Venue  map[string]GateLimitsView `json:"venue"`
}

type VenueConfig struct {
	Venues []Venue `json:"venues"`
	Gate   Gate    `json:"gate"`
}

type SeedView struct {
	MoomooAttempted bool `json:"moomooAttempted"`
}

type VenueSetup struct {
	File     VenueConfig `json:"file"`
	Running  VenueConfig `json:"running"`
	CredKeys []string    `json:"credKeys"`
	Seed     SeedView    `json:"seed"`
	Revision uint64      `json:"revision"`
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

type TestConnectionArgs struct {
	Broker      string `json:"broker"`
	Env         string `json:"env"`
	Credentials string `json:"credentials"`
	KeyID       string `json:"keyId"`
	SecretKey   string `json:"secretKey"`
	AccountID   string `json:"accountId"`
}

type TestAccount struct {
	AccountID   string `json:"accountId"`
	AccountType string `json:"accountType"`
	Env         string `json:"env"`
}

type TestConnectionResult struct {
	Status      MutationStatus `json:"status"`
	Reason      string         `json:"reason"`
	OK          bool           `json:"ok"`
	Env         string         `json:"env"`
	AccountID   string         `json:"accountId"`
	AccountType string         `json:"accountType"`
	Message     string         `json:"message"`
	Accounts    []TestAccount  `json:"accounts"`
}
