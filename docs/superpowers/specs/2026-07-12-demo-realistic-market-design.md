# Demo realistic market: pressure-coupled persistent book

- **Date:** 2026-07-12
- **Status:** Approved
- **Scope:** `engine/internal/synth`, `engine/internal/broker/sim` (additive port), demo wiring in `engine/cmd/etape/main.go`
- **Revises:** the "Generator model" section of `docs/superpowers/specs/2026-07-11-demo-synthetic-data-design.md`. Everything else in that spec (universe, event surface, seeder shape, requester, UI entry) stands.

## 1. Problem

The demo engine's output looks like a bar code: price oscillates between two fixed
cents instead of forming patterns, and the best bid appears as an immovable wall.
Verified root causes:

1. **Prints are glued to a re-anchored book.** 90% of prints execute at the touch
   (`tick.go`), and `replenish` (`book.go`) re-anchors both touches to `mid ± 1¢`
   after every trade — the chart literally plots two rails.
2. **Drift is not dt-scaled** (`price.go`): drift applies per *call* while noise
   scales `√dt`, so at the 50ms live cadence trends never build (they do build in
   the seeder's 60s strides — which is why dailies looked fine and live didn't).
3. **The cent-snap erases sub-cent accumulation.** Production vols (large caps
   0.005–0.015) produce ~0.2¢ per-step moves that `Round(x*100)/100` destroys.
   The unit tests use `Vol: 0.5` (30–100× production) and never saw it.
4. **Causality is one-way: Mid → book.** Eating liquidity never moves price;
   user sim fills don't touch the book at all (one-way mark bridge).

Fixing parameters alone yields wandering lines but a fake DOM and no feedback
loop. The fix is causality inversion.

## 2. Requirements (user-confirmed)

1. **Purpose: practice-trading arena**, not just visual demo. Patterns must be
   worth reading and trading.
2. **Full feedback loop:** the user's sim orders participate in the synthetic
   market — market orders consume book liquidity and move price; resting limits
   appear in the DOM with real queue position; the market reacts to user flow.
3. **Behaviors — all four:** (a) momentum runners (gap drives, parabolic legs,
   flushes, squeezes); (b) trend + pullback structure (pullbacks toward VWAP,
   breakouts with retests, failed breakouts); (c) range/mean-reversion tape
   respecting levels, with visible absorption; (d) realistic microstructure
   (depleting/refilling touches, vol-widening spreads, imbalance preceding moves).
4. **Constraints:** keep the existing event surface (ticks, 10-level book,
   quotes, 1m bars, movers, fundamentals) and journal schema — the UI never
   changes; 12 symbols at the 50ms step cadence on a laptop; `-demo-seed`
   determinism preserved (modulo user interaction); boot seeding stays roughly
   at current cost; code must be reviewable in independently landable chunks.
5. **Simplifications (user-decided):** **no halts** — the LULD detector, `HALT`
   regime, and freeze/reopen logic are deleted (real LULD applies only
   09:30–16:00, so a pre-market-focused demo loses little); the halt event
   surface stays wire-compatible but never fires in demo. **Universe stays 12
   symbols** (keeps the top-10 movers pool rotating).

## 3. Architecture

Two layers per symbol, one-way influence, price emergent at the bottom:

```
director (regimes + leg machine + session phase)      1 step per StepTo
        │  fair value FV + flow posture (+ scripts)
        ▼
persistent limit-order book                            budget quoting, rate×dt
        │  Poisson prints consume queues  ──►  ticks → bars/VWAP/movers/journal
        ▲
        └── user orders (sim Market port): sweeps consume, limits rest FIFO
```

**Authority precedence (one rule):** the book is the only price authority.
The FV path steers *globally* through pressure and quoting anchors; level
scripts override posture only *locally* (within a few ticks of their level,
≤2 active per symbol, minimum spacing between scripted levels); when a script
ends, base posture resumes. Pressure is a single proportional, self-correcting
term — there is no second control loop.

### 3.1 Director: fair value (rework of `price.go`)

- `priceState.Mid` → `FV`: latent fair value, full float64 precision, **never
  rounded, never emitted**. Prices the user sees only ever come from the book.
