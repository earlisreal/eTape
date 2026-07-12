# Journal Vacuum Boot-Path Revision — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop the engine from running `VACUUM` on every daily boot (a ~30–50 s recurring wait), replacing it with a per-boot storage-telemetry sys_event, an un-trippable anomaly backstop, an explicit `etape -vacuum` maintenance mode, and a neutral boot-status banner so the remaining seal window reads as expected maintenance instead of a red "feed disconnected" error.

**Architecture:** Move disk reclamation off the boot hot path. The live boot block measures the DB (pre-maintenance) → prunes/seals (unchanged) → runs `VACUUM` *only* when pre-maintenance free space proves cross-day page-reuse failure → emits a `storage` telemetry event. A new `sys.boot` snapshot topic drives a neutral UI banner (`sealing`→`connecting`→`ready`) that also gates the red feed banner until the engine is truly ready. Manual reclaim moves to `etape -vacuum`, which refuses to run while an engine holds the single-instance lock.

**Tech Stack:** Go engine (`github.com/earlisreal/eTape/engine`, `modernc.org/sqlite`), TypeScript + React UI, `tygo` for Go→TS type generation, WebSocket + JSON transport.

## Context

The seal-and-compress feature merged 2026-07-11 (`7ca5b40`) runs `VacuumIfNeeded` on essentially every live boot: sealing one trading day frees ~2.2 GB, far above the 64 MB `vacuumFreelistThreshold`, so `VACUUM` fires almost every day. `VACUUM` cost tracks *total DB size* (it rewrites the whole file), so at the 3–5 GB steady-state corpus that is ~30–50 s added to boot-to-feed, every boot, forever. The page-reuse plateau experiment (2026-07-12, **PASSED**) confirmed SQLite reuses freed freelist pages under zstd-blob churn — the file plateaus at its high-water mark without a daily VACUUM, so the daily VACUUM only shrinks a trough nobody uses. This plan implements the approved revision spec: `docs/superpowers/specs/2026-07-12-journal-vacuum-boot-revision-design.md`.

## Global Constraints

- **Single-writer exception.** `PruneJournal`/`SealJournalDays`/`Vacuum`/`VacuumIfNeeded` touch `s.db` directly. They are boot-time-only maintenance ops that must run *before* the feed producer starts and *after* `st.Flush()` has drained queued writes. Never convert them to the async writer pattern.
- **Honesty policy.** Every maintenance step is `log + sys_event + continue`. Nothing in the maintenance block may return a boot-fatal error or block feed connect.
- **No structural changes.** No schema change, no new on-disk files, no read-path or writer-goroutine change, no change to `cmd/etape/scheduler.go` (`RequestSeal` day-roll), and **no change to `store/seal.go`'s sealing logic** (per the decision below, seal progress is reported from an up-front count, not from inside the seal loop).
- **Thresholds are package vars, not config keys** — lowercase, in `retention.go`, so tests can shrink them: `vacuumAdviseFreeBytes int64 = 4 << 30` (4 GiB), `vacuumBackstopFloor int64 = 6 << 30` (6 GiB). Do **not** add `[store]` config keys for these.
- **Wire types.** Any change under `engine/internal/uihub/wsmsg/` requires running `make gen-ts` in `engine/`; CI gate `make gen-ts-check` must pass (no drift). Never hand-edit `ui/src/gen/wsmsg.ts` — the only hand-authored wire declarations live in `engine/tygo.yaml`'s `frontmatter` block (the `Topic` union and envelope frames).
- **UI styling.** Follow the frontend-design skill for the banner. It is **neutral/info-toned**, never the red danger tone (that tone is reserved for real feed failures). Match the existing banner visual language (`ReplayBanner.tsx`, `FeedStatusBanner.tsx`).
- **Safety.** This touches boot and a destructive `VACUUM` path. `etape -vacuum` must refuse when an engine instance holds the lock. No order-writing code is involved; live-account safety rules are unaffected.

## Decisions locked (from review)

- **Seal progress reporting:** single `sys.boot {phase:"sealing", daysTotal:N}` update published *before* the seal, with `N` counted up front via a new `Store.PendingSealDays()`. **No per-day granularity, no change to `seal.go`.**
- **Page-reuse experiment file:** `engine/internal/store/pagereuse_experiment_test.go` is committed **as-is** (build tag `pagereuse`, excluded from normal `go test`, needs a prod DB copy to run) so the gate is reproducible.

## File Structure

**Engine (Go):**
- `engine/internal/store/retention.go` — add `SizeStats` + methods, `Vacuum`, `JournalFootprint`, `PendingSealDays`, threshold package vars, `FormatStorageReport`; keep `VacuumIfNeeded` (demoted). Test: `engine/internal/store/retention_test.go`.
- `engine/internal/uihub/wsmsg/wsmsg.go` — `TopicSysBoot` const + `AllTopics` entry.
- `engine/internal/uihub/wsmsg/payloads.go` — `BootStatus` DTO (tygo-generated).
- `engine/tygo.yaml` — add `"sys.boot"` to the `Topic` union; then regen `ui/src/gen/wsmsg.ts`.
- `engine/internal/uihub/mirror.go` — `boot` field + `applyPub`/`snapshotFrames` cases.
- `engine/internal/uihub/api.go` — initialize `m.boot` by mode.
- `engine/cmd/etape/main.go` — rewrite the live maintenance block; register `-vacuum`; branch into vacuum mode after the lock; publish `sys.boot` phases (live + replay).
- `engine/cmd/etape/vacuum.go` — **new** `runVacuumMode`. Test: `engine/cmd/etape/vacuum_test.go`.

**UI (TS/React):**
- `ui/src/data/BootStore.ts` — **new** (clone of `SessionStore.ts`).
- `ui/src/data/registry.ts` — add `boot` store + `sys.boot` route.
- `ui/src/chrome/panels/registry.tsx` — add `"sys.boot"` to `ConnectionStatusPanel`'s topics (puts it in the always-subscribed union).
- `ui/src/chrome/BootStatusBanner.tsx` — **new** neutral banner. Test: `ui/src/chrome/BootStatusBanner.test.tsx`.
- `ui/src/chrome/FeedStatusBanner.tsx` — gate on `phase === "ready"`.
- `ui/src/chrome/AppShell.tsx` — mount `BootStatusBanner`, pass `boot` store to `FeedStatusBanner`.

**Docs:** operator runbook note; commit the experiment file.

---

### Task 1: Store size/vacuum API + pending-seal count

**Files:**
- Modify: `engine/internal/store/retention.go`
- Test: `engine/internal/store/retention_test.go` (**already exists**, `package store` — add to it; it can shrink the unexported threshold vars and already defines `mustLoc(t)`; `openAtClock(t, now)` is in `seal_test.go`, same package — reuse both, do not redefine)

