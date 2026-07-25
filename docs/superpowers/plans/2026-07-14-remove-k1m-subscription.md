# eTape Engine — Plan 7: Remove K_1M Subscription, Tick-Derived 1m Bars

> **For agentic workers:** Use `superpowers:subagent-driven-development` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the `K_1M` subscription from all demand profiles and live data paths, removing one push stream per symbol from OpenD. Derive 1m bars exclusively from TICKER data (the shadow aggregator promoted to authoritative source). Historical 1m bars continue via `Qot_RequestHistoryKL` (request-response, no subscription needed) for deep backfill.

**Why:** The K_1M push stream adds per-symbol bandwidth and decode overhead that competes with TICKER/QUOTE pushes for the single md-core goroutine inbox. Removing it reduces data flow without losing chart functionality — tick-derived 1m bars are functionally equivalent for live trading, and historical accuracy is preserved via the history API.

**Architecture change:**
```
Before: K_1M push → Bars1mEvent → apply1m() → authoritative 1m bars → cascade→5m/15m/30m/60m
        TICKER push → TicksEvent → applyTicks() → 10s bars + shadow 1m (validation only)

After:  TICKER push → TicksEvent → applyTicks() → 10s bars + shadow 1m (authoritative)
        Qot_RequestHistoryKL(Res1m) → seedHistory1m() → historical 1m bars (backfill only)
```

---

## Global Constraints

- **Module path:** `github.com/earlisreal/eTape/engine`; execute on current `main` branch.
- **Dependency rule:** unchanged — domain packages never import adapters.
- **Single-writer core:** the md core remains single-goroutine. Removing one event type reduces pressure; it does not change the concurrency model.
- **US-first scope:** unchanged.
- **Exchange timestamps are authoritative** for all bucketing. Unchanged.
- **Bar bucketing must match the UI test-mirror byte-for-byte.** Tick-derived 1m bars use the same `BucketStartMs` bucketing as K_1M bars — no change to the bucketing math, only the data source.
- **Determinism:** unchanged. The apply path still does no I/O and never reads the wall clock.
- **CI gates:** `go build ./...`, `go vet ./...`, `go test -race ./...` pass at every task boundary.

---

## Task 1: Remove `SubKL1m` from demand profiles

Remove `SubKL1m` from all places that declare what a symbol subscribes to. This is the first layer — it stops new symbols from requesting K_1M, but existing subscriptions persist until released (min-hold 60s).

**Files:**
- Modify: `engine/internal/feed/feed.go`
- Modify: `engine/internal/uihub/commands.go`

### Step 1: Remove `SubKL1m` from `WatchDemand()` and `FocusedDemand()`

`engine/internal/feed/feed.go`:

Remove `SubKL1m` from the subscription lists:

```go
const (
	SubQuote SubType = iota
	SubBook
	SubTicker
	// SubKL1m REMOVED — 1m bars now derived from TICKER data
)

func FocusedDemand(id, symbol string) Demand {
	return Demand{ID: id, Symbol: symbol, Focused: true,
		Subs: []SubType{SubQuote, SubBook, SubTicker}} // was + SubKL1m (4 → 3 slots)
}

func WatchDemand(id, symbol string) Demand {
	return Demand{ID: id, Symbol: symbol,
		Subs: []SubType{SubTicker}} // was [SubTicker, SubKL1m] (2 → 1 slot)
}
```

### Step 2: Remove `SubKL1m` from panel demand in `commands.go`

`engine/internal/uihub/commands.go` — the symbol-panel subscription block:

```go
// Around line 445, change:
subs := []feed.SubType{feed.SubQuote, feed.SubTicker} // was + feed.SubKL1m
```

### Step 3: Update tests

- `engine/internal/feed/feed_test.go`: update `WatchDemand` test expectations (2 subs → 1 sub, no `SubKL1m`)
- `engine/internal/uihub/commands_test.go`: update demand assertions

### Step 4: Run to verify pass

```bash
cd engine && go test -race ./internal/feed/ ./internal/uihub/ -v
```

### Step 5: Commit

```bash
git commit -m "refactor(feed): remove SubKL1m from demand profiles (1 slot per symbol)"
```

---

## Task 2: Remove `SubKL1m` subscription management

Remove K_1M from the subManager so it stops issuing `Qot_Sub` for K_1M and stops tracking active K_1M subscriptions.

**Files:**
- Modify: `engine/internal/feed/opend/subman.go`

