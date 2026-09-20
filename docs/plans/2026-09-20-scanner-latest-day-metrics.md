# Scanner latest-day metrics

Status: Implemented on 2026-09-20. Daily snapshot metrics and raw Alpaca daily REL VOL are live in the Scanner path.

## Goal and confirmed decisions

Use Moomoo base snapshot `volume` and `turnover` directly, independent of the Scanner session. Do not sum premarket, after-hours, or overnight fields. The user accepts provider daily totals and that premarket may still display the previous trading day's total. This supersedes the earlier custom close-to-close cycle design.

REL VOL = latest snapshot daily volume / arithmetic mean raw Alpaca SIP daily volume over up to 50 preceding completed trading days. Exclude the numerator's represented date. Remove same-time-of-day adjustment entirely.

- Use genuinely available shorter listing history and divide by its actual count; missing requests or unexplained gaps are not shorter listing history.
- Keep raw volumes across splits; no split normalization. Splits may temporarily distort the ratio.
- Keep saved numeric thresholds unchanged. Apply the new metrics consistently to admission filters, Most active sorting, and Scanner Sync.
- Preserve candidate discovery, sticky membership, board resets, pool limits, price/change calculations, and default sorting.
- Valid zero volume/turnover remains zero. Unavailable metrics display `—`; empty history or zero mean makes REL VOL unavailable. Retain valid same-date values on transient refresh failures, without presenting stale data as a new day.
- Use existing batched Moomoo snapshots and Alpaca SIP daily-history infrastructure. No Scanner OpenD history requests, BOATS feed, session-rank sweeps, or session-component persistence.

## Current-code evidence

- `engine/internal/scan/scan.go` keeps rank volume/turnover for discovery and refreshes the published daily metrics from batched snapshot base fields. Snapshot observation dates must match the represented metric date; mismatches stay unavailable. REL VOL consumes the same daily snapshot volume and the raw Alpaca daily baseline. Filtering, row projection, Most active ordering, and asynchronous history warming remain here.
- `engine/internal/scan/relative_volume.go` now builds a compact up-to-50-day full-day mean profile.
- `engine/cmd/etape/main.go` wires `scannerRELVolFetcher` to the raw Alpaca SIP daily entry point.
- `engine/internal/hist/alpaca/alpaca.go` keeps chart `DailyBars` adjusted/capped and adds a raw Scanner daily entry point with pagination exhaustion errors.
- Go `engine/internal/uihub/wsmsg/payloads.go` owns the contract; volume is nullable so missing daily data is distinct from zero. UI stores and Scanner Sync consume the generated contract.

## Verified provider evidence