**Interfaces:**
- Produces: `type SizeStats struct{ PageSize, PageCount, FreelistPages int64 }`; `(SizeStats).FileBytes() int64`; `(SizeStats).FreeBytes() int64`; `(SizeStats).NeedsBackstopVacuum() bool`; `(SizeStats).AdviseVacuum() bool`; `(*Store).SizeStats() (SizeStats, error)`; `(*Store).Vacuum() error`; `(*Store).JournalFootprint() (chunkBytes, rawRows int64, err error)`; `(*Store).PendingSealDays() ([]string, error)`.
- Consumes: existing unexported `(*Store).daysToSeal(before string)` and `dayKey(ms int64) string` (both in `seal.go`, same package — call them, do not modify them). Existing `VacuumIfNeeded`/`vacuumFreelistThreshold` stay as-is (demoted; no engine-boot caller after Task 4).

- [ ] **Step 1: Write the failing tests**

`retention_test.go` already exists (with `mustLoc(t) *time.Location`); `seal_test.go` (same package) already defines `openAtClock(t, now time.Time) *Store` — `Open(Options{Path: …, Clock: clock.NewFake(now), FlushInterval: time.Hour})` against a fresh temp-dir DB. Reuse both; do not redefine them. There is no existing raw-row seeder, so add one small package-local helper (`seedRawDay`) alongside the new tests, built on the existing `journalInsertSQL` constant (`journal.go`: `INSERT INTO journal (day, seq, ts_exch, ts_recv, symbol, kind, seed, payload) VALUES (?,?,?,?,?,?,?,?)`) — the same direct-SQL pattern `seal_test.go` already uses to seed rows.

Add to `engine/internal/store/retention_test.go`:

```go
// seedRawDay inserts n raw journal rows for day directly via journalInsertSQL
// (same pattern seal_test.go uses), bypassing RecordEvent's seq-cache bookkeeping
// since these tests only need rows to exist, not to be recorded live.
func seedRawDay(t *testing.T, s *Store, day string, n int) {
	t.Helper()
	payload := strings.Repeat("x", 256) // large enough that bulk delete produces a real freelist
	for i := 0; i < n; i++ {
		if _, err := s.db.Exec(journalInsertSQL, day, i+1, recvBase+int64(i), recvBase+int64(i),
			"AAPL", "tick", 0, payload); err != nil {
			t.Fatalf("seedRawDay: %v", err)
		}
	}
}

func TestSizeStatsBytes(t *testing.T) {
	s := SizeStats{PageSize: 4096, PageCount: 100, FreelistPages: 10}
	if got := s.FileBytes(); got != 4096*100 {
		t.Fatalf("FileBytes=%d", got)
	}
	if got := s.FreeBytes(); got != 4096*10 {
		t.Fatalf("FreeBytes=%d", got)
	}
}

func TestBackstopAndAdvisePredicates(t *testing.T) {
	orig1, orig2 := vacuumBackstopFloor, vacuumAdviseFreeBytes
	t.Cleanup(func() { vacuumBackstopFloor, vacuumAdviseFreeBytes = orig1, orig2 })
	vacuumBackstopFloor = 6 << 20 // 6 MiB
	vacuumAdviseFreeBytes = 4 << 20

	// file 8 MiB, free 5 MiB: threshold = max(6MiB, 4MiB) = 6MiB → below → no backstop.
	below := SizeStats{PageSize: 1 << 20, PageCount: 8, FreelistPages: 5}
	if below.NeedsBackstopVacuum() {
		t.Fatal("should not trip backstop below floor")
	}
	// file 8 MiB, free 7 MiB: 7 > 6 → backstop.
	above := SizeStats{PageSize: 1 << 20, PageCount: 8, FreelistPages: 7}
	if !above.NeedsBackstopVacuum() {
		t.Fatal("should trip backstop above floor")
	}
	// file 20 MiB, free 9 MiB: threshold = max(6, 10) = 10MiB → below.
	half := SizeStats{PageSize: 1 << 20, PageCount: 20, FreelistPages: 9}
	if half.NeedsBackstopVacuum() {
		t.Fatal("half-file rule should dominate the floor here")
	}
	if !above.AdviseVacuum() { // 7 MiB free > 4 MiB advise threshold
		t.Fatal("7 MiB free should advise")
	}
	if !below.AdviseVacuum() { // 5 MiB free > 4 MiB advise threshold (below the 6 MiB BACKSTOP, still above ADVISE)
		t.Fatal("5 MiB free should advise")
	}
}

func TestVacuumReclaimsFreePages(t *testing.T) {
	st := openAtClock(t, time.Date(2026, 7, 11, 12, 0, 0, 0, mustLoc(t)))
	seedRawDay(t, st, "2026-07-08", 5000)
	st.Flush()
	if _, err := st.db.Exec("DELETE FROM journal WHERE day='2026-07-08'"); err != nil {
		t.Fatal(err)
	}
	pre, _ := st.SizeStats()
	if pre.FreeBytes() == 0 {
		t.Skip("no freelist accumulated; page churn too small on this platform")
	}
	if err := st.Vacuum(); err != nil {
		t.Fatal(err)
	}
	post, _ := st.SizeStats()
	if post.FreeBytes() >= pre.FreeBytes() {
		t.Fatalf("vacuum did not reclaim: pre=%d post=%d", pre.FreeBytes(), post.FreeBytes())
	}
}

func TestPendingSealDays(t *testing.T) {
	st := openAtClock(t, time.Date(2026, 7, 11, 12, 0, 0, 0, mustLoc(t)))
	seedRawDay(t, st, "2026-07-08", 10)
	seedRawDay(t, st, "2026-07-09", 10)
	seedRawDay(t, st, "2026-07-11", 10) // today (ET) — must be excluded
	st.Flush()
	days, err := st.PendingSealDays()
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0] != "2026-07-08" || days[1] != "2026-07-09" {
		t.Fatalf("pending=%v want [07-08 07-09]", days)
	}
}
```

Add `"strings"` to `retention_test.go`'s imports (for `seedRawDay`'s payload filler and Task 2's `FormatStorageReport` test below), and confirm `time`/`testing` are already imported — this file already exists, so only add what's missing.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/store/ -run 'SizeStats|Backstop|Vacuum|PendingSeal' -v`
Expected: FAIL — `SizeStats`, `NeedsBackstopVacuum`, `Vacuum`, `PendingSealDays` undefined.

- [ ] **Step 3: Implement in `retention.go`**

Add below the existing `vacuumFreelistThreshold` block (leave `VacuumIfNeeded` untouched):

```go
// Thresholds for the boot-path maintenance decisions. Package vars (not consts)
// so tests can shrink them; deliberately NOT config keys — see the design spec.
var (
	vacuumAdviseFreeBytes int64 = 4 << 30 // post-maintenance free above this → advisory hint
	vacuumBackstopFloor   int64 = 6 << 30 // pre-maintenance free above max(floor, file/2) → backstop
)

