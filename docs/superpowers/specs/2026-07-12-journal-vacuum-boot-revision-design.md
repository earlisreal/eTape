# Journal maintenance revision — remove the boot-time VACUUM wait

**Date:** 2026-07-12
**Status:** Approved
**Revises:** §4 "Prune and file shrink" and the boot sequence of
`docs/superpowers/specs/2026-07-11-journal-storage-optimization-design.md`.
**Problem:** the merged seal-and-compress feature runs `VACUUM` on essentially
every daily boot, adding ~30–50 s (extrapolated at 30-day steady state;
4.3–64.7 s measured) to boot-to-feed, every boot, forever.

## Context and measurements

Live boot in `engine/cmd/etape/main.go` (~lines 470–491) runs
`PruneJournal → Flush → SealJournalDays → Flush → VacuumIfNeeded` before the
OpenD feed connects. Measured 2026-07-12 on a prod DB snapshot (throwaway
harness, real store code, fake clock):

- Sealing one full trading day (~2.2 M rows, ~2.3 GB raw → ~100 MB) takes
  ~13–15 s.
- `VACUUM` took 4.3–64.7 s for the same-size day. Its cost tracks **total
  retained DB size** (it rewrites the whole file, ~77–200 MB/s on this
  machine), not bytes freed.
- Sealing a normal day frees ~2.2 GB — far above the 64 MB
  `vacuumFreelistThreshold` (`engine/internal/store/retention.go:46`) — so
  VACUUM fires on essentially **every** daily boot. At the 3–5 GB steady-state
  corpus that is ~30–50 s per boot.

Verified prod state (2026-07-12): `~/.eTape/etape.db` is 4.9 GB, freelist 0,
**no `journal_chunks` rows yet** — the merged seal code has not booted against
prod, so the first mass seal is still ahead (see "Deploy" below). Raw days
2026-07-06..07-10; hot days ~1.9 GB each.

**The load-bearing SQLite fact:** freed freelist pages are reused by
subsequent INSERTs before the file grows. Without a daily VACUUM the file
converges to its high-water mark ≈ sealed corpus (3–5 GB) + one raw day
(~2.3 GB) + slack — the *same peak* a daily-vacuuming regime reaches anyway,
because the file re-grows by a raw day between boots regardless. Daily VACUUM
only shrinks a trough nobody uses.

What VACUUM is actually *for* here: (1) one-time reclaim after the first mass
seal; (2) reclaim after a deliberate retention cut; (3) anomalous bloat (the
reuse assumption failing). None is a daily event, and no freelist threshold
can discriminate them from churn: a normal day's seal frees ~2.2 GB, the same
magnitude as a 30→7 retention cut. The fix is recalibrating *who decides*,
not the number.

## Decision

**Stop vacuuming automatically on the normal boot path.** Disk reclamation
becomes (a) an explicit one-shot maintenance mode `etape -vacuum`, (b) a
per-boot storage-telemetry sys_event so bloat is visible, and (c) a
deliberately un-trippable last-resort backstop that fires only on anomalous
bloat, never on daily churn.

No schema change, no new files on disk, no read-path or writer-goroutine
change, no change to the day-roll `RequestSeal` scheduler. Steady-state boot
maintenance drops from ~45–65 s to ~13–15 s (seal only).

## Design

### 1. Boot path (`engine/cmd/etape/main.go`, live branch)

Replace the `VacuumIfNeeded` block with measurement + backstop + report:

```go
stats0, statsErr := st.SizeStats()          // pre-maintenance snapshot (2 PRAGMAs)
// PruneJournal → Flush → SealJournalDays → Flush   (unchanged)
if statsErr == nil && stats0.FreeBytes() > vacuumBackstopThreshold(stats0.FileBytes()) {
    // Anomalous: free pages accumulated ACROSS days without being reused.
    log.Warn(...); st.AppendSysEvent("retention", "backstop vacuum: ...")
    if err := st.Vacuum(); err != nil { /* log + sys_event; continue booting */ }
}
if stats1, err := st.SizeStats(); err == nil {   // post-maintenance report
    st.AppendSysEvent("storage", formatStorageReport(stats1, adviseHint))
}
st.AppendSysEvent("boot", "engine up")
// feed connect (unchanged)
```

- **The backstop measures the freelist *before* prune/seal.** At steady state
  the pre-maintenance freelist is ≈ 0 (yesterday's writes consumed the pages
  yesterday's seal freed); it stays ≈ 0 after N days offline (nothing frees
  pages while the engine is off), and a retention cut's frees appear only
  *after* this boot's prune. Only genuine cross-day reuse failure accumulates
  there — so a multi-day-offline 09:25 boot can never trip it. When it does
  trip, the VACUUM runs after the seal so it compacts everything at once.
- Every step (`SizeStats`, backstop `Vacuum`, report) is log + sys_event +
  continue — never blocks feed connect (existing honesty policy).
- Runs at the already-sanctioned point: writer goroutine up, only producers
  are maintenance's own sys_events fenced by the existing `Flush()` barriers —
  the same safety argument `VacuumIfNeeded` has today.

