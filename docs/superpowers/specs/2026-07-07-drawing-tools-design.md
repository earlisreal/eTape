# Chart Drawing Tools — Design

**Date:** 2026-07-07
**Status:** Approved (Earl, 2026-07-07)
**Scope:** UI-only. Zero engine changes — drawings persist through the existing generic
`GetConfig`/`SetConfig` KV commands; no new wire messages.

## Decision summary

Hand-rolled on the Lightweight Charts primitives API — the pattern the codebase already
uses (`SessionShadingPrimitive`, `DiamondFillPrimitive`) — not the third-party
`lightweight-charts-drawing` plugin. The plugin (MIT, peer-dep `^5.0.0`) was evaluated
and declined: v0.1.1 with a single release day (2026-02-26, untouched since), unknown
sub-minute/cross-timeframe anchor behavior, a per-chart `DrawingManager` that owns state
eTape needs to own for sync, and ~64 unused tools — all against the reviewability rule.
It remains a permitted MIT reference crib for hit-test/renderer math during
implementation.

Decisions (Earl, 2026-07-07):

| Decision | Choice |
|---|---|
| Tool set (v1) | Horizontal line, horizontal ray, trendline, ray, rectangle, + transient price-range measure |
| Sync keying | **Symbol-keyed** (TradingView behavior) — any chart showing the symbol shows its drawings; group sync falls out because grouped charts share a symbol |
| Lifetime | Until deleted; per-symbol clear-all; survives restarts |
| Toolbar | Slim always-visible left rail (~28 px) overlaid per chart |
| Ladder integration | Not in v1, but the store is chart-agnostic so the DOM ladder can read h-line levels in a fast follow |

## Data model (`ui/src/render/chart/drawings/model.ts`)

```ts
type DrawingKind = "hline" | "hray" | "trendline" | "ray" | "rect";
interface Anchor { timeMs: number; price: number }   // epoch ms + raw price
interface Drawing {
  id: string;              // crypto.randomUUID()
  symbol: string;          // "US.AAPL"
  kind: DrawingKind;
  anchors: Anchor[];       // hline/hray: 1, trendline/ray/rect: 2
  createdMs: number;
  updatedMs: number;
}
```

- `hline`: y at `price`, spans the full pane width (`timeMs` recorded at creation,
  unused for geometry). `hray`: from `timeMs` rightward at `price`. `trendline`:
  segment between anchors. `ray`: through both anchors, extended right indefinitely.
  `rect`: opposite corners.
- No per-drawing style in v1: each kind renders in one fixed style derived from the
  Daylight Ledger palette, so theme switches restyle drawings automatically and the
  store holds no colors.
- The measure tool is transient UI state — never a `Drawing`, never stored, never
  synced.
- `model.ts` exports a validator used on load: entries failing shape/enum checks are
  dropped (count logged), never crash a chart.

## DrawingStore (`store.ts`)

A shared store beside `bars`/`indicators`/`fills`, created in the same `makeStores()`
call, following the existing revision-counter contract:

- `forSymbol(sym): Drawing[]` · `upsert(d)` · `remove(id)` · `clearSymbol(sym)` ·
  `getRev(): number` · `ensureLoaded(sym): void`
- **Same-window sync:** all panels share the one store; `ChartPanel.isDirty()` adds a
  `drawingsRev` check next to bars/indicators/fills — no new paint machinery.
- **Cross-window sync:** `BroadcastChannel("etape.drawings")` carrying
  `{op: "upsert" | "remove" | "clear", …}` messages, mirroring `linkGroups.ts`. Remote
  messages apply locally without re-publishing and without re-persisting (echo-guard,
  single-writer: only the originating window calls `SetConfig`).
- **Persistence:** one KV key per symbol — `drawings.<SYMBOL>` holding the full array —
  via the injected command client, debounced ~500 ms per symbol (the `WorkspaceStore`
  pattern). Last-write-wins per key is sufficient for a single user.
- **Lazy load:** `ensureLoaded(sym)` fires `GetConfig` once per symbol per session
  (called on chart symbol apply); absent/malformed → empty list. Loads never block
  rendering.

## Rendering (`primitive.ts`)

One `DrawingsPrimitive` per chart, attached to the candle series with
`zOrder: "top"` (drawings above candles and indicator lines), like
`DiamondFillPrimitive`. It draws, for the chart's current symbol: all persisted
drawings, the selection highlight + endpoint/corner handles, the placement ghost
(anchor 1 committed, second point tracking the cursor), and the measure box
(Δ points, Δ%, bar count). Pan/zoom repaints come free with the primitive contract.
Repaint triggers split by rate: persisted-drawing changes ride the existing
rAF-coalesced scheduler — `ChartPanel.isDirty()` adds a `drawingsRev` check and the
paint callback calls the primitive's `requestUpdate` — while transient overlay state
(ghost tracking, drag feedback, measure box, selection) calls `requestUpdate`
directly from the interaction layer for sub-frame responsiveness mid-gesture.

### Time→x across timeframes

The trading workspace shows 1m + 10s of the same symbol, so an anchor's `timeMs`
usually isn't a bar time on the other chart. Each chart resolves x itself with a pure
function `(timeMs, bars, timeframeMs) → fractional logical index`:

