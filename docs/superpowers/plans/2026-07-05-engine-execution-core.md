# eTape Engine — Plan 4 of 6: Execution Core (multi-venue)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the engine's broker-agnostic execution subsystem: the `exec` domain (venue-keyed `Order`/`Fill`/`Position`/`AccountSnapshot`, `"ET"`+ULID order IDs), the append-only exec event log persisted through Plan 3's SQLite writer, the venue-keyed single-writer fold (`replay(log) == state`) with fold-derived cross-venue aggregates, the two-layer safety gate (master/venue arm → duplicate → per-venue caps → global caps → day-loss auto-disarm), the `Broker` interface + `Capabilities`, and `SimBroker` — the whole subsystem verified end-to-end against SimBroker with table-driven fold + gate tests, race-free and deterministic.

**Architecture:** `exec` is a domain package: it imports only `clock` and `session` (domain siblings) and defines the `EventStore` and `Broker` interfaces that adapters (Plan 5) and the store (Plan 3, extended here) satisfy — it never imports `store`, `md`, `broker/*`, `feed/opend`, or `uihub`. One single-writer goroutine (the `exec.Core` coordinator) owns all execution state: commands, broker events, and last-trade marks enter its inbox; it runs the gate in-loop against one consistent multi-venue state, appends events to the store **synchronously** (append failure blocks the order — safety over availability), folds them, and dispatches broker I/O to off-loop worker goroutines so a slow venue never blocks the writer. `go test -race` enforces the discipline. Order-lifecycle events implement both `isExecEvent()` (persisted, event-sourced) and `isBrokerEvent()` (emitted by adapters), so the coordinator routes them with one type switch; account and positions are broker-reconciled snapshots (not in the log), so `replay(log) == state` scopes to order + fill state.

