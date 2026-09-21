# Scanner Session Volume and Column Settings

Status: Implemented on 2026-09-21; shared design confirmed on 2026-09-21.

## Goal

Add provider-reported current-session share volume to Scanner as a distinct,
sortable `SESSION VOL` column and optional minimum admission filter. Preserve the
existing latest daily `VOL` metric. Also let each Scanner Panel hide and reorder
metric columns while keeping `SYMBOL` visible and first. Hidden columns remain
filterable.

## Non-goals

- Do not sum volume across sessions, derive volume from bars or ticks, add a
  market-data request/subscription, or change the daily volume used by `VOL`,
  REL VOL, Most active, or its default sort.
- Do not add a provider-side volume filter, discovery mode, TOML option, storage
  version, database migration, dependency, or React market-data state.
- Do not build a shared table-column framework, drag-and-drop interaction,
  column resizing, saved widths, or column customization for another panel.
- Do not let column visibility disable, clear, or otherwise alter filters.
- Do not change the sticky-board lifecycle, ranking modes, session boundaries,
  Scanner Sync ownership, or order behavior.

## Current-code evidence

- [`engine/internal/scan/scan.go`](../../engine/internal/scan/scan.go) already
  decodes rank volume for pre-market, RTH, after-hours, and overnight candidate
  discovery. Every poll merges the sticky board, refreshes it with batched OpenD
  3203 snapshots, filters admissions through `rankRowsFiltered`, and publishes
  rows. The published `ScannerRow.Volume` is deliberately replaced by the
  snapshot's base daily volume.
- The checked-in OpenD snapshot contract already exposes nullable base
  `Volume`, `PreMarket.Volume`, `AfterMarket.Volume`, and `Overnight.Volume`.
  Snapshot enrichment currently selects matching-session price and turnover but
  does not retain matching-session volume separately.
- [`engine/internal/uihub/wsmsg/payloads.go`](../../engine/internal/uihub/wsmsg/payloads.go)
  owns `ScannerRow` and `ScannerFilters`; generated
  [`ui/src/gen/wsmsg.ts`](../../ui/src/gen/wsmsg.ts) must not be edited manually.
  `SetScannerFilters` saves the contract as `scanner.filters.v2`. Additive Go
  numeric fields decode to zero, so a new zero/off threshold does not require a
  persistence migration.
- [`ui/src/chrome/panels/ScannerPanel.tsx`](../../ui/src/chrome/panels/ScannerPanel.tsx)
  owns the fixed column list, cells, filter draft, filter summary, and per-panel
  sort persistence. [`ui/src/chrome/scannerSync.ts`](../../ui/src/chrome/scannerSync.ts)
  applies that persisted sort to both the table and Scanner Sync.
- [`ui/src/chrome/AppShell.tsx`](../../ui/src/chrome/AppShell.tsx) merges settings
  patches into one panel and persists the workspace. Layout export in
  [`ui/src/chrome/backup.ts`](../../ui/src/chrome/backup.ts) already preserves
  non-symbol panel settings, so Scanner column settings require no separate
  storage or export path.
- No existing table supports hiding or reordering columns. The reusable
  `ResizableColumns` helper only handles widths and is outside this feature's
  scope.

## Accepted behavior

### Session Volume source and lifetime

`Session Volume` is the provider-reported raw share count for the active Scanner
session:

| Scanner session | OpenD snapshot source |
| --- | --- |
| Pre-market | `PreMarket.Volume` |
| RTH | base `Volume` |
| After-hours | `AfterMarket.Volume` |
| Overnight | `Overnight.Volume` |

Never add these fields together. Preserve a valid zero. Treat an absent or
negative value as unavailable; publish unavailable as `null` and render it as
`—`.

Reuse matching-session volume already present in rank responses as the initial
value, then prefer a valid matching-session value from the existing batched
snapshot. Retain the last valid value only within the same session instance when
a refresh fails or omits the field. Track both phase and the existing session
pool-day identity so a sticky row cannot carry pre-market volume into RTH or a
prior day's same-named session. A pre-market bootstrap candidate discovered
during RTH has unavailable RTH Session Volume until its RTH snapshot supplies
one. Closed periods must not relabel a stale session value as current.

Keep rank volume's current discovery role and daily snapshot volume's current
`VOL`/REL VOL/Most-active role. Session Volume is an additional projected row
metric, not a reinterpretation of either.

### Admission filter and persistence

Add `minSessionVolume` in raw shares to shared Scanner filters. Zero disables
the filter. A positive threshold admits a candidate only when current Session
Volume is available and greater than or equal to the threshold. Compare raw
values, including the exact boundary.

