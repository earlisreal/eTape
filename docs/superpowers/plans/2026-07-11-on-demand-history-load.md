# On-demand deeper history loading — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When the user pans a chart near the earliest loaded bar, fetch the next chunk of older history and paint it in place — depth grows only for symbols the user explores, survives restarts via the SQLite archive, and every connected client updates.

**Architecture:** Engine-owned deepening through the existing seed path. A new `LoadOlderBars` WS command (deferred ack) drives `backfill.Orchestrator.LoadOlder`/`LoadOlderDaily`, which serves archive-first then walks the existing provider chains. Fetched bars enter `md.Core` via a new `SeedOlder1m` path (intraday) or the unchanged `SeedDaily` (pre-2016 daily). Intraday deepening emits a new constant-cost `md.BarPrepend` delta; D/W/M keep full `BarSnapshot`. The UI prepends batch deltas into `BarStore` and preserves the viewport by time-range around the `setData` rebuild.

**Tech Stack:** Go engine (`engine/`), TypeScript + React + Vite + TradingView Lightweight Charts UI (`ui/`), WebSocket + JSON transport with tygo-generated TS types, SQLite feed archive.

**Spec:** `docs/superpowers/specs/2026-07-11-on-demand-history-design.md` (Approved 2026-07-11, committed 0dfa5ee). Builds on `docs/superpowers/specs/2026-07-10-history-bars-providers-design.md`.

## Global Constraints

- **No new config keys.** `backfill.intraday_days` (20 trading days) doubles as the intraday chunk size; the 2016-01-01 floor reuses the existing `dailyFloor` (`backfill.go:172`).
- **Reuse `feed.Bar` across store↔backfill↔md.Core.** md converts to `md.Bar` internally at seed time. Turnover/BuyV/SellV/Ticks are not carried by history bars; deep bars have `InProgress=false` and zero buy/sell delta (existing semantics).
- **Deferred ack rides the conn outbox.** There is one shared WS connection for the whole UI (`App.tsx`), so a `LoadOlderBars` handler MUST NOT block the per-connection reader goroutine — it spawns the fetch and acks later.
- **Any `backfillWG.Add(1)` reachable from a command must originate on `Hub.Run`'s goroutine, never a per-connection goroutine** — `main.go`'s shutdown order (`<-hubDone → scanWG.Wait() → backfillWG.Wait()`) only holds if every `Add` happens-before that ordering, which is only guaranteed on Run's single goroutine (the existing `triggerBackfill` relies on exactly this). This is why Task 7 routes `LoadOlder` through a new `Hub` per-verb channel (mirroring `EnsureDemand`) instead of calling the orchestrator directly from `commands.handle`.
- **`AckStatus` has only `"accepted"`/`"blocked"`** (`wsmsg.go:113-120`). Do NOT add an `"error"` status: map the spec's "error" ack to `"blocked"` with a `Reason`. Success/exhausted are both `"accepted"` and distinguished by the ack `Value` (`{added, exhausted}`).
- **`md.bars` is `classMDKeep`** — per-bar deltas get keep-latest-coalesced (`coalesce.go:32-45`). A batch prepend frame MUST bypass coalescing (both ingest-side `stageMD` and per-client `outboundCoalesceKey`) or it will be silently dropped.
- **tygo contract:** new wire DTOs go in `engine/internal/uihub/wsmsg/payloads.go` (tygo-generated). `wsmsg.go` frames are hand-declared. Regenerate with `make gen-ts` in `engine/`; CI enforces `make gen-ts-check`.
- **Working tree is clean** as of planning (no concurrent `ChartPanel.tsx`/`chartTheme.ts` edits) — re-verify with `git status` before starting.

---

## File Structure

**Engine (new behavior added to existing files):**
- `engine/internal/md/update.go` — add `BarPrepend` Update type.
- `engine/internal/md/core.go` — add `seedOlder1mMsg` + `SeedOlder1m` + apply dispatch case.
- `engine/internal/md/bars.go` — add `barEngine.seedOlder1m` + `seedOlder1mTFs`.
- `engine/internal/uihub/mirror.go` — add `staged.Batch` field + `md.BarPrepend` case (front-insert cache).
- `engine/internal/uihub/hub.go` — `stageMD` batch branch (bypass keep-latest).
- `engine/internal/uihub/coalesce.go` — `outboundCoalesceKey` returns `""` for batch frames.
- `engine/internal/uihub/wsmsg/payloads.go` — `LoadOlderBarsArgs`, `LoadOlderResult`.
- `engine/internal/uihub/conn.go` — deferred-ack plumbing on `commandHandler.handle` + dispatch.
- `engine/internal/uihub/commands.go` — extend `demandCtl` with `LoadOlder`; `LoadOlderBars` command case.
- `engine/internal/uihub/commands_test.go` — 28 existing `handle(...)` call sites updated for the new 5-arg/2-return signature (Task 6); `spyDemandCtl` gains `LoadOlder` (Task 7).
- `engine/internal/uihub/hub.go` — new `loadOlderCh`/`loadOlderSlot`, `SetLoadOlder`/`loadOlderFn()`, public `LoadOlder`, `handleLoadOlder`, `Run`-loop case (Task 7) — **in addition to** the Task 3 `stageMD` batch branch.
- `engine/internal/backfill/backfill.go` — `LoadOlder`/`LoadOlderDaily` + watermark + singleflight; `noteBackfilled` in `Backfill`; extend `Seeder` with `SeedOlder1m`.
- `engine/internal/backfill/backfill_test.go` — extend the existing `fakeSeeder` with `SeedOlder1m` (required for the package to keep compiling once `Seeder` grows).
- `engine/cmd/etape/main.go` — hoist `orch` to function scope; fallback chain-less orchestrator for replay/disabled; `loadOlderFn` closure (backfillWG-tracked); `hub.SetLoadOlder(loadOlderFn)`.

**UI (new behavior + one new file):**
- `ui/src/gen/wsmsg.ts` — regenerated (Task 4).
- `ui/src/data/BarStore.ts` — batch prepend branch in `apply`.
- `ui/src/render/chart/ChartApiFacade.ts` — visible time-range get/set methods.
- `ui/src/chrome/panels/ChartPanel.tsx` — implement facade methods in `makeFacade`; wire left-edge trigger + reset.
- `ui/src/render/chart/ChartController.ts` — viewport preservation in `setAllBars`; export `LEFT_PAD_BARS`.
- `ui/src/render/chart/olderHistory.ts` — **new** — testable trigger + guard state machine.
- `ui/src/render/chart/ChartController.test.ts` — extend the facade fake.
- E2E: `ui/e2e/…` — replay-mode archive-backed `LoadOlderBars`.

---

## Task 1: `BarPrepend` md Update type

**Files:**
- Modify: `engine/internal/md/update.go` (add type near `BarSnapshot`, `update.go:27-38`; register `isUpdate` near `update.go:62-70`)
- Test: `engine/internal/md/update_test.go` (create or append)

**Interfaces:**
- Produces: `md.BarPrepend{ Symbol string; TF session.Timeframe; Bars []Bar }` implementing `md.Update`.

- [ ] **Step 1: Write the failing test**

```go
// engine/internal/md/update_test.go
package md

import "testing"

func TestBarPrependIsUpdate(t *testing.T) {
	var u Update = BarPrepend{Symbol: "US.AAPL", TF: "1m", Bars: []Bar{{BucketMs: 1}}}
	if _, ok := u.(BarPrepend); !ok {
		t.Fatalf("BarPrepend does not satisfy Update")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/md/ -run TestBarPrependIsUpdate -v`
Expected: FAIL — `undefined: BarPrepend`

- [ ] **Step 3: Add the type**

```go
// update.go — add after BarSnapshot (update.go:27-38)

// BarPrepend carries ONLY the newly-added older bars for one (symbol,
// timeframe). SeedOlder1m emits it instead of a full BarSnapshot so the wire
// cost per pan chunk stays constant regardless of accumulated depth. Bars are
// ascending and strictly older than the client's current earliest bar.
type BarPrepend struct {
	Symbol string
	TF     session.Timeframe
	Bars   []Bar
}
```

```go
// update.go — add alongside the other isUpdate() methods (update.go:62-70)
func (BarPrepend) isUpdate() {}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/md/ -run TestBarPrependIsUpdate -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add engine/internal/md/update.go engine/internal/md/update_test.go
git commit -m "feat(md): add BarPrepend update type for deep-history deltas"
```

---

## Task 2: `SeedOlder1m` path in md.Core

**Files:**
- Modify: `engine/internal/md/core.go` (inbox union `core.go:22-48`; public seed `core.go:115-129`; `apply` dispatch `core.go:179-194`)
- Modify: `engine/internal/md/bars.go` (add `seedOlder1m` + `seedOlder1mTFs`, model on `seedHistory1m` `bars.go:275-304` and `emitSeedSnapshots` `bars.go:330-338`)
- Test: `engine/internal/md/bars_test.go` (append)

**Interfaces:**
- Consumes: `md.Bar` (`update.go:88-99`), `md.BarPrepend` (Task 1), `session.TF1m…TF60m`.
- Produces: `func (c *Core) SeedOlder1m(symbol string, bars []feed.Bar)` — enqueues older 1m bars; cascades to 5m/15m/30m/60m; emits one `BarPrepend` per intraday TF containing only the newly-added older bars; runs one `reseedSymbol`. Daily/week/month are NOT emitted (older 1m falls inside official-daily-covered days, so `deriveDaily` no-ops on `dailyOfficial` buckets).

- [ ] **Step 1: Write the failing test**