**Tech Stack:** Go 1.26.4 (module from Plans 1–3), `github.com/oklog/ulid/v2` (order IDs — added here), `modernc.org/sqlite` (via Plan 3's `store`), `github.com/BurntSushi/toml` (config), stdlib `encoding/json` (event codec), `go test -race` + `golangci-lint`. No broker networking in this plan — real adapters are Plan 5; this plan proves the chassis on `SimBroker`.

## Global Constraints

Copied verbatim (or tightly paraphrased) from the approved specs and Plans 1–3. Every task's requirements implicitly include this section.

- **Module path:** `github.com/earlisreal/eTape/engine`. All imports are `github.com/earlisreal/eTape/engine/...`.
- **Branch dependency:** this plan builds on **Plan 3** (`store`/`replay`), which lives on branch `worktree-engine-store-journal-replay` (worktree `.claude/worktrees/engine-store-journal-replay`) and is **not yet merged to main**. Execute Plan 4 on a branch cut from Plan 3's tip (or from `main` once Plan 3 merges). Plans 1–2 are on `main`. Never touch the main checkout's `ui/` directory (owned by concurrent UI sessions); stage files explicitly.
- **Dependency rule:** domain packages (`feed`, `md`, `exec`, `session`) never import adapters (`feed/opend`, `broker/*`), `uihub`, or `store`. `exec` imports only `clock` and `session`. The persistence seam is an `exec.EventStore` **interface** defined in `exec` and implemented by `*store.Store` (so `store` imports `exec`, never the reverse — the same direction as `store` importing `feed` in Plan 3). Adapters (`broker/sim` here; `broker/tradezero`, `broker/alpaca` in Plan 5) import `exec`, never the reverse. (go-engine-design §Dependency rule; multi-broker-execution-design §Ripple effects)
- **Single-writer core:** exactly one goroutine (`exec.Core`) owns execution domain state, consuming typed messages from its inbox; everything I/O-shaped (broker POSTs, `Broker.Events()` pumps, the marks feed) is its own goroutine that only passes messages. No mutexes in the domain. `go test -race` enforces "no shared execution state" as a checked invariant. (go-engine-design §Single-writer core; multi-broker-execution-design §Exec core)
- **Append-before-POST + append-blocks-submit:** `OrderSubmitted` is appended to `exec_events` **before** the adapter POST (crash recovery resolves dangling `Submitted` against the broker snapshot). Exec-event append failure **blocks** order submission (safety over availability) — the append is synchronous and error-returning, unlike the fire-and-forget journal path. (portfolio-orders-design §Error handling, §store; go-engine-design §Error handling)
- **Boot disarmed, always:** the engine boots with the master switch and every venue disarmed; arming is an explicit UI command; arm/disarm state is **not** persisted (boot is always off regardless of the log). (portfolio-orders-design §exec/gate; multi-broker-execution-design §Gate)
- **Every order names its venue:** the engine performs **zero** routing. `OrderRequest` requires a valid `Venue`; a request without one is malformed. Order IDs are `"ET"`+ULID — globally unique across venues and restarts by construction (28 chars; fits TZ's 36-char cap). (multi-broker-execution-design §Domain changes)
- **Gate limits are config, never code:** all caps live in the `[gate.global]` / `[gate.venue.<id>]` config sections. (portfolio-orders-design §exec/gate; multi-broker-execution-design §Gate)
- **Safety rule (standing):** never place, modify, or cancel real orders. This plan uses **only `SimBroker`** — no real credentials are read, no network broker is contacted. Credentials in `~/.eJournal/credentials.json` are untouched. Real adapters land in Plan 5. (CLAUDE.md)
- **Persisted timestamps are `INTEGER` epoch ms** (matching Plan 3's `journal`/`bars_*` and the domain's int64 `TsMs` fields), not the spec's `TEXT` ISO. (Plan 3 Global Constraints; store/schema.go)
- **Repo is PUBLIC; sensitive-sweep every commit.** No account identifiers or credentials in checked-in fixtures. SimBroker fixtures carry only synthetic symbols/prices. (memory: repo public)
- **CI gates:** `cd engine && go build ./... && go vet ./... && go test -race ./... && golangci-lint run` must all pass. Avoid shadowing the predeclared `max`/`min` (Plan 3 hit the `predeclared` linter — name locals `mx`, not `max`). (Makefile; .golangci.yml)

---

## Plan sequence context

This is **Plan 4 of 6** (roadmap in Plan 1's header: 1 Foundation/OpenD client → 2 Market-data core → 3 Store/journal/replay → **4 Execution core** → 5 Broker adapters → 6 uihub/pollers/main). Plan 4's deliverable: *the exec core accepts commands, gates them, folds events, and persists — verified end-to-end against SimBroker with table-driven fold + gate tests.*

**Consumed from Plans 1–3 (exact, verified against the worktrees):**

```go
// clock (clock/clock.go, clock/fake.go) — Plan 1/2
type Ticker interface { C() <-chan time.Time; Stop() }
type Clock interface { Now() time.Time; After(d time.Duration) <-chan time.Time; NewTicker(d time.Duration) Ticker }
type System struct{}                 // real clock
func NewFake(start time.Time) *Fake  // (*Fake).Advance(d) fires due wakers in order; NO Set/AdvanceTo

// session (session/session.go) — Plan 2
func Loc() *time.Location
func DayMs(tsMs int64) int64         // ET wall-midnight ms containing tsMs (the day-key helper)

// md (md/update.go) — Plan 2; the last-trade marks feed exec bridges in Plan 6
type Mark struct { Symbol string; Price float64; TsMs int64 }
func (c *md.Core) Marks() <-chan md.Mark   // keep-latest, drop-on-full

// config (config/config.go) — Plan 1/2/3, extended here
type Config struct { OpenD OpenD `toml:"opend"`; Feed Feed `toml:"feed"`; MD MD `toml:"md"`; Store Store `toml:"store"` }
func Default() Config
func Load(path string) (Config, error)     // decodes TOML over Default(); missing file → defaults

// store (store/store.go, journal.go, sysevents.go) — Plan 3, extended here
func Open(opt Options) (*Store, error)
func (s *Store) Flush()                     // synchronous barrier
func (s *Store) Close() error
func (s *Store) AppendSysEvent(kind, detail string)   // async
type pendingWrite struct { query string; args []any } // internal writer contract
type writeOp interface{ render() []pendingWrite }      // ops enqueued via s.writes
// s.db (*sql.DB, WAL) is read-concurrent; s.writes is the single-writer channel; s.clk is the store clock.
```

**Single-writer idiom to mirror** (from `md/core.go` — typed inbox, no mutex, shutdown via ctx, no panic-recover):

```go
func (c *Core) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case m := <-c.inbox:
            c.apply(m)
        }
    }
}
```

**Produced for later plans:**

- `exec.Core` — the coordinator. Plan 5 constructs it with real `broker/tradezero` + `broker/alpaca` instances (both satisfy the `exec.Broker` interface produced here). Plan 6 wires it into `cmd/etape` (config → store → exec boot: `Recover` then `Run`), pumps `md.Core.Marks()` → `Core.FeedMark`, exposes `Core.Do(cmd)` to WS commands and `Core.Updates()` to the `exec.*` WS topics.
- `exec.Broker` + `exec.Capabilities` — the adapter contract. Plan 5's TZ/Alpaca adapters and the v1.x moomoo adapter implement it.
- `exec.EventStore` + `store` exec methods (`AppendExecEvent`, `ReadExecEventsSince`, `QueryFills`) — Plan 6's `exec.fills` chart-backfill query reuses `QueryFills`.
- `broker/sim.Broker` — the E2E/replay/practice broker. Plan 6's Playwright E2E boots it alongside Plan 3's `replay.Feed`/`replay.Clock` to make the full `etape --replay` order-lifecycle walk.

**Deviations from the roadmap/specs, flagged:**

- **`cmd/etape` is NOT wired in this plan.** The deliverable is the exec subsystem verified by tests against SimBroker; full boot wiring (config → store → OpenD → pre-subscribe → exec) is Plan 6, and real brokers are Plan 5. Prior plans ended with a `cmd/etape` live smoke because they had a live feed to smoke; exec has no live broker until Plan 5, so the honest Plan 4 deliverable is the capstone test suite (Task 13).
- **`exec_events`/`fills` timestamps are `INTEGER` ms, not `TEXT`** (codebase convention; see Global Constraints). The spec's `seq INTEGER PRIMARY KEY AUTOINCREMENT` stands — the fold uses `last_insert_rowid()` as the returned seq and reads back `ORDER BY seq`.
- **Account/positions are broker-reconciled, not event-sourced** (per the exec spec): `Apply` handles the order log; `ApplyReconcile` handles account/position snapshots (not persisted). `replay(log) == state` is therefore a property over order + fill state; account/positions are re-fetched via `Broker.Snapshot` on boot.
- **Day-loss enforcement is BOTH a submit-time gate check AND auto-disarm.** The original design relied solely on auto-disarm (`BrokerAccount` events flip `MasterArmed` off on breach), reasoning that a submit after a breach would be caught by the master-armed check. The final whole-branch review found this "equivalent" claim false in two windows — an explicit re-arm after breach, and the gap before a venue's first account push — so `Evaluate` now calls `BreachedDayLoss` directly as an authoritative submit-time check (Task 8's `gate.go`), with auto-disarm remaining as the reactive layer on top.
- **SimBroker uses a marketability rule, not literal "immediate fill at limit."** The design-spec calls v1 SimBroker "dumb — immediate fill at limit price." Taken literally, a far-from-market limit could never rest, so the exec-spec integration walk (*far-from-market limit → replace → cancel → kill*) would be impossible to exercise. SimBroker therefore fills market orders (and marketable limits) immediately and **rests** non-marketable limits until canceled/replaced or crossed by a later mark (`SetMark`). This is the minimum realism the lifecycle E2E needs; queue-position/partial-fill realism is still deferred to v1.5.
- **`exec` defines its own `Mark` type** (identical shape to `md.Mark`) rather than importing `md`, keeping the execution domain decoupled from the market-data domain. Plan 6 bridges `md.Core.Marks()` → `Core.FeedMark` with a one-line copy.

---

## File Structure (Plan 4)

```
engine/
  go.mod, go.sum                              MODIFY  — add github.com/oklog/ulid/v2
  internal/
    config/
      config.go                     MODIFY  — [[venue]] array + [gate.global]/[gate.venue.<id>]
      config_test.go                MODIFY  — venue + gate parse/defaults
    exec/
      types.go                      NEW     — VenueID, Side, OrderType, TIF, OrderStatus, Order, Fill, Position, AccountSnapshot, OrderRequest, ReplaceRequest, OrderAck
      types_test.go                 NEW     — enum String(), OrderRequest.Validate
      orderid.go                    NEW     — OrderIDGen: "ET"+ULID
      orderid_test.go               NEW     — prefix/length, monotonic, unique
      events.go                     NEW     — Event union (isExecEvent), Source, envelope/fill helpers, JSON codec (EncodeEvent/DecodeEvent)
      events_test.go                NEW     — round-trip every variant, unknown-kind error
      broker.go                     NEW     — Broker iface, Capabilities, BrokerEvent union (isBrokerEvent), Mark, MarkSource, EventStore iface
      state.go                      NEW     — State, VenueState, orderIndex, Apply (log fold), DecodeAndApply
      state_test.go                 NEW     — table-driven Apply per event; replay(log)==state
      reconcile.go                  NEW     — ApplyReconcile (account/positions), arming setters, cross-venue aggregates
      reconcile_test.go             NEW     — snapshot overwrite, aggregate math
      gate.go                       NEW     — GateConfig, Evaluate (ordered rules), BreachedDayLoss
      gate_test.go                  NEW     — one test per rule, market-order valuation
      core.go                       NEW     — Core coordinator: inbox loop, Do, FeedMark, Updates, Recover, Run
      core_test.go                  NEW     — single-venue E2E: arm→submit→fill; disarmed→blocked
      update.go                     NEW     — Update union (isExecUpdate) for uihub
      capstone_test.go              NEW     — multi-venue E2E + aggregate gate + replay(log)==state across store
    broker/
      sim/
        sim.go                      NEW     — SimBroker: exec.Broker impl, marketability fills, SetMark
        sim_test.go                 NEW     — submit/accept/fill, rest, replace, cancel, cancelall, snapshot, caps
    store/
      exec.go                       NEW     — exec_events/fills schema + AppendExecEvent (sync), ReadExecEventsSince, QueryFills
      exec_test.go                  NEW     — append returns increasing seq, fill projection, read-back order, append error
      schema.go                     MODIFY  — add exec_events + fills DDL to schemaSQL
```

---

## Task 1: `exec` domain value types + `OrderRequest`/`Validate`

Stands up the `exec` package with the broker-agnostic value types every later task references. Pure data + enums with `String()`; the only behavior is `OrderRequest.Validate` (the "a request without a valid venue is malformed" rule). No I/O, no concurrency.

**Files:**
- Create: `engine/internal/exec/types.go`
- Create: `engine/internal/exec/types_test.go`

**Interfaces:**
- Consumes: nothing (leaf package).
- Produces (used by every later task):

```go
type VenueID string

type Side uint8
const ( SideBuy Side = iota; SideSell; SideShort; SideCover )   // has String(): "BUY"/"SELL"/"SHORT"/"COVER"

type OrderType uint8
const ( TypeMarket OrderType = iota; TypeLimit )                // has String(): "MARKET"/"LIMIT"

type TIF uint8
const ( TIFDay TIF = iota; TIFGTC; TIFIOC; TIFFOK )             // has String()

type OrderStatus uint8
const ( StatusSubmitted OrderStatus = iota; StatusAccepted; StatusPartiallyFilled;
        StatusFilled; StatusCanceled; StatusRejected; StatusExpired; StatusBlocked; StatusReplaced ) // has String()

type Order struct {
    Venue        VenueID
    ID           string        // "ET"+ULID clientOrderID
    Symbol       string
    Side         Side
    Type         OrderType
    TIF          TIF
    Qty          float64
    LimitPrice   float64
    StopPrice    float64
    Status       OrderStatus
    ExecutedQty  float64
    LeavesQty    float64
    AvgFillPrice float64
    RejectReason string
    ReplacesID   string        // links a replace-chain (TZ emulation); "" otherwise
    CreatedMs    int64
    UpdatedMs    int64
}

type Fill struct { Venue VenueID; OrderID, Symbol string; Side Side; Qty, Price float64; TsMs int64 }
type Position struct { Venue VenueID; Symbol string; Qty float64; AvgPrice float64 } // Qty signed: +long/-short
type AccountSnapshot struct {
    Venue VenueID
    Equity, BuyingPower, AvailableCash, SodEquity, Realized, DayPnL, Leverage float64
    TsMs int64
}

type OrderRequest struct {
    Venue      VenueID
    Symbol     string
    Side       Side
    Type       OrderType
    TIF        TIF
    Qty        float64
    LimitPrice float64
    StopPrice  float64
    ClientOrderID string          // set by Core (ET+ULID); adapters echo it
}
func (r OrderRequest) Validate() error

type ReplaceRequest struct { Qty, LimitPrice, StopPrice float64 } // new TOTAL qty
type OrderAck struct { OrderID string; Accepted bool; Message string }
```

- [ ] **Step 1: Write the failing test**

Create `engine/internal/exec/types_test.go`:
```go
package exec

import "testing"

func TestSideString(t *testing.T) {
    for s, want := range map[Side]string{SideBuy: "BUY", SideSell: "SELL", SideShort: "SHORT", SideCover: "COVER"} {
        if got := s.String(); got != want {
            t.Errorf("Side(%d).String() = %q, want %q", s, got, want)
        }
    }
}

func TestStatusString(t *testing.T) {
    if StatusPartiallyFilled.String() != "PARTIALLY_FILLED" {
        t.Errorf("got %q", StatusPartiallyFilled.String())
    }
    if StatusBlocked.String() != "BLOCKED" {
        t.Errorf("got %q", StatusBlocked.String())
    }
}

func TestOrderRequestValidate(t *testing.T) {
    good := OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 100}
    if err := good.Validate(); err != nil {
        t.Fatalf("good.Validate() = %v, want nil", err)
    }
    bad := []OrderRequest{
        {Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 100},                 // no venue
        {Venue: "sim-1", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 100},                 // no symbol
        {Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 0, LimitPrice: 100},  // qty 0
        {Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 0},   // limit 0 on a limit order
    }
    for i, r := range bad {
        if err := r.Validate(); err == nil {
            t.Errorf("bad[%d].Validate() = nil, want error", i)
        }
    }
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/exec/ -run 'TestSide|TestStatus|TestOrderRequest' -v`
Expected: FAIL — build error, `exec` package / symbols undefined.

- [ ] **Step 3: Implement `types.go`**

Create `engine/internal/exec/types.go`:
```go
// Package exec is eTape's broker-agnostic execution domain: venue-keyed orders,
// fills, positions, and accounts; the append-only event log and its fold; the
// two-layer safety gate; and the Broker/EventStore interfaces adapters and the
// store satisfy. It imports only clock and session (domain siblings) — never
// store, md, uihub, broker adapters, or feed/opend.
package exec

import (
    "errors"
    "fmt"
)

type VenueID string

type Side uint8

const (
    SideBuy Side = iota
    SideSell
    SideShort
    SideCover
)

func (s Side) String() string {
    switch s {
    case SideBuy:
        return "BUY"
    case SideSell:
        return "SELL"
    case SideShort:
        return "SHORT"
    case SideCover:
        return "COVER"
    default:
        return fmt.Sprintf("Side(%d)", uint8(s))
    }
}

type OrderType uint8

const (
    TypeMarket OrderType = iota
    TypeLimit
)

func (t OrderType) String() string {
    switch t {
    case TypeMarket:
        return "MARKET"
    case TypeLimit:
        return "LIMIT"
    default:
        return fmt.Sprintf("OrderType(%d)", uint8(t))
    }
}

type TIF uint8

const (
    TIFDay TIF = iota
    TIFGTC
    TIFIOC
    TIFFOK
)

func (t TIF) String() string {
    switch t {
    case TIFDay:
        return "DAY"
    case TIFGTC:
        return "GTC"
    case TIFIOC:
        return "IOC"
    case TIFFOK:
        return "FOK"
    default:
        return fmt.Sprintf("TIF(%d)", uint8(t))
    }
}

type OrderStatus uint8

const (
    StatusSubmitted OrderStatus = iota
    StatusAccepted
    StatusPartiallyFilled
    StatusFilled
    StatusCanceled
    StatusRejected
    StatusExpired
    StatusBlocked
    StatusReplaced
)

func (s OrderStatus) String() string {
    switch s {
    case StatusSubmitted:
        return "SUBMITTED"
    case StatusAccepted:
        return "ACCEPTED"
    case StatusPartiallyFilled:
        return "PARTIALLY_FILLED"
    case StatusFilled:
        return "FILLED"
    case StatusCanceled:
        return "CANCELED"
    case StatusRejected:
        return "REJECTED"
    case StatusExpired:
        return "EXPIRED"
    case StatusBlocked:
        return "BLOCKED"
    case StatusReplaced:
        return "REPLACED"
    default:
        return fmt.Sprintf("OrderStatus(%d)", uint8(s))
    }
}

// Order is one order's full lifecycle state. Working = Status in
// {Submitted, Accepted, PartiallyFilled}.
type Order struct {
    Venue        VenueID
    ID           string
    Symbol       string
    Side         Side
    Type         OrderType
    TIF          TIF
    Qty          float64
    LimitPrice   float64
    StopPrice    float64
    Status       OrderStatus
    ExecutedQty  float64
    LeavesQty    float64
    AvgFillPrice float64
    RejectReason string
    ReplacesID   string
    CreatedMs    int64
    UpdatedMs    int64
}

// Working reports whether the order can still fill or be canceled.
func (o Order) Working() bool {
    return o.Status == StatusSubmitted || o.Status == StatusAccepted || o.Status == StatusPartiallyFilled
}

type Fill struct {
    Venue   VenueID
    OrderID string
    Symbol  string
    Side    Side
    Qty     float64
    Price   float64
    TsMs    int64
}

// Position mirrors the broker's authoritative per-symbol position; Qty is signed
// (positive long, negative short).
type Position struct {
    Venue    VenueID
    Symbol   string
    Qty      float64
    AvgPrice float64
}

type AccountSnapshot struct {
    Venue         VenueID
    Equity        float64
    BuyingPower   float64
    AvailableCash float64
    SodEquity     float64
    Realized      float64
    DayPnL        float64
    Leverage      float64
    TsMs          int64
}

// OrderRequest is a fully-specified order to one venue. ClientOrderID is set by
// the Core before the gate runs; adapters echo it back on order events.
type OrderRequest struct {
    Venue         VenueID
    Symbol        string
    Side          Side
    Type          OrderType
    TIF           TIF
    Qty           float64
    LimitPrice    float64
    StopPrice     float64
    ClientOrderID string
}

// Validate enforces the "a request without a valid venue is malformed" rule and
// basic field sanity. The gate performs the risk checks; this is structural.
func (r OrderRequest) Validate() error {
    if r.Venue == "" {
        return errors.New("exec: order request missing venue")
    }
    if r.Symbol == "" {
        return errors.New("exec: order request missing symbol")
    }
    if r.Qty <= 0 {
        return fmt.Errorf("exec: order request qty %v must be > 0", r.Qty)
    }
    if r.Type == TypeLimit && r.LimitPrice <= 0 {
        return fmt.Errorf("exec: limit order missing limit price")
    }
    return nil
}

type ReplaceRequest struct {
    Qty        float64
    LimitPrice float64
    StopPrice  float64
}

type OrderAck struct {
    OrderID  string
    Accepted bool
    Message  string
}
```

- [ ] **Step 4: Run it — verify it passes**

Run: `cd engine && go test ./internal/exec/ -run 'TestSide|TestStatus|TestOrderRequest' -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
cd engine && gofmt -w internal/exec/ && go vet ./internal/exec/
git add internal/exec/types.go internal/exec/types_test.go
git commit -m "feat(engine/exec): broker-agnostic domain value types + OrderRequest.Validate"
```

---

## Task 2: Order IDs — `"ET"`+ULID

The order-ID generator: `"ET"` + a 26-char ULID = a 28-char, time-ordered, collision-free-across-restarts client order ID (fits TZ's 36-char cap; Alpaca 128; moomoo 64). Entropy and time are injected so tests are deterministic; production seeds with `crypto/rand`.

**Files:**
- Modify: `engine/go.mod`, `engine/go.sum` (add `github.com/oklog/ulid/v2`)
- Create: `engine/internal/exec/orderid.go`
- Create: `engine/internal/exec/orderid_test.go`

**Interfaces:**
- Consumes: `clock.Clock` (Plan 2).
- Produces:

```go
type OrderIDGen struct{ /* unexported: clk, mu, entropy */ }
func NewOrderIDGen(clk clock.Clock, seed io.Reader) *OrderIDGen // seed=crypto/rand.Reader in prod
func (g *OrderIDGen) Next() string                              // "ET"+ULID, 28 chars, monotonic
```

- [ ] **Step 1: Add the ULID dependency**

Run:
```bash
cd engine && go get github.com/oklog/ulid/v2@latest && go mod tidy
```
Expected: `go.mod` now `require`s `github.com/oklog/ulid/v2 vX.Y.Z`; `go.sum` updated. Commit the resolved version as-is.

- [ ] **Step 2: Write the failing test**

Create `engine/internal/exec/orderid_test.go`:
```go
package exec

import (
    "math/rand"
    "strings"
    "testing"
    "time"

    "github.com/earlisreal/eTape/engine/internal/clock"
)

func TestOrderIDFormat(t *testing.T) {
    g := NewOrderIDGen(clock.NewFake(time.UnixMilli(1_700_000_000_000)), rand.New(rand.NewSource(1)))
    id := g.Next()
    if !strings.HasPrefix(id, "ET") {
        t.Fatalf("id %q missing ET prefix", id)
    }
    if len(id) != 28 {
        t.Fatalf("id %q length %d, want 28", id, len(id))
    }
}

func TestOrderIDMonotonicUnique(t *testing.T) {
    clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
    g := NewOrderIDGen(clk, rand.New(rand.NewSource(1)))
    seen := map[string]bool{}
    prev := ""
    for i := 0; i < 1000; i++ {
        if i%100 == 0 {
            clk.Advance(time.Millisecond) // exercise both same-ms and advancing-ms paths
        }
        id := g.Next()
        if seen[id] {
            t.Fatalf("duplicate id %q at %d", id, i)
        }
        seen[id] = true
        if prev != "" && id <= prev {
            t.Fatalf("id %q not > prev %q (ULIDs are lexicographically time-ordered)", id, prev)
        }
        prev = id
    }
}
```

- [ ] **Step 3: Run it — verify it fails**

Run: `cd engine && go test ./internal/exec/ -run TestOrderID -v`
Expected: FAIL — `NewOrderIDGen` undefined.

- [ ] **Step 4: Implement `orderid.go`**

Create `engine/internal/exec/orderid.go`:
```go
package exec

import (
    "io"
    "sync"

    "github.com/oklog/ulid/v2"

    "github.com/earlisreal/eTape/engine/internal/clock"
)

// OrderIDGen mints "ET"+ULID client order IDs — 28 chars, lexicographically
// time-ordered, and collision-free across venues and restarts by construction.
// Callers are the single-writer Core; the mutex guards the monotonic entropy
// source so the type is nonetheless safe for incidental concurrent use.
type OrderIDGen struct {
    clk     clock.Clock
    mu      sync.Mutex
    entropy *ulid.MonotonicEntropy
}

// NewOrderIDGen seeds the generator. In production pass crypto/rand.Reader; tests
// pass a deterministic reader (e.g. math/rand) for reproducible IDs.
func NewOrderIDGen(clk clock.Clock, seed io.Reader) *OrderIDGen {
    return &OrderIDGen{clk: clk, entropy: ulid.Monotonic(seed, 0)}
}

// Next returns the next "ET"+ULID order ID.
func (g *OrderIDGen) Next() string {
    g.mu.Lock()
    defer g.mu.Unlock()
    id := ulid.MustNew(ulid.Timestamp(g.clk.Now()), g.entropy)
    return "ET" + id.String()
}
```

- [ ] **Step 5: Run it — verify it passes**

Run: `cd engine && go test ./internal/exec/ -run TestOrderID -v`
Expected: PASS (both).

- [ ] **Step 6: Commit**

```bash
cd engine && gofmt -w internal/exec/ && go vet ./internal/exec/
git add go.mod go.sum internal/exec/orderid.go internal/exec/orderid_test.go
git commit -m "feat(engine/exec): ET+ULID order-id generator (injected clock + entropy)"
```

---

## Task 3: Event union + envelope + JSON codec

The append-only event log's types. Order-lifecycle events (`OrderAccepted`, `OrderRejected`, `OrderFilled`, `OrderCanceled`, `OrderExpired`, `OrderReplaced`, `StreamGap`) implement **both** `isExecEvent()` (persisted) and `isBrokerEvent()` (Task 4 — emitted by adapters); the local-only `OrderSubmitted`/`OrderBlocked` implement `isExecEvent()` only. Each event exposes `Kind()`/`Venue()`/`OrderID()`/`TsMs()` so the coordinator derives the store envelope + fill projection without a giant switch at the call site. `EncodeEvent`/`DecodeEvent` are the kind-discriminated JSON codec (mirrors Plan 3's `store/codec.go` pattern, but owned by `exec` so the fold consumes typed events).

**Files:**
- Create: `engine/internal/exec/events.go`
- Create: `engine/internal/exec/events_test.go`

**Interfaces:**
- Consumes: Task 1 types.
- Produces:

```go
type Source uint8
const ( SrcLocal Source = iota; SrcWS; SrcREST; SrcReconcile ) // String(): "local"/"ws"/"rest"/"reconcile"

type Event interface {
    isExecEvent()
    Kind() string      // stable JSON discriminator, e.g. "order_submitted"
    Venue() VenueID
    OrderID() string
    TsMs() int64
}

type OrderSubmitted struct{ Order Order }
type OrderAccepted  struct{ V VenueID; OID, BrokerOrderID string; Ts int64 }
type OrderRejected  struct{ V VenueID; OID, Reason string; Ts int64 }
type OrderBlocked   struct{ V VenueID; OID string; Req OrderRequest; Reason string; Ts int64 }
type OrderFilled    struct{ F Fill; CumQty, LeavesQty, AvgPrice float64 }
type OrderCanceled  struct{ V VenueID; OID string; Ts int64 }
type OrderExpired   struct{ V VenueID; OID string; Ts int64 }
type OrderReplaced  struct{ V VenueID; OID string; NewQty, NewLimit, NewStop float64; Ts int64 }
type StreamGap      struct{ V VenueID; Ts int64 }

// Persistence DTOs (the EventStore seam — Task 4/5). Kept in exec so store
// imports exec, never the reverse.
type EventEnvelope struct { Seq, TsMs int64; Source, Venue, OrderID, Kind string; Payload []byte }
type FillRow struct { OrderID, Symbol, Side string; Qty, Price float64; TsMs int64; Venue string }

func EncodeEvent(ev Event) (kind string, payload []byte, err error)
func DecodeEvent(kind string, payload []byte) (Event, error)
func EnvelopeOf(ev Event, src Source, seq int64) EventEnvelope
func FillRowOf(ev Event) (*FillRow, bool) // (row, true) only for OrderFilled
```

- [ ] **Step 1: Write the failing test**

Create `engine/internal/exec/events_test.go`:
```go
package exec

import (
    "reflect"
    "testing"
)

func allEvents() []Event {
    return []Event{
        OrderSubmitted{Order: Order{Venue: "sim-1", ID: "ET1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 100, Status: StatusSubmitted, LeavesQty: 10, CreatedMs: 1000, UpdatedMs: 1000}},
        OrderAccepted{V: "sim-1", OID: "ET1", BrokerOrderID: "B1", Ts: 1001},
        OrderRejected{V: "sim-1", OID: "ET1", Reason: "R114", Ts: 1002},
        OrderBlocked{V: "sim-1", OID: "ET1", Req: OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 100, ClientOrderID: "ET1"}, Reason: "master disarmed", Ts: 1003},
        OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 10, Price: 100, TsMs: 1004}, CumQty: 10, LeavesQty: 0, AvgPrice: 100},
        OrderCanceled{V: "sim-1", OID: "ET1", Ts: 1005},
        OrderExpired{V: "sim-1", OID: "ET1", Ts: 1006},
        OrderReplaced{V: "sim-1", OID: "ET1", NewQty: 20, NewLimit: 101, Ts: 1007},
        StreamGap{V: "sim-1", Ts: 1008},
    }
}

func TestEventCodecRoundTrip(t *testing.T) {
    for _, ev := range allEvents() {
        kind, payload, err := EncodeEvent(ev)
        if err != nil {
            t.Fatalf("EncodeEvent(%T) error: %v", ev, err)
        }
        got, err := DecodeEvent(kind, payload)
        if err != nil {
            t.Fatalf("DecodeEvent(%q) error: %v", kind, err)
        }
        if !reflect.DeepEqual(got, ev) {
            t.Errorf("round-trip %q:\n got %#v\nwant %#v", kind, got, ev)
        }
    }
}

func TestDecodeUnknownKind(t *testing.T) {
    if _, err := DecodeEvent("nope", []byte("{}")); err == nil {
        t.Fatal("DecodeEvent(unknown) = nil error, want error")
    }
}

func TestEnvelopeAndFillRow(t *testing.T) {
    ev := OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 10, Price: 100, TsMs: 1004}, CumQty: 10, LeavesQty: 0, AvgPrice: 100}
    env := EnvelopeOf(ev, SrcWS, 42)
    if env.Seq != 42 || env.Source != "ws" || env.Venue != "sim-1" || env.OrderID != "ET1" || env.Kind != "order_filled" || env.TsMs != 1004 {
        t.Fatalf("envelope wrong: %+v", env)
    }
    fr, ok := FillRowOf(ev)
    if !ok || fr.OrderID != "ET1" || fr.Side != "BUY" || fr.Qty != 10 || fr.Price != 100 || fr.Venue != "sim-1" {
        t.Fatalf("fill row wrong: %+v ok=%v", fr, ok)
    }
    if _, ok := FillRowOf(OrderCanceled{V: "sim-1", OID: "ET1", Ts: 1}); ok {
        t.Fatal("FillRowOf(OrderCanceled) ok=true, want false")
    }
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/exec/ -run 'TestEvent|TestDecode|TestEnvelope' -v`
Expected: FAIL — codec/envelope symbols undefined.

- [ ] **Step 3: Implement `events.go`**

Create `engine/internal/exec/events.go`:
```go
package exec

import (
    "encoding/json"
    "fmt"
)

type Source uint8

const (
    SrcLocal Source = iota
    SrcWS
    SrcREST
    SrcReconcile
)

func (s Source) String() string {
    switch s {
    case SrcLocal:
        return "local"
    case SrcWS:
        return "ws"
    case SrcREST:
        return "rest"
    case SrcReconcile:
        return "reconcile"
    default:
        return fmt.Sprintf("Source(%d)", uint8(s))
    }
}

// Event is a persisted execution-log event. The single AUTOINCREMENT seq the
// store assigns gives the total order the fold replays; each event carries the
// fields the fold needs plus enough for the store envelope.
type Event interface {
    isExecEvent()
    Kind() string
    Venue() VenueID
    OrderID() string
    TsMs() int64
}

type OrderSubmitted struct{ Order Order }
type OrderAccepted struct {
    V             VenueID
    OID           string
    BrokerOrderID string
    Ts            int64
}
type OrderRejected struct {
    V      VenueID
    OID    string
    Reason string
    Ts     int64
}
type OrderBlocked struct {
    V      VenueID
    OID    string
    Req    OrderRequest
    Reason string
    Ts     int64
}
type OrderFilled struct {
    F         Fill
    CumQty    float64
    LeavesQty float64
    AvgPrice  float64
}
type OrderCanceled struct {
    V   VenueID
    OID string
    Ts  int64
}
type OrderExpired struct {
    V   VenueID
    OID string
    Ts  int64
}
type OrderReplaced struct {
    V        VenueID
    OID      string
    NewQty   float64
    NewLimit float64
    NewStop  float64
    Ts       int64
}
type StreamGap struct {
    V  VenueID
    Ts int64
}

func (OrderSubmitted) isExecEvent() {}
func (OrderAccepted) isExecEvent()  {}
func (OrderRejected) isExecEvent()  {}
func (OrderBlocked) isExecEvent()   {}
func (OrderFilled) isExecEvent()    {}
func (OrderCanceled) isExecEvent()  {}
func (OrderExpired) isExecEvent()   {}
func (OrderReplaced) isExecEvent()  {}
func (StreamGap) isExecEvent()      {}

func (OrderSubmitted) Kind() string { return "order_submitted" }
func (OrderAccepted) Kind() string  { return "order_accepted" }
func (OrderRejected) Kind() string  { return "order_rejected" }
func (OrderBlocked) Kind() string   { return "order_blocked" }
func (OrderFilled) Kind() string    { return "order_filled" }
func (OrderCanceled) Kind() string  { return "order_canceled" }
func (OrderExpired) Kind() string   { return "order_expired" }
func (OrderReplaced) Kind() string  { return "order_replaced" }
func (StreamGap) Kind() string      { return "stream_gap" }

func (e OrderSubmitted) Venue() VenueID { return e.Order.Venue }
func (e OrderAccepted) Venue() VenueID  { return e.V }
func (e OrderRejected) Venue() VenueID  { return e.V }
func (e OrderBlocked) Venue() VenueID   { return e.V }
func (e OrderFilled) Venue() VenueID    { return e.F.Venue }
func (e OrderCanceled) Venue() VenueID  { return e.V }
func (e OrderExpired) Venue() VenueID   { return e.V }
func (e OrderReplaced) Venue() VenueID  { return e.V }
func (e StreamGap) Venue() VenueID      { return e.V }

func (e OrderSubmitted) OrderID() string { return e.Order.ID }
func (e OrderAccepted) OrderID() string  { return e.OID }
func (e OrderRejected) OrderID() string  { return e.OID }
func (e OrderBlocked) OrderID() string   { return e.OID }
func (e OrderFilled) OrderID() string    { return e.F.OrderID }
func (e OrderCanceled) OrderID() string  { return e.OID }
func (e OrderExpired) OrderID() string   { return e.OID }
func (e OrderReplaced) OrderID() string  { return e.OID }
func (e StreamGap) OrderID() string      { return "" }

func (e OrderSubmitted) TsMs() int64 { return e.Order.UpdatedMs }
func (e OrderAccepted) TsMs() int64  { return e.Ts }
func (e OrderRejected) TsMs() int64  { return e.Ts }
func (e OrderBlocked) TsMs() int64   { return e.Ts }
func (e OrderFilled) TsMs() int64    { return e.F.TsMs }
func (e OrderCanceled) TsMs() int64  { return e.Ts }
func (e OrderExpired) TsMs() int64   { return e.Ts }
func (e OrderReplaced) TsMs() int64  { return e.Ts }
func (e StreamGap) TsMs() int64      { return e.Ts }

// newByKind returns a fresh pointer of the concrete type for a kind, for
// json.Unmarshal to populate.
func newByKind(kind string) (Event, any, error) {
    switch kind {
    case "order_submitted":
        v := &OrderSubmitted{}
        return nil, v, nil
    case "order_accepted":
        v := &OrderAccepted{}
        return nil, v, nil
    case "order_rejected":
        v := &OrderRejected{}
        return nil, v, nil
    case "order_blocked":
        v := &OrderBlocked{}
        return nil, v, nil
    case "order_filled":
        v := &OrderFilled{}
        return nil, v, nil
    case "order_canceled":
        v := &OrderCanceled{}
        return nil, v, nil
    case "order_expired":
        v := &OrderExpired{}
        return nil, v, nil
    case "order_replaced":
        v := &OrderReplaced{}
        return nil, v, nil
    case "stream_gap":
        v := &StreamGap{}
        return nil, v, nil
    default:
        return nil, nil, fmt.Errorf("exec: unknown event kind %q", kind)
    }
}

// EncodeEvent serializes an event to its kind + JSON payload (the concrete
// struct; sealed unions carry no struct tags, matching the feed/md convention).
func EncodeEvent(ev Event) (string, []byte, error) {
    payload, err := json.Marshal(ev)
    if err != nil {
        return "", nil, fmt.Errorf("exec: encode %s: %w", ev.Kind(), err)
    }
    return ev.Kind(), payload, nil
}

// DecodeEvent reconstructs a typed event from its kind + JSON payload.
func DecodeEvent(kind string, payload []byte) (Event, error) {
    _, target, err := newByKind(kind)
    if err != nil {
        return nil, err
    }
    if err := json.Unmarshal(payload, target); err != nil {
        return nil, fmt.Errorf("exec: decode %s: %w", kind, err)
    }
    // target is *T; return the T value so DeepEqual matches the encoded value.
    switch v := target.(type) {
    case *OrderSubmitted:
        return *v, nil
    case *OrderAccepted:
        return *v, nil
    case *OrderRejected:
        return *v, nil
    case *OrderBlocked:
        return *v, nil
    case *OrderFilled:
        return *v, nil
    case *OrderCanceled:
        return *v, nil
    case *OrderExpired:
        return *v, nil
    case *OrderReplaced:
        return *v, nil
    case *StreamGap:
        return *v, nil
    default:
        return nil, fmt.Errorf("exec: decode: unhandled target %T", target)
    }
}

// EnvelopeOf builds the store envelope for an event (payload encoded inline).
func EnvelopeOf(ev Event, src Source, seq int64) EventEnvelope {
    kind, payload, err := EncodeEvent(ev)
    if err != nil {
        // A domain event that cannot encode is a programmer error; store a marker
        // rather than silently dropping (the coordinator treats an encode failure
        // as an append failure and blocks the order).
        payload = []byte(fmt.Sprintf("{\"encodeError\":%q}", err.Error()))
        kind = ev.Kind()
    }
    return EventEnvelope{
        Seq:     seq,
        TsMs:    ev.TsMs(),
        Source:  src.String(),
        Venue:   string(ev.Venue()),
        OrderID: ev.OrderID(),
        Kind:    kind,
        Payload: payload,
    }
}

// FillRowOf extracts the fills-projection row for OrderFilled events.
func FillRowOf(ev Event) (*FillRow, bool) {
    f, ok := ev.(OrderFilled)
    if !ok {
        return nil, false
    }
    return &FillRow{
        OrderID: f.F.OrderID,
        Symbol:  f.F.Symbol,
        Side:    f.F.Side.String(),
        Qty:     f.F.Qty,
        Price:   f.F.Price,
        TsMs:    f.F.TsMs,
        Venue:   string(f.F.Venue),
    }, true
}
```

Add the persistence DTOs to `types.go` (they are exec-owned; keep them next to the domain types). Append to `engine/internal/exec/types.go`:
```go
// EventEnvelope is the persisted form of an Event: the indexed columns plus the
// JSON payload. Defined in exec so the store imports exec (never the reverse).
type EventEnvelope struct {
    Seq     int64
    TsMs    int64
    Source  string
    Venue   string
    OrderID string
    Kind    string
    Payload []byte
}

// FillRow is the fills-projection row written in the same transaction as an
// OrderFilled event.
type FillRow struct {
    OrderID string
    Symbol  string
    Side    string
    Qty     float64
    Price   float64
    TsMs    int64
    Venue   string
}
```

- [ ] **Step 4: Run it — verify it passes**

Run: `cd engine && go test ./internal/exec/ -run 'TestEvent|TestDecode|TestEnvelope' -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
cd engine && gofmt -w internal/exec/ && go vet ./internal/exec/
git add internal/exec/events.go internal/exec/events_test.go internal/exec/types.go
git commit -m "feat(engine/exec): event log union + envelope/fill DTOs + kind-tagged JSON codec"
```

---

## Task 4: `Broker` interface + `Capabilities` + `BrokerEvent` + `Mark`/`MarkSource` + `EventStore`

The three seams the coordinator plugs into: the `Broker` port (adapters + SimBroker implement it), the `BrokerEvent` union it emits, the `Mark`/`MarkSource` last-trade feed the gate values market orders against, and the `EventStore` port the store implements. The order-lifecycle events from Task 3 gain `isBrokerEvent()` here — a broker emits them and they are also persisted, so the coordinator's type switch routes an inbound `BrokerEvent` that also satisfies `Event` straight to the append+fold path.

**Files:**
- Create: `engine/internal/exec/broker.go`

**Interfaces:**
- Consumes: Task 1 + Task 3 types.
- Produces:

```go
type Capabilities struct { NativeReplace, FlattenAll, OvernightSession bool }

type Broker interface {
    Capabilities() Capabilities
    SubmitOrder(ctx context.Context, req OrderRequest) (OrderAck, error)
    ReplaceOrder(ctx context.Context, orderID string, req ReplaceRequest) error
    CancelOrder(ctx context.Context, orderID string) error
    CancelAll(ctx context.Context, symbol string) error   // "" = everything on this venue
    Snapshot(ctx context.Context) (AccountSnapshot, []Position, []Order, error)
    Events() <-chan BrokerEvent                           // domain events + ConnUp/ConnDown + reconcile
}

type BrokerEvent interface{ isBrokerEvent() }
// Order-lifecycle events (Task 3) ALSO implement isBrokerEvent().
type BrokerConnUp    struct{ V VenueID }
type BrokerConnDown  struct{ V VenueID }
type BrokerAccount   struct{ Account AccountSnapshot }        // reconcile: not persisted
type BrokerPositions struct{ V VenueID; Positions []Position } // reconcile: not persisted

type Mark struct { Symbol string; Price float64; TsMs int64 } // shape matches md.Mark
type MarkSource interface { LastTrade(symbol string) (price float64, ok bool) }

type EventStore interface {
    AppendExecEvent(env EventEnvelope, fill *FillRow) (seq int64, err error)
    ReadExecEventsSince(fromMs int64) ([]EventEnvelope, error)
}
```

- [ ] **Step 1: Implement `broker.go`** (interfaces + markers — no separate test; Task 6/9/10 exercise them)

Create `engine/internal/exec/broker.go`:
```go
package exec

import "context"

// Capabilities advertises what a venue's adapter supports natively so the Core
// and gate can adapt (e.g. TZ emulates replace; only Alpaca flattens).
type Capabilities struct {
    NativeReplace    bool // Alpaca PATCH, moomoo ModifyOrder-Normal; TZ false
    FlattenAll       bool // Alpaca DELETE /v2/positions only
    OvernightSession bool // Alpaca (Blue Ocean), moomoo (OVERNIGHT); TZ false
}

// Broker is the per-venue adapter contract. One instance per configured venue;
// implemented by broker/sim here and broker/tradezero, broker/alpaca in Plan 5.
type Broker interface {
    Capabilities() Capabilities
    SubmitOrder(ctx context.Context, req OrderRequest) (OrderAck, error)
    ReplaceOrder(ctx context.Context, orderID string, req ReplaceRequest) error
    CancelOrder(ctx context.Context, orderID string) error
    CancelAll(ctx context.Context, symbol string) error
    Snapshot(ctx context.Context) (AccountSnapshot, []Position, []Order, error)
    Events() <-chan BrokerEvent
}

// BrokerEvent is anything a Broker pushes: order-lifecycle events (which also
// satisfy Event and are persisted), connection transitions, and account/position
// reconcile snapshots (which are not persisted).
type BrokerEvent interface{ isBrokerEvent() }

// Order-lifecycle events are emitted by adapters AND persisted.
func (OrderAccepted) isBrokerEvent() {}
func (OrderRejected) isBrokerEvent() {}
func (OrderFilled) isBrokerEvent()   {}
func (OrderCanceled) isBrokerEvent() {}
func (OrderExpired) isBrokerEvent()  {}
func (OrderReplaced) isBrokerEvent() {}
func (StreamGap) isBrokerEvent()     {}

type BrokerConnUp struct{ V VenueID }
type BrokerConnDown struct{ V VenueID }
type BrokerAccount struct{ Account AccountSnapshot }
type BrokerPositions struct {
    V         VenueID
    Positions []Position
}

func (BrokerConnUp) isBrokerEvent()    {}
func (BrokerConnDown) isBrokerEvent()  {}
func (BrokerAccount) isBrokerEvent()   {}
func (BrokerPositions) isBrokerEvent() {}

// Mark is a last-trade price the gate values market orders against and the Core
// marks positions with. Its shape matches md.Mark; Plan 6 bridges the two.
type Mark struct {
    Symbol string
    Price  float64
    TsMs   int64
}

// MarkSource reads the latest trade price for a symbol.
type MarkSource interface {
    LastTrade(symbol string) (price float64, ok bool)
}

// EventStore is the persistence seam. Implemented by *store.Store (Task 5).
// AppendExecEvent is synchronous and error-returning: append failure blocks the
// order. ReadExecEventsSince returns events with TsMs >= fromMs, ordered by seq
// (the boot-replay input).
type EventStore interface {
    AppendExecEvent(env EventEnvelope, fill *FillRow) (seq int64, err error)
    ReadExecEventsSince(fromMs int64) ([]EventEnvelope, error)
}

// Interface-satisfaction guards (compile-time; concrete impls live in broker/sim
// and store). ctx import kept live for the Broker signatures above.
var _ = context.Background
```

- [ ] **Step 2: Verify it builds**

Run: `cd engine && go build ./internal/exec/ && go vet ./internal/exec/`
Expected: no output (success). The dual `isExecEvent`/`isBrokerEvent` on order-lifecycle types compiles.

- [ ] **Step 3: Commit**

```bash
cd engine && gofmt -w internal/exec/
git add internal/exec/broker.go
git commit -m "feat(engine/exec): Broker/Capabilities/BrokerEvent + Mark/MarkSource + EventStore seams"
```

---

## Task 5: `store` — `exec_events`/`fills` tables + synchronous append + readers

Extends Plan 3's `store` to persist the exec log. Unlike the fire-and-forget journal, exec-event append is **synchronous and error-returning** (spec: append failure blocks order submission), so it goes through the single writer goroutine as a done-channel op that commits its own transaction and returns `last_insert_rowid()` as the seq. `fills` is a same-transaction projection of `OrderFilled`. `*store.Store` thereby satisfies `exec.EventStore`. `store` imports `exec` for the envelope/fill DTOs (the same direction as it already imports `feed`).

**Files:**
- Modify: `engine/internal/store/schema.go` (add DDL)
- Create: `engine/internal/store/exec.go`
- Create: `engine/internal/store/exec_test.go`

**Interfaces:**
- Consumes: Plan 3 `Store` (writer goroutine, `s.writes`, `s.db`), `exec.EventEnvelope`/`exec.FillRow` (Task 3).
- Produces (satisfies `exec.EventStore` + adds the chart-backfill query):

```go
func (s *Store) AppendExecEvent(env exec.EventEnvelope, fill *exec.FillRow) (seq int64, err error) // synchronous
func (s *Store) ReadExecEventsSince(fromMs int64) ([]exec.EventEnvelope, error)                    // ORDER BY seq
func (s *Store) QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error)              // chart backfill
```

- [ ] **Step 1: Add the DDL**

In `engine/internal/store/schema.go`, extend `schemaSQL` (append inside the backtick string, after `sys_events`):
```sql
CREATE TABLE IF NOT EXISTS exec_events (
  seq      INTEGER PRIMARY KEY AUTOINCREMENT,
  ts       INTEGER NOT NULL,          -- event ts (epoch ms)
  source   TEXT    NOT NULL,          -- local|ws|rest|reconcile
  venue    TEXT    NOT NULL,
  type     TEXT    NOT NULL,          -- event kind, e.g. order_submitted
  order_id TEXT    NOT NULL,          -- "" for stream_gap
  payload  TEXT    NOT NULL           -- JSON of the concrete event
);
CREATE INDEX IF NOT EXISTS idx_exec_events_ts ON exec_events(ts);
CREATE TABLE IF NOT EXISTS fills (
  fill_id  INTEGER PRIMARY KEY AUTOINCREMENT,
  seq      INTEGER NOT NULL REFERENCES exec_events(seq),
  order_id TEXT    NOT NULL,
  symbol   TEXT    NOT NULL,
  side     TEXT    NOT NULL,
  qty      REAL    NOT NULL,
  price    REAL    NOT NULL,
  ts       INTEGER NOT NULL,
  venue    TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_fills_symbol_ts ON fills(symbol, ts);
```
Update the leading comment on `schemaSQL` to drop the "Plan 4 adds exec_events/fills" note (it is now present).

- [ ] **Step 2: Write the failing test**

Create `engine/internal/store/exec_test.go`:
```go
package store

import (
    "path/filepath"
    "testing"

    "github.com/earlisreal/eTape/engine/internal/exec"
)

func openTestStore(t *testing.T) *Store {
    t.Helper()
    s, err := Open(Options{Path: filepath.Join(t.TempDir(), "t.db")})
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { _ = s.Close() })
    return s
}

func TestAppendExecEventSeqAndReadBack(t *testing.T) {
    s := openTestStore(t)
    mk := func(ts int64, oid string) exec.EventEnvelope {
        return exec.EventEnvelope{TsMs: ts, Source: "local", Venue: "sim-1", OrderID: oid, Kind: "order_submitted", Payload: []byte(`{"Order":{"ID":"` + oid + `"}}`)}
    }
    s1, err := s.AppendExecEvent(mk(1000, "ETa"), nil)
    if err != nil {
        t.Fatal(err)
    }
    s2, err := s.AppendExecEvent(mk(1001, "ETb"), nil)
    if err != nil {
        t.Fatal(err)
    }
    if s2 <= s1 {
        t.Fatalf("seq not increasing: %d then %d", s1, s2)
    }
    got, err := s.ReadExecEventsSince(0)
    if err != nil {
        t.Fatal(err)
    }
    if len(got) != 2 || got[0].Seq != s1 || got[1].Seq != s2 || got[0].OrderID != "ETa" || got[1].OrderID != "ETb" {
        t.Fatalf("read-back wrong: %+v", got)
    }
    // fromMs filter excludes older events.
    since, _ := s.ReadExecEventsSince(1001)
    if len(since) != 1 || since[0].OrderID != "ETb" {
        t.Fatalf("since filter wrong: %+v", since)
    }
}

func TestAppendExecEventFillProjection(t *testing.T) {
    s := openTestStore(t)
    env := exec.EventEnvelope{TsMs: 2000, Source: "ws", Venue: "sim-1", OrderID: "ETc", Kind: "order_filled", Payload: []byte(`{}`)}
    fill := &exec.FillRow{OrderID: "ETc", Symbol: "AAPL", Side: "BUY", Qty: 10, Price: 100, TsMs: 2000, Venue: "sim-1"}
    seq, err := s.AppendExecEvent(env, fill)
    if err != nil {
        t.Fatal(err)
    }
    rows, err := s.QueryFills("AAPL", 0, 9999)
    if err != nil {
        t.Fatal(err)
    }
    if len(rows) != 1 || rows[0].OrderID != "ETc" || rows[0].Qty != 10 || rows[0].Price != 100 {
        t.Fatalf("fill query wrong: %+v", rows)
    }
    // The fill row references the event seq.
    var refSeq int64
    if err := s.db.QueryRow("SELECT seq FROM fills WHERE order_id='ETc'").Scan(&refSeq); err != nil {
        t.Fatal(err)
    }
    if refSeq != seq {
        t.Fatalf("fill seq FK = %d, want %d", refSeq, seq)
    }
}
```

- [ ] **Step 3: Run it — verify it fails**

Run: `cd engine && go test ./internal/store/ -run TestAppendExec -v`
Expected: FAIL — `AppendExecEvent`/`ReadExecEventsSince`/`QueryFills` undefined.

- [ ] **Step 4: Implement `exec.go`**

Create `engine/internal/store/exec.go`:
```go
package store

import (
    "database/sql"

    "github.com/earlisreal/eTape/engine/internal/exec"
)

const (
    execEventInsertSQL = `INSERT INTO exec_events (ts, source, venue, type, order_id, payload)
        VALUES (?, ?, ?, ?, ?, ?)`
    fillInsertSQL = `INSERT INTO fills (seq, order_id, symbol, side, qty, price, ts, venue)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
)

// execAppendOp is a synchronous write: it commits its own transaction in the
// writer goroutine and reports the assigned seq (or an error) back on done.
type execAppendOp struct {
    env  exec.EventEnvelope
    fill *exec.FillRow
    done chan execAppendResult
}

type execAppendResult struct {
    seq int64
    err error
}

func (execAppendOp) render() []pendingWrite { return nil } // handled specially by the writer

// AppendExecEvent persists one exec event (and, for OrderFilled, its fills-
// projection row) in a single transaction, synchronously. It returns the
// AUTOINCREMENT seq. On error the caller (Core) blocks the order — the event log
// is the source of truth and must be durable before the broker POST.
func (s *Store) AppendExecEvent(env exec.EventEnvelope, fill *exec.FillRow) (int64, error) {
    op := execAppendOp{env: env, fill: fill, done: make(chan execAppendResult, 1)}
    s.writes <- op
    r := <-op.done
    return r.seq, r.err
}

// commitExecAppend runs in the writer goroutine only (invoked from the writer
// select). It owns the sole DB write handle, so this cannot race the batched
// journal/archive commits.
func (s *Store) commitExecAppend(op execAppendOp) execAppendResult {
    tx, err := s.db.Begin()
    if err != nil {
        return execAppendResult{err: err}
    }
    res, err := tx.Exec(execEventInsertSQL, op.env.TsMs, op.env.Source, op.env.Venue,
        op.env.Kind, op.env.OrderID, string(op.env.Payload))
    if err != nil {
        _ = tx.Rollback()
        return execAppendResult{err: err}
    }
    seq, err := res.LastInsertId()
    if err != nil {
        _ = tx.Rollback()
        return execAppendResult{err: err}
    }
    if op.fill != nil {
        if _, err := tx.Exec(fillInsertSQL, seq, op.fill.OrderID, op.fill.Symbol, op.fill.Side,
            op.fill.Qty, op.fill.Price, op.fill.TsMs, op.fill.Venue); err != nil {
            _ = tx.Rollback()
            return execAppendResult{err: err}
        }
    }
    if err := tx.Commit(); err != nil {
        _ = tx.Rollback()
        return execAppendResult{err: err}
    }
    return execAppendResult{seq: seq}
}

// ReadExecEventsSince returns events with ts >= fromMs, ordered by seq (the boot-
// replay order). Payload bytes are copied out of the row scan.
func (s *Store) ReadExecEventsSince(fromMs int64) ([]exec.EventEnvelope, error) {
    rows, err := s.db.Query(
        `SELECT seq, ts, source, venue, type, order_id, payload
         FROM exec_events WHERE ts >= ? ORDER BY seq`, fromMs)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []exec.EventEnvelope
    for rows.Next() {
        var e exec.EventEnvelope
        var payload string
        if err := rows.Scan(&e.Seq, &e.TsMs, &e.Source, &e.Venue, &e.Kind, &e.OrderID, &payload); err != nil {
            return nil, err
        }
        e.Payload = []byte(payload)
        out = append(out, e)
    }
    return out, rows.Err()
}

// QueryFills returns fills for a symbol in [fromMs, toMs), ascending — the chart-
// annotation backfill query (Plan 6 exposes it on exec.fills).
func (s *Store) QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error) {
    rows, err := s.db.Query(
        `SELECT order_id, symbol, side, qty, price, ts, venue
         FROM fills WHERE symbol = ? AND ts >= ? AND ts < ? ORDER BY ts`, symbol, fromMs, toMs)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []exec.FillRow
    for rows.Next() {
        var f exec.FillRow
        if err := rows.Scan(&f.OrderID, &f.Symbol, &f.Side, &f.Qty, &f.Price, &f.TsMs, &f.Venue); err != nil {
            return nil, err
        }
        out = append(out, f)
    }
    return out, rows.Err()
}

