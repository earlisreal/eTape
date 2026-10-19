# Price alerts with sound

Status: ready-for-agent
Approved: 2026-10-19
Implementation: engine and UI implemented; validation and review in progress.

## Goal

Set a specific price from a Chart Panel, receive an audible notification when
the condition is met, and manage saved alerts and trigger history without
keeping their charts open.

The canonical terms are in [CONTEXT.md](../../CONTEXT.md). Monitoring lifetime
and the audio boundary are recorded in the [monitoring ADR](../../docs/adr/0008-engine-owned-price-alert-monitoring.md).

## Agreed behavior

### Monitoring and lifetime

- Price Alerts belong to a symbol, independently of charts, Workspaces, Link
  Groups, Execution Venues, trading arm state, and broker permissions. They are
  notifications and cannot place, modify, or cancel orders.
- Save alerts across eTape restarts. An untriggered alert has no automatic daily
  reset or expiry; it remains until triggered or explicitly deleted.
- Evaluate at-or-above or at-or-below conditions on Last-Eligible Price. Reaching
  or passing the level qualifies; bid/ask changes alone do not.
- Use only newly accepted, fresh eligible trades. Cached prices, ticker seeds,
  historical bars, and history replay cannot themselves produce a trigger.
- After a restart, reconnect, or other monitoring resumption, its first fresh
  eligible trade can trigger an already-met condition. Record the reason as
  `Target met on resume`, without claiming when it first became true offline.
- Each alert is one-shot: persist its Price Alert Trigger, stop monitoring that
  alert, and require explicit Rearm before another occurrence.
- While the engine runs with all browser windows closed, continue monitoring
  and record triggers silently. Opening a Workspace later shows their history
  without replaying old sounds or popups.
- Prices are absolute dollar levels. The initial feature does not adjust
  targets for stock splits; manual review after splits is explicit.

### Sessions and availability

- Each alert has Pre-market, Regular, and After-hours checkboxes, all enabled by
  default. Require at least one selected session; overnight is outside scope.
- Outside its selected sessions, retain the alert and show that it is waiting
  for a selected session. Evaluate only fresh eligible trades after resumption.
- Distinguish `Monitoring`, `Waiting for data`, `Waiting for data capacity`,
  `Outside selected session`, `Paused by you`, and `Triggered`.
- A saved/enabled alert is not necessarily Monitoring. Startup and data gaps
  wait for fresh eligible input; a quiet symbol alone does not establish a gap.
- Recover from feed interruptions automatically on fresh eligible data.
  Explicit Pause is different: a Paused alert remains paused until the trader
  resumes it, including across restarts.
- When data capacity is unavailable, save the alert in a visible waiting state.
  Preserve existing execution/chart subscriptions. Share an already-subscribed
  symbol where possible; obtain capacity before claiming monitoring readiness.
- Start waiting alerts automatically when capacity and fresh data become
  available. The normal resume rule applies if the target is already met.

### Chart creation and editing

- Use a dedicated bell tool and the chart context-menu action
  `Create price alert at $…`. No new modifier-click binding in the initial flow.
- With the bell tool active, pointer movement previews the price. A chart click
  opens an editor with the selected symbol and exact editable price.
- Suggest at-or-above for a chosen price at/above the trusted current price and
  at-or-below for a lower price. Show the resulting condition explicitly; the
  trader may change it.
- Editor fields: symbol, condition, price, optional label, and selected sessions.
  Use existing instrument-price precision, including subdollar precision.
- If a trusted current price already meets the proposed condition, warn clearly
  but allow Save. The next fresh eligible trade in a selected session can
  trigger it. Without trusted current data, show the appropriate waiting state
  instead of claiming the condition is currently met or unmet.
- Show a dashed amber Price Alert Line and bell-marked Alert Price Tag on
  matching-symbol charts. Clicking the tag opens the editor.
- Dragging the tag previews a proposed price only; releasing opens the editor
  and Save commits it. Escape/Cancel discards the preview.
- Preserve the chart's chosen context and scaling. Alerts outside the visible
  price range do not force the chart to rescale. Existing order, crosshair, and
  timer label priorities remain authoritative; overlapping alert tags need a
  chooser, with panel editing always available.
- Show Paused alert levels with a muted appearance and a pause indication;
  remove triggered levels from normal chart overlays while retaining history.
