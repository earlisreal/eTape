package md

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
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
	return feed.Tick{Symbol: "US.AAPL", Seq: seq, TsMs: t0Ms + offMs, Price: price, Volume: vol, Dir: dir,
		Type: feed.TransactionRegular, Condition: feed.TradeConditionAutomaticMatch,
		RangeEligible: true, LastEligible: true, VolumeEligible: true}
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

func TestFeedSeedsEmitSnapshotsAndHistoryReady(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{
		{Symbol: "US.AAPL", BucketMs: t0Ms, O: 100, H: 101, L: 99, C: 100.5, Volume: 10},
	}})
	c.Feed(feed.TicksEvent{Seed: true, Ticks: []feed.Tick{
		tick(1, 0, 100, 10, feed.Buy), tick(2, 11_000, 101, 5, feed.Sell),
	}})
	var bars1m, bars10s, finalized10s, ready bool
	for _, update := range drain() {
		switch value := update.(type) {
		case BarUpdate:
			finalized10s = finalized10s || value.Bar.TF == session.TF10s && !value.Bar.InProgress
		case BarSnapshot:
			bars1m = bars1m || value.TF == session.TF1m
			bars10s = bars10s || value.TF == session.TF10s
		case HistoryReadyUpdate:
			ready = ready || value.Symbol == "US.AAPL"
		}
	}
	if !bars1m || !bars10s || !finalized10s || !ready {
		t.Fatalf("seed delivery bars1m=%v bars10s=%v finalized10s=%v ready=%v", bars1m, bars10s, finalized10s, ready)
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
		{Symbol: "US.AAPL", Seq: 1, TsMs: t0Ms + oneDayMs, Price: 101, Volume: 1, Dir: feed.Buy,
			Type: feed.TransactionRegular, Condition: feed.TradeConditionAutomaticMatch}, // day 2, low seq
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

func TestDropStatsDistinguishesInboxAndUpdates(t *testing.T) {
	c := New(Config{})
	for i := 0; i < cap(c.inbox); i++ {
		c.inbox <- eventMsg{ev: feed.ConnUpEvent{}}
	}
	c.Feed(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 1}})

	for i := 0; i < cap(c.updates); i++ {
		c.updates <- ConnUpdate{Up: true}
	}
	c.emit(ConnUpdate{Up: true})

	got := c.DropStats()
	if got.Inbox != 1 || got.Updates != 1 {
		t.Fatalf("DropStats = %+v, want inbox=1 updates=1", got)
	}
	if got.Total() != 2 || c.DroppedUpdates() != 2 {
		t.Fatalf("drop totals = %d/%d, want 2/2", got.Total(), c.DroppedUpdates())
	}

	clean := New(Config{})
	clean.Feed(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 1}})
	clean.emit(ConnUpdate{Up: true})
	if got := clean.DropStats(); got.Total() != 0 {
		t.Fatalf("successful enqueue changed DropStats = %+v", got)
	}
}