- **dt-correct physics:** `FV *= 1 + (μ(reg,leg)·dtSec + σ·√dtSec·Z)`, clamped.
  μ is per-second (indicative: TrendUp +1 bps/s, Parabolic +6, Flush −8 —
  calibrated by the acceptance suite). σ comes from a per-personality
  **per-√hour vol** (`SigmaHr`: runner 0.05–0.12, mid 0.008–0.018, large
  0.003–0.006), replacing `spec.Vol`, targeted at realistic daily vols
  (runner 20–50%, mid 3–6%, large 1–2.5%).
- **Regime machinery kept:** enum, transition matrices, dwell timers — minus
  `HALT` and `detectHalt`, which are deleted.
- **Leg machine (new, inside TREND/PARABOLIC/FLUSH):** impulse → pullback →
  consolidate. Impulse length draws 0.5–2.0× a rolling 1m-range EWMA; pullbacks
  retrace 30–60% and **target the nearer of live session VWAP or a fast EWMA**,
  planting a DEFEND script there (§3.4). Trends get a readable staircase rhythm
  instead of a continuous wobble.
- **VWAP saturation backstop:** in trends, `μ_eff = μ·(1 − w·ext)` where `ext`
  is fractional extension above VWAP scaled per personality — bounds runaway
  extension independently of the legs (tunable in isolation via forced-scenario
  hooks). Anchor reversion continues to govern QUIET/CHOP.
- **Runner excursion budget (replaces halts as the runaway bound):** each
  runner draws a daily excursion budget at day start from the existing `GapPct`
  machinery; parabolic/flush legs consume it. Together with the saturation
  backstop and the per-window event cap (§3.2), this bounds any leg without
  frozen-book edge cases.
- **Session-phase curve (04:00–20:00 ET):** a phase multiplier table scales
  arrival rates, book depth targets, and spread targets: pre-market thin
  (0.2–0.4× depth, wide spreads, sparse flow), ramp to full over the first
  minutes after 09:30, midday lull, close ramp, post-market decay to 20:00.
  Overnight (20:00–04:00) flow is nil; FV drift accrues into the next
  rollover's gap reprice.
- **Pre-market gap drive (runners):** the gap is realized *progressively* — a
  catalyst fires at a seeded random pre-market time, then gap-building legs
  grind FV up through the thin book on escalating volume into the open, so the
  movers panel shows gappers climbing before 09:30 rather than a price that
  teleported overnight. Opening-range levels are born in the first 30 minutes
  and join the level table.

### 3.2 Persistent book (rework of `book.go`)

The ladder is **never re-anchored to mid again**. `replenish`, `topUp`,
`plantRoundWall`, and `maxTouchDrift` are deleted. `rebuildAround` survives at
exactly three call sites: construction, the day-rollover gap reprice
(overnight gaps are exogenous in real markets too — though runner gaps now
realize progressively via the pre-market drive rather than as a rollover
teleport), and the coarse→fine seeder boundary.

- **Budget quoting.** Each side accrues replenishment budget `rate(spec, reg,
  phase) × dt`; rates are regime-asymmetric (bid-side starved in FLUSH, ask-side
  in PARABOLIC — air pockets) and phase-scaled. Budget is spent inserting size
  on a cent grid around `FV ± hs_t`, filling whichever levels are short of
  their target profile (deeper levels fatter); level-table prices get their
  multipliers (§3.3).
- **Spread targeting.** `hs_t = clamp(hsBase + γ·rv1m, MinCents,
  MaxCents·regime/phase posture)` where `rv1m` is an EWMA of squared 1m
  returns — spread widens with volatility and fast regimes, and narrows only by
  quoting one tick inside per spend. Never by teleporting.
- **Depletion moves price.** `consume` keeps its walking logic (and the
  never-empty `extendSide` guarantee), but with no rebuild afterwards a swept
  level stays gone for `deficit/rate` seconds. The touch is wherever the flow
  left it.
- **Pressure closes the loop.** Aggressor buy-probability per print:
  `pBuy = clamp(0.5 + β·(FV − bookMid)/hs_t + m(reg), 0.05, 0.95)` (β ≈ 0.2;
  `m` is the regime skew, replacing `buyProb`). FV above the book mid ⇒
  buy-heavy flow eats asks faster than the lagged refill ⇒ the mid walks up
  toward FV. FV never touches the book directly.