var _ sql.Result // keep database/sql import if trimmed by goimports in a future edit
```

- [ ] **Step 5: Wire the synchronous op into the writer goroutine**

In `engine/internal/store/store.go`, in `writer`'s inbox `case op, ok := <-s.writes:` block, handle `execAppendOp` **before** the generic `op.render()` path (mirror the `flushReq` special case). Change the block to:
```go
case op, ok := <-s.writes:
    if !ok { // channel closed by Close: final flush, then exit
        commit()
        return
    }
    switch v := op.(type) {
    case flushReq:
        commit()
        close(v.done)
        continue
    case execAppendOp:
        commit()                       // flush any pending batch first, then the sync exec tx
        v.done <- s.commitExecAppend(v)
        continue
    }
    buf = append(buf, op.render()...)
    if len(buf) >= s.batch {
        commit()
    }
```
(Remove the old `if fr, isFlush := op.(flushReq); isFlush { ... }` block — it is replaced by the switch.)

- [ ] **Step 6: Run it — verify it passes**

Run: `cd engine && go test -race ./internal/store/ -run 'TestAppendExec' -v`
Expected: PASS (both). Also run the full store suite to confirm no regression: `cd engine && go test -race ./internal/store/`
Expected: ok.

- [ ] **Step 7: Commit**

```bash
cd engine && gofmt -w internal/store/ && go vet ./internal/store/
git add internal/store/schema.go internal/store/exec.go internal/store/exec_test.go internal/store/store.go
git commit -m "feat(engine/store): exec_events/fills tables + synchronous AppendExecEvent, readers"
```

---

## Task 6: Fold — `State`/`VenueState` + `Apply` (log events) + `replay(log) == state`

The pure, single-writer fold over the persisted event log. `Apply(s *State, ev Event)` mutates state in the one writer goroutine (no locks); the invariant is determinism — `replay(log) == state`. This task folds the **order** side of the log (orders, fills, the global order index used for the duplicate-ID check and event routing). Account/positions/arming are Task 7. The property test folds a log twice and asserts byte-identical states, and folds a store round-trip and asserts equality.

**Files:**
- Create: `engine/internal/exec/state.go`
- Create: `engine/internal/exec/state_test.go`

**Interfaces:**
- Consumes: Task 1/3 types.
- Produces:

```go
type VenueState struct {
    Armed    bool                  // Task 7 sets this; boot=false
    Orders   map[string]Order      // by order ID; working + terminal-today
    Fills    []Fill                // today, in fold order
    Positions map[string]Position  // by symbol; broker-reconciled (Task 7)
    Account  AccountSnapshot       // broker-reconciled (Task 7)
}

