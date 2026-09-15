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

## Polling cadence follow-up

The local configuration was checked selectively: `premarket_ms=2000` and
`rth_ms=3000`. No configuration was modified. In `scan.go:276`, the base ticker
uses PremarketMs and checks elapsed time against the session interval. With
those settings, scheduled RTH polls normally land four seconds apart, not
three; slow requests and enrichment/filter pokes can alter actual timing.
Pokes invoke a full poll independently of the ticker's interval gate.

The [polling probe](probe_polling.py) compares 16 rank-plus-snapshot cycles at
two-second start intervals with 61 at one-second intervals. It holds the initial
merged, OTC-filtered candidate set fixed while refreshing the current premarket
top-100 ranking each iteration. It stops on API failure, performs no retries,
subscriptions/history/trading calls, and does not change the running Scanner.
This measures provider round trips and sampled prices, not complete Go engine
processing, React rendering, or end-to-end alert latency.

Live premarket result, approximately 06:42–06:43 ET on 2026-09-15:
each snapshot requested the same 243 merged symbols.

| Interval | Polls | Median cycle | p95 cycle | Errors / interval overruns |
| --- | --- | --- | --- | --- |
| 2 seconds | 16 over 30.004s | 156.8 ms | 192.0 ms | 0 / 0 |
| 1 second | 61 over 60.017s | 176.1 ms | 193.1 ms | 0 / 0 |

Observed starts were within 0.7 ms of the requested intervals. During the
one-second stage, 58 of 60 refresh comparisons contained price changes. Its
274 symbol-price transitions compare with 199 when the same sampled stream is
downsampled to two seconds. The intervening samples contained 132 changed
symbol prices compared with the previous two-second boundary; 12 had reverted
by the next boundary. Those are observed samples, not a count of all underlying
trades or proof that a specific alert threshold would have crossed.

This shows that polling faster sometimes captures additional price information;
it was not merely receiving identical cached responses. The two live stages
occurred sequentially, so their total transition counts should not be compared
as a controlled market-speed benchmark. [Measured output](polling-probe.json).
All live completeness assertions and saved-output checks passed. No provider
failures were induced. This short premarket test does not validate one-second
polling during RTH or the engine's full shared workload.

The user confirmed eTape was running during this test. The separate SDK probe
therefore added traffic alongside the app, rather than replacing its polling.
The successful result is evidence of short-term coexistence with that workload;
the app's actual request counts, retries, latency and UI responsiveness were
not instrumented. Its existing traffic may also have warmed provider/OpenD
caches. Do not treat these timings as an isolated idle-service benchmark or
infer exact remaining shared request capacity from them.

At one snapshot batch per cycle, approximate sustained Scanner snapshot demand:

| Interval | Requests / 30s | Implication |
| --- | --- | --- |
| 2 seconds | 15 | Existing configured extended-hours cadence |
| 1 second | 30 | Leaves nominal room for other callers, subject to shared pacing |
| 500 ms | 60 | Uses the full documented ceiling before other callers or retries |

Two batches per one-second cycle would also use the full nominal ceiling.
The one-second option therefore depends on the shared pacing and bounded retry
requirements above, with slower refresh as batch count or shared demand grows.
500 ms was not tested live because it has no nominal request headroom. Fixing
the timer's session-interval rounding and accounting for enrichment pokes are
also prerequisites to promising an accurate configured cadence.
