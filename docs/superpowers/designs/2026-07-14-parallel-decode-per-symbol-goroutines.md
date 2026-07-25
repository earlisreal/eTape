# eTape Engine — Design: Parallel Decode + Per-Symbol Goroutines

> **Status:** Draft. Follows Plan 7 (remove K_1M subscription). Implement only if benchmarks show tick bursts cause visible lag after K_1M removal.

**Goal:** Eliminate cross-symbol blocking in the md core by replacing the single-goroutine design with per-symbol goroutines, and parallelize OpenD protobuf decode to reduce upstream latency.

**Problem statement:** The current single-writer md core processes all events from all symbols sequentially. During a TICKER burst on one symbol (e.g., AAPL at market open), other symbols' bars/quotes/books stall until the inbox drains. With 50-100 symbols, this creates tail latency of 10-50ms for non-bursting symbols during bursts.

**Approach:** Two-layer parallelism:
1. **Parallel decode:** Multiple goroutines decode OpenD protobuf frames concurrently (upstream optimization).
2. **Per-symbol goroutines:** Each symbol owns its own goroutine and state; events route to the correct goroutine by symbol key.

---

## Architecture

### Before (single goroutine)

```
┌─────────────────────────────────────────────────────┐
│  TCP receive → DecodePush() → Pushes()              │
│                        ↓                            │
│  OpenDFeed.pump → events channel                    │
│                        ↓                            │
│  md.Core.Run (ONE goroutine) ← inbox (1024 buf)     │
│    ├─ applyTicks()   → tape ring + 10s bars         │
│    ├─ applyQuote()   → quoteStore                   │
│    ├─ applyBook()    → bookStore                    │
│    └─ applyBar()     → indicators + cascade          │
└─────────────────────────────────────────────────────┘
```

**Bottleneck:** All events serialized through one goroutine. AAPL tick burst blocks MSFT bar update.

### After (parallel decode + per-symbol goroutines)

```
┌──────────────────────────────────────────────────────┐
│  Layer 1: Reader (single, unchanged I/O path)         │
│  reader goroutine: read(44) → read(body) → frames ch  │
│                        ↓                              │
│  ┌──────────────────────────────────────────────────┐│
│  │ Layer 2: Decode goroutines (parallel, N workers)  ││
│  │                                                   ││
│  │  decoder[0]: DecodePush(frame-A) → events ch      ││
│  │  decoder[1]: DecodePush(frame-B) → events ch      ││
│  │  ...                                              ││
│  │  (pure function, no shared state — safe to parallelize) ││
│  └──────────────────────────────────────────────────┘│
│                        ↓ (fan-in merged events)       │
│  ┌──────────────────────────────────────────────────┐│
│  │ Layer 3: Router (single, hash lookup)             ││
│  │                                                   ││
│  │  router goroutine:                                ││
│  │    for ev := range events {                       ││
│  │      sym := ev.symbol()                            ││
│  │      symGoroutines[sym] <- eventMsg{ev}           ││
│  │    }                                              ││
│  └──────────────────────────────────────────────────┘│
│                        ↓                              │
│  ┌──────────────────────────────────────────────────┐│
│  │ Layer 4: Per-symbol goroutines (N instances)      ││
│  │                                                   ││
│  │  symbolCore["AAPL"]: inbox → apply → updates ch   ││
│  │  symbolCore["MSFT"]: inbox → apply → updates ch   ││
│  │  ...                                              ││
│  │  (each owns its own state: tape, quote, book, bars) ││
│  └──────────────────────────────────────────────────┘│
│                        ↓                              │
│  ┌──────────────────────────────────────────────────┐│
│  │ Layer 5: Aggregator (single, collects all outputs)││
│  │                                                   ││
│  │  aggregator goroutine:                            ││
│  │    for each symbolCore.updates ch {              ││
│  │      fan-in to uihub.Hub channel                  ││
│  │    }                                              ││
│  └──────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────┘
```

---

