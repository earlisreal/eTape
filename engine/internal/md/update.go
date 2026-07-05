package md

import (
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// Update is the sealed union of everything the md core emits on Updates().
type Update interface{ isUpdate() }

// QuoteUpdate carries the symbol's latest quote (replace semantics).
type QuoteUpdate struct{ Quote feed.Quote }

// BookUpdate carries the symbol's latest book (full replace — cheaper than
// diffing at ≤10 rows).
type BookUpdate struct{ Book feed.Book }

// TapeUpdate carries the deduped prints accepted from one feed batch.
type TapeUpdate struct {
	Symbol string
	Ticks  []feed.Tick
}

// BarUpdate carries one bar (in-progress or closed) for any timeframe.
type BarUpdate struct{ Bar Bar }

// IndicatorUpdate carries either a full snapshot or a single incremental
// point for one indicator instance/slot.
type IndicatorUpdate struct {
	InstanceID string  // the requested instance
	SeriesKey  string  // instanceId (single-slot) or "instanceId#slot" (matches the UI contract)
	Points     []Point // Snapshot: full series; else exactly one point
	Snapshot   bool
}

// MismatchUpdate flags a K_1M vs tick-derived 1m disagreement for a bucket.
type MismatchUpdate struct {
	Symbol   string
	BucketMs int64
	Detail   string
}

// ConnUpdate passes through feed-connection transitions.
type ConnUpdate struct{ Up bool }

// ResyncedUpdate passes through a completed reconnect + resubscribe cycle.
type ResyncedUpdate struct{}

func (QuoteUpdate) isUpdate()     {}
func (BookUpdate) isUpdate()      {}
func (TapeUpdate) isUpdate()      {}
func (BarUpdate) isUpdate()       {}
func (IndicatorUpdate) isUpdate() {}
func (MismatchUpdate) isUpdate()  {}
func (ConnUpdate) isUpdate()      {}
func (ResyncedUpdate) isUpdate()  {}

// Point is one (time, value) sample of an indicator series.
type Point struct {
	TimeMs int64
	Value  float64
}

// Mark is a last-trade signal consumed by execution (Plan 4).
type Mark struct {
	Symbol string
	Price  float64
	TsMs   int64
}

// Bar is the md-side bar: raw OHLCV plus tick-derived delta fields and
// display state. BuyV/SellV/Ticks are zero when no tick data covers the bar
// (e.g. deep-history backfill) — the DELTA indicator reads 0 there, honestly.
type Bar struct {
	Symbol     string
	TF         session.Timeframe
	BucketMs   int64
	O, H, L, C float64
	V          int64
	BuyV       int64
	SellV      int64
	Ticks      int32
	InProgress bool
	Gap        bool // first bar after a feed gap (resync) — UI renders the flag
}
