# eTape — UI Redesign: Daylight Ledger (v1.1)

**Date:** 2026-07-07
**Status:** Approved (design); implementation plan not yet written
**Depends on:** `docs/superpowers/specs/2026-07-03-ui-design.md` (the v1 UI design — this
document revises its visual system, workspace seeding, symbol-linking UX, and several
panel presentations; everything not mentioned here — data plane, stores, painters'
architecture, order entry/hotkeys, engine topics — stands as specified there)

## Purpose

The implemented v1 UI is architecturally sound (dockview layout, engine-persisted
workspaces, canvas painters, link groups) but visually broken: fonts declared but never
loaded, chrome styled ad hoc with inline objects, native unstyled form controls, and
light/dark styles mixed on one screen. This redesign gives eTape a deliberate visual
identity ("Daylight Ledger"), replaces the seeded-workspace startup with a blank
workspace + panel catalog, moves symbol-group control onto the panels themselves, and
adds type-to-load symbol switching. Pure UI work: **no engine changes required.**

## Decisions made during brainstorming

| Question | Decision |
|---|---|
| Visual direction | **Daylight Ledger** (chosen over "Carbon & Amber" refined-dark and "Instrument Panel" midnight-blue): warm paper surfaces, ink text, serif ledger panel headers over a double rule; light is the default (Earl's preference), dark stays as a Settings toggle |
| Color discipline | Green/red are reserved exclusively for market direction (candles, bid/ask, buy/sell prints, P&L). All state — focus, armed, in-flight orders, scanner hits, today's news, working-order marks — uses **bronze**. Kill switch and rejects use a distinct danger red |
| Startup | Blank workspace + catalog, one window. The monitoring/trading **seeds stop auto-loading**; their layouts become catalog **presets** (rebuilt with sane proportions) loadable into any workspace |
| Catalog shape | Empty state shows presets as the hero with a compact panel index beside ("presets first"); once a panel exists, the same catalog lives behind a top-bar **+ Add panel** dropdown |
| Multi-window | Start with one window (`main`). A top-bar **⧉ New window** button creates the next free `window-N` workspace and opens it in a new browser window. `?workspace=<name>` addressing and cross-window link groups (BroadcastChannel) unchanged |
| Symbol linking | Link groups stay, but the four top-bar symbol boxes are **removed**; each panel header carries its group swatch — click opens a picker (red/green/blue/yellow/pinned) |
| Type-to-load | Typing on a focused symbol-bearing panel edits the symbol **inline in the panel header** (not an overlay); Enter loads it into the panel's group, Esc restores |
| Scanner parameters | Behind a **⚙ filters** button (popover), with a one-line active-filter summary under the header; the inline input row is gone |
| Sortable tables | Scanner/movers, open orders, and positions get clickable column headers (▴/▾ indicator); sort persists per panel |
| Panel merges | **Account + Positions merge** into one Account panel (stats strip + positions table). Connection-link dots move to the top bar as an always-visible latency readout |
| Settings | One modal from the top-bar gear: Appearance (theme), Orders & hotkeys (the existing editor moves in), Sounds |

Rejected alternatives: dark-first directions A/B (Earl is light-biased); single global
symbol without groups (loses multi-symbol chart walls); persistent catalog sidebar
(permanent space cost) and keyboard-palette-only (nothing visible on blank start);
keeping per-component inline styles (unmaintainable) and adopting Tailwind (needless
dependency for a one-person app).

## Visual system — "Daylight Ledger"

### Palette (light, the default)

| Token (Palette field) | Value | Use |
|---|---|---|
| `bg` | `#FBFAF7` | panel + chart background (warm paper) |
| `surface` | `#F2F0EA` | top bar, headers, controls |
| `border` | `#DDD9CF` | hairlines |
| `borderStrong` *(new)* | `#C9C4B8` | double rules, control borders |
| `text` | `#171A1E` | ink |
| `textMuted` | `#6A7280` | secondary, axis labels, seen rows |
| `up` | `#177A58` | bullish / bid / buy — deep ink green |
| `down` | `#C2334D` | bearish / ask / sell — deep ink red |
| `accent` | `#9A6A1B` | **bronze — all state**: focus ring, armed chip, in-flight chips, scanner hits, today's news, working-order marks, degraded-latency |
| `danger` | `#A81E30` | kill switch, rejects, red latency |

Derived tokens (volume alphas, depth fills, flashes, session shading, indicator
defaults, link swatches) re-ink from these during implementation, same roles as today.
The **dark palette keeps every token role** with values re-derived to match this
identity (bronze state accent, ink-adjacent surfaces); exact dark values are an
implementation-time task using the same discipline rules. `getPalette(mode)` and the
painter contract (painters receive a `Palette`, never read globals) are unchanged.

### Type

- **IBM Plex Serif** (500/600) — panel titles, section labels, the wordmark: the ledger voice.
- **IBM Plex Sans** (400/500/600) — controls, chips, body chrome.
- **IBM Plex Mono** (400/500/600) — every number and symbol: tape, ladder, prices, axes,
  timestamps, filter summaries. Tabular figures (`font-variant-numeric: tabular-nums`).
- Fonts **self-hosted in `ui/`** (`@font-face`, woff2, preloaded). The current UI's
  serif-fallback look is literally this bug: `FONTS` declares Plex but nothing loads it.
- `FONTS` in `palette.ts` gains a `serif` entry.

### Signature & structure

- **Ledger headers**: every panel header is serif over a `3px double` rule
  (`borderStrong`). This is the one identity element; everything else stays quiet.
- Native `<select>` / `<input>` are replaced by a small set of shared styled controls
  (`.btn`, `.btn-primary`, `.ctl` chip-input, popover) built once and reused.
- Panel bodies stay dense: 11px mono data rows, 9.5px uppercase column headers.

### CSS architecture

`palette.ts` remains the single source of truth. At theme load, its values are written
to CSS custom properties on `:root` (`--bg`, `--accent`, …); a real `global.css` defines
base typography, the shared control classes, and panel-frame classes consuming those
variables. Per-component inline style objects are replaced by classes; painters keep
receiving the `Palette` object directly (canvas doesn't read CSS vars). dockview is
themed by overriding its CSS variables from the same tokens, eliminating the current
light/dark mixing.

## Shell

### Top bar (replaces WorkspaceHeader)

Left → right: **eTape** wordmark (serif) + workspace name · **latency readout** ·
spacer · **+ Add panel** · **⧉ New window** · **⚙ Settings** · **arm chip**.

- **Latency readout**: the three links (`eng` UI↔engine, `moo` engine↔moomoo, `tz`
  engine↔TradeZero) as `dot + label + ms`, threshold-colored (ok green / degraded
  bronze / bad red), fed by the existing `sys.health` topic at its ~2 s cadence.
  Click opens the Connection Status panel (adds it if not in the layout).
- **Arm chip**: bronze `ARMED` / muted `DISARMED`, click toggles (same engine command
  as the account panel's control; both are views of engine-side state).
- The four link-group symbol boxes are **gone** — group control lives on panels.

### Blank start & catalog

A workspace with no panels renders the empty state ("presets first" layout):

- Heading "Empty workspace" + one line: *"Load a preset and rearrange it, or build
  from the panel list. Everything is saved as you go."*
- **Start from a preset** (hero): Monitoring and Trading cards with layout thumbnails
  and one-line descriptions. Click applies the preset layout to this workspace.
- **Or add panels one by one** (beside): compact rows — glyph, name, one-liner — for
  every catalog panel. Click adds the panel to the layout.

Once ≥1 panel exists, the identical catalog (presets + panel list) opens as a dropdown
from **+ Add panel**. Removing the last panel returns to the empty state.

**Catalog panels (v1.1):** Chart, DOM Ladder, Time & Sales, Scanner, Movers, News,
Account (merged), Open Orders, Order Ticket, Connection Status. The smoke-painter panel
leaves the catalog (dev-only; registry keeps it behind a dev flag).

**Presets** are layout templates (dockview layout + panel configs) in a
`presets.ts` module — they replace `seeds/workspaces.ts` and are **rebuilt** with sane
proportions:

- *Monitoring*: 4 charts (2×2, one per link group) left ⅔; scanner over movers over
  news right ⅓.
- *Trading*: 2 charts (focus group, 1m + 10s) left; DOM + tape center; ticket over
  account over open orders right.

Loading a preset into a non-empty workspace replaces the layout after an inline
confirm ("Replace current layout?").

### New window / workspace model

- A workspace is a named document persisted engine-side (existing `workspace.<name>`
  config keys, dockview layout JSON — format unchanged; previously saved documents
  keep loading).
- Default window = workspace `main` (no URL param). `?workspace=<name>` addresses any
  workspace. **⧉ New window** allocates the next free `window-N` name, `window.open`s
  `?workspace=window-N`, which starts blank. If the popup is blocked, a toast shows
  the URL to open manually.
- Link groups stay global across windows (BroadcastChannel `etape.link`, unchanged).
- Workspace rename/delete/clone (manager UI) stays out of scope, as in v1.

### Settings modal

Opened from the gear. Three sections:

1. **Appearance** — theme: Light (default) / Dark. Persists to the existing `theme`
   config key.
2. **Orders & hotkeys** — the existing action-template/hotkey editor
   (`OrderSettingsModal` content) moves here unchanged. The order ticket's gear
   shortcut opens Settings directly to this section.
3. **Sounds** — the existing sound config section.

## Panel frame

Header anatomy (left → right): **group swatch** · **symbol** (mono, bold; on panels
with a symbol) · per-type controls (chart: timeframe ▾, indicators; tape: min size;
scanner: session tag, updated-at, ⚙ filters) · **✕ close**.

- **Group swatch**: colored square (link group) or outlined square (pinned). Click
  opens a picker popover: Red/Green/Blue/Yellow group rows, "Pinned — own symbol",
  hint line ("Panels in the same group load the same symbol together."). Selecting
  re-links the panel immediately; config saves with the workspace.
- **Focus**: dockview's active panel is *the* focused panel — bronze 2px ring +
  bronze-tinted header. One per window.

### Type-to-load

On a focused, symbol-bearing panel (chart, DOM, tape, news):

- A printable character (A–Z, 0–9, `.`) starts inline symbol editing **in the header**:
  the symbol slot switches to bronze underlined edit mode with a caret, seeded with
  the typed character. The panel body stays fully visible. A right-aligned hint shows
  "⏎ load · esc keep <current>".
- **Enter** normalizes (uppercase; bare tickers get the `US.` prefix — existing
  `normalizeSymbol`) and: grouped panel → focuses the group (all member panels across
  all windows follow, engine echo for persistence/pre-subscription, unchanged
  mechanism); pinned panel → sets only that panel's own symbol.
- **Esc** (or clicking elsewhere / focus loss) cancels and restores the current symbol.
- **Capture rules** (implemented as a small unit-tested state machine):
  - never starts while Ctrl/Cmd/Alt is held (order hotkeys unaffected);
  - never starts when a real input/textarea/select/contenteditable or any modal has
    focus;
  - only on panels that display a symbol; inactive panels ignore keys;
  - while editing: Backspace edits, Enter commits, Esc cancels; non-printable keys
    are ignored.
- **Unknown/bad symbol**: the engine's subscribe/backfill error surfaces as a toast
  with the engine's reason; the header reverts to the previous symbol; a grouped
  panel does **not** move the group on failure — never a half-switched group.

## Panels — presentation deltas

Data contracts, topics, and painter architecture are unchanged from the v1 design;
this section is what changes on screen. All tabular panels: clickable column headers
toggle sort (▴/▾ on the active column, dark label); sort persists in panel config.

- **Chart** — re-inked (LWC theme from the new palette); header controls styled;
  behavior unchanged (indicators, fills-on-chart diamonds, sessions, cold-symbol
  states per v1 spec).
- **Scanner / Movers** — parameters (change ≥ %, float ≤, vol ≥, price ≤; nullable
  fields render "off") live in a **⚙ filters popover** with reset-defaults + Apply;
  a one-line mono summary sits under the header ("change ≥ 10% · float ≤ 20M ·
  vol ≥ 100k"). New-hit rows: bronze left edge + tint for one refresh; already-seen
  rows muted; "no print yet" honest states kept. Default sort: % change descending.
  Movers stays this same component parameterized for RTH.
- **News** — each row: headline; meta line **date + seen-time + source** in mono.
  Rows from today: bronze left edge + tint (same "fresh" language as scanner hits)
  with the date rendered as "today"; older rows show "Jul 4"-style muted dates.
- **DOM Ladder** — **classic two-column**: bids left (best at top, descending), asks
  right (best at top, ascending), spread line above, center divider. Depth bars grow
  outward from the divider (cumulative reads from the bars; the separate Cum column
  is dropped). Working orders: bronze inner edge on their price row. Last-trade flash
  behavior kept. This is a `paintLadder` layout change with new goldens.
- **Time & Sales** — the **entire row** takes the print's direction color (buy green /
  sell red / neutral muted); the timestamp renders dimmed within that color; blocks
  ≥ 10,000 shares render bold. Min-size filter and pause-on-scroll kept.
- **Account (merged)** — replaces the separate Account bar + Positions panels: stats
  strip (Equity, Buying power, Day P&L, Realized — mono, P&L in market colors) with
  the bronze arm/disarm chip at right; positions table below (Symbol, Qty, Avg, Last,
  Unrl P&L, Flatten), sortable. Topics: `exec.account` + `exec.positions`.
- **Open Orders** — lifecycle chips: green outline WORKING, bronze PENDING/REPLACING,
  danger REJECTED with the verbatim R-code in the note column; per-row ✕, Cancel all,
  bronze stream-gap badge ("stream gap — reconciled, verify") after TZ reconnects.
- **Order Ticket** — bid × ask quote line (click a side to seed the price field),
  BUY/SELL/SHORT/COVER side row, chip-styled type/price/qty/sizing/TIF controls,
  venue selector in the header, hotkey preset buttons, and the kill switch: full-width,
  danger-red bordered, deliberately loud, always visible.
- **Connection Status** — unchanged content (per-link now + min/avg/max, engine event
  log), re-skinned; now duplicated in summary by the top-bar latency readout.

## Persistence

Unchanged mechanism, restated as a requirement: every layout move/resize/add/close and
every panel-config change (symbol, group, timeframe, filters, **sort**) debounce-saves
the workspace document engine-side (`WorkspaceStore` → `SetConfig`); reload restores
exactly. New panels must keep stable React keys (no remount on drag) and
ResizeObserver-driven canvas resize, as today. Theme and order config keep their
existing keys. `window-N` documents are created through the same config CRUD.

## Engine ↔ UI contract

**No engine work.** All changes ride existing topics (`sys.health` for the top-bar
latency readout, exec/md/scanner/news as-is) and the existing config CRUD (workspace
documents, theme, order config).

## Error handling (additions to the v1 table)

| Edge | Behavior |
|---|---|
| Type-to-load: unknown symbol / subscribe failure | Toast with engine reason; header reverts; group not moved |
| New-window popup blocked | Toast with the workspace URL to open manually |
| Preset applied over non-empty workspace | Inline confirm before replacing the layout |
| Font files fail to load | System fallbacks (`ui-monospace`, `system-ui`, serif) — layout must not depend on Plex metrics |
| Workspace doc save fails | Existing WS reconnect/outbox semantics; layout retries on next change (no data invented) |

## Testing

- **Unit (plain TS)**: type-to-load state machine (every capture rule above, table-
  driven); sort comparators + persistence round-trip; `window-N` name allocation;
  scanner filter summary formatting; preset application (layout JSON validity).
- **Golden-image**: re-baseline all painters for the new palette; **new goldens** for
  the two-column ladder (full/empty/one-sided book, working-order mark) and full-row
  tape (buy/sell/neutral, bold blocks, min-size filter).
- **Store/replay**: unchanged (no store changes).
- **Playwright E2E**: blank start → add panels from catalog → drag/resize → reload →
  layout intact; preset load + confirm-replace; type-to-load on a grouped chart moves
  DOM/tape (and reverts cleanly on a bad symbol); sort click survives reload; existing
  specs that assume seeds or header symbol boxes updated in the same pass.
- E2E/vitest split per the existing config (Playwright specs excluded from vitest).

## Out of scope (v1.1)

Workspace manager (rename/delete/clone); per-workspace theming; import/export; the
session ribbon (direction A's signature — not adopted); ladder click-trading;
chart-drag amend; drawing tools; halt feed; short-locate flow; options; multi-account;
mobile/responsive layouts (desktop trading tool).

## Open items

- Exact dark-palette values — derived at implementation start using the same tokens
  and discipline; dark stays a first-class theme (golden tests run both).
- Tape bold-block threshold fixed at 10k shares for v1.1; revisit if it needs to be
  per-symbol/configurable once real flow is watched.
- Plex woff2 files added to the repo (subset to Latin; preload the four weights
  actually used).
- Whether the top-bar latency readout needs a compact mode on narrow windows.
