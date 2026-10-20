# Alert and Watchlist symbol navigation

Status: approved design on 2026-10-21; implementation pending.

## Goal and settled decisions

- Remove `View chart` from the Alerts Panel's Alerts and History tabs and from
  the notification popup. Make the displayed symbol a single-click control.
- Panel symbol clicks load the Alerts Panel's currently selected Link Group.
  A selected color is required; pinned panels cannot navigate and show
  `Choose a Link Group color to load symbols`.
- Watchlist must also require a selected color. Remove its green fallback,
  retaining its existing double-click row navigation and single-click selection.
- Update existing followers of the chosen color across open Workspaces. Do not
  create a Chart Panel, change an Execution Venue, or bring main forward. If no
  chart follows that color, updating the group is sufficient.
- Popup symbol clicks prefer a colored Alerts Panel open anywhere across live
  Workspaces. Use the saved source color only when no open Alerts Panel has a
  color. Resolve this at click time, not when the notification was created.
- Persist the source color on alert definitions and snapshot it on each trigger
  so it remains available after restarts, later edits, or definition deletion.
- When several grouped Alerts Panels are open, use the most recently used one.
  Ungrouped panels are excluded. Background/minimized windows and inactive tabs
  still count as open.
- Creating an alert without a source color remains allowed. If a popup has
  neither an eligible Alerts Panel nor a saved source color, disable its symbol
  navigation and show a choose-color instruction. Keep View alerts available.
- Editing and Rearm preserve the original saved source, including an absent
  source on an old alert. No separate source-color picker is added.

## Non-goals

No changes to trigger evaluation, alert sound/claims, monitoring, history
retention, order submission, other list panels, or workspace activation.
No new dependencies or additional chart-creation path.

## Current-code evidence

- [AlertsPanel.tsx](../../ui/src/chrome/panels/AlertsPanel.tsx) has `View chart`
  in both tabs. [PanelFrame.tsx](../../ui/src/chrome/PanelFrame.tsx) already
  supplies its selected Link Group and picker; no additional panel picker is needed.
- [WatchlistPanel.tsx](../../ui/src/chrome/panels/WatchlistPanel.tsx) uses
  `linkGroups.focus(group ?? "green", symbol)` on double-click.
  [WatchlistPanel.test.tsx](../../ui/src/chrome/panels/WatchlistPanel.test.tsx)
  explicitly asserts that fallback and must change.
- [registry.tsx](../../ui/src/chrome/panels/registry.tsx) supplies a live
  `group` separately from frozen `config.group`. Resolve it with
  `group === undefined ? config.group : group` so detaching to `null` cannot
  revive the original color. Alerts remains `symbolBearing: false`; its color
  is a navigation destination, not a filter on the global alert list.
- [LinkGroups.focus](../../ui/src/chrome/linkGroups.ts) updates local followers,
  broadcasts across Workspaces, and echoes symbol focus to the engine. It
  creates no chart and changes no venue. Route the full stored symbol including
  `US.`; strip the prefix only for display.
- [PriceAlertNotifications.tsx](../../ui/src/chrome/alerts/PriceAlertNotifications.tsx)
  is global to a Workspace, with one engine-claimed popup owner per burst.
  A workspace can contain zero or several Alerts Panels. The popup currently
  shows the first trigger's symbol and a count for a burst.
- There is no existing registry of all open Alerts Panels.
  [WorkspaceStore](../../ui/src/chrome/workspace.ts) broadcasts saved documents,
  which can belong to closed Workspaces and lag live color changes.
  [windows.ts](../../ui/src/chrome/windows.ts) has main-only presence, while
  [HotkeyTargetCoordinator](../../ui/src/chrome/hotkeyTarget.ts) tracks only one
  active panel of any type. Its channel request/replay/update/clear and revision
  patterns can guide narrow Alerts Panel coordination without changing hotkeys.
- [navigation.ts](../../ui/src/chrome/alerts/navigation.ts) dispatches
  `etape:alert-chart`; [AppShell.tsx](../../ui/src/chrome/AppShell.tsx) handles
  it by activating a matching chart or creating a pinned chart. Remove this
  path once panel and popup callers use Link Groups.
