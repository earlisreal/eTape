# Realistic Synthetic Demo Data — Engine Chunk Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **On approval:** save this document to `docs/superpowers/plans/2026-07-11-demo-synthetic-data-plan.md` and commit it as `docs(plans): add demo synthetic data implementation plan` (per the project's auto-commit-plans convention — do **not** push).

**Goal:** Replace the toy replay-journal `-demo` with a live synthetic feed: a new `engine/internal/synth` package that plugs into the *live* engine pipeline where OpenD normally sits, generating realistic ticks/quotes/books/bars in real time from one seeded PRNG, plus a boot seeder for warm history and a synthetic requester so Movers/Stock Info/news populate.

**Architecture:** `synth.Generator` is the single stateful simulator (universe draw → regime-switching price → L2 book → Poisson ticks → 1m bars/quotes), driven by one `*rand.Rand` + one injected `clock.Clock`. `synth.Feed` wraps it and satisfies both `feed.Feed` (for `pipe`/backfill) **and** `uihub.Feed` (for `hub.SetFeed`). `synth.Requester` wraps it and satisfies the pollers' `Request(ctx, protoID, proto.Message) (opend.Frame, error)` seam by marshaling real protobuf Response frames. A boot seeder fast-runs the same model to write ~1y dailies + ~3d 1m archives + ~2h of journaled ticks, then the live loop continues from the identical PRNG/book/price state. `-demo` becomes a third boot mode alongside live/replay; everything downstream (`md.Core`, journal, `markBridge`, SimBroker, uihub) is untouched.

**Tech Stack:** Go (engine); `math/rand` seeded via `rand.NewSource` (repo convention); existing `clock.Clock`/`clock.Fake` seam; generated moomoo protobuf packages under `engine/internal/feed/opend/pb/*`; `google.golang.org/protobuf/proto`.

## Global Constraints

- **Determinism:** all randomness flows from **one** `*rand.Rand` seeded with `-demo-seed` (default: a per-launch random seed). Given the same seed + the same sequence of `clock` timestamps, two runs are byte-identical. Tests inject `clock.Fake` + a fixed seed. Use `math/rand` (`rand.New(rand.NewSource(seed))`), matching existing tests (e.g. `md/determinism_test.go:18`).
- **Symbols are fictional and `US.`-prefixed** (e.g. `US.VLCN`, `US.MERI`, `US.QNTM`). Never real tickers — same safety logic as the REPLAY banner. The `supportedMarket`/`US.`-prefix gate must pass.
- **Book invariant, always:** `bestBid < bestAsk`; both sides sorted best-first; every level size > 0. This is load-bearing — SimBroker book-walk fills (5cde5be / 2026-07-10) price against this book.
- **Never touch `~/.eTape`.** Demo uses a fresh `os.MkdirTemp` dir + `demo.db`, discarded after the run.
- **`demojournal` / `genjournal` stay byte-for-byte untouched** — the E2E suite, replay smoke test, and future UI-driven-replay fixtures depend on them. `-demo` simply stops calling `demojournal.Generate`.
- **No live-path behavior change when `!demo`:** the boot restructuring must leave the pure-live (`-replay`/no-flag) code paths byte-identical. Verify by diffing behavior, not just compiling.
- **Scope: engine chunk only.** UI entry (Try-demo button, DEMO banner, `StartDemo`) is a separate later plan — it depends on the UI-driven-replay control plane (`StartReplay`/`GoLive`/`sys.session`), which is **not merged** (confirmed: grep on `main` is empty; only `childArgs`/`relaunch` scaffolding sits on a locked worktree). This plan ships behind `-demo`, verified via `./run.sh demo`.

---

## Context

Today's `-demo` (`cmd/etape/main.go:147-168`) pre-generates a 20-minute toy journal via `demojournal.Generate` — price marches up $0.05/tick on a fixed 10s cadence, volume is a constant 100, the L2 book is written once at open and frozen, and `-replay-hold` holds the last frame. The chart is a staircase, the DOM is frozen, and Movers shows "No movers right now" (scanner data is live-only, never journaled). It is neither marketing-grade nor a usable practice sandbox.

The approved spec (`docs/superpowers/specs/2026-07-11-demo-synthetic-data-design.md`, committed `39d1952` + amendment `daf326e`) replaces this with a **live synthetic feed**: demo runs the *real* engine pipeline with `synth.Feed` plugged in where `OpenDFeed` sits, streaming realistic data indefinitely (24/7, weekends included), with warm history at boot so every timeframe/indicator looks real, and a mixed universe (low-float runners + large caps + mid fillers) whose Movers board moves with the synthetic symbols. Intended outcome: open the app on a Saturday, click through to demo (CLI `-demo` for now), and get a believable, practiceable market — book↔tick consistent so SimBroker fills behave.

---

## File Structure

New package `engine/internal/synth/` (one responsibility per file; the `Generator` is the only cross-file stateful object; everything else is pure functions over passed-in state + the shared `*rand.Rand`):