[Moomoo snapshots](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-market-snapshot.html) expose base totals and separate session fields. [Alpaca aggregation rules](https://docs.alpaca.markets/us/docs/market-data-faq) allow eligible extended-hours trades to update daily volume without changing daily OHLC; daily buckets use New York dates. Do not label daily volume strictly regular-session-only or promise identical provider aggregation.

Read-only verification on 2026-09-20 used AAPL for 2026-09-18:

| Metric | Moomoo snapshot | Moomoo unadjusted K_DAY | Alpaca raw SIP 1Day |
| --- | ---: | ---: | ---: |
| Volume | 86,588,203 | 86,588,203 | 86,726,840 |
| Dollar turnover | 29,099,784,713 | 29,099,784,713 | Not requested |

Snapshot update time was `2026-09-18 20:02:15.560` ET. Moomoo matched exactly; Alpaca differed by 138,637 shares, approximately 0.16%. This is one symbol/date, not universal equivalence or proof of intraday rollover behavior. Verification used one OpenD history-symbol slot, leaving 299. Production implementation adds no historical OpenD requests. No orders or production configuration changed.

## Daily-date handling

Track the daily date represented by snapshot totals internally. It controls the historical window and cache key, independently of wall-clock midnight, board-reset time, active phase, and the subscription pool's 20:00 key.

Use the existing ET exchange calendar and validated snapshot observation metadata. An updated extended-session price timestamp alone does not prove base daily volume has advanced. Before regular trading, expect the prior trading day's base totals; adopt the current date once a trustworthy current-day base total is established. Do not infer rollover merely from increasing/decreasing volume: corrections and equal totals are possible.

Examples:

- Tuesday premarket with Monday's base total: exclude Monday from the denominator.
- Tuesday RTH with Tuesday's base total: include Monday and exclude Tuesday.
- Friday after-hours/weekend with Friday's base total: exclude Friday. Board lifecycle stays independent.

On date change, invalidate mismatched fallback and recompute the baseline. Unknown attribution at cold start yields unavailable. Retain fallback on refresh failure only while it still belongs to the expected represented date. Keep existing closed-period polling behavior.

Read-only verification confirmed that the snapshot base totals match Moomoo unadjusted K_DAY for the checked symbol/date. Runtime snapshot dates are checked before publication; a missing or mismatched observation leaves daily metrics unavailable rather than relabeling stale data. No historical OpenD quota is used by the Scanner path.

## Historical baseline

Request raw SIP `1Day` bars for 50 exchange trading dates strictly preceding the numerator date. Filter by ET date and exclude current/newer buckets, even if an inclusive endpoint returns them. Fully page responses; reject truncation, duplicates, invalid dates/volumes, and unexplained gaps. Use overflow-safe arithmetic.

Use trustworthy listing metadata to distinguish legitimate short listing history from missing leading data. Returned zero-volume days are valid samples. An unexplained missing date or uncertain listing boundary stays unavailable and retryable; do not substitute older dates or assume zero.

Reuse the existing worker, symbol cap, retry delays, credentials, HTTP client, rate limiter, and publication path. Cache a compact mean/sample count by symbol and numerator date. Remove minute arrays, same-time phase restrictions, and midnight-only cache assumptions. Reject stale worker completions after the numerator date changes. Preserve pool warming that ignores the REL VOL threshold while history loads.

Add the smallest raw-daily entry point on the existing Alpaca client; preserve chart `DailyBars` adjustments. Request complete historical buckets within the provider's recency allowance. A delayed/partial required bucket remains unavailable until complete. No IEX or OpenD-history fallback.

## File-level implementation steps

1. **Source:** update `engine/internal/hist/alpaca/alpaca.go` and tests for raw daily requests, complete date ranges, and pagination completeness. Wire the Scanner fetcher in `engine/cmd/etape/main.go` and its tests. Preserve chart requests and existing historical credential resolution.
2. **Formula:** replace minute profiles in `engine/internal/scan/relative_volume.go`; update `relative_volume_test.go` for the full-day mean, short listings, and unavailable cases.
3. **Scanner:** update `engine/internal/scan/scan.go` and `scan_test.go` to resolve base volume/turnover/date before admission and publication. Remove extended-session volume overwrites and cumulative-session REL VOL inputs. Replace phase-based turnover fallback with daily provenance. Keep rank discovery and price/change logic; rank session volume must never masquerade as daily volume. Preserve SSR and short-interest enrichment.
4. **Contract:** make `ScannerRow.volume` nullable in `engine/internal/uihub/wsmsg/payloads.go` to distinguish missing from zero. Regenerate `ui/src/gen/wsmsg.ts`; never edit generated sources manually. Keep filter settings and persistence unchanged.
5. **UI:** update `ui/src/data/ScannerStore.ts`, `ui/src/chrome/scannerSync.ts`, `ui/src/chrome/panels/ScannerPanel.tsx`, `scannerFilter.ts`, and affected tests/fixtures. Render missing volume as `—`, sort nulls last both ways, exclude unavailable metrics from positive minima, and explain latest daily totals/full-day REL VOL in tooltips. Reuse formatters; no metric calculation in React state.
6. **Synthetic data:** inspect `engine/internal/synth/requester.go`; use existing aggregates for base daily fields and deterministic fixtures for history/date boundaries. Do not build a new simulator model.
7. **Docs:** update the Scanner README, relevant root/engine/UI guides, `ui/src/chrome/README.md`, `docs/external-apis.md`, and `CONTEXT.md` for final sources, formula, availability, and premarket behavior. The glossary records the agreed target now; runtime guides change with implementation.

## Validation

- Verify base fields in every phase; large session-specific totals are never added/substituted. Zero is valid; missing/negative/non-finite values are unavailable.
- Formula example: 500,000 shares / 2,000,000 mean shares = 0.25x at every time of day. Use exactly 50 dates when available and actual count for confirmed short listings.
- Cover numerator-date exclusion, premarket prior-day values, open rollover, weekend/holiday/early-close/DST behavior, restart, failures, same-date corrections, and stale worker results.
- Cover short listings, missing history, zero mean, duplicate/truncated responses, raw adjustment, and complete daily request bounds. Assert no BOATS or Scanner OpenD history requests.
- Verify filter admission/pool warming, sticky rows, Most active ordering, saved thresholds, null-last sorting, and Scanner Sync.
- Run focused Go Scanner/Alpaca/wiring tests and affected UI tests during implementation, then the full [Windows CI-equivalent checklist](../../README.md#ci-equivalent-validation-on-windows). [.github/workflows/ci.yml](../../.github/workflows/ci.yml) is authoritative: Go tests/race/vet/lint, generated-contract check, UI install/lint/tests/build, diff and line-ending checks. Hosted CI must pass. Report every skipped required check and reason; run E2E proportionally for changed Scanner interactions.
- Validation completed during implementation: focused and full Go tests, `go vet ./...`, engine build, generated-contract check, focused UI tests, full UI tests, UI typecheck, and `git diff --check`. The remaining Windows CI-equivalent checks are recorded in the handoff if they cannot run in this environment.

## Non-goals

No same-time mode, custom close-to-close totals, separately added overnight volume, BOATS integration, new OpenD history quota use, rank sweeps, chart adjustment changes, new dependencies, persistent history store, discovery redesign, or order changes. No ADR is needed for this reversible metric/source change.

## Rollout, rollback, and risks

Ship the daily metrics and formula together, keeping saved filters. A positive REL VOL minimum becomes more restrictive early in the day because its denominator is a full-day average. Regenerate the nullable-volume contract with matching engine/UI changes. Roll back the scoped implementation and docs together if regressions occur.

Risks: daily-date attribution at open, stale data relabeled as new, provider aggregation differences, adjusted-volume leakage, and short-history misclassification. The AAPL observation supports approximate comparability, not universal equality.

Implementation and validation completed in this task. The plan is committed with the scoped code and documentation changes.
