# Scanner REL VOL (Relative Volume — Daily Rate)

## Status

Ready for implementation by Codex Luna Max. This is a planning-only handoff:
do not implement the feature in the planning session that created this file.
It supersedes the provider-`Volume Ratio` design in
[2026-08-18-scanner-volume-ratio.md](2026-08-18-scanner-volume-ratio.md).

## Goal

Replace Scanner's Moomoo-supplied `Vol Ratio` with eTape-calculated **Relative
Volume (Daily Rate)**, displayed as **`REL VOL`**. It must closely reproduce
Warrior Trading's same-time-of-day multiplier while covering pre-market, RTH,
and after-hours:

```text
REL VOL = current cumulative volume from 04:00 ET
          ------------------------------------------------
          mean cumulative volume at the same ET minute over
          the immediately preceding 15 trading days
```

Show two decimals, compacting values at 1,000 and 1,000,000 with `K` and `M`
suffixes, and show `—` when the number cannot be calculated. It is a
multiplier, not a percentage.

## Locked Product Decisions

- Replace the old Scanner column and filter; do not add a second volume-ratio
  column.
- UI label: `REL VOL`.
- Include 04:00–20:00 ET on normal trading days: pre-market, RTH, and
  after-hours. Exclude overnight. Use the existing NYSE calendar's earlier
  `DataClose` on early-close days.
- Use the current ET minute. The historical denominator includes finalized
  one-minute bars beginning at 04:00 and ending immediately before the current
  minute; the snapshot numerator is current through its observation time.
- Baseline: arithmetic mean over the **immediately preceding 15 trading
  days**, using whichever of those days have complete archive data. At least
  one valid day is required. Do not substitute an older sixteenth day for a
  missing day.
- A complete historical day has every expected extended-hours 1-minute bucket
  from 04:00 through that date's `session.Schedule(day).DataClose`; zero-volume
  bars are valid. An absent bucket makes that day ineligible.
- No fallback to Moomoo's `volumeRatio`. An unavailable derived value remains
  `—`; a positive REL VOL threshold does not match it.
- Preserve the current sticky-board behavior: REL VOL gates new admissions;
  an already admitted row remains until the normal board/filter reset even if
  its later score falls below the threshold.
- Preserve a saved `volRatio` sort's direction by migrating it to `relVol`.
  Reset the old numerical ratio threshold safely while preserving every other
  saved Scanner filter.

## Calibration Evidence