func TestFeedContextBlocksUntilSeedFitsInbox(t *testing.T) {
	c := New(Config{})
	for i := 0; i < cap(c.inbox); i++ {
		c.inbox <- eventMsg{ev: feed.ConnUpEvent{}}
	}
	seed := feed.TicksEvent{Seed: true, Ticks: []feed.Tick{tick(1, 0, 100, 1, feed.Buy)}}
	done := make(chan struct{})
	go func() {
		c.FeedContext(context.Background(), seed)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("seed enqueue did not block on full inbox")
	case <-time.After(20 * time.Millisecond):
	}
	<-c.inbox
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("seed enqueue did not resume after inbox space became available")
	}
	found := false
	for len(c.inbox) > 0 {
		msg := <-c.inbox
		if got, ok := msg.(eventMsg); ok {
			if ticks, ok := got.ev.(feed.TicksEvent); ok && ticks.Seed {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("seed missing from inbox")
	}
}

func TestFinalizedBarSinkIsLosslessWhenUpdatesFull(t *testing.T) {
	finals := make(chan Bar, 2)
	c := New(Config{FinalizedBar: func(b Bar) { finals <- b }})
	for i := 0; i < cap(c.updates); i++ {
		c.updates <- ConnUpdate{Up: true}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	c.FeedContext(ctx, feed.TicksEvent{Seed: true, Ticks: []feed.Tick{
		tick(1, 0, 100, 1, feed.Buy), tick(2, 11_000, 101, 1, feed.Buy),
	}})
	select {
	case got := <-finals:
		if got.TF != session.TF10s || got.InProgress || got.BucketMs != t0Ms {
			t.Fatalf("finalized sink got %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("finalized sink missed bar while Updates was full")
	}
	select {
	case got := <-finals:
		t.Fatalf("finalized sink received duplicate/unexpected bar %+v", got)
	case <-time.After(20 * time.Millisecond):
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

func TestSeedChartHistoryEmitsOnePreparedBarrier(t *testing.T) {
	c, drain := runCore(t)
	c.SeedChartHistory("US.AAPL",
		[]feed.Bar{{Symbol: "US.AAPL", BucketMs: session.DayMs(t0Ms), O: 1, H: 2, L: 0.5, C: 1.5}},
		[]feed.Bar{{Symbol: "US.AAPL", BucketMs: t0Ms, O: 1, H: 2, L: 0.5, C: 1.5}},
		[]feed.Bar{{Symbol: "US.AAPL", BucketMs: t0Ms, O: 1, H: 2, L: 0.5, C: 1.5}},
	)
	prepared := 0
	for _, update := range drain() {
		if ready, ok := update.(HistoryReadyUpdate); ok && ready.Prepared {
			prepared++
		}
	}
	if prepared != 1 {
		t.Fatalf("prepared barriers=%d, want 1", prepared)
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

func TestCorePublishesEstimatedLULDOnlyWhenVisibleStateChanges(t *testing.T) {
	now := time.UnixMilli(t0Ms)
	c := New(Config{Clock: clock.NewFake(now)})
	c.apply(eventMsg{at: now, ev: feed.QuoteEvent{Quote: feed.Quote{
		Symbol: "US.AAPL", PrevClose: 100, ProviderStatus: feed.ProviderStatusNormal,
	}}})
	c.apply(eventMsg{at: now, ev: feed.TicksEvent{Ticks: []feed.Tick{tick(1, 0, 100, 1, feed.Buy)}}})
	updates := drainLULDUpdates(c)
	if len(updates) != 1 || updates[0].Value.State != LULDWarming {
		t.Fatalf("initial LULD updates = %+v, want one warming update", updates)
	}
	c.applyTime(now.Add(30 * time.Second))
	if got := drainLULDUpdates(c); len(got) != 0 {
		t.Fatalf("unchanged warming state published = %+v", got)
	}
	c.applyTime(now.Add(5 * time.Minute))
	updates = drainLULDUpdates(c)
	if len(updates) != 1 || updates[0].Value.State != LULDEstimated || updates[0].Value.Lower != 95 || updates[0].Value.Upper != 105 {
		t.Fatalf("estimated LULD updates = %+v", updates)
	}
	c.applyTime(now.Add(5*time.Minute + time.Second))
	if got := drainLULDUpdates(c); len(got) != 0 {
		t.Fatalf("unchanged estimated state published = %+v", got)
	}
}

func TestCoreReconnectSeedSurvivesResyncedWithoutPrematureReady(t *testing.T) {
	now := time.UnixMilli(t0Ms)
	c := New(Config{Clock: clock.NewFake(now)})
	c.apply(eventMsg{at: now, ev: feed.QuoteEvent{Quote: feed.Quote{
		Symbol: "US.AAPL", PrevClose: 100, ProviderStatus: feed.ProviderStatusNormal,
	}}})
	drainLULDUpdates(c)
	c.apply(eventMsg{at: now, ev: feed.ConnDownEvent{}})
	if got := drainLULDUpdates(c); len(got) != 1 || got[0].Value.State != LULDFrozen {
		t.Fatalf("disconnect LULD state = %+v, want frozen", got)
	}
	c.apply(eventMsg{at: now, ev: feed.ConnUpEvent{}})
	drainLULDUpdates(c)
	seedAt := now.Add(time.Minute)
	c.apply(eventMsg{at: seedAt, ev: feed.TicksEvent{Seed: true, Ticks: []feed.Tick{tick(2, 60_000, 100, 1, feed.Buy)}}})
	if got := drainLULDUpdates(c); len(got) != 0 {
		t.Fatalf("seed unexpectedly published ready estimate = %+v", got)
	}
	c.apply(eventMsg{at: seedAt, ev: feed.ResyncedEvent{}})
	if got := drainLULDUpdates(c); len(got) != 0 {
		t.Fatalf("Resynced discarded seed state or published early estimate = %+v", got)
	}
	c.applyTime(seedAt.Add(5 * time.Minute))
	if got := drainLULDUpdates(c); len(got) != 1 || got[0].Value.State != LULDEstimated {
		t.Fatalf("post-reconnect estimate = %+v, want one estimate after warm-up", got)
	}
}

func TestCoreSessionTickSeedUsesCurrentClockForLULDWarmup(t *testing.T) {
	now := etTime(t, 10, 10)
	c := New(Config{Clock: clock.NewFake(now)})
	c.apply(eventMsg{at: now, ev: feed.QuoteEvent{Quote: feed.Quote{
		Symbol: "US.AAPL", PrevClose: 100, ProviderStatus: feed.ProviderStatusNormal,
	}}})
	drainLULDUpdates(c)
	c.apply(seedSessionTicksMsg{symbol: "US.AAPL", ticks: []feed.Tick{
		makeLULDPrint(etTime(t, 10, 9), 100),
	}})
	if got := drainLULDUpdates(c); len(got) != 0 {
		t.Fatalf("session seed changed the warming state = %+v", got)
	}
	c.applyTime(now.Add(4 * time.Minute))
	if got := drainLULDUpdates(c); len(got) != 0 {
		t.Fatalf("historical tick shortened current warm-up = %+v", got)
	}
}

func drainLULDUpdates(c *Core) []EstimatedLULDUpdate {
	var out []EstimatedLULDUpdate
	for {
		select {
		case u := <-c.Updates():
			if l, ok := u.(EstimatedLULDUpdate); ok {
				out = append(out, l)
			}
		default:
			return out
		}
	}
}
