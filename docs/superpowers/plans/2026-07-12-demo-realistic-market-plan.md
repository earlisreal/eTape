# Demo Realistic Market (Pressure-Coupled Persistent Book) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the demo generator's "bar-code" price model with a pressure-coupled persistent order book where FV steers flow, book depletion moves price, and (chunk 4) the user's sim orders participate in the synthetic market.

**Architecture:** Two layers per symbol with one-way influence and price emergent at the bottom: a *director* (regimes + leg machine + session phase) produces a latent fair value FV and a flow posture; a *persistent limit-order book* is quoted from a per-side budget around FV±half-spread and is the only price authority; Poisson prints consume book queues with an aggressor mix biased by pressure `pBuy = 0.5 + β·(FV−bookMid)/hs`. Eating liquidity (synthetic or user) moves the touch; the lagged budget refill lets the mid walk toward FV. FV is never rounded and never emitted — every visible price comes from the book.

**Tech Stack:** Go (`engine/internal/synth`, `engine/internal/broker/sim`), stdlib `testing`, `clock.Fake` for deterministic time, `math/rand` per-symbol child streams. No new third-party deps; no ML.

## Global Constraints

Every task's requirements implicitly include this section. Values copied from the spec (`docs/superpowers/specs/2026-07-12-demo-realistic-market-design.md`).

- **Universe: exactly 12 symbols** (`DrawUniverse`, 2 runner / 5 large-cap / 5 mid-cap). Do not change the count.
- **Step cadence: 50 ms live** (`feedTickMs`); all new physics must be `rate × dt` and accrue per inter-print gap so **60 s seeder strides and 50 ms live strides produce matching statistics** (stride invariance — dedicated test in chunk 6).
- **`-demo-seed` determinism preserved** (modulo user interaction). Same seed + fake clock ⇒ byte-identical run.
- **Per-symbol PRNG streams, one-symbol user blast radius** — a hard requirement: `SweepUser`/`PlaceUser`/`CancelUser` draw no randomness and perturb only the touched symbol; the other 11 stay byte-identical to an untouched run (tested in chunk 4).
- **Event surface + journal schema unchanged** — UI never changes: ticks, 10-level `feed.Book`, `feed.Quote`, 1m `feed.Bar`, movers (`RankRows`), fundamentals. The halt event surface stays wire-compatible but never fires.
- **`feed` package imports nothing but stdlib**; `synth` never imports `broker/sim` (chunk-4 port is primitive-typed, satisfied structurally).
- **Lock order (chunk 4): `sim.mu → g.mu`; the generator never calls out** (pull-only `DrainUserFills`).
- **Boot cost stays roughly current** — `TestSeed_WithinBudget` (3 s; ×8 under `-race`) must stay green; re-verified in chunk 6.
- **Laptop perf:** all 12 symbols always step; keep per-step work small (spec §7 budget: ~4–8k ops typical, <20 µs/core).
- **Reviewability:** each chunk lands green independently. Keep the codebase's conventions: `rng *rand.Rand` threaded explicitly (never a package global), `…Locked` suffix + "Caller holds mu." on sim helpers, high doc-comment density that explains *why*.
- **Calibration:** indicative coefficients in this plan (β, refill rates, μ table, SigmaHr, satWeight, spread γ, script weights) are **starting points**; exact thresholds/bands are fixed in the chunk-6 calibration pass. Tests use **production parameters** — the `Vol: 0.5` blind spot (`price_test.go:12`) is the cautionary tale.

---

## File Structure

`engine/internal/synth/` (existing files modified unless noted):

- `universe.go` — `SymbolSpec`: replace `Vol` with `SigmaHr`; set per personality (chunk 1).
- `price.go` — director/FV: `priceState.Mid`→`FV`; dt-correct per-second μ; SigmaHr noise; unrounded FV; delete halt machinery; regimes 7→6; VWAP saturation; posture-skew ramp state (chunks 1, 2).
- `phase.go` — **new** — session-phase multiplier table + `sessionPhase(nowMs)` (chunk 2).
- `book.go` — persistent book: add `bidBudget`/`askBudget` + `quote`; add prune + FV-distance guard; delete `replenish`/`topUp`/`plantRoundWall`/`maxTouchDrift`; keep `rebuildAround` (3 sites), `consume`, `extendSide` (chunk 2).
- `tick.go` — spread-target `hs_t`, pressure `pBuy`, regime skew, Hawkes-lite + urgency λ; rewire `genTicks` interleaved quote/consume loop (chunk 2).
- `levels.go` — **new** — level memory (chunk 3).
- `scripts.go` — **new** — DEFEND/BREAK/FAKEOUT/RETEST + leg machine + pre-market gap drive (chunk 5).
- `generator.go` — per-symbol child `rng`; thread FV/phase/vwap; user-order API + `DrainUserFills` (chunk 4).
- `feed.go` — `Feed.Run` pumps `DrainUserFills` after `StepTo` (chunk 4).
- `seeder.go` — thread `rt.rng`/FV/phase/vwap through coarse+fine passes (chunks 1, 2).
- `requester.go` — catalyst-linked demo headlines (chunk 5).
- `stats_test.go` / `price_test.go` / `book_test.go` / `tick_test.go` / `generator_test.go` / `seeder_test.go` — retune to production params; new acceptance suite (all chunks).

`engine/internal/broker/sim/sim.go` — `Options.Market`, `SetMarket`, `ApplyUserFill`, externally-resting flag (chunk 4).

`engine/cmd/etape/main.go` — post-construction `SetMarket` wiring in the demo branch (chunk 4).

`engine/cmd/synthplot/` — **new** — dev-only eyeball harness (chunk 6).

---

# CHUNK 1 — Physics fix + halt removal (S–M)

**Chunk deliverable:** dt-correct per-second drift, per-√hour SigmaHr vols, unrounded latent FV, per-symbol PRNG streams, VWAP saturation, and all halt machinery deleted. Charts are visibly better on their own (FV drifts smoothly instead of snapping between two rails). Book is still one-way (mid→book via the existing `replenish`) — the bar code is not fully dead until chunk 2.

---

### Task 1.1: Per-symbol PRNG streams

**Files:**
- Modify: `engine/internal/synth/generator.go` — `Generator` struct + doc (98–116), `New` (122–148), `stepSymbol` (204–231), `rolloverSymbol` (290), `kickRunnerGap` (323–345), `symRuntime` (65–96, add `rng` field).
- Modify: `engine/internal/synth/seeder.go` — `Seed` (`rebuildAround` call, ~117), `seedDailyHistory` (`stepPrice` call, ~204), `seedIntraday` (`stepPrice`/`genTicks` calls, ~279/281).
- Test: `engine/internal/synth/generator_test.go`.

**Interfaces:**
- Produces: `symRuntime.rng *rand.Rand` (per-symbol child stream). `Generator` no longer has an `rng` field; the master source lives only as a local in `New`.
- Consumes: nothing new.

- [ ] **Step 1: Write the failing test** — add to `generator_test.go`:

```go
func TestNew_SeedsDistinctPerSymbolStreams(t *testing.T) {
	g := New(7, clock.NewFake(timeMs(1_700_000_000_000)))
	seen := map[*rand.Rand]bool{}
	for _, code := range g.order {
		rt := g.syms[code]
		if rt.rng == nil {
			t.Fatalf("symbol %s has nil rng", code)
		}
		if seen[rt.rng] {
			t.Fatalf("symbol %s shares an rng instance with another symbol", code)
		}
		seen[rt.rng] = true
	}
	if len(seen) != len(g.order) {
		t.Fatalf("got %d distinct streams, want %d", len(seen), len(g.order))
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd engine && go test ./internal/synth/ -run TestNew_SeedsDistinctPerSymbolStreams -v`
Expected: FAIL — `rt.rng` undefined (field does not exist yet).

- [ ] **Step 3: Add the `rng` field to `symRuntime`** (generator.go, in the struct at 65–96):

```go
type symRuntime struct {
	spec  SymbolSpec
	rng   *rand.Rand // per-symbol PRNG child stream (seeded in New, g.order order)
	price *priceState
	book  *bookState
	// ...rest unchanged...
}
```

- [ ] **Step 4: Seed children in `New` and drop the shared `Generator.rng`** — replace the struct field and `New` body:

```go
// Generator is the single stateful simulator for the whole synthetic universe.
// The master source (seeded from New's `seed`) draws the universe and then
// seeds one child *rand.Rand per symbol in g.order (sorted) order; every
// per-symbol step/quote/tick draw uses that child, so perturbing one symbol
// (e.g. a user sweep, chunk 4) cannot shift any other symbol's random
// sequence. All access goes through mu.
type Generator struct {
	mu sync.Mutex

	syms  map[string]*symRuntime
	order []string

	lastStepMs int64
	curDay     string
}

func New(seed int64, clk clock.Clock) *Generator {
	master := rand.New(rand.NewSource(seed))
	specs := DrawUniverse(master)

	nowMs := clk.Now().UnixMilli()

	g := &Generator{
		syms:       make(map[string]*symRuntime, len(specs)),
		order:      make([]string, 0, len(specs)),
		lastStepMs: nowMs,
		curDay:     etDay(nowMs),
	}

	for _, spec := range specs {
		child := rand.New(rand.NewSource(master.Int63()))
		ps := newPriceState(spec)
		g.syms[spec.Code] = &symRuntime{
			spec:      spec,
			rng:       child,
			price:     ps,
			book:      newBook(child, spec, ps.Mid),
			prevClose: spec.PrevClose,
			day1m:     make(map[int64]feed.Bar),
		}
		g.order = append(g.order, spec.Code)
	}
	return g
}
```

- [ ] **Step 5: Replace every `g.rng` with `rt.rng`** in the per-symbol paths:
  - `stepSymbol` (210, 212): `stepPrice(rt.rng, …)`, `genTicks(rt.rng, …)`.
  - `rolloverSymbol` (290): `rt.book.rebuildAround(rt.rng, rt.spec, rt.price.Mid, false)`.
  - `kickRunnerGap` (324, 325, 338): `between(rt.rng, …)`, `rt.rng.Float64()`, `stepPrice(rt.rng, …)`.
  - `seeder.go` `Seed` (~117): `rt.book.rebuildAround(rt.rng, …)`; `seedDailyHistory` (~204 `stepPrice`, **and `seedDayVolume(rt.rng, …)` at ~215**); `seedIntraday` (~279/281): `stepPrice(rt.rng, …)`, `genTicks(rt.rng, …)`.

Verify no `g.rng` references remain: `cd engine && grep -rn 'g\.rng' internal/synth/` → expect no output. This repo-wide grep is the backstop for any site the list above missed.

- [ ] **Step 6: Run the new test and the existing determinism test**

Run: `cd engine && go test ./internal/synth/ -run 'TestNew_SeedsDistinctPerSymbolStreams|TestGenerator_Deterministic_ByteIdentical|TestSeed_' -v`
Expected: PASS (determinism still holds — two runs of the same seed remain byte-identical).

- [ ] **Step 7: Run the full package + build**

Run: `cd engine && go build ./... && go test ./internal/synth/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add engine/internal/synth/generator.go engine/internal/synth/seeder.go engine/internal/synth/generator_test.go
git commit -m "feat(synth): per-symbol PRNG child streams"
```

---

### Task 1.2: Delete halt machinery

**Files:**
- Modify: `price.go` — remove `RegHalt` (enum + `String`), `numRegimes` 7→6, `haltWindowMs`, `pricePoint`, `priceState.HaltUntilMs`, `priceState.win`, `Halted`, `detectHalt`, the halt short-circuit + `detectHalt` call in `stepPrice`, `reversion`'s halt comment, and the Halt row/column from all three `transMatrix` matrices.
- Modify: `tick.go` — `lambda` drop the `RegHalt` case; `genTicks` drop the `ps.Halted(fromMs)` check + comment; `buyProb` comment.
- Modify: `generator.go` — `kickRunnerGap` drop `HaltUntilMs`/`win` resets.
- Test: `price_test.go`, `tick_test.go` — delete halt tests.

