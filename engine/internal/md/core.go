// Package md is the market-data core: one goroutine owns books, tape, quotes,
// bars and indicators, consuming feed events and control messages from a
// single inbox and emitting typed updates + last-trade marks. The apply path
// does no I/O and never reads the wall clock — replaying the same events
// reproduces the same state, always.
package md

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// Config sizes the core. Zero values get defaults.
type Config struct {
	TapeRing     int       // per-symbol tick ring capacity (default 65536)
	AnchorSecs   int64     // intraday bucket anchor (default session.AnchorSecsDefault)
	FinalizedBar func(Bar) // optional lossless sink, called before UI emission
}

type inMsg interface{ isInMsg() }

type eventMsg struct{ ev feed.Event }
type ensureIndicatorMsg struct {
	connID uint64
	id     string
	spec   IndicatorSpec
}
type releaseIndicatorMsg struct {
	connID uint64
	id     string
}
type seedDailyMsg struct {
	symbol string
	bars   []feed.Bar
}
type seedHistory1mMsg struct {
	symbol string
	bars   []feed.Bar
}
type seedHistory10sMsg struct {
	symbol string
	bars   []feed.Bar
}
type seedChartHistoryMsg struct {
	symbol                 string
	daily, bars1m, bars10s []feed.Bar
	queued                 time.Time
	done                   chan struct{}
}
type seedOlder1mMsg struct {
	symbol string
	bars   []feed.Bar
}
type seedSessionTicksMsg struct {
	symbol string
	ticks  []feed.Tick
}
type historyBarrierMsg struct {
	symbol string
	done   chan struct{}
}

func (eventMsg) isInMsg()            {}
func (ensureIndicatorMsg) isInMsg()  {}
func (releaseIndicatorMsg) isInMsg() {}
func (seedDailyMsg) isInMsg()        {}
func (seedHistory1mMsg) isInMsg()    {}
func (seedHistory10sMsg) isInMsg()   {}
func (seedChartHistoryMsg) isInMsg() {}
func (seedOlder1mMsg) isInMsg()      {}
func (seedSessionTicksMsg) isInMsg() {}
func (historyBarrierMsg) isInMsg()   {}

// Core is the single-writer market-data state machine.
type Core struct {
	cfg     Config
	inbox   chan inMsg
	updates chan Update
	marks   chan Mark
	bookOut chan feed.Book
	dropped atomic.Uint64

	// Domain state — touched ONLY inside Run's goroutine.
	books   *bookStore
	quotes  *quoteStore
	tapes   map[string]*ring
	lastSeq map[string]int64 // per-symbol tick dedup high-water
	lastDay map[string]int64 // ET day of lastSeq (sequences restart daily)
	bars    *barEngine       // Task 11
	inds    *indicatorSet    // Task 12

	// seeding is true only while barEngine.seedHistory1m/seedDaily are
	// looping over a history batch. It suppresses barOut's per-bar fan-out
	// (BarUpdate + indicator recompute) so a deep seed emits a handful of
	// BarSnapshots instead of thousands of per-bar updates that would
	// overflow the updates channel. Touched only inside Run's goroutine, like
	// every other field above.
	seeding bool
}

// New builds a Core; Run must be started before Feed is called.
func New(cfg Config) *Core {
	if cfg.TapeRing == 0 {
		cfg.TapeRing = 65536
	}
	if cfg.AnchorSecs == 0 {
		cfg.AnchorSecs = session.AnchorSecsDefault
	}
	return &Core{
		cfg:     cfg,
		inbox:   make(chan inMsg, 1024),
		updates: make(chan Update, 8192),
		marks:   make(chan Mark, 1024),
		bookOut: make(chan feed.Book, 1024),
		books:   newBookStore(),
		quotes:  newQuoteStore(),
		tapes:   make(map[string]*ring),
		lastSeq: make(map[string]int64),
		lastDay: make(map[string]int64),
		bars:    newBarEngine(cfg.AnchorSecs),
		inds:    newIndicatorSet(),
	}
}