- Never reuse an Order Price Pill's order-cancellation semantics for an alert.

### Alerts Panel and history

- Use a dockable Alerts Panel. Closing or moving the panel does not change
  monitoring. Its data is shared across Workspaces.
- Retain a distinct Price Alert Trigger for every occurrence. Rearming or
  deleting the saved alert does not delete past trigger entries.
- History records retain the symbol, condition, target, triggering Last-Eligible
  Price, event time, label, session, and reason as they were at the trigger.
- History remains until the trader explicitly deletes its entries; no automatic
  retention purge is part of the initial feature.

### Notification and sound

- Price Alert Sound is Beacon: the audition's three rounded same-pitch pulses.
- A trigger produces a brief nonblocking popup plus its persistent history
  entry. The popup does not switch symbols, change Link Group focus, steal
  keyboard focus, or move between Workspaces.
- Emit sound once across open Workspaces for the same trigger or burst, at the
  engine-instance scope. A second panel/window must not duplicate the cue.
- Coalesce simultaneous triggers into one Beacon and a popup showing the count;
  retain every independent trigger entry. Additional triggers during the cue
  update the burst display without overlapping audio or a queued sound backlog.
- Existing global sound enable and master volume apply. Muted, unavailable, or
  gesture-locked audio still permits monitoring, the popup, and history; missed
  sounds do not replay after unmute, audio unlock, or Workspace reconnection.
- Settings control: Price alerts sound enabled/disabled plus Beacon
  Preview under General / Sounds. Per-alert sound selection or mute is outside
  the initial feature; use Pause to stop that alert's monitoring.
- Accessible text conveys symbol, condition, target, actual triggering price,
  time, and reason independently of color or sound.

## Panel controls and navigation

Saved alert definitions and trigger occurrences have separate lifecycles:

- Label the two panel tabs `Alerts` and `History`. Alerts contains saved
  definitions, including triggered ones; History contains independent trigger
  occurrences. This replaces the exploratory `Active / Triggered` tab labels.
- Open the panel through the existing Add Panel menu. A new Top Bar bell or
  drawer is not needed in the initial scope.
- Alerts rows show symbol, condition/target, label, sessions, and actual state.
  Use Edit, Pause/Resume, Delete, and Rearm where applicable. Rearm opens the
  editor, so an already-met warning is visible before confirming.
- Pausing/resuming/editing a saved alert never erases its history. Editing a
  triggered definition does not silently rearm it; Rearm is explicit.
- History rows show each trigger snapshot, with View chart, Delete history entry,
  and View alert when its saved definition still exists. Deleted definitions
  leave their history readable and identifiable as `Alert deleted`.
- Popup `View alerts` opens/focuses an Alerts Panel in the current Workspace.
  Clicking a row selects it. Only an explicit View chart action navigates:
  focus a matching-symbol chart in the current Workspace, otherwise open a
  pinned Chart Panel without changing the trader's Link Group symbols.
- Keep the first version focused on per-row management: no bulk actions,
  additional trigger sources, overnight session, modifier-click bindings,
  automatic corporate-action adjustments, or custom sound library.

## Acceptance scenarios

1. Create NVDA at-or-above a target via the bell tool or chart menu. Review the
   exact price and sessions, then Save; the chart level and Alerts row agree.
2. Drag an alert level and Cancel: its saved price stays unchanged. Save a drag
   preview: all Workspaces show the new target.
3. Close its chart, switch symbols, close its panel, or change Workspace: an
   enabled alert continues monitoring while the engine runs.
4. A fresh Last-Eligible Price reaches or jumps through the target: one durable
   trigger appears, the alert stops, and one eligible Workspace plays Beacon.
5. Several alerts trigger together: one cue and counted popup, with one history
   entry for every trigger.
6. An already-met target is created/rearmed after its warning: cached data does
   not trigger it; the next qualifying fresh eligible trade can.
7. Restart beyond an untriggered target: the first fresh qualifying trade records
   `Target met on resume`; historical data does not replay an event.
8. With every browser window closed, a running engine records a trigger; a later
   Workspace sees its history without old sound/popup replay.
9. Disconnect/reconnect the feed: enabled alerts wait and recover automatically
   on fresh data; user-paused alerts remain paused.
