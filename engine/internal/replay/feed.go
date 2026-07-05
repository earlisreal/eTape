package replay

import (
	"context"
	"errors"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// ErrUnsupported is returned by replay's query methods: the md core's seeds
// arrive as recorded seed events on Events(), not through backfill queries.
var ErrUnsupported = errors.New("replay: query not supported (seeds arrive as journal events)")

// FeedOptions configures NewFeed.
type FeedOptions struct {
	Rows     []store.JournalRow // a day's rows, seq-ordered (from store.ReadJournalDay)
	Sim      *Clock             // simulated clock, advanced per event
	Pace     clock.Clock        // real clock for playback throttle; nil = no throttle
	Speed    float64            // >0: real-time × Speed; <=0: as fast as possible
	EventBuf int                // Events() capacity, default 4096
}

// Feed replays a day's journal rows as a feed.Feed. Run must be started
// before Events is drained.
type Feed struct {
	rows  []store.JournalRow
	sim   *Clock
	pace  clock.Clock
	speed float64
	out   chan feed.Event
}

// NewFeed builds a replay Feed. Run must be started before Events is drained.
func NewFeed(opt FeedOptions) *Feed {
	if opt.EventBuf <= 0 {
		opt.EventBuf = 4096
	}
	return &Feed{
		rows:  opt.Rows,
		sim:   opt.Sim,
		pace:  opt.Pace,
		speed: opt.Speed,
		out:   make(chan feed.Event, opt.EventBuf),
	}
}

// Events is the replayed stream; it closes when the journal is exhausted.
func (f *Feed) Events() <-chan feed.Event { return f.out }

// Run emits every row in order, advancing the simulated clock to each event's
// ts_exch and (when Speed>0 and Pace!=nil) throttling to real-time × Speed.
func (f *Feed) Run(ctx context.Context) error {
	defer close(f.out)
	var prev int64
	for i, r := range f.rows {
		if i > 0 && f.speed > 0 && f.pace != nil {
			if gap := r.TsExch - prev; gap > 0 {
				d := time.Duration(float64(gap)/f.speed) * time.Millisecond
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-f.pace.After(d):
				}
			}
		}
		if f.sim != nil {
			f.sim.AdvanceTo(time.UnixMilli(r.TsExch))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f.out <- r.Event:
		}
		prev = r.TsExch
	}
	return nil
}

// Ensure/Release are no-ops: the journal already holds exactly what was
// subscribed, so replay has no demand-driven backfill to perform.
func (f *Feed) Ensure(feed.Demand) {}
func (f *Feed) Release(string)     {}

// HistoryBars, RecentTicks, CachedBars1m, BookSnapshot and QuoteSnapshot are
// unsupported in replay: md-core seeds arrive as recorded seed events on the
// event stream itself, not via backfill queries.
func (f *Feed) HistoryBars(context.Context, string, feed.Resolution, time.Time, time.Time) ([]feed.Bar, error) {
	return nil, ErrUnsupported
}

func (f *Feed) RecentTicks(context.Context, string, int) ([]feed.Tick, error) {
	return nil, ErrUnsupported
}

func (f *Feed) CachedBars1m(context.Context, string, int) ([]feed.Bar, error) {
	return nil, ErrUnsupported
}

func (f *Feed) BookSnapshot(context.Context, string) (feed.Book, error) {
	return feed.Book{}, ErrUnsupported
}

func (f *Feed) QuoteSnapshot(context.Context, string) (feed.Quote, error) {
	return feed.Quote{}, ErrUnsupported
}

var _ feed.Feed = (*Feed)(nil)