func (c *Core) Updates() <-chan Update  { return c.updates }
func (c *Core) Marks() <-chan Mark      { return c.marks }
func (c *Core) Books() <-chan feed.Book { return c.bookOut }
func (c *Core) DroppedUpdates() uint64  { return c.dropped.Load() }

// Feed enqueues a feed event. Non-blocking: live tick events (book/quote/trade)
// are time-sensitive and safe to drop — OpenD re-delivers on next push.
// Sustained drops are visible in DroppedUpdates() and the dropped-updates
// watcher's sys.events. The seed path (SeedDaily/SeedHistory1m) uses separate
// blocking sends and must never be dropped.
// ponytail: drop-on-full for live ticks; seed sends still block so backfill
// order is preserved. If inbox saturation recurs under full load, split into
// separate seedCh + liveCh channels.
func (c *Core) Feed(ev feed.Event) {
	select {
	case c.inbox <- eventMsg{ev: ev}:
	default:
		c.dropped.Add(1)
	}
}

// FeedContext preserves cache seeds under inbox backpressure. Live events keep
// Feed's drop-on-full behavior; callers can cancel a blocked seed on shutdown.
func (c *Core) FeedContext(ctx context.Context, ev feed.Event) {
	if !seedEvent(ev) {
		c.Feed(ev)
		return
	}
	select {
	case c.inbox <- eventMsg{ev: ev}:
	case <-ctx.Done():
	}
}

func seedEvent(ev feed.Event) bool {
	switch e := ev.(type) {
	case feed.TicksEvent:
		return e.Seed
	case feed.Bars1mEvent:
		return e.Seed
	case feed.QuoteEvent:
		return e.Seed
	case feed.BookEvent:
		return e.Seed
	default:
		return false
	}
}

func (c *Core) EnsureIndicator(connID uint64, id string, spec IndicatorSpec) {
	c.inbox <- ensureIndicatorMsg{connID: connID, id: id, spec: spec}
}
func (c *Core) ReleaseIndicator(connID uint64, id string) {
	c.inbox <- releaseIndicatorMsg{connID: connID, id: id}
}
func (c *Core) SeedDaily(symbol string, bars []feed.Bar) {
	c.inbox <- seedDailyMsg{symbol: symbol, bars: bars}
}
func (c *Core) SeedHistory1m(symbol string, bars []feed.Bar) {
	c.inbox <- seedHistory1mMsg{symbol: symbol, bars: bars}
}

// SeedHistory10s enqueues a batch of 10s bars for deep-history seed.
func (c *Core) SeedHistory10s(symbol string, bars []feed.Bar) {
	c.inbox <- seedHistory10sMsg{symbol: symbol, bars: bars}
}

// SeedChartHistory applies one complete focused-chart seed and waits for its
// ordered chart-ready barrier.
func (c *Core) SeedChartHistory(symbol string, daily, bars1m, bars10s []feed.Bar) {
	done := make(chan struct{})
	c.inbox <- seedChartHistoryMsg{
		symbol: symbol, daily: daily, bars1m: bars1m, bars10s: bars10s,
		queued: time.Now(), done: done,
	}
	<-done
}

// SeedOlder1m enqueues a strictly-older chunk of 1m bars (a pan-triggered
// deeper-history load). It upserts into the existing series, cascades into
// 5m/15m/30m/60m, and emits one BarPrepend per intraday timeframe carrying
// only the newly-added older bars — never a full BarSnapshot re-emit.
func (c *Core) SeedOlder1m(symbol string, bars []feed.Bar) {
	c.inbox <- seedOlder1mMsg{symbol: symbol, bars: bars}
}

// SeedSessionTicks reconstructs a symbol's tick-derived bars (10s + shadow
// 1m) from a batch of persisted ticks (e.g. the journal, after a restart)
// without touching the tape ring and without emitting TapeUpdate/Mark — a
// reconstruction must not replay tape/mark side effects or push a stale
// last-trade price into execution.
func (c *Core) SeedSessionTicks(symbol string, ticks []feed.Tick) {
	c.inbox <- seedSessionTicksMsg{symbol: symbol, ticks: ticks}
}

