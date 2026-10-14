# Account protection verification

## Goal

Remove the persistent Chart Risk Entry protection banner from charts. Show
current protection problems once per symbol in the selected Account panel,
automatically verify broker positions, and retire warnings after confirmed
flatness without reviving them on a later trade.

## Current evidence

- `ui/src/chrome/panels/tv/ChartOrderMarkers.tsx` calculates remaining shares
  using entry fills minus linked exits. Ordinary sells and outside exits do
  not reduce that historical sum.
- `engine/internal/exec/risk_entry.go` retains protection failures in history.
- Account polling refreshes balances; full broker snapshots normally happen
  at startup/reconnection. Alpaca also pushes absolute position quantities.
- The existing snapshot handler synchronizes risk protection, so an
  observational check must avoid that activation path.

## Decisions

- Earl approved Account-only warnings and automatic verification on
  2026-10-14. A manual Refresh button is outside this change.
- The engine owns the warning projection. It includes the verified current
  position quantity, rather than asking React to join separately delivered
  position and order updates.
- Broker checks are bounded and coalesced per venue. Only unresolved risk
  protection problems require checks; retry/reverification uses the existing
  one-second execution timer with a throttled interval.
- A check must never submit, modify, cancel, or resume an order. Concurrent
  position/order changes invalidate its result. Failure remains visibly
  unverified and retries automatically.
- Persist the entry's cumulative filled quantity at a confirmed flat
  boundary, preserving failure history. Subsequent additional fills reopen
  actionable exposure; an unrelated later trade does not.

## Implementation

1. Add chart regression coverage and remove the historical chart banner.
2. Add durable flat-boundary accounting and observational verification in
   the execution core. Reuse broker snapshots and existing position projection.
3. Publish Account protection warnings through `exec.status`, map them from
   Go-owned wire types, and regenerate TypeScript.
4. Display venue-scoped warnings in Account across its tabs. Unknown position
   readiness must not appear as a confirmed zero-position count.
5. Update engine/UI guides and validate the complete Windows CI checklist.

## Validation and risks

Cover ordinary/external flat exits, restart/new-symbol exposure, late entry
fills, actual position quantities, failed/stale concurrent checks, venue
scope, and absence of broker mutations from verification. Run engine tests,
race tests, vet/lint, contract generation checks, UI lint/tests/build, Markdown
link validation, and diff checks. Complete main integration, push, and hosted
CI per [the agent guide](../../AGENTS.md).

Rollback by reverting the task commit. Historical failure details remain
available; automatic checks do not establish that a protective order will fill.