## Layer 1: Reader (unchanged I/O path)

The reader goroutine handles TCP framing only — no decode. It yields complete frames on a channel for the decode layer to consume.

**File:** `engine/internal/feed/opend/client.go` (modify `serveConn`)

```go
// serveConn reads from the TCP socket, assembles frames, and yields them on
// c.frames. No protobuf decode happens here — that's done in parallel by
// decode workers. On disconnect, all pending frames are drained and c.state
// transitions to ConnDown.
func (c *Client) serveConn(ctx context.Context, sctx *connState) {
    defer close(c.frames)  // signal to decoders: no more frames
    defer func() {
        c.mu.Lock()
        c.state = ConnDown
        c.mu.Unlock()
        select {
        case c.stateCh <- ConnDown:
        default:
        }
    }()

    for {
        // Read the 44-byte header
        header := make([]byte, 44)
        _, err := io.ReadFull(c.conn, header)
        if err != nil {
            return
        }

        frame, err := ParseHeader(header)
        if err != nil {
            slog.Warn("frame parse error", "err", err)
            continue
        }

        // Read the body
        if frame.BodyLen > 0 {
            body := make([]byte, frame.BodyLen)
            _, err = io.ReadFull(c.conn, body)
            if err != nil {
                return
            }
            frame.Body = body
        }

        select {
        case c.frames <- *frame:
        case <-ctx.Done():
            return
        }
    }
}
```

---

## Layer 2: Decode goroutines (parallel)

Multiple decoder goroutines read frames from `c.frames` and decode them in parallel. Each call to `DecodePush()` is a pure function with no shared state — safe to run concurrently.

**File:** `engine/internal/feed/opend/decode_parallel.go` (new file)

```go
package opend

import (
	"log/slog"
)

// DecodeWorker runs N parallel workers that read raw frames from framesCh
// and emit decoded feed.Events on eventsCh. Workers are created by
// StartDecoders() and stopped when the returned stop channel is closed.
type decodeWorker struct {
	frames  <-chan *Frame
	events  chan<- Event
	stopped chan struct{}
}

func (w *decodeWorker) run(ctx context.Context) {
	w.stopped = make(chan struct{})
	for {
		select {
		case frame, ok := <-w.frames:
			if !ok {
				return
			}
			evs, err := DecodePush(*frame)  // pure function, no shared state
			if err != nil {
				// Decode failures are logged and dropped — the feed wrapper
				// owns coalescing/backpressure. See decode.go for details.
				slog.Debug("decode error", "protoID", frame.ProtoID, "err", err)
				continue
			}
			for _, ev := range evs {
				select {
				case w.events <- ev:
				case <-ctx.Done():
					return
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// StartDecoders creates numWorkers parallel decode goroutines. Each reads from
// framesCh and fans decoded events into a single merged events channel. Returns
// the shared events channel and a stop function. The events channel is closed
// when stop() is called.
func StartDecoders(ctx context.Context, frames <-chan *Frame, numWorkers int) (<-chan Event, func()) {
	if numWorkers <= 0 {
		numWorkers = 4
	}

	eventsCh := make(chan Event, 4096)  // larger buffer to absorb decode bursts
	stopChan := make(chan struct{})

	var workers []struct{ stopped chan struct{} }
	for i := 0; i < numWorkers; i++ {
		w := &decodeWorker{
			frames: frames,
			events: eventsCh,
		}
		go w.run(ctx)
		workers = append(workers, struct{ stopped chan struct{} }{w.stopped})
	}

	stop := func() {
		close(stopChan)
		for _, w := range workers {
			<-w.stopped  // wait for each worker to exit
		}
		close(eventsCh)
	}

	return eventsCh, stop
}
```

**Why parallel decode matters:** Protobuf unmarshal allocates and walks nested message structures. For a TICKER push with 100 ticks, this is ~50μs of CPU time on a single core. With 50 symbols each getting a push simultaneously, that's ~2.5ms of serial decode work — enough to add tail latency to the md core inbox.