- **Posture leads flow:** on regime/leg/script transitions, quoting posture
  (refill asymmetry, depth bias, inside-quoting) switches immediately while the
  aggressor-mix shift ramps in over 1–3s — DOM imbalance visibly precedes the
  tape turning.
- **Guards:** prune levels >24 ticks from the touch; FV-distance guard — if
  `|bookMid − FV|` exceeds a per-personality bound for >5s (unreachable if
  quoting works), log and do a bounded soft re-quote of the far side, never a
  silent rebuild (this is the old `maxTouchDrift` bug class; it gets a
  dedicated regression test); hard per-window event cap (~200 events/symbol)
  bounds flush cascades.

### 3.3 Level memory (`levels.go`, new)

Per symbol, a sorted slice (≤16 entries) of prices that matter: prior-day
high/low/close, multi-day highs/lows **seeded from the last ~20 coarse
dailies**, running session high/low, opening-range high/low, session VWAP
(cent-bucketed), round dollars/halves within ±10% of price. Each entry carries
a size multiplier `λ_L ∈ [2,6]` applied to quoting targets at that price —
persistent, finite walls. On a break (level fully consumed and traded through
by >2 ticks) the entry flips side at `0.7·λ_L` for the retest, decaying over
~10 minutes. Day rollover refreshes the table from the archived daily.

### 3.4 Interaction scripts (`scripts.go`, new)

Episodic, seeded-PRNG state machines that activate when price comes within a
few ticks of a level not visited recently (≤2 active per symbol; draws are
personality/regime-weighted, e.g. default DEFEND .55 / BREAK .30 / FAKEOUT .15,
runner-parabolic BREAK-heavy):

- **DEFEND(L):** plant a wall (4–8× mean size) with a finite absorption budget
  (2–4× the wall). The wall visibly refills as prints hammer it. Price leaves ⇒
  level held. Budget exhausted ⇒ **auto-escalate to BREAK** — a level holds
  until enough volume eats it, then breaks, with the cause printed on the tape
  and DOM.
- **BREAK(L):** cancel-rate spike + refill starvation on the defending side,
  aggressor flow leans in. On trade-through: a follow-through burst (λ×2,
  skewed aggressors, 5–20s), then a scheduled **RETEST** — the leg machine
  retargets L from the far side within 1–4 minutes, defended there with ~70%
  hold; a successful retest launches the next impulse leg.
- **FAKEOUT(L):** identical DOM signature to BREAK until a 1–3 tick
  trade-through, then a hard flip — wall re-plants on the original side, price
  reclaims. Because FAKEOUT and BREAK are indistinguishable until resolution,
  liquidity-pull is a real-but-not-certain signal.
- **SQUEEZE (runners only):** when price trades through session high after ≥2
  DEFEND touches there, fire an accelerated leg (transient aggressor boost,
  λ×2, starved ask refill). Capped and budget-bound (§3.1).

**Pullback-to-VWAP, concretely:** the trend's pullback leg targets the current
VWAP value and opens a DEFEND there; the wall absorbs into the bounce — with
enough budget randomness that ~30% slice through instead. Traders must confirm,
not assume.

### 3.5 Tape texture (`tick.go`)

The Poisson print loop keeps its shape (sizes, NEUTRAL ~10% mid prints,
seq/session/bar folding). Added: **Hawkes-lite self-excitation** — one decaying
float per symbol boosts λ after prints, so bursts cluster like a real tape;
the **session-phase multiplier** on λ; and **urgency scaling** — λ rises with
`|FV − bookMid|/hs_t` (capped), so tape speed precedes and accompanies moves.
Per-print aggressor side comes from `pBuy` (§3.2).

### 3.6 Demo news coupling

The requester's stub headlines get linked to actual generator events — a
pre-market catalyst, a gap, a break of yesterday's high — so demo headlines
correspond to visible moves ("VLCN receives FDA fast-track — shares surge").

## 4. Feedback loop: SimBroker ↔ Generator

