# Window-scoped stock Following

## Goal and scope

Run two or more named workspace windows side by side, each filling its own
pinned charts. One can follow a Scanner for gainers while another follows a
Scanner with different filters and sort, or the shared Watchlist. Stock
Following is available in the main window, the original Monitoring window,
and custom workspaces. Crypto Monitoring uses its existing Crypto List
Following flow.

## Current behavior

- Scanner and Watchlist panels in every window update one saved stock
  Following setting in `workspace.monitoring`. Only that workspace applies
  the stock chart assignments. The separate Crypto Monitoring window uses a
  different coordinator. [AppShell](../../ui/src/chrome/AppShell.tsx) ·
  [Crypto Following](../../ui/src/chrome/useCryptoFollowing.ts)
- **New window** opens the saved Monitoring workspace or creates a blank
  named workspace with its own ID. A Scanner already saves its filters and
  sort per panel. The engine identifies Scanner boards by workspace and panel
  and shares discovery across them. [New window dialog](../../ui/src/chrome/NewWindowModal.tsx) ·
  [Scanner engine](../../engine/internal/scan/multipanel.go)

## Behavior

### Create and control a window

- In **New window**, choose **Blank** or **Monitoring layout** when creating
  a named workspace. The Monitoring layout starts with four unassigned
  pinned charts, a Scanner, and Stock Info. Following starts off. Each
  creation gets its own saved layout, window identity, and Following state.
  The original **Monitoring** entry still opens its saved workspace.
- Give each stock workspace one **Following** switch that displays the
  selected source and status. **Use this Scanner** and **Use this Watchlist**
  in a panel header select that source for its own workspace and turn
  Following on. Select one source at a time. Each Scanner uses its panel's
  filters and sort. The Watchlist uses the shared list's saved order,
  regardless of how its table is sorted.
- Save the active source and ON/OFF state with the workspace. Selecting a
  source or changing the switch in one window only affects that workspace.
  Reopening a workspace restores its selection, switch, and saved charts.
  Turning Following off holds the current symbols and keeps the selection.

### Assign chart symbols

- Fill the workspace's pinned Chart Panels in layout order. A pinned chart
  has no Link Group. Take the top symbols up to the number of pinned charts,
  keep qualifying symbols in their existing slots, and fill available slots
  from the remaining ranked symbols. Linked charts take their symbols from
  their Link Group.
- If a Scanner produces fewer results than there are slots, hold unmatched
  charts at their current symbols. For the Watchlist, wait for membership on
  the current connection, then clear surplus charts. A confirmed empty list
  clears all pinned chart symbols. Show progress such as **Following 2/4**.
- If the selected Scanner is removed, hold chart symbols and prompt for a
  source. Removing a Watchlist panel leaves Following on the shared list.
  The window switch can still turn it off. With no pinned charts, show
  Following as paused and resume when a chart becomes eligible.

## Implementation

1. Extend the named-workspace creation in
   [NewWindowModal.tsx](../../ui/src/chrome/NewWindowModal.tsx). Seed the new
   ID from [presets.ts](../../ui/src/chrome/presets.ts) when Monitoring layout
   is selected. Use that ID for the initial popup target in
   [windows.ts](../../ui/src/chrome/windows.ts) and on reopen. Reuse the
   catalog, saved bounds, and startup restoration for named workspaces.
2. Read and write the current workspace's `scannerSync` in
   [workspace.ts](../../ui/src/chrome/workspace.ts) and
   [AppShell.tsx](../../ui/src/chrome/AppShell.tsx). Replace the stock
   Monitoring-wide subscription with the current document. Serialize source,
   toggle, layout, and chart writes for each workspace so a queued save or
   layout import cannot resurrect an old setting. Make acknowledged workspace
   writes visible to the next read, including a legacy Scanner lookup. Verify
   this against the engine's buffered config store.
   [Config commands](../../engine/internal/uihub/commands.go) ·
   [Config storage](../../engine/internal/store/config.go)
3. Wire the switch in [TopBar.tsx](../../ui/src/chrome/TopBar.tsx) and the
   local selection shortcuts through
   [ScannerControls.tsx](../../ui/src/chrome/panels/ScannerControls.tsx),
   [ScannerPanel.tsx](../../ui/src/chrome/panels/ScannerPanel.tsx), and
   [WatchlistPanel.tsx](../../ui/src/chrome/panels/WatchlistPanel.tsx).
   Display missing source, no pinned charts, and waiting for data in the
   workspace control. Keep the saved cross-window Scanner selection in the
   original Monitoring workspace until the user replaces it. That window's
   switch controls it even while the source window is closed.
4. Reuse [scannerSync.ts](../../ui/src/chrome/scannerSync.ts), the existing
   Scanner registration command, and the chart symbol runtime. Resolve local
   Scanner data by workspace and panel ID. Keep external Scanner lookups and
   demand for the saved legacy selection only. Coalesce chart assignments and
   recheck the current source, chart eligibility, and Watchlist connection
   before a queued batch applies. Patch only affected chart symbols and
   release chart demand when a slot clears. Use the existing WebSocket
   message shapes.
5. In [backup.ts](../../ui/src/chrome/backup.ts), export the stock Following
   enabled setting with the structural layout. Omit source identities and
   chart assignments. On stock layout import, preserve enabled intent and
   wait for a new source selection in the destination. Keep legacy Crypto
   layout import behavior. Update the [UI guide](../../ui/README.md),
   [workspace guide](../../ui/src/chrome/README.md), and
   [glossary](../../CONTEXT.md#workspace-layout).

## Validation

- Test a blank custom window and two windows created from Monitoring layout.
  Use different Scanner filters and sorts, with matching panel IDs across
  preset instances. Switch one window to the Watchlist, remove the panel, and
  verify it keeps following until turned off from the window switch. Check
  that the other window keeps its selected source and charts at each step.
- Cover linked versus pinned charts, duplicate symbols, stable slots, Scanner
  shortages, empty and delayed Watchlist membership after reconnect, removing
  a selected Scanner, and adding a pinned chart while paused. Check that
  queued updates respect a source switch, OFF, and Link Group changes.
- Check that a queued layout save preserves source changes and that an import
  does not revive a previous source. Verify saved Monitoring and custom
  layouts keep their panels and settings. Exercise the legacy cross-window
  Scanner with its host closed, and verify Crypto Monitoring still follows
  its selected Crypto List.
- Exercise the workflow in separate browser windows against controlled demo
  data. Verify reopening saved windows restores independent settings, charts,
  and bounds. Check the Following control and panel headers at narrow sizes.
  Extend the existing [Scanner browser coverage](../../ui/e2e/scanner-multipanel.spec.ts)
  where useful.
- Run focused UI tests, applicable install, lint, type, build, and browser
  checks, and `git diff --check`. If config persistence changes in the engine,
  run the applicable engine tests and race coverage. Verify full hosted CI on
  the pushed implementation commit.
  [Windows validation checklist](../../README.md#ci-equivalent-validation-on-windows)

## Rollout and rollback

Keep layout version 8 and the saved `scannerSync` field. The original stock
Monitoring workspace starts with its saved layout, assigned symbols, source,
and enabled state. Existing custom workspaces can opt in using a local Scanner
or Watchlist. The main risks are one workspace receiving another's chart
assignments, a stale save overwriting a change, and legacy Scanner data going
stale when its host window closes. The isolation, persistence, and legacy
checks above cover those cases.

An older build can reopen named workspaces and display their saved charts.
On that build, stock Following operates through the original Monitoring
workspace, so use its saved source if automated assignments are needed after
rolling back.
