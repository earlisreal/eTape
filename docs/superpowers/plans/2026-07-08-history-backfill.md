# Deep-History Backfill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the already-built deep-history machinery so charts show full daily history and ~20 trading days of intraday 1m, warm-started from SQLite and gap-filled from moomoo, with Alpaca as a 1m-depth fallback.

**Architecture:** A new `internal/backfill` orchestrator runs at engine boot for each fed symbol: it warm-starts from the SQLite bar archives (instant, zero quota), then gap-fills from moomoo's `Qot_RequestHistoryKL` (daily full depth + 1m intraday), seeding `md.Core` in bounded chunks so the per-bar `BarUpdate` fan-out never overflows the core's drop-on-full updates channel. An optional Alpaca historical-data client (`internal/hist/alpaca`, paper keys) fills the older 1m gap when moomoo's depth falls short. Seeded bars reach the UI and the SQLite archive through the existing `forwardMD` path unchanged.

**Tech Stack:** Go 1.26, `github.com/BurntSushi/toml`, stdlib `net/http`, existing `internal/{feed,md,store,clock,creds,session,broker/netx}` packages.

## Global Constraints

- Backfill runs in **live mode only** — replay's `HistoryBars` returns nil and issues no network history. `main.go` constructs the orchestrator only in the `live` branch.
- **Never touch live-account keys.** The Alpaca data client uses the **paper `alpaca` creds** (`creds_key` default `"alpaca"`); free market data works with any key. The `alpaca-live` keys are never read here.
- Seeded bars carry the moomoo-style symbol string **with the `US.` prefix** (e.g. `US.AAPL`) — the whole system keys market data by that exact string. Alpaca's URL path strips the prefix, but the returned `feed.Bar.Symbol` keeps it.
- moomoo bars are ascending (oldest-first), bucket-START keyed; the adapter already normalizes end-labeled intraday K-lines and applies forward rehab. Do not re-sort or re-shift.
- Seed batches must stay small enough that one `md.Core` apply cannot overflow the 8192-deep updates channel: **`seed_chunk` default 500** (≤ ~4k emitted updates/chunk, each 1m bar fans out to ~8 updates).
- Spec: `docs/superpowers/specs/2026-07-08-history-backfill-design.md`.

---

## File Structure

**Phase 1 — boot backfill, moomoo only (fixes the reported chart bug):**
- Create `engine/internal/backfill/window.go` — `intradayFrom(now, tradingDays)` ET-calendar helper.
- Create `engine/internal/backfill/backfill.go` — `Orchestrator`, the `HistFetcher`/`Seeder`/`Archive` interfaces, `Config`, `seedChunked`, `moomooFetcher`/`MoomooFetcher`, `Backfill`, `Run`.
- Create `engine/internal/backfill/backfill_test.go` — window, chunk, orchestrator, and pool tests (fakes).
- Modify `engine/internal/config/config.go` — add the `Backfill` struct (no Alpaca sub-field yet) + defaults.
- Modify `engine/internal/config/config_test.go` — assert defaults + override.
- Modify `engine/cmd/etape/main.go` — construct + run the orchestrator in the live branch; join it on shutdown.

**Phase 2 — Alpaca fallback:**
- Modify `engine/internal/config/config.go` — add the `BackfillAlpaca` sub-struct.
- Modify `engine/internal/config/config_test.go` — assert Alpaca defaults.
- Create `engine/internal/hist/alpaca/alpaca.go` — the historical-data REST `Client`.
- Create `engine/internal/hist/alpaca/alpaca_test.go` — mock-server tests.
- Modify `engine/internal/backfill/backfill.go` — fallback arbitration in `fillDaily`/`fill1m`.
- Modify `engine/internal/backfill/backfill_test.go` — fallback tests.
- Modify `engine/cmd/etape/main.go` — build the Alpaca client and pass it as the fallback.

---

## Task 1: `[backfill]` config block

