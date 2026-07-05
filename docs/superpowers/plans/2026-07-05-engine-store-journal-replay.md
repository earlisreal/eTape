# eTape Engine — Plan 3 of 6: Store, Journal & Replay

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add durable persistence and deterministic replay to the engine: one SQLite/WAL writer goroutine, an always-on journal tee that records every `feed.Event` at the Feed boundary, `bars_1m`/`bars_daily` archives fed by the md core's finalized bars, `config`/`sys_events` tables, retention pruning at boot, and a `replay` package (journal-backed `feed.Feed` + a simulated `clock.Clock`) so `etape --replay <day> --speed N` reconstructs a session with byte-identical bars/indicators.

**Architecture:** Recording *is* the journal — there is no separate record mode. A store writer goroutine owns the only SQLite handle for writes (WAL allows concurrent reads); it consumes a channel of typed write ops and flushes them in batched transactions. The journal tee sits in `cmd/etape`'s feed→core pipe: every event is `RecordEvent`-ed at the same moment `core.Feed` sees it, so `replay(journal)` reproduces exactly what the core saw (seed events included). The journal stores one row per `feed.Event` (all 7 variants, `Seed` flag preserved) as a `kind`-discriminated JSON payload — preserving batch boundaries, which is what makes update-stream reproduction exact. `replay` is just another `feed.Feed` implementation reading journal rows for a day and re-emitting them, paced by a simulated clock driven by event timestamps × a speed factor. The md core is unchanged and takes no clock, so the Task 13 invariant (`replay(events) == state`) extends across the SQLite boundary.

**Tech Stack:** Go 1.26.4 (module from Plan 1/2), `modernc.org/sqlite` (pure-Go, cgo-free SQLite driver — plays clean with `go test -race`, cross-compiles for the later Wails packaging, no C toolchain in CI), `database/sql`, `encoding/json` (stdlib, lossless float64 round-trip). No new tooling beyond the driver.

## Global Constraints

Copied from the approved specs and Plans 1–2. Every task's requirements implicitly include this section.

- **Module path:** `github.com/earlisreal/eTape/engine`. This plan builds on the Plan 2 code on branch `worktree-engine-market-data-core` (worktree `.claude/worktrees/engine-market-data-core`), which contains Plans 1+2 and is **not yet merged to main**. Execute Plan 3 on a branch cut from that tip (or from `main` once Plan 2 merges). Never touch the main checkout's `ui/` directory (owned by concurrent UI sessions).
- **Dependency rule:** domain packages (`feed`, `md`, `session`, `clock`) never import adapters (`feed/opend`, `replay`), `uihub`, or `store`. `store` may import domain types (`feed`, `session`, `clock`) for serialization; it **must not** import `md` (the archive tap converts `md.Bar → feed.Bar` in `cmd/etape`). `replay` is an adapter: it may import `feed`, `clock`, `session`, `store`. No import cycles: `feed`→stdlib only; `store`→`feed`,`session`,`clock`; `replay`→`store`,`feed`,`clock`,`session`. (go-engine-design §Dependency rule)
- **Single-writer persistence:** exactly one goroutine (`store` writer loop) executes SQL writes. The md core never touches disk. Writes funnel through a buffered channel and flush in batched transactions (~250 ms default). Reads (`ReadJournalDay`, `GetConfig`, archive reads) query the shared `*sql.DB` directly — WAL permits concurrent readers. (go-engine-design §store)
- **Journal completeness = determinism.** The journal records the **entire** `feed.Event` stream in arrival order: all 7 variants (`TicksEvent`, `QuoteEvent`, `BookEvent`, `Bars1mEvent`, `ConnUpEvent`, `ConnDownEvent`, `ResyncedEvent`), the `Seed` flag preserved, one row per event (batch boundaries intact). Replaying a day's rows in `seq` order re-emits the identical stream. Anything less breaks `replay(journal) == live`. (go-engine-design §store, §md Invariant)
- **Timestamps:** exchange timestamps (`TsMs`/`BucketMs`, epoch **ms int64**) are authoritative for bucketing and drive the replay clock; receive time (`ts_recv`) is metadata only (latency, day-partition key). All persisted timestamps are `INTEGER` epoch ms — matching the domain's `TsMs`/`BucketMs` int64 fields — **not** the ISO `TEXT` strings the design spec sketched (documented refinement: ms integers are cheaper, parse-free, and deterministic). (go-engine-design §Error handling)
- **Trade-incapability rule:** this plan touches no `Trd_*` protocols. `exec_events`/`fills` tables belong to Plan 4 (Execution Core) and are **not** created here. (CLAUDE.md; roadmap)
- **Honesty policy:** never render stale as live; a SQLite/disk failure degrades journal/archive with a loud `sys_events` banner + log but **never blocks market data** (in-memory state keeps flowing). The writer drains-and-logs on commit failure, so a full write channel means genuine overload surfaced upstream, never silent loss. (go-engine-design §Error handling)
- **Repo is PUBLIC; sensitive-sweep every commit.** Test databases are ephemeral (`t.TempDir()`), never committed. No account identifiers or credentials in any checked-in fixture. Credentials stay in `~/.eJournal/credentials.json` (untouched). (memory: repo public)
- **Determinism / testability:** anything time-dependent takes `clock.Clock`, never `time.Now`. The store writer takes a `clock.Clock` (for `sys_events` timestamps and retention math); tests inject `clock.NewFake`. The md core still takes no clock. (go-engine-design §Clock)
- **Config growth:** a `[store]` section is added to `~/.eTape/config.toml`; a missing file/section yields defaults. The struct grows section-by-section per plan. (go-engine-design §store)
- **CI gates:** `go build ./...`, `go vet ./...`, `go test -race ./...`, and `golangci-lint run` (v2 config from Plan 1) all pass at every task boundary. Run from `engine/`. **Run `gofmt -w` (or `goimports -w`) on new/changed files before the lint gate** — golangci-lint v2's enabled formatters (`gofmt`, `goimports`) fail on unformatted code; the code blocks in this plan are hand-written and may need reformatting (trailing-comment alignment, import grouping).

---

## Plan sequence context