Apply the threshold to every ranking mode and active Scanner session at the
existing shared admission seam. Applying changed filters clears and rebuilds
the board; an admitted row remains sticky if its Session Volume subsequently
falls, becomes unavailable, or the next session begins. New admissions in the
new session use only that session's value.

Validate the threshold as finite and non-negative at the engine boundary and
include it in filter equality. Save it in `scanner.filters.v2`; older records
default it to zero. Do not add a config-file default. Reset Defaults sets it to
zero. Older records without `sessionVolumeUnit` default that display preference
to K. Reset Defaults sets both volume-unit selectors to K.

In the filter popover, add `session vol ≥` with its own K/M selector. Both daily
and session thresholds remain raw shares; persist independent `volumeUnit` and
`sessionVolumeUnit` display preferences. The filter summary includes an active
Session Volume threshold even when `SESSION VOL` is hidden.

### Display, sorting, and Scanner Sync

The default visible order is:

```text
SYMBOL · % · LAST · FLOAT · REL VOL · VOL · SESSION VOL · TURNOVER · SHORT INT
```

Format Session Volume with the existing compact-share formatter. Its tooltip
states that it is provider-reported volume for the current Scanner session.
Sort the unrounded nullable value with the existing null-last behavior. Preserve
all existing default sorts; only an explicit `SESSION VOL` header click selects
it. Persist that sort like the other Scanner columns, and let Scanner Sync
consume the same accessor and ordering.

### Per-panel column settings

Add a dedicated `Columns` popover in the Scanner header. It lists every metric
column with a visibility checkbox and accessible Move Up / Move Down buttons,
plus Reset. Changes apply immediately through the existing per-panel
`onConfigChange` path. Move buttons disable at their applicable edges. Reuse the
Scanner popover's local outside-click and `Escape` behavior; do not introduce a
shared popover abstraction.

`SYMBOL` is always visible and first and is absent from the hide/reorder
controls. Every metric column, including `SESSION VOL`, may be hidden and moved.
All metrics may be hidden simultaneously because the fixed symbol column still
leaves a usable row target.

Store the complete metric order and an explicit hidden-ID list under one
Scanner-specific panel setting. On read, accept only unique known metric IDs,
drop unknown IDs, append missing known IDs in default order, and ignore invalid
hidden IDs. This keeps old layouts valid and makes newly introduced columns
visible by default without confusing an omitted future ID with a deliberately
hidden one. Reset restores the accepted default order and visibility.

Rendering uses the validated visible order for headers, cells, and the empty
state's `colSpan`. Column settings affect presentation only: filter controls and
active filter-summary fragments remain available for hidden fields.

If the active sort column becomes hidden, immediately select the ranking mode's
default sort when its column remains visible; otherwise select `SYMBOL`
ascending. Persist the fallback so Scanner Sync is never controlled by an
invisible sort.

## File-level implementation

1. **Wire contract:** In
   [`engine/internal/uihub/wsmsg/payloads.go`](../../engine/internal/uihub/wsmsg/payloads.go),
   add nullable `sessionVolume` to `ScannerRow` and numeric
   `minSessionVolume` to `ScannerFilters`. Regenerate
   [`ui/src/gen/wsmsg.ts`](../../ui/src/gen/wsmsg.ts) from the Go owner.
2. **Engine metric:** In
   [`engine/internal/scan/scan.go`](../../engine/internal/scan/scan.go), carry a
   nullable Session Volume with phase/pool-day provenance, validate rank and
   snapshot sources, prefer the matching snapshot, retain only same-session
   fallback, invalidate transitions before filtering/publication, and project
   the nullable row field. Reuse the existing session helpers and snapshot
   request; add no worker or cache subsystem.
3. **Engine filter:** Extend defaults, validation, `sameFilters`, and
   `rankRowsFiltered` with the inclusive minimum. Preserve sticky publication
   and REL VOL pool warming. Add focused cases to
   [`engine/internal/scan/scan_test.go`](../../engine/internal/scan/scan_test.go)
   rather than creating a new test package.
4. **Persistence and commands:** Extend
   [`engine/cmd/etape/scanner_filters_test.go`](../../engine/cmd/etape/scanner_filters_test.go)
   and
   [`engine/internal/uihub/commands_test.go`](../../engine/internal/uihub/commands_test.go)
   for additive v2 restore/save and command validation. Production persistence
   code should remain unchanged unless these tests expose a gap.
5. **UI data and filtering:** Normalize omitted `sessionVolume` to `null` in
   [`ui/src/data/ScannerStore.ts`](../../ui/src/data/ScannerStore.ts) for mixed
   or older fixtures. Extend
   [`ui/src/chrome/panels/scannerFilter.ts`](../../ui/src/chrome/panels/scannerFilter.ts)
   and its existing tests with unavailable/exact-boundary filtering and the
   `session vol` summary fragment.