func (c *Core) SyncHistory(symbol string) {
	done := make(chan struct{})
	c.inbox <- historyBarrierMsg{symbol: symbol, done: done}
	<-done
}

// Run is the single writer. It returns when ctx is done.
func (c *Core) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m := <-c.inbox:
			c.apply(m)
		}
	}
}

func (c *Core) emit(u Update) {
	select {
	case c.updates <- u:
	default:
		c.dropped.Add(1)
	}
}

// barOut is the single door for bar emissions: update stream + indicators.
// While c.seeding is true (inside seedHistory1m/seedDaily), it is a no-op:
// the seed path still mutates series state via upsert before calling barOut,
// so suppressing the emit here never changes computed state -- only what
// gets published. The seed functions emit one BarSnapshot per timeframe (and
// one indicator reseed per attached instance) after their loop instead.
func (c *Core) barOut(b Bar) {
	if c.seeding {
		return
	}
	if !b.InProgress && c.cfg.FinalizedBar != nil {
		c.cfg.FinalizedBar(b)
	}
	c.emit(BarUpdate{Bar: b})
	c.inds.onBar(c, b)
}

func (c *Core) mark(m Mark) {
	select {
	case c.marks <- m:
	default: // marks are keep-latest downstream; dropping stale ones is safe
	}
}

func (c *Core) emitBook(b feed.Book) {
	select {
	case c.bookOut <- b:
	default: // keep-latest downstream; dropping a stale book is safe
	}
}

func (c *Core) apply(m inMsg) {
	switch msg := m.(type) {
	case eventMsg:
		c.applyEvent(msg.ev)
	case ensureIndicatorMsg:
		c.inds.ensure(c, msg.connID, msg.id, msg.spec) // Task 12
		// Indicator snapshots stay engine-side; UI pulls matching viewport after
		// this ordered barrier reaches hub. Without it SubscribeIndicator's ack
		// races ahead of reseed/mirror application on timeframe changes.
		c.emit(HistoryReadyUpdate{Symbol: msg.spec.Symbol})
	case releaseIndicatorMsg:
		c.inds.release(msg.connID, msg.id)
	case seedDailyMsg:
		c.bars.seedDaily(c, msg.symbol, msg.bars) // Task 11
	case seedHistory1mMsg:
		c.bars.seedHistory1m(c, msg.symbol, msg.bars)
	case seedHistory10sMsg:
		started := time.Now()
		c.bars.seedHistory10s(c, msg.symbol, msg.bars)
		slog.Info("10s history seed complete", "symbol", msg.symbol, "bars", len(msg.bars),
			"elapsed", time.Since(started).Round(time.Millisecond))
	case seedChartHistoryMsg:
		started := time.Now()
		queueWait := started.Sub(msg.queued)
		c.bars.seedDaily(c, msg.symbol, msg.daily)
		c.bars.seedHistory1m(c, msg.symbol, msg.bars1m)
		c.bars.seedHistory10s(c, msg.symbol, msg.bars10s)
		c.emit(HistoryReadyUpdate{Symbol: msg.symbol, Prepared: true})
		close(msg.done)
		slog.Info("chart history core seed complete", "symbol", msg.symbol,
			"daily", len(msg.daily), "bars1m", len(msg.bars1m), "bars10s", len(msg.bars10s),
			"queueWait", queueWait.Round(time.Millisecond),
			"processing", time.Since(started).Round(time.Millisecond))
	case seedOlder1mMsg:
		c.bars.seedOlder1m(c, msg.symbol, msg.bars)
	case seedSessionTicksMsg:
		c.seedSessionTicks(msg.symbol, msg.ticks)
	case historyBarrierMsg:
		c.emit(HistoryReadyUpdate{Symbol: msg.symbol, Prepared: true})
		close(msg.done)
	}
}