This is **Plan 3 of 6** (roadmap in Plan 1's header: 1 Foundation/OpenD client → 2 Market-data core → **3 Store/journal/replay** → 4 Execution core → 5 Broker adapters → 6 uihub/pollers/main). Plan 3's deliverable: *recording is always-on; `etape --replay <day> --speed N` reconstructs a session with byte-identical bars/indicators.*

**Consumed from Plans 1–2 (exact, verified against the worktree):**

```go
// feed (feed/feed.go, feed/events.go) — sealed unions, NO struct tags
type Direction uint8 // Neutral=0, Buy=1, Sell=2; has String()
type Tick struct { Symbol string; Seq int64; TsMs int64; Price float64; Volume int64; Turnover float64; Dir Direction; RecvTsMs int64 }
type Quote struct { Symbol string; TsMs int64; Last, Open, High, Low, PrevClose float64; Volume int64; Turnover float64 }
type BookLevel struct { Price float64; Volume int64; Orders int32 }
type Book struct { Symbol string; TsMs int64; Bids, Asks []BookLevel }
type Bar struct { Symbol string; BucketMs int64; O, H, L, C float64; Volume int64; Turnover float64 } // bucket START
type Resolution uint8 // Res1m=0, ResDay=1
type Demand struct { ID, Symbol string; Subs []SubType; Focused bool }
type Event interface{ isEvent() } // TicksEvent{Ticks []Tick; Seed bool}, QuoteEvent{Quote; Seed bool},
                                  // BookEvent{Book; Seed bool}, Bars1mEvent{Bars []Bar; Seed bool},
                                  // ConnUpEvent{}, ConnDownEvent{}, ResyncedEvent{}
type Feed interface {
	Events() <-chan Event
	Ensure(d Demand); Release(id string)
	HistoryBars(ctx context.Context, symbol string, res Resolution, from, to time.Time) ([]Bar, error)
	RecentTicks(ctx context.Context, symbol string, n int) ([]Tick, error)
	CachedBars1m(ctx context.Context, symbol string, n int) ([]Bar, error)
	BookSnapshot(ctx context.Context, symbol string) (Book, error)
	QuoteSnapshot(ctx context.Context, symbol string) (Quote, error)
}

// clock (clock/clock.go, clock/fake.go)
type Ticker interface { C() <-chan time.Time; Stop() }
type Clock interface { Now() time.Time; After(d time.Duration) <-chan time.Time; NewTicker(d time.Duration) Ticker }
type System struct{}                       // real clock
func NewFake(start time.Time) *Fake        // Advance(d) fires due wakers in order; NO AdvanceTo/Set

// session (session/session.go)
type Timeframe string // TF10s,TF1m,TF5m,TF15m,TF30m,TF60m,TFDay,TFWeek,TFMonth
func Loc() *time.Location
func DayMs(tsMs int64) int64               // ET wall-midnight ms containing tsMs (the only day-key helper)

// md (md/core.go, md/update.go, md/indicator.go)
func New(cfg Config) *Core                 // Config{TapeRing int; AnchorSecs int64}
func (c *Core) Feed(ev feed.Event)
func (c *Core) Run(ctx context.Context) error
func (c *Core) Updates() <-chan Update
func (c *Core) Marks() <-chan Mark
func (c *Core) EnsureIndicator(id string, spec IndicatorSpec)
func (c *Core) SeedDaily(symbol string, bars []feed.Bar)     // no callers yet — Plan 6 chart backfill
func (c *Core) SeedHistory1m(symbol string, bars []feed.Bar) // no callers yet
type Bar struct { Symbol string; TF session.Timeframe; BucketMs int64; O,H,L,C float64; V int64; BuyV, SellV int64; Ticks int32; InProgress, Gap bool }
type BarUpdate struct{ Bar Bar } // isUpdate(); emitted on Updates() for every timeframe, in-progress and finalized
```

**Produced for later plans:**

- `store.Store` — the SQLite writer + journal/archive/config/sys_events API. Plan 4 extends the schema with `exec_events`/`fills`; Plan 6 consumes `GetConfig`/`SetConfig`/`ListConfig` (WS config CRUD), `RecentSysEvents` (`sys.events` topic), `ReadDailyBars`/`ReadBars1m` (chart-open backfill), and `AppendSysEvent` (connects/gaps/quota banners).
- `replay.Feed` / `replay.Clock` — the replay seam. Plan 4's `SimBroker` joins these to make the full `etape --replay` E2E (replay Feed + replay Clock + SimBroker) the design spec describes; Plan 6's Playwright E2E boots on them.

**Deviations from the roadmap/design-spec, flagged:**

- **`SimBroker` is NOT in this plan.** The design-spec §replay bundles SimBroker with replay, but SimBroker implements the exec-spec `Broker` interface, which does not exist until Plan 4. The plan roadmap correctly places SimBroker in Plan 4. Plan 3 delivers the *market-data* replay (Feed + Clock); `--replay` reconstructs bars/indicators without needing orders.
- **Persisted timestamps are `INTEGER` epoch ms, not `TEXT` ISO** (see Global Constraints). Matches the domain int64 fields; avoids parse ambiguity.
- **Journal `kind` set extends the spec's `tick|book|quote|bar1m|gap`** to `ticks|quote|book|bars1m|conn_up|conn_down|resynced`. There is no `GapEvent` in the codebase; gaps are reproduced faithfully by replaying `ConnDownEvent`→`ConnUpEvent`→`ResyncedEvent` (the md bar engine re-derives the `Gap` flag). Conn/resync events carry no exchange timestamp — their `ts_exch` column stores `ts_recv`.
- **Bar archives store OHLCV only** (`symbol, ts, o, h, l, c, v`), matching the design spec and the `feed.Bar` seed path (`SeedHistory1m`/`SeedDaily` take `[]feed.Bar`, which has no delta fields). The archive tap converts `md.Bar → feed.Bar`, dropping `BuyV/SellV/Ticks` (tick-delta is a live-only, tick-derived quantity; a history-seeded bar honestly reports delta 0).
- **`clock.Fake` gains no method.** `replay.Clock` wraps a `clock.Fake` and adds `AdvanceTo(t)` in the `replay` package — Plan 2 code is untouched.

---

## File Structure (Plan 3)

```
engine/
  go.mod, go.sum                            MODIFY  — add modernc.org/sqlite
  internal/
    config/
      config.go                   MODIFY  — [store] section (DBPath, RetentionDays, FlushMs)
      config_test.go              MODIFY  — [store] defaults + override
    store/
      store.go                    NEW     — Store: Open/Close/Flush, writer goroutine, batched txns, pragmas
      schema.go                   NEW     — embedded DDL (journal, bars_1m, bars_daily, config, sys_events)
      codec.go                    NEW     — feed.Event ⇄ JSON payload; kind/seed/symbol/exch-ts helpers; dayKey
      codec_test.go               NEW     — round-trip every variant (reflect.DeepEqual)
      journal.go                  NEW     — RecordEvent, seq assignment, JournalRow, ReadJournalDay, JournalDays
      journal_test.go             NEW     — append/read ordering, seq monotonic per day
      bars.go                     NEW     — ArchiveBar1m/ArchiveDaily (upsert), ReadBars1m/ReadDailyBars
      bars_test.go                NEW     — idempotent upsert, read-back
      config.go                   NEW     — SetConfig/GetConfig/ListConfig/DeleteConfig
      sysevents.go                NEW     — AppendSysEvent, RecentSysEvents
      aux_test.go                 NEW     — config + sys_events round-trips
      retention.go                NEW     — PruneJournal (by ET day; archives untouched)
      retention_test.go           NEW     — prune old days, keep recent + archives
    replay/
      clock.go                    NEW     — replay.Clock: clock.Clock + AdvanceTo (wraps clock.Fake)
      clock_test.go               NEW
      feed.go                     NEW     — replay.Feed: feed.Feed from journal rows, paced emit
      feed_test.go                NEW
      determinism_test.go         NEW     — capstone: replay(journal) == live, byte-identical updates
  cmd/etape/
    main.go                       MODIFY  — open store; live journal tee + bar-archive tap; --replay/--speed swap; prune at boot
```

---

## Task 1: `[store]` config, SQLite driver, and the store skeleton (Open/Close/Flush + writer goroutine)

Stands up the `store` package: the pure-Go SQLite driver, WAL pragmas via DSN, embedded schema/migrations, and the single writer goroutine with batched-transaction flushing and a synchronous `Flush` barrier. No domain ops yet — this is the persistence chassis every later task plugs into.

**Files:**
- Modify: `engine/go.mod`, `engine/go.sum` (add driver)
- Modify: `engine/internal/config/config.go`, `engine/internal/config/config_test.go`
- Create: `engine/internal/store/store.go`, `engine/internal/store/schema.go`
- Create: `engine/internal/store/store_test.go`

**Interfaces:**
- Consumes: `clock.Clock`/`clock.System` (Plan 2).
- Produces (used by all later store tasks + `cmd/etape`):

```go
type Options struct {
	Path          string        // SQLite file path
	Clock         clock.Clock   // default clock.System{}; used for sys_events ts + retention
	FlushInterval time.Duration // default 250ms
	BatchMax      int           // default 512; force-flush when buffered writes reach this
}
func Open(opt Options) (*Store, error) // opens, applies pragmas, migrates, starts the writer
func (s *Store) Flush()                // synchronous barrier: all queued writes committed on return
func (s *Store) Close() error          // Flush + stop writer + close DB

// internal writer contract (used by later tasks' ops):
type pendingWrite struct { query string; args []any }
type writeOp interface{ render() []pendingWrite } // ops implement this; enqueued via s.writes
```

- [ ] **Step 1: Add the SQLite driver**

Run:
```bash
cd engine && go get modernc.org/sqlite@latest && go mod tidy
```
Expected: `go.mod` now `require`s `modernc.org/sqlite vX.Y.Z` (and its transitive deps); `go.sum` updated. Commit the resolved version as-is.

- [ ] **Step 2: Add the `[store]` config section (write the failing test)**

In `engine/internal/config/config_test.go`, add:
```go
func TestStoreDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Store.RetentionDays != 30 {
		t.Fatalf("RetentionDays default = %d, want 30", cfg.Store.RetentionDays)
	}
	if cfg.Store.FlushMs != 250 {
		t.Fatalf("FlushMs default = %d, want 250", cfg.Store.FlushMs)
	}
	if cfg.Store.DBPath != "" {
		t.Fatalf("DBPath default = %q, want empty (resolved by main)", cfg.Store.DBPath)
	}
}

func TestStoreOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[store]\ndb_path = \"/tmp/x.db\"\nretention_days = 7\nflush_ms = 100\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.DBPath != "/tmp/x.db" || cfg.Store.RetentionDays != 7 || cfg.Store.FlushMs != 100 {
		t.Fatalf("store override not applied: %+v", cfg.Store)
	}
}
```
Ensure `config_test.go` imports include `"os"`, `"path/filepath"`, `"testing"`.

- [ ] **Step 3: Run it — verify it fails**

Run: `cd engine && go test ./internal/config/ -run TestStore -v`
Expected: FAIL — `cfg.Store` undefined (compile error).

- [ ] **Step 4: Implement the `[store]` section**

In `engine/internal/config/config.go`, add the `Store` struct and wire it into `Config` + `Default`:
```go
// Store configures SQLite persistence (journal, bar archives, config, sys_events).
type Store struct {
	DBPath        string `toml:"db_path"`        // empty → resolved to ~/.eTape/etape.db by main
	RetentionDays int    `toml:"retention_days"` // journal pruned older than this many days
	FlushMs       int    `toml:"flush_ms"`       // writer batch-flush interval
}
```
Add the field to `Config`:
```go
type Config struct {
	OpenD OpenD `toml:"opend"`
	Feed  Feed  `toml:"feed"`
	MD    MD    `toml:"md"`
	Store Store `toml:"store"`
}
```
And to `Default()`:
```go
	return Config{
		OpenD: OpenD{Host: "127.0.0.1", Port: 11111},
		Feed:  Feed{ExtendedTime: true, UnsubHysteresisSecs: 300, QuotaSlots: 100},
		MD:    MD{TapeRing: 65536, SessionAnchor: "09:30"},
		Store: Store{DBPath: "", RetentionDays: 30, FlushMs: 250},
	}
```

- [ ] **Step 5: Run config tests — verify they pass**

Run: `cd engine && go test ./internal/config/ -v`
Expected: PASS (existing + new store tests).

- [ ] **Step 6: Write the store schema**

Create `engine/internal/store/schema.go`:
```go
package store

// schemaSQL is the Plan 3 schema (market-data plane). Plan 4 adds
// exec_events/fills. All timestamps are epoch milliseconds (INTEGER),
// matching the domain's TsMs/BucketMs int64 fields.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS journal (
  day     TEXT    NOT NULL,   -- ET trading day, "YYYY-MM-DD"
  seq     INTEGER NOT NULL,   -- per-day monotonic, arrival order
  ts_exch INTEGER NOT NULL,   -- event exchange ts (ms); ts_recv for conn/resync events
  ts_recv INTEGER NOT NULL,   -- pipeline receive ts (ms) — metadata only
  symbol  TEXT    NOT NULL,   -- "" for conn/resync events
  kind    TEXT    NOT NULL,   -- ticks|quote|book|bars1m|conn_up|conn_down|resynced
  seed    INTEGER NOT NULL,   -- 0/1: feed.Event Seed flag
  payload TEXT    NOT NULL,   -- JSON of the whole feed.Event struct
  PRIMARY KEY (day, seq)
);
CREATE TABLE IF NOT EXISTS bars_1m (
  symbol TEXT NOT NULL, ts INTEGER NOT NULL,
  o REAL, h REAL, l REAL, c REAL, v INTEGER,
  PRIMARY KEY (symbol, ts)
);
CREATE TABLE IF NOT EXISTS bars_daily (
  symbol TEXT NOT NULL, ts INTEGER NOT NULL,
  o REAL, h REAL, l REAL, c REAL, v INTEGER,
  PRIMARY KEY (symbol, ts)
);
CREATE TABLE IF NOT EXISTS config (
  key TEXT PRIMARY KEY, value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sys_events (
  seq    INTEGER PRIMARY KEY AUTOINCREMENT,
  ts     INTEGER NOT NULL,
  kind   TEXT NOT NULL,
  detail TEXT NOT NULL
);
`
```

- [ ] **Step 7: Write the store skeleton test (failing)**

Create `engine/internal/store/store_test.go`:
```go
package store

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// open makes a temp-file store with a fast flush for tests.
func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Options{
		Path:          t.TempDir() + "/test.db",
		Clock:         clock.NewFake(time.UnixMilli(0)),
		FlushInterval: time.Hour, // tests drive flushing explicitly via Flush()
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenCreatesSchemaAndWAL(t *testing.T) {
	s := open(t)
	// All five tables exist.
	for _, tbl := range []string{"journal", "bars_1m", "bars_daily", "config", "sys_events"} {
		var name string
		row := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl)
		if err := row.Scan(&name); err != nil {
			t.Fatalf("table %s missing: %v", tbl, err)
		}
	}
	// WAL is on.
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestFlushAndCloseAreSafeWhenEmpty(t *testing.T) {
	s := open(t)
	s.Flush() // no queued writes — must not hang
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
```

- [ ] **Step 8: Run it — verify it fails**

Run: `cd engine && go test ./internal/store/ -run TestOpen -v`
Expected: FAIL — `Open`/`Store` undefined.

- [ ] **Step 9: Implement the store skeleton**

Create `engine/internal/store/store.go`:
```go
// Package store is eTape's SQLite persistence: the always-on feed journal,
// 1m/daily bar archives, config docs, and sys_events. Exactly one goroutine
// executes writes (batched transactions); reads use the shared *sql.DB under
// WAL. It imports domain types (feed, session, clock) for serialization but
// never md/opend/uihub.
package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // driver name "sqlite"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// Store owns the SQLite handle and the single writer goroutine.
type Store struct {
	db     *sql.DB
	clk    clock.Clock
	writes chan writeOp
	batch  int

	wg        sync.WaitGroup
	closeOnce sync.Once    // Close is idempotent (tests Close explicitly AND via t.Cleanup)
	dropped   atomicUint64 // journal rows dropped on encode failure (see RecordEvent, Task 3)

	daySeq map[string]int64 // per-day next-seq cache; writer goroutine ONLY (Task 3)
}

type pendingWrite struct {
	query string
	args  []any
}

// writeOp is one queued mutation; render (called in the writer goroutine only)
// turns it into SQL statements.
type writeOp interface{ render() []pendingWrite }

// flushReq is a synchronous barrier; it renders nothing and signals done after
// the buffer is committed.
type flushReq struct{ done chan struct{} }

func (flushReq) render() []pendingWrite { return nil }

// Open opens (creating if absent) the SQLite DB, applies WAL pragmas, migrates
// the schema, and starts the writer goroutine.
func Open(opt Options) (*Store, error) {
	if opt.Clock == nil {
		opt.Clock = clock.System{}
	}
	if opt.FlushInterval <= 0 {
		opt.FlushInterval = 250 * time.Millisecond
	}
	if opt.BatchMax <= 0 {
		opt.BatchMax = 512
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"+
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", opt.Path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", opt.Path, err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	s := &Store{
		db:     db,
		clk:    opt.Clock,
		writes: make(chan writeOp, 4096),
		batch:  opt.BatchMax,
		daySeq: make(map[string]int64),
	}
	s.wg.Add(1)
	go s.writer(opt.FlushInterval)
	return s, nil
}

// Flush blocks until every write queued before the call is committed.
func (s *Store) Flush() {
	done := make(chan struct{})
	s.writes <- flushReq{done: done}
	<-done
}

// Close stops the writer (final-flushing buffered writes) and closes the DB.
// Idempotent: safe to call more than once (e.g. an explicit Close plus a
// t.Cleanup). Callers must ensure no producer still calls RecordEvent/Archive*/
// SetConfig/AppendSysEvent after Close begins — a send on the closed channel
// panics (cmd/etape joins its feed pipe before Close; see Task 10).
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.writes)
		s.wg.Wait()
	})
	return s.db.Close()
}

