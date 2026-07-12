package synth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

// nopHist is a HistoryStore that returns nothing, used where a test doesn't
// exercise HistoryBars.
type nopHist struct{}

func (nopHist) ReadBars1m(string, int64, int64) ([]feed.Bar, error) { return nil, nil }
func (nopHist) ReadDailyBars(string) ([]feed.Bar, error)            { return nil, nil }

// recordingHist tracks the arguments HistoryBars ends up calling it with, so
// tests can check the Res1m/ResDay routing without a real store.Store.
type recordingHist struct {
	bars1mSymbol         string
	bars1mFrom, bars1mTo int64
	dailySymbol          string
	bars                 []feed.Bar
	err                  error
}

func (r *recordingHist) ReadBars1m(symbol string, fromMs, toMs int64) ([]feed.Bar, error) {
	r.bars1mSymbol = symbol
	r.bars1mFrom = fromMs
	r.bars1mTo = toMs
	return r.bars, r.err
}

func (r *recordingHist) ReadDailyBars(symbol string) ([]feed.Bar, error) {
	r.dailySymbol = symbol
	return r.bars, r.err
}

func TestFeed_ValidateUniverse(t *testing.T) {
	g := New(1, clock.NewFake(timeMs(0)))
	f := NewFeed(g, nopHist{}, clock.NewFake(timeMs(0)))
	real := g.Symbols()[0]
	if err := f.Validate(context.Background(), real); err != nil {
		t.Errorf("universe symbol rejected: %v", err)
	}
	if err := f.Validate(context.Background(), "US.NOPE"); !errors.Is(err, feed.ErrUnknownSymbol) {
		t.Errorf("want ErrUnknownSymbol, got %v", err)
	}
}