**Interfaces:**
- Produces: `Regime` enum with 6 values (`RegQuiet`…`RegFlush`); `numRegimes = 6`; `transMatrix` returns `[6][6]float64`.

- [ ] **Step 1: Find every halt reference** (do this first so nothing is missed):

Run: `cd engine && grep -rn 'RegHalt\|Halted\|detectHalt\|HaltUntilMs\|haltWindowMs\|pricePoint\|\.win\b' internal/synth/`
Expected list to resolve: enum/String/matrices/stepPrice in `price.go`; `lambda`/`genTicks` in `tick.go`; `kickRunnerGap` in `generator.go`; halt-named tests in `price_test.go` (e.g. `TestDetectHalt_*`, `TestStepPrice_Halt*`) and any in `tick_test.go`.

- [ ] **Step 2: Delete the halt tests** identified above (whole test functions that assert freeze/`Halted`/`detectHalt` behavior, e.g. `TestStepPrice_RunnerHaltFreezes` in `price_test.go`). Also delete the halt-only test helper `stepForceMove` (`price_test.go:16-22`), which manipulates `ps.win`/`pricePoint` and won't compile once those are removed, plus any other surviving `pricePoint`/`.win` usage.

- [ ] **Step 3: Edit `price.go` enum + `String` + `numRegimes`:**

```go
const (
	RegQuiet Regime = iota
	RegChop
	RegTrendUp
	RegTrendDown
	RegParabolic
	RegFlush
)

func (r Regime) String() string {
	switch r {
	case RegQuiet:
		return "QUIET"
	case RegChop:
		return "CHOP"
	case RegTrendUp:
		return "TREND_UP"
	case RegTrendDown:
		return "TREND_DOWN"
	case RegParabolic:
		return "PARABOLIC"
	case RegFlush:
		return "FLUSH"
	}
	return "UNKNOWN"
}

const numRegimes = 6
```

- [ ] **Step 4: Delete `haltWindowMs`, `pricePoint`, `detectHalt`, `Halted`; trim `priceState`:**

```go
type priceState struct {
	Mid    float64
	Anchor float64
	Reg    Regime

	DwellLeftMs int64
}
```

Remove the halt short-circuit and the `detectHalt` call from `stepPrice` (drop lines 110–113 and 146–148); the function now always advances.

- [ ] **Step 5: Trim `transMatrix` to 6×6** — drop the last (`Halt`) column and the last (`Halt`) row from each matrix. Row sums stay 1.0 (the Halt column was always 0.00). Result:

```go
func transMatrix(pers Personality) [numRegimes][numRegimes]float64 {
	switch pers {
	case PersRunner:
		return [numRegimes][numRegimes]float64{
			// Quiet, Chop, TrendUp, TrendDown, Parabolic, Flush
			{0.20, 0.41, 0.15, 0.15, 0.05, 0.04},
			{0.10, 0.36, 0.15, 0.15, 0.13, 0.11},
			{0.05, 0.21, 0.40, 0.05, 0.25, 0.04},
			{0.05, 0.21, 0.05, 0.40, 0.04, 0.25},
			{0.02, 0.21, 0.15, 0.03, 0.35, 0.24},
			{0.02, 0.21, 0.03, 0.15, 0.24, 0.35},
		}
	case PersMidCap:
		return [numRegimes][numRegimes]float64{
			{0.35, 0.41, 0.10, 0.10, 0.02, 0.02},
			{0.30, 0.41, 0.12, 0.12, 0.02, 0.03},
			{0.10, 0.31, 0.45, 0.05, 0.05, 0.04},
			{0.10, 0.31, 0.05, 0.45, 0.04, 0.05},
			{0.03, 0.26, 0.25, 0.05, 0.30, 0.11},
			{0.03, 0.26, 0.05, 0.25, 0.11, 0.30},
		}
	default: // PersLargeCap
		return [numRegimes][numRegimes]float64{
			{0.50, 0.36, 0.06, 0.06, 0.01, 0.01},
			{0.35, 0.51, 0.06, 0.06, 0.01, 0.01},
			{0.15, 0.36, 0.40, 0.05, 0.02, 0.02},
			{0.15, 0.36, 0.05, 0.40, 0.02, 0.02},
			{0.05, 0.36, 0.20, 0.05, 0.25, 0.09},
			{0.05, 0.36, 0.05, 0.20, 0.09, 0.25},
		}
	}
}
```

- [ ] **Step 6: Edit `tick.go`** — in `lambda`, delete `if reg == RegHalt { return 0 }`; in `genTicks`, delete the `if ps.Halted(fromMs) { return nil }` block and update the doc comment to drop the Halted reference (keep the `lam <= 0` defensive guard, reworded as a pure divide-by-zero guard). In `buyProb`, change the comment "chop/quiet/halt" → "chop/quiet".

- [ ] **Step 7: Edit `kickRunnerGap`** (generator.go) — delete `rt.price.HaltUntilMs = 0` and `rt.price.win = rt.price.win[:0]`; keep `rt.price.DwellLeftMs = 0`.

- [ ] **Step 8: Build + test**

Run: `cd engine && go build ./... && go test ./internal/synth/`
Expected: PASS. Confirm no halt references remain: `grep -rn 'RegHalt\|Halted\|detectHalt' internal/synth/` → no output.

- [ ] **Step 9: Commit**

```bash
git add engine/internal/synth/price.go engine/internal/synth/tick.go engine/internal/synth/generator.go engine/internal/synth/price_test.go engine/internal/synth/tick_test.go
git commit -m "feat(synth): remove halt/LULD machinery (regimes 7->6)"
```

---

### Task 1.3: Rename `priceState.Mid` → `FV` (mechanical, behavior-identical)

**Files:** `price.go`, `generator.go`, `tick.go`, `seeder.go`, and every `_test.go` referencing `priceState.Mid`. Book helper params named `mid float64` are **not** renamed — only the struct field `priceState.Mid`.

**Interfaces:** Produces `priceState.FV` (replaces `.Mid`); no behavior change (still cent-rounded — the unround lands in 1.4).

- [ ] **Step 1: Find all references:** `cd engine && grep -rn '\.Mid\b\|Mid:' internal/synth/` — expect `price.go` (field decl, `newPriceState`, `stepPrice`), `generator.go` (`New`, `rolloverSymbol`, `kickRunnerGap`), `tick.go` (`genTicks` `b.replenish(rng, spec, ps.Mid)` + doc), `seeder.go` (coarse-pass O/H/L/C tracking), and the tests `stats_test.go`, `generator_test.go` (e.g. `TestGenerator_BigJumpDoesNotCorruptState`, `TestGenerator_StepSymbolClampsLargeDtMs`, `TestKickRunnerGap_*`), `seeder_test.go` (`TestSeed_LeavesGeneratorAtNowMsWithNoSeam`), `tick_test.go` (`TestGenTicks_ExecuteAtTouchAndTurnover`: `ps.Mid, ps.Anchor = 100, 100`). Rename every hit; the grep is the source of truth, not this list.

- [ ] **Step 2: Rename the field** in `price.go`:

```go
type priceState struct {
	FV     float64 // latent fair value: full float64, never rounded, never emitted
	Anchor float64
	Reg    Regime

	DwellLeftMs int64
}
```

`newPriceState`: `FV: spec.Open`. In `stepPrice`, `ps.Mid` → `ps.FV` throughout (leave the `math.Round` line for now — removed in 1.4).

- [ ] **Step 3: Rename at call sites** — `generator.go` (`newBook(child, spec, ps.FV)`, `rt.price.FV` in rollover/kick), `tick.go` (`b.replenish(rng, spec, ps.FV)`), `seeder.go` (all `ps.Mid`→`ps.FV`), test files (`rt.price.FV`).

- [ ] **Step 4: Build + test**

Run: `cd engine && go build ./... && go test ./internal/synth/`
Expected: PASS (pure rename; determinism unchanged). Confirm: `grep -rn 'price\.Mid\|ps\.Mid\|\.price\.Mid' internal/synth/` → no output.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/
git commit -m "refactor(synth): rename priceState.Mid -> FV (latent fair value)"
```

---

### Task 1.4: dt-correct per-second μ + SigmaHr vols + unrounded FV

**Files:**
- Modify: `universe.go` — `SymbolSpec`: replace `Vol float64` with `SigmaHr float64`; set it per personality in `drawSpec`; delete the `Vol` assignments.
- Modify: `price.go` — `driftBps`→`driftPerSec` (per-second fractional units); new noise term using `SigmaHr` and `√(dt/3600)`; remove the `math.Round` cent-snap on `FV` (keep a positive floor); make `reversion` govern QUIET/CHOP only (0 for trends/parabolic/flush).
- Test: `price_test.go` — `spec()` helper uses `SigmaHr`; retune drift/bound tests to production scale.

**Interfaces:**
- Produces: `SymbolSpec.SigmaHr float64` (per-√hour vol). `driftPerSec(reg Regime) float64`. `reversion(reg Regime) float64` now returns 0 for trend/parabolic/flush.

> **Note on ordering:** this task changes `stepPrice`'s signature (adds a `vwap` parameter). To keep the chunk green, Task 1.4 threads `vwap` to **every** caller and pre-existing test, passing `0` (saturation off). Task 1.5 then replaces the `0` at the two live sites (`stepSymbol`, `seedIntraday`) with the real session VWAP.

- [ ] **Step 1: Write the failing test** — trends must build now that drift is dt-scaled and reversion no longer fights them. Add to `price_test.go`:

```go
func TestStepPrice_DtCorrectTrendBuilds(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	s := spec(PersLargeCap) // SigmaHr set by the spec() helper (see Step 5)
	ps := newPriceState(s)
	ps.Reg = RegTrendUp
	ps.DwellLeftMs = 10 * 60_000 // pin the regime for 10 minutes

	start := ps.FV
	now := int64(0)
	for i := 0; i < 600; i++ { // 600 * 1s = 10 min at the live-ish cadence
		now += 1000
		stepPrice(rng, s, ps, 0 /*vwap: no saturation in this test*/, now, 1000)
	}
	if ps.FV <= start*1.001 {
		t.Fatalf("TrendUp did not build: start=%.4f end=%.4f", start, ps.FV)
	}
	// FV is latent and unrounded: it must not be cent-snapped.
	if ps.FV == math.Round(ps.FV*100)/100 && ps.FV != start {
		t.Errorf("FV appears cent-rounded (%.6f); it must stay full precision", ps.FV)
	}
}
```

(This test calls `stepPrice` with the new `vwap` parameter that Step 4 below adds; passing `0` disables saturation.)

- [ ] **Step 2: Run it — expect a compile failure** (`stepPrice` arity / `SigmaHr` missing), then a logic failure once it compiles.

Run: `cd engine && go test ./internal/synth/ -run TestStepPrice_DtCorrectTrendBuilds -v`
Expected: FAIL.

- [ ] **Step 3: Edit `SymbolSpec` + `drawSpec`** (universe.go):

```go
type SymbolSpec struct {
	Code        string
	Pers        Personality
	Open        float64
	PrevClose   float64
	FloatShares int64
	Spread      SpreadProfile

	BookMeanSize  float64
	BookSizeSigma float64

	LambdaMin float64
	LambdaMax float64

	SigmaHr float64 // per-sqrt-hour volatility (replaces Vol); daily sigma ~= SigmaHr*sqrt(6.5)
	GapPct  float64
}
```

In `drawSpec`, replace each `spec.Vol = between(...)` line:
- PersRunner: `spec.SigmaHr = between(rng, 0.05, 0.12)`
- PersLargeCap: `spec.SigmaHr = between(rng, 0.003, 0.006)`
- PersMidCap: `spec.SigmaHr = between(rng, 0.008, 0.018)`

- [ ] **Step 4: Rewrite the physics in `stepPrice`** (price.go). Add the `vwap float64` parameter (used in 1.5; ignored here beyond passing through) and replace the drift/noise/round block:

```go
// stepPrice advances ps by dtMs milliseconds, ending at nowMs. It counts down
// the regime dwell timer, applies dt-correct per-second drift + per-sqrt-hour
// noise to the latent FV, mean-reverts toward Anchor in QUIET/CHOP only (trends
// are bounded by VWAP saturation, not reversion), and clamps FV to a positive
// floor. FV is never cent-rounded — visible prices come only from the book.
func stepPrice(rng *rand.Rand, spec SymbolSpec, ps *priceState, vwap float64, nowMs, dtMs int64) {
	ps.DwellLeftMs -= dtMs
	if ps.DwellLeftMs <= 0 {
		ps.Reg = nextRegime(rng, spec.Pers, ps.Reg)
		ps.DwellLeftMs = dwellDuration(rng, ps.Reg)
	}

	dtSec := float64(dtMs) / 1000
	drift := driftPerSec(ps.Reg) * dtSec
	// (VWAP saturation is grafted onto `drift` here in Task 1.5.)
	noise := rng.NormFloat64() * spec.SigmaHr * math.Sqrt(dtSec/3600)
	change := drift + noise
	if change > maxStepChange {
		change = maxStepChange
	} else if change < -maxStepChange {
		change = -maxStepChange
	}
	ps.FV *= 1 + change

	decay := math.Exp(-reversion(ps.Reg) * dtSec)
	ps.FV = ps.Anchor + (ps.FV-ps.Anchor)*decay
	ps.Anchor *= 1 + rng.NormFloat64()*0.0002

	if ps.FV < priceFloor {
		ps.FV = priceFloor
	}
	if ps.Anchor < priceFloor {
		ps.Anchor = priceFloor
	}
}
```

- [ ] **Step 5: Replace `driftBps` with `driftPerSec` and shrink `reversion`:**

```go
// driftPerSec returns the regime's per-second fractional drift on FV.
// Indicative (calibrated by the acceptance suite): TrendUp +1 bps/s,
// Parabolic +6, Flush -8; Quiet/Chop no directional bias.
func driftPerSec(reg Regime) float64 {
	switch reg {
	case RegTrendUp:
		return 0.0001
	case RegTrendDown:
		return -0.0001
	case RegParabolic:
		return 0.0006
	case RegFlush:
		return -0.0008
	default:
		return 0
	}
}