**Files:**
- Modify: `engine/internal/config/config.go`
- Test: `engine/internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Backfill` struct with fields `Enabled bool`, `IntradayDays int`, `DailyYears int`, `Concurrency int`, `SeedChunk int`; new field `Backfill Backfill` on `config.Config`; defaults set in `config.Default()`.

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/config/config_test.go`:

```go
func TestBackfillDefaultsAndOverride(t *testing.T) {
	// Defaults when absent.
	cfg, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Backfill.Enabled || cfg.Backfill.IntradayDays != 20 ||
		cfg.Backfill.DailyYears != 0 || cfg.Backfill.Concurrency != 3 || cfg.Backfill.SeedChunk != 500 {
		t.Fatalf("backfill defaults = %+v", cfg.Backfill)
	}
	// Overrides parse.
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[backfill]\nenabled = false\nintraday_days = 5\nconcurrency = 8\nseed_chunk = 250\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backfill.Enabled || cfg.Backfill.IntradayDays != 5 || cfg.Backfill.Concurrency != 8 || cfg.Backfill.SeedChunk != 250 {
		t.Fatalf("backfill override = %+v", cfg.Backfill)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/config/ -run TestBackfillDefaultsAndOverride`
Expected: FAIL — `cfg.Backfill undefined`.

- [ ] **Step 3: Add the struct and default**

In `engine/internal/config/config.go`, add the struct (place it after the `Health` struct, before `Config`):

```go
// Backfill is the [backfill] section: deep-history warm-start + gap-fill at boot.
type Backfill struct {
	Enabled      bool `toml:"enabled"`
	IntradayDays int  `toml:"intraday_days"` // trading days of 1m history to backfill
	DailyYears   int  `toml:"daily_years"`   // 0 = all available daily history
	Concurrency  int  `toml:"concurrency"`   // bounded boot worker pool
	SeedChunk    int  `toml:"seed_chunk"`    // max bars per Seed* call (drop-on-full guard)
}
```

Add the field to `Config` (after `Health Health`):

```go
	Health   Health   `toml:"health"`
	Backfill Backfill `toml:"backfill"`
```

Add the default inside `Default()`'s returned `Config` literal (after the `Health:` line):

```go
		Health:   Health{Enabled: true, ProbeMs: 5000},
		Backfill: Backfill{Enabled: true, IntradayDays: 20, DailyYears: 0, Concurrency: 3, SeedChunk: 500},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/config/config.go engine/internal/config/config_test.go
git commit -m "feat(config): add [backfill] section"
```

---

## Task 2: `intradayFrom` ET-window helper

**Files:**
- Create: `engine/internal/backfill/window.go`
- Test: `engine/internal/backfill/backfill_test.go`

**Interfaces:**
- Produces: `func intradayFrom(now time.Time, tradingDays int) time.Time` — returns ET midnight `tradingDays` weekdays before `now` (weekends skipped; US holidays not modeled, matching `session`'s v1 stance).

- [ ] **Step 1: Write the failing test**

Create `engine/internal/backfill/backfill_test.go`:

```go
package backfill

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

func TestIntradayFromSkipsWeekends(t *testing.T) {
	// Wednesday 2026-07-08 12:00 ET.
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	// 1 trading day back = Tuesday 2026-07-07 00:00 ET.
	got := intradayFrom(now, 1)
	want := time.Date(2026, 7, 7, 0, 0, 0, 0, session.Loc())
	if !got.Equal(want) {
		t.Fatalf("intradayFrom(1) = %s, want %s", got, want)
	}
	// 3 trading days back from Wed spans the weekend: Tue, Mon, Fri 2026-07-03.
	got = intradayFrom(now, 3)
	want = time.Date(2026, 7, 3, 0, 0, 0, 0, session.Loc())
	if !got.Equal(want) {
		t.Fatalf("intradayFrom(3) = %s, want %s", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/backfill/ -run TestIntradayFrom`
Expected: FAIL — package/`intradayFrom` undefined.

- [ ] **Step 3: Write the helper**

Create `engine/internal/backfill/window.go`:

```go
package backfill

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// intradayFrom returns ET midnight `tradingDays` weekdays before now. Weekends
// are skipped; US market holidays are not modeled (a holiday counts as a
// trading day here), matching the session package's documented v1 stance — the
// result is only a lower bound for the history query, so over-counting a
// holiday just widens the window harmlessly.
func intradayFrom(now time.Time, tradingDays int) time.Time {
	if tradingDays < 1 {
		tradingDays = 1
	}
	et := now.In(session.Loc())
	d := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, session.Loc())
	for tradingDays > 0 {
		d = d.AddDate(0, 0, -1)
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			tradingDays--
		}
	}
	return d
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/backfill/ -run TestIntradayFrom`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/backfill/window.go engine/internal/backfill/backfill_test.go
git commit -m "feat(backfill): intradayFrom ET-window helper"
```

---

## Task 3: `seedChunked` bounded-batch seeder

**Files:**
- Modify: `engine/internal/backfill/backfill.go` (create with the interfaces + helper)
- Test: `engine/internal/backfill/backfill_test.go`

**Interfaces:**
- Produces: `Seeder` interface (`SeedDaily(symbol string, bars []feed.Bar)`, `SeedHistory1m(symbol string, bars []feed.Bar)`); `func seedChunked(chunk int, bars []feed.Bar, seed func([]feed.Bar))` — calls `seed` with successive ≤`chunk` slices, order preserved, no call for empty input.

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/backfill/backfill_test.go`:

```go
import (
	// keep existing imports; add:
	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestSeedChunkedSplitsAndPreservesOrder(t *testing.T) {
	bars := make([]feed.Bar, 1200)
	for i := range bars {
		bars[i] = feed.Bar{Symbol: "US.AAPL", BucketMs: int64(i)}
	}
	var calls [][]feed.Bar
	seedChunked(500, bars, func(b []feed.Bar) {
		calls = append(calls, append([]feed.Bar(nil), b...))
	})
	if len(calls) != 3 || len(calls[0]) != 500 || len(calls[1]) != 500 || len(calls[2]) != 200 {
		t.Fatalf("chunk sizes = %d,%d,%d (want 500,500,200)", len(calls[0]), len(calls[1]), len(calls[2]))
	}
	// Order preserved end-to-end.
	var flat []feed.Bar
	for _, c := range calls {
		flat = append(flat, c...)
	}
	for i := range flat {
		if flat[i].BucketMs != int64(i) {
			t.Fatalf("order broken at %d: %d", i, flat[i].BucketMs)
		}
	}
	// Empty input => no calls.
	calls = nil
	seedChunked(500, nil, func(b []feed.Bar) { calls = append(calls, b) })
	if len(calls) != 0 {
		t.Fatalf("empty input produced %d calls", len(calls))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/backfill/ -run TestSeedChunked`
Expected: FAIL — `seedChunked` undefined.

- [ ] **Step 3: Create `backfill.go` with the interfaces and helper**

Create `engine/internal/backfill/backfill.go`:

```go
// Package backfill wires eTape's deep-history path: at boot it warm-starts each
// fed symbol from the SQLite bar archives, then gap-fills from moomoo (daily
// full depth + intraday 1m) with an optional Alpaca 1m-depth fallback, seeding
// md.Core in bounded chunks so the per-bar BarUpdate fan-out never overflows
// the core's drop-on-full updates channel.
package backfill

import (
	"context"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// HistFetcher pulls history from one source. Bars are ascending, bucket-START
// keyed, and price-adjusted (moomoo forward-rehab / Alpaca adjustment=all). A
// source that has no data for the range returns (nil, nil).
type HistFetcher interface {
	DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error)
	Intraday1m(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error)
}

// Seeder receives backfilled bars. Implemented by *md.Core.
type Seeder interface {
	SeedDaily(symbol string, bars []feed.Bar)
	SeedHistory1m(symbol string, bars []feed.Bar)
}

// Archive is the quota-free local warm-start source. Implemented by *store.Store.
type Archive interface {
	ReadDailyBars(symbol string) ([]feed.Bar, error)
	ReadBars1m(symbol string, fromMs, toMs int64) ([]feed.Bar, error)
}

// seedChunked calls seed with successive ≤chunk slices of bars, preserving
// order. Chunking bounds a single md.Core apply's emitted-update count so it
// cannot overflow the 8192-deep updates channel (each 1m bar fans out to ~8
// updates: 1m + intraday cascade + daily + weekly/monthly); the concurrent
// forwardMD drains between chunks.
func seedChunked(chunk int, bars []feed.Bar, seed func([]feed.Bar)) {
	if chunk <= 0 {
		chunk = 500
	}
	for i := 0; i < len(bars); i += chunk {
		end := i + chunk
		if end > len(bars) {
			end = len(bars)
		}
		seed(bars[i:end])
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/backfill/ -run TestSeedChunked`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/backfill/backfill.go engine/internal/backfill/backfill_test.go
git commit -m "feat(backfill): Seeder/HistFetcher/Archive interfaces + seedChunked"
```

---

## Task 4: `Orchestrator` — warm-start + moomoo gap-fill + worker pool

**Files:**
- Modify: `engine/internal/backfill/backfill.go`
- Test: `engine/internal/backfill/backfill_test.go`

**Interfaces:**
- Consumes: `HistFetcher`, `Seeder`, `Archive`, `seedChunked`, `intradayFrom` (Tasks 2–3); `clock.Clock`.
- Produces: `Config{IntradayDays, DailyYears, Concurrency, SeedChunk int}`; `func New(primary, fallback HistFetcher, seeder Seeder, archive Archive, clk clock.Clock, cfg Config) *Orchestrator`; `func (o *Orchestrator) Backfill(ctx context.Context, symbol string)`; `func (o *Orchestrator) Run(ctx context.Context, symbols []string)`. In this task `fallback` is stored but only reached on primary failure; the shallow-depth gap logic lands in Task 8.

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/backfill/backfill_test.go`:

```go
import (
	// add to existing imports:
	"context"
	"sync"
	"sync/atomic"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// fakeFetcher returns canned bars and records call ranges.
type fakeFetcher struct {
	daily, m1 []feed.Bar
	dErr, mErr error
	m1Calls    atomic.Int32
}

func (f *fakeFetcher) DailyBars(_ context.Context, _ string, _, _ time.Time) ([]feed.Bar, error) {
	return f.daily, f.dErr
}
func (f *fakeFetcher) Intraday1m(_ context.Context, _ string, _, _ time.Time) ([]feed.Bar, error) {
	f.m1Calls.Add(1)
	return f.m1, f.mErr
}

// fakeSeeder records seeded bars per method.
type fakeSeeder struct {
	mu           sync.Mutex
	daily, hist  []feed.Bar
}

func (s *fakeSeeder) SeedDaily(_ string, b []feed.Bar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.daily = append(s.daily, b...)
}
func (s *fakeSeeder) SeedHistory1m(_ string, b []feed.Bar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hist = append(s.hist, b...)
}

// fakeArchive returns canned warm-start bars.
type fakeArchive struct {
	daily, m1 []feed.Bar
}

func (a *fakeArchive) ReadDailyBars(_ string) ([]feed.Bar, error)             { return a.daily, nil }
func (a *fakeArchive) ReadBars1m(_ string, _, _ int64) ([]feed.Bar, error)    { return a.m1, nil }

func bar(ms int64) feed.Bar { return feed.Bar{Symbol: "US.AAPL", BucketMs: ms, C: 1} }

func TestBackfillWarmStartThenGapFill(t *testing.T) {
	primary := &fakeFetcher{
		daily: []feed.Bar{bar(1), bar(2)},
		m1:    []feed.Bar{bar(10), bar(11), bar(12)},
	}
	seeder := &fakeSeeder{}
	archive := &fakeArchive{
		daily: []feed.Bar{bar(0)},   // one warm-start daily bar
		m1:    []feed.Bar{bar(9)},   // one warm-start 1m bar
	}
	o := New(primary, nil, seeder, archive, clock.NewFake(time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())), Config{IntradayDays: 20, SeedChunk: 500})
	o.Backfill(context.Background(), "US.AAPL")

	// Daily: warm-start(1) + moomoo(2) = 3 seeded.
	if len(seeder.daily) != 3 {
		t.Fatalf("daily seeded = %d, want 3", len(seeder.daily))
	}
	// 1m: warm-start(1) + moomoo(3) = 4 seeded.
	if len(seeder.hist) != 4 {
		t.Fatalf("1m seeded = %d, want 4", len(seeder.hist))
	}
}

func TestBackfillPrimaryDailyErrorIsNonFatal(t *testing.T) {
	primary := &fakeFetcher{dErr: context.DeadlineExceeded, m1: []feed.Bar{bar(10)}}
	seeder := &fakeSeeder{}
	o := New(primary, nil, seeder, &fakeArchive{}, clock.NewFake(time.Now()), Config{IntradayDays: 20, SeedChunk: 500})
	o.Backfill(context.Background(), "US.AAPL") // must not panic
	if len(seeder.daily) != 0 {
		t.Fatalf("daily seeded on error = %d, want 0", len(seeder.daily))
	}
	if len(seeder.hist) != 1 {
		t.Fatalf("1m still seeded = %d, want 1", len(seeder.hist))
	}
}

func TestRunBoundedPoolCoversEverySymbol(t *testing.T) {
	primary := &fakeFetcher{m1: []feed.Bar{bar(10)}}
	seeder := &fakeSeeder{}
	o := New(primary, nil, seeder, &fakeArchive{}, clock.NewFake(time.Now()), Config{Concurrency: 2, IntradayDays: 20, SeedChunk: 500})
	o.Run(context.Background(), []string{"US.AAPL", "US.TSLA", "US.MSFT"})
	if got := primary.m1Calls.Load(); got != 3 {
		t.Fatalf("Intraday1m called %d times, want 3 (one per symbol)", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/backfill/ -run 'TestBackfill|TestRun'`
Expected: FAIL — `New`, `Backfill`, `Run` undefined.

- [ ] **Step 3: Implement the orchestrator**

Append to `engine/internal/backfill/backfill.go` (and add `"log/slog"`, `"sync"`, and `"github.com/earlisreal/eTape/engine/internal/clock"` to its import block):

```go
// Config sizes the orchestrator. Zero fields get defaults in New.
type Config struct {
	IntradayDays int
	DailyYears   int
	Concurrency  int
	SeedChunk    int
}

// Orchestrator runs the per-symbol backfill sequence. primary is moomoo;
// fallback (Alpaca) is optional and may be nil.
type Orchestrator struct {
	primary  HistFetcher
	fallback HistFetcher
	seeder   Seeder
	archive  Archive
	clk      clock.Clock
	cfg      Config
}

func New(primary, fallback HistFetcher, seeder Seeder, archive Archive, clk clock.Clock, cfg Config) *Orchestrator {
	if cfg.IntradayDays <= 0 {
		cfg.IntradayDays = 20
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 3
	}
	if cfg.SeedChunk <= 0 {
		cfg.SeedChunk = 500
	}
	return &Orchestrator{primary: primary, fallback: fallback, seeder: seeder, archive: archive, clk: clk, cfg: cfg}
}

// Run backfills every symbol through a bounded worker pool, honoring ctx.
// Per-symbol failures are isolated inside Backfill (logged, never propagated).
func (o *Orchestrator) Run(ctx context.Context, symbols []string) {
	sem := make(chan struct{}, o.cfg.Concurrency)
	var wg sync.WaitGroup
	for _, s := range symbols {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(sym string) {
			defer wg.Done()
			defer func() { <-sem }()
			o.Backfill(ctx, sym)
		}(s)
	}
	wg.Wait()
}

// Backfill runs warm-start → daily gap-fill → 1m gap-fill for one symbol.
// Every step is best-effort: a failure is logged and the next step still runs,
// so a single dead source never blanks the chart.
func (o *Orchestrator) Backfill(ctx context.Context, symbol string) {
	now := o.clk.Now()
	from1m := intradayFrom(now, o.cfg.IntradayDays)
	o.warmStart(symbol, from1m, now)
	o.fillDaily(ctx, symbol, o.dailyFrom(now), now)
	o.fill1m(ctx, symbol, from1m, now)
}

// dailyFrom is DailyYears ago, or the epoch (all available) when DailyYears==0.
func (o *Orchestrator) dailyFrom(now time.Time) time.Time {
	if o.cfg.DailyYears <= 0 {
		return time.Unix(0, 0)
	}
	return now.AddDate(-o.cfg.DailyYears, 0, 0)
}

func (o *Orchestrator) warmStart(symbol string, from1m, now time.Time) {
	if daily, err := o.archive.ReadDailyBars(symbol); err != nil {
		slog.Warn("backfill: warm-start daily read failed", "symbol", symbol, "err", err)
	} else if len(daily) > 0 {
		seedChunked(o.cfg.SeedChunk, daily, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
	}
	if m1, err := o.archive.ReadBars1m(symbol, from1m.UnixMilli(), now.UnixMilli()); err != nil {
		slog.Warn("backfill: warm-start 1m read failed", "symbol", symbol, "err", err)
	} else if len(m1) > 0 {
		seedChunked(o.cfg.SeedChunk, m1, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	}
}

func (o *Orchestrator) fillDaily(ctx context.Context, symbol string, from, to time.Time) {
	bars, err := o.primary.DailyBars(ctx, symbol, from, to)
	if err != nil {
		slog.Warn("backfill: primary daily failed", "symbol", symbol, "err", err)
		return
	}
	seedChunked(o.cfg.SeedChunk, bars, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
}

func (o *Orchestrator) fill1m(ctx context.Context, symbol string, from, to time.Time) {
	bars, err := o.primary.Intraday1m(ctx, symbol, from, to)
	if err != nil {
		slog.Warn("backfill: primary 1m failed", "symbol", symbol, "err", err)
		return
	}
	seedChunked(o.cfg.SeedChunk, bars, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/backfill/`
Expected: PASS (all tests).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/backfill/backfill.go engine/internal/backfill/backfill_test.go
git commit -m "feat(backfill): Orchestrator warm-start + moomoo gap-fill + worker pool"
```

---

## Task 5: Wire boot backfill into `main.go` (Phase 1 end-to-end)

**Files:**
- Modify: `engine/cmd/etape/main.go`
- Test: build + vet + full suite + documented manual smoke (integration wiring; the orchestrator logic is covered by Task 4).

**Interfaces:**
- Consumes: `backfill.New`, `backfill.MoomooFetcher` (added here), `backfill.Config`, `(*Orchestrator).Run`; `config.Backfill`.
- Produces: `func MoomooFetcher(fd feed.Feed) HistFetcher` in the backfill package; boot-time backfill goroutine + `backfillWG` join in `main`.

- [ ] **Step 1: Add the moomoo fetcher wrapper (with a compile check)**

Append to `engine/internal/backfill/backfill.go`:

```go
// MoomooFetcher adapts a feed.Feed (the live OpenD feed) as the primary
// HistFetcher: ResDay for daily, Res1m for intraday.
func MoomooFetcher(fd feed.Feed) HistFetcher { return moomooFetcher{fd: fd} }

type moomooFetcher struct{ fd feed.Feed }

func (m moomooFetcher) DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	return m.fd.HistoryBars(ctx, symbol, feed.ResDay, from, to)
}
func (m moomooFetcher) Intraday1m(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	return m.fd.HistoryBars(ctx, symbol, feed.Res1m, from, to)
}
```

Run: `cd engine && go build ./internal/backfill/`
Expected: builds clean.

- [ ] **Step 2: Add the `backfillSymbols` helper**

In `engine/cmd/etape/main.go`, add near `splitCSV` (bottom of the file):

```go
// backfillSymbols is the de-duplicated union of the watchlist and the --watch/
// --focus flags — the same set the feed subscribes at boot.
func backfillSymbols(cfg config.Config, watch, focus string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range cfg.Feed.Watchlist {
		add(s)
	}
	for _, s := range splitCSV(watch) {
		add(s)
	}
	for _, s := range splitCSV(focus) {
		add(s)
	}
	return out
}
```

- [ ] **Step 3: Construct and run the orchestrator in the live branch**

In `engine/cmd/etape/main.go`, add the import `"github.com/earlisreal/eTape/engine/internal/backfill"`.

Declare the wait group next to the other shutdown wait groups (near `var pipeWG sync.WaitGroup`, ~line 202):

```go
	var backfillWG sync.WaitGroup
```

Inside the `if live {` block, immediately AFTER the `fd.Ensure(...)` loops for watch/focus and BEFORE `startPollers(...)` (~line 224), add:

```go
		if cfg.Backfill.Enabled {
			orch := backfill.New(
				backfill.MoomooFetcher(fd),
				nil, // Alpaca fallback wired in Phase 2
				core,
				st,
				clock.System{},
				backfill.Config{
					IntradayDays: cfg.Backfill.IntradayDays,
					DailyYears:   cfg.Backfill.DailyYears,
					Concurrency:  cfg.Backfill.Concurrency,
					SeedChunk:    cfg.Backfill.SeedChunk,
				},
			)
			symbols := backfillSymbols(cfg, *watch, *focus)
			backfillWG.Add(1)
			go func() {
				defer backfillWG.Done()
				orch.Run(ctx, symbols)
				log.Info("boot backfill complete", "symbols", len(symbols))
			}()
		}
```

- [ ] **Step 4: Join the backfill pool on shutdown**

In the shutdown block, add `backfillWG.Wait()` right after `srv.Wait()` (~line 267), before `pipeWG.Wait()`:

```go
	srv.Wait()       // every conn.run() returned: no more SetConfig via dispatch
	backfillWG.Wait() // boot backfill workers stopped: no more Seed* into the core
	pipeWG.Wait()    // feed->core pipe stopped: no more RecordEvent
```

(The workers are ctx-bound, so cancellation stops them promptly; joining here guarantees no Seed* call is in flight past shutdown.)

- [ ] **Step 5: Build, vet, and run the full suite**

Run:
```bash
cd engine && go build ./... && go vet ./... && go test ./...
```
Expected: build + vet clean; all tests PASS.

- [ ] **Step 6: Manual smoke against live OpenD**

With OpenD running and logged in (see README), run the engine against a symbol and confirm deep history now appears:

```bash
cd engine && go run ./cmd/etape --watch US.AAPL
```
Expected in logs: `"boot backfill complete" symbols=1` and no `droppedUpdates` growth on shutdown. In the UI (or via a ws client), the daily series for `US.AAPL` now spans years and the 1m series ~20 trading days, instead of ~2 days. Stop with Ctrl-C; confirm clean `"shutdown complete"`.

- [ ] **Step 7: Commit**

```bash
git add engine/internal/backfill/backfill.go engine/cmd/etape/main.go
git commit -m "feat(engine): run deep-history backfill at boot for fed symbols"
```

---

## Task 6: `[backfill.alpaca]` config sub-block

**Files:**
- Modify: `engine/internal/config/config.go`
- Test: `engine/internal/config/config_test.go`

**Interfaces:**
- Produces: `config.BackfillAlpaca{Enabled bool, CredsKey string, Feed string}`; new field `Alpaca BackfillAlpaca` on `config.Backfill`; defaults in `Default()`.

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/config/config_test.go`:

```go
func TestBackfillAlpacaDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Backfill.Alpaca
	if !a.Enabled || a.CredsKey != "alpaca" || a.Feed != "iex" {
		t.Fatalf("alpaca backfill defaults = %+v", a)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/config/ -run TestBackfillAlpacaDefaults`
Expected: FAIL — `cfg.Backfill.Alpaca undefined`.

- [ ] **Step 3: Add the sub-struct and field**

In `engine/internal/config/config.go`, add the sub-struct after the `Backfill` struct:

```go
// BackfillAlpaca is the [backfill.alpaca] section: the optional 1m-depth
// fallback source. Uses the PAPER creds key (free data; live keys untouched).
type BackfillAlpaca struct {
	Enabled  bool   `toml:"enabled"`
	CredsKey string `toml:"creds_key"`
	Feed     string `toml:"feed"` // "iex" (free) | "sip"
}
```

Add the field to `Backfill` (after `SeedChunk`):

```go
	SeedChunk    int            `toml:"seed_chunk"`
	Alpaca       BackfillAlpaca `toml:"alpaca"`
```

Update the default in `Default()`:

```go
		Backfill: Backfill{
			Enabled: true, IntradayDays: 20, DailyYears: 0, Concurrency: 3, SeedChunk: 500,
			Alpaca: BackfillAlpaca{Enabled: true, CredsKey: "alpaca", Feed: "iex"},
		},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/config/config.go engine/internal/config/config_test.go
git commit -m "feat(config): add [backfill.alpaca] fallback section"
```

---

## Task 7: Alpaca historical-data client

**Files:**
- Create: `engine/internal/hist/alpaca/alpaca.go`
- Test: `engine/internal/hist/alpaca/alpaca_test.go`

**Interfaces:**
- Consumes: `netx.NewHTTPClient`, `netx.NewTokenBucket`, `clock.Clock`, `feed.Bar`.
- Produces: `func New(base, keyID, secret, feedName string, clk clock.Clock) *Client`; `(*Client).DailyBars(ctx, symbol, from, to) ([]feed.Bar, error)`; `(*Client).Intraday1m(ctx, symbol, from, to) ([]feed.Bar, error)` — satisfies `backfill.HistFetcher` structurally.

- [ ] **Step 1: Write the failing tests**

Create `engine/internal/hist/alpaca/alpaca_test.go`:

```go
package alpaca

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func TestIntraday1mParsesStripsPrefixAndMapsTime(t *testing.T) {
	var gotPath, gotTF, gotAdj, gotFeed string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTF = r.URL.Query().Get("timeframe")
		gotAdj = r.URL.Query().Get("adjustment")
		gotFeed = r.URL.Query().Get("feed")
		_, _ = w.Write([]byte(`{"bars":[{"t":"2026-07-07T13:30:00Z","o":100,"h":101,"l":99.5,"c":100.5,"v":1234}],"next_page_token":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(time.UnixMilli(0)))
	bars, err := c.Intraday1m(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(1<<40))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v2/stocks/AAPL/bars" || gotTF != "1Min" || gotAdj != "all" || gotFeed != "iex" {
		t.Fatalf("request = path %q tf %q adj %q feed %q", gotPath, gotTF, gotAdj, gotFeed)
	}
	if len(bars) != 1 {
		t.Fatalf("bars = %d, want 1", len(bars))
	}
	b := bars[0]
	// Symbol keeps the US. prefix; time maps to epoch-ms bucket start.
	if b.Symbol != "US.AAPL" || b.BucketMs != 1783431000_000 || b.O != 100 || b.C != 100.5 || b.Volume != 1234 {
		t.Fatalf("bar = %+v", b)
	}
}

func TestBarsPaginateViaNextPageToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_token") == "" {
			_, _ = w.Write([]byte(`{"bars":[{"t":"2026-07-07T13:30:00Z","o":1,"h":1,"l":1,"c":1,"v":1}],"next_page_token":"PAGE2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"bars":[{"t":"2026-07-07T13:31:00Z","o":2,"h":2,"l":2,"c":2,"v":2}],"next_page_token":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(time.UnixMilli(0)))
	bars, err := c.DailyBars(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(1<<40))
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 || bars[0].C != 1 || bars[1].C != 2 {
		t.Fatalf("paginated bars = %+v", bars)
	}
}

func TestBarsErrorStatusSurfaces(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/stocks/AAPL/bars", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"message":"forbidden"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "K", "S", "iex", clock.NewFake(time.UnixMilli(0)))
	_, err := c.Intraday1m(context.Background(), "US.AAPL", time.UnixMilli(0), time.UnixMilli(1<<40))
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want a 403 error, got %v", err)
	}
}
```

Note: `2026-07-07T13:30:00Z` = 09:30 ET on 2026-07-07 = epoch 1783431000 s = `1783431000_000` ms (the md core test fixes `2026-07-06T13:30:00Z = 1783344600`; +86400 s = one day).

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/hist/alpaca/`
Expected: FAIL — package/`New` undefined.

- [ ] **Step 3: Implement the client**

Create `engine/internal/hist/alpaca/alpaca.go`:

```go
// Package alpaca is a read-only client for Alpaca's historical market-data
// REST API (data.alpaca.markets), used as eTape's 1m-depth backfill fallback.
// It is deliberately separate from internal/broker/alpaca (the execution
// adapter): different base URL, no order surface, and it authenticates with
// the PAPER data key so live-account keys are never touched. It implements
// backfill.HistFetcher structurally.
package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

const defaultDataBase = "https://data.alpaca.markets"

// maxPages caps pagination as a runaway backstop (10k bars/page).
const maxPages = 50

// Client is the Alpaca historical-bars transport.
type Client struct {
	base   string
	keyID  string
	secret string
	feed   string
	hc     *http.Client
	clk    clock.Clock
	bucket *netx.TokenBucket
}

// New builds a Client. base defaults to the production data host; feedName
// defaults to "iex" (the free tier). Tests pass a mock server URL.
func New(base, keyID, secret, feedName string, clk clock.Clock) *Client {
	if base == "" {
		base = defaultDataBase
	}
	if feedName == "" {
		feedName = "iex"
	}
	return &Client{
		base: base, keyID: keyID, secret: secret, feed: feedName,
		hc:     netx.NewHTTPClient(15 * time.Second),
		clk:    clk,
		bucket: netx.NewTokenBucket(clk, 200.0/60.0, 5),
	}
}

func (c *Client) DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	return c.bars(ctx, symbol, "1Day", from, to)
}

func (c *Client) Intraday1m(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	return c.bars(ctx, symbol, "1Min", from, to)
}

type barJSON struct {
	T string  `json:"t"` // RFC3339 bar-start, UTC
	O float64 `json:"o"`
	H float64 `json:"h"`
	L float64 `json:"l"`
	C float64 `json:"c"`
	V int64   `json:"v"`
}

type barsResp struct {
	Bars          []barJSON `json:"bars"`
	NextPageToken *string   `json:"next_page_token"`
}

// bars pages through /v2/stocks/{sym}/bars, mapping each UTC bar-start to an
// epoch-ms bucket start. The symbol keeps its US. prefix on the returned bars
// (the rest of eTape keys by that string) but is stripped for the URL path.
func (c *Client) bars(ctx context.Context, symbol, timeframe string, from, to time.Time) ([]feed.Bar, error) {
	sym := strings.TrimPrefix(symbol, "US.")
	var out []feed.Bar
	pageToken := ""
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("timeframe", timeframe)
		q.Set("start", from.UTC().Format(time.RFC3339))
		q.Set("end", to.UTC().Format(time.RFC3339))
		q.Set("adjustment", "all") // split + dividend, closest to moomoo forward-rehab
		q.Set("feed", c.feed)
		q.Set("limit", "10000")
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		if err := c.bucket.Take(ctx); err != nil {
			return nil, err
		}
		reqURL := c.base + "/v2/stocks/" + url.PathEscape(sym) + "/bars?" + q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("APCA-API-KEY-ID", c.keyID)
		req.Header.Set("APCA-API-SECRET-KEY", c.secret)
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("alpaca data: status=%d body=%s", resp.StatusCode, body)
		}
		var br barsResp
		if err := json.Unmarshal(body, &br); err != nil {
			return nil, fmt.Errorf("alpaca data decode: %w", err)
		}
		for _, b := range br.Bars {
			ts, err := time.Parse(time.RFC3339, b.T)
			if err != nil {
				continue
			}
			out = append(out, feed.Bar{
				Symbol: symbol, BucketMs: ts.UnixMilli(),
				O: b.O, H: b.H, L: b.L, C: b.C, Volume: b.V,
			})
		}
		if br.NextPageToken == nil || *br.NextPageToken == "" {
			break
		}
		pageToken = *br.NextPageToken
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/hist/alpaca/`
Expected: PASS (all three tests).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/hist/alpaca/
git commit -m "feat(hist/alpaca): read-only historical bars client (fallback source)"
```

---

## Task 8: Alpaca fallback arbitration + wiring

**Files:**
- Modify: `engine/internal/backfill/backfill.go` (`fillDaily`, `fill1m`)
- Modify: `engine/internal/backfill/backfill_test.go`
- Modify: `engine/cmd/etape/main.go`

**Interfaces:**
- Consumes: `HistFetcher` fallback (Task 7's `*alpaca.Client`), everything from Task 4.
- Produces: fallback-aware `fillDaily`/`fill1m`; `main.go` builds the Alpaca client from paper creds and passes it to `backfill.New` instead of `nil`.

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/backfill/backfill_test.go`:

```go
// splitFetcher lets a test give the primary and fallback different data and
// record what range the fallback was asked for.
type recordFallback struct {
	m1        []feed.Bar
	daily     []feed.Bar
	m1From    atomic.Int64
	m1To      atomic.Int64
	m1Calls   atomic.Int32
	dailyCall atomic.Int32
}

func (r *recordFallback) DailyBars(_ context.Context, _ string, _, _ time.Time) ([]feed.Bar, error) {
	r.dailyCall.Add(1)
	return r.daily, nil
}
func (r *recordFallback) Intraday1m(_ context.Context, _ string, from, to time.Time) ([]feed.Bar, error) {
	r.m1Calls.Add(1)
	r.m1From.Store(from.UnixMilli())
	r.m1To.Store(to.UnixMilli())
	return r.m1, nil
}

func TestFallbackFillsShallowGap(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	from := intradayFrom(now, 20)
	// Primary returns only recent bars: oldest is 5 days after `from` — a wide gap.
	oldestMs := from.UnixMilli() + 5*24*3600*1000
	primary := &fakeFetcher{m1: []feed.Bar{bar(oldestMs), bar(oldestMs + 60000)}}
	fb := &recordFallback{m1: []feed.Bar{bar(from.UnixMilli())}}
	seeder := &fakeSeeder{}
	o := New(primary, fb, seeder, &fakeArchive{}, clock.NewFake(now), Config{IntradayDays: 20, SeedChunk: 500})
	o.Backfill(context.Background(), "US.AAPL")

	if fb.m1Calls.Load() != 1 {
		t.Fatalf("fallback 1m calls = %d, want 1", fb.m1Calls.Load())
	}
	// Fallback asked for [from, oldest).
	if fb.m1From.Load() != from.UnixMilli() || fb.m1To.Load() != oldestMs {
		t.Fatalf("fallback range = [%d,%d), want [%d,%d)", fb.m1From.Load(), fb.m1To.Load(), from.UnixMilli(), oldestMs)
	}
	// Seeded = primary(2) + fallback(1).
	if len(seeder.hist) != 3 {
		t.Fatalf("1m seeded = %d, want 3", len(seeder.hist))
	}
}

func TestFallbackSkippedWhenPrimaryDeepEnough(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	from := intradayFrom(now, 20)
	// Primary's oldest is right at `from` — full depth, no gap.
	primary := &fakeFetcher{m1: []feed.Bar{bar(from.UnixMilli()), bar(from.UnixMilli() + 60000)}}
	fb := &recordFallback{m1: []feed.Bar{bar(0)}}
	o := New(primary, fb, &fakeSeeder{}, &fakeArchive{}, clock.NewFake(now), Config{IntradayDays: 20, SeedChunk: 500})
	o.Backfill(context.Background(), "US.AAPL")
	if fb.m1Calls.Load() != 0 {
		t.Fatalf("fallback called %d times, want 0 (primary deep enough)", fb.m1Calls.Load())
	}
}

func TestFallbackFillsWholeWindowOnPrimaryError(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	from := intradayFrom(now, 20)
	primary := &fakeFetcher{mErr: context.DeadlineExceeded, dErr: context.DeadlineExceeded}
	fb := &recordFallback{m1: []feed.Bar{bar(from.UnixMilli())}, daily: []feed.Bar{bar(1), bar(2)}}
	seeder := &fakeSeeder{}
	o := New(primary, fb, seeder, &fakeArchive{}, clock.NewFake(now), Config{IntradayDays: 20, SeedChunk: 500})
	o.Backfill(context.Background(), "US.AAPL")

	// 1m: fallback asked for the whole [from, now] window.
	if fb.m1Calls.Load() != 1 || fb.m1From.Load() != from.UnixMilli() || fb.m1To.Load() != now.UnixMilli() {
		t.Fatalf("fallback 1m range = [%d,%d) calls=%d", fb.m1From.Load(), fb.m1To.Load(), fb.m1Calls.Load())
	}
	// Daily: primary errored, fallback daily used (2 bars).
	if fb.dailyCall.Load() != 1 || len(seeder.daily) != 2 {
		t.Fatalf("daily fallback calls=%d seeded=%d", fb.dailyCall.Load(), len(seeder.daily))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/backfill/ -run TestFallback`
Expected: FAIL — fallback is not consulted yet (calls == 0 where 1 is wanted).

- [ ] **Step 3: Make `fillDaily` and `fill1m` fallback-aware**

Replace `fillDaily` and `fill1m` in `engine/internal/backfill/backfill.go` with:

```go
// gapThresholdMs ignores sub-day gaps between the requested `from` and the
// primary's oldest bar — those are just weekend/holiday edges, not a real
// depth shortfall, and must not trigger a fallback fetch every boot.
const gapThresholdMs = 24 * 3600 * 1000

func (o *Orchestrator) fillDaily(ctx context.Context, symbol string, from, to time.Time) {
	bars, err := o.primary.DailyBars(ctx, symbol, from, to)
	if err != nil {
		slog.Warn("backfill: primary daily failed", "symbol", symbol, "err", err)
		if o.fallback == nil {
			return
		}
		if bars, err = o.fallback.DailyBars(ctx, symbol, from, to); err != nil {
			slog.Warn("backfill: fallback daily failed", "symbol", symbol, "err", err)
			return
		}
	}
	seedChunked(o.cfg.SeedChunk, bars, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
}

func (o *Orchestrator) fill1m(ctx context.Context, symbol string, from, to time.Time) {
	bars, err := o.primary.Intraday1m(ctx, symbol, from, to)
	if err != nil {
		slog.Warn("backfill: primary 1m failed", "symbol", symbol, "err", err)
		bars = nil
	}
	if len(bars) > 0 {
		seedChunked(o.cfg.SeedChunk, bars, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	}
	if o.fallback == nil {
		return
	}
	// Fallback fills only the older gap [from, gapTo). If the primary succeeded
	// and its oldest bar is within a day of `from`, the window is covered.
	gapTo := to
	if len(bars) > 0 {
		oldestMs := bars[0].BucketMs
		if oldestMs-from.UnixMilli() < gapThresholdMs {
			return
		}
		gapTo = time.UnixMilli(oldestMs)
	}
	gap, err := o.fallback.Intraday1m(ctx, symbol, from, gapTo)
	if err != nil {
		slog.Warn("backfill: fallback 1m failed", "symbol", symbol, "err", err)
		return
	}
	if len(gap) > 0 {
		seedChunked(o.cfg.SeedChunk, gap, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/backfill/`
Expected: PASS (fallback tests + all Task 4 tests still green).

- [ ] **Step 5: Wire the Alpaca client into `main.go`**

In `engine/cmd/etape/main.go`, add the aliased import (alongside the existing `backfill` import):

```go
	histalpaca "github.com/earlisreal/eTape/engine/internal/hist/alpaca"
```

In the live branch, replace the `nil, // Alpaca fallback wired in Phase 2` argument to `backfill.New` with a constructed fallback. Insert this just before the `orch := backfill.New(...)` call, and pass `fallback` instead of `nil`:

```go
			var fallback backfill.HistFetcher
			if cfg.Backfill.Alpaca.Enabled {
				if p, err := credsFile.Get(cfg.Backfill.Alpaca.CredsKey); err == nil {
					fallback = histalpaca.New("", p.KeyID, p.SecretKey, cfg.Backfill.Alpaca.Feed, clock.System{})
				} else {
					log.Warn("backfill: alpaca fallback disabled (no creds)", "key", cfg.Backfill.Alpaca.CredsKey, "err", err)
				}
			}
			orch := backfill.New(
				backfill.MoomooFetcher(fd),
				fallback,
				core,
				st,
				clock.System{},
				backfill.Config{
					IntradayDays: cfg.Backfill.IntradayDays,
					DailyYears:   cfg.Backfill.DailyYears,
					Concurrency:  cfg.Backfill.Concurrency,
					SeedChunk:    cfg.Backfill.SeedChunk,
				},
			)
```

(`credsFile` is already loaded in the live branch. `credsFile.Get` returns an error for a missing/empty entry, so a machine without the paper `alpaca` key simply runs moomoo-only.)

- [ ] **Step 6: Build, vet, and run the full suite**

Run:
```bash
cd engine && go build ./... && go vet ./... && go test ./...
```
Expected: build + vet clean; all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add engine/internal/backfill/backfill.go engine/internal/backfill/backfill_test.go engine/cmd/etape/main.go
git commit -m "feat(backfill): Alpaca 1m-depth fallback arbitration + wiring"
```

---

## Self-Review Notes

- **Spec coverage:** warm-start (Task 4 `warmStart`), moomoo daily full depth + 1m ~20d (Tasks 4/5), chunked seeding / drop-on-full guard (Task 3 + `seedChunked` used everywhere), boot worker pool live-only (Task 5), Alpaca fallback source + gap-only arbitration + adjustment=all + paper creds (Tasks 6–8), config block (Tasks 1/6), error isolation + logged degrade (Tasks 4/8), reusable `Backfill(ctx, symbol)` entry point for the on-demand session (Task 4). All spec sections map to a task.
- **Drop-guard verification:** the spec calls for a `DroppedUpdates()==0` integration assertion. The deterministic unit coverage is `TestSeedChunkedSplitsAndPreservesOrder` (Task 3), which proves no single apply exceeds `seed_chunk`; the end-to-end no-drop property is confirmed by the Task 5 manual smoke (watch `droppedUpdates` on shutdown). A real-core concurrent-drainer test was considered but omitted as scheduling-flaky — the chunk-size bound is the actual guarantee and it is unit-tested.
- **Type consistency:** `HistFetcher`/`Seeder`/`Archive`, `New(primary, fallback, seeder, archive, clk, cfg)`, `Config{IntradayDays,DailyYears,Concurrency,SeedChunk}`, `MoomooFetcher(fd)`, and `alpaca.New(base,keyID,secret,feedName,clk)` are used identically across tasks. `feed.Bar.Symbol` retains the `US.` prefix in every producer.
