# Chart Order Gesture disclosure in Settings

Status: implemented — design approved by Earl on 2026-09-30.

## Problem

The live Engine-Held Stop-Limit disclosure disappears when the pointer moves
toward **I understand — enable** after a Chart Order Gesture. Its dialog shares
the chart preview's wrapper: preview refresh and pointer leave can set that
wrapper to opacity 0 and pointer-events none while the dialog remains mounted.

Move the disclosure to Settings when configuring a Chart Order Gesture, so the
trader reads it before order entry and its visibility is independent of the
chart's preview lifecycle.

## Domain and existing authority

- A Chart Order Gesture belongs to an Action Template. The chart's Link Group
  determines the Execution Venue; a gesture does not identify an account.
- Live Held Stop-Limit Acknowledgement belongs to an actual live account
  identity and applies across chart, ticket, hotkeys and Deck. It enables local
  custody without placing an order.
- The engine checks acknowledgement on new live Engine-Held Stop-Limit
  admissions. Paper/sim and broker-native routes do not require it. At engine
  startup, credential/account or OpenD endpoint changes invalidate a stored
  acknowledgement through the existing identity fingerprint.
- Settings stages Action Template edits until **Save**. Account acknowledgement
  already uses a separate immediate engine command and authoritative venue
  status. Imports save normalized bindings without selecting the gesture field.

Sources: [glossary](../../CONTEXT.md),
[Link Group venue ownership](../../docs/adr/0005-link-group-owned-execution-venue.md),
[implemented stop-limit plan](../../docs/plans/2026-09-30-chart-order-markers-and-engine-held-stop-limit.md),
[gesture selection](../../ui/src/chrome/exec/OrderSettingsSection.tsx),
[chart entry](../../ui/src/chrome/panels/tv/ChartStopLimitEntry.tsx),
[engine admission](../../engine/internal/exec/core.go),
[acknowledgement command](../../engine/internal/uihub/commands.go).

## Approved Settings flow

1. Adding or changing a nonempty Chart Order Gesture opens a stable Settings
   disclosure. Selecting **Unbound** needs no disclosure. Canceling the
   disclosure keeps the previous binding; continuing stages the selected
   binding, which becomes active only through the existing Settings **Save**.
2. Every bound template has a **Review / enable live accounts** entry point.
   This includes existing and imported bindings, so account enablement is
   reachable without changing or recreating a gesture.
3. Explain that the gesture invokes its Action Template at the clicked stop
   price, using the chart's Link Group Execution Venue. Explain local custody:
   **Held by eTape — no broker protection** before trigger; primary moomoo OpenD
   Last-Eligible Prints drive triggering; engine/feed loss pauses evaluation and
   requires manual Resume.
4. List all configured live Execution Venues with broker and venue labels. Each
   unacknowledged account has its own explicitly named enablement button; none
   is selected or enabled by default. Already acknowledged accounts show their
   status without requiring another acknowledgement. Paper/sim needs no account
   acknowledgement, and gestures remain configurable with no enabled live
   account.
5. Explicit account enablement takes effect immediately through the existing
   acknowledgement command. Clearly state that this enables Engine-Held
   Stop-Limits across all order-entry methods for that account, and that closing
   Settings discards template edits but retains explicit account enablement.
   Canceling template configuration does not revoke an acknowledged account.

Use accessible controls and the existing Settings theme. Pointer movement,
chart hover updates and modifier release cannot dismiss or hide the disclosure.
Close controls and keyboard dismissal follow the same cancellation behavior.

## Approved chart flow

- Remove the first-use chart disclosure and its acknowledgement command path.
- An existing or imported gesture that reaches an unacknowledged live held route
  remains blocked. Show the named venue and a clear instruction to open
  **Settings → Orders & hotkeys → Review / enable live accounts**. Announce the
  blocked attempt and keep the preview's custody label visible alongside it.
- Account acknowledgement does not submit or replay the blocked gesture. The
  trader must make a fresh gesture after enablement is confirmed. Existing
  modifier rearm and execution validation rules still apply.
- The engine remains the admission authority, including for imports, account
  changes and races after preview. Native and paper/sim entry keep their existing
  execution behavior.

## Failure and scope rules

While an account acknowledgement is pending, prevent duplicate enablement
requests for that account. A rejected or unknown result shows explicit feedback;
present confirmed enablement only from authoritative venue status. Missing
status or unavailable engine connection cannot imply successful enablement.
Account removal and identity changes refresh the displayed account status rather
than transferring acknowledgement to another account.

Reuse SettingsModal's existing command interface and ExecStore, the existing
account acknowledgement command, and the existing Settings entry point. Keep
chart previews imperative; account/settings status is low-frequency UI state.
Add no new persistent gesture-acknowledgement flag or dependency.

This is a chart-entry and Settings change. Existing ticket acknowledgement,
hotkey/Deck rejection feedback, held-order Resume, engine identity/restart rules,
order routing and generated contracts retain their current scope. In particular,
Resume does not currently recheck acknowledgement; changing that is separate
work. The reversible UI placement does not warrant a new ADR.

## Acceptance checks

- Selecting/changing a binding opens the Settings disclosure; Unbound does not.
  Cancel retains the previous binding, and continuing stages it until Save.
- Existing/imported bound templates expose the review entry point. Review alone
  changes neither the binding nor account enablement.
- Live accounts are individually named and enabled explicitly; enabling one
  does not enable another. Already acknowledged accounts need no repeat
  acknowledgement. Configuring a gesture requires no live account.
- Account enablement persists immediately and survives abandoning template
  edits. Pending, rejected, unknown and authoritative status updates display the
  correct result and never submit an order.
- Unacknowledged live held chart entry shows the Settings instruction and
  custody label, sends neither SubmitOrder nor AcknowledgeHeldStopLimit, and
  opens no chart disclosure. Imported bindings and switching to another account
  follow the same guard.
- Confirmed account enablement requires a fresh chart gesture. Existing
  paper/sim and native-route chart entry, preview snapshot and modifier rearm
  checks continue to pass.
- Moving the pointer over disclosure controls keeps them visible and usable;
  cancellation and keyboard behavior preserve the previous staged binding.

Use the existing OrderSettingsSection, SettingsModal and ChartStopLimitEntry
test seams. After implementation, update relevant execution/panel READMEs and
run the repository's required Windows validation checklist with proportional
behavioral coverage. No live-order activity is authorized by this specification.

## Implementation and validation

The Settings disclosure now stages chart-gesture edits until Save and offers
per-live-account enablement from every bound template. Account status is shown
as enabled only after ExecStore confirms it. The chart blocks unacknowledged
live Engine-Held routes with a visible Settings instruction and custody label;
acknowledgement does not replay the blocked gesture. The execution and chart
panel READMEs were updated. No live-order activity was used.

Pending acknowledgement guards survive closing and reopening Settings in the
same ExecStore. False status deltas keep the request pending; confirmed status,
account removal, rejection, or an unknown command result clears it. A newer
status snapshot that still reports false also clears stale pending state after
an engine reconnect, allowing the current account identity to be acknowledged.

Passed checks: `go test ./...`, `go test -race -short ./...`, `go vet ./...`,
`golangci-lint run`, `mingw32-make -C engine gen-ts-check`, `npm ci`,
`npm run lint`, `npm test`, and `npm run build`. Focused Settings and chart-entry
regression tests also passed. Hosted CI must still complete successfully after
the change is pushed.

## Design verification

The original symptom was reproduced through the actual ChartStopLimitEntry:

```powershell
cd ui
node node_modules/vitest/vitest.mjs run --project chart-panel src/chrome/panels/tv/ChartStopLimitEntry.test.tsx -t 'keeps the live disclosure visible while moving to the enable button'
```

The temporary check failed because opacity became 0. A one-pixel move also
failed; suppressing the scheduled preview refresh preserved visibility. The
dialog stayed mounted, ruling out disclosure-state clearing; movement occurred
after pointerup, ruling out drag cancellation. Temporary probes were removed,
and the restored ChartStopLimitEntry suite passed all three tests.

The design and glossary passed documentation-link and git diff checks. Earl
accepted all five recommendations across the two grilling rounds, completing
shared understanding; the implementation and validation above complete that
approved design.
