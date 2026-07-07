# eTape — On-Demand Symbol Subscription Design (v1)

**Date:** 2026-07-08
**Status:** Approved (design); implementation plan not yet written.
**Depends on:** `docs/superpowers/specs/2026-07-03-go-engine-design.md` (feed
demand model, uihub, journal), `docs/superpowers/specs/2026-07-03-ui-design.md`
(panels, link groups, type-to-load), `docs/superpowers/specs/2026-07-07-ui-redesign-design.md`
(PanelFrame, workspace, catalog).

## Purpose

Today the engine subscribes to moomoo market data only for symbols named at boot
(`[feed] watchlist` in config.toml, `--watch`/`--focus` CLI flags). Typing a new
symbol into a chart, ladder, or tape panel changes what the panel *asks the stores
for*, but nothing tells the engine to subscribe — the panel stays empty unless the
symbol happened to be in the boot set. This spec wires the UI to the engine's
existing refcounted demand model (`feed.Ensure`/`Release`, subman LRU + hysteresis)
so any symbol loaded into a panel subscribes on demand, releases on switch, and
validates existence before the UI commits the change.

The boot-time watchlist stays: it is the pre-subscribe path that gives the day's
planned symbols full-session tick history for 10s bars (`get_rt_ticker` backfill is
~1,000 ticks ≈ <1s on hot symbols). On-demand symbols get whatever OpenD's
quota-free caches hold at ensure time (a day of 1m bars, ~1,000 ticks, book+quote
snapshot) — full context for ≥1m timeframes, thin 10s history growing from
subscribe time.

## Decisions made during brainstorming

| Question | Decision |
|---|---|
| Demand channel | **Per-panel imperative commands** (`EnsureSymbol`/`ReleaseSymbol`) mapping 1:1 onto `feed.Ensure`/`Release`; hub tracks per-connection demand sets and auto-releases on disconnect |
| Subscription profile | **Panel-driven**: chart/tape → `watch` (TICKER+KL_1M, 2 slots); ladder → `focused` (+QUOTE+BOOK, 4 slots, eviction-proof); news → `interest` (no subs, 0 slots, news rotation only) |
| Release semantics | **Release on switch/unmount** (upsert-by-id on switch); subman's 5-min hysteresis is the flip-back grace window; longer grace is a config knob (`unsub_hysteresis_secs`), not new code |
| Validation | **Existence probe before ack**: single-symbol `Qot_GetSecuritySnapshot` (3203, subscription-free, quota-free) with session-lifetime positive cache; typos reject and the UI reverts |
| Quota-full behavior | **Never reject on quota** — `subman.desired()` gives newest demands slots and starves the oldest watch-profile symbols (already logged). Rejecting the active trade's load to protect a stale watchlist sub would be backwards |
| News integration | News poller symbol closure becomes config watchlist ∪ CLI `--watch`/`--focus` ∪ live hub demands (closes the "--focus skips news" pre-live debt item) |
| `FocusGroup` command | Upgraded from no-op to format check + existence probe + honest ack — `LinkGroups.focusChecked`'s revert-on-reject machinery starts working with zero UI changes on the grouped path |

Rejected alternatives: **declarative per-connection set-sync** (idempotent by
construction but batch acks make one-symbol reject/revert UX awkward, and it forces
the UI to centralize demand computation); **engine infers demands from persisted
workspace JSON** (couples engine to UI persistence shape, no ack UX, can't
distinguish mounted panels from saved workspaces); **always-Focused profile**
(burns quota ~2× and marks casual lookups eviction-proof); **sticky-for-session
demands** (a day of lookups pins quota; active demands are never evicted so the
100-slot wall is reachable).

## Wire contract (uihub commands)

All three follow the existing command/ack dispatch in `uihub/commands.go`; args
structs live in the Go `wsmsg` package and regenerate into `ui/src/gen/wsmsg.ts`
via tygo.

### `EnsureSymbol { demandId, symbol, profile }`

`profile ∈ "watch" | "focused" | "interest"`. Handler steps, in order:

