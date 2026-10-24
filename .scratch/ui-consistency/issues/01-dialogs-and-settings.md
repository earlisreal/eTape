# Consistent dialogs, popups, and Settings

Type: task
Status: resolved

Improve UI/UX with consistent outside-click dismissal, popup/modal/button
appearance, and a review of Settings. Authorized directly by Earl on 2026-10-24.

## Answer

- Shared dialog and dismissal lifecycle: themed shell, close control, outside
  clicks, Escape, modal Tab containment, focus restoration, nested surfaces, and
  pending-operation dismissal guards. Panel type-to-load registration is balanced.
- Settings has persistent navigation/header/footer, section/save guidance,
  grouped General controls, accessible sound controls, and a narrow-window layout.
- Chart controls share workspace typography/chrome; canvas market colors stay
  unchanged. Chart/indicator/Ladder/Tape dialogs retain draggable headers.
- Menus, Link Group, Scanner filters/columns, drawing-clear confirmation, alert
  editors and trailing-stop editors use the shared controls and dismissal.
- Settings imports use nested confirmations. Workspace rename is inline; Delete
  uses the shared two-click confirmation. Existing validation and protected
  workspace rules remain in force.

Validation and delivery results are recorded in the task handoff. Interaction
coverage is registered in `ui/vitest.config.ts`; browser cases and screenshots
are produced by `ui/e2e/ui-consistency.spec.ts` using the isolated demo engine.

## Validation

- Full UI test suite: 1,622 tests passed, including render baselines.
- UI lint, typecheck, and production build passed. The existing large-bundle
  advisory remains; no dependencies or build configuration changed.
- Ten focused browser checks passed for settings, import/export, nested
  confirmation, outside/Escape dismissal, focus return, light/dark/narrow
  layouts, chart settings, New Window, and Practice. Screenshots inspected.
- The older `settings-redesign.spec.ts` was also attempted: its order test
  assumes the former non-portalled panel header and its venue test expects
  obsolete onboarding copy. That file is unchanged; those two cases still need
  maintenance. No live broker actions were exercised.
- Local engine/race/contract checks are outside this UI-only change; full hosted
  CI on pushed main remains the delivery gate. Markdown links and whitespace
  are checked before committing.