// reversion returns the per-second Anchor mean-reversion rate. Only QUIET/CHOP
// revert; trends/parabolic/flush are bounded by VWAP saturation (Task 1.5) and
// the daily excursion budget (chunk 5), so they get 0 here — otherwise
// reversion would fight and flatten every trend leg.
func reversion(reg Regime) float64 {
	switch reg {
	case RegQuiet:
		return 0.1
	case RegChop:
		return 0.05
	default:
		return 0
	}
}
```

Update the `spec()` test helper (`price_test.go:11-14`) to (a) set `SigmaHr` instead of `Vol` (production-scale, e.g. large-cap `SigmaHr: 0.005`) and drop the `Vol` field, and (b) **fill in the currently-unset `BookMeanSize`/`BookSizeSigma`** (e.g. `BookMeanSize: 1000, BookSizeSigma: 300`) — the fixture omits them today, and the chunk-2 book tests (`TestBook_QuoteRefillsDepletedSideTowardFV`, the impact/guard tests) reference `s.BookMeanSize` directly. Existing book/tick tests assert book *invariants*, not exact sizes, so this is safe; retune any that happen to assert an exact size. Retune any drift-bound assertions (e.g. the old 0.3×–3× band) to production scale with generous bounds — mark them "finalized in chunk 6".

- [ ] **Step 6: Thread the new `vwap` parameter (pass `0`) to every caller and pre-existing test** — Task 1.4 must leave the package compiling:
  - `generator.go` `stepSymbol` (~210): `stepPrice(rt.rng, rt.spec, rt.price, 0, nowMs, dtMs)`.
  - `generator.go` `kickRunnerGap` (~338): `stepPrice(rt.rng, rt.spec, rt.price, 0, nowMs, 0)`.
  - `seeder.go` `seedDailyHistory` (~204): `stepPrice(rt.rng, rt.spec, ps, 0, subNext, subNext-sub)`.
  - `seeder.go` `seedIntraday` (~279): `stepPrice(rt.rng, rt.spec, rt.price, 0, next, next-cur)` (or the actual local names).
  - pre-existing `price_test.go` tests that call the 5-arg form: `TestStepPrice_DeterministicAndCentSnapped` (~32), `TestStepPrice_BoundedDriftOverHours` (~52), `TestStepPrice_DayScaleReversionConverges` (~106), `TestStepPrice_ExtremeDrawCapped` (~171) — insert `0` in the new `vwap` position. Note: `TestStepPrice_DeterministicAndCentSnapped` asserts cent-snapping, which this task removes — retune it to assert the FV is *not* cent-snapped (or delete the snap assertion).

- [ ] **Step 7: Run + build**

Run: `cd engine && go test ./internal/synth/ -run TestStepPrice -v && go build ./...`
Expected: PASS. Confirm `Vol` is gone: `grep -rn '\bVol\b' internal/synth/` → no output (or only comments).

- [ ] **Step 8: Run the full package** — retune any other test that fails on the new scale (e.g. `stats_test.go` drift ratios). Keep bands generous.

Run: `cd engine && go test ./internal/synth/`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add engine/internal/synth/
git commit -m "feat(synth): dt-correct per-second drift + per-sqrt-hour SigmaHr vols; unrounded FV"
```

---

### Task 1.5: VWAP saturation backstop

**Files:**
- Modify: `tick.go` — add `sessionAgg.vwap()`.
- Modify: `price.go` — apply saturation to `drift` in trend/parabolic regimes; add `satWeight`.
- Modify: `generator.go` — `stepSymbol` computes `rt.sess.vwap()` and passes it to `stepPrice`; `kickRunnerGap` passes `0`.
- Modify: `seeder.go` — coarse pass passes `0`; fine pass passes `rt.sess.vwap()`.
- Test: `price_test.go`.

**Interfaces:**
- Consumes: `stepPrice(rng, spec, ps, vwap float64, nowMs, dtMs)` (parameter added in 1.4).
- Produces: `func (sess *sessionAgg) vwap() float64`; `func satWeight(spec SymbolSpec) float64`.

- [ ] **Step 1: Write the failing test** (`price_test.go`):

```go
func TestStepPrice_VWAPSaturationBoundsUptrend(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	s := spec(PersLargeCap)
	ps := newPriceState(s)
	ps.Reg = RegTrendUp
	ps.DwellLeftMs = 60 * 60_000 // pin TrendUp for an hour

	vwap := ps.FV // fixed reference well below where an unbounded trend would run
	now := int64(0)
	for i := 0; i < 3600; i++ {
		now += 1000
		stepPrice(rng, s, ps, vwap, now, 1000)
	}
	ext := (ps.FV - vwap) / vwap
	if ext > 0.20 { // large cap: extension must saturate well under 20%
		t.Fatalf("VWAP saturation failed to bound uptrend: extension=%.3f", ext)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (drift not yet saturated; extension runs past the bound).

Run: `cd engine && go test ./internal/synth/ -run TestStepPrice_VWAPSaturationBoundsUptrend -v`

- [ ] **Step 3: Add `sessionAgg.vwap()`** (tick.go):

```go
// vwap returns the session volume-weighted average price, or 0 when no volume
// has printed yet (caller treats 0 as "no saturation reference").
func (sess *sessionAgg) vwap() float64 {
	if sess.Vol <= 0 {
		return 0
	}
	return sess.Turnover / float64(sess.Vol)
}
```

- [ ] **Step 4: Graft saturation onto `drift` in `stepPrice`** — replace the `// (VWAP saturation ...)` comment line from 1.4:

```go
	drift := driftPerSec(ps.Reg) * dtSec
	if vwap > 0 {
		switch ps.Reg {
		case RegTrendUp, RegParabolic:
			if ext := (ps.FV - vwap) / vwap; ext > 0 {
				drift *= 1 - satWeight(spec)*ext
			}
		case RegTrendDown, RegFlush:
			if ext := (vwap - ps.FV) / vwap; ext > 0 {
				drift *= 1 - satWeight(spec)*ext
			}
		}
	}
```

Add `satWeight` (price.go):

```go
// satWeight scales VWAP-extension saturation per personality: drift zeroes at
// extension 1/satWeight and reverses beyond it. Runners tolerate far more
// extension than large caps. Indicative; calibrated in chunk 6.
func satWeight(spec SymbolSpec) float64 {
	switch spec.Pers {
	case PersRunner:
		return 3.0 // ~33% extension zeroes drift
	case PersMidCap:
		return 8.0
	default:
		return 20.0 // ~5% extension zeroes drift
	}
}
```

- [ ] **Step 5: Replace the `0` placeholder (from Task 1.4) with the real session VWAP at the two live sites** (`kickRunnerGap` and the coarse `seedDailyHistory` stay `0` — no meaningful session yet):
  - `generator.go` `stepSymbol`: `vwap := rt.sess.vwap()` then `stepPrice(rt.rng, rt.spec, rt.price, vwap, nowMs, dtMs)`.
  - `seeder.go` fine `seedIntraday`: `stepPrice(rt.rng, rt.spec, rt.price, rt.sess.vwap(), next, next-cur)` — note `seedIntraday` operates on `rt.price` directly (it has no local `ps`, unlike `seedDailyHistory`).

- [ ] **Step 6: Run + build + full package**

Run: `cd engine && go test ./internal/synth/ && go build ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add engine/internal/synth/
git commit -m "feat(synth): VWAP-extension saturation backstop on trend drift"
```

**Chunk 1 gate:** `cd engine && go test ./... && go vet ./...` green; run the demo (`go run ./cmd/etape -demo -demo-seed 42`) and eyeball a chart — FV should drift smoothly, not snap between two rails. The DOM is still a moving wall (persistent book is chunk 2).

---

# CHUNK 2 — Persistent book + session curve (L)

**Chunk deliverable:** the ladder is never re-anchored to mid again. Depth is quoted from a per-side budget around FV±`hs_t`; depletion moves the touch and stays gone until the budget refills; the aggressor mix comes from pressure `pBuy`; λ gains Hawkes-lite bursts, urgency scaling, and the session-phase multiplier; prune + FV-distance guard + per-window event cap bound pathologies. **This kills the bar code** (headline acceptance test in Task 2.8).

**Strategy:** build the new pieces (`sessionPhase`, `halfSpreadTarget`, `quote`, pressure `pBuy`, `lambdaEff`) *alongside* the existing `replenish`/`buyProb` path so every task stays green; Task 2.6 flips `genTicks` onto them; Task 2.7 deletes the dead code and adds the guards.

---

### Task 2.1: Session-phase multiplier curve

**Files:** Create `engine/internal/synth/phase.go`; test `engine/internal/synth/phase_test.go`.

**Interfaces:**
- Produces: `type phaseMul struct { lambda, depth, spread float64 }`; `func sessionPhase(nowMs int64) phaseMul`.

- [ ] **Step 1: Write the failing test** (`phase_test.go`):

