# Scanner float resolution via 3203 snapshots — design

**Date:** 2026-07-07 · **Status:** approved · **Scope:** `engine/internal/scan` (+ one config field)

Closes the blocking item in `docs/2026-07-07-engine-pre-live-checklist.md`: float-universe
warm-up is a permanent no-op, so `MaxFloatShares` low-float screening silently does nothing.

## Problem

`refreshUniverse` asks the 3215 screener (`Qot_StockFilter`) to echo `FLOAT_SHARE` values, but
3215 never returns float values — even as a sort field it errors `Unknown key`
(live-verified 2026-07-06, `docs/2026-07-03-premarket-scanner-api.md`). The screener gives
*set membership only*. Consequently `Poller.universe` stays empty forever, every
`ScannerRow.FloatShares` is `nil`, and float filtering is a silent no-op. The poller also has
no logging, so nothing ever surfaced. Two latent secondary gaps: `refreshUniverse` fetches only
one page of 200 (the ≤50M universe is ~3,888 symbols), and there is no on-miss float path for
symbols outside the universe (fresh IPOs).

## Decision — on-demand snapshot cache; 3215 removed

`p.universe` has exactly one consumer: a float lookup when filtering rank rows. Set membership
is never used. So instead of fixing the warm-up (3215 membership paging + bulk 3203 fill +
on-miss fallback — the fallback is needed regardless), the poller snapshots exactly the symbols
on the rank board, on demand, and caches for the day.

Float source: `Qot_GetSecuritySnapshot` (proto **3203**), field `equityExData.outstandingShares`
= **true free float in raw shares** (verified 2026-07-03: DJT 163.6M vs 276.95M issued —
locked stake excluded; YRD 15.0M vs 87.5M issued). ≤400 codes/request, 60 req/30s, zero
subscription quota. The pb package `qotgetsecuritysnapshot` is already generated.

Removed entirely: `refreshUniverse`, the `filterpb` (3215) import, the `universe` map, the
`uniTick` ticker, and the `UniverseRefreshH` config field.

## Float cache

```go
type floatEntry struct {
    shares float64
    bad    bool // definitively unresolvable this ET day (OTC error, zero float, no equity data)
}
floats map[string]floatEntry // absent = unknown (transient)
```