### 2. Store API (`engine/internal/store/retention.go`)

```go
// SizeStats is the DB's physical size profile (PRAGMA page_count/page_size/freelist_count).
type SizeStats struct{ PageSize, PageCount, FreelistPages int64 }
func (st SizeStats) FileBytes() int64
func (st SizeStats) FreeBytes() int64
func (s *Store) SizeStats() (SizeStats, error)

// Vacuum runs an unconditional VACUUM. Boot-time-only / no-live-producer
// contract as PruneJournal.
func (s *Store) Vacuum() error

// Package vars (not consts) so tests can shrink them — chunkSize pattern.
var vacuumAdviseFreeBytes int64 = 4 << 30 // post-maintenance free above this → advisory hint
var vacuumBackstopFloor   int64 = 6 << 30 // pre-maintenance free above max(floor, file/2) → backstop
func vacuumBackstopThreshold(fileBytes int64) int64 // max(vacuumBackstopFloor, fileBytes/2)
```

`VacuumIfNeeded` and its 64 MB threshold stay unchanged but demoted: no
engine-boot caller; used only by the manual mode as a "nothing worth
reclaiming" early-out (no latency budget to protect there).

Calibration: heavy day frees ~2.2–2.3 GB; normal *post*-maintenance freelist
~2.3–2.6 GB (< 4 GB advise → no noise); *pre*-maintenance freelist ≈ 0
(≪ 6 GB backstop). A retention cut (30→7 ≈ +2.3 GB) lands between: advisory
hint, never backstop. Revisit both numbers after a week of `storage`
sys_events.

### 3. Manual reclaim: `etape -vacuum` (new `engine/cmd/etape/vacuum.go`)

A boolean flag on the existing flag-based CLI (consistent with
`-demo`/`-replay`). In `boot()`, **after** config load and **after**
`singleinstance.Acquire(dbPath + ".lock")` succeeds, branch into
`runVacuumMode` — no uihub, no browser open, no feed:

1. `store.Open` on the same DB (same DSN/pragmas).
2. `PruneJournal → Flush → SealJournalDays → Flush → VacuumIfNeeded` — the
   exact boot-maintenance sequence, so a vacuum run also opportunistically
   seals/prunes anything pending.
3. Log + print file size before/after; report the threshold early-out
   explicitly ("freelist 12 MB ≤ 64 MB — nothing to reclaim").
4. `st.Close()`, exit 0 (non-zero on any step error).

**Single-instance safety (mandatory):** on `ErrAlreadyRunning` the vacuum
branch must *not* take the normal "open the browser to the running instance"
path — it logs "engine is running; stop it before running etape -vacuum" and
exits 1. This makes any future unattended scheduling (launchd/cron firing
`etape -vacuum` nightly) safe by construction: worst case it skips.
Scheduling itself stays outside the engine.

### 4. Telemetry: per-boot `storage` sys_event

Every live boot appends one sys_event (kind `"storage"`), e.g.
`storage: file 7.4 GB, free 2.5 GB (34%), journal_chunks ~4.9 GB, raw rows 0`,
with a suffix hint when post-maintenance free bytes exceed
`vacuumAdviseFreeBytes`: "consider `etape -vacuum` to reclaim now (otherwise
reabsorbed over ~N days)". This is the falsification instrument for the
design's load-bearing hypothesis: if freelist reuse drifts in production, the
numbers show it within days instead of surfacing as silent disk exhaustion.

### 5. Runbook

Run `etape -vacuum` (engine stopped) after: (a) deliberately lowering
`retention_days`, (b) a `storage` sys_event advising it, (c) anomaly
investigation. Otherwise never — daily churn reabsorbs its own free space.

### 6. Deploy / first boot after this change

Prod has not mass-sealed yet (verified above). First boot with this design:
mass-seals ~4.4 GB of raw days (one-time, spec-estimated 1–3 min — run it
outside RTH per the standing note), runs **no** VACUUM, and leaves a
~4.3–4.5 GB freelist. The backstop cannot fire (pre-maintenance freelist is
0); the advisory hint will. The free space is reabsorbed by ~2 days of live
writes — or run `etape -vacuum` once at deploy to reclaim immediately.

### Decisions taken by default (overridable at review)

- **Backstop: kept** (vs. advisory-only). Constraint: unbounded growth is not
  acceptable; the hypothesis is not yet field-proven.
- **Thresholds: package vars, not config keys.** Config surface for numbers
  you should never touch invites misconfiguration.
- **No launchd/cron wrapper now** — on-demand manual is fine; the lock makes
  adding one later trivial and safe.
- **No `-force` flag** on `etape -vacuum` until a real need appears.

## Latency and disk profile

| Scenario | Merged code (today) | This design |
|---|---|---|
| Steady-state daily boot | seal ~13–15 s + VACUUM ~30–50 s | seal ~13–15 s |
| 09:25 ET boot after N days offline | seal × N + corpus-sized VACUUM | seal × N only |
| First boot after a retention cut | corpus VACUUM inline | fast boot; advisory; reclaim at leisure |
| Anomalous bloat | masked by daily VACUUM | one loud backstop VACUUM, bounded ~2× |