1. **Shape check** — same market-prefix allowlist as the UI's `normalizeSymbol`
   (`US.`, `HK.`); anything else → `blocked("unsupported market")`. The engine
   never trusts the client's normalization.
2. **Existence probe** (skipped for cache hits and when `Feed` is nil):
   `Validate(ctx, symbol)` → clean OpenD "no such symbol" →
   `blocked("unknown symbol <sym>")`; transport error/timeout →
   `blocked("feed unavailable")`.
3. **Profile → demand mapping** (hub-side, single enforcement point):
   - `watch` → `Subs: [TICKER, KL_1M]`, `Focused: false`
   - `focused` → `Subs: [QUOTE, TICKER, KL_1M]` **+ BOOK only for `US.` symbols**
     (HK entitlement is LV1; a book sub would fail and retry forever in the
     subman), `Focused: true`
   - `interest` → `Subs: nil` (no quota; symbol appears in the hub demand set for
     the news rotation only)
4. **`feed.Ensure`** with internal id `dyn/<connID>/<demandId>` — upsert: re-ensuring
   the same demandId with a new symbol atomically swaps the demand; the old
   symbol's subs enter hysteresis. A rejected ensure (steps 1–2) leaves the prior
   demand for that id untouched.

The tape panel's KL_1M sub is deliberate over-subscription: any symbol on screen
gets 1m bars journaled, consistent with record-from-day-one; a third
tape-only profile is not worth the surface.

### `ReleaseSymbol { demandId }`

Releases `dyn/<connID>/<demandId>`; unknown id is a no-op; always accepted.

### `FocusGroup { group, symbol }` (existing command, upgraded)

Format check + existence probe + ack. Registers **no** demand state — demands
arrive from member panels as they follow the group. Replay mode acks accepted.

## Engine wiring

- **`feedCtl` seam** (pattern matches `execDoer`/`configStore`):

  ```go
  type feedCtl interface {
      Ensure(d feed.Demand)
      Release(id string)
      Validate(ctx context.Context, symbol string) error
  }
  ```

  `uihub.Config` gains `Feed feedCtl`. Live: `main.go` passes the `OpenDFeed`.
  Replay/tests: nil → probe skipped, ensure/release no-op, every ack accepted —
  Playwright E2E boots unchanged.
- **`OpenDFeed.Validate`** — new method: single-symbol snapshot via a new small
  `Qot_GetSecuritySnapshot` (3203) helper in `feed/opend` (the same helper the
  scanner float-universe fallback needs, per the pre-live checklist), ~2s timeout,
  engine-process-lifetime positive cache (negatives are not cached: intraday listings
  must not be locked out, and the RPC is cheap). Rate limit 60 req/30s is
  irrelevant at interactive typing rates.
- **Per-connection demand tracking** — the hub records each connection's live
  demand ids; the existing `hub.Unregister(c)` teardown (deferred in `conn.run`)
  releases them all. A killed tab cannot leak quota; hysteresis keeps the subs
  warm ~5 min so a reload re-ensures with zero churn. `dyn/` ids are disjoint
  from boot ids (`boot-watch-*`/`boot-focus-*`) by construction.
- **News closure** — `startPollers` composes: config watchlist ∪ CLI
  `--watch`/`--focus` symbols ∪ `hub.ActiveDemandSymbols()` (new snapshot method,
  includes `interest` demands), deduped. The poller itself is untouched (single
  `WatchMs`-cadence rotation).
- **Demand id note** — `subman.Ensure` already upserts by id and `Release` is
  already a no-op for unknown ids; no subman changes are needed beyond test
  coverage for empty-subs demands.

## UI wiring

- **`PanelDef.demand?: "watch" | "focused" | "interest"`** — chart: `watch`,
  tape: `watch`, ladder: `focused`, news: `interest`; all other panels omit it.
  Without `interest`, a pinned news panel on a symbol nothing else shows would
  stay permanently blank under the news-follows-demands decision.