type State struct {
    MasterArmed bool                       // Task 7; boot=false
    Venues      map[VenueID]*VenueState
    orderIndex  map[string]VenueID         // orderID -> venue; global duplicate check + routing
}

func NewState(venues []VenueID) *State
func (s *State) Apply(ev Event)            // fold one persisted event (order log)
func (s *State) Venue(v VenueID) *VenueState
func (s *State) OrderVenue(orderID string) (VenueID, bool)
func Replay(events []Event, venues []VenueID) *State
```

- [ ] **Step 1: Write the failing test**

Create `engine/internal/exec/state_test.go`:
```go
package exec

import (
    "reflect"
    "testing"
)

// submitEv builds an OrderSubmitted for a fresh working order.
func submitEv(v VenueID, id, sym string, side Side, qty, limit float64, ts int64) OrderSubmitted {
    return OrderSubmitted{Order: Order{Venue: v, ID: id, Symbol: sym, Side: side, Type: TypeLimit,
        TIF: TIFDay, Qty: qty, LimitPrice: limit, Status: StatusSubmitted, LeavesQty: qty,
        CreatedMs: ts, UpdatedMs: ts}}
}

func TestApplyOrderLifecycle(t *testing.T) {
    venues := []VenueID{"sim-1"}
    s := NewState(venues)
    s.Apply(submitEv("sim-1", "ET1", "AAPL", SideBuy, 10, 100, 1000))
    if v, ok := s.OrderVenue("ET1"); !ok || v != "sim-1" {
        t.Fatalf("order index missing ET1: %v %v", v, ok)
    }
    o := s.Venue("sim-1").Orders["ET1"]
    if o.Status != StatusSubmitted || !o.Working() {
        t.Fatalf("after submit: %+v", o)
    }
    s.Apply(OrderAccepted{V: "sim-1", OID: "ET1", BrokerOrderID: "B1", Ts: 1001})
    if s.Venue("sim-1").Orders["ET1"].Status != StatusAccepted {
        t.Fatalf("after accept: %+v", s.Venue("sim-1").Orders["ET1"])
    }
    s.Apply(OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 10, Price: 100, TsMs: 1002}, CumQty: 10, LeavesQty: 0, AvgPrice: 100})
    o = s.Venue("sim-1").Orders["ET1"]
    if o.Status != StatusFilled || o.ExecutedQty != 10 || o.LeavesQty != 0 || o.AvgFillPrice != 100 || o.Working() {
        t.Fatalf("after fill: %+v", o)
    }
    if len(s.Venue("sim-1").Fills) != 1 {
        t.Fatalf("fills = %d, want 1", len(s.Venue("sim-1").Fills))
    }
}

func TestApplyPartialThenFill(t *testing.T) {
    s := NewState([]VenueID{"sim-1"})
    s.Apply(submitEv("sim-1", "ET1", "AAPL", SideBuy, 10, 100, 1000))
    s.Apply(OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 4, Price: 100, TsMs: 1001}, CumQty: 4, LeavesQty: 6, AvgPrice: 100})
    if o := s.Venue("sim-1").Orders["ET1"]; o.Status != StatusPartiallyFilled || o.LeavesQty != 6 {
        t.Fatalf("after partial: %+v", o)
    }
    s.Apply(OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 6, Price: 101, TsMs: 1002}, CumQty: 10, LeavesQty: 0, AvgPrice: 100.6})
    if o := s.Venue("sim-1").Orders["ET1"]; o.Status != StatusFilled || o.ExecutedQty != 10 || o.AvgFillPrice != 100.6 {
        t.Fatalf("after full: %+v", o)
    }
}

func TestApplyBlockedAndCancel(t *testing.T) {
    s := NewState([]VenueID{"sim-1"})
    s.Apply(OrderBlocked{V: "sim-1", OID: "ETb", Req: OrderRequest{Venue: "sim-1", Symbol: "AAPL", ClientOrderID: "ETb"}, Reason: "master disarmed", Ts: 1000})
    // Blocked orders are recorded (duplicate-ID defense) but terminal + not working.
    if _, ok := s.OrderVenue("ETb"); !ok {
        t.Fatal("blocked order not indexed")
    }
    if o := s.Venue("sim-1").Orders["ETb"]; o.Status != StatusBlocked || o.Working() {
        t.Fatalf("blocked order: %+v", o)
    }
    s.Apply(submitEv("sim-1", "ET1", "AAPL", SideBuy, 10, 100, 1001))
    s.Apply(OrderCanceled{V: "sim-1", OID: "ET1", Ts: 1002})
    if o := s.Venue("sim-1").Orders["ET1"]; o.Status != StatusCanceled || o.Working() {
        t.Fatalf("after cancel: %+v", o)
    }
}

// The core invariant: folding a log twice yields byte-identical state, and the
// fold is chunking-invariant (same events, any grouping, same state).
func TestReplayEqualsState(t *testing.T) {
    venues := []VenueID{"sim-1", "sim-2"}
    log := []Event{
        submitEv("sim-1", "ET1", "AAPL", SideBuy, 10, 100, 1000),
        submitEv("sim-2", "ET2", "MSFT", SideShort, 5, 300, 1001),
        OrderAccepted{V: "sim-1", OID: "ET1", BrokerOrderID: "B1", Ts: 1002},
        OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 10, Price: 100, TsMs: 1003}, CumQty: 10, LeavesQty: 0, AvgPrice: 100},
        OrderCanceled{V: "sim-2", OID: "ET2", Ts: 1004},
        OrderReplaced{V: "sim-1", OID: "ET1", NewQty: 10, NewLimit: 100, Ts: 1005},
    }
    a := Replay(log, venues)
    b := Replay(log, venues)
    if !reflect.DeepEqual(a, b) {
        t.Fatal("two replays of the same log differ")
    }
    // Chunking invariance: fold event-by-event vs all-at-once → same state.
    c := NewState(venues)
    for _, ev := range log {
        c.Apply(ev)
    }
    if !reflect.DeepEqual(a, c) {
        t.Fatal("Replay != incremental Apply")
    }
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/exec/ -run 'TestApply|TestReplay' -v`
Expected: FAIL — `NewState`/`Apply`/`Replay` undefined.

- [ ] **Step 3: Implement `state.go`**

Create `engine/internal/exec/state.go`:
```go
package exec

// VenueState is one venue's execution state. Orders + Fills are event-sourced
// (this file); Positions + Account are broker-reconciled (reconcile.go). Armed is
// set by arming commands (reconcile.go); boot is always false.
type VenueState struct {
    Armed     bool
    Orders    map[string]Order
    Fills     []Fill
    Positions map[string]Position
    Account   AccountSnapshot
}

func newVenueState() *VenueState {
    return &VenueState{Orders: map[string]Order{}, Positions: map[string]Position{}}
}

// State is the whole multi-venue execution state, owned by the single Core
// writer goroutine. orderIndex maps every order ID ever seen (working, terminal,
// or blocked) to its venue — the global duplicate-ID check and broker-event
// routing both read it.
type State struct {
    MasterArmed bool
    Venues      map[VenueID]*VenueState
    orderIndex  map[string]VenueID
}

// NewState builds empty state for a fixed venue set (venues come from config;
// unknown-venue events are ignored defensively).
func NewState(venues []VenueID) *State {
    s := &State{Venues: map[VenueID]*VenueState{}, orderIndex: map[string]VenueID{}}
    for _, v := range venues {
        s.Venues[v] = newVenueState()
    }
    return s
}

// Venue returns the venue state, creating it if the venue was not pre-registered
// (keeps the fold total for logs that predate a config change).
func (s *State) Venue(v VenueID) *VenueState {
    vs, ok := s.Venues[v]
    if !ok {
        vs = newVenueState()
        s.Venues[v] = vs
    }
    return vs
}

// OrderVenue reports which venue owns an order ID.
func (s *State) OrderVenue(orderID string) (VenueID, bool) {
    v, ok := s.orderIndex[orderID]
    return v, ok
}

// Apply folds one persisted event into state. Deterministic and I/O-free — the
// basis of replay(log) == state. Account/position events are handled by
// ApplyReconcile (reconcile.go), not here.
func (s *State) Apply(ev Event) {
    switch e := ev.(type) {
    case OrderSubmitted:
        o := e.Order
        if o.LeavesQty == 0 && o.ExecutedQty == 0 {
            o.LeavesQty = o.Qty
        }
        s.Venue(o.Venue).Orders[o.ID] = o
        s.orderIndex[o.ID] = o.Venue
    case OrderBlocked:
        // Recorded for the duplicate-ID defense; terminal, never working.
        vs := s.Venue(e.V)
        vs.Orders[e.OID] = Order{Venue: e.V, ID: e.OID, Symbol: e.Req.Symbol, Side: e.Req.Side,
            Type: e.Req.Type, TIF: e.Req.TIF, Qty: e.Req.Qty, LimitPrice: e.Req.LimitPrice,
            StopPrice: e.Req.StopPrice, Status: StatusBlocked, RejectReason: e.Reason,
            CreatedMs: e.Ts, UpdatedMs: e.Ts}
        s.orderIndex[e.OID] = e.V
    case OrderAccepted:
        s.mutate(e.V, e.OID, e.Ts, func(o *Order) { o.Status = StatusAccepted })
    case OrderRejected:
        s.mutate(e.V, e.OID, e.Ts, func(o *Order) { o.Status = StatusRejected; o.RejectReason = e.Reason })
    case OrderFilled:
        s.applyFill(e)
    case OrderCanceled:
        s.mutate(e.V, e.OID, e.Ts, func(o *Order) {
            if o.Status != StatusFilled {
                o.Status = StatusCanceled
            }
        })
    case OrderExpired:
        s.mutate(e.V, e.OID, e.Ts, func(o *Order) {
            if o.Status != StatusFilled {
                o.Status = StatusExpired
            }
        })
    case OrderReplaced:
        s.mutate(e.V, e.OID, e.Ts, func(o *Order) {
            o.Qty = e.NewQty
            if e.NewLimit > 0 {
                o.LimitPrice = e.NewLimit
            }
            if e.NewStop > 0 {
                o.StopPrice = e.NewStop
            }
            o.LeavesQty = e.NewQty - o.ExecutedQty
            o.Status = StatusAccepted
        })
    case StreamGap:
        // A gap marker: reconcile (Core) resolves state against a fresh snapshot.
        // The fold records nothing here; the marker exists for audit + replay.
    }
}

// mutate applies fn to an existing order and stamps UpdatedMs; no-op if unknown.
func (s *State) mutate(v VenueID, id string, ts int64, fn func(*Order)) {
    vs := s.Venue(v)
    o, ok := vs.Orders[id]
    if !ok {
        return
    }
    fn(&o)
    o.UpdatedMs = ts
    vs.Orders[id] = o
}

func (s *State) applyFill(e OrderFilled) {
    vs := s.Venue(e.F.Venue)
    vs.Fills = append(vs.Fills, e.F)
    o, ok := vs.Orders[e.F.OrderID]
    if !ok {
        // Fill for an order we never saw submitted (reconcile gap): index it.
        o = Order{Venue: e.F.Venue, ID: e.F.OrderID, Symbol: e.F.Symbol, Side: e.F.Side, CreatedMs: e.F.TsMs}
        s.orderIndex[e.F.OrderID] = e.F.Venue
    }
    o.ExecutedQty = e.CumQty
    o.LeavesQty = e.LeavesQty
    o.AvgFillPrice = e.AvgPrice
    if e.LeavesQty <= 0 {
        o.Status = StatusFilled
    } else {
        o.Status = StatusPartiallyFilled
    }
    o.UpdatedMs = e.F.TsMs
    vs.Orders[o.ID] = o
}

// Replay folds a full log from empty state — the boot path and the property-test
// entry point.
func Replay(events []Event, venues []VenueID) *State {
    s := NewState(venues)
    for _, ev := range events {
        s.Apply(ev)
    }
    return s
}
```

- [ ] **Step 4: Run it — verify it passes**

Run: `cd engine && go test -race ./internal/exec/ -run 'TestApply|TestReplay' -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
cd engine && gofmt -w internal/exec/ && go vet ./internal/exec/
git add internal/exec/state.go internal/exec/state_test.go
git commit -m "feat(engine/exec): single-writer order-log fold + replay(log)==state property"
```

---

## Task 7: Reconcile (account/positions) + arming + cross-venue aggregates

The non-event-sourced side of state: `ApplyReconcile` overwrites broker-authoritative account + positions (boot snapshot + live pushes); the arming setters flip the master/venue switches (boot=off, never persisted); and the aggregate methods compute the cross-venue quantities the gate and the `exec.positions` topic need (per-symbol net position across venues, working same-direction exposure, summed day P&L). All pure, all single-writer.

**Files:**
- Create: `engine/internal/exec/reconcile.go`
- Create: `engine/internal/exec/reconcile_test.go`

**Interfaces:**
- Consumes: Task 6 `State`/`VenueState`.
- Produces:

```go
func (s *State) ReconcileAccount(a AccountSnapshot)
func (s *State) ReconcilePositions(v VenueID, ps []Position)
func (s *State) ReconcileOpenOrders(v VenueID, orders []Order) // boot: adopt broker's working orders
func (s *State) SetMasterArmed(on bool)
func (s *State) SetVenueArmed(v VenueID, on bool)
func (s *State) IsArmed(v VenueID) bool                        // master AND venue

// aggregates (signed; long +, short -)
func (s *State) VenuePositionShares(v VenueID, symbol string) float64
func (s *State) SymbolNetShares(symbol string) float64        // across all venues
func (s *State) VenueWorkingSameDir(v VenueID, symbol string, side Side) float64
func (s *State) SymbolWorkingSameDir(symbol string, side Side) float64 // across all venues
func (s *State) TotalDayPnL() float64
```

- [ ] **Step 1: Write the failing test**

Create `engine/internal/exec/reconcile_test.go`:
```go
package exec

import "testing"

func TestReconcileOverwrites(t *testing.T) {
    s := NewState([]VenueID{"sim-1"})
    s.ReconcilePositions("sim-1", []Position{{Venue: "sim-1", Symbol: "AAPL", Qty: 100, AvgPrice: 50}})
    if s.VenuePositionShares("sim-1", "AAPL") != 100 {
        t.Fatalf("got %v", s.VenuePositionShares("sim-1", "AAPL"))
    }
    // A later push is authoritative and REPLACES (not accumulates).
    s.ReconcilePositions("sim-1", []Position{{Venue: "sim-1", Symbol: "AAPL", Qty: 40, AvgPrice: 51}})
    if s.VenuePositionShares("sim-1", "AAPL") != 40 {
        t.Fatalf("overwrite failed: %v", s.VenuePositionShares("sim-1", "AAPL"))
    }
    s.ReconcileAccount(AccountSnapshot{Venue: "sim-1", Equity: 10000, DayPnL: -250})
    if s.Venue("sim-1").Account.DayPnL != -250 {
        t.Fatalf("account not reconciled: %+v", s.Venue("sim-1").Account)
    }
}

func TestArming(t *testing.T) {
    s := NewState([]VenueID{"sim-1", "sim-2"})
    if s.IsArmed("sim-1") {
        t.Fatal("boot should be disarmed")
    }
    s.SetVenueArmed("sim-1", true)
    if s.IsArmed("sim-1") {
        t.Fatal("venue armed but master off → not armed")
    }
    s.SetMasterArmed(true)
    if !s.IsArmed("sim-1") {
        t.Fatal("master+venue on → armed")
    }
    if s.IsArmed("sim-2") {
        t.Fatal("sim-2 venue still off")
    }
}

func TestCrossVenueAggregates(t *testing.T) {
    s := NewState([]VenueID{"sim-1", "sim-2"})
    s.ReconcilePositions("sim-1", []Position{{Venue: "sim-1", Symbol: "AAPL", Qty: 100}})
    s.ReconcilePositions("sim-2", []Position{{Venue: "sim-2", Symbol: "AAPL", Qty: -30}})
    if got := s.SymbolNetShares("AAPL"); got != 70 {
        t.Fatalf("net = %v, want 70", got)
    }
    // working same-direction buy exposure across venues
    s.Apply(submitEv("sim-1", "ET1", "AAPL", SideBuy, 10, 100, 1000))
    s.Apply(submitEv("sim-2", "ET2", "AAPL", SideBuy, 5, 100, 1001))
    s.Apply(submitEv("sim-1", "ET3", "AAPL", SideSell, 4, 200, 1002)) // opposite dir, excluded
    if got := s.SymbolWorkingSameDir("AAPL", SideBuy); got != 15 {
        t.Fatalf("working buy = %v, want 15", got)
    }
    if got := s.VenueWorkingSameDir("sim-1", "AAPL", SideBuy); got != 10 {
        t.Fatalf("venue working buy = %v, want 10", got)
    }
    s.ReconcileAccount(AccountSnapshot{Venue: "sim-1", DayPnL: -100})
    s.ReconcileAccount(AccountSnapshot{Venue: "sim-2", DayPnL: -50})
    if got := s.TotalDayPnL(); got != -150 {
        t.Fatalf("total day pnl = %v, want -150", got)
    }
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/exec/ -run 'TestReconcile|TestArming|TestCrossVenue' -v`
Expected: FAIL — reconcile/arming/aggregate methods undefined.

- [ ] **Step 3: Implement `reconcile.go`**

Create `engine/internal/exec/reconcile.go`:
```go
package exec

// ReconcileAccount overwrites a venue's account snapshot (broker is authoritative;
// eTape mirrors). Not persisted.
func (s *State) ReconcileAccount(a AccountSnapshot) {
    s.Venue(a.Venue).Account = a
}

// ReconcilePositions replaces a venue's positions with the broker's authoritative
// set (a full snapshot per push; absent symbols mean flat). Not persisted.
func (s *State) ReconcilePositions(v VenueID, ps []Position) {
    vs := s.Venue(v)
    vs.Positions = make(map[string]Position, len(ps))
    for _, p := range ps {
        vs.Positions[p.Symbol] = p
    }
}

// ReconcileOpenOrders adopts the broker's working-order set on boot/reconnect.
// Orders the broker reports that the log did not are inserted; log orders the
// broker no longer reports as working are left as-is (their terminal transition,
// if any, arrives as a synthesized reconcile event from the adapter — Plan 5).
func (s *State) ReconcileOpenOrders(v VenueID, orders []Order) {
    vs := s.Venue(v)
    for _, o := range orders {
        o.Venue = v
        vs.Orders[o.ID] = o
        s.orderIndex[o.ID] = v
    }
}

// SetMasterArmed / SetVenueArmed flip the two-layer switches. Not persisted —
// boot is always disarmed.
func (s *State) SetMasterArmed(on bool) { s.MasterArmed = on }

func (s *State) SetVenueArmed(v VenueID, on bool) { s.Venue(v).Armed = on }

// IsArmed reports whether trading on a venue is permitted: master AND venue.
func (s *State) IsArmed(v VenueID) bool {
    vs, ok := s.Venues[v]
    return s.MasterArmed && ok && vs.Armed
}

// VenuePositionShares is the signed share position for a symbol on one venue.
func (s *State) VenuePositionShares(v VenueID, symbol string) float64 {
    vs, ok := s.Venues[v]
    if !ok {
        return 0
    }
    return vs.Positions[symbol].Qty
}

// SymbolNetShares is the signed net position for a symbol summed across venues.
func (s *State) SymbolNetShares(symbol string) float64 {
    var net float64
    for _, vs := range s.Venues {
        net += vs.Positions[symbol].Qty
    }
    return net
}

// sameDir reports whether a side increases a long (Buy/Cover) or a short
// (Sell/Short) position in the same direction as `ref`.
func sameDir(a, b Side) bool { return longward(a) == longward(b) }

func longward(sd Side) bool { return sd == SideBuy || sd == SideCover }

// VenueWorkingSameDir sums leaves-qty of working orders on a venue whose side
// pushes the position the same way as `side`.
func (s *State) VenueWorkingSameDir(v VenueID, symbol string, side Side) float64 {
    vs, ok := s.Venues[v]
    if !ok {
        return 0
    }
    var q float64
    for _, o := range vs.Orders {
        if o.Symbol == symbol && o.Working() && sameDir(o.Side, side) {
            q += o.LeavesQty
        }
    }
    return q
}

// SymbolWorkingSameDir is VenueWorkingSameDir summed across venues.
func (s *State) SymbolWorkingSameDir(symbol string, side Side) float64 {
    var q float64
    for v := range s.Venues {
        q += s.VenueWorkingSameDir(v, symbol, side)
    }
    return q
}

// TotalDayPnL sums each venue's authoritative day P&L (adapter-sourced).
func (s *State) TotalDayPnL() float64 {
    var t float64
    for _, vs := range s.Venues {
        t += vs.Account.DayPnL
    }
    return t
}
```

- [ ] **Step 4: Run it — verify it passes**

Run: `cd engine && go test -race ./internal/exec/ -run 'TestReconcile|TestArming|TestCrossVenue' -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
cd engine && gofmt -w internal/exec/ && go vet ./internal/exec/
git add internal/exec/reconcile.go internal/exec/reconcile_test.go
git commit -m "feat(engine/exec): reconcile (account/positions) + arming + cross-venue aggregates"
```

---

## Task 8: Gate — `GateConfig` + `Evaluate` (ordered rules) + `BreachedDayLoss`

The safety envelope: a pure function checked in the Core loop against one consistent state. `Evaluate` returns `(ok, reason)` — first failing rule wins. Rule order per the multi-broker spec: master armed → venue armed → duplicate ID → per-venue (max order value → max resulting venue position $ & shares → max open orders) → global (max resulting per-symbol position across venues $ & shares). Day-loss is enforced separately via `BreachedDayLoss` (the Core auto-disarms the master on breach — see the flagged deviation), so an order after a breach fails the master-armed rule.

**Files:**
- Create: `engine/internal/exec/gate.go`
- Create: `engine/internal/exec/gate_test.go`

**Interfaces:**
- Consumes: Task 6/7 `State`, Task 1 `OrderRequest`, Task 4 `MarkSource`.
- Produces:

```go
type GlobalLimits struct { MaxDayLoss, MaxSymbolPositionValue, MaxSymbolPositionShares float64 }
type VenueLimits struct { MaxOrderValue, MaxPositionValue, MaxPositionShares float64; MaxOpenOrders int }
type GateConfig struct { Global GlobalLimits; Venue map[VenueID]VenueLimits }

func Evaluate(s *State, cfg GateConfig, req OrderRequest, marks MarkSource) (ok bool, reason string)
func BreachedDayLoss(s *State, cfg GateConfig) bool // true when summed day P&L <= -MaxDayLoss
// helper: qty*price valuation (limit → LimitPrice; market → mark)
func orderValue(req OrderRequest, marks MarkSource) (value float64, ok bool)
```

A limit of `0` means "unset — do not enforce this cap" (config zero-value), so each numeric check is `limit > 0 && candidate > limit`.

- [ ] **Step 1: Write the failing test**

Create `engine/internal/exec/gate_test.go`:
```go
package exec

import "testing"

type fakeMarks map[string]float64

func (m fakeMarks) LastTrade(sym string) (float64, bool) { v, ok := m[sym]; return v, ok }

func baseCfg() GateConfig {
    return GateConfig{
        Global: GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionValue: 100000, MaxSymbolPositionShares: 1000},
        Venue: map[VenueID]VenueLimits{
            "sim-1": {MaxOrderValue: 5000, MaxPositionValue: 20000, MaxPositionShares: 200, MaxOpenOrders: 3},
            "sim-2": {MaxOrderValue: 5000, MaxPositionValue: 20000, MaxPositionShares: 200, MaxOpenOrders: 3},
        },
    }
}

