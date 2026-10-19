package wsmsg

type PriceAlertSessions struct {
	Pre  bool `json:"pre"`
	RTH  bool `json:"rth"`
	Post bool `json:"post"`
}

type PriceAlert struct {
	ID        string             `json:"id"`
	Symbol    string             `json:"symbol"`
	Condition string             `json:"condition"`
	Price     float64            `json:"price"`
	Label     string             `json:"label"`
	Sessions  PriceAlertSessions `json:"sessions"`
	State     string             `json:"state"`
	Version   uint64             `json:"version"`
	CreatedMs int64              `json:"createdMs"`
	UpdatedMs int64              `json:"updatedMs"`
}

type PriceAlertTrigger struct {
	ID          string             `json:"id"`
	AlertID     string             `json:"alertId"`
	Symbol      string             `json:"symbol"`
	Condition   string             `json:"condition"`
	Price       float64            `json:"price"`
	ActualPrice float64            `json:"actualPrice"`
	Label       string             `json:"label"`
	Sessions    PriceAlertSessions `json:"sessions"`
	Session     string             `json:"session"`
	TsMs        int64              `json:"tsMs"`
	Reason      string             `json:"reason"`
}

type PriceAlertState struct {
	Alerts       []PriceAlert        `json:"alerts"`
	History      []PriceAlertTrigger `json:"history"`
	HistoryTotal int                 `json:"historyTotal"`
	Revision     uint64              `json:"revision"`
	Error        string              `json:"error"`
}

type PriceAlertNotification struct {
	ID        string              `json:"id"`
	Triggers  []PriceAlertTrigger `json:"triggers"`
	ExpiresMs int64               `json:"expiresMs"`
}

type UpsertPriceAlertArgs struct {
	ID        string             `json:"id"`
	Version   uint64             `json:"version"`
	Rearm     bool               `json:"rearm"`
	Symbol    string             `json:"symbol"`
	Condition string             `json:"condition"`
	Price     float64            `json:"price"`
	Label     string             `json:"label"`
	Sessions  PriceAlertSessions `json:"sessions"`
}

type PriceAlertActionArgs struct {
	ID      string `json:"id"`
	Version uint64 `json:"version"`
	Paused  bool   `json:"paused"`
}

type ClaimPriceAlertNotificationArgs struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type PriceAlertHistoryArgs struct {
	BeforeID string `json:"beforeId"`
}
type PriceAlertPreviewArgs struct {
	Symbol string `json:"symbol"`
}
type PriceAlertPreview struct {
	Price float64 `json:"price"`
	TsMs  int64   `json:"tsMs"`
}