```go
package synth

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

func etMs(t *testing.T, hh, mm int) int64 {
	t.Helper()
	// 2026-07-13 is a Monday; pick any RTH-bearing weekday.
	tm := time.Date(2026, 7, 13, hh, mm, 0, 0, session.Loc())
	return tm.UnixMilli()
}

func TestSessionPhase_ThinPreMarketFullRTH(t *testing.T) {
	pre := sessionPhase(etMs(t, 8, 0))    // 08:00 ET pre-market
	rth := sessionPhase(etMs(t, 10, 30))  // 10:30 ET full RTH
	night := sessionPhase(etMs(t, 2, 0))  // 02:00 ET overnight

	if !(pre.lambda < rth.lambda) {
		t.Errorf("pre-market lambda %.3f should be < RTH %.3f", pre.lambda, rth.lambda)
	}
	if !(pre.depth < rth.depth) {
		t.Errorf("pre-market depth %.3f should be < RTH %.3f", pre.depth, rth.depth)
	}
	if !(pre.spread > rth.spread) {
		t.Errorf("pre-market spread %.3f should be > RTH %.3f", pre.spread, rth.spread)
	}
	if night.lambda != 0 {
		t.Errorf("overnight lambda should be 0, got %.3f", night.lambda)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`sessionPhase` undefined).

- [ ] **Step 3: Implement `phase.go`:**

```go
// This file implements the intraday session-phase curve: a piecewise-linear
// multiplier table over 04:00-20:00 ET that scales arrival rates, book-depth
// targets, and spread targets so the demo shows a thin, wide pre-market, a
// ramp into the 09:30 open, a midday lull, a close ramp, and a post-market
// decay. Overnight (20:00-04:00) flow is nil. Values are indicative; the
// chunk-6 calibration pass finalizes them (pre-market-profile acceptance test).
package synth

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// phaseMul holds the session-phase scaling multipliers for a point in time.
type phaseMul struct {
	lambda float64 // arrival-rate multiplier
	depth  float64 // book-depth-target multiplier
	spread float64 // spread-target (ceiling) multiplier
}

type phaseAnchor struct {
	min                   int // minutes since ET midnight
	lambda, depth, spread float64
}

// phaseAnchors are lerp control points, ascending by minute.
var phaseAnchors = []phaseAnchor{
	{0, 0.00, 0.20, 3.0},    // 00:00 overnight
	{240, 0.00, 0.20, 3.0},  // 04:00 pre-market opens
	{245, 0.05, 0.20, 2.5},  // 04:05 first pre-market flow
	{569, 0.30, 0.40, 1.6},  // 09:29 pre-open peak
	{570, 0.60, 0.55, 1.4},  // 09:30 open
	{600, 1.00, 1.00, 1.0},  // 10:00 full RTH
	{690, 1.00, 1.00, 1.0},  // 11:30
	{750, 0.65, 0.90, 1.1},  // 12:30 midday lull
	{840, 0.70, 1.00, 1.05}, // 14:00
	{955, 1.20, 1.00, 1.0},  // 15:55 close ramp peak
	{960, 0.90, 0.80, 1.1},  // 16:00 close
	{1080, 0.10, 0.30, 2.0}, // 18:00 post-market decay
	{1200, 0.00, 0.20, 3.0}, // 20:00 post-market ends
	{1440, 0.00, 0.20, 3.0}, // 24:00
}

// sessionPhase returns the phaseMul for nowMs's ET wall-clock time.
func sessionPhase(nowMs int64) phaseMul {
	t := time.UnixMilli(nowMs).In(session.Loc())
	m := t.Hour()*60 + t.Minute()
	for i := 1; i < len(phaseAnchors); i++ {
		a, b := phaseAnchors[i-1], phaseAnchors[i]
		if m <= b.min {
			f := 0.0
			if b.min > a.min {
				f = float64(m-a.min) / float64(b.min-a.min)
			}
			return phaseMul{
				lambda: a.lambda + f*(b.lambda-a.lambda),
				depth:  a.depth + f*(b.depth-a.depth),
				spread: a.spread + f*(b.spread-a.spread),
			}
		}
	}
	return phaseMul{lambda: 0, depth: 0.20, spread: 3.0}
}
```

- [ ] **Step 4: Run + build**

Run: `cd engine && go test ./internal/synth/ -run TestSessionPhase -v && go build ./...`
Expected: PASS. (Function is not wired into behavior yet — Go does not flag unused package-level funcs.)

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/phase.go engine/internal/synth/phase_test.go
git commit -m "feat(synth): intraday session-phase multiplier curve"
```

---

### Task 2.2: `rv1m` EWMA + spread-target function

**Files:**
- Modify: `generator.go` — add `rv1m`, `prevBarClose` to `symRuntime`; update them on 1m-bar close in `stepSymbol`.
- Modify: `tick.go` — add `halfSpreadTarget`, `spreadRegimeMult`, `spreadGamma`.
- Test: `tick_test.go`.

**Interfaces:**
- Produces: `symRuntime.rv1m float64`, `symRuntime.prevBarClose float64`; `func halfSpreadTarget(spec SymbolSpec, reg Regime, phase phaseMul, rv1m float64) float64`.

- [ ] **Step 1: Write the failing test** (`tick_test.go`):

```go
func TestHalfSpreadTarget_WidensWithVolAndClampsToProfile(t *testing.T) {
	s := spec(PersLargeCap) // Spread{MinCents:1, MaxCents:5, FlushMult:4} in the helper
	full := phaseMul{lambda: 1, depth: 1, spread: 1}

	calm := halfSpreadTarget(s, RegQuiet, full, 0)
	vol := halfSpreadTarget(s, RegQuiet, full, 1e-4) // high realized 1m variance

	minHs := float64(s.Spread.MinCents) / 100
	ceil := float64(s.Spread.MaxCents) / 100 * spreadRegimeMult(RegQuiet) * full.spread
	if calm < minHs-1e-9 {
		t.Errorf("calm hs %.4f below MinCents floor %.4f", calm, minHs)
	}
	if vol <= calm {
		t.Errorf("hs should widen with rv1m: calm=%.4f vol=%.4f", calm, vol)
	}
	if vol > ceil+1e-9 {
		t.Errorf("hs %.4f exceeded ceiling %.4f", vol, ceil)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`halfSpreadTarget` undefined).

- [ ] **Step 3: Implement in `tick.go`:**

```go
const spreadGamma = 0.8

// spreadRegimeMult widens the spread ceiling in fast/volatile regimes.
func spreadRegimeMult(reg Regime) float64 {
	switch reg {
	case RegFlush, RegParabolic:
		return 2.0
	case RegTrendUp, RegTrendDown:
		return 1.3
	default:
		return 1.0
	}
}

// halfSpreadTarget returns the target half-spread in dollars: a MinCents base
// widened by realized 1m volatility (rv1m is an EWMA of squared 1m returns),
// clamped to [MinCents, MaxCents * regime-posture * phase-posture]. The book
// narrows toward this only by quoting one tick inside per spend (quote()),
// never by teleporting. Indicative gamma; calibrated in chunk 6.
func halfSpreadTarget(spec SymbolSpec, reg Regime, phase phaseMul, rv1m float64) float64 {
	base := math.Max(0.01, float64(spec.Spread.MinCents)/100)
	hs := base + spreadGamma*math.Sqrt(rv1m)*spec.Open
	ceil := float64(spec.Spread.MaxCents) / 100 * spreadRegimeMult(reg) * phase.spread
	if ceil < base {
		ceil = base
	}
	if hs > ceil {
		hs = ceil
	}
	if hs < base {
		hs = base
	}
	return hs
}
```

- [ ] **Step 4: Track `rv1m` on 1m-bar close** — add fields to `symRuntime` (`rv1m float64`, `prevBarClose float64`) and update in `stepSymbol`'s bar-close block (generator.go 218–223):

```go
	for _, tk := range ticks {
		if closed := rt.bar.add(tk); closed != nil {
			rt.day1m[closed.BucketMs] = *closed
			rt.pendingBars = append(rt.pendingBars, *closed)
			if rt.prevBarClose > 0 {
				r := (closed.C - rt.prevBarClose) / rt.prevBarClose
				const rvAlpha = 0.2
				rt.rv1m = rvAlpha*r*r + (1-rvAlpha)*rt.rv1m
			}
			rt.prevBarClose = closed.C
		}
	}
```

- [ ] **Step 5: Run + build + full package**

Run: `cd engine && go test ./internal/synth/ && go build ./...`
Expected: PASS. (`halfSpreadTarget` used only by its test until Task 2.6; `rv1m` used in 2.6.)

- [ ] **Step 6: Commit**

```bash
git add engine/internal/synth/tick.go engine/internal/synth/generator.go engine/internal/synth/tick_test.go
git commit -m "feat(synth): rv1m EWMA + volatility-widened spread target"
```

---

### Task 2.3: Budget quoting (persistent book)

**Files:**
- Modify: `book.go` — add `bidBudget`, `askBudget` to `bookState`; add `quote`, `quoteSide`, `targetSize`, `refillRate`, `baseRefillPerSec`.
- Test: `book_test.go`.

**Interfaces:**
- Produces: `func (b *bookState) quote(rng *rand.Rand, spec SymbolSpec, reg Regime, phase phaseMul, fv, hs, dtSec float64)`; helpers `quoteSide`, `targetSize`, `refillRate`.
- Consumes: `phaseMul` (2.1), existing `consume`/`extendSide`/`fixCrossed`/`ordersFor`/`tickStep`/`round2`/`priceFloor`/`bookDepth`.

- [ ] **Step 1: Write the failing test** (`book_test.go`) — depletion stays gone until budget refills, and refilling tightens the touch toward FV±hs without teleporting:

```go
func TestBook_QuoteRefillsDepletedSideTowardFV(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	s := spec(PersLargeCap)
	fv := 100.0
	hs := 0.02
	b := newBook(rng, s, fv)

	// Sweep the asks hard; the touch should move up (asks eaten).
	beforeAsk, _ := b.best2ask()
	b.consume(feed.Buy, 5*int64(s.BookMeanSize))
	afterAsk, _ := b.best2ask()
	if !(afterAsk >= beforeAsk) {
		t.Fatalf("sweep should not lower best ask: before=%.2f after=%.2f", beforeAsk, afterAsk)
	}

	// Quote over 2 seconds of budget; the ask touch should recover toward fv+hs.
	target := round2(fv + hs)
	for i := 0; i < 40; i++ {
		b.quote(rng, s, RegQuiet, phaseMul{lambda: 1, depth: 1, spread: 1}, fv, hs, 0.05)
	}
	_, ask := b.best()
	if ask > afterAsk+1e-9 {
		t.Errorf("quote should tighten the ask, not widen it: %.2f -> %.2f", afterAsk, ask)
	}
	if ask > target+0.05 {
		t.Errorf("ask %.2f did not recover toward target %.2f after refill", ask, target)
	}
	assertBookInvariants(t, b)
}
```

Add a tiny whitebox helper to `book_test.go` if not present: `func (b *bookState) best2ask() (float64, int64) { if len(b.asks)==0 { return 0,0 }; return b.asks[0].Price, b.asks[0].Size }` (or inline `b.asks[0]`).

- [ ] **Step 2: Run — expect FAIL** (`quote` undefined).

- [ ] **Step 3: Add budget fields + `quote`/`quoteSide`/helpers to `book.go`:**

```go
type bookState struct {
	bids []level
	asks []level

	bidBudget float64 // accrued replenishment budget (shares) not yet quoted
	askBudget float64
}

// baseRefillPerSec is the per-second share-refill baseline, as a fraction of
// BookMeanSize. Regime asymmetry and phase depth scale it. Indicative.
const baseRefillPerSec = 0.5