- [PriceAlertEditor.tsx](../../ui/src/chrome/alerts/PriceAlertEditor.tsx),
  [ChartPriceAlerts.tsx](../../ui/src/chrome/alerts/ChartPriceAlerts.tsx), and
  [ChartPanel.tsx](../../ui/src/chrome/panels/ChartPanel.tsx) do not capture
  creation color. Neither the [engine model](../../engine/internal/pricealert/manager.go)
  nor [WebSocket types](../../engine/internal/uihub/wsmsg/pricealerts.go) has it.
  Remembering setup color requires metadata end to end. Existing
  [storage](../../engine/internal/store/pricealerts.go) persists complete JSON,
  so an optional added field needs no SQL table migration.
- The [Price Alerts specification](../../.scratch/price-alerts/spec.md) and
  [UI guide](../../ui/README.md) require the old chart navigation. The approved
  plan must supersede that rule. Interface changes do not alter
  [ADR 0008](../adr/0008-engine-owned-price-alert-monitoring.md)'s monitoring
  ownership or [ADR 0005](../adr/0005-link-group-owned-execution-venue.md)'s
  venue ownership.

## Routing and lifecycle rules

| User action / state | Destination |
| --- | --- |
| Symbol in Alerts or History | That Alerts Panel's current color |
| Symbol in an ungrouped Alerts Panel | No navigation; choose-color instruction |
| Watchlist row double-click | That Watchlist Panel's current color; no navigation if ungrouped |
| Popup symbol, one or more grouped Alerts Panels open | Most recently used grouped Alerts Panel's current color |
| Popup symbol, no grouped Alerts Panel open | The displayed trigger's saved source color |
| Popup symbol, neither destination available | No navigation; choose-color instruction and View alerts |

Record Alerts Panel use on explicit user activation/interaction and a deliberate
color change. Retain its last-use priority when the trader switches to a chart
or another panel; the global hotkey target must not decide popup routing.
Programmatic restoration/replay is not new user activity. For restored panels
without an interaction, or equal revisions, use a stable workspace/panel/owner
identity ordering. Recent-use priority is session-local and not persisted.

Capture `sourceGroup` from the initiating panel's live group when opening the
create editor: chart context menu, chart bell, or Alerts Panel + Alert. Store
only red/green/blue/yellow, or absence for an ungrouped source. Validate this
field at the engine boundary. The engine preserves the saved value on updates
and Rearm, and copies it into each trigger. Trigger colors remain independent
of later definition changes or deletion.

Existing alert/history JSON without the field stays valid and has no saved
source; never infer one from the symbol, a matching chart, or green. These
alerts continue monitoring. Their popups can use a grouped open Alerts Panel,
otherwise show the missing-destination state. Alert creation remains available
when the source is ungrouped, with the same navigation rule.

Keep the current burst display: the clickable symbol represents the first
displayed trigger only; its source is the fallback. The count and View alerts
cover the remaining triggers. A notification itself never switches symbols.
Resolve the live destination on activation and describe it in the symbol's
tooltip/accessibility label. Neither a source fallback nor panel navigation
changes venues, creates charts, or activates another workspace.

## File-level implementation

1. In [AlertsPanel.tsx](../../ui/src/chrome/panels/AlertsPanel.tsx), consume live
   group/linkGroups props. Replace tickers with the house quiet
   [Button](../../ui/src/chrome/controls/Button.tsx), remove `View chart`, and
   require a color. Keep row selection and management actions. Give symbols a
   destination label, keyboard activation, and visible focus.
2. In WatchlistPanel, preserve live `null`, remove green fallback, and show the
   same no-color instruction. Keep selection, menus, and list management usable.
3. Add optional validated source-color metadata to the engine
   alert/draft/trigger model, Go wire owners, and
   [hub mapping/upsert](../../engine/internal/uihub/price_alerts.go). Regenerate
   [TypeScript](../../ui/src/gen/wsmsg.ts) from Go; never edit it manually.
   Thread source colors from chart/editor/panel callers and preserve them through
   edit/Rearm. Trigger snapshots retain their own colors. Existing JSON storage
   handles the added field without a schema migration.
4. Add narrow cross-window Alerts Panel coordination under the existing alerts
   module, reusing the channel/revision conventions already present. Publish
   actual live panel identities, current colors, and the agreed selection order;
   register changes on workspace readiness, add/remove, color re-pick, and
   interaction. Include inactive tabs in an open Workspace. Handle late joiners,
   orderly close, and abandoned owners without persisting live presence into
   workspace layouts. Use a per-window-instance Web Lock for owner lifetime,
   following the existing workspace-lock pattern but covering main too. Check
   owner liveness when resolving stale entries; background/minimized windows
   must not lose priority merely because a heartbeat timer was throttled.
   Saved layouts in closed windows are never candidates. This uses the same
   browser-profile scope as existing cross-window LinkGroups.
   Keep this separate from the hotkey target and notification sound/claim logic.
   Gate popup routing until discovery/liveness is resolved: an unavailable
   coordination API or a still-unresolved owner is not evidence that no panel
   is open. Show navigation temporarily unavailable instead of guessing a source
   fallback. Publish live color changes immediately without waiting for saved
   workspace documents.
