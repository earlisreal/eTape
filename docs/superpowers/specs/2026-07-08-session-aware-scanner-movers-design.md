# Session-aware Scanner + Movers

**Date:** 2026-07-08
**Status:** Design (approved in brainstorming; awaiting spec review)
**Related:** `2026-07-07-scanner-float-fallback-design.md` (just merged — `a8b441d`),
`2026-07-03-ui-design.md`, `2026-07-03-premarket-scanner-api.md`

## Context

Today the scanner surface is split into two panels that are each hardwired to a
single session: `registry.tsx` mounts `ScannerPanel` once with `session="premarket"`
(the **Scanner**) and once with `session="rth"` (the **Movers**). Each panel is
therefore dead for half the trading day — Movers shows nothing pre-market, Scanner
shows nothing during RTH.

Worse, the engine (`engine/internal/scan/scan.go`) fetches rank data from the
**pre-market** API (`Qot_GetUSPreMarketRank`, protoID 3410) in *every* session and
only reads its pre-market fields. During RTH / post-market those fields are frozen at
the pre-market close, so the "RTH" and "after-hours" boards publish stale numbers
wearing a live-looking `refreshedAt`.

Goal: make both panels useful all day. Both **auto-follow the currently-live session**;
their only difference becomes their interaction model — **Scanner has filters, Movers
is a plain sortable board**. The engine fetches each session's data from the correct
per-session rank API so the board is genuinely live in pre-market, RTH, post-market,
and overnight.

## Goals

- Both panels render whichever session is currently live (pre-market → RTH →
  post-market → overnight), following the market automatically.
- Engine publishes **live** rank per session via the correct per-session API.
- Scanner keeps its threshold filters; Movers drops them (plain sortable board).
- No flash/sound storm at session rollovers.

## Non-goals (deferred)

- **Top-losers / most-active modes** in Movers — gainers-only for now (`SortDir`
  descending). Movers is a top-N gainers board that differs from Scanner only by
  having no filters.
- **Rolling 5-minute % change (momentum)** — the rank APIs report cumulative session
  change, not a 5-min rate. True 5-min momentum would need `Qot_StockFilter`
  `ChangeRate5min` (StockField 15, 10 req/30s); noted as a possible future extension.
