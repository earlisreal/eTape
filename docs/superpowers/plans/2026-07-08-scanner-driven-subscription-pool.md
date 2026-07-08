# Scanner-Driven Subscription Pool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Auto-subscribe the top-10 filtered Scanner symbols at watch tier, sticky for a 20:00-ET-anchored trading day (cap 30), so any mover clicked mid-session already has session-long tick history — and delete the watchlist/`--watch`/`--focus` boot machinery entirely.

**Architecture:** A new pure-logic `scan.Pool` type (own file, no I/O) held by `scan.Poller`. Each `pollOnce`, after `rankRows()` produces the filtered rows, the poller feeds the top-10 symbols to the pool; the pool returns a delta (admissions, evictions, first-admissions-needing-backfill) and the poller executes it via `feed.Ensure`/`feed.Release` calls (demand id `scan:<symbol>`, `feed.WatchDemand` shape) plus an async per-symbol deep-history backfill on first admission — exactly parallel to how it already calls `Publisher.Publish`. The feed handle and backfill trigger are injected at construction and are nil-tolerant (nil ⇒ pool disabled, for tests/replay).

**Tech Stack:** Go (engine), stdlib `testing` (no testify), `sync/atomic` for the lock-free pool-membership snapshot, `google.golang.org/protobuf` (existing). moomoo OpenD feed via the existing `feed`/`feed/opend`/`backfill` packages.

## Global Constraints

- Module import root: `github.com/earlisreal/eTape/engine`. The `feed` package is `github.com/earlisreal/eTape/engine/internal/feed` (package name `feed`); do not confuse it with the already-imported `feed/opend`.
- Tests: stdlib `testing` only — no testify, no third-party assertion libs. Failure messages use `t.Fatalf("...: %+v, want %v", got, want)`. Hand-rolled fakes/spies, table-driven where natural.
- Pool sizing: `poolTrackN = 10`, `poolCap = 30` — compile-time constants in the `scan` package (mirror the existing `const` block style at `scan.go:313-316`). **No config keys** (Earl explicitly declined them).
- Pool day boundary: **20:00 ET → 20:00 ET**. One overnight → pre-market → RTH → after-hours cycle shares a pool day. Boundary is computed from poll time (never wall-clock randomness).
- Demand shape: `feed.WatchDemand(id, symbol)` (TICKER + K_1M, 2 quota slots, not eviction-proof). Demand id: exactly `"scan:" + symbol`.
- **Delta execution order within one poll: Release BEFORE Ensure.** On a pool-day reset a symbol can appear in both `Evicted` (old-day clear) and `Admitted` (new-day top-10); releasing first then ensuring yields the correct final state (subscribed). Reversing the order would leave it released.
- **Shutdown invariant (do not break):** on-admission backfill makes the scan poller a *transitive store-writer* (backfill → `md.Core.Seed*` → `core.Updates()` → `st.Archive*`). `main.go`'s ordered shutdown closes `st` only after every store-writer is joined (see the comment block at `main.go:277-299`). The scan poller must therefore be joined (a new `scanWG`) **before** `backfillWG.Wait()` at `main.go:304`, so no `backfillWG.Add` can race a `backfillWG.Wait` and no `Seed*` runs after `st.Close()`.
- nil feed ⇒ pool disabled: `scan.Poller.updatePool` is a no-op and `PoolSymbols()` returns `nil` when the injected feed is nil (tests, replay, any non-live wiring). Production always injects a real `*opend.OpenDFeed` (never a typed-nil pointer).

---

> **Line-number caveat for the executor:** All `main.go` line numbers are from the current unmodified file. Task 3 inserts ~12 lines into `main.go`, so cited line numbers in Tasks 4–6 will have drifted by the time you reach them. Every edit is anchored to *code content* (e.g. "the `symbols := backfillSymbols(...)` line", "the `backfillSymbols` function"), so match on content, not line number.

## File Structure

**New files:**
- `engine/internal/scan/pool.go` — `Pool` type, `Delta`, `poolTrackN`/`poolCap` constants, admission/eviction/day-reset logic. Pure, no I/O.
- `engine/internal/scan/pool_test.go` — table/scenario unit tests for `Pool`.

**Modified files:**
- `engine/internal/session/session.go` — add `PoolDay(t time.Time) int64` (20:00-ET-anchored day key).
- `engine/internal/session/session_test.go` — add `TestPoolDay`.
- `engine/internal/scan/scan.go` — add `demandFeed` interface, `feed`/`backfill`/`pool`/`poolSyms` fields, new `New` signature, `updatePool`, `PoolSymbols`, `scanDemandID`; call `updatePool` in `pollOnce`.
- `engine/internal/scan/scan_test.go` — update `newTestPoller` for the new `New` signature; add `spyFeed` + poller-level tests.
- `engine/cmd/etape/main.go` — inject feed + backfill trigger into the poller; add `scanWG` (declare + wait); switch news set to pool ∪ live demands; delete `--watch`/`--focus` flags, boot `Ensure` loops, `backfillSymbols`, `splitCSV`.
- `engine/cmd/etape/news_symbols.go` — rewrite `newsSymbols` to `(pool, liveDemands []string)`.
- `engine/cmd/etape/news_symbols_test.go` — rewrite for the 2-arg signature.
- `engine/internal/feed/feed.go` — delete `FocusedDemand`.
- `engine/internal/feed/feed_test.go` — drop the `FocusedDemand` assertions from `TestDemandProfiles`.
- `engine/internal/feed/opend/subman_test.go` — inline the focused demand at lines 170, 191 (replace `feed.FocusedDemand(...)`).
- `engine/internal/config/config.go` — delete `Feed.Watchlist`.
- `engine/internal/config/config_test.go` — drop the `watchlist` fixture line + assertion.

---

### Task 1: `session.PoolDay` — 20:00-ET-anchored day key

**Files:**
- Modify: `engine/internal/session/session.go` (add after `DayMs`, near line 126)
- Test: `engine/internal/session/session_test.go`