10. Exhaust data capacity: the alert stays saved and visibly waiting without
    displacing execution/chart monitoring; it starts when data becomes available.
11. Rearm and trigger again: both trigger occurrences remain. Delete the saved
    alert: both history snapshots remain readable until explicitly deleted.
12. Mute sounds or block audio: visual/history delivery still works. Unmute or
    unlock later: no missed sound backlog plays.
13. An excluded session produces no trigger; the first eligible trade in a
    selected session can trigger under the resume rules.
14. A split does not silently alter an absolute target. The feature communicates
    the need to review saved levels after splits.
15. View chart never changes another Link Group's symbol or sends an order.

## Audition and visual review

Conversation-local concepts and sound audition are outside application source
in the task's visualization directory. They use illustrative prices and local
synthesized audio; they do not monitor market data or implement runtime alerts.

Audition candidates were Beacon, Woodblock, and Signal. Earl selected Beacon.
The initial concept's alternate Top Bar drawer is exploration history; the
chosen production management surface is a dockable Alerts Panel.

## Codebase facts for subsequent planning

- [Chart context menu](../../ui/src/chrome/panels/ChartPanel.tsx) already resolves
  a clicked coordinate to price.
- [Chart Order Gesture](../../ui/src/chrome/panels/tv/ChartConditionalOrderEntry.tsx)
  uses exact modifier bindings for conditional orders.
- [Chart overlay guide](../../ui/src/chrome/panels/tv/README.md) documents existing
  order price pill priorities and collisions.
- [Sound guide](../../ui/src/sound/README.md) documents shared sound controls and
  panel-owned Scanner Sound; Price Alerts require their own monitoring lifetime.
- [Session clock](../../ui/src/chrome/SessionClock.tsx) has a cross-Workspace
  announcement precedent, but browser-profile coordination alone cannot satisfy
  the stronger engine-instance audio scope.
- [Toast](../../ui/src/chrome/Toast.tsx) defaults to four seconds and has no
  history or action buttons; alert popup/history behavior needs explicit support.
- [Engine-held demand](../../engine/cmd/etape/main.go) acquires ticker
  subscriptions independent of charts/windows. The [subscription manager](../../engine/internal/feed/opend/subman.go)
  shares symbol/subtype subscriptions, but priority must distinguish alert demand
  from existing execution/chart demand.
- [Market-data core](../../engine/internal/md/core.go) has a separate live
  Last-Eligible print lane excluding cached seeds/history, with explicit data
  gaps for disconnects, resync, and lost tick input.
- [Held-order lifecycle](../../engine/internal/exec/held_lifecycle.go) requires
  fresh input after gaps. Receive age is different from time since an illiquid
  symbol last traded; execution-only safety gates do not apply to alerts.
- Current UI health exposes aggregate link/quota state, not authoritative
  per-alert monitoring readiness. Saved alert state alone is insufficient.
- Pre-market, regular, and after-hours subscriptions are established; overnight
  ticker coverage and authoritative split adjustments are not established.

## Comments

### 2026-10-19 — rounds 1 and 2, Q1-Q9

Earl selected global saved one-shot alerts, manual Rearm, all three daytime
sessions by default, Last-Eligible Price threshold conditions, first-fresh-trade
evaluation after resume, one cue across Workspaces with silent recording when
no window is open, the dockable Alerts Panel, Beacon, and warning-but-allow
behavior for already-met targets.

### 2026-10-19 — round 3, Q10-Q14

Earl selected bell-tool/context-menu creation, persistence until triggered or
deleted, Beacon plus a brief popup and retained history, existing global sound
controls without missed-sound replay, automatic fresh-data recovery, and saving
alerts in a visible waiting state when data capacity is unavailable.

### 2026-10-19 — round 4, Q15-Q19

Earl selected drag-preview-then-Save editing, per-alert session checkboxes,
retaining every trigger across Rearm/deletion of the saved alert, one cue per
burst while preserving every trigger, and absolute levels with manual split
review.

### 2026-10-19 — final shared-understanding review, Q20

Earl approved the complete design, including Alerts / History tabs, Add Panel
entry, editor-based Rearm, explicit navigation without changing Link Group
symbols, and General / Sounds Beacon enable/Preview without per-alert mute.
Feature implementation was subsequently authorized through the implement skill.
