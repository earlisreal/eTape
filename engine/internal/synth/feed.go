// This file wraps Generator (Task 6) to satisfy the two interfaces the rest
// of the engine actually depends on: feed.Feed (the market-data source the
// md core consumes) and uihub.Feed (the on-demand symbol-subscription
// control surface). Feed.Run is the synthetic universe's only "live"
// component — it steps the generator forward on a clock tick and re-emits
// whatever Drain coalesces, standing in for opend.OpenDFeed's real network
// pump. Every call the generator itself doesn't need ctx for (it has no I/O
// to cancel) still carries one, purely to satisfy feed.Feed's signature.
package synth

import (
	"context"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/uihub"
)

// feedTickMs is how often Run steps the generator forward and drains its
// accumulated events, per the design spec's ~50ms cadence (fast enough that
// the 300ms/150ms quote/book throttles inside Drain — not this loop — are
// what actually pace the visible update rate).
const feedTickMs = 50 * time.Millisecond

// HistoryStore is the store surface Feed.HistoryBars needs (satisfied by
// *store.Store). The synthetic universe never writes bars to a real journal
// itself — Task 8/9's wiring is responsible for feeding the same store this
// reads from — so this interface exists purely to keep synth decoupled from
// the store package's concrete type.
type HistoryStore interface {
	ReadBars1m(symbol string, fromMs, toMs int64) ([]feed.Bar, error)
	ReadDailyBars(symbol string) ([]feed.Bar, error)
}

// Feed adapts a *Generator to feed.Feed and uihub.Feed. It has no network
// connection to lose, so — unlike opend.OpenDFeed — it emits exactly one
// ConnUpEvent for the process lifetime and never ConnDownEvent or
// ResyncedEvent: there is no reconnect to model.
type Feed struct {
	gen *Generator
	st  HistoryStore
	clk clock.Clock

	out        chan feed.Event
	connUpOnce sync.Once
}

// NewFeed builds a Feed over gen. Run must be started before Events is
// drained.
func NewFeed(gen *Generator, st HistoryStore, clk clock.Clock) *Feed {
	return &Feed{
		gen: gen,
		st:  st,
		clk: clk,
		out: make(chan feed.Event, 4096),
	}
}

// Events is the synthetic stream; it closes when Run returns.
func (f *Feed) Events() <-chan feed.Event { return f.out }

// Run emits one ConnUpEvent, then steps and drains the generator on a
// feedTickMs ticker until ctx is done, closing Events() on return. It never
// emits ResyncedEvent — the synthetic feed has no reconnect to resync from.
func (f *Feed) Run(ctx context.Context) error {
	defer close(f.out)

	f.emitConnUp(ctx)

	tk := f.clk.NewTicker(feedTickMs)
	defer tk.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tk.C():
			now := f.clk.Now().UnixMilli()
			f.gen.StepTo(now)
			for _, ev := range f.gen.Drain(now) {
				select {
				case f.out <- ev:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

// emitConnUp sends the process-lifetime ConnUpEvent exactly once. connUpOnce
// guards it even though Run only ever calls this once itself, matching the
// defensive pattern the brief calls out (a future caller invoking Run twice,
// or concurrently, must not double-emit).
func (f *Feed) emitConnUp(ctx context.Context) {
	f.connUpOnce.Do(func() {
		select {
		case f.out <- feed.ConnUpEvent{}:
		case <-ctx.Done():
		}
	})
}

// Ensure and Release are no-ops: the synthetic universe simulates every
// symbol unconditionally regardless of demand, so there is no subscription
// budget to manage.
func (f *Feed) Ensure(feed.Demand) {}
func (f *Feed) Release(string)     {}

// HistoryBars reads from the backing store rather than the generator: the
// generator only holds live/recent in-memory state (RecentTicks/
// CachedBars1m), while durable history — including anything predating this
// process's lifetime — lives in the store the same as the real feeds.
func (f *Feed) HistoryBars(ctx context.Context, symbol string, res feed.Resolution, from, to time.Time) ([]feed.Bar, error) {
	switch res {
	case feed.ResDay:
		return f.st.ReadDailyBars(symbol)
	default:
		return f.st.ReadBars1m(symbol, from.UnixMilli(), to.UnixMilli())
	}
}

// RecentTicks delegates to the generator's in-memory ~2h tick ring. ctx is
// unused — the generator has no I/O to cancel — but is required to satisfy
// feed.Feed.
func (f *Feed) RecentTicks(ctx context.Context, symbol string, n int) ([]feed.Tick, error) {
	return f.gen.RecentTicks(symbol, n), nil
}

// CachedBars1m delegates to the generator's closed-1m-bar cache. ctx is
// unused for the same reason as RecentTicks.
func (f *Feed) CachedBars1m(ctx context.Context, symbol string, n int) ([]feed.Bar, error) {
	return f.gen.CachedBars1m(symbol, n), nil
}

// BookSnapshot delegates to the generator, translating its bool-ok signature
// into feed.ErrUnknownSymbol.
func (f *Feed) BookSnapshot(ctx context.Context, symbol string) (feed.Book, error) {
	book, ok := f.gen.BookOf(symbol)
	if !ok {
		return feed.Book{}, feed.ErrUnknownSymbol
	}
	return book, nil
}

// QuoteSnapshot delegates to the generator, translating its bool-ok signature
// into feed.ErrUnknownSymbol.
func (f *Feed) QuoteSnapshot(ctx context.Context, symbol string) (feed.Quote, error) {
	quote, ok := f.gen.QuoteOf(symbol)
	if !ok {
		return feed.Quote{}, feed.ErrUnknownSymbol
	}
	return quote, nil
}

// Validate reports whether symbol exists in the synthetic universe. Unlike
// opend.OpenDFeed's Validate, this is a pure in-memory lookup — no quota, no
// network round trip, nothing to time out.
func (f *Feed) Validate(ctx context.Context, symbol string) error {
	if f.gen.Has(symbol) {
		return nil
	}
	return feed.ErrUnknownSymbol
}

var _ feed.Feed = (*Feed)(nil)
var _ uihub.Feed = (*Feed)(nil)