The port lives in `broker/sim` with **primitive types only**, satisfied
structurally by `*synth.Generator` — no adapter, no import cycle (`synth`
never imports `sim`). A nil `Market` keeps replay/live sim venues byte-identical
to today (regression-locked by the existing sim suite).

```go
// package sim
type Market interface {
    // Marketable sweep against the live book: consumes queues, prints a real
    // tick, moves the touch, applies capped permanent impact to FV.
    // limitPx==0 ⇒ market order. allOrNone previews depth first (pure read).
    SweepUser(symbol string, side feed.Direction, qty int64, limitPx float64,
        allOrNone bool) (avgPx float64, filled int64)
    // Rest a user limit in the book (FIFO: joins behind current synthetic size).
    PlaceUser(symbol, orderID string, side feed.Direction, qty int64, px float64)
    CancelUser(symbol, orderID string) (remaining int64)
}

// package synth (pull side)
type UserFill struct{ Symbol, OrderID string; Px float64; Qty, TsMs int64 }
func (g *Generator) DrainUserFills() []UserFill
```

Semantics:

- **Aggressive orders** (`SweepUser`, under `g.mu`): walk the same persistent
  queues synthetic flow uses; emit a real sequenced `feed.Tick` folded into
  bars, session VWAP, movers volume, and the tick ring — the user's print is
  on the T&S and in the candle. Permanent impact:
  `FV += sign·κ·(filled/BookMeanSize)·0.01`, κ ≈ 0.3, capped per fill per
  personality. Temporary impact is the moved touch itself, decaying only as
  budgets refill. Sim's `FillLatencyMs` still gates *when* the sweep runs;
  `SlippageBps` is forced to 0 when `Market` is set — impact is real, not
  modeled. Marketable-limit remainder rests at the limit price.
- **Resting limits** (`PlaceUser`): inserted at the level (creating it if
  inside the spread — the user can narrow the displayed spread). The level
  records `synthAhead` = synthetic size at placement; consumption depletes
  `synthAhead` first, then fills user qty, then trailing size. Replenishment
  always joins **behind** resting user qty. Book snapshots include user size —
  the order is visibly in the DOM. Cancel/replace = cancel + re-place at the
  queue tail (the realistic replace penalty). Orders resting in the market
  carry an **externally-resting flag** so the broker's snapshot-book crossing
  path skips them — no double fills.
- **Fill delivery — no callbacks:** the generator never calls the broker.
  Lock order is one-way: `sim.mu → g.mu`. Resting fills buffer per symbol;
  `Feed.Run` pumps `DrainUserFills()` immediately after each `StepTo` into the
  broker's new `ApplyUserFill(orderID, qty, px)`, which reuses `fillLocked`
  accounting (order events, positions, P&L, chart markers all via existing
  paths). Drained in `g.order` symbol order; within a step, print order.
- **Stops** stay mark-triggered in sim via the unchanged `markBridge`; on
  trigger they route through `SweepUser`. In demo, book snapshots stop being
  the *pricing* source but keep serving marks/stop context.
- **Wiring:** `buildBrokers` constructs the sim venue before the demo branch
  creates the Generator, so the port attaches via a post-construction
  `SetMarket` setter on the sim broker (not at `Options` construction time).
- **User market distortion** is bounded by the demo venue's existing gate
  envelope (order-size limits); no new synth-side limits. Deliberately shoving
  a thin runner around is a feature, within those bounds.

## 5. Seeder & continuity

Shape unchanged (coarse 365d → `rebuildAround` → fine 3d + today → journal 2h
ticks → flush):

- **Coarse pass:** FV-only walk at the existing 60s substeps — with dt-correct
  drift this is now statistically the same process live runs, so seeded dailies
  and live behavior finally agree. `seedDayVolume` unchanged; boot cost
  unchanged. The final ~20 dailies seed the level table.
- **Fine pass:** full new engine at the existing 60s strides. All new physics
  are `rate × dt` and accrue per inter-print gap, so 60s strides and 50ms live
  strides produce matching statistics — **stride invariance is a designed,
  explicitly tested property** (realized vol and implied λ within ±25% across
  stride sizes).