// vacuumBackstopThreshold is the pre-maintenance free-byte level above which the
// boot path runs an anomaly-backstop VACUUM: max(floor, half the file). A normal
// day's ~2.2 GB of seal-freed pages appears only AFTER this boot's prune/seal, so
// the pre-maintenance freelist is ≈ 0 in every normal scenario and can never trip
// this — only genuine cross-day reuse failure accumulates here.
func vacuumBackstopThreshold(fileBytes int64) int64 {
	if h := fileBytes / 2; h > vacuumBackstopFloor {
		return h
	}
	return vacuumBackstopFloor
}

// SizeStats is the DB's physical size profile (PRAGMA page_size/page_count/
// freelist_count).
type SizeStats struct{ PageSize, PageCount, FreelistPages int64 }

func (st SizeStats) FileBytes() int64 { return st.PageSize * st.PageCount }
func (st SizeStats) FreeBytes() int64 { return st.PageSize * st.FreelistPages }

// NeedsBackstopVacuum reports whether PRE-maintenance free space indicates
// cross-day page-reuse failure (anomalous bloat). Pass the pre-prune snapshot.
func (st SizeStats) NeedsBackstopVacuum() bool {
	return st.FreeBytes() > vacuumBackstopThreshold(st.FileBytes())
}

// AdviseVacuum reports whether POST-maintenance free space is high enough to
// suggest a manual `etape -vacuum` (advisory only; reabsorbed by daily churn
// otherwise). Pass the post-seal snapshot.
func (st SizeStats) AdviseVacuum() bool { return st.FreeBytes() > vacuumAdviseFreeBytes }

// SizeStats reads the current physical size profile via three PRAGMAs.
func (s *Store) SizeStats() (SizeStats, error) {
	var st SizeStats
	if err := s.db.QueryRow("PRAGMA page_size").Scan(&st.PageSize); err != nil {
		return st, err
	}
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&st.PageCount); err != nil {
		return st, err
	}
	if err := s.db.QueryRow("PRAGMA freelist_count").Scan(&st.FreelistPages); err != nil {
		return st, err
	}
	return st, nil
}

// Vacuum runs an unconditional VACUUM. Boot-time-only / no-live-producer
// contract, identical to PruneJournal: call it before the feed producer starts
// and after Flush() has drained queued writes (VACUUM needs exclusive access).
func (s *Store) Vacuum() error {
	_, err := s.db.Exec("VACUUM")
	return err
}

// JournalFootprint returns the sealed-chunk byte total and the raw (unsealed)
// journal row count — the two numbers the per-boot storage telemetry reports.
func (s *Store) JournalFootprint() (chunkBytes, rawRows int64, err error) {
	if err = s.db.QueryRow("SELECT COALESCE(SUM(LENGTH(body)),0) FROM journal_chunks").Scan(&chunkBytes); err != nil {
		return
	}
	err = s.db.QueryRow("SELECT COUNT(*) FROM journal").Scan(&rawRows)
	return
}

