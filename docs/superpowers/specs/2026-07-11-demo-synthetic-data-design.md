# Design: realistic synthetic demo data (live synth feed)

## Context

Today's demo (`-demo`) pre-generates a toy 20-minute journal — price marches up
$0.05/tick on a fixed 10s cadence, volume is a constant 100, and the L2 book is
written once at open and never updated — then replays it with `-replay-hold`.
The chart is a staircase, the DOM ladder is frozen, and the Movers panel shows
"No movers right now" (scanner data is live-only, never journaled).

Goals (Earl, 2026-07-11):

1. **Both marketing and practice grade** — the demo is the public first-run
   experience *and* a practice sandbox, so book↔tick consistency matters
   (SimBroker fills walk the book).
2. **Mixed universe** — low-float runners + steady large caps + mid-cap
   fillers, with the Movers/scanner board moving with the synthetic symbols.
3. **Flows anytime** — open the app on a Saturday afternoon and data streams
   live, indefinitely. Not a canned historical day.
4. **Warm history at boot** — a few days of intraday plus ~a year of synthetic
   dailies, so every timeframe and indicator (VWAP/EMA/MACD) looks real.
5. **UI-entered** — demo is reached from the UI (like UI-driven replay), not by
   a documented CLI flag or the `etape-demo.cmd` wrapper.

**Chosen approach (Earl approved):** a **live synthetic feed**. Demo mode runs
the *live* engine pipeline with a new `synth.Feed` plugged in where the OpenD
client normally sits, generating ticks/quotes/books/bars in real time from a
seeded PRNG. Rejected alternatives: extending the stamped-journal replay
(finite, inherits all the modeling work with none of the architectural payoff)
and bundling a recorded real day (moomoo redistribution/licensing risk, one
fixed day forever).

## Architecture

A new package `engine/internal/synth` provides:

- **`synth.Feed`** — implements `feed.Feed` (`internal/feed/feed.go:122-131`),
  the same seam `OpenDFeed` and `replay.Feed` implement. Emits
  `TicksEvent`/`QuoteEvent`/`BookEvent`/`Bars1mEvent` on `Events()`, driven by
  the wall clock and one seeded PRNG.
- **`synth.Requester`** — implements the `Request(ctx, protoID, proto.Message)
  (opend.Frame, error)` surface the pollers already depend on
  (`scan.go:41`, `news.go:24`), answering rank/static-info/snapshot/news
  protocols from generator state.
- **A boot-time seeder** — fast-writes history into the demo store before the
  live generator starts.

Everything downstream is untouched and real: `pipe()` → journal → `md.Core` →
uihub; `markBridge` already feeds live-configured sim venues marks + books
(`main.go:658-675`, the 5cde5be behavior), so SimBroker book-walk fills work
against the synthetic book unchanged.

### Boot path (`cmd/etape/main.go`)

`-demo` switches from the replay branch to the **live** branch (`live = true`)
with these substitutions:

- Construct `synth.Feed` instead of `opend.New` + `NewOpenDFeed`; same
  `hub.SetFeed(fd)` + `pipe(fd.Events(), core, st)` wiring. **Journaling stays
  on** (into the demo store) — that is what makes `warmStart`'s session-tick /
  10s-bar seeding work on chart open.
- Backfill orchestrator runs with an **empty provider chain** and no
  moomoo/tail fetchers: `warmStart` serves the seeded archives and sets the
  pan-back watermark (`backfill.go:225`, `:371`); a chain-less `walkChain`
  exhausts cleanly, so Alpaca/Yahoo/OpenD are never touched.
- `startPollers` is widened from concrete `*opend.Client` to the requester
  interfaces the pollers already define locally; demo wires scanner + news +
  stockinfo to `synth.Requester` with a **nil demandFeed** (pool disabled —
  the whole universe is always generated). The quota poller is not started.
  The health poller's "moomoo" link probes `synth.Requester` (reports
  healthy).
- Kept from today's demo boot: fresh temp dir + `demo.db`, one injected
  `{broker: "sim", env: "paper"}` venue with generous gates, credentials never
  loaded.

### Demand & queries

- `Validate` returns `feed.ErrUnknownSymbol` for symbols outside the demo
  universe → typing a random ticker gets the normal "unknown symbol" ack
  (`commands.go:316`), no hang.
- `Ensure`/`Release` are accepted no-ops — all universe symbols generate
  continuously regardless of demand.
- Query methods answer genuinely from generator state: `BookSnapshot`,
  `QuoteSnapshot`, `RecentTicks`, `CachedBars1m`, `HistoryBars` (reads the
  seeded history).
