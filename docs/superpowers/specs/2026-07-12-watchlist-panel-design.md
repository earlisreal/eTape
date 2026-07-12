# Watchlist panel + demo-mode integration — design

**Date:** 2026-07-12
**Status:** approved
**Related:** `2026-07-08-scanner-driven-subscription-pool-design.md` (deleted the old
watchlist/`--watch`/`--focus` mechanism this feature does NOT resurrect),
`2026-07-08-on-demand-symbol-subscription-design.md` (demand system, type-to-load),
`2026-07-11-demo-synthetic-data-design.md` (synthetic universe, demo entry/exit).

## Purpose

A user-pinned symbol list as a first-class panel, plus demo-mode onboarding: when the
user switches to demo, the watchlist auto-populates with the synthetic universe and
auto-shows so the user discovers what symbols exist, open charts retarget to synthetic
symbols instead of toasting "unknown symbol", and Return-to-live restores everything.

## Decisions (user-fixed)

1. **Row data = quota-free snapshot polling.** The engine polls `Qot_GetSecuritySnapshot`
   (3203) for all watchlist symbols in one batched request and pushes rows over a WS
   topic — the Movers pattern. Zero subscription-quota slots. ~3s cadence.
2. **One global list**, shared across all windows/workspaces, persisted engine-side.
3. **Full revert on Return-to-live**, implemented as wholesale restore of the pre-demo
   workspace doc: panels opened during demo disappear, panels closed during demo come
   back. Demo is a throwaway sandbox. (Explicitly confirmed over surgical per-panel
   revert.)

## Architecture overview

Two structural moves carry the design:

1. **The rows push *is* the list.** One topic, `watchlist.rows`, carries a full snapshot
   (~3s cadence + immediate push on every mutation). Every window renders the last
   snapshot. There is no get-list query, no list-changed event, and no client-side
   membership cache to reconcile. Add/remove commands just change what the next push
   contains.
2. **Ownership splits along the existing boundary.** Engine owns the *data*: membership,
   validation, persistence, polling, demo seeding. UI owns *workspace shape*: which
   panels exist and each panel's symbol, because the engine has no model of dockview
   layout.

A verified negative locks in engine ownership: `TopicConfig` is declared in the wire
types but never published anywhere in the engine — a UI-managed config-key list could
not sync across windows without building that machinery first. First-class commands +
a push topic are the smaller diff.

## Engine

### New package `engine/internal/watchlist`

**`list.go` — `watchlist.List`**

```go
type configStore interface {          // satisfied by *store.Store
    GetConfig(key string) (string, bool, error)
    SetConfig(key, value string)      // async single-writer
    Flush()                           // durability barrier
}

func NewList(st configStore) (*List, error)   // loads config key "watchlist" (JSON array); absent → empty
func (l *List) Add(symbol string) (added bool, err error)  // dedupe; ErrFull past cap
func (l *List) Remove(symbol string) (removed bool)        // idempotent
func (l *List) Symbols() []string                          // copy, insertion order
func (l *List) Seed(symbols []string)                      // demo boot: replace wholesale, no probe
```

- **Persistence:** config key `"watchlist"` (JSON string array) via the store's existing
  async single-writer, with a synchronous `Flush()` after every mutation. Mutations are
  a-few-per-day; Flush cost is irrelevant and buys durability across the demo flow's
  *deliberate* process re-exec (an add moments before StartDemo must survive). A
  dedicated SQLite table was considered and rejected: for a personal list measured in
  dozens of symbols, a JSON blob through existing store methods is fewer moving parts,
  no migration, equally reviewable. Persistence is internal to the package — no new
  persistence wire command.
- **Hard cap: 400 symbols** (the 3203 batch ceiling — one request per tick, always).
  `Add` past the cap returns a typed error surfaced as a rejecting ack. Insertion order
  is preserved and is the payload's authoritative order.

**`poller.go` — `watchlist.Poller`**