// PendingSealDays returns exactly the days SealJournalDays would compress on this
// boot (distinct raw days strictly older than the current ET day). Used by the
// boot path to size the "preparing journal" banner before the blocking seal.
// Reuses the same day boundary as SealJournalDays (dayKey + daysToSeal) so the
// count can never disagree with what the seal actually does.
func (s *Store) PendingSealDays() ([]string, error) {
	return s.daysToSeal(dayKey(s.clk.Now().UnixMilli()))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/store/ -run 'SizeStats|Backstop|Vacuum|PendingSeal' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/store/retention.go engine/internal/store/retention_test.go
git commit -m "feat(store): add SizeStats, Vacuum, backstop/advise predicates, PendingSealDays"
```

---

### Task 2: Storage-report formatter

**Files:**
- Modify: `engine/internal/store/retention.go`
- Test: `engine/internal/store/retention_test.go`

**Interfaces:**
- Produces: `func FormatStorageReport(st SizeStats, chunkBytes, rawRows int64, advise bool) string` — the `detail` string for the per-boot `storage` sys_event (kind supplies the `storage:` prefix). Exported because it is called from `cmd/etape`.

- [ ] **Step 1: Write the failing test**

```go
func TestFormatStorageReport(t *testing.T) {
	st := SizeStats{PageSize: 4096, PageCount: 1_953_125, FreelistPages: 610_351} // ~8.0 GB file, ~2.5 GB free (~31%)
	got := FormatStorageReport(st, 4_900_000_000, 0, false)
	want := "file 8.0 GB, free 2.5 GB (31%), journal_chunks ~4.9 GB, raw rows 0"
	if got != want {
		t.Fatalf("report:\n got=%q\nwant=%q", got, want)
	}
	adv := FormatStorageReport(st, 4_900_000_000, 0, true)
	if adv == want || !strings.Contains(adv, "etape -vacuum") {
		t.Fatalf("advisory suffix missing: %q", adv)
	}
}
```

Add `"strings"` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/store/ -run FormatStorageReport -v`
Expected: FAIL — `FormatStorageReport` undefined. (Adjust the exact `want` string in Step 3 to match the finalized formatter, then re-assert.)

- [ ] **Step 3: Implement in `retention.go`**

```go
import (
	"fmt"
	"time" // already imported
)

// humanBytes renders a byte count as a one-decimal GB (or whole MB below 1 GB)
// string. Uses DECIMAL units (÷1e9 / ÷1e6) to match the spec's reported figures
// and the page-reuse experiment's gbExp helper (pages*4096/1e9), not binary GiB.
func humanBytes(n int64) string {
	const gb = 1_000_000_000
	if n >= gb {
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
	}
	return fmt.Sprintf("%d MB", n/1_000_000)
}

// FormatStorageReport builds the per-boot `storage` sys_event detail. When
// advise is true it appends a hint to run `etape -vacuum`, estimating the
// reabsorption horizon from a nominal raw-day size (a display estimate, not a
// tunable threshold).
func FormatStorageReport(st SizeStats, chunkBytes, rawRows int64, advise bool) string {
	file := st.FileBytes()
	free := st.FreeBytes()
	pct := 0
	if file > 0 {
		pct = int(free * 100 / file)
	}
	rep := fmt.Sprintf("file %s, free %s (%d%%), journal_chunks ~%s, raw rows %d",
		humanBytes(file), humanBytes(free), pct, humanBytes(chunkBytes), rawRows)
	if advise {
		const rawDayBytesEstimate = 2 << 30 // ~one trading day of raw feed
		days := free / rawDayBytesEstimate
		if days < 1 {
			days = 1
		}
		rep += fmt.Sprintf(" — consider `etape -vacuum` to reclaim %s now (otherwise reabsorbed over ~%d days)",
			humanBytes(free), days)
	}
	return rep
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/store/ -run FormatStorageReport -v`
Expected: PASS. (If the byte-rounding differs, correct the `want` literal to the produced value — the goal is a stable, readable format.)

- [ ] **Step 5: Commit**

```bash
git add engine/internal/store/retention.go engine/internal/store/retention_test.go
git commit -m "feat(store): add FormatStorageReport for per-boot storage telemetry"
```

---

### Task 3: `sys.boot` wire contract + mirror snapshot + tygo regen

**Files:**
- Modify: `engine/internal/uihub/wsmsg/wsmsg.go` (topic const + `AllTopics`)
- Modify: `engine/internal/uihub/wsmsg/payloads.go` (`BootStatus` DTO)
- Modify: `engine/tygo.yaml` (add `"sys.boot"` to the `Topic` union)
- Regenerate: `ui/src/gen/wsmsg.ts` via `make gen-ts`
- Modify: `engine/internal/uihub/mirror.go` (`boot` field + two cases)
- Modify: `engine/internal/uihub/api.go` (init `m.boot`)
- Test: `engine/internal/uihub/mirror_test.go` (or `hub_test.go`)

**Interfaces:**
- Produces (Go): `wsmsg.TopicSysBoot Topic = "sys.boot"`; `type wsmsg.BootStatus struct{ Phase string; DaysTotal int }`.
- Produces (TS, generated): `BootStatus` in `ui/src/gen/wsmsg.ts`; `"sys.boot"` added to the `Topic` union.
- Consumes: `handlePub` (already topic-agnostic: `applyPub` + `broadcast`); `snapshotFrames`; mirror `session` init precedent (`api.go`).

- [ ] **Step 1: Add the topic const + allow-list entry** in `wsmsg.go`

In the `const (...)` topic block, under the `TopicSys*` group:

```go
	TopicSysHealth  Topic = "sys.health"
	TopicSysSession Topic = "sys.session"
	TopicSysEvents  Topic = "sys.events"
	TopicSysBoot    Topic = "sys.boot"
	TopicConfig     Topic = "config"
```

In `AllTopics`, add `TopicSysBoot: true` to the `TopicSys*` line.

- [ ] **Step 2: Add the `BootStatus` DTO** in `payloads.go`

```go
// BootStatus is the sys.boot snapshot: the engine's current boot phase, so the
// UI shows a neutral "preparing journal / connecting" banner during the pre-feed
// maintenance window instead of the red feed-disconnected strip. Snapshot-bearing
// (like SessionSnapshot): re-delivered to every new subscriber, also pushed as a
// delta on each transition. Phase is one of "sealing" | "connecting" | "ready".
// DaysTotal is the day count for the "sealing" phase (0 otherwise).
type BootStatus struct {
	Phase     string `json:"phase"`
	DaysTotal int    `json:"daysTotal,omitempty"`
}
```

- [ ] **Step 3: Add `"sys.boot"` to the tygo `Topic` union** in `engine/tygo.yaml`

In the `frontmatter` block's `export type Topic = ... | "sys.health" | "sys.session" | "sys.events"` line, add `| "sys.boot"`:

```
        | "sys.health" | "sys.session" | "sys.events" | "sys.boot"
```

- [ ] **Step 4: Add mirror state + cases** in `mirror.go`

In the `mirror` struct's `// system` group:

```go
	// system
	health  wsmsg.HealthSnapshot
	session wsmsg.SessionSnapshot
	boot    wsmsg.BootStatus
	events  []wsmsg.SysEvent // bounded recent
```

In `applyPub`'s `switch s.Topic`, add:

```go
	case wsmsg.TopicSysBoot:
		m.boot = s.Payload.(wsmsg.BootStatus)
```

In `snapshotFrames`'s `switch topic`, next to `TopicSysSession`:

```go
	case wsmsg.TopicSysBoot:
		out = append(out, staged{Topic: topic, Payload: m.boot})
```

- [ ] **Step 5: Initialize `m.boot` by mode** in `api.go`

Find where `m.session = wsmsg.SessionSnapshot{Mode: cfg.Mode, ...}` is set (~line 103) and add immediately after:

```go
	// Live boots start "connecting"; the boot goroutine advances the phase
	// (sealing → connecting → ready). Replay/demo have no maintenance window,
	// so seed "ready" — a mid-boot subscriber must never see the seal banner.
	if cfg.Mode == "live" {
		m.boot = wsmsg.BootStatus{Phase: "connecting"}
	} else {
		m.boot = wsmsg.BootStatus{Phase: "ready"}
	}
```

- [ ] **Step 6: Write the failing mirror test** in `mirror_test.go`

```go
func TestSysBootSnapshotAndPublish(t *testing.T) {
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10, 10)
	m.boot = wsmsg.BootStatus{Phase: "connecting"}

	frames := m.snapshotFrames(wsmsg.TopicSysBoot)
	if len(frames) != 1 || frames[0].Payload.(wsmsg.BootStatus).Phase != "connecting" {
		t.Fatalf("snapshot=%+v", frames)
	}

	m.applyPub(staged{Topic: wsmsg.TopicSysBoot, Payload: wsmsg.BootStatus{Phase: "sealing", DaysTotal: 3}})
	frames = m.snapshotFrames(wsmsg.TopicSysBoot)
	got := frames[0].Payload.(wsmsg.BootStatus)
	if got.Phase != "sealing" || got.DaysTotal != 3 {
		t.Fatalf("after publish=%+v", got)
	}
}
```

- [ ] **Step 7: Regenerate TS types and run Go tests**

Run: `cd engine && make gen-ts && go test ./internal/uihub/ -run SysBoot -v && make gen-ts-check`
Expected: `wsmsg.ts` now contains `BootStatus` and `"sys.boot"` in `Topic`; the Go test PASSes; `gen-ts-check` reports no drift.

- [ ] **Step 8: Commit**

```bash
git add engine/internal/uihub/wsmsg/ engine/tygo.yaml engine/internal/uihub/mirror.go engine/internal/uihub/api.go engine/internal/uihub/mirror_test.go ui/src/gen/wsmsg.ts
git commit -m "feat(uihub): add sys.boot snapshot topic + BootStatus payload"
```

---

### Task 4: Boot maintenance block rewrite + `sys.boot`/`sys.events` publishes

**Files:**
- Modify: `engine/cmd/etape/main.go` (live branch, `main.go:469-491`, plus the `sys.boot` "ready" publish in both live and replay feed-startup paths)

**Interfaces:**
- Consumes: `store.SizeStats`/`Vacuum`/`JournalFootprint`/`PendingSealDays`/`FormatStorageReport` (Task 1–2); `wsmsg.TopicSysBoot`/`BootStatus`/`TopicSysEvents`/`SysEvent` (Task 3); `hub.Publish(topic, key, payload)` (in scope as `hub`, `Run` already started at `main.go:412`, before this block).

**Verified:** `main.go` currently imports the parent package `github.com/earlisreal/eTape/engine/internal/uihub` but **not** `.../uihub/wsmsg` — add `"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"` to the import block (grouped with the other `internal/` imports) before using `wsmsg.TopicSysBoot` etc. `internal/store` and `fmt` are already imported.

- [ ] **Step 1: Add the `wsmsg` import**

In `main.go`'s import block, add `"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"` alongside the existing `"github.com/earlisreal/eTape/engine/internal/uihub"` line.

- [ ] **Step 2: Replace the live maintenance block** (`main.go:469-491`)

Replace everything from `if live {` down to `st.AppendSysEvent("boot", "engine up")` (inclusive) with:

```go
	if live {
		stats0, statsErr := st.SizeStats() // PRE-maintenance snapshot for the anomaly backstop
		if pending, err := st.PendingSealDays(); err == nil && len(pending) > 0 {
			hub.Publish(wsmsg.TopicSysBoot, "", wsmsg.BootStatus{Phase: "sealing", DaysTotal: len(pending)})
		}
		if n, err := st.PruneJournal(cfg.Store.RetentionDays); err == nil && n > 0 {
			log.Info("pruned journal", "rows", n)
		}
		st.Flush() // drain the queued prune sys_event so it doesn't race the seal's write transaction
		if sum, err := st.SealJournalDays(); err != nil {
			log.Error("seal journal", "err", err)
			detail := fmt.Sprintf("journal seal error: %v", err)
			st.AppendSysEvent("retention", detail)
			hub.Publish(wsmsg.TopicSysEvents, "", wsmsg.SysEvent{Kind: "retention", Detail: detail, Level: "danger"})
		} else if sum.Days > 0 || sum.Failed > 0 {
			log.Info("sealed journal", "days", sum.Days, "chunks", sum.Chunks, "rows", sum.Rows,
				"failed", sum.Failed, "mbBefore", sum.BytesBefore>>20, "mbAfter", sum.BytesAfter>>20)
			st.AppendSysEvent("retention", fmt.Sprintf(
				"sealed %d day(s): %d rows → %d chunks (%d MB → %d MB); %d day(s) left raw",
				sum.Days, sum.Rows, sum.Chunks, sum.BytesBefore>>20, sum.BytesAfter>>20, sum.Failed))
		}
		st.Flush() // drain queued sys_events so no writer tx races a possible backstop VACUUM
		if statsErr == nil && stats0.NeedsBackstopVacuum() {
			log.Warn("backstop vacuum: cross-day free-page accumulation",
				"freeMB", stats0.FreeBytes()>>20, "fileMB", stats0.FileBytes()>>20)
			detail := fmt.Sprintf("backstop vacuum: %d MB free across days, compacting", stats0.FreeBytes()>>20)
			st.AppendSysEvent("retention", detail)
			hub.Publish(wsmsg.TopicSysEvents, "", wsmsg.SysEvent{Kind: "retention", Detail: detail, Level: "warn"})
			if err := st.Vacuum(); err != nil {
				log.Error("backstop vacuum", "err", err)
				failDetail := fmt.Sprintf("backstop vacuum failed: %v", err)
				st.AppendSysEvent("retention", failDetail)
				hub.Publish(wsmsg.TopicSysEvents, "", wsmsg.SysEvent{Kind: "retention", Detail: failDetail, Level: "danger"})
			} else {
				log.Info("backstop vacuum done")
			}
		}
		if stats1, err := st.SizeStats(); err == nil { // POST-maintenance telemetry
			chunkBytes, rawRows, _ := st.JournalFootprint()
			advise := stats1.AdviseVacuum()
			report := store.FormatStorageReport(stats1, chunkBytes, rawRows, advise)
			st.AppendSysEvent("storage", report)
			level := ""
			if advise {
				level = "warn" // surfaces as a toast via connectEventToasts
			}
			hub.Publish(wsmsg.TopicSysEvents, "", wsmsg.SysEvent{Kind: "storage", Detail: report, Level: level})
		}
		st.AppendSysEvent("boot", "engine up")
		hub.Publish(wsmsg.TopicSysBoot, "", wsmsg.BootStatus{Phase: "connecting"})
		dropWG.Add(1)
		go watchDroppedUpdates(ctx, &dropWG, core, st)
		// ... (existing opend.New / fd.Run / pipe / runSealScheduler startup, unchanged) ...
```

- [ ] **Step 3: Publish `ready` after the live feed goroutines start**

At the end of the `if live {` block, immediately after `runSealScheduler` is launched (`main.go:504-505`) — the feed goroutines (`client.Run`, `fd.Run`, `pipe`) are now running:

```go
		sealSchedWG.Add(1)
		go runSealScheduler(ctx, &sealSchedWG, st, clock.System{}, log)
		hub.Publish(wsmsg.TopicSysBoot, "", wsmsg.BootStatus{Phase: "ready"})
```

- [ ] **Step 4: Publish `ready` in the replay/demo path**

**Verified:** the `if live {` block runs `main.go:469–570`; its pairing `} else {` is at **line 571**, and the replay branch's own feed goroutine starts at **line 574** (`fd := replay.NewFeed(...)` then `go func() { _ = fd.Run(ctx) }()`), followed by the pipe goroutine at line 576 and a `log.Info("engine up (replay)", ...)` near line 582, before the branch's closing `}` at line 583. Add the publish at the tail of that branch, after the existing `log.Info("engine up (replay)", ...)` line and before the closing `}`:

```go
		hub.Publish(wsmsg.TopicSysBoot, "", wsmsg.BootStatus{Phase: "ready"})
```

(The mirror is already seeded `ready` for replay in Task 3 Step 5; this is the belt-and-suspenders delta so any already-connected client transitions promptly.)

- [ ] **Step 5: Build + vet**

Run: `cd engine && go build ./... && go vet ./cmd/etape/ ./internal/store/ ./internal/uihub/`
Expected: no errors. (No unit test drives `boot()` directly — see the Verification section for the `-demo`/temp-DB smoke that exercises the phase ordering.)

- [ ] **Step 6: Commit**

```bash
git add engine/cmd/etape/main.go
git commit -m "feat(engine): remove boot VACUUM, add backstop + storage telemetry + sys.boot phases"
```

---

### Task 5: `etape -vacuum` manual reclaim mode

**Files:**
- Modify: `engine/cmd/etape/main.go` (register `-vacuum`; refuse in the `ErrAlreadyRunning` branch; branch into `runVacuumMode` after the lock is acquired, before the normal store open)
- Create: `engine/cmd/etape/vacuum.go`
- Test: `engine/cmd/etape/vacuum_test.go`

**Interfaces:**
- Produces: `func runVacuumMode(dbPath string, cfg config.Config, log *slog.Logger) int` — opens the store, runs `PruneJournal → Flush → SealJournalDays → Flush → VacuumIfNeeded`, prints file size before/after, returns an exit code (0 ok, 1 on any step error).
- Consumes: `store.Open`, `PruneJournal`, `SealJournalDays`, `Flush`, `VacuumIfNeeded` (demoted; the "nothing worth reclaiming" early-out), `SizeStats` (for before/after sizes); `singleinstance.ErrAlreadyRunning`.

- [ ] **Step 1: Register the flag** in `boot()` (`main.go`, near the other flags ~line 87)

```go
	vacuum := flag.Bool("vacuum", false, "run one-shot journal maintenance (prune+seal+vacuum) then exit; refuses if an engine is running")
```

- [ ] **Step 2: Refuse when an instance is running** — in the `errors.Is(err, singleinstance.ErrAlreadyRunning)` branch (`main.go:232-241`), at the top of the branch body:

```go
	if errors.Is(err, singleinstance.ErrAlreadyRunning) {
		if *vacuum {
			log.Error("etape -vacuum: engine is running; stop it before running maintenance")
			return 1, false, nil // must NOT open the browser to the running instance
		}
		log.Info("eTape is already running; opening it instead", "addr", cfg.UIHub.Addr())
		// ... existing browser-open + return ...
	}
```

- [ ] **Step 3: Branch into vacuum mode after the lock is held** — after `log.Info("single-instance lock acquired", ...)` (`main.go:247`), before the store open at line 275:

```go
	if *vacuum {
		code := runVacuumMode(dbPath, cfg, log)
		return code, false, nil // deferred releaseLock() runs on return
	}
```

- [ ] **Step 4: Write the failing test** `vacuum_test.go`

This test lives in `package main`, a different package from `store`, so it cannot reach `store`'s unexported `journalInsertSQL`/`s.db` (those are only usable inside Task 1's `retention_test.go`, same package). Seed the temp DB instead with `demojournal.Generate(dbPath, day)` — the same generator `main.go`'s `-demo` flag already uses — which writes a full synthetic day of raw journal rows to a fresh DB file and needs no `Store` handle open concurrently.