- Conn lifecycle: emit `ConnUp` once at start (harmless; ignored by the
  mirror); never emit `Resynced` (would spuriously mark gaps / re-arm
  backfill).

### Flags

- `-demo` **survives as the internal relaunch vehicle and power-user
  shortcut** but leaves the documented surface (see UI entry). Self-restart
  works by re-execing with boot-time flags, so the flag *is* the mechanism.
- `-demo-day` and `-demo-speed` are **removed** — data is stamped to real wall
  time, always 1×.
- New `-demo-seed <n>` pins the PRNG. Default: random per launch, so repeat
  practice sessions differ.
- `demojournal`/`genjournal` stay untouched — the E2E suite, the replay smoke
  test, and the UI-driven-replay fixtures keep their byte-deterministic
  journal generator.

## Generator model

### Universe — 12 fictional symbols

Fictional tickers, `US.`-prefixed so the `supportedMarket` gate passes (e.g.
`US.VLCN`, `US.MERI`, `US.QNTM`). Never real tickers: real symbols with fake
prices are misleading, and fictional names make "this is synthetic"
unmistakable — the same safety logic as the REPLAY banner.

| Personality | Count | Traits |
|---|---|---|
| Low-float runner | 2 | $2–15, 5–20M float, gapped +40–80% vs synthetic prev close, thin fast book, spread 1–5¢ blowing out in flushes |
| Large cap | 5 | $80–500, penny spread, deep book, steady drift + chop, 1–5 ticks/s |
| Mid-cap filler | 5 | modest ±2–6% days so the movers board looks real |

Per-symbol personality parameters: price level, float, spread profile, book
depth/size distribution, tick intensity, volatility, regime transition matrix.

### Price process

Regime-switching random walk per symbol: a Markov chain over `QUIET`, `CHOP`,
`TREND_UP`, `TREND_DOWN`, `PARABOLIC`, `FLUSH`, `HALT`, with per-personality
transition probabilities and dwell times of seconds-to-minutes. Price step =
drift(regime) + noise(vol), snapped to $0.01, with soft mean-reversion toward
a slowly-wandering anchor so hours-long runs stay bounded. Runners get
elevated `PARABOLIC`/`FLUSH` weights and a leg structure — push → consolidate
→ push/flush episodes every ~10–20 minutes — so any window shows action.

**Halts (runners only):** LULD-style — a >10% move within 5 minutes triggers a
5-minute halt: no ticks, book frozen. Consumers see simple event absence.

### Tick engine

Poisson arrivals, intensity λ(personality, regime) — large caps ~1–5/s steady,
runners 0.5–30/s bursty. Sizes lognormal with a 100-share bias plus occasional
1k–10k blocks. Direction is momentum-correlated (buy-heavy in up regimes) and
**executes against the current book**: buys print at the ask, sells at the
bid, occasional NEUTRAL inside prints. Turnover = price × volume.

### Book engine (L2)

10 levels/side maintained continuously per symbol:

- Level sizes lognormal per personality (large caps 500–5,000; runners
  100–1,500); order counts derived from size + noise.
- **Trades consume the touch** — an exhausted level promotes the next one;
  that is how price moves. This is the book↔tick consistency SimBroker's
  book-walk pricing needs.
- Replenishment arrives at/near the touch; occasional large walls at round
  numbers; spread widens in fast regimes, blows out in flushes.
- `BookEvent` emission coalesced to ~100–250ms, matching OpenD push cadence.

### Quotes & bars

- `QuoteEvent` (last, session OHLC, cumulative volume/turnover, prevClose)
  throttled to ~300ms on change. prevClose = yesterday's synthetic close —
  this is what makes movers %-change real.
- 1m bars aggregate from the generator's own ticks, with in-progress updates
  during the minute and a final emit on close, matching OpenD's END-labeled
  K_1M push behavior.

### Clock & rollover

The demo market never closes: every day is a trading day, no dead hours — a
gentle intensity wave plus runner episodes keep any moment watchable. Dailies
roll at ET midnight: prevCloses update, runners pick a fresh gap. All
randomness flows from the one seeded PRNG; byte-determinism in tests comes
from a fixed seed + injected fake clock.

## History seeding (boot)

Before the live generator starts, the seeder writes through existing store
APIs (then `Flush()`):

1. **~1 year of dailies** per symbol via `ArchiveDaily` — a daily-granularity
   run of the same personality model (runners show prior spike days), ending
   at yesterday's close = today's prevClose/gap base.
2. **~3 days of 1m bars** via `ArchiveBar1m` — a no-sleep fast run of the
   intraday model, closes stitched to the dailies.