---

## Layer 3: Router (single goroutine)

The router reads decoded events from the merged channel and dispatches each event to the correct symbol goroutine via hash lookup. This is the fan-out point — it's single-threaded but extremely fast (O(1) map lookup).

**File:** `engine/internal/md/router.go` (new file)

```go
package md

import (
	"context"
	"log/slog"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// Router fans decoded feed.Events from the parallel decode layer into per-symbol
// inbox channels. It is a single goroutine that performs an O(1) hash lookup
// to route each event to the correct symbol core. The router must be started
// after all symbol cores are created (so the map is populated) and before any
// events start flowing.
type Router struct {
	symbolInboxes map[string]chan inMsg  // symbol → per-symbol inbox
	aggregatorCh  chan<- Update          // aggregated output channel
}

// NewRouter creates a router with the given symbol inboxes. Each inbox must be
// at least size 256 to absorb short bursts without blocking the decode layer.
func NewRouter(symbolInboxes map[string]chan inMsg, aggregatorCh chan<- Update) *Router {
	return &Router{
		symbolInboxes: symbolInboxes,
		aggregatorCh:  aggregatorCh,
	}
}

// Run routes events to per-symbol inboxes. It blocks until ctx is done or the
// events channel closes. Events for the same symbol are always delivered in
// order (guaranteed by per-symbol inbox serialization).
func (r *Router) Run(ctx context.Context, events <-chan feed.Event) {
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			r.route(ev)
		case <-ctx.Done():
			return
		}
	}
}

// route dispatches one event to the correct symbol goroutine. It handles all
// event types and extracts the symbol key appropriately.
func (r *Router) route(ev feed.Event) {
	symbol := eventSymbol(ev)
	if symbol == "" {
		// Connection events affect all symbols — broadcast.
		if _, ok := ev.(feed.ConnUpEvent); ok || ev.(feed.ConnDownEvent); ok {
			for _, ch := range r.symbolInboxes {
				ch <- eventMsg{ev: ev}
			}
			return
		}
		slog.Warn("router: event with no symbol", "type", typeOf(ev))
		return
	}

	inbox, ok := r.symbolInboxes[symbol]
	if !ok {
		// Symbol not yet registered — drop the event. The md core will
		// create state on first event, but the router must pre-populate
		// the map (see StartSymbolCore).
		return
	}

	select {
	case inbox <- eventMsg{ev: ev}:
	case <-context.Background().Done(): // shouldn't happen, but guard against panics
		slog.Error("router: context unexpectedly done")
	}
}

// eventSymbol extracts the symbol key from a feed.Event. Returns "" for
// connection events (which are broadcast to all symbols).
func eventSymbol(ev feed.Event) string {
	switch e := ev.(type) {
	case feed.TicksEvent:
		if len(e.Ticks) > 0 {
			return e.Ticks[0].Symbol
		}
	case feed.QuoteEvent:
		return e.Quote.Symbol
	case feed.BookEvent:
		return e.Book.Symbol
	}
	return ""
}

// typeOf returns a human-readable type name for an event (used in logging).
func typeOf(ev feed.Event) string {
	switch ev.(type) {
	case feed.TicksEvent:
		return "TicksEvent"
	case feed.QuoteEvent:
		return "QuoteEvent"
	case feed.BookEvent:
		return "BookEvent"
	default:
		return "Unknown"
	}
}
```

---

## Layer 4: Per-symbol goroutines (the core change)

Each symbol gets its own goroutine that owns all state for that symbol. No locks needed — each goroutine is the sole writer to its own state.

**File:** `engine/internal/md/symbol_core.go` (new file, replaces current `core.go`)