```go
package main

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/demojournal"
	"github.com/earlisreal/eTape/engine/internal/store"
)

func TestRunVacuumModeHappyPath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "etape.db")
	// demojournal.Generate uses the real wall clock to open its own Store and
	// writes raw (unsealed) rows for the given day, then closes — so today's
	// real ET date is always strictly after "2026-07-08", and runVacuumMode's
	// own real-clock SealJournalDays call below will seal it deterministically
	// regardless of when this test runs.
	if err := demojournal.Generate(dbPath, "2026-07-08"); err != nil {
		t.Fatalf("generate demo journal: %v", err)
	}

	cfg := config.Default()
	cfg.Store.RetentionDays = 30
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if code := runVacuumMode(dbPath, cfg, log); code != 0 {
		t.Fatalf("exit=%d want 0", code)
	}

	// After the run the day is sealed: no raw rows, chunks present.
	st2, err := store.Open(store.Options{Path: dbPath, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	_, rawRows, err := st2.JournalFootprint()
	if err != nil {
		t.Fatal(err)
	}
	if rawRows != 0 {
		t.Fatalf("expected sealed (0 raw rows), got %d", rawRows)
	}
}
```

- [ ] **Step 5: Run test to verify it fails**

Run: `cd engine && go test ./cmd/etape/ -run RunVacuumMode -v`
Expected: FAIL — `runVacuumMode` undefined.