### Step 1: Remove `SubKL1m` case from `pbSubType()`

```go
func pbSubType(s feed.SubType) int32 {
	switch s {
	case feed.SubQuote:
		return int32(qotcommon.SubType_SubType_Basic)
	case feed.SubBook:
		return int32(qotcommon.SubType_SubType_OrderBook)
	case feed.SubTicker:
		return int32(qotcommon.SubType_SubType_Ticker)
		// case feed.SubKL1m: REMOVED — no more K_1M subscriptions
	}
	return 0
}
```

### Step 2: Update tests

- `engine/internal/feed/opend/subman_test.go`: remove `SubKL1m` from test demand definitions (4 subs → 3 for focused, etc.)

### Step 3: Run to verify pass

```bash
cd engine && go test -race ./internal/feed/opend/ -v
```

### Step 4: Commit

```bash
git commit -m "refactor(feed/opend): remove SubKL1m from subscription manager"
```

---

## Task 3: Promote shadow aggregator to authoritative 1m source

The shadow `tickAgg` already builds 1m bars from ticks. Currently its finalized bars merge delta fields (BuyV/SellV/Ticks) into the K_1M authoritative bar. After this task, the shadow becomes the sole 1m source — no more `apply1m()`, no more K_1M auth bars.

**Files:**
- Modify: `engine/internal/md/bars.go`

### Step 1: Remove fields and methods that existed only for K_1M auth bar management

Remove from `symbolBars`:
```go
shadowFinals  map[int64]Bar  // shadow → auth delta merge source
compared      map[int64]bool // per-bucket validation guard
gapPending    bool           // gap flag for tick-derived bars (kept: still needed)
```

Remove from `barEngine`:
- `fillDelta()` — copies delta from shadow to auth bar
- `mergeShadowDelta()` — merges shadow delta into auth bar
- `validate()` — compares K_1M vs shadow 1m

### Step 2: Rewrite `applyTicks()` to emit shadow 1m bars as authoritative

Current `applyTicks()` drives both the 10s series (`agg10`) and the shadow (validation only). After this change, shadow bars are emitted directly into `sb.series[session.TF1m]` and cascade to higher timeframes:

```go
func (e *barEngine) applyTicks(c *Core, ticks []feed.Tick) {
	if len(ticks) == 0 {
		return
	}
	sb := e.sym(ticks[0].Symbol)
	for _, t := range ticks {
		if day := session.DayMs(t.TsMs); day != sb.curDay {
			sb.curDay = day
			// Day boundary: clear open buckets, no shadowFinals to clear
		}
		// 10s bars — unchanged
		for _, b := range sb.agg10.addTick(t, sb.gapPending) {
			if b.Gap && b.InProgress {
				sb.gapPending = false
			}
			sb.series[session.TF10s].upsert(b)
			c.barOut(b)
		}
		// Shadow 1m bars — now authoritative
		for _, b := range sb.shadow.addTick(t, false) {
			if b.InProgress {
				// In-progress: update the live forming bar
				e.updateLive1m(c, sb, b)
			} else {
				// Finalized: emit directly, cascade to higher TFs
				e.finalize1m(c, sb, b)
			}
		}
	}
}
```

### Step 3: Implement `updateLive1m()` and `finalize1m()`

```go
// updateLive1m updates or inserts the in-progress 1m bar from shadow data.
func (e *barEngine) updateLive1m(c *Core, sb *symbolBars, shadow Bar) {
	oneM := sb.series[session.TF1m]
	existing := oneM.get(shadow.BucketMs)
	if existing != nil && !existing.InProgress {
		return // finalized bar: don't overwrite with in-progress data
	}
	nb := Bar{
		Symbol: shadow.Symbol, TF: session.TF1m, BucketMs: shadow.BucketMs,
		O: shadow.O, H: shadow.H, L: shadow.L, C: shadow.C, V: shadow.V,
		BuyV: shadow.BuyV, SellV: shadow.SellV, Ticks: shadow.Ticks,
		InProgress: true, Gap: shadow.Gap,
	}
	if existing != nil && existing.InProgress {
		nb.Gap = existing.Gap // preserve gap flag from the original forming bar
		oneM.upsert(nb)
		c.barOut(nb)
	} else {
		oneM.upsert(nb)
		c.barOut(nb)
	}
	e.cascade(c, sb, nb.BucketMs)
}

// finalize1m emits a completed 1m bar and cascades to higher timeframes.
func (e *barEngine) finalize1m(c *Core, sb *symbolBars, shadow Bar) {
	oneM := sb.series[session.TF1m]
	nb := Bar{
		Symbol: shadow.Symbol, TF: session.TF1m, BucketMs: shadow.BucketMs,
		O: shadow.O, H: shadow.H, L: shadow.L, C: shadow.C, V: shadow.V,
		BuyV: shadow.BuyV, SellV: shadow.SellV, Ticks: shadow.Ticks,
		InProgress: false, Gap: shadow.Gap,
	}
	if oneM.upsert(nb) {
		c.barOut(nb)
		e.cascade(c, sb, nb.BucketMs)
		e.deriveDaily(c, sb, nb.BucketMs)
	}
}
```

