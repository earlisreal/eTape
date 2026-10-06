# Chart Risk Entry freshness warning

Date: 2026-10-07

Display-only follow-up to the auto-send freshness fix. During setup, rendering
checks all existing gates except the saved eligible-print freshness. Submission
still requires a fresh print after its query. A failed submission saves its
blocking reason, which remains visible during later repaints and clears after
a successful explicit retry.

## Checks

- The two new regression cases failed before the fix: expired and untrusted
  setup previews incorrectly displayed the freshness warning.
- `npx vitest run --project chart-panel`: passed, 186 tests in 11 files, including
  all 24 Risk Entry component tests. New cases cover quiet setup, visible
  send-time failure through repaint, no submission on failure, and fresh retry.
- `npm run lint`: passed.
- `npm run build`: passed, including both TypeScript checks.
- Scoped `git diff --check`: passed.

Proportional UI checks apply under AGENTS.md's small-isolated-change rule.
The full local engine/contract/dependency/golden/E2E checks were not required:
engine, contracts, dependencies, layout, and canvas output are untouched.
Hosted CI is verified for the pushed commit before handoff. No live orders.

The unrelated `request_budget.go` edit and existing debug files were excluded
from the task commit and preserved in the working tree.