- [ ] **Step 6: Implement** `engine/cmd/etape/vacuum.go`

```go
package main

import (
	"log/slog"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// runVacuumMode is the `etape -vacuum` one-shot maintenance mode: no uihub, no
// browser, no feed. It runs the exact boot-maintenance sequence (prune → seal →
// vacuum), so it also opportunistically compresses anything pending, then exits.
// The caller must already hold the single-instance lock (so no live engine can
// race the VACUUM). Returns a process exit code.
func runVacuumMode(dbPath string, cfg config.Config, log *slog.Logger) int {
	st, err := store.Open(store.Options{
		Path: dbPath, Clock: clock.System{},
		FlushInterval: time.Duration(cfg.Store.FlushMs) * time.Millisecond,
	})
	if err != nil {
		log.Error("vacuum: open store", "err", err)
		return 1
	}
	defer st.Close()

	before, _ := st.SizeStats()
	log.Info("vacuum: starting", "fileMB", before.FileBytes()>>20, "freeMB", before.FreeBytes()>>20)

	if n, err := st.PruneJournal(cfg.Store.RetentionDays); err != nil {
		log.Error("vacuum: prune", "err", err)
		return 1
	} else if n > 0 {
		log.Info("vacuum: pruned", "rows", n)
	}
	st.Flush()
	if sum, err := st.SealJournalDays(); err != nil {
		log.Error("vacuum: seal", "err", err)
		return 1
	} else if sum.Days > 0 || sum.Failed > 0 {
		log.Info("vacuum: sealed", "days", sum.Days, "rows", sum.Rows, "failed", sum.Failed)
	}
	st.Flush()
	if ran, err := st.VacuumIfNeeded(); err != nil {
		log.Error("vacuum: VACUUM", "err", err)
		return 1
	} else if !ran {
		log.Info("vacuum: nothing to reclaim", "freeMB", before.FreeBytes()>>20, "thresholdMB", 64)
	}

	after, _ := st.SizeStats()
	log.Info("vacuum: done", "fileMB", after.FileBytes()>>20, "freeMB", after.FreeBytes()>>20,
		"reclaimedMB", (before.FileBytes()-after.FileBytes())>>20)
	return 0
}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `cd engine && go test ./cmd/etape/ -run RunVacuumMode -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 8: Commit**

```bash
git add engine/cmd/etape/main.go engine/cmd/etape/vacuum.go engine/cmd/etape/vacuum_test.go
git commit -m "feat(engine): add etape -vacuum one-shot maintenance mode"
```

---

### Task 6: UI boot-status banner + red-banner gate