```go
package md

import (
	"context"
	"sync/atomic"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// SymbolCore is the per-symbol state machine: one goroutine owns all state for
// one symbol. It consumes events from its inbox channel and emits updates on
// its updates channel. The core does no I/O and never reads the wall clock —
// event timestamps only, so replaying the same events reproduces the same state.
type SymbolCore struct {
	cfg      Config
	symbol   string
	inbox    <-chan inMsg       // provided by router
	updates  chan<- Update      // provided by aggregator
	marks    chan<- Mark        // provided by aggregator (can be merged with updates)
	dropped  atomic.Uint64

	// Domain state — touched ONLY inside Run's goroutine. No locks needed.
	quote *quoteEntry
	book  *bookEntry
	tape  *ring
	seq   int64     // per-symbol tick dedup high-water
	day   int64     // ET day of seq (sequences restart daily)
	bars  *symbolBars
	inds  *indicatorSet

	// seeding is true only while barEngine seed functions are looping over a
	// history batch. It suppresses per-bar fan-out so deep seeds emit one
	// BarSnapshot instead of thousands of per-bar updates.
	seeding bool
}

type quoteEntry struct {
	symbol string
	last   float64
	open   float64
	high   float64
	low    float64
	prev   float64
	volume int64
	tsMs   int64
}

type bookEntry struct {
	symbol string
	bids   []feed.BookLevel
	asks   []feed.BookLevel
	tsMs   int64
}

// NewSymbolCore creates a symbol core with its own inbox channel. The caller
// must pass the shared config and output channels; each symbol gets its own
// inbox (populated by the router).
func NewSymbolCore(symbol string, cfg Config, inbox <-chan inMsg, updates chan<- Update, marksOut chan<- Mark) *SymbolCore {
	return &SymbolCore{
		cfg:     cfg,
		symbol:  symbol,
		inbox:   inbox,
		updates: updates,
		marks:   marksOut,
		quote:   &quoteEntry{},
		book:    &bookEntry{},
		tape:    newRing(cfg.TapeRing),
		bars:    newSymbolBars(symbol, cfg.AnchorSecs),
		inds:    newIndicatorSetForSymbol(symbol),
	}
}

// Run is the single writer for this symbol. It returns when inbox is closed or
// ctx is done. The inbox is closed by the router when the system shuts down;
// ctx is used for external cancellation (e.g., symbol removal).
func (c *SymbolCore) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-c.inbox:
			if !ok {
				return
			}
			c.apply(m)
		}
	}
}

func (c *SymbolCore) emit(u Update) {
	select {
	case c.updates <- u:
	default:
		c.dropped.Add(1)
	}
}

func (c *SymbolCore) barOut(b Bar) {
	if c.seeding {
		return
	}
	c.emit(BarUpdate{Bar: b})
	c.inds.onBar(c, b)
}

func (c *SymbolCore) mark(m Mark) {
	select {
	case c.marks <- m:
	default:
	}
}

func (c *SymbolCore) emitBook(b feed.Book) {
	select {
	case c.updates <- BookUpdate{Book: b}:
	default:
		c.dropped.Add(1)
	}
}

func (c *SymbolCore) apply(m inMsg) {
	switch msg := m.(type) {
	case eventMsg:
		c.applyEvent(msg.ev)
	case seedDailyMsg:
		c.bars.seedDaily(c, msg.symbol, msg.bars)
	case seedHistory1mMsg:
		c.bars.seedHistory1m(c, msg.symbol, msg.bars)
	case seedSessionTicksMsg:
		c.seedSessionTicks(msg.symbol, msg.ticks)
	}
}

func (c *SymbolCore) applyEvent(ev feed.Event) {
	switch e := ev.(type) {
	case feed.TicksEvent:
		c.applyTicks(e)
	case feed.QuoteEvent:
		q := quoteEntry{
			symbol: e.Quote.Symbol, last: e.Quote.Last, open: e.Quote.Open,
			high: e.Quote.High, low: e.Quote.Low, prev: e.Quote.PrevClose,
			volume: e.Quote.Volume, tsMs: e.Quote.TsMs,
		}
		c.quote = &q
		c.emit(QuoteUpdate{Quote: feed.Quote{
			Symbol: q.symbol, Last: q.last, Open: q.open, High: q.high,
			Low: q.low, PrevClose: q.prev, Volume: q.volume, TsMs: q.tsMs,
		}})
	case feed.BookEvent:
		b := bookEntry{
			symbol: e.Book.Symbol, bids: e.Book.Bids, asks: e.Book.Asks,
			tsMs: e.Book.TsMs,
		}
		c.book = &b
		c.emit(BookUpdate{Book: feed.Book{Symbol: b.symbol, Bids: b.bids, Asks: b.asks}})
		c.emitBook(e.Book)
	case feed.ConnUpEvent:
		c.emit(ConnUpdate{Up: true})
	case feed.ConnDownEvent:
		c.emit(ConnUpdate{Up: false})
	case feed.ResyncedEvent:
		c.bars.markGaps()
		c.emit(ResyncedUpdate{})
	}
}

// dedupTicks applies the (day, seq) high-water dedup for this symbol.
func (c *SymbolCore) dedupTicks(ticks []feed.Tick) []feed.Tick {
	accepted := make([]feed.Tick, 0, len(ticks))
	for _, t := range ticks {
		day := session.DayMs(t.TsMs)
		if day != c.day {
			c.day = day
			c.seq = 0
		}
		if t.Seq != 0 && t.Seq <= c.seq {
			continue
		}
		c.seq = t.Seq
		accepted = append(accepted, t)
	}
	return accepted
}

// applyTicks dedups, appends to tape, drives tick-derived bars.
func (c *SymbolCore) applyTicks(e feed.TicksEvent) {
	if len(e.Ticks) == 0 {
		return
	}
	accepted := c.dedupTicks(e.Ticks)
	if len(accepted) == 0 {
		return
	}
	for _, t := range accepted {
		c.tape.append(t)
	}
	c.bars.applyTicks(c, accepted) // shadow → 10s + 1m (authoritative)
	c.emit(TapeUpdate{Symbol: c.symbol, Ticks: accepted})
	last := accepted[len(accepted)-1]
	c.mark(Mark{Symbol: last.Symbol, Price: last.Price, TsMs: last.TsMs})
}

// seedSessionTicks reconstructs tick-derived bars from persisted ticks.
func (c *SymbolCore) seedSessionTicks(symbol string, ticks []feed.Tick) {
	if len(ticks) == 0 {
		return
	}
	accepted := c.dedupTicks(ticks)
	if len(accepted) == 0 {
		return
	}
	c.seeding = true
	c.bars.applyTicks(c, accepted)
	c.seeding = false
	c.bars.emitTickSeedSnapshots(c, symbol)
	c.inds.reseedSymbol(c, symbol)
}
```