func armedState() *State {
    s := NewState([]VenueID{"sim-1", "sim-2"})
    s.SetMasterArmed(true)
    s.SetVenueArmed("sim-1", true)
    s.SetVenueArmed("sim-2", true)
    return s
}

func buyReq(v VenueID, sym string, qty, limit float64, id string) OrderRequest {
    return OrderRequest{Venue: v, Symbol: sym, Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: qty, LimitPrice: limit, ClientOrderID: id}
}

func TestGateMasterAndVenueArm(t *testing.T) {
    cfg := baseCfg()
    marks := fakeMarks{"AAPL": 100}
    s := NewState([]VenueID{"sim-1"}) // disarmed
    if ok, reason := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 10, 100, "ET1"), marks); ok || reason == "" {
        t.Fatalf("disarmed master should block, got ok=%v reason=%q", ok, reason)
    }
    s.SetMasterArmed(true) // venue still off
    if ok, _ := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 10, 100, "ET1"), marks); ok {
        t.Fatal("venue disarmed should block")
    }
}

func TestGateDuplicateID(t *testing.T) {
    cfg, marks := baseCfg(), fakeMarks{"AAPL": 100}
    s := armedState()
    s.Apply(submitEv("sim-1", "ETdup", "AAPL", SideBuy, 10, 100, 1000))
    if ok, reason := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 10, 100, "ETdup"), marks); ok {
        t.Fatalf("duplicate ID should block, reason=%q", reason)
    }
}

func TestGateMaxOrderValue(t *testing.T) {
    cfg, marks := baseCfg(), fakeMarks{"AAPL": 100}
    s := armedState()
    // 60 * 100 = 6000 > venue cap 5000
    if ok, _ := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 60, 100, "ET1"), marks); ok {
        t.Fatal("order value over cap should block")
    }
    // 40 * 100 = 4000 <= 5000
    if ok, reason := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 40, 100, "ET1"), marks); !ok {
        t.Fatalf("order value under cap should pass, reason=%q", reason)
    }
}

func TestGateMarketOrderValuationUsesMark(t *testing.T) {
    cfg := baseCfg()
    s := armedState()
    req := OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeMarket, Qty: 60, ClientOrderID: "ET1"}
    if ok, reason := Evaluate(s, cfg, req, fakeMarks{"AAPL": 100}); ok { // 60*100=6000 > 5000
        t.Fatalf("market order valued at mark over cap should block, reason=%q", reason)
    }
    if ok, reason := Evaluate(s, cfg, req, fakeMarks{}); ok || reason == "" { // no mark → cannot value → block
        t.Fatalf("market order without a mark should block, ok=%v reason=%q", ok, reason)
    }
}

func TestGateMaxResultingVenuePosition(t *testing.T) {
    cfg, marks := baseCfg(), fakeMarks{"AAPL": 100}
    s := armedState()
    s.ReconcilePositions("sim-1", []Position{{Venue: "sim-1", Symbol: "AAPL", Qty: 150}})
    // 150 held + 10 working (add below) + 50 new = 210 > 200 shares cap
    s.Apply(submitEv("sim-1", "ETw", "AAPL", SideBuy, 10, 100, 1000))
    if ok, _ := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 50, 100, "ET1"), marks); ok {
        t.Fatal("resulting venue position over shares cap should block")
    }
    // 150 + 10 + 30 = 190 <= 200
    if ok, reason := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 30, 100, "ET1"), marks); !ok {
        t.Fatalf("under shares cap should pass, reason=%q", reason)
    }
}

func TestGateMaxOpenOrders(t *testing.T) {
    cfg, marks := baseCfg(), fakeMarks{"AAPL": 100}
    s := armedState()
    s.Apply(submitEv("sim-1", "ET1", "AAPL", SideBuy, 1, 100, 1000))
    s.Apply(submitEv("sim-1", "ET2", "AAPL", SideBuy, 1, 100, 1001))
    s.Apply(submitEv("sim-1", "ET3", "AAPL", SideBuy, 1, 100, 1002))
    // 3 working + this = 4 > cap 3
    if ok, _ := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 1, 100, "ET4"), marks); ok {
        t.Fatal("exceeding max open orders should block")
    }
}

func TestGateGlobalSymbolPosition(t *testing.T) {
    cfg, marks := baseCfg(), fakeMarks{"AAPL": 100}
    cfg.Global.MaxSymbolPositionShares = 250
    s := armedState()
    s.ReconcilePositions("sim-1", []Position{{Venue: "sim-1", Symbol: "AAPL", Qty: 150}})
    s.ReconcilePositions("sim-2", []Position{{Venue: "sim-2", Symbol: "AAPL", Qty: 80}})
    // global net 230 + 40 new on sim-1 = 270 > 250 global cap (per-venue caps allow it)
    if ok, _ := Evaluate(s, cfg, buyReq("sim-1", "AAPL", 40, 100, "ET1"), marks); ok {
        t.Fatal("resulting cross-venue symbol position over global cap should block")
    }
}

func TestBreachedDayLoss(t *testing.T) {
    cfg := baseCfg()
    s := armedState()
    s.ReconcileAccount(AccountSnapshot{Venue: "sim-1", DayPnL: -600})
    s.ReconcileAccount(AccountSnapshot{Venue: "sim-2", DayPnL: -500}) // total -1100 <= -1000
    if !BreachedDayLoss(s, cfg) {
        t.Fatal("summed day loss over cap should be a breach")
    }
    s.ReconcileAccount(AccountSnapshot{Venue: "sim-2", DayPnL: -100}) // total -700
    if BreachedDayLoss(s, cfg) {
        t.Fatal("under cap should not breach")
    }
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/exec/ -run 'TestGate|TestBreached' -v`
Expected: FAIL — `Evaluate`/`BreachedDayLoss`/`GateConfig` undefined.

- [ ] **Step 3: Implement `gate.go`**

Create `engine/internal/exec/gate.go`:
```go
package exec

import "math"

// GlobalLimits caps aggregate risk across all venues. A zero value means "cap
// not set — do not enforce".
type GlobalLimits struct {
    MaxDayLoss              float64
    MaxSymbolPositionValue  float64
    MaxSymbolPositionShares float64
}

// VenueLimits caps one venue's risk. Zero values mean "cap not set".
type VenueLimits struct {
    MaxOrderValue     float64
    MaxPositionValue  float64
    MaxPositionShares float64
    MaxOpenOrders     int
}

type GateConfig struct {
    Global GlobalLimits
    Venue  map[VenueID]VenueLimits
}

// signedQty returns the request's signed effect on a position (long +, short -).
func signedQty(req OrderRequest) float64 {
    if longward(req.Side) {
        return req.Qty
    }
    return -req.Qty
}

// orderValue values an order for the max-order-value / position-value checks:
// limit orders at their limit price, market orders at the last-trade mark. ok is
// false when a market order has no mark (cannot be valued → must block).
func orderValue(req OrderRequest, marks MarkSource) (float64, bool) {
    px := req.LimitPrice
    if req.Type == TypeMarket {
        m, ok := marks.LastTrade(req.Symbol)
        if !ok {
            return 0, false
        }
        px = m
    }
    return req.Qty * px, true
}

// markOr returns the last-trade mark, falling back to the request price for
// value caps (a limit order always has a price).
func markOr(req OrderRequest, marks MarkSource) float64 {
    if m, ok := marks.LastTrade(req.Symbol); ok {
        return m
    }
    return req.LimitPrice
}

// Evaluate runs the two-layer gate. Returns (true, "") to allow, or (false,
// reason) at the first failing rule. Pure — the Core calls it in-loop.
func Evaluate(s *State, cfg GateConfig, req OrderRequest, marks MarkSource) (bool, string) {
    // 1. master armed
    if !s.MasterArmed {
        return false, "master disarmed"
    }
    // 2. venue armed
    if vs, ok := s.Venues[req.Venue]; !ok || !vs.Armed {
        return false, "venue disarmed"
    }
    // 3. duplicate ID (global — one event log)
    if _, dup := s.orderIndex[req.ClientOrderID]; dup {
        return false, "duplicate order id"
    }

    vl := cfg.Venue[req.Venue]

    // 4a. per-venue max order value
    val, ok := orderValue(req, marks)
    if !ok {
        return false, "no mark to value market order"
    }
    if vl.MaxOrderValue > 0 && val > vl.MaxOrderValue {
        return false, "order value exceeds venue cap"
    }

    // 4b. per-venue max resulting position (shares + value)
    mark := markOr(req, marks)
    venueResult := s.VenuePositionShares(req.Venue, req.Symbol) +
        directional(s.VenueWorkingSameDir(req.Venue, req.Symbol, req.Side), req.Side) +
        signedQty(req)
    if vl.MaxPositionShares > 0 && math.Abs(venueResult) > vl.MaxPositionShares {
        return false, "resulting venue position exceeds share cap"
    }
    if vl.MaxPositionValue > 0 && math.Abs(venueResult)*mark > vl.MaxPositionValue {
        return false, "resulting venue position exceeds value cap"
    }

    // 4c. per-venue max open orders
    if vl.MaxOpenOrders > 0 && workingCount(s.Venue(req.Venue), req.Symbol) >= vl.MaxOpenOrders {
        return false, "max open orders on venue"
    }

    // 5. global max resulting per-symbol position (shares + value) across venues
    globalResult := s.SymbolNetShares(req.Symbol) +
        directional(s.SymbolWorkingSameDir(req.Symbol, req.Side), req.Side) +
        signedQty(req)
    if cfg.Global.MaxSymbolPositionShares > 0 && math.Abs(globalResult) > cfg.Global.MaxSymbolPositionShares {
        return false, "resulting symbol position exceeds global share cap"
    }
    if cfg.Global.MaxSymbolPositionValue > 0 && math.Abs(globalResult)*mark > cfg.Global.MaxSymbolPositionValue {
        return false, "resulting symbol position exceeds global value cap"
    }
    return true, ""
}

// directional signs a same-direction working-exposure magnitude by side.
func directional(mag float64, side Side) float64 {
    if longward(side) {
        return mag
    }
    return -mag
}

// workingCount counts working orders (any symbol) on a venue — the max-open-
// orders cap is a venue-wide working-order count.
func workingCount(vs *VenueState, _ string) int {
    n := 0
    for _, o := range vs.Orders {
        if o.Working() {
            n++
        }
    }
    return n
}

// BreachedDayLoss reports whether the summed venue day P&L has breached the
// global max-day-loss cap. The Core calls this on each account refresh and
// auto-disarms the master switch on breach.
func BreachedDayLoss(s *State, cfg GateConfig) bool {
    if cfg.Global.MaxDayLoss <= 0 {
        return false
    }
    return s.TotalDayPnL() <= -cfg.Global.MaxDayLoss
}
```

- [ ] **Step 4: Run it — verify it passes**

Run: `cd engine && go test -race ./internal/exec/ -run 'TestGate|TestBreached' -v`
Expected: PASS (all).

- [ ] **Step 5: Commit**

```bash
cd engine && gofmt -w internal/exec/ && go vet ./internal/exec/
git add internal/exec/gate.go internal/exec/gate_test.go
git commit -m "feat(engine/exec): two-layer safety gate + day-loss breach check"
```

---

## Task 9: Config growth — `[[venue]]` + `[gate.global]` + `[gate.venue.<id>]`

Adds the execution config sections. `[[venue]]` is an array-of-tables (one per configured `(broker, account, env)`); `[gate.global]` is a nested table; `[gate.venue.<id>]` is a map-of-tables keyed by venue slug. Parsing only — the mapping from these config structs to `exec.GateConfig` and to adapter instances happens at wiring time (Plan 6). `config` stays free of `exec` (it is a leaf; the wiring layer bridges).

**Files:**
- Modify: `engine/internal/config/config.go`
- Modify: `engine/internal/config/config_test.go`

**Interfaces:**
- Consumes: existing `config.Config`/`Default`/`Load`.
- Produces:

```go
type Venue struct { ID, Broker, Env, Credentials, AccountID string } // [[venue]]
type GateGlobal struct { MaxDayLoss, MaxSymbolPositionValue, MaxSymbolPositionShares float64 }
type GateVenue  struct { MaxOrderValue, MaxPositionValue, MaxPositionShares float64; MaxOpenOrders int }
type Gate struct { Global GateGlobal; Venue map[string]GateVenue }
// Config gains: Venues []Venue `toml:"venue"`; Gate Gate `toml:"gate"`
```

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/config/config_test.go`:
```go
func TestVenueAndGateParse(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "config.toml")
    body := `
[[venue]]
id = "alpaca-paper"
broker = "alpaca"
env = "paper"
credentials = "alpaca"

[[venue]]
id = "tz-live"
broker = "tradezero"
env = "live"
credentials = "tradeZero"
account_id = "ACC123"

[gate.global]
max_day_loss = 1000.0
max_symbol_position_value = 100000.0
max_symbol_position_shares = 1000.0

[gate.venue.alpaca-paper]
max_order_value = 5000.0
max_position_value = 20000.0
max_position_shares = 200.0
max_open_orders = 3
`
    if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
        t.Fatal(err)
    }
    cfg, err := Load(path)
    if err != nil {
        t.Fatal(err)
    }
    if len(cfg.Venues) != 2 || cfg.Venues[0].ID != "alpaca-paper" || cfg.Venues[1].Broker != "tradezero" || cfg.Venues[1].AccountID != "ACC123" {
        t.Fatalf("venues wrong: %+v", cfg.Venues)
    }
    if cfg.Gate.Global.MaxDayLoss != 1000 {
        t.Fatalf("gate global wrong: %+v", cfg.Gate.Global)
    }
    gv, ok := cfg.Gate.Venue["alpaca-paper"]
    if !ok || gv.MaxOrderValue != 5000 || gv.MaxOpenOrders != 3 {
        t.Fatalf("gate venue wrong: %+v ok=%v", gv, ok)
    }
}

func TestVenueDefaultsEmpty(t *testing.T) {
    cfg := Default()
    if len(cfg.Venues) != 0 {
        t.Fatalf("default venues should be empty, got %+v", cfg.Venues)
    }
    if cfg.Gate.Venue != nil && len(cfg.Gate.Venue) != 0 {
        t.Fatalf("default gate venue map should be empty, got %+v", cfg.Gate.Venue)
    }
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/config/ -run 'TestVenue|TestGate' -v`
Expected: FAIL — `cfg.Venues`/`cfg.Gate` undefined.

- [ ] **Step 3: Implement the sections**

In `engine/internal/config/config.go`, add the section structs (near the other section types):
```go
// Venue is one configured execution venue.  ->  [[venue]]
type Venue struct {
    ID          string `toml:"id"`          // slug used in events, topics, commands, gate config
    Broker      string `toml:"broker"`      // tradezero | alpaca | moomoo | sim
    Env         string `toml:"env"`         // paper | live
    Credentials string `toml:"credentials"` // key into ~/.eJournal/credentials.json
    AccountID   string `toml:"account_id"`  // broker-specific (TZ accountId, moomoo accID)
}

// GateGlobal caps aggregate risk across all venues.  ->  [gate.global]
type GateGlobal struct {
    MaxDayLoss              float64 `toml:"max_day_loss"`
    MaxSymbolPositionValue  float64 `toml:"max_symbol_position_value"`
    MaxSymbolPositionShares float64 `toml:"max_symbol_position_shares"`
}

// GateVenue caps one venue's risk.  ->  [gate.venue.<id>]
type GateVenue struct {
    MaxOrderValue     float64 `toml:"max_order_value"`
    MaxPositionValue  float64 `toml:"max_position_value"`
    MaxPositionShares float64 `toml:"max_position_shares"`
    MaxOpenOrders     int     `toml:"max_open_orders"`
}

// Gate is the two-layer safety-gate config.  ->  [gate]
type Gate struct {
    Global GateGlobal           `toml:"global"`
    Venue  map[string]GateVenue `toml:"venue"`
}
```
Add the fields to `Config`:
```go
type Config struct {
    OpenD  OpenD   `toml:"opend"`
    Feed   Feed    `toml:"feed"`
    MD     MD      `toml:"md"`
    Store  Store   `toml:"store"`
    Venues []Venue `toml:"venue"`
    Gate   Gate    `toml:"gate"`
}
```
`Default()` needs no change — venues default to an empty slice and `Gate.Venue` to a nil map, which the wiring layer treats as "no configured venues / no caps". (Do **not** pre-populate the slice in `Default()`; BurntSushi appends file entries to slice defaults.)

- [ ] **Step 4: Run it — verify it passes**

Run: `cd engine && go test ./internal/config/ -run 'TestVenue|TestGate' -v`
Expected: PASS (both). Also re-run the full config suite: `cd engine && go test ./internal/config/`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
cd engine && gofmt -w internal/config/ && go vet ./internal/config/
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(engine/config): [[venue]] array + [gate.global]/[gate.venue.<id>] sections"
```

---

## Task 10: `SimBroker` — the `exec.Broker` implementation

The deterministic broker that proves the whole chassis without a network. It implements `exec.Broker` with a **marketability** fill model: market orders (and marketable limits) fill immediately at the mark/limit; non-marketable limits **rest** until canceled, replaced into marketability, or crossed by a later `SetMark`. It emits order-lifecycle events (which double as `BrokerEvent`s) plus `BrokerPositions`/`BrokerAccount` reconcile snapshots on its `Events()` channel. `Capabilities{NativeReplace, FlattenAll: true}` so the E2E exercises native replace + flatten. Lives under `broker/` (the package dir Plan 5's real adapters extend); imports `exec`, never the reverse.

**Files:**
- Create: `engine/internal/broker/sim/sim.go`
- Create: `engine/internal/broker/sim/sim_test.go`

**Interfaces:**
- Consumes: `exec.Broker`/`exec.BrokerEvent`/events/types (Tasks 1/3/4), `clock.Clock`.
- Produces:

```go
func New(venue exec.VenueID, clk clock.Clock) *Broker // var _ exec.Broker = (*Broker)(nil)
func (b *Broker) SetMark(symbol string, price float64) // seed/move price; crosses resting orders
func (b *Broker) SetAccount(a exec.AccountSnapshot)     // test hook: emit a BrokerAccount (drives day-loss)
```

- [ ] **Step 1: Write the failing test**

Create `engine/internal/broker/sim/sim_test.go`:
```go
package sim

import (
    "context"
    "testing"
    "time"

    "github.com/earlisreal/eTape/engine/internal/clock"
    "github.com/earlisreal/eTape/engine/internal/exec"
)

func newSim(t *testing.T) *Broker {
    t.Helper()
    b := New("sim-1", clock.NewFake(time.UnixMilli(1000)))
    b.SetMark("AAPL", 100)
    return b
}

// drain reads the next event within a timeout (events are emitted synchronously
// into a buffered channel, so this returns promptly).
func drain(t *testing.T, b *Broker) exec.BrokerEvent {
    t.Helper()
    select {
    case ev := <-b.Events():
        return ev
    case <-time.After(time.Second):
        t.Fatal("timed out waiting for broker event")
        return nil
    }
}

func TestSimMarketableLimitFills(t *testing.T) {
    b := newSim(t)
    req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 100, ClientOrderID: "ET1"}
    ack, err := b.SubmitOrder(context.Background(), req)
    if err != nil || !ack.Accepted {
        t.Fatalf("submit: ack=%+v err=%v", ack, err)
    }
    if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
        t.Fatal("first event should be OrderAccepted")
    }
    f, ok := drain(t, b).(exec.OrderFilled)
    if !ok || f.F.Qty != 10 || f.F.Price != 100 || f.LeavesQty != 0 {
        t.Fatalf("expected full fill at 100, got %+v ok=%v", f, ok)
    }
    if _, ok := drain(t, b).(exec.BrokerPositions); !ok {
        t.Fatal("fill should be followed by a BrokerPositions snapshot")
    }
}

