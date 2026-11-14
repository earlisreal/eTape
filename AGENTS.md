# eTape Agent Guide

eTape is a local-first US-stock trading platform with a Go engine and a TypeScript/React/Vite UI. moomoo OpenD supplies primary market data. Simulator, Alpaca, TradeZero, and moomoo adapters handle execution.

## Guides

- For launch, configuration, or releases, see the [user guide](README.md).
- For implementation and tests, start with the [engine](engine/README.md) or [UI](ui/README.md) guide. For providers and broker contracts, see [external APIs](docs/external-apis.md).
- For tickets, use the [local tracker](docs/agents/issue-tracker.md) and its [triage statuses](docs/agents/triage-labels.md). For design work, see the [specifications](docs/specs/README.md), [plans](docs/plans/README.md), and [domain guidance](docs/agents/domain.md).
- For an explicit skill path, check the named `SKILL.md` and read its required references even if the skill is absent from the catalog.

## Workflow

- For intermittent bugs, use available recordings and logs to locate the symptom and its timing. Reproduce the failure with a focused check when feasible and state which causes remain unconfirmed.
- For UI regressions involving streaming updates, browser layout, or multiple windows, verify the affected interaction with controlled demo data or fixtures when unit tests cannot exercise it.
- For changes to saved or imported workspaces, check that layouts from before the change still load and retain unrelated panels and settings.
- Update the guide, specification, or decision record that owns any changed flow, interface, dependency, invariant, or operation.
- In handoffs, lead with what changed for Earl and include steps to verify it in the app when applicable.

## Invariants

- Keep high-frequency data in imperative stores and controllers, outside React state. Coalesce canvas and chart rendering.
- Change the WebSocket contract in `engine/internal/uihub/wsmsg` and regenerate `ui/src/gen/wsmsg.ts` from the Go types.
- TICKER ticks build exchange-time 10-second bars. One-minute K-lines feed larger intraday bars. Weekly and monthly bars derive from fetched daily bars.
- OpenD uses raw TCP plus protobuf at the configured address. Unlock trading in the OpenD GUI.
- Keep credentials, config, and the database under `~/.eTape/`. Keep sensitive runtime data out of commits.

## Validation

Run `git diff --check` for every task. In the handoff, list checks run, their results, and the reason for each skipped required check.

- For prose-only changes, local validation consists of Markdown link and whitespace checks. Hand off after local validation and report hosted CI as pending if it is still running.
- For code and executable inputs, run focused tests and applicable lint, type, install, and build checks for the changed behavior and its callers. Include race tests for concurrency or lifecycle changes and `gen-ts-check` for WebSocket contract changes. Broaden local checks when impact is unclear or failures reveal wider regressions.
- Full hosted CI runs on pushes to `main` and pull requests. For code and executable inputs, resolve failures and verify a successful full run for the pushed `main` commit before handoff. Use [the CI workflow](.github/workflows/ci.yml) for the full suite and the [Windows checklist](README.md#ci-equivalent-validation-on-windows) for broader local verification or CI diagnosis.

## Live-order safety

Earl must explicitly authorize the current session and reconfirm the live run before any real order placement, modification, or cancellation. Read-only account checks are allowed. For an authorized live test, use a low-priced, liquid symbol during RTH. Go long one share with a marketable limit and flatten immediately.

## Git

Keep commits scoped and preserve unrelated work. Include task-owned `.scratch/` tracker files in the task commit, subject to the draft rules below. Use `docs(specs):` for approved specs and `docs(plans):` for plans. Bypassing a `main` hook requires explicit authorization for the task.

Keep specs and plans uncommitted during drafting and revision. Publish standalone specs and plans once approved. After implementing a plan, commit it with the resulting changes unless Earl asks to keep it as a draft.

After validating any task that changes repository files, including small fixes, proceed under standing authorization unless Earl asks to defer a step.

1. Commit the task's changes in its branch or worktree.
2. Fetch `origin/main`, integrate upstream changes if needed, and merge the task branch or detached worktree HEAD into local `main`, preserving unrelated work.
3. Push `main` to `origin` immediately after merging. Before handoff, verify that local and remote `main` contain the task commit and satisfy the hosted CI requirements in [Validation](#validation).
