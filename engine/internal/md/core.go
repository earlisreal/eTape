// Package md is the market-data core: one goroutine owns books, tape, quotes,
// bars and indicators, consuming feed events and control messages from a
// single inbox and emitting typed updates + last-trade marks. The apply path
// does no I/O and never reads the wall clock — replaying the same events
// reproduces the same state, always.
package md

import (
	"context"
	"sync/atomic"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// Config sizes the core. Zero values get defaults.
type Config struct {
	TapeRing   int   // per-symbol tick ring capacity (default 65536)
	AnchorSecs int64 // intraday bucket anchor (default session.AnchorSecsDefault)
}

type inMsg interface{ isInMsg() }

type eventMsg struct{ ev feed.Event }
type ensureIndicatorMsg struct {
	id   string
	spec IndicatorSpec
}
type releaseIndicatorMsg struct{ id string }
type seedDailyMsg struct {
	symbol string
	bars   []feed.Bar
}
type seedHistory1mMsg struct {
	symbol string
	bars   []feed.Bar
}
type seedOlder1mMsg struct {
	symbol string
	bars   []feed.Bar
}
type seedSessionTicksMsg struct {
	symbol string
	ticks  []feed.Tick
}

func (eventMsg) isInMsg()            {}
func (ensureIndicatorMsg) isInMsg()  {}
func (releaseIndicatorMsg) isInMsg() {}
func (seedDailyMsg) isInMsg()        {}
func (seedHistory1mMsg) isInMsg()    {}
func (seedOlder1mMsg) isInMsg()      {}
func (seedSessionTicksMsg) isInMsg() {}

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

// Feed enqueues a feed event. Blocking by design: the inbox is deep and the
// apply path is allocation-light, so sustained blocking means the core is
// genuinely overloaded — that must surface upstream, not vanish.
func (c *Core) Feed(ev feed.Event) { c.inbox <- eventMsg{ev: ev} }

func (c *Core) EnsureIndicator(id string, spec IndicatorSpec) {
	c.inbox <- ensureIndicatorMsg{id: id, spec: spec}
}
func (c *Core) ReleaseIndicator(id string) { c.inbox <- releaseIndicatorMsg{id: id} }
func (c *Core) SeedDaily(symbol string, bars []feed.Bar) {
	c.inbox <- seedDailyMsg{symbol: symbol, bars: bars}
}
func (c *Core) SeedHistory1m(symbol string, bars []feed.Bar) {
	c.inbox <- seedHistory1mMsg{symbol: symbol, bars: bars}
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
		c.inds.ensure(c, msg.id, msg.spec) // Task 12
	case releaseIndicatorMsg:
		c.inds.release(msg.id)
	case seedDailyMsg:
		c.bars.seedDaily(c, msg.symbol, msg.bars) // Task 11
	case seedHistory1mMsg:
		c.bars.seedHistory1m(c, msg.symbol, msg.bars)
	case seedOlder1mMsg:
		c.bars.seedOlder1m(c, msg.symbol, msg.bars)
	case seedSessionTicksMsg:
		c.seedSessionTicks(msg.symbol, msg.ticks)
	}
}

func (c *Core) applyEvent(ev feed.Event) {
	switch e := ev.(type) {
	case feed.TicksEvent:
		c.applyTicks(e)
	case feed.QuoteEvent:
		c.emit(QuoteUpdate{Quote: c.quotes.set(e.Quote)})
	case feed.BookEvent:
		stored := c.books.set(e.Book)
		c.emit(BookUpdate{Book: stored})
		c.emitBook(stored)
	case feed.Bars1mEvent:
		c.bars.apply1m(c, e.Bars) // Task 11
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
