# Scanner Price Filter and Popover Dismissal

Status: Executed on 2026-09-21.

## Goal

Add optional inclusive minimum and maximum price filters to Scanner, using the
same session-aware provider price shown in its `Last` column. Also close the
Scanner settings popover on outside click or `Escape` without applying its
draft.

## Non-goals

- Do not add a market-data request, subscription, React price state, provider-side
  candidate filter, new settings component, or dependency.
- Do not reinterpret Scanner price as eTape's trade-condition-filtered
  Last-Eligible Price or change the existing `Last` column.
- Do not change the sticky-board lifecycle, ranking modes, sorting, Scanner Sync,
  or any other filter's semantics.
- Do not generalize outside-click handling across unrelated popovers.

## Current-code evidence

- [`engine/internal/scan/scan.go`](../../engine/internal/scan/scan.go) already
  normalizes each rank candidate into `rankItem.Last`, refreshes that value from
  the matching regular or extended-session snapshot, and routes every ranking
  mode through `rankRowsFiltered` before admission. Applying filters resets and
  rebuilds the board; admitted rows then remain sticky for the trading cycle.
- [`engine/internal/uihub/wsmsg/payloads.go`](../../engine/internal/uihub/wsmsg/payloads.go)
  owns `ScannerFilters` and already publishes nullable `ScannerRow.Last`.
  `SetScannerFilters` persists the contract as `scanner.filters.v2`; omitted Go
  numeric fields decode to zero, so additive zero/off bounds are backward
  compatible.
- [`ui/src/chrome/panels/ScannerPanel.tsx`](../../ui/src/chrome/panels/ScannerPanel.tsx)
  owns the filter draft, gear trigger, inline popover, Apply/Reset actions, and
  filter summary. The popover currently has no outside-click or keyboard
  dismissal.
- Existing popovers such as
  [`ExportTradesPopover.tsx`](../../ui/src/chrome/panels/ExportTradesPopover.tsx)
  use element refs plus a document `mousedown` listener and an `Escape` listener.
  Scanner needs refs for both its popover and gear trigger because the trigger
  can be portaled into the Panel Header while the popover remains in the panel.
- [`ui/src/chrome/panels/scannerFilter.ts`](../../ui/src/chrome/panels/scannerFilter.ts)
  owns the summary text and pure threshold helper used by its focused tests.

## Accepted behavior

### Price bounds

Add `minPrice` and `maxPrice` to the shared Scanner filters. Zero means off;
the UI renders an off value as a blank input. Reset Defaults clears both. Save
and restore the bounds with the existing `scanner.filters.v2` record, with no
new persistence key or migration.

Apply the bounds to gainers, losers, and most-active candidates in every Scanner
session. Compare the unrounded `rankItem.Last` value inclusively: an active
minimum requires `Last >= minPrice`, and an active maximum requires
`Last <= maxPrice`. If either bound is active, a missing, non-finite, or
non-positive price does not match. With both bounds off, preserve current row
eligibility.

Use the normalized Scanner price already shown in `Last`: premarket price during
premarket, regular current price during RTH, and the matching extended-session
price after hours or overnight. Do not push the bounds into OpenD's RTH-only
stock-filter request because that would produce inconsistent cross-mode and
cross-session behavior.

The bounds participate in the existing admission filter. Applying changed
bounds clears and rebuilds the board; later price movement does not evict an
already admitted sticky row before the normal reset.

Accept finite non-negative values. When both bounds are active, reject
`minPrice > maxPrice` at the engine trust boundary. In the UI, keep the popover
open, show a compact validation message, disable Apply, and send no command for
that invalid range. Exact-boundary values match.

Label the two native numeric inputs `price ≥` and `price ≤`, allow arbitrary
decimal precision, and do not round before sending or comparing. Show active
bounds in the existing summary, for example
`price ≥ $1.25 · price ≤ $20`.

### Popover dismissal

While the Scanner settings popover is open, a document `mousedown` outside both
the popover and gear trigger closes it. A click inside either element does not
invoke outside dismissal, so the portaled trigger continues to toggle normally.
`Escape` also closes it. Both paths discard the draft: reopening copies the
current applied filters, and only Apply sends `SetScannerFilters`.

Reuse the established local ref/listener pattern in `ScannerPanel`; do not add a
hook or shared popover abstraction for this single call site.