// refillRate returns a side's per-second share-refill rate for a regime.
// The aggressed side starves (air pockets): FLUSH starves bids, PARABOLIC
// starves asks. Calibrated in chunk 6.
func refillRate(spec SymbolSpec, reg Regime, bid bool) float64 {
	base := spec.BookMeanSize * baseRefillPerSec
	switch reg {
	case RegFlush:
		if bid {
			return base * 0.3
		}
		return base * 1.2
	case RegParabolic:
		if bid {
			return base * 1.2
		}
		return base * 0.3
	case RegTrendUp:
		if bid {
			return base * 1.1
		}
		return base * 0.7
	case RegTrendDown:
		if bid {
			return base * 0.7
		}
		return base * 1.1
	default:
		return base
	}
}

// targetSize is the desired resting size at depth index i (0 = touch); deeper
// levels are fatter. Scaled by phase depth.
func targetSize(spec SymbolSpec, phase phaseMul, i int) int64 {
	sz := int64(spec.BookMeanSize * phase.depth * (1 + 0.15*float64(i)))
	if sz < 1 {
		sz = 1
	}
	return sz
}

// quote accrues dt-scaled budget per side and spends it to move each side
// toward its target depth profile around fv +/- hs, WITHOUT re-anchoring the
// existing touch. A swept level stays gone until budget refills it (depletion
// moves price); narrowing happens only one tick inside per spend, never by
// teleport. Ends by re-asserting a non-crossed touch.
func (b *bookState) quote(rng *rand.Rand, spec SymbolSpec, reg Regime, phase phaseMul, fv, hs, dtSec float64) {
	b.bidBudget += refillRate(spec, reg, true) * phase.depth * dtSec
	b.askBudget += refillRate(spec, reg, false) * phase.depth * dtSec

	bidTarget := round2(fv - hs)
	askTarget := round2(fv + hs)
	if bidTarget < priceFloor {
		bidTarget = priceFloor
	}
	if askTarget < bidTarget+0.01 {
		askTarget = bidTarget + 0.01
	}

	b.bids, b.bidBudget = quoteSide(rng, spec, phase, b.bids, bidTarget, b.bidBudget, false)
	b.asks, b.askBudget = quoteSide(rng, spec, phase, b.asks, askTarget, b.askBudget, true)
	b.fixCrossed(rng, spec)
}

// quoteSide spends budget to move a side toward its target profile: (1) improve
// the touch by at most one tick toward `target` per call (never teleporting);
// (2) top up in-profile levels toward targetSize; (3) extend depth to bookDepth.
// ascending=true for asks. Returns the updated side and leftover budget. The
// side is never left empty.
func quoteSide(rng *rand.Rand, spec SymbolSpec, phase phaseMul, side []level, target, budget float64, ascending bool) ([]level, float64) {
	if len(side) == 0 {
		sz := targetSize(spec, phase, 0)
		if budget >= float64(sz) {
			side = append(side, level{Price: target, Size: sz, Orders: ordersFor(sz)})
			budget -= float64(sz)
		} else {
			side = append(side, level{Price: target, Size: 1, Orders: 1}) // never empty
		}
	} else {
		touch := side[0].Price
		wider := (ascending && touch > target) || (!ascending && touch < target)
		if wider {
			var newTouch float64
			if ascending {
				newTouch = round2(touch - 0.01)
				if newTouch < target {
					newTouch = target
				}
			} else {
				newTouch = round2(touch + 0.01)
				if newTouch > target {
					newTouch = target
				}
			}
			sz := targetSize(spec, phase, 0)
			if budget >= float64(sz) && newTouch >= priceFloor {
				side = append([]level{{Price: newTouch, Size: sz, Orders: ordersFor(sz)}}, side...)
				budget -= float64(sz)
			}
		}
	}

	for i := range side {
		want := targetSize(spec, phase, i)
		if side[i].Size < want {
			add := int64(math.Min(float64(want-side[i].Size), budget))
			if add > 0 {
				side[i].Size += add
				side[i].Orders = ordersFor(side[i].Size)
				budget -= float64(add)
			}
		}
	}

	for len(side) < bookDepth {
		want := targetSize(spec, phase, len(side))
		if budget < float64(want) {
			break
		}
		last := side[len(side)-1].Price
		var px float64
		if ascending {
			px = round2(last + tickStep(rng))
		} else {
			px = round2(last - tickStep(rng))
			if px < priceFloor {
				break
			}
		}
		side = append(side, level{Price: px, Size: want, Orders: ordersFor(want)})
		budget -= float64(want)
	}

	return side, budget
}
```

- [ ] **Step 4: Run + build** (existing `replenish` still runs in `genTicks`; `quote` is exercised only by its test until Task 2.6).

Run: `cd engine && go test ./internal/synth/ -run TestBook -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/book.go engine/internal/synth/book_test.go
git commit -m "feat(synth): budget-quoted persistent book (quote, no re-anchor)"
```

---

### Task 2.4: Pressure `pBuy` + regime skew + posture-lead ramp

**Files:**
- Modify: `price.go` — add `SkewCur`, `SkewTarget` to `priceState`; ramp them in `stepPrice`; add `postureTau`.
- Modify: `tick.go` — add `pressureBeta`, `pressureSkew`, `pBuy`, `pickDirectionPressure` (alongside the old `buyProb`/`pickDirection`).
- Test: `tick_test.go`, `price_test.go`.

**Interfaces:**
- Produces: `priceState.SkewCur/SkewTarget float64`; `func pressureSkew(reg Regime) float64`; `func pBuy(fv, bookMid, hs, skewRamped float64) float64`; `func pickDirectionPressure(rng *rand.Rand, fv, bookMid, hs, skewCur float64) feed.Direction`.

- [ ] **Step 1: Write the failing tests** (`tick_test.go`):

```go
func TestPBuy_BiasesTowardFV(t *testing.T) {
	hs := 0.02
	up := pBuy(100.05, 100.00, hs, 0)   // FV above book mid => buy-heavy
	dn := pBuy(99.95, 100.00, hs, 0)    // FV below => sell-heavy
	flat := pBuy(100.00, 100.00, hs, 0) // aligned => balanced
	if !(up > flat && flat > dn) {
		t.Fatalf("pBuy not monotone in (FV-bookMid): up=%.3f flat=%.3f dn=%.3f", up, flat, dn)
	}
	if flat < 0.49 || flat > 0.51 {
		t.Errorf("aligned pBuy should be ~0.5, got %.3f", flat)
	}
	// clamped to [0.05, 0.95]
	if got := pBuy(200, 100, hs, 0.5); got > 0.95+1e-9 {
		t.Errorf("pBuy not clamped high: %.3f", got)
	}
}
```

`price_test.go` (posture ramp lags the regime skew):

```go
func TestStepPrice_PostureSkewRampsGradually(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	s := spec(PersLargeCap)
	ps := newPriceState(s)
	ps.Reg = RegTrendUp
	ps.DwellLeftMs = 60_000
	// One 50ms step: skew target jumps to +0.15 but SkewCur lags well behind.
	stepPrice(rng, s, ps, 0, 50, 50)
	if ps.SkewTarget <= 0 {
		t.Fatalf("SkewTarget should be positive in TrendUp, got %.3f", ps.SkewTarget)
	}
	if ps.SkewCur >= ps.SkewTarget*0.5 {
		t.Errorf("SkewCur should lag SkewTarget after one 50ms step: cur=%.4f target=%.4f", ps.SkewCur, ps.SkewTarget)
	}
}
```

- [ ] **Step 2: Run — expect FAIL.**

- [ ] **Step 3: Add pressure functions to `tick.go`:**

```go
const pressureBeta = 0.2

// pressureSkew is the regime aggressor-mix bias m(reg) added to the 0.5
// baseline buy-probability (replaces buyProb).
func pressureSkew(reg Regime) float64 {
	switch reg {
	case RegTrendUp, RegParabolic:
		return 0.15
	case RegTrendDown, RegFlush:
		return -0.15
	default:
		return 0
	}
}

// pBuy is the per-print aggressor buy-probability: 0.5, plus a proportional
// term pulling flow toward FV (FV above book mid => buy-heavy => asks eaten =>
// mid walks up toward FV), plus the (ramped) regime skew. Clamped [0.05,0.95].
func pBuy(fv, bookMid, hs, skewRamped float64) float64 {
	if hs <= 0 {
		hs = 0.01
	}
	p := 0.5 + pressureBeta*(fv-bookMid)/hs + skewRamped
	if p < 0.05 {
		p = 0.05
	}
	if p > 0.95 {
		p = 0.95
	}
	return p
}

// pickDirectionPressure samples an aggressor side: neutralProb inside cross,
// else Buy/Sell weighted by pBuy.
func pickDirectionPressure(rng *rand.Rand, fv, bookMid, hs, skewCur float64) feed.Direction {
	if rng.Float64() < neutralProb {
		return feed.Neutral
	}
	if rng.Float64() < pBuy(fv, bookMid, hs, skewCur) {
		return feed.Buy
	}
	return feed.Sell
}
```

- [ ] **Step 4: Add ramp state + ramp in `stepPrice`** (`price.go`). Add fields to `priceState`:

```go
	SkewCur    float64 // current (ramped) aggressor skew
	SkewTarget float64 // target skew for the current regime
```

Add `const postureTau = 2.0` and, at the end of `stepPrice` (after the FV/Anchor updates), ramp:

```go
	// Posture leads flow: quoting posture switches on the regime immediately
	// (quote() reads Reg directly), but the aggressor mix ramps in over ~postureTau
	// seconds, so DOM imbalance visibly precedes the tape turning.
	ps.SkewTarget = pressureSkew(ps.Reg)
	ramp := 1 - math.Exp(-dtSec/postureTau)
	ps.SkewCur += (ps.SkewTarget - ps.SkewCur) * ramp
```

- [ ] **Step 5: Run + build + full package**

Run: `cd engine && go test ./internal/synth/ && go build ./...`
Expected: PASS (old `buyProb`/`pickDirection` still power `genTicks` until 2.6).

- [ ] **Step 6: Commit**

```bash
git add engine/internal/synth/tick.go engine/internal/synth/price.go engine/internal/synth/tick_test.go engine/internal/synth/price_test.go
git commit -m "feat(synth): pressure pBuy + regime skew + posture-lead ramp"
```

---

### Task 2.5: Hawkes-lite self-excitation + urgency λ

**Files:**
- Modify: `price.go` — add `Hawkes float64` to `priceState`; add `hawkesMax`.
- Modify: `tick.go` — add `lambdaEff`, `hawkesDecayTau`, `hawkesJump`, `urgencyCap`.
- Test: `tick_test.go`.

**Interfaces:**
- Produces: `priceState.Hawkes float64`; `func lambdaEff(spec SymbolSpec, reg Regime, phase phaseMul, hawkes, urgency float64) float64`; constants `hawkesDecayTau`, `hawkesJump`, `hawkesMax`, `urgencyCap`.

- [ ] **Step 1: Write the failing test** (`tick_test.go`):

```go
func TestLambdaEff_BoostedByHawkesAndUrgencyScaledByPhase(t *testing.T) {
	s := spec(PersLargeCap)
	full := phaseMul{lambda: 1, depth: 1, spread: 1}
	thin := phaseMul{lambda: 0.2, depth: 0.3, spread: 2}

	baseCalm := lambdaEff(s, RegChop, full, 0, 0)
	boosted := lambdaEff(s, RegChop, full, 1.0, 0.5)
	if boosted <= baseCalm {
		t.Errorf("hawkes+urgency should raise lambda: base=%.3f boosted=%.3f", baseCalm, boosted)
	}
	if lambdaEff(s, RegChop, thin, 0, 0) >= baseCalm {
		t.Errorf("thin phase should lower lambda below full-RTH base")
	}
	// urgency is capped
	capped := lambdaEff(s, RegChop, full, 0, 100)
	ceil := lambda(s, RegChop) * full.lambda * urgencyCap
	if capped > ceil+1e-6 {
		t.Errorf("urgency not capped: %.3f > %.3f", capped, ceil)
	}
}
```

- [ ] **Step 2: Run — expect FAIL.**

- [ ] **Step 3: Implement in `tick.go` (+ `hawkesMax` in `price.go`):**

```go
const (
	hawkesDecayTau = 5.0 // seconds: self-excitation decay
	hawkesJump     = 0.4 // per-print excitation increment
	urgencyCap     = 2.0 // max urgency multiplier
)