Disk: file sits at its high-water mark (~7–8 GB at spec steady state) instead
of oscillating down to ~5 GB between boots. Peak usage is unchanged; only the
trough is forgone.

Out of scope but flagged: seal latency itself (~13–15 s/day, × N after
offline gaps) is now the largest boot cost. A future optimization can
pipeline the CPU-bound zstd encoding across days; nothing here forecloses it.

## Verification plan

1. **Pre-code gate — page-reuse plateau experiment** (running 2026-07-12,
   throwaway harness `engine/internal/store/pagereuse_experiment_test.go`,
   build tag `pagereuse`, prod-DB copy in `/tmp/etape-pagereuse`): mass-seal + 5
   synthetic write→seal cycles, recording `page_count`/`freelist_count` per
   phase. **Pass: page_count plateaus within 1.1× of the cycle-1 high-water
   mark. Fail ⇒ pivot to the `VACUUM INTO` + swap fallback.**
   *Result: PENDING — to be recorded here before implementation.*
2. Unit tests: `SizeStats`, `Vacuum`, backstop trigger and advisory hint with
   shrunk package vars.
3. Wiring tests: `-vacuum` happy path on a temp DB (seals + prunes + vacuums,
   exit 0); `-vacuum` against a running instance → refusal, exit 1, no
   browser open; boot with large post-maintenance freelist → hint present.
4. Deploy checks: record `PRAGMA freelist_count`/`page_count` on the live DB;
   boot-time before/after measurement (expect ~45–65 s → ~13–15 s at steady
   state; first boot is the one-time mass seal).

## Risk register

1. **Concurrent-process vacuum → journal drops (replay data loss).**
   Structural mitigation: the `-vacuum` branch sits after
   `singleinstance.Acquire` and refuses when the engine runs; wiring test
   asserts the refusal.
2. **Freelist-reuse hypothesis fails.** Three layers: pre-code experiment
   (§ Verification 1), production telemetry (§4 of Design), backstop (§1).
3. **Backstop misfire at 09:25 ET.** Pre-maintenance trigger is ≈ 0 in every
   normal scenario; floor sits ~2.5× above the heaviest observed daily churn;
   unit-tested with shrunk vars.
4. **Fragmentation / read locality of sealed chunks on reused pages.**
   Chunk blobs interleave with next-day raw pages; `ReadJournalDay` streams
   via the `(day, chunk_no)` PK — extra seeks are noise vs zstd decode on
   SSD, and replay is offline tooling. Accepted; a manual `etape -vacuum`
   fully defragments if it ever measurably matters.
5. **Degradation.** Every new step is log + sys_event + continue — never
   blocks market data.

## Rejected alternatives

1. **Async dual-write / rotate to a second DB (original idea), steelmanned:**
   fresh file takes new writes at boot while the old compacts in background,
   reads merge both. Pays a permanent multi-file read-merge layer (mirror
   variant: ~2× hot-path write I/O), doubles the honesty-policy divergence
   surface (two files that can disagree about drops), and still has to
   compact/swap the old file — compaction relocated, not removed, to buy back
   a wait the analysis shows is eliminable.
2. **Day-roll (00:30 ET) automatic vacuum:** new writer-op type + timing
   safety argument for a benefit the reuse hypothesis says is ≈ 0, and the
   engine usually isn't running at 00:30 ET.
3. **`VACUUM INTO` + atomic swap:** sound primitive (original untouched until
   one rename; WAL sidecars must be handled; never swap under a live engine),
   but it's a scheduler for a reclaim that rarely needs to run. **Held in
   reserve as the fallback** if the experiment falsifies the hypothesis. The
   "stage in background, swap next boot" variant is unsound — it discards
   everything written between staging and swap.
4. **`auto_vacuum=INCREMENTAL`:** permanent ptrmap overhead + fragmentation,
   requires one full VACUUM to convert the existing file, and has its own
   who-runs-it-when question.
5. **Per-day cold files (retention = file delete):** structurally eliminates
   VACUUM with perfect locality, but ~600–900 LOC, demojournal/e2e tooling
   fallout, a hard one-way downgrade, and new crash windows — for the same
   steady-state boot latency. Right answer only if retention/scale grows
   substantially; this design forecloses none of it.
6. **Threshold recalibration alone:** daily churn and rare bloat are the same
   magnitude; no number discriminates them. The recalibrated shape survives
   only as the deliberately un-trippable backstop.

## Files touched

`engine/cmd/etape/main.go` (maintenance block, flag registration, vacuum
branch after `singleinstance.Acquire`), new `engine/cmd/etape/vacuum.go`,
`engine/internal/store/retention.go` (+ `retention_test.go`), runbook note in
the 2026-07-11 storage spec or companion doc. Unchanged: `store/seal.go`,
`store/store.go`, `store/journal.go`, `store/schema.go`,
`cmd/etape/scheduler.go`, `internal/demojournal`.