- **`DemandRegistry`** (new class, `ui/src/wire/`, owned by `App.tsx` alongside
  `LinkGroups`):
  - `ensure(panelId, symbol, profile): Promise<AckMsg>` — sends `EnsureSymbol`
    (demandId = panel instance id), records locally, returns the ack;
  - `release(panelId)` — sends `ReleaseSymbol`, forgets locally;
  - on `WsClient.onState` reconnect, **re-announces every live demand** — covers
    WS drops and engine restarts (engine demand state is in-memory by design;
    the UI is the source of truth for what's on screen).
- **PanelFrame** — one new effect: when `def.demand` is set and the effective
  symbol (group symbol or `settings.symbol`) changes, `void registry.ensure(...)`;
  `release` on unmount. Symbol switches need no explicit release (upsert).
  Unexpected rejections on this fire-and-forget path toast, same as the existing
  commit error pattern.
- **Commit paths:**
  - *Grouped type-to-load*: unchanged — `focusChecked` → `FocusGroup` → probe →
    reject reverts the group (machinery already built). After an accepted move,
    member panels' effects fire their ensures (probe cache hit).
  - *Pinned type-to-load*: the pinned branch of `PanelFrame.commit` awaits
    `registry.ensure(...)` before `onConfigChange` — accepted → apply, blocked →
    toast + revert. Pinned panels gain the same typo protection grouped panels
    have.
  - *Remote windows*: panels following a bus symbol change ensure on their own
    connection — demands are conn-scoped, multi-window needs no coordination.
- **Untouched:** stores, topics, mirror, painting, `App.tsx`
  subscribe-all-topics. Seeded bars/ticks/book arrive on the same broadcast
  topics panels already consume.

## Error handling & edge cases

- **Probe failure modes** — "unknown symbol" vs "feed unavailable" are distinct
  reject reasons (see wire contract); type-to-load fails fast instead of hanging
  or loading a dead panel.
- **Sub failure after accepted ensure** (entitlement edge, OpenD hiccup) — subman
  already logs and retries every pass; no new machinery. The systematic case (HK
  book on LV1) is prevented by the US-only book guard.
- **Quota pressure** — newest demands win slots; oldest watch-profile symbols
  starve (subman logs transitions). `focused` demands sort first and effectively
  never starve (25 simultaneous focused symbols would be needed to exhaust the
  100-slot budget). Boot watchlist symbols are the designed starvation victims —
  their 10s journal continuity gaps under pressure, which is the correct
  trade-off.
- **Rapid flipping transient** — flipping a ladder through many symbols pins each
  old symbol's slots for moomoo's 60s min-hold; a burst can briefly starve
  watchlist symbols until holds expire. Self-healing via the subman's
  starve-and-retry loop; the probe rejecting typos limits accidental flips.
- **Lifecycle races** — per-connection commands dispatch sequentially and ensure
  is an upsert, so fast A→B→C flips settle on C; a rejected ensure never
  disturbs the demand it would have replaced.
- **Out of scope (pre-live checklist)** — symbols with open positions are not
  auto-demanded: close every panel showing a position's symbol and its P&L marks
  freeze until re-subscribed. Pre-existing gap with the fixed watchlist;
  release-on-switch makes it marginally more reachable. Starvation surfacing in
  the health panel (subman `Slots()`/`Starved()` are currently unconsumed) is a
  possible follow-up, not v1.

## Testing

- **Engine** — `commands_test.go`: all three handlers against a spy `feedCtl`
  (accept/reject/feed-unavailable, profile mapping incl. US-book-guard and
  `interest`, per-conn namespacing, upsert, release-unknown no-op, nil-feed
  replay behavior). Hub lifecycle: demands released when a conn dies.
  `OpenDFeed.Validate` against the fake rpc seam: exists / not-exists /
  transport error / cache hit (second call makes no RPC). subman: one
  empty-subs demand case proving zero quota use. News closure composition test.
- **UI (vitest)** — `DemandRegistry` (send, bookkeeping, reconnect re-announce);
  `PanelFrame` (ensure on mount/switch, release on unmount, pinned commit ack
  gating); panel-def profile table.
- **E2E (Playwright, replay)** — existing type-to-load flows are the regression
  net; replay's accept-everything behavior means no new scenarios required.