// lambdaEff is the effective arrival rate: the regime baseline scaled by the
// session phase, boosted by Hawkes self-excitation (recent prints) and urgency
// (FV/book-mid divergence pulling tape speed up ahead of a move). hawkes is
// assumed already clamped to hawkesMax by the caller.
func lambdaEff(spec SymbolSpec, reg Regime, phase phaseMul, hawkes, urgency float64) float64 {
	u := 1 + urgency
	if u > urgencyCap {
		u = urgencyCap
	}
	return lambda(spec, reg) * phase.lambda * (1 + hawkes) * u
}
```

In `price.go`: add `Hawkes float64` to `priceState` and `const hawkesMax = 3.0` (bounds the λ multiplier so a flush cascade cannot run away).

- [ ] **Step 4: Run + build + full package**

Run: `cd engine && go test ./internal/synth/ && go build ./...`
Expected: PASS (wired in 2.6).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/tick.go engine/internal/synth/price.go engine/internal/synth/tick_test.go
git commit -m "feat(synth): Hawkes-lite + urgency-scaled effective lambda"
```

---

### Task 2.6: Rewire `genTicks` onto the pressure-coupled loop (the flip)

**Files:**
- Modify: `tick.go` — rewrite `genTicks` to interleave budget quoting, pressure-driven direction, Hawkes/urgency λ; new signature adds `phase phaseMul`; remove the post-print `replenish`.
- Modify: `generator.go` — `stepSymbol` computes `phase := sessionPhase(nowMs)` and passes it.
- Modify: `seeder.go` — `seedIntraday` passes `sessionPhase(next)`.
- Test: `tick_test.go`.

**Interfaces:**
- Consumes: `sessionPhase`, `halfSpreadTarget`, `quote`, `pickDirectionPressure`, `lambdaEff`, `hawkesMax`, `symRuntime.rv1m`, `priceState.SkewCur`/`Hawkes`.
- Produces: `func genTicks(rng *rand.Rand, spec SymbolSpec, ps *priceState, b *bookState, sess *sessionAgg, phase phaseMul, rv1m float64, symbol string, fromMs, toMs, seqBase int64) []feed.Tick`.

- [ ] **Step 1: Write the failing test** — over a fixed window the mid walks toward FV (pressure closes the loop), and the loop respects the event cap. Add to `tick_test.go`:

```go
func TestGenTicks_MidWalksTowardFV(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	s := spec(PersLargeCap)
	ps := newPriceState(s)
	ps.Reg = RegChop
	ps.DwellLeftMs = 10 * 60_000
	b := newBook(rng, s, ps.FV)
	// Push FV above the book mid; pressure should drag the mid up toward it.
	ps.FV *= 1.02
	var sess sessionAgg
	full := phaseMul{lambda: 1, depth: 1, spread: 1}

	beforeBid, beforeAsk := b.best()
	beforeMid := (beforeBid + beforeAsk) / 2
	genTicks(rng, s, ps, b, &sess, full, 0 /*rv1m*/, s.Code, 0, 60_000, 1)
	afterBid, afterAsk := b.best()
	afterMid := (afterBid + afterAsk) / 2

	if !(afterMid > beforeMid) {
		t.Fatalf("book mid did not walk toward FV: before=%.4f after=%.4f FV=%.4f", beforeMid, afterMid, ps.FV)
	}
	if afterMid > ps.FV+0.05 {
		t.Errorf("book mid overshot FV badly: mid=%.4f FV=%.4f", afterMid, ps.FV)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (signature mismatch / no walk).

- [ ] **Step 3: Rewrite `genTicks`** (tick.go):

```go
// genTicks samples a Poisson trade-arrival process over [fromMs, toMs). Before
// each arrival it decays Hawkes over the gap and accrues+spends quoting budget
// (persistent book, no re-anchor); each arrival's aggressor side comes from
// pressure pBuy (FV vs book mid) plus the ramped regime skew, then sweeps the
// book (Buy/Sell) or crosses inside (NEUTRAL). Depletion moves the touch and
// stays gone until budget refills it. lambda is scaled by session phase and
// boosted by Hawkes + urgency. A per-window event cap bounds flush cascades.
func genTicks(rng *rand.Rand, spec SymbolSpec, ps *priceState, b *bookState, sess *sessionAgg, phase phaseMul, rv1m float64, symbol string, fromMs, toMs, seqBase int64) []feed.Tick {
	var ticks []feed.Tick
	seq := seqBase
	toMsF := float64(toMs)
	tMs := float64(fromMs)
	lastMs := float64(fromMs)

	windowSec := float64(toMs-fromMs) / 1000
	maxEvents := int(spec.LambdaMax*(1+hawkesMax)*urgencyCap*windowSec) + 16 // stride-invariant cap
	events := 0

	for {
		bid, ask := b.best()
		bookMid := (bid + ask) / 2
		hs := halfSpreadTarget(spec, ps.Reg, phase, rv1m)
		urgency := 0.0
		if hs > 0 {
			urgency = math.Abs(ps.FV-bookMid) / hs
		}
		lam := lambdaEff(spec, ps.Reg, phase, ps.Hawkes, urgency)
		if lam <= 0 {
			break // nil-flow (overnight) phase
		}

		gapSec := -math.Log(rng.Float64()) / lam
		tMs += gapSec * 1000
		if tMs >= toMsF {
			break
		}
		if events >= maxEvents {
			break
		}

		ps.Hawkes *= math.Exp(-gapSec / hawkesDecayTau)
		dtGap := (tMs - lastMs) / 1000
		b.quote(rng, spec, ps.Reg, phase, ps.FV, hs, dtGap)
		lastMs = tMs

		tsMs := int64(tMs)
		bid, ask = b.best() // re-read after quoting
		bookMid = (bid + ask) / 2
		dir := pickDirectionPressure(rng, ps.FV, bookMid, hs, ps.SkewCur)
		size := drawSize(rng)

		var execPrice float64
		var filled int64
		if dir == feed.Neutral {
			execPrice = round2((bid + ask) / 2)
			filled = size
		} else {
			execPrice, filled = b.consume(dir, size)
		}
		if filled <= 0 {
			continue
		}

		tk := feed.Tick{
			Symbol:   symbol,
			Seq:      seq,
			TsMs:     tsMs,
			Price:    execPrice,
			Volume:   filled,
			Turnover: execPrice * float64(filled),
			Dir:      dir,
			RecvTsMs: tsMs,
		}
		seq++
		ticks = append(ticks, tk)
		sess.update(tk)

		ps.Hawkes += hawkesJump
		if ps.Hawkes > hawkesMax {
			ps.Hawkes = hawkesMax
		}
		events++
	}

	// Tail quote for the remainder of the window (keeps budget accrual dt-correct).
	if tail := (toMsF - lastMs) / 1000; tail > 0 {
		hs := halfSpreadTarget(spec, ps.Reg, phase, rv1m)
		b.quote(rng, spec, ps.Reg, phase, ps.FV, hs, tail)
	}
	return ticks
}
```

- [ ] **Step 4: Update both callers, and maintain `rv1m` in the seeder too.** To avoid duplicating the rv1m-on-bar-close logic (generator.go 2.2 block + seeder.go), first extract it into a `symRuntime` method and call it from both:

```go
// foldBarClose updates the rv1m EWMA (squared 1m returns) from a newly closed
// 1m bar. Shared by the live step path and the fine seeder pass so both track
// the same volatility statistic (load-bearing for stride invariance, chunk 6).
func (rt *symRuntime) foldBarClose(closed feed.Bar) {
	if rt.prevBarClose > 0 {
		r := (closed.C - rt.prevBarClose) / rt.prevBarClose
		const rvAlpha = 0.2
		rt.rv1m = rvAlpha*r*r + (1-rvAlpha)*rt.rv1m
	}
	rt.prevBarClose = closed.C
}
```

  - Replace the inline rv1m block added in Task 2.2 (`generator.go` `stepSymbol`) with `rt.foldBarClose(*closed)`.
  - `generator.go` `stepSymbol`: `phase := sessionPhase(nowMs)` then
    `ticks := genTicks(rt.rng, rt.spec, rt.price, rt.book, &rt.sess, phase, rt.rv1m, rt.spec.Code, simFromMs, nowMs, rt.lastSeq+1)`.
  - `seeder.go` `seedIntraday` bar-close loop (~284-289): call `rt.foldBarClose(*closed)` alongside `st.ArchiveBar1m`, and pass `sessionPhase(next)` + `rt.rv1m` into its `genTicks` call.

- [ ] **Step 5: Retune `tick_test.go`** for the new signature (tests that called `genTicks(...)` with the old arg list, and any asserting the old `replenish`/`buyProb` behavior). Use production `SigmaHr` and a `full` `phaseMul`.

- [ ] **Step 6: Run + build + full package**

Run: `cd engine && go test ./internal/synth/ && go build ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add engine/internal/synth/tick.go engine/internal/synth/generator.go engine/internal/synth/seeder.go engine/internal/synth/tick_test.go
git commit -m "feat(synth): rewire genTicks onto pressure-coupled persistent book"
```

---

### Task 2.7: Delete dead code + add prune / FV-distance guard / event cap wiring

**Files:**
- Modify: `book.go` — delete `replenish`, `topUp`, `plantRoundWall`, `maxTouchDrift`, `maxTouchDriftMult`; add `prune`, `fvDistanceGuard`, `softRequoteToward`, guard state on `bookState`, a test-only `quoteDisabled` flag.
- Modify: `tick.go` — delete now-unused `buyProb`, `pickDirection`.
- Modify: `generator.go` — call `rt.book.prune()` + `rt.book.fvDistanceGuard(...)` in `stepSymbol` after `genTicks`; gate `quote` on `quoteDisabled`.
- Test: `book_test.go` — delete `replenish`-specific tests; add FV-distance-guard regression; update `stats_test.go` spread ceiling (was `maxTouchDrift`-based).

**Interfaces:**
- Produces: `func (b *bookState) prune(maxTicksFromTouch int)`; `func (b *bookState) fvDistanceGuard(spec SymbolSpec, fv float64, nowMs int64)`; `bookState.fvBreachSinceMs int64`, `bookState.fvGuardFires int`, `bookState.quoteDisabled bool` (test-only fault injection); `func fvGuardBound(spec SymbolSpec) float64`; consts `fvGuardGraceMs`, `pruneTicks`.

- [ ] **Step 1: Write the failing regression test** (`book_test.go`) — the guard fires only under fault injection and soft-re-quotes without rebuilding:

```go
func TestBook_FVDistanceGuardFiresUnderFaultBoundedRequote(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	s := spec(PersLargeCap)
	fv := 100.0
	b := newBook(rng, s, fv)
	b.quoteDisabled = true // fault: quoting can't correct drift

	// Walk the ask touch far above FV via repeated buy sweeps (no quote refill).
	for i := 0; i < 50; i++ {
		b.consume(feed.Buy, int64(s.BookMeanSize))
	}
	askLevelsBefore := len(b.asks)

	// Guard grace is 5s; advance past it.
	b.fvDistanceGuard(s, fv, 0)
	b.fvDistanceGuard(s, fv, fvGuardGraceMs+1)

	if b.fvGuardFires == 0 {
		t.Fatalf("FV-distance guard did not fire under fault injection")
	}
	if len(b.asks) < askLevelsBefore {
		t.Errorf("guard rebuilt (dropped levels) instead of soft re-quoting: %d -> %d", askLevelsBefore, len(b.asks))
	}
	assertBookInvariants(t, b)
}