**Files:**
- Create: `ui/src/data/BootStore.ts`
- Modify: `ui/src/data/registry.ts`
- Modify: `ui/src/chrome/panels/registry.tsx` (`ConnectionStatusPanel` topics)
- Create: `ui/src/chrome/BootStatusBanner.tsx`
- Modify: `ui/src/chrome/FeedStatusBanner.tsx`
- Modify: `ui/src/chrome/AppShell.tsx`
- Test: `ui/src/chrome/BootStatusBanner.test.tsx`

**Interfaces:**
- Consumes: generated `BootStatus` + `"sys.boot"` topic (Task 3); `ReactStore`; `useSyncExternalStore`; theme `palette`.
- Produces: `BootStore` (`boot` in `Stores`); `BootStatusBanner({ boot })`; `FeedStatusBanner` gains a `boot` prop.

- [ ] **Step 1: Create `BootStore.ts`** (clone of `SessionStore.ts`)

```ts
import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, BootStatus } from "../wire/contract";

// BootStatus.phase generates as `string`; annotate the literal union here.
export type BootState = Omit<BootStatus, "phase"> & { phase: "connecting" | "sealing" | "ready" };

export class BootStore extends ReactStore<BootState> {
  // Seeds "connecting" (never "ready"): sys.boot is snapshot-bearing, so on a
  // fresh page load the real phase arrives within ~1 frame. "connecting" keeps
  // the red FeedStatusBanner suppressed until the engine confirms readiness,
  // and only briefly shows a neutral "Connecting…" strip — never a false red.
  constructor() {
    super({ phase: "connecting" });
  }
  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.topic !== "sys.boot") return;
    this.set(m.payload as BootState); // snapshot & delta are both full replaces
  }
}
```

- [ ] **Step 2: Wire the store into `registry.ts`**

Import + add to `Stores`, `makeStores`, and `routeToStore`:

```ts
import { BootStore } from "./BootStore";
// ... in interface Stores:
  boot: BootStore;
// ... in makeStores():
    boot: new BootStore(),
// ... in routeToStore(), alongside the sys.* cases:
    case "sys.boot": stores.boot.apply(m); return;
```

- [ ] **Step 3: Add `"sys.boot"` to the always-subscribed topic union** in `ui/src/chrome/panels/registry.tsx`

Find the `ConnectionStatusPanel` registration (`topics: ["sys.health", "sys.events", "sys.session"]`) and add `"sys.boot"`:

```ts
    topics: ["sys.health", "sys.events", "sys.session", "sys.boot"],
```

