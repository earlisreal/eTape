# Watchlist Sync to Following

Implemented on 2026-11-21.

Reorder the shared US-stock Watchlist and use its top symbols to fill the
pinned charts in stock Monitoring. The workspace follows one source at a
time, either the Watchlist or a selected Scanner.

## Watchlist order

- The Watchlist opens in **Manual order**. Drag a row by its handle or use
  keyboard-accessible **Move up** and **Move down** actions. Save each completed
  move immediately and append newly added symbols at the bottom.
- Column sorting changes the table view. While sorted, offer **Manual order**
  to restore the saved view and its reorder controls. Following always reads
  the saved order.
- The engine owns one ordered list across all workspaces. Watchlist panels in
  Manual order reflect membership and order changes as the engine publishes
  them. Row double-click still navigates through the panel's Link Group.

## Monitoring behavior

- **Use this Watchlist** selects the shared list and enables **Sync to
  Following** from any Watchlist panel, including an empty or ungrouped panel.
  The Monitoring window picks up the selection when open or on its next open.
- Every Watchlist panel shows the same source and ON/OFF state. **OFF** keeps
  the source selected and holds the current chart symbols. **Use this Scanner**
  switches the source and enables sync. The selection belongs to Monitoring and
  survives closing the Watchlist panels and restarting eTape.
- While ON, take the first `N` saved symbols, where `N` is the number of pinned
  Chart Panels in Monitoring. Keep one chart in place for each symbol that
  remains in the top set. Fill open slots in layout order and clear any surplus
  slots. If the list has two symbols for four charts, show **2/4**. An empty
  saved list clears all four and shows **0/4**.
- Hold the current charts and show **Waiting for Watchlist** until membership
  arrives at startup or after reconnect. Use that membership even if quotes
  are still loading. With zero pinned charts, keep sync enabled and show it
  as paused.
- Layout exports carry the enabled setting without the source or symbol
  assignments. Importing such a layout waits for explicit source selection.
  The shared list and its order live in the engine's database.

## Implementation

1. **Save and publish moves.** The engine already stores the Watchlist as an
   ordered JSON array and sends the full ordered membership on `watchlist.rows`.
   Add a `WatchlistMove` command carrying the symbol, the destination symbol,
   and whether to place it before or after that symbol. Validate both members
   and apply the move under the list's existing lock. A relative move preserves
   concurrent additions and removals. Report a missing member or invalid move
   through the command acknowledgement. Reuse the config key and the Watchlist
   publisher. Publish moves with cached rows and use current membership when a
   quote refresh completes, so an in-flight request cannot restore an older
   order. Regenerate the TypeScript command types from Go.
   [Watchlist list](../../engine/internal/watchlist/list.go) ·
   [Poller](../../engine/internal/watchlist/poller.go) ·
   [Command dispatch](../../engine/internal/uihub/commands.go) ·
   [Engine adapter](../../engine/cmd/etape/main.go) ·
   [Wire types](../../engine/internal/uihub/wsmsg/payloads.go)
2. **Add panel controls.** Use symbol identities for drag targets and arrow
   moves. Send the command on drop or keyboard activation. Render the engine's
   published order and use the existing toast for rejection. Reuse the
   Following control pattern with Watchlist-specific labels and counts, and
   expose it in both the empty and populated panel.
   [Watchlist panel](../../ui/src/chrome/panels/WatchlistPanel.tsx) ·
   [Following control](../../ui/src/chrome/panels/ScannerControls.tsx)
3. **Store the source on Monitoring.** Extend the saved `scannerSync` setting
   to identify Watchlist as a source without a panel reference. Read existing
   workspace documents with a Scanner panel reference as Scanner sources and
   documents with only `enabled` as waiting for a source. A source switch
   replaces the previous source fields. Merge source edits into the latest
   Monitoring document, using the existing Monitoring preset on first use.
   Coordinate saves so a queued chart or layout write cannot overwrite a
   source switch. Use workspace change notifications to update other windows.
   Preserve the enabled-only layout export and the older import paths.
   [Workspace store](../../ui/src/chrome/workspace.ts) ·
   [Monitoring preset](../../ui/src/chrome/presets.ts) ·
   [Layout import and export](../../ui/src/chrome/backup.ts)
4. **Drive the charts from membership.** All workspaces already subscribe to
   `watchlist.rows`. Track receipt of a full payload for the current connection
   separately from quote freshness. In stock Monitoring, use ordered membership
   with the existing pinned-chart planner and make clearing unfilled slots
   specific to Watchlist mode. Recheck the active source, connection and
   eligible slots before applying queued assignments. Represent a cleared
   chart explicitly in the panel symbol runtime so its old mounted symbol
   cannot return through a fallback. Clear the saved symbol, release its
   chart demand and preserve its settings.
   [App subscriptions](../../ui/src/App.tsx) ·
   [Watchlist store](../../ui/src/data/WatchlistStore.ts) ·
   [Monitoring coordination](../../ui/src/chrome/AppShell.tsx) ·
   [Chart assignment planner](../../ui/src/chrome/scannerSync.ts) ·
   [Panel symbol resolution](../../ui/src/chrome/PanelFrame.tsx)
5. **Update the guides.** Record Manual order, source switching, and layout
   export behavior in the UI guide. Update the Monitoring behavior and
   vocabulary in the subsystem guide and domain notes.
   [UI guide](../../ui/README.md) ·
   [Workspace guide](../../ui/src/chrome/README.md) ·
   [Domain notes](../../CONTEXT.md)

## Verification

- Engine tests cover persistence across reload, relative moves, invalid and
  repeated moves, concurrent additions and removals, and a move accepted
  during a quote request. Verify the quote response preserves the new order.
  Run focused engine tests with the race detector and `gen-ts-check`.
- UI tests cover manual and sorted views, accepted and rejected moves, loaded
  empty versus unavailable membership, stable slots, duplicate chart symbols,
  and surplus clearing. Check that switching sources during a queued layout
  save retains both changes. Check old Scanner layouts and layout-only
  exports for retained panel settings and paused imports.
- In a browser with controlled data, open Watchlist and Monitoring in separate
  windows. Drag a symbol across the cutoff, use a keyboard move, and verify a
  swap inside the top set holds the chart positions. Remove symbols through the
  empty state and check that cleared charts release their demand. Close the
  Watchlist panels and reopen Monitoring to verify it resumes with the saved
  source. Check that OFF holds chart symbols and **Use this Scanner** switches
  the source. Verify Crypto Monitoring follows its own source. Reconnect with
  membership delayed and confirm the existing charts hold until the list arrives.
- Run applicable install, lint, type, and build checks, `git diff --check`, and
  full hosted CI on the pushed implementation commit.
  [Windows validation checklist](../../README.md#ci-equivalent-validation-on-windows)

## Rollout and rollback

Existing Watchlists start in their current saved order. The existing ordered
array lets an older engine read the new order after a rollback. Before reverting
to a build that understands only Scanner sources, select a Scanner if Following
should continue. Otherwise the older build has an enabled setting without a
Scanner source and pauses.
