// Package feed defines the broker-agnostic market-data domain: tick, quote,
// book and bar types, the Event union, subscription demands, and the Feed
// interface implemented by feed/opend (live) and replay (Plan 3, journal).
// It sits at the bottom of the domain graph and imports nothing but stdlib.
package feed

import (
	"context"
	"time"
)

// Direction is the aggressor side of a trade print.
type Direction uint8

const (
	Neutral Direction = iota
	Buy
	Sell
)

func (d Direction) String() string {
	switch d {
	case Buy:
		return "BUY"
	case Sell:
		return "SELL"
	}
	return "NEUTRAL"
}

// Tick is one trade print. TsMs is the exchange timestamp (authoritative for
// bucketing); RecvTsMs is OpenD receive time, used only for latency metrics.
type Tick struct {
	Symbol   string
	Seq      int64
	TsMs     int64
	Price    float64
	Volume   int64
	Turnover float64
	Dir      Direction
	RecvTsMs int64
}

// Quote is the latest basic quote. moomoo's BasicQot carries no bid/ask —
// top-of-book comes from Book; the md core composes the two.
type Quote struct {
	Symbol    string
	TsMs      int64
	Last      float64
	Open      float64
	High      float64
	Low       float64
	PrevClose float64
	Volume    int64
	Turnover  float64
}

// BookLevel is one price level of one side.
type BookLevel struct {
	Price  float64
	Volume int64
	Orders int32
}

// Book is a full replacement snapshot of the visible depth (10 levels on US
// LV3). TsMs is OpenD's server receive time — display only, never bucketing.
type Book struct {
	Symbol string
	TsMs   int64
	Bids   []BookLevel
	Asks   []BookLevel
}

// Bar is a raw OHLCV bar keyed by its bucket START (epoch ms). The adapter
// normalizes moomoo's end-labeled intraday K-lines before they reach here.
type Bar struct {
	Symbol     string
	BucketMs   int64
	O, H, L, C float64
	Volume     int64
	Turnover   float64
}

// SubType is a broker-agnostic subscription kind.
type SubType uint8

const (
	SubQuote SubType = iota
	SubBook
	SubTicker
	SubKL1m
)

// Demand is a consumer's declaration of interest. The subscription manager
// refcounts demands; a symbol's live subscriptions are the union of demands.
type Demand struct {
	ID           string
	Symbol       string
	Subs         []SubType
	Focused      bool // focused symbols survive LRU eviction under quota pressure
	WantsHistory bool // chart-capable demand: worth a deep-history backfill (see uihub.Hub.handleEnsureDemand)
}

// WatchDemand is the watchlist profile: tape/10s/1m recording, no depth
// (2 quota slots).
func WatchDemand(id, symbol string) Demand {
	return Demand{ID: id, Symbol: symbol,
		Subs: []SubType{SubTicker, SubKL1m}, WantsHistory: true}
}

// Resolution selects a history series.
type Resolution uint8

const (
	Res1m Resolution = iota
	ResDay
)

// Feed is the adapter-agnostic market-data source. Events() is the single
// stream the md core consumes and the journal (Plan 3) tees; queries are
// blocking request/response.
type Feed interface {
	Events() <-chan Event
	Ensure(d Demand)
	Release(id string)
	HistoryBars(ctx context.Context, symbol string, res Resolution, from, to time.Time) ([]Bar, error)
	RecentTicks(ctx context.Context, symbol string, n int) ([]Tick, error)
	CachedBars1m(ctx context.Context, symbol string, n int) ([]Bar, error)
	BookSnapshot(ctx context.Context, symbol string) (Book, error)
	QuoteSnapshot(ctx context.Context, symbol string) (Quote, error)
}
