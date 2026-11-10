# Keep the Scanner divider at its dragged size

Type: task
Status: resolved

## Reproduction

The supplied recording shows the Chart/Scanner divider jumping back during a
drag. An isolated browser reproduction moved the divider from x=437 to x=407;
a Scanner refresh restored x=437 within 50 ms while the pointer stayed down.
The same drag passed when refreshes were suppressed.

## Fix

AppShell rebuilt Dockview's component map on each Scanner update. Dockview's
options update forced layout using the proportions saved before the drag.
Memoize the map by panel identity and keep its callbacks stable. Factories
read current panel config when explicitly restoring a workspace with the same
IDs. Completed layout changes also update AppShell's workspace snapshot;
otherwise later Scanner Sync saves overwrite the new grid with old dimensions.

## Validation

- Three browser scenarios passed: live Scanner refreshes, Sync off/on, chart
  symbol reassignment, dragging both ways, release, saved-width reload, and
  independent Scanner filters.
- 125 focused workspace, panel, preset, Scanner Sync, and persistence tests passed.
- UI lint, typecheck, production build, five local Markdown links, and
  `git diff --check` passed.
- Full hosted CI is required on the pushed main commit before handoff.
- Local engine/race/contract checks are unnecessary for this UI-only change;
  the hosted workflow runs its complete engine and UI suites.

## Comments

All reproduction and browser validation used an isolated sim-only demo engine;
no live broker orders or user runtime configuration were changed.