- Seeder-time re-anchors are the coarse→fine boundary (no book existed during
  the coarse pass) and the day-rollover gap reprices, exactly as in live
  operation. Handoff to live is exactly today's: same per-symbol PRNG streams
  and in-memory state, `lastStepMs = nowMs`, buffers cleared.

## 6. Determinism

- **Per-symbol PRNG streams:** the master `rand.Rand(seed)` draws the universe,
  then seeds 12 child sources in `g.order` order. All stepping/quoting/tick
  draws for a symbol use its child. Same `-demo-seed` + fake clock ⇒
  byte-identical run (existing test upgraded to span book/tick/user paths).
- **User blast radius is one symbol:** `SweepUser`/`PlaceUser`/`CancelUser`
  draw no randomness; they perturb only the touched symbol's state. The other
  11 symbols stay byte-identical to an untouched run — tested explicitly.
- No map iteration on rng-consuming paths (sorted slices throughout).

## 7. Performance

Per 50ms step, 12 symbols: ~4–8k arithmetic ops typical (FV step, spread/
pressure, budget quoting ≤4 levels/side, expected λ·0.05 prints at ~50–80 ops
each); worst case (two parabolic runners + user sweeps) ~20k ops — well under
20µs on a laptop core, <0.1% duty cycle. All 12 symbols always step (movers
ranking, history accumulation, journal); the hub's existing demand/throttle
machinery still decides what ships to the UI. New memory <1MB; the existing 2h
tick ring (~15–25MB) still dominates. Boot: coarse pass unchanged; fine pass
gains a small constant — budget asserted by the existing seeder-timing test.

## 8. Testing

Keep all existing invariant/determinism/seeder tests (spread assertions
rewritten against `hs_t` + guard). New statistical acceptance suite (extends
`stats_test.go`; multi-seed, generous bands, **production parameters** — the
Vol=0.5 test blind spot is the cautionary tale):

1. **Bar-code regression (headline):** over 4 simulated RTH hours, per
   non-degenerate symbol: median 1m range ≥ 2 ticks (large caps) / ≥ 5
   (runners); distinct print prices per active minute above a per-personality
   floor (indicative: ≥3 large cap, ≥6 runner); <60% of prints on exactly two
   rails.
2. **Stride invariance:** same seed at 50ms vs 60s strides agrees on realized
   vol and implied λ within ±25% — pins the history↔live seam.
3. **Vol calibration:** realized daily vol within ±40% of personality target.
4. **Trend efficiency:** trends build in TREND dwells; QUIET chops.
5. **Impact:** scripted sweep of 3× touch size ⇒ mid moves ≥1 tick, <100%
   reverted at 1s, ≥50% reverted at 60s (temporary impact decays; permanent
   remains).
6. **Imbalance precedes moves:** top-3-level imbalance correlates with the
   next 5s return at ≥1s lead.
7. **VWAP pullback:** median trend-pullback trough within a band around VWAP.
8. **Level respect:** scripted levels absorb a minimum number of prints before
   breaking (indicative: median ≥2 touches); post-break retest touches the
   flipped level within a rate band; FAKEOUT reclaim rate in band.
   Exact thresholds and bands throughout this suite are fixed during the
   chunk-6 calibration pass; the indicative values above are starting points.
9. **Pre-market profile:** pre-market spread/depth/vol ratios vs RTH in band;
   runner gap fraction realized before 09:30 in band; volume ramps into the
   open.
10. **Feedback loop:** the **paired-seed impact test** (same seed with vs
    without a scripted user buy ⇒ treated run's mid measurably higher 30s
    later; untouched symbols byte-identical); user print visible in TicksEvent
    and the 1m bar; resting limit visible in the book snapshot and filled
    exactly once via `DrainUserFills` after `synthAhead` depletes; scripted-user
    determinism; race-detector concurrency pass.
11. **Guard regression:** FV-distance guard fires only under fault injection
    (quoting disabled) and performs a bounded re-quote, never a rebuild.

**Forced-scenario hooks:** tests can pin a regime/leg/script sequence so each
behavior (pullback-to-VWAP, DEFEND-hold, BREAK-retest, FAKEOUT, squeeze,
pre-market gap drive) is reproducible in CI, and each restoring force (anchor
reversion, VWAP saturation, pressure) is tunable in isolation.