**Interfaces:**
- Consumes: existing `session.Loc() *time.Location` (`session.go:65`).
- Produces: `func PoolDay(t time.Time) int64` — a stable integer key identifying the 20:00-ET → 20:00-ET trading day containing `t`. Equal keys ⇔ same pool day. Value is the Unix second of the anchoring 20:00 ET instant.

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/session/session_test.go`:

```go
func TestPoolDay(t *testing.T) {
	et := func(y int, mo time.Month, d, h, mi int) time.Time {
		return time.Date(y, mo, d, h, mi, 0, 0, Loc())
	}
	// The pool day anchored at 2026-07-07 20:00 ET spans 2026-07-07 20:00 ET
	// through 2026-07-08 20:00 ET (overnight -> pre-market -> RTH -> after-hours).
	anchor := PoolDay(et(2026, 7, 7, 20, 0))
	sameDay := []time.Time{
		et(2026, 7, 7, 20, 0),  // boundary start (overnight)
		et(2026, 7, 7, 23, 0),  // overnight, same calendar date
		et(2026, 7, 8, 3, 0),   // overnight, next calendar date
		et(2026, 7, 8, 9, 30),  // RTH open
		et(2026, 7, 8, 16, 0),  // RTH close
		et(2026, 7, 8, 19, 59), // last minute before the next boundary
	}
	for _, ts := range sameDay {
		if got := PoolDay(ts); got != anchor {
			t.Fatalf("PoolDay(%s)=%d, want %d (same pool day)", ts, got, anchor)
		}
	}
	// 20:00 ET on 2026-07-08 opens a NEW pool day.
	if next := PoolDay(et(2026, 7, 8, 20, 0)); next == anchor {
		t.Fatalf("PoolDay must roll over at 20:00 ET: got %d == %d", next, anchor)
	}
	// 19:59 vs 20:00 on the same date are different pool days.
	if PoolDay(et(2026, 7, 8, 19, 59)) == PoolDay(et(2026, 7, 8, 20, 0)) {
		t.Fatalf("PoolDay must differ across the 20:00 ET boundary")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/session/ -run TestPoolDay -v`
Expected: FAIL — `undefined: PoolDay`.

- [ ] **Step 3: Implement `PoolDay`**

Add to `engine/internal/session/session.go` (after the `DayMs` function, ~line 127):

```go
// PoolDay returns a stable integer key identifying the 20:00-ET-anchored
// trading day containing t: the pool day runs 20:00 ET -> 20:00 ET, so one
// overnight -> pre-market -> RTH -> after-hours cycle shares a key. The key is
// the Unix second of the anchoring 20:00 ET instant. Used by the scanner's
// subscription pool to sticky-subscribe a mover for the whole trading cycle
// and reset at 20:00 ET.
func PoolDay(t time.Time) int64 {
	et := t.In(Loc())
	y, m, d := et.Date()
	anchor := time.Date(y, m, d, 20, 0, 0, 0, Loc())
	if et.Before(anchor) {
		anchor = anchor.AddDate(0, 0, -1)
	}
	return anchor.Unix()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/session/ -run TestPoolDay -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/session/session.go engine/internal/session/session_test.go
git commit -m "feat(session): add PoolDay 20:00-ET-anchored trading-day key"
```

---

### Task 2: `scan.Pool` — sticky top-N subscription pool (pure logic)

**Files:**
- Create: `engine/internal/scan/pool.go`
- Test: `engine/internal/scan/pool_test.go`

**Interfaces:**
- Consumes: `session.PoolDay(now)` (Task 1).
- Produces:
  - `const poolTrackN = 10`, `const poolCap = 30`
  - `type Delta struct { Admitted []string; Backfill []string; Evicted []string }`
  - `func NewPool() *Pool`
  - `func (p *Pool) Update(ranked []string, now time.Time) Delta` — feed the current filtered rank symbols (rank order); returns the demand delta. `Admitted` = newly pooled (→ Ensure), in rank order. `Backfill` = subset of `Admitted` on their first admission this pool day (→ seed). `Evicted` = removed (→ Release), sorted.
  - `func (p *Pool) Symbols() []string` — current members, sorted (for the news set).

- [ ] **Step 1: Write the failing tests**

Create `engine/internal/scan/pool_test.go`:

```go
package scan

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

func et(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, session.Loc())
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestPoolAdmitsTopNAndBackfillsFirstTime(t *testing.T) {
	p := NewPool()
	d := p.Update([]string{"US.A", "US.B", "US.C"}, et(2026, 7, 8, 9, 30))
	want := []string{"US.A", "US.B", "US.C"}
	if !reflect.DeepEqual(d.Admitted, want) {
		t.Fatalf("Admitted=%v, want %v", d.Admitted, want)
	}
	if !reflect.DeepEqual(d.Backfill, want) {
		t.Fatalf("Backfill=%v, want %v (all first-time)", d.Backfill, want)
	}
	if len(d.Evicted) != 0 {
		t.Fatalf("Evicted=%v, want none", d.Evicted)
	}
}

func TestPoolStickyWhenSymbolDropsOffTopN(t *testing.T) {
	p := NewPool()
	base := et(2026, 7, 8, 9, 30)
	p.Update([]string{"US.A", "US.B"}, base)
	d := p.Update([]string{"US.B"}, base.Add(time.Minute)) // A drops out of the top-N
	if len(d.Admitted) != 0 || len(d.Evicted) != 0 {
		t.Fatalf("no delta expected when a member drops off-board: %+v", d)
	}
	if !containsStr(p.Symbols(), "US.A") {
		t.Fatalf("A must stay pooled (sticky): %v", p.Symbols())
	}
}

func TestPoolCapEvictsLongestOffBoardAndNeverEvictsTopN(t *testing.T) {
	p := NewPool()
	base := et(2026, 7, 8, 9, 30)
	// Fill exactly cap (30) members over 3 polls of 10 distinct symbols each,
	// with strictly increasing timestamps so off-board age is deterministic.
	for g := 0; g < 3; g++ {
		top := make([]string, 10)
		for i := range top {
			top[i] = fmt.Sprintf("US.S%02d", g*10+i)
		}
		p.Update(top, base.Add(time.Duration(g)*time.Minute))
	}
	if len(p.Symbols()) != poolCap {
		t.Fatalf("pool should be full at %d, got %d", poolCap, len(p.Symbols()))
	}
	// New poll: top-N = {S30 (new), S00 (oldest off-board, but now back on-board)}.
	// Admitting S30 forces one eviction. S00 is protected (in top-N this poll),
	// so the victim is the next-oldest off-board member: S01.
	d := p.Update([]string{"US.S30", "US.S00"}, base.Add(3*time.Minute))
	if !reflect.DeepEqual(d.Admitted, []string{"US.S30"}) {
		t.Fatalf("Admitted=%v, want [US.S30]", d.Admitted)
	}
	if !reflect.DeepEqual(d.Evicted, []string{"US.S01"}) {
		t.Fatalf("Evicted=%v, want [US.S01] (longest off-board, S00 protected)", d.Evicted)
	}
	if containsStr(d.Evicted, "US.S00") {
		t.Fatalf("a current top-N member must never be evicted: %v", d.Evicted)
	}
}

func TestPoolResetsAt2000ETBoundary(t *testing.T) {
	p := NewPool()
	p.Update([]string{"US.A", "US.B"}, et(2026, 7, 8, 19, 0)) // before 20:00 ET
	d := p.Update([]string{"US.C"}, et(2026, 7, 8, 20, 0))    // crosses the boundary
	if !reflect.DeepEqual(d.Evicted, []string{"US.A", "US.B"}) {
		t.Fatalf("Evicted=%v, want [US.A US.B] (whole prior pool day released)", d.Evicted)
	}
	if !reflect.DeepEqual(d.Admitted, []string{"US.C"}) {
		t.Fatalf("Admitted=%v, want [US.C]", d.Admitted)
	}
	if !reflect.DeepEqual(d.Backfill, []string{"US.C"}) {
		t.Fatalf("Backfill=%v, want [US.C] (fresh pool day)", d.Backfill)
	}
	if !reflect.DeepEqual(p.Symbols(), []string{"US.C"}) {
		t.Fatalf("pool after reset = %v, want [US.C]", p.Symbols())
	}
}

func TestPoolReadmitAfterEvictionNoRebackfill(t *testing.T) {
	p := NewPool()
	base := et(2026, 7, 8, 9, 30)
	d0 := p.Update([]string{"US.A"}, base) // first admission -> backfill
	if !reflect.DeepEqual(d0.Backfill, []string{"US.A"}) {
		t.Fatalf("A should backfill on first admission: %v", d0.Backfill)
	}
	// Fill 30 fresh symbols over 3 polls, always ranked without A, so cap
	// pressure evicts A (the oldest off-board member).
	for g := 0; g < 3; g++ {
		top := make([]string, 10)
		for i := range top {
			top[i] = fmt.Sprintf("US.F%02d", g*10+i)
		}
		p.Update(top, base.Add(time.Duration(g+1)*time.Minute))
	}
	if containsStr(p.Symbols(), "US.A") {
		t.Fatalf("A should have been cap-evicted: %v", p.Symbols())
	}
	// Re-admit A within the SAME pool day.
	d := p.Update([]string{"US.A"}, base.Add(5*time.Minute))
	if !containsStr(d.Admitted, "US.A") {
		t.Fatalf("A should be re-admitted: %v", d.Admitted)
	}
	if containsStr(d.Backfill, "US.A") {
		t.Fatalf("A must NOT re-backfill within the same pool day: %v", d.Backfill)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/scan/ -run TestPool -v`
Expected: FAIL — `undefined: NewPool`, `undefined: poolCap`.

- [ ] **Step 3: Implement `pool.go`**

Create `engine/internal/scan/pool.go`:

```go
package scan

import (
	"sort"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

const (
	poolTrackN = 10 // top-N filtered rank symbols tracked live each poll
	poolCap    = 30 // max sticky members per pool day (cap > N: always an evictable off-board member)
)

// Delta is the subscription change one Update produces. Admitted symbols get a
// watch-tier Ensure; Backfill (a subset of Admitted, first admission this pool
// day) additionally get a deep-history seed; Evicted symbols get a Release.
type Delta struct {
	Admitted []string // newly pooled this poll, rank order
	Backfill []string // first admission this pool day (subset of Admitted)
	Evicted  []string // removed this poll, sorted
}

// Pool is the sticky top-N scanner subscription pool: pure logic, no I/O. The
// poller feeds it the filtered rank symbols each poll and executes the returned
// delta against the feed. See docs/superpowers/specs/2026-07-08-scanner-driven-
// subscription-pool-design.md.
type Pool struct {
	members    map[string]int64 // symbol -> last-seen-in-top-N poll time (UnixMilli)
	backfilled map[string]bool  // symbols already backfilled this pool day
	day        int64            // current pool-day key (0 = uninitialized)
}

func NewPool() *Pool {
	return &Pool{members: map[string]int64{}, backfilled: map[string]bool{}}
}

// Update feeds the current filtered rank symbols (rank order) and the poll time,
// and returns the demand delta. Symbols beyond the top-N are ignored for
// admission but existing members stay pooled (sticky) until cap eviction or the
// 20:00-ET pool-day reset.
func (p *Pool) Update(ranked []string, now time.Time) Delta {
	var d Delta

	// Pool-day reset (20:00 ET): release everything and start fresh.
	if day := session.PoolDay(now); day != p.day {
		for s := range p.members {
			d.Evicted = append(d.Evicted, s)
		}
		sort.Strings(d.Evicted)
		p.members = map[string]int64{}
		p.backfilled = map[string]bool{}
		p.day = day
	}

	n := poolTrackN
	if len(ranked) < n {
		n = len(ranked)
	}
	topN := ranked[:n]
	inTop := make(map[string]bool, n)
	for _, s := range topN {
		inTop[s] = true
	}

	ts := now.UnixMilli()
	for _, s := range topN {
		if _, ok := p.members[s]; ok {
			p.members[s] = ts // refresh last-seen-in-top-N
			continue
		}
		// New admission: enforce cap by evicting the longest-off-board member.
		if len(p.members) >= poolCap {
			if victim := p.oldestOffBoard(inTop); victim != "" {
				delete(p.members, victim)
				d.Evicted = append(d.Evicted, victim)
			}
		}
		p.members[s] = ts
		d.Admitted = append(d.Admitted, s)
		if !p.backfilled[s] {
			p.backfilled[s] = true
			d.Backfill = append(d.Backfill, s)
		}
	}
	return d
}

// oldestOffBoard returns the pooled member NOT currently in the top-N with the
// oldest last-seen-in-top-N timestamp (ties broken by symbol for determinism).
// cap (30) > N (10) guarantees an off-board member exists whenever the pool is
// full, so a current top-N member is never chosen.
func (p *Pool) oldestOffBoard(inTop map[string]bool) string {
	cands := make([]string, 0, len(p.members))
	for s := range p.members {
		if !inTop[s] {
			cands = append(cands, s)
		}
	}
	sort.Strings(cands) // deterministic tiebreak
	var victim string
	var oldest int64
	for _, s := range cands {
		if ls := p.members[s]; victim == "" || ls < oldest {
			victim, oldest = s, ls
		}
	}
	return victim
}

// Symbols returns the current pool members, sorted. Used to compose the news
// rotation set.
func (p *Pool) Symbols() []string {
	out := make([]string, 0, len(p.members))
	for s := range p.members {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/scan/ -run TestPool -v`
Expected: PASS (all five `TestPool*` tests).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/scan/pool.go engine/internal/scan/pool_test.go
git commit -m "feat(scan): add sticky top-N subscription Pool (pure logic)"
```

---

### Task 3: Integrate the Pool into `scan.Poller` and wire it in `main.go`

Injects a nil-tolerant feed handle + async backfill trigger into the poller, executes pool deltas in `pollOnce`, exposes `PoolSymbols()`, and wires the live startup path (feed injection, backfill-on-admission via `backfillWG`, and a new `scanWG` joined before `backfillWG.Wait()` for the shutdown invariant). Existing watchlist/focus/news machinery is left intact here and removed in Tasks 4–6. At the end of this task the pool is live and the build is green.

**Files:**
- Modify: `engine/internal/scan/scan.go`
- Modify: `engine/internal/scan/scan_test.go`
- Modify: `engine/cmd/etape/main.go`

**Interfaces:**
- Consumes: `Pool`/`Delta`/`NewPool` (Task 2); `feed.Demand`, `feed.WatchDemand` (`engine/internal/feed`); `*opend.OpenDFeed.Ensure/Release`; `backfill.Orchestrator.Backfill(ctx, symbol)` (already per-symbol + async-safe).
- Produces:
  - `type demandFeed interface { Ensure(d feed.Demand); Release(id string) }`
  - `func New(cfg config.Scan, r requester, pub Publisher, clk clock.Clock, feed demandFeed, backfill func(string)) *Poller`
  - `func (p *Poller) PoolSymbols() []string` — lock-free snapshot of current pool members (for the news set).
  - `func scanDemandID(symbol string) string` — returns `"scan:" + symbol`.

- [ ] **Step 1: Write the failing poller-level tests**

Add to `engine/internal/scan/scan_test.go`. First extend the import block with the `feed` package, `reflect`, and `sort`:

```go
	"reflect"
	"sort"
	...
	"github.com/earlisreal/eTape/engine/internal/feed"
```

Then add the spy + tests:

```go
// spyFeed records Ensure/Release calls for pool-delta assertions.
type spyFeed struct {
	ensured  []feed.Demand
	released []string
}

func (s *spyFeed) Ensure(d feed.Demand) { s.ensured = append(s.ensured, d) }
func (s *spyFeed) Release(id string)    { s.released = append(s.released, id) }

func rows(syms ...string) []wsmsg.ScannerRow {
	out := make([]wsmsg.ScannerRow, len(syms))
	for i, s := range syms {
		out[i] = wsmsg.ScannerRow{Symbol: s}
	}
	return out
}

func TestUpdatePoolEnsuresWatchDemandsAndBackfills(t *testing.T) {
	sf := &spyFeed{}
	var backfilled []string
	clk := clock.NewFake(et(2026, 7, 8, 14, 0)) // RTH, well inside a pool day
	p := New(config.Scan{}, &fakeReq{}, &capturePub{}, clk, sf, func(s string) { backfilled = append(backfilled, s) })

	p.updatePool(clk.Now(), rows("US.A", "US.B"))

	if len(sf.ensured) != 2 {
		t.Fatalf("want 2 Ensure calls, got %+v", sf.ensured)
	}
	if sf.ensured[0].ID != "scan:US.A" || sf.ensured[0].Symbol != "US.A" {
		t.Fatalf("Ensure[0]=%+v, want id scan:US.A", sf.ensured[0])
	}
	if len(sf.ensured[0].Subs) != 2 || sf.ensured[0].Focused {
		t.Fatalf("pool must use the 2-slot non-focused watch shape: %+v", sf.ensured[0])
	}
	if !reflect.DeepEqual(backfilled, []string{"US.A", "US.B"}) {
		t.Fatalf("backfilled=%v, want [US.A US.B]", backfilled)
	}
	if !reflect.DeepEqual(p.PoolSymbols(), []string{"US.A", "US.B"}) {
		t.Fatalf("PoolSymbols()=%v, want [US.A US.B]", p.PoolSymbols())
	}
}

func TestUpdatePoolReleasesOnDayReset(t *testing.T) {
	sf := &spyFeed{}
	clk := clock.NewFake(et(2026, 7, 8, 19, 0))
	p := New(config.Scan{}, &fakeReq{}, &capturePub{}, clk, sf, nil) // nil backfill tolerated

	p.updatePool(et(2026, 7, 8, 19, 0), rows("US.A", "US.B")) // pool day D
	p.updatePool(et(2026, 7, 8, 20, 0), rows("US.C"))         // crosses 20:00 ET -> day D+1

	sort.Strings(sf.released)
	if !reflect.DeepEqual(sf.released, []string{"scan:US.A", "scan:US.B"}) {
		t.Fatalf("released=%v, want [scan:US.A scan:US.B]", sf.released)
	}
}

func TestUpdatePoolNilFeedInert(t *testing.T) {
	clk := clock.NewFake(et(2026, 7, 8, 14, 0))
	p := New(config.Scan{}, &fakeReq{}, &capturePub{}, clk, nil, nil)
	p.updatePool(clk.Now(), rows("US.A")) // must not panic
	if p.PoolSymbols() != nil {
		t.Fatalf("nil feed must disable the pool: PoolSymbols()=%v", p.PoolSymbols())
	}
}

func TestPollOnceDrivesPool(t *testing.T) {
	fr := &fakeReq{
		rankResp: rankResp(rankItem{Symbol: "US.LOWF", ChangePct: 12.5, Last: 4.2, Volume: 300_000}),
		snap: func(codes []string) (*snappb.Response, error) {
			return snapResp(snap("LOWF", 20_000_000, true)), nil
		},
	}
	sf := &spyFeed{}
	clk := clock.NewFake(et(2026, 7, 8, 8, 0)) // pre-market
	p := New(config.Scan{Enabled: true, MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000},
		fr, &capturePub{}, clk, sf, nil)

	p.pollOnce(context.Background(), clk.Now())

	if len(sf.ensured) != 1 || sf.ensured[0].ID != "scan:US.LOWF" {
		t.Fatalf("pollOnce should Ensure the filtered top row via the pool: %+v", sf.ensured)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/scan/ -run 'TestUpdatePool|TestPollOnceDrivesPool' -v`
Expected: FAIL to compile — `New` takes 4 args, `p.updatePool`/`p.PoolSymbols` undefined.

- [ ] **Step 3: Add the feed/backfill/pool wiring to `scan.go`**

In `engine/internal/scan/scan.go`, extend the import block:

```go
	"sync/atomic"
	...
	"github.com/earlisreal/eTape/engine/internal/feed"
```

Add the interface just below the existing `requester` interface (after line 38):

```go
// demandFeed is the subscription-control surface the pool drives. Satisfied by
// *opend.OpenDFeed. A nil demandFeed disables the pool (tests/replay).
type demandFeed interface {
	Ensure(d feed.Demand)
	Release(id string)
}
```

Extend the `Poller` struct (lines 57-65) with the new fields:

```go
type Poller struct {
	cfg      config.Scan
	r        requester
	pub      Publisher
	clk      clock.Clock
	feed     demandFeed          // nil => pool disabled
	backfill func(string)        // async per-symbol deep-history seed; nil => no backfill
	pool     *Pool
	poolSyms atomic.Pointer[[]string] // lock-free snapshot for the news set
	floats   map[string]floatEntry      // symbol -> resolved float; absent = unknown
	seen     map[string]map[string]bool // session -> symbol -> seen
	seenDay  int64                      // ET day of the current seen-sets + float cache
}
```

Replace the constructor (lines 67-70):

```go
func New(cfg config.Scan, r requester, pub Publisher, clk clock.Clock, feed demandFeed, backfill func(string)) *Poller {
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk, feed: feed, backfill: backfill, pool: NewPool(),
		floats: map[string]floatEntry{}, seen: map[string]map[string]bool{}}
}
```

Insert the `updatePool` call into `pollOnce` immediately after `rows := rankRows(...)` (line 126), before the rank publish:

```go
	rows := rankRows(items, p.floats, p.cfg)
	p.updatePool(now, rows)
	sess := sessionKey(phase)
```

Add these methods (place after `pollOnce`, before `rankRows`):

```go
func scanDemandID(symbol string) string { return "scan:" + symbol }

// updatePool feeds the filtered top rows to the pool and executes the returned
// delta: Release evicted symbols, Ensure admitted symbols at watch tier, and
// trigger an async deep-history backfill on first admission. Release runs before
// Ensure so a symbol re-admitted on a pool-day reset ends up subscribed. A nil
// feed disables the pool entirely (tests/replay).
func (p *Poller) updatePool(now time.Time, rows []wsmsg.ScannerRow) {
	if p.feed == nil {
		return
	}
	syms := make([]string, len(rows))
	for i, r := range rows {
		syms[i] = r.Symbol
	}
	d := p.pool.Update(syms, now)
	for _, s := range d.Evicted {
		p.feed.Release(scanDemandID(s))
	}
	for _, s := range d.Admitted {
		p.feed.Ensure(feed.WatchDemand(scanDemandID(s), s))
	}
	if p.backfill != nil {
		for _, s := range d.Backfill {
			p.backfill(s)
		}
	}
	snap := p.pool.Symbols()
	p.poolSyms.Store(&snap)
}

// PoolSymbols returns a snapshot of the current pool members (sorted), or nil
// before the first poll / when the pool is disabled. Safe to call from another
// goroutine (the news poller).
func (p *Poller) PoolSymbols() []string {
	if s := p.poolSyms.Load(); s != nil {
		return *s
	}
	return nil
}
```

- [ ] **Step 4: Update `newTestPoller` for the new signature**

In `engine/internal/scan/scan_test.go`, change `newTestPoller` (line 260-262) to pass a nil feed + nil backfill so every existing test keeps the pool disabled:

```go
func newTestPoller(cfg config.Scan, fr *fakeReq, pub *capturePub) *Poller {
	return New(cfg, fr, pub, clock.NewFake(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)), nil, nil)
}
```

- [ ] **Step 5: Run the scan package tests**

Run: `cd engine && go test ./internal/scan/ -v`
Expected: PASS — all existing tests (nil-feed, pool disabled) plus the four new poller tests.

- [ ] **Step 6: Wire the poller injection + shutdown join in `main.go`**

In `engine/cmd/etape/main.go`:

(a) Declare `scanWG` alongside the other shutdown WaitGroups (next to `var backfillWG sync.WaitGroup`, ~line 205):

```go
	var backfillWG sync.WaitGroup
	var scanWG sync.WaitGroup
```

(b) Inside the `if live {` block, declare the backfill trigger before the backfill setup (just after the `go func() { _ = fd.Run(ctx) }()` / pipe wiring, ~line 221):

```go
		var backfillOne func(string)
```

(c) Inside `if cfg.Backfill.Enabled {`, immediately after `orch := backfill.New(...)` is constructed (before the `symbols := backfillSymbols(...)` boot-batch line at 252), ADD the trigger assignment. It registers each seed with `backfillWG` so the ordered shutdown joins them. The boot-batch block (lines 252-258) is left untouched in this task — it is removed in Task 5:

```go
			backfillOne = func(sym string) {
				backfillWG.Add(1)
				go func() {
					defer backfillWG.Done()
					orch.Backfill(ctx, sym)
				}()
			}
```

(d) Update the `startPollers` call (line 260) to pass `fd`, `backfillOne`, and `&scanWG` (watch/focus CSVs stay for now):

```go
		startPollers(ctx, cfg, client, fd, hub, uihubClk, st, hasTZVenue(cfg), splitCSV(*watch), splitCSV(*focus), backfillOne, &scanWG)
```

(e) Update the `startPollers` definition (line 383) — add the `fd`, `backfillOne`, `scanWG` params, construct the poller as a variable, and wrap its goroutine in `scanWG`:

```go
func startPollers(ctx context.Context, cfg config.Config, client *opend.Client, fd *opend.OpenDFeed, hub *uihub.Hub, clk clock.Clock, st *store.Store, hasTZ bool, watchCSV, focusCSV []string, backfillOne func(string), scanWG *sync.WaitGroup) {
	scanPoller := scan.New(cfg.Scan, client, hub, clk, fd, backfillOne)
	symbols := func() []string {
		return newsSymbols(cfg.Feed.Watchlist, watchCSV, focusCSV, hub.ActiveDemandSymbols)
	}
	scanWG.Add(1)
	go func() { defer scanWG.Done(); _ = scanPoller.Run(ctx) }()
	go func() { _ = news.New(cfg.News, client, hub, clk, symbols).Run(ctx) }()
	// health: moomoo probe via the OpenD client; app-ping RTT source is nil in v1
	// (ui-engine shows down until ping tracking is wired). The health poller's
	// sys.events are also persisted by main via a store hook if desired.
	go func() { _ = health.New(cfg.Health, hub, clk, moomooProbe{c: client}, nil, hasTZ).Run(ctx) }()
	_ = st // reserved: wire health.Event -> st.AppendSysEvent in a later pass
}
```

(f) Join the scan poller in the ordered shutdown — insert `scanWG.Wait()` immediately before `backfillWG.Wait()` (line 304):

```go
	srv.Wait()        // every conn.run() returned: no more SetConfig via dispatch
	scanWG.Wait()     // scan poller stopped: no more backfillWG.Add from pool admissions
	backfillWG.Wait() // backfill workers stopped: no more Seed* into the core
	pipeWG.Wait()     // feed->core pipe stopped: no more RecordEvent
```

- [ ] **Step 7: Build and run the full engine test suite**

Run: `cd engine && go build ./... && go test ./...`
Expected: PASS. (Existing `main`/`config`/`feed` tests still reference watchlist/focus — unchanged and green in this task.)

- [ ] **Step 8: Commit**

```bash
git add engine/internal/scan/scan.go engine/internal/scan/scan_test.go engine/cmd/etape/main.go
git commit -m "feat(scan): drive the subscription pool from pollOnce + wire feed/backfill/shutdown"
```

---

### Task 4: News set = pool ∪ live UI demands

Replaces the news rotation set's "watchlist ∪ `--watch`/`--focus` ∪ live demands" with "pool members ∪ live demands" — the single touch-point between this design and the on-demand design.

**Files:**
- Modify: `engine/cmd/etape/news_symbols.go`
- Modify: `engine/cmd/etape/news_symbols_test.go`
- Modify: `engine/cmd/etape/main.go` (`startPollers` closure + signature + call site)

**Interfaces:**
- Consumes: `scan.Poller.PoolSymbols()` (Task 3), `hub.ActiveDemandSymbols()`.
- Produces: `func newsSymbols(pool, liveDemands []string) []string` — union, deduped, sorted, empty strings dropped.

- [ ] **Step 1: Rewrite the failing test**

Replace the body of `engine/cmd/etape/news_symbols_test.go` with:

```go
package main

import (
	"reflect"
	"testing"
)

func TestNewsSymbols_UnionDedupSorted(t *testing.T) {
	got := newsSymbols(
		[]string{"US.AAPL", "US.MSFT"},      // pool members
		[]string{"US.MSFT", "US.NVDA", ""},  // live UI demands (+ empty)
	)
	want := []string{"US.AAPL", "US.MSFT", "US.NVDA"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNewsSymbols_Empty(t *testing.T) {
	if got := newsSymbols(nil, nil); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./cmd/etape/ -run TestNewsSymbols -v`
Expected: FAIL to compile — `newsSymbols` still takes 4 args.

- [ ] **Step 3: Rewrite `newsSymbols`**

Replace the whole of `engine/cmd/etape/news_symbols.go` with:

```go
package main

import "sort"

// newsSymbols composes the news poller's rotation set: current scanner-pool
// members ∪ live UI demands (interest demands included), deduped and sorted.
// Empty strings are dropped.
func newsSymbols(pool, liveDemands []string) []string {
	set := map[string]struct{}{}
	add := func(ss []string) {
		for _, s := range ss {
			if s != "" {
				set[s] = struct{}{}
			}
		}
	}
	add(pool)
	add(liveDemands)
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Switch the news closure to pool ∪ live and drop the CSV params**

In `engine/cmd/etape/main.go`, update `startPollers` — drop `watchCSV, focusCSV` from the signature and feed the closure from the pool:

```go
func startPollers(ctx context.Context, cfg config.Config, client *opend.Client, fd *opend.OpenDFeed, hub *uihub.Hub, clk clock.Clock, st *store.Store, hasTZ bool, backfillOne func(string), scanWG *sync.WaitGroup) {
	scanPoller := scan.New(cfg.Scan, client, hub, clk, fd, backfillOne)
	symbols := func() []string {
		return newsSymbols(scanPoller.PoolSymbols(), hub.ActiveDemandSymbols())
	}
	scanWG.Add(1)
	go func() { defer scanWG.Done(); _ = scanPoller.Run(ctx) }()
	go func() { _ = news.New(cfg.News, client, hub, clk, symbols).Run(ctx) }()
	go func() { _ = health.New(cfg.Health, hub, clk, moomooProbe{c: client}, nil, hasTZ).Run(ctx) }()
	_ = st // reserved: wire health.Event -> st.AppendSysEvent in a later pass
}
```

Update the call site (line 260) to drop the CSV args:

```go
		startPollers(ctx, cfg, client, fd, hub, uihubClk, st, hasTZVenue(cfg), backfillOne, &scanWG)
```

- [ ] **Step 5: Build and test**

Run: `cd engine && go build ./... && go test ./cmd/etape/ -v`
Expected: PASS. (`*watch`/`*focus`/`cfg.Feed.Watchlist` are still referenced by the boot `Ensure` loops and `backfillSymbols` — removed in Tasks 5–6 — so the build stays green.)

- [ ] **Step 6: Commit**

```bash
git add engine/cmd/etape/news_symbols.go engine/cmd/etape/news_symbols_test.go engine/cmd/etape/main.go
git commit -m "feat(news): compose rotation set from scanner pool ∪ live demands"
```

---

### Task 5: Delete boot-time backfill seeding

Backfill now happens on pool admission (Task 3), so the boot-time batch seed dies. The `backfill.Orchestrator` stays (it powers `backfillOne`); only the boot batch and `backfillSymbols` go.

**Files:**
- Modify: `engine/cmd/etape/main.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: nothing new (deletion only).

- [ ] **Step 1: Delete the boot backfill batch**

In `engine/cmd/etape/main.go`, inside `if cfg.Backfill.Enabled {`, delete the batch seed (lines 252-258) — the `symbols := backfillSymbols(...)` line and the `backfillWG.Add(1); go func() { ... orch.Run(ctx, symbols) ... }()` block. Keep `orch := backfill.New(...)` and the `backfillOne` assignment from Task 3. The block should end right after the `backfillOne = func(sym string){ ... }` assignment and its closing `}` for `if cfg.Backfill.Enabled`.

- [ ] **Step 2: Delete the `backfillSymbols` function**

Delete the entire `backfillSymbols` function (lines 425-446):

```go
// backfillSymbols is the de-duplicated union of the watchlist and the --watch/
// --focus flags — the same set the feed subscribes at boot.
func backfillSymbols(cfg config.Config, watch, focus string) []string {
	...
}
```

- [ ] **Step 3: Build and test**

Run: `cd engine && go build ./... && go test ./...`
Expected: PASS. (`splitCSV`, `*watch`, `*focus`, `cfg.Feed.Watchlist` are still used by the boot `Ensure` loops — removed in Task 6 — so the build is green.)

- [ ] **Step 4: Verify no boot batch remains**

Run: `cd engine && grep -n "backfillSymbols\|orch.Run" cmd/etape/main.go`
Expected: no matches.

- [ ] **Step 5: Commit**

```bash
git add engine/cmd/etape/main.go
git commit -m "refactor(backfill): drop boot-batch seeding (replaced by pool-admission backfill)"
```

---

### Task 6: Delete watchlist, `--watch`/`--focus`, and `feed.FocusedDemand`

The final removal: boot subscription flags, the config watchlist, the now-unused `feed.FocusedDemand`, and the `splitCSV` helper. Amends the tests that reference them.

**Files:**
- Modify: `engine/cmd/etape/main.go`
- Modify: `engine/internal/feed/feed.go`
- Modify: `engine/internal/feed/feed_test.go`
- Modify: `engine/internal/feed/opend/subman_test.go`
- Modify: `engine/internal/config/config.go`
- Modify: `engine/internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: nothing new (deletion only). `feed.WatchDemand` survives; the `Demand.Focused` field and uihub's inline focused demand (`demandForProfile` "focused" case) are unaffected.

- [ ] **Step 1: Update the config test fixture + assertion**

In `engine/internal/config/config_test.go`, `TestFeedAndMDSectionsWithDefaults`: remove the `watchlist = ["US.AAPL", "US.TSLA"]` line from the TOML fixture, and remove the `len(cfg.Feed.Watchlist) != 2` clause from the assertion at line 61 (keep the `cfg.Feed.QuotaSlots != 300` and other clauses).

- [ ] **Step 2: Delete `config.Feed.Watchlist`**

In `engine/internal/config/config.go`, remove the `Watchlist` field from the `Feed` struct (line 28):

```go
type Feed struct {
	ExtendedTime        bool `toml:"extended_time"`
	UnsubHysteresisSecs int  `toml:"unsub_hysteresis_secs"`
	QuotaSlots          int  `toml:"quota_slots"`
}
```

- [ ] **Step 3: Update the feed demand tests**

In `engine/internal/feed/feed_test.go`, rewrite `TestDemandProfiles` to drop the `FocusedDemand` assertions and keep only `WatchDemand`:

```go
func TestDemandProfiles(t *testing.T) {
	w := WatchDemand("watch-AAPL", "US.AAPL")
	if w.Focused || len(w.Subs) != 2 {
		t.Fatalf("watch profile = %+v, want 2 subs, not Focused", w)
	}
	// Watch profile is exactly TICKER + K_1M (tape/10s/1m recording, no depth).
	if w.Subs[0] != SubTicker || w.Subs[1] != SubKL1m {
		t.Fatalf("watch subs = %v, want [SubTicker SubKL1m]", w.Subs)
	}
}
```

In `engine/internal/feed/opend/subman_test.go`, replace the two `feed.FocusedDemand(...)` calls (lines 170, 191) with the equivalent inline focused demand (same 4-slot shape `FocusedDemand` produced):

Line 170:
```go
	m.Ensure(feed.Demand{ID: "f", Symbol: "US.FOC", Focused: true,
		Subs: []feed.SubType{feed.SubQuote, feed.SubBook, feed.SubTicker, feed.SubKL1m}}) // 4 slots, focused
```

Line 191:
```go
	m.Ensure(feed.Demand{ID: "f", Symbol: "US.TSLA", Focused: true,
		Subs: []feed.SubType{feed.SubQuote, feed.SubBook, feed.SubTicker, feed.SubKL1m}})
```

- [ ] **Step 4: Delete `feed.FocusedDemand`**

In `engine/internal/feed/feed.go`, delete the `FocusedDemand` function (lines 103-108, including its doc comment). Keep `WatchDemand`.

- [ ] **Step 5: Delete the boot flags, `Ensure` loops, and `splitCSV`**

In `engine/cmd/etape/main.go`:
- Delete the `--watch` and `--focus` flag definitions (lines 45-46).
- Delete the boot watch/focus `Ensure` loops (lines 222-227):
  ```go
		for _, s := range append(cfg.Feed.Watchlist, splitCSV(*watch)...) {
			fd.Ensure(feed.WatchDemand("boot-watch-"+s, s))
		}
		for _, s := range splitCSV(*focus) {
			fd.Ensure(feed.FocusedDemand("boot-focus-"+s, s))
		}
  ```
- Delete the `splitCSV` function (lines 448-460) — it now has no callers.
- Remove the `"strings"` import (line ~20) — `splitCSV` was its only user.

- [ ] **Step 6: Build and test the whole engine**

Run: `cd engine && go build ./... && go test ./...`
Expected: PASS across all packages.

- [ ] **Step 7: Verify the machinery is gone**

Run: `cd engine && grep -rn "FocusedDemand\|Feed.Watchlist\|splitCSV\|\"watch\"\|\"focus\"" cmd/etape/main.go internal/feed/feed.go internal/config/config.go`
Expected: no matches for `FocusedDemand`, `Feed.Watchlist`, or `splitCSV` (the `--watch`/`--focus` flag defs are gone; uihub's `"watch"`/`"focused"` profile strings are unaffected and live in `internal/uihub`, not these files).

- [ ] **Step 8: Commit**

```bash
git add engine/cmd/etape/main.go engine/internal/feed/feed.go engine/internal/feed/feed_test.go engine/internal/feed/opend/subman_test.go engine/internal/config/config.go engine/internal/config/config_test.go
git commit -m "refactor(feed): delete watchlist/--watch/--focus + feed.FocusedDemand"
```

---

## Verification (whole feature)

- [ ] `cd engine && go build ./... && go vet ./... && go test ./...` — all green.
- [ ] `grep -rn "backfillSymbols\|FocusedDemand" engine/` — no matches (machinery fully removed).
- [ ] Confirm the ordered shutdown reads: `srv.Wait()` → `scanWG.Wait()` → `backfillWG.Wait()` → `pipeWG.Wait()` → `forwardWG.Wait()` → `<-execDone` → `brokerWG.Wait()` → `st.Close()`.
- [ ] **Live-OpenD smoke test** (rides the outstanding live-verify checklist item, alongside the session-aware scanner work): during a real pre-market, confirm the pool fills as movers rank, `scan:<symbol>` watch demands appear, deep-history backfill fires on first admission, and subscription slots stay within budget (full pool ≈ 60/100). Not automatable here — note it as a manual step for Earl.

## Self-Review (completed during authoring)

- **Spec coverage:** admission/stickiness/cap-eviction/top-N-never-evicted/20:00-reset/first-admission-backfill/re-admission-no-rebackfill → Task 2 `Pool` + tests; feed.Ensure/Release parallel to Publish + nil-feed inert → Task 3; news set = pool ∪ live → Task 4; backfill-on-admission (reusing `Orchestrator.Backfill` + its 30-day quota dedup, async) → Task 3; watchlist/`--watch`/`--focus`/`FocusedDemand`/boot-seeding removal + test fallout → Tasks 4–6; constants N=10/cap=30 no config → Task 2; pool-day boundary helper → Task 1. Out-of-scope items (UI badge, pool persistence, configurable N/cap, subman health surfacing) correctly omitted.
- **Plan-time verifications resolved:** (1) feed injection point — `fd` exists before `startPollers`, injected as a distinct param from the `client` requester (Task 3 6e). (2) per-symbol backfill seam — `Orchestrator.Backfill(ctx, symbol)` already exists, exported, async-safe; no extraction needed (Task 3). (3) 20:00-ET helper did NOT exist — added as `session.PoolDay` (Task 1). (4) `feed.FocusedDemand` has no consumer besides `--focus` boot (uihub builds its focused demand inline) — safe to delete (Task 6).
- **Shutdown correctness:** the on-admission backfill turns the scan poller into a transitive store-writer; handled by joining `scanWG` before `backfillWG.Wait()` so no `Add` races `Wait` and no `Seed*` runs post-`Close()`.
- **Type consistency:** `New(...feed demandFeed, backfill func(string))`, `updatePool`, `PoolSymbols`, `scanDemandID`, `Delta{Admitted,Backfill,Evicted}`, `Pool.Update/Symbols`, `session.PoolDay`, `newsSymbols(pool, liveDemands []string)` are used identically across tasks and tests.
- **Placeholder scan:** none — every code step carries complete code.
