package md

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// script builds a deterministic mixed-event day: seed bars, seed ticks, live
// pushes for two symbols, a resync, indicator lifecycle.
func script() []feed.Event {
	rng := rand.New(rand.NewSource(99))
	var evs []feed.Event
	mk := func(sym string, seq int64, offMs int64, px float64, v int64, d feed.Direction) feed.Tick {
		return feed.Tick{Symbol: sym, Seq: seq, TsMs: t0Ms + offMs, Price: px, Volume: v, Dir: d}
	}
	// Cache seeds.
	evs = append(evs, feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{
		{Symbol: "US.AAPL", BucketMs: t0Ms - 120_000, O: 99, H: 99.5, L: 98.9, C: 99.2, Volume: 800},
		{Symbol: "US.AAPL", BucketMs: t0Ms - 60_000, O: 99.2, H: 100, L: 99.1, C: 99.9, Volume: 900},
	}})
	seq := int64(0)
	px := 100.0
	dirs := []feed.Direction{feed.Buy, feed.Sell, feed.Neutral}
	var batch []feed.Tick
	for off := int64(0); off < 300_000; off += 1_000 + int64(rng.Intn(4000)) {
		seq++
		px += rng.Float64() - 0.5
		batch = append(batch, mk("US.AAPL", seq, off, px, int64(rng.Intn(500)+1), dirs[rng.Intn(3)]))
		if len(batch) == 3 {
			evs = append(evs, feed.TicksEvent{Ticks: batch})
			batch = nil
		}
	}
	if len(batch) > 0 {
		evs = append(evs, feed.TicksEvent{Ticks: batch})
	}
	evs = append(evs,
		feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", TsMs: t0Ms + 100_000, Last: px}},
		feed.BookEvent{Book: feed.Book{Symbol: "US.AAPL", TsMs: t0Ms + 100_000,
			Bids: []feed.BookLevel{{Price: px - 0.01, Volume: 300}},
			Asks: []feed.BookLevel{{Price: px + 0.01, Volume: 200}}}},
		feed.Bars1mEvent{Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: t0Ms, O: 100, H: 101, L: 99.5, C: 100.4, Volume: 4000}}},
		feed.ConnDownEvent{}, feed.ConnUpEvent{}, feed.ResyncedEvent{},
		feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: t0Ms, O: 100, H: 101, L: 99.5, C: 100.4, Volume: 4000}}},
	)
	return evs
}

// run feeds the script through a fresh core and returns every update, in order.
func run(t *testing.T, evs []feed.Event) []Update {
	t.Helper()
	c := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	// Collect concurrently so the 8192-cap updates channel never overflows
	// (an overflow would DROP updates and break determinism-by-count).
	var got []Update
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for {
			select {
			case u := <-c.Updates():
				got = append(got, u)
			case <-done:
				for { // core stopped: drain whatever is still buffered
					select {
					case u := <-c.Updates():
						got = append(got, u)
					default:
						return
					}
				}
			}
		}
	}()
	c.EnsureIndicator(1, "vwap-1", IndicatorSpec{Symbol: "US.AAPL", TF: session.TF10s, Type: IndVWAP})
	for _, ev := range evs {
		c.Feed(ev)
	}
	c.EnsureIndicator(1, "ema-1", IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndEMA,
		Params: map[string]float64{"period": 2}})
	// Let the single writer finish the inbox, then stop and drain.
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	<-collected
	return got
}

func TestReplayProducesIdenticalUpdates(t *testing.T) {
	evs := script()
	a := run(t, evs)
	b := run(t, evs)
	if len(a) != len(b) {
		t.Fatalf("update counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			t.Fatalf("update %d differs:\n%#v\n%#v", i, a[i], b[i])
		}
	}
}

// Chunking invariance: re-batching the same ticks into different event sizes
// must not change any FINALIZED bar.
func TestChunkingCannotChangeFinalBars(t *testing.T) {
	evs := script()
	rechunked := make([]feed.Event, 0, len(evs))
	for _, ev := range evs {
		if te, ok := ev.(feed.TicksEvent); ok && !te.Seed {
			for _, tk := range te.Ticks { // one event per tick
				rechunked = append(rechunked, feed.TicksEvent{Ticks: []feed.Tick{tk}})
			}
			continue
		}
		rechunked = append(rechunked, ev)
	}
	finals := func(us []Update) map[string]Bar {
		out := make(map[string]Bar)
		for _, u := range us {
			if bu, ok := u.(BarUpdate); ok && !bu.Bar.InProgress {
				out[fmt.Sprintf("%s/%s/%d", bu.Bar.Symbol, bu.Bar.TF, bu.Bar.BucketMs)] = bu.Bar
			}
		}
		return out
	}
	a := finals(run(t, evs))
	b := finals(run(t, rechunked))
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("finalized bars diverge under re-chunking:\n%d bars vs %d bars", len(a), len(b))
	}
}