Warrior's public definition says its Relative Volume compares current volume
with past trading volume for the same time period, but does not disclose the
lookback length or exact session policy. See [Warrior Trading's Relative Volume
definition](https://www.warriortrading.com/relative-volume-day-trading-terminology/).

Two dated, user-supplied Warrior Scanner samples fit one common model when
compared with raw Moomoo extended-hour one-minute bars:

| Symbol | ET sample time | Warrior volume | Warrior Daily Rate | Implied denominator | Moomoo 15-day same-minute mean | Difference |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| BTCT | 2026-08-24 08:03:54 | 23.76M | 9.05 | 2.625M | 2.724M | +3.8% |
| SDOT | 2026-08-21 10:26:14 | 2.29M | 39.64 | 57.77K | 59.10K | +2.3% |

For BTCT the baseline summed bars 04:00–08:02; for SDOT it summed
04:00–10:25. Competing full-day/time-normalized models did not fit both symbols
under one shared lookback. The remaining low-single-digit variance is expected
from provider-feed differences, in-progress snapshots, and screenshot
rounding; do not promise literal tick-for-tick parity with Warrior.

Moomoo documents `pre_volume` and `after_volume` as separate pre-market and
after-hours volumes in its [market-snapshot API](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-market-snapshot.html)
and its [quote definitions](https://openapi.moomoo.com/moomoo-api-doc/en/quote/quote.html).
The live BTCT snapshot also showed a stale ordinary `volume` during pre-market,
so the existing display `Volume` field must not be reused as the numerator.

## Calculation Contract

### Time boundary and historical baseline

1. Convert `now` to `session.Loc()` and require `session.PhaseAt(now)` to be
   `PreMarket`, `RTH`, or `PostMarket`. Return unavailable for `Overnight`,
   `Closed`, before 04:00, and at/after the scheduled `DataClose`.
2. Let `cutoff` be the start of `now`'s ET minute. For a historical date, sum
   one-minute bars whose bucket starts in `[04:00, cutoff)`. Thus a sample at
   `08:03:54` uses the bar beginning `08:02`, not the partial `08:03` bar.
3. Obtain exactly the preceding 15 trading dates with repeated
   `session.PreviousTradingDay`; never include the current date. Read their
   archive range in one `ReadBars1m` call.
4. Validate each date against its own `session.Schedule(date).DataClose`:
   every expected minute bucket in `[04:00, DataClose)` must be present and
   have a non-negative volume. Treat zero volume as a real bar, not missing
   history.
5. For every valid date, form the cumulative series beginning at 04:00. A
   normal date contributes through 19:59; an early-close date contributes only
   until its data close. For a current minute later than a historical early
   close, that early-close date has no denominator sample at that minute.
6. At the requested minute, mean the available cumulative samples. Return
   unavailable if no sample exists, the mean is non-positive/non-finite, or the
   current cumulative numerator is unavailable/non-finite.

Keep this logic pure and covered by unit tests. Do not put ET time math in the
UI or hand-roll holiday/DST handling; reuse `engine/internal/session`.

### Live numerator by phase

Add a distinct internal cumulative-volume value to `rankItem`; do not change
the meaning of the existing Scanner `Volume` cell.

| Current phase | REL VOL numerator | Do not use |
| --- | --- | --- |
| Pre-market | `basic.preMarket.volume` | `basic.volume` (can be prior regular-session/stale volume) |
| RTH | `basic.preMarket.volume + basic.volume` | a rank endpoint's phase-only volume |
| After-hours | `basic.preMarket.volume + basic.volume + basic.afterMarket.volume` | `overnight.volume` |
| Overnight / closed | unavailable | any prior-day or overnight volume |

Use protobuf field presence, not `Get…` defaults, for each required component.
If an expected component is absent, leave REL VOL unavailable rather than
silently treating unknown data as zero. Preserve the existing `Last`,
`ChangePct`, and visible `Volume` phase refresh behavior.

Before landing, run a live read-only smoke comparison during RTH and
after-hours against a symbol's Moomoo one-minute cumulative bars. It verifies
that OpenD's RTH `basic.volume` remains the regular-session component before
the addition is trusted in production.

## Engine Design

### 1. Add a small, pure calculator

Create `engine/internal/scan/relative_volume.go` for package-private logic:

- `relativeVolumeLookback = 15` and the 04:00 ET session start constant.
- A compact immutable `relativeVolumeProfile` containing the per-minute
  historical mean and sample count for the current ET date. It needs no new
  database table or configuration setting.
- A builder accepting `now` and archived `[]feed.Bar`, producing the profile
  only from the preceding 15 dates and rejecting incomplete dates as defined
  above.
- A pure accessor that combines a valid profile at the current minute with the
  current cumulative snapshot volume and returns `*float64`/unavailable.
- Helpers for exact ET-minute indexing and expected-bucket validation. Guard
  negative volumes, overflow, NaN, and infinity.

Use raw extended-hours `feed.Bar` data already written by
`backfill.HistoryBars` (`Res1m`, `ExtendedTime`, unadjusted). Do not request
history from OpenD in the calculator or scanner poll loop.

### 2. Cache profiles asynchronously in the Scanner poller

Extend `engine/internal/scan/scan.go` using the existing short-interest worker
pattern, but local-only:

- Inject a narrow `ReadBars1m(symbol, fromMs, toMs)` function into `scan.New`.
  Pass `st.ReadBars1m` from `engine/cmd/etape/main.go`; pass `nil` in demo or
  tests that do not need REL VOL. Do not add a broad new provider interface.
- Add Poller-owned profile cache, pending-symbol set, FIFO queue, wake channel,
  and a single worker. Cache entries are keyed by symbol plus ET date and
  record the last attempted ET minute and whether all 15 eligible dates were
  available.
- Start the worker from `Poller.Run` only when the archive reader exists. The
  worker performs one local archive read/build per queued symbol, never an
  OpenD request. It recomputes an incomplete profile no more than once per ET
  minute so a just-started `WarmArchive` can improve a partial baseline; a
  complete 15-day profile is reused for the rest of the ET day.
- On a materially changed profile, non-blockingly `poke` the normal poller.
  The next ordinary full `scanner.rank` payload carries the new value. Do not
  create a row-level event or React-side merge.
- Clear or naturally invalidate current-day profile state at the ET day reset.
  Never reuse a prior trading day's numerator/profile across a phase or date.

The hot poll path only reads the in-memory profile and calculates one division
per row. SQLite work stays on the background worker, and the UI remains on the
existing imperative Scanner store, preserving the high-frequency invariant.

### 3. Avoid the active-filter warm-up deadlock

An active REL VOL filter cannot block its own history warm-up. Preserve the
existing `Pool` limits rather than expanding Scanner's universe:

1. Refresh snapshot fields and attach a cached REL VOL to each `rankItem` when
   available.
2. With `minRelativeVolume == 0`, call `updatePool` with the normal visible
   sticky board, exactly as today.
3. With a positive threshold, derive a **pool-only** candidate list using the
   current filters except `minRelativeVolume`; give that list to `updatePool`.
   This retains the existing top-10 / sticky-cap-30 backfill mechanism for
   symbols whose score is not ready.
4. Apply `minRelativeVolume` only when admitting a new symbol to the visible
   board. Nil scores fail a positive threshold; the candidate is not published
   until its archive profile arrives and it passes.
5. Keep the old sticky rule after admission. A later score decline remains
   visible until a normal board reset or a filter change.

This makes the new filter useful on a fresh day without triggering history
fetches for every rank response symbol. It also reuses the current Scanner
backfill policy and bounds rather than adding a second universe or a new quota
path.

### 4. Replace the engine contract and old provider path

In `engine/internal/scan/scan.go` and tests:

- Replace `rankItem.VolumeRatio` with `RelativeVolume` and add the private
  phase-stamped cumulative volume needed by the calculator.
- Replace `validVolumeRatio`, snapshot `basic.VolumeRatio` reads, and Top
  Movers fallback/retention logic. Remove all provider-volume-ratio behavior;
  it must not survive as a hidden fallback.
- Rename `MinVolumeRatio` to `MinRelativeVolume` in defaults, validation,
  filtering, sticky-admission tests, and all callers.
- Use a phase-aware snapshot helper to set the private cumulative volume, with
  field-presence validation and no overnight result.
- Apply the cached ratio to `rankItem` before `rankRowsFiltered` so the filter
  remains engine authoritative.

In `engine/internal/uihub/wsmsg/payloads.go`:

- Replace nullable JSON `volumeRatio` with `relativeVolume` on `ScannerRow`.
- Replace `minVolumeRatio` with `minRelativeVolume` on `ScannerFilters`.
- Keep the existing nullable TypeScript annotations. The Go types remain the
  sole owner of the streaming contract.

Regenerate—never hand-edit—both `ui/src/gen/wsmsg.ts` and Wails generated
models after the Go owner changes.

### 5. Migrate saved filters safely

`scanner.filters.v1` contains a threshold whose semantics are no longer valid.
Implement a deliberate v2 migration at Scanner construction/restore time:

1. Define and persist the new filter payload under `scanner.filters.v2` from
   `engine/internal/uiapi/mutations.go` after successful `SetScannerFilters`.
2. At `startPollers`, load and validate v2 first. If it is absent, decode the
   legacy v1 shape into a local legacy struct, copy its mode, change threshold,
   float cap, volume threshold, and units into the new filters, set
   `MinRelativeVolume = 0`, validate, install it, and write v2.
3. Do not delete v1. Leaving it intact makes the migration recoverable for a
   downgrade, while v2 is authoritative for the new build.
4. Invalid/malformed v1 or v2 falls back to `scan.Defaults`; it must never
   restore the old numerical threshold under the new meaning.
5. Rename corresponding `uiapi` model fields and conversions so generated
   Wails bindings expose only `minRelativeVolume`.

## UI and Scanner Sync

Update only the existing Scanner paths:

- `ui/src/data/ScannerStore.ts`: normalize missing `relativeVolume` to `null`,
  just as other nullable Scanner fields are normalized.
- `ui/src/chrome/panels/scannerFilter.ts`: rename the field and summary to
  REL VOL semantics; a positive threshold rejects `null`, zero disables it.
- `ui/src/chrome/panels/ScannerPanel.tsx`: rename the draft/default field,
  filter input and accessibility label to `rel vol ≥`; replace `Vol Ratio`
  with `REL VOL`; render the two-decimal `K`/`M`-compacted REL VOL formatter;
  keep the seven-column empty state and existing imperative store use.
- `ui/src/chrome/scannerSync.ts`: replace the `volRatio` accessor with
  `relVol`. In `readScannerSort`, map legacy persisted `{ col: "volRatio",
  dir }` to `{ col: "relVol", dir }` before returning it. This retains both
  the user's selected sort intent and Monitoring Scanner Sync's ordering.
- Update typed fixtures and `ui/src/wire/mutations.ts` decoding to use only
  `relativeVolume` / `minRelativeVolume`. Missing values remain `null`/zero;
  no compatibility path may resurrect provider `volumeRatio`.

## File-Level Execution Order

1. Add pure calculation and profile tests in
   `engine/internal/scan/relative_volume.go` and
   `engine/internal/scan/relative_volume_test.go`.
2. Adapt `engine/internal/scan/scan.go` and `scan_test.go` to populate
   phase-correct cumulative volume, queue/archive profiles, make the
   two-stage pool behavior explicit, and remove provider-ratio code/tests.
3. Change `engine/internal/uihub/wsmsg/payloads.go`, then regenerate the
   WebSocket contract.
4. Change `engine/internal/uiapi/models.go` and `mutations.go`, change
   `engine/cmd/etape/main.go` for the archive-reader injection and v1→v2
   restore, then regenerate Wails bindings.
5. Update the TypeScript store, filter helper, sorter/migration, Scanner panel,
   and their existing tests.
6. Update durable documentation:
   - `engine/internal/scan/README.md` for the formula, archive-only worker,
     availability semantics, and pool warm-up behavior;
   - `ui/src/chrome/README.md` for `REL VOL` as a Scanner Source sort;
   - `README.md` feature bullet to name Relative Volume (Daily Rate), not
     provider Volume Ratio;
   - `docs/external-apis.md` to remove the Scanner claim that Moomoo's
     `volumeRatio` is displayed; retain only the API fact if another consumer
     needs it;
   - `CONTEXT.md` only if the approved domain wording changes from the current
     Relative Volume entry.

## Required Tests

### Engine calculation tests

- Exact 15-day average at a fixed ET minute, including the exclusive current
  minute boundary used by BTCT and SDOT.
- Fewer valid dates (for example 2 of the previous 15) calculate their mean;
  zero valid dates, a zero denominator, bad volume, NaN, or infinity return
  unavailable.
- One absent minute rejects that entire historical date, while a present
  zero-volume minute remains valid.
- Weekends, holidays, current date exclusion, DST, and an early-close date use
  `session.PreviousTradingDay`/`Schedule`, not fixed UTC offsets.
- An early-close sample contributes before its own data close and is absent
  after it.
- Pre/RTH/post numerator construction, field absence, and the rule that stale
  `basic.Volume` is never used in pre-market or overnight.

### Poller, persistence, and contract tests

- Initial `—`, worker completion, and normal full-payload republish; no
  history/OpenD request on the scan poll path.
- Per-symbol ET-minute retry throttle and reuse of a complete profile.
- Positive REL VOL threshold waits for an asynchronously warmed profile,
  admits only scores at/above the boundary, and keeps later-declining rows
  sticky until reset.
- Pool-only non-REL-VOL candidates seed history when the positive filter is
  active, while they never appear in the published board before passing.
- Snapshot failure/phase rollover never leaks a stale cumulative volume into a
  new phase's score.
- v2 wins over v1; v1 copies unrelated settings but resets the threshold to
  zero; malformed saved data falls back safely; mutations persist v2.
- Go `wsmsg`, generated TS/Wails contracts, UI mutation decoding, and Scanner
  Store all expose nullable `relativeVolume` and no `volumeRatio` field.

### UI tests

- `REL VOL` header, two-decimal `K`/`M`-compacted display, `—`, filter
  input/submission/reset, summary, and seven-column empty state.
- Shared sorting places finite higher REL VOL values ahead of unavailable
  values, and Scanner Sync receives the same ordering.
- A persisted `volRatio` `asc` and `desc` sort each become `relVol` with the
  identical direction; a newly clicked header saves `relVol`.

## Validation and Rollout

Run the focused migration-gate checks after implementation:

```powershell
cd engine
go test ./internal/scan ./internal/uiapi ./internal/uihub/wsmsg ./cmd/etape
go test -race ./internal/scan
mingw32-make gen-contracts-check
go build ./cmd/etape

cd ..\ui
npx vitest run src/data/ScannerStore.test.ts src/chrome/panels/scannerFilter.test.ts src/chrome/scannerSync.test.ts src/chrome/panels/ScannerPanel.test.tsx src/wire/mutations.test.ts
npm run typecheck
```

Then run a read-only live smoke check during pre-market, RTH, and after-hours:

1. confirm current volume is phase-cumulative from 04:00;
2. confirm the denominator draws only raw extended-hours archive bars;
3. compare BTCT/SDOT or fresh synchronized Warrior samples within the
   calibration tolerance (start with approximately 5%, then record actual
   observed variance);
4. verify a fresh profile shows `—` first and appears without a UI reload.

The temporary Wails migration gate permits the focused tests, targeted race
test, generated-contract check, and TypeScript typecheck above. Defer the full
repository race suite, Playwright E2E, package/native Wails smoke, golden and
unrelated panel suites, and soak checks; record each deferral in the execution
handoff. Hosted CI must still complete before merge.

## Rollback and Risks

- Rollback is a normal code rollback. `scanner.filters.v1` remains untouched;
  v2 is additive, so a prior build can still read its old threshold.
- The value is a calibrated approximation, not a licensed Warrior feed. Feed
  differences can move a live, partial-minute result by a few percent.
- REL VOL intentionally stays unavailable while archive history is missing or
  incomplete. Do not mask this with Moomoo's different `volumeRatio` formula.
- A positive threshold may need one background warm-up cycle on a newly seen
  symbol. The two-stage existing Scanner pool prevents this from becoming an
  unbounded history-fetch fan-out.
