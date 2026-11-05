# Chart Risk Entry position adds

Approved behavior: [specification](../../.scratch/chart-risk-position-adds/spec.md).

## Goal and scope

Add to a protected long position using the existing two-point gesture, a new
per-add risk budget, and a shared keep-or-raise stop. Support existing fixed and
trailing Chart Risk Entry protection. Do not adopt unrelated or broker-native
protection, allow multiple pending entries, or execute live verification.

## Current code and decisions

- `engine/internal/exec/risk_entry.go` rejects positions/working orders and owns
  one entry/stop pair. Keep the original entry as protection owner, link additions
  to it, and aggregate their confirmed fills for coverage and cleanup.
- `trailing_ownership.go` retires entry coverage after external reductions. Count
  all entries in a shared strategy once; additions must not look like reductions.
- `ChartRiskEntry.tsx` and `riskEntry.ts` own imperative preview and submission.
  Infer compatible protection, preserve its mode/cushion, and show combined size.
- Keep the existing stop until first fill; apply the selected stop once, durably,
  without resetting trailing progress. A stop that rose while waiting wins.

## Implementation

1. Extend execution metadata and its Go-owned WebSocket projection with the
   shared owner and proposed/applied add stop. Preserve historical event decoding.
2. Admit one add only against full, healthy managed coverage. Reuse the shared
   stop and acquire only the new BUY's market-data lease.
3. Update fill synchronization, retirement, cancel/failure/trigger/recovery paths,
   and marker amendments for shared ownership. Keep frozen exits and late-fill
   reservations intact.
4. Update chart validation, inherited sizing, preview, and focused interaction
   tests. Regenerate TypeScript from Go.
5. Update subsystem READMEs and the plan index; record validation in the tracker.

## Validation

Exercise fixed/trailing adds, first-fill timing, partial/canceled/rejected buys,
keep-or-raise races, single pending entry, partial sales, triggered cancellation,
late fills, recovery, and amendments. Run focused execution tests with race
detection, affected UI suites, typecheck/lint/build, generated-contract checking,
Markdown link validation, and `git diff --check`. Commit, integrate and push main,
then require a successful complete hosted CI run on that commit.

## Rollout and risks

Existing unlinked events retain their old behavior. Shared ownership metadata
is persisted before activation; never replay uncertain submissions. Downgrading
while a shared strategy is active is unsupported: close/cancel and reconcile
before rolling back. The main risks are double retirement, frozen-exit overfill,
and changing existing protection before fills; cover those through public command
and broker-event tests. Estimated risk still excludes fees and execution gaps.
