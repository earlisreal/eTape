# Journal seal + VACUUM boot timing — 2026-07-12

Follow-up to the journal storage optimization
(`docs/superpowers/specs/2026-07-11-journal-storage-optimization-design.md`,
merged `7ca5b40`). At merge time, review flagged that the design's "1–3 min"
seal estimate covered sealing only, not `VACUUM`'s file-rewrite time, and
recommended running the first live boot outside RTH. This doc measures the
actual cost, and finds the risk is bigger than a one-time migration: **VACUUM
recurs on every daily boot**, not just the first.

## Method

Snapshotted the real production DB (`~/.eTape/etape.db`, 5.05 GB, 5 raw days:
07-06 through 07-10) via `sqlite3 .backup` to `/tmp/etape-seal-timing/etape.db`
— production data untouched. Wrote a throwaway test
(`engine/internal/store/seal_timing_manual_test.go`, deleted after the run —
not committed) that opens the copy via the real `store.Open`, wired to a
`clock.Fake` to control what day counts as "today," and times the exact boot
sequence `main.go` runs before the feed connects:
`PruneJournal → Flush → SealJournalDays → Flush → VacuumIfNeeded`.

Ran it three times against the same evolving copy, advancing the fake clock
each time so each pass sealed the next unsealed day — simulating three
successive daily boots:

```
SEAL_TIMING_DB=/tmp/etape-seal-timing/etape.db SEAL_TIMING_TODAY=<RFC3339> \
  go test ./internal/store/ -run TestManualSealTiming -v -timeout 20m
```

## Results

| Pass | Day(s) sealed | Rows | Raw → compressed | Seal time | VACUUM time | DB size after |
|---|---|---|---|---|---|---|
| 1 | 07-06 + 07-07 | 34,080 | 71 MB → 1 MB | 0.34s | **64.7s** | 5057 → 4962 MB |
| 2 | 07-08 (full day) | 2,210,656 | 2328 MB → 96 MB | 13.0s | **32.7s** | 4962 → 2993 MB |
| 3 | 07-09 (full day) | 2,313,837 | 2385 MB → 107 MB | 14.8s | **4.4s** | 2993 → 880 MB |

## Reads

- **Sealing (compress) itself is cheap and predictable**: ~13–15s for a full
  ~2.2M-row trading day (~23× compression ratio), independent of overall DB
  size. This part matches the design doc's expectations.
- **VACUUM cost tracks total retained (non-free) data in the file, not the
  amount freed** — because SQLite's `VACUUM` rewrites the *entire* database
  into a new file and swaps it in. Pass 1 freed only ~95 MB but took 64.7s
  because it still had to copy ~4.96 GB of surrounding live data. Pass 3 freed
  ~2.4 GB but took only 4.4s because the file itself was already down to
  <1 GB by then. Observed throughput ≈ 77–200 MB/s of retained data on this
  machine/disk (noisy — OS cache state, not purely disk-bound).
- **This is not a one-time cost.** `vacuumFreelistThreshold` is 64 MB
  (`engine/internal/store/retention.go:43-46`); a normal trading day's raw
  deletion frees ~2+ GB, so `VacuumIfNeeded` will fire on essentially every
  live boot going forward, not just the first one after this feature shipped.
- **Projected steady state**: at 30-day retention the design doc projects a
  ~3–5 GB retained (sealed) corpus. Every daily boot's `VacuumIfNeeded` has to
  rewrite that whole corpus again after each day's seal frees its raw rows —
  extrapolating from pass 2/3's rates, that's **~30–50s of VACUUM per boot,
  indefinitely**, on top of ~13–15s of sealing.
- **Feed connection is gated behind this.** In `engine/cmd/etape/main.go`, the
  HTTP/UI server binds and the browser opens *before* the
  `PruneJournal/SealJournalDays/VacuumIfNeeded` block (`main.go:412-445` vs.
  `469-491`); the OpenD feed client (`opend.New`/`fd.Run`) only starts *after*
  that block completes (`main.go:494+`). So the app window appears instantly,
  but live quotes/ticks/book are delayed by the full seal+vacuum duration on
  every boot — roughly 45–65s at steady state, not a one-time migration cost.

## Caveats

- Single machine/disk, cold vs. warm OS file-cache state not controlled
  between passes — throughput numbers (77–200 MB/s) are directional, not a
  guaranteed rate on other hardware.
- Measured against a 5-day-old production snapshot, not the full 30-day
  steady-state size the design doc projects (3–5 GB) — steady-state VACUUM
  time is extrapolated from pass 2/3's per-GB rate, not directly measured at
  that scale.
- Harness called the real `store` package functions directly (not a full
  `etape` binary boot) — accurately reflects the boot-sequence cost since it
  reuses the exact functions `main.go` calls in the same order, but doesn't
  exercise anything else concurrent at boot.

## Implication (not yet actioned)

The original merge-time risk note ("first boot may cost 1–3 min + VACUUM
time, run it outside RTH") undersold the problem: the recurring version means
every daily restart pays a ~45–65s VACUUM tax before market data starts,
indefinitely, as long as retention holds multiple sealed days. Two candidate
fixes, neither implemented here:

1. Raise `vacuumFreelistThreshold` well above a typical day's freed size (a
   few GB) so VACUUM only runs occasionally, not near-daily.
2. Move `VacuumIfNeeded` off the pre-feed-connect critical path (e.g., run it
   after the feed starts, or on a slower cadence than every boot) so it no
   longer delays live data.