| File | Responsibility |
|---|---|
| `universe.go` | Fictional name pool (~20), `Personality` enum, `SymbolSpec` (per-run static params), `DrawUniverse(rng)` → 12 specs (2 runner / 5 large / 5 mid). |
| `price.go` | `Regime` enum + Markov transition + regime-switching random walk + soft mean-reversion + runner LULD halts. |
| `book.go` | `bookState`: 10 levels/side, consume-the-touch, replenish, spread profile, `snapshot()` → `feed.Book`. |
| `tick.go` | Poisson arrivals, lognormal sizes, momentum-correlated direction, execute-against-book, `feed.Tick` emission. |
| `bars.go` | 1m aggregation (in-progress + close) → `feed.Bar`; `feed.Quote` builder (last/OHLC/cumVol/turnover/prevClose). |
| `generator.go` | `Generator`: binds the above under one `rng`+`clk`+mutex; `stepTo(logicalNow)`, coalesced `drain()`, query accessors, ET-midnight daily rollover. |
| `feed.go` | `Feed` (satisfies `feed.Feed` **and** `uihub.Feed`); `Run(ctx)` emission loop with coalescing + `ConnUp`-once. |
| `requester.go` | `Requester` (satisfies the pollers' `Request` seam + `health.prober` `ProbeRTT`); marshals rank/static/snapshot/news pb Response frames. |
| `seeder.go` | Boot-time history seeder: ~1y dailies (`ArchiveDaily`), ~3d 1m (`ArchiveBar1m`), today-so-far 1m + last ~2h ticks (`RecordEvent`), then `Flush`. |

Modified: `engine/cmd/etape/main.go` (flags + three-mode boot + `startPollers` widening + call sites), `run.sh` (demo mode help/args).

---

## Task 1: Universe & personalities

**Files:**
- Create: `engine/internal/synth/universe.go`
- Test: `engine/internal/synth/universe_test.go`

**Interfaces:**
- Produces:
  - `type Personality uint8` with `PersRunner`, `PersLargeCap`, `PersMidCap` (+ `String()`).
  - `type SpreadProfile struct { MinCents, MaxCents int; FlushMult float64 }`
  - `type SymbolSpec struct { Code string; Pers Personality; Open, PrevClose float64; FloatShares int64; Spread SpreadProfile; BookMeanSize, BookSizeSigma float64; LambdaMin, LambdaMax float64; Vol float64; GapPct float64 }` — the immutable per-run parameters for one symbol. `Code` is `US.`-prefixed.
  - `func DrawUniverse(rng *rand.Rand) []SymbolSpec` — draws 12 distinct names from the pool, assigns 2×runner / 5×largecap / 5×midcap, and fills every parametric from the personality's ranges via `rng`. Returns specs in a **stable order** (sorted by `Code`) so downstream iteration is deterministic.
  - `var namePool = []string{...}` (~20 fictional 3–4 letter tickers, no `US.` prefix stored; `DrawUniverse` prepends `US.`).

- [ ] **Step 1: Write the failing test**

```go
package synth

import (
	"math/rand"
	"strings"
	"testing"
)

func TestDrawUniverse_DeterministicAndWellFormed(t *testing.T) {
	a := DrawUniverse(rand.New(rand.NewSource(42)))
	b := DrawUniverse(rand.New(rand.NewSource(42)))
	if len(a) != 12 {
		t.Fatalf("want 12 symbols, got %d", len(a))
	}
	// same seed -> identical universe
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("nondeterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
	// counts per personality
	var run, lc, mc int
	seen := map[string]bool{}
	for _, s := range a {
		if !strings.HasPrefix(s.Code, "US.") {
			t.Errorf("symbol %q missing US. prefix", s.Code)
		}
		if seen[s.Code] {
			t.Errorf("duplicate symbol %q", s.Code)
		}
		seen[s.Code] = true
		switch s.Pers {
		case PersRunner:
			run++
			if s.Open < 2 || s.Open > 15 {
				t.Errorf("runner %s open %.2f out of $2-15", s.Code, s.Open)
			}
			if s.FloatShares < 5_000_000 || s.FloatShares > 20_000_000 {
				t.Errorf("runner %s float %d out of 5-20M", s.Code, s.FloatShares)
			}
		case PersLargeCap:
			lc++
			if s.Open < 80 || s.Open > 500 {
				t.Errorf("largecap %s open %.2f out of $80-500", s.Code, s.Open)
			}
		case PersMidCap:
			mc++
		}
	}
	if run != 2 || lc != 5 || mc != 5 {
		t.Fatalf("personality mix = runner:%d large:%d mid:%d, want 2/5/5", run, lc, mc)
	}
}

func TestDrawUniverse_DiffersAcrossSeeds(t *testing.T) {
	a := DrawUniverse(rand.New(rand.NewSource(1)))
	b := DrawUniverse(rand.New(rand.NewSource(2)))
	same := true
	for i := range a {
		if a[i].Code != b[i].Code || a[i].Pers != b[i].Pers {
			same = false
			break
		}
	}
	if same {
		t.Fatal("different seeds produced identical universe assignment")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd engine && go test ./internal/synth/ -run TestDrawUniverse -v`
Expected: FAIL — package/symbols not defined.

- [ ] **Step 3: Implement `universe.go`**

Provide the enum + `String()`, the `SymbolSpec`/`SpreadProfile` structs, a `namePool` of ~20 fictional tickers (e.g. `VLCN MERI QNTM ZPHR NRVA KLTX OBSD HLYX DRGO VNTA CRUX FYNE AXOM PLRS TSVI OMBR GLDN WYRE ECLP THRA`), and `DrawUniverse`:
- Shuffle a copy of `namePool` with `rng.Shuffle`, take the first 12, sort those 12 by name for stable output order.
- Assign personalities by fixed slot count (first 2 → runner, next 5 → largecap, last 5 → midcap) **after** the sort, so the *assignment* still varies per seed (different names land in the slots) while iteration order is stable.
- Fill parametrics from personality ranges using helper `func between(rng *rand.Rand, lo, hi float64) float64 { return lo + rng.Float64()*(hi-lo) }`:
  - Runner: `Open` $2–15, `FloatShares` 5–20M, `Spread{1,5,4.0}`, `BookMeanSize` 100–1500, `LambdaMin`0.5/`LambdaMax`30, `Vol` high, `GapPct` +40–80%. `PrevClose = Open / (1 + GapPct/100)`.
  - LargeCap: `Open` $80–500, `FloatShares` 200M–2B, `Spread{1,2,1.5}`, `BookMeanSize` 500–5000, `LambdaMin`1/`LambdaMax`5, `Vol` low, `GapPct` ±1%. `PrevClose ≈ Open`.
  - MidCap: `Open` $15–80, mid float, `Spread{1,3,2.0}`, moderate depth, `LambdaMin`0.5/`LambdaMax`3, `Vol` moderate, `GapPct` ±2–6%.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd engine && go test ./internal/synth/ -run TestDrawUniverse -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/universe.go engine/internal/synth/universe_test.go
git commit -m "feat(synth): fictional universe draw with per-run personality assignment"
```

---

## Task 2: Price / regime process (incl. runner halts)

**Files:**
- Create: `engine/internal/synth/price.go`
- Test: `engine/internal/synth/price_test.go`

**Interfaces:**
- Consumes: `SymbolSpec`, `Personality` (Task 1).
- Produces:
  - `type Regime uint8`: `RegQuiet, RegChop, RegTrendUp, RegTrendDown, RegParabolic, RegFlush, RegHalt` (+ `String()`).
  - `type priceState struct { Mid, Anchor float64; Reg Regime; DwellLeftMs int64; HaltUntilMs int64; win []pricePoint }` — mutable per-symbol price state; `win` is a rolling 5-min window of `{tsMs int64; px float64}` for halt detection.
  - `func newPriceState(spec SymbolSpec) *priceState` — seeds `Mid=Anchor=spec.Open`, `Reg=RegChop`.
  - `func stepPrice(rng *rand.Rand, spec SymbolSpec, ps *priceState, nowMs, dtMs int64)` — advances regime (Markov dwell countdown → transition) and `Mid` for elapsed `dtMs`; snaps `Mid` to $0.01; applies soft mean-reversion toward `Anchor`; slowly wanders `Anchor`. For runners, detects a >10% move within the trailing 5-min window and sets `HaltUntilMs = nowMs + 5*60_000`, forcing `Reg=RegHalt`; while `nowMs < HaltUntilMs`, `Mid` is frozen and `Reg` stays `RegHalt`.
  - `func (ps *priceState) Halted(nowMs int64) bool`.

- [ ] **Step 1: Write the failing tests**

```go
package synth

import (
	"math"
	"math/rand"
	"testing"
)

func spec(p Personality) SymbolSpec { // minimal helper
	return SymbolSpec{Code: "US.TST", Pers: p, Open: 10, PrevClose: 10, Vol: 0.5,
		Spread: SpreadProfile{1, 5, 4}, LambdaMin: 1, LambdaMax: 10}
}

func TestStepPrice_DeterministicAndCentSnapped(t *testing.T) {
	run := func() float64 {
		rng := rand.New(rand.NewSource(7))
		s := spec(PersLargeCap)
		ps := newPriceState(s)
		now := int64(0)
		for i := 0; i < 5000; i++ {
			now += 200
			stepPrice(rng, s, ps, now, 200)
		}
		return ps.Mid
	}
	a, b := run(), run()
	if a != b {
		t.Fatalf("nondeterministic: %v vs %v", a, b)
	}
	if math.Abs(a*100-math.Round(a*100)) > 1e-9 {
		t.Errorf("mid %.4f not snapped to cent", a)
	}
}

func TestStepPrice_BoundedDriftOverHours(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	s := spec(PersLargeCap)
	ps := newPriceState(s)
	now := int64(0)
	for i := 0; i < 6*60*60*5; i++ { // ~6h at 200ms steps
		now += 200
		stepPrice(rng, s, ps, now, 200)
		if ps.Mid <= 0 {
			t.Fatalf("price went non-positive: %v", ps.Mid)
		}
	}
	// mean-reversion keeps a large cap within a sane band of its open
	if ps.Mid < s.Open*0.3 || ps.Mid > s.Open*3 {
		t.Errorf("unbounded drift: open %.2f -> %.2f", s.Open, ps.Mid)
	}
}

func TestStepPrice_RunnerHaltFreezes(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	s := spec(PersRunner)
	ps := newPriceState(s)
	// force a >10% jump inside the window by seeding the window then a big move
	ps.Mid = 10
	ps.win = []pricePoint{{tsMs: 0, px: 10}}
	stepForceMove(ps, 1000, 11.5) // test-only helper: injects an 11.5 print at t=1000ms
	detectHalt(s, ps, 1000)       // exported-for-test seam or call via stepPrice
	if !ps.Halted(1000) {
		t.Fatal("expected halt after >10% move in 5min")
	}
	before := ps.Mid
	stepPrice(rng, s, ps, 2000, 1000)
	if ps.Mid != before {
		t.Errorf("price moved during halt: %v -> %v", before, ps.Mid)
	}
}
```

*(If you prefer not to expose `stepForceMove`/`detectHalt`, drive the halt through `stepPrice` with a `PARABOLIC`-forcing seed instead; keep the assertion "halt engages then freezes price".)*

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/synth/ -run TestStepPrice -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `price.go`**

- Per-personality transition matrix `transMatrix(p Personality) [7][7]float64` (rows sum to 1). Runners get elevated `RegParabolic`/`RegFlush` weights and a leg structure (parabolic tends to → chop → parabolic/flush); large caps mostly `RegQuiet`/`RegChop` with rare `RegTrendUp/Down`.
- Dwell: on entering a regime, draw `DwellLeftMs` uniformly from seconds-to-minutes (`between(rng, 3_000, 120_000)`, tighter for `RegFlush`). Decrement by `dtMs`; when ≤0, sample the next regime from the matrix row and reset dwell.
- Drift+noise per step: `drift := driftBps(reg) * spec.Vol`; `noise := rng.NormFloat64() * spec.Vol * sqrt(dtMs/1000)`; `Mid *= 1 + (drift+noise)/100`; then mean-revert: `Mid += (Anchor - Mid) * reversion(reg) * dtMs/1000` (reversion near 0 during trends/parabolic, larger in quiet); `Anchor` wanders slowly (`Anchor *= 1 + rng.NormFloat64()*0.0002`). Snap: `Mid = math.Round(Mid*100)/100`; clamp `Mid ≥ 0.01`.
- Halt (runner only): push `{nowMs, Mid}` to `win`, drop points older than `nowMs-300_000`; if `(max(win.px)-min(win.px))/min(win.px) > 0.10`, set `HaltUntilMs = nowMs+300_000`, `Reg = RegHalt`, clear window. While `Halted(nowMs)`, `stepPrice` returns immediately without moving `Mid`.

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test ./internal/synth/ -run TestStepPrice -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/price.go engine/internal/synth/price_test.go
git commit -m "feat(synth): regime-switching price walk with mean-reversion and runner halts"
```

---

## Task 3: L2 book engine

**Files:**
- Create: `engine/internal/synth/book.go`
- Test: `engine/internal/synth/book_test.go`

**Interfaces:**
- Consumes: `SymbolSpec`, `SpreadProfile` (Task 1).
- Produces:
  - `type bookState struct { bids, asks []level }` where `type level struct { Price float64; Size int64; Orders int32 }` (bids high→low, asks low→high).
  - `func newBook(rng *rand.Rand, spec SymbolSpec, mid float64) *bookState` — builds 10 levels/side centered on `mid` with the personality's spread and lognormal sizes.
  - `func (b *bookState) rebuildAround(rng *rand.Rand, spec SymbolSpec, mid float64, flush bool)` — re-centers to `mid`, widening spread when `flush`.
  - `func (b *bookState) consume(side feed.Direction, qty int64) (execPrice float64, filled int64)` — walks the touch on the given side, decrementing/promoting levels; returns VWAP of the fill. Buy consumes asks, sell consumes bids.
  - `func (b *bookState) replenish(rng *rand.Rand, spec SymbolSpec, mid float64)` — tops levels back toward target sizes at/near the touch; adds occasional round-number walls.
  - `func (b *bookState) best() (bid, ask float64)`
  - `func (b *bookState) snapshot(symbol string, tsMs int64) feed.Book` — copies to `[]feed.BookLevel` (`feed.go:59-63`); note `feed.BookLevel` names the size field `Volume` (map `level.Size` → `BookLevel.Volume`, `level.Orders` → `BookLevel.Orders`).

- [ ] **Step 1: Write the failing tests**

```go
package synth

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func assertBookInvariants(t *testing.T, b *bookState) {
	t.Helper()
	if len(b.bids) == 0 || len(b.asks) == 0 {
		t.Fatal("empty side")
	}
	if !(b.bids[0].Price < b.asks[0].Price) {
		t.Fatalf("crossed book: bid %.2f >= ask %.2f", b.bids[0].Price, b.asks[0].Price)
	}
	if !sort.SliceIsSorted(b.bids, func(i, j int) bool { return b.bids[i].Price > b.bids[j].Price }) {
		t.Error("bids not descending")
	}
	if !sort.SliceIsSorted(b.asks, func(i, j int) bool { return b.asks[i].Price < b.asks[j].Price }) {
		t.Error("asks not ascending")
	}
	for _, lv := range append(append([]level{}, b.bids...), b.asks...) {
		if lv.Size <= 0 {
			t.Errorf("non-positive size %d @ %.2f", lv.Size, lv.Price)
		}
	}
}

func TestBook_InvariantsAndConsumePromotes(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	s := spec(PersLargeCap)
	b := newBook(rng, s, 100)
	assertBookInvariants(t, b)

	askTouchBefore := b.asks[0].Price
	touchSize := b.asks[0].Size
	// consume the entire touch + into the next level
	px, filled := b.consume(feed.Buy, touchSize+1)
	if filled != touchSize+1 {
		t.Fatalf("filled %d, want %d", filled, touchSize+1)
	}
	if px < askTouchBefore {
		t.Errorf("buy VWAP %.4f below prior touch %.4f", px, askTouchBefore)
	}
	if b.asks[0].Price <= askTouchBefore {
		t.Errorf("touch not promoted: still %.2f", b.asks[0].Price)
	}
	assertBookInvariants(t, b)
}

func TestBook_ReplenishKeepsInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	s := spec(PersRunner)
	b := newBook(rng, s, 5)
	for i := 0; i < 200; i++ {
		b.consume(feed.Buy, b.asks[0].Size/2+1)
		b.replenish(rng, s, 5.10)
		assertBookInvariants(t, b)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/synth/ -run TestBook -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `book.go`**

- `newBook`/`rebuildAround`: half-spread = `max(0.01, spread.MinCents/100)` (×`spread.FlushMult` when `flush`); `bestBid = round2(mid - halfSpread)`, `bestAsk = round2(mid + halfSpread)`; ensure `bestAsk ≥ bestBid + 0.01`. Levels step outward by `0.01`–a-few-cents; size per level `lognormal(spec.BookMeanSize, spec.BookSizeSigma)` via `int64(math.Exp(rng.NormFloat64()*sigma) * mean)`, min 1; `Orders = max(1, Size/round-lot + rng noise)`.
- `consume`: loop over the side's levels from the touch; subtract `min(remaining, level.Size)`; accumulate `price*qty` for VWAP; when a level hits 0, drop it (promoting the next). If the side empties (deep sweep), synthesize a worse level one tick away so the book never goes empty. Return `sum/filled`.
- `replenish`: for each side, if fewer than 10 levels or touch size below target, add/top levels back toward `mid`-centered targets; occasionally (small `rng` prob) plant a larger wall at the nearest round number ($ or $0.50). Always re-assert best-first ordering + `bestBid < bestAsk` (nudge apart by a cent if a sweep crossed them).

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test ./internal/synth/ -run TestBook -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/book.go engine/internal/synth/book_test.go
git commit -m "feat(synth): L2 book engine with consume-the-touch and replenishment"
```

---

## Task 4: Tick engine

**Files:**
- Create: `engine/internal/synth/tick.go`
- Test: `engine/internal/synth/tick_test.go`

**Interfaces:**
- Consumes: `priceState`/`Regime` (Task 2), `bookState` (Task 3), `SymbolSpec` (Task 1).
- Produces:
  - `func lambda(spec SymbolSpec, reg Regime) float64` — Poisson intensity per second for `(personality, regime)`; `RegHalt` → 0.
  - `func drawSize(rng *rand.Rand) int64` — lognormal with 100-share bias plus occasional 1k–10k blocks.
  - `func genTicks(rng *rand.Rand, spec SymbolSpec, ps *priceState, b *bookState, sess *sessionAgg, symbol string, fromMs, toMs, seqBase int64) []feed.Tick` — samples Poisson arrivals in `[fromMs, toMs)`; for each, picks a direction (momentum-correlated with `ps.Reg`, occasional NEUTRAL), draws a size, calls `b.consume`, prints the tick at the exec price, replenishes, and updates `sess` (cumVol/turnover/OHLC). Returns ticks oldest-first with monotonically increasing `Seq` from `seqBase`. Emits nothing while `ps.Halted`.
  - `type sessionAgg struct { Open, High, Low, Last float64; Vol int64; Turnover float64; hasOpen bool }` (shared with Task 5).

- [ ] **Step 1: Write the failing tests**

```go
package synth

import (
	"math/rand"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestGenTicks_ExecuteAtTouchAndTurnover(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	s := spec(PersLargeCap)
	ps := newPriceState(s)
	ps.Reg = RegTrendUp
	b := newBook(rng, s, 100)
	var sess sessionAgg
	ticks := genTicks(rng, s, ps, b, &sess, s.Code, 0, 10_000, 1)
	if len(ticks) == 0 {
		t.Fatal("no ticks generated over 10s at lambda>0")
	}
	var seq int64
	for _, tk := range ticks {
		if tk.Seq <= seq {
			t.Errorf("seq not increasing: %d after %d", tk.Seq, seq)
		}
		seq = tk.Seq
		if tk.Turnover != tk.Price*float64(tk.Volume) {
			t.Errorf("turnover %.4f != price*vol %.4f", tk.Turnover, tk.Price*float64(tk.Volume))
		}
		if tk.Dir == feed.Buy && tk.Price < 100 { // buys lift the ask (>= mid)
			t.Errorf("buy printed below mid: %.2f", tk.Price)
		}
	}
	// up-regime should skew buy-heavy
	var buys int
	for _, tk := range ticks {
		if tk.Dir == feed.Buy {
			buys++
		}
	}
	if buys*2 < len(ticks) {
		t.Errorf("up-regime not buy-heavy: %d/%d buys", buys, len(ticks))
	}
}

func TestGenTicks_SilentDuringHalt(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	s := spec(PersRunner)
	ps := newPriceState(s)
	ps.HaltUntilMs = 999_999
	b := newBook(rng, s, 5)
	var sess sessionAgg
	if got := genTicks(rng, s, ps, b, &sess, s.Code, 0, 60_000, 1); len(got) != 0 {
		t.Fatalf("halt should silence ticks, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/synth/ -run TestGenTicks -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `tick.go`**

- Poisson: from `fromMs`, repeatedly draw inter-arrival `gap := -math.Log(rng.Float64()) / lambda(spec, ps.Reg)` seconds; advance a cursor; stop at `toMs`. (Recompute `lambda` if you subdivide by regime; single regime per call is fine since `genTicks` is called on short windows.)
- Direction: base buy-probability from regime (`RegTrendUp`/`RegParabolic` ~0.7, `RegTrendDown`/`RegFlush` ~0.3, else ~0.5); ~10% NEUTRAL inside-print at mid. Buy → `consume(feed.Buy, size)` (walks asks), sell → `consume(feed.Sell, size)`.
- Build `feed.Tick{Symbol, Seq, TsMs, Price: execPrice, Volume: filled, Turnover: execPrice*filled, Dir}` (`RecvTsMs` = `TsMs`). Update `sess`: first tick sets `Open`; track `High`/`Low`/`Last`; `Vol += filled`; `Turnover += tk.Turnover`. Call `b.replenish` after each trade.

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test ./internal/synth/ -run TestGenTicks -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/tick.go engine/internal/synth/tick_test.go
git commit -m "feat(synth): Poisson tick engine executing against the book"
```

---

## Task 5: Bars & quotes

**Files:**
- Create: `engine/internal/synth/bars.go`
- Test: `engine/internal/synth/bars_test.go`

**Interfaces:**
- Consumes: `feed.Tick`, `sessionAgg` (Task 4).
- Produces:
  - `type barAgg struct { bucketMs int64; o,h,l,c float64; vol int64; turn float64; open bool }` — one in-progress 1m bar.
  - `func (ba *barAgg) add(tk feed.Tick) (closed *feed.Bar)` — folds a tick into the current minute; when the tick's minute bucket differs from `ba.bucketMs`, returns the just-closed `feed.Bar` (finalized, END-labeled semantics) and starts a new bucket. `BucketMs` = floor to minute.
  - `func (ba *barAgg) inProgress(symbol string) (feed.Bar, bool)` — the live, not-yet-closed bar for in-minute chart updates.
  - `func buildQuote(symbol string, sess *sessionAgg, prevClose float64, tsMs int64) feed.Quote` — assembles `feed.Quote{Symbol, TsMs, Last, Open, High, Low, PrevClose, Volume, Turnover}` (`feed.go:46-56`).

- [ ] **Step 1: Write the failing tests**

```go
package synth

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestBarAgg_OHLCContinuityAndClose(t *testing.T) {
	var ba barAgg
	mk := func(ts int64, px float64, v int64) feed.Tick {
		return feed.Tick{Symbol: "US.TST", TsMs: ts, Price: px, Volume: v, Turnover: px * float64(v)}
	}
	var closed *feed.Bar
	for _, tk := range []feed.Tick{
		mk(60_000, 10.00, 100), mk(60_500, 10.20, 50), mk(61_000, 9.90, 30), // minute 1
	} {
		if c := ba.add(tk); c != nil {
			closed = c
		}
	}
	if closed == nil {
		t.Fatal("expected minute-1 bar to close when minute-2 tick arrived")
	}
	if closed.O != 10.00 || closed.H != 10.20 || closed.L != 9.90 {
		t.Errorf("bad OHLC: %+v", *closed)
	}
	if closed.Volume != 150 {
		t.Errorf("volume %d want 150", closed.Volume)
	}
	if closed.BucketMs != 60_000 {
		t.Errorf("bucket %d want 60000", closed.BucketMs)
	}
}

func TestBuildQuote_PrevCloseAndCumulative(t *testing.T) {
	sess := &sessionAgg{Open: 10, High: 11, Low: 9.5, Last: 10.8, Vol: 1234, Turnover: 13000, hasOpen: true}
	q := buildQuote("US.TST", sess, 9.0, 123)
	if q.PrevClose != 9.0 || q.Last != 10.8 || q.Volume != 1234 {
		t.Errorf("bad quote: %+v", q)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd engine && go test ./internal/synth/ -run 'TestBarAgg|TestBuildQuote' -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `bars.go`** per the interface (minute-bucket via `tk.TsMs - tk.TsMs%60000`; carry `c` forward as next bar's implicit reference; `buildQuote` is a direct field copy).

- [ ] **Step 4: Run to verify pass**

Run: `cd engine && go test ./internal/synth/ -run 'TestBarAgg|TestBuildQuote' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/bars.go engine/internal/synth/bars_test.go
git commit -m "feat(synth): 1m bar aggregation and quote builder"
```

---

## Task 6: `Generator` orchestration + daily rollover

**Files:**
- Create: `engine/internal/synth/generator.go`
- Test: `engine/internal/synth/generator_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–5, plus `clock.Clock` (`engine/internal/clock/clock.go`).
- Produces:
  - `type symRuntime struct { spec SymbolSpec; price *priceState; book *bookState; sess sessionAgg; bar barAgg; day1m map[int64]feed.Bar; dailies []feed.Bar; ticks []feed.Tick /*ring, last ~2h*/; prevClose float64; lastSeq int64; dirtyBook, dirtyQuote bool; lastBookMs, lastQuoteMs int64 }`
  - `type Generator struct { rng *rand.Rand; clk clock.Clock; mu sync.Mutex; syms map[string]*symRuntime; order []string; lastStepMs int64; curDay string }`
  - `func New(seed int64, clk clock.Clock) *Generator` — draws the universe, builds per-symbol runtimes at their opens, sets `lastStepMs = clk.Now()` truncated appropriately.
  - `func (g *Generator) StepTo(nowMs int64)` — under `mu`, for each symbol advances price (Task 2) then generates ticks over `[lastStepMs, nowMs)` (Task 4), folds them into `bar`/`sess` (Task 5), marks `dirtyBook`/`dirtyQuote`, appends ticks to the ring (trim older than `nowMs-2h`), and handles **ET-midnight rollover** (when `etDay(nowMs) != g.curDay`: set each `prevClose = sess.Last` (or last daily close), reset `sess`, redraw runner gaps via `stepPrice` regime kick, append yesterday's daily bar to `dailies`, update `curDay`).
  - `func (g *Generator) Drain(nowMs int64) []feed.Event` — under `mu`, returns coalesced events: any newly-closed `Bars1mEvent`, in-progress `Bars1mEvent` on a ~1s cadence, `TicksEvent` (ticks accumulated since last drain), `QuoteEvent` if `dirtyQuote` and ≥300ms since last quote, `BookEvent` if `dirtyBook` and ≥150ms since last book. Clears dirty flags + per-drain tick buffer.
  - Accessors (all take `mu`): `Symbols() []string`, `Has(code string) bool`, `BookOf(code) (feed.Book, bool)`, `QuoteOf(code) (feed.Quote, bool)`, `RecentTicks(code, n) []feed.Tick`, `CachedBars1m(code, n) []feed.Bar`, `RankRows() []RankRow` (code, last, pctChange vs prevClose, vol, float — sorted by pctChange desc), `Fundamentals(code) (Fundamentals, bool)` (float, sharesOut, 52wk hi/lo from `dailies`).

- [ ] **Step 1: Write the failing tests**

```go
package synth

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestGenerator_Deterministic_ByteIdentical(t *testing.T) {
	run := func() []feed.Event {
		start := int64(1_700_000_000_000)
		fk := clock.NewFake(timeMs(start))
		g := New(123, fk)
		var out []feed.Event
		for i := 0; i < 300; i++ {
			fk.Advance(200 * time.Millisecond) // Advance takes a Duration; 200 alone = 200ns
			now := start + int64(i+1)*200
			g.StepTo(now)
			out = append(out, g.Drain(now)...)
		}
		return out
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("event count differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !eventsEqual(a[i], b[i]) { // helper: compare by concrete type + fields
			t.Fatalf("event %d differs", i)
		}
	}
}

func TestGenerator_UniverseOnly(t *testing.T) {
	g := New(1, clock.NewFake(timeMs(0)))
	if g.Has("US.NOTREAL") {
		t.Error("generator claims a non-universe symbol")
	}
	if len(g.Symbols()) != 12 {
		t.Fatalf("want 12 symbols, got %d", len(g.Symbols()))
	}
}
```

*(Add `timeMs`, `eventsEqual` test helpers. `clock.NewFake` + `Advance` per `engine/internal/clock/fake.go:27,52`.)*

- [ ] **Step 2: Run to verify failure** → `go test ./internal/synth/ -run TestGenerator -v` → FAIL.

- [ ] **Step 3: Implement `generator.go`.** Key correctness points: hold `mu` for the whole of `StepTo`/`Drain`/accessors; drive Poisson/price from `g.rng` only; `etDay(ms)` via a fixed `America/New_York` `time.Location` loaded once (package var). Seq is per-symbol monotonic (`lastSeq`). The tick ring trims by `TsMs < nowMs-7_200_000`.

- [ ] **Step 4: Run to verify pass** → PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/generator.go engine/internal/synth/generator_test.go
git commit -m "feat(synth): stateful generator with coalesced drain and ET daily rollover"
```

---

## Task 7: `synth.Feed`

**Files:**
- Create: `engine/internal/synth/feed.go`
- Test: `engine/internal/synth/feed_test.go`

**Interfaces:**
- Consumes: `*Generator` (Task 6); `feed.Feed` (`engine/internal/feed/feed.go:122-131`); `uihub.Feed` (`engine/internal/uihub/api.go:37-41`); `store` reads for `HistoryBars`.
- Produces:
  - `type Feed struct { gen *Generator; st HistoryStore; clk clock.Clock; out chan feed.Event; connUpOnce sync.Once }` where `type HistoryStore interface { ReadBars1m(symbol string, fromMs, toMs int64) ([]feed.Bar, error); ReadDailyBars(symbol string) ([]feed.Bar, error) }` (satisfied by `*store.Store`).
  - `func NewFeed(gen *Generator, st HistoryStore, clk clock.Clock) *Feed`
  - `func (f *Feed) Run(ctx context.Context) error` — emits one `ConnUpEvent` first; ticks at ~50ms via `f.clk.NewTicker`; each fire: `now := f.clk.Now().UnixMilli(); f.gen.StepTo(now); for _, ev := range f.gen.Drain(now) { emit }`. **Never emits `ResyncedEvent`.** Closes `out` on `ctx.Done()`.
  - `feed.Feed` methods: `Events()`→`f.out`; `Ensure`/`Release` no-ops; `HistoryBars` reads `f.st` by resolution (`Res1m`→`ReadBars1m(from,to)`, `ResDay`→`ReadDailyBars`); `RecentTicks`/`CachedBars1m`/`BookSnapshot`/`QuoteSnapshot` from `f.gen` accessors.
  - `uihub.Feed` method: `Validate(ctx, symbol) error` → `nil` if `f.gen.Has(symbol)`, else `feed.ErrUnknownSymbol`.
  - Compile assertions: `var _ feed.Feed = (*Feed)(nil)`; `var _ uihub.Feed = (*Feed)(nil)`.

- [ ] **Step 1: Write the failing tests**

```go
package synth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

type nopHist struct{}

func (nopHist) ReadBars1m(string, int64, int64) ([]feed.Bar, error) { return nil, nil }
func (nopHist) ReadDailyBars(string) ([]feed.Bar, error)           { return nil, nil }

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
	go f.Run(ctx)
	// first event is ConnUp
	select {
	case ev := <-f.Events():
		if _, ok := ev.(feed.ConnUpEvent); !ok {
			t.Fatalf("first event not ConnUp: %T", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no ConnUp emitted")
	}
	// advancing the fake clock drives data events; never a Resynced
	for i := 0; i < 50; i++ {
		fk.Advance(200 * time.Millisecond)
	}
	cancel()
}
```

*(If driving `Run` with a fake clock is awkward because the ticker fires only on `Advance`, assert `Drain` behavior directly and cover `Run`'s ConnUp-once in a focused sub-test. The load-bearing invariants are: ConnUp emitted exactly once, no Resynced ever, all four event types reachable.)*

- [ ] **Step 2: Run to verify failure** → `go test ./internal/synth/ -run TestFeed -v` → FAIL.

- [ ] **Step 3: Implement `feed.go`** with the two compile assertions.

- [ ] **Step 4: Run to verify pass** → PASS. Also run `go build ./...` to confirm the interface assertions hold.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/feed.go engine/internal/synth/feed_test.go
git commit -m "feat(synth): synth.Feed satisfying feed.Feed and uihub.Feed"
```

---

## Task 8: `synth.Requester` (movers / Stock Info / news)

**Files:**
- Create: `engine/internal/synth/requester.go`
- Test: `engine/internal/synth/requester_test.go`

**Interfaces:**
- Consumes: `*Generator` (Task 6); `opend.Frame` (`opend/frame.go:37-44`); the protoIDs (`opend/protoid.go`) and pb packages; `google.golang.org/protobuf/proto`.
- Produces:
  - `type Requester struct { gen *Generator }`; `func NewRequester(gen *Generator) *Requester`.
  - `func (r *Requester) Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)` — builds the pb Response for the protoID from generator state, `proto.Marshal`s it into `opend.Frame{ProtoID: protoID, FmtType: 0, Body: body}` with `RetType=0` (success). Handles: `ProtoQotGetUSPreMarketRank` (3410), `ProtoQotGetTopMoversRank` (3413), `ProtoQotGetUSAfterHoursRank` (3411), `ProtoQotGetUSOvernightRank` (3412), `ProtoQotGetStaticInfo` (3202), `ProtoQotGetSecuritySnapshot` (3203), `ProtoQotGetSearchNews` (3263). Unknown protoID → a minimal success Response for that type (or `ErrRequestTimeout`-free empty) so the poller degrades quietly.
  - `func (r *Requester) ProbeRTT(ctx context.Context) (time.Duration, error)` → `(2*time.Millisecond, nil)` — satisfies `health.prober` so the demo "moomoo" health link reports healthy.

  **Field-fidelity rule (load-bearing):** populate exactly — and only — the pb fields the pollers dereference. Set `RetType = 0` (success) on every response. The exact getter chains (verified against the code) are:
  - **3410 pre-market rank** (`scan.go:305-324`, pb `qotgetuspremarketrank` as `rankpb`): per row of `resp.GetS2C().GetDataList()` the poller reads only `GetSecurity()` (→ `.GetCode()`, rendered `"US."+code`), `GetPreMarketChangeRatio()`, `GetPreMarketPrice()`, `GetPreMarketVolume()`.
  - **3413 top-movers / RTH rank** (`scan.go:326-347`, pb `qotgettopmoversrank` as `tmrpb`): per row reads `GetSecurity()`, `GetChangeRatio()`, `GetCurPrice()`, `GetVolume()`. (The request carries a required `Market` field the synth requester can ignore.)
  - **3411 / 3412 after-hours / overnight rank** (pb `qotgetusafterhoursrank`/`qotgetusovernightrank`): mirror the same code+ratio+price+volume shape the scanner's fetch fn for each reads.
  - **3203 snapshot** (`stockinfo.go` `fetchSnapshots`→`snapshotToPayload`, pb `qotgetsecuritysnapshot`): per item of `resp.GetS2C().GetSnapshotList()`, `GetBasic().GetSecurity()` keys it; `SnapshotBasicData` reads `GetName`, `GetCurPrice`, `GetLastClosePrice`, `GetVolume`, `GetHighest52WeeksPrice`, `GetLowest52WeeksPrice`; `EquitySnapshotExData` reads `GetIssuedShares`, `GetOutstandingShares`, `GetIssuedMarketVal`, `GetOutstandingMarketVal`, `GetPeRate`, `GetPeTTMRate`, `GetEarningsPershare`. This is also where the scanner gets **float** (`GetEquityExData().GetOutstandingShares()`, `scan.go:566-575`) — rank rows do NOT carry float/name/turnover.
  - **3202 static** (pb `qotgetstaticinfo`): per item of `resp.GetS2C().GetStaticInfoList()`, only `GetBasic().GetSecurity()` and `GetBasic().GetExchType()` are read.
  - **3263 news** (`news.go:75-94`, pb `qotgetsearchnews` as `newspb`): per item of `resp.GetS2C().GetSearchNewsList()` reads `GetTitle()` (there is no `GetHeadline`), `GetSource()`, `GetUrl()`, `GetNewsSubType()`, `GetPublishTime()`, `GetViewCount()`.
  Do **not** populate fields the pollers ignore.

- [ ] **Step 1: Write the failing test**

```go
package synth

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	rankpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetuspremarketrank"
)

func TestRequester_PreMarketRank_UnmarshalsWithUniverseRows(t *testing.T) {
	g := New(5, clock.NewFake(timeMs(0)))
	r := NewRequester(g)
	fr, err := r.Request(context.Background(), opend.ProtoQotGetUSPreMarketRank,
		&rankpb.Request{}) // C2S paging ignored by the synth requester
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	var resp rankpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.GetRetType() != 0 {
		t.Fatalf("retType %d", resp.GetRetType())
	}
	rows := resp.GetS2C().GetDataList()
	if len(rows) == 0 {
		t.Fatal("no rank rows for synthetic universe")
	}
	// every ranked code must be a real universe symbol
	uni := map[string]bool{}
	for _, s := range g.Symbols() {
		uni[s] = true
	}
	for _, row := range rows {
		// adjust getter to the actual field the scanner reads for the security code
		if code := codeFromRankRow(row); !uni[code] {
			t.Errorf("rank row for non-universe code %q", code)
		}
	}
}
```

*(Replace `codeFromRankRow` + `rankpb` import path with the real pb package/getters found in `scan.go`'s `fetchPreMarket`. Add sibling tests for 3202/3203/3263 asserting the pollers' getters return non-empty, generator-consistent values.)*

- [ ] **Step 2: Run to verify failure** → `go test ./internal/synth/ -run TestRequester -v` → FAIL.

- [ ] **Step 3: Implement `requester.go`.** One `build<Proto>Response(g)` helper per protoID; `Request` switches on `protoID`, marshals, returns the frame. Keep rank %-change = `(last-prevClose)/prevClose*100` so the Movers board mirrors the charts.

- [ ] **Step 4: Run to verify pass** → PASS. Cross-check by grepping `scan.go`/`stockinfo.go`/`news.go` for every getter called on the response and confirming your builder sets that field.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/requester.go engine/internal/synth/requester_test.go
git commit -m "feat(synth): synthetic requester for movers, stock info, and news"
```

---

## Task 9: Boot-time history seeder

**Files:**
- Create: `engine/internal/synth/seeder.go`
- Test: `engine/internal/synth/seeder_test.go`

**Interfaces:**
- Consumes: `*Generator` (Task 6); store archive/journal writers — `type SeedStore interface { ArchiveDaily(feed.Bar); ArchiveBar1m(feed.Bar); RecordEvent(feed.Event, int64); Flush() }` (satisfied by `*store.Store`; see `store/bars.go:25,28`, `store/journal.go:19`, `store/store.go:114`).
- Produces:
  - `func (g *Generator) Seed(st SeedStore, nowMs int64)` — fast-runs the model in **logical time** (no `clk`, no sleeps), leaving the live generator at exactly `nowMs`:
    1. **~1y dailies** per symbol: a daily-granularity walk of the same personality model (runners get occasional prior spike days) ending at *yesterday's* close; each day → `ArchiveDaily(feed.Bar{...})`. Yesterday's close is today's `prevClose`/gap base.
    2. **~3d 1m + today-so-far 1m**: fast-step the intraday model minute-by-minute from `todayMidnightET - 3d` to `nowMs`, emitting each closed 1m bar via `ArchiveBar1m`; the last 3 days' *daily* bars are re-derived from these 1m closes and re-archived so `dailies` and `bars_1m` stitch.
    3. **Last ~2h ticks**: for the window `[nowMs-2h, nowMs)`, emit the generated `TicksEvent`s (batched per symbol) via `RecordEvent(ev, tsMs)` into the journal so `warmStart` rebuilds today's 10s series.
    4. `st.Flush()`.
    The generator's price/book/seq state is left at `nowMs` so the live `Feed.Run` continues with **no seam** at the boot instant.

- [ ] **Step 1: Write the failing test**

```go
package synth

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

type capStore struct {
	daily, m1 []feed.Bar
	ticks     int
	flushed   bool
}

func (c *capStore) ArchiveDaily(b feed.Bar)  { c.daily = append(c.daily, b) }
func (c *capStore) ArchiveBar1m(b feed.Bar)  { c.m1 = append(c.m1, b) }
func (c *capStore) RecordEvent(ev feed.Event, _ int64) {
	if te, ok := ev.(feed.TicksEvent); ok {
		c.ticks += len(te.Ticks)
	}
}
func (c *capStore) Flush() { c.flushed = true }

func TestSeed_WritesWarmHistory(t *testing.T) {
	nowMs := int64(1_700_000_000_000)
	g := New(9, fixedClockAt(nowMs))
	st := &capStore{}
	g.Seed(st, nowMs)

	if !st.flushed {
		t.Error("seeder did not Flush")
	}
	// ~1y dailies * 12 symbols -> at least a few thousand daily bars
	if len(st.daily) < 12*200 {
		t.Errorf("too few daily bars: %d", len(st.daily))
	}
	// ~3 days of 1m across symbols
	if len(st.m1) < 12*300 {
		t.Errorf("too few 1m bars: %d", len(st.m1))
	}
	if st.ticks == 0 {
		t.Error("no ticks journaled for the 2h window")
	}
	// OHLC sanity on a sample
	for _, b := range st.m1[:min(200, len(st.m1))] {
		if b.H < b.L || b.O <= 0 || b.C <= 0 {
			t.Fatalf("bad 1m bar: %+v", b)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure** → `go test ./internal/synth/ -run TestSeed -v` → FAIL.

- [ ] **Step 3: Implement `seeder.go`.** Reuse `stepPrice`/`genTicks`/`barAgg` at coarser granularity for dailies (one representative walk per day) and minute granularity for intraday. Keep it O(symbols × minutes) — the 2h tick window is the only tick-level pass. Budget ≤ ~3s for 12 symbols (see timing sub-test below).

- [ ] **Step 4: Run to verify pass** → PASS.

- [ ] **Step 5: Add a timing guard + commit**

```go
func TestSeed_WithinBudget(t *testing.T) {
	nowMs := int64(1_700_000_000_000)
	g := New(9, fixedClockAt(nowMs))
	st := &capStore{}
	start := time.Now()
	g.Seed(st, nowMs)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("seed took %v, budget 3s", d)
	}
}
```

```bash
git add engine/internal/synth/seeder.go engine/internal/synth/seeder_test.go
git commit -m "feat(synth): boot-time history seeder (dailies, 1m, 2h ticks)"
```

---

## Task 10: Boot wiring, flags, `startPollers` widening, `run.sh`

**Files:**
- Modify: `engine/cmd/etape/main.go` (flags ~78-88; demo config block 147-168; mode booleans ~248/271/283/289; feed branch ~388-469; `startPollers` def ~707 + call site ~469; `forwardMD` call ~370). **Line numbers drift — anchor on symbols, not lines.** `moomooProbe`, `rttProber`, `firstAlpacaProber`, `buildBrokers` live in `boot.go` (same `package main`).
- Modify: `run.sh` (demo mode help/args)

**Interfaces (new local types in `main.go`):**
- `type pollerRequester interface { Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error) }`
- `type demandFeeder interface { Ensure(d feed.Demand); Release(id string) }`
- Reuse existing `rttProber` (already a param type on `startPollers`) for both the moomoo and alpaca probes.

- [ ] **Step 1: Flags — remove `-demo-day`/`-demo-speed`, add `-demo-seed`**

In the flag block (main.go:83-85) replace:
```go
demo      := flag.Bool("demo", false, "run the built-in synthetic demo market (no OpenD/broker needed)")
demoSeed  := flag.Int64("demo-seed", 0, "PRNG seed for -demo; 0 = random per launch")
```
Delete `demoDay` and `demoSpeed`. Keep the `-demo`/`-replay` mutual-exclusion check (141-144).

- [ ] **Step 2: Demo config block — no journal, no replay rewrite**

Edit **only the body of the existing `if *demo {` branch** (main.go:147-168): keep the temp-dir + `demo.db` + injected sim venue + generous gates, but **delete** the three replay-flag rewrites (`*replayDay = *demoDay`, `*replayHold = true`, `*speed = *demoSpeed`) and the `demojournal.Generate(...)` call. **Leave the `else { ... }` branch (main.go:169-190) completely untouched** — it serves BOTH pure-live and replay and must keep calling `config.SeedDefaultIfMissing(*cfgPath)` (live-only, gated on `*replayDay == ""`) then `config.Load(*cfgPath)`. Do NOT narrow it to `else if` (that would leave the no-flag pure-live case matching neither branch, so `cfg` stays zero-valued — silently guts live boot). Resulting shape:
```go
if *demo {
	cfg = config.Default()
	cfg.Venues = append(cfg.Venues, config.Venue{ID: "sim-paper", Broker: "sim", Env: "paper"})
	cfg.Gate.Global = config.GateGlobal{MaxDayLoss: 100000, MaxSymbolPositionValue: 100000, MaxSymbolPositionShares: 100000}
	cfg.Gate.Venue = map[string]config.GateVenue{"sim-paper": {MaxOrderValue: 100000, MaxPositionValue: 100000, MaxPositionShares: 100000, MaxOpenOrders: 50}}
	demoDir, err := os.MkdirTemp("", "etape-demo-*")
	if err != nil { log.Error("demo temp dir", "err", err); return 1, false }
	cfg.Store.DBPath = filepath.Join(demoDir, "demo.db")
} else {
	// UNCHANGED (main.go:169-190): config.SeedDefaultIfMissing(*cfgPath) when *replayDay=="" ,
	// then cfg, err = config.Load(*cfgPath). Do not touch.
}
```
After removing the `demojournal.Generate` call, confirm the `demojournal` import in `main.go` is now unused and remove that import line only (the package itself stays for `genjournal` + the E2E/replay-smoke suites).

- [ ] **Step 3: Mode booleans** (verified safe: pure-live stays `true`, replay `false`, demo `false` — but ONLY when all four edits below land together; redefining `live` alone breaks demo at the replayRows block)

- `live := *replayDay == "" && !*demo` (main.go:~248).
- Replay-only block gates on `*replayDay != ""` instead of `!live`: the `replayRows`/`execClk` block (~271) `if !live {` becomes `if *replayDay != "" {`.
- Creds stay `if live {` (~283) — demo & replay skip creds. ✓
- `buildBrokers(cfg, credsFile, execClk, !live)` (~289): `!live` is true for demo *and* replay → both force sim. ✓ (no change needed).
- `forwardMD(ctx, core, hub, live, st)` (~370) → `forwardMD(ctx, core, hub, live || *demo, st)` so demo archives finalized bars during the run (`forwardMD` gates archiving via `if !live { continue }` at its def ~623).

- [ ] **Step 4: Feed branch — merge live + demo, keep replay in `else`**

Change `if live {` (~388) to `if live || *demo {`. (`moomooProbe` and `rttProber` are defined in `boot.go`, same `package main`, so referencing them here compiles.) Inside, factor the feed/requester/probe/chain construction on `*demo`:
```go
var feedForHub uihub.Feed
var pollReq pollerRequester
var mmProbe rttProber
var demand demandFeeder
var tail backfill.TailFetcher
var dailyChain, intradayChain []backfill.Source

if *demo {
	gen := synth.New(demoSeedValue(*demoSeed), clock.System{})
	gen.Seed(st, clock.System{}.Now().UnixMilli())
	st.Flush()
	sf := synth.NewFeed(gen, st, clock.System{})
	req := synth.NewRequester(gen)
	go func() { _ = sf.Run(ctx) }()
	var eventsFeed feed.Feed = sf
	feedForHub, pollReq, mmProbe, demand, tail = sf, req, req, nil, nil
	pipeWG.Add(1)
	go pipe(ctx, &pipeWG, eventsFeed.Events(), core, st) // journaling ON into demo.db
	log.Info("engine up (demo synth feed)", "seed", demoSeedValue(*demoSeed), "symbols", gen.Symbols())
} else {
	client = opend.New(opend.Options{Addr: cfg.OpenD.Addr(), Clock: clock.System{}})
	of := opend.NewOpenDFeed(client, opend.FeedOptions{Budget: cfg.Feed.QuotaSlots, Hysteresis: ..., DisableExtendedTime: !cfg.Feed.ExtendedTime})
	go func() { _ = client.Run(ctx) }()
	go func() { _ = of.Run(ctx) }()
	feedForHub, pollReq, mmProbe, demand, tail = of, client, moomooProbe{c: client}, of, of
	pipeWG.Add(1)
	go pipe(ctx, &pipeWG, of.Events(), core, st)
	// ... existing alpaca/yahoo/moomoo chain construction, appended into dailyChain/intradayChain ...
}
hub.SetFeed(feedForHub)

// prune + boot sysevent + dropped-updates watcher: keep for both (fresh demo.db prune is a no-op)
```
Then build the orchestrator for **both** modes (demo → nil chains + nil tail = chain-less, which `warmStart`-serves the seeded archives; live → the chains built above):
```go
if cfg.Backfill.Enabled || *demo {
	orch = backfill.New(dailyChain, intradayChain, tail, core, st, clock.System{}, backfill.Config{
		IntradayDays: cfg.Backfill.IntradayDays, DailyYears: cfg.Backfill.DailyYears,
		Concurrency: cfg.Backfill.Concurrency, SeedChunk: cfg.Backfill.SeedChunk,
	})
	hubBackfill = func(sym string, done func(ok bool)) { /* unchanged closure */ }
}
var backfillOne func(string)
if hubBackfill != nil { backfillOne = func(sym string) { hubBackfill(sym, nil) } }
hub.SetBackfill(hubBackfill)
startPollers(ctx, cfg, pollReq, demand, hub, uihubClk, st, hasTZVenue(cfg), mmProbe, firstAlpacaProber(vbs), backfillOne, !*demo /*startQuota*/, &scanWG)
```
Move the `watchDroppedUpdates` + `PruneJournal` + `AppendSysEvent("boot","engine up")` lines so they run for both branches (prune is harmless on the fresh demo db). The replay `else` branch (456-468) is unchanged.

- [ ] **Step 5: Widen `startPollers`**

New signature + body (main.go:~707):
```go
func startPollers(ctx context.Context, cfg config.Config, r pollerRequester, demand demandFeeder,
	hub *uihub.Hub, clk clock.Clock, st *store.Store, hasTZ bool, mmProbe rttProber,
	alpacaProbe rttProber, backfillOne func(string), startQuota bool, scanWG *sync.WaitGroup) {

	scanPoller := scan.New(cfg.Scan, r, hub, clk, demand, backfillOne)
	symbols := func() []string { return newsSymbols(scanPoller.PoolSymbols(), hub.ActiveDemandSymbols()) }
	scanWG.Add(1)
	go func() { defer scanWG.Done(); _ = scanPoller.Run(ctx) }()
	go func() { _ = news.New(cfg.News, r, hub, clk, symbols).Run(ctx) }()
	go func() { _ = stockinfo.New(cfg.StockInfo, r, hub, clk, symbols, st).Run(ctx) }()

	var qsrc health.QuotaSource
	if startQuota {
		qp := quota.New(quota.Config{SubWarnHeadroom: cfg.Feed.QuotaWarnHeadroom, HistWarnRemain: cfg.Feed.HistQuotaWarnRemain}, r, hub, clk)
		go func() { _ = qp.Run(ctx) }()
		qsrc = qp
	}
	go func() { _ = health.New(cfg.Health, hub, clk, mmProbe, nil, hasTZ, alpacaProbe, qsrc).Run(ctx) }()
}
```
Note: `scan.New`/`news.New`/`stockinfo.New`/`quota.New` already accept their local one-method `requester` interface (all four are the identical `Request(ctx, uint32, proto.Message)(opend.Frame,error)`), so passing `r pollerRequester` is directly assignable. `demand` (nil in demo) disables the scanner pool (`scan.go:167` guards `if p.feed == nil`). `health.New` tolerates nil `QuotaSource`, nil `alpaca prober`, and nil `probe`. After the Step-4 merge there is a **single** `startPollers` call site (~469), threading `pollReq`/`demand`/`mmProbe` from Step 4 with `firstAlpacaProber(vbs)` (returns nil in demo — all-sim) and `!*demo` for `startQuota`.

- [ ] **Step 6: Add `demoSeedValue` helper**

```go
// demoSeedValue returns the seed to use: the flag if non-zero, else a random
// per-launch seed. Kept off the hot path; determinism in tests comes from
// passing a fixed -demo-seed.
func demoSeedValue(flagSeed int64) int64 {
	if flagSeed != 0 {
		return flagSeed
	}
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand, imported UNALIASED as `rand` (main.go:10)
	return int64(binary.LittleEndian.Uint64(b[:]))
}
```
*(Verified: `crypto/rand` is imported **unaliased** as `rand` in `main.go:10` — used at `exec.NewOrderIDGen(execClk, rand.Reader)` — so the identifier is `rand.Read`, not `crand.Read`. You MUST add `encoding/binary` to the import block; it is not currently imported.)*

- [ ] **Step 7: Update `run.sh` demo mode**

The demo mode no longer takes `[DAY] [SPEED]`. Update the `demo)` case + usage text to accept an optional `[SEED]` and pass `-demo -demo-seed <seed>` (omit `-demo-seed` when no seed given). Drop the `demojournal.Generate`/`genjournal` invocation from the demo path (the engine now self-seeds); keep the temp-dir creation only if the script still pre-makes one, else let the engine own it. Build the UI bundle, then `exec go run ./cmd/etape -dist "$UI_DIR/dist" -demo [-demo-seed N] "$@"`.

- [ ] **Step 8: Build + vet + full engine test run**

Run:
```bash
cd engine && go build ./... && go vet ./... && go test ./...
```
Expected: build clean; all existing suites (including `uihubtest`, `demojournal`, replay smoke) still PASS; new `synth` tests PASS.

- [ ] **Step 9: Commit**

```bash
git add engine/cmd/etape/main.go run.sh
git commit -m "feat(engine): wire synth demo feed as a third boot mode behind -demo"
```

---

## Task 11: Boot integration test + statistical sanity + manual verification

**Files:**
- Create: `engine/internal/synth/stats_test.go` (statistical sanity)
- Modify: `engine/internal/uihubtest/` — add `synth_demo_test.go` mirroring `replay_smoke_test.go`

**Interfaces:** consumes the whole `synth` package + `uihubtest` harness helpers (`openStore`, `forwardMD`, `sendCommand`, `sendQuery`, `waitFrame`, `deterministicReader` — `uihubtest/e2e_test.go`).

- [ ] **Step 1: Statistical sanity test** (`synth/stats_test.go`)

Drive a `Generator` for simulated hours with a fake clock and assert: (a) large-cap drift stays bounded (already in Task 2, extend to the full generator); (b) observed spread per personality stays within `Spread.MinCents..MaxCents*FlushMult`; (c) observed tick intensity per personality is within `[LambdaMin, LambdaMax]` bounds (±tolerance); (d) `bestBid < bestAsk` holds across the whole run for every symbol.

- [ ] **Step 2: Run it** → PASS.

- [ ] **Step 3: Boot integration test** (`uihubtest/synth_demo_test.go`)

Mirror `replay_smoke_test.go` but substitute `synth.New(seed, fake)` + `synth.NewFeed` + `synth.NewRequester` for the replay feed, seed history via `gen.Seed(st, now)`, and reconstruct `markBridge` inline. Assert:
- After `gen.Seed` + a few `EnsureSymbol`s, a `BarSnapshot` query returns warm bars at 1m **and** daily (3 days of 1m + dailies present).
- The movers topic publishes rows consistent with the generator's quotes (drive the scanner poller against `synth.NewRequester`, or assert `Requester.Request(3410)` rows match `QuoteOf` %-change).
- A sim order **fills against the synthetic book** via the book-walk path (submit a marketable order on a universe symbol; assert a fill event with a price consistent with the fed `BookSnapshot` — the same assertion shape as `TestE2EReplayDemoJournal_SimFillsPriceAgainstReplayedBook`).

- [ ] **Step 4: Run it** → `cd engine && go test ./internal/uihubtest/ -run SynthDemo -v` → PASS.

- [ ] **Step 5: Manual verification (record results in the commit/PR body)**

```bash
./run.sh demo            # random universe
./run.sh demo 12345      # pinned seed -> reproducible universe/day
```
On a weekend, confirm in the browser (`http://127.0.0.1:8686`):
- Charts warm at all timeframes (10s/1m/5m/15m/30m/60m/D) with VWAP/EMA/MACD looking real.
- DOM ladder breathing (levels changing, spread widening in fast moves).
- Movers board populated and moving; a `scanner.hit` flash on a new entrant.
- At least one runner observed halting (no ticks, frozen book ~5 min) then resuming.
- Stock Info panel populated (float, shares, 52wk, fictional headlines).
- A practice order fills against the synthetic book (marketable), P&L updates.
- Pan a chart back: history loads archive-first and exhausts cleanly at the archive edge (no crash, no infinite spinner).
- Also confirm **first-run-no-OpenD** graceful boot: run the engine live with no OpenD and no venues (`cd engine && go run ./cmd/etape -no-open`) — UI serves, health shows feed down, no crash-loop. (This is the state a future "Try demo" user lands in.)

- [ ] **Step 6: Commit**

```bash
git add engine/internal/synth/stats_test.go engine/internal/uihubtest/synth_demo_test.go
git commit -m "test(synth): statistical sanity + boot integration (warm history, movers, sim fills)"
```

---

## Self-Review — spec coverage

- **Live synthetic feed replacing replay journal** → Tasks 6–7 + Task 10 boot merge.
- **Mixed universe, per-run personality assignment, fictional US.-prefixed names** → Task 1.
- **Regime-switching price + mean-reversion + LULD halts** → Task 2.
- **Book↔tick consistency (SimBroker fills)** → Tasks 3–4 + Task 11 fill assertion.
- **Quotes/1m bars (prevClose-driven movers %)** → Task 5.
- **Flows 24/7 + ET-midnight rollover** → Task 6.
- **Warm history at boot (~1y dailies, ~3d 1m, ~2h ticks)** → Task 9; served via chain-less `warmStart` (Task 10).
- **Movers/Stock Info/news via unchanged pollers** → Task 8 + `startPollers` widening (Task 10).
- **`-demo` survives; `-demo-day`/`-demo-speed` removed; `-demo-seed` added** → Task 10 Steps 1–2.
- **Fresh temp `demo.db`; never touch `~/.eTape`; creds never loaded** → Task 10 Steps 2–3.
- **`demojournal`/`genjournal` untouched; E2E + replay smoke unaffected** → Task 10 Step 8, Task 11.
- **Determinism (seed + fake clock byte-identical)** → Global Constraints + Tasks 1/2/6 determinism tests.
- **Boot budget ≤3s** → Task 9 timing guard.
- **UI entry (banner, Try-demo, StartDemo)** → **out of scope** (separate plan; blocked on unmerged UI-driven-replay control plane).

**Verification summary:** `cd engine && go build ./... && go vet ./... && go test ./...` must be green (new `synth` suite + untouched existing suites), the `uihubtest` boot integration must show warm history + moving movers + a synthetic-book fill, and the manual `./run.sh demo` checklist must pass on a weekend.

---

## Execution Handoff

On approval: save this plan to `docs/superpowers/plans/2026-07-11-demo-synthetic-data-plan.md`, commit it (`docs(plans): add demo synthetic data implementation plan`, no push), then choose execution:

1. **Subagent-Driven (recommended)** — fresh subagent per task, two-stage review between tasks. REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Consider `/model sonnet` for execution per the phase-router.
2. **Inline Execution** — execute tasks in-session with checkpoints. REQUIRED SUB-SKILL: superpowers:executing-plans.

The generator tasks (1–6) are a clean dependency chain; 7/8/9 fan out from the `Generator`; 10 integrates; 11 verifies. Consider a git worktree for isolation (superpowers:using-git-worktrees).