func TestSimNonMarketableRestsThenCancel(t *testing.T) {
    b := newSim(t)
    // Buy limit 90 with mark 100 → not marketable → rests.
    req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 90, ClientOrderID: "ET1"}
    if _, err := b.SubmitOrder(context.Background(), req); err != nil {
        t.Fatal(err)
    }
    if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
        t.Fatal("rested order should emit OrderAccepted only")
    }
    if err := b.CancelOrder(context.Background(), "ET1"); err != nil {
        t.Fatal(err)
    }
    if _, ok := drain(t, b).(exec.OrderCanceled); !ok {
        t.Fatal("cancel should emit OrderCanceled")
    }
    // Canceling an unknown/terminal order errors.
    if err := b.CancelOrder(context.Background(), "ET1"); err == nil {
        t.Fatal("second cancel should error (order gone)")
    }
}

func TestSimSetMarkCrossesRestingOrder(t *testing.T) {
    b := newSim(t)
    req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 95, ClientOrderID: "ET1"}
    _, _ = b.SubmitOrder(context.Background(), req)
    _ = drain(t, b) // OrderAccepted
    b.SetMark("AAPL", 94) // mark drops to/through 95 → buy limit 95 now marketable
    f, ok := drain(t, b).(exec.OrderFilled)
    if !ok || f.F.Price != 95 {
        t.Fatalf("crossing should fill at limit 95, got %+v ok=%v", f, ok)
    }
    _ = drain(t, b) // BrokerPositions
}

func TestSimReplaceAndSnapshot(t *testing.T) {
    b := newSim(t)
    _, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 90, ClientOrderID: "ET1"})
    _ = drain(t, b) // OrderAccepted
    if err := b.ReplaceOrder(context.Background(), "ET1", exec.ReplaceRequest{Qty: 20, LimitPrice: 91}); err != nil {
        t.Fatal(err)
    }
    if r, ok := drain(t, b).(exec.OrderReplaced); !ok || r.NewQty != 20 || r.NewLimit != 91 {
        t.Fatalf("replace event wrong: %+v ok=%v", r, ok)
    }
    _, _, orders, err := b.Snapshot(context.Background())
    if err != nil {
        t.Fatal(err)
    }
    if len(orders) != 1 || orders[0].Qty != 20 || orders[0].LimitPrice != 91 {
        t.Fatalf("snapshot orders wrong: %+v", orders)
    }
    if !b.Capabilities().NativeReplace || !b.Capabilities().FlattenAll {
        t.Fatal("SimBroker should advertise native replace + flatten")
    }
}

func TestSimCancelAll(t *testing.T) {
    b := newSim(t)
    _, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 1, LimitPrice: 90, ClientOrderID: "ET1"})
    _, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "MSFT", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 1, LimitPrice: 90, ClientOrderID: "ET2"})
    _, _ = drain(t, b), drain(t, b) // two OrderAccepted
    if err := b.CancelAll(context.Background(), ""); err != nil {
        t.Fatal(err)
    }
    got := map[string]bool{}
    got[drain(t, b).(exec.OrderCanceled).OID] = true
    got[drain(t, b).(exec.OrderCanceled).OID] = true
    if !got["ET1"] || !got["ET2"] {
        t.Fatalf("cancel-all should cancel both, got %v", got)
    }
}
```

- [ ] **Step 2: Run it — verify it fails**

Run: `cd engine && go test ./internal/broker/sim/ -v`
Expected: FAIL — `sim` package / `New` undefined.

- [ ] **Step 3: Implement `sim.go`**

Create `engine/internal/broker/sim/sim.go`:
```go
// Package sim is a deterministic in-memory exec.Broker used for tests, replay,
// and (v1.5) practice mode. It fills market and marketable-limit orders
// immediately and rests non-marketable limits until canceled, replaced, or
// crossed by a later SetMark. It imports exec, never the reverse.
package sim

import (
    "context"
    "fmt"
    "sync"

    "github.com/earlisreal/eTape/engine/internal/clock"
    "github.com/earlisreal/eTape/engine/internal/exec"
)

// Broker is a single-venue simulated broker.
type Broker struct {
    venue exec.VenueID
    clk   clock.Clock
    ev    chan exec.BrokerEvent

    mu     sync.Mutex
    marks  map[string]float64
    orders map[string]*exec.Order // resting (working) orders
    pos    map[string]*exec.Position
    acct   exec.AccountSnapshot
    bseq   int64 // broker order-id counter
}

var _ exec.Broker = (*Broker)(nil)

// New builds a SimBroker for a venue.
func New(venue exec.VenueID, clk clock.Clock) *Broker {
    return &Broker{
        venue:  venue,
        clk:    clk,
        ev:     make(chan exec.BrokerEvent, 256),
        marks:  map[string]float64{},
        orders: map[string]*exec.Order{},
        pos:    map[string]*exec.Position{},
        acct:   exec.AccountSnapshot{Venue: venue},
    }
}

func (b *Broker) Capabilities() exec.Capabilities {
    return exec.Capabilities{NativeReplace: true, FlattenAll: true, OvernightSession: false}
}

func (b *Broker) Events() <-chan exec.BrokerEvent { return b.ev }

func (b *Broker) emit(e exec.BrokerEvent) { b.ev <- e }

// SetMark seeds/moves a symbol's price and crosses any resting orders it makes
// marketable.
func (b *Broker) SetMark(symbol string, price float64) {
    b.mu.Lock()
    b.marks[symbol] = price
    crossed := b.crossRestingLocked(symbol, price)
    b.mu.Unlock()
    for _, ev := range crossed {
        b.emit(ev)
    }
}

// SetAccount overwrites the venue account and emits a BrokerAccount reconcile
// (the test hook that drives day-loss auto-disarm deterministically).
func (b *Broker) SetAccount(a exec.AccountSnapshot) {
    a.Venue = b.venue
    b.mu.Lock()
    b.acct = a
    b.mu.Unlock()
    b.emit(exec.BrokerAccount{Account: a})
}

func (b *Broker) now() int64 { return b.clk.Now().UnixMilli() }

func marketable(side exec.Side, limit, mark float64) bool {
    switch side {
    case exec.SideBuy, exec.SideCover:
        return limit >= mark
    default: // Sell, Short
        return limit <= mark
    }
}

func (b *Broker) SubmitOrder(_ context.Context, req exec.OrderRequest) (exec.OrderAck, error) {
    if err := req.Validate(); err != nil {
        return exec.OrderAck{}, err
    }
    b.mu.Lock()
    b.bseq++
    brokerID := fmt.Sprintf("SIM-%d", b.bseq)
    o := &exec.Order{
        Venue: b.venue, ID: req.ClientOrderID, Symbol: req.Symbol, Side: req.Side,
        Type: req.Type, TIF: req.TIF, Qty: req.Qty, LimitPrice: req.LimitPrice,
        StopPrice: req.StopPrice, Status: exec.StatusAccepted, LeavesQty: req.Qty,
        CreatedMs: b.now(), UpdatedMs: b.now(),
    }
    b.orders[o.ID] = o
    mark, hasMark := b.marks[req.Symbol]
    var post []exec.BrokerEvent
    post = append(post, exec.OrderAccepted{V: b.venue, OID: o.ID, BrokerOrderID: brokerID, Ts: b.now()})
    // Market orders need a mark; without one they are rejected (mirrors gate).
    if req.Type == exec.TypeMarket && !hasMark {
        delete(b.orders, o.ID)
        post = append(post, exec.OrderRejected{V: b.venue, OID: o.ID, Reason: "sim: no mark for market order", Ts: b.now()})
        b.mu.Unlock()
        for _, e := range post {
            b.emit(e)
        }
        return exec.OrderAck{OrderID: o.ID, Accepted: true, Message: brokerID}, nil
    }
    fillPx := req.LimitPrice
    doFill := req.Type == exec.TypeMarket || marketable(req.Side, req.LimitPrice, mark)
    if req.Type == exec.TypeMarket {
        fillPx = mark
    }
    if doFill {
        post = append(post, b.fillLocked(o, fillPx)...)
    }
    b.mu.Unlock()
    for _, e := range post {
        b.emit(e)
    }
    return exec.OrderAck{OrderID: o.ID, Accepted: true, Message: brokerID}, nil
}

// fillLocked fully fills a resting order at price px, updates position + account,
// and returns the events to emit (OrderFilled + BrokerPositions). Caller holds mu.
func (b *Broker) fillLocked(o *exec.Order, px float64) []exec.BrokerEvent {
    qty := o.LeavesQty
    o.ExecutedQty = o.Qty
    o.LeavesQty = 0
    o.AvgFillPrice = px
    o.Status = exec.StatusFilled
    o.UpdatedMs = b.now()
    delete(b.orders, o.ID)

    signed := qty
    if !(o.Side == exec.SideBuy || o.Side == exec.SideCover) {
        signed = -qty
    }
    p := b.pos[o.Symbol]
    if p == nil {
        p = &exec.Position{Venue: b.venue, Symbol: o.Symbol}
        b.pos[o.Symbol] = p
    }
    p.Qty += signed
    p.AvgPrice = px // simplistic: last fill price (v1.5 does weighted avg)

    fill := exec.Fill{Venue: b.venue, OrderID: o.ID, Symbol: o.Symbol, Side: o.Side, Qty: qty, Price: px, TsMs: b.now()}
    return []exec.BrokerEvent{
        exec.OrderFilled{F: fill, CumQty: o.ExecutedQty, LeavesQty: 0, AvgPrice: px},
        exec.BrokerPositions{V: b.venue, Positions: b.positionsLocked()},
    }
}

// crossRestingLocked fills any resting orders on a symbol that the new mark makes
// marketable. Caller holds mu.
func (b *Broker) crossRestingLocked(symbol string, mark float64) []exec.BrokerEvent {
    var out []exec.BrokerEvent
    for _, o := range b.orders {
        if o.Symbol == symbol && marketable(o.Side, o.LimitPrice, mark) {
            out = append(out, b.fillLocked(o, o.LimitPrice)...)
        }
    }
    return out
}

func (b *Broker) positionsLocked() []exec.Position {
    out := make([]exec.Position, 0, len(b.pos))
    for _, p := range b.pos {
        out = append(out, *p)
    }
    return out
}

func (b *Broker) ReplaceOrder(_ context.Context, orderID string, req exec.ReplaceRequest) error {
    b.mu.Lock()
    o, ok := b.orders[orderID]
    if !ok {
        b.mu.Unlock()
        return fmt.Errorf("sim: replace: order %s not working", orderID)
    }
    o.Qty = req.Qty
    if req.LimitPrice > 0 {
        o.LimitPrice = req.LimitPrice
    }
    if req.StopPrice > 0 {
        o.StopPrice = req.StopPrice
    }
    o.LeavesQty = req.Qty - o.ExecutedQty
    o.UpdatedMs = b.now()
    post := []exec.BrokerEvent{exec.OrderReplaced{V: b.venue, OID: orderID, NewQty: req.Qty, NewLimit: req.LimitPrice, NewStop: req.StopPrice, Ts: b.now()}}
    // A replace into marketability fills immediately.
    if mark, ok := b.marks[o.Symbol]; ok && marketable(o.Side, o.LimitPrice, mark) {
        post = append(post, b.fillLocked(o, o.LimitPrice)...)
    }
    b.mu.Unlock()
    for _, e := range post {
        b.emit(e)
    }
    return nil
}

func (b *Broker) CancelOrder(_ context.Context, orderID string) error {
    b.mu.Lock()
    _, ok := b.orders[orderID]
    if !ok {
        b.mu.Unlock()
        return fmt.Errorf("sim: cancel: order %s not working", orderID)
    }
    delete(b.orders, orderID)
    b.mu.Unlock()
    b.emit(exec.OrderCanceled{V: b.venue, OID: orderID, Ts: b.now()})
    return nil
}

func (b *Broker) CancelAll(_ context.Context, symbol string) error {
    b.mu.Lock()
    var ids []string
    for id, o := range b.orders {
        if symbol == "" || o.Symbol == symbol {
            ids = append(ids, id)
        }
    }
    for _, id := range ids {
        delete(b.orders, id)
    }
    b.mu.Unlock()
    for _, id := range ids {
        b.emit(exec.OrderCanceled{V: b.venue, OID: id, Ts: b.now()})
    }
    return nil
}

func (b *Broker) Snapshot(_ context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
    b.mu.Lock()
    defer b.mu.Unlock()
    orders := make([]exec.Order, 0, len(b.orders))
    for _, o := range b.orders {
        orders = append(orders, *o)
    }
    return b.acct, b.positionsLocked(), orders, nil
}
```

- [ ] **Step 4: Run it — verify it passes**

Run: `cd engine && go test -race ./internal/broker/sim/ -v`
Expected: PASS (all).

- [ ] **Step 5: Commit**

```bash
cd engine && gofmt -w internal/broker/ && go vet ./internal/broker/...
git add internal/broker/sim/sim.go internal/broker/sim/sim_test.go
git commit -m "feat(engine/broker): SimBroker — marketability fills, resting orders, replace/cancel/snapshot"
```

---

## Task 11: `Core` coordinator — single-writer loop + submit path + broker-event intake

The single-writer goroutine that owns all execution state. Commands (with reply channels), broker events (pumped from every venue's `Events()`), and last-trade marks enter its inbox; it runs the gate in-loop, appends synchronously, folds, and dispatches broker I/O to off-loop workers so a slow venue never blocks the writer. This task delivers the submit path, arm/disarm, marks intake, broker-event intake (accept/reject/fill/reconcile), the `Update` output stream, and a single-venue end-to-end test against SimBroker + a real `store`.

**Files:**
- Create: `engine/internal/exec/update.go`
- Create: `engine/internal/exec/core.go`
- Create: `engine/internal/exec/core_test.go`

**Interfaces:**
- Consumes: everything above + `clock.Clock`, `session.DayMs`.
- Produces:

```go
type Command interface{ isCommand() }
type SubmitOrder  struct{ Venue VenueID; Symbol string; Side Side; Type OrderType; TIF TIF; Qty, LimitPrice, StopPrice float64 }
type CancelOrder  struct{ Venue VenueID; OrderID string }
type ReplaceOrder struct{ Venue VenueID; OrderID string; Qty, LimitPrice, StopPrice float64 }
type Flatten      struct{ Venue VenueID }
type KillSwitch   struct{ Venue VenueID } // "" = all venues
type Arm          struct{ Venue VenueID } // "" = master
type Disarm       struct{ Venue VenueID } // "" = master
type CmdAck struct{ Accepted bool; Reason, OrderID string }

type Update interface{ isExecUpdate() }
type OrderUpdate    struct{ Order Order }
type FillUpdate     struct{ Fill Fill }
type AccountUpdate  struct{ Account AccountSnapshot; VenueArmed, MasterArmed bool }
type PositionUpdate struct{ Position Position }
type StatusUpdate   struct{ Venue VenueID; Connected, MasterArmed bool; Note string }

type CoreConfig struct {
    Venues  []VenueID
    Gate    GateConfig
    Store   EventStore
    Brokers map[VenueID]Broker
    Clock   clock.Clock
    IDGen   *OrderIDGen
    SysLog  func(kind, detail string) // optional; store.AppendSysEvent in prod
}
func NewCore(cfg CoreConfig) *Core
func (c *Core) Do(cmd Command) CmdAck    // synchronous; enqueues with reply, waits
func (c *Core) FeedMark(m Mark)          // non-blocking, keep-latest
func (c *Core) Updates() <-chan Update
func (c *Core) Recover(ctx context.Context) error // boot: replay events + snapshot venues
func (c *Core) Run(ctx context.Context) error     // single-writer loop
```

- [ ] **Step 1: Implement `update.go`**

Create `engine/internal/exec/update.go`:
```go
package exec

// Update is a typed change the Core emits for uihub (Plan 6 maps these to the
// exec.* WS topics and owns coalescing). Sealed union.
type Update interface{ isExecUpdate() }

type OrderUpdate struct{ Order Order }
type FillUpdate struct{ Fill Fill }
type AccountUpdate struct {
    Account     AccountSnapshot
    VenueArmed  bool
    MasterArmed bool
}
type PositionUpdate struct{ Position Position }
type StatusUpdate struct {
    Venue       VenueID
    Connected   bool
    MasterArmed bool
    Note        string
}

func (OrderUpdate) isExecUpdate()    {}
func (FillUpdate) isExecUpdate()     {}
func (AccountUpdate) isExecUpdate()  {}
func (PositionUpdate) isExecUpdate() {}
func (StatusUpdate) isExecUpdate()   {}
```

- [ ] **Step 2: Implement `core.go`**

Create `engine/internal/exec/core.go`:
```go
package exec

import (
    "context"
    "log/slog"
    "sync/atomic"

    "github.com/earlisreal/eTape/engine/internal/clock"
    "github.com/earlisreal/eTape/engine/internal/session"
)

// Command is a UI→engine execution command. Sealed union.
type Command interface{ isCommand() }

type SubmitOrder struct {
    Venue      VenueID
    Symbol     string
    Side       Side
    Type       OrderType
    TIF        TIF
    Qty        float64
    LimitPrice float64
    StopPrice  float64
}
type CancelOrder struct {
    Venue   VenueID
    OrderID string
}
type ReplaceOrder struct {
    Venue      VenueID
    OrderID    string
    Qty        float64
    LimitPrice float64
    StopPrice  float64
}
type Flatten struct{ Venue VenueID }
type KillSwitch struct{ Venue VenueID }
type Arm struct{ Venue VenueID }
type Disarm struct{ Venue VenueID }

func (SubmitOrder) isCommand()  {}
func (CancelOrder) isCommand()  {}
func (ReplaceOrder) isCommand() {}
func (Flatten) isCommand()      {}
func (KillSwitch) isCommand()   {}
func (Arm) isCommand()          {}
func (Disarm) isCommand()       {}

// CmdAck is the synchronous accepted|blocked ack; order outcomes arrive later as
// Updates.
type CmdAck struct {
    Accepted bool
    Reason   string
    OrderID  string
}

type cmdReq struct {
    cmd   Command
    reply chan CmdAck
}

// markState is the Core's latest-mark map; implements MarkSource.
type markState map[string]float64

func (m markState) LastTrade(sym string) (float64, bool) { v, ok := m[sym]; return v, ok }

// Core is the single-writer execution coordinator.
type Core struct {
    venues  []VenueID
    gate    GateConfig
    store   EventStore
    brokers map[VenueID]Broker
    clk     clock.Clock
    idgen   *OrderIDGen
    syslog  func(kind, detail string)

    cmds    chan cmdReq
    bevents chan BrokerEvent
    markCh  chan Mark
    updates chan Update
    dropped atomic.Uint64

    state *State
    marks markState
}