---

## Layer 5: Aggregator (single goroutine, collects all outputs)

Each symbol core emits updates on its own channel. The aggregator merges them into a single output channel for the uihub to consume.

**File:** `engine/internal/md/aggregator.go` (new file)

```go
package md

import (
	"context"
	"log/slog"

	"github.com/earlisreal/eTape/engine/internal/uihub"
)

// Aggregator merges update streams from all symbol cores into a single channel
// consumed by the uihub. It is a single goroutine that multiplexes N input
// channels onto one output channel using select. Dropped updates are counted
// per-symbol (not per-aggregator) to avoid masking individual symbol issues.
type Aggregator struct {
	hub         *uihub.Hub        // sends updates to WS clients
	markSink    chan<- Mark       // optional: marks go here for execution layer
	symbolChans map[string]chan Update  // symbol → its update channel
}

// NewAggregator creates an aggregator that fans all symbol outputs into the
// provided hub. Returns the mark sink channel (for execution layer) and a
// stop function.
func NewAggregator(hub *uihub.Hub) *Aggregator {
	return &Aggregator{
		hub:         hub,
		markSink:    make(chan Mark, 1024),
		symbolChans: make(map[string]chan Update),
	}
}

// Register adds a symbol's update channel to the aggregator. Called once per
// symbol during startup (after the router populates its inbox map).
func (a *Aggregator) Register(symbol string, updates chan Update) {
	a.symbolChans[symbol] = updates
}

// Run multiplexes all symbol update channels onto the hub channel. It blocks
// until ctx is done or all symbol channels close. Marks are forwarded to the
// mark sink if provided.
func (a *Aggregator) Run(ctx context.Context) {
	// Build a select case map for dynamic fan-in.
	cases := make(map[int]reflect.SelectCase, len(a.symbolChans)+2)
	cases[0] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(a.markSink)}
	idx := 1
	for sym, ch := range a.symbolChans {
		cases[idx] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch)}
		a.hub.Register(symbol, idx) // track which index is which symbol
		idx++
	}
	// Note: reflect.Select is slower than a hand-written select with known N,
	// but necessary because N changes at runtime (symbols join/leave). If N is
	// bounded and known at startup (e.g., max 200 symbols), a static select
	// array can be used instead for better performance.

	for {
		idx, val, ok := reflect.Select(cases)
		if !ok {
			continue // channel closed; skip
		}
		if idx == 0 {
			// Mark from markSink
			if m, ok2 := val.Interface().(Mark); ok2 {
				select {
				case a.markSink <- m:
				default:
				}
			}
		} else {
			// Update from symbol core
			if u, ok2 := val.Interface().(Update); ok2 {
				a.hub.Publish(u)
			}
		}
	}
}
```