### Step 4: Remove `apply1m()` entirely

The entire `apply1m()`, `seedHistory1m()`, `seedOlder1m()` methods that operated on K_1M auth bars are removed. The shadow aggregator replaces their live path; historical seeding is handled separately (Task 5).

### Step 5: Update `cascade()` and `deriveDaily()` — no changes needed

Both functions read from `sb.series[session.TF1m]` and cascade up. Since the shadow now feeds that series directly, the cascade logic is unchanged.

### Step 6: Update tests

- `engine/internal/md/bars_test.go`: remove K_1M auth bar validation tests; update tick-driven test assertions to expect authoritative 1m bars from ticks
- Remove validation threshold constants (`mismatchPriceTol`, `mismatchVolPct`, etc.) — no longer used

### Step 7: Run to verify pass

```bash
cd engine && go test -race ./internal/md/ -v
```

### Step 8: Commit

```bash
git commit -m "refactor(md): promote shadow aggregator to authoritative 1m source; remove apply1m/validate"
```

---

## Task 4: Remove K_1M decode path and `Bars1mEvent`

Remove the OpenD push decode for `ProtoQotUpdateKL` and the `Bars1mEvent` type. No more K_1M events flow through the system.

**Files:**
- Modify: `engine/internal/feed/opend/decode.go`
- Modify: `engine/internal/feed/events.go`
- Modify: `engine/internal/md/core.go`

### Step 1: Remove `ProtoQotUpdateKL` case from `DecodePush()`

`engine/internal/feed/opend/decode.go`:

```go
// REMOVE the entire case ProtoQotUpdateKL block in DecodePush()
// K_1M pushes are no longer decoded — we derive 1m from ticks instead.
```

### Step 2: Remove `Bars1mEvent` from events.go

`engine/internal/feed/events.go`:

```go
// REMOVE: Bars1mEvent struct and its isEvent() method
```

Remove from the exhaustiveness test in `feed_test.go`.

### Step 3: Remove `Bars1mEvent` dispatch from core.go

`engine/internal/md/core.go`:

```go
func (c *Core) applyEvent(ev feed.Event) {
	switch e := ev.(type) {
	// ... other cases ...
	// REMOVE: case feed.Bars1mEvent: c.bars.apply1m(c, e.Bars)
	}
}
```

### Step 4: Update tests

- `engine/internal/feed/opend/decode_test.go`: remove `TestDecodePushKLFiltersNon1m` and related K_1M decode tests
- `engine/internal/feed/feed_test.go`: remove `Bars1mEvent{}` from exhaustiveness check
- `engine/internal/md/determinism_test.go`: remove Bars1mEvent usage in test vectors

### Step 5: Run to verify pass

```bash
cd engine && go test -race ./internal/feed/ ./internal/feed/opend/ ./internal/md/ -v
```

### Step 6: Commit

```bash
git commit -m "refactor(feed,md): remove K_1M decode path and Bars1mEvent"
```

---

## Task 5: Remove K_1M seed block from OpenDFeed; clean up unused methods

Remove the K_1M cache seeding in `seed()` and remove `Tail1m()` / `CachedBars1m()` from the `OpenDFeed` API.

**Files:**
- Modify: `engine/internal/feed/opend/opendfeed.go`
- Modify: `engine/internal/feed/feed.go` (Feed interface)

### Step 1: Remove K_1M seed block from `seed()`

```go
func (f *OpenDFeed) seed(ctx context.Context, symbol string, subs []feed.SubType) {
	// REMOVE the entire if has(feed.SubKL1m) block
	// No more cache seeding for 1m bars — historical backfill handled by Task 6
}
```

### Step 2: Remove `Tail1m()` and `CachedBars1m()` from OpenDFeed

