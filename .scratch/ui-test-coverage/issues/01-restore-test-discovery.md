# Restore omitted UI test coverage

Type: task
Status: resolved

Earl requested this fix on 2026-10-25 after the repository audit identified
existing UI test files omitted from normal validation.

## Baseline and scope

- There are 173 Vitest test files outside Playwright's `e2e` directory.
- Existing projects collect 101 files; 72 files belong to no project.
- The manual `npm test` command additionally omits the registered
  `LadderSettingsDialog.test.tsx`, so it schedules only 100 files.
- Keep the existing project batches and native canvas process isolation.
- Restore all files, fix failures exposed by coverage, and prevent silent
  omissions without removing assertions or adding skips.

## Regression seam

The new runner compares Vitest's actual project/file inventory with Node's
filesystem glob using Vitest's default test pattern. Running it against the
old configuration failed deterministically with all 72 unassigned files before
executing any tests. It also rejects duplicate project ownership and test
files outside the expected inventory.

## Comments

- A default `unit` project collects files not claimed by existing projects.
- `npm test` uses the actual inventory rather than a manual command chain;
  native canvas projects run one file per process.

## Answer

- Restored all 73 omitted files to normal `npm test` and hosted CI, including
  Ladder Settings. All 173 files are assigned exactly once across 11 projects.
- Added an inventory guard against missing/duplicate project ownership and a
  runner that discovers project batches instead of maintaining a command list.
  Golden, Ladder and Tape tests retain one file per Node process.
- Restored coverage exposed four stale assertions. Cancel Last now verifies
  the Link Group venue already passed by production; chart control tests verify
  the existing shared workspace palette and font. No production behavior changed.

## Validation

- Regression: the inventory guard failed with all 72 unassigned files against
  the old configuration, before running tests.
- Complete `npm test`: 173 files and 2,162 tests passed, including the restored
  72-file/533-test fallback batch and seven Ladder Settings tests.
- ESLint, TypeScript and production build passed; the existing large-bundle
  advisory remains. Changed Markdown local links resolve; `git diff --check`
  passed.
- Local engine/race/contract checks and browser E2E were not repeated for this
  test-only change. Full hosted CI on pushed main is the final handoff gate and
  includes engine checks on Linux/Windows plus UI lint, tests and build.