**Note:** The aggregator uses `reflect.Select` for dynamic fan-in. If the max symbol count is known at startup (e.g., 200), a hand-written select with a fixed array is faster and preferred. See the "Alternative: Static Select" section below.

---

## Alternative: Static Select Aggregator (if N is bounded)

If the maximum number of symbols is known at boot (e.g., from config or scanner pool size), use a static select instead of reflect.Select:

```go
// maxSymbols is the configured maximum symbol count (from config).
const maxSymbols = 200

type Aggregator struct {
	hub      *uihub.Hub
	markSink chan Mark
	cores    [maxSymbols]*SymbolCore
	n        int // actual number of active cores
}

func (a *Aggregator) Run(ctx context.Context) {
	for {
		select {
		case m := <-a.markSink:
			// Forward mark to execution layer
		default:
		}
		// Static select over all cores — O(N) but N ≤ 200, compiled at build time.
		var zero Update
		for i := 0; i < a.n; i++ {
			select {
			case u, ok := <-a.cores[i].updates:
				if !ok {
					continue
				}
				a.hub.Publish(u)
			default:
			}
		}
		// Yield to prevent tight spinning when all channels are empty.
		select {
		case <-time.After(1 * time.Millisecond):
		case <-ctx.Done():
			return
		}
	}
}
```

**Tradeoff:** Static select is faster (no reflection, better CPU cache behavior) but requires knowing N at compile time or using a large fixed array. Dynamic reflect.Select handles runtime symbol additions/removals but has ~3x slower per-iteration overhead.

---

## Wiring in main.go

The entry point changes from creating one `md.Core` to creating N `SymbolCore` instances:

```go
// Before (single core):
core := md.New(md.Config{...})
go func() { _ = core.Run(ctx) }()

// After (per-symbol cores):
cores := make(map[string]*md.SymbolCore)
for _, sym := range symbols {
    inbox := make(chan md.inMsg, 256)
    updateCh := make(chan md.Update, 1024)
    markCh := make(chan md.Mark, 1024)

    cores[sym] = md.NewSymbolCore(sym, cfg, inbox, updateCh, markCh)
    go func() { _ = cores[sym].Run(ctx) }()

    router.Inboxes()[sym] = inbox
    aggregator.Register(sym, updateCh)
}

// Start decode layer:
frames := client.Frames()  // raw frames from reader
events, stopDecoders := opend.StartDecoders(ctx, frames, numWorkers)

// Start router:
router := md.NewRouter(routerInboxes, aggregatorOutput)
go router.Run(ctx, events)

// Start aggregator:
agg := md.NewAggregator(hub)
for sym, core := range cores {
    agg.Register(sym, core.updates)
}
go agg.Run(ctx)
```