```go
// REMOVE these methods from *OpenDFeed:
func (f *OpenDFeed) Tail1m(ctx context.Context, symbol string) ([]feed.Bar, error) { ... }
func (f *OpenDFeed) CachedBars1m(ctx context.Context, symbol string, n int) ([]feed.Bar, error) { ... }
```

### Step 3: Remove `CachedBars1m` from Feed interface

`engine/internal/feed/feed.go`:

```go
type Feed interface {
	Events() <-chan Event
	Ensure(d Demand)
	Release(id string)
	HistoryBars(ctx context.Context, symbol string, res Resolution, from, to time.Time) ([]Bar, error)
	RecentTicks(ctx context.Context, symbol string, n int) ([]Tick, error)
	// CachedBars1m REMOVED — no longer needed without K_1M subscription
	// Tail1m REMOVED — no longer needed without K_1M subscription
	BookSnapshot(ctx context.Context, symbol string) (Book, error)
	QuoteSnapshot(ctx context.Context, symbol string) (Quote, error)
}
```

### Step 4: Update tests

- `engine/internal/feed/opend/opendfeed_test.go`: remove test cases that use SubKL1m or Tail1m/CachedBars1m

### Step 5: Run to verify pass

```bash
cd engine && go test -race ./internal/feed/ ./internal/feed/opend/ -v
```

### Step 6: Commit

```bash
git commit -m "refactor(feed/opend): remove K_1M seed block, Tail1m, CachedBars1m"
```

---

## Task 6: Update backfill orchestrator — remove tail step, keep deep history

The backfill no longer has a quota-free tail step (it relied on the K_1M cache). Deep history via `Qot_RequestHistoryKL` (Res1m) is unchanged — it's request-response, not subscription-based.

**Files:**
- Modify: `engine/internal/backfill/backfill.go`
- Modify: `engine/internal/store/bars.go` (keep the table; warm-start from archive still works)

### Step 1: Remove `TailFetcher` interface and `tail1m()` method

```go
// REMOVE: TailFetcher interface
// REMOVE: tail1m() method on Orchestrator
```

### Step 2: Update `Backfill()` to skip the tail step

```go
func (o *Orchestrator) Backfill(ctx context.Context, symbol string) error {
	now := o.clk.Now()
	from1m := intradayFrom(now, o.cfg.IntradayDays)
	o.warmStart(ctx, symbol, from1m, now)
	// tail1m step REMOVED — no K_1M cache to read
	// warmStart already seeds from archive (today's bars from prior session)
	o.fill1m(ctx, symbol, from1m, now, 0, false) // no tailOldestMs, no tailOK
	err := o.fillDaily(ctx, symbol, o.dailyFrom(now), now)
	o.noteBackfilled(symbol, from1m)
	return err
}
```

### Step 3: Update `fill1m()` to accept optional tail cutoff

The `tailOldestMs` parameter becomes unused (or remove it entirely):

```go
func (o *Orchestrator) fill1m(ctx context.Context, symbol string, from, to time.Time, _ int64, _ bool) {
	// trimOlderThan call removed — no tail to trim against
	bars, served, err := walkChain(ctx, symbol, from, to, o.intraday, intraday1m)
	if len(bars) == 0 {
		if err != nil {
			slog.Warn("backfill: deep 1m unavailable", "symbol", symbol, "err", err)
		}
		return
	}
	o.archive1m(bars) // still archive deep history bars
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	slog.Info("backfill: deep 1m served", "symbol", symbol, "provider", served, "bars", len(bars))
}
```

### Step 4: Update `Seeder` interface — keep `SeedHistory1m`, remove `SeedOlder1m`

`seedOlder1m` was only used for K_1M tail deepening. The archive-first path in `LoadOlder` still works via `ReadBars1m` (SQLite). Remove the direct seeding method:

```go
type Seeder interface {
	SeedDaily(symbol string, bars []feed.Bar)
	SeedHistory1m(symbol string, bars []feed.Bar) // kept: deep history from Qot_RequestHistoryKL
	// SeedOlder1m REMOVED — no longer needed without K_1M tail
	SeedSessionTicks(symbol string, ticks []feed.Tick)
}
```

### Step 5: Update `LoadOlder()` to remove `seedOlderUnlessCanceled` call

```go
func (o *Orchestrator) loadOlder(ctx context.Context, symbol string) (olderResult, error) {
	// ... archive-first path unchanged ...
	// Provider chain: still calls walkChain → archive1m → SeedHistory1m
	o.archive1m(bars)
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) }) // was SeedOlder1m
	// ...
}
```

