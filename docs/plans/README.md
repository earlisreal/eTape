# Plans

Create one focused Markdown file per approved change. Name `YYYY-MM-DD-short-feature.md`. Include goal, non-goals, current-code evidence, design decisions, file-level steps, tests, rollout/rollback, and risks. Remove completed task narration; move durable outcomes into subsystem guides or [specifications](../specs/README.md).

Implemented:

- [Tick recording for wick investigations and future order-flow charts](2026-10-08-tick-recording.md)

## Completion checklist

- Implementation and proportional tests complete.
- Relevant READMEs updated for any changed flow, interface, dependency, invariant, or operational behavior.
- Generated sources regenerated from owners, never hand-edited.
- Markdown links and code references validated.
- No credentials, account identifiers, balances, live keys, or capture secrets added.
- `git diff --check` clean; commits scoped; complete main integration and push per [AGENTS.md](../../AGENTS.md#git).
