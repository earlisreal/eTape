# Scanner Dollar Turnover

Status: Executed on 2026-09-16.

## Goal

Add current-session Dollar Turnover to the existing Scanner candidates as a sortable column and optional minimum admission filter. Monitoring Scanner Sync follows the same sort.

Decision record: [draft spec](../../.scratch/scanner-turnover/spec.md). Terminology: [Dollar Turnover](../../CONTEXT.md).

## Non-goals

No turnover percentage, float rotation, cumulative-day metric, market-wide turnover discovery mode, new subscription, background worker, dependency, or order behavior change. Reuse existing rank requests and batched snapshots.

## Current-code evidence

- `engine/internal/scan/scan.go` decodes rank data into `rankItem`, merges candidates with the sticky board, refreshes snapshots, filters new admissions, and publishes complete rows. Rank and snapshot turnover fields are currently discarded.
- RTH gainers/losers use 3413; extended sessions use 3410/3411/3412. RTH most-active uses 3215 and already receives the common 3203 snapshot enrichment.
- [Moomoo snapshot documentation](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-market-snapshot.html) exposes turnover plus separate premarket, after-hours, and overnight turnover fields. Checked-in protobufs expose the corresponding nullable fields.
- The board retains rows across session transitions and clears when post-market begins. RTH bootstrap also fetches premarket candidates; those values must not be presented as RTH turnover.
- `engine/internal/uihub/wsmsg/payloads.go` owns row/filter contracts. `SetScannerFilters` persists `scanner.filters.v2`; restore uses Go decoding, so an omitted numeric field can default to zero.
- `ScannerPanel.tsx` saves sorting in panel settings; `scannerSync.ts` owns shared ranking; generic sorting already keeps nulls last in either direction.

## Accepted behavior

### Measurement and refresh

Use provider-reported dollar turnover for the current premarket, RTH, after-hours, or overnight session. Preserve valid zero. Reject absent, negative, NaN, or infinite source values; never calculate last price times volume.

Prefer a valid matching-session snapshot, then a valid matching-session rank result, then the last valid value from that same session instance. Failed or unusable refreshes do not erase that same-session fallback. Without any valid value, publish null. RTH most-active can obtain turnover entirely from its existing snapshot; do not add a 3215 field just for fallback.

Associate cached turnover with a session instance, not only a phase name: returning to RTH on another trading date must not resurrect yesterday's value. Use the existing ET session calendar and timing model. Invalidate before merging/filtering/publishing after a transition, including when a rank request fails. A premarket bootstrap candidate during RTH needs RTH data before its turnover is available. Overnight crossing midnight follows the existing actual session boundaries, not an arbitrary midnight reset. Closed periods retain the last published board under existing scanner scheduling.

Do not change other Scanner fields' fallback behavior as part of this task. Provider timestamps, when available, must be checked against the expected session to avoid accepting an old session block as fresh.

### Admission and persistence

Add `minTurnover` in raw USD to shared Scanner filters. Zero disables it. A positive threshold admits only candidates with available turnover greater than or equal to the threshold; compare raw values. Existing admitted rows remain sticky if turnover later becomes unavailable or falls below the threshold. Applying a changed threshold clears/rebuilds the board through the existing filter path.

Use `Min turnover ($M)` with decimal input, converting millions to raw dollars at the UI boundary. Validate finite non-negative input at the engine boundary. Save via the existing v2 filter record; missing fields from older settings mean zero. Reset Defaults disables it. Keep the existing filter scope across Scanner panels.

### Display and sorting

Place `Turnover` immediately after `Vol`. Tooltip: `Dollar value traded in the current session.` Display compact USD with up to one decimal and no trailing `.0`: `$850K`, `$12.5M`, `$1.2B`; below $1K show dollars with up to two decimals. Handle suffix-rounding boundaries consistently. Zero is `$0`; unavailable is `—`.

Sort numeric, unrounded values with nulls last in either direction and existing tie behavior. Preserve existing default sorts and mode-change behavior. Persist the selected turnover sort in panel settings; Scanner Sync consumes the same accessor. Show an active threshold in the existing filter summary.