## File-level implementation

1. **Contract:** Add numeric `MinPrice` / `MaxPrice` (`minPrice` / `maxPrice`)
   fields to `ScannerFilters` in
   [`engine/internal/uihub/wsmsg/payloads.go`](../../engine/internal/uihub/wsmsg/payloads.go).
   Regenerate [`ui/src/gen/wsmsg.ts`](../../ui/src/gen/wsmsg.ts) from the Go owner;
   never edit generated TypeScript manually.
2. **Engine:** In
   [`engine/internal/scan/scan.go`](../../engine/internal/scan/scan.go), set zero
   defaults, validate finite non-negative bounds and their order, compare the
   normalized raw price in `rankRowsFiltered`, and include both fields in
   `sameFilters`. Keep publication of already admitted sticky rows unchanged.
3. **Persistence:** Extend
   [`engine/cmd/etape/scanner_filters_test.go`](../../engine/cmd/etape/scanner_filters_test.go)
   and
   [`engine/internal/uihub/commands_test.go`](../../engine/internal/uihub/commands_test.go)
   to prove v2 round trips, older v2 records default both bounds to zero, and
   invalid ranges are rejected. Production persistence code should remain
   unchanged unless those tests expose a gap.
4. **UI:** In
   [`ui/src/chrome/panels/ScannerPanel.tsx`](../../ui/src/chrome/panels/ScannerPanel.tsx),
   extend defaults/drafts with the two blank-when-off numeric inputs, inline
   range validation, Reset/Apply behavior, and refs/listeners for outside
   `mousedown` and `Escape`. Keep the implementation inside the existing panel.
5. **Summary helper:** Extend
   [`ui/src/chrome/panels/scannerFilter.ts`](../../ui/src/chrome/panels/scannerFilter.ts)
   and its focused tests with the two price bounds, raw inclusive comparison,
   unavailable-price behavior, and concise dollar summary text. Do not add a
   formatter abstraction unless an existing formatter already fits.
6. **Docs:** Update [`engine/internal/scan/README.md`](../../engine/internal/scan/README.md)
   and the Scanner filter list in [`README.md`](../../README.md) with the
   session-aware source, inclusive zero/off bounds, unavailable-price behavior,
   and sticky admission semantics. The dismissal interaction and additive wire
   fields do not need separate architecture documentation.

## Validation

Add the smallest focused checks that cover the new branches:

- Engine filter tests for min-only, max-only, range, both exact boundaries,
  below/above exclusion, zero/off, and invalid or unavailable prices.
- Engine validation/equality tests for negative, NaN, infinity, reversed ranges,
  and a filter change causing the normal board rebuild while later price movement
  preserves sticky membership.
- Persistence/command tests for fractional bounds, old v2 JSON, Reset defaults,
  valid round trips, and rejected invalid ranges.
- Scanner panel tests for blank defaults, arbitrary decimal submission, Reset,
  invalid-range message/disabled Apply, and both summary fragments.
- Popover tests proving inside clicks stay open, outside `mousedown` and `Escape`
  close without sending, reopening restores applied values, and the gear still
  toggles when rendered through the header portal.

Run proportional checks while implementing:

```text
cd engine && go test ./internal/scan ./internal/uihub ./cmd/etape
cd engine && mingw32-make gen-ts-check
cd ui && npm test -- ScannerPanel scannerFilter
cd ui && npm run typecheck
git diff --check
```

Because this changes the engine-owned wire contract and spans engine/UI, finish
with the current [Windows CI-equivalent checklist](../../README.md#ci-equivalent-validation-on-windows),
using [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) as the source
of truth. Report every skipped required check and why.

## Rollout, rollback, and risks

Ship engine and UI together through the normal build. Existing saved settings
leave both bounds off, and no database migration or service is introduced.
Rollback is a scoped revert; persisted additive JSON fields are ignored by the
older Go decoder.

The main correctness risk is filtering a different price from the one shown in
`Last`; keeping admission at the shared normalized `rankRowsFiltered` seam and
testing each session source prevents that drift. The main UI risk is treating
the portaled gear as an outside click; checking both refs prevents close/reopen
flicker. Provider ranks remain the candidate universe, so price bounds do not
discover symbols absent from the existing rankings.

The plan was executed with the implementation and validation described above.