### Step 6: Update tests

- `engine/internal/backfill/backfill_test.go`: update fakeTail/fakeSeeder to match new interfaces; remove tail-related test cases

### Step 7: Run to verify pass

```bash
cd engine && go test -race ./internal/backfill/ -v
```

### Step 8: Commit

```bash
git commit -m "refactor(backfill): remove tail step, TailFetcher, SeedOlder1m; deep history via Qot_RequestHistoryKL unchanged"
```

---

## Task 7: Update backfill.go decoder — remove unused K_1M helper

The `cachedBars1m()` method in `backfill.go` (the one that calls `qotgetkl.Request`) is now dead code since no caller uses it. Remove the method and its proto import.

**Files:**
- Modify: `engine/internal/feed/opend/backfill.go`

### Step 1: Remove `cachedBars1m()` method

```go
// REMOVE: cachedBars1m() — called by OpenDFeed.CachedBars1m (also removed)
// The proto import for qotgetkl may become unused; check before removing.
```

### Step 2: Clean up unused imports

If `qotgetkl` package is no longer imported elsewhere, remove it from the import block.

### Step 3: Run to verify pass

```bash
cd engine && go build ./...
```

### Step 4: Commit

```bash
git commit -m "refactor(feed/opend): remove cachedBars1m() and qotgetkl import"
```

---

## Task 8: Update synth/demo seeder — adapt to new bar source

The synthetic demo seeder (`engine/internal/synth/seeder.go`) archives bars for replay. It currently calls `ArchiveBar1m` on K_1M-derived bars. After this change, it should archive tick-derived 1m bars instead.

**Files:**
- Modify: `engine/internal/synth/seeder.go`

### Step 1: Update the seeder to not archive bars from K_1M seed data

The synthetic seeder generates simulated ticks. The 1m bars it produces are already tick-derived (from the same shadow aggregator). Remove any direct `ArchiveBar1m` calls that bypass the md core's bar emission path:

```go
// In Seeder.seedBars or equivalent: remove direct ArchiveBar1m() calls.
// Bars flow through md.Core → uihub → archive at the backfill layer, not from synth.
```

### Step 2: Run to verify pass

```bash
cd engine && go test -race ./internal/synth/ -v
```

### Step 3: Commit

```bash
git commit -m "refactor(synth): remove direct ArchiveBar1m calls from synthetic seeder"
```

---

## Task 9: Final cleanup — remove `bars_1m` table? (optional)

**Decision point:** The `bars_1m` SQLite table is still used for warm-start (reading today's bars from prior session). Warm-start works because `seedSessionTicks` reconstructs 10s + shadow 1m from the tick journal, and `ReadBars1m` reads pre-archived historical bars. 

**Recommendation:** Keep the table. It serves as a cache for deep-history bars fetched via `Qot_RequestHistoryKL`, surviving restarts without re-fetching. The warm-start path in `seedSessionTicks` reconstructs 10s bars from ticks but doesn't pre-build 1m history — the archive fills that gap.

If you want to remove it entirely (trade warm-start depth for faster boot), do so in a follow-up task after verifying warm-start works via tick reconstruction alone.

---

## Summary of changes

| Component | Before | After |
|-----------|--------|-------|
| **Subscription per symbol** | 1 slot (TICKER + K_1M) | 1 slot (TICKER only) |
| **1m bar source (live)** | K_1M push → Bars1mEvent → apply1m() | TICKER → shadow tickAgg → direct emit |
| **1m bar source (historical)** | K_1M cache seed + history API | History API only (Qot_RequestHistoryKL) |
| **Shadow aggregator** | Validation + delta merge only | Authoritative 1m source |
| **K_1M decode path** | ProtoQotUpdateKL → Bars1mEvent | Removed |
| **Tail step in backfill** | Qot_GetKL cache read (~9ms, quota-free) | Removed (deep history via Qot_RequestHistoryKL) |
| **Validation** | K_1M vs shadow comparison | Removed (shadow is now the source) |
| **bars_1m SQLite table** | Archived from K_1M bars | Archived from deep-history bars (unchanged usage) |

**Net lines removed:** ~200 (validation, merge, K_1M-specific event handling)
**Net lines added:** ~50 (shadow promotion helpers)
**Net effect:** cleaner code, less data flow, one fewer subscription per symbol.
