# Scanner Volume Ratio

Status: Approved on 2026-08-18.

## Goal

Add Moomoo's native **Volume Ratio** to Scanner as a sortable `Vol Ratio`
column and a persisted `Vol Ratio ≥ N` filter. Show the provider value in
regular and extended sessions when available, retain the Scanner's existing
sticky-board behavior, and let Scanner Sync follow a Scanner sorted by Volume
Ratio.

## Non-goals

- Do not calculate a custom relative-volume metric, use a same-time-of-day
  cumulative-volume comparison, or present the value as a percentage.
- Do not expand Scanner's universe beyond its existing session rank candidates,
  add a global stock screener, switch to StockFilter, add a subscription, or
  make another request per row.
- Do not invent an extended-hours formula. The extended-hours value is Moomoo's
  provider-supplied snapshot field and is shown as such.
- Do not add a TOML setting, a new dependency, or a migration. A zero filter
  threshold is off, including for existing saved filter JSON that lacks the new
  field.

## Research and current-code evidence

- Moomoo documents Volume Ratio as current average volume per minute since
  market open divided by the previous five trading days' average volume per
  minute: [official formula](https://support.futunn.com/hant/topic120). It is a
  unitless multiplier.
- OpenD's snapshot response (3203) exposes optional `volumeRatio`; RTH Top
  Movers exposes it too, while the US pre-market, after-hours, and overnight
  rank responses do not. Details and source links are retained in
  [`moomoo-volume-ratio-research.md`](../../.scratch/relative-volume/moomoo-volume-ratio-research.md).
- The 2026-08-18 04:14 ET pre-market snapshot returned values for the current
  leading gainers, confirming the field is populated outside RTH (but not
  documenting how Moomoo aggregates extended-hours activity):

  | Symbol | Snapshot Volume Ratio | Scanner display |
  |---|---:|---:|
  | PFSA | 19.466 | 19.47 |
  | XOS | 0.489 | 0.49 |
  | WETO | 2.447 | 2.45 |
  | EJH | 5.150 | 5.15 |
  | WFF | 58.061 | 58.06 |

- [`scan.go`](../../engine/internal/scan/scan.go) already collects rank
  candidates, refreshes every accumulated board row with a batched 3203
  snapshot each poll, and admits matching candidates to a sticky board. Its
  RTH rank path can provide the fallback value without another endpoint.
- [`payloads.go`](../../engine/internal/uihub/wsmsg/payloads.go) owns the
  engine-to-UI `ScannerRow` and `ScannerFilters` contract. Its generated
  TypeScript projection at [`ui/src/gen/wsmsg.ts`](../../ui/src/gen/wsmsg.ts)
  must be regenerated, never edited.
- [`ScannerPanel.tsx`](../../ui/src/chrome/panels/ScannerPanel.tsx) owns the
  table and filter popover. [`scannerSync.ts`](../../ui/src/chrome/scannerSync.ts)
  owns the shared sorter used both by the panel and Monitoring Scanner Sync.
- Scanner filters are persisted as `scanner.filters.v1` and validated by the
  engine; missing numeric JSON fields naturally deserialize as zero.

## Design decisions

### Meaning and availability

Use **Volume Ratio** as the canonical term and `Vol Ratio` as the compact
column label. It is a multiplier: `1.00` is the five-day per-minute baseline,
not 100%. A missing value is unavailable, not zero.

Carry it as nullable data throughout the engine and wire contract. Accept only
finite, non-negative provider values; treat a missing, negative, NaN, or
infinite value as unavailable. Display an unavailable value as `—`, and a
present value as a plain number with two decimal places (for example, `19.47`).

The batched snapshot value is primary in every session. During RTH, seed the
rank item from Top Movers' optional Volume Ratio before refreshing snapshots;
a valid snapshot overwrites it. If the current snapshot omits the field or
fails, the current RTH rank value remains the fallback. Extended-session rank
endpoints have no fallback field, so they show the latest valid snapshot value
or `—`.

### Filter and sticky board

Add `MinVolumeRatio` / `minVolumeRatio` to the engine-owned Scanner filters.
It is a minimum multiplier, defaults to `0` (off), and uses this rule when
active:

```text
row matches only when volumeRatio is available and volumeRatio >= minVolumeRatio
```

Validate it at the engine trust boundary exactly like the existing numeric
thresholds: finite and non-negative. Include it in filter equality so a change
clears and repopulates the board. Apply the threshold only when a rank
candidate enters the board. Once admitted, that symbol remains for the normal
cycle/reset even if its later Volume Ratio falls below the threshold—the
existing sticky-board contract.

### Scope, sorting, and sync

Filter only the existing rank candidates. Do not invoke a full-market Moomoo
filter request.

Add a `volRatio` sort accessor beside the existing Scanner accessors. The
existing `sortRows` behavior already places unavailable values last in either
direction. Because both the Scanner table and Monitoring Scanner Sync use
`rankScannerRows`, selecting that sort automatically makes Sync use the
highest Volume Ratio symbols; no second synchronization path is needed.

### User interface and persistence

Add `Vol Ratio` after `Vol` in the Scanner table. Add one accessible numeric
input to the existing filter popover, labelled `vol ratio ≥`, with a fractional
step and no multiplier suffix. The summary includes an active threshold as
`vol ratio ≥ N`; it omits the threshold at zero. Reset restores zero.

No separate configuration is necessary: the engine's default and the UI's
fallback both set `minVolumeRatio: 0`, and the existing generic saved-filter
store persists a nonzero user choice. This makes old saved filters safely
default to off.

No ADR is needed: this is a reversible, localized provider-field and UI
choice, with its durable terminology recorded in [`CONTEXT.md`](../../CONTEXT.md).

## File-level implementation

1. In [`engine/internal/uihub/wsmsg/payloads.go`](../../engine/internal/uihub/wsmsg/payloads.go), add nullable `VolumeRatio` to `ScannerRow` and numeric `MinVolumeRatio` to `ScannerFilters`, with the generated TypeScript annotations required for explicit `null` values. Regenerate `ui/src/gen/wsmsg.ts` from this Go owner.

2. In [`engine/internal/scan/scan.go`](../../engine/internal/scan/scan.go):
   - carry nullable Volume Ratio in `rankItem`;
   - read the optional RTH Top Movers value as the fallback, without confusing an absent protobuf field with zero;
   - merge that fallback into active RTH candidates before the snapshot pass, then let every valid 3203 snapshot value override it;
   - read/validate the snapshot's optional Volume Ratio in all sessions without adding a request; and
   - add the filter default, validation, equality check, admission predicate, and row projection. Preserve the existing snapshot failure and sticky-board behavior for all other fields.

3. In [`engine/internal/scan/scan_test.go`](../../engine/internal/scan/scan_test.go), extend existing rank/snapshot helpers and add focused cases for:
   - off, equality-boundary, unavailable, and invalid minimum-ratio filtering;
   - snapshot-over-RTH-rank priority and RTH fallback when the snapshot omits/fails;
   - pre-market, after-hours, and overnight snapshot propagation; and
   - a passing symbol remaining sticky after a later ratio drop, with a changed threshold resetting/reapplying the board.

4. In [`ui/src/chrome/panels/ScannerPanel.tsx`](../../ui/src/chrome/panels/ScannerPanel.tsx), add the default filter field, `Vol Ratio` header/cell, null display, filter input, filter summary argument, and six-column empty-state span. Keep the table data in the existing imperative Scanner store; do not add React market-data state.

5. In [`ui/src/chrome/panels/scannerFilter.ts`](../../ui/src/chrome/panels/scannerFilter.ts) and its tests, extend the existing threshold shape, pure helper semantics, and one-line summary so unavailable ratios fail only a positive ratio threshold and active summaries use a plain number.

6. In [`ui/src/chrome/scannerSync.ts`](../../ui/src/chrome/scannerSync.ts) and its tests, add the `volRatio` accessor and prove the shared ranking chooses finite higher ratios before unavailable values. Existing `AppShell` consumption then gives Scanner Sync the selected sort automatically.

7. In [`ui/src/chrome/panels/ScannerPanel.test.tsx`](../../ui/src/chrome/panels/ScannerPanel.test.tsx) and affected typed fixtures, cover the two-decimal plain-number cell, `—` for unavailable, filter submission/reset, the summary, and header sort. Supply explicit `null` values in test rows after wire generation.

8. Update durable documentation:
   - [`CONTEXT.md`](../../CONTEXT.md) already records the accepted term, display, and filter semantics;
   - [`engine/internal/scan/README.md`](../../engine/internal/scan/README.md) will document the snapshot-primary/RTH-fallback source, extended-hours caveat, and sticky threshold behavior;
   - [`ui/src/chrome/README.md`](../../ui/src/chrome/README.md) will state that Volume Ratio is a Scanner Source sort that Monitoring Sync follows;
   - [`docs/external-apis.md`](../../docs/external-apis.md) will document the optional Moomoo snapshot field and its extended-hours caveat; and
   - [`README.md`](../../README.md) will list Volume Ratio among Scanner filters.

## Validation

Run focused tests while implementing, then the Windows CI-equivalent checklist
required for a substantial engine-and-UI change:

```powershell
Set-Location engine
go test ./...
go test -race -short ./...
go vet ./...
golangci-lint run  # pinned v2.12.2
Set-Location ..
mingw32-make -C engine gen-ts-check
Set-Location ui
npm ci
npm run lint
npm test
npm run build
npm run e2e
Set-Location ..
git diff --check
```

Also verify that `ui/src/gen/wsmsg.ts` is generated without drift, the UI does
not route Scanner payloads through React state, and no credentials or runtime
data enter the change.

## Rollout and rollback

This rolls out with the normal local engine/UI build. Existing saved Scanner
filters deserialize with `minVolumeRatio = 0`, so the feature is initially
off and does not alter users' candidate boards. A nonzero threshold takes
effect on the next poll and intentionally resets that board.

Rollback is a scoped revert: old engines ignore the extra saved JSON field and
old UIs ignore the extra row field. No database, entitlement, provider, or
subscription change is involved.

## Risks

- Moomoo documents the core five-day formula but does not document the
  extended-hours aggregation behind snapshot `volumeRatio`. The UI must label
  it only as provider Volume Ratio and preserve `—` when unavailable.
- Snapshot and rank timestamps can differ slightly; snapshot wins by design,
  with the current RTH rank value only as an outage/omission fallback.
- A positive ratio filter excludes unknown data by explicit product decision;
  the sticky board means a once-matching row can remain visible until the
  standard cycle/reset.
