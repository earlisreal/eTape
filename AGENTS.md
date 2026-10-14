# eTape Agent Guide

Local-first US-stock trading platform: Go engine plus TypeScript/React/Vite UI. moomoo OpenD supplies primary market data; sim, Alpaca, TradeZero, and moomoo adapters execute orders.

## Guides

- [Codebase and user guide](README.md)
- [Engine](engine/README.md), [UI](ui/README.md), [external APIs](docs/external-apis.md)
- [Documentation](docs/README.md), [plans](docs/plans/README.md), [prototypes](prototypes/README.md), [scripts](scripts/README.md)

## Agent skills

### Issue tracker

Issues are tracked as local Markdown files under `.scratch/<feature-slug>/`. See `docs/agents/issue-tracker.md`.
`.scratch/` is versioned project history. Before handoff, commit every tracker file created or changed by the current task, subject to the spec/plan approval gate below; leave unrelated tracker files untouched.

### Triage labels

Issue status uses the five default canonical triage roles. See `docs/agents/triage-labels.md`.

### Domain docs

This repository uses a single-context domain layout. See `docs/agents/domain.md`.

### Skill resolution

Explicit skill paths are authoritative: verify and read the referenced `SKILL.md` before deciding availability. Treat any skills it references as active dependencies and read them before proceeding; absence from the catalog alone is insufficient.

## Invariants

- High-frequency data never flows through React state. Stores/controllers mutate imperatively; canvas/chart rendering coalesces work.
- Go types in `engine/internal/uihub/wsmsg` own WebSocket contract. `ui/src/gen/wsmsg.ts` is generated; never edit it.
- TICKER ticks build exchange-time 10-second bars. One-minute K-lines feed larger intraday bars. Daily is fetched; weekly/monthly derive from daily.
- OpenD uses raw TCP plus protobuf at configured address. Trade unlock occurs in OpenD GUI, not engine.
- Credentials/config/database stay under `~/.eTape/`; never commit sensitive runtime data.
- Every executed plan updates relevant READMEs when flow, interfaces, dependencies, invariants, or operations change.

## Commands

```bash
cd engine && go build ./cmd/etape
cd engine && go test ./...
cd engine && mingw32-make gen-ts-check
cd ui && npm test
cd ui && npm run typecheck
cd ui && npm run e2e
```

## Validation

Use focused local validation for code and executable-input changes, including
approved plans and substantial changes. Select tests and applicable lint,
type, and build checks for the affected behavior and callers. Include race
tests for concurrency/lifecycle changes, `gen-ts-check` for WebSocket contract
changes, and relevant install/build checks for dependency or build changes.
Broaden local checks when impact is unclear or failures reveal wider regressions.
Run `git diff --check` for every task.

Full hosted CI continues to run on every push to `main` and every pull request.
Before handing off code or executable-input changes, wait for a successful full
run on the pushed `main` commit and resolve failures. Releases and tags do not
replace this gate. [.github/workflows/ci.yml](.github/workflows/ci.yml) remains
the executable source of truth for the full suite; the
[Windows checklist](README.md#ci-equivalent-validation-on-windows) is available
for broader local verification and CI failure diagnosis. Every handoff must
list checks run and results, plus every skipped required check and its reason.

For prose-only documentation changes, including approved plans, specs, and
tracker files, validate Markdown links and run `git diff --check`. Skip
engine/UI checks and hand off without waiting for hosted CI; report it as
pending if it is still running. Changes to executable inputs, code, CI, build
configuration, dependencies, or generated contracts follow normal validation
regardless of file extension.

## Live-order safety

Never place, modify, or cancel real orders unless Earl explicitly authorizes current session and reconfirms live run. Read-only account checks allowed. Authorized live leg: RTH only, cheap liquid symbol, long only, one-share marketable limit, flatten immediately.

## Git

Keep commits scoped. Main hook escape only when task explicitly authorizes it. Preserve unrelated changes. Approved specs use `docs(specs):`; plans use `docs(plans):`.

Keep specs and plans as uncommitted drafts while they are being grilled or revised. After a plan is implemented, commit it with the resulting changes unless Earl explicitly asks to keep it as a draft; no separate final-approval gate applies to an executed plan.

After completing any task that changes repository files, including small tasks and review fixes, automatically:

1. Run the required validation and commit only the task's changes in its branch or worktree.
2. Fetch `origin/main`, integrate upstream changes if needed, and merge the task branch or detached worktree HEAD into local `main`, preserving unrelated work.
3. Push `main` to `origin` immediately after merging. Before handoff, verify that local and remote `main` contain the task commit and satisfy the hosted CI requirements in [Validation](#validation).

This is standing authorization for commit, merge, and push; no further confirmation is needed. Explicit requests to keep work as a draft, leave changes uncommitted, or defer merging or pushing override this rule.