- **Manual session selector** — auto-follow only (no UI to page back to an earlier
  session's board once it rolls over).
- **Broader universe paging** beyond the existing top-N (the `rank_pages` config is
  currently dead; wiring it is out of scope here).

## Change metric semantics (decided)

The board ranks by **each session's native change ratio** — i.e. "% vs the most-recent
regular-session close" — with **no recompute against a fixed prior close**:

| Session | API | Native change ratio |
|---|---|---|
| Pre-market | `Qot_GetUSPreMarketRank` (3410) | vs prior RTH close |
| RTH | `Qot_GetTopMoversRank` | vs prior RTH close |
| Post-market | `Qot_GetUSAfterHoursRank` | vs **today's** RTH close |
| Overnight | `Qot_GetUSOvernightRank` | vs most-recent RTH close (= prior close during the overnight window) |

**Consistency of "overnight = vs prior close":** the reference is always the last
*completed* RTH close. For a stock trading Monday night, post-market (16:00–20:00 Mon),
overnight (20:00 Mon–04:00 Tue), and Tuesday pre-market (04:00–09:30) all reference
**Monday's** close; it only rolls forward when Tuesday's RTH closes. So overnight
behaves exactly like pre-market in this respect.

**Intentional consequence:** because we use native ratios (not a fixed prior close), a
stock up 80% on the day but flat after-hours shows **~0%** in the post-market board —
that board reflects only the after-hours move. This is the accepted "vs most-recent
close" choice (the simpler option; avoids fetching a prior close per symbol).

## Engine design (`engine/internal/scan/`)

### Session model (`internal/session/session.go`)
Add an **`Overnight`** phase between `PostMarket` and `Closed`. It occupies the
current `Closed` gap on trading nights — provisionally **20:00–04:00 ET**. Exact venue
hours (moomoo overnight/Blue Ocean) are a **live-verify** item; keep the window a named
constant so it is trivially adjustable. `PhaseAt` returns `Overnight` in that window;
`Phase.String()` gains the case.

### Session-aware fetch (`scan.go`)
- `pollOnce` computes the phase **once** and passes it to the fetch, so the API and the
  publish `key` agree. `sessionOf` gains `Overnight → "overnight"`.
- `fetchRank` branches on phase and calls the correct proto, each normalized into the
  existing `rankItem{Symbol, ChangePct, Last, Volume}` (gainers-only, `SortDir`
  descending). Field getters differ per API — map each response's change / price /
  volume fields into `rankItem` (exact getter names are a live-verify item; the
  project's scanner-API doc confirms these are "same shape" siblings of 3410).
- Add protoID constants in `internal/feed/opend/protoid.go` for `Qot_GetTopMoversRank`,
  `Qot_GetUSAfterHoursRank`, `Qot_GetUSOvernightRank`. The compiled pb packages already
  exist under `internal/feed/opend/pb/` (`qotgettopmoversrank`, `qotgetusafterhoursrank`,
  `qotgetusovernightrank`).

### Unchanged
- **Float resolution** (`resolveFloats` / `snapshotBatch` / `floatEntry`, just merged)
  is session-agnostic and works for every session as-is.
- `rankRows` filter (`MinChangePct` / `MinVolume` / `MaxFloatShares`), `newHits`, and
  the `scanner.rank` / `scanner.hit` publish shape are unchanged.
- Poll cadence: `pollInterval` keeps RTH on `rth_ms` and everything else on
  `premarket_ms` (2 s) — fine for post-market/overnight spike detection.

### Day-reset ↔ overnight interaction (must handle)
`resetIfNewDay` clears the seen-sets **and** the float cache at the ET-day boundary
(midnight). Overnight sessions cross ET midnight, so this would clear state
**mid-session** and re-flash every overnight symbol. Decide during implementation:
move the reset boundary off midnight (e.g. ~04:00 or session-relative), or make the
reset seed the new seen-set silently. The same concern applies to the UI's
`msUntilEtMidnight` reset (below) — keep the two boundaries consistent.

## UI design (`ui/src/`)

- **`wire/contract.ts`** — add `"overnight"` to `ScannerSession`.
- **`data/ScannerStore.ts`** — add `currentView()`: returns the session view with the
  freshest `refreshedAt` (null until any data arrives → "waiting…"). The backend's
  session decision stays the single source of truth for "which session is live"; at
  each rollover the new session's `refreshedAt` overtakes and both panels switch
  automatically (latency ≈ one poll interval).
- **Suppress the rollover flash/sound burst** — `snapshotFrames` only baselines *new*
  subscribers, so a live client receives a session's first frame as a `delta` against an
  empty per-session seen-set, flashing all ~35 rows and firing one `scannerHit` chime
  each (via `sound/useSoundWiring.ts`). Fix: treat a session's **first delta** as a
  silent baseline (seed the seen-set without `isNewHit`/hit callbacks). Genuinely-new
  symbols still flash after that.
- **`chrome/panels/ScannerPanel.tsx`** — replace the `session` prop with
  `variant: "scanner" | "movers"`; read `stores.scanner.currentView()` instead of a
  fixed session; midnight reset → `resetSeen()` (all sessions). `variant === "movers"`
  hides the filter popover (`⚙ filters`) and the filter-summary row — leaving the plain
  sortable table (default sort % desc; column-click sort retained), the group-target
  swatches, and click-to-focus. Header keeps the session label
  (`SESSION_LABEL[currentSession]`), so the panel visibly announces which board is live;
  add the `"overnight"` label.
- **`chrome/panels/registry.tsx`** — both `scanner` and `movers` entries render
  `ScannerPanel` with the `variant` prop instead of a fixed `session`; refresh their
  `description` text.

## Live-verification items (against a running OpenD)

1. Each new API's request shape (outer `Request{C2S}` wrapper, required fields like
   `Market`) and its change / price / volume field getters → correct `rankItem` mapping.
2. `Qot_GetUSPreMarketRank` behavior during RTH (frozen vs. empty vs. error) — confirms
   the bug being fixed.
3. `Qot_GetUSOvernightRank` change-ratio reference point (expect: most-recent RTH close;
   normalize/note if not).
4. Overnight session window hours for the venue moomoo exposes.
5. Rate-limit headroom per API (all are request/response — **zero subscription quota**;
   3410 is 60 req/30s with large headroom at 2 s polling).

## Testing

- Engine: table-driven `fetchRank` per-phase API selection + `rankItem` mapping (mock
  `requester`); `sessionOf`/`PhaseAt` overnight window; `rankRows` unchanged
  (regression). Existing `scan_test.go` patterns.
- UI: `ScannerStore.currentView()` (freshest-`refreshedAt` selection; empty → null);
  first-delta silent-baseline suppression (no `isNewHit`, no hit callback);
  `ScannerPanel` variant behavior (movers hides filters); `registry.test.tsx`
  (reconcile with the already-dirty working-tree copy — do not clobber unrelated
  changes).
- End-to-end: run the engine against OpenD in each session window and confirm the panel
  swaps and shows live numbers (see verification items).

## Coordination / sequencing

The `scanner-float-fallback` branch merged to `main` (`a8b441d`) during design and
rewrote much of `scan.go` (removed 3215 universe → on-demand 3203 floats). **This spec
is written against that merged state.** No further coordination needed, but rebase onto
latest `main` before implementing since `scan.go` is the shared hot file.

Any visual treatment (session-label pill, Movers board styling) should go through the
`frontend-design` flow at implementation time.