Three states drive the filter semantics (decided in this design's brainstorm):

| Cache state | `MaxFloatShares > 0` | `MaxFloatShares == 0` |
|---|---|---|
| known, `shares > cap` | **drop row** | include, float shown |
| known, `shares <= cap` | include, float shown | include, float shown |
| bad | **drop row** | include, float blank |
| absent (transient) | include, float blank | include, float blank |

Rationale: known-bad symbols (OTC without quote rights, preferreds/units with float 0, ETFs
with no equity data) are never low-float equity candidates and clutter the board — drop them
when float screening is on. A symbol whose snapshot merely failed this poll must still show
(blank float) so a hot gapper is never hidden by a hiccup. With screening off, nothing is
dropped for float reasons; floats are still resolved because the value on the board is
informational. `MinChangePct` / `MinVolume` filtering is unchanged.

The cache resets on the ET-day boundary: `resetSeenIfNewDay` becomes `resetIfNewDay` and clears
the float cache alongside the seen-sets. That picks up overnight splits/offerings for free; no
periodic refresh cadence exists anymore. `bad` marks therefore also last at most one ET day.

## Poll flow

`pollOnce` becomes:

1. `fetchRank` (3410) — unchanged, plus a `slog` warning on failure (today a silent `return`).
2. `resetIfNewDay` — seen-sets + float cache.
3. `resolveFloats(ctx, missing)` — new: rank symbols absent from the cache go out in one 3203
   batch (≤35 today; chunked at 400 codes for safety); results populate the cache **before**
   filtering, so there is no first-poll window of unfiltered junk.
4. `rankRows(items, floats, cfg)` — same pure-transform shape, three-state semantics above.
5. Publish rank + hits — unchanged.

Steady state costs zero extra requests: board symbols persist poll-to-poll and stay cached.
Request construction: strip the `US.` prefix back off (`symbolOf` inverse), market
`QotMarket_US_Security`, wrapped in the outer proto2 `Request{C2S: ...}` like every other call
site.

Snapshot response parsing, per security:

- `equityExData.outstandingShares > 0` → known, value cached in raw shares.
- Zero or missing `equityExData` (ETFs, preferreds, units) → `bad`.
- Requested but absent from a successful response → `bad` (deterministic omission), logged.

## Error handling

Two failure classes, handled differently — the distinction prevents retry storms:

- **Transport/context errors** (OpenD unreachable, timeout): `slog` warn, leave symbols
  unresolved (absent). No in-poll retry; the next poll's miss-detection retries naturally.
- **Application errors** (`retType != 0` — the "one bad code fails the whole batch" case):
  binary split-retry. Halve the failing batch and recurse; when a failing batch is a single
  code, mark it `bad` and log which one. One bad code in 35 costs ~⌈log₂35⌉ ≈ 6 extra requests.

Documented ambiguity, resolved conservatively: the 07-03 research says one bad code errors the
whole batch; the 07-06 note says OTC codes fail per-code. The design assumes whole-batch
failure. If errors actually arrive per-code in-band, the split path never triggers — correct
either way.

**Backstop cap:** at most **8** 3203 requests per poll cycle (constant, not config). Symbols
left unresolved when the cap hits stay absent until the next poll. This bounds the pathological
first-poll-of-the-day case (empty cache, board full of OTC junk).

Rate budget vs limits — comfortably inside, so no shared rate limiter is built:

| Protocol | Limit | Worst case here |
|---|---|---|
| 3410 rank | 60 req/30s | 15/30s (one page per 2s pre-market poll) |
| 3203 snapshot | 60 req/30s | ~8 in one poll after a day reset; ~0 steady state |

## Logging

`slog`, matching engine convention (the poller currently has none): warn on rank fetch failure,
warn on snapshot transport failure, info when a code is marked `bad` and why. Volume is bounded
by construction — a symbol is marked `bad` at most once per ET day.

## Config

`UniverseRefreshH` is deleted from `config.Scan`. Implementation must sweep: defaults, the
sample/active TOML, the SetConfig live-update path and tygo-generated TS types if `Scan` flows
through them, and check whether the TOML decoder rejects unknown keys (if strict, a stale
`universe_refresh_h` in a config file fails boot — clean it up either way). No new config: the
8-request cap and 400-code chunk are constants.

## Testing

Existing 3 tests adapt; all new logic stays fake-requester-testable in the current style:

- `rankRows` table gains the three-state cases: known-under-cap (in, value), known-over-cap
  (dropped), bad-with-cap (dropped), bad-without-cap (in, blank), absent (in, blank).
- `resolveFloats` against a fake requester: happy batch populates cache; zero-float / missing
  `equityExData` → bad; requested-but-absent from success → bad; transport error leaves
  symbols absent; `retType != 0` split-retry isolates exactly the bad code; per-poll request
  cap honored; 400-code chunking.
- Day reset clears float cache alongside seen-sets.
- `pollOnce` end-to-end: rank + snapshot responses in, published rows and hits out.

## Non-goals (tracked, not this change)

- **`RankPages` is unused** — rank always pulls one page of 35. Pre-existing; separate item.
- **Re-snapshot-on-hit** for alert payload freshness (rank rows lag real time ~7s median,
  measured 2026-07-06) — separate feature, unrelated to float.
- **SQLite float persistence** — YAGNI; the cache rebuilds in one request after restart.
- **sys.events degraded-scanner signal** — slog + visibly blank floats cover v1.

On landing, remove the "Blocking for the scanner feature" item from
`docs/2026-07-07-engine-pre-live-checklist.md`.