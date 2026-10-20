# Selected Execution Venue badges

Status: implemented on 2026-10-21. Plan approved for implementation on 2026-10-21.

## Goal

Make a selected real-money Execution Venue recognizable at a glance throughout
the workspace: show a static red-outlined **LIVE** badge beside the venue in
the Top Bar and beside the venue selectors in Account and Order Ticket Panel
Headers. Use neutral **PAPER** and **SIM** badges for known practice venues.

Example Top Bar cue: `NVDA · moomoo [LIVE]`, followed by the existing T-M-S
Venue Instrument Eligibility group when available.

## Non-goals

No changes to venue selection, Link Group ownership, hotkey targeting, trading
locks, live-switch confirmation, order routing, or existing-order ownership.
No additional chart overlays, banners, animation, sounds, settings, persisted
state, dependencies, engine changes, or WebSocket contract changes.

## Design basis

- [TopBar.tsx](../../ui/src/chrome/TopBar.tsx) already displays the hotkey
  target's Link Group dot, symbol, and venue, plus Venue Instrument Eligibility.
  Its cue receives venue environment metadata separately from the target.
- [AppShell.tsx](../../ui/src/chrome/AppShell.tsx) owns the cross-window hotkey
  target and reads execution status. Its `appShellExecSignature` currently
  includes master arm state and venue IDs/brokers/environments so environment
  updates render without subscribing the shell to account/P&L changes.
- [AccountPanel.tsx](../../ui/src/chrome/panels/AccountPanel.tsx) and
  [OrderTicketPanel.tsx](../../ui/src/chrome/panels/OrderTicketPanel.tsx) already
  resolve their selected venue's status and portal venue selectors into their
  Panel Headers.
- [VenueStatus](../../ui/src/gen/wsmsg.ts) already supplies `broker` and optional
  `env` through `exec.status`. The simulator is configured as broker `sim` with
  environment `paper` in [config.go](../../engine/internal/config/config.go).
  Generated TypeScript must remain untouched.
- [global.css](../../ui/src/global.css) defines `.chip` and the red `.chip-live`
  treatment already used by Settings. Top Bar labels can truncate, and T-M-S
  tiles are hidden below 900px; the new badge must retain its own space.
- [ADR 0005](../adr/0005-link-group-owned-execution-venue.md) and the
  [domain glossary](../../CONTEXT.md) establish that each Link Group owns its
  Execution Venue. A pinned panel has none; there is no global venue fallback.

## Design decisions

### Meaning and appearance

Resolve the badge only from the exact selected venue's current status entry.
Normalize `env` case before classification; never infer environment from the
venue ID, broker name, account balance, connection state, or trading lock.

| Matching venue metadata | Badge | Appearance |
| --- | --- | --- |
| `env: live`, any broker | LIVE | Existing `.chip-live`: red text/border, faint red background |
| `env: paper`, `broker: sim` | SIM | Muted text/border, neutral background |
| `env: paper`, another broker | PAPER | Same neutral treatment as SIM |
| Missing venue/status, or missing/unrecognized `env` | None | Keep the existing venue text and blocked-state cues |

An explicit live environment takes precedence over broker identity. Unknown
metadata must never be presented as PAPER or SIM. A disconnected live venue
keeps its LIVE badge while its status metadata remains available; toggling the
trading lock does not change the badge. If the selected venue or its metadata
disappears, remove the badge instead of retaining another venue's state.

Keep badges static and display-only. Include the environment in the Top Bar
cue's accessible name and tooltip, and give panel badges descriptive accessible
text/tooltips such as `Live venue: real-money account`. Preserve Link Group dot
colors and all existing lock, connection, and eligibility indicators.

### Placement and scope

- **Top Bar:** insert the badge immediately after the existing symbol/venue
  label and before the T-M-S tiles. Match the current hotkey target's venue
  against this window's execution status; use the existing cross-window target
  coordination without adding environment to its messages. A grouped target
  with a venue can show its badge even while hotkeys are blocked by a missing
  symbol. No target, an ungrouped target, or no venue means no badge.
- **Account and Order Ticket:** insert the badge immediately after each venue
  selector in its Panel Header. Each follows that panel's resolved Link Group
  venue, so different groups can show different environments simultaneously.
  Pinned panels show no badge. Working orders on a previous venue retain their
  existing warning without affecting the selected venue's badge.
- **Sizing:** keep the badge non-shrinking and let the existing target label
  ellipsize first. Retain the badge when eligibility tiles collapse. Verify
  narrow Top Bars and Panel Headers keep the badge and existing actions readable.

## File-level implementation

1. Add a small display-only
   `ui/src/chrome/exec/VenueEnvironmentBadge.tsx` shared by all three surfaces.
   Keep classification in one place, using existing `VenueStatus` types and
   existing chip styling. Expose the same classified label for the Top Bar's
   accessible name/tooltip. Add only a neutral chip modifier and necessary
   non-shrinking layout rules to [global.css](../../ui/src/global.css).
2. In [AppShell.tsx](../../ui/src/chrome/AppShell.tsx), include environment in
   the existing primitive execution signature and pass the exact target's
   matching venue metadata into TopBar. Preserve the selective subscription:
   account/P&L updates and shell-equivalent status replacements must not cause
   new shell renders. Do not subscribe the shell to the whole execution snapshot.
3. In [TopBar.tsx](../../ui/src/chrome/TopBar.tsx), render the shared badge in
   the target cue and include its environment in accessible text. Retain all
   existing target-state messages and eligibility query behavior.
4. In [AccountPanel.tsx](../../ui/src/chrome/panels/AccountPanel.tsx) and
   [OrderTicketPanel.tsx](../../ui/src/chrome/panels/OrderTicketPanel.tsx), render
   the shared badge beside the existing selector using already-resolved status.
   Keep their current subscriptions, selector handlers, confirmation, and
   portal/fallback behavior. Let the shared
   [PanelFrame](../../ui/src/chrome/PanelFrame.tsx) action slot and each inner
   action row shrink so a long venue selector yields space to the badge and
   existing buttons. Top Bar side columns reserve their content width while
   its center label can truncate before its badge.
5. Extend the existing four test files below; cover shared classification
   through TopBar tests. Register the existing PanelFrame tests in the
   `chrome-regressions` project in [vitest.config.ts](../../ui/vitest.config.ts)
   so its affected shared action slot is also covered by local and hosted tests.
   Update the Top Bar description in the [Chrome guide](../../ui/src/chrome/README.md)
   and venue presentation in the [execution UI guide](../../ui/src/chrome/exec/README.md).
   Mark this plan implemented and move its index entry only after execution.

## Acceptance and regression coverage

- [TopBar.test.tsx](../../ui/src/chrome/TopBar.test.tsx): LIVE/PAPER/SIM text
  and styling; mixed-case environment; misleading venue IDs; missing/unknown
  metadata; target changes/removal; a grouped missing-symbol target; no badge
  for ungrouped/no-venue targets; LIVE while locked/disconnected; accessible
  environment text; unchanged T-M-S ordering and query arguments.
- [AppShell.test.tsx](../../ui/src/chrome/AppShell.test.tsx): an environment
  arriving or changing for the same venue ID/broker updates the Top Bar without
  a target revision. Existing account-only and shell-equivalent-status render
  assertions remain valid. An incoming cross-window target selects the correct
  venue metadata, and removal clears the badge.
- [AccountPanel.test.tsx](../../ui/src/chrome/panels/AccountPanel.test.tsx) and
  [OrderTicketPanel.test.tsx](../../ui/src/chrome/panels/OrderTicketPanel.test.tsx):
  independently selected Link Groups, accepted/canceled venue switches, metadata
  changes, pinned/missing venues, and live badges surviving disarm/disconnection.
  Existing order and confirmation assertions remain intact.
- Inspect mock/simulated UI in light and dark themes, normal and narrow windows,
  and compact Panel Headers. Verify the badge stays legible alongside long venue
  IDs, eligibility tiles, and header actions. Check two workspace windows with
  different Link Group venues and a changing hotkey target. Use mocked live
  metadata only; no real-order operations are needed.
- [venue-environment.spec.ts](../../ui/e2e/venue-environment.spec.ts) exercises
  production headers with long venue IDs, light/dark themes, narrow windows,
  and cross-window environment updates using intercepted demo status frames.
  [ticketless-hotkeys.spec.ts](../../ui/e2e/ticketless-hotkeys.spec.ts) also
  verifies SIM follows the target and disappears for an ungrouped target.

## Validation and handoff

Run from `ui/`:

```bash
npx vitest run --project chrome-regressions src/chrome/TopBar.test.tsx src/chrome/AppShell.test.tsx src/chrome/PanelFrame.test.tsx src/chrome/panels/AccountPanel.test.tsx src/chrome/panels/OrderTicketPanel.test.tsx
npm run lint
npm run build
# Use a spare ETAPE_UIHUB_PORT for the isolated demo engine.
npx playwright test e2e/ticketless-hotkeys.spec.ts e2e/venue-environment.spec.ts
```

The build includes type checking. Complete the visual checks above, validate
changed Markdown links, and run `git diff --check`. No local engine, race,
contract-generation, or dependency-install checks are needed for this UI-only
presentation change; broaden checks if implementation changes that scope.

Commit the executed plan with the implementation and relevant guide changes,
integrate upstream, merge and push main, verify local/remote commit containment,
and wait for successful full hosted CI per [AGENTS.md](../../AGENTS.md#validation)
and the [CI workflow](../../.github/workflows/ci.yml). Report checks and results,
including every skipped required check and its reason.

## Rollout, rollback, and risks

Ship in the normal UI update with no flag or data migration. Reverting the
scoped implementation commit restores the prior presentation.

The main risks are stale badges when environment metadata arrives without a
venue-ID change, a badge reflecting a different Link Group's venue, falsely
classifying unknown metadata as practice, and clipping in narrow headers.
The selective-subscription, venue-scope, unknown-state, and visual checks above
address those cases. LIVE describes the account environment; it does not
promise connection, instrument eligibility, or permission to submit an order.