func NewCore(cfg CoreConfig) *Core {
    sl := cfg.SysLog
    if sl == nil {
        sl = func(string, string) {}
    }
    return &Core{
        venues:  cfg.Venues,
        gate:    cfg.Gate,
        store:   cfg.Store,
        brokers: cfg.Brokers,
        clk:     cfg.Clock,
        idgen:   cfg.IDGen,
        syslog:  sl,
        cmds:    make(chan cmdReq),
        bevents: make(chan BrokerEvent, 1024),
        markCh:  make(chan Mark, 256),
        updates: make(chan Update, 4096),
        state:   NewState(cfg.Venues),
        marks:   markState{},
    }
}

func (c *Core) Updates() <-chan Update { return c.updates }

func (c *Core) DroppedUpdates() uint64 { return c.dropped.Load() }

// Do submits a command and blocks for its accepted|blocked ack. Safe from any
// goroutine.
func (c *Core) Do(cmd Command) CmdAck {
    reply := make(chan CmdAck, 1)
    c.cmds <- cmdReq{cmd: cmd, reply: reply}
    return <-reply
}

// FeedMark delivers a last-trade mark; keep-latest, drop-on-full (never blocks
// the caller — mirrors md.Core's mark path).
func (c *Core) FeedMark(m Mark) {
    select {
    case c.markCh <- m:
    default:
    }
}

// emit sends an update; drop-and-count on overflow (uihub owns coalescing).
func (c *Core) emit(u Update) {
    select {
    case c.updates <- u:
    default:
        c.dropped.Add(1)
    }
}

func (c *Core) now() int64 { return c.clk.Now().UnixMilli() }

// Recover rebuilds state at boot: replay today's persisted events, then seed
// account/positions/open-orders from each venue's broker snapshot. Call before
// Run.
func (c *Core) Recover(ctx context.Context) error {
    fromMs := session.DayMs(c.now())
    envs, err := c.store.ReadExecEventsSince(fromMs)
    if err != nil {
        return err
    }
    for _, env := range envs {
        ev, err := DecodeEvent(env.Kind, env.Payload)
        if err != nil {
            return err
        }
        c.state.Apply(ev)
    }
    for _, v := range c.venues {
        b, ok := c.brokers[v]
        if !ok {
            continue
        }
        acct, pos, orders, err := b.Snapshot(ctx)
        if err != nil {
            c.syslog("exec.recover", "snapshot "+string(v)+": "+err.Error())
            continue
        }
        c.state.ReconcileAccount(acct)
        c.state.ReconcilePositions(v, pos)
        c.state.ReconcileOpenOrders(v, orders)
    }
    return nil
}

// Run is the single writer. It pumps every venue's broker events into the inbox
// and processes commands, broker events, and marks one at a time until ctx ends.
func (c *Core) Run(ctx context.Context) error {
    for v, b := range c.brokers {
        go c.pump(ctx, v, b)
    }
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case req := <-c.cmds:
            req.reply <- c.handleCmd(ctx, req.cmd)
        case be := <-c.bevents:
            c.handleBrokerEvent(ctx, be)
        case m := <-c.markCh:
            c.marks[m.Symbol] = m.Price
        }
    }
}

// pump forwards one venue's broker events into the shared inbox.
func (c *Core) pump(ctx context.Context, _ VenueID, b Broker) {
    ch := b.Events()
    for {
        select {
        case <-ctx.Done():
            return
        case be, ok := <-ch:
            if !ok {
                return
            }
            select {
            case c.bevents <- be:
            case <-ctx.Done():
                return
            }
        }
    }
}

// appendAndFold persists an event synchronously (append failure is returned so
// the submit path can block), then folds it and emits the matching Update.
func (c *Core) appendAndFold(ev Event, src Source) error {
    env := EnvelopeOf(ev, src, 0)
    fill, _ := FillRowOf(ev)
    if _, err := c.store.AppendExecEvent(env, fill); err != nil {
        return err
    }
    c.state.Apply(ev)
    c.emitForEvent(ev)
    return nil
}

// emitForEvent pushes the Update(s) an event implies.
func (c *Core) emitForEvent(ev Event) {
    if f, ok := ev.(OrderFilled); ok {
        c.emit(FillUpdate{Fill: f.F})
    }
    if v, ok := c.state.OrderVenue(ev.OrderID()); ok {
        if o, ok := c.state.Venue(v).Orders[ev.OrderID()]; ok {
            c.emit(OrderUpdate{Order: o})
        }
    }
}

func (c *Core) handleCmd(ctx context.Context, cmd Command) CmdAck {
    switch cm := cmd.(type) {
    case SubmitOrder:
        return c.handleSubmit(ctx, cm)
    case CancelOrder:
        return c.handleCancel(ctx, cm)
    case ReplaceOrder:
        return c.handleReplace(ctx, cm)
    case Flatten:
        return c.handleFlatten(ctx, cm)
    case KillSwitch:
        return c.handleKill(ctx, cm)
    case Arm:
        return c.handleArm(cm.Venue, true)
    case Disarm:
        return c.handleArm(cm.Venue, false)
    default:
        return CmdAck{Accepted: false, Reason: "unknown command"}
    }
}

func (c *Core) handleSubmit(ctx context.Context, cm SubmitOrder) CmdAck {
    req := OrderRequest{
        Venue: cm.Venue, Symbol: cm.Symbol, Side: cm.Side, Type: cm.Type, TIF: cm.TIF,
        Qty: cm.Qty, LimitPrice: cm.LimitPrice, StopPrice: cm.StopPrice,
        ClientOrderID: c.idgen.Next(),
    }
    if err := req.Validate(); err != nil {
        return CmdAck{Accepted: false, Reason: err.Error(), OrderID: req.ClientOrderID}
    }
    if ok, reason := Evaluate(c.state, c.gate, req, c.marks); !ok {
        ev := OrderBlocked{V: req.Venue, OID: req.ClientOrderID, Req: req, Reason: reason, Ts: c.now()}
        if err := c.appendAndFold(ev, SrcLocal); err != nil {
            slog.Error("exec: append OrderBlocked failed", "err", err)
        }
        return CmdAck{Accepted: false, Reason: reason, OrderID: req.ClientOrderID}
    }
    // Append OrderSubmitted BEFORE the POST (crash-recovery rule). Append failure
    // blocks submission.
    o := Order{Venue: req.Venue, ID: req.ClientOrderID, Symbol: req.Symbol, Side: req.Side,
        Type: req.Type, TIF: req.TIF, Qty: req.Qty, LimitPrice: req.LimitPrice,
        StopPrice: req.StopPrice, Status: StatusSubmitted, LeavesQty: req.Qty,
        CreatedMs: c.now(), UpdatedMs: c.now()}
    if err := c.appendAndFold(OrderSubmitted{Order: o}, SrcLocal); err != nil {
        return CmdAck{Accepted: false, Reason: "event append failed: " + err.Error(), OrderID: req.ClientOrderID}
    }
    b := c.brokers[req.Venue]
    go c.postSubmit(ctx, b, req)
    return CmdAck{Accepted: true, OrderID: req.ClientOrderID}
}

// postSubmit performs the broker POST off the writer loop; a transport error is
// fed back as an OrderRejected event (Plan 5 adds the retry-once-same-ID probe).
func (c *Core) postSubmit(ctx context.Context, b Broker, req OrderRequest) {
    if b == nil {
        return
    }
    if _, err := b.SubmitOrder(ctx, req); err != nil {
        c.bevents <- OrderRejected{V: req.Venue, OID: req.ClientOrderID, Reason: "transport: " + err.Error(), Ts: c.now()}
    }
}

func (c *Core) handleCancel(ctx context.Context, cm CancelOrder) CmdAck {
    if _, ok := c.state.OrderVenue(cm.OrderID); !ok {
        return CmdAck{Accepted: false, Reason: "unknown order", OrderID: cm.OrderID}
    }
    b := c.brokers[cm.Venue]
    go func() {
        if b != nil {
            if err := b.CancelOrder(ctx, cm.OrderID); err != nil {
                slog.Warn("exec: cancel failed", "order", cm.OrderID, "err", err)
            }
        }
    }()
    return CmdAck{Accepted: true, OrderID: cm.OrderID}
}

func (c *Core) handleReplace(ctx context.Context, cm ReplaceOrder) CmdAck {
    if _, ok := c.state.OrderVenue(cm.OrderID); !ok {
        return CmdAck{Accepted: false, Reason: "unknown order", OrderID: cm.OrderID}
    }
    b := c.brokers[cm.Venue]
    rr := ReplaceRequest{Qty: cm.Qty, LimitPrice: cm.LimitPrice, StopPrice: cm.StopPrice}
    go func() {
        if b != nil {
            if err := b.ReplaceOrder(ctx, cm.OrderID, rr); err != nil {
                slog.Warn("exec: replace failed", "order", cm.OrderID, "err", err)
            }
        }
    }()
    return CmdAck{Accepted: true, OrderID: cm.OrderID}
}

func (c *Core) handleFlatten(ctx context.Context, cm Flatten) CmdAck {
    b := c.brokers[cm.Venue]
    if b == nil {
        return CmdAck{Accepted: false, Reason: "unknown venue"}
    }
    if !b.Capabilities().FlattenAll {
        return CmdAck{Accepted: false, Reason: "flatten unsupported on venue"}
    }
    go func() {
        // Flatten is modeled as cancel-all here; real flatten uses the native
        // position-close primitive (Plan 5, Alpaca DELETE /v2/positions).
        if err := b.CancelAll(ctx, ""); err != nil {
            slog.Warn("exec: flatten failed", "venue", cm.Venue, "err", err)
        }
    }()
    return CmdAck{Accepted: true}
}

func (c *Core) handleKill(ctx context.Context, cm KillSwitch) CmdAck {
    // Kill never places orders: cancel-all on the targeted venue(s) + disarm.
    targets := c.venues
    if cm.Venue != "" {
        targets = []VenueID{cm.Venue}
        c.state.SetVenueArmed(cm.Venue, false)
    } else {
        c.state.SetMasterArmed(false)
    }
    for _, v := range targets {
        b := c.brokers[v]
        if b == nil {
            continue
        }
        go func(b Broker, v VenueID) {
            if err := b.CancelAll(ctx, ""); err != nil {
                slog.Warn("exec: kill cancel-all failed", "venue", v, "err", err)
            }
        }(b, v)
    }
    c.syslog("exec.kill", "kill switch: venue="+string(cm.Venue))
    c.emitStatus()
    return CmdAck{Accepted: true}
}

func (c *Core) handleArm(v VenueID, on bool) CmdAck {
    if v == "" {
        c.state.SetMasterArmed(on)
    } else {
        if _, ok := c.state.Venues[v]; !ok {
            return CmdAck{Accepted: false, Reason: "unknown venue"}
        }
        c.state.SetVenueArmed(v, on)
    }
    c.emitStatus()
    for _, vv := range c.venues {
        c.emit(AccountUpdate{Account: c.state.Venue(vv).Account, VenueArmed: c.state.Venue(vv).Armed, MasterArmed: c.state.MasterArmed})
    }
    return CmdAck{Accepted: true}
}

func (c *Core) emitStatus() {
    for _, v := range c.venues {
        c.emit(StatusUpdate{Venue: v, Connected: true, MasterArmed: c.state.MasterArmed})
    }
}

func (c *Core) handleBrokerEvent(_ context.Context, be BrokerEvent) {
    switch e := be.(type) {
    case Event: // order-lifecycle or StreamGap — persist + fold + emit
        if err := c.appendAndFold(e, SrcWS); err != nil {
            slog.Error("exec: append broker event failed", "kind", e.Kind(), "err", err)
        }
    case BrokerAccount:
        c.state.ReconcileAccount(e.Account)
        if BreachedDayLoss(c.state, c.gate) && c.state.MasterArmed {
            c.state.SetMasterArmed(false)
            c.syslog("exec.autodisarm", "day-loss breach: master disarmed")
            c.emitStatus()
        }
        vs := c.state.Venue(e.Account.Venue)
        c.emit(AccountUpdate{Account: e.Account, VenueArmed: vs.Armed, MasterArmed: c.state.MasterArmed})
    case BrokerPositions:
        c.state.ReconcilePositions(e.V, e.Positions)
        for _, p := range e.Positions {
            c.emit(PositionUpdate{Position: p})
        }
    case BrokerConnUp:
        c.emit(StatusUpdate{Venue: e.V, Connected: true, MasterArmed: c.state.MasterArmed})
    case BrokerConnDown:
        c.emit(StatusUpdate{Venue: e.V, Connected: false, MasterArmed: c.state.MasterArmed})
    }
}
```

- [ ] **Step 3: Write the single-venue E2E test**

Create `engine/internal/exec/core_test.go`:
```go
package exec

import (
    "context"
    "math/rand"
    "path/filepath"
    "testing"
    "time"

    "github.com/earlisreal/eTape/engine/internal/broker/sim"
    "github.com/earlisreal/eTape/engine/internal/clock"
    "github.com/earlisreal/eTape/engine/internal/store"
)

func newTestCore(t *testing.T, venues ...VenueID) (*Core, map[VenueID]*sim.Broker, context.CancelFunc) {
    t.Helper()
    clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
    st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "e.db"), Clock: clk})
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { _ = st.Close() })
    brokers := map[VenueID]Broker{}
    sims := map[VenueID]*sim.Broker{}
    for _, v := range venues {
        b := sim.New(v, clk)
        b.SetMark("AAPL", 100)
        brokers[v] = b
        sims[v] = b
    }
    cfg := CoreConfig{
        Venues: venues,
        Gate: GateConfig{
            Global: GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionShares: 1000, MaxSymbolPositionValue: 1_000_000},
            Venue:  map[VenueID]VenueLimits{},
        },
        Store: st, Brokers: brokers, Clock: clk,
        IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(1))),
    }
    for _, v := range venues {
        cfg.Gate.Venue[v] = VenueLimits{MaxOrderValue: 100000, MaxPositionValue: 1_000_000, MaxPositionShares: 1000, MaxOpenOrders: 10}
    }
    c := NewCore(cfg)
    ctx, cancel := context.WithCancel(context.Background())
    if err := c.Recover(ctx); err != nil {
        cancel()
        t.Fatal(err)
    }
    go func() { _ = c.Run(ctx) }()
    t.Cleanup(cancel)
    return c, sims, cancel
}

// waitFor reads Updates until pred returns true or a timeout elapses.
func waitFor(t *testing.T, c *Core, pred func(Update) bool) Update {
    t.Helper()
    deadline := time.After(2 * time.Second)
    for {
        select {
        case u := <-c.Updates():
            if pred(u) {
                return u
            }
        case <-deadline:
            t.Fatal("timed out waiting for expected update")
            return nil
        }
    }
}

func TestCoreDisarmedBlocks(t *testing.T) {
    c, _, _ := newTestCore(t, "sim-1")
    ack := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 10, LimitPrice: 100})
    if ack.Accepted || ack.Reason != "master disarmed" {
        t.Fatalf("disarmed submit should block, got %+v", ack)
    }
    // A blocked order still emits an OrderUpdate with StatusBlocked.
    u := waitFor(t, c, func(u Update) bool { _, ok := u.(OrderUpdate); return ok })
    if u.(OrderUpdate).Order.Status != StatusBlocked {
        t.Fatalf("expected blocked order update, got %+v", u)
    }
}

func TestCoreArmSubmitFill(t *testing.T) {
    c, _, _ := newTestCore(t, "sim-1")
    if ack := c.Do(Arm{}); !ack.Accepted { // master
        t.Fatalf("master arm: %+v", ack)
    }
    if ack := c.Do(Arm{Venue: "sim-1"}); !ack.Accepted {
        t.Fatalf("venue arm: %+v", ack)
    }
    ack := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 10, LimitPrice: 100})
    if !ack.Accepted {
        t.Fatalf("armed submit should be accepted, got %+v", ack)
    }
    // SimBroker fills the marketable limit (mark=100, limit=100).
    fu := waitFor(t, c, func(u Update) bool { _, ok := u.(FillUpdate); return ok }).(FillUpdate)
    if fu.Fill.Qty != 10 || fu.Fill.Price != 100 || fu.Fill.OrderID != ack.OrderID {
        t.Fatalf("fill wrong: %+v", fu.Fill)
    }
    // The broker's position snapshot lands as a PositionUpdate.
    pu := waitFor(t, c, func(u Update) bool {
        p, ok := u.(PositionUpdate)
        return ok && p.Position.Symbol == "AAPL"
    }).(PositionUpdate)
    if pu.Position.Qty != 10 {
        t.Fatalf("position qty = %v, want 10", pu.Position.Qty)
    }
}
```

- [ ] **Step 4: Run it — verify it passes**

Run: `cd engine && go test -race ./internal/exec/ -run 'TestCore' -v`
Expected: PASS (both). Then the full exec + broker suites: `cd engine && go test -race ./internal/exec/ ./internal/broker/...`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
cd engine && gofmt -w internal/exec/ && go vet ./internal/exec/
git add internal/exec/update.go internal/exec/core.go internal/exec/core_test.go
git commit -m "feat(engine/exec): Core coordinator — single-writer loop, submit/gate/append/fold, broker intake"
```

---

## Task 12: Order lifecycle + boot recovery — integration coverage

Locks the coordinator behaviors Task 11 implemented but did not yet exercise end-to-end: cancel of a resting order, native replace, the kill switch (cancel-all + master disarm), and **boot recovery** — a fresh `Core` on the same store replaying today's persisted events back into order state. These are in-package (`package exec`) tests, so the recovery test reads `c.state` directly *after* `Recover` and *before* `Run` starts (single goroutine → race-free).

**Files:**
- Create: `engine/internal/exec/core_lifecycle_test.go`

**Interfaces:**
- Consumes: Task 11 `Core` + Task 10 SimBroker + Task 5 store.
- Produces: no new production code — coverage that gates the lifecycle + recovery contract.

- [ ] **Step 1: Write the lifecycle + recovery tests**

