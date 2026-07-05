package md

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// runCore starts a core and returns it plus a drain helper.
func runCore(t *testing.T) (*Core, func() []Update) {
	t.Helper()
	c := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.Run(ctx) }()
	var got []Update
	drain := func() []Update {
		for {
			select {
			case u := <-c.Updates():
				got = append(got, u)
			case <-time.After(100 * time.Millisecond):
				return got
			}
		}
	}
	return c, drain
}

// ET 2026-07-06 (Monday) 09:30:00 = 2026-07-06T13:30:00Z = epoch 1783344600.
// This MUST be an exact 09:30 ET anchor instant — the cascade tests depend
// on it landing on 10s/1m/5m bucket boundaries.
const t0Ms = int64(1783344600_000)

func tick(seq int64, offMs int64, price float64, vol int64, dir feed.Direction) feed.Tick {
	return feed.Tick{Symbol: "US.AAPL", Seq: seq, TsMs: t0Ms + offMs, Price: price, Volume: vol, Dir: dir}
}

func TestTapeDedupsBySeqWithinDay(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.TicksEvent{Seed: true, Ticks: []feed.Tick{
		tick(1, 0, 100, 10, feed.Buy), tick(2, 500, 100.1, 5, feed.Sell),
	}})
	// Live push overlaps the seed (seq 2) then continues (seq 3).
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		tick(2, 500, 100.1, 5, feed.Sell), tick(3, 900, 100.2, 7, feed.Buy),
	}})
	var tapes []TapeUpdate
	var marks int
	for _, u := range drain() {
		if tu, ok := u.(TapeUpdate); ok {
			tapes = append(tapes, tu)
		}
	}
	for {
		select {
		case <-c.Marks():
			marks++
			continue
		default:
		}
		break
	}
	if len(tapes) != 2 {
		t.Fatalf("TapeUpdates = %d, want 2 (one per accepted batch)", len(tapes))
	}
	if n := len(tapes[0].Ticks) + len(tapes[1].Ticks); n != 3 {
		t.Fatalf("accepted ticks = %d, want 3 (dup seq=2 dropped)", n)
	}
	if marks != 2 {
		t.Fatalf("marks = %d, want 2 (one per batch)", marks)
	}
}

func TestBookAndQuoteReplaceAndEmit(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.BookEvent{Book: feed.Book{Symbol: "US.AAPL", Bids: []feed.BookLevel{{Price: 100, Volume: 5}}}})
	c.Feed(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 100.5}})
	c.Feed(feed.ConnDownEvent{})
	c.Feed(feed.ResyncedEvent{})
	var kinds []string
	for _, u := range drain() {
		switch u.(type) {
		case BookUpdate:
			kinds = append(kinds, "book")
		case QuoteUpdate:
			kinds = append(kinds, "quote")
		case ConnUpdate:
			kinds = append(kinds, "conn")
		case ResyncedUpdate:
			kinds = append(kinds, "resynced")
		}
	}
	want := []string{"book", "quote", "conn", "resynced"}
	if len(kinds) != 4 {
		t.Fatalf("updates = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("updates order = %v, want %v", kinds, want)
		}
	}
}

// TestTapeDedupResetsOnDayBoundary verifies moomoo's daily sequence restart:
// a low seq on a new ET day must NOT be treated as a duplicate of a high seq
// from the previous day.
func TestTapeDedupResetsOnDayBoundary(t *testing.T) {
	c, drain := runCore(t)
	const oneDayMs = int64(24 * 3600 * 1000)
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		tick(500, 0, 100, 10, feed.Buy), // day 1, high seq
	}})
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		{Symbol: "US.AAPL", Seq: 1, TsMs: t0Ms + oneDayMs, Price: 101, Volume: 1, Dir: feed.Buy}, // day 2, low seq
	}})
	var tapes []TapeUpdate
	for _, u := range drain() {
		if tu, ok := u.(TapeUpdate); ok {
			tapes = append(tapes, tu)
		}
	}
	if len(tapes) != 2 {
		t.Fatalf("TapeUpdates = %d, want 2 (day-2 seq=1 must not dedup against day-1 seq=500)", len(tapes))
	}
	if len(tapes[1].Ticks) != 1 || tapes[1].Ticks[0].Seq != 1 {
		t.Fatalf("day-2 batch = %+v, want the seq=1 tick accepted", tapes[1])
	}
}

// TestDroppedUpdatesIncrementsWhenFull verifies the honesty counter: once the
// updates channel is saturated, further emits are dropped and counted rather
// than blocking the single writer.
func TestDroppedUpdatesIncrementsWhenFull(t *testing.T) {
	c := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	// Flood far more quote events than the updates channel capacity (8192)
	// without ever draining Updates(), forcing overflow.
	const n = 9000
	for i := 0; i < n; i++ {
		c.Feed(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: float64(i)}})
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.DroppedUpdates() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := c.DroppedUpdates(); got == 0 {
		t.Fatalf("DroppedUpdates = %d, want > 0 after flooding an undrained updates channel", got)
	}
}

// TestSeedDailyAndSeedHistory1mDoNotPanic exercises the SeedDaily/
// SeedHistory1m mutators end-to-end through the inbox — Task 11 will give
// them real behavior, but Task 9 must wire the plumbing without panicking
// and without touching state outside Run's goroutine.
func TestSeedDailyAndSeedHistory1mDoNotPanic(t *testing.T) {
	c, _ := runCore(t)
	c.SeedDaily("US.AAPL", []feed.Bar{{Symbol: "US.AAPL", BucketMs: session.DayMs(t0Ms), O: 1, H: 2, L: 0.5, C: 1.5, Volume: 100}})
	c.SeedHistory1m("US.AAPL", []feed.Bar{{Symbol: "US.AAPL", BucketMs: t0Ms, O: 1, H: 2, L: 0.5, C: 1.5, Volume: 10}})
	c.EnsureIndicator("panel-1", IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m})
	c.ReleaseIndicator("panel-1")
}