3. **Today-so-far** — fast-generate from ET midnight to "now": 1m bars to the
   archive; tick-level `TicksEvent`s into the journal for the **last ~2 hours
   only** (a full day × 12 symbols is ~10⁶ rows for no visible benefit; 2h of
   10s history matches a live cold subscribe). The live generator continues
   from the same PRNG/book/price state — no seam at the boot instant.

Boot budget: ≤ ~3s for generation + writes. The existing `orch.Backfill` /
`warmStart` path then serves these archives to charts and sets the watermark,
so `LoadOlderBars` pan-back works archive-first and exhausts cleanly at the
archive edge.

## Synthetic requester (movers, Stock Info, news)

The **unchanged** pollers run against `synth.Requester`:

- **Rank protocols** (pre-market/RTH/after-hours/overnight) → rows computed
  from generator state: %change vs prevClose, last, volume, float. The Movers
  board mirrors the charts and updates as runners move; `scanner.hit` flashes
  fire for new entrants.
- **Static info (3202) + snapshot (3203)** → plausible per-personality
  fundamentals (float, shares outstanding, 52wk range derived from the seeded
  daily history) — Stock Info populates.
- **News (3263)** → light fictional headlines per symbol on a slow cadence so
  the Stock Info news list breathes.

## UI entry (rides the UI-driven-replay control plane)

Per `2026-07-11-ui-driven-replay-design.md`, mode switches are engine
self-restarts with rewritten argv. Demo becomes a third mode:

- `StartDemo` command alongside `StartReplay`/`GoLive`; `rewriteArgv` also
  strips/appends `-demo`.
- `sys.session` mode becomes `"live" | "replay" | "demo"`; a persistent
  **DEMO** banner with "Return to live", visually distinct from REPLAY.
- The launcher unifies: one Practice/Demo entry offering "replay a recorded
  day" or "synthetic demo market".
- **First-run affordance:** when no venue/feed is configured, the UI surfaces
  "Try demo" prominently. New-user story: run `etape.exe`, engine boots
  live-but-empty, click Try Demo. `etape-demo.cmd` is deleted;
  README/README-FIRST change to "run eTape, click Try Demo".

**Verify at implementation time:** a live boot with no OpenD and no venues
comes up gracefully (UI served, health shows feed down, no crash-loop) —
that's the state a first-run user lands in before clicking Try Demo.

## Storage & isolation

Each demo boot creates a fresh temp dir with its own `demo.db`; the seeded
history and the journaled live synthetic events all live there and are
discarded after the run. `~/.eTape` (real journal, archives, trades,
settings) is never written; synthetic days never appear in the live replay-day
picker. Persistent demo history (a `~/.eTape/demo.db` accumulating across
runs) was considered and rejected: gap-filling between runs, a second
long-lived schema, unbounded growth — for continuity the demo doesn't need.

## Testing

- **Generator unit invariants:** bid < ask always; levels sorted, positive
  sizes; ticks execute at the touch; volume conservation through
  consume/replenish; OHLC continuity across ticks/bars/dailies; halt behavior
  (no emissions, frozen book); daily/1m/tick boundary stitching; two runs
  with the same seed + fake clock are byte-identical.
- **Statistical sanity:** bounded drift over simulated hours; spread and tick
  intensity distributions within personality bounds.
- **Boot integration (`uihubtest`-style):** demo boot with fake clock →
  `EnsureSymbol` → warm `BarSnapshot` (3 days of 1m + dailies present); movers
  topic publishes rows consistent with quotes; a sim order **fills against
  the synthetic book** via the book-walk path.
- **Seeder timing:** boot-time seed completes within budget.
- **Existing suites untouched:** `genjournal`/`demojournal` E2E fixtures and
  the replay smoke test keep working as-is.
- **Manual:** run demo on a weekend — charts warm at all timeframes, DOM
  breathing, movers moving, halt observed on a runner, practice order fills.

## Sequencing

1. **Engine work first, behind `-demo`:** `synth` package (feed + requester +
   seeder), boot-path switch to live-branch wiring, `startPollers` widening.
   Independently landable and manually verifiable via `./run.sh demo`.
2. **UI entry second:** `StartDemo`, `sys.session` demo mode, DEMO banner,
   Try-demo affordance, `etape-demo.cmd` deletion, README updates — lands
   with/after the UI-driven-replay control plane.

## Out of scope

- config.toml exposure of the universe (hardcoded personalities; `-demo-seed`
  is the only knob).
- Session-hours realism curves (open/close volume U-shape).
- Multi-day runner storylines beyond prevClose continuity.
- Corporate actions, splits, dividends; halts on non-runners.
- Pause/scrub/speed for demo (it's a live stream, not a replay).
- SimBroker changes (realistic fills landed 2026-07-10).