Create `engine/internal/exec/core_lifecycle_test.go`:
```go
package exec

import (
    "context"
    "math/rand"
    "path/filepath"
    "testing"
    "time"

    "github.com/earlisreal/eTape/engine/internal/broker/sim"
    "github.com/earlisreal/eTape/engine/internal/clock"
    "github.com/earlisreal/eTape/engine/internal/store"
)

func armBoth(t *testing.T, c *Core, v VenueID) {
    t.Helper()
    if ack := c.Do(Arm{}); !ack.Accepted {
        t.Fatalf("master arm: %+v", ack)
    }
    if ack := c.Do(Arm{Venue: v}); !ack.Accepted {
        t.Fatalf("venue arm: %+v", ack)
    }
}

func TestCoreCancelRestingOrder(t *testing.T) {
    c, sims, _ := newTestCore(t, "sim-1")
    sims["sim-1"].SetMark("AAPL", 100)
    armBoth(t, c, "sim-1")
    // Buy limit 90 with mark 100 → rests.
    ack := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 10, LimitPrice: 90})
    if !ack.Accepted {
        t.Fatalf("submit: %+v", ack)
    }
    // Wait for the accepted (working) order update.
    waitFor(t, c, func(u Update) bool {
        o, ok := u.(OrderUpdate)
        return ok && o.Order.ID == ack.OrderID && o.Order.Status == StatusAccepted
    })
    if cack := c.Do(CancelOrder{Venue: "sim-1", OrderID: ack.OrderID}); !cack.Accepted {
        t.Fatalf("cancel: %+v", cack)
    }
    u := waitFor(t, c, func(u Update) bool {
        o, ok := u.(OrderUpdate)
        return ok && o.Order.ID == ack.OrderID && o.Order.Status == StatusCanceled
    }).(OrderUpdate)
    if u.Order.Working() {
        t.Fatalf("canceled order still working: %+v", u.Order)
    }
}

func TestCoreReplaceRestingOrder(t *testing.T) {
    c, sims, _ := newTestCore(t, "sim-1")
    sims["sim-1"].SetMark("AAPL", 100)
    armBoth(t, c, "sim-1")
    ack := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 10, LimitPrice: 90})
    waitFor(t, c, func(u Update) bool { o, ok := u.(OrderUpdate); return ok && o.Order.Status == StatusAccepted })
    if rack := c.Do(ReplaceOrder{Venue: "sim-1", OrderID: ack.OrderID, Qty: 20, LimitPrice: 91}); !rack.Accepted {
        t.Fatalf("replace: %+v", rack)
    }
    u := waitFor(t, c, func(u Update) bool {
        o, ok := u.(OrderUpdate)
        return ok && o.Order.ID == ack.OrderID && o.Order.Qty == 20
    }).(OrderUpdate)
    if u.Order.LimitPrice != 91 {
        t.Fatalf("replace didn't apply limit: %+v", u.Order)
    }
}

func TestCoreKillSwitchDisarmsAndCancels(t *testing.T) {
    c, sims, _ := newTestCore(t, "sim-1")
    sims["sim-1"].SetMark("AAPL", 100)
    armBoth(t, c, "sim-1")
    a1 := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 1, LimitPrice: 90})
    a2 := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "MSFT", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 1, LimitPrice: 90})
    if !a1.Accepted || !a2.Accepted {
        t.Fatalf("submits: %+v %+v", a1, a2)
    }
    if kack := c.Do(KillSwitch{}); !kack.Accepted {
        t.Fatalf("kill: %+v", kack)
    }
    canceled := map[string]bool{}
    for len(canceled) < 2 {
        u := waitFor(t, c, func(u Update) bool {
            o, ok := u.(OrderUpdate)
            return ok && o.Order.Status == StatusCanceled
        }).(OrderUpdate)
        canceled[u.Order.ID] = true
    }
    if !canceled[a1.OrderID] || !canceled[a2.OrderID] {
        t.Fatalf("kill did not cancel both: %v", canceled)
    }
    // Master is disarmed after kill: a new submit is blocked.
    if ack := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 1, LimitPrice: 90}); ack.Accepted || ack.Reason != "master disarmed" {
        t.Fatalf("post-kill submit should be blocked, got %+v", ack)
    }
}

// A fresh Core on the same store replays today's persisted events into order
// state (crash-recovery). Read c.state directly after Recover, before Run.
func TestCoreBootRecoveryReplaysLog(t *testing.T) {
    clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
    dbPath := filepath.Join(t.TempDir(), "recover.db")
    st, err := store.Open(store.Options{Path: dbPath, Clock: clk})
    if err != nil {
        t.Fatal(err)
    }
    defer func() { _ = st.Close() }()

    mkCore := func() (*Core, *sim.Broker) {
        b := sim.New("sim-1", clk)
        b.SetMark("AAPL", 100)
        cfg := CoreConfig{
            Venues: []VenueID{"sim-1"},
            Gate: GateConfig{
                Global: GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionShares: 1000, MaxSymbolPositionValue: 1_000_000},
                Venue:  map[VenueID]VenueLimits{"sim-1": {MaxOrderValue: 100000, MaxPositionValue: 1_000_000, MaxPositionShares: 1000, MaxOpenOrders: 10}},
            },
            Store: st, Brokers: map[VenueID]Broker{"sim-1": b}, Clock: clk,
            IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(2))),
        }
        return NewCore(cfg), b
    }

    // Core A: arm, submit → fill; wait for the fill, then stop.
    ctxA, cancelA := context.WithCancel(context.Background())
    cA, _ := mkCore()
    if err := cA.Recover(ctxA); err != nil {
        t.Fatal(err)
    }
    go func() { _ = cA.Run(ctxA) }()
    armBoth(t, cA, "sim-1")
    ack := cA.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 10, LimitPrice: 100})
    waitFor(t, cA, func(u Update) bool { f, ok := u.(FillUpdate); return ok && f.Fill.OrderID == ack.OrderID })
    cancelA()
    st.Flush() // ensure all exec appends are durable

    // Core B: fresh state on the same store. Recover replays the log.
    ctxB, cancelB := context.WithCancel(context.Background())
    defer cancelB()
    cB, _ := mkCore()
    if err := cB.Recover(ctxB); err != nil {
        t.Fatal(err)
    }
    o, ok := cB.state.Venue("sim-1").Orders[ack.OrderID]
    if !ok {
        t.Fatalf("recovered state missing order %s", ack.OrderID)
    }
    if o.Status != StatusFilled || o.ExecutedQty != 10 {
        t.Fatalf("recovered order state wrong: %+v", o)
    }
    if len(cB.state.Venue("sim-1").Fills) != 1 {
        t.Fatalf("recovered fills = %d, want 1", len(cB.state.Venue("sim-1").Fills))
    }
    // Boot is always disarmed regardless of the log.
    if cB.state.MasterArmed || cB.state.Venue("sim-1").Armed {
        t.Fatal("recovered state should boot disarmed")
    }
}
```

- [ ] **Step 2: Run it — verify it passes**

Run: `cd engine && go test -race ./internal/exec/ -run 'TestCoreCancel|TestCoreReplace|TestCoreKill|TestCoreBoot' -v`
Expected: PASS (all four).

- [ ] **Step 3: Commit**

```bash
cd engine && gofmt -w internal/exec/ && go vet ./internal/exec/
git add internal/exec/core_lifecycle_test.go
git commit -m "test(engine/exec): lifecycle (cancel/replace/kill) + boot-recovery log replay"
```

---

## Task 13: Capstone — multi-venue E2E, aggregate gate, day-loss auto-disarm, `replay(log) == state`

The deliverable verification. Two SimBroker venues under one `Core`: cross-venue aggregate gate math (a submit within per-venue caps but over the global symbol cap is blocked), master-vs-venue arming, cross-venue day-loss auto-disarm, and the plan's headline invariant — reading the persisted `exec_events` back through the store, decoding, and folding reproduces the live Core's order + fill state byte-for-byte. Duplicate-ID-across-venues is covered at the unit level (Task 8, `TestGateDuplicateID`); live IDs are unique ULIDs by construction.

**Files:**
- Create: `engine/internal/exec/capstone_test.go`

**Interfaces:**
- Consumes: everything. No new production code.

- [ ] **Step 1: Write the capstone tests**

Create `engine/internal/exec/capstone_test.go`:
```go
package exec

import (
    "context"
    "math/rand"
    "path/filepath"
    "reflect"
    "testing"
    "time"

    "github.com/earlisreal/eTape/engine/internal/broker/sim"
    "github.com/earlisreal/eTape/engine/internal/clock"
    "github.com/earlisreal/eTape/engine/internal/store"
)

// buildMultiCore wires a Core over two SimBroker venues on a caller-owned store
// so the test can read exec_events back.
func buildMultiCore(t *testing.T, st *store.Store, clk clock.Clock, global GlobalLimits, per VenueLimits) (*Core, map[VenueID]*sim.Broker) {
    t.Helper()
    venues := []VenueID{"sim-1", "sim-2"}
    brokers := map[VenueID]Broker{}
    sims := map[VenueID]*sim.Broker{}
    for _, v := range venues {
        b := sim.New(v, clk)
        b.SetMark("AAPL", 100)
        brokers[v] = b
        sims[v] = b
    }
    cfg := CoreConfig{
        Venues: venues,
        Gate:   GateConfig{Global: global, Venue: map[VenueID]VenueLimits{"sim-1": per, "sim-2": per}},
        Store:  st, Brokers: brokers, Clock: clk,
        IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(3))),
    }
    return NewCore(cfg), sims
}

func TestCapstoneAggregateGateBlocksCrossVenue(t *testing.T) {
    clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
    st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "agg.db"), Clock: clk})
    if err != nil {
        t.Fatal(err)
    }
    defer func() { _ = st.Close() }()
    // Per-venue shares cap 200 (generous), global symbol cap 250.
    c, _ := buildMultiCore(t, st,
        GlobalLimits{MaxDayLoss: 100000, MaxSymbolPositionShares: 250, MaxSymbolPositionValue: 1_000_000},
        VenueLimits{MaxOrderValue: 1_000_000, MaxPositionValue: 1_000_000, MaxPositionShares: 200, MaxOpenOrders: 10})
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    if err := c.Recover(ctx); err != nil {
        t.Fatal(err)
    }
    go func() { _ = c.Run(ctx) }()
    if ack := c.Do(Arm{}); !ack.Accepted {
        t.Fatal(ack.Reason)
    }
    c.Do(Arm{Venue: "sim-1"})
    c.Do(Arm{Venue: "sim-2"})
    // Seed cross-venue positions: 150 on sim-1, 80 on sim-2 (net 230).
    c.state.ReconcilePositions("sim-1", []Position{{Venue: "sim-1", Symbol: "AAPL", Qty: 150}})
    // ^ direct seed is racy vs Run; instead push via the broker reconcile path:
    // (replace the two direct ReconcilePositions lines with SetAccount/SetMark-free
    // BrokerPositions injection below in the real implementation).
    _ = c
}
```

**Note for the implementer:** the snippet above shows the *shape* but `c.state.ReconcilePositions` must NOT be called while `Run` is active (data race). Drive positions through the broker instead. Use this corrected body for `TestCapstoneAggregateGateBlocksCrossVenue`:
```go
func TestCapstoneAggregateGateBlocksCrossVenue(t *testing.T) {
    clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
    st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "agg.db"), Clock: clk})
    if err != nil {
        t.Fatal(err)
    }
    defer func() { _ = st.Close() }()
    c, sims := buildMultiCore(t, st,
        GlobalLimits{MaxDayLoss: 100000, MaxSymbolPositionShares: 250, MaxSymbolPositionValue: 1_000_000},
        VenueLimits{MaxOrderValue: 1_000_000, MaxPositionValue: 1_000_000, MaxPositionShares: 200, MaxOpenOrders: 10})
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    if err := c.Recover(ctx); err != nil {
        t.Fatal(err)
    }
    go func() { _ = c.Run(ctx) }()
    c.Do(Arm{})
    c.Do(Arm{Venue: "sim-1"})
    c.Do(Arm{Venue: "sim-2"})
    // Buy-fill 150 on sim-1 and 80 on sim-2 (marks=100, limits=100 → marketable).
    a1 := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 150, LimitPrice: 100})
    a2 := c.Do(SubmitOrder{Venue: "sim-2", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 80, LimitPrice: 100})
    if !a1.Accepted || !a2.Accepted {
        t.Fatalf("seed submits: %+v %+v", a1, a2)
    }
    // Wait until both venues report their positions (net 230).
    got := map[VenueID]float64{}
    for got["sim-1"] != 150 || got["sim-2"] != 80 {
        pu := waitFor(t, c, func(u Update) bool { p, ok := u.(PositionUpdate); return ok && p.Position.Symbol == "AAPL" }).(PositionUpdate)
        got[pu.Position.Venue] = pu.Position.Qty
    }
    _ = sims
    // A further 40 on sim-1: per-venue result 150+40=190<=200 (ok), but global
    // 230+40=270 > 250 → blocked by the global layer.
    ack := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 40, LimitPrice: 100})
    if ack.Accepted || ack.Reason != "resulting symbol position exceeds global share cap" {
        t.Fatalf("cross-venue global cap should block, got %+v", ack)
    }
}

func TestCapstoneMasterVsVenueArming(t *testing.T) {
    clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
    st, _ := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "arm.db"), Clock: clk})
    defer func() { _ = st.Close() }()
    c, _ := buildMultiCore(t, st,
        GlobalLimits{MaxDayLoss: 100000, MaxSymbolPositionShares: 100000, MaxSymbolPositionValue: 1e12},
        VenueLimits{MaxOrderValue: 1e12, MaxPositionValue: 1e12, MaxPositionShares: 100000, MaxOpenOrders: 100})
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    _ = c.Recover(ctx)
    go func() { _ = c.Run(ctx) }()
    c.Do(Arm{})              // master on
    c.Do(Arm{Venue: "sim-1"}) // sim-1 on, sim-2 off
    if ack := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 1, LimitPrice: 100}); !ack.Accepted {
        t.Fatalf("sim-1 should accept: %+v", ack)
    }
    if ack := c.Do(SubmitOrder{Venue: "sim-2", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 1, LimitPrice: 100}); ack.Accepted || ack.Reason != "venue disarmed" {
        t.Fatalf("sim-2 disarmed should block: %+v", ack)
    }
}

func TestCapstoneDayLossAutoDisarm(t *testing.T) {
    clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
    st, _ := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "dl.db"), Clock: clk})
    defer func() { _ = st.Close() }()
    c, sims := buildMultiCore(t, st,
        GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionShares: 100000, MaxSymbolPositionValue: 1e12},
        VenueLimits{MaxOrderValue: 1e12, MaxPositionValue: 1e12, MaxPositionShares: 100000, MaxOpenOrders: 100})
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    _ = c.Recover(ctx)
    go func() { _ = c.Run(ctx) }()
    c.Do(Arm{})
    c.Do(Arm{Venue: "sim-1"})
    c.Do(Arm{Venue: "sim-2"})
    // Push day P&Ls summing past -1000 → auto-disarm.
    sims["sim-1"].SetAccount(AccountSnapshot{Venue: "sim-1", DayPnL: -600})
    sims["sim-2"].SetAccount(AccountSnapshot{Venue: "sim-2", DayPnL: -500})
    waitFor(t, c, func(u Update) bool { s, ok := u.(StatusUpdate); return ok && !s.MasterArmed })
    if ack := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 1, LimitPrice: 100}); ack.Accepted || ack.Reason != "master disarmed" {
        t.Fatalf("after day-loss breach submit should block, got %+v", ack)
    }
}

// The headline invariant: the persisted log, read back and folded, equals the
// live Core's order + fill state (account/positions are broker-reconciled and
// excluded — they are not in the log).
func TestCapstoneReplayLogEqualsState(t *testing.T) {
    clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
    st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "replay.db"), Clock: clk})
    if err != nil {
        t.Fatal(err)
    }
    defer func() { _ = st.Close() }()
    venues := []VenueID{"sim-1", "sim-2"}
    c, sims := buildMultiCore(t, st,
        GlobalLimits{MaxDayLoss: 1e9, MaxSymbolPositionShares: 1e9, MaxSymbolPositionValue: 1e12},
        VenueLimits{MaxOrderValue: 1e12, MaxPositionValue: 1e12, MaxPositionShares: 1e9, MaxOpenOrders: 100})
    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan struct{})
    _ = c.Recover(ctx)
    go func() { _ = c.Run(ctx); close(done) }()
    c.Do(Arm{})
    c.Do(Arm{Venue: "sim-1"})
    c.Do(Arm{Venue: "sim-2"})

    // Drive a mixed session: two fills on sim-1, a rest+cancel on sim-2, one fill on sim-2.
    f1 := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 10, LimitPrice: 100})
    waitFor(t, c, func(u Update) bool { f, ok := u.(FillUpdate); return ok && f.Fill.OrderID == f1.OrderID })
    f2 := c.Do(SubmitOrder{Venue: "sim-1", Symbol: "MSFT", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 5, LimitPrice: 100})
    waitFor(t, c, func(u Update) bool { f, ok := u.(FillUpdate); return ok && f.Fill.OrderID == f2.OrderID })
    sims["sim-2"].SetMark("AAPL", 100)
    r1 := c.Do(SubmitOrder{Venue: "sim-2", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 7, LimitPrice: 90}) // rests
    waitFor(t, c, func(u Update) bool { o, ok := u.(OrderUpdate); return ok && o.Order.ID == r1.OrderID && o.Order.Status == StatusAccepted })
    c.Do(CancelOrder{Venue: "sim-2", OrderID: r1.OrderID})
    waitFor(t, c, func(u Update) bool { o, ok := u.(OrderUpdate); return ok && o.Order.ID == r1.OrderID && o.Order.Status == StatusCanceled })
    f3 := c.Do(SubmitOrder{Venue: "sim-2", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 3, LimitPrice: 100})
    waitFor(t, c, func(u Update) bool { f, ok := u.(FillUpdate); return ok && f.Fill.OrderID == f3.OrderID })

    // Quiesce: stop Run so reading c.state is race-free, then flush the store.
    cancel()
    <-done
    st.Flush()

    // Read the persisted log back and fold it.
    envs, err := st.ReadExecEventsSince(0)
    if err != nil {
        t.Fatal(err)
    }
    replayed := NewState(venues)
    for _, env := range envs {
        ev, err := DecodeEvent(env.Kind, env.Payload)
        if err != nil {
            t.Fatalf("decode %s: %v", env.Kind, err)
        }
        replayed.Apply(ev)
    }

    // Compare the log-derived view (orders + fills + index) — NOT positions/account.
    if !reflect.DeepEqual(replayed.orderIndex, c.state.orderIndex) {
        t.Fatalf("order index differs:\n replay=%v\n live=%v", replayed.orderIndex, c.state.orderIndex)
    }
    for _, v := range venues {
        if !reflect.DeepEqual(replayed.Venue(v).Orders, c.state.Venue(v).Orders) {
            t.Fatalf("venue %s orders differ:\n replay=%#v\n live=%#v", v, replayed.Venue(v).Orders, c.state.Venue(v).Orders)
        }
        if !reflect.DeepEqual(replayed.Venue(v).Fills, c.state.Venue(v).Fills) {
            t.Fatalf("venue %s fills differ:\n replay=%#v\n live=%#v", v, replayed.Venue(v).Fills, c.state.Venue(v).Fills)
        }
    }
}
```

**Note for the implementer:** delete the first (illustrative, racy) `TestCapstoneAggregateGateBlocksCrossVenue` stub — keep only the corrected version. The stub is included above solely to make the race hazard explicit; the file must compile with exactly one function of that name.

- [ ] **Step 2: Run the capstone**

Run: `cd engine && go test -race ./internal/exec/ -run TestCapstone -v`
Expected: PASS (all four).

- [ ] **Step 3: Full-suite regression + lint**

Run:
```bash
cd engine && go build ./... && go vet ./... && go test -race ./... && golangci-lint run
```
Expected: all pass. This is the plan's deliverable gate — the exec subsystem accepts commands, gates them, folds events, persists, and is deterministic under `replay(log) == state`, all race-clean.

- [ ] **Step 4: Commit**

```bash
cd engine && gofmt -w internal/exec/
git add internal/exec/capstone_test.go
git commit -m "test(engine/exec): capstone — multi-venue gate/arming/day-loss + replay(log)==state across store"
```

---

## Self-review (author's checklist — completed)

**Spec coverage** (multi-broker-execution-design + portfolio-orders-design):
- Venue model (`(broker, account, env)`, slug tags every event/topic/command) → Tasks 1, 9. ✅
- Domain gains `Venue` on Order/Fill/Position/AccountSnapshot; `OrderRequest` requires venue → Task 1. ✅
- `"ET"`+ULID IDs, unique across venues/restarts → Task 2. ✅
- Append-only event log + envelope (seq/ts/source/venue) → Tasks 3, 5. ✅
- Append-before-POST + append-blocks-submit → Task 5 (sync append), Task 11 (submit path). ✅
- One fold, venue-keyed state + cross-venue aggregates, `replay(log)==state` → Tasks 6, 7, 13. ✅
- Two-layer gate (master→venue→dup→per-venue→global) + day-loss auto-disarm → Tasks 8, 11, 13. ✅
- `Broker` interface + `Capabilities` (NativeReplace/FlattenAll/OvernightSession) → Task 4. ✅
- `OrderReplaced` event; native replace (SimBroker) / TZ emulation deferred to Plan 5 → Tasks 3, 10. ✅
- KillSwitch = cancel-all all venues + master disarm; flatten not in kill path; venue-scoped kill → Task 11. ✅
- `exec_events`/`fills` with `venue` column; fills projection; chart-backfill query → Task 5. ✅
- SimBroker implements `Capabilities` like any adapter → Task 10. ✅
- Commands (Submit/Cancel/Replace/Flatten/Kill/Arm/Disarm) with sync accepted|blocked ack → Task 11. ✅
- Boot disarmed always; boot replay + REST/snapshot reconcile → Tasks 7, 11, 12. ✅
- Testing: table-driven fold+gate, one test per gate rule, multi-venue scenarios, `replay(log)==state`, `go test -race` → Tasks 6–8, 13. ✅

**Deferred to Plan 5/6 (flagged, not gaps):** real TZ/Alpaca adapters, moomoo v1.x, adapter reconnect/staleness/reconcile-synthesis, transport-error retry-once probe, native flatten primitive, `cmd/etape` boot wiring + `md.Marks()→FeedMark` bridge + WS topic mapping, TZ replace emulation.

**Placeholder scan:** none — every step carries real code/commands. The one illustrative racy stub in Task 13 is explicitly called out for deletion.

**Type consistency:** `VenueID`, `Side`/`OrderType`/`TIF`/`OrderStatus`, `Order`/`Fill`/`Position`/`AccountSnapshot`, `OrderRequest`/`ReplaceRequest`/`OrderAck`, the `Event`/`BrokerEvent`/`Update`/`Command` unions, `EventEnvelope`/`FillRow`, `GateConfig`/`GlobalLimits`/`VenueLimits`, `CoreConfig`/`CmdAck`, and the `EventStore`/`Broker`/`MarkSource` interfaces are used with identical names and signatures across Tasks 1–13 and match the consumed Plan 1–3 signatures verbatim.
