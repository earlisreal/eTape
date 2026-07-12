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

// TestSeedHistory1mLossless is the regression test for the "only a few 1m
// bars render" bug: seeding a full multi-day history (finalized bars,
// cascading to 5m/15m/30m/60m/daily/weekly/monthly, ~8 emits/bar pre-fix)
// with NO concurrent drain must not overflow the 8192-deep updates channel.
// Before the fix (per-bar BarUpdate emission during the seed loop), this
// flooded the channel and DroppedUpdates() went non-zero — the seeded
// history bars were silently lost and never reached the mirror/UI. After the
// fix (one BarSnapshot per touched timeframe), the whole seed costs a
// handful of emits, well under the channel capacity.
func TestSeedHistory1mLossless(t *testing.T) {
	c := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	const n = 20000
	bars := make([]feed.Bar, n)
	for i := range bars {
		bars[i] = bar1m(i, 100, 101, 99, 100.5, 100)
	}
	c.SeedHistory1m("US.AAPL", bars)

	// Give the seed apply time to finish without draining Updates() at all —
	// exactly what a slow/absent-yet consumer during a deep backfill looks
	// like.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if d := c.DroppedUpdates(); d != 0 {
		t.Fatalf("seed dropped %d update(s) (history delivery is lossy)", d)
	}
}

// TestSeedHistory1mEmitsCompleteSnapshot verifies the seed's lossless
// replacement: draining after a seed yields exactly one BarSnapshot for the
// seeded timeframe, carrying every seeded bar in order (not per-bar
// BarUpdates, and not a partial/truncated series).
func TestSeedHistory1mEmitsCompleteSnapshot(t *testing.T) {
	c, drain := runCore(t)
	const n = 500
	bars := make([]feed.Bar, n)
	for i := range bars {
		bars[i] = bar1m(i, 100, 101, 99, 100.5, 100)
	}
	c.SeedHistory1m("US.AAPL", bars)

	var snaps []BarSnapshot
	for _, u := range drain() {
		if bs, ok := u.(BarSnapshot); ok && bs.Symbol == "US.AAPL" && bs.TF == session.TF1m {
			snaps = append(snaps, bs)
		}
		if _, ok := u.(BarUpdate); ok {
			t.Fatalf("seed emitted a per-bar BarUpdate instead of a snapshot: %+v", u)
		}
	}
	if len(snaps) != 1 {
		t.Fatalf("BarSnapshot count for US.AAPL/1m = %d, want 1", len(snaps))
	}
	if got := len(snaps[0].Bars); got != n {
		t.Fatalf("snapshot bars = %d, want %d (lossless)", got, n)
	}
	for i, b := range snaps[0].Bars {
		if b.BucketMs != t0Ms+int64(i)*60_000 {
			t.Fatalf("snapshot bar %d out of order: %+v", i, b)
		}
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
	c.EnsureIndicator(1, "panel-1", IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m})
	c.ReleaseIndicator(1, "panel-1")
}
