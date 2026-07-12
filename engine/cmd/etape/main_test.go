package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/broker/stub"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
)

type recordingSink struct {
	mu    sync.Mutex
	marks map[string]float64
	books map[string]feed.Book
}

func (r *recordingSink) SetMark(sym string, px float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.marks == nil {
		r.marks = map[string]float64{}
	}
	r.marks[sym] = px
}

func (r *recordingSink) SetBook(sym string, book feed.Book) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.books == nil {
		r.books = map[string]feed.Book{}
	}
	r.books[sym] = book
}

func (r *recordingSink) get(sym string) (float64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.marks[sym]
	return v, ok
}

func (r *recordingSink) getBook(sym string) (feed.Book, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.books[sym]
	return v, ok
}

func TestMarkBridgeForwardsToSinks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	core := md.New(md.Config{TapeRing: 1024, AnchorSecs: 9*3600 + 30*60})
	go func() { _ = core.Run(ctx) }()

	execCore := exec.NewCore(exec.CoreConfig{
		Venues: []exec.VenueID{"sim-paper"}, Clock: clock.System{},
		Brokers: map[exec.VenueID]exec.Broker{}, IDGen: exec.NewOrderIDGen(clock.System{}, nil),
	})
	go func() { _ = execCore.Run(ctx) }()

	sink := &recordingSink{}
	go markBridge(ctx, core, execCore, []simSink{sink})

	core.Feed(feed.TicksEvent{Ticks: []feed.Tick{{
		Symbol: "US.AAPL", TsMs: time.Now().UnixMilli(), Price: 191.23, Volume: 100,
	}}})

	deadline := time.After(2 * time.Second)
	for {
		if v, ok := sink.get("US.AAPL"); ok {
			if v != 191.23 {
				t.Fatalf("mark = %v, want 191.23", v)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("sink never received a mark")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestMarkBridgeForwardsBooksToSinks mirrors TestMarkBridgeForwardsToSinks
// for the Books() side of the bridge: a fed feed.BookEvent must reach every
// sim sink's SetBook with the same book md.Core stored.
func TestMarkBridgeForwardsBooksToSinks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	core := md.New(md.Config{TapeRing: 1024, AnchorSecs: 9*3600 + 30*60})
	go func() { _ = core.Run(ctx) }()

	execCore := exec.NewCore(exec.CoreConfig{
		Venues: []exec.VenueID{"sim-paper"}, Clock: clock.System{},
		Brokers: map[exec.VenueID]exec.Broker{}, IDGen: exec.NewOrderIDGen(clock.System{}, nil),
	})
	go func() { _ = execCore.Run(ctx) }()

	sink := &recordingSink{}
	go markBridge(ctx, core, execCore, []simSink{sink})

	want := feed.Book{
		Symbol: "US.AAPL", TsMs: time.Now().UnixMilli(),
		Bids: []feed.BookLevel{{Price: 191.20, Volume: 100}},
		Asks: []feed.BookLevel{{Price: 191.25, Volume: 200}},
	}
	core.Feed(feed.BookEvent{Book: want})

	deadline := time.After(2 * time.Second)
	for {
		if got, ok := sink.getBook("US.AAPL"); ok {
			if got.Symbol != want.Symbol || len(got.Bids) != 1 || got.Bids[0].Price != 191.20 || len(got.Asks) != 1 || got.Asks[0].Price != 191.25 {
				t.Fatalf("book = %+v, want %+v", got, want)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("sink never received a book")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// fakeDailyBarSource hands out each entry in bars, oldest first, on
// successive DrainDailyBars calls (an empty/absent entry mimics a poll that
// found nothing new).
type fakeDailyBarSource struct {
	mu   sync.Mutex
	bars [][]feed.Bar
}

func (f *fakeDailyBarSource) DrainDailyBars() []feed.Bar {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bars) == 0 {
		return nil
	}
	next := f.bars[0]
	f.bars = f.bars[1:]
	return next
}

// recordingDailyBarSink implements both dailyBarSeeder and dailyBarArchiver
// so a single fake stands in for the *md.Core/*store.Store pair
// forwardDailyBars writes to.
type recordingDailyBarSink struct {
	mu       sync.Mutex
	archived []feed.Bar
	seeded   map[string][]feed.Bar
}

func (r *recordingDailyBarSink) ArchiveDaily(b feed.Bar) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.archived = append(r.archived, b)
}

func (r *recordingDailyBarSink) SeedDaily(symbol string, bars []feed.Bar) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seeded == nil {
		r.seeded = map[string][]feed.Bar{}
	}
	r.seeded[symbol] = append(r.seeded[symbol], bars...)
}

// TestForwardDailyBars_PersistsNewlyClosedDaysAndStopsOnCancel drives
// forwardDailyBars with a fake generator that has exactly one daily bar
// ready on its first poll, and checks both sinks receive it, then that the
// loop actually exits once ctx is canceled (it must not leak a goroutine
// spinning on the ticker forever).
func TestForwardDailyBars_PersistsNewlyClosedDaysAndStopsOnCancel(t *testing.T) {
	bar := feed.Bar{Symbol: "US.TST", BucketMs: 1_700_000_000_000, O: 10, H: 11, L: 9, C: 10.5, Volume: 1000, Turnover: 10500}
	src := &fakeDailyBarSource{bars: [][]feed.Bar{{bar}}}
	sink := &recordingDailyBarSink{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		forwardDailyBars(ctx, src, sink, sink, 10*time.Millisecond)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for {
		sink.mu.Lock()
		got := len(sink.archived)
		sink.mu.Unlock()
		if got >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("forwardDailyBars never archived the bar")
		case <-time.After(5 * time.Millisecond):
		}
	}

	sink.mu.Lock()
	if len(sink.archived) != 1 || sink.archived[0] != bar {
		t.Fatalf("archived = %+v, want [%+v]", sink.archived, bar)
	}
	if len(sink.seeded["US.TST"]) != 1 || sink.seeded["US.TST"][0] != bar {
		t.Fatalf("seeded[US.TST] = %+v, want [%+v]", sink.seeded["US.TST"], bar)
	}
	sink.mu.Unlock()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("forwardDailyBars did not stop after ctx cancel")
	}
}

// A venue configured with Broker: "sim" runs a real sim.Broker in live mode
// too (a practice venue against live marks), not only in replay. simSinksOf
// must pick it up either way — there is no live/replay distinction to make;
// the type-assertion alone identifies sim brokers correctly in both modes.
func TestSimSinksOfSelectsLiveSimVenue(t *testing.T) {
	simBroker := sim.New("simulator", clock.System{}, 100_000, sim.Options{})
	vbs := []venueBroker{
		{ID: "simulator", Broker: simBroker},
		{ID: "alpaca-paper", Broker: stub.New()},
	}

	sinks := simSinksOf(vbs)

	if len(sinks) != 1 {
		t.Fatalf("got %d sinks, want 1", len(sinks))
	}
	if sinks[0] != simSink(simBroker) {
		t.Fatalf("sink is not the configured sim broker")
	}
}