**Eyeball harness (dev-only, not CI):** `cmd/synthplot` dumps 1m candles + DOM
heatmap CSV for a seed — final calibration is a human loop; the suite above is
the regression net.

## 9. Honest ceiling (accepted, disclosed)

- No strategic agents: nobody hunts stops or fades the user specifically;
  "reacts to user flow" is mechanical (book/imbalance/λ inputs), not
  adversarial.
- S/R and VWAP magnetism are planted priors, not emergent idiosyncrasy.
- No cross-symbol structure (no beta, no sympathy moves) — symbols independent,
  as today.
- Single lit venue; no hidden liquidity beyond NEUTRAL prints; no open/close
  auctions.
- Impact is a calibrated formula (κ), not a consequence of other agents'
  reactions.

## 10. Risks

1. **Calibration is the real work** (β, refill rates, κ, spread γ, μ table,
   script weights) — mitigated by the acceptance suite + forced-scenario hooks
   + `synthplot`; an explicit tuning pass is budgeted in chunk 6.
2. **Persistent-book pathologies** (starvation, one-sided books, FV/mid
   divergence) — FV guard, prune, event cap, extended multi-seed invariant
   sweeps.
3. **Lock-order invariant** (`sim.mu → g.mu`, generator never calls out) —
   enforced by the pull-only design, documented at both ends, race tests.
4. **Stride invariance may not hold at first** — the dedicated seam test
   catches it before "history looks different from live" ships.
5. **Determinism regressions** (map iteration, wall-clock reads) — byte-identical
   + blast-radius tests span all new paths.
6. **Structure scripts fighting the path** — bounded by the authority
   precedence rule (§3) and forced-scenario tests per script.

## 11. Delivery (six chunks, each lands green)

| # | Chunk | Contents | Size |
|---|-------|----------|------|
| 1 | Physics fix + halt removal | FV rename; dt-scaled per-second μ; `SigmaHr` vols; unrounded FV; VWAP saturation; per-symbol PRNG streams; delete halt machinery; retune price/stats tests. Visibly better charts alone. | S–M |
| 2 | Persistent book + session curve | Delete re-anchoring; budget quoting; spread targeting; pressure `pBuy`; Hawkes-lite + urgency λ; session-phase multipliers; prune/guard/event cap; bar-code + impact + imbalance tests. Kills the bar code. | L |
| 3 | Level memory | `levels.go`; multi-day seeding from coarse dailies; break/flip/decay; rollover wiring; level-respect + VWAP-pullback tests. | M |
| 4 | Feedback loop | Generator user-order API + `DrainUserFills`; `sim.Options.Market` + `SetMarket` + `ApplyUserFill` + externally-resting flag; `Feed.Run` pump; main.go demo wiring; paired-seed impact + FIFO + exactly-once + blast-radius tests. | M |
| 5 | Structure scripts | Leg machine; DEFEND/BREAK/FAKEOUT/RETEST; squeeze; pre-market gap drive + catalyst scheduling; posture-lead ramp; catalyst-linked headlines; forced-scenario hooks + script tests. | M–L |
| 6 | Calibration & hardening | Stride-invariance + pre-market-profile tests; threshold tuning across seeds; `cmd/synthplot`; seeder boot-budget re-verify. | M |

Overall: **L**. Chunks 1–2 fix the reported problem; 4 makes it a practice
arena; 5 makes it worth practicing on.

## 12. Design provenance

Produced by a blind four-proposal design panel (pure agent-based market,
regime-directed order flow, minimal structural evolution, data-calibrated/
learned generation) scored by three adversarial judges (trader, engine
maintainer, skeptic). The winning substrate is the minimal structural
evolution ("pressure-coupled persistent book"); the leg machine, level
scripts, posture lead, Hawkes-lite texture, multi-day level seeding,
paired-seed impact test, and catalyst headlines are judge-mandated grafts from
the other proposals. A full agent-based simulation was rejected as
research-grade calibration risk; learned/data-driven generation was reduced to
"fit acceptance-test bands from real journaled tape" (a possible follow-up,
out of scope here). Halts and universe-size decisions were made by Earl during
review.