func TestFeed_RunEmitsConnUpThenData(t *testing.T) {
	start := int64(1_700_000_000_000)
	fk := clock.NewFake(timeMs(start))
	g := New(7, fk)
	f := NewFeed(g, nopHist{}, fk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()

	// first event is ConnUp
	select {
	case ev := <-f.Events():
		if _, ok := ev.(feed.ConnUpEvent); !ok {
			t.Fatalf("first event not ConnUp: %T", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no ConnUp emitted")
	}

	// Drive the fake clock from a dedicated goroutine, sleeping briefly
	// between ticks so Run's goroutine actually gets scheduled to consume
	// each one. A tight synchronous Advance loop on the test's own goroutine
	// races Run for the scheduler and can complete before Run ever runs even
	// once — that was tried first and reliably starved Run, producing zero
	// events; driving the clock concurrently and receiving with a blocking
	// select (not a default-case poll) fixes that.
	stopAdvancing := make(chan struct{})
	advanceDone := make(chan struct{})
	go func() {
		defer close(advanceDone)
		for {
			select {
			case <-stopAdvancing:
				return
			default:
				fk.Advance(feedTickMs)
				time.Sleep(time.Millisecond)
			}
		}
	}()

	sawConnUp := 0
	sawTicks, sawQuote, sawBook, sawBars := false, false, false, false
	deadline := time.After(5 * time.Second)
drainLoop:
	for {
		select {
		case ev := <-f.Events():
			switch ev.(type) {
			case feed.ConnUpEvent:
				sawConnUp++
			case feed.ResyncedEvent:
				t.Fatal("synth feed must never emit ResyncedEvent")
			case feed.TicksEvent:
				sawTicks = true
			case feed.QuoteEvent:
				sawQuote = true
			case feed.BookEvent:
				sawBook = true
			case feed.Bars1mEvent:
				sawBars = true
			}
			if sawTicks && sawQuote && sawBook && sawBars {
				break drainLoop
			}
		case <-deadline:
			break drainLoop
		}
	}
	close(stopAdvancing)
	<-advanceDone

	if sawConnUp != 0 {
		t.Errorf("ConnUp re-emitted %d times after the first drain loop", sawConnUp)
	}
	if !sawTicks {
		t.Error("never saw a TicksEvent within the deadline")
	}
	if !sawQuote {
		t.Error("never saw a QuoteEvent within the deadline")
	}
	if !sawBook {
		t.Error("never saw a BookEvent within the deadline")
	}
	if !sawBars {
		t.Error("never saw a Bars1mEvent within the deadline")
	}

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}

	// Events() must be closed after Run returns. close(f.out) is deferred
	// inside Run, so it has already executed by the time done received a
	// value; drain any buffered leftovers and confirm the channel reports
	// closed rather than blocking or yielding more data forever.
	drained := 0
	for {
		_, ok := <-f.Events()
		if !ok {
			break
		}
		drained++
		if drained > 100000 {
			t.Fatal("Events() never closed after Run returned")
		}
	}
}

func TestFeed_EnsureReleaseAreNoops(t *testing.T) {
	g := New(2, clock.NewFake(timeMs(0)))
	f := NewFeed(g, nopHist{}, clock.NewFake(timeMs(0)))
	// Must not panic and must not block.
	f.Ensure(feed.Demand{ID: "x", Symbol: g.Symbols()[0]})
	f.Release("x")
}

func TestFeed_RecentTicksAndCachedBars1mDelegateToGenerator(t *testing.T) {
	start := int64(1_700_000_000_000)
	fk := clock.NewFake(timeMs(start))
	g := New(3, fk)
	code := g.Symbols()[0]

	// Advance the generator directly (bypassing Run) so there's tick/bar
	// history to delegate to.
	fk.Advance(90 * time.Second)
	g.StepTo(fk.Now().UnixMilli())

	f := NewFeed(g, nopHist{}, fk)

	wantTicks := g.RecentTicks(code, 5)
	gotTicks, err := f.RecentTicks(context.Background(), code, 5)
	if err != nil {
		t.Fatalf("RecentTicks: %v", err)
	}
	if len(gotTicks) != len(wantTicks) {
		t.Fatalf("RecentTicks len = %d, want %d", len(gotTicks), len(wantTicks))
	}

	wantBars := g.CachedBars1m(code, 5)
	gotBars, err := f.CachedBars1m(context.Background(), code, 5)
	if err != nil {
		t.Fatalf("CachedBars1m: %v", err)
	}
	if len(gotBars) != len(wantBars) {
		t.Fatalf("CachedBars1m len = %d, want %d", len(gotBars), len(wantBars))
	}
}

func TestFeed_BookSnapshotAndQuoteSnapshot(t *testing.T) {
	g := New(4, clock.NewFake(timeMs(0)))
	f := NewFeed(g, nopHist{}, clock.NewFake(timeMs(0)))
	code := g.Symbols()[0]

	book, err := f.BookSnapshot(context.Background(), code)
	if err != nil {
		t.Fatalf("BookSnapshot: %v", err)
	}
	if book.Symbol != code {
		t.Errorf("BookSnapshot.Symbol = %q, want %q", book.Symbol, code)
	}

	quote, err := f.QuoteSnapshot(context.Background(), code)
	if err != nil {
		t.Fatalf("QuoteSnapshot: %v", err)
	}
	if quote.Symbol != code {
		t.Errorf("QuoteSnapshot.Symbol = %q, want %q", quote.Symbol, code)
	}

	if _, err := f.BookSnapshot(context.Background(), "US.NOPE"); !errors.Is(err, feed.ErrUnknownSymbol) {
		t.Errorf("BookSnapshot unknown symbol: want ErrUnknownSymbol, got %v", err)
	}
	if _, err := f.QuoteSnapshot(context.Background(), "US.NOPE"); !errors.Is(err, feed.ErrUnknownSymbol) {
		t.Errorf("QuoteSnapshot unknown symbol: want ErrUnknownSymbol, got %v", err)
	}
}

func TestFeed_HistoryBarsRoutesByResolution(t *testing.T) {
	g := New(5, clock.NewFake(timeMs(0)))
	rec := &recordingHist{bars: []feed.Bar{{Symbol: "US.AAA"}}}
	f := NewFeed(g, rec, clock.NewFake(timeMs(0)))

	from := time.UnixMilli(1_000)
	to := time.UnixMilli(2_000)

	bars, err := f.HistoryBars(context.Background(), "US.AAA", feed.Res1m, from, to)
	if err != nil {
		t.Fatalf("HistoryBars Res1m: %v", err)
	}
	if rec.bars1mSymbol != "US.AAA" || rec.bars1mFrom != from.UnixMilli() || rec.bars1mTo != to.UnixMilli() {
		t.Errorf("ReadBars1m called with (%q, %d, %d), want (US.AAA, %d, %d)",
			rec.bars1mSymbol, rec.bars1mFrom, rec.bars1mTo, from.UnixMilli(), to.UnixMilli())
	}
	if len(bars) != 1 {
		t.Errorf("HistoryBars Res1m returned %d bars, want 1", len(bars))
	}

	rec.dailySymbol = ""
	bars, err = f.HistoryBars(context.Background(), "US.BBB", feed.ResDay, from, to)
	if err != nil {
		t.Fatalf("HistoryBars ResDay: %v", err)
	}
	if rec.dailySymbol != "US.BBB" {
		t.Errorf("ReadDailyBars called with %q, want US.BBB", rec.dailySymbol)
	}
	if len(bars) != 1 {
		t.Errorf("HistoryBars ResDay returned %d bars, want 1", len(bars))
	}
}

func TestFeed_HistoryBarsPropagatesError(t *testing.T) {
	g := New(6, clock.NewFake(timeMs(0)))
	wantErr := errors.New("boom")
	rec := &recordingHist{err: wantErr}
	f := NewFeed(g, rec, clock.NewFake(timeMs(0)))

	if _, err := f.HistoryBars(context.Background(), "US.AAA", feed.Res1m, time.Time{}, time.Time{}); !errors.Is(err, wantErr) {
		t.Errorf("HistoryBars did not propagate store error: %v", err)
	}
}

// Compile-time interface assertions live in feed.go; this test just exercises
// Run's ConnUp-once behavior in isolation (no reliance on Drain producing
// data), guarding against a regression where ConnUp gets re-sent on every
// tick.
func TestFeed_RunConnUpOnce(t *testing.T) {
	fk := clock.NewFake(timeMs(0))
	g := New(9, fk)
	f := NewFeed(g, nopHist{}, fk)
	ctx, cancel := context.WithCancel(context.Background())

	go f.Run(ctx)

	select {
	case ev := <-f.Events():
		if _, ok := ev.(feed.ConnUpEvent); !ok {
			t.Fatalf("first event not ConnUp: %T", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no ConnUp emitted")
	}

	for i := 0; i < 10; i++ {
		fk.Advance(50 * time.Millisecond)
	}

	timeout := time.After(500 * time.Millisecond)
drainLoop:
	for {
		select {
		case ev := <-f.Events():
			if _, ok := ev.(feed.ConnUpEvent); ok {
				t.Fatal("ConnUp emitted a second time")
			}
		case <-timeout:
			break drainLoop
		}
	}
	cancel()
}
