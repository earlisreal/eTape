# Plans

Create one focused Markdown file per approved change. Name `YYYY-MM-DD-short-feature.md`. Include goal, non-goals, current-code evidence, design decisions, file-level steps, tests, rollout/rollback, and risks. Remove completed task narration; move durable outcomes into subsystem guides or [specifications](../specs/README.md).

Approved, implementation pending:

- [Visible Range Volume Profile Indicator](2026-10-10-volume-profile-indicator.md)

Implemented:

- [Chart Risk Entry acceptance without a recent trade](2026-10-15-chart-risk-accept-without-ticks.md)
- [Chart price-label priority](2026-10-14-chart-price-label-priority.md)
- [Account protection verification](2026-10-14-account-protection-verification.md)
- [Tick recording for wick investigations and future order-flow charts](2026-10-08-tick-recording.md)

## Completion checklist

- Implementation and proportional tests complete.
- Relevant READMEs updated for any changed flow, interface, dependency, invariant, or operational behavior.
- Generated sources regenerated from owners, never hand-edited.
- Markdown links and code references validated.
- No credentials, account identifiers, balances, live keys, or capture secrets added.
- `git diff --check` clean; commits scoped; complete main integration and push per [AGENTS.md](../../AGENTS.md#git).