// writer is the single write goroutine: batch until the flush ticker fires,
// the batch cap is hit, or a barrier arrives; commit in one transaction.
func (s *Store) writer(flush time.Duration) {
	defer s.wg.Done()
	ticker := s.clk.NewTicker(flush)
	defer ticker.Stop()
	var buf []pendingWrite
	commit := func() {
		if len(buf) == 0 {
			return
		}
		s.commit(buf)
		buf = buf[:0]
	}
	for {
		select {
		case op, ok := <-s.writes:
			if !ok { // channel closed by Close: final flush, then exit
				commit()
				return
			}
			if fr, isFlush := op.(flushReq); isFlush {
				commit()
				close(fr.done)
				continue
			}
			buf = append(buf, op.render()...)
			if len(buf) >= s.batch {
				commit()
			}
		case <-ticker.C():
			commit()
		}
	}
}

// commit applies a batch in one transaction; on failure it logs loudly and
// drops the batch (honesty policy: journal degrades, market data never blocks).
func (s *Store) commit(buf []pendingWrite) {
	tx, err := s.db.Begin()
	if err != nil {
		slog.Error("store: begin tx", "err", err, "batch", len(buf))
		return
	}
	for _, pw := range buf {
		if _, err := tx.Exec(pw.query, pw.args...); err != nil {
			slog.Error("store: exec", "err", err, "query", pw.query)
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Error("store: commit", "err", err, "batch", len(buf))
		_ = tx.Rollback()
	}
}
```

Add the `Options` type and the tiny `atomicUint64` alias to the same file (the `"sync/atomic"` import is already in the import block above — do NOT add a second `import` statement):
```go
// Options configures Open.
type Options struct {
	Path          string
	Clock         clock.Clock
	FlushInterval time.Duration
	BatchMax      int
}

type atomicUint64 = atomic.Uint64
```

- [ ] **Step 10: Run store tests — verify they pass**

Run: `cd engine && go test -race ./internal/store/ -v`
Expected: PASS (schema/WAL + empty flush/close).

- [ ] **Step 11: Full gate + commit**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all pass.
```bash
cd engine
git add go.mod go.sum internal/config/config.go internal/config/config_test.go internal/store/store.go internal/store/schema.go internal/store/store_test.go
git commit -m "feat(engine/store): SQLite/WAL skeleton - config [store], schema, batched single-writer"
```

## Task 2: Journal event codec — `feed.Event` ⇄ JSON, kind/seed/symbol/exch-ts helpers

The determinism-critical pure layer: marshal any `feed.Event` to JSON and reconstruct it byte-for-byte, plus the column-extraction helpers the writer uses. No struct tags exist on the feed types, so JSON keys are the Go field names; `kind` selects the target type on decode. Tested in isolation so a reviewer can reject the codec independently.

**Files:**
- Create: `engine/internal/store/codec.go`
- Create: `engine/internal/store/codec_test.go`

**Interfaces:**
- Consumes: `feed.*` (Plan 2), `session.DayMs`/`session.Loc` (Plan 2), stdlib `encoding/json`.
- Produces (used by Task 3 writer + Task 4 reader):

```go
const (
	kindTicks    = "ticks"
	kindQuote    = "quote"
	kindBook     = "book"
	kindBars1m   = "bars1m"
	kindConnUp   = "conn_up"
	kindConnDown = "conn_down"
	kindResynced = "resynced"
)
func eventKind(ev feed.Event) string             // one of the kind* consts
func eventSeed(ev feed.Event) bool               // Seed flag; false for conn/resync
func eventSymbol(ev feed.Event) string           // primary symbol; "" for conn/resync/empty batches
func eventExchTs(ev feed.Event, fallback int64) int64 // primary exchange ts ms; fallback if none
func encodePayload(ev feed.Event) ([]byte, error)     // json of the whole event struct
func decodePayload(kind string, payload []byte) (feed.Event, error)
func dayKey(tsMs int64) string                   // ET day "YYYY-MM-DD" for a ms timestamp
```

- [ ] **Step 1: Write the round-trip test (failing)**

Create `engine/internal/store/codec_test.go`:
```go
package store

import (
	"reflect"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// every representative event, including the Seed flag and slice batches.
func sampleEvents() []feed.Event {
	return []feed.Event{
		feed.TicksEvent{Seed: false, Ticks: []feed.Tick{
			{Symbol: "US.AAPL", Seq: 1, TsMs: 1_700_000_000_000, Price: 309.12, Volume: 100, Turnover: 30912, Dir: feed.Buy, RecvTsMs: 1_700_000_000_050},
			{Symbol: "US.AAPL", Seq: 2, TsMs: 1_700_000_001_000, Price: 309.10, Volume: 50, Dir: feed.Sell},
		}},
		feed.TicksEvent{Seed: true, Ticks: []feed.Tick{
			{Symbol: "US.MSFT", Seq: 10, TsMs: 1_700_000_002_000, Price: 400, Volume: 5, Dir: feed.Neutral},
		}},
		feed.QuoteEvent{Seed: false, Quote: feed.Quote{Symbol: "US.AAPL", TsMs: 1_700_000_003_000, Last: 309.2, Open: 300, High: 310, Low: 299, PrevClose: 301, Volume: 12345, Turnover: 3_800_000}},
		feed.QuoteEvent{Seed: true, Quote: feed.Quote{Symbol: "US.MSFT", TsMs: 1_700_000_004_000, Last: 401}},
		feed.BookEvent{Seed: false, Book: feed.Book{Symbol: "US.AAPL", TsMs: 1_700_000_005_000,
			Bids: []feed.BookLevel{{Price: 309.1, Volume: 300, Orders: 4}, {Price: 309.0, Volume: 500, Orders: 7}},
			Asks: []feed.BookLevel{{Price: 309.2, Volume: 200, Orders: 3}}}},
		feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{
			{Symbol: "US.AAPL", BucketMs: 1_700_000_040_000, O: 100, H: 101, L: 99.5, C: 100.4, Volume: 4000, Turnover: 400000}}},
		feed.Bars1mEvent{Seed: false, Bars: []feed.Bar{
			{Symbol: "US.AAPL", BucketMs: 1_700_000_100_000, O: 100.4, H: 100.9, L: 100.1, C: 100.8, Volume: 900}}},
		feed.ConnUpEvent{},
		feed.ConnDownEvent{},
		feed.ResyncedEvent{},
	}
}

func TestCodecRoundTrip(t *testing.T) {
	for i, ev := range sampleEvents() {
		payload, err := encodePayload(ev)
		if err != nil {
			t.Fatalf("event %d encode: %v", i, err)
		}
		got, err := decodePayload(eventKind(ev), payload)
		if err != nil {
			t.Fatalf("event %d decode: %v", i, err)
		}
		if !reflect.DeepEqual(ev, got) {
			t.Fatalf("event %d round-trip mismatch:\n in: %#v\nout: %#v", i, ev, got)
		}
	}
}

func TestEventColumnHelpers(t *testing.T) {
	tks := feed.TicksEvent{Ticks: []feed.Tick{{Symbol: "US.AAPL", TsMs: 555}}}
	if eventKind(tks) != kindTicks || eventSymbol(tks) != "US.AAPL" || eventExchTs(tks, 9) != 555 {
		t.Fatalf("ticks helpers wrong: %s %s %d", eventKind(tks), eventSymbol(tks), eventExchTs(tks, 9))
	}
	if eventSeed(feed.QuoteEvent{Seed: true}) != true {
		t.Fatal("QuoteEvent seed not read")
	}
	cu := feed.ConnUpEvent{}
	if eventKind(cu) != kindConnUp || eventSymbol(cu) != "" || eventExchTs(cu, 42) != 42 {
		t.Fatalf("conn helpers wrong: %s %q %d", eventKind(cu), eventSymbol(cu), eventExchTs(cu, 42))
	}
}

func TestDecodeUnknownKind(t *testing.T) {
	if _, err := decodePayload("bogus", []byte("{}")); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestDayKey(t *testing.T) {
	// 2026-07-06 13:30:00 UTC == 09:30 ET (EDT). Day key must be the ET date.
	const ms = int64(1783344600_000)
	if got := dayKey(ms); got != "2026-07-06" {
		t.Fatalf("dayKey = %q, want 2026-07-06", got)
	}
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/store/ -run 'Codec|EventColumn|DecodeUnknown|DayKey' -v`
Expected: FAIL — codec functions undefined.

- [ ] **Step 3: Implement the codec**

Create `engine/internal/store/codec.go`:
```go
package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

const (
	kindTicks    = "ticks"
	kindQuote    = "quote"
	kindBook     = "book"
	kindBars1m   = "bars1m"
	kindConnUp   = "conn_up"
	kindConnDown = "conn_down"
	kindResynced = "resynced"
)

// eventKind returns the journal `kind` discriminator for ev.
func eventKind(ev feed.Event) string {
	switch ev.(type) {
	case feed.TicksEvent:
		return kindTicks
	case feed.QuoteEvent:
		return kindQuote
	case feed.BookEvent:
		return kindBook
	case feed.Bars1mEvent:
		return kindBars1m
	case feed.ConnUpEvent:
		return kindConnUp
	case feed.ConnDownEvent:
		return kindConnDown
	case feed.ResyncedEvent:
		return kindResynced
	default:
		return ""
	}
}

// eventSeed reports the Seed flag (false for conn/resync events).
func eventSeed(ev feed.Event) bool {
	switch e := ev.(type) {
	case feed.TicksEvent:
		return e.Seed
	case feed.QuoteEvent:
		return e.Seed
	case feed.BookEvent:
		return e.Seed
	case feed.Bars1mEvent:
		return e.Seed
	default:
		return false
	}
}

// eventSymbol returns the primary symbol, or "" (conn/resync, empty batch).
func eventSymbol(ev feed.Event) string {
	switch e := ev.(type) {
	case feed.TicksEvent:
		if len(e.Ticks) > 0 {
			return e.Ticks[0].Symbol
		}
	case feed.QuoteEvent:
		return e.Quote.Symbol
	case feed.BookEvent:
		return e.Book.Symbol
	case feed.Bars1mEvent:
		if len(e.Bars) > 0 {
			return e.Bars[0].Symbol
		}
	}
	return ""
}

// eventExchTs returns the primary exchange ts (ms), or fallback when the event
// carries none (conn/resync, empty batch).
func eventExchTs(ev feed.Event, fallback int64) int64 {
	switch e := ev.(type) {
	case feed.TicksEvent:
		if len(e.Ticks) > 0 {
			return e.Ticks[0].TsMs
		}
	case feed.QuoteEvent:
		return e.Quote.TsMs
	case feed.BookEvent:
		return e.Book.TsMs
	case feed.Bars1mEvent:
		if len(e.Bars) > 0 {
			return e.Bars[0].BucketMs
		}
	}
	return fallback
}

// encodePayload marshals the whole event struct. No struct tags exist on the
// feed types, so JSON keys are the exported Go field names — stable and lossless
// (Go's json round-trips float64 exactly).
func encodePayload(ev feed.Event) ([]byte, error) {
	return json.Marshal(ev)
}

// decodePayload reconstructs a feed.Event: kind selects the concrete type,
// json.Unmarshal fills it (including the Seed flag inside the payload).
func decodePayload(kind string, payload []byte) (feed.Event, error) {
	switch kind {
	case kindTicks:
		var v feed.TicksEvent
		return v, json.Unmarshal(payload, &v)
	case kindQuote:
		var v feed.QuoteEvent
		return v, json.Unmarshal(payload, &v)
	case kindBook:
		var v feed.BookEvent
		return v, json.Unmarshal(payload, &v)
	case kindBars1m:
		var v feed.Bars1mEvent
		return v, json.Unmarshal(payload, &v)
	case kindConnUp:
		return feed.ConnUpEvent{}, nil
	case kindConnDown:
		return feed.ConnDownEvent{}, nil
	case kindResynced:
		return feed.ResyncedEvent{}, nil
	default:
		return nil, fmt.Errorf("store: unknown journal kind %q", kind)
	}
}

// dayKey formats the ET trading day ("YYYY-MM-DD") containing a ms timestamp.
func dayKey(tsMs int64) string {
	return time.UnixMilli(session.DayMs(tsMs)).In(session.Loc()).Format("2006-01-02")
}
```

Note: `decodePayload` returns `v` (a value, not `&v`) so the concrete type matches what `encodePayload` was given — `reflect.DeepEqual` in the test then compares like types. The `json.Unmarshal(payload, &v)` error is returned alongside; when nil, `v` holds the decoded event.

- [ ] **Step 4: Run codec tests — verify they pass**

Run: `cd engine && go test -race ./internal/store/ -run 'Codec|EventColumn|DecodeUnknown|DayKey' -v`
Expected: PASS.

- [ ] **Step 5: Full gate + commit**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all pass.
```bash
cd engine
git add internal/store/codec.go internal/store/codec_test.go
git commit -m "feat(engine/store): journal event codec - feed.Event<->JSON round-trip, column helpers"
```

## Task 3: Journal append — `RecordEvent`, per-day seq assignment

The high-frequency write path: `RecordEvent(ev, recvMs)` enqueues a `recordOp`; the writer (single goroutine) assigns a per-day monotonic `seq` in arrival order, extracts the columns via the Task 2 helpers, and batches the INSERT. Blocking-by-design (like `md.Core.Feed`): the journal is the practice-data asset, so completeness beats silent loss; the writer drains-and-logs on commit failure, so a full channel means genuine overload surfaced upstream.

**Files:**
- Modify: `engine/internal/store/store.go` (add `RecordEvent`, `recordOp`, `nextSeq`, `maxSeq`, `DroppedJournalRows`)
- Create: `engine/internal/store/journal.go` (SQL const + `RecordEvent` doc home)
- Create: `engine/internal/store/journal_test.go`

**Interfaces:**
- Consumes: codec helpers (Task 2), `feed.Event`.
- Produces (used by Task 4 reader + `cmd/etape` tee):

```go
func (s *Store) RecordEvent(ev feed.Event, recvMs int64) // blocking enqueue
func (s *Store) DroppedJournalRows() uint64              // rows dropped on encode failure
```

- [ ] **Step 1: Write the append/ordering test (failing)**

Create `engine/internal/store/journal_test.go`:
```go
package store

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// recvBase: 2026-07-06 09:30 ET in ms (used so every event lands on one day).
const recvBase = int64(1783344600_000)

func TestRecordAssignsMonotonicSeqPerDay(t *testing.T) {
	s := open(t)
	evs := sampleEvents()
	for i, ev := range evs {
		s.RecordEvent(ev, recvBase+int64(i)) // monotonic recv, same ET day
	}
	s.Flush()

	rows, err := s.db.Query("SELECT seq, kind, seed, symbol FROM journal WHERE day='2026-07-06' ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int
	var prev int64
	for rows.Next() {
		var seq int64
		var kind, symbol string
		var seed int
		if err := rows.Scan(&seq, &kind, &seed, &symbol); err != nil {
			t.Fatal(err)
		}
		n++
		if seq != prev+1 {
			t.Fatalf("seq gap: got %d after %d", seq, prev)
		}
		prev = seq
	}
	if n != len(evs) {
		t.Fatalf("rows = %d, want %d", n, len(evs))
	}
}

func TestRecordContinuesSeqAcrossReopen(t *testing.T) {
	path := t.TempDir() + "/j.db"
	s1, err := Open(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	s1.RecordEvent(feed.ConnUpEvent{}, recvBase)
	s1.RecordEvent(feed.ConnDownEvent{}, recvBase+1)
	s1.Flush()
	_ = s1.Close()

	s2, err := Open(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.RecordEvent(feed.ResyncedEvent{}, recvBase+2)
	s2.Flush()

	var maxSeq int64
	if err := s2.db.QueryRow("SELECT MAX(seq) FROM journal WHERE day='2026-07-06'").Scan(&maxSeq); err != nil {
		t.Fatal(err)
	}
	if maxSeq != 3 {
		t.Fatalf("max seq after reopen = %d, want 3 (continues, no reset)", maxSeq)
	}
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/store/ -run TestRecord -v`
Expected: FAIL — `RecordEvent` undefined.

- [ ] **Step 3: Implement `RecordEvent` + seq assignment**

Create `engine/internal/store/journal.go`:
```go
package store

import (
	"database/sql"
	"errors"
	"log/slog"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

const journalInsertSQL = `INSERT INTO journal
	(day, seq, ts_exch, ts_recv, symbol, kind, seed, payload)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

// RecordEvent journals one feed event. Blocking by design: the journal is the
// practice-data asset, so back-pressure surfaces upstream rather than dropping.
// recvMs is the pipeline receive time (ms) — it sets the day partition and the
// ts_exch fallback for events without an exchange timestamp.
func (s *Store) RecordEvent(ev feed.Event, recvMs int64) {
	s.writes <- recordOp{s: s, ev: ev, recvMs: recvMs}
}

// DroppedJournalRows counts rows dropped because their payload failed to encode.
func (s *Store) DroppedJournalRows() uint64 { return s.dropped.Load() }

type recordOp struct {
	s      *Store
	ev     feed.Event
	recvMs int64
}

// render runs in the writer goroutine only, so touching s.daySeq is race-free.
func (o recordOp) render() []pendingWrite {
	payload, err := encodePayload(o.ev)
	if err != nil {
		o.s.dropped.Add(1)
		slog.Error("store: journal encode failed, row dropped", "err", err, "kind", eventKind(o.ev))
		return nil
	}
	day := dayKey(o.recvMs)
	seq := o.s.nextSeq(day)
	seed := 0
	if eventSeed(o.ev) {
		seed = 1
	}
	return []pendingWrite{{
		query: journalInsertSQL,
		args: []any{day, seq, eventExchTs(o.ev, o.recvMs), o.recvMs,
			eventSymbol(o.ev), eventKind(o.ev), seed, string(payload)},
	}}
}

// nextSeq returns the next per-day seq, seeding from the DB max on first use of
// a day (so restarts mid-day continue rather than collide). Writer goroutine only.
func (s *Store) nextSeq(day string) int64 {
	seq, ok := s.daySeq[day]
	if !ok {
		seq = s.maxSeq(day)
	}
	seq++
	s.daySeq[day] = seq
	return seq
}

func (s *Store) maxSeq(day string) int64 {
	var m sql.NullInt64 // not `max`: avoid shadowing the builtin (predeclared linter)
	if err := s.db.QueryRow("SELECT MAX(seq) FROM journal WHERE day=?", day).Scan(&m); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Error("store: maxSeq query", "err", err, "day", day)
	}
	if m.Valid {
		return m.Int64
	}
	return 0
}
```

- [ ] **Step 4: Run journal tests — verify they pass**

Run: `cd engine && go test -race ./internal/store/ -run TestRecord -v`
Expected: PASS.

- [ ] **Step 5: Full gate + commit**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all pass.
```bash
cd engine
git add internal/store/store.go internal/store/journal.go internal/store/journal_test.go
git commit -m "feat(engine/store): journal append - RecordEvent, per-day monotonic seq, batched insert"
```

## Task 4: Journal reader — `ReadJournalDay`, `JournalDays`

The replay source: read a day's rows in `seq` order and decode each `payload` back to a `feed.Event`, returning rows rich enough for the replay clock (`ts_exch`). `JournalDays` lists recorded days for `--replay` discovery and retention.

**Files:**
- Modify: `engine/internal/store/journal.go` (add reader + `JournalRow`)
- Modify: `engine/internal/store/journal_test.go` (add reader test)

**Interfaces:**
- Consumes: codec (Task 2), journal rows (Task 3).
- Produces (used by Task 9 `replay.Feed` + Task 11 capstone + `cmd/etape`):

```go
type JournalRow struct {
	Seq     int64
	TsExch  int64
	TsRecv  int64
	Day     string
	Symbol  string
	Kind    string
	Seed    bool
	Event   feed.Event
}
func (s *Store) ReadJournalDay(day string) ([]JournalRow, error) // ordered by seq
func (s *Store) JournalDays() ([]string, error)                  // distinct days, ascending
```

- [ ] **Step 1: Write the round-trip reader test (failing)**

Add to `engine/internal/store/journal_test.go`:
```go
import "reflect" // add to the import block

func TestReadJournalDayRoundTrips(t *testing.T) {
	s := open(t)
	in := sampleEvents()
	for i, ev := range in {
		s.RecordEvent(ev, recvBase+int64(i))
	}
	s.Flush()

	rows, err := s.ReadJournalDay("2026-07-06")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(in) {
		t.Fatalf("read %d rows, want %d", len(rows), len(in))
	}
	for i, r := range rows {
		if r.Seq != int64(i+1) {
			t.Fatalf("row %d seq = %d, want %d", i, r.Seq, i+1)
		}
		if !reflect.DeepEqual(r.Event, in[i]) {
			t.Fatalf("row %d event mismatch:\n in: %#v\nout: %#v", i, in[i], r.Event)
		}
	}
}

func TestJournalDaysDistinct(t *testing.T) {
	s := open(t)
	// One event on 2026-07-06, one a day later (recvBase + 24h).
	s.RecordEvent(feed.ConnUpEvent{}, recvBase)
	s.RecordEvent(feed.ConnUpEvent{}, recvBase+24*3600*1000)
	s.Flush()
	days, err := s.JournalDays()
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0] != "2026-07-06" || days[1] != "2026-07-07" {
		t.Fatalf("days = %v, want [2026-07-06 2026-07-07]", days)
	}
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/store/ -run 'ReadJournalDay|JournalDays' -v`
Expected: FAIL — `ReadJournalDay`/`JournalDays` undefined.

- [ ] **Step 3: Implement the reader**

Add to `engine/internal/store/journal.go`:
```go
// JournalRow is one decoded journal entry.
type JournalRow struct {
	Seq    int64
	TsExch int64
	TsRecv int64
	Day    string
	Symbol string
	Kind   string
	Seed   bool
	Event  feed.Event
}

// ReadJournalDay returns a day's events in seq order, decoded to feed.Events.
func (s *Store) ReadJournalDay(day string) ([]JournalRow, error) {
	rows, err := s.db.Query(
		`SELECT seq, ts_exch, ts_recv, symbol, kind, seed, payload
		 FROM journal WHERE day=? ORDER BY seq`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JournalRow
	for rows.Next() {
		var r JournalRow
		var seed int
		var payload string
		if err := rows.Scan(&r.Seq, &r.TsExch, &r.TsRecv, &r.Symbol, &r.Kind, &seed, &payload); err != nil {
			return nil, err
		}
		r.Day = day
		r.Seed = seed != 0
		ev, err := decodePayload(r.Kind, []byte(payload))
		if err != nil {
			return nil, err
		}
		r.Event = ev
		out = append(out, r)
	}
	return out, rows.Err()
}

// JournalDays returns the distinct recorded days, ascending.
func (s *Store) JournalDays() ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT day FROM journal ORDER BY day")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run reader tests — verify they pass**

Run: `cd engine && go test -race ./internal/store/ -run 'ReadJournalDay|JournalDays' -v`
Expected: PASS.

- [ ] **Step 5: Full gate + commit**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all pass.
```bash
cd engine
git add internal/store/journal.go internal/store/journal_test.go
git commit -m "feat(engine/store): journal reader - ReadJournalDay (decoded, seq-ordered), JournalDays"
```

## Task 5: Bar archives — `ArchiveBar1m`/`ArchiveDaily` upsert + read-back

Persistent OHLCV archives fed by the md core's finalized 1m/daily bars (the `cmd/etape` tap in Task 10 converts `md.Bar → feed.Bar`). Idempotent upsert (`INSERT OR REPLACE`) so re-emitted finalized bars after a resync overwrite cleanly. Read-back returns `[]feed.Bar` for Plan 6's chart-open backfill (`SeedHistory1m`/`SeedDaily`).

**Files:**
- Create: `engine/internal/store/bars.go`
- Create: `engine/internal/store/bars_test.go`

**Interfaces:**
- Consumes: `feed.Bar` (Plan 2). `store` never imports `md`.
- Produces (used by `cmd/etape` tap + Plan 6):

```go
func (s *Store) ArchiveBar1m(b feed.Bar) // async upsert into bars_1m
func (s *Store) ArchiveDaily(b feed.Bar) // async upsert into bars_daily
func (s *Store) ReadBars1m(symbol string, fromMs, toMs int64) ([]feed.Bar, error) // [from,to], ascending
func (s *Store) ReadDailyBars(symbol string) ([]feed.Bar, error)                  // all, ascending
```

- [ ] **Step 1: Write the archive test (failing)**

Create `engine/internal/store/bars_test.go`:
```go
package store

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestArchive1mUpsertAndRead(t *testing.T) {
	s := open(t)
	s.ArchiveBar1m(feed.Bar{Symbol: "US.AAPL", BucketMs: 1000, O: 10, H: 11, L: 9, C: 10.5, Volume: 100})
	s.ArchiveBar1m(feed.Bar{Symbol: "US.AAPL", BucketMs: 2000, O: 10.5, H: 12, L: 10, C: 11.8, Volume: 200})
	// Re-finalize the first bucket with corrected values — must REPLACE, not duplicate.
	s.ArchiveBar1m(feed.Bar{Symbol: "US.AAPL", BucketMs: 1000, O: 10, H: 11.5, L: 9, C: 11, Volume: 150})
	s.Flush()

	got, err := s.ReadBars1m("US.AAPL", 0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("bars = %d, want 2 (upsert, no dupes)", len(got))
	}
	if got[0].BucketMs != 1000 || got[0].C != 11 || got[0].Volume != 150 {
		t.Fatalf("bucket 1000 not replaced: %+v", got[0])
	}
	if got[1].BucketMs != 2000 {
		t.Fatalf("ordering wrong: %+v", got)
	}
	// Range filter excludes bucket 2000.
	only1, err := s.ReadBars1m("US.AAPL", 0, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if len(only1) != 1 || only1[0].BucketMs != 1000 {
		t.Fatalf("range filter wrong: %+v", only1)
	}
}

func TestArchiveDailyReadAll(t *testing.T) {
	s := open(t)
	s.ArchiveDaily(feed.Bar{Symbol: "US.AAPL", BucketMs: 200, O: 1, H: 2, L: 1, C: 2, Volume: 9})
	s.ArchiveDaily(feed.Bar{Symbol: "US.AAPL", BucketMs: 100, O: 1, H: 2, L: 1, C: 2, Volume: 8})
	s.Flush()
	got, err := s.ReadDailyBars("US.AAPL")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].BucketMs != 100 || got[1].BucketMs != 200 {
		t.Fatalf("daily read not ascending: %+v", got)
	}
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/store/ -run TestArchive -v`
Expected: FAIL — archive methods undefined.

- [ ] **Step 3: Implement the archives**

Create `engine/internal/store/bars.go`:
```go
package store

import "github.com/earlisreal/eTape/engine/internal/feed"

const (
	bar1mUpsertSQL  = `INSERT OR REPLACE INTO bars_1m (symbol, ts, o, h, l, c, v) VALUES (?, ?, ?, ?, ?, ?, ?)`
	dailyUpsertSQL  = `INSERT OR REPLACE INTO bars_daily (symbol, ts, o, h, l, c, v) VALUES (?, ?, ?, ?, ?, ?, ?)`
	bars1mSelectSQL = `SELECT ts, o, h, l, c, v FROM bars_1m WHERE symbol=? AND ts>=? AND ts<=? ORDER BY ts`
	dailySelectSQL  = `SELECT ts, o, h, l, c, v FROM bars_daily WHERE symbol=? ORDER BY ts`
)

type barOp struct {
	query string
	b     feed.Bar
}

func (o barOp) render() []pendingWrite {
	return []pendingWrite{{
		query: o.query,
		args:  []any{o.b.Symbol, o.b.BucketMs, o.b.O, o.b.H, o.b.L, o.b.C, o.b.Volume},
	}}
}

// ArchiveBar1m upserts a finalized 1m bar. Idempotent by (symbol, ts).
func (s *Store) ArchiveBar1m(b feed.Bar) { s.writes <- barOp{query: bar1mUpsertSQL, b: b} }

// ArchiveDaily upserts a daily bar (official auction OHLCV). Idempotent.
func (s *Store) ArchiveDaily(b feed.Bar) { s.writes <- barOp{query: dailyUpsertSQL, b: b} }

// ReadBars1m returns 1m bars in [fromMs, toMs], ascending.
func (s *Store) ReadBars1m(symbol string, fromMs, toMs int64) ([]feed.Bar, error) {
	return s.readBars(bars1mSelectSQL, symbol, fromMs, toMs)
}

// ReadDailyBars returns all daily bars for a symbol, ascending.
func (s *Store) ReadDailyBars(symbol string) ([]feed.Bar, error) {
	return s.readBars(dailySelectSQL, symbol)
}

func (s *Store) readBars(query string, args ...any) ([]feed.Bar, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	symbol, _ := args[0].(string)
	var out []feed.Bar
	for rows.Next() {
		b := feed.Bar{Symbol: symbol}
		if err := rows.Scan(&b.BucketMs, &b.O, &b.H, &b.L, &b.C, &b.Volume); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run archive tests — verify they pass**

Run: `cd engine && go test -race ./internal/store/ -run TestArchive -v`
Expected: PASS.

- [ ] **Step 5: Full gate + commit**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all pass.
```bash
cd engine
git add internal/store/bars.go internal/store/bars_test.go
git commit -m "feat(engine/store): bar archives - 1m/daily idempotent upsert + range/all read-back"
```

## Task 6: `config` + `sys_events` tables — CRUD and append/query

Low-frequency accessors Plan 6 consumes: JSON config documents (workspaces, templates, thresholds) and the system-event log (connects, gaps, quota pressure, auto-disarms). Writes go through the writer channel (single-writer discipline); `Flush()` before reading gives read-after-write in tests.

**Files:**
- Create: `engine/internal/store/config.go`, `engine/internal/store/sysevents.go`
- Create: `engine/internal/store/aux_test.go`

**Interfaces:**
- Produces (used by Plan 6 uihub + Task 7 retention logging):

```go
func (s *Store) SetConfig(key, value string)     // async upsert
func (s *Store) DeleteConfig(key string)         // async
func (s *Store) GetConfig(key string) (string, bool, error)
func (s *Store) ListConfig() (map[string]string, error)
type SysEvent struct { Seq int64; TsMs int64; Kind, Detail string }
func (s *Store) AppendSysEvent(kind, detail string) // async; ts = s.clk.Now()
func (s *Store) RecentSysEvents(n int) ([]SysEvent, error) // newest first
```

- [ ] **Step 1: Write the CRUD test (failing)**

Create `engine/internal/store/aux_test.go`:
```go
package store

import "testing"

func TestConfigCRUD(t *testing.T) {
	s := open(t)
	s.SetConfig("hotkeys", `{"buy":"b"}`)
	s.SetConfig("theme", `"dark"`)
	s.Flush()

	v, ok, err := s.GetConfig("hotkeys")
	if err != nil || !ok || v != `{"buy":"b"}` {
		t.Fatalf("GetConfig hotkeys = %q ok=%v err=%v", v, ok, err)
	}
	if _, ok, _ := s.GetConfig("missing"); ok {
		t.Fatal("missing key reported present")
	}
	// Overwrite + delete.
	s.SetConfig("theme", `"light"`)
	s.DeleteConfig("hotkeys")
	s.Flush()
	all, err := s.ListConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all["theme"] != `"light"` {
		t.Fatalf("ListConfig = %v, want {theme:\"light\"}", all)
	}
}

func TestSysEventsAppendRecent(t *testing.T) {
	s := open(t) // Fake clock frozen at ms 0
	s.AppendSysEvent("boot", "engine up")
	s.AppendSysEvent("gap", "US.AAPL feed gap")
	s.Flush()
	evs, err := s.RecentSysEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("sys_events = %d, want 2", len(evs))
	}
	if evs[0].Kind != "gap" || evs[1].Kind != "boot" { // newest first
		t.Fatalf("order wrong: %+v", evs)
	}
	if evs[0].TsMs != 0 {
		t.Fatalf("ts not from injected clock: %d", evs[0].TsMs)
	}
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/store/ -run 'ConfigCRUD|SysEvents' -v`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement config CRUD**

Create `engine/internal/store/config.go`:
```go
package store

import (
	"database/sql"
	"errors"
)

const (
	configUpsertSQL = `INSERT OR REPLACE INTO config (key, value) VALUES (?, ?)`
	configDeleteSQL = `DELETE FROM config WHERE key=?`
)

type configOp struct {
	query string
	args  []any
}

func (o configOp) render() []pendingWrite { return []pendingWrite{{query: o.query, args: o.args}} }

// SetConfig upserts a JSON config document. Async — call Flush for durability.
func (s *Store) SetConfig(key, value string) {
	s.writes <- configOp{query: configUpsertSQL, args: []any{key, value}}
}

// DeleteConfig removes a config key. Async.
func (s *Store) DeleteConfig(key string) {
	s.writes <- configOp{query: configDeleteSQL, args: []any{key}}
}

// GetConfig reads one key; ok is false when absent.
func (s *Store) GetConfig(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow("SELECT value FROM config WHERE key=?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// ListConfig returns all config documents.
func (s *Store) ListConfig() (map[string]string, error) {
	rows, err := s.db.Query("SELECT key, value FROM config")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Implement sys_events**

Create `engine/internal/store/sysevents.go`:
```go
package store

const sysEventInsertSQL = `INSERT INTO sys_events (ts, kind, detail) VALUES (?, ?, ?)`

// SysEvent is one system-log entry.
type SysEvent struct {
	Seq    int64
	TsMs   int64
	Kind   string
	Detail string
}

type sysEventOp struct {
	ts     int64
	kind   string
	detail string
}

func (o sysEventOp) render() []pendingWrite {
	return []pendingWrite{{query: sysEventInsertSQL, args: []any{o.ts, o.kind, o.detail}}}
}

// AppendSysEvent logs a system event stamped with the store clock. Async.
func (s *Store) AppendSysEvent(kind, detail string) {
	s.writes <- sysEventOp{ts: s.clk.Now().UnixMilli(), kind: kind, detail: detail}
}

// RecentSysEvents returns the newest n events, newest first.
func (s *Store) RecentSysEvents(n int) ([]SysEvent, error) {
	rows, err := s.db.Query("SELECT seq, ts, kind, detail FROM sys_events ORDER BY seq DESC LIMIT ?", n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SysEvent
	for rows.Next() {
		var e SysEvent
		if err := rows.Scan(&e.Seq, &e.TsMs, &e.Kind, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run aux tests — verify they pass**

Run: `cd engine && go test -race ./internal/store/ -run 'ConfigCRUD|SysEvents' -v`
Expected: PASS.

- [ ] **Step 6: Full gate + commit**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all pass.
```bash
cd engine
git add internal/store/config.go internal/store/sysevents.go internal/store/aux_test.go
git commit -m "feat(engine/store): config CRUD + sys_events append/query"
```

## Task 7: Retention — prune journal at boot by ET day

Bounded disk: at boot, delete journal rows older than `RetentionDays` trading days; bar archives are kept forever (the analytical asset). The cutoff is an ET day string, and `day` compares lexicographically = chronologically. Logs the outcome to `sys_events`.

**Files:**
- Create: `engine/internal/store/retention.go`
- Create: `engine/internal/store/retention_test.go`

**Interfaces:**
- Consumes: `s.clk`, `session.DayMs`/`session.Loc`, `AppendSysEvent`.
- Produces (used by `cmd/etape` boot):

```go
func (s *Store) PruneJournal(retentionDays int) (deleted int64, err error) // rows deleted
```

- [ ] **Step 1: Write the retention test (failing)**

Create `engine/internal/store/retention_test.go`:
```go
package store

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestPruneJournalByDay(t *testing.T) {
	// Clock "now" = 2026-07-06 12:00 ET. Retention 2 days keeps 07-05, 07-06;
	// drops 07-01. Archives untouched.
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, mustLoc(t))
	s, err := Open(Options{Path: t.TempDir() + "/r.db", Clock: clock.NewFake(now)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	day := func(y, m, d int) int64 {
		return time.Date(y, time.Month(m), d, 10, 0, 0, 0, mustLoc(t)).UnixMilli()
	}
	// Comments on their own lines so gofmt has no trailing-comment block to align.
	s.RecordEvent(feed.ConnUpEvent{}, day(2026, 7, 1)) // old: pruned
	s.RecordEvent(feed.ConnUpEvent{}, day(2026, 7, 5)) // kept
	s.RecordEvent(feed.ConnUpEvent{}, day(2026, 7, 6)) // kept
	// Bar archives are never pruned.
	s.ArchiveDaily(feed.Bar{Symbol: "US.AAPL", BucketMs: day(2026, 1, 1), C: 1})
	s.Flush()

	deleted, err := s.PruneJournal(2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	days, _ := s.JournalDays()
	if len(days) != 2 || days[0] != "2026-07-05" || days[1] != "2026-07-06" {
		t.Fatalf("remaining days = %v, want [2026-07-05 2026-07-06]", days)
	}
	daily, _ := s.ReadDailyBars("US.AAPL")
	if len(daily) != 1 {
		t.Fatalf("archive pruned! got %d daily bars, want 1", len(daily))
	}
}

func mustLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/store/ -run TestPrune -v`
Expected: FAIL — `PruneJournal` undefined.

- [ ] **Step 3: Implement retention**

Create `engine/internal/store/retention.go`:
```go
package store

import (
	"fmt"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// PruneJournal deletes journal rows older than retentionDays trading days
// (bar archives are kept forever). retentionDays <= 0 is a no-op. The cutoff
// day is inclusive-keep: rows on or after it are retained.
func (s *Store) PruneJournal(retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoffMs := s.clk.Now().AddDate(0, 0, -retentionDays).UnixMilli()
	cutoffDay := time.UnixMilli(session.DayMs(cutoffMs)).In(session.Loc()).Format("2006-01-02")
	res, err := s.db.Exec("DELETE FROM journal WHERE day < ?", cutoffDay)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	s.AppendSysEvent("retention", fmt.Sprintf("pruned %d journal rows before %s (retention %dd)", n, cutoffDay, retentionDays))
	return n, nil
}
```

Note: `PruneJournal` calls `db.Exec` directly (a single bulk DELETE at boot, before the high-frequency journal stream starts) rather than routing through the writer channel — it is a boot-time maintenance op, not part of the hot path. This is the one sanctioned direct write; it runs before any `RecordEvent`, so it never races the writer.

- [ ] **Step 4: Run retention test — verify it passes**

Run: `cd engine && go test -race ./internal/store/ -run TestPrune -v`
Expected: PASS.

- [ ] **Step 5: Full gate + commit**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all pass.
```bash
cd engine
git add internal/store/retention.go internal/store/retention_test.go
git commit -m "feat(engine/store): retention - prune journal older than N trading days at boot"
```

## Task 8: `replay.Clock` — simulated `clock.Clock` driven by journal timestamps

The time seam for replay. It wraps a `clock.Fake` (reusing its tested, order-correct timer/ticker firing) and adds `AdvanceTo(absoluteTime)` — which `replay.Feed` calls per event so any component reading the clock (Plan 4 exec, Plan 6 pollers/coalescing) sees simulated session time, not wall time. Plan 2's `clock` package is untouched.

**Files:**
- Create: `engine/internal/replay/clock.go`
- Create: `engine/internal/replay/clock_test.go`

**Interfaces:**
- Consumes: `clock.Fake`, `clock.Ticker`, `clock.Clock` (Plan 2).
- Produces (used by Task 9 + Task 10):

```go
type Clock struct{ /* wraps *clock.Fake */ }
func NewClock(start time.Time) *Clock
func (c *Clock) Now() time.Time
func (c *Clock) After(d time.Duration) <-chan time.Time
func (c *Clock) NewTicker(d time.Duration) clock.Ticker
func (c *Clock) AdvanceTo(t time.Time) // forward-only; no-op if t <= now
var _ clock.Clock = (*Clock)(nil)
```

- [ ] **Step 1: Write the clock test (failing)**

Create `engine/internal/replay/clock_test.go`:
```go
package replay

import (
	"testing"
	"time"
)

func TestClockAdvanceToForwardOnly(t *testing.T) {
	start := time.UnixMilli(1000)
	c := NewClock(start)
	if !c.Now().Equal(start) {
		t.Fatalf("Now = %v, want %v", c.Now(), start)
	}
	c.AdvanceTo(time.UnixMilli(500)) // backwards → no-op
	if c.Now().UnixMilli() != 1000 {
		t.Fatalf("backwards AdvanceTo moved clock: %d", c.Now().UnixMilli())
	}
	c.AdvanceTo(time.UnixMilli(3000))
	if c.Now().UnixMilli() != 3000 {
		t.Fatalf("Now = %d, want 3000", c.Now().UnixMilli())
	}
}

func TestClockTickerFiresOnAdvance(t *testing.T) {
	c := NewClock(time.UnixMilli(0))
	tk := c.NewTicker(time.Second)
	defer tk.Stop()
	c.AdvanceTo(time.UnixMilli(2500)) // crosses 1s and 2s
	select {
	case <-tk.C():
	default:
		t.Fatal("ticker did not fire after AdvanceTo crossed its interval")
	}
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/replay/ -v`
Expected: FAIL — package/`NewClock` undefined.

- [ ] **Step 3: Implement `replay.Clock`**

Create `engine/internal/replay/clock.go`:
```go
// Package replay is a feed.Feed + clock.Clock that reconstructs a recorded
// session from the store journal. It is an adapter (like feed/opend): it may
// import store and the domain packages, never the other way around.
package replay

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// Clock is a simulated clock driven by journal event timestamps. It wraps a
// clock.Fake (whose Advance fires due timers/tickers in chronological order)
// and exposes absolute-time stepping via AdvanceTo.
type Clock struct{ f *clock.Fake }

// NewClock returns a Clock frozen at start (the first event's timestamp).
func NewClock(start time.Time) *Clock { return &Clock{f: clock.NewFake(start)} }

func (c *Clock) Now() time.Time                         { return c.f.Now() }
func (c *Clock) After(d time.Duration) <-chan time.Time { return c.f.After(d) }
func (c *Clock) NewTicker(d time.Duration) clock.Ticker { return c.f.NewTicker(d) }

// AdvanceTo moves simulated time forward to t, firing any due timers/tickers.
// Forward-only: an earlier or equal t is a no-op (journal ts_exch can be flat
// or, for conn events, reuse a neighbor's recv time).
func (c *Clock) AdvanceTo(t time.Time) {
	if d := t.Sub(c.f.Now()); d > 0 {
		c.f.Advance(d)
	}
}

var _ clock.Clock = (*Clock)(nil)
```

- [ ] **Step 4: Run clock tests — verify they pass**

Run: `cd engine && go test -race ./internal/replay/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine
git add internal/replay/clock.go internal/replay/clock_test.go
git commit -m "feat(engine/replay): simulated Clock - clock.Clock + AdvanceTo over clock.Fake"
```

## Task 9: `replay.Feed` — journal-backed `feed.Feed`

The replay data source: re-emit a day's journal rows as `feed.Event`s in `seq` order, advancing the simulated clock to each event's `ts_exch`, optionally throttled to real-time × speed. Demands are no-ops (the journal already contains exactly what was subscribed); history/snapshot queries return `ErrUnsupported` (md seeds arrive as recorded seed events, not queries). Closing `Events()` at end-of-journal lets consumers detect the session's end.

**Files:**
- Create: `engine/internal/replay/feed.go`
- Create: `engine/internal/replay/feed_test.go`

**Interfaces:**
- Consumes: `store.JournalRow` (Task 4), `replay.Clock` (Task 8), `clock.Clock`, `feed.*`.
- Produces (used by Task 10 `cmd/etape` + Task 11 capstone):

```go
type FeedOptions struct {
	Rows     []store.JournalRow // a day's rows, seq-ordered (from store.ReadJournalDay)
	Sim      *Clock             // simulated clock, advanced per event
	Pace     clock.Clock        // real clock for playback throttle; nil = no throttle
	Speed    float64            // >0: real-time × Speed; <=0: as fast as possible
	EventBuf int                // Events() capacity, default 4096
}
func NewFeed(opt FeedOptions) *Feed
func (f *Feed) Events() <-chan feed.Event
func (f *Feed) Run(ctx context.Context) error // emits all rows then closes Events()
var _ feed.Feed = (*Feed)(nil)
var ErrUnsupported = errors.New("replay: query not supported (seeds arrive as journal events)")
```

- [ ] **Step 1: Write the feed test (failing)**

Create `engine/internal/replay/feed_test.go`:
```go
package replay

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/store"
)

func TestFeedEmitsInSeqOrderAndAdvancesClock(t *testing.T) {
	rows := []store.JournalRow{
		{Seq: 1, TsExch: 1000, Kind: "conn_up", Event: feed.ConnUpEvent{}},
		{Seq: 2, TsExch: 2000, Kind: "ticks", Event: feed.TicksEvent{Ticks: []feed.Tick{{Symbol: "US.AAPL", TsMs: 2000, Price: 10}}}},
		{Seq: 3, TsExch: 3000, Kind: "resynced", Event: feed.ResyncedEvent{}},
	}
	sim := NewClock(time.UnixMilli(1000))
	f := NewFeed(FeedOptions{Rows: rows, Sim: sim, Speed: 0}) // no throttle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()

	var got []feed.Event
	for ev := range f.Events() { // closes at end-of-journal
		got = append(got, ev)
	}
	if len(got) != 3 {
		t.Fatalf("emitted %d events, want 3", len(got))
	}
	if _, ok := got[0].(feed.ConnUpEvent); !ok {
		t.Fatalf("first event = %T, want ConnUpEvent", got[0])
	}
	if _, ok := got[1].(feed.TicksEvent); !ok {
		t.Fatalf("second event = %T, want TicksEvent", got[1])
	}
	if sim.Now().UnixMilli() != 3000 {
		t.Fatalf("sim clock = %d, want 3000 (advanced to last event)", sim.Now().UnixMilli())
	}
}

func TestFeedQueriesUnsupported(t *testing.T) {
	f := NewFeed(FeedOptions{Sim: NewClock(time.UnixMilli(0))})
	if _, err := f.RecentTicks(context.Background(), "US.AAPL", 10); err != ErrUnsupported {
		t.Fatalf("RecentTicks err = %v, want ErrUnsupported", err)
	}
	if _, err := f.BookSnapshot(context.Background(), "US.AAPL"); err != ErrUnsupported {
		t.Fatalf("BookSnapshot err = %v, want ErrUnsupported", err)
	}
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/replay/ -run TestFeed -v`
Expected: FAIL — `NewFeed` undefined.

- [ ] **Step 3: Implement `replay.Feed`**

Create `engine/internal/replay/feed.go`:
```go
package replay

import (
	"context"
	"errors"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// ErrUnsupported is returned by replay's query methods: the md core's seeds
// arrive as recorded seed events on Events(), not through backfill queries.
var ErrUnsupported = errors.New("replay: query not supported (seeds arrive as journal events)")

// FeedOptions configures NewFeed.
type FeedOptions struct {
	Rows     []store.JournalRow
	Sim      *Clock
	Pace     clock.Clock
	Speed    float64
	EventBuf int
}

// Feed replays a day's journal rows as a feed.Feed.
type Feed struct {
	rows  []store.JournalRow
	sim   *Clock
	pace  clock.Clock
	speed float64
	out   chan feed.Event
}

// NewFeed builds a replay Feed. Run must be started before Events is drained.
func NewFeed(opt FeedOptions) *Feed {
	if opt.EventBuf <= 0 {
		opt.EventBuf = 4096
	}
	return &Feed{
		rows:  opt.Rows,
		sim:   opt.Sim,
		pace:  opt.Pace,
		speed: opt.Speed,
		out:   make(chan feed.Event, opt.EventBuf),
	}
}

// Events is the replayed stream; it closes when the journal is exhausted.
func (f *Feed) Events() <-chan feed.Event { return f.out }

// Run emits every row in order, advancing the simulated clock to each event's
// ts_exch and (when Speed>0 and Pace!=nil) throttling to real-time × Speed.
func (f *Feed) Run(ctx context.Context) error {
	defer close(f.out)
	var prev int64
	for i, r := range f.rows {
		if i > 0 && f.speed > 0 && f.pace != nil {
			if gap := r.TsExch - prev; gap > 0 {
				d := time.Duration(float64(gap)/f.speed) * time.Millisecond
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-f.pace.After(d):
				}
			}
		}
		if f.sim != nil {
			f.sim.AdvanceTo(time.UnixMilli(r.TsExch))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f.out <- r.Event:
		}
		prev = r.TsExch
	}
	return nil
}

// Ensure/Release are no-ops: the journal already holds exactly what was subscribed.
func (f *Feed) Ensure(feed.Demand) {}
func (f *Feed) Release(string)     {}

// Query methods are unsupported in replay.
func (f *Feed) HistoryBars(context.Context, string, feed.Resolution, time.Time, time.Time) ([]feed.Bar, error) {
	return nil, ErrUnsupported
}
func (f *Feed) RecentTicks(context.Context, string, int) ([]feed.Tick, error) {
	return nil, ErrUnsupported
}
func (f *Feed) CachedBars1m(context.Context, string, int) ([]feed.Bar, error) {
	return nil, ErrUnsupported
}
func (f *Feed) BookSnapshot(context.Context, string) (feed.Book, error) {
	return feed.Book{}, ErrUnsupported
}
func (f *Feed) QuoteSnapshot(context.Context, string) (feed.Quote, error) {
	return feed.Quote{}, ErrUnsupported
}

var _ feed.Feed = (*Feed)(nil)
```

- [ ] **Step 4: Run feed tests — verify they pass**

Run: `cd engine && go test -race ./internal/replay/ -v`
Expected: PASS.

- [ ] **Step 5: Full gate + commit**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all pass.
```bash
cd engine
git add internal/replay/feed.go internal/replay/feed_test.go
git commit -m "feat(engine/replay): journal-backed feed.Feed - seq-ordered emit, sim-clock pacing"
```

## Task 10: `cmd/etape` wiring — journal tee, bar-archive tap, `--replay`/`--speed`, boot prune

Turns recording on and makes replay real. The live path opens the store, prunes at boot, journals every event in the feed→core pipe, and taps finalized 1m/daily bars into the archive. `--replay <day>` swaps the OpenD feed for `replay.Feed` + `replay.Clock` (no re-recording), reconstructing the session through the identical md core. This harness is replaced by Plan 6's full boot sequence; behavior is proven by Task 11.

**Files:**
- Modify (replace): `engine/cmd/etape/main.go`

**Interfaces:**
- Consumes: `store` (Open/RecordEvent/ArchiveBar1m/ArchiveDaily/ReadJournalDay/PruneJournal/AppendSysEvent), `replay` (NewFeed/NewClock), `md`, `feed`, `opend`, `config`, `clock`, `session`.
- Produces: the `etape` binary with `--replay`/`--speed` flags; no exported Go API.

- [ ] **Step 1: Replace `cmd/etape/main.go`**

Write `engine/cmd/etape/main.go` in full:
```go
// Command etape is the eTape engine. In this plan it is the market-data +
// persistence harness: connect OpenD → journal tee → md core, archive finalized
// bars, and (with --replay) reconstruct a recorded day from the journal. Plan 6
// replaces main with the full boot sequence (store → uihub → OpenD → exec).
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/replay"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
)

func main() {
	home, _ := os.UserHomeDir()
	cfgPath := flag.String("config", filepath.Join(home, ".eTape", "config.toml"), "path to config.toml")
	watch := flag.String("watch", "", "comma-separated symbols to watch (adds to config watchlist)")
	focus := flag.String("focus", "", "comma-separated symbols to focus (adds depth + quote)")
	replayDay := flag.String("replay", "", "replay a recorded day (YYYY-MM-DD) instead of connecting to OpenD")
	speed := flag.Float64("speed", 0, "replay speed factor (>0: real-time x speed; <=0: as fast as possible)")
	verbose := flag.Bool("v", false, "log quotes/books/tape (noisy)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}
	anchorSecs, err := cfg.MD.AnchorSecs()
	if err != nil {
		log.Error("bad session_anchor", "err", err)
		os.Exit(1)
	}
	dbPath := cfg.Store.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(home, ".eTape", "etape.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Error("make db dir", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(store.Options{
		Path:          dbPath,
		Clock:         clock.System{},
		FlushInterval: time.Duration(cfg.Store.FlushMs) * time.Millisecond,
	})
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	// NOTE: no `defer st.Close()`. The feed pipe writes to the store on a blocking
	// send, so the store must be closed ONLY after that goroutine has stopped —
	// otherwise RecordEvent races close(s.writes) (send on closed channel → panic).
	// We join pipeWG below, then Close explicitly.

	core := md.New(md.Config{TapeRing: cfg.MD.TapeRing, AnchorSecs: anchorSecs})
	go func() { _ = core.Run(ctx) }()
	go drainMarks(ctx, core)

	var pipeWG sync.WaitGroup // tracks the feed→core pipe goroutine(s)
	live := *replayDay == ""
	if live {
		if n, err := st.PruneJournal(cfg.Store.RetentionDays); err != nil {
			log.Warn("prune journal", "err", err)
		} else if n > 0 {
			log.Info("pruned journal", "rows", n)
		}
		st.AppendSysEvent("boot", "engine up")
		startLive(ctx, log, st, core, cfg, splitCSV(*watch), splitCSV(*focus), &pipeWG)
		log.Info("engine up (live)", "opend", cfg.OpenD.Addr(), "anchor", cfg.MD.SessionAnchor, "db", dbPath)
	} else {
		startReplay(ctx, log, st, core, *replayDay, *speed, splitCSV(*focus), &pipeWG)
	}

	// Consume updates until shutdown; tap finalized 1m/daily bars to the archive
	// (live only — replay must not rewrite the archive it reads from).
	var archive *store.Store
	if live {
		archive = st
	}
	drainUpdates(ctx, log, core, archive, *verbose)

	// ctx is done: join the pipe (no more store writes) BEFORE closing the store.
	pipeWG.Wait()
	if err := st.Close(); err != nil {
		log.Error("close store", "err", err)
	}
	log.Info("shutdown complete", "droppedUpdates", core.DroppedUpdates(), "droppedJournal", st.DroppedJournalRows())
}

// startLive wires OpenD → journal tee → core and installs demands + indicators.
func startLive(ctx context.Context, log *slog.Logger, st *store.Store, core *md.Core, cfg config.Config, watch, focus []string, pipeWG *sync.WaitGroup) {
	client := opend.New(opend.Options{Addr: cfg.OpenD.Addr(), Clock: clock.System{}})
	fd := opend.NewOpenDFeed(client, opend.FeedOptions{
		Budget:              cfg.Feed.QuotaSlots,
		Hysteresis:          time.Duration(cfg.Feed.UnsubHysteresisSecs) * time.Second,
		DisableExtendedTime: !cfg.Feed.ExtendedTime,
	})
	go func() { _ = client.Run(ctx) }()
	go func() { _ = fd.Run(ctx) }()
	pipeWG.Add(1)
	go pipe(ctx, pipeWG, fd.Events(), core, st) // st != nil → journal tee active

	seen := 0
	for _, s := range append(cfg.Feed.Watchlist, watch...) {
		fd.Ensure(feed.WatchDemand("boot-watch-"+s, s))
		seen++
	}
	for _, s := range focus {
		fd.Ensure(feed.FocusedDemand("boot-focus-"+s, s))
		seen++
	}
	if seen == 0 {
		log.Warn("no symbols demanded; pass --watch/--focus or set [feed].watchlist")
	}
	setupIndicators(core, focus)
}

// startReplay wires replay.Feed → core (no journal tee) from a recorded day.
func startReplay(ctx context.Context, log *slog.Logger, st *store.Store, core *md.Core, day string, speed float64, focus []string, pipeWG *sync.WaitGroup) {
	rows, err := st.ReadJournalDay(day)
	if err != nil {
		log.Error("read journal", "err", err, "day", day)
		return
	}
	if len(rows) == 0 {
		log.Warn("no journal rows for day", "day", day)
		return
	}
	sim := replay.NewClock(time.UnixMilli(rows[0].TsExch))
	fd := replay.NewFeed(replay.FeedOptions{Rows: rows, Sim: sim, Pace: clock.System{}, Speed: speed})
	go func() { _ = fd.Run(ctx) }()
	pipeWG.Add(1)
	go pipe(ctx, pipeWG, fd.Events(), core, nil) // nil journal → no re-recording
	setupIndicators(core, focus)
	log.Info("engine up (replay)", "day", day, "rows", len(rows), "speed", speed)
}

// pipe forwards feed events into the core, journaling each first when journal != nil.
// It owns a pipeWG slot so main can join it before closing the store.
func pipe(ctx context.Context, wg *sync.WaitGroup, in <-chan feed.Event, core *md.Core, journal *store.Store) {
	defer wg.Done()
	sys := clock.System{}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-in:
			if !ok { // replay feed exhausted
				return
			}
			if journal != nil {
				journal.RecordEvent(ev, sys.Now().UnixMilli())
			}
			core.Feed(ev)
		}
	}
}

func setupIndicators(core *md.Core, focus []string) {
	if len(focus) == 0 {
		return
	}
	f := focus[0]
	core.EnsureIndicator("harness-vwap", md.IndicatorSpec{Symbol: f, TF: session.TF1m, Type: md.IndVWAP})
	core.EnsureIndicator("harness-ema9", md.IndicatorSpec{Symbol: f, TF: session.TF1m, Type: md.IndEMA,
		Params: map[string]float64{"period": 9}})
}

func drainMarks(ctx context.Context, core *md.Core) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-core.Marks():
		}
	}
}

func drainUpdates(ctx context.Context, log *slog.Logger, core *md.Core, archive *store.Store, verbose bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-core.Updates():
			switch v := u.(type) {
			case md.BarUpdate:
				if v.Bar.InProgress {
					continue
				}
				if archive != nil {
					b := feed.Bar{Symbol: v.Bar.Symbol, BucketMs: v.Bar.BucketMs,
						O: v.Bar.O, H: v.Bar.H, L: v.Bar.L, C: v.Bar.C, Volume: v.Bar.V}
					switch v.Bar.TF {
					case session.TF1m:
						archive.ArchiveBar1m(b)
					case session.TFDay:
						archive.ArchiveDaily(b)
					}
				}
				log.Info("bar", "sym", v.Bar.Symbol, "tf", v.Bar.TF, "bucket", v.Bar.BucketMs,
					"o", v.Bar.O, "h", v.Bar.H, "l", v.Bar.L, "c", v.Bar.C,
					"v", v.Bar.V, "delta", v.Bar.BuyV-v.Bar.SellV, "ticks", v.Bar.Ticks, "gap", v.Bar.Gap)
			case md.IndicatorUpdate:
				if v.Snapshot {
					log.Info("indicator snapshot", "id", v.InstanceID, "key", v.SeriesKey, "points", len(v.Points))
				}
			case md.MismatchUpdate:
				log.Warn("1m mismatch", "sym", v.Symbol, "bucket", v.BucketMs, "detail", v.Detail)
			case md.ConnUpdate:
				log.Info("feed connection", "up", v.Up)
			case md.ResyncedUpdate:
				log.Info("feed resynced")
			case md.QuoteUpdate:
				if verbose {
					log.Info("quote", "sym", v.Quote.Symbol, "last", v.Quote.Last)
				}
			case md.BookUpdate:
				if verbose {
					log.Info("book", "sym", v.Book.Symbol, "bids", len(v.Book.Bids), "asks", len(v.Book.Asks))
				}
			case md.TapeUpdate:
				if verbose {
					log.Info("tape", "sym", v.Symbol, "ticks", len(v.Ticks))
				}
			}
		}
	}
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
```

- [ ] **Step 2: Build + gate**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all pass (no new unit test here; existing suites green; Task 11 proves replay behavior).

- [ ] **Step 3: Manual smoke — record then replay (documented; live leg needs OpenD)**

If OpenD is running (`127.0.0.1:11111`), record a short live session, then replay it:
```bash
cd engine
# Record ~30s to a scratch DB (Ctrl-C to stop):
go run ./cmd/etape --config /dev/null --focus US.AAPL   # writes ~/.eTape/etape.db
# Find the recorded day and replay it as fast as possible:
go run ./cmd/etape --config /dev/null --replay "$(date +%Y-%m-%d)" --speed 0 --focus US.AAPL
```
Expected: the replay run logs `engine up (replay)` with a non-zero row count and re-emits the same finalized bars the live run logged. (No OpenD → skip; Task 11 covers the behavior in CI.)

- [ ] **Step 4: Commit**

```bash
cd engine
git add cmd/etape/main.go
git commit -m "feat(engine): journal tee + bar-archive tap; --replay/--speed reconstruction; boot prune"
```

## Task 11: Capstone — `replay(journal) == live`, byte-identical updates

The plan's deliverable as an executable proof: a scripted feed-event day, recorded to SQLite, read back, and replayed through `replay.Feed` into a fresh md core, produces the byte-identical update stream a direct live run produces — extending Task 13's `replay(events) == state` across the persistence boundary. An external test package (`replay_test`) so `replay`'s production package never imports `md`.

**Files:**
- Create: `engine/internal/replay/determinism_test.go` (`package replay_test`)

**Interfaces:**
- Consumes: `replay.NewFeed`/`NewClock`, `store.Open`/`RecordEvent`/`ReadJournalDay`, `md.New`/`Feed`/`Updates`/`EnsureIndicator`, `feed.*`, `session`.

- [ ] **Step 1: Write the capstone test**

Create `engine/internal/replay/determinism_test.go`:
```go
package replay_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/replay"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// capBase: 2026-07-06 09:30 ET in ms (all scripted events land on one day).
const capBase = int64(1783344600_000)

// scriptEvents is a deterministic mixed-event day: seed 1m bars, batched ticks,
// a quote, a book, a live 1m bar, a conn/resync cycle, and a re-seed.
func scriptEvents() []feed.Event {
	tk := func(seq, offMs int64, px float64, v int64, d feed.Direction) feed.Tick {
		return feed.Tick{Symbol: "US.AAPL", Seq: seq, TsMs: capBase + offMs, Price: px, Volume: v, Dir: d}
	}
	return []feed.Event{
		feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{
			{Symbol: "US.AAPL", BucketMs: capBase - 120_000, O: 99, H: 99.5, L: 98.9, C: 99.2, Volume: 800},
			{Symbol: "US.AAPL", BucketMs: capBase - 60_000, O: 99.2, H: 100, L: 99.1, C: 99.9, Volume: 900},
		}},
		feed.TicksEvent{Ticks: []feed.Tick{tk(1, 0, 100.0, 120, feed.Buy), tk(2, 1500, 100.2, 80, feed.Sell)}},
		feed.TicksEvent{Ticks: []feed.Tick{tk(3, 12_000, 100.1, 60, feed.Neutral), tk(4, 25_000, 100.4, 200, feed.Buy)}},
		feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", TsMs: capBase + 30_000, Last: 100.4}},
		feed.BookEvent{Book: feed.Book{Symbol: "US.AAPL", TsMs: capBase + 30_000,
			Bids: []feed.BookLevel{{Price: 100.39, Volume: 300}},
			Asks: []feed.BookLevel{{Price: 100.41, Volume: 200}}}},
		feed.Bars1mEvent{Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: capBase, O: 100, H: 101, L: 99.5, C: 100.4, Volume: 4000}}},
		feed.ConnDownEvent{}, feed.ConnUpEvent{}, feed.ResyncedEvent{},
		feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: capBase, O: 100, H: 101, L: 99.5, C: 100.4, Volume: 4000}}},
	}
}

// collect runs a fresh core, registers a fixed indicator pair, feeds events via
// feedInto, and returns every update in order. Mirrors md's determinism harness.
func collect(t *testing.T, feedInto func(feedOne func(feed.Event))) []md.Update {
	t.Helper()
	c := md.New(md.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()

	var got []md.Update
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for {
			select {
			case u := <-c.Updates():
				got = append(got, u)
			case <-done:
				for {
					select {
					case u := <-c.Updates():
						got = append(got, u)
					default:
						return
					}
				}
			}
		}
	}()

	c.EnsureIndicator("vwap-1", md.IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: md.IndVWAP})
	c.EnsureIndicator("ema-1", md.IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: md.IndEMA,
		Params: map[string]float64{"period": 2}})
	feedInto(c.Feed)
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	<-collected
	return got
}

func TestReplayJournalMatchesLive(t *testing.T) {
	evs := scriptEvents()

	// Live: feed the scripted events straight into a fresh core.
	live := collect(t, func(feedOne func(feed.Event)) {
		for _, ev := range evs {
			feedOne(ev)
		}
	})

	// Journal round-trip: record → read → replay.Feed → fresh core.
	replayed := collect(t, func(feedOne func(feed.Event)) {
		s, err := store.Open(store.Options{Path: t.TempDir() + "/cap.db"})
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer s.Close()
		for i, ev := range evs {
			s.RecordEvent(ev, capBase+int64(i))
		}
		s.Flush()
		rows, err := s.ReadJournalDay("2026-07-06")
		if err != nil {
			t.Fatalf("read journal: %v", err)
		}
		// Codec + ordering proof: read-back events equal the recorded ones.
		if len(rows) != len(evs) {
			t.Fatalf("journal rows = %d, want %d", len(rows), len(evs))
		}
		for i := range rows {
			if !reflect.DeepEqual(rows[i].Event, evs[i]) {
				t.Fatalf("row %d event mismatch:\n in: %#v\nout: %#v", i, evs[i], rows[i].Event)
			}
		}
		// Drive the replay feed into the core.
		sim := replay.NewClock(time.UnixMilli(rows[0].TsExch))
		rf := replay.NewFeed(replay.FeedOptions{Rows: rows, Sim: sim, Speed: 0})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = rf.Run(ctx) }()
		for ev := range rf.Events() { // closes at end-of-journal
			feedOne(ev)
		}
	})

	if len(live) != len(replayed) {
		t.Fatalf("update counts differ: live %d vs replay %d", len(live), len(replayed))
	}
	for i := range live {
		if !reflect.DeepEqual(live[i], replayed[i]) {
			t.Fatalf("update %d differs:\n live: %#v\nrepl: %#v", i, live[i], replayed[i])
		}
	}
}
```

> If this fails: a `len` mismatch means the codec or ordering lost/reordered an event (check `encodePayload`/`decodePayload` and the `seq` assignment); a per-update `DeepEqual` mismatch with equal lengths means nondeterminism in the md core surfaced by the round-trip (that is a Task 9–12 bug, not a store bug — do not loosen this test). The `time.Sleep(200ms)` drain is content-determinism, not timing; if it flakes, add an inbox-empty spin, never a longer sleep.

- [ ] **Step 2: Run it (repeat to shake out nondeterminism)**

Run: `cd engine && go test -race ./internal/replay/ -run TestReplayJournalMatchesLive -v -count=5`
Expected: PASS, 5/5.

- [ ] **Step 3: Full gate + commit**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run && go test -race ./...`
Expected: all pass.
```bash
cd engine
git add internal/replay/determinism_test.go
git commit -m "test(engine/replay): capstone - replay(journal)==live, byte-identical update stream"
```

---

## Self-Review

**Spec coverage** (go-engine-design §store, §replay; roadmap Plan 3):

| Spec requirement | Task |
|---|---|
| One SQLite/WAL writer goroutine | Task 1 (writer loop, WAL pragmas, batched txns) |
| Journal tee at the Feed boundary | Task 3 (`RecordEvent`) + Task 10 (`pipe` tee) |
| Journal records the full feed stream (seed incl.) | Task 2 (codec, all 7 variants + Seed) + Task 3 |
| `bars_1m` / `bars_daily` archives | Task 5 + Task 10 (finalized-bar tap) |
| `config` / `sys_events` tables | Task 1 (schema) + Task 6 (CRUD/append) |
| Retention / prune-at-boot | Task 7 + Task 10 (boot prune, live only) |
| `replay` journal-backed `feed.Feed` | Task 9 |
| Replay `clock` (event ts × speed) | Task 8 (`AdvanceTo`) + Task 9 (pacing) |
| `etape --replay <day> --speed N` | Task 10 |
| Byte-identical bars/indicators | Task 11 (capstone) |
| `exec_events`/`fills` NOT here | (deferred to Plan 4 — Global Constraints) |
| SimBroker NOT here | (deferred to Plan 4 — Deviations) |

**Placeholder scan:** none — every step carries complete code, exact commands, and expected output. No "TBD"/"add error handling"/"similar to Task N".

**Type consistency (checked against the worktree):**
- `store.Store` methods return/consume `feed.Bar` (has `Volume`), never `md.Bar` (has `V`); the `md.Bar → feed.Bar` conversion is confined to `drainUpdates` in `cmd/etape` (Task 10). `store` imports `feed`/`session`/`clock`, never `md`.
- `store.JournalRow` (Task 4) is consumed unchanged by `replay.FeedOptions.Rows` (Task 9) and the capstone (Task 11).
- `replay.Clock` satisfies `clock.Clock` (`Now`/`After`/`NewTicker(d) clock.Ticker`) — compile-time asserted; `replay.Feed` satisfies `feed.Feed` (all 5 query methods + `Events`/`Ensure`/`Release`) — compile-time asserted.
- Journal `kind` strings (`ticks|quote|book|bars1m|conn_up|conn_down|resynced`) are defined once in Task 2 and consumed by encode/decode symmetrically.
- `clock.NewFake(start)` / `clock.System{}` / `clock.Ticker` used exactly as their Plan 2 signatures; `session.DayMs`/`session.Loc`/`session.TF1m`/`session.TFDay` used as verified.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-05-engine-store-journal-replay.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