func TestBook_FVDistanceGuardSilentUnderNormalQuoting(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	s := spec(PersLargeCap)
	fv := 100.0
	b := newBook(rng, s, fv)
	full := phaseMul{lambda: 1, depth: 1, spread: 1}
	for i := 0; i < 200; i++ {
		b.consume(feed.Buy, int64(s.BookMeanSize)/2)
		b.quote(rng, s, RegChop, full, fv, 0.02, 0.05)
		b.fvDistanceGuard(s, fv, int64(i)*50)
	}
	if b.fvGuardFires != 0 {
		t.Errorf("guard should not fire when quoting keeps the book near FV: fires=%d", b.fvGuardFires)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (guard undefined; `quoteDisabled` undefined).

- [ ] **Step 3: Delete the dead functions + their doc-comment references** — remove `replenish`, `topUp`, `plantRoundWall`, `maxTouchDrift`, `maxTouchDriftMult` from `book.go`; remove `buyProb`, `pickDirection` from `tick.go`. **Also fix the surviving prose that names them**: `book.go`'s package doc (line ~3, "replenishes back toward target depth"), the `bookDepth` comment (line ~14, "newBook/replenish maintain"), and `halfSpread`'s doc (line ~72, "both rebuildAround and replenish anchor") — reword to describe `quote` instead. Delete `book_test.go` tests that exercise the removed functions (`TestBook_Replenish*`, `TestBook_ReplenishRecentersDriftedTouch`, `TestBook_ReplenishRespectsSpreadProfile`, etc.). (The clean-grep verification is deferred to Step 7, after the `stats_test.go` fix in Step 6.)

- [ ] **Step 4: Add guard state + `prune`/`fvDistanceGuard`/`softRequoteToward`** to `book.go`:

```go
const (
	pruneTicks      = 24   // drop levels more than this many cents from the touch
	fvGuardGraceMs  = 5000 // divergence must persist this long before the guard acts
)

// (added to bookState)
//   fvBreachSinceMs int64
//   fvGuardFires    int
//   quoteDisabled   bool // test-only fault injection (disables quote())

// fvGuardBound is the per-personality |bookMid - FV| tolerance before the
// FV-distance guard engages. Unreachable if quoting works. Indicative.
func fvGuardBound(spec SymbolSpec) float64 {
	switch spec.Pers {
	case PersRunner:
		return spec.Open * 0.15
	case PersMidCap:
		return spec.Open * 0.06
	default:
		return spec.Open * 0.03
	}
}

// prune drops levels more than maxTicks cents from the current touch, keeping
// the ladder bounded after long one-sided drift. Never empties a side.
func (b *bookState) prune(maxTicks int) {
	span := float64(maxTicks) / 100
	if len(b.bids) > 0 {
		touch := b.bids[0].Price
		kept := b.bids[:0]
		for _, lv := range b.bids {
			if touch-lv.Price <= span {
				kept = append(kept, lv)
			}
		}
		if len(kept) > 0 {
			b.bids = kept
		}
	}
	if len(b.asks) > 0 {
		touch := b.asks[0].Price
		kept := b.asks[:0]
		for _, lv := range b.asks {
			if lv.Price-touch <= span {
				kept = append(kept, lv)
			}
		}
		if len(kept) > 0 {
			b.asks = kept
		}
	}
}

// fvDistanceGuard is the maxTouchDrift bug class's dedicated backstop: if the
// book mid diverges from FV beyond fvGuardBound for longer than fvGuardGraceMs,
// it performs a bounded soft re-quote of the far side toward FV (adding levels,
// never discarding the ladder) and counts the fire. Reaching here means quoting
// failed; it is not part of the normal path.
func (b *bookState) fvDistanceGuard(spec SymbolSpec, fv float64, nowMs int64) {
	bid, ask := b.best()
	mid := (bid + ask) / 2
	if math.Abs(mid-fv) <= fvGuardBound(spec) {
		b.fvBreachSinceMs = 0
		return
	}
	if b.fvBreachSinceMs == 0 {
		b.fvBreachSinceMs = nowMs
		return
	}
	if nowMs-b.fvBreachSinceMs < fvGuardGraceMs {
		return
	}
	b.softRequoteToward(spec, fv)
	b.fvGuardFires++
	b.fvBreachSinceMs = nowMs
}

// softRequoteToward adds up to a few levels on whichever side is short toward
// FV, so the touch can migrate back without discarding resting liquidity.
func (b *bookState) softRequoteToward(spec SymbolSpec, fv float64) {
	const steps = 5
	if len(b.asks) > 0 && b.asks[0].Price > fv {
		px := b.asks[0].Price
		for i := 0; i < steps && px-0.01 >= fv; i++ {
			px = round2(px - 0.01)
			sz := int64(spec.BookMeanSize)
			b.asks = append([]level{{Price: px, Size: sz, Orders: ordersFor(sz)}}, b.asks...)
		}
	}
	if len(b.bids) > 0 && b.bids[0].Price < fv {
		px := b.bids[0].Price
		for i := 0; i < steps && px+0.01 <= fv; i++ {
			px = round2(px + 0.01)
			sz := int64(spec.BookMeanSize)
			b.bids = append([]level{{Price: px, Size: sz, Orders: ordersFor(sz)}}, b.bids...)
		}
	}
}
```

Gate `quote` on the fault flag (top of `quote`): `if b.quoteDisabled { return }`.

- [ ] **Step 5: Wire prune + guard into `stepSymbol`** (generator.go). These must run **every** step, including quiet (zero-tick) ones — quoting and drift happen regardless — so place them **before** the `if len(ticks) == 0 { return }` early return, and mark the book dirty so the tail-quote is shipped:

```go
	ticks := genTicks(...)
	rt.book.prune(pruneTicks)
	rt.book.fvDistanceGuard(rt.spec, rt.price.FV, nowMs)
	rt.dirtyBook = true
	if len(ticks) == 0 {
		return
	}
	// ...existing tick/bar folding...
```

- [ ] **Step 6: Fix `stats_test.go` spread ceiling** — the current ceiling at `stats_test.go:163` is `maxSpread := 2*halfSpread(rt.spec, false) + 2*maxTouchDrift(rt.spec) + 1e-9`. Replace it with an `hs_t`-based bound, e.g. `maxSpread := 2*halfSpreadTarget(rt.spec, RegFlush, phaseMul{lambda: 1, depth: 1, spread: 3}, rt.rv1m) + pruneTicks*0.01`. Keep `assertBookInvariants` (unchanged, still reused here).

- [ ] **Step 7: Verify clean, then run + build + vet**

Run: `cd engine && grep -rn 'replenish\|topUp\|plantRoundWall\|maxTouchDrift\|buyProb\|pickDirection\b' internal/synth/` → expect **no output** (functions, tests, doc comments, and the stats_test ceiling all gone).
Then: `cd engine && go test ./internal/synth/ && go build ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add engine/internal/synth/
git commit -m "feat(synth): delete re-anchoring; add prune, FV-distance guard, event cap"
```

---

### Task 2.8: Acceptance suite — bar-code regression + impact + imbalance

**Files:** Modify `stats_test.go` (extend `TestGenerator_StatisticalSanityAcrossSeedsAndPersonalities` or add sibling tests). Production params, multi-seed, generous bands (finalized in chunk 6).

**Interfaces:** consumes the full generator via `New(seed, clock.NewFake(...))` + `StepTo` (existing `stats_test.go` pattern).

- [ ] **Step 1: Bar-code regression (headline).** Add `TestGenerator_NoBarCode` — over 4 simulated RTH hours (`StepTo` at 60s strides from 09:30), per non-degenerate symbol:
  - median 1m range ≥ 2 ticks (large cap) / ≥ 5 ticks (runner);
  - distinct print prices per active minute ≥ 3 (large) / ≥ 6 (runner);
  - < 60% of prints land on exactly two price rails.

Collect per-symbol from `Drain`'s `TicksEvent`/`Bars1mEvent`. Concrete band values are indicative; mark "finalized in chunk 6".

- [ ] **Step 2: Impact test.** Add `TestBook_SweepImpactDecays` — build a book at FV, `consume` a sweep of 3× touch size, record `bookMid`; then `quote` over 1s and 60s of budget:
  - mid moves ≥ 1 tick immediately;
  - < 100% reverted at 1s;
  - ≥ 50% reverted at 60s (temporary impact decays as budget refills).

- [ ] **Step 3: Imbalance-precedes-moves test.** Add `TestGenerator_ImbalancePrecedesMoves` — over an RTH run, sample top-3-level book imbalance and the subsequent 5s mid return; assert positive correlation at ≥ 1s lead (generous threshold). Use a fixed seed sweep.

- [ ] **Step 4: Run (with `-race` for the determinism-adjacent paths)**

Run: `cd engine && go test ./internal/synth/ -run 'TestGenerator_NoBarCode|TestBook_SweepImpactDecays|TestGenerator_ImbalancePrecedesMoves' -v && go test -race ./internal/synth/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/synth/stats_test.go engine/internal/synth/book_test.go
git commit -m "test(synth): bar-code regression + impact + imbalance acceptance suite"
```

**Chunk 2 gate:** `cd engine && go test ./... && go vet ./...` green; run `go run ./cmd/etape -demo -demo-seed 42` and confirm the DOM touches deplete/refill and price forms patterns rather than two rails. **The bar code is dead.**

---

# CHUNKS 3–6 — Task-level outlines (EXPAND BEFORE EXECUTING)

> Per the plan-scope decision, chunks 3–6 are captured as task-level outlines: files, interfaces (from the spec), and test intents. **Before executing each chunk, re-run recon against the code that chunks 1–2 actually produced and expand that chunk into full bite-sized TDD tasks** (same fidelity as chunks 1–2). The concrete type shapes below are the spec's intent; exact signatures may shift once the persistent book from chunk 2 is real.

---

## CHUNK 3 — Level memory (M)

**Deliverable:** persistent, finite S/R walls at prices that matter; break/flip/decay behavior; multi-day seeding from coarse dailies; rollover refresh. (Spec §3.3.)

**New file:** `engine/internal/synth/levels.go`. **Touches:** `generator.go` (per-symbol level table on `symRuntime`, rollover refresh), `book.go` (`quote` applies the level size multiplier `λ_L` at matching cent prices), `seeder.go` (seed table from the last ~20 coarse dailies).

**Interfaces (spec intent):**
- `type levelKind uint8` (PriorHigh/PriorLow/PriorClose/MultiDayHigh/MultiDayLow/SessionHigh/SessionLow/ORHigh/ORLow/VWAP/RoundDollar/RoundHalf).
- `type priceLevel struct { Px float64; Mult float64 /* λ_L ∈ [2,6] */; Kind levelKind; FlippedUntilMs int64 }`.
- `type levelTable struct { entries []priceLevel /* sorted by Px, ≤16 */ }`.
- `func (lt *levelTable) multiplierAt(px float64) float64` — returns `λ_L` for a cent-matching level (1.0 if none); consumed by `quote`'s `targetSize`.
- `func (lt *levelTable) onBreak(px float64)` — flip side at `0.7·λ_L`, decay over ~10 min.
- `func (lt *levelTable) refresh(dailies []feed.Bar, sessHi, sessLo, orHi, orLo, vwap, price float64)` — rebuild from prior-day + multi-day + session + round numbers within ±10% of price.

**Tasks (expand):**
1. `priceLevel`/`levelKind`/`levelTable` types + `multiplierAt` (unit test: exact-cent match returns λ_L; miss returns 1.0).
2. Seed the table from the last ~20 coarse dailies (multi-day highs/lows) — wire in `seeder.go`; test against a known daily series.
3. Round-dollar/half + prior-day/session/OR entries + `refresh`; rollover wiring in `rolloverSymbol`.
4. Apply `λ_L` in `quote.targetSize`; break detection (level fully consumed + traded through >2 ticks) → `onBreak` flip at 0.7 with ~10 min decay.

**Test intents (spec §8 #8):** scripted levels absorb a minimum number of prints before breaking (median ≥ 2 touches, indicative); VWAP-pullback trough sits in a band around VWAP (§8 #7); post-break retest touches the flipped level within a rate band.

---

## CHUNK 4 — Feedback loop: SimBroker ↔ Generator (M)

**Deliverable:** the user's sim orders participate in the synthetic market — market orders sweep the book and move price; resting limits sit in the DOM with real FIFO queue position; fills pulled via `DrainUserFills` after each `StepTo`. (Spec §4.)

**Touches:** `engine/internal/broker/sim/sim.go` (new `Options.Market`, `SetMarket`, `ApplyUserFill`, externally-resting flag), `engine/internal/synth/generator.go` (user-order API + `DrainUserFills` + `SweepUser`/`PlaceUser`/`CancelUser`), `engine/internal/synth/feed.go` (`Feed.Run` pump), `engine/cmd/etape/main.go` (post-construction `SetMarket` in the demo branch).

**Interfaces (verbatim from spec §4):**
```go
// package sim
type Market interface {
	SweepUser(symbol string, side feed.Direction, qty int64, limitPx float64,
		allOrNone bool) (avgPx float64, filled int64)
	PlaceUser(symbol, orderID string, side feed.Direction, qty int64, px float64)
	CancelUser(symbol, orderID string) (remaining int64)
}

// package synth (pull side)
type UserFill struct{ Symbol, OrderID string; Px float64; Qty, TsMs int64 }
func (g *Generator) DrainUserFills() []UserFill
```
Plus: `sim.Options.Market Market` (nil ⇒ byte-identical to today); `func (b *Broker) SetMarket(m Market)` (post-construction — `buildBrokers` runs before the Generator exists); `func (b *Broker) ApplyUserFill(orderID string, qty, px float64)` (reuses `fillLocked` accounting: order events, positions, P&L, chart markers via existing `emit` path); an externally-resting flag on the internal order so `crossRestingOnBookLocked` skips user limits resting in the synthetic book (no double fills). `SlippageBps` forced to 0 when `Market` is set; `FillLatencyMs` still gates *when* the sweep runs.

**Semantics (spec §4):** `SweepUser` walks the same persistent queues under `g.mu`, emits a real sequenced `feed.Tick` folded into bars/VWAP/movers/tick-ring, applies capped permanent impact `FV += sign·κ·(filled/BookMeanSize)·0.01` (κ ≈ 0.3); marketable-limit remainder rests. `PlaceUser` records `synthAhead` (synthetic size at placement); consumption depletes `synthAhead` first, then user qty, then trailing; replenishment always joins behind user qty; cancel/replace = cancel + re-place at the tail. **Lock order `sim.mu → g.mu`; generator never calls out.** `Feed.Run` calls `g.DrainUserFills()` immediately after each `StepTo`, feeding `b.ApplyUserFill`. Stops stay mark-triggered via the unchanged `markBridge`, then route through `SweepUser`.

**Tasks (expand):**
1. `synth`: `UserFill`, per-symbol resting-fill buffer, `DrainUserFills` (drained in `g.order` symbol order, print order within a step).
2. `synth`: `SweepUser` (aggressive path, `g.mu`, permanent impact, real tick emission).
3. `synth`: `PlaceUser`/`CancelUser` (FIFO `synthAhead`, book snapshots include user size).
4. `sim`: `Options.Market` + `SetMarket` + externally-resting flag + `SlippageBps→0` when set (nil-Market byte-identical — regression-locked by the existing sim suite).
5. `sim`: `ApplyUserFill` (reuses `fillLocked`; returns `[]exec.BrokerEvent` via `emit`, mu held then released — match the existing pattern).
6. `feed.go`: `Feed.Run` pump after `StepTo`.
7. `main.go`: post-construction `SetMarket` wiring in the demo branch (sim built at `boot.go:84/89`, Generator at `main.go:596`).

**Test intents (spec §8 #10, §6):** paired-seed impact test (same seed with vs without a scripted user buy ⇒ treated run's mid measurably higher 30s later; **untouched symbols byte-identical** — blast radius = one symbol); user print visible in `TicksEvent` and the 1m bar; resting limit visible in the book snapshot and filled exactly once via `DrainUserFills` after `synthAhead` depletes; scripted-user determinism; **race-detector concurrency pass** (`go test -race`); nil-`Market` sim suite unchanged.

---

## CHUNK 5 — Structure scripts (M–L)

**Deliverable:** the leg machine and level-interaction scripts that make patterns worth trading; pre-market gap drive; posture-lead ramp; catalyst-linked headlines. (Spec §3.1, §3.4, §3.6.)

**New file:** `engine/internal/synth/scripts.go`. **Touches:** `price.go`/`generator.go` (leg machine inside TREND/PARABOLIC/FLUSH; runner daily excursion budget from `GapPct`; pre-market gap drive + catalyst scheduling), `levels.go` (scripts plant/flip walls), `requester.go` (catalyst-linked demo headlines), forced-scenario hooks.

**Interfaces (spec intent):**
- Leg machine: `type legPhase uint8` (Impulse/Pullback/Consolidate); impulse length draws 0.5–2.0× a rolling 1m-range EWMA; pullbacks retrace 30–60% targeting the nearer of session VWAP or a fast EWMA, planting a DEFEND there.
- `type script interface { step(...); active() bool }`; concrete DEFEND(L)/BREAK(L)/FAKEOUT(L)/SQUEEZE state machines (≤2 active per symbol, personality/regime-weighted draws — default DEFEND .55 / BREAK .30 / FAKEOUT .15). DEFEND auto-escalates to BREAK on absorption-budget exhaustion; BREAK schedules a RETEST within 1–4 min at ~70% hold; FAKEOUT is DOM-identical to BREAK until a 1–3 tick trade-through then hard flip.
- Runner excursion budget drawn at day start from `GapPct`; consumed by parabolic/flush legs (bounds runaway with the VWAP saturation backstop + event cap — replaces halts).
- Pre-market gap drive: catalyst fires at a seeded pre-market time; gap-building legs grind FV up through the thin book into 09:30; opening-range levels born in the first 30 min.
- Forced-scenario hooks: test-only setters to pin a regime/leg/script sequence.

**Tasks (expand):** leg machine; DEFEND; BREAK+RETEST; FAKEOUT; SQUEEZE (runners); runner excursion budget; pre-market gap-drive + catalyst scheduling; posture-lead ramp integration (SkewCur already ramps — connect script transitions to `SkewTarget`); catalyst-linked headlines in `requester.go`; forced-scenario hooks.

**Test intents (spec §8 #7–9, §10):** pullback-to-VWAP (median trough band); DEFEND hold / absorption-then-break; BREAK follow-through + retest touches flipped level in a rate band; FAKEOUT reclaim rate in band; SQUEEZE capped/budget-bound; pre-market spread/depth/vol ratios vs RTH in band + runner gap fraction realized before 09:30; each behavior reproducible in CI via forced-scenario hooks; each restoring force (anchor reversion, VWAP saturation, pressure) tunable in isolation.

---

## CHUNK 6 — Calibration & hardening (M)

**Deliverable:** the acceptance suite's thresholds tuned across seeds; the two seam tests that pin history↔live and pre-market; a dev-only eyeball harness; boot-budget re-verified. (Spec §5, §8, §10.)

**New:** `engine/cmd/synthplot/` (dev-only, not CI — dumps 1m candles + DOM heatmap CSV for a seed). **Touches:** `stats_test.go` (threshold tuning), `seeder_test.go` (boot budget).

**Tasks (expand):**
1. **Stride-invariance test (load-bearing for the history↔live seam):** same seed at 50 ms vs 60 s strides agrees on realized vol and implied λ within ±25%. This is the test that catches "history looks different from live."
2. **Pre-market-profile test:** pre-market spread/depth/vol ratios vs RTH in band; runner gap fraction realized before 09:30 in band; volume ramps into the open.
3. **Threshold tuning pass:** finalize every indicative band/coefficient across the seed sweep (β, refill rates, κ, spread γ, μ table, SigmaHr, satWeight, script weights, all §8 thresholds).
4. **`cmd/synthplot`:** CSV dump for a seed (human calibration loop).
5. **Boot-budget re-verify:** confirm `TestSeed_WithinBudget` (3 s; ×8 under `-race`) still passes with the fine pass's added per-stride work; adjust budget doc if the measured constant moved.

**Test intents (spec §8 #2, #9):** stride invariance ±25%; pre-market profile in band. `synthplot` is not a CI gate.

---

## Self-Review (against the spec)

**Spec coverage** — every §3–§8 requirement maps to a task:
- §1 root causes: #1 re-anchored book → Task 2.7 (delete `replenish`/re-anchor); #2 non-dt-scaled drift → Task 1.4; #3 cent-snap → Task 1.4 (unrounded FV); #4 one-way causality → Task 2.6 (pressure) + chunk 4 (user feedback). ✓
- §3.1 director/FV: Tasks 1.3–1.5, 2.4 (posture), chunk 5 (leg machine, excursion budget, pre-market). ✓
- §3.2 persistent book: Tasks 2.3, 2.6, 2.7. ✓
- §3.3 level memory: chunk 3. §3.4 scripts: chunk 5. §3.5 tape texture: Tasks 2.5–2.6. §3.6 headlines: chunk 5. ✓
- §4 feedback loop: chunk 4. §5 seeder/continuity: Tasks 1.1/1.4/1.5/2.6 thread `rt.rng`/FV/phase/vwap through the passes; chunk 6 stride-invariance. §6 determinism: Task 1.1 + chunk 4 blast radius. ✓
- §5 simplifications (halts removed): Task 1.2. §7 perf + §8 testing: acceptance suite Task 2.8 + chunk 6. ✓

**Placeholder scan:** chunks 1–2 contain full code and concrete commands; no "TBD"/"add error handling"/"similar to Task N". Chunks 3–6 are outlines *by design* (Earl-approved scope) and are explicitly marked EXPAND BEFORE EXECUTING — not silent placeholders.

**Type consistency:** `priceState.FV` (not `.Mid`) used from Task 1.3 onward; `stepPrice(...vwap, nowMs, dtMs)` signature consistent from 1.4; `genTicks(...phase, rv1m,...)` consistent from 2.6; `phaseMul{lambda,depth,spread}`, `halfSpreadTarget`, `quote`, `pBuy`, `lambdaEff`, `pickDirectionPressure` names used consistently across the tasks that produce and consume them.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-12-demo-realistic-market-plan.md`. Two execution options:

**1. Subagent-Driven (recommended)** — a fresh subagent per task, two-stage review between tasks, fast iteration. Best fit for this codebase's worktree + subagent convention.

**2. Inline Execution** — execute tasks in this session using `superpowers:executing-plans`, batch execution with checkpoints for review.

Chunks 3–6 must be expanded to full TDD fidelity (recon + bite-sized tasks) before their execution begins.