func (c *Core) applyEvent(ev feed.Event) {
	switch e := ev.(type) {
	case feed.TicksEvent:
		if e.Seed && len(e.Ticks) > 0 {
			c.applyTicks(e)
			symbol := e.Ticks[0].Symbol
			c.bars.emitTickSeedSnapshots(c, symbol)
			c.emit(HistoryReadyUpdate{Symbol: symbol})
		} else {
			c.applyTicks(e)
		}
	case feed.QuoteEvent:
		c.emit(QuoteUpdate{Quote: c.quotes.set(e.Quote)})
	case feed.BookEvent:
		stored := c.books.set(e.Book)
		c.emit(BookUpdate{Book: stored})
		c.emitBook(stored)
	case feed.Bars1mEvent:
		if e.Seed && len(e.Bars) > 0 {
			c.bars.seedHistory1m(c, e.Bars[0].Symbol, e.Bars)
			c.emit(HistoryReadyUpdate{Symbol: e.Bars[0].Symbol})
		} else {
			c.bars.apply1m(c, e.Bars) // Task 11
		}
	case feed.ConnUpEvent:
		c.emit(ConnUpdate{Up: true})
	case feed.ConnDownEvent:
		c.emit(ConnUpdate{Up: false})
	case feed.ResyncedEvent:
		c.bars.markGaps() // Task 11: next tick-derived bars carry Gap
		c.emit(ResyncedUpdate{})
	}
}

// dedupTicks applies the (day, seq) high-water dedup, advancing lastSeq/lastDay,
// and returns the accepted ticks. Shared by applyTicks and seedSessionTicks.
func (c *Core) dedupTicks(symbol string, ticks []feed.Tick) []feed.Tick {
	accepted := make([]feed.Tick, 0, len(ticks))
	for _, t := range ticks {
		day := session.DayMs(t.TsMs)
		if day != c.lastDay[t.Symbol] {
			c.lastDay[t.Symbol] = day
			c.lastSeq[t.Symbol] = 0
		}
		if t.Seq != 0 && t.Seq <= c.lastSeq[t.Symbol] {
			continue // seed/live overlap or duplicate push
		}
		c.lastSeq[t.Symbol] = t.Seq
		accepted = append(accepted, t)
	}
	return accepted
}

// applyTicks dedups by (day, seq), appends to the tape, drives tick-derived
// bars, and emits one TapeUpdate + one Mark per accepted batch.
func (c *Core) applyTicks(e feed.TicksEvent) {
	if len(e.Ticks) == 0 {
		return
	}
	symbol := e.Ticks[0].Symbol
	accepted := c.dedupTicks(symbol, e.Ticks)
	if len(accepted) == 0 {
		return
	}
	tape := c.tapes[symbol]
	if tape == nil {
		tape = newRing(c.cfg.TapeRing)
		c.tapes[symbol] = tape
	}
	for _, t := range accepted {
		tape.append(t)
	}
	c.bars.applyTicks(c, accepted) // Task 11 (10s + shadow 1m)
	c.emit(TapeUpdate{Symbol: symbol, Ticks: accepted})
	last := accepted[len(accepted)-1]
	c.mark(Mark{Symbol: last.Symbol, Price: last.Price, TsMs: last.TsMs})
}

// seedSessionTicks reconstructs tick-derived bars from a batch of persisted
// ticks (see SeedSessionTicks) — dedup only, no tape append, no TapeUpdate,
// no Mark. Bar emission is suppressed for the whole batch (c.seeding), then
// one BarSnapshot per touched timeframe replaces it, matching the
// seedHistory1m/seedDaily pattern.
func (c *Core) seedSessionTicks(symbol string, ticks []feed.Tick) {
	if len(ticks) == 0 {
		return
	}
	accepted := c.dedupTicks(symbol, ticks) // same dedup as live
	if len(accepted) == 0 {
		return
	}
	c.seeding = true
	c.bars.applyTicks(c, accepted) // agg10 + shadow; barOut suppressed
	c.seeding = false
	c.bars.emitTickSeedSnapshots(c, symbol) // one BarSnapshot per touched TF
	c.inds.reseedSymbol(c, symbol)          // same as seedHistory1m
}