- Binary-search the chart's own bar array for the surrounding bars; interpolate a
  fractional logical index between them; convert via
  `timeScale.logicalToCoordinate()`.
- Beyond the data at either end, extrapolate by the chart's bar interval (rays keep
  pointing into the future; an anchor before the loaded history keeps the line's
  true slope rather than clipping it).
- `hline` needs no x at all; `hray`/`rect`/lines use the mapping per anchor.

### Facade additions

`ChartApiFacade` grows: `logicalToCoordinate`, `coordinateToLogical`,
`coordinateToPrice`, `setPanZoomEnabled(on: boolean)` (wraps
`applyOptions({handleScroll, handleScale})`). `ChartController` is untouched — the
drawings layer sits beside it sharing the facade.

## Interaction (`interaction.ts`)

`DrawingInteraction` — a plain class per chart (no React), pointer + key handlers on
the chart host div, converting px ↔ `{timeMs, price}` through the facade. State
machine:

- **`select`** (default): click hit-tests in pixel space — point-to-segment distance
  (~5 px threshold) for lines, edges/corners for rects, handle radius for endpoints.
  Hit selects; drag on body moves the whole drawing (all anchors shifted); drag on a
  handle moves that anchor; `Delete` removes the selection; `Esc` deselects; click on
  empty space deselects.
- **`armed(tool)`**: crosshair cursor; first click places anchor 1 and shows the ghost;
  second click commits (hline/hray commit on the first click). After commit, revert to
  `select` (TradingView behavior). `Esc` cancels placement.
- **`measure`**: drag shows the live box; it stays after release until the next
  pointerdown or `Esc`; never persisted.
- **Pan/zoom lock:** while armed or dragging, `setPanZoomEnabled(false)`; restored on
  commit/cancel/release — otherwise every drag also pans the chart.
- **Magnet** (toolbar toggle, default on): when placing an anchor or dragging a
  handle within ~6 px of the hovered bar's O/H/L/C, snap price to it. Time snapping
  is separate and always on: while placing or handle-dragging, anchors snap to the
  hovered bar's time on the drawing chart (stored as that bar's `timeMs`).
- **Symbol switch mid-gesture** (link group moves under the chart): cancel any
  in-progress placement/drag, drop selection, restore pan/zoom.
- Mutations flow `interaction → store` only; the primitive re-renders off the store,
  so a drag on one chart live-updates every chart showing the symbol.

## Toolbar (`ui/src/chrome/panels/DrawingRail.tsx`)

React overlay rail (~28 px wide) inside the chart host's left edge, below the
existing `ChartControls` row, styled with Daylight Ledger tokens. Buttons, top to
bottom: **cursor · h-line · h-ray · trendline · ray · rect · measure · magnet
(toggle) · trash**.

- Active tool is per-chart React state (low-rate chrome — allowed), pushed
  imperatively into `DrawingInteraction`; the active button gets the selected
  treatment.
- **Trash:** with a selection, deletes it; with none, opens a confirm popover —
  "Clear all drawings for `<symbol>`" — which clears every chart showing that symbol
  (by design; it's one symbol-keyed set).
- Keyboard: `Esc` and `Delete` only. No letter hotkeys in v1 — type-to-load owns bare
  keystrokes on symbol-bearing panels.

## Error handling

- **Load failure / malformed data:** validator drops bad entries, chart starts with
  what survived; never blocks the chart.
- **Save failure:** toast with the engine's reason; in-memory drawings stay
  authoritative for the session; dirty state retries on the next mutation/debounce
  tick.
- **No `BroadcastChannel`:** single-window mode fully works — sync is layered on the
  store, not a dependency.

## Testing

- **Pure geometry** (`geometry.ts`): hit-test math, time→fractional-logical
  interpolation + right-extrapolation, magnet snapping — plain test tables.
- **`DrawingStore`:** rev semantics, echo-guard (remote apply never re-publishes or
  re-persists), lazy load, per-symbol debounced persist — fake bus + fake command
  client, mirroring `linkGroups.test.ts`.
- **`DrawingInteraction`:** state-machine transitions from synthetic pointer/key
  events against a stub facade, including pan/zoom lock toggling and mid-gesture
  symbol switch.
- **Integration** (`ChartPanel.test.tsx` style): two panels, one store — a store
  mutation repaints both; placement on one syncs to the other.
- Canvas-rendering smoke tests run on the main checkout, not worktrees (known
  node-canvas/vitest fork-pool quirk).

## File layout

```
ui/src/render/chart/drawings/
  model.ts        # types + validation
  geometry.ts     # hit-test, interpolation, magnet (pure)
  store.ts        # DrawingStore + bus sync + persistence
  primitive.ts    # DrawingsPrimitive renderer
  interaction.ts  # pointer/key state machine
ui/src/chrome/panels/DrawingRail.tsx
```

Wiring lives in `ChartPanel` (as fills/indicators do today).

## Sequencing

Independent of the Daylight Ledger redesign execution except the rail's visual
tokens; whichever lands first, the other rebases trivially.

## Non-goals (v1)

- Per-drawing style editing (color/width/labels) and drawing lock/hide.
- Ladder rendering of h-line levels (store shape already supports it — fast follow).
- Letter hotkeys, fibs, text/notes, arrows, alerts on levels, per-timeframe
  visibility filters.