---

## Data flow comparison

### Before (single goroutine)

```
TCP → reader → DecodePush() → pushCh → pump → eventsCh → core.inbox → apply() → updatesCh → uihub
                                                                 ↓
                                                              marksCh → exec
                                                                 ↓
                                                              bookOutCh → broker
```

**Latency path:** All events serialized. One event at a time through the entire pipeline.

### After (parallel)

```
TCP → reader → framesCh ──→ decoder[0] →\
                                         ├→ merge → router → inbox["AAPL"] → core["AAPL"].Run() → updates["AAPL"] → aggregator → uihub
TCP → reader → framesCh ──→ decoder[1] →/       │            inbox["MSFT"] → core["MSFT"].Run() → updates["MSFT"] ───────┘
                                         │
decode happens in parallel for different symbols. Routing and per-symbol processing also happen in parallel.
```

**Latency path:** Decode is parallel. Routing is O(1) hash lookup. Per-symbol processing is independent. Aggregation merges outputs.

---

## Hardware requirements

| Resource | Current (single goroutine) | Parallel + per-symbol | Delta |
|----------|---------------------------|----------------------|-------|
| **CPU** | 0.1-0.3% of one core (md core) | 0.2-0.5% across 2-4 cores (decoders + router + N symbol cores) | +0.3% total, spread across more cores |
| **RAM** | ~50-100 MB (ring buffers + maps) | ~150-250 MB (+ decoders, per-symbol inboxes, aggregator) | +100-150 MB |
| **Network** | 1-5 Mbps | Same | None |

A modern laptop or server handles this trivially. The RAM increase is the only real constraint — each symbol's inbox buffer (256 × 8 bytes = 2KB) × 100 symbols = 200KB, plus per-core state overhead.

---

## Implementation order

This design is intentionally scoped as a follow-up to Plan 7 (remove K_1M). Implement in this order:

1. **Plan 7 first:** Remove K_1M subscription. ~200 lines removed. Simple diff.
2. **Benchmark after Plan 7:** Measure tick latency before/after to confirm the bottleneck.
3. **If lag persists:** Implement parallel decode (Layers 1-2) — adds ~150 lines, no state changes.
4. **If more needed:** Implement per-symbol goroutines (Layers 3-5) — major refactor, ~400+ lines added.

**Don't skip ahead.** Per-symbol goroutines are a big rearchitect. Parallel decode alone might solve the problem with far less complexity.

---

## Risks and mitigations

| Risk | Impact | Mitigation |
|------|--------|-----------|
| **Event ordering within symbol lost** | High — dedup breaks, bars emit out of order | Router routes by symbol key; per-symbol inbox preserves order |
| **Aggregator becomes new bottleneck** | Medium — N→1 merge under burst | Use `reflect.Select` with buffered channels; aggregator is lightweight (just forwarding) |
| **Symbol lifecycle (add/remove) breaks select** | Medium — static select array must handle dynamic N | Use reflect.Select for correctness; optimize to static if N is bounded |
| **Debugging complexity increases** | Medium — non-deterministic ordering between symbols | Log symbol + event type on all path edges; add per-symbol metrics |
| **Backward compatibility with existing uihub** | Low — uihub interface unchanged (still receives Update stream) | Aggregator wraps uihub.Publish; no changes to WS message format |

---

## Acceptance criteria

1. `go build ./...` passes with no errors.
2. `go test -race ./...` passes — no data races across goroutines.
3. Tick burst benchmark: AAPL tick burst (500 ticks in 2s) shows <5ms tail latency for MSFT bars (was 10-50ms before).
4. No regression on single-symbol throughput vs single-goroutine baseline.
5. Memory usage at 100 symbols stays under 300 MB RSS.