(AppShell subscribes the union of every catalog panel's topics up front, so this guarantees `sys.boot` is subscribed regardless of which panels are mounted — the banner then receives the snapshot on connect.)

- [ ] **Step 4: Write the failing component test** `BootStatusBanner.test.tsx`

```tsx
import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { BootStatusBanner } from "./BootStatusBanner";
import { BootStore } from "../data/BootStore";
import { ThemeProvider } from "./ThemeProvider";

function withStore(phase: "connecting" | "sealing" | "ready", daysTotal = 0) {
  const boot = new BootStore();
  boot.apply({ kind: "snapshot", topic: "sys.boot", payload: { phase, daysTotal } } as any);
  return boot;
}

describe("BootStatusBanner", () => {
  it("shows a sealing message", () => {
    render(<ThemeProvider><BootStatusBanner boot={withStore("sealing", 2)} /></ThemeProvider>);
    expect(screen.getByTestId("boot-status-banner").textContent).toMatch(/compressing 2 days/i);
  });
  it("shows a connecting message", () => {
    render(<ThemeProvider><BootStatusBanner boot={withStore("connecting")} /></ThemeProvider>);
    expect(screen.getByTestId("boot-status-banner").textContent).toMatch(/connecting to market data/i);
  });
  it("hides when ready", () => {
    render(<ThemeProvider><BootStatusBanner boot={withStore("ready")} /></ThemeProvider>);
    expect(screen.queryByTestId("boot-status-banner")).toBeNull();
  });
});
```

- [ ] **Step 5: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/BootStatusBanner.test.tsx`
Expected: FAIL — module `./BootStatusBanner` not found.

- [ ] **Step 6: Implement `BootStatusBanner.tsx`** (invoke the frontend-design skill; keep it neutral/info-toned)

```tsx
import { useSyncExternalStore } from "react";
import type { BootStore } from "../data/BootStore";
import { useTheme } from "./ThemeProvider";

// Neutral, self-gating strip shown during the pre-feed boot window (journal seal
// + connect). Deliberately NOT the red danger tone of FeedStatusBanner: this is
// expected daily maintenance, not a failure. Hidden once the engine is ready.
export function BootStatusBanner({ boot }: { boot: BootStore }): JSX.Element | null {
  const { palette } = useTheme();
  const s = useSyncExternalStore(boot.subscribe.bind(boot), boot.getSnapshot.bind(boot));
  if (s.phase === "ready") return null;

  let text: string;
  if (s.phase === "sealing") {
    const n = s.daysTotal ?? 1;
    text = n > 1
      ? `Preparing journal — compressing ${n} days…`
      : `Preparing journal — compressing 1 day (~15 s)…`;
  } else {
    text = "Connecting to market data…";
  }

  return (
    <div
      data-testid="boot-status-banner"
      className="serif"
      style={{
        display: "flex", alignItems: "center", gap: 6,
        padding: "5px 12px", fontSize: 12, color: palette.textMuted,
        background: palette.surface,
        borderBottom: `1px solid ${palette.border}`,
      }}
    >
      <span aria-hidden="true">⏳</span>
      {text}
    </div>
  );
}
```

> **Verified palette tokens** (`ui/src/render/palette.ts`): there is **no** `muted` field. Use the neutral tokens `textMuted` (secondary text), `surface` (header/control background), and `border` (hairlines) as above — never `palette.warn`/`palette.danger` (those are reserved for the red feed-failure tone). `accent`/`neutral` are available if a subtle tint is wanted. Match the padding/typography of the sibling banners (`ReplayBanner`, `FeedStatusBanner`); confirm the final look with the frontend-design skill.

- [ ] **Step 7: Gate `FeedStatusBanner` on `phase === "ready"`**

Add a `boot: BootStore` prop and short-circuit before the existing checks:

```tsx
import type { BootStore } from "../data/BootStore";
// ...
export function FeedStatusBanner(
  { health, boot, engineState, onOpenConnection }:
  { health: HealthStore; boot: BootStore; engineState: ConnState; onOpenConnection: () => void },
): JSX.Element | null {
  const { palette } = useTheme();
  const state = useSyncExternalStore(health.subscribe.bind(health), health.getSnapshot.bind(health));
  const bootState = useSyncExternalStore(boot.subscribe.bind(boot), boot.getSnapshot.bind(boot));

  if (bootState.phase !== "ready") return null; // expected boot maintenance — the neutral BootStatusBanner owns this window
  if (engineState !== "open") return null;
  // ... existing engine-moomoo down check + render, unchanged ...
}
```

- [ ] **Step 8: Mount in `AppShell.tsx`** (banner stack ~line 470)

Add `BootStatusBanner` at the top of the stack and pass `boot` to `FeedStatusBanner`:

```tsx
        <BootStatusBanner boot={stores.boot} />
        <ReplayBanner session={stores.session} engineState={engineState} onGoLive={async () => { /* unchanged */ }} />
        <FeedStatusBanner health={stores.health} boot={stores.boot} engineState={engineState} onOpenConnection={onOpenConnection} />
```

Add the import: `import { BootStatusBanner } from "./BootStatusBanner";`

- [ ] **Step 9: Run tests + typecheck + build**

Run: `cd ui && npx vitest run src/chrome/BootStatusBanner.test.tsx && npx tsc --noEmit && npm run build`
Expected: banner tests PASS; no type errors; build clean.

- [ ] **Step 10: Commit**

```bash
git add ui/src/data/BootStore.ts ui/src/data/registry.ts ui/src/chrome/panels/registry.tsx ui/src/chrome/BootStatusBanner.tsx ui/src/chrome/FeedStatusBanner.tsx ui/src/chrome/AppShell.tsx ui/src/chrome/BootStatusBanner.test.tsx
git commit -m "feat(ui): boot-status banner + gate feed-disconnected banner on ready"
```

---

### Task 7: Runbook note + commit the page-reuse experiment

**Files:**
- Modify: `docs/superpowers/specs/2026-07-11-journal-storage-optimization-design.md` (or a short companion doc) — operator runbook pointer
- Add (commit as-is): `engine/internal/store/pagereuse_experiment_test.go`

- [ ] **Step 1: Add a runbook note** to the 2026-07-11 storage spec (a short paragraph):

> **Operator note (2026-07-12 revision):** the engine no longer vacuums on boot. To reclaim disk after lowering `retention_days`, after a `storage` sys_event advises it, or during anomaly investigation, stop the engine and run `etape -vacuum` once. It refuses to run while an engine holds the lock. See `docs/superpowers/specs/2026-07-12-journal-vacuum-boot-revision-design.md`.

- [ ] **Step 2: Commit the experiment + runbook**

The build-tagged experiment file is excluded from normal `go test` and needs a prod DB copy to run; committing it keeps the pre-code gate reproducible.

```bash
git add engine/internal/store/pagereuse_experiment_test.go docs/superpowers/specs/2026-07-11-journal-storage-optimization-design.md
git commit -m "docs+test: journal-vacuum runbook note + page-reuse experiment harness"
```

---

## Verification

Run all from the repo root unless noted.

1. **Engine unit tests:**
   `cd engine && go test ./internal/store/ ./internal/uihub/ ./cmd/etape/`
   Expected: all PASS (Task 1/2/3/5 tests).

2. **Type-generation gate:**
   `cd engine && make gen-ts-check`
   Expected: no drift — `ui/src/gen/wsmsg.ts` matches the committed output.

3. **Build + vet:**
   `cd engine && go build ./... && go vet ./...`

4. **UI:**
   `cd ui && npx vitest run src/chrome/BootStatusBanner.test.tsx && npx tsc --noEmit && npm run build`
   (Per the UI canvas-test quirk in project memory, run any canvas-touching test files individually; the banner tests here are DOM-only.)

5. **Boot-phase smoke (`-demo`, no OpenD needed):**
   Build and run `etape -demo`; open the UI. Expected: no `sealing` banner (demo skips maintenance), the neutral banner is absent almost immediately (phase `ready`), and the red `FeedStatusBanner` never flashes during startup.

6. **Live seal-window smoke (temp DB):**
   Seed a temp DB with ≥1 older raw day (reuse `genjournal` or the demo generator against distinct days), point a live boot at it with `--config` overriding `store.db_path`, and boot with a browser attached. Expected: the neutral "Preparing journal — compressing N day(s)…" banner appears during the seal, transitions to "Connecting to market data…", then disappears at `ready`; a `storage` sys_event is present in the Connection panel; total boot-to-feed is seal-time only (no VACUUM wait). Confirm the red feed banner stays hidden until `ready`.

7. **`etape -vacuum` behavior:**
   - Stopped engine: `etape -vacuum` seals/prunes/vacuums a temp DB, logs before/after sizes, exits 0. On a DB with < 64 MB freelist it logs "nothing to reclaim" and exits 0.
   - Running engine: start `etape` (holding the lock), then `etape -vacuum` → logs "engine is running; stop it before running maintenance", exits 1, opens no browser.

8. **Deploy check (manual, outside RTH):** on the real `~/.eTape/etape.db`, record `PRAGMA page_count`/`freelist_count` before and after the first live boot with this build. Expected: the one-time mass seal runs, **no** VACUUM, an advisory `storage` toast appears; free space reabsorbs over ~2 days of live writes (or run `etape -vacuum` once to reclaim immediately).

9. **Page-reuse gate (already PASSED — reproducible):**
   `cd engine && go test -tags pagereuse -run TestPageReusePlateau ./internal/store -v -timeout 90m` (requires a prod DB copy; documented result: plateau at 1.0×).

## Self-Review notes (spec coverage)

- §1 boot path → Task 4. §2 store API → Task 1/2 (thresholds kept private via `SizeStats` predicate methods; `VacuumIfNeeded`/64 MB kept, demoted). §3 `etape -vacuum` + lock refusal → Task 5. §4 storage telemetry → Task 2 + Task 4. §7 `sys.boot` topic/banner + red-banner gate + `sys.events` toast synergy + seal-failure path (phase still advances; error via `sys.events`) → Task 3/4/6. §Verification 1 (experiment) → committed in Task 7; §Verification 2–5 → Verification section. Deploy/first-boot (§6) → Verification step 8.
- Deviation from the spec's illustrative pseudocode, by explicit decision: seal progress is a single up-front `sealing` update (`PendingSealDays`), not per-day, so `seal.go` is unchanged; `formatStorageReport`/`vacuumBackstopThreshold` are exposed to `cmd/etape` as an exported `FormatStorageReport` free function and `SizeStats.NeedsBackstopVacuum()`/`.AdviseVacuum()` methods (keeping the threshold vars private).

## Execution handoff

Per project convention, execute via **superpowers:subagent-driven-development in an isolated git worktree** (fresh subagent per task, review between tasks). Tasks 1→2→3 are ordered (2 depends on 1's `SizeStats`; 4 depends on 1–3; 5 depends on 1; 6 depends on 3's generated types). Task 7 is independent. At the end, with tests/build green and no unresolved review issues, ask Earl before merging into local `main`.
