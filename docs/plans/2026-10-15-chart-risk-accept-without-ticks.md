# Chart Risk Entry acceptance without a recent trade

Approved design: [spec](../../.scratch/chart-risk-accept-without-ticks/spec.md).

## Goal and evidence

Accept a valid conditional pair when chart placement finishes, even when a quiet
symbol has no recent eligible trade. The current risk-specific admission gate in
`engine/internal/exec/risk_entry.go` and UI freshness check reject that case.
Ordinary engine-held orders already persist WAITING orders before a fresh trade
arrives. Existing Risk Entry endpoint edits also omit initial auto-send behavior.

## Implementation

1. Add component and engine regressions before changing production behavior.
   Cover missing/stale trades, immediate fresh activation, a print expiring
   during ticker acquisition, completed price corrections, and preserved gates.
2. Remove only the market-trade freshness prerequisite from risk admission and
   draft validation. After ticker acquisition, check current print trust and
   freshness before immediate activation, and revalidate account age and session
   expiry. Persist the existing WAITING state with a visible market-data reason
   when no fresh trade exists. Persistence cannot authorize an expired trigger.
3. Reuse the existing future-print trigger handler, expiry, custody, cancellation,
   and feed/restart pause semantics. Display the waiting reason on chart markers.
4. Submit completed draft price corrections on release when auto-send is enabled.
   Preserve the reviewed quantity cap, busy/unknown guards, draft ownership, and
   manual keyboard submission. Show immediate-trigger warnings only for trusted
   recent trades.
5. Update subsystem guides, run the complete Windows CI-equivalent checklist and
   focused component/engine checks, review the diff, commit the task changes,
   integrate and push main, and verify hosted CI.

No new dependencies, generated-contract changes, broker routing changes, generic
retry loop, or live orders. Fresh account and execution admission gates remain.

## Rollout and rollback

Ship the engine and UI together; the existing WebSocket contract is sufficient.
Verify one accepted pair waits without a trade and activates once on a fresh
qualifying print using the simulated fixtures. Revert the scoped commit if
acceptance, quantity caps, or trigger ownership regress.