```go
// bars_test.go
package md

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// drainUpdates collects everything currently buffered on c.updates.
func drainUpdates(c *Core) []Update {
	var out []Update
	for {
		select {
		case u := <-c.updates:
			out = append(out, u)
		default:
			return out
		}
	}
}

func TestSeedOlder1mEmitsPrependAndCascades(t *testing.T) {
	c := New(Config{}) // confirmed real signature: func New(cfg Config) *Core (core.go:78)
	sym := "US.AAPL"

	// Seed an initial recent 1m run so an "earliest" exists (two 1m bars in one 5m bucket).
	base := session.BucketStartMsAnchored(1_700_000_000_000, session.TF5m, c.bars.anchorSecs)
	c.bars.seedHistory1m(c, sym, []feed.Bar{
		{Symbol: sym, BucketMs: base, O: 10, H: 11, L: 9, C: 10, Volume: 100},
		{Symbol: sym, BucketMs: base + 60_000, O: 10, H: 12, L: 10, C: 11, Volume: 120},
	})
	_ = drainUpdates(c)

	// Now seed a strictly-older 1m chunk (a full earlier 5m bucket).
	older := base - 300_000 // 5 minutes earlier, aligned
	c.bars.seedOlder1m(c, sym, []feed.Bar{
		{Symbol: sym, BucketMs: older, O: 5, H: 6, L: 4, C: 5, Volume: 50},
		{Symbol: sym, BucketMs: older + 60_000, O: 5, H: 7, L: 5, C: 6, Volume: 60},
	})
	ups := drainUpdates(c)

	// Expect BarPrepend for TF1m and TF5m carrying only the new older bars.
	prepends := map[session.Timeframe][]Bar{}
	for _, u := range ups {
		if p, ok := u.(BarPrepend); ok {
			prepends[p.TF] = p.Bars
		}
		if _, ok := u.(BarSnapshot); ok {
			t.Fatalf("SeedOlder1m must not emit BarSnapshot for intraday TFs")
		}
	}
	if len(prepends[session.TF1m]) != 2 {
		t.Fatalf("TF1m prepend: want 2 bars, got %d", len(prepends[session.TF1m]))
	}
	if len(prepends[session.TF5m]) != 1 {
		t.Fatalf("TF5m prepend: want 1 new bucket, got %d", len(prepends[session.TF5m]))
	}
	// The prepended TF5m bar must be strictly older than the pre-existing one.
	if prepends[session.TF5m][0].BucketMs >= base {
		t.Fatalf("prepended 5m bucket not older than existing earliest")
	}
	// The pre-existing earliest 5m bucket must NOT be re-emitted (no boundary mutation).
	for _, b := range prepends[session.TF5m] {
		if b.BucketMs == base {
			t.Fatalf("boundary 5m bar was re-emitted; chunk boundary should not mutate it")
		}
	}
}
```

Note: match `New(...)` and any helper (`session.BucketStartMsAnchored`, `anchorSecs`) to the real signatures — the recon confirms `barEngine.anchorSecs` and `session.BucketStartMsAnchored` exist (`bars.go:340-357`). If `New` needs more args, mirror an existing md test's construction.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/md/ -run TestSeedOlder1mEmitsPrepend -v`
Expected: FAIL — `c.bars.seedOlder1m undefined`

- [ ] **Step 3: Implement `SeedOlder1m`**

Add to `core.go` (inbox union, `core.go:22-48`). The inbox is `chan inMsg` where `inMsg interface{ isInMsg() }`, so the new type MUST register `isInMsg` (every message type does, `core.go:43-48`) or `SeedOlder1m` won't compile:

```go
type seedOlder1mMsg struct {
	symbol string
	bars   []feed.Bar
}

func (seedOlder1mMsg) isInMsg() {}
```

Public enqueue (`core.go:115-129`):

```go
func (c *Core) SeedOlder1m(symbol string, bars []feed.Bar) {
	c.inbox <- seedOlder1mMsg{symbol: symbol, bars: bars}
}
```

Dispatch in `apply` (`core.go:179-194`, alongside `seedHistory1mMsg`). The switch binds the value to `msg` (`switch msg := m.(type)`), so use `msg.*`, NOT `m.*`:

```go
	case seedOlder1mMsg:
		c.bars.seedOlder1m(c, msg.symbol, msg.bars)
```

Add to `bars.go` (near `seedHist1mTFs`, `bars.go:260-263`):

```go
// seedOlder1mTFs are the intraday timeframes deepened by an older-1m chunk.
// Daily/week/month are excluded: older 1m always lands inside the official-
// daily-covered range (>=2016), where deriveDaily no-ops on dailyOfficial
// buckets, so no D/W/M bar mutates.
var seedOlder1mTFs = []session.Timeframe{
	session.TF1m, session.TF5m, session.TF15m, session.TF30m, session.TF60m,
}
```

Add `seedOlder1m` (model on `seedHistory1m`, `bars.go:275-304`):

```go
// seedOlder1m upserts a strictly-older 1m chunk, cascades to 5m/15m/30m/60m,
// and emits one BarPrepend per intraday TF carrying ONLY the newly-added older
// bars (constant per-chunk wire cost). Per-bar emission is suppressed for the
// whole batch (c.seeding); indicators reseed once at the end.
//
// "Newly added" = bars older than each TF's previous earliest bucket. Chunks
// are whole trading days and intraday buckets are session-anchored (never span
// days), so the previously-earliest 5m-60m bucket cannot be mutated by an older
// chunk — the strict "< prevOldest" filter captures exactly the new bars.
func (e *barEngine) seedOlder1m(c *Core, symbol string, bars []feed.Bar) {
	if len(bars) == 0 {
		return
	}
	sb := e.sym(symbol)
	oneM := sb.series[session.TF1m]

	// Capture each intraday TF's earliest bucket before seeding.
	prevOldest := make(map[session.Timeframe]int64, len(seedOlder1mTFs))
	for _, tf := range seedOlder1mTFs {
		s := sb.series[tf]
		if len(s.bars) > 0 {
			prevOldest[tf] = s.bars[0].BucketMs
		} else {
			prevOldest[tf] = math.MaxInt64
		}
	}

	forming := int64(-1)
	if lb := oneM.last(); lb != nil && lb.InProgress {
		forming = lb.BucketMs
	}
	c.seeding = true
	for _, raw := range bars {
		if raw.BucketMs == forming {
			continue
		}
		nb := Bar{
			Symbol: symbol, TF: session.TF1m, BucketMs: raw.BucketMs,
			O: raw.O, H: raw.H, L: raw.L, C: raw.C, V: raw.Volume,
		}
		e.fillDelta(sb, &nb)
		if oneM.upsert(nb) {
			c.barOut(nb) // suppressed while seeding
			e.cascade(c, sb, nb.BucketMs)
			e.deriveDaily(c, sb, nb.BucketMs) // no-ops on dailyOfficial buckets
		}
	}
	c.seeding = false

	// Emit the new-older prefix per intraday TF (series ascending → prefix < prevOldest).
	for _, tf := range seedOlder1mTFs {
		s := sb.series[tf]
		var newer []Bar
		for _, b := range s.bars {
			if b.BucketMs >= prevOldest[tf] {
				break
			}
			newer = append(newer, b)
		}
		if len(newer) > 0 {
			c.emit(BarPrepend{Symbol: symbol, TF: tf, Bars: newer})
		}
	}
	c.inds.reseedSymbol(c, symbol)
}
```

