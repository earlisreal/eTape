# Startup market-data latency

Status: Implemented on 2026-10-07.

Plan: [Startup market-data latency](../../docs/plans/2026-10-07-startup-market-data-latency.md).

## Accepted scope

Review whether the pull pause is necessary, then improve startup delays affecting
charts, Scanner, Level 2, and Time & Sales. Preserve provider pacing and quota
checks. Reuse persisted request timing instead of imposing a new full wait on
each ordinary launch. Remove unrelated optional-metadata and serial-cache
dependencies. Repair Scanner REL VOL candidate warming. No loading banner is
part of this change.

## Comments

- 2026-10-07: Earl reported startup warming and requested scrutiny of the pull pause.
- 2026-10-07: Earl authorized improving delay causes and checking Level 2 / Time & Sales.
- Code tracing found no Ladder/Tape UI history barrier; fixes belong in shared engine paths.
