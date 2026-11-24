# Watchlist session columns

## Goal and scope

Add sortable **Session Change %** and **Session Volume** columns to the US-stock
Watchlist. Give each Watchlist panel a **Columns** menu that saves the metrics
it shows. Column reordering and resizing, Crypto List changes, and additional
market-data requests are outside this release.

## What Earl sees

- Existing and new panels show **Symbol**, **Last**, **%Chg**, **Session %**,
  **Volume**, and **Session Vol** by default. The compact session headers have
  full names in their tooltips and accessible labels.
- **Columns** in the panel header offers a visibility checkbox for every
  metric and a **Reset columns** action. Symbol stays visible and first.
  Changes apply immediately, including when the list is empty. Each panel
  saves its own choices with the workspace and carries them in layout exports.
- One label above the table identifies **Pre-market**, **Regular**,
  **After-hours**, **Overnight**, or **Closed**. It appears when a session
  column is visible and the current session is known.
- Both new columns use the existing descending, ascending, and saved-order
  sort cycle. They sort by the unrounded values, with unavailable values last
  in either direction. Hiding the active sort column returns to saved row
  order and re-enables dragging. Showing it again leaves saved order selected.

## Session values

Use Scanner’s US Eastern session classification and the existing moomoo Watchlist
snapshot batch. Available session values match the daily columns during
regular trading and can diverge during extended hours.

| Active session | Session Change % | Session Volume |
| --- | --- | --- |
| Pre-market | moomoo pre-market change percentage | moomoo pre-market share count |
| Regular | Current regular price versus the prior regular close | Base snapshot share count |
| After-hours | moomoo after-hours change percentage | moomoo after-hours share count |
| Overnight | moomoo overnight change percentage | moomoo overnight share count |
| Closed | — | — |

Use the existing signed, color-coded percentage and compact share formatting.
Display dashes for missing values, including at the start of a session until
current data arrives. Preserve reported zeroes. Evaluate each metric separately
so a missing change percentage can appear alongside a valid share count.

## Implementation

1. Extend the Watchlist poller’s existing OpenD 3203 snapshot projection.
   Derive the active phase and trading-cycle day from the engine clock. Use the
   snapshot observation time to check that values belong to that phase and day.
   Compare poll start and completion across a boundary so an in-flight response
   cannot publish under the new session. Keep the existing daily Last, %Chg,
   and Volume calculations and the single batched request path.
   [Watchlist poller](../../engine/internal/watchlist/poller.go) ·
   [Scanner projection](../../engine/internal/scan/scan.go) ·
   [Session calendar](../../engine/internal/session/session.go)
2. Add nullable session metrics to each row and the engine’s session phase to
   the snapshot. Clear prior-session fields before publishing current membership
   via any path, including row moves and failed or partial refreshes. Retain
   cached values from a failed refresh only within the same session. Generate
   the TypeScript contract from Go. Normalize missing fields from older payloads
   to unavailable, and suppress cached session values while disconnected.
   [Go contract](../../engine/internal/uihub/wsmsg/payloads.go) ·
   [Watchlist store](../../ui/src/data/WatchlistStore.ts)
3. Use the existing panel header actions slot for **Columns** and the Scanner
   interaction pattern for checkboxes, keyboard dismissal, and reset. Persist
   hidden metric IDs through the Watchlist panel’s workspace settings. Accept
   missing settings and ignore unknown IDs on load. Render headers and cells
   from the same visible column list. Fit the header controls in narrow panels
   and let the table scroll horizontally when needed. Watchlist Following
   continues to read the shared saved symbol order.
   [Watchlist panel](../../ui/src/chrome/panels/WatchlistPanel.tsx) ·
   [Scanner panel](../../ui/src/chrome/panels/ScannerPanel.tsx) ·
   [Workspace settings](../../ui/src/chrome/AppShell.tsx) ·
   [Layout backup](../../ui/src/chrome/backup.ts)
4. Document the columns, their session basis, and the reset control in the
   user-facing Watchlist section of the [UI guide](../../ui/README.md).

## Verification

- Use clock-controlled poller fixtures for all four active phases, closed
  dates, and phase or trading-day changes. Cover zero, missing and invalid
  metrics, partial or failed batches, and a response arriving across a boundary.
  Verify snapshots stamped with a prior session are rejected and a normal
  batch still uses one snapshot request.
  [Watchlist poller tests](../../engine/internal/watchlist/poller_test.go)
- Test rendering, raw-value sorting, hide and reset, return to saved order,
  and row dragging. Use controlled browser frames to verify the session label,
  narrow-panel controls, independent settings in two panels, and persistence
  across reload and export/import. Check that a pre-change layout opens with
  the six defaults and retains unrelated panels and settings. Verify that
  Watchlist Following still uses saved row order.
  [Panel tests](../../ui/src/chrome/panels/WatchlistPanel.test.tsx) ·
  [Following browser tests](../../ui/e2e/watchlist-following.spec.ts)
- Run focused engine and UI tests, Watchlist race tests, `gen-ts-check`,
  applicable install, lint, type and build checks, and `git diff --check`.
  Verify full hosted CI on the pushed implementation commit.
  [Windows validation checklist](../../README.md#ci-equivalent-validation-on-windows)

## Rollout and rollback

Ship with the six default columns and use the current workspace layout version.
Saved visibility uses additive panel settings, so reverting the feature removes
its controls and columns while older builds can still read the workspace.

OpenD’s extended-session blocks have no per-block timestamp. Check the
top-level timestamp against available read-only extended-hours snapshots
when implementing freshness validation. If the snapshot cannot establish that
a value belongs to the active session, show a dash.