`bars.go` already imports `"math"` — no import change needed.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/md/ -run TestSeedOlder1mEmitsPrepend -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add engine/internal/md/core.go engine/internal/md/bars.go engine/internal/md/bars_test.go
git commit -m "feat(md): add SeedOlder1m path emitting BarPrepend deltas"
```

---

## Task 3: Mirror + hub batch-prepend handling

**Files:**
- Modify: `engine/internal/uihub/mirror.go` (`staged` `mirror.go:16-21`; `applyMD` `mirror.go:124-144`; reuse `barKey` `mirror.go:92`, `mapBar` `map.go:178-183`)
- Modify: `engine/internal/uihub/hub.go` (`stageMD` `hub.go:678-703`)
- Modify: `engine/internal/uihub/coalesce.go` (`outboundCoalesceKey` `coalesce.go:57-98`)
- Test: `engine/internal/uihub/mirror_test.go` (append)

**Interfaces:**
- Consumes: `md.BarPrepend` (Task 1).
- Produces: a `staged{Topic: TopicBars, Payload: []wsmsg.Bar, Batch: true}` frame that broadcasts immediately on the lossless lane as a **delta** (not snapshot), and front-inserts into the mirror's per-key bar cache so snapshot-on-subscribe stays correct.

- [ ] **Step 1: Write the failing test**

```go
// mirror_test.go
func TestMirrorBarPrependFrontInsertsAndStagesBatch(t *testing.T) {
	m := testMirror() // reuse the existing helper in mirror_test.go (newMirror takes 7 args)
	// Seed a snapshot so the cache has an existing (newer) run.
	m.applyMD(md.BarSnapshot{Symbol: "US.AAPL", TF: "1m", Bars: []md.Bar{
		{Symbol: "US.AAPL", TF: "1m", BucketMs: 2_000_000},
		{Symbol: "US.AAPL", TF: "1m", BucketMs: 2_060_000},
	}})

	out := m.applyMD(md.BarPrepend{Symbol: "US.AAPL", TF: "1m", Bars: []md.Bar{
		{Symbol: "US.AAPL", TF: "1m", BucketMs: 1_000_000},
		{Symbol: "US.AAPL", TF: "1m", BucketMs: 1_060_000},
	}})

	if len(out) != 1 || !out[0].Batch || out[0].Snap {
		t.Fatalf("want one Batch (non-Snap) staged frame, got %+v", out)
	}
	payload, ok := out[0].Payload.([]wsmsg.Bar)
	if !ok || len(payload) != 2 {
		t.Fatalf("want 2-bar batch payload, got %+v", out[0].Payload)
	}
	// Cache must now be ascending with the prepended bars at the front.
	cached := m.bars[barKey("US.AAPL", "1m")]
	if len(cached) != 4 || cached[0].BucketStart != isoMs(1_000_000) {
		t.Fatalf("front-insert failed: %+v", cached)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/uihub/ -run TestMirrorBarPrepend -v`
Expected: FAIL — `unknown field Batch` / no `md.BarPrepend` case

- [ ] **Step 3: Implement**

`mirror.go` — add `Batch` to `staged` (`mirror.go:16-21`):

```go
type staged struct {
	Topic   wsmsg.Topic
	Key     string
	Payload any
	Snap    bool
	Batch   bool // a bars batch-prepend delta: broadcast immediately, lossless, never coalesced
}
```

`applyMD` — add case after `BarSnapshot` (`mirror.go:124-144`):

```go
	case md.BarPrepend:
		if len(v.Bars) == 0 {
			return nil
		}
		out := make([]wsmsg.Bar, len(v.Bars))
		for i, b := range v.Bars {
			out[i] = mapBar(b)
		}
		k := barKey(v.Symbol, string(v.TF))
		// out is ascending and strictly older than the cached run's head.
		m.bars[k] = append(out, m.bars[k]...)
		return []staged{{Topic: wsmsg.TopicBars, Payload: out, Batch: true}}
```

`hub.go` — `stageMD` `classMDKeep` branch (`hub.go:678-703`):

```go
	case classMDKeep:
		if s.Snap {
			h.broadcast(s, true)
			return
		}
		if s.Batch {
			// Batch prepend: broadcast now as a delta on the lossless lane.
			// Keep-latest coalescing would let a later single-bar delta drop it.
			h.broadcast(s, false)
			return
		}
		h.pendKeep[dedupOf(s)] = s
```

`coalesce.go` — `outboundCoalesceKey`, guard before the topic switch (`coalesce.go:57-63`):

```go
	if snap {
		return ""
	}
	if s.Batch {
		return "" // bars batch-prepend is lossless/ordered; never shed
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/uihub/ -run TestMirrorBarPrepend -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add engine/internal/uihub/mirror.go engine/internal/uihub/hub.go engine/internal/uihub/coalesce.go engine/internal/uihub/mirror_test.go
git commit -m "feat(uihub): stage bars batch-prepend as immediate lossless delta"
```

---

## Task 4: Wire DTOs + tygo regen

**Files:**
- Modify: `engine/internal/uihub/wsmsg/payloads.go` (add near other command args, `payloads.go:253-434`)
- Regenerate: `ui/src/gen/wsmsg.ts` (via `make gen-ts`)
- Test: `engine/internal/uihub/wsmsg/` build + `make gen-ts-check`

**Interfaces:**
- Produces: `wsmsg.LoadOlderBarsArgs{ Symbol string; Daily bool }` and `wsmsg.LoadOlderResult{ Added int; Exhausted bool }`; both regenerate into `ui/src/gen/wsmsg.ts`.

- [ ] **Step 1: Add the DTOs**

```go
// payloads.go — add with the other command arg DTOs

// LoadOlderBarsArgs is the args for the LoadOlderBars command. Daily=true
// requests the one-shot pre-2016 daily fetch; false deepens the intraday 1m
// series by one chunk.
type LoadOlderBarsArgs struct {
	Symbol string `json:"symbol"`
	Daily  bool   `json:"daily"`
}

// LoadOlderResult is the deferred-ack Value for LoadOlderBars.
type LoadOlderResult struct {
	Added     int  `json:"added"`
	Exhausted bool `json:"exhausted"`
}
```

- [ ] **Step 2: Regenerate + verify build**

Run:
```bash
cd engine && make gen-ts && go build ./... && make gen-ts-check
```
Expected: `gen-ts` updates `ui/src/gen/wsmsg.ts` with `LoadOlderBarsArgs`/`LoadOlderResult`; build passes; `gen-ts-check` passes (no drift after committing).

- [ ] **Step 3: Confirm generated TS**

Read `ui/src/gen/wsmsg.ts` and confirm both interfaces are present with `symbol/daily` and `added/exhausted` fields.

- [ ] **Step 4: Commit**

```bash
git add engine/internal/uihub/wsmsg/payloads.go ui/src/gen/wsmsg.ts
git commit -m "feat(wsmsg): add LoadOlderBars args + result DTOs"
```

---

## Task 5: Orchestrator `LoadOlder` / `LoadOlderDaily`

**Files:**
- Modify: `engine/internal/backfill/backfill.go` (add fields to `Orchestrator` `backfill.go:109-117`; init in `New` `backfill.go:119-127`; `noteBackfilled` call in `Backfill` `backfill.go:158-165`; new methods; reuse `intradayFrom` `window.go:14-27`, `dailyFloor` `backfill.go:172`, `walkChain` `backfill.go:299-313`, `archive1m`/`archiveDailyBars` `backfill.go:214-225`, `store.ReadBars1m`/`ReadDailyBars`)
- Modify imports: add `golang.org/x/sync/singleflight` (pattern per `opendfeed.go:279-308`) and `sync`.
- Test: `engine/internal/backfill/loadolder_test.go` (create)

**Interfaces:**
- Consumes: `Seeder.SeedOlder1m` (must be added to the `Seeder` interface — see Step 3), `Archive.ReadBars1m`/`ReadDailyBars`, chains, `clock.Clock`.
- Produces:
  - `func (o *Orchestrator) LoadOlder(ctx context.Context, symbol string) (added int, exhausted bool, err error)`
  - `func (o *Orchestrator) LoadOlderDaily(ctx context.Context, symbol string) (added int, exhausted bool, err error)`
  - `func (o *Orchestrator) noteBackfilled(symbol string, from1m time.Time)` — records the initial 1m watermark.
  - Extends `Seeder` interface with `SeedOlder1m(symbol string, bars []feed.Bar)`.

**Reuse `backfill_test.go`'s existing stubs — do not redefine them** (same package `backfill`; redefinition is a duplicate-declaration compile error). It already provides: `fakeFetcher` (implements `HistFetcher`: `DailyBars`/`Intraday1m`, fields `daily`/`m1`/`dErr`/`mErr`), `fakeTail` (`Tail1m`), `fakeSeeder` (`SeedSessionTicks`/`SeedDaily`/`SeedHistory1m`), `fakeArchive` (`ReadDailyBars`/`ReadBars1m`/`ReadJournalTicks`/`ArchiveBar1m`/`ArchiveDaily`), and helpers `bar(ms int64) feed.Bar`, `tick(ms int64) feed.Tick`, `chain(fs ...HistFetcher) []Source`, `fixedNow() time.Time`. There is no `fakeSource` type — use `fakeFetcher`.

**Important — extend `fakeSeeder` before writing these tests**, or the whole package's existing tests stop compiling: adding `SeedOlder1m` to the `Seeder` interface (Step 3 below) means `fakeSeeder` no longer satisfies `Seeder` unless it also implements it. Add to `backfill_test.go`:

```go
// fakeSeeder already exists; add this method alongside its others.
func (s *fakeSeeder) SeedOlder1m(symbol string, bars []feed.Bar) {
	s.older = append(s.older, bars...) // add an `older []feed.Bar` field to fakeSeeder
}
```

**Also note:** `fakeArchive.ReadBars1m(_ string, _, _ int64) ([]feed.Bar, error)` returns `a.m1` unconditionally — it ignores the `fromMs`/`toMs` args. So the archive-first tests below must set `a.m1` directly to the fixture that should be "read back," not rely on range filtering.

- [ ] **Step 1: Write the failing tests**

```go
// loadolder_test.go
package backfill

import (
	"context"
	"testing"
	"time"
)

func TestLoadOlderErrorsWithoutWatermark(t *testing.T) {
	o := New(nil, nil, &fakeTail{}, &fakeSeeder{}, &fakeArchive{}, clockAt(fixedNow()), Config{})
	_, _, err := o.LoadOlder(context.Background(), "US.AAPL")
	if err == nil {
		t.Fatalf("want error when no watermark exists")
	}
}

func TestLoadOlderArchiveFirstServesWithoutChain(t *testing.T) {
	watermark := fixedNow().AddDate(0, 0, -20)
	older := watermark.AddDate(0, 0, -20)
	arch := &fakeArchive{m1: []feed.Bar{bar(older.UnixMilli()), bar(older.Add(time.Minute).UnixMilli())}}
	seed := &fakeSeeder{}
	// nil intraday chain => archive-only; walkChain over nil returns (nil,"",nil).
	o := New(nil, nil, &fakeTail{}, seed, arch, clockAt(fixedNow()), Config{IntradayDays: 20})
	o.noteBackfilled("US.AAPL", watermark)
	added, exhausted, err := o.LoadOlder(context.Background(), "US.AAPL")
	if err != nil || added == 0 || exhausted {
		t.Fatalf("archive-first should serve: added=%d exhausted=%v err=%v", added, exhausted, err)
	}
	if len(seed.older) == 0 {
		t.Fatalf("SeedOlder1m not called")
	}
}

func TestLoadOlderExhaustsAtFloor(t *testing.T) {
	o := New(nil, nil, &fakeTail{}, &fakeSeeder{}, &fakeArchive{}, clockAt(fixedNow()), Config{})
	o.noteBackfilled("US.AAPL", dailyFloor) // watermark already at 2016 floor
	_, exhausted, err := o.LoadOlder(context.Background(), "US.AAPL")
	if err != nil || !exhausted {
		t.Fatalf("want exhausted at floor, got exhausted=%v err=%v", exhausted, err)
	}
}

func TestLoadOlderExhaustsWhenArchiveAndChainEmpty(t *testing.T) {
	watermark := fixedNow().AddDate(0, 0, -20)
	o := New(nil, nil, &fakeTail{}, &fakeSeeder{}, &fakeArchive{}, clockAt(fixedNow()), Config{IntradayDays: 20})
	o.noteBackfilled("US.AAPL", watermark) // empty archive + nil chain
	_, exhausted, err := o.LoadOlder(context.Background(), "US.AAPL")
	if err != nil || !exhausted {
		t.Fatalf("want exhausted (pre-listing), got exhausted=%v err=%v", exhausted, err)
	}
}

func TestLoadOlderDailyOneShot(t *testing.T) {
	pre2016 := []feed.Bar{bar(dailyFloor.AddDate(-1, 0, 0).UnixMilli())}
	src := &fakeFetcher{daily: pre2016}
	seed := &fakeSeeder{}
	o := New(chain(src), nil, &fakeTail{}, seed, &fakeArchive{}, clockAt(fixedNow()), Config{})
	o.noteBackfilled("US.KO", fixedNow().AddDate(0, 0, -20))
	added, exhausted, err := o.LoadOlderDaily(context.Background(), "US.KO")
	if err != nil || added == 0 || !exhausted {
		t.Fatalf("daily one-shot: added=%d exhausted=%v err=%v", added, exhausted, err)
	}
	// Second call must be a no-op exhausted (one-shot) — src.daily must NOT be re-fetched.
	added2, exhausted2, _ := o.LoadOlderDaily(context.Background(), "US.KO")
	if added2 != 0 || !exhausted2 {
		t.Fatalf("second daily call should be exhausted no-op")
	}
}
```

`clockAt(t time.Time)` is a tiny `clock.Clock` fake returning a fixed `Now()`; if `backfill_test.go` doesn't already have one, add it there (implement the same `clock.Clock` interface `warmStart`/`fillDaily` use via `o.clk.Now()`). Match every `New(...)` call's argument order/types to the confirmed real signature: `New(daily, intraday []Source, tail TailFetcher, seeder Seeder, archive Archive, clk clock.Clock, cfg Config) *Orchestrator`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/backfill/ -run TestLoadOlder -v`
Expected: FAIL — `o.LoadOlder undefined`, `SeedOlder1m` not in `Seeder`

- [ ] **Step 3: Extend `Seeder` + orchestrator state**

`backfill.go` — extend `Seeder` (`backfill.go:54-58`):

```go
type Seeder interface {
	SeedDaily(symbol string, bars []feed.Bar)
	SeedHistory1m(symbol string, bars []feed.Bar)
	SeedOlder1m(symbol string, bars []feed.Bar)
	SeedSessionTicks(symbol string, ticks []feed.Tick)
}
```

`*md.Core` already gains `SeedOlder1m` in Task 2, so it still satisfies `Seeder`.

Add fields to `Orchestrator` (`backfill.go:109-117`):

```go
	mu        sync.Mutex
	oldest1m  map[string]int64 // symbol -> oldest loaded 1m watermark (ms); floor of explored depth
	dailyDone map[string]bool  // symbol -> pre-2016 daily one-shot already served
	older     singleflight.Group
	olderDay  singleflight.Group
```

Init maps in `New` (`backfill.go:119-127`):

```go
	return &Orchestrator{
		daily: daily, intraday: intraday, tail: tail, seeder: seeder, archive: archive,
		clk: clk, cfg: cfg,
		oldest1m: map[string]int64{}, dailyDone: map[string]bool{},
	}
```

- [ ] **Step 4: Implement `noteBackfilled` + `Backfill` hook**

```go
// noteBackfilled records the initial 1m watermark for a symbol once its boot/
// chart-open backfill has run. Takes the minimum so a later re-run never raises
// the floor.
func (o *Orchestrator) noteBackfilled(symbol string, from1m time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ms := from1m.UnixMilli()
	if cur, ok := o.oldest1m[symbol]; !ok || ms < cur {
		o.oldest1m[symbol] = ms
	}
}
```

Call it at the end of `Backfill` (`backfill.go:158-165`), reusing the already-computed `from1m`:

```go
func (o *Orchestrator) Backfill(ctx context.Context, symbol string) error {
	now := o.clk.Now()
	from1m := intradayFrom(now, o.cfg.IntradayDays)
	o.warmStart(ctx, symbol, from1m, now)
	tailOldestMs, tailOK := o.tail1m(ctx, symbol)
	o.fill1m(ctx, symbol, from1m, now, tailOldestMs, tailOK)
	err := o.fillDaily(ctx, symbol, o.dailyFrom(now), now)
	o.noteBackfilled(symbol, from1m)
	return err
}
```

- [ ] **Step 5: Implement `LoadOlder`**

```go
// archiveCoverSlackMs: an archive window counts as "covered" if its earliest
// bar is within ~2 trading days of the window start (IPO/holiday gaps aside).
const archiveCoverSlackMs = 2 * 24 * 60 * 60 * 1000

// LoadOlder deepens the shared 1m series by one intraday chunk (IntradayDays
// trading days older than the current watermark), archive-first, floored at
// 2016-01-01. exhausted=true means the floor or the symbol's listing date was
// reached — the caller must stop asking.
func (o *Orchestrator) LoadOlder(ctx context.Context, symbol string) (int, bool, error) {
	v, err, _ := o.older.Do(symbol, func() (any, error) {
		return o.loadOlder(ctx, symbol)
	})
	if err != nil {
		return 0, false, err
	}
	r := v.(olderResult)
	return r.added, r.exhausted, nil
}

type olderResult struct {
	added     int
	exhausted bool
}

func (o *Orchestrator) loadOlder(ctx context.Context, symbol string) (olderResult, error) {
	o.mu.Lock()
	cur, ok := o.oldest1m[symbol]
	o.mu.Unlock()
	if !ok {
		return olderResult{}, fmt.Errorf("load older: no backfill watermark for %s", symbol)
	}
	floorMs := dailyFloor.UnixMilli()
	if cur <= floorMs {
		return olderResult{exhausted: true}, nil
	}
	to := time.UnixMilli(cur) // exclusive upper bound
	from := intradayFrom(to, o.cfg.IntradayDays)
	if from.Before(dailyFloor) {
		from = dailyFloor
	}

	// Archive-first.
	if bars, err := o.archive.ReadBars1m(symbol, from.UnixMilli(), cur-1); err == nil && len(bars) > 0 {
		if bars[0].BucketMs <= from.UnixMilli()+archiveCoverSlackMs {
			o.seedOlderUnlessCanceled(ctx, symbol, bars)
			o.advanceWatermark(symbol, from.UnixMilli())
			return olderResult{added: len(bars), exhausted: from.UnixMilli() <= floorMs}, nil
		}
	}

	// Provider chain.
	bars, served, err := walkChain(ctx, symbol, from, to, o.intraday, intraday1m)
	if len(bars) == 0 {
		// No archive coverage AND no chain data: floor or pre-listing reached.
		o.advanceWatermark(symbol, from.UnixMilli())
		return olderResult{exhausted: true}, err
	}
	o.archive1m(bars)
	o.seedOlderUnlessCanceled(ctx, symbol, bars)
	o.advanceWatermark(symbol, from.UnixMilli())
	slog.Info("load older: deep 1m served", "symbol", symbol, "provider", served, "bars", len(bars), "from", from)
	return olderResult{added: len(bars), exhausted: from.UnixMilli() <= floorMs}, nil
}

func (o *Orchestrator) advanceWatermark(symbol string, ms int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if cur, ok := o.oldest1m[symbol]; !ok || ms < cur {
		o.oldest1m[symbol] = ms
	}
}

func (o *Orchestrator) seedOlderUnlessCanceled(ctx context.Context, symbol string, bars []feed.Bar) {
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedOlder1m(symbol, b) })
}
```

Add `LoadOlderDaily`:

```go
// LoadOlderDaily one-shot-fetches pre-2016 daily (archive-first, then the daily
// chain: Alpaca empty pre-2016 -> Yahoo to listing). Always exhausted after one
// success or one empty result.
func (o *Orchestrator) LoadOlderDaily(ctx context.Context, symbol string) (int, bool, error) {
	v, err, _ := o.olderDay.Do(symbol, func() (any, error) {
		return o.loadOlderDaily(ctx, symbol)
	})
	if err != nil {
		return 0, false, err
	}
	r := v.(olderResult)
	return r.added, r.exhausted, nil
}

func (o *Orchestrator) loadOlderDaily(ctx context.Context, symbol string) (olderResult, error) {
	o.mu.Lock()
	done := o.dailyDone[symbol]
	o.mu.Unlock()
	if done {
		return olderResult{exhausted: true}, nil
	}

	floorMs := dailyFloor.UnixMilli()

	// Archive-first: if the archive already holds pre-2016 daily, re-seed those.
	if all, err := o.archive.ReadDailyBars(symbol); err == nil && len(all) > 0 && all[0].BucketMs < floorMs {
		var pre []feed.Bar
		for _, b := range all {
			if b.BucketMs >= floorMs {
				break
			}
			pre = append(pre, b)
		}
		o.seedDailyUnlessCanceled(ctx, symbol, pre)
		o.markDailyDone(symbol)
		return olderResult{added: len(pre), exhausted: true}, nil
	}

	from := time.Unix(0, 0)
	to := dailyFloor
	bars, served, err := walkChain(ctx, symbol, from, to, o.daily, dailyBars)
	if len(bars) == 0 {
		o.markDailyDone(symbol) // never ask again this session
		return olderResult{exhausted: true}, err
	}
	o.archiveDailyBars(bars)
	o.seedDailyUnlessCanceled(ctx, symbol, bars)
	o.markDailyDone(symbol)
	slog.Info("load older: pre-2016 daily served", "symbol", symbol, "provider", served, "bars", len(bars))
	return olderResult{added: len(bars), exhausted: true}, nil
}

func (o *Orchestrator) markDailyDone(symbol string) {
	o.mu.Lock()
	o.dailyDone[symbol] = true
	o.mu.Unlock()
}

func (o *Orchestrator) seedDailyUnlessCanceled(ctx context.Context, symbol string, bars []feed.Bar) {
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
}
```

Add imports: `"fmt"` and `"golang.org/x/sync/singleflight"` (`sync` is already imported in `backfill.go`; `golang.org/x/sync v0.20.0` is already in `go.mod` — no `go get`).

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd engine && go test ./internal/backfill/ -run TestLoadOlder -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add engine/internal/backfill/backfill.go engine/internal/backfill/loadolder_test.go
git commit -m "feat(backfill): add LoadOlder/LoadOlderDaily with archive-first + watermark"
```

---

## Task 6: Deferred-ack command plumbing

**Files:**
- Modify: `engine/internal/uihub/conn.go` (`commandHandler` interface `conn.go:21-23`; `dispatch` command case `conn.go:315-319`)
- Modify: `engine/internal/uihub/commands.go` (`handle` signature `commands.go:91`; **exactly 49** `return <ack>` statements in the switch → `return <ack>, false`)
- Modify: `engine/internal/uihub/commands_test.go` (**all 28 existing `cd.handle(...)`/`cd2.handle(...)` call sites** pass a 5th `reply` arg and consume `(ack, _)` — mechanical but must be complete for the package to compile)

**Interfaces:**
- Produces: `commandHandler.handle(ctx, name, args, connID, reply func(wsmsg.AckMsg)) (ack wsmsg.AckMsg, deferred bool)`. When `deferred` is true, dispatch does not send the returned ack; the handler (or a goroutine it spawned) calls `reply` later. `reply` sets `Kind`/`CorrID` and enqueues on the conn outbox (goroutine-safe).

- [ ] **Step 1: Write the failing test**

```go
// commands_test.go
func TestDispatchSendsSynchronousAck(t *testing.T) {
	// Build a commands handler + a fake conn that captures enqueued JSON.
	// Send an existing command (e.g. "SubscribeIndicator" with valid args) and
	// assert exactly one ack frame with the right corrId is enqueued.
	// (Model on the existing commands_test.go harness.)
}
```

`commands_test.go` already has a full harness (spies `spyExec`, `spyCfg`, `spyInd`, `spyDemandCtl`, `spyVenueAdmin`, `spyVenueTester`) and 28 existing `cd.handle(...)`/`cd2.handle(...)` calls (lines 50, 73, 83, 92, 102, 111, 159, 167, 176, 180, 188, 237, 262, 278, 290, 303, 312, 324, 333, 342, 353, 358, 387, 400, 409, 432, 448, 480) — extend the new test into that file and, in the same change, update every existing call site: add a 5th arg (`func(wsmsg.AckMsg) {}` when the test doesn't care about a deferred reply) and change `ack := cd.handle(...)` to `ack, _ := cd.handle(...)`. This is mechanical but must be done for **all 28** sites or the package won't compile — do it as one sweep, not incrementally.

`export_test.go`'s `NewCommandsForTest` (line 25-27) wraps `newCommands` and is unaffected by *this* task's signature change (it only changes `handle`, not `newCommands`) — but note it for Task 7, which does change `newCommands`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/uihub/ -run TestDispatchSendsSynchronousAck -v`
Expected: FAIL (compile error until the signature changes / test written)

- [ ] **Step 3: Change the interface + dispatch**

`conn.go` (`conn.go:21-23`):

```go
type commandHandler interface {
	handle(ctx context.Context, name string, args json.RawMessage, connID uint64, reply func(wsmsg.AckMsg)) (wsmsg.AckMsg, bool)
}
```

`conn.go` dispatch command case (`conn.go:294-336`):

```go
	case "command":
		send := func(ack wsmsg.AckMsg) {
			ack.Kind = "ack"
			ack.CorrID = head.CorrID
			c.enqueueJSON(ack)
		}
		ack, deferred := c.cmd.handle(ctx, head.Name, head.Args, c.nid, send)
		if !deferred {
			send(ack)
		}
```

- [ ] **Step 4: Update `commands.handle` signature + all returns**

`commands.go:91`:

```go
func (cd *commands) handle(ctx context.Context, name string, args json.RawMessage, connID uint64, reply func(wsmsg.AckMsg)) (wsmsg.AckMsg, bool) {
```

Mechanically change every `return <ack>` inside the switch to `return <ack>, false` (blocked/accepted/ackFromCmd/etc.), and the final default `return`. Example transforms:

```go
		if err := json.Unmarshal(args, &a); err != nil || a.InstanceID == "" {
			return blocked("bad args"), false
		}
		...
		return wsmsg.AckMsg{Status: "accepted"}, false
```

(The `reply` param is unused by every existing case — that is fine; Task 7 is its first user.)

- [ ] **Step 5: Run test + full build**

Run: `cd engine && go build ./... && go test ./internal/uihub/ -run TestDispatch -v`
Expected: build passes; PASS

- [ ] **Step 6: Commit**

```bash
git add engine/internal/uihub/conn.go engine/internal/uihub/commands.go engine/internal/uihub/commands_test.go
git commit -m "refactor(uihub): support deferred command acks via reply callback"
```

---

## Task 7: `LoadOlderBars` command + engine wiring

**Design note (why this shape):** `commands.handle` runs on the **per-connection reader goroutine** (`conn.dispatch`), not on `Hub.Run`'s single goroutine. `main.go`'s shutdown order is `<-hubDone → scanWG.Wait() → backfillWG.Wait()`, and the existing deep-backfill trigger (`triggerBackfill`) is safe from that race only because it is invoked exclusively from `handleEnsureDemand`/`rearmBackfill`, both of which run *on* `Hub.Run`'s goroutine (confirmed: `backfillWG.Add(1)` inside the injected `SetBackfill` closure always executes there). A naive `LoadOlderBars` handler that calls `orch.LoadOlder` (and `backfillWG.Add`) directly from the conn goroutine would not have that guarantee. **Fix: route `LoadOlder` through the Hub exactly like `EnsureDemand`** — a new per-verb channel + `Run`-loop case + atomic-slot-injected fetcher (mirroring `ensureDemandCh`/`SetBackfill`/`backfill()`). This also means `demandCtl` (already the interface `commands.dem` uses, satisfied by `*Hub`) just gains one method — **no change to `newCommands`'s signature, `api.go`, or `export_test.go`** is needed.

**Files:**
- Modify: `engine/internal/uihub/hub.go` — new `loadOlderReq` struct + `loadOlderCh chan loadOlderReq` field (Hub struct, alongside `ensureDemandCh` `hub.go:113`) + init in `NewHub`; new `loadOlderSlot atomic.Pointer[loadOlderBox]` (alongside `backfillSlot` `hub.go:125`); `SetLoadOlder`/`loadOlderFn()` (mirror `SetBackfill`/`backfill()` `hub.go:221-235`); public `LoadOlder(...)` (mirror `EnsureDemand` `hub.go:249-254`); `handleLoadOlder` handler; new `case` in `Run`'s select (`hub.go:344-386`, alongside `case r := <-h.ensureDemandCh:` `hub.go:358-359`) and in `drain` (`hub.go:400-429`) if that function has a matching per-verb case for `ensureDemandCh` (check before assuming; mirror it if so, skip if `drain` doesn't cover per-verb channels this way).
- Modify: `engine/internal/uihub/commands.go` — extend the `demandCtl` interface (`commands.go:42-45`) with `LoadOlder(symbol string, daily bool, done func(added int, exhausted bool, err error))`; add the `LoadOlderBars` case in `handle`, calling `cd.dem.LoadOlder(...)`.
- Modify: `engine/internal/uihub/commands_test.go` — add a matching `LoadOlder` method to `spyDemandCtl` (`commands_test.go:200-222`) with a settable stub field.
- Modify: `engine/cmd/etape/main.go` — hoist `orch` to function scope; build a fallback chain-less orchestrator for replay/backfill-disabled; define the `backfillWG`-tracked `loadOlderFn` closure; call `hub.SetLoadOlder(loadOlderFn)`.

**Interfaces:**
- Consumes: `wsmsg.LoadOlderBarsArgs`, `wsmsg.LoadOlderResult` (Task 4).
- Produces: `demandCtl.LoadOlder(symbol string, daily bool, done func(added int, exhausted bool, err error))`; `Hub.SetLoadOlder(fn func(sym string, daily bool, done func(added int, exhausted bool, err error)))`. Deferred ack: success/exhausted → `AckMsg{Status:"accepted", Value: LoadOlderResult{...}}`. Provider/chain error or missing watermark → `AckMsg{Status:"blocked", Reason: ...}`. Bars arrive separately as `md.bars` pushes (Task 3).

- [ ] **Step 1: Write the failing tests**

```go
// commands_test.go — extend spyDemandCtl (commands_test.go:200-222) with:
type spyDemandCtl struct {
	ensured []struct {
		conn uint64
		d    feed.Demand
	}
	released []struct {
		conn uint64
		id   string
	}
	loadOlderFn func(symbol string, daily bool, done func(added int, exhausted bool, err error))
}

func (s *spyDemandCtl) LoadOlder(symbol string, daily bool, done func(added int, exhausted bool, err error)) {
	if s.loadOlderFn != nil {
		s.loadOlderFn(symbol, daily, done)
		return
	}
	done(0, true, nil) // default stub: nothing to fetch (mirrors the Hub's nil-slot fallback)
}

func TestLoadOlderBarsDeferredAckAccepted(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	dem.loadOlderFn = func(_ string, _ bool, done func(added int, exhausted bool, err error)) {
		go done(19000, false, nil) // async, as the real Hub path is
	}
	var got wsmsg.AckMsg
	done := make(chan struct{})
	ack, deferred := cd.handle(context.Background(), "LoadOlderBars", mustJSON(t, wsmsg.LoadOlderBarsArgs{Symbol: "US.AAPL"}), 1,
		func(a wsmsg.AckMsg) { got = a; close(done) })
	if !deferred {
		t.Fatalf("want deferred=true, got ack=%+v", ack)
	}
	<-done
	if got.Status != "accepted" {
		t.Fatalf("want accepted, got %+v", got)
	}
	v, ok := got.Value.(wsmsg.LoadOlderResult)
	if !ok || v.Added != 19000 || v.Exhausted {
		t.Fatalf("want LoadOlderResult{19000,false}, got %+v", got.Value)
	}
}

func TestLoadOlderBarsErrorBlocks(t *testing.T) {
	cd, dem, _ := newCmdWith(t, nil, false)
	dem.loadOlderFn = func(_ string, _ bool, done func(added int, exhausted bool, err error)) {
		done(0, false, errors.New("no watermark"))
	}
	var got wsmsg.AckMsg
	_, deferred := cd.handle(context.Background(), "LoadOlderBars", mustJSON(t, wsmsg.LoadOlderBarsArgs{Symbol: "US.AAPL"}), 1,
		func(a wsmsg.AckMsg) { got = a })
	if !deferred || got.Status != "blocked" || got.Reason == "" {
		t.Fatalf("want deferred blocked ack with a reason, got deferred=%v ack=%+v", deferred, got)
	}
}

func TestLoadOlderBarsNoFetchSurfaceExhausted(t *testing.T) {
	// dem.loadOlderFn left nil -> spyDemandCtl's default stub -> done(0, true, nil),
	// modeling the Hub's loadOlderSlot never having been set (replay/disabled
	// with no fallback orchestrator).
	cd, _, _ := newCmdWith(t, nil, false)
	var got wsmsg.AckMsg
	_, deferred := cd.handle(context.Background(), "LoadOlderBars", mustJSON(t, wsmsg.LoadOlderBarsArgs{Symbol: "US.AAPL"}), 1,
		func(a wsmsg.AckMsg) { got = a })
	if !deferred || got.Status != "accepted" {
		t.Fatalf("want deferred accepted ack, got deferred=%v ack=%+v", deferred, got)
	}
	v, ok := got.Value.(wsmsg.LoadOlderResult)
	if !ok || !v.Exhausted {
		t.Fatalf("want Exhausted=true, got %+v", got.Value)
	}
}
```

(`mustJSON(t, v)` — a tiny `json.Marshal` helper returning `json.RawMessage`; add it if `commands_test.go` doesn't already have an equivalent. Match `newCmdWith`'s real signature, `commands_test.go:224-233`.)

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/uihub/ -run TestLoadOlderBars -v`
Expected: FAIL — no `LoadOlderBars` case, `spyDemandCtl` doesn't implement `LoadOlder`

- [ ] **Step 3: Extend `demandCtl` + add the command case**

`commands.go` — extend the interface (`commands.go:42-45`):

```go
type demandCtl interface {
	EnsureDemand(connID uint64, d feed.Demand)
	ReleaseDemand(connID uint64, demandID string)
	LoadOlder(symbol string, daily bool, done func(added int, exhausted bool, err error))
}
```

Add the case in `handle`:

```go
	case "LoadOlderBars":
		var a wsmsg.LoadOlderBarsArgs
		if err := json.Unmarshal(args, &a); err != nil || a.Symbol == "" {
			return blocked("bad args"), false
		}
		cd.dem.LoadOlder(a.Symbol, a.Daily, func(added int, exhausted bool, err error) {
			if err != nil {
				reply(wsmsg.AckMsg{Status: "blocked", Reason: err.Error()})
				return
			}
			reply(wsmsg.AckMsg{Status: "accepted", Value: wsmsg.LoadOlderResult{Added: added, Exhausted: exhausted}})
		})
		return wsmsg.AckMsg{}, true // deferred
```

- [ ] **Step 4: Add `Hub.LoadOlder` — the Run-loop-routed public entry point**

`hub.go` — new request struct (alongside `ensureDemandReq`, `hub.go:48-51`):

```go
type loadOlderReq struct {
	symbol string
	daily  bool
	done   func(added int, exhausted bool, err error)
}
```

New box type (alongside `backfillBox`, `hub.go:95-97`):

```go
// loadOlderBox mirrors backfillBox/feedBox: SetLoadOlder is called once at
// boot from main's goroutine, after the Hub is already running.
type loadOlderBox struct {
	fn func(sym string, daily bool, done func(added int, exhausted bool, err error))
}
```

New `Hub` struct fields (alongside `ensureDemandCh` and `backfillSlot`, `hub.go:113`/`125`):

```go
	loadOlderCh chan loadOlderReq

	loadOlderSlot atomic.Pointer[loadOlderBox]
```

Init in `NewHub` (alongside the other channel inits, unbuffered like `ensureDemandCh`):

```go
	loadOlderCh: make(chan loadOlderReq),
```

`SetLoadOlder`/`loadOlderFn()` (mirror `SetBackfill`/`backfill()`, `hub.go:221-235`):

```go
// SetLoadOlder injects the pan-triggered deeper-history fetch trigger after
// the hub is running. Safe to call once from boot; nil until then (replay
// without a fallback orchestrator, or tests, never call it) — LoadOlder then
// acks exhausted with no fetch attempted.
func (h *Hub) SetLoadOlder(fn func(sym string, daily bool, done func(added int, exhausted bool, err error))) {
	h.loadOlderSlot.Store(&loadOlderBox{fn: fn})
}

func (h *Hub) loadOlderFn() func(sym string, daily bool, done func(added int, exhausted bool, err error)) {
	if b := h.loadOlderSlot.Load(); b != nil {
		return b.fn
	}
	return nil
}
```

Public entry point (mirror `EnsureDemand`, `hub.go:249-254`; satisfies the extended `demandCtl`):

```go
// LoadOlder marshals a pan-triggered deeper-history request onto Run's
// goroutine so the injected fetcher's backfillWG.Add (if any) always executes
// there — the same safety property triggerBackfill relies on for its own
// WaitGroup, needed here because commands.handle runs on a per-connection
// goroutine, not Run's.
func (h *Hub) LoadOlder(symbol string, daily bool, done func(added int, exhausted bool, err error)) {
	select {
	case h.loadOlderCh <- loadOlderReq{symbol: symbol, daily: daily, done: done}:
	case <-h.closed:
		done(0, true, nil)
	}
}
```

Handler (alongside `handleEnsureDemand`, `hub.go:451-478`):

```go
// handleLoadOlder runs on Run's own goroutine, so the injected fn's
// backfillWG.Add (main.go's loadOlderFn) is race-safe against the shutdown
// sequence's backfillWG.Wait(), exactly like triggerBackfill's fn.
func (h *Hub) handleLoadOlder(r loadOlderReq) {
	fn := h.loadOlderFn()
	if fn == nil {
		r.done(0, true, nil) // no fetch surface injected — nothing older to serve
		return
	}
	fn(r.symbol, r.daily, r.done)
}
```

Add the case to `Run`'s select (`hub.go:344-386`, alongside `case r := <-h.ensureDemandCh:`):

```go
		case r := <-h.loadOlderCh:
			h.handleLoadOlder(r)
```

Read `drain()` (`hub.go:400-429`) before this step: if it has a per-verb case mirroring `ensureDemandCh` (used by a test-only `sync()` barrier), add a matching `case r := <-h.loadOlderCh: h.handleLoadOlder(r)` there too so barrier-based tests don't hang on an unconsumed request; skip if `drain` doesn't work that way.

- [ ] **Step 5: Wire `loadOlderFn` in main.go**

Hoist the orchestrator to function scope, alongside `backfillWG` (`main.go:358`, before `if live {` at `main.go:362`):

```go
	var backfillWG sync.WaitGroup
	var orch *backfill.Orchestrator
```

Change the existing construction (`main.go:405`) from `:=` to `=`:

```go
	orch = backfill.New(dailyChain, intradayChain, fd, core, st, clock.System{}, backfill.Config{...})
```

After the whole `if live { ... } else { ... }` dispatch resolves (i.e. after the replay `else` branch ends, `main.go:456`), add:

```go
	if orch == nil && st != nil {
		// No live backfill chains were built (replay, or cfg.Backfill.Enabled ==
		// false) — a chain-less orchestrator still serves archive-first
		// LoadOlder/LoadOlderDaily and acks exhausted past the archive, per the
		// spec's "no special casing beyond a nil-chain check." walkChain over a
		// nil chain returns (nil,"",nil), so LoadOlder degrades cleanly.
		orch = backfill.New(nil, nil, nil, core, st, clock.System{}, backfill.Config{IntradayDays: cfg.Backfill.IntradayDays})
	}
	loadOlderFn := func(sym string, daily bool, done func(added int, exhausted bool, err error)) {
		if orch == nil { // st itself was nil — should not happen in practice
			done(0, true, nil)
			return
		}
		backfillWG.Add(1)
		go func() {
			defer backfillWG.Done()
			if daily {
				added, exhausted, err := orch.LoadOlderDaily(ctx, sym)
				done(added, exhausted, err)
				return
			}
			added, exhausted, err := orch.LoadOlder(ctx, sym)
			done(added, exhausted, err)
		}()
	}
	hub.SetLoadOlder(loadOlderFn)
```

`core`, `st`, `ctx`, `hub` are all already in function scope by this point (confirmed: `ctx` from `main.go:220`, `st` from `main.go:229`, `hub` from the `uihub.New(...)` call at `main.go:296-305`, `core` passed into that same call).

- [ ] **Step 6: Run tests + build**

Run: `cd engine && go build ./... && go test ./internal/uihub/ -run TestLoadOlderBars -v`
Expected: build passes; PASS

- [ ] **Step 7: Commit**

```bash
git add engine/internal/uihub/hub.go engine/internal/uihub/commands.go engine/internal/uihub/commands_test.go engine/cmd/etape/main.go
git commit -m "feat(uihub): LoadOlderBars command routed through Hub.Run for WaitGroup safety"
```

---

## Task 8: `BarStore` batch-prepend branch (UI)

**Files:**
- Modify: `ui/src/data/BarStore.ts` (`apply` delta path `BarStore.ts:31-55`)
- Test: `ui/src/data/BarStore.test.ts` (create or append)

**Interfaces:**
- Consumes: a `DeltaMsg` on `md.bars` whose `payload` is `Bar[]` (batch prepend) vs a single `Bar` (existing single delta) vs a `SnapshotMsg` (full replace).
- Produces: batch-`unshift` of strictly-older sorted bars; per-key rev bump so `ChartPanel`'s `isDirty` fires.

- [ ] **Step 1: Write the failing test**

```ts
// BarStore.test.ts
import { describe, expect, it } from "vitest";
import { BarStore } from "./BarStore";

const bar = (bucketStart: string): Record<string, unknown> => ({
  symbol: "US.AAPL", timeframe: "1m", bucketStart, o: 1, h: 1, l: 1, c: 1, v: 1, inProgress: false,
});

describe("BarStore batch prepend", () => {
  it("unshifts a strictly-older batch and bumps rev", () => {
    const s = new BarStore();
    s.apply({ kind: "snapshot", topic: "md.bars", payload: [bar("2024-01-02T10:00:00Z"), bar("2024-01-02T10:01:00Z")] } as never);
    const rev0 = s.getRev("US.AAPL", "1m");

    s.apply({ kind: "delta", topic: "md.bars", payload: [bar("2024-01-01T10:00:00Z"), bar("2024-01-01T10:01:00Z")] } as never);

    const series = s.series("US.AAPL", "1m");
    expect(series.map((b) => b.bucketStart)).toEqual([
      "2024-01-01T10:00:00Z", "2024-01-01T10:01:00Z",
      "2024-01-02T10:00:00Z", "2024-01-02T10:01:00Z",
    ]);
    expect(s.getRev("US.AAPL", "1m")).toBeGreaterThan(rev0);
  });

  it("ignores non-older bars in a batch (no duplicates)", () => {
    const s = new BarStore();
    s.apply({ kind: "snapshot", topic: "md.bars", payload: [bar("2024-01-02T10:00:00Z")] } as never);
    s.apply({ kind: "delta", topic: "md.bars", payload: [bar("2024-01-02T10:00:00Z"), bar("2024-01-01T10:00:00Z")] } as never);
    expect(s.series("US.AAPL", "1m").map((b) => b.bucketStart)).toEqual([
      "2024-01-01T10:00:00Z", "2024-01-02T10:00:00Z",
    ]);
  });
});
```

- [ ] **Step 2: Run to verify failure**

Run: `cd ui && npx vitest run src/data/BarStore.test.ts`
Expected: FAIL — batch delta currently mis-handled (payload cast to single `Bar`)

- [ ] **Step 3: Implement the batch branch**

In `BarStore.apply`, at the top of the `delta` handling (before the single-bar cast at `BarStore.ts:31`):

```ts
    if (Array.isArray(m.payload)) {
      this.prependBatch(m.payload as Bar[]);
      return;
    }
```

Add the method:

```ts
  // prependBatch inserts a strictly-older, ascending run at the FRONT of a
  // series (the deep-history BarPrepend delta). Bars not strictly older than the
  // current earliest are dropped (idempotent against reconnect re-snapshots).
  private prependBatch(older: Bar[]): void {
    if (older.length === 0) return;
    const first = older[0];
    const k = this.key(first.symbol, first.timeframe);
    const arr = this.series_.get(k) ?? [];
    const earliest = arr[0]?.bucketStart;
    const fresh = earliest === undefined ? older : older.filter((b) => b.bucketStart < earliest);
    if (fresh.length === 0) return;
    fresh.sort((a, b) => (a.bucketStart < b.bucketStart ? -1 : a.bucketStart > b.bucketStart ? 1 : 0));
    this.series_.set(k, [...fresh, ...arr]);
    this.bumpRev(k);
    this.markDirty();
  }
```

- [ ] **Step 4: Run to verify pass**

Run: `cd ui && npx vitest run src/data/BarStore.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add ui/src/data/BarStore.ts ui/src/data/BarStore.test.ts
git commit -m "feat(ui/BarStore): batch-prepend branch for deep-history deltas"
```

---

## Task 9: Facade visible-range methods

**Files:**
- Modify: `ui/src/render/chart/ChartApiFacade.ts` (interface `ChartApiFacade.ts:17-50`)
- Modify: `ui/src/chrome/panels/ChartPanel.tsx` (`makeFacade` `ChartPanel.tsx:46-104`)
- Modify: `ui/src/render/chart/ChartController.test.ts` (extend the facade fake — `function fakeFacade()` at line 28, object literal lines 34-63; NOT the `fakeSeries()` fake at lines 13-26)

**Interfaces:**
- Produces on `ChartApiFacade`:
  - `getVisibleRange(): { from: number; to: number } | null` — visible **time** range (UTC seconds; the chart uses UTCTimestamp for all TFs, per `makeFacade`'s `Math.floor(ms/1000)` at `ChartPanel.tsx:74`).
  - `setVisibleRange(range: { from: number; to: number }): void`.

(Note: `getVisibleLogicalRange` from the spec is intentionally omitted — the left-edge trigger runs inside `subscribeVisibleLogicalRangeChange`, which already hands the `LogicalRange` to the handler `ChartPanel.tsx:220`, so no facade accessor is needed for it.)

- [ ] **Step 1: Add to the interface**

```ts
// ChartApiFacade.ts — inside interface ChartApiFacade
  getVisibleRange(): { from: number; to: number } | null;
  setVisibleRange(range: { from: number; to: number }): void;
```

- [ ] **Step 2: Implement in `makeFacade`**

```ts
// ChartPanel.tsx — inside the facade object (near scrollToRealTime, line 80)
    getVisibleRange: () => {
      const r = chart.timeScale().getVisibleRange();
      return r ? { from: r.from as unknown as number, to: r.to as unknown as number } : null;
    },
    setVisibleRange: (range) =>
      chart.timeScale().setVisibleRange({ from: range.from as unknown as Time, to: range.to as unknown as Time }),
```

- [ ] **Step 3: Extend the test fake**

In `ChartController.test.ts`, add `getVisibleRange`/`setVisibleRange` to the fake facade. Back them with a mutable field so Task 10's test can drive them:

```ts
  let visibleRange: { from: number; to: number } | null = null;
  const setVisibleRangeCalls: Array<{ from: number; to: number }> = [];
  // in the fake:
  getVisibleRange: () => visibleRange,
  setVisibleRange: (r: { from: number; to: number }) => { setVisibleRangeCalls.push(r); visibleRange = r; },
```

Expose `visibleRange`/`setVisibleRangeCalls` to the test scope (module-level `let`s in the test file).

- [ ] **Step 4: Run existing controller tests (should still pass)**

Run: `cd ui && npx vitest run src/render/chart/ChartController.test.ts`
Expected: PASS (fake compiles with new methods; no behavior change yet)

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/chart/ChartApiFacade.ts ui/src/chrome/panels/ChartPanel.tsx ui/src/render/chart/ChartController.test.ts
git commit -m "feat(ui/chart): add visible time-range get/set to ChartApiFacade"
```

---

## Task 10: Viewport preservation around front-growth rebuild

**Files:**
- Modify: `ui/src/render/chart/ChartController.ts` (`setAllBars` `ChartController.ts:222-237` only — `LEFT_PAD_BARS` at `ChartController.ts:37` is **already** `export const LEFT_PAD_BARS = 4;` and already imported by the test file; no change needed there)
- Test: `ui/src/render/chart/ChartController.test.ts` (append)

**Interfaces:**
- Consumes: `facade.getVisibleRange()`/`setVisibleRange()` (Task 9).
- Produces: after a `setData` rebuild, the visible **time** range is restored unless the viewport was parked at the right edge (newest bar visible). Exported `LEFT_PAD_BARS` for Task 11's trigger.

- [ ] **Step 1: Write the failing tests**

```ts
// ChartController.test.ts — new cases
it("restores the visible time range after a front-growth rebuild (scrolled back)", () => {
  // Arrange: controller with an initial series; simulate being scrolled back by
  // setting visibleRange to an interior window (to < newest bar time).
  // Act: apply a snapshot that PREPENDS older bars (grows at the front).
  // Assert: setVisibleRange was called once with the pre-rebuild range.
});

it("does NOT restore when parked at the right edge (newest bar visible)", () => {
  // Arrange: visibleRange.to >= newest bar time.
  // Act: front-growth rebuild.
  // Assert: setVisibleRange was NOT called (default follow behavior kept).
});
```

Drive these through the existing test's mechanism for pushing bars into the controller (via the `bars` reader + `sync()`), forcing the `setAllBars` path (either cold `backfilled=false`, or a front-growth snapshot that trips the anchor check `ChartController.ts:174-178`).

- [ ] **Step 2: Run to verify failure**

Run: `cd ui && npx vitest run src/render/chart/ChartController.test.ts`
Expected: FAIL — `setVisibleRange` never called

- [ ] **Step 3: Implement in `setAllBars`**

```ts
  private setAllBars(bars: Bar[]): void {
    const before = this.facade.getVisibleRange();
    const pad = this.leftPad(bars);
    this.candle.setData([...pad, ...bars.map((b) => this.mainPoint(b))]);
    this.volume.setData([...pad, ...bars.map((b) => toVolume(b, this.palette))]);
    // Restore the pre-rebuild time window unless parked at the right edge —
    // LWC's setData preserves LOGICAL indices, so a front-prepend would else
    // teleport the viewport ~1 chunk into the past.
    if (before && bars.length > 0) {
      const newestSec = Math.floor(new Date(bars[bars.length - 1].bucketStart).getTime() / 1000);
      const atRightEdge = before.to >= newestSec;
      if (!atRightEdge) this.facade.setVisibleRange(before);
    }
    this.backfilled = true;
    this.lastAppliedCount = bars.length;
    this.lastAppliedKey = keyOf(bars[bars.length - 1]);
    this.lastTailBucket = bars[bars.length - 1].bucketStart;
    this.lastBarsOp = "reset";
  }
```

`LEFT_PAD_BARS` is already exported at `ChartController.ts:37` — no export step needed; Task 11 imports it directly.

- [ ] **Step 4: Run to verify pass**

Run: `cd ui && npx vitest run src/render/chart/ChartController.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/chart/ChartController.ts ui/src/render/chart/ChartController.test.ts
git commit -m "fix(ui/chart): preserve viewport time-range across front-growth rebuilds"
```

---

## Task 11: Older-history trigger + guard state machine

**Files:**
- Create: `ui/src/render/chart/olderHistory.ts`
- Test: `ui/src/render/chart/olderHistory.test.ts`

**Interfaces:**
- Consumes: `LEFT_PAD_BARS` (Task 10); an injected `load(daily: boolean): Promise<{ status: string; value?: unknown }>` (wraps `commands.sendCommand("LoadOlderBars", …)`); `now(): number` for cooldown/timeout (injectable for tests).
- Produces:
  - `class OlderHistoryController` with:
    - `maybeTrigger(range: { from: number; to: number } | null, isIntraday: boolean): void`
    - `reset(): void` (on symbol change — clears exhausted/inflight/cooldown/timeout)
  - Guards: one in-flight per kind; `exhausted` per kind (`intraday` | `daily`); ~5s cooldown after a blocked ack; 30s timeout clears in-flight (lost ack on reconnect).

- [ ] **Step 1: Write the failing tests**

```ts
// olderHistory.test.ts
import { describe, expect, it, vi } from "vitest";
import { OlderHistoryController } from "./olderHistory";

const range = (from: number, to: number) => ({ from, to });

describe("OlderHistoryController", () => {
  it("fires when fewer than 1.5 screens remain to the left", () => {
    const load = vi.fn().mockResolvedValue({ status: "accepted", value: { added: 100, exhausted: false } });
    const c = new OlderHistoryController({ load, now: () => 0 });
    // screens = to-from = 100; remaining = from - LEFT_PAD_BARS = 10 - 4 = 6 < 150
    c.maybeTrigger(range(10, 110), true);
    expect(load).toHaveBeenCalledWith(false);
  });

  it("does not fire when far from the left edge", () => {
    const load = vi.fn().mockResolvedValue({ status: "accepted", value: { added: 0, exhausted: false } });
    const c = new OlderHistoryController({ load, now: () => 0 });
    c.maybeTrigger(range(1000, 1100), true); // remaining 996 > 150
    expect(load).not.toHaveBeenCalled();
  });

  it("suppresses a second request while one is in flight", () => {
    const load = vi.fn().mockReturnValue(new Promise(() => {})); // never resolves
    const c = new OlderHistoryController({ load, now: () => 0 });
    c.maybeTrigger(range(10, 110), true);
    c.maybeTrigger(range(10, 110), true);
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("stops asking after an exhausted ack (per kind)", async () => {
    const load = vi.fn().mockResolvedValue({ status: "accepted", value: { added: 0, exhausted: true } });
    const c = new OlderHistoryController({ load, now: () => 0 });
    c.maybeTrigger(range(10, 110), true);
    await Promise.resolve(); await Promise.resolve();
    c.maybeTrigger(range(10, 110), true);
    expect(load).toHaveBeenCalledTimes(1);
    // daily kind is independent — still allowed
    c.maybeTrigger(range(10, 110), false);
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("applies a cooldown after a blocked ack", async () => {
    const load = vi.fn().mockResolvedValue({ status: "blocked", reason: "not ready" });
    let t = 0;
    const c = new OlderHistoryController({ load, now: () => t });
    c.maybeTrigger(range(10, 110), true);
    await Promise.resolve(); await Promise.resolve();
    t = 1000; c.maybeTrigger(range(10, 110), true); // within 5s cooldown
    expect(load).toHaveBeenCalledTimes(1);
    t = 6000; c.maybeTrigger(range(10, 110), true); // cooldown elapsed
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("reset() re-enables an exhausted kind", async () => {
    const load = vi.fn().mockResolvedValue({ status: "accepted", value: { added: 0, exhausted: true } });
    const c = new OlderHistoryController({ load, now: () => 0 });
    c.maybeTrigger(range(10, 110), true);
    await Promise.resolve(); await Promise.resolve();
    c.reset();
    c.maybeTrigger(range(10, 110), true);
    expect(load).toHaveBeenCalledTimes(2);
  });
});
```

- [ ] **Step 2: Run to verify failure**

Run: `cd ui && npx vitest run src/render/chart/olderHistory.test.ts`
Expected: FAIL — module does not exist

- [ ] **Step 3: Implement**

```ts
// olderHistory.ts
import { LEFT_PAD_BARS } from "./ChartController";

type Kind = "intraday" | "daily";
type Ack = { status: string; reason?: string; value?: unknown };

interface Deps {
  load: (daily: boolean) => Promise<Ack>;
  now: () => number;
}

const SCREENS_THRESHOLD = 1.5;
const COOLDOWN_MS = 5_000;
const TIMEOUT_MS = 30_000;

// OlderHistoryController decides when to ask the engine for an older chunk and
// guards against duplicate/looping requests. It is UI-framework-agnostic and
// fully unit-testable; ChartPanel wires it to sendCommand + timeframe/symbol.
export class OlderHistoryController {
  private inflight: Record<Kind, boolean> = { intraday: false, daily: false };
  private exhausted: Record<Kind, boolean> = { intraday: false, daily: false };
  private cooldownUntil: Record<Kind, number> = { intraday: 0, daily: 0 };
  private timers: Partial<Record<Kind, ReturnType<typeof setTimeout>>> = {};

  constructor(private readonly deps: Deps) {}

  maybeTrigger(range: { from: number; to: number } | null, isIntraday: boolean): void {
    if (!range) return;
    const kind: Kind = isIntraday ? "intraday" : "daily";
    if (this.inflight[kind] || this.exhausted[kind]) return;
    if (this.deps.now() < this.cooldownUntil[kind]) return;

    const screens = range.to - range.from;
    const remaining = range.from - LEFT_PAD_BARS;
    if (screens <= 0 || remaining >= SCREENS_THRESHOLD * screens) return;

    this.inflight[kind] = true;
    this.timers[kind] = setTimeout(() => { this.inflight[kind] = false; }, TIMEOUT_MS);

    this.deps.load(!isIntraday === false ? false : true); // see note below
    // NOTE: load(daily) — daily=true only for the D/W/M kind:
  }

  reset(): void {
    this.inflight = { intraday: false, daily: false };
    this.exhausted = { intraday: false, daily: false };
    this.cooldownUntil = { intraday: 0, daily: 0 };
    for (const k of Object.keys(this.timers) as Kind[]) {
      const t = this.timers[k];
      if (t) clearTimeout(t);
    }
    this.timers = {};
  }

  private clearTimer(kind: Kind): void {
    const t = this.timers[kind];
    if (t) { clearTimeout(t); this.timers[kind] = undefined; }
  }

  private settle(kind: Kind, ack: Ack): void {
    this.clearTimer(kind);
    this.inflight[kind] = false;
    if (ack.status === "accepted") {
      const v = ack.value as { exhausted?: boolean } | undefined;
      if (v?.exhausted) this.exhausted[kind] = true;
    } else {
      this.cooldownUntil[kind] = this.deps.now() + COOLDOWN_MS;
    }
  }
}
```

Fix the `maybeTrigger` load call (the placeholder line above is intentionally wrong so the test drives it out). Replace with:

```ts
    const daily = kind === "daily";
    this.deps.load(daily).then((ack) => this.settle(kind, ack)).catch(() => this.settle(kind, { status: "blocked" }));
```

- [ ] **Step 4: Run to verify pass**

Run: `cd ui && npx vitest run src/render/chart/olderHistory.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/chart/olderHistory.ts ui/src/render/chart/olderHistory.test.ts
git commit -m "feat(ui/chart): OlderHistoryController trigger + guard state machine"
```

---

## Task 12: Wire the trigger into ChartPanel

**Files:**
- Modify: `ui/src/chrome/panels/ChartPanel.tsx` (mount effect: construct the controller before `clampRight`'s definition; call `maybeTrigger` inside `clampRight` `ChartPanel.tsx:220-225`; `reset()` in `applySymbol` `ChartPanel.tsx:281-289`; unsubscribe/reset in cleanup `ChartPanel.tsx:401-409`)
- Test: covered by Task 11 unit tests + the Task 13 E2E; add a light `ChartPanel` render smoke if the panel already has one.

**Interfaces:**
- Consumes: `OlderHistoryController` (Task 11); `commands.sendCommand` (`ChartPanel.tsx` `commands` prop); `isIntradayTimeframe` (`ChartPanel.tsx:16`, from `render/chart/barClose`); `Timeframe` (`ChartPanel.tsx:14`, from `render/chart/barBucket`); `tfRef` (declared line 147); `currentSymbol` (declared line 250, reassigned in `applySymbol` line 282).

- [ ] **Step 1: Construct the controller in the mount effect, before `clampRight`**

`olderHistory.load` reads `currentSymbol` by closure (a reassigned `let`), which is intentional — it must always send the *current* symbol, not the mount-time one, since `applySymbol` reassigns it on every link-group change. `currentSymbol` is declared at line 250; `clampRight` at line 220 is defined earlier in source order but only *invoked* later (LWC calls it on pan events, well after mount completes), so there is no runtime ordering hazard either way — but for readability, construct `olderHistory` immediately before the `clampRight` definition (i.e. insert just before `ChartPanel.tsx:220`, ahead of the facade build at line 227 is also fine — either placement works; before `clampRight` reads cleanest since `clampRight` is the first consumer):

```ts
    const olderHistory = new OlderHistoryController({
      load: (daily) => commands.sendCommand("LoadOlderBars", { symbol: currentSymbol, daily }),
      now: () => Date.now(),
    });
```

Import: `import { OlderHistoryController } from "../../render/chart/olderHistory";`

- [ ] **Step 2: Trigger in `clampRight`**

Extend the handler (`ChartPanel.tsx:220-225`):

```ts
    const clampRight = (range: LogicalRange | null) => {
      const visibleBars = range ? range.to - range.from : RIGHT_OFFSET_BARS;
      const target = clampRightScroll(timeScale.scrollPosition(), visibleBars);
      if (target !== null) timeScale.scrollToPosition(target, false);
      olderHistory.maybeTrigger(
        range ? { from: range.from, to: range.to } : null,
        isIntradayTimeframe(tfRef.current as Timeframe),
      );
      scheduleRefreshSelection();
    };
```

- [ ] **Step 3: Reset on symbol change + cleanup**

In `applySymbol` (`ChartPanel.tsx:281-289`), after `currentSymbol` is reassigned at line 282, call `olderHistory.reset();`. `applySymbol` runs on initial mount, every link-group symbol change, and group re-pick, so this fires exactly on every symbol change. In the mount effect cleanup (`ChartPanel.tsx:401-409`, alongside `timeScale.unsubscribeVisibleLogicalRangeChange(clampRight)` at line 403), call `olderHistory.reset();` to clear any pending 30s timeout.

- [ ] **Step 4: Typecheck + build + existing tests**

Run:
```bash
cd ui && npm run build && npx vitest run src/render/chart/olderHistory.test.ts src/data/BarStore.test.ts src/render/chart/ChartController.test.ts
```
Expected: build passes; all PASS. (Per the repo's vitest forks-pool quirk, run canvas-touching test files individually if a batched run flakes.)

- [ ] **Step 5: Commit**

```bash
git add ui/src/chrome/panels/ChartPanel.tsx
git commit -m "feat(ui/chart): pan-triggered LoadOlderBars via OlderHistoryController"
```

---

## Task 13: End-to-end — replay-mode archive-backed deepening

**Files:**
- Create/modify: an e2e spec under `ui/e2e/…` (match the existing replay/no-OpenD harness used by prior chart features)
- Uses: a replay archive fixture with >20 trading days of 1m + pre-2016 daily for one symbol.

**Interfaces:**
- Consumes: the full stack (engine replay/demo build + UI). Verifies `LoadOlderBars` serves archived depth and paints prepended bars without OpenD, and acks `exhausted` past the archive.

- [ ] **Step 1: Write the E2E**

Model on the existing "no-OpenD verify recipe" (see the chart-polish-batch memory / prior e2e). The test:
1. Boots the engine in replay/demo against a fixture archive whose 1m depth exceeds the 20-day boot window.
2. Opens a 1m chart, waits for the boot seed.
3. Programmatically scrolls the time scale toward the left edge (or drives `setVisibleLogicalRange`) to trip `maybeTrigger`.
4. Asserts the series grew at the front (earliest `bucketStart` moved older) and the viewport did not teleport.
5. Scrolls again past the archived floor and asserts no further growth (exhausted).

```ts
// ui/e2e/deep-history.spec.ts (skeleton — fill selectors to match existing e2e helpers)
import { test, expect } from "@playwright/test";

test("pan left loads archived older 1m bars in replay mode", async ({ page }) => {
  // boot replay engine + UI (reuse existing fixtures/harness)
  // open chart, read earliest bucketStart via window hook / evaluate
  // scroll left, wait for BarStore rev bump
  // assert earliest bucketStart is older than before
});
```

- [ ] **Step 2: Run the E2E**

Run: `cd ui && npx playwright test e2e/deep-history.spec.ts`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add ui/e2e/deep-history.spec.ts
git commit -m "test(ui/e2e): archive-backed LoadOlderBars in replay mode"
```

---

## Task 14: Full verification sweep

- [ ] **Step 1: Engine full test + build**

Run:
```bash
cd engine && go build ./... && go vet ./... && go test ./... && make gen-ts-check
```
Expected: all PASS, no drift.

- [ ] **Step 2: UI full test + build**

Run:
```bash
cd ui && npm run build && npx vitest run
```
Expected: PASS (run canvas-touching files individually if the forks-pool quirk flakes a batched run).

- [ ] **Step 3: Manual replay smoke (no OpenD)**

Boot the engine in replay/demo mode + UI, open a liquid symbol's 1m chart, pan left repeatedly, and confirm: older bars appear in place, the viewport does not jump, 5m/15m/30m/60m switches keep the deepened depth, and panning stops growing at the archive floor. Switch to Daily and pan left on an old symbol (e.g. KO) to confirm pre-2016 daily loads once.

---

## Self-Review (completed against the spec, and re-checked after verification passes caught 10 real mismatches — all fixed inline above)

- **§1 Trigger** → Tasks 10 (viewport preservation using the already-exported `LEFT_PAD_BARS`), 11 (threshold + guards), 12 (wired into `clampRight` + reset on symbol change). Cooldown/timeout/exhausted-per-kind all in Task 11.
- **§2 Protocol / deferred ack** → Tasks 4 (DTOs), 6 (deferred-ack plumbing across all 49 `handle` returns + 28 test call sites), 7 (command + Hub-routed wiring). Error maps to `blocked` (Global Constraints).
- **§3 Engine fetch** → Task 5 (`LoadOlder`/`LoadOlderDaily`, singleflight, watermark, archive-first heuristic, exhausted semantics, `Seeder` extension + `fakeSeeder` fix).
- **§4 Seed + emission** → Tasks 1 (`BarPrepend`), 2 (`SeedOlder1m` + reseed, correct `msg.*` dispatch + `isInMsg()` registration), 3 (mirror front-insert + anti-coalesce via `staged.Batch`), 8 (`BarStore` prepend). Pre-2016 daily reuses `SeedDaily` unchanged (Task 5 seeds via `SeedDaily`).
- **§5 Viewport preservation** → Task 10.
- **Testing (spec)** → engine unit (Tasks 2,3,5), UI unit (Tasks 8,10,11), E2E (Task 13), full sweep (Task 14). Live verify with Earl remains outstanding (needs OpenD + real Alpaca/Yahoo).
- **What does NOT change** → boot backfill sequence untouched (Task 5 only appends `noteBackfilled`); SQLite schema untouched (new read/write call sites only); provider chains reused; no new config keys; `newCommands`'s constructor signature and `api.go` are untouched (Task 7's Hub-routing redesign needed neither).

---

## Notes for the executor

- The deferred-ack change (Task 6) touches every existing command's `return` (49 in `commands.go`) **and** every existing call site in `commands_test.go` (28, listed in Task 6). Both must be complete in the same change or the package won't build. Do it as one sweep, build, then move on.
- Task 7's `LoadOlder` deliberately goes through a new `Hub` channel + `Run`-loop case (mirroring `EnsureDemand`/`SetBackfill`) rather than being called directly from `commands.handle`. This is not incidental style — it's required for `backfillWG.Add` to stay race-free against the shutdown sequence (see the Global Constraints entry). Don't simplify it back to a direct call.
- Extending `Seeder` (Task 5) and `demandCtl` (Task 7) each break one existing test fake (`fakeSeeder` in `backfill_test.go`; `spyDemandCtl` in `commands_test.go`) — extend both fakes in the same commit as the interface change, not after.
- `md.bars` batch frames MUST carry `Batch: true` and bypass both coalescing axes (Task 3) — verify with the mirror test that a following single-bar delta does not drop the prepend.
- Sample test code throughout uses real, verified signatures (`md.New(Config{})`, `backfill.New(daily, intraday []Source, tail, seeder, archive, clk, cfg)`, `testMirror()`, the real `fakeArchive`/`fakeSeeder`/`fakeFetcher`/`bar`/`tick`/`chain`/`fixedNow` helpers) — reuse them, don't redefine same-named helpers in a new test file within the same package.
- Live verification (real Alpaca/Yahoo fetch on pan, pre-2016 daily on an old symbol, moomoo-fallback quota behavior) is explicitly deferred to a session with Earl + OpenD.