## File-level implementation

1. **Contract:** Add nullable `turnover` to `ScannerRow` and numeric `minTurnover` to `ScannerFilters` in `engine/internal/uihub/wsmsg/payloads.go`. Regenerate `ui/src/gen/wsmsg.ts` with the existing generator; never hand-edit it.
2. **Engine:** In `engine/internal/scan/scan.go`, carry validated rank/snapshot turnover and minimal session provenance, resolve precedence, clear cross-session fallback, project rows, validate/compare filters, and include the new threshold in filter equality. Keep admission separate from sticky-row publication and preserve REL VOL pool-warming behavior.
3. **Persistence:** Extend existing tests in `engine/cmd/etape/scanner_filters_test.go` and `engine/internal/uihub/commands_test.go` for round trips and old records defaulting to zero. Change restore/command code only if tests show the additive field needs handling.
4. **UI:** Normalize omitted turnover to null in `ui/src/data/ScannerStore.ts`. Extend `ui/src/chrome/scannerSync.ts`, `ui/src/chrome/panels/ScannerPanel.tsx`, and `ui/src/chrome/panels/scannerFilter.ts` for sorting, display, default filters, decimal input, and summary. Reuse a suitable existing money formatter or add a small helper in `ui/src/chrome/format.ts`. Update typed fixtures and empty-table column spans.
5. **Synthetic data:** Inspect `engine/internal/synth/requester.go` and existing generator aggregates. Replace placeholder turnover only where an existing matching-session aggregate supplies the real synthetic value; use deterministic protocol fixtures for boundary tests. Do not invent production estimates or a new simulator model.
6. **Docs:** Update `engine/internal/scan/README.md`, `ui/src/chrome/README.md`, root `README.md`, and `docs/external-apis.md` where needed for turnover sources, session boundaries, admission semantics, saved threshold, and Scanner Sync. Retain the glossary addition. No ADR is needed for this reversible field addition.

## Validation

Extend existing focused tests, without adding a test framework:

- Decode correct fields for all four sessions and zero/null/negative/non-finite cases; snapshot precedence and rank fallback; most-active snapshot enrichment.
- Retain same-session values on refresh failure; clear across phase/date changes, including failed first rank poll, missing snapshot blocks, overnight midnight, and premarket bootstrap during RTH.
- Prove exact-threshold inclusion, below-threshold/unavailable exclusion, off-at-zero, sticky retention, rebuild after filter changes, and unchanged REL VOL pool warming.
- Persist/restore a fractional-million threshold as raw dollars; old settings and Reset Defaults disable it; reject invalid threshold commands.
- Check column placement, tooltip, compact formatting and suffix boundaries, null-last sorting both ways, per-panel sort persistence, Scanner Sync order, missing older-engine fields, and filter command/summary.

Then run the current [Windows CI-equivalent checklist](../../README.md#ci-equivalent-validation-on-windows), with [.github/workflows/ci.yml](../../.github/workflows/ci.yml) authoritative: full Go tests, short race tests, vet, pinned golangci-lint v2.12.2, generated-contract check, UI dependency install/lint/tests/build (includes typecheck), and `git diff --check`. Also build the engine and run a proportional Scanner UI smoke check; use existing E2E coverage if it can exercise the changed flow. Check Go LF endings and generated drift. Report every skipped required check and why; hosted CI must pass after implementation is pushed.

## Rollout, rollback, and risks

Deploy engine and UI together through the normal build. Existing saved settings leave the new filter off. Older-engine rows render unavailable in the new UI; an older engine cannot enforce the new filter. Roll back with a scoped revert; no database schema migration or new runtime service is introduced.

The main correctness risk is stale turnover crossing session boundaries while the board remains sticky. Session provenance and transition tests are required. Provider availability can leave values null, which intentionally prevents new admissions under a positive filter. A displayed compact value can round to the threshold while its raw value is lower; filtering deliberately uses raw dollars. Ranking only covers the existing candidate set.

Keep spec and plan uncommitted while under review. On confirmed implementation, complete validation and README updates, then commit the executed plan/spec with resulting changes and push as required by the repository agent guide.
