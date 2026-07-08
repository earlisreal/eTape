# Scanner-Driven Subscription Pool (replaces watchlist/focus)

**Date:** 2026-07-08
**Status:** Approved
**Supersedes:** the watchlist/`--watch`/`--focus` boot-subscription mechanism
(engine design §subscriptions; CLAUDE.md "pre-subscribe TICKER for the day's
watchlist" guidance).
**Depends on:** `2026-07-08-on-demand-symbol-subscription-design.md` — this
design lands **after** that plan executes, consumes its `Ensure`/`Release`
demand seams, and amends its news-symbol closure.

## Goal

Tick recording — and therefore 10s-chart history — should follow what is
actually moving instead of a pre-declared list. Earl trades what's moving up;
the scanner already knows what that is. The top 10 filtered Scanner symbols
are auto-subscribed at watch tier and stay subscribed for the trading day, so
any mover clicked mid-session already has session-long tick history. The
watchlist and `--focus` machinery is deleted outright.

## Decisions (locked with Earl, 2026-07-08)

| Question | Decision |
|---|---|
| Retention when a symbol leaves the top N | Sticky for the pool day, bounded by a pool cap; evict longest-off-board |
| Source list | **Filtered** Scanner rows (post MinChangePct / MinVolume / float-cap), not the raw Movers rank |
| Sessions feeding the pool | All four (overnight, pre-market, RTH, after-hours) |
| Sizing | N = 10 tracked live, pool cap = 30 symbols/day — fixed constants, no config keys |
| Watchlist/`--watch`/`--focus` | Removed entirely; no manual pin lane |
| Pool day boundary | 20:00 ET → 20:00 ET (start of overnight), so overnight runners keep recording through the next open |
| Deep-history backfill trigger | On first pool admission per day (boot-time watchlist seeding dies with the watchlist) |
| Sequencing | After the on-demand-subscription plan executes |
| Pool owner | `scan.Poller` (not a separate component, not uihub) |

## Architecture

A new `scan.Pool` type — its own file in `engine/internal/scan/`, pure logic,
no I/O — held by `scan.Poller`. On each `pollOnce`, after `rankRows()`
produces the filtered rows, the poller feeds the top 10 symbols (in rank
order) to the pool. The pool returns the delta — admissions and evictions —
and the poller executes it as `feed.Ensure` / `feed.Release` calls, exactly
parallel to how it already calls `Publisher.Publish`.

- **Feed handle:** injected into the poller at construction. The poller is
  created inside the live-only startup path, after the `OpenDFeed` exists, so
  no `SetFeed` indirection is needed — but a nil feed must be tolerated
  (pool disabled, poller otherwise unchanged) for tests and any non-live
  wiring. Verify the exact injection point at plan time.
- **Demand shape:** reuse `feed.WatchDemand` (TICKER + K_1M, 2 quota slots,
  not eviction-proof), demand id `scan:<symbol>`.
- **Coexistence with UI demands:** pool demands and panel-driven demands are
  independent demand ids over the same subman. Both demanding one symbol
  costs nothing extra (subman unions subtypes per symbol); releasing one
  leaves the other live.
- **Non-blocking:** `Ensure`/`Release` are cheap subman upserts; the
  deep-history backfill kicked on admission is async (see Backfill) so
  `pollOnce` never stalls on history fetches.

## Pool semantics

- **Admission:** any symbol in the filtered top 10 not already pooled.
  Sticky — dropping out of the top 10 does not release it.
- **Cap and eviction:** cap 30. When full and a new symbol qualifies, evict
  the member whose last-seen-in-top-10 timestamp is oldest. Members currently
  in the top 10 are never evicted (cap 30 > N 10 guarantees an evictable
  member exists). Eviction calls `feed.Release("scan:<symbol>")`; subman's
  existing MinHold (60s) + hysteresis (5m) still smooth the actual
  unsubscribe downstream, which is desirable.
- **Pool day:** 20:00 ET → 20:00 ET, matching one overnight → pre-market →
  RTH → after-hours cycle. On the first poll past the boundary, release all
  `scan:*` demands and clear the pool. An overnight runner admitted at 22:00
  keeps recording straight through the next day's open. After-hours movers
  from the ending day drop at 20:00 and re-enter only if still ranking
  overnight. Reuse the scanner's existing ET-day/session helpers for the
  boundary; timestamps come from poll time, not wall-clock randomness.
- **Restart mid-day:** the pool rebuilds from live polls only. Previously
  pooled symbols that no longer rank are not re-admitted; their
  already-journaled ticks survive, but recording resumes only on re-entry.
  Accepted v1 simplification — no pool persistence.
- **Closed sessions / weekends:** the scanner keeps polling (3410 during
  Closed); the pool simply admits nothing new when filters yield no rows.
- **Constants:** N = 10, cap = 30, compile-time constants in the scan
  package.

## Quota accounting

Subscription budget is 100 slots. Full pool = 30 × 2 = 60 slots, leaving ~40
for UI panel demands (watch 2 / focused 4 per the on-demand design). Under
contention subman already starves stalest watch-tier demands first and
focused always wins — so heavy UI use degrades the pool's oldest members
first, never the ladder. No new quota logic is introduced.

## Watchlist/focus removal

- Delete `[feed] watchlist` (`config.Feed.Watchlist`), the `--watch` and
  `--focus` CLI flags, and the boot-time `Ensure` loops in `main.go`.
- Delete `feed.FocusedDemand` (its only consumer was the `--focus` boot
  path; the UI ladder's focused tier lives in uihub's own
  `demandForProfile`). `feed.WatchDemand` survives as the pool's demand
  shape.
- **News poller set** becomes: current pool members ∪ live UI demands
  (`hub.ActiveDemandSymbols()`). This amends the on-demand design's
  "watchlist ∪ CLI flags ∪ live demands" closure — the single touch-point
  between the two designs. The poller needs a pool-membership accessor
  (e.g. `Pool.Symbols()`), read via the same closure re-evaluation the news
  rotation already does each tick.
- Delete boot-time `backfillSymbols` seeding (replaced by
  backfill-on-admission below).

**Semantic shift — state prominently:** the `[scan]` filter config
(MinChangePct / MinVolume / float cap) stops being display-only and becomes
the recording-eligibility rule. **A mis-set filter no longer just hides UI
rows — it silently stops tick recording for the day.** The only recording
escape hatch for a quiet, unranked symbol is keeping a panel open on it
(on-demand watch tier).

## Backfill on admission

A symbol's **first admission per pool day** triggers the same per-symbol
deep-history seed the boot path performs today (async, reusing the existing
backfiller's rate limiting and idempotence). Movers gain daily/1m archive
context moments after they start moving. Re-admission after eviction within
the same pool day does not re-trigger (the archives are already seeded).

- Historical-quota note: ≤30 pool symbols/day consume ≤30 of the 100
  historical K-line slots (all periods of one symbol share a slot) —
  comfortable alongside UI-driven fetches.
- Subscribe-time auto-seed (OpenD's quota-free caches: a day of 1m bars,
  ~1000 ticks, book+quote) continues to apply unchanged via
  `OpenDFeed.Ensure`.
- Plan-time verification: the boot backfill path must be callable
  per-symbol mid-session; if it's currently a one-shot batch, extract the
  per-symbol seam.

## Sequencing

Execute the on-demand-symbol-subscription plan first, as written. This
design then lands on top of its seams:

1. It reuses `feed.Ensure`/`Release` demand semantics unchanged.
2. It amends the news closure that plan introduces (watchlist ∪ flags ∪
   live demands → pool ∪ live demands).
3. Its removal of `--focus` does not touch the UI's FocusGroup/probe work —
   engine boot flags and UI focus groups are unrelated despite the shared
   name.

## Testing

- **`scan.Pool` unit tests** (pure logic, table-driven): admission,
  stickiness across polls, cap eviction picks longest-off-board, top-10
  members never evicted, 20:00 ET boundary reset, first-admission-per-day
  backfill triggering (as an emitted delta, not an I/O assertion),
  re-admission within a day does not re-trigger backfill.
- **Poller-level tests** with a fake feed: Ensure/Release invocations match
  pool deltas; nil feed → pool inert, polling/publishing unaffected.
- **Removal fallout:** amend existing config/main/news tests that reference
  watchlist/`--watch`/`--focus`; news-set test covers pool ∪ live demands.
- **Live verification** rides the outstanding live-OpenD checklist item
  alongside the session-aware scanner work (verify pool fills during a real
  pre-market and slots stay within budget).

## Out of scope (v1)

- UI badge/indicator for pooled symbols on scanner rows.
- Pool persistence across engine restarts.
- Configurable N / cap / boundary (fixed constants until proven wrong).
- Per-session N overrides.
- Subman health surfacing (`Slots()`/`Starved()` consumers) — unchanged
  from the on-demand design's deferral.