```go
func NewPoller(list *List, r requester, pub publisher, clk clock.Clock, interval time.Duration) *Poller
func (p *Poller) Run(ctx context.Context) error
func (p *Poller) Poke()   // non-blocking wake: immediate membership publish + fresh poll
```

- `requester` is the existing `pollerRequester` seam (main.go, satisfied by both
  `opend.Client` and `synth.Requester`); `publisher` is satisfied by
  `hub.Publish(topic, key, payload)`; `clock.Clock` matches `scan.Poller`'s injection.
  **No new interfaces, no demo branches anywhere in this package.**
- Tick: default **3s**, config-tunable (`cfg.Watchlist.PollInterval`). One batched 3203
  for the whole list per tick; `ChangePct` computed from lastPrice vs prevClose
  (mirroring stockinfo's snapshot-field mapping); publish full snapshot.
- **Empty list → zero 3203 calls**, but the (empty) snapshot is still published each
  tick — the push-is-the-list invariant stays unconditionally true.
- **Batch-failure resilience:** binary-split-and-retry on a failed batch, lifted from
  `scan.Poller.snapshotBatch` (scan.go). Symbols are probe-validated at add time, so
  this is a delisting/edge safety net, not a hot path.
- **`Poke()` (single-writer, no races):** the poller goroutine is the only publisher of
  the topic. On poke it (1) immediately publishes a membership-updated snapshot from its
  row cache — new symbols appear instantly with dash placeholders, removed symbols
  vanish instantly — then (2) runs a fresh poll and publishes again.
- **Rate budget:** 1 req/3s = 10 of the shared 60 req/30s 3203 budget (alongside
  `scan.Poller`, stockinfo, `probe()`). Interval is the tunable escape hatch; if live
  measurement shows saturation, the pre-registered fallback is piggybacking on
  `scan.Poller`'s tick (not built now).
- Wiring: started inside the existing `startPollers(...)` (main.go), which already
  receives requester, hub, clock, and store. In `-demo` the same poller runs unmodified
  against `synth.Requester` (verified: it iterates multi-code 3203 batches).
- **Replay mode:** the poller runs against whatever the replay requester answers; if
  3203 yields no data the panel shows dash placeholders. Accepted v1 behavior — the
  watchlist's data columns are a live/demo feature.

### Wire protocol (payloads.go → `make gen-ts` → wsmsg.ts)

```go
type WatchlistRow struct {
    Symbol    string
    Last      *float64 // null = no print yet (ScannerRow convention)
    ChangePct *float64
    Volume    int64
}

type WatchlistRowsPayload struct {
    RefreshedAt *time.Time     // null until the first successful poll completes
    Symbols     []string       // authoritative membership + order — always current
    Rows        []WatchlistRow // may lag Symbols (mutation push, failed poll); keyed by Symbol
}

type WatchlistAddArgs    struct{ Symbol string }
type WatchlistRemoveArgs struct{ Symbol string }
```

- **`Symbols`/`Rows` split is deliberate:** membership is always instantly correct; row
  data may lag by up to one poll. The panel renders dash placeholders instead of rows
  vanishing — idempotent across missed pushes and reconnects. Demo-entry orchestration
  needs only `Symbols`, so it never waits on a poll.
- **Not reusing `ScannerRow`:** it carries `floatShares` (scanner-specific), and
  coupling the types means either's evolution drags the other.
- New topic `watchlist.rows` in the `Topic` union. Published via `hub.Publish`.
  **Hard requirement: the hub mirror must serve `watchlist.rows` snapshot-on-subscribe**
  (same as `scanner.rank` populating a late-opened Movers panel) — late-opened panels,
  second windows, and the demo-entry barrier all depend on it.

### Commands (commands.go, beside EnsureSymbol/ReleaseSymbol)

- **`WatchlistAdd{Symbol}`:** normalize (uppercase, ensure `US.` prefix — same
  normalization as type-to-load) → existing `probe()` (quota-free 3203 validation,
  positives cached, runs in the conn goroutine — exact `EnsureSymbol` pattern) →
  `list.Add` → `poller.Poke()` → ack. Rejections (`unknown symbol`,
  `watchlist full (400)`) return `Ack{status: rejected, reason}`. Duplicate add acks
  `accepted` (harmless no-op).
- **`WatchlistRemove{Symbol}`:** `list.Remove` → `poller.Poke()` → always accepted
  (idempotent). Non-optimistic in the UI — the row disappears on the poke-driven push.

### Demo seeding

In main.go's existing demo-boot block, **after** store init and **before** the WS
listener accepts connections: `list.Seed(<the 12 drawn universe symbols>)`. Race-free by
construction — the first `watchlist.rows` snapshot any client receives already carries
all 12. No protocol change, no probe (the synth universe is trusted by definition),
written into the throwaway temp `demo.db`.

**"Real watchlist untouched" is a structural guarantee, not code:** the demo process
runs against its own temp DB; the live store — where the real watchlist lives — is
never opened. Zero revert logic for the list itself.

## UI

### `WatchlistStore` (ui/src/data/)

Plain snapshot store: `symbols: string[]`, `rows: Map<symbol, WatchlistRow>`,
`refreshedAt`, and O(1) `has(symbol)`. Wired into `routeToStore` in `data/registry.ts`.
Deliberately none of `ScannerStore`'s flash/mute machinery — that highlights scanner
*churn*; a user-curated stable list has no equivalent event.

App.tsx subscribes the union of every catalog panel's topics up front (verified), so
adding `watchlist.rows` to the panel's topics makes the store globally warm in every
window, panel open or not. Consequences: the chart context-menu toggle is always
accurate, and demo-entry orchestration never depends on the watchlist panel having
mounted.

### `WatchlistPanel` — a new component, not a third ScannerPanel variant

ScannerPanel's two variants share topic, payload, and mutation model (none). Watchlist
differs on all three. Threading a third branch through every axis would degrade
ScannerPanel for its original modes — false reuse. What *is* reused: `sortColumns.ts`,
the row visual language, and `linkGroups.focus` verbatim.

- Registry: `PANELS["watchlist"]` — `topics: ["watchlist.rows"]`, **no `demand`
  profile** (the poller runs engine-side regardless of panel presence), title
  "Watchlist", added to `CATALOG_ORDER` after Movers.
- **Columns:** Symbol, Last, %Chg (sign-colored green/red), Volume. Default sort =
  payload order (insertion order, server-owned); all columns sortable via
  `sortColumns.ts`. No flash animation in v1.
- **Row interactions:** click = select highlight; double-click =
  `linkGroups.focus(config.group ?? "green", symbol)` — identical to ScannerPanel.
  Right-click = context menu.
- **Placeholders:** symbols in `Symbols` but absent from `Rows` render dashes.
- **Staleness:** if `now − refreshedAt` exceeds ~3 ticks (10s), dim data columns
  rather than blanking — degraded state stays visible and honest.
- **Add affordance:** a plain text input pinned in the panel body (not the ledger
  header's type-to-load slot). Enter → `WatchlistAdd`; clear on accept; warn toast on
  rejection. No symbol-search modal — none exists, none needed.
- **Remove affordance:** context menu only ("Remove from watchlist", danger-styled).
  No hover-× in v1.
- **Empty state:** short copy + the add input ("Add a symbol to start your watchlist").

### Context menus

Generalize `TVContextMenu`'s prop from `chrome: TvChrome` to `chrome: MenuChrome`, a
5-field structural subset — verified: the component uses exactly `surface`, `border`,
`text`, `hover`, `down`. `TvChrome` satisfies it structurally → **zero change at
ChartPanel's call site**. The app `Palette` lacks `hover`, so non-chart callers get a
one-function `menuChrome(palette): MenuChrome` adapter in `chrome/`.

| Surface | Entries |
|---|---|
| Chart (`buildMenuItems`, ChartPanel.tsx) | **Toggle**: `stores.watchlist.has(sym)` read synchronously at menu-open → "Add {sym} to watchlist" or "Remove {sym} from watchlist" (danger). Uses the chart's already-resolved symbol (link-group or settings.symbol). |
| Scanner/Movers row (their first context menu; one implementation covers both variants) | Single entry: "Add {sym} to watchlist" — unconditional, idempotent. YAGNI on more. |
| Watchlist row | Single entry: "Remove {sym} from watchlist" (danger). |

The toggle-vs-unconditional asymmetry is deliberate: the chart menu is where a user
manages a symbol they're focused on; scanner rows are a firehose where a redundant
idempotent add is harmless.

## Demo transition orchestration

### Why `applyWorkspace`, not settings patches (load-bearing correction)

Dockview invokes each panel's factory exactly **once**, at panel-creation time, and
keeps the element mounted for the panel's whole life — `config` is frozen inside the
factory closure (AppShell.tsx components map; PanelFrame's own comments; the
modalTracker singleton exists precisely because mounted PanelFrames can't receive live
props). `onConfigChange` flows panel→AppShell only. Therefore **patching
`ws.panels[i].settings` from AppShell does not update a mounted panel** — a
settings-patch transition would persist symbols without changing what any panel shows.
The only live retarget paths today are the LinkGroups observable (grouped panels) and a
full remount.

`applyWorkspace(next)` (the tested preset/import path) is the correct seam: it silently
hydrates LinkGroups from `next.groups` (no bus post, no engine echo), replaces
`ws`/`wsRef`, saves, and rebuilds the dockview grid (`api.clear()` +
`api.fromJSON(next.layout)`) — every panel remounts with fresh frozen configs. This
retargets grouped *and* pinned panels in one move and makes revert symmetrical. Full
remount at a mode boundary is appropriate: the entire engine process and dataset just
changed.

### Pure planner + thin AppShell glue

All decision logic lives in a pure module `ui/src/chrome/demoTransition.ts`
(typeToLoad-style, fully unit-testable); AppShell runs it from a `prevModeRef`-gated
effect that fires on these edges:

- `live → demo` or `replay → demo` — entry **with snapshot** (any real session → demo)
- `pending → demo` — joined mid-demo (page load into a demo engine): entry, **no snapshot**
- `demo → live` — revert (GoLive always relaunches into live; demo → replay is blocked
  engine-side)

`demo → demo` (mid-demo WS reconnect) is not an edge and never re-runs entry — user
symbol changes made during demo are preserved.

```ts
interface DemoContext {
  snapshot: Workspace | null;   // structuredClone of the pre-demo doc; null on pending→demo
  universe: string[];           // Symbols from the first demo watchlist.rows push
}

planDemoEntry(current: Workspace, universe: string[]): Workspace  // patched doc (groups + pinned symbols)
planDemoRevert(ctx: DemoContext, current: Workspace): Workspace   // restored or fallback-patched doc
```

### Entry sequence

1. Socket reconnects; app-level subscriptions re-establish; hub snapshot-on-subscribe
   delivers `watchlist.rows` (seeded pre-listen — `Symbols` complete even if
   `RefreshedAt` is null). `sys.session` flips mode to `"demo"`.
2. On the `live→demo` edge: capture `snapshot = structuredClone(wsRef.current)` into a
   module-level in-memory ref (the tab never reloads across the relaunch, so the ref
   survives the whole demo session).
3. Gate on `WatchlistStore.symbols` non-empty — a WS-message-driven barrier, never a
   timer (typically already true by the time the session snapshot lands). Safety
   timeout ~5s: if it never fills (unexpected), skip auto-load gracefully and leave
   panels as-is rather than deadlock.
4. `planDemoEntry` builds the patched doc:
   - **Link-group focus map rewritten deterministically** over the sorted universe:
     green→uni[0], red→uni[1], blue→uni[2], yellow→uni[3] — all four fixed groups,
     whether or not the doc uses them. Deterministic + window-agnostic, so every window
     computes the same mapping and the silent hydrate causes no cross-window divergence.
   - **Pinned symbol-bearing panels** (chart, tape, ladder — anything resolving its own
     `settings.symbol`) cycle uni[4:] in stable panel-id order, wrapping if needed.
5. `applyWorkspace(patchedDoc)`; then auto-add the watchlist panel if absent via
   `addPanel("watchlist")` — appended separately rather than baked into the planned doc
   so dockview computes its grid placement (hand-building serialized layout JSON is
   fragile). No auto-added-panel bookkeeping is needed: revert-with-snapshot drops the
   panel because it isn't in the snapshot. **Enabling change:** `addPanel` switches from
   the render-time `ws` closure to `wsRef.current` (aligning it with `removePanel`/
   `onGroupChange`'s documented wsRef discipline) so it composes with `applyWorkspace`
   in the same tick through the pending queue.
6. Remounted panels ensure their new synth symbols (validated in-memory, no toasts).
   Signal transition-complete (releases the reannounce gate).

On `pending→demo` the same entry runs with `snapshot = null` — it still normalizes any
non-universe symbols and ensures the watchlist panel is present.

### Revert sequence

- **With snapshot** (`live→demo` happened in this tab): `applyWorkspace(ctx.snapshot)`.
  The pre-demo doc comes back exactly — symbols, groups, layout — and the auto-added
  watchlist panel is gone because it isn't in the snapshot. A watchlist panel that
  existed *before* demo is in the snapshot and stays. Panels opened mid-demo vanish;
  panels closed mid-demo return (user-confirmed semantics).
- **Without snapshot** (hard refresh mid-demo, then GoLive): fallback doc = current doc
  with every `settings.symbol` ∈ `ctx.universe` patched to the default seed (`US.AAPL`)
  and every `groups` focus entry whose symbol ∈ `ctx.universe` replaced with the same
  default. Nothing stays wedged on a fictional symbol, and — critically — demo state is never written into the
  real DB as "restored" state. The watchlist panel lingers (without a snapshot there is
  no way to know it was auto-added); the user closes it manually. Same failure family
  as the pre-existing refresh-mid-demo workspace quirk, not a new one.

### Reannounce gating

`DemandRegistry.reannounce()` fires on socket-open and is fire-and-forget (verified: no
toasts on rejection) — but it sends doomed `EnsureSymbol`s across mode boundaries (real
symbols against synth on entry; synth symbols against live 3203 on exit) and leaves
stale `live` entries. Mitigation: the registry defers reannounce until the
post-reconnect session mode is known, via an injected `reannounceGate: () => Promise<void>`
wired in App.tsx:

- Mode unchanged from last-known (normal engine restart / WS blip): resolve on the
  first `sys.session` snapshot — one round-trip of added latency in the common path.
- Mode changed (demo boundary): resolve when AppShell signals transition-applied, with
  a ~5s safety timeout so a panel-less client can never deadlock demand recovery.

Deliberately **not** clearing `live` on mode change — that would starve panels whose
symbol is unchanged across the transition. After `applyWorkspace` remounts, panels'
ensures have already upserted correct symbols; the eventual reannounce is idempotent.

### Multi-window

`sys.session` mode is engine-global; each window independently runs the same edges
against its own workspace doc (`?workspace=` URL param — per-window docs are the
supported configuration). Each window auto-shows its own watchlist panel and reverts
its own doc. Group assignments are identical across windows by construction
(deterministic mapping); hydrate is silent, so no BroadcastChannel fights. Two windows
pointed at the *same* workspace name already last-writer-clobber each other's debounced
saves today — pre-existing, out of scope.

## Testing

**Go**
- `watchlist/list_test.go`: add/remove/dedupe/normalization, 400-cap rejection, persist
  round-trip against a fake `configStore`, `Seed` replace semantics,
  Flush-called-on-mutation.
- `watchlist/poller_test.go` (fake clock + fake requester, scan_test style): cadence;
  empty list publishes but issues zero requests; `Poke` publishes membership
  immediately then refreshed rows; `Symbols`/`Rows` divergence on partial failure;
  binary-split on batch error.
- commands: WatchlistAdd probe-reject ack, duplicate-add accepted, remove idempotent.

**UI (vitest, pure functions — no React/WS/dockview)**
- `demoTransition.test.ts`, table-driven: entry with zero symbol panels; more pinned
  panels than universe (wrap-around); watchlist already open (no auto-add, survives
  revert); deterministic group mapping; revert-with-snapshot exact restore;
  pending→demo (no snapshot) entry; no-snapshot revert fallback (universe symbols →
  default, group entries cleared).
- `WatchlistStore.test.ts`: snapshot apply, `has()`, placeholder/staleness fields.
- `DemandRegistry.test.ts` additions: gate defers reannounce; timeout releases it;
  unchanged-mode fast path.

## Risk register

| Risk | Mitigation |
|---|---|
| Settings patches don't reach mounted panels (frozen factory closures) | Root of the corrected design: transitions go through `applyWorkspace` remounts, the tested preset/import path. |
| Transition noise (reannounce fires doomed ensures pre-apply) | Reannounce gate (§ above); entry barrier is WS-driven and typically one round-trip. |
| Mid-demo refresh poisoning the real workspace after GoLive | `pending→demo` captures no snapshot; no-snapshot revert patches universe symbols to the default seed — demo state can't be restored into the real DB. |
| 3203 budget saturation | 10/30s share, config-tunable interval, empty-list zero-call, pre-registered fallback (piggyback scan tick). |
| Batch-failing codes | Binary-split retry lifted from scan.go; probe-at-add makes it a safety net. |
| New topic missing from hub mirror | Named a hard requirement; covered by an integration check in the plan. |
| Shared-workspace multi-window clobber | Pre-existing with same-name windows; unaffected; out of scope. |

## Accepted tradeoffs

- Cross-window add/remove latency is one poke-push (sub-second), not local echo.
- No flash/diff highlighting, no drag-to-reorder, no rename/multiple lists, no hover-×
  in v1. Cap stays 400 (batch ceiling). %Chg = last vs prevClose in v1 (no
  session-aware pre-market base).
- Every open window auto-shows its own watchlist panel on demo entry.
- `applyWorkspace` remounts all panels at the mode boundary (canvases rebuild) —
  appropriate for a whole-session context switch.
- Hard refresh mid-demo loses the revert snapshot; fallback un-wedges symbols, the
  auto-added panel lingers.
- Replay mode: watchlist panel shows membership with dash placeholders unless the
  replay requester answers 3203.

## Implementation order

1. Engine: `watchlist` package (List + Poller) + wire types + commands + demo seed +
   `startPollers` wiring + `make gen-ts` (+ hub mirror coverage for the new topic).
2. UI data: `WatchlistStore` + topic routing + App.tsx subscription union.
3. UI panel: `WatchlistPanel` + registry/catalog entry.
4. Context menus: `MenuChrome` generalization + `menuChrome(palette)` adapter + three
   menu surfaces.
5. Demo transitions: `demoTransition.ts` planners + AppShell edge-effect + `addPanel`
   wsRef alignment + DemandRegistry reannounce gate.
6. Tests alongside each step (plan-mandated coverage: no deferral).

Key files: `engine/internal/watchlist/{list,poller}.go` (new),
`engine/internal/uihub/{commands.go,wsmsg/payloads.go}`, `engine/cmd/etape/main.go`
(demo seed + startPollers), `ui/src/data/{WatchlistStore.ts,registry.ts}`,
`ui/src/chrome/panels/{registry.tsx,WatchlistPanel.tsx}`,
`ui/src/chrome/panels/tv/TVContextMenu.tsx`, `ui/src/chrome/{demoTransition.ts,AppShell.tsx}`,
`ui/src/wire/DemandRegistry.ts`.