5. Route popup symbol clicks through the live registry, then the trigger's source
   color, using existing LinkGroups supplied by AppShell. Keep burst count,
   View alerts, dismissal, timeout, and notification claims. Remove the obsolete
   chart-event helper/handler after checking every caller; repurpose the existing
   navigation module only if it keeps this behavior together without extra layers.
6. Update the UI guide and Price Alerts specification for panel, popup, and
   Watchlist behavior. Update the
   [engine alert guide](../../engine/internal/pricealert/README.md) and affected
   hub documentation for source metadata. Index this plan in [Plans](README.md)
   after approval; mark implemented after execution.

## Acceptance and validation

- Extend [AlertsPanel tests](../../ui/src/chrome/alerts/AlertsPanel.test.tsx),
  [popup tests](../../ui/src/chrome/alerts/PriceAlertNotifications.test.tsx), and
  Watchlist tests for exact symbols/destinations, live color changes, explicit
  detachment, keyboard activation, and no fallback or chart event.
- Verify row selection and management actions remain independent. No chart
  creation, venue change, order submission, or main-window activation occurs.
  Reuse group-broadcast coverage; check two open Workspaces with existing
  followers and the case where no chart follows the selected color.
- Cover source colors through create/edit/Rearm, invalid values, old
  JSON, storage reload, wire mapping, trigger colors after definition changes
  or deletion, and agreed burst behavior. Extend existing manager/hub and
  editor/chart tests for these paths.
- Cover panel-color priority over source color across windows, multiple-panel
  selection, color changes and detachment, inactive tabs, late joining windows,
  panel/window close, abandoned-owner removal, and missing destinations. Use
  injectable channel/liveness inputs for deterministic tests; keep new
  tests under the already-included `chrome/alerts` regression-test directory.
  Include stable startup/tie ordering, stale replay rejection, retained priority
  after switching away from Alerts, and unresolved/unavailable coordination.
- Add a focused `ui/e2e/alert-navigation.spec.ts` using the existing demo/mock
  fixtures. Exercise real BroadcastChannel/Web Lock behavior across two windows:
  panel priority over source, a color re-pick, and fallback after its owner closes.
  Use synthetic alert data; no live account or order operation is needed.
- Inspect light/dark themes and narrow panels for ticker focus, hover, and the
  no-color instruction.

For this prose-only approved plan, validate Markdown links and run
`git diff --check`. Engine/UI checks are skipped and hosted CI need not finish
before handoff, per [AGENTS.md](../../AGENTS.md#validation).

For implementation, run from `ui/`:

```bash
npx vitest run --project chrome-regressions src/chrome/alerts src/chrome/panels/WatchlistPanel.test.tsx src/chrome/AppShell.test.tsx
npx vitest run --project chart-panel src/chrome/panels/ChartPanel.test.tsx
npm run lint
npm run build
npx playwright test e2e/alert-navigation.spec.ts
```

Run from `engine/`:

```bash
go test ./internal/pricealert ./internal/store ./internal/uihub
go test -race ./internal/pricealert
go build ./cmd/etape
mingw32-make gen-ts-check
```

The contract check regenerates from Go and compares against HEAD; commit the
generated update before its final clean check. Complete the visual/cross-window
checks above, validate Markdown links, and run `git diff --check`. Broaden tests
if implementation failures reveal wider impact.

Commit the executed plan with implementation/docs, integrate upstream, merge
and push main, verify local/remote commit containment, and await successful
full hosted [CI](../../.github/workflows/ci.yml) per
[AGENTS.md](../../AGENTS.md#validation). Report checks and required skips.

## Rollout, rollback, and risks

Ship through the normal update without a feature flag or SQL migration. Old
data keeps loading without guessing a color. Revert the scoped implementation
to restore prior navigation. Optional JSON metadata is readable by older
versions that ignore unknown fields, though their subsequent edits may drop it.
Live panel presence and interaction priority are not durable.

Navigation changes every following panel in the selected color across open
Workspaces, including trading panels. Risks are stale colors after detachment,
stale open-panel presence after a window disappears, inconsistent selection
across windows, and losing setup color after alert edits/deletion.
