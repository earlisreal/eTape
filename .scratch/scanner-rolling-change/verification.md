# Scanner candidate capacity verification

Date: 2026-09-15, approximately 06:29 ET / 18:29 Manila.

## Conclusion

100 candidates per rank and a deduplicated same-cycle startup merge are feasible
on the observed healthy OpenD connection. This does not establish unconditional
safety for failures, growing retained boards, or every market session. Do not
implement this as only changing counts and concatenating historical rank rows.

## Live evidence

The read-only [probe](probe_rank_capacity.py) requests four rankings in each
direction, filters confirmed US_PINK symbols using static information (as the
engine does), then snapshots merged symbol sets. No subscriptions, history
requests, account queries, order operations or engine configuration changes.
Compact measured output: [capacity-probe.json](capacity-probe.json).

| Check | Result |
| --- | --- |
| After-hours, overnight, premarket and RTH; gainers and losers | All eight returned 100 unique US symbols |
| Rank request duration | 41–109 ms |
| Unique symbols across all eight lists | 641; 56 confirmed OTC/Pink excluded |
| Premarket startup gainers: after-hours + overnight + premarket | 241 symbols; all 241 snapshots returned in 161 ms |
| Premarket startup losers: same three sessions | 216 symbols; all 216 snapshots returned in 108 ms |
| Four-session capacity-only merge, gainers | 315 snapshots in 158 ms |
| Four-session capacity-only merge, losers | 291 snapshots in 137 ms |
| Two repeat snapshots of the 241-symbol startup set | All returned; 109/116 ms; 9/6 premarket prices changed |
| API requests in successful probe | 8 rank, 2 static, 6 snapshot; no API errors |

The four-session checks measure batch capacity only: RTH was inactive, so they
do not validate the date or freshness of a real RTH startup merge. Snapshot
latencies are individual round trips, not end-to-end startup times or a stress
benchmark. Measurements are from one connection at one time.

## Data correctness findings

- Of 241 merged gainers, 187 had a positive premarket price and 54 did not.
  Of 216 merged losers, 181 had a positive premarket price and 35 did not.
- Snapshot update times ranged back to 2026-09-14 19:54:59 ET. A successful
  batch does not mean every symbol has current-session data. Positive prices
  alone likewise do not prove a recent trade or independent session timestamp.
- Previously verified after-hours retention still permits finding prior movers,
  but discovery and current-price eligibility must remain separate.
- Existing Scanner failures/omissions preserve old values; those values cannot
  be silently added as fresh rolling samples or used to fabricate crossings.

## Capacity and failure-path findings

The official [snapshot documentation](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-market-snapshot.html)
allows 400 symbols/request and 60 requests/30 seconds. The
[premarket rank documentation](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-us-pre-market-rank.html)
allows 1–200 symbols/request. A three-session startup set of 100 each has at
most 300 unique symbols; a four-session set has at most 400, before accumulated
rows or other discovery modes are added.

- One healthy snapshot every two seconds is about 15 requests/30 seconds.
  Fetch prior-session ranks once for bootstrap, not on every refresh.
- The shared OpenD snapshot transport has no aggregate rate limiter covering
  Scanner, Watchlist, Stock Info and symbol validation. Cadence is not a hard
  guarantee against the documented limit.
- Scanner allows eight snapshot requests per poll. At a two-second interval,
  that ceiling permits 120 requests/30 seconds before other callers. It is a
  per-poll retry budget, not a sliding-window rate limit.
- Scanner, Watchlist and Stock Info split application errors into smaller
  batches. Watchlist and Stock Info have no equivalent request budget; an
  all-failing 100-symbol binary split can issue 199 requests. Global failures
  such as throttling must not be treated as evidence of a bad individual code.
- The sticky board can grow beyond a single batch. Candidate count is not a
  retained-board cap; scheduling must avoid starving symbols when refresh work
  exceeds a poll's budget.

Before shipping, implementation must gate on valid current-session observations,
pace aggregate snapshot work, bound retries/back off on global errors, and keep
current-session discovery working if an earlier-session bootstrap fails. These
conditions are supported by code review; failure storms were not induced live.

## Checks and limits

- Live probe assertions passed for row counts, unique symbols, response
  completeness, snapshot batch size and repeated snapshot completeness.
- Existing focused engine tests passed:

  `go test ./internal/scan -run 'Test(ResolveFloats|ResolveExch|PollOnceDropsOTC|RTHBootstrap|RTHFilterReset|AccumulatedRows|SnapshotRefresh|BoardSurvives)' -count=1`

  Go reported an unrelated telemetry-token permission warning; test exit was 0.
- `git diff --check` passed.
- Full Windows CI-equivalent checks were not run: this task adds a diagnostic
  probe and evidence, with no production engine/UI/contracts/build changes.
  They remain required for implementing the Scanner feature.
- Not tested: sustained RTH load, concurrent retry storms, entitlement failure
  recovery live, holidays/early closes, or an all-day accumulated board.