6. **UI sorting:** Add the Session Volume accessor in
   [`ui/src/chrome/scannerSync.ts`](../../ui/src/chrome/scannerSync.ts) and cover
   null-last manual sorting and Scanner Sync ordering in its focused tests. Do
   not change `scannerModeSort`.
7. **Scanner presentation:** In
   [`ui/src/chrome/panels/ScannerPanel.tsx`](../../ui/src/chrome/panels/ScannerPanel.tsx),
   add the filter input, default column metadata, field-specific cell renderer,
   Columns trigger/popover, validated settings reader, immediate hide/move/reset
   updates, dynamic cells/`colSpan`, and hidden-sort fallback. Keep the small
   settings helpers local unless focused tests demonstrate that extraction is
   necessary.
8. **Fixtures and mocks:** Update typed Scanner rows in UI fixtures/mock-engine
   data and existing Go synthetic/protocol fixtures with deterministic Session
   Volume values. Do not expand the simulator model beyond fields needed by the
   existing snapshot response.
9. **Documentation:** Update [`CONTEXT.md`](../../CONTEXT.md) with the accepted
   `Session Volume` term; update
   [`engine/internal/scan/README.md`](../../engine/internal/scan/README.md),
   [`ui/src/chrome/README.md`](../../ui/src/chrome/README.md),
   [`docs/external-apis.md`](../external-apis.md), and the Scanner feature list
   in [`README.md`](../../README.md) for provider sources, session provenance,
   filter semantics, Scanner Sync sorting, and per-panel column settings. Preserve
   any unrelated working-tree edits in those files.

## Validation

Add the smallest focused checks that cover each new branch:

- Decode and select the correct rank/snapshot volume in all four sessions;
  preserve zero; reject absent/negative values; prefer a valid snapshot; retain
  only same-session fallback.
- Clear stale values on pre-market-to-RTH, RTH-to-after-hours, overnight/date,
  weekend/closed, restart, and pool-day transitions, including RTH pre-market
  bootstrap and failed/omitted snapshot cases.
- Prove filter off-at-zero, exact-boundary inclusion, below/unavailable
  exclusion, every mode/session, normal rebuild on filter change, and sticky
  retention after admission.
- Prove negative/NaN/infinite command rejection, `sameFilters` detection, old v2
  JSON defaulting to zero, raw-share round trip, and Reset Defaults.
- UI checks cover independent daily/session K/M conversion, filter submission and
  summary while the column is hidden, nullable rendering, compact formatting,
  manual sorting, null-last behavior, and Scanner Sync order.
- Column checks cover the default order, fixed `SYMBOL`, hide/show, Move Up/Down
  edge states, all-metrics-hidden rendering, Reset, immediate settings patches,
  malformed/duplicate/unknown saved IDs, automatic inclusion of missing known
  columns, dynamic `colSpan`, and active hidden-sort fallback.
- Confirm workspace save and layout export/import retain one Scanner's settings
  without changing another Scanner Panel. Reuse existing persistence/export
  tests rather than adding an end-to-end framework.

Run proportional checks during implementation:

```text
cd engine && go test ./internal/scan ./internal/uihub ./cmd/etape
cd engine && mingw32-make gen-ts-check
cd ui && npm test -- ScannerPanel scannerFilter scannerSync
cd ui && npm run typecheck
git diff --check
```

Because this changes the engine-owned wire contract, spans engine/UI, and changes
saved panel behavior, finish with the current
[Windows CI-equivalent checklist](../../README.md#ci-equivalent-validation-on-windows),
using [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) as the
executable source of truth. Report every skipped required check and why; hosted
CI must still pass.

## Rollout, rollback, and risks

Ship engine and UI together through the normal build. Existing saved filters
leave `minSessionVolume` off. Existing panel settings have no column record, so
they receive the full default order including `SESSION VOL`. Older UIs ignore
the additive row/filter fields and column setting; older engines cannot provide
or enforce Session Volume, so mixed versions show it as unavailable and must not
be treated as the supported rollout. No database or config migration is needed.

Rollback is a scoped revert of the contract, engine, UI, tests, and docs.
Persisted additive JSON and Scanner-specific panel settings are ignored by older
decoders/readers.

The primary correctness risk is a sticky row carrying volume across session
boundaries; explicit phase/pool-day provenance and transition tests contain it.
The primary UI risk is an invisible sort silently driving Scanner Sync; forced
fallback and persistence contain it. Saved column settings are untrusted JSON;
the validated known-ID reader prevents missing, duplicate, or obsolete IDs from
breaking rendering. The extra default column may tighten narrow layouts, which
the accepted hide/reorder controls address without adding resize machinery.

The plan was implemented with the required validation, durable-guide updates,
and scoped code/documentation changes under the repository's executed-plan
workflow.
