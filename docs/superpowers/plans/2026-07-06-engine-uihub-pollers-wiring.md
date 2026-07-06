# eTape Engine — Plan 6 of 6: uihub, Pollers & Main Wiring

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the engine's finished subsystems (Plans 1–5) into the complete `etape` binary the UI talks to — a `uihub` WebSocket server that streams every topic as snapshot-then-delta with per-class coalescing and correlation-ID commands, a `wsmsg` contract package that `tygo` compiles into `ui/src/gen` (drift fails the build), the `scan`/`news`/`health` pollers, and a `cmd/etape` full boot sequence (config → store → uihub → OpenD → pre-subscribe → exec) with graceful, ordered shutdown — proven end-to-end by a replay+SimBroker capstone that a Playwright client can drive.

**Architecture:** `uihub` is a new edge package that sits between the browser and the two already-built single-writer cores (`md.Core`, `exec.Core`). It never mutates domain state: it consumes `md.Core.Updates()` and `exec.Core.Updates()` into a **keep-latest state mirror** (per topic, per key), coalesces per topic class, and fans out to per-connection writer goroutines; inbound `command`/`query` frames are dispatched to `exec.Core.Do(...)` and the `store` accessors. A **new topic subscription is answered from the mirror** (full snapshot) then followed by deltas — so no synchronous "current state" query is ever added to the merged cores. The `wsmsg` sub-package holds pure DTO structs (stdlib only, explicit `json:` tags) and is the single `tygo` source of truth; the domain→wire mappers live in `uihub` itself. Pollers are independent goroutines that issue request/response protoIDs through the existing `opend.Client.Request` (zero subscription quota) and publish results into the hub. `cmd/etape/main.go` is **replaced** (not extended): it wires the whole graph, bridges `md.Core.Marks()` → `exec.Core.FeedMark`, and in `--replay` mode swaps the live feed/clock for `replay.Feed`/`replay.Clock` + a `sim` broker so the identical hub/cores run against a recorded day.

**Tech Stack:** Go 1.26.4 (module from Plans 1–5); `github.com/coder/websocket` (WS server — already added by Plan 5 for the Alpaca client, reused here server-side); stdlib `net/http` (static `ui/dist` + `/ws` upgrade) + `encoding/json`; the committed protobuf bindings under `internal/feed/opend/pb/` (rank 3410 / filter 3215 / snapshot 3203 / news 3263 — all already generated, no proto work); `github.com/gzuidhof/tygo` (Go→TS generation, dev tool, not a runtime import); `go test -race` + `golangci-lint`.

## Global Constraints

Copied verbatim (or tightly paraphrased) from the approved specs and the earlier plans. Every task's requirements implicitly include this section.

- **Module path:** `github.com/earlisreal/eTape/engine`. All imports are `github.com/earlisreal/eTape/engine/...`. Go 1.26.4.
- **Branch dependency:** builds on **Plan 5** (`internal/broker/tradezero`, `internal/broker/alpaca`, `internal/broker/netx`, `internal/creds`, and the `exec.Broker.Flatten(ctx)` method + `exec.TypeStop`/`exec.TypeStopLimit` order types). **Create the Plan 6 worktree branch from `main` only after Plan 5 has merged** — the broker factory (Task 14) imports the adapter packages, so the module will not compile without them. If Plan 5 has not merged when execution begins, stop and surface it; do not stub the adapters.
- **Dependency rule:** `uihub` (and its `wsmsg` sub-package's *mappers*, which live in `uihub`) is an **edge package** — it MAY import `md`, `exec`, `store`, `feed`, `session`, `clock`, `config`, and `wsmsg`. Domain packages (`feed`, `md`, `exec`, `session`) still NEVER import `uihub`, `store`, or adapters. `wsmsg` itself imports **stdlib only** (so `tygo` sees a dependency-free package). Pollers (`scan`, `news`, `health`) import `feed/opend` + the `pb/*` bindings + `wsmsg` + `store` + `clock` + `session` + `config`, and publish through a small `Publisher` interface — they never import `uihub` directly. (go-engine-design §Dependency rule)
- **Single-writer cores are not modified.** `md.Core` and `exec.Core` are consumed exactly as merged: their `Updates()`/`Marks()` output channels and (for exec) `Do(cmd) CmdAck` + `FeedMark(m)` inputs. uihub owns **all** coalescing and snapshot assembly (the cores emit typed facts and `drop-and-count` on overflow; "uihub owns coalescing" — Plan 2/4 source comments). No new methods are added to the cores.
- **Trade-incapability rule:** the feed/OpenD connection and the pollers implement **no `Trd_*` protocols**. Order writes live only in the `broker/*` adapters (their own connections). uihub routes UI order commands to `exec.Core.Do(...)`, which routes to the configured adapter — uihub never speaks a broker wire protocol. (CLAUDE.md; multi-broker-execution-design)
- **Safety rule (standing, hard — CLAUDE.md):** never place/modify/cancel **real** orders. The capstone and all uihub/poller tests run against `broker/sim` + `replay` + `httptest`/in-process fakes only. No test opens a socket to a real broker or to live OpenD. Any live-venue wiring in `main` is exercised only when Earl runs the binary himself against a real config — the plan's automated deliverable is the replay+sim capstone.
- **Repo is PUBLIC; sensitive-sweep every commit.** No account identifiers, credentials, or `loginUserID`-bearing frames in checked-in fixtures/testdata. Credentials stay in `~/.eJournal/credentials.json` (loaded by `internal/creds`, never logged, never committed).
- **Timestamps on the wire (from the UI contract, verified):** market-data topics (`md.quote`/`md.book`/`md.tape`/`md.bars`), `sys.events`, and the poller payloads (`scanner.*.refreshedAt`/`at`, `news.item.seen_at`) use **ISO-8601 UTC strings**; execution topics (`exec.orders`/`fills`/`account`) use **epoch-milliseconds numbers** (`tsMs`, `createdMs`, `updatedMs`). Mappers honor this split exactly.
- **JSON field names are camelCase and load-bearing:** every `wsmsg` struct carries explicit `json:"..."` tags matching the hand-authored `ui/src/wire/contract.ts` field-for-field (`id`, `limitPrice`, `avgFillPrice`, `replacesId`, `createdMs`, `bucketStart`, `changePct`, `floatShares`, `seen_at`, …). tygo is configured to honor these tags so `ui/src/gen/*` is a field-compatible drop-in for the interim contract (the discriminant literal types `kind`/`status`/`link` get special handling — see Task 4).
- **Persisted timestamps** are `INTEGER` epoch ms (Plan 3/4 convention), set from `clock.Clock`; poller "seen_at"/RTT stamps come from `clock.Clock`, never `time.Now` directly. (determinism / replay seam)
- **Boot independence (go-engine-design §Boot sequence):** each stage retries with backoff independently; **a dead OpenD never blocks the kill switch.** `md` and `exec` are independent subsystems that meet only at `uihub` and at the `md.Core.Marks()` → `exec.Core.FeedMark` bridge. uihub must be listening **before** OpenD is dialed (UI shows "connecting" states rather than failing to connect at all).
- **CI gates (every task ends green):** `cd engine && go build ./... && go vet ./... && go test -race ./... && golangci-lint run`. The `gen-ts` drift check (Task 4) is added to CI and must also be green from Task 4 onward.

---

## Plan sequence context (6 engine plans)

1. Foundation & OpenD Protocol Client — **done** (`feed/opend`, `clock`, `config`, generated `pb/`).
2. Market-Data Core — **done** (`feed`, `session`, `md`).
3. Store, Journal & Replay — **done** (`store`, `replay`).
4. Execution Core (multi-venue) — **done** (`exec` domain + fold + two-layer gate + `Broker` interface + `SimBroker` + `Core`).
5. Broker Adapters — **must be merged before this plan** (`broker/tradezero`, `broker/alpaca`, `broker/netx`, `creds`; adds `exec.Broker.Flatten` + `exec.TypeStop`/`TypeStopLimit`).
6. **uihub, Pollers & Main Wiring (this plan)** — the `uihub` WS server, `wsmsg`+tygo, `scan`/`news`/`health` pollers, and `cmd/etape` full boot.

**Deliverable:** the complete `etape` binary. Run live, it serves the UI, streams market data + execution state, and routes orders to configured venues. Run `etape --replay <day> --speed N`, it reconstructs a recorded session against `SimBroker` and serves the identical WS contract — the mode the UI's Plan 6 Playwright E2E boots on. Automated proof: the Task 16 capstone drives the booted engine over a real WebSocket and asserts snapshot-then-delta on every topic plus a full order lifecycle (`SubmitOrder` → gate ack → `exec.orders` `SUBMITTED`→`FILLED`) and a `QueryFills` round-trip.

## Authoritative references (read before implementing)

- `docs/superpowers/specs/2026-07-03-go-engine-design.md` — §uihub (connection model, topics, coalescing table, backpressure), §Pollers (`scan`/`news`/`health` mechanics), §Boot sequence, §Testing (contract tests + tygo-in-CI).
- `docs/superpowers/specs/2026-07-03-ui-design.md` — topic/command catalog, reject-reason-verbatim rule, reconnect = re-snapshot.
- `docs/superpowers/specs/2026-07-04-multi-broker-execution-design.md` — venue model → Core construction, uihub↔exec command/event path, per-venue coalescing rates.
- `ui/src/wire/contract.ts` (the `ui-execution-surfaces` worktree copy is newest) — the field-for-field JSON target for `wsmsg`. `ui/src/wire/codec.ts`, `ui/src/wire/WsClient.ts`, `ui/src/data/registry.ts` — the client's kind/topic allow-sets, subscribe refcounting, reconnect buffering, and per-store snapshot/delta semantics.
- `docs/2026-07-03-premarket-scanner-api.md` — rank 3410 / filter 3215 / snapshot 3203 request+response fields, rate limits, the FLOAT_SHARE = thousands unit caveat, the batch-fails-on-bad-code trap.
- `docs/2026-07-03-news-aggregation-options.md` — `Qot_GetSearchNews` 3263 shape, cadence, dedup-by-URL, coarse `publish_time` → stamp `seen_at` locally.

## Design decisions (locked; rationale for the non-obvious ones)

These resolve the gaps the specs left open. Each is repeated at the task where it is implemented.

1. **Snapshots come from a uihub-owned state mirror, not from new core queries.** uihub consumes `md.Core.Updates()` and `exec.Core.Updates()` from boot into a keep-latest cache keyed by `(topic, key)`. A new subscription is answered by serializing the current mirror entry (or entries) as a `snapshot`, after which the client receives `delta`s. *Why not add a synchronous query to the cores (the spec's "reads enter the single-writer loop" hint):* that means re-opening merged Plan 2/4 code, and uihub must maintain keep-latest state anyway to coalesce and to assemble cross-cutting payloads. The mirror is that state; snapshots are free.
2. **Two cross-cutting joins live in the mirror.** (a) The wire `Quote{bid,ask,last}` — `md`'s `QuoteUpdate` carries `feed.Quote` which has **no** bid/ask (those are on `Book`); the mirror fills `bid`/`ask` from the latest cached top-of-book for the symbol (0 until a book arrives). (b) The wire `ExecStatus{masterArmed, global, venues[]}` aggregate — `exec.Core` only emits per-venue `StatusUpdate`/`AccountUpdate`; the mirror accumulates per-venue `VenueStatus` (merging connected/note from `StatusUpdate`, `venueArmed` from `AccountUpdate`) plus the static per-venue `GateLimitsView` + global limits from config, and republishes the whole `ExecStatus` on any change.
3. **wsmsg is a pure DTO package (stdlib only); mappers live in `uihub`.** Keeps the tygo source dependency-free and keeps domain→wire conversion (enum-int→string, ms→ISO) next to the hub that uses it.
4. **Enum + timestamp mapping is explicit.** `exec` uint8 enums (`Side`/`OrderType`/`TIF`/`OrderStatus`) map to the wire's string literals; `md`/feed int64 ms → RFC3339-milli UTC strings on md topics; exec ms passes through as numbers. The UI never sees the display-only `PendingNew`/`Replacing` states — the engine emits only the 9 real `OrderStatus` values.
5. **Coalescing is per topic class, all in uihub, rates from `[uihub]` config** (spec defaults, "tune after Monday"): `md.quote`/`md.book`/`md.bars` keep-latest-per-key @ 30 Hz; `md.tape` batched-append @ 30 Hz; `md.indicator` **event-driven** (low rate — full-series updates sent as `snapshot` frames, single points as `delta` frames; not rate-coalesced, so no points are dropped); `exec.account` @ 4 Hz; `exec.positions` batched @ 100 ms; `exec.orders`/`exec.fills`/`exec.status` event-driven (no rate cap); poller topics event-driven. A pathologically full per-connection queue is dropped and the client is forced to re-snapshot (never back-pressures the engine).
6. **Commands ride one generic `command`/`ack` path; `QueryFills` rides `query`/`result`.** uihub's per-connection reader maps `CommandMsg.name`+`args` → an `exec.Command` variant (or a config/indicator/focus handler) and returns `AckMsg{status, reason, orderId, value}`. `QueryFills` → `store.QueryFills` → `Fill[]` in a `ResultMsg`; any unknown query still replies `{payload: []}` so the UI promise never hangs.
7. **News dedup = by URL** (falling back to `symbol|headline`) — resolving the go-engine-design ("story ID") vs news-aggregation-options ("URL/title") disagreement in favor of URL, because `Qot_GetSearchNews` exposes no stable ID. Ordering is by engine-stamped `seen_at`, not the coarse `publish_time`.
8. **`sys.health` links are feed-scoped in v1:** `ui-engine` (app ping/pong RTT) + `engine-moomoo` (OpenD probe RTT). `engine-tz`/`engine-alpaca` per-venue connectivity is surfaced via `exec.status.venues[].connected`, not duplicated into `sys.health` — the `HealthLink.link` union already contains `engine-tz`, so the mirror emits it only if a TZ venue is configured (else omitted), and does not add new link kinds. (multi-broker design never specifies `sys.health` growth; this keeps it minimal.)
9. **`--replay` uses a `sim` broker for every configured venue.** In replay mode the broker factory ignores `cfg.Venue.Broker` and substitutes `broker/sim` (a recorded day has no live broker), so the full command path (arm → gate → submit → fill) is exercised deterministically. Live mode uses the real factory.

---

## File Structure

```
engine/
  go.mod                                          MODIFY  Task 4 (tools) — coder/websocket already present via Plan 5
  Makefile                                        MODIFY  Task 4  + gen-ts target
  tygo.yaml                                       CREATE  Task 4  tygo config (wsmsg → ui/src/gen)
  .github/… or CI script                          MODIFY  Task 4  gen-ts drift check (see task for exact location)
  internal/
    config/
      config.go        config_test.go             MODIFY  Task 1  + [uihub] [scan] [news] [health] sections
    uihub/
      wsmsg/
        wsmsg.go                                   CREATE  Task 2  envelope + client/server msgs + Topic consts + enums
        payloads.go                                CREATE  Task 2  Quote/Book/Tick/Bar/exec/scanner/news/health DTOs
        wsmsg_test.go                              CREATE  Task 2  JSON field-name round-trip vs contract.ts
      map.go           map_test.go                 CREATE  Task 3  md/exec/feed → wsmsg mappers (enum+ts mapping)
      mirror.go        mirror_test.go              CREATE  Task 5  keep-latest state cache + snapshot builder
      coalesce.go      coalesce_test.go            CREATE  Task 6  per-topic-class coalescer (fake-clock driven)
      conn.go          conn_test.go                CREATE  Task 7  per-connection reader/writer, sub set, slow-client drop, ping
      commands.go      commands_test.go            CREATE  Task 8  command dispatch (name/args → exec.Command/config/indicator/focus)
      query.go         query_test.go              CREATE  Task 9  query dispatch (QueryFills)
      hub.go                                       CREATE  Task 5/7 Hub: connection registry, Publish, broadcast (built across 5→10)
      server.go        server_test.go              CREATE  Task 10 http.Server: static ui/dist + /ws upgrade; wires everything
    scan/
      scan.go          scan_test.go                CREATE  Task 11 pre-market/RTH rank + float-universe warm-up + snapshot fallback
    news/
      news.go          news_test.go                CREATE  Task 12 Qot_GetSearchNews poll + normalize + URL dedup
    health/
      health.go        health_test.go             CREATE  Task 13 moomoo probe RTT + app ping RTT + sys.health/sys.events
    uihubtest/
      e2e_test.go                                  CREATE  Task 16 capstone: booted engine (replay+sim) driven over a real WS
  cmd/etape/
    main.go                                        MODIFY  Task 15 REPLACE with full boot sequence
    boot.go            boot_test.go                CREATE  Task 14/15 broker factory + gate/venue config mapping + wiring helpers
ui/
  src/gen/wsmsg.ts                                 CREATE  Task 4  tygo output (committed; UI Plan 6 swaps contract.ts → this)
```

`internal/uihubtest` is a new leaf test package (imports `uihub`, `store`, `exec`, `broker/sim`, `replay`, `config`) — it exists so the capstone can import the hub plus the store/broker without any import cycle inside `uihub` itself.

---

## Task 1: Config sections for uihub + pollers

**Files:**
- Modify: `engine/internal/config/config.go`
- Test: `engine/internal/config/config_test.go`

**Interfaces:**
- Consumes: the existing `config.Config`/`config.Default()`/`config.Load(path)` and the `net.JoinHostPort`+`strconv` idiom already used by `OpenD.Addr()`.
- Produces: `config.UIHub` (+ `Addr()`), `config.Scan`, `config.News`, `config.Health`, added as fields `UIHub`/`Scan`/`News`/`Health` on `Config`, with defaults set in `Default()`. Later tasks read these.

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/config/config_test.go`:

```go
func TestDefaultHasUIHubAndPollerSections(t *testing.T) {
	c := config.Default()
	if got := c.UIHub.Addr(); got != "127.0.0.1:8686" {
		t.Fatalf("UIHub.Addr() = %q, want 127.0.0.1:8686", got)
	}
	if c.UIHub.OutboundQueue != 1024 {
		t.Fatalf("UIHub.OutboundQueue = %d, want 1024", c.UIHub.OutboundQueue)
	}
	if c.UIHub.MDRateHz != 30 || c.UIHub.AccountRateHz != 4 || c.UIHub.PositionMs != 100 {
		t.Fatalf("UIHub rates = %v/%v/%v, want 30/4/100", c.UIHub.MDRateHz, c.UIHub.AccountRateHz, c.UIHub.PositionMs)
	}
	if !c.Scan.Enabled || c.Scan.PremarketMs != 2000 || c.Scan.MaxFloatShares != 50_000_000 {
		t.Fatalf("Scan defaults wrong: %+v", c.Scan)
	}
	if !c.News.Enabled || c.News.FocusedMs != 20000 {
		t.Fatalf("News defaults wrong: %+v", c.News)
	}
	if !c.Health.Enabled || c.Health.ProbeMs != 5000 {
		t.Fatalf("Health defaults wrong: %+v", c.Health)
	}
}

func TestLoadOverridesUIHubSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := "[uihub]\nport = 9000\nmd_rate_hz = 15.0\n\n[scan]\nmin_change_pct = 8.0\n"
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.UIHub.Port != 9000 || c.UIHub.MDRateHz != 15 {
		t.Fatalf("override failed: port=%d rate=%v", c.UIHub.Port, c.UIHub.MDRateHz)
	}
	// Unset fields in a present section still fall back to Default() (Load merges onto Default()).
	if c.UIHub.OutboundQueue != 1024 {
		t.Fatalf("OutboundQueue lost its default: %d", c.UIHub.OutboundQueue)
	}
	if c.Scan.MinChangePct != 8 {
		t.Fatalf("scan override failed: %v", c.Scan.MinChangePct)
	}
}
```

> Note: `TestLoadOverridesUIHubSection` relies on `Load` unmarshaling the TOML **onto a `Default()` value** (unset keys keep their defaults). Verified (2026-07-06 pass): the merged `config.Load` does exactly `cfg := Default(); toml.DecodeFile(path, &cfg)` — so this assertion holds as written.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/config/ -run 'UIHub|PollerSections' -v`
Expected: FAIL — `c.UIHub undefined` (compile error).

- [ ] **Step 3: Add the structs and defaults**

In `engine/internal/config/config.go`, add these types (near the other section structs):

```go
// UIHub is the [uihub] section: the WS/HTTP server the UI connects to.
type UIHub struct {
	Host          string  `toml:"host"`
	Port          int     `toml:"port"`
	DistDir       string  `toml:"dist_dir"`        // path to built ui/dist; empty => no static file serving (dev proxies /ws)
	OutboundQueue int     `toml:"outbound_queue"`  // per-connection outbound buffer depth; overflow => drop + force re-snapshot
	MDRateHz      float64 `toml:"md_rate_hz"`      // flush rate for md.quote/book/bars/tape/indicator
	AccountRateHz float64 `toml:"account_rate_hz"` // flush rate for exec.account
	PositionMs    int     `toml:"position_ms"`     // batch interval for exec.positions
	TapeSnapshot  int     `toml:"tape_snapshot"`   // recent ticks retained per symbol for the tape snapshot
}

func (u UIHub) Addr() string { return net.JoinHostPort(u.Host, strconv.Itoa(u.Port)) }

// Scan is the [scan] section: pre-market/RTH rank scanner + low-float universe.
type Scan struct {
	Enabled          bool    `toml:"enabled"`
	PremarketMs      int     `toml:"premarket_ms"`       // rank poll interval before 09:30 ET
	RTHMs            int     `toml:"rth_ms"`             // rank poll interval during RTH
	RankPages        int     `toml:"rank_pages"`         // pages of <=35 to pull per rank refresh
	MinChangePct     float64 `toml:"min_change_pct"`     // client-side gainer threshold (%)
	MaxFloatShares   float64 `toml:"max_float_shares"`   // float cap in ACTUAL shares (not thousands)
	MinVolume        int64   `toml:"min_volume"`         // session cumulative volume floor
	UniverseRefreshH int     `toml:"universe_refresh_h"` // low-float universe refresh interval (hours)
}

// News is the [news] section: Qot_GetSearchNews polling.
type News struct {
	Enabled   bool `toml:"enabled"`
	FocusedMs int  `toml:"focused_ms"` // poll interval for focused symbols
	WatchMs   int  `toml:"watch_ms"`   // step interval for the watchlist rotation
	MaxPerReq int  `toml:"max_per_req"`
}

// Health is the [health] section: moomoo probe RTT + sys.health/sys.events emission.
type Health struct {
	Enabled bool `toml:"enabled"`
	ProbeMs int  `toml:"probe_ms"` // probe + sys.health emit interval
}
```

Add these fields to the `Config` struct:

```go
	UIHub  UIHub  `toml:"uihub"`
	Scan   Scan   `toml:"scan"`
	News   News   `toml:"news"`
	Health Health `toml:"health"`
```

In `Default()`, set the defaults on the returned `Config`:

```go
	c.UIHub = UIHub{
		Host: "127.0.0.1", Port: 8686, DistDir: "",
		OutboundQueue: 1024, MDRateHz: 30, AccountRateHz: 4, PositionMs: 100, TapeSnapshot: 200,
	}
	c.Scan = Scan{
		Enabled: true, PremarketMs: 2000, RTHMs: 3000, RankPages: 2,
		MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000, UniverseRefreshH: 24,
	}
	c.News = News{Enabled: true, FocusedMs: 20000, WatchMs: 3000, MaxPerReq: 50}
	c.Health = Health{Enabled: true, ProbeMs: 5000}
```

(Match the existing `Default()` construction style — if it uses a composite literal for the whole `Config`, add the fields there instead.) Ensure `net` and `strconv` are imported (already used by `OpenD.Addr()`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/config/ -v && go vet ./internal/config/ && golangci-lint run`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/config/config.go engine/internal/config/config_test.go
git commit -m "feat(engine/config): add [uihub] [scan] [news] [health] sections"
```

---

## Task 2: `wsmsg` — the wire contract DTOs (tygo source of truth)

**Files:**
- Create: `engine/internal/uihub/wsmsg/wsmsg.go`, `engine/internal/uihub/wsmsg/payloads.go`
- Test: `engine/internal/uihub/wsmsg/wsmsg_test.go`

**Interfaces:**
- Consumes: stdlib only (`encoding/json`). **No domain imports** — this package is the tygo source and must stay dependency-free.
- Produces: the `Topic` constants; the string enum types `Side`/`OrderType`/`TIF`/`OrderStatus`/`TickDirection`/`Broker`; server frames `Snapshot`/`Delta`/`Ack`/`Pong`/`Result`; client frames `Subscribe`/`Unsubscribe`/`Command`/`Query`/`Ping`; every payload DTO (`Quote`, `Book`, `Tick`, `Bar`, `IndicatorPoint`, `Order`, `Fill`, `PositionRow`, `AccountRow`, `ExecStatus`+`VenueStatus`+`GateLimitsView`+`GlobalLimitsView`, `ScannerRow`+`ScannerRankPayload`+`ScanHitPayload`, `NewsItem`, `HealthLink`+`HealthSnapshot`, `SysEvent`); the command-arg DTOs. Field names/types match `ui/src/wire/contract.ts` exactly.

- [ ] **Step 1: Write the failing test**

`engine/internal/uihub/wsmsg/wsmsg_test.go`:

```go
package wsmsg_test

import (
	"encoding/json"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestOrderJSONFieldNames(t *testing.T) {
	o := wsmsg.Order{
		Venue: "sim", ID: "ET1", Symbol: "US.AAPL",
		Side: wsmsg.SideBuy, Type: wsmsg.OrderLimit, TIF: wsmsg.TIFDay,
		Qty: 100, LimitPrice: 3.47, Status: wsmsg.StatusSubmitted,
		LeavesQty: 100, ReplacesID: "", CreatedMs: 1_700_000_000_000, UpdatedMs: 1_700_000_000_000,
	}
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"venue", "id", "symbol", "side", "type", "tif", "qty",
		"limitPrice", "stopPrice", "status", "executedQty", "leavesQty", "avgFillPrice",
		"rejectReason", "replacesId", "createdMs", "updatedMs"} {
		if _, ok := m[k]; !ok {
			t.Errorf("Order JSON missing key %q; got %v", k, m)
		}
	}
	if m["side"] != "BUY" || m["type"] != "LIMIT" || m["status"] != "SUBMITTED" {
		t.Errorf("enum strings wrong: side=%v type=%v status=%v", m["side"], m["type"], m["status"])
	}
}

func TestEnvelopeAndPositionNullVenue(t *testing.T) {
	snap := wsmsg.SnapshotMsg{Kind: "snapshot", Topic: wsmsg.TopicExecPositions,
		Payload: []wsmsg.PositionRow{{Venue: nil, Symbol: "US.AAPL", Qty: 50, AvgPrice: 3.5}}}
	b, _ := json.Marshal(snap)
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if got["kind"] != "snapshot" || got["topic"] != "exec.positions" {
		t.Fatalf("envelope wrong: %v", got)
	}
	rows := got["payload"].([]any)
	row := rows[0].(map[string]any)
	if v, ok := row["venue"]; !ok || v != nil {
		t.Fatalf("cross-venue row must serialize venue:null, got %v (present=%v)", v, ok)
	}
}

func TestQuoteAndScannerNullables(t *testing.T) {
	q := wsmsg.Quote{Symbol: "US.AAPL", Bid: 3.46, Ask: 3.48, Last: 3.47, Ts: "2026-07-06T13:31:00.000Z"}
	b, _ := json.Marshal(q)
	var qm map[string]any
	_ = json.Unmarshal(b, &qm)
	for _, k := range []string{"symbol", "bid", "ask", "last", "ts"} {
		if _, ok := qm[k]; !ok {
			t.Errorf("Quote missing %q", k)
		}
	}
	row := wsmsg.ScannerRow{Symbol: "US.XYZ", ChangePct: nil, Last: nil, FloatShares: nil, Volume: 0}
	rb, _ := json.Marshal(row)
	var rm map[string]any
	_ = json.Unmarshal(rb, &rm)
	if rm["changePct"] != nil || rm["last"] != nil || rm["floatShares"] != nil {
		t.Errorf("scanner nullables must serialize as null, got %v", rm)
	}
	if rm["volume"] != float64(0) {
		t.Errorf("volume 0 is legitimate, must be present: %v", rm)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/uihub/wsmsg/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write `wsmsg.go` (envelope, enums, topics)**

`engine/internal/uihub/wsmsg/wsmsg.go`:

```go
// Package wsmsg is the eTape engine<->UI WebSocket contract: pure DTO structs
// with explicit json tags. It imports stdlib only so tygo can compile it into
// ui/src/gen without pulling domain types. Domain->wire mappers live in the
// parent uihub package, never here.
package wsmsg

import "encoding/json"

// Topic is the logical channel a snapshot/delta belongs to.
type Topic string

const (
	TopicQuote     Topic = "md.quote"
	TopicBook      Topic = "md.book"
	TopicTape      Topic = "md.tape"
	TopicBars      Topic = "md.bars"
	TopicIndicator Topic = "md.indicator"

	TopicScannerRank Topic = "scanner.rank"
	TopicScannerHit  Topic = "scanner.hit"
	TopicNews        Topic = "news.item"

	TopicExecAccount   Topic = "exec.account"
	TopicExecPositions Topic = "exec.positions"
	TopicExecOrders    Topic = "exec.orders"
	TopicExecFills     Topic = "exec.fills"
	TopicExecStatus    Topic = "exec.status"

	TopicSysHealth Topic = "sys.health"
	TopicSysEvents Topic = "sys.events"
	TopicConfig    Topic = "config"
)

// AllTopics is the set a client may subscribe to (server-side allow-list).
var AllTopics = map[Topic]bool{
	TopicQuote: true, TopicBook: true, TopicTape: true, TopicBars: true, TopicIndicator: true,
	TopicScannerRank: true, TopicScannerHit: true, TopicNews: true,
	TopicExecAccount: true, TopicExecPositions: true, TopicExecOrders: true,
	TopicExecFills: true, TopicExecStatus: true,
	TopicSysHealth: true, TopicSysEvents: true, TopicConfig: true,
}

// Wire enum types (string literals matching ui/src/wire/contract.ts).
type Side string

const (
	SideBuy   Side = "BUY"
	SideSell  Side = "SELL"
	SideShort Side = "SHORT"
	SideCover Side = "COVER"
)

type OrderType string

const (
	OrderMarket    OrderType = "MARKET"
	OrderLimit     OrderType = "LIMIT"
	OrderStop      OrderType = "STOP"
	OrderStopLimit OrderType = "STOP_LIMIT"
)

type TIF string

const (
	TIFDay TIF = "DAY"
	TIFGTC TIF = "GTC"
	TIFIOC TIF = "IOC"
	TIFFOK TIF = "FOK"
)

type OrderStatus string

const (
	StatusSubmitted       OrderStatus = "SUBMITTED"
	StatusAccepted        OrderStatus = "ACCEPTED"
	StatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	StatusFilled          OrderStatus = "FILLED"
	StatusCanceled        OrderStatus = "CANCELED"
	StatusRejected        OrderStatus = "REJECTED"
	StatusExpired         OrderStatus = "EXPIRED"
	StatusBlocked         OrderStatus = "BLOCKED"
	StatusReplaced        OrderStatus = "REPLACED"
)

type TickDirection string

const (
	DirBuy     TickDirection = "BUY"
	DirSell    TickDirection = "SELL"
	DirNeutral TickDirection = "NEUTRAL"
)

type Broker string

const (
	BrokerTradeZero Broker = "tradezero"
	BrokerAlpaca    Broker = "alpaca"
	BrokerMoomoo    Broker = "moomoo"
)

// ---- server -> client frames ----
// Struct names carry the "Msg" suffix to match ui/src/wire/contract.ts exactly
// (SnapshotMsg/DeltaMsg/AckMsg/PongMsg/ResultMsg) so the tygo output is a
// drop-in for the interim hand-authored contract.

type SnapshotMsg struct {
	Kind    string `json:"kind"` // always "snapshot"
	Topic   Topic  `json:"topic"`
	Key     string `json:"key,omitempty"`
	Payload any    `json:"payload"`
}

type DeltaMsg struct {
	Kind    string `json:"kind"` // always "delta"
	Topic   Topic  `json:"topic"`
	Key     string `json:"key,omitempty"`
	Payload any    `json:"payload"`
}

type AckMsg struct {
	Kind    string `json:"kind"` // always "ack"
	CorrID  string `json:"corrId"`
	Status  string `json:"status"` // "accepted" | "blocked"
	Reason  string `json:"reason,omitempty"`
	OrderID string `json:"orderId,omitempty"`
	Value   any    `json:"value,omitempty"`
}

type PongMsg struct {
	Kind string `json:"kind"` // always "pong"
	T    int64  `json:"t"`
}

type ResultMsg struct {
	Kind    string `json:"kind"` // always "result"
	CorrID  string `json:"corrId"`
	Payload any    `json:"payload"`
}

// ---- client -> server frames ----

type SubscribeMsg struct {
	Kind  string `json:"kind"` // "subscribe"
	Topic Topic  `json:"topic"`
}

type UnsubscribeMsg struct {
	Kind  string `json:"kind"` // "unsubscribe"
	Topic Topic  `json:"topic"`
}

type CommandMsg struct {
	Kind   string          `json:"kind"` // "command"
	CorrID string          `json:"corrId"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
}

type QueryMsg struct {
	Kind   string          `json:"kind"` // "query"
	CorrID string          `json:"corrId"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
}

type PingMsg struct {
	Kind string `json:"kind"` // "ping"
	T    int64  `json:"t"`
}

// ---- command / query argument DTOs ----

type SubmitOrderArgs struct {
	Venue      string    `json:"venue"`
	Symbol     string    `json:"symbol"`
	Side       Side      `json:"side"`
	Type       OrderType `json:"type"`
	TIF        TIF       `json:"tif"`
	Qty        float64   `json:"qty"`
	LimitPrice float64   `json:"limitPrice"`
	StopPrice  float64   `json:"stopPrice"`
}

type CancelOrderArgs struct {
	Venue   string `json:"venue"`
	OrderID string `json:"orderId"`
}

type ReplaceOrderArgs struct {
	Venue      string  `json:"venue"`
	OrderID    string  `json:"orderId"`
	Qty        float64 `json:"qty"`
	LimitPrice float64 `json:"limitPrice"`
	StopPrice  float64 `json:"stopPrice"`
}

type FlattenArgs struct {
	Venue string `json:"venue"`
}

type KillSwitchArgs struct {
	Venue string `json:"venue,omitempty"` // omitted/empty => all venues
}

type ArmArgs struct {
	Venue string `json:"venue,omitempty"` // omitted/empty => master
}

type QueryFillsArgs struct {
	Symbol string `json:"symbol"`
	FromMs int64  `json:"fromMs"`
	ToMs   int64  `json:"toMs"`
}

type GetConfigArgs struct {
	Key string `json:"key"`
}

type SetConfigArgs struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}
```

- [ ] **Step 4: Write `payloads.go` (topic payload DTOs)**

`engine/internal/uihub/wsmsg/payloads.go`:

```go
package wsmsg

// ---- market-data payloads (timestamps are ISO-8601 UTC strings) ----

type Quote struct {
	Symbol string  `json:"symbol"`
	Bid    float64 `json:"bid"`
	Ask    float64 `json:"ask"`
	Last   float64 `json:"last"`
	Ts     string  `json:"ts"`
}

type BookLevel struct {
	Price float64 `json:"price"`
	Size  int64   `json:"size"`
}

type Book struct {
	Symbol string      `json:"symbol"`
	Bids   []BookLevel `json:"bids"`
	Asks   []BookLevel `json:"asks"`
	Ts     string      `json:"ts"`
}

type Tick struct {
	Symbol    string        `json:"symbol"`
	Price     float64       `json:"price"`
	Size      int64         `json:"size"`
	Direction TickDirection `json:"direction"`
	Ts        string        `json:"ts"`
}

type Bar struct {
	Symbol      string `json:"symbol"`
	Timeframe   string `json:"timeframe"`
	BucketStart string `json:"bucketStart"`
	O           float64 `json:"o"`
	H           float64 `json:"h"`
	L           float64 `json:"l"`
	C           float64 `json:"c"`
	V           int64   `json:"v"`
	InProgress  bool    `json:"inProgress"`
	Gap         bool    `json:"gap,omitempty"`
}

type IndicatorPoint struct {
	TimeMs int64   `json:"timeMs"`
	Value  float64 `json:"value"`
}

// ---- execution payloads (timestamps are epoch-ms numbers) ----

type Order struct {
	Venue        string      `json:"venue"`
	ID           string      `json:"id"`
	Symbol       string      `json:"symbol"`
	Side         Side        `json:"side"`
	Type         OrderType   `json:"type"`
	TIF          TIF         `json:"tif"`
	Qty          float64     `json:"qty"`
	LimitPrice   float64     `json:"limitPrice"`
	StopPrice    float64     `json:"stopPrice"`
	Status       OrderStatus `json:"status"`
	ExecutedQty  float64     `json:"executedQty"`
	LeavesQty    float64     `json:"leavesQty"`
	AvgFillPrice float64     `json:"avgFillPrice"`
	RejectReason string      `json:"rejectReason"`
	ReplacesID   string      `json:"replacesId"`
	CreatedMs    int64       `json:"createdMs"`
	UpdatedMs    int64       `json:"updatedMs"`
}

type Fill struct {
	Venue   string  `json:"venue"`
	OrderID string  `json:"orderId"`
	Symbol  string  `json:"symbol"`
	Side    Side    `json:"side"`
	Qty     float64 `json:"qty"`
	Price   float64 `json:"price"`
	TsMs    int64   `json:"tsMs"`
}

// PositionRow.Venue is a pointer so a cross-venue net row serializes venue:null.
type PositionRow struct {
	Venue         *string `json:"venue"`
	Symbol        string  `json:"symbol"`
	Qty           float64 `json:"qty"`
	AvgPrice      float64 `json:"avgPrice"`
	UnrealizedPnl float64 `json:"unrealizedPnl"`
}

type AccountRow struct {
	Venue         string  `json:"venue"`
	Equity        float64 `json:"equity"`
	BuyingPower   float64 `json:"buyingPower"`
	AvailableCash float64 `json:"availableCash"`
	SodEquity     float64 `json:"sodEquity"`
	Realized      float64 `json:"realized"`
	DayPnl        float64 `json:"dayPnl"`
	Leverage      float64 `json:"leverage"`
	TsMs          int64   `json:"tsMs"`
}

type GateLimitsView struct {
	MaxOrderValue     float64 `json:"maxOrderValue"`
	MaxPositionValue  float64 `json:"maxPositionValue"`
	MaxPositionShares float64 `json:"maxPositionShares"`
	MaxOpenOrders     int     `json:"maxOpenOrders"`
}

type GlobalLimitsView struct {
	MaxDayLoss              float64 `json:"maxDayLoss"`
	MaxSymbolPositionValue  float64 `json:"maxSymbolPositionValue"`
	MaxSymbolPositionShares float64 `json:"maxSymbolPositionShares"`
}

type VenueStatus struct {
	Venue            string         `json:"venue"`
	Broker           Broker         `json:"broker"`
	Connected        bool           `json:"connected"`
	VenueArmed       bool           `json:"venueArmed"`
	ReconcilePending bool           `json:"reconcilePending"`
	Note             string         `json:"note"`
	LastReconcileMs  *int64         `json:"lastReconcileMs"`
	Gate             GateLimitsView `json:"gate"`
}

type ExecStatus struct {
	MasterArmed bool             `json:"masterArmed"`
	Global      GlobalLimitsView `json:"global"`
	Venues      []VenueStatus    `json:"venues"`
}

// ---- scanner / news / health payloads ----

type ScannerRow struct {
	Symbol      string   `json:"symbol"`
	ChangePct   *float64 `json:"changePct"`   // null = no print yet
	Last        *float64 `json:"last"`        // null = no print yet
	FloatShares *float64 `json:"floatShares"` // ACTUAL shares (engine converts moomoo thousands); null = unknown
	Volume      int64    `json:"volume"`      // 0 is legitimate
}

type ScannerRankPayload struct {
	RefreshedAt string       `json:"refreshedAt"`
	Rows        []ScannerRow `json:"rows"`
}

type ScanHitPayload struct {
	Symbol string `json:"symbol"`
	At     string `json:"at"`
}

type NewsItem struct {
	Symbol   string `json:"symbol"`
	Headline string `json:"headline"`
	Source   string `json:"source"`
	URL      string `json:"url"`
	SeenAt   string `json:"seen_at"`
}

type HealthLink struct {
	Link   string   `json:"link"` // "ui-engine" | "engine-moomoo" | "engine-tz"
	Ms     *float64 `json:"ms"`
	Min    *float64 `json:"min"`
	Avg    *float64 `json:"avg"`
	Max    *float64 `json:"max"`
	Status string   `json:"status"` // "ok" | "degraded" | "down"
}

type HealthSnapshot struct {
	Links []HealthLink `json:"links"`
}

type SysEvent struct {
	Seq    int64  `json:"seq"`
	Ts     string `json:"ts"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd engine && go test ./internal/uihub/wsmsg/ -v && go vet ./internal/uihub/wsmsg/ && golangci-lint run`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add engine/internal/uihub/wsmsg/
git commit -m "feat(engine/uihub): wsmsg wire contract DTOs (tygo source)"
```

---

## Task 3: Domain → wire mappers

**Files:**
- Create: `engine/internal/uihub/map.go`
- Test: `engine/internal/uihub/map_test.go`

**Interfaces:**
- Consumes: `exec` (`Order`/`Fill`/`Position`/`AccountSnapshot` + `Side`/`OrderType`/`TIF`/`OrderStatus` enums), `feed` (`Quote`/`Book`/`Tick`/`Direction`), `md` (`Bar`/`Point`), `session` (`Timeframe`), and `wsmsg`.
- Produces (all package-private to `uihub`, package `uihub`): `sideToWire`, `orderTypeToWire`, `tifToWire`, `statusToWire`, `dirToWire`, `isoMs`, `mapOrder`, `mapFill`, `mapPosition(p, mark)`, `mapAccount`, `mapQuote(q, bid, ask)`, `mapBook`, `mapTick`, `mapBar`, `mapIndicatorPoint`. Used by the mirror (Task 5) and the update-fan-in (Task 10/15).

- [ ] **Step 1: Write the failing test**

`engine/internal/uihub/map_test.go`:

```go
package uihub

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestMapOrderEnumsAndTimestamps(t *testing.T) {
	o := exec.Order{
		Venue: "sim", ID: "ET1", Symbol: "US.AAPL",
		Side: exec.SideShort, Type: exec.TypeStopLimit, TIF: exec.TIFGTC,
		Qty: 80, LimitPrice: 3.55, StopPrice: 3.60, Status: exec.StatusPartiallyFilled,
		ExecutedQty: 30, LeavesQty: 50, AvgFillPrice: 3.54,
		ReplacesID: "ET0", CreatedMs: 1_700_000_000_000, UpdatedMs: 1_700_000_005_000,
	}
	w := mapOrder(o)
	if w.Side != wsmsg.SideShort || w.Type != wsmsg.OrderStopLimit || w.TIF != wsmsg.TIFGTC {
		t.Fatalf("enum map wrong: %+v", w)
	}
	if w.Status != wsmsg.StatusPartiallyFilled {
		t.Fatalf("status map wrong: %v", w.Status)
	}
	if w.CreatedMs != 1_700_000_000_000 || w.UpdatedMs != 1_700_000_005_000 {
		t.Fatalf("exec ms must pass through as numbers: %+v", w)
	}
	if w.ID != "ET1" || w.ReplacesID != "ET0" || w.LeavesQty != 50 {
		t.Fatalf("field copy wrong: %+v", w)
	}
}

func TestMapQuoteJoinsBidAskAndISOTime(t *testing.T) {
	q := feed.Quote{Symbol: "US.AAPL", Last: 3.47, TsMs: 1_783_344_660_000} // 2026-07-06T13:31:00Z
	w := mapQuote(q, 3.46, 3.48)
	if w.Bid != 3.46 || w.Ask != 3.48 || w.Last != 3.47 {
		t.Fatalf("quote join wrong: %+v", w)
	}
	if w.Ts != "2026-07-06T13:31:00.000Z" {
		t.Fatalf("md timestamp must be ISO-8601 UTC ms: %q", w.Ts)
	}
}

func TestMapPositionUnrealizedFromMark(t *testing.T) {
	p := exec.Position{Venue: "sim", Symbol: "US.AAPL", Qty: 100, AvgPrice: 3.50}
	w := mapPosition(p, 3.60) // long 100 @ 3.50, mark 3.60 => +10.00
	if w.Venue == nil || *w.Venue != "sim" {
		t.Fatalf("venue must be set for a venue-scoped row: %+v", w)
	}
	if w.UnrealizedPnl < 9.999 || w.UnrealizedPnl > 10.001 {
		t.Fatalf("unrealized pnl = %v, want ~10", w.UnrealizedPnl)
	}
}

func TestMapBarTimeframeAndBucket(t *testing.T) {
	b := md.Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 1_783_344_660_000, // 2026-07-06T13:31:00Z
		O: 1, H: 2, L: 0.5, C: 1.5, V: 1000, InProgress: true}
	w := mapBar(b)
	if w.Timeframe != "1m" || w.BucketStart != "2026-07-06T13:31:00.000Z" {
		t.Fatalf("bar tf/bucket wrong: tf=%q bucket=%q", w.Timeframe, w.BucketStart)
	}
	if !w.InProgress || w.V != 1000 {
		t.Fatalf("bar fields wrong: %+v", w)
	}
}

func TestMapTickDirection(t *testing.T) {
	w := mapTick(feed.Tick{Symbol: "US.AAPL", Price: 3.47, Volume: 10, Dir: feed.Sell, TsMs: 1_783_344_660_000})
	if w.Direction != wsmsg.DirSell || w.Size != 10 {
		t.Fatalf("tick map wrong: %+v", w)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/uihub/ -run Map -v`
Expected: FAIL — undefined `mapOrder` etc.

- [ ] **Step 3: Write `map.go`**

`engine/internal/uihub/map.go`:

```go
package uihub

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// isoMs renders an epoch-ms timestamp as an ISO-8601 UTC string with millisecond
// precision (the format md/scanner/news/sys.events topics use on the wire).
func isoMs(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func sideToWire(s exec.Side) wsmsg.Side {
	switch s {
	case exec.SideBuy:
		return wsmsg.SideBuy
	case exec.SideSell:
		return wsmsg.SideSell
	case exec.SideShort:
		return wsmsg.SideShort
	case exec.SideCover:
		return wsmsg.SideCover
	default:
		return wsmsg.SideBuy
	}
}

func orderTypeToWire(t exec.OrderType) wsmsg.OrderType {
	switch t {
	case exec.TypeMarket:
		return wsmsg.OrderMarket
	case exec.TypeLimit:
		return wsmsg.OrderLimit
	case exec.TypeStop:
		return wsmsg.OrderStop
	case exec.TypeStopLimit:
		return wsmsg.OrderStopLimit
	default:
		return wsmsg.OrderMarket
	}
}

func tifToWire(t exec.TIF) wsmsg.TIF {
	switch t {
	case exec.TIFDay:
		return wsmsg.TIFDay
	case exec.TIFGTC:
		return wsmsg.TIFGTC
	case exec.TIFIOC:
		return wsmsg.TIFIOC
	case exec.TIFFOK:
		return wsmsg.TIFFOK
	default:
		return wsmsg.TIFDay
	}
}

func statusToWire(s exec.OrderStatus) wsmsg.OrderStatus {
	switch s {
	case exec.StatusSubmitted:
		return wsmsg.StatusSubmitted
	case exec.StatusAccepted:
		return wsmsg.StatusAccepted
	case exec.StatusPartiallyFilled:
		return wsmsg.StatusPartiallyFilled
	case exec.StatusFilled:
		return wsmsg.StatusFilled
	case exec.StatusCanceled:
		return wsmsg.StatusCanceled
	case exec.StatusRejected:
		return wsmsg.StatusRejected
	case exec.StatusExpired:
		return wsmsg.StatusExpired
	case exec.StatusBlocked:
		return wsmsg.StatusBlocked
	case exec.StatusReplaced:
		return wsmsg.StatusReplaced
	default:
		return wsmsg.StatusSubmitted
	}
}

func dirToWire(d feed.Direction) wsmsg.TickDirection {
	switch d {
	case feed.Buy:
		return wsmsg.DirBuy
	case feed.Sell:
		return wsmsg.DirSell
	default:
		return wsmsg.DirNeutral
	}
}

func mapOrder(o exec.Order) wsmsg.Order {
	return wsmsg.Order{
		Venue: string(o.Venue), ID: o.ID, Symbol: o.Symbol,
		Side: sideToWire(o.Side), Type: orderTypeToWire(o.Type), TIF: tifToWire(o.TIF),
		Qty: o.Qty, LimitPrice: o.LimitPrice, StopPrice: o.StopPrice,
		Status: statusToWire(o.Status), ExecutedQty: o.ExecutedQty, LeavesQty: o.LeavesQty,
		AvgFillPrice: o.AvgFillPrice, RejectReason: o.RejectReason, ReplacesID: o.ReplacesID,
		CreatedMs: o.CreatedMs, UpdatedMs: o.UpdatedMs,
	}
}

func mapFill(f exec.Fill) wsmsg.Fill {
	return wsmsg.Fill{
		Venue: string(f.Venue), OrderID: f.OrderID, Symbol: f.Symbol,
		Side: sideToWire(f.Side), Qty: f.Qty, Price: f.Price, TsMs: f.TsMs,
	}
}

// mapPosition maps a venue-scoped position. mark is the latest last-trade price
// (0 if unknown); UnrealizedPnl = (mark - AvgPrice) * Qty with Qty signed.
func mapPosition(p exec.Position, mark float64) wsmsg.PositionRow {
	v := string(p.Venue)
	var upl float64
	if mark != 0 {
		upl = (mark - p.AvgPrice) * p.Qty
	}
	return wsmsg.PositionRow{Venue: &v, Symbol: p.Symbol, Qty: p.Qty, AvgPrice: p.AvgPrice, UnrealizedPnl: upl}
}

func mapAccount(a exec.AccountSnapshot) wsmsg.AccountRow {
	return wsmsg.AccountRow{
		Venue: string(a.Venue), Equity: a.Equity, BuyingPower: a.BuyingPower,
		AvailableCash: a.AvailableCash, SodEquity: a.SodEquity, Realized: a.Realized,
		DayPnl: a.DayPnL, Leverage: a.Leverage, TsMs: a.TsMs,
	}
}

func mapQuote(q feed.Quote, bid, ask float64) wsmsg.Quote {
	return wsmsg.Quote{Symbol: q.Symbol, Bid: bid, Ask: ask, Last: q.Last, Ts: isoMs(q.TsMs)}
}

func mapBook(b feed.Book) wsmsg.Book {
	bids := make([]wsmsg.BookLevel, len(b.Bids))
	for i, l := range b.Bids {
		bids[i] = wsmsg.BookLevel{Price: l.Price, Size: l.Volume}
	}
	asks := make([]wsmsg.BookLevel, len(b.Asks))
	for i, l := range b.Asks {
		asks[i] = wsmsg.BookLevel{Price: l.Price, Size: l.Volume}
	}
	return wsmsg.Book{Symbol: b.Symbol, Bids: bids, Asks: asks, Ts: isoMs(b.TsMs)}
}

func mapTick(t feed.Tick) wsmsg.Tick {
	return wsmsg.Tick{Symbol: t.Symbol, Price: t.Price, Size: t.Volume, Direction: dirToWire(t.Dir), Ts: isoMs(t.TsMs)}
}

func mapBar(b md.Bar) wsmsg.Bar {
	return wsmsg.Bar{
		Symbol: b.Symbol, Timeframe: string(b.TF), BucketStart: isoMs(b.BucketMs),
		O: b.O, H: b.H, L: b.L, C: b.C, V: b.V, InProgress: b.InProgress, Gap: b.Gap,
	}
}

func mapIndicatorPoint(p md.Point) wsmsg.IndicatorPoint {
	return wsmsg.IndicatorPoint{TimeMs: p.TimeMs, Value: p.Value}
}
```

> `map.go` deliberately does **not** import `session`: `string(b.TF)` converts the `session.Timeframe` field to a string without referencing the package. `map_test.go` does import `session` (to build `md.Bar{TF: session.TF1m}`), which is correct.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/uihub/ -run Map -v && go vet ./internal/uihub/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/uihub/map.go engine/internal/uihub/map_test.go
git commit -m "feat(engine/uihub): domain->wire mappers (enum+timestamp+pnl joins)"
```

---

## Task 4: tygo pipeline — generate `ui/src/gen/wsmsg.ts` + CI drift gate

**Files:**
- Create: `engine/tygo.yaml`
- Modify: `engine/Makefile`, `engine/go.mod` (tool dependency)
- Create (generated, committed): `ui/src/gen/wsmsg.ts`
- Modify: the CI entry point (see Step 5 — verify whether a `.github/workflows/*` file exists; else the Makefile target is the gate)

**Interfaces:**
- Consumes: the `wsmsg` package (Task 2) as the sole tygo source.
- Produces: `ui/src/gen/wsmsg.ts` whose exported interfaces/type-aliases match `ui/src/wire/contract.ts` field-for-field (envelope `*Msg` names, payload names, string-literal enum unions); a `make gen-ts` target that regenerates it and a `make gen-ts-check` target that fails on drift.

This task has no Go unit test; its "test" is deterministic regeneration + a field-name diff against the hand-authored contract.

- [ ] **Step 1: Add tygo as a pinned tool dependency**

Run (from `engine/`):

```bash
cd engine && go get -tool github.com/gzuidhof/tygo@latest && go mod tidy
```

This adds a `tool` directive to `go.mod` (Go 1.24+ tool-dependency mechanism) pinning the resolved tygo version. Confirm `go tool tygo --help` runs.

- [ ] **Step 2: Write `engine/tygo.yaml`**

`engine/tygo.yaml` (output path is relative to `engine/`, the dir tygo runs in):

```yaml
packages:
  - path: "github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
    output_path: "../ui/src/gen/wsmsg.ts"
    type_mappings:
      "json.RawMessage": "unknown"
    frontmatter: |
      // Code generated by tygo from engine/internal/uihub/wsmsg. DO NOT EDIT.
      // Regenerate with `make gen-ts` in engine/. CI fails on drift.
    # If tygo does not emit string-literal unions for the typed string const
    # groups (Side/OrderType/TIF/OrderStatus/TickDirection/Broker), add explicit
    # `type_mappings` overrides + a `frontmatter` union block here — see Step 4.
```

- [ ] **Step 3: Add Makefile targets**

Append to `engine/Makefile` (and add `gen-ts gen-ts-check` to the `.PHONY` line):

```makefile
gen-ts:
	go tool tygo generate
gen-ts-check: gen-ts
	git diff --exit-code -- ../ui/src/gen/wsmsg.ts || \
	  (echo "wsmsg contract drift: ui/src/gen/wsmsg.ts is stale — run 'make gen-ts' and commit" && exit 1)
```

- [ ] **Step 4: Generate and reconcile against the interim contract**

Run: `cd engine && make gen-ts`

Then open `ui/src/gen/wsmsg.ts` and diff its type shapes against `ui/src/wire/contract.ts` (the `ui-execution-surfaces` worktree copy). Acceptance criteria — the generated file must contain:
- Envelope interfaces named `SnapshotMsg`, `DeltaMsg`, `AckMsg`, `PongMsg`, `ResultMsg`, `SubscribeMsg`, `UnsubscribeMsg`, `CommandMsg`, `QueryMsg`, `PingMsg` with the exact fields from Task 2.
- Payload interfaces `Quote`, `Book`, `BookLevel`, `Tick`, `Bar`, `IndicatorPoint`, `Order`, `Fill`, `PositionRow` (with `venue: string | null`), `AccountRow`, `GateLimitsView`, `GlobalLimitsView`, `VenueStatus`, `ExecStatus`, `ScannerRow` (`changePct/last/floatShares: number | null`), `ScannerRankPayload`, `ScanHitPayload`, `NewsItem` (`seen_at`), `HealthLink`, `HealthSnapshot`, `SysEvent`.
- Enum unions: `Side = "BUY" | "SELL" | "SHORT" | "COVER"`, `OrderType = "MARKET" | "LIMIT" | "STOP" | "STOP_LIMIT"`, `TIF`, `OrderStatus` (9 values), `TickDirection`, `Broker`, and `Topic`.

**If tygo emits `export type Side = string`** (loses the literal union) for the typed-const groups, add a `frontmatter` block to `tygo.yaml` that declares the exact unions and an `exclude` for the auto-generated loose aliases, e.g.:

```yaml
    frontmatter: |
      // Code generated by tygo ... DO NOT EDIT.
      export type Side = "BUY" | "SELL" | "SHORT" | "COVER";
      export type OrderType = "MARKET" | "LIMIT" | "STOP" | "STOP_LIMIT";
      export type TIF = "DAY" | "GTC" | "IOC" | "FOK";
      export type OrderStatus = "SUBMITTED" | "ACCEPTED" | "PARTIALLY_FILLED" | "FILLED" | "CANCELED" | "REJECTED" | "EXPIRED" | "BLOCKED" | "REPLACED";
      export type TickDirection = "BUY" | "SELL" | "NEUTRAL";
      export type Broker = "tradezero" | "alpaca" | "moomoo";
    exclude_files: []   # keep; use `flavor`/const options per tygo version if needed
```

Regenerate (`make gen-ts`) until the enums are literal unions. Then add the top-level `ServerMessage`/`ClientMessage`/`TopicName` union aliases via the frontmatter (tygo won't infer them):

```
export type ServerMessage = SnapshotMsg | DeltaMsg | AckMsg | PongMsg | ResultMsg;
export type ClientMessage = SubscribeMsg | UnsubscribeMsg | CommandMsg | QueryMsg | PingMsg;
```

(`TopicName` is already produced as `Topic` from the `Topic` const group; UI Plan 6 aliases `TopicName = Topic` when it swaps imports — note this for the UI-side handoff.)

**Discriminant-field fidelity (`kind`/`status`/`link`) — verification-pass finding.** tygo emits a plain Go `string` field as `field: string`, so `SnapshotMsg.kind`, `AckMsg.status`, and `HealthLink.link`/`status` would generate as `string` rather than the literal unions the interim `contract.ts` uses — which weakens the `ServerMessage`/`ClientMessage` discriminated-union narrowing the UI's `WsClient.onMessage` relies on. Resolve as follows:
- **`status` and `link`/health-`status` (cheap, do it):** in Task 2, declare typed string aliases with const groups — `type AckStatus string` (`"accepted"`/`"blocked"`), `type LinkName string` (`"ui-engine"`/`"engine-moomoo"`/`"engine-tz"`), `type LinkStatus string` (`"ok"`/`"degraded"`/`"down"`) — and type `AckMsg.Status`/`HealthLink.Link`/`HealthLink.Status` with them (small assignment ripples: `ackFromCmd`/`blocked` in `commands.go` and `linkFor` in `health.go` build these with the typed consts or an explicit conversion). tygo then emits literal unions for all three, exactly like `Side`/`OrderType`.
- **`kind` (envelope discriminant):** a single shared `Kind` type can't carry a different literal per envelope, and per-struct kind types are ugly. Options, pick one at execution: (a) add `exclude_files: ["wsmsg.go"]` to `tygo.yaml` and hand-declare the 10 envelope interfaces (with literal `kind`) + `ServerMessage`/`ClientMessage` in the frontmatter, letting tygo generate only the payload/arg DTOs (move the arg DTOs to `payloads.go` first; verify tygo still resolves cross-file type refs like `Side`); or (b) accept `kind: string` in the generated file and have **UI Plan 6 retain the literal `kind` discriminants** on its side during the contract swap (it already has them in `contract.ts`). Default to (b) unless a strict byte-for-byte `ui/src/gen` is required — the engine's JSON is identical either way; this is a TS-narrowing nicety, not an engine-correctness issue.

- [ ] **Step 5: Wire the drift gate into CI**

Determine the CI entry point: `ls engine/../.github/workflows/ 2>/dev/null` and `ls .github/workflows/`. 
- If a workflow file exists, add a step `cd engine && make gen-ts-check` after the build/test steps.
- If none exists (the recon found only the `Makefile`), the authoritative gate is `make gen-ts-check`; add a one-line note to `engine/Makefile`'s header comment that `gen-ts-check` must pass before merge, and record in the plan's execution ledger that no workflow file was present. Do not fabricate a CI system.

Verify the gate works: run `make gen-ts-check` (should pass on freshly-generated output), then hand-edit `ui/src/gen/wsmsg.ts`, rerun (should FAIL), then `make gen-ts` to restore.

- [ ] **Step 6: Verify the full engine still builds and lint is clean**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run`
Expected: PASS (the tool directive doesn't affect the build; `tygo.yaml` is not Go).

- [ ] **Step 7: Commit**

```bash
git add engine/tygo.yaml engine/Makefile engine/go.mod engine/go.sum ui/src/gen/wsmsg.ts
git commit -m "build(engine/uihub): tygo pipeline generating ui/src/gen/wsmsg.ts + drift gate"
```

> **UI-side handoff note (not this plan's work):** UI Plan 6 swaps `ui/src/wire/contract.ts` imports over to `ui/src/gen/wsmsg.ts` (aliasing `TopicName = Topic`) and re-captures fixtures from real engine output. Engine Plan 6 only *generates and commits* the file.

---

## Task 5: uihub state mirror + snapshot builder

**Files:**
- Create: `engine/internal/uihub/mirror.go`
- Test: `engine/internal/uihub/mirror_test.go`

**Interfaces:**
- Consumes: `md` (`Update` union + `Bar`/`Point`), `exec` (`Update` union + `Order`/`Fill`/`Position`/`AccountSnapshot`), `wsmsg`, `session` (`Timeframe`), the Task 3 mappers.
- Produces (package-private): `type staged struct{ Topic wsmsg.Topic; Key string; Payload any; Snap bool }`; `type venueMeta struct{ ID string; Broker wsmsg.Broker; Gate wsmsg.GateLimitsView }`; `newMirror(venues []venueMeta, global wsmsg.GlobalLimitsView, tapeCap, newsCap, fillsCap, eventsCap int) *mirror`; `(*mirror).applyMD(u md.Update) []staged`; `(*mirror).applyExec(u exec.Update) []staged`; `(*mirror).applyPub(s staged)` (poller/health/sys events); `(*mirror).snapshotFrames(topic wsmsg.Topic) []staged`. The `mirror` is **not** goroutine-safe — the hub loop (Task 7) owns it single-threaded; tests call it directly.

**Design (locked decision #1/#2):** the mirror is the single keep-latest state cache. Every `applyX` updates state **and returns the delta frame(s)** to broadcast. `snapshotFrames` serializes current state for a new subscriber. The two cross-cutting joins live here: (a) `md.quote` bid/ask come from the latest cached top-of-book; (b) `exec.status` is assembled from per-venue `StatusUpdate`/`AccountUpdate` plus static config gate limits.

- [ ] **Step 1: Write the failing test**

`engine/internal/uihub/mirror_test.go`:

```go
package uihub

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func testMirror() *mirror {
	return newMirror(
		[]venueMeta{{ID: "sim", Broker: wsmsg.BrokerAlpaca, Gate: wsmsg.GateLimitsView{MaxOrderValue: 1000}}},
		wsmsg.GlobalLimitsView{MaxDayLoss: 500},
		200, 200, 500, 500,
	)
}

func TestMirrorQuoteJoinsBookBidAsk(t *testing.T) {
	m := testMirror()
	m.applyMD(md.BookUpdate{Book: feed.Book{Symbol: "US.AAPL", TsMs: 1,
		Bids: []feed.BookLevel{{Price: 3.46, Volume: 100}},
		Asks: []feed.BookLevel{{Price: 3.48, Volume: 120}}}})
	d := m.applyMD(md.QuoteUpdate{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.47, TsMs: 2}})
	if len(d) != 1 || d[0].Topic != wsmsg.TopicQuote {
		t.Fatalf("expected one quote delta, got %v", d)
	}
	q := d[0].Payload.(wsmsg.Quote)
	if q.Bid != 3.46 || q.Ask != 3.48 || q.Last != 3.47 {
		t.Fatalf("bid/ask join failed: %+v", q)
	}
}

func TestMirrorBarsSeriesUpsertAndSnapshot(t *testing.T) {
	m := testMirror()
	m.applyMD(md.BarUpdate{Bar: md.Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 60_000, C: 1, InProgress: true}})
	m.applyMD(md.BarUpdate{Bar: md.Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 60_000, C: 2, InProgress: false}}) // finalize same bucket
	m.applyMD(md.BarUpdate{Bar: md.Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 120_000, C: 3, InProgress: true}})  // new bucket
	frames := m.snapshotFrames(wsmsg.TopicBars)
	if len(frames) != 1 {
		t.Fatalf("expected one bars snapshot frame (one series), got %d", len(frames))
	}
	series := frames[0].Payload.([]wsmsg.Bar)
	if len(series) != 2 {
		t.Fatalf("expected 2 bars (upserted 60s + new 120s), got %d: %+v", len(series), series)
	}
	if series[0].C != 2 || !(series[1].C == 3) {
		t.Fatalf("bar upsert/append wrong: %+v", series)
	}
}

func TestMirrorExecStatusAggregate(t *testing.T) {
	m := testMirror()
	m.applyExec(exec.StatusUpdate{Venue: "sim", Connected: true, MasterArmed: false, Note: "up"})
	d := m.applyExec(exec.AccountUpdate{
		Account:    exec.AccountSnapshot{Venue: "sim", Equity: 100000, DayPnL: -50, TsMs: 5},
		VenueArmed: true, MasterArmed: true,
	})
	// AccountUpdate produces both an exec.account delta and an exec.status delta.
	var accountSeen, statusSeen bool
	for _, s := range d {
		switch s.Topic {
		case wsmsg.TopicExecAccount:
			accountSeen = true
		case wsmsg.TopicExecStatus:
			st := s.Payload.(wsmsg.ExecStatus)
			if !st.MasterArmed || len(st.Venues) != 1 || !st.Venues[0].VenueArmed || !st.Venues[0].Connected {
				t.Fatalf("exec.status aggregate wrong: %+v", st)
			}
			if st.Global.MaxDayLoss != 500 || st.Venues[0].Gate.MaxOrderValue != 1000 {
				t.Fatalf("gate limits not merged from config: %+v", st)
			}
			statusSeen = true
		}
	}
	if !accountSeen || !statusSeen {
		t.Fatalf("AccountUpdate must yield account+status deltas; got %v", d)
	}
}

func TestMirrorPositionsSnapshotUsesMark(t *testing.T) {
	m := testMirror()
	m.applyMD(md.QuoteUpdate{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.60, TsMs: 1}}) // sets mark
	m.applyExec(exec.PositionUpdate{Position: exec.Position{Venue: "sim", Symbol: "US.AAPL", Qty: 100, AvgPrice: 3.50}})
	frames := m.snapshotFrames(wsmsg.TopicExecPositions)
	if len(frames) != 1 {
		t.Fatalf("positions snapshot is one full-replace frame, got %d", len(frames))
	}
	rows := frames[0].Payload.([]wsmsg.PositionRow)
	if len(rows) != 1 || rows[0].UnrealizedPnl < 9.99 || rows[0].UnrealizedPnl > 10.01 {
		t.Fatalf("position pnl from mark wrong: %+v", rows)
	}
}

func TestMirrorOrdersSnapshotIsArray(t *testing.T) {
	m := testMirror()
	m.applyExec(exec.OrderUpdate{Order: exec.Order{Venue: "sim", ID: "ET1", Symbol: "US.AAPL", Status: exec.StatusSubmitted}})
	m.applyExec(exec.OrderUpdate{Order: exec.Order{Venue: "sim", ID: "ET2", Symbol: "US.AAPL", Status: exec.StatusAccepted}})
	frames := m.snapshotFrames(wsmsg.TopicExecOrders)
	if len(frames) != 1 {
		t.Fatalf("orders snapshot is a single Order[] frame, got %d", len(frames))
	}
	if got := len(frames[0].Payload.([]wsmsg.Order)); got != 2 {
		t.Fatalf("expected 2 orders, got %d", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/uihub/ -run Mirror -v`
Expected: FAIL — undefined `newMirror` / `mirror`.

- [ ] **Step 3: Write `mirror.go`**

`engine/internal/uihub/mirror.go`:

```go
package uihub

import (
	"sort"
	"strconv"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// staged is one delta/snapshot frame the hub will broadcast. Snap is true only
// for a mid-stream full-series indicator update (broadcast as kind:"snapshot");
// every other staged frame is a delta.
type staged struct {
	Topic   wsmsg.Topic
	Key     string
	Payload any
	Snap    bool
}

// venueMeta is the static per-venue config the mirror needs to assemble exec.status.
type venueMeta struct {
	ID     string
	Broker wsmsg.Broker
	Gate   wsmsg.GateLimitsView
}

type mirror struct {
	global wsmsg.GlobalLimitsView

	// market data (keyed by symbol unless noted)
	quotes     map[string]wsmsg.Quote
	books      map[string]wsmsg.Book
	tape       map[string][]wsmsg.Tick        // bounded recent ring per symbol
	bars       map[string][]wsmsg.Bar         // key "SYMBOL:TF", sorted by bucketStart
	indicators map[string][]wsmsg.IndicatorPoint // key instanceId or instanceId#slot
	marks      map[string]float64             // last price per symbol (pnl + display)

	// scanner / news
	rank map[string]wsmsg.ScannerRankPayload // key session
	news []wsmsg.NewsItem                     // bounded recent

	// execution
	accounts    map[string]wsmsg.AccountRow // key venue
	positions   map[string]exec.Position    // key "venue|symbol"; mapped w/ mark on read
	orders      map[string]wsmsg.Order      // key orderID
	fills       []wsmsg.Fill                // bounded recent
	venueStatus map[string]*wsmsg.VenueStatus
	masterArmed bool

	// system
	health wsmsg.HealthSnapshot
	events []wsmsg.SysEvent // bounded recent

	tapeCap, newsCap, fillsCap, eventsCap int
	venueOrder                            []string // stable venue order for exec.status
}

func newMirror(venues []venueMeta, global wsmsg.GlobalLimitsView, tapeCap, newsCap, fillsCap, eventsCap int) *mirror {
	m := &mirror{
		global:      global,
		quotes:      map[string]wsmsg.Quote{},
		books:       map[string]wsmsg.Book{},
		tape:        map[string][]wsmsg.Tick{},
		bars:        map[string][]wsmsg.Bar{},
		indicators:  map[string][]wsmsg.IndicatorPoint{},
		marks:       map[string]float64{},
		rank:        map[string]wsmsg.ScannerRankPayload{},
		accounts:    map[string]wsmsg.AccountRow{},
		positions:   map[string]exec.Position{},
		orders:      map[string]wsmsg.Order{},
		venueStatus: map[string]*wsmsg.VenueStatus{},
		tapeCap:     tapeCap, newsCap: newsCap, fillsCap: fillsCap, eventsCap: eventsCap,
	}
	for _, v := range venues {
		m.venueStatus[v.ID] = &wsmsg.VenueStatus{Venue: v.ID, Broker: v.Broker, Gate: v.Gate}
		m.venueOrder = append(m.venueOrder, v.ID)
	}
	return m
}

func barKey(symbol, tf string) string { return symbol + ":" + tf }

// applyMD updates market-data state and returns delta frames to broadcast.
func (m *mirror) applyMD(u md.Update) []staged {
	switch v := u.(type) {
	case md.QuoteUpdate:
		m.marks[v.Quote.Symbol] = v.Quote.Last
		bid, ask := m.topOfBook(v.Quote.Symbol)
		q := mapQuote(v.Quote, bid, ask)
		m.quotes[v.Quote.Symbol] = q
		return []staged{{Topic: wsmsg.TopicQuote, Payload: q}}
	case md.BookUpdate:
		b := mapBook(v.Book)
		m.books[v.Book.Symbol] = b
		// keep the cached quote's bid/ask fresh (no separate quote delta emitted)
		if q, ok := m.quotes[v.Book.Symbol]; ok {
			q.Bid, q.Ask = m.topOfBook(v.Book.Symbol)
			m.quotes[v.Book.Symbol] = q
		}
		return []staged{{Topic: wsmsg.TopicBook, Payload: b}}
	case md.TapeUpdate:
		out := make([]wsmsg.Tick, 0, len(v.Ticks))
		for _, t := range v.Ticks {
			wt := mapTick(t)
			out = append(out, wt)
			m.marks[t.Symbol] = t.Price
		}
		m.appendTape(v.Symbol, out)
		if len(out) == 0 {
			return nil
		}
		return []staged{{Topic: wsmsg.TopicTape, Payload: out}}
	case md.BarUpdate:
		wb := mapBar(v.Bar)
		m.upsertBar(wb)
		m.marks[wb.Symbol] = wb.C
		return []staged{{Topic: wsmsg.TopicBars, Payload: wb}}
	case md.IndicatorUpdate:
		return m.applyIndicator(v)
	default:
		return nil // MismatchUpdate/ConnUpdate/ResyncedUpdate are handled by main->sys.events, not topics
	}
}

func (m *mirror) topOfBook(symbol string) (bid, ask float64) {
	b, ok := m.books[symbol]
	if !ok {
		return 0, 0
	}
	if len(b.Bids) > 0 {
		bid = b.Bids[0].Price
	}
	if len(b.Asks) > 0 {
		ask = b.Asks[0].Price
	}
	return bid, ask
}

func (m *mirror) appendTape(symbol string, ticks []wsmsg.Tick) {
	r := append(m.tape[symbol], ticks...)
	if len(r) > m.tapeCap {
		r = r[len(r)-m.tapeCap:]
	}
	m.tape[symbol] = r
}

func (m *mirror) upsertBar(b wsmsg.Bar) {
	k := barKey(b.Symbol, b.Timeframe)
	series := m.bars[k]
	for i := range series {
		if series[i].BucketStart == b.BucketStart {
			series[i] = b
			m.bars[k] = series
			return
		}
	}
	series = append(series, b)
	sort.Slice(series, func(i, j int) bool { return series[i].BucketStart < series[j].BucketStart })
	m.bars[k] = series
}

func (m *mirror) applyIndicator(v md.IndicatorUpdate) []staged {
	key := v.SeriesKey
	if v.Snapshot {
		pts := make([]wsmsg.IndicatorPoint, len(v.Points))
		for i, p := range v.Points {
			pts[i] = mapIndicatorPoint(p)
		}
		m.indicators[key] = pts
		return []staged{{Topic: wsmsg.TopicIndicator, Key: key, Payload: pts, Snap: true}}
	}
	if len(v.Points) == 0 {
		return nil
	}
	p := mapIndicatorPoint(v.Points[len(v.Points)-1])
	m.indicators[key] = append(m.indicators[key], p)
	return []staged{{Topic: wsmsg.TopicIndicator, Key: key, Payload: p}}
}

// applyExec updates execution state and returns delta frames to broadcast.
func (m *mirror) applyExec(u exec.Update) []staged {
	switch v := u.(type) {
	case exec.OrderUpdate:
		w := mapOrder(v.Order)
		m.orders[w.ID] = w
		return []staged{{Topic: wsmsg.TopicExecOrders, Payload: w}}
	case exec.FillUpdate:
		w := mapFill(v.Fill)
		m.fills = append(m.fills, w)
		if len(m.fills) > m.fillsCap {
			m.fills = m.fills[len(m.fills)-m.fillsCap:]
		}
		return []staged{{Topic: wsmsg.TopicExecFills, Payload: w}}
	case exec.PositionUpdate:
		m.positions[string(v.Position.Venue)+"|"+v.Position.Symbol] = v.Position
		return []staged{{Topic: wsmsg.TopicExecPositions, Payload: m.positionsPayload()}}
	case exec.AccountUpdate:
		a := mapAccount(v.Account)
		m.accounts[a.Venue] = a
		m.masterArmed = v.MasterArmed
		if vs := m.venueStatus[a.Venue]; vs != nil {
			vs.VenueArmed = v.VenueArmed
		}
		return []staged{
			{Topic: wsmsg.TopicExecAccount, Key: a.Venue, Payload: a},
			{Topic: wsmsg.TopicExecStatus, Payload: m.execStatus()},
		}
	case exec.StatusUpdate:
		m.masterArmed = v.MasterArmed
		if vs := m.venueStatus[string(v.Venue)]; vs != nil {
			vs.Connected = v.Connected
			vs.Note = v.Note
		}
		return []staged{{Topic: wsmsg.TopicExecStatus, Payload: m.execStatus()}}
	default:
		return nil
	}
}

func (m *mirror) positionsPayload() []wsmsg.PositionRow {
	rows := make([]wsmsg.PositionRow, 0, len(m.positions))
	keys := make([]string, 0, len(m.positions))
	for k := range m.positions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := m.positions[k]
		rows = append(rows, mapPosition(p, m.marks[p.Symbol]))
	}
	return rows
}

func (m *mirror) execStatus() wsmsg.ExecStatus {
	vs := make([]wsmsg.VenueStatus, 0, len(m.venueOrder))
	for _, id := range m.venueOrder {
		if s := m.venueStatus[id]; s != nil {
			vs = append(vs, *s)
		}
	}
	return wsmsg.ExecStatus{MasterArmed: m.masterArmed, Global: m.global, Venues: vs}
}

// applyPub records a poller/health/sys event into the mirror (so late subscribers
// get a snapshot). Broadcast of these is event-driven, done by the hub directly.
func (m *mirror) applyPub(s staged) {
	switch s.Topic {
	case wsmsg.TopicScannerRank:
		m.rank[s.Key] = s.Payload.(wsmsg.ScannerRankPayload)
	case wsmsg.TopicNews:
		switch p := s.Payload.(type) {
		case wsmsg.NewsItem:
			m.appendNews(p)
		case []wsmsg.NewsItem:
			for _, it := range p {
				m.appendNews(it)
			}
		}
	case wsmsg.TopicSysHealth:
		m.health = s.Payload.(wsmsg.HealthSnapshot)
	case wsmsg.TopicSysEvents:
		switch p := s.Payload.(type) {
		case wsmsg.SysEvent:
			m.appendEvent(p)
		case []wsmsg.SysEvent:
			for _, it := range p {
				m.appendEvent(it)
			}
		}
	}
	// scanner.hit and config are not mirrored (hit is transient; config is command-served).
}

func (m *mirror) appendNews(it wsmsg.NewsItem) {
	m.news = append(m.news, it)
	if len(m.news) > m.newsCap {
		m.news = m.news[len(m.news)-m.newsCap:]
	}
}

func (m *mirror) appendEvent(e wsmsg.SysEvent) {
	m.events = append(m.events, e)
	if len(m.events) > m.eventsCap {
		m.events = m.events[len(m.events)-m.eventsCap:]
	}
}

// snapshotFrames serializes current state for a new subscriber to `topic`.
func (m *mirror) snapshotFrames(topic wsmsg.Topic) []staged {
	var out []staged
	switch topic {
	case wsmsg.TopicQuote:
		for _, s := range sortedKeys(m.quotes) {
			out = append(out, staged{Topic: topic, Payload: m.quotes[s]})
		}
	case wsmsg.TopicBook:
		for _, s := range sortedKeys(m.books) {
			out = append(out, staged{Topic: topic, Payload: m.books[s]})
		}
	case wsmsg.TopicTape:
		for _, s := range sortedKeysSlice(m.tape) {
			out = append(out, staged{Topic: topic, Payload: append([]wsmsg.Tick(nil), m.tape[s]...)})
		}
	case wsmsg.TopicBars:
		for _, k := range sortedKeysBars(m.bars) {
			out = append(out, staged{Topic: topic, Payload: append([]wsmsg.Bar(nil), m.bars[k]...)})
		}
	case wsmsg.TopicIndicator:
		for _, k := range sortedKeysInd(m.indicators) {
			out = append(out, staged{Topic: topic, Key: k, Payload: append([]wsmsg.IndicatorPoint(nil), m.indicators[k]...)})
		}
	case wsmsg.TopicScannerRank:
		for _, sess := range sortedKeysRank(m.rank) {
			out = append(out, staged{Topic: topic, Key: sess, Payload: m.rank[sess]})
		}
	case wsmsg.TopicNews:
		out = append(out, staged{Topic: topic, Payload: append([]wsmsg.NewsItem(nil), m.news...)})
	case wsmsg.TopicExecAccount:
		for _, v := range m.venueOrder {
			if a, ok := m.accounts[v]; ok {
				out = append(out, staged{Topic: topic, Key: v, Payload: a})
			}
		}
	case wsmsg.TopicExecPositions:
		out = append(out, staged{Topic: topic, Payload: m.positionsPayload()})
	case wsmsg.TopicExecOrders:
		out = append(out, staged{Topic: topic, Payload: m.ordersPayload()})
	case wsmsg.TopicExecFills:
		out = append(out, staged{Topic: topic, Payload: append([]wsmsg.Fill(nil), m.fills...)})
	case wsmsg.TopicExecStatus:
		out = append(out, staged{Topic: topic, Payload: m.execStatus()})
	case wsmsg.TopicSysHealth:
		out = append(out, staged{Topic: topic, Payload: m.health})
	case wsmsg.TopicSysEvents:
		out = append(out, staged{Topic: topic, Payload: append([]wsmsg.SysEvent(nil), m.events...)})
	}
	// scanner.hit and config have no snapshot.
	return out
}

func (m *mirror) ordersPayload() []wsmsg.Order {
	ids := make([]string, 0, len(m.orders))
	for id := range m.orders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]wsmsg.Order, 0, len(ids))
	for _, id := range ids {
		out = append(out, m.orders[id])
	}
	return out
}

// small sorted-key helpers keep snapshot ordering deterministic (test-stable).
func sortedKeys(mp map[string]wsmsg.Quote) []string { return sortedMapKeys(mp) }
func sortedKeysSlice(mp map[string][]wsmsg.Tick) []string {
	ks := make([]string, 0, len(mp))
	for k := range mp {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
func sortedKeysBars(mp map[string][]wsmsg.Bar) []string {
	ks := make([]string, 0, len(mp))
	for k := range mp {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
func sortedKeysInd(mp map[string][]wsmsg.IndicatorPoint) []string {
	ks := make([]string, 0, len(mp))
	for k := range mp {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
func sortedKeysRank(mp map[string]wsmsg.ScannerRankPayload) []string {
	ks := make([]string, 0, len(mp))
	for k := range mp {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// sortedMapKeys returns the keys of a map[string]wsmsg.Quote sorted; the books
// map reuses it via a tiny adapter to avoid generics churn under the repo's Go
// baseline. (If the repo already uses type params elsewhere, replace all the
// sortedKeys* helpers with one generic func[K comparable] — verify at impl time.)
func sortedMapKeys(mp map[string]wsmsg.Quote) []string {
	ks := make([]string, 0, len(mp))
	for k := range mp {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

var _ = strconv.Itoa // retained if venue/int keying is added; remove if unused at impl time
```

> **Simplify at implementation time:** the repeated `sortedKeys*` helpers are verbose. Go 1.26 supports generics and `slices.Sorted(maps.Keys(...))`; the implementer SHOULD collapse them to one generic helper (`func sortedKeysOf[V any](mp map[string]V) []string`) or `slices.Sorted(maps.Keys(mp))`. The verbose form above is written only so the task compiles as-is; treat the collapse as expected cleanup, not a deviation. Likewise drop the `strconv` blank-import guard.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/uihub/ -run Mirror -v && go vet ./internal/uihub/`
Expected: PASS.

- [ ] **Step 5: Refactor the sorted-key helpers to one generic + drop the strconv guard, re-run**

Run: `cd engine && go test ./internal/uihub/ -run Mirror -v && golangci-lint run`
Expected: PASS with no lint findings.

- [ ] **Step 6: Commit**

```bash
git add engine/internal/uihub/mirror.go engine/internal/uihub/mirror_test.go
git commit -m "feat(engine/uihub): state mirror + snapshot builder (bid/ask + exec.status joins)"
```

---

## Task 6: Hub loop + per-class coalescer + broadcast

**Files:**
- Create: `engine/internal/uihub/hub.go`, `engine/internal/uihub/coalesce.go`
- Test: `engine/internal/uihub/hub_test.go`

**Interfaces:**
- Consumes: `clock.Clock` (tickers), `md.Update`, `exec.Update`, `wsmsg`, the `mirror` (Task 5).
- Produces: the `client` interface (`id() uint64`, `enqueue([]byte) bool`, `close()`); `type HubConfig struct{ MDInterval, AccountInterval, PositionInterval time.Duration; Buf int }`; `NewHub(clk, HubConfig, *mirror) *Hub`; `(*Hub).Run(ctx) error`; `(*Hub).Register(client)`, `(*Hub).Unregister(client)`, `(*Hub).Subscribe(client, wsmsg.Topic)`, `(*Hub).Unsubscribe(client, wsmsg.Topic)`; `(*Hub).PublishMD(md.Update)`, `(*Hub).PublishExec(exec.Update)`, `(*Hub).Publish(wsmsg.Topic, string, any)` (satisfies the pollers' `Publisher`). The `Hub.Run` goroutine is the **single owner** of the mirror, the client set, and all coalescer buffers — no locks.

**Design (locked decision #5):** on `PublishMD`/`PublishExec` the loop applies to the mirror, then classifies each returned `staged`: keep-latest md (quote/book/bars) stages on the `MDInterval` ticker; `md.tape` batch-appends on the same ticker; `md.indicator` broadcasts immediately (snapshot- or delta-kind per `Snap`); `exec.account` keep-latest on the `AccountInterval` ticker; `exec.positions` full-replace on the `PositionInterval` ticker (dirty flag); `exec.orders`/`exec.fills`/`exec.status` and every `Publish` (pollers/health/sys) broadcast immediately. **Overflow policy:** `enqueue` returns false when a client's outbound queue is full; the hub `close()`s and drops that client — the browser reconnects and re-subscribes (reconnect = re-snapshot), which is the spec's "drop + force re-sync."

- [ ] **Step 1: Write the failing test**

`engine/internal/uihub/hub_test.go`:

```go
package uihub

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type fakeClient struct {
	mu     sync.Mutex
	nid    uint64
	frames [][]byte
	full   bool
	closed bool
}

func (c *fakeClient) id() uint64 { return c.nid }
func (c *fakeClient) enqueue(b []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.full {
		return false
	}
	c.frames = append(c.frames, append([]byte(nil), b...))
	return true
}
func (c *fakeClient) close() { c.mu.Lock(); c.closed = true; c.mu.Unlock() }
func (c *fakeClient) got() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.frames...)
}

func decodeKindTopic(t *testing.T, b []byte) (kind string, topic string) {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	k, _ := m["kind"].(string)
	tp, _ := m["topic"].(string)
	return k, tp
}

func newTestHub(clk clock.Clock) *Hub {
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 200, 200, 500, 500)
	return NewHub(clk, HubConfig{
		MDInterval: 33 * time.Millisecond, AccountInterval: 250 * time.Millisecond,
		PositionInterval: 100 * time.Millisecond, Buf: 64,
	}, m)
}

func TestHubSubscribeSendsSnapshotThenCoalescedDelta(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	c := &fakeClient{nid: 1}
	h.Register(c)
	// seed a quote before subscribe so the snapshot is non-empty
	h.PublishMD(md.QuoteUpdate{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.47, TsMs: 1}})
	syncHub(h) // barrier: ensure the publish was applied (see helper note)
	h.Subscribe(c, wsmsg.TopicQuote)
	syncHub(h)

	// snapshot should have arrived (kind:snapshot, topic md.quote)
	frames := c.got()
	if len(frames) == 0 {
		t.Fatal("expected a snapshot frame after subscribe")
	}
	k, tp := decodeKindTopic(t, frames[0])
	if k != "snapshot" || tp != "md.quote" {
		t.Fatalf("first frame should be md.quote snapshot, got %s/%s", k, tp)
	}

	// a new quote should NOT broadcast until the md ticker fires (keep-latest coalescing)
	h.PublishMD(md.QuoteUpdate{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.50, TsMs: 2}})
	syncHub(h)
	before := len(c.got())
	clk.Advance(33 * time.Millisecond) // fire md ticker
	syncHub(h)
	after := c.got()
	if len(after) <= before {
		t.Fatalf("expected a coalesced delta after md tick; before=%d after=%d", before, len(after))
	}
	k, tp = decodeKindTopic(t, after[len(after)-1])
	if k != "delta" || tp != "md.quote" {
		t.Fatalf("last frame should be md.quote delta, got %s/%s", k, tp)
	}
}

func TestHubExecOrdersBroadcastImmediately(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	c := &fakeClient{nid: 1}
	h.Register(c)
	h.Subscribe(c, wsmsg.TopicExecOrders)
	syncHub(h)
	base := len(c.got())
	h.PublishExec(exec.OrderUpdate{Order: exec.Order{Venue: "sim", ID: "ET1", Symbol: "US.AAPL", Status: exec.StatusSubmitted}})
	syncHub(h)
	// event-driven: no ticker advance needed
	frames := c.got()
	if len(frames) <= base {
		t.Fatalf("exec.orders must broadcast immediately, got %d frames", len(frames))
	}
	k, tp := decodeKindTopic(t, frames[len(frames)-1])
	if k != "delta" || tp != "exec.orders" {
		t.Fatalf("expected exec.orders delta, got %s/%s", k, tp)
	}
}

func TestHubOverflowClosesClient(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := newTestHub(clk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	c := &fakeClient{nid: 1, full: true} // every enqueue fails
	h.Register(c)
	h.Subscribe(c, wsmsg.TopicExecOrders)
	syncHub(h)
	h.PublishExec(exec.OrderUpdate{Order: exec.Order{Venue: "sim", ID: "ET1", Status: exec.StatusSubmitted}})
	syncHub(h)
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		t.Fatal("a client whose queue is always full must be closed and dropped")
	}
}
```

> **`syncHub` barrier helper:** the hub processes channel messages asynchronously, so tests need a barrier that returns only after all prior sends are drained. Implement `syncHub(h *Hub)` in the test file as a round-trip: `h.Publish(wsmsg.Topic("__sync"), "", nil)` won't work (unknown topic). Instead add a **test-only** synchronous ping to `Hub`: a `syncCh chan chan struct{}`; `syncHub` sends a reply channel and blocks on it, and the `Run` loop answers it. Add this in Step 3 (a `func (h *Hub) sync()` guarded for tests via a `_test.go`-only export, or a plain unexported method the in-package test can call). Because `hub_test.go` is `package uihub` (internal), call `h.sync()` directly and name the helper `syncHub(h) { h.sync() }`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/uihub/ -run Hub -v`
Expected: FAIL — undefined `NewHub`/`Hub`/`HubConfig`.

- [ ] **Step 3: Write `coalesce.go` (classification helpers)**

`engine/internal/uihub/coalesce.go`:

```go
package uihub

import "github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

// dedupOf computes the keep-latest coalescing key for a staged md/account frame.
func dedupOf(s staged) string {
	switch p := s.Payload.(type) {
	case wsmsg.Quote:
		return "q|" + p.Symbol
	case wsmsg.Book:
		return "b|" + p.Symbol
	case wsmsg.Bar:
		return "bar|" + p.Symbol + "|" + p.Timeframe + "|" + p.BucketStart
	case wsmsg.AccountRow:
		return "acct|" + p.Venue
	default:
		return string(s.Topic) + "|" + s.Key
	}
}

// coalesceClass buckets a staged frame into how the hub should flush it.
type coalesceClass int

const (
	classMDKeep    coalesceClass = iota // quote/book/bars -> md ticker, keep-latest by dedup
	classTape                           // md.tape -> md ticker, batch-append
	classAccount                        // exec.account -> account ticker, keep-latest by venue
	classPositions                      // exec.positions -> position ticker, full-replace
	classImmediate                      // everything else -> broadcast now
)

func classify(topic wsmsg.Topic) coalesceClass {
	switch topic {
	case wsmsg.TopicQuote, wsmsg.TopicBook, wsmsg.TopicBars:
		return classMDKeep
	case wsmsg.TopicTape:
		return classTape
	case wsmsg.TopicExecAccount:
		return classAccount
	case wsmsg.TopicExecPositions:
		return classPositions
	default:
		return classImmediate // indicator, orders, fills, status, scanner.*, news, sys.*
	}
}
```

- [ ] **Step 4: Write `hub.go`**

`engine/internal/uihub/hub.go`:

```go
package uihub

import (
	"context"
	"encoding/json"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// client is the hub's view of a connected UI socket (implemented by *conn, Task 7).
type client interface {
	id() uint64
	enqueue(b []byte) bool // false => outbound queue full; hub closes+drops the client
	close()
}

type HubConfig struct {
	MDInterval       time.Duration
	AccountInterval  time.Duration
	PositionInterval time.Duration
	Buf              int // channel buffer depth for md/exec/pub inbound
}

type subReq struct {
	c     client
	topic wsmsg.Topic
}

type pub struct {
	topic   wsmsg.Topic
	key     string
	payload any
}

type Hub struct {
	clk clock.Clock
	cfg HubConfig
	m   *mirror

	register   chan client
	unregister chan client
	subCh      chan subReq
	unsubCh    chan subReq
	mdCh       chan md.Update
	execCh     chan exec.Update
	pubCh      chan pub
	syncCh     chan chan struct{} // test barrier

	// Run-loop-owned:
	clients   map[client]map[wsmsg.Topic]bool
	pendKeep  map[string]staged            // classMDKeep, flushed on md ticker
	tapePend  map[string][]wsmsg.Tick      // symbol -> accumulated ticks
	acctPend  map[string]staged            // venue -> latest account frame
	posLatest staged
	posDirty  bool
}

func NewHub(clk clock.Clock, cfg HubConfig, m *mirror) *Hub {
	if cfg.Buf <= 0 {
		cfg.Buf = 1024
	}
	return &Hub{
		clk: clk, cfg: cfg, m: m,
		register:   make(chan client),
		unregister: make(chan client),
		subCh:      make(chan subReq),
		unsubCh:    make(chan subReq),
		mdCh:       make(chan md.Update, cfg.Buf),
		execCh:     make(chan exec.Update, cfg.Buf),
		pubCh:      make(chan pub, cfg.Buf),
		syncCh:     make(chan chan struct{}),
		clients:    map[client]map[wsmsg.Topic]bool{},
		pendKeep:   map[string]staged{},
		tapePend:   map[string][]wsmsg.Tick{},
		acctPend:   map[string]staged{},
	}
}

// Public entry points (safe from any goroutine; they only send on channels).
func (h *Hub) Register(c client)                        { h.register <- c }
func (h *Hub) Unregister(c client)                      { h.unregister <- c }
func (h *Hub) Subscribe(c client, t wsmsg.Topic)        { h.subCh <- subReq{c, t} }
func (h *Hub) Unsubscribe(c client, t wsmsg.Topic)      { h.unsubCh <- subReq{c, t} }
func (h *Hub) PublishMD(u md.Update)                    { h.mdCh <- u }
func (h *Hub) PublishExec(u exec.Update)                { h.execCh <- u }
func (h *Hub) Publish(t wsmsg.Topic, key string, p any) { h.pubCh <- pub{t, key, p} }
func (h *Hub) sync()                                    { done := make(chan struct{}); h.syncCh <- done; <-done }

func (h *Hub) Run(ctx context.Context) error {
	mdTick := h.clk.NewTicker(h.cfg.MDInterval)
	acctTick := h.clk.NewTicker(h.cfg.AccountInterval)
	posTick := h.clk.NewTicker(h.cfg.PositionInterval)
	defer mdTick.Stop()
	defer acctTick.Stop()
	defer posTick.Stop()

	for {
		select {
		case <-ctx.Done():
			for c := range h.clients {
				c.close()
			}
			return ctx.Err()
		case c := <-h.register:
			h.clients[c] = map[wsmsg.Topic]bool{}
		case c := <-h.unregister:
			delete(h.clients, c)
			c.close()
		case r := <-h.subCh:
			if subs, ok := h.clients[r.c]; ok {
				subs[r.topic] = true
				h.sendSnapshot(r.c, r.topic)
			}
		case r := <-h.unsubCh:
			if subs, ok := h.clients[r.c]; ok {
				delete(subs, r.topic)
			}
		case u := <-h.mdCh:
			for _, s := range h.m.applyMD(u) {
				h.stageMD(s)
			}
		case u := <-h.execCh:
			for _, s := range h.m.applyExec(u) {
				h.stageExec(s)
			}
		case p := <-h.pubCh:
			s := staged{Topic: p.topic, Key: p.key, Payload: p.payload}
			h.m.applyPub(s)
			h.broadcast(s, false)
		case <-mdTick.C():
			h.flushMD()
		case <-acctTick.C():
			h.flushAcct()
		case <-posTick.C():
			if h.posDirty {
				h.broadcast(h.posLatest, false)
				h.posDirty = false
			}
		case done := <-h.syncCh:
			close(done)
		}
	}
}

func (h *Hub) stageMD(s staged) {
	switch classify(s.Topic) {
	case classTape:
		ticks, _ := s.Payload.([]wsmsg.Tick)
		sym := ""
		if len(ticks) > 0 {
			sym = ticks[0].Symbol
		}
		h.tapePend[sym] = append(h.tapePend[sym], ticks...)
	case classMDKeep:
		h.pendKeep[dedupOf(s)] = s
	default: // indicator: immediate; Snap decides snapshot vs delta
		h.broadcast(s, s.Snap)
	}
}

func (h *Hub) stageExec(s staged) {
	switch classify(s.Topic) {
	case classAccount:
		h.acctPend[dedupOf(s)] = s
	case classPositions:
		h.posLatest = s
		h.posDirty = true
	default: // orders, fills, status
		h.broadcast(s, false)
	}
}

func (h *Hub) flushMD() {
	for k, s := range h.pendKeep {
		h.broadcast(s, false)
		delete(h.pendKeep, k)
	}
	for sym, ticks := range h.tapePend {
		if len(ticks) == 0 {
			continue
		}
		h.broadcast(staged{Topic: wsmsg.TopicTape, Payload: ticks}, false)
		delete(h.tapePend, sym)
	}
}

func (h *Hub) flushAcct() {
	for k, s := range h.acctPend {
		h.broadcast(s, false)
		delete(h.acctPend, k)
	}
}

func (h *Hub) broadcast(s staged, snap bool) {
	var b []byte
	var err error
	if snap {
		b, err = json.Marshal(wsmsg.SnapshotMsg{Kind: "snapshot", Topic: s.Topic, Key: s.Key, Payload: s.Payload})
	} else {
		b, err = json.Marshal(wsmsg.DeltaMsg{Kind: "delta", Topic: s.Topic, Key: s.Key, Payload: s.Payload})
	}
	if err != nil {
		return
	}
	var dead []client
	for c, subs := range h.clients {
		if subs[s.Topic] {
			if !c.enqueue(b) {
				dead = append(dead, c)
			}
		}
	}
	for _, c := range dead {
		delete(h.clients, c)
		c.close()
	}
}

func (h *Hub) sendSnapshot(c client, topic wsmsg.Topic) {
	for _, fr := range h.m.snapshotFrames(topic) {
		b, err := json.Marshal(wsmsg.SnapshotMsg{Kind: "snapshot", Topic: fr.Topic, Key: fr.Key, Payload: fr.Payload})
		if err != nil {
			continue
		}
		if !c.enqueue(b) {
			delete(h.clients, c)
			c.close()
			return
		}
	}
}
```

> The `staged.Snap` field and the `applyIndicator` snapshot branch that sets it were defined in Task 5. `stageMD`'s `default` case (indicator) broadcasts immediately with `s.Snap` so a full-series indicator update reaches subscribers as `kind:"snapshot"` and a single new point as `kind:"delta"`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd engine && go test ./internal/uihub/ -run 'Hub|Mirror|Map' -race -v && golangci-lint run`
Expected: PASS (the `-race` run proves the loop is the single owner: only channel sends cross goroutines).

- [ ] **Step 6: Commit**

```bash
git add engine/internal/uihub/hub.go engine/internal/uihub/coalesce.go engine/internal/uihub/hub_test.go engine/internal/uihub/mirror.go
git commit -m "feat(engine/uihub): hub loop, per-class coalescer, snapshot-then-delta broadcast"
```

---

## Task 7: WebSocket connection (reader/writer, ping, overflow)

**Files:**
- Create: `engine/internal/uihub/conn.go`
- Test: `engine/internal/uihub/conn_test.go`

**Interfaces:**
- Consumes: `github.com/coder/websocket` (already in `go.mod` at v1.8.15 via Plan 5, currently `// indirect`; importing it directly here + `go mod tidy` promotes it to a direct dep — no `go get` needed), `wsmsg`, the `Hub` (Subscribe/Unsubscribe/Unregister), and two handler interfaces.
- Produces: `type commandHandler interface{ handle(name string, args json.RawMessage) wsmsg.AckMsg }`; `type queryHandler interface{ handle(name string, args json.RawMessage) any }`; `type conn struct{...}` implementing `client`; `newConn(id uint64, ws wsSocket, h *Hub, cmd commandHandler, q queryHandler, outBuf int) *conn`; `(*conn).run(ctx)` (starts reader+writer, blocks until either ends); and a small `wsSocket` interface over coder/websocket for test fakes (`Read(ctx) ([]byte, error)`, `Write(ctx, []byte) error`, `Close(code, reason)`).

- [ ] **Step 1: Write the failing test**

`engine/internal/uihub/conn_test.go`:

```go
package uihub

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// fakeSocket is an in-memory wsSocket: reads pop from `in`, writes append to `out`.
type fakeSocket struct {
	in     chan []byte
	mu     sync.Mutex
	out    [][]byte
	closed bool
}

func newFakeSocket() *fakeSocket { return &fakeSocket{in: make(chan []byte, 16)} }
func (s *fakeSocket) Read(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case b, ok := <-s.in:
		if !ok {
			return nil, errors.New("closed")
		}
		return b, nil
	}
}
func (s *fakeSocket) Write(ctx context.Context, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out = append(s.out, append([]byte(nil), b...))
	return nil
}
func (s *fakeSocket) Close(code int, reason string) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}
func (s *fakeSocket) writes() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.out...)
}

type fakeCmd struct{ last string }
func (f *fakeCmd) handle(name string, _ json.RawMessage) wsmsg.AckMsg {
	f.last = name
	return wsmsg.AckMsg{Kind: "ack", Status: "accepted", OrderID: "ET9"}
}

type fakeQuery struct{}
func (fakeQuery) handle(_ string, _ json.RawMessage) any { return []wsmsg.Fill{} }

func TestConnPingPong(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newFakeSocket()
	c := newConn(1, sock, h, &fakeCmd{}, fakeQuery{}, 8)
	go c.run(ctx)

	sock.in <- []byte(`{"kind":"ping","t":123}`)
	waitFor(t, func() bool {
		for _, w := range sock.writes() {
			var m map[string]any
			_ = json.Unmarshal(w, &m)
			if m["kind"] == "pong" && m["t"] == float64(123) {
				return true
			}
		}
		return false
	})
}

func TestConnCommandProducesAck(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newFakeSocket()
	cmd := &fakeCmd{}
	c := newConn(1, sock, h, cmd, fakeQuery{}, 8)
	go c.run(ctx)

	sock.in <- []byte(`{"kind":"command","corrId":"c1","name":"SubmitOrder","args":{}}`)
	waitFor(t, func() bool {
		for _, w := range sock.writes() {
			var m map[string]any
			_ = json.Unmarshal(w, &m)
			if m["kind"] == "ack" && m["corrId"] == "c1" && m["status"] == "accepted" && m["orderId"] == "ET9" {
				return true
			}
		}
		return false
	})
	if cmd.last != "SubmitOrder" {
		t.Fatalf("command not dispatched: %q", cmd.last)
	}
}

func TestConnSubscribeRoutesToHub(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 10, 10, 10, 10)
	h := NewHub(clk, HubConfig{MDInterval: time.Second, AccountInterval: time.Second, PositionInterval: time.Second, Buf: 8}, m)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	sock := newFakeSocket()
	c := newConn(1, sock, h, &fakeCmd{}, fakeQuery{}, 8)
	h.Register(c)
	go c.run(ctx)
	sock.in <- []byte(`{"kind":"subscribe","topic":"exec.status"}`)
	h.sync()
	h.sync() // second barrier: subscribe processed after the reader forwards it
	// exec.status snapshot is always available (assembled aggregate) => a frame should be written
	waitFor(t, func() bool {
		for _, w := range sock.writes() {
			var mm map[string]any
			_ = json.Unmarshal(w, &mm)
			if mm["kind"] == "snapshot" && mm["topic"] == "exec.status" {
				return true
			}
		}
		return false
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/uihub/ -run Conn -v`
Expected: FAIL — undefined `newConn`/`conn`/`wsSocket`.

- [ ] **Step 3: Write `conn.go`**

`engine/internal/uihub/conn.go`:

```go
package uihub

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// wsSocket is the minimal surface over github.com/coder/websocket the conn needs
// (server.go adapts a *websocket.Conn to it; tests supply an in-memory fake).
type wsSocket interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, b []byte) error
	Close(code int, reason string) error
}

type commandHandler interface {
	handle(name string, args json.RawMessage) wsmsg.AckMsg
}

type queryHandler interface {
	handle(name string, args json.RawMessage) any
}

type conn struct {
	nid  uint64
	ws   wsSocket
	hub  *Hub
	cmd  commandHandler
	qry  queryHandler
	out  chan []byte
	once sync.Once
	done chan struct{}
}

func newConn(id uint64, ws wsSocket, h *Hub, cmd commandHandler, q queryHandler, outBuf int) *conn {
	if outBuf <= 0 {
		outBuf = 1024
	}
	return &conn{nid: id, ws: ws, hub: h, cmd: cmd, qry: q, out: make(chan []byte, outBuf), done: make(chan struct{})}
}

func (c *conn) id() uint64 { return c.nid }

// enqueue is called by the hub loop (broadcast/snapshot) AND by this conn's own
// reader (ack/result/pong). Non-blocking: on a full queue it tears the conn down
// and returns false so the hub drops it.
func (c *conn) enqueue(b []byte) bool {
	select {
	case c.out <- b:
		return true
	case <-c.done:
		return false
	default:
		c.close()
		return false
	}
}

func (c *conn) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.ws.Close(1000, "closing")
	})
}

// run starts the writer and reader; returns when either ends. Callers Register
// the conn with the hub before calling run (so snapshots can be delivered).
func (c *conn) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); c.writeLoop(ctx) }()
	c.readLoop(ctx) // blocks
	c.close()
	c.hub.Unregister(c) // clean up hub-side subscription state
	cancel()
	wg.Wait()
}

func (c *conn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case b := <-c.out:
			if err := c.ws.Write(ctx, b); err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *conn) readLoop(ctx context.Context) {
	for {
		b, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		c.dispatch(ctx, b)
		select {
		case <-c.done:
			return
		default:
		}
	}
}

func (c *conn) dispatch(ctx context.Context, b []byte) {
	var head struct {
		Kind   string          `json:"kind"`
		Topic  wsmsg.Topic     `json:"topic"`
		CorrID string          `json:"corrId"`
		Name   string          `json:"name"`
		Args   json.RawMessage `json:"args"`
		T      int64           `json:"t"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return // drop malformed frames silently (matches the UI codec's drop-and-count)
	}
	switch head.Kind {
	case "subscribe":
		if wsmsg.AllTopics[head.Topic] {
			c.hub.Subscribe(c, head.Topic)
		}
	case "unsubscribe":
		if wsmsg.AllTopics[head.Topic] {
			c.hub.Unsubscribe(c, head.Topic)
		}
	case "command":
		ack := c.cmd.handle(head.Name, head.Args)
		ack.Kind = "ack"
		ack.CorrID = head.CorrID
		c.enqueueJSON(ack)
	case "query":
		payload := c.qry.handle(head.Name, head.Args)
		c.enqueueJSON(wsmsg.ResultMsg{Kind: "result", CorrID: head.CorrID, Payload: payload})
	case "ping":
		c.enqueueJSON(wsmsg.PongMsg{Kind: "pong", T: head.T})
	default:
		// unknown kind: ignore
	}
	_ = ctx
}

func (c *conn) enqueueJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.enqueue(b)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/uihub/ -run Conn -race -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/uihub/conn.go engine/internal/uihub/conn_test.go
git commit -m "feat(engine/uihub): per-connection ws reader/writer, ping/pong, overflow drop"
```

---

## Task 8: Command dispatch (order/arm/config/indicator commands → ack)

**Files:**
- Create: `engine/internal/uihub/commands.go`
- Test: `engine/internal/uihub/commands_test.go`

**Interfaces:**
- Consumes: `exec` (Command variants + `CmdAck`), `md` (`IndicatorSpec`/`IndicatorType`), `session` (`Timeframe`), `wsmsg`.
- Produces: interfaces `execDoer{ Do(exec.Command) exec.CmdAck }`, `configStore{ GetConfig(string)(string,bool,error); SetConfig(string,string) }`, `indicatorCtl{ EnsureIndicator(string, md.IndicatorSpec); ReleaseIndicator(string) }`; `type commands struct{...}` implementing `commandHandler`; `newCommands(execDoer, configStore, indicatorCtl) *commands`; wire→domain enum parsers `sideFromWire`/`orderTypeFromWire`/`tifFromWire`. `*exec.Core`, `*store.Store`, `*md.Core` satisfy the three interfaces respectively.

- [ ] **Step 1: Write the failing test**

`engine/internal/uihub/commands_test.go`:

```go
package uihub

import (
	"encoding/json"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
)

type spyExec struct{ last exec.Command; ack exec.CmdAck }

func (s *spyExec) Do(c exec.Command) exec.CmdAck { s.last = c; return s.ack }

type spyCfg struct {
	got    map[string]string
	values map[string]string
}

func (s *spyCfg) GetConfig(k string) (string, bool, error) {
	v, ok := s.values[k]
	return v, ok, nil
}
func (s *spyCfg) SetConfig(k, v string) {
	if s.got == nil {
		s.got = map[string]string{}
	}
	s.got[k] = v
}

type spyInd struct{ ensured, released string }

func (s *spyInd) EnsureIndicator(id string, _ md.IndicatorSpec) { s.ensured = id }
func (s *spyInd) ReleaseIndicator(id string)                    { s.released = id }

func TestCommandsSubmitOrderMapsEnums(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true, OrderID: "ET5"}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{})
	ack := cd.handle("SubmitOrder", json.RawMessage(`{"venue":"sim","symbol":"US.AAPL","side":"SHORT","type":"STOP_LIMIT","tif":"GTC","qty":80,"limitPrice":3.55,"stopPrice":3.6}`))
	if ack.Status != "accepted" || ack.OrderID != "ET5" {
		t.Fatalf("ack wrong: %+v", ack)
	}
	so, ok := ex.last.(exec.SubmitOrder)
	if !ok {
		t.Fatalf("expected exec.SubmitOrder, got %T", ex.last)
	}
	if so.Side != exec.SideShort || so.Type != exec.TypeStopLimit || so.TIF != exec.TIFGTC {
		t.Fatalf("enum parse wrong: %+v", so)
	}
	if so.Qty != 80 || so.LimitPrice != 3.55 || so.StopPrice != 3.6 || string(so.Venue) != "sim" {
		t.Fatalf("field copy wrong: %+v", so)
	}
}

func TestCommandsBlockedPassesReason(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: false, Reason: "R114 gate: max order value"}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{})
	ack := cd.handle("SubmitOrder", json.RawMessage(`{"venue":"sim","symbol":"US.AAPL","side":"BUY","type":"MARKET","tif":"DAY","qty":1}`))
	if ack.Status != "blocked" || ack.Reason != "R114 gate: max order value" {
		t.Fatalf("blocked reason must pass through verbatim: %+v", ack)
	}
}

func TestCommandsKillSwitchAllVenues(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{})
	cd.handle("KillSwitch", json.RawMessage(`{}`)) // no venue => all
	ks, ok := ex.last.(exec.KillSwitch)
	if !ok || ks.Venue != "" {
		t.Fatalf("KillSwitch{} => all venues (empty VenueID), got %T %+v", ex.last, ex.last)
	}
}

func TestCommandsArmMaster(t *testing.T) {
	ex := &spyExec{ack: exec.CmdAck{Accepted: true}}
	cd := newCommands(ex, &spyCfg{}, &spyInd{})
	cd.handle("Arm", json.RawMessage(`{}`))
	if _, ok := ex.last.(exec.Arm); !ok {
		t.Fatalf("expected exec.Arm, got %T", ex.last)
	}
}

func TestCommandsGetSetConfig(t *testing.T) {
	cfg := &spyCfg{values: map[string]string{"theme": `"dark"`}}
	cd := newCommands(&spyExec{}, cfg, &spyInd{})
	get := cd.handle("GetConfig", json.RawMessage(`{"key":"theme"}`))
	if get.Status != "accepted" {
		t.Fatalf("GetConfig should accept: %+v", get)
	}
	raw, ok := get.Value.(json.RawMessage)
	if !ok || string(raw) != `"dark"` {
		t.Fatalf("GetConfig must return stored JSON value verbatim: %v", get.Value)
	}
	set := cd.handle("SetConfig", json.RawMessage(`{"key":"theme","value":"light"}`))
	if set.Status != "accepted" || cfg.got["theme"] != `"light"` {
		t.Fatalf("SetConfig must persist raw JSON value: %+v / %v", set, cfg.got)
	}
}

func TestCommandsIndicatorLifecycle(t *testing.T) {
	ind := &spyInd{}
	cd := newCommands(&spyExec{}, &spyCfg{}, ind)
	cd.handle("SubscribeIndicator", json.RawMessage(`{"instanceId":"i1","symbol":"US.AAPL","timeframe":"1m","type":"VWAP","params":{}}`))
	if ind.ensured != "i1" {
		t.Fatalf("SubscribeIndicator should EnsureIndicator, got %q", ind.ensured)
	}
	cd.handle("UnsubscribeIndicator", json.RawMessage(`{"instanceId":"i1"}`))
	if ind.released != "i1" {
		t.Fatalf("UnsubscribeIndicator should ReleaseIndicator, got %q", ind.released)
	}
}

func TestCommandsUnknownBlocked() {} // placeholder removed below (see note)
```

> Remove the stray `TestCommandsUnknownBlocked` stub before running — it's shown only to remind the implementer to also assert the unknown-command path:
> ```go
> func TestCommandsUnknown(t *testing.T) {
>   cd := newCommands(&spyExec{}, &spyCfg{}, &spyInd{})
>   ack := cd.handle("Nope", json.RawMessage(`{}`))
>   if ack.Status != "blocked" { t.Fatalf("unknown command must block, got %+v", ack) }
> }
> ```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/uihub/ -run Commands -v`
Expected: FAIL — undefined `newCommands`.

- [ ] **Step 3: Write `commands.go`**

`engine/internal/uihub/commands.go`:

```go
package uihub

import (
	"encoding/json"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type execDoer interface {
	Do(exec.Command) exec.CmdAck
}

type configStore interface {
	GetConfig(key string) (string, bool, error)
	SetConfig(key, value string)
}

type indicatorCtl interface {
	EnsureIndicator(id string, spec md.IndicatorSpec)
	ReleaseIndicator(id string)
}

type commands struct {
	ex  execDoer
	cfg configStore
	ind indicatorCtl
}

func newCommands(ex execDoer, cfg configStore, ind indicatorCtl) *commands {
	return &commands{ex: ex, cfg: cfg, ind: ind}
}

func blocked(reason string) wsmsg.AckMsg { return wsmsg.AckMsg{Status: "blocked", Reason: reason} }

func ackFromCmd(a exec.CmdAck) wsmsg.AckMsg {
	status := "accepted"
	if !a.Accepted {
		status = "blocked"
	}
	return wsmsg.AckMsg{Status: status, Reason: a.Reason, OrderID: a.OrderID}
}

func (cd *commands) handle(name string, args json.RawMessage) wsmsg.AckMsg {
	switch name {
	case "SubmitOrder":
		var a wsmsg.SubmitOrderArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		return ackFromCmd(cd.ex.Do(exec.SubmitOrder{
			Venue: exec.VenueID(a.Venue), Symbol: a.Symbol,
			Side: sideFromWire(a.Side), Type: orderTypeFromWire(a.Type), TIF: tifFromWire(a.TIF),
			Qty: a.Qty, LimitPrice: a.LimitPrice, StopPrice: a.StopPrice,
		}))
	case "CancelOrder":
		var a wsmsg.CancelOrderArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		return ackFromCmd(cd.ex.Do(exec.CancelOrder{Venue: exec.VenueID(a.Venue), OrderID: a.OrderID}))
	case "ReplaceOrder":
		var a wsmsg.ReplaceOrderArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		return ackFromCmd(cd.ex.Do(exec.ReplaceOrder{
			Venue: exec.VenueID(a.Venue), OrderID: a.OrderID,
			Qty: a.Qty, LimitPrice: a.LimitPrice, StopPrice: a.StopPrice,
		}))
	case "Flatten":
		var a wsmsg.FlattenArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		return ackFromCmd(cd.ex.Do(exec.Flatten{Venue: exec.VenueID(a.Venue)}))
	case "KillSwitch":
		var a wsmsg.KillSwitchArgs
		_ = json.Unmarshal(args, &a) // empty ok => all venues
		return ackFromCmd(cd.ex.Do(exec.KillSwitch{Venue: exec.VenueID(a.Venue)}))
	case "Arm":
		var a wsmsg.ArmArgs
		_ = json.Unmarshal(args, &a)
		return ackFromCmd(cd.ex.Do(exec.Arm{Venue: exec.VenueID(a.Venue)}))
	case "Disarm":
		var a wsmsg.ArmArgs
		_ = json.Unmarshal(args, &a)
		return ackFromCmd(cd.ex.Do(exec.Disarm{Venue: exec.VenueID(a.Venue)}))
	case "GetConfig":
		var a wsmsg.GetConfigArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		v, ok, err := cd.cfg.GetConfig(a.Key)
		if err != nil {
			return blocked("config read error")
		}
		if !ok {
			return wsmsg.AckMsg{Status: "accepted"} // absent key => accepted with no value
		}
		return wsmsg.AckMsg{Status: "accepted", Value: json.RawMessage(v)}
	case "SetConfig":
		var a wsmsg.SetConfigArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return blocked("bad args")
		}
		cd.cfg.SetConfig(a.Key, string(a.Value))
		return wsmsg.AckMsg{Status: "accepted"}
	case "SubscribeIndicator":
		var a struct {
			InstanceID string             `json:"instanceId"`
			Symbol     string             `json:"symbol"`
			Timeframe  string             `json:"timeframe"`
			Type       string             `json:"type"`
			Params     map[string]float64 `json:"params"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.InstanceID == "" {
			return blocked("bad args")
		}
		cd.ind.EnsureIndicator(a.InstanceID, md.IndicatorSpec{
			Symbol: a.Symbol, TF: session.Timeframe(a.Timeframe),
			Type: md.IndicatorType(a.Type), Params: a.Params,
		})
		return wsmsg.AckMsg{Status: "accepted"}
	case "UnsubscribeIndicator":
		var a struct {
			InstanceID string `json:"instanceId"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.InstanceID == "" {
			return blocked("bad args")
		}
		cd.ind.ReleaseIndicator(a.InstanceID)
		return wsmsg.AckMsg{Status: "accepted"}
	case "FocusGroup":
		// Link-group focus is UI-local (BroadcastChannel); the engine acks and no-ops.
		return wsmsg.AckMsg{Status: "accepted"}
	default:
		return blocked("unknown command: " + name)
	}
}

func sideFromWire(s wsmsg.Side) exec.Side {
	switch s {
	case wsmsg.SideSell:
		return exec.SideSell
	case wsmsg.SideShort:
		return exec.SideShort
	case wsmsg.SideCover:
		return exec.SideCover
	default:
		return exec.SideBuy
	}
}

func orderTypeFromWire(t wsmsg.OrderType) exec.OrderType {
	switch t {
	case wsmsg.OrderLimit:
		return exec.TypeLimit
	case wsmsg.OrderStop:
		return exec.TypeStop
	case wsmsg.OrderStopLimit:
		return exec.TypeStopLimit
	default:
		return exec.TypeMarket
	}
}

func tifFromWire(t wsmsg.TIF) exec.TIF {
	switch t {
	case wsmsg.TIFGTC:
		return exec.TIFGTC
	case wsmsg.TIFIOC:
		return exec.TIFIOC
	case wsmsg.TIFFOK:
		return exec.TIFFOK
	default:
		return exec.TIFDay
	}
}
```

> **Verified (2026-07-06 pass):** the `SubscribeIndicator` arg shape (`instanceId`/`symbol`/`timeframe`/`type`/`params` with `params: Record<string,number>`) matches exactly what `ui/src/render/chart/ChartController.ts` sends — no change needed. `exec.TypeStop`/`exec.TypeStopLimit` require Plan 5 merged (Task 1 of Plan 5 adds them) — this task will not compile until Plan 5 lands.

- [ ] **Step 4: Run tests to verify they pass** (delete the `TestCommandsUnknownBlocked` stub, add the `TestCommandsUnknown` from the note)

Run: `cd engine && go test ./internal/uihub/ -run Commands -v && golangci-lint run`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/uihub/commands.go engine/internal/uihub/commands_test.go
git commit -m "feat(engine/uihub): command dispatch (orders/arm/config/indicator -> ack)"
```

---

## Task 9: Query dispatch (QueryFills → result)

**Files:**
- Create: `engine/internal/uihub/query.go`
- Test: `engine/internal/uihub/query_test.go`

**Interfaces:**
- Consumes: `exec` (`FillRow`), `wsmsg`.
- Produces: interface `fillsQuerier{ QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error) }` (`*store.Store` satisfies it); `type queries struct{...}` implementing `queryHandler`; `newQueries(fillsQuerier) *queries`; `fillRowToWire(exec.FillRow) wsmsg.Fill`.

- [ ] **Step 1: Write the failing test**

`engine/internal/uihub/query_test.go`:

```go
package uihub

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type spyFills struct {
	rows []exec.FillRow
	err  error
	sym  string
}

func (s *spyFills) QueryFills(symbol string, _, _ int64) ([]exec.FillRow, error) {
	s.sym = symbol
	return s.rows, s.err
}

func TestQueryFillsReturnsFills(t *testing.T) {
	f := &spyFills{rows: []exec.FillRow{{OrderID: "ET1", Symbol: "US.AAPL", Side: "BUY", Qty: 100, Price: 3.47, TsMs: 5, Venue: "sim"}}}
	q := newQueries(f)
	out := q.handle("QueryFills", json.RawMessage(`{"symbol":"US.AAPL","fromMs":0,"toMs":9}`))
	fills, ok := out.([]wsmsg.Fill)
	if !ok || len(fills) != 1 {
		t.Fatalf("expected []wsmsg.Fill of len 1, got %T %v", out, out)
	}
	if fills[0].Side != wsmsg.SideBuy || fills[0].OrderID != "ET1" || f.sym != "US.AAPL" {
		t.Fatalf("fill map wrong: %+v (queried %q)", fills[0], f.sym)
	}
}

func TestQueryFillsEmptyOnError(t *testing.T) {
	q := newQueries(&spyFills{err: errors.New("boom")})
	out := q.handle("QueryFills", json.RawMessage(`{"symbol":"X","fromMs":0,"toMs":1}`))
	if fills, ok := out.([]wsmsg.Fill); !ok || len(fills) != 0 {
		t.Fatalf("error must yield empty []wsmsg.Fill (never nil/hang): %T %v", out, out)
	}
}

func TestQueryUnknownReturnsEmptySlice(t *testing.T) {
	q := newQueries(&spyFills{})
	out := q.handle("Nope", json.RawMessage(`{}`))
	// must be a non-nil, JSON-marshals-to-[] value so the UI promise resolves to []
	b, _ := json.Marshal(out)
	if string(b) != "[]" {
		t.Fatalf("unknown query must resolve to []; marshaled to %s", b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/uihub/ -run Query -v`
Expected: FAIL — undefined `newQueries`.

- [ ] **Step 3: Write `query.go`**

`engine/internal/uihub/query.go`:

```go
package uihub

import (
	"encoding/json"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type fillsQuerier interface {
	QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error)
}

type queries struct {
	fills fillsQuerier
}

func newQueries(f fillsQuerier) *queries { return &queries{fills: f} }

func fillRowToWire(r exec.FillRow) wsmsg.Fill {
	return wsmsg.Fill{
		Venue: r.Venue, OrderID: r.OrderID, Symbol: r.Symbol,
		Side: wsmsg.Side(r.Side), Qty: r.Qty, Price: r.Price, TsMs: r.TsMs,
	}
}

func (q *queries) handle(name string, args json.RawMessage) any {
	switch name {
	case "QueryFills":
		var a wsmsg.QueryFillsArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return []wsmsg.Fill{}
		}
		rows, err := q.fills.QueryFills(a.Symbol, a.FromMs, a.ToMs)
		if err != nil {
			return []wsmsg.Fill{}
		}
		out := make([]wsmsg.Fill, 0, len(rows))
		for _, r := range rows {
			out = append(out, fillRowToWire(r))
		}
		return out
	default:
		return []any{} // unknown query -> resolves to [] on the UI, never hangs
	}
}
```

> Verified (2026-07-06 pass): `exec.FillRow.Side` is populated via `Side.String()` (`internal/exec/events.go`), which returns exactly `"BUY"`/`"SELL"`/`"SHORT"`/`"COVER"` — so `wsmsg.Side(r.Side)` is safe as written; no normalizer needed.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/uihub/ -run Query -v && golangci-lint run`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/uihub/query.go engine/internal/uihub/query_test.go
git commit -m "feat(engine/uihub): query dispatch (QueryFills -> result, empty on miss)"
```

---

## Task 10: HTTP/WS server (static `ui/dist` + `/ws` upgrade)

**Files:**
- Create: `engine/internal/uihub/server.go`
- Test: `engine/internal/uihub/server_test.go`

**Interfaces:**
- Consumes: `net/http`, `github.com/coder/websocket`, the `Hub`, `commandHandler`, `queryHandler`.
- Produces: `type ServerConfig struct{ DistDir string; OutBuf int }`; `NewServer(*Hub, commandHandler, queryHandler, ServerConfig) *Server`; `(*Server).Handler() http.Handler` (mux: `/ws` upgrade + static SPA fallback); an internal `coderSocket` adapting `*websocket.Conn` to `wsSocket`.

- [ ] **Step 1: Write the failing test**

`engine/internal/uihub/server_test.go`:

```go
package uihub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// minimal fakes so the server test doesn't need real cores.
type doerNoop struct{}

func (doerNoop) Do(exec.Command) exec.CmdAck { return exec.CmdAck{Accepted: true} }

type cfgNoop struct{}

func (cfgNoop) GetConfig(string) (string, bool, error) { return "", false, nil }
func (cfgNoop) SetConfig(string, string)               {}

type indNoop struct{}

func (indNoop) EnsureIndicator(string, md.IndicatorSpec) {}
func (indNoop) ReleaseIndicator(string)                  {}

func TestServerWSSubscribeSnapshot(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	// Build a hub with a mirror via the exported constructor path used by main.
	h, m := uihub.NewHubForTest(clk) // see note: a tiny test constructor exported in server_test-support
	_ = m
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	srv := uihub.NewServer(h,
		uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{}),
		uihub.NewQueriesForTest(fillsNoop{}),
		uihub.ServerConfig{OutBuf: 32})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// exec.status always has an assembled snapshot
	sub, _ := json.Marshal(wsmsg.SubscribeMsg{Kind: "subscribe", Topic: wsmsg.TopicExecStatus})
	if err := c.Write(ctx, websocket.MessageText, sub); err != nil {
		t.Fatal(err)
	}
	rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
	defer rcancel()
	_, data, err := c.Read(rctx)
	if err != nil {
		t.Fatal(err)
	}
	var mm map[string]any
	_ = json.Unmarshal(data, &mm)
	if mm["kind"] != "snapshot" || mm["topic"] != "exec.status" {
		t.Fatalf("expected exec.status snapshot, got %v", mm)
	}
}

type fillsNoop struct{}

func (fillsNoop) QueryFills(string, int64, int64) ([]exec.FillRow, error) { return nil, nil }

func TestServerStaticFileServing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>etape</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.UnixMilli(0))
	h, _ := uihub.NewHubForTest(clk)
	srv := uihub.NewServer(h,
		uihub.NewCommandsForTest(doerNoop{}, cfgNoop{}, indNoop{}),
		uihub.NewQueriesForTest(fillsNoop{}),
		uihub.ServerConfig{DistDir: dir, OutBuf: 32})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("static index should 200, got %d", resp.StatusCode)
	}
	// SPA fallback: an unknown non-file path also returns index.html
	resp2, _ := http.Get(ts.URL + "/trading")
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("SPA fallback should serve index.html for /trading, got %d", resp2.StatusCode)
	}
}
```

> **Test-support exports:** the server test is `package uihub_test` (external) so it can dial a real WS, but it needs to build a hub/commands/queries whose real constructors take unexported interfaces. Add a tiny `export_test.go` (`package uihub`) exposing: `func NewHubForTest(clk clock.Clock) (*Hub, *mirror)`, `func NewCommandsForTest(ex execDoer, c configStore, i indicatorCtl) commandHandler`, `func NewQueriesForTest(f fillsQuerier) queryHandler`. This is the standard stdlib whitebox-support idiom (same pattern Plan 4 used with `export_test.go`). Inside, `NewHubForTest` builds a mirror with nil venues and calls `NewHub` with second-scale intervals.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/uihub/ -run Server -v`
Expected: FAIL — undefined `NewServer`/`NewHubForTest`.

- [ ] **Step 3: Write `server.go` (+ `export_test.go` support)**

`engine/internal/uihub/server.go`:

```go
package uihub

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/coder/websocket"
)

type ServerConfig struct {
	DistDir string // built ui/dist; empty => no static serving (dev proxies /ws)
	OutBuf  int    // per-connection outbound queue depth
}

type Server struct {
	hub    *Hub
	cmd    commandHandler
	qry    queryHandler
	cfg    ServerConfig
	nextID atomic.Uint64
}

func NewServer(h *Hub, cmd commandHandler, qry queryHandler, cfg ServerConfig) *Server {
	if cfg.OutBuf <= 0 {
		cfg.OutBuf = 1024
	}
	return &Server{hub: h, cmd: cmd, qry: qry, cfg: cfg}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.serveWS)
	if s.cfg.DistDir != "" {
		mux.Handle("/", s.spaHandler(s.cfg.DistDir))
	}
	return mux
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	// Localhost app: accept same-origin plus the Vite dev origin. InsecureSkipVerify
	// is acceptable because the server binds 127.0.0.1 only (see main).
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c.SetReadLimit(1 << 20) // 1 MiB frame cap
	id := s.nextID.Add(1)
	conn := newConn(id, coderSocket{c: c}, s.hub, s.cmd, s.qry, s.cfg.OutBuf)
	s.hub.Register(conn)
	conn.run(r.Context()) // blocks until the socket closes; run() calls hub.Unregister
}

// spaHandler serves files from dir, falling back to index.html for unknown paths.
func (s *Server) spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, index)
	})
}

// coderSocket adapts *websocket.Conn to the wsSocket interface conn expects.
type coderSocket struct {
	c *websocket.Conn
}

func (s coderSocket) Read(ctx context.Context) ([]byte, error) {
	_, b, err := s.c.Read(ctx)
	return b, err
}

func (s coderSocket) Write(ctx context.Context, b []byte) error {
	return s.c.Write(ctx, websocket.MessageText, b)
}

func (s coderSocket) Close(code int, reason string) error {
	return s.c.Close(websocket.StatusCode(code), reason)
}
```

`engine/internal/uihub/export_test.go`:

```go
package uihub

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func NewHubForTest(clk clock.Clock) (*Hub, *mirror) {
	m := newMirror(nil, wsmsg.GlobalLimitsView{}, 200, 200, 500, 500)
	h := NewHub(clk, HubConfig{
		MDInterval: 20 * time.Millisecond, AccountInterval: 250 * time.Millisecond,
		PositionInterval: 100 * time.Millisecond, Buf: 256,
	}, m)
	return h, m
}

func NewCommandsForTest(ex execDoer, c configStore, i indicatorCtl) commandHandler {
	return newCommands(ex, c, i)
}

func NewQueriesForTest(f fillsQuerier) queryHandler { return newQueries(f) }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd engine && go test ./internal/uihub/... -race -v && go vet ./internal/uihub/... && golangci-lint run`
Expected: PASS (full uihub package green under `-race`).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/uihub/server.go engine/internal/uihub/export_test.go engine/internal/uihub/server_test.go
git commit -m "feat(engine/uihub): http/ws server (static SPA + /ws upgrade)"
```

---

## Task 11: Scan poller (pre-market/RTH rank + float universe)

**Files:**
- Create: `engine/internal/scan/scan.go`
- Test: `engine/internal/scan/scan_test.go`

**Interfaces:**
- Consumes: `opend.Client.Request` (via a `requester` interface), the `pb/qotgetuspremarketrank`, `pb/qotstockfilter`, `pb/qotgetsecuritysnapshot` bindings, `google.golang.org/protobuf/proto`, `wsmsg`, `config.Scan`, `clock.Clock`, `session` (phase), and a `Publisher` (`Publish(topic, key, payload)` — the `Hub`).
- Produces: `type Publisher interface{ Publish(wsmsg.Topic, string, any) }`; `type requester interface{ Request(ctx, protoID uint32, req proto.Message) (opend.Frame, error) }`; `New(cfg config.Scan, r requester, pub Publisher, clk clock.Clock) *Poller`; `(*Poller).Run(ctx) error`; pure transform `rankRows(items, universe map[string]float64, cfg) []wsmsg.ScannerRow`.

**Design (locked decision #7 for news; scanner mechanics from the scanner API doc):** warm-up loads the low-float universe (3215, `FLOAT_SHARE` filter, **thousands→actual** conversion) into a `map[symbol]floatShares`, refreshed every `UniverseRefreshH`. The poll loop rank-queries 3410 (pre-market cadence before 09:30 ET, RTH cadence after), converts each item to a `ScannerRow` (float from the universe; nil if unknown), applies client-side thresholds (`MinChangePct`, `MaxFloatShares`, `MinVolume`), publishes the full ranking as `scanner.rank` (keyed by session), and emits `scanner.hit` for symbols newly qualifying since the last refresh (per-session seen-set, reset at ET midnight).

- [ ] **Step 1: Write the failing test**

`engine/internal/scan/scan_test.go`:

```go
package scan

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestRankRowsFloatUnitAndThresholds(t *testing.T) {
	cfg := config.Scan{MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 100_000}
	// universe stores ACTUAL shares already converted from moomoo thousands.
	universe := map[string]float64{"US.LOWF": 20_000_000, "US.BIGF": 500_000_000}
	items := []rankItem{
		{Symbol: "US.LOWF", ChangePct: 12.5, Last: 4.2, Volume: 300_000}, // passes
		{Symbol: "US.BIGF", ChangePct: 20.0, Last: 8.0, Volume: 900_000}, // fails float cap
		{Symbol: "US.THIN", ChangePct: 30.0, Last: 1.0, Volume: 5_000},   // fails volume floor
		{Symbol: "US.FLAT", ChangePct: 1.0, Last: 2.0, Volume: 500_000},  // fails change threshold
	}
	rows := rankRows(items, universe, cfg)
	if len(rows) != 1 || rows[0].Symbol != "US.LOWF" {
		t.Fatalf("only US.LOWF should pass all thresholds, got %+v", rows)
	}
	if rows[0].FloatShares == nil || *rows[0].FloatShares != 20_000_000 {
		t.Fatalf("float should be actual shares from universe: %+v", rows[0])
	}
	if rows[0].ChangePct == nil || *rows[0].ChangePct != 12.5 {
		t.Fatalf("changePct wrong: %+v", rows[0])
	}
}

func TestRankRowsUnknownFloatIsNilNotZero(t *testing.T) {
	cfg := config.Scan{MinChangePct: 5, MaxFloatShares: 50_000_000, MinVolume: 0}
	rows := rankRows([]rankItem{{Symbol: "US.NEW", ChangePct: 9, Last: 2, Volume: 200_000}}, map[string]float64{}, cfg)
	// Unknown float: keep the row (can't disprove the cap) but floatShares must be nil, not 0.
	if len(rows) != 1 || rows[0].FloatShares != nil {
		t.Fatalf("unknown float must be nil and row retained: %+v", rows)
	}
}

func TestNewHitsSeenSet(t *testing.T) {
	p := &Poller{seen: map[string]map[string]bool{}}
	first := p.newHits("premarket", []wsmsg.ScannerRow{{Symbol: "US.A"}, {Symbol: "US.B"}})
	if len(first) != 2 {
		t.Fatalf("first pass: both are new hits, got %v", first)
	}
	second := p.newHits("premarket", []wsmsg.ScannerRow{{Symbol: "US.A"}, {Symbol: "US.C"}})
	if len(second) != 1 || second[0] != "US.C" {
		t.Fatalf("second pass: only US.C is new, got %v", second)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/scan/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write `scan.go`**

`engine/internal/scan/scan.go`:

```go
// Package scan is the pre-market/RTH rank scanner poller. It issues request/
// response protoIDs (3410 rank, 3215 filter, 3203 snapshot) through the OpenD
// client — no subscription quota — and publishes scanner.rank/scanner.hit.
package scan

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	rankpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetuspremarketrank"
	filterpb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotstockfilter"
)

type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

type requester interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)
}

// rankItem is the poller-internal normalized form of one rank row (decoupled
// from the pb type so the transform is unit-testable without protobuf).
type rankItem struct {
	Symbol    string
	ChangePct float64
	Last      float64
	Volume    int64
}

type Poller struct {
	cfg      config.Scan
	r        requester
	pub      Publisher
	clk      clock.Clock
	universe map[string]float64            // symbol -> actual float shares
	seen     map[string]map[string]bool    // session -> symbol -> seen
	seenDay  int64                          // ET day of the current seen-sets
}

func New(cfg config.Scan, r requester, pub Publisher, clk clock.Clock) *Poller {
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk,
		universe: map[string]float64{}, seen: map[string]map[string]bool{}}
}

func (p *Poller) Run(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	p.refreshUniverse(ctx) // best-effort warm-up; logs+continues on error
	uniTick := p.clk.NewTicker(time.Duration(p.cfg.UniverseRefreshH) * time.Hour)
	defer uniTick.Stop()
	// Poll on a short base interval; the effective cadence is session-derived.
	base := p.clk.NewTicker(time.Duration(p.cfg.PremarketMs) * time.Millisecond)
	defer base.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-uniTick.C():
			p.refreshUniverse(ctx)
		case now := <-base.C():
			interval := p.pollInterval(now)
			if now.Sub(last) < interval {
				continue
			}
			last = now
			p.pollOnce(ctx, now)
		}
	}
}

func (p *Poller) pollInterval(now time.Time) time.Duration {
	if session.PhaseAt(now) == session.RTH {
		return time.Duration(p.cfg.RTHMs) * time.Millisecond
	}
	return time.Duration(p.cfg.PremarketMs) * time.Millisecond
}

func (p *Poller) sessionOf(now time.Time) string {
	switch session.PhaseAt(now) {
	case session.RTH:
		return "rth"
	case session.PostMarket:
		return "afterhours"
	default:
		return "premarket"
	}
}

func (p *Poller) pollOnce(ctx context.Context, now time.Time) {
	items, err := p.fetchRank(ctx)
	if err != nil {
		return // transient; next tick retries
	}
	p.resetSeenIfNewDay(now)
	rows := rankRows(items, p.universe, p.cfg)
	sess := p.sessionOf(now)
	p.pub.Publish(wsmsg.TopicScannerRank, sess, wsmsg.ScannerRankPayload{
		RefreshedAt: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Rows:        rows,
	})
	for _, sym := range p.newHits(sess, rows) {
		p.pub.Publish(wsmsg.TopicScannerHit, sess, wsmsg.ScanHitPayload{
			Symbol: sym, At: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		})
	}
}

// rankRows is the pure transform: apply float lookup + client-side thresholds.
func rankRows(items []rankItem, universe map[string]float64, cfg config.Scan) []wsmsg.ScannerRow {
	out := make([]wsmsg.ScannerRow, 0, len(items))
	for _, it := range items {
		if it.ChangePct < cfg.MinChangePct {
			continue
		}
		if cfg.MinVolume > 0 && it.Volume < cfg.MinVolume {
			continue
		}
		var floatPtr *float64
		if f, ok := universe[it.Symbol]; ok {
			if cfg.MaxFloatShares > 0 && f > cfg.MaxFloatShares {
				continue // known float exceeds cap -> reject
			}
			fv := f
			floatPtr = &fv
		}
		cp, lp := it.ChangePct, it.Last
		out = append(out, wsmsg.ScannerRow{
			Symbol: it.Symbol, ChangePct: &cp, Last: &lp, FloatShares: floatPtr, Volume: it.Volume,
		})
	}
	return out
}

func (p *Poller) newHits(sess string, rows []wsmsg.ScannerRow) []string {
	s := p.seen[sess]
	if s == nil {
		s = map[string]bool{}
		p.seen[sess] = s
	}
	var hits []string
	for _, r := range rows {
		if !s[r.Symbol] {
			s[r.Symbol] = true
			hits = append(hits, r.Symbol)
		}
	}
	return hits
}

func (p *Poller) resetSeenIfNewDay(now time.Time) {
	day := session.DayMs(now.UnixMilli())
	if day != p.seenDay {
		p.seenDay = day
		p.seen = map[string]map[string]bool{}
	}
}

// fetchRank issues 3410 and normalizes the response to []rankItem.
func (p *Poller) fetchRank(ctx context.Context) ([]rankItem, error) {
	req := &rankpb.C2S{
		SortDir: proto.Int32(0), // descending = gainers
		Offset:  proto.Int32(0),
		Count:   proto.Int32(35),
	}
	// OpenD request messages wrap the inner C2S in a required outer Request{C2S:...}
	// (proto2 required field) — a bare C2S serializes to different bytes and OpenD
	// rejects it. Confirmed against every merged call site in feed/opend/backfill.go.
	fr, err := p.r.Request(ctx, opend.ProtoQotGetUSPreMarketRank, &rankpb.Request{C2S: req})
	if err != nil {
		return nil, err
	}
	var resp rankpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil {
		return nil, err
	}
	if resp.GetRetType() != 0 { // surface OpenD-side errors instead of looking like "0 rows"
		return nil, fmt.Errorf("rank retType=%d: %s", resp.GetRetType(), resp.GetRetMsg())
	}
	var out []rankItem
	for _, d := range resp.GetS2C().GetDataList() {
		out = append(out, rankItem{
			Symbol:    symbolOf(d.GetSecurity()),
			ChangePct: d.GetPreMarketChangeRatio(),
			Last:      d.GetPreMarketPrice(),
			Volume:    d.GetPreMarketVolume(),
		})
	}
	return out, nil
}

// refreshUniverse loads the low-float universe via 3215 (FLOAT_SHARE is in
// THOUSANDS on the wire; convert to actual shares here, once).
func (p *Poller) refreshUniverse(ctx context.Context) {
	req := &filterpb.C2S{
		Begin:  proto.Int32(0),
		Num:    proto.Int32(200),
		Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security)), // required field (US-only scope)
		BaseFilterList: []*filterpb.BaseFilter{{
			FieldName: proto.Int32(int32(filterpb.StockField_StockField_FloatShare)),
			FilterMin: proto.Float64(0),
			FilterMax: proto.Float64(p.cfg.MaxFloatShares / 1000.0), // actual -> thousands for the request
		}},
	}
	fr, err := p.r.Request(ctx, opend.ProtoQotStockFilter, &filterpb.Request{C2S: req})
	if err != nil {
		return
	}
	var resp filterpb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil || resp.GetRetType() != 0 {
		return
	}
	uni := map[string]float64{}
	for _, d := range resp.GetS2C().GetDataList() {
		sym := symbolOf(d.GetSecurity())
		for _, bd := range d.GetBaseDataList() {
			if bd.GetFieldName() == int32(filterpb.StockField_StockField_FloatShare) {
				uni[sym] = bd.GetValue() * 1000.0 // thousands -> actual
			}
		}
	}
	if len(uni) > 0 {
		p.universe = uni
	}
}
```

Add a `symbolOf` helper (shared with the news poller — put it in `scan.go` and reference from `news` via a tiny local copy, or in a shared `internal/opendutil`; simplest is a copy in each poller since it's three lines):

```go
// symbolOf renders a moomoo Security as eTape's "US.<code>" convention.
func symbolOf(s *qotcommon.Security) string {
	if s == nil {
		return ""
	}
	return "US." + s.GetCode() // US-only scope (CLAUDE.md); Market is always QotMarket_US here
}
```

`qotcommon` is already in the main import block above (used by both `symbolOf` and `refreshUniverse`'s `Market` field).

> **pb names verified against the merged bindings (2026-07-06 verification pass):** the `Request{C2S: ...}` wrapper, the `Market` required field, the `StockField_StockField_FloatShare` enum path, and every response getter (`GetS2C`/`GetDataList`/`GetSecurity`/`GetPreMarketChangeRatio`/`GetPreMarketPrice`/`GetPreMarketVolume`/`GetBaseDataList`/`GetFieldName`/`GetValue`) are confirmed exact. The **transform (`rankRows`/`newHits`) is the reviewable logic and is fully tested**; `fetchRank`/`refreshUniverse` are thin protobuf glue verified against the real bindings (compile) + the capstone/manual run.
>
> **Deferred (flag, not built here):** the `Qot_GetSecuritySnapshot` (3203) per-symbol float **fallback** for symbols absent from the universe is intentionally omitted from v1 to bound scope — such symbols simply get `floatShares: null` (the UI renders "unknown", never a fabricated 0). Add the 3203 batch fallback (with the one-bad-code-fails-the-batch split/retry) as a follow-up. Record this in the execution ledger.

- [ ] **Step 4: Run tests + build against real bindings**

Run: `cd engine && go test ./internal/scan/ -v && go build ./internal/scan/ && go vet ./internal/scan/ && golangci-lint run`
Expected: PASS (build proves the pb getters resolve; unit tests prove the transform).

- [ ] **Step 5: Commit**

```bash
git add engine/internal/scan/
git commit -m "feat(engine/scan): pre-market/RTH rank poller + low-float universe"
```

---

## Task 12: News poller (Qot_GetSearchNews + URL dedup)

**Files:**
- Create: `engine/internal/news/news.go`
- Test: `engine/internal/news/news_test.go`

**Interfaces:**
- Consumes: the same `requester` + `Publisher` shape (define locally), `pb/qotgetsearchnews`, `proto`, `config.News`, `clock.Clock`, `wsmsg`.
- Produces: `New(cfg config.News, r requester, pub Publisher, clk clock.Clock, symbols func() []string) *Poller`; `(*Poller).Run(ctx) error`; pure `normalize(resp, symbol, seenAt string) []wsmsg.NewsItem`; `(*Poller).dedup(items) []wsmsg.NewsItem` (by URL, fallback `symbol|headline`).

**Design (locked decision #7):** poll `Qot_GetSearchNews` (3263) for focused + watchlist symbols; normalize to `NewsItem{symbol, headline, source, url, seen_at}`; dedup by URL (fallback `symbol|headline`); stamp `seen_at` from `clk.Now()` (moomoo `publish_time` is date-only, unreliable for ordering); publish new items as `news.item` deltas.

- [ ] **Step 1: Write the failing test**

`engine/internal/news/news_test.go`:

```go
package news

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestDedupByURL(t *testing.T) {
	p := &Poller{seen: map[string]bool{}}
	in := []wsmsg.NewsItem{
		{Symbol: "US.AAPL", Headline: "A", URL: "http://x/1", SeenAt: "t1"},
		{Symbol: "US.AAPL", Headline: "A", URL: "http://x/1", SeenAt: "t2"}, // dup url
		{Symbol: "US.AAPL", Headline: "B", URL: "", SeenAt: "t3"},           // no url -> symbol|headline key
		{Symbol: "US.AAPL", Headline: "B", URL: "", SeenAt: "t4"},           // dup by fallback key
	}
	out := p.dedup(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 unique items, got %d: %+v", len(out), out)
	}
	// second call: all already seen
	if again := p.dedup(in); len(again) != 0 {
		t.Fatalf("all should be seen on second pass, got %d", len(again))
	}
}

func TestNormalizeStampsSeenAt(t *testing.T) {
	items := normalize([]searchNews{{Title: "PR drops", Source: "Newswire", URL: "http://x/9"}}, "US.AAPL", "2026-07-06T13:31:00.000Z")
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	it := items[0]
	if it.Symbol != "US.AAPL" || it.Headline != "PR drops" || it.Source != "Newswire" || it.URL != "http://x/9" || it.SeenAt != "2026-07-06T13:31:00.000Z" {
		t.Fatalf("normalize wrong: %+v", it)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/news/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write `news.go`**

`engine/internal/news/news.go`:

```go
// Package news is the poll-only news aggregator (Qot_GetSearchNews, 3263). No
// push exists for news; ordering is by engine-stamped seen_at, dedup by URL.
package news

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	newspb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsearchnews"
)

type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

type requester interface {
	Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error)
}

// searchNews is the poller-internal normalized item (decouples the transform
// from the pb type for testing).
type searchNews struct {
	Title  string
	Source string
	URL    string
}

type Poller struct {
	cfg     config.News
	r       requester
	pub     Publisher
	clk     clock.Clock
	symbols func() []string // focused + watchlist symbols to rotate through
	seen    map[string]bool // dedup keys
}

func New(cfg config.News, r requester, pub Publisher, clk clock.Clock, symbols func() []string) *Poller {
	return &Poller{cfg: cfg, r: r, pub: pub, clk: clk, symbols: symbols, seen: map[string]bool{}}
}

func (p *Poller) Run(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	tick := p.clk.NewTicker(time.Duration(p.cfg.WatchMs) * time.Millisecond)
	defer tick.Stop()
	idx := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			syms := p.symbols()
			if len(syms) == 0 {
				continue
			}
			sym := syms[idx%len(syms)]
			idx++
			p.pollSymbol(ctx, sym)
		}
	}
}

func (p *Poller) pollSymbol(ctx context.Context, symbol string) {
	req := &newspb.C2S{
		Keyword:  proto.String(symbol),
		MaxCount: proto.Int32(int32(p.cfg.MaxPerReq)),
	}
	fr, err := p.r.Request(ctx, opend.ProtoQotGetSearchNews, &newspb.Request{C2S: req})
	if err != nil {
		return
	}
	var resp newspb.Response
	if err := proto.Unmarshal(fr.Body, &resp); err != nil || resp.GetRetType() != 0 {
		return
	}
	raw := make([]searchNews, 0)
	for _, n := range resp.GetS2C().GetSearchNewsList() {
		raw = append(raw, searchNews{Title: n.GetTitle(), Source: n.GetSource(), URL: n.GetUrl()})
	}
	seenAt := p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	fresh := p.dedup(normalize(raw, symbol, seenAt))
	if len(fresh) > 0 {
		p.pub.Publish(wsmsg.TopicNews, "", fresh)
	}
}

func normalize(raw []searchNews, symbol, seenAt string) []wsmsg.NewsItem {
	out := make([]wsmsg.NewsItem, 0, len(raw))
	for _, n := range raw {
		out = append(out, wsmsg.NewsItem{
			Symbol: symbol, Headline: n.Title, Source: n.Source, URL: n.URL, SeenAt: seenAt,
		})
	}
	return out
}

func (p *Poller) dedup(items []wsmsg.NewsItem) []wsmsg.NewsItem {
	out := make([]wsmsg.NewsItem, 0, len(items))
	for _, it := range items {
		key := it.URL
		if key == "" {
			key = it.Symbol + "|" + it.Headline
		}
		if p.seen[key] {
			continue
		}
		p.seen[key] = true
		out = append(out, it)
	}
	return out
}
```

> pb names verified (2026-07-06 pass): `newspb.Request{C2S:...}` wrapper, response `Response`/`GetS2C().GetSearchNewsList()`, getters `GetTitle`/`GetSource`/`GetUrl` all exact. `NewsSubType` defaults to ALL if unset — fine for v1. The `symbols func() []string` is supplied by `main` (watchlist ∪ focus).

- [ ] **Step 4: Run tests + build**

Run: `cd engine && go test ./internal/news/ -v && go build ./internal/news/ && golangci-lint run`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/news/
git commit -m "feat(engine/news): Qot_GetSearchNews poller + URL dedup + seen_at stamping"
```

---

## Task 13: Health poller (moomoo probe RTT + sys.health/sys.events)

**Files:**
- Create: `engine/internal/health/health.go`
- Test: `engine/internal/health/health_test.go`

**Interfaces:**
- Consumes: `Publisher`, `clock.Clock`, `config.Health`, `wsmsg`, and a `prober` interface for the moomoo RTT probe + an app-ping RTT source.
- Produces: `type prober interface{ ProbeRTT(ctx) (time.Duration, error) }`; `type pingSource interface{ LastPingRTT() (time.Duration, bool) }`; `New(cfg, pub, clk, prober, pingSource, hasTZ bool) *Poller`; `(*Poller).Run(ctx) error`; `(*Poller).Event(kind, detail string)` (append a `sys.events` item and publish it); pure `buildHealth(uiRTT, moomooRTT *time.Duration, hasTZ bool) wsmsg.HealthSnapshot`.

**Design (locked decision #8):** `sys.health` carries `ui-engine` (app ping/pong RTT) + `engine-moomoo` (OpenD probe RTT); `engine-tz` appears only if a TZ venue is configured. Per-venue broker connectivity is surfaced via `exec.status`, not duplicated here. `Event` is called by main on connects/gaps/quota/auto-disarm to publish `sys.events` (and main also persists them via `store.AppendSysEvent`).

- [ ] **Step 1: Write the failing test**

`engine/internal/health/health_test.go`:

```go
package health

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestBuildHealthStatuses(t *testing.T) {
	ok := 20 * time.Millisecond
	slow := 800 * time.Millisecond
	snap := buildHealth(&ok, &slow, true)
	byLink := map[string]wsmsg.HealthLink{}
	for _, l := range snap.Links {
		byLink[l.Link] = l
	}
	if byLink["ui-engine"].Status != "ok" {
		t.Fatalf("20ms ui-engine should be ok: %+v", byLink["ui-engine"])
	}
	if byLink["engine-moomoo"].Status != "degraded" {
		t.Fatalf("800ms moomoo should be degraded: %+v", byLink["engine-moomoo"])
	}
	if _, hasTZ := byLink["engine-tz"]; !hasTZ {
		t.Fatal("engine-tz link must be present when hasTZ=true")
	}
}

func TestBuildHealthDownWhenNil(t *testing.T) {
	snap := buildHealth(nil, nil, false)
	byLink := map[string]wsmsg.HealthLink{}
	for _, l := range snap.Links {
		byLink[l.Link] = l
	}
	if byLink["engine-moomoo"].Status != "down" || byLink["engine-moomoo"].Ms != nil {
		t.Fatalf("nil RTT => down with null ms: %+v", byLink["engine-moomoo"])
	}
	if _, hasTZ := byLink["engine-tz"]; hasTZ {
		t.Fatal("engine-tz must be absent when hasTZ=false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/health/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write `health.go`**

`engine/internal/health/health.go`:

```go
// Package health emits sys.health (link RTTs) and sys.events (connects/gaps/etc.).
package health

import (
	"context"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type Publisher interface {
	Publish(topic wsmsg.Topic, key string, payload any)
}

type prober interface {
	ProbeRTT(ctx context.Context) (time.Duration, error)
}

type pingSource interface {
	LastPingRTT() (time.Duration, bool)
}

type Poller struct {
	cfg    config.Health
	pub    Publisher
	clk    clock.Clock
	probe  prober
	pings  pingSource
	hasTZ  bool
	seq    int64
}

func New(cfg config.Health, pub Publisher, clk clock.Clock, probe prober, pings pingSource, hasTZ bool) *Poller {
	return &Poller{cfg: cfg, pub: pub, clk: clk, probe: probe, pings: pings, hasTZ: hasTZ}
}

func (p *Poller) Run(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	tick := p.clk.NewTicker(time.Duration(p.cfg.ProbeMs) * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C():
			var mo *time.Duration
			if p.probe != nil {
				if d, err := p.probe.ProbeRTT(ctx); err == nil {
					mo = &d
				}
			}
			var ui *time.Duration
			if p.pings != nil {
				if d, ok := p.pings.LastPingRTT(); ok {
					ui = &d
				}
			}
			p.pub.Publish(wsmsg.TopicSysHealth, "", buildHealth(ui, mo, p.hasTZ))
		}
	}
}

// Event appends and publishes a sys.events item. main also persists it via store.
func (p *Poller) Event(kind, detail string) {
	p.seq++
	p.pub.Publish(wsmsg.TopicSysEvents, "", wsmsg.SysEvent{
		Seq: p.seq, Ts: p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Kind: kind, Detail: detail,
	})
}

func buildHealth(uiRTT, moomooRTT *time.Duration, hasTZ bool) wsmsg.HealthSnapshot {
	links := []wsmsg.HealthLink{
		linkFor("ui-engine", uiRTT),
		linkFor("engine-moomoo", moomooRTT),
	}
	if hasTZ {
		links = append(links, linkFor("engine-tz", nil)) // TZ RTT surfaced later from exec; down until wired
	}
	return wsmsg.HealthSnapshot{Links: links}
}

func linkFor(name string, rtt *time.Duration) wsmsg.HealthLink {
	if rtt == nil {
		return wsmsg.HealthLink{Link: name, Status: "down"}
	}
	ms := float64(rtt.Microseconds()) / 1000.0
	status := "ok"
	switch {
	case ms >= 2000:
		status = "down"
	case ms >= 500:
		status = "degraded"
	}
	return wsmsg.HealthLink{Link: name, Ms: &ms, Status: status}
}
```

> `min`/`avg`/`max` on `HealthLink` are left nil in v1 (single-sample `ms` only); a rolling window is a cheap follow-up. The moomoo `prober` is implemented in `main` (Task 15) as a lightweight `Qot_GetGlobalState` (1002) round-trip on the OpenD client; the `pingSource` is a small counter the uihub updates on ping/pong (v1 may return `false` until app-ping RTT tracking is added — flag it and keep `ui-engine` "down" if unwired).

- [ ] **Step 4: Run tests + build**

Run: `cd engine && go test ./internal/health/ -v && go build ./internal/health/ && golangci-lint run`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/internal/health/
git commit -m "feat(engine/health): sys.health link RTTs + sys.events emitter"
```

---

## Task 14: Wiring API — `uihub.New` + broker factory + gate mapping

**Files:**
- Create: `engine/internal/uihub/api.go` (public constructor building mirror+hub+server from cores)
- Create: `engine/cmd/etape/boot.go` (broker factory + `config.Gate` → `exec.GateConfig` + `config.Venue` → `uihub.VenueMeta`)
- Test: `engine/internal/uihub/api_test.go`, `engine/cmd/etape/boot_test.go`

**Interfaces:**
- Produces (uihub): exported `type VenueMeta`, `GateLimits`, `GlobalLimits`, `Config`, `ExecCore`, `Stores`, `Indicators`; `func New(clk, Config, ExecCore, Stores, Indicators) (*Hub, *Server)`.
- Produces (main): `buildGateConfig(config.Gate) exec.GateConfig`; `type venueBroker struct{ ID exec.VenueID; Broker exec.Broker; Run func(context.Context) error }`; `buildBrokers(cfg config.Config, cr creds.File, clk clock.Clock, replay bool) ([]venueBroker, error)`; `venueMetas(cfg config.Config) []uihub.VenueMeta`.

- [ ] **Step 1: Write the failing tests**

`engine/internal/uihub/api_test.go`:

```go
package uihub_test

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub"
)

type apiExec struct{}

func (apiExec) Do(exec.Command) exec.CmdAck { return exec.CmdAck{Accepted: true} }

type apiStores struct{}

func (apiStores) GetConfig(string) (string, bool, error)              { return "", false, nil }
func (apiStores) SetConfig(string, string)                            {}
func (apiStores) QueryFills(string, int64, int64) ([]exec.FillRow, error) { return nil, nil }

type apiInd struct{}

func (apiInd) EnsureIndicator(string, md.IndicatorSpec) {}
func (apiInd) ReleaseIndicator(string)                  {}

func TestUIHubNewBuildsRunnableHubAndServer(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	h, srv := uihub.New(clk, uihub.Config{
		Venues: []uihub.VenueMeta{{ID: "sim", Broker: "alpaca", Gate: uihub.GateLimits{MaxOrderValue: 1000}}},
		Global: uihub.GlobalLimits{MaxDayLoss: 500},
		MD:     20 * time.Millisecond, Account: 250 * time.Millisecond, Position: 100 * time.Millisecond,
		Buf: 128, TapeCap: 100, NewsCap: 100, FillsCap: 100, EventsCap: 100, OutBuf: 64,
	}, apiExec{}, apiStores{}, apiInd{})
	if h == nil || srv == nil {
		t.Fatal("New must return a hub and server")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()
	// smoke: publish an exec update; no panic, mirror knows the venue for exec.status
	h.PublishExec(exec.StatusUpdate{Venue: "sim", Connected: true})
	h.Publish("sys.events", "", nil) // generic publish path works
}
```

`engine/cmd/etape/boot_test.go`:

```go
package main

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
)

func TestBuildGateConfigMapsVenueAndGlobal(t *testing.T) {
	g := config.Gate{
		Global: config.GateGlobal{MaxDayLoss: 500, MaxSymbolPositionValue: 10000, MaxSymbolPositionShares: 5000},
		Venue:  map[string]config.GateVenue{"sim": {MaxOrderValue: 1000, MaxOpenOrders: 10}},
	}
	gc := buildGateConfig(g)
	if gc.Global.MaxDayLoss != 500 || gc.Global.MaxSymbolPositionValue != 10000 {
		t.Fatalf("global map wrong: %+v", gc.Global)
	}
	vl, ok := gc.Venue["sim"]
	if !ok || vl.MaxOrderValue != 1000 || vl.MaxOpenOrders != 10 {
		t.Fatalf("venue map wrong: %+v", gc.Venue)
	}
}

func TestBuildBrokersReplayIsAllSim(t *testing.T) {
	cfg := config.Config{Venues: []config.Venue{
		{ID: "tz", Broker: "tradezero"}, {ID: "al", Broker: "alpaca"},
	}}
	vbs, err := buildBrokers(cfg, creds.File{}, clock.System{}, true) // replay => sim regardless of Broker
	if err != nil {
		t.Fatal(err)
	}
	if len(vbs) != 2 {
		t.Fatalf("want 2 venue brokers, got %d", len(vbs))
	}
	for _, vb := range vbs {
		if vb.Run != nil {
			t.Fatalf("replay sim brokers need no Run goroutine: %s", vb.ID)
		}
		if !vb.Broker.Capabilities().FlattenAll { // sim reports FlattenAll=true
			t.Fatalf("expected sim broker for %s", vb.ID)
		}
	}
}

func TestBuildBrokersMoomooAndUnknownError(t *testing.T) {
	if _, err := buildBrokers(config.Config{Venues: []config.Venue{{ID: "mm", Broker: "moomoo"}}}, creds.File{}, clock.System{}, false); err == nil {
		t.Fatal("moomoo venue must error (deferred to v1.x)")
	}
	if _, err := buildBrokers(config.Config{Venues: []config.Venue{{ID: "x", Broker: "bogus"}}}, creds.File{}, clock.System{}, false); err == nil {
		t.Fatal("unknown broker must error")
	}
}

func TestBuildBrokersLiveSim(t *testing.T) {
	vbs, err := buildBrokers(config.Config{Venues: []config.Venue{{ID: "sim", Broker: "sim"}}}, creds.File{}, clock.System{}, false)
	if err != nil || len(vbs) != 1 || vbs[0].Run != nil {
		t.Fatalf("live sim venue should build a sim broker with no Run: %v / %+v", err, vbs)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd engine && go test ./internal/uihub/ -run UIHubNew -v; go test ./cmd/etape/ -v`
Expected: FAIL — undefined `uihub.New`, `buildGateConfig`, `buildBrokers`.

- [ ] **Step 3: Write `uihub/api.go`**

`engine/internal/uihub/api.go`:

```go
package uihub

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// ExecCore is the exec.Core surface uihub commands need (satisfied by *exec.Core).
type ExecCore interface {
	Do(exec.Command) exec.CmdAck
}

// Stores is the store surface uihub needs (satisfied by *store.Store).
type Stores interface {
	GetConfig(key string) (string, bool, error)
	SetConfig(key, value string)
	QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error)
}

// Indicators is the md.Core surface uihub needs (satisfied by *md.Core).
type Indicators interface {
	EnsureIndicator(id string, spec md.IndicatorSpec)
	ReleaseIndicator(id string)
}

type GateLimits struct {
	MaxOrderValue     float64
	MaxPositionValue  float64
	MaxPositionShares float64
	MaxOpenOrders     int
}

type GlobalLimits struct {
	MaxDayLoss              float64
	MaxSymbolPositionValue  float64
	MaxSymbolPositionShares float64
}

type VenueMeta struct {
	ID     string
	Broker string
	Gate   GateLimits
}

type Config struct {
	Venues                []VenueMeta
	Global                GlobalLimits
	MD, Account, Position time.Duration
	Buf                   int
	TapeCap, NewsCap      int
	FillsCap, EventsCap   int
	OutBuf                int
	DistDir               string
}

// New builds the mirror, hub, and server from the cores. Caller runs h.Run(ctx)
// and serves srv.Handler(); uses h.PublishMD/PublishExec/Publish for fan-in.
func New(clk clock.Clock, cfg Config, ex ExecCore, st Stores, ind Indicators) (*Hub, *Server) {
	vms := make([]venueMeta, 0, len(cfg.Venues))
	for _, v := range cfg.Venues {
		vms = append(vms, venueMeta{
			ID:     v.ID,
			Broker: wsmsg.Broker(v.Broker),
			Gate: wsmsg.GateLimitsView{
				MaxOrderValue: v.Gate.MaxOrderValue, MaxPositionValue: v.Gate.MaxPositionValue,
				MaxPositionShares: v.Gate.MaxPositionShares, MaxOpenOrders: v.Gate.MaxOpenOrders,
			},
		})
	}
	global := wsmsg.GlobalLimitsView{
		MaxDayLoss: cfg.Global.MaxDayLoss, MaxSymbolPositionValue: cfg.Global.MaxSymbolPositionValue,
		MaxSymbolPositionShares: cfg.Global.MaxSymbolPositionShares,
	}
	m := newMirror(vms, global, cfg.TapeCap, cfg.NewsCap, cfg.FillsCap, cfg.EventsCap)
	h := NewHub(clk, HubConfig{MDInterval: cfg.MD, AccountInterval: cfg.Account, PositionInterval: cfg.Position, Buf: cfg.Buf}, m)
	cmd := newCommands(ex, st, ind)
	qry := newQueries(st)
	srv := NewServer(h, cmd, qry, ServerConfig{DistDir: cfg.DistDir, OutBuf: cfg.OutBuf})
	return h, srv
}
```

- [ ] **Step 4: Write `cmd/etape/boot.go`**

`engine/cmd/etape/boot.go`:

```go
package main

import (
	"context"
	"fmt"

	"github.com/earlisreal/eTape/engine/internal/broker/alpaca"
	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/broker/tradezero"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/uihub"
)

func buildGateConfig(g config.Gate) exec.GateConfig {
	vc := map[exec.VenueID]exec.VenueLimits{}
	for id, v := range g.Venue {
		vc[exec.VenueID(id)] = exec.VenueLimits{
			MaxOrderValue: v.MaxOrderValue, MaxPositionValue: v.MaxPositionValue,
			MaxPositionShares: v.MaxPositionShares, MaxOpenOrders: v.MaxOpenOrders,
		}
	}
	return exec.GateConfig{
		Global: exec.GlobalLimits{
			MaxDayLoss: g.Global.MaxDayLoss, MaxSymbolPositionValue: g.Global.MaxSymbolPositionValue,
			MaxSymbolPositionShares: g.Global.MaxSymbolPositionShares,
		},
		Venue: vc,
	}
}

func venueMetas(cfg config.Config) []uihub.VenueMeta {
	out := make([]uihub.VenueMeta, 0, len(cfg.Venues))
	for _, v := range cfg.Venues {
		gv := cfg.Gate.Venue[v.ID]
		out = append(out, uihub.VenueMeta{
			ID: v.ID, Broker: v.Broker,
			Gate: uihub.GateLimits{
				MaxOrderValue: gv.MaxOrderValue, MaxPositionValue: gv.MaxPositionValue,
				MaxPositionShares: gv.MaxPositionShares, MaxOpenOrders: gv.MaxOpenOrders,
			},
		})
	}
	return out
}

type venueBroker struct {
	ID     exec.VenueID
	Broker exec.Broker
	Run    func(ctx context.Context) // nil for sim; adapters' Run(ctx) returns no error (Plan 5)
}

// buildBrokers constructs one exec.Broker per configured venue. In replay mode
// every venue is a SimBroker (a recorded day has no live broker). In live mode it
// dispatches on Venue.Broker; moomoo is deferred to v1.x (error).
func buildBrokers(cfg config.Config, cr creds.File, clk clock.Clock, replay bool) ([]venueBroker, error) {
	out := make([]venueBroker, 0, len(cfg.Venues))
	for _, v := range cfg.Venues {
		id := exec.VenueID(v.ID)
		if replay {
			out = append(out, venueBroker{ID: id, Broker: sim.New(id, clk)})
			continue
		}
		switch v.Broker {
		case "sim":
			out = append(out, venueBroker{ID: id, Broker: sim.New(id, clk)})
		case "tradezero":
			pair, err := cr.Get(v.Credentials)
			if err != nil {
				return nil, fmt.Errorf("venue %s: %w", v.ID, err)
			}
			a, err := tradezero.New(tradezero.Config{Venue: id, AccountID: v.AccountID, Creds: pair, Clock: clk})
			if err != nil {
				return nil, fmt.Errorf("venue %s: %w", v.ID, err)
			}
			out = append(out, venueBroker{ID: id, Broker: a, Run: a.Run})
		case "alpaca":
			pair, err := cr.Get(v.Credentials)
			if err != nil {
				return nil, fmt.Errorf("venue %s: %w", v.ID, err)
			}
			a, err := alpaca.New(alpaca.Config{Venue: id, Env: v.Env, Creds: pair, Clock: clk})
			if err != nil {
				return nil, fmt.Errorf("venue %s: %w", v.ID, err)
			}
			out = append(out, venueBroker{ID: id, Broker: a, Run: a.Run})
		case "moomoo":
			return nil, fmt.Errorf("venue %s: moomoo trading venue is deferred to v1.x", v.ID)
		default:
			return nil, fmt.Errorf("venue %s: unknown broker %q", v.ID, v.Broker)
		}
	}
	return out, nil
}
```

> **Verified against merged Plan 5 (2026-07-06 seam re-check):** `tradezero.Config{Venue, AccountID, RESTBase, WSURL, Route, Creds, Clock}` and `alpaca.Config{Venue, Env, RESTBase, WSURL, Creds, Clock}` — both `New(Config) (*Adapter, error)` — match exactly; `creds.Load(path) (File, error)`, `creds.DefaultPath()`, `File.Get(key) (Pair, error)`, `Pair{KeyID, SecretKey}` match; `*Adapter` satisfies `exec.Broker` (incl. the merged `Flatten(ctx)`). **One fix applied:** the adapter method is `Run(ctx context.Context)` with **no error return**, so `venueBroker.Run` is `func(context.Context)` (not `func(...) error`) and the Task 15 goroutine calls `run(ctx)` directly — corrected above.
>
> **Minor wire edge (dev-only):** `config.Venue.Broker` accepts `"sim"`, but the wire `wsmsg.Broker` union is `tradezero|alpaca|moomoo` (mirrors the UI's `Broker` type). A venue configured `broker = "sim"` emits `broker: "sim"` in `exec.status`, which is outside the UI union (harmless at runtime — TS is structural — but the UI has no chip for it). This only bites if Earl adds a literal `[[venue]]` with `broker = "sim"` for local testing; replay mode does **not** hit it (`venueMetas` reads the *configured* broker, e.g. `"alpaca"`, while `buildBrokers` swaps the *instance* to sim). Leave as-is for v1; if a first-class sim venue is wanted later, add `"sim"` to both the Go `wsmsg.Broker` consts and the UI `Broker` union.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd engine && go test ./internal/uihub/ ./cmd/etape/ -race -v && golangci-lint run`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add engine/internal/uihub/api.go engine/internal/uihub/api_test.go engine/cmd/etape/boot.go engine/cmd/etape/boot_test.go
git commit -m "feat(engine): uihub.New wiring API + broker factory + gate/venue config mapping"
```

---

## Task 15: `cmd/etape/main.go` full boot sequence + ordered shutdown

**Files:**
- Modify (replace): `engine/cmd/etape/main.go`
- Test: covered by the Task 16 capstone (main is thin glue over already-tested units); a `boot_test.go` compile-smoke of the helpers is added here.

**Interfaces:**
- Consumes: everything above + the existing `store`/`md`/`feed/opend`/`replay`/`config`/`clock`/`creds`/`exec` APIs.
- Produces: the full binary. Boot order (go-engine-design §Boot sequence): **config → store → md.Core → exec (Recover→Run) → uihub (listen) → OpenD/feed (dial) → pre-subscribe → pollers → mark bridge + fan-in**. Shutdown drains **all store writers** (feed pipe *and* exec) before `store.Close()`.

**Design (locked decisions #1/#9 + the Plan 4 shutdown finding):**
- Clocks: `uihubClk = clock.System{}` always (real-time coalescing/streaming); `execClk = replay.Clock` in replay mode, else `clock.System{}` (deterministic order/fill timestamps under replay).
- uihub must be listening before OpenD is dialed, and exec's kill switch works even if OpenD never connects — so exec + uihub are started before the feed, and the feed runs in its own goroutine that never gates exec.
- Shutdown ordering (generalizes the current `pipeWG.Wait()`-then-`Close()`): on ctx cancel → `httpSrv.Shutdown` → `pipeWG.Wait()` (feed→core pipe stopped) → `<-execDone` (exec.Core.Run returned; no more `AppendExecEvent`) → `brokerWG.Wait()` (adapter Run goroutines returned) → `st.Close()`.

- [ ] **Step 1: Write `main.go` (full replacement)**

`engine/cmd/etape/main.go`:

```go
// Command etape is the eTape engine: the full boot sequence wiring the market-
// data plane (OpenD -> feed -> md.Core), the execution subsystem (exec.Core +
// broker venues), and the uihub WebSocket server the UI connects to. With
// --replay it reconstructs a recorded day against SimBroker over the identical
// hub/contract (the mode the UI Playwright E2E boots on).
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/health"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/news"
	"github.com/earlisreal/eTape/engine/internal/replay"
	"github.com/earlisreal/eTape/engine/internal/scan"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/uihub"
)

func main() {
	home, _ := os.UserHomeDir()
	cfgPath := flag.String("config", filepath.Join(home, ".eTape", "config.toml"), "path to config.toml")
	watch := flag.String("watch", "", "comma-separated symbols to watch")
	focus := flag.String("focus", "", "comma-separated symbols to focus (depth + quote)")
	replayDay := flag.String("replay", "", "replay a recorded day (YYYY-MM-DD) instead of live OpenD")
	speed := flag.Float64("speed", 0, "replay speed (>0: real-time x speed; <=0: as fast as possible)")
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

	live := *replayDay == ""
	uihubClk := clock.System{}
	var execClk clock.Clock = clock.System{}

	// --- store ---
	st, err := store.Open(store.Options{
		Path: dbPath, Clock: clock.System{},
		FlushInterval: time.Duration(cfg.Store.FlushMs) * time.Millisecond,
	})
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	// NOTE: st.Close() is deferred until AFTER every store-writer goroutine has
	// stopped (feed pipe + exec.Core) — see the shutdown block below.

	// --- md core ---
	core := md.New(md.Config{TapeRing: cfg.MD.TapeRing, AnchorSecs: anchorSecs})
	go func() { _ = core.Run(ctx) }()

	// --- replay clock (execClk) if replaying ---
	var replayRows []store.JournalRow
	if !live {
		replayRows, err = st.ReadJournalDay(*replayDay)
		if err != nil || len(replayRows) == 0 {
			log.Error("replay day unavailable", "day", *replayDay, "err", err, "rows", len(replayRows))
			_ = st.Close()
			os.Exit(1)
		}
		execClk = replay.NewClock(time.UnixMilli(replayRows[0].TsExch))
	}

	// --- exec subsystem (Recover -> Run) ---
	var credsFile creds.File
	if live {
		if credsFile, err = creds.Load(creds.DefaultPath()); err != nil {
			log.Warn("load creds (non-sim venues will fail)", "err", err)
			credsFile = creds.File{}
		}
	}
	vbs, err := buildBrokers(cfg, credsFile, execClk, !live)
	if err != nil {
		log.Error("build brokers", "err", err)
		_ = st.Close()
		os.Exit(1)
	}
	brokers := map[exec.VenueID]exec.Broker{}
	venueIDs := make([]exec.VenueID, 0, len(vbs))
	var brokerWG sync.WaitGroup
	for _, vb := range vbs {
		brokers[vb.ID] = vb.Broker
		venueIDs = append(venueIDs, vb.ID)
		if vb.Run != nil {
			brokerWG.Add(1)
			go func(run func(context.Context)) { defer brokerWG.Done(); run(ctx) }(vb.Run)
		}
	}
	execCore := exec.NewCore(exec.CoreConfig{
		Venues: venueIDs, Gate: buildGateConfig(cfg.Gate), Store: st,
		Brokers: brokers, Clock: execClk, IDGen: exec.NewOrderIDGen(execClk, rand.Reader),
		SysLog: st.AppendSysEvent,
	})
	if err := execCore.Recover(ctx); err != nil {
		log.Warn("exec recover (continuing; reactive reconcile will catch up)", "err", err)
	}
	execDone := make(chan struct{})
	go func() { defer close(execDone); _ = execCore.Run(ctx) }()

	// --- uihub (listening BEFORE OpenD is dialed) ---
	hub, srv := uihub.New(uihubClk, uihub.Config{
		Venues: venueMetas(cfg), Global: uihub.GlobalLimits{
			MaxDayLoss: cfg.Gate.Global.MaxDayLoss, MaxSymbolPositionValue: cfg.Gate.Global.MaxSymbolPositionValue,
			MaxSymbolPositionShares: cfg.Gate.Global.MaxSymbolPositionShares,
		},
		MD:      hz(cfg.UIHub.MDRateHz), Account: hz(cfg.UIHub.AccountRateHz),
		Position: time.Duration(cfg.UIHub.PositionMs) * time.Millisecond,
		Buf:      4096, TapeCap: cfg.UIHub.TapeSnapshot, NewsCap: 500, FillsCap: 1000, EventsCap: 500,
		OutBuf:   cfg.UIHub.OutboundQueue, DistDir: cfg.UIHub.DistDir,
	}, execCore, st, core)
	go func() { _ = hub.Run(ctx) }()
	httpSrv := &http.Server{Addr: cfg.UIHub.Addr(), Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("uihub listen", "err", err)
		}
	}()
	log.Info("uihub up", "addr", cfg.UIHub.Addr(), "dist", cfg.UIHub.DistDir)

	// --- fan-in: md/exec Updates -> hub; mark bridge md -> exec ---
	go forwardMD(ctx, core, hub, live, st)
	go forwardExec(ctx, execCore, hub)
	go markBridge(ctx, core, execCore)

	// --- feed (live OpenD or replay) ---
	var pipeWG sync.WaitGroup
	var client *opend.Client
	if live {
		if n, err := st.PruneJournal(cfg.Store.RetentionDays); err == nil && n > 0 {
			log.Info("pruned journal", "rows", n)
		}
		st.AppendSysEvent("boot", "engine up")
		client = opend.New(opend.Options{Addr: cfg.OpenD.Addr(), Clock: clock.System{}})
		fd := opend.NewOpenDFeed(client, opend.FeedOptions{
			Budget: cfg.Feed.QuotaSlots, Hysteresis: time.Duration(cfg.Feed.UnsubHysteresisSecs) * time.Second,
			DisableExtendedTime: !cfg.Feed.ExtendedTime,
		})
		go func() { _ = client.Run(ctx) }()
		go func() { _ = fd.Run(ctx) }()
		pipeWG.Add(1)
		go pipe(ctx, &pipeWG, fd.Events(), core, st)
		for _, s := range append(cfg.Feed.Watchlist, splitCSV(*watch)...) {
			fd.Ensure(feed.WatchDemand("boot-watch-"+s, s))
		}
		for _, s := range splitCSV(*focus) {
			fd.Ensure(feed.FocusedDemand("boot-focus-"+s, s))
		}
		startPollers(ctx, cfg, client, hub, uihubClk, st, hasTZVenue(cfg))
	} else {
		sim := execClk.(*replay.Clock)
		fd := replay.NewFeed(replay.FeedOptions{Rows: replayRows, Sim: sim, Pace: clock.System{}, Speed: *speed})
		go func() { _ = fd.Run(ctx) }()
		pipeWG.Add(1)
		go pipe(ctx, &pipeWG, fd.Events(), core, nil)     // no journal re-recording in replay
		go func() { pipeWG.Wait(); stop() }()             // self-terminate when the journal is exhausted
		log.Info("engine up (replay)", "day", *replayDay, "rows", len(replayRows), "speed", *speed)
	}

	<-ctx.Done()

	// --- ordered shutdown: stop accepting, drain all store writers, then Close ---
	shutCtx, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
	_ = httpSrv.Shutdown(shutCtx)
	cancelShut()
	pipeWG.Wait() // feed->core pipe stopped: no more RecordEvent
	<-execDone    // exec.Core.Run returned: no more AppendExecEvent
	brokerWG.Wait()
	if err := st.Close(); err != nil {
		log.Error("close store", "err", err)
	}
	log.Info("shutdown complete", "droppedUpdates", core.DroppedUpdates(), "droppedJournal", st.DroppedJournalRows())
}

func hz(rate float64) time.Duration {
	if rate <= 0 {
		return 33 * time.Millisecond
	}
	return time.Duration(float64(time.Second) / rate)
}

// forwardMD drains md.Core.Updates(): publishes each to the hub and (live only)
// archives finalized 1m/daily bars — merging the old drainUpdates archiving with
// the new hub fan-in.
func forwardMD(ctx context.Context, core *md.Core, hub *uihub.Hub, live bool, archive *store.Store) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-core.Updates():
			hub.PublishMD(u)
			if !live {
				continue
			}
			if bu, ok := u.(md.BarUpdate); ok && !bu.Bar.InProgress {
				b := feed.Bar{Symbol: bu.Bar.Symbol, BucketMs: bu.Bar.BucketMs,
					O: bu.Bar.O, H: bu.Bar.H, L: bu.Bar.L, C: bu.Bar.C, Volume: bu.Bar.V}
				switch bu.Bar.TF {
				case session.TF1m:
					archive.ArchiveBar1m(b)
				case session.TFDay:
					archive.ArchiveDaily(b)
				}
			}
		}
	}
}

func forwardExec(ctx context.Context, execCore *exec.Core, hub *uihub.Hub) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-execCore.Updates():
			hub.PublishExec(u)
		}
	}
}

// markBridge copies md.Core.Marks() -> exec.Core.FeedMark (the single md<->exec seam).
func markBridge(ctx context.Context, core *md.Core, execCore *exec.Core) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-core.Marks():
			execCore.FeedMark(exec.Mark{Symbol: m.Symbol, Price: m.Price, TsMs: m.TsMs})
		}
	}
}

func startPollers(ctx context.Context, cfg config.Config, client *opend.Client, hub *uihub.Hub, clk clock.Clock, st *store.Store, hasTZ bool) {
	symbols := func() []string {
		out := append([]string(nil), cfg.Feed.Watchlist...)
		return out
	}
	go func() { _ = scan.New(cfg.Scan, client, hub, clk).Run(ctx) }()
	go func() { _ = news.New(cfg.News, client, hub, clk, symbols).Run(ctx) }()
	// health: moomoo probe via the OpenD client; app-ping RTT source is nil in v1
	// (ui-engine shows down until ping tracking is wired). The health poller's
	// sys.events are also persisted by main via a store hook if desired.
	go func() { _ = health.New(cfg.Health, hub, clk, moomooProbe{c: client}, nil, hasTZ).Run(ctx) }()
	_ = st // reserved: wire health.Event -> st.AppendSysEvent in a later pass
}

func hasTZVenue(cfg config.Config) bool {
	for _, v := range cfg.Venues {
		if v.Broker == "tradezero" {
			return true
		}
	}
	return false
}

// pipe forwards feed events into the core, journaling each first when journal != nil.
func pipe(ctx context.Context, wg *sync.WaitGroup, in <-chan feed.Event, core *md.Core, journal *store.Store) {
	defer wg.Done()
	sys := clock.System{}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-in:
			if !ok {
				return
			}
			if journal != nil {
				journal.RecordEvent(ev, sys.Now().UnixMilli())
			}
			core.Feed(ev)
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

Add the moomoo health probe to `boot.go`:

```go
// moomooProbe measures OpenD round-trip latency with a lightweight Qot_GetGlobalState.
type moomooProbe struct {
	c *opend.Client
}

func (p moomooProbe) ProbeRTT(ctx context.Context) (time.Duration, error) {
	if p.c == nil {
		return 0, errors.New("no opend client")
	}
	start := time.Now()
	// UserID is a required (deprecated) proto2 field — a zero C2S{} fails to marshal.
	_, err := p.c.Request(ctx, opend.ProtoGetGlobalState,
		&getglobalstate.Request{C2S: &getglobalstate.C2S{UserID: proto.Uint64(0)}})
	return time.Since(start), err
}
```

with imports `getglobalstate "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/getglobalstate"`, `google.golang.org/protobuf/proto`, `time`, `errors`, `opend` (added to `boot.go`'s import block).

> **Verified (2026-07-06 pass):** the `getglobalstate` package path, the `Request{C2S:...}` wrapper, and the required `C2S.UserID` field are all confirmed (a bare `C2S{}` returns `proto: required field ... userID not set` — this fix makes every health probe succeed). Remaining execution notes: (1) `replay.Clock` is a concrete `*replay.Clock` (the `execClk.(*replay.Clock)` assertion holds because the replay branch sets it). (2) The `symbols` closure for news is watchlist-only above — extend to watchlist ∪ focus if focus symbols should also be polled. (3) If wiring `moomooProbe` proves fragile, pass `nil` as the prober (engine-moomoo shows "down") — health still emits `sys.health`; note the choice in the ledger.

- [ ] **Step 2: Build + vet + lint the whole module**

Run: `cd engine && go build ./... && go vet ./... && golangci-lint run`
Expected: PASS (the binary compiles with the full wiring).

- [ ] **Step 3: Manual replay smoke (requires a recorded day in the dev DB)**

Run: `cd engine && go run ./cmd/etape --replay <a-recorded-day> --speed 0` — expect logs `uihub up ...` and `engine up (replay) ...`, then clean self-termination when the journal is exhausted (the capstone in Task 16 automates the assertion). If no recorded day exists locally, skip and rely on Task 16.

- [ ] **Step 4: Commit**

```bash
git add engine/cmd/etape/main.go engine/cmd/etape/boot.go
git commit -m "feat(engine/cmd): full boot sequence (store->md->exec->uihub->feed->pollers) + ordered shutdown"
```

---

## Task 16: Capstone — end-to-end over a real WebSocket (replay feed + SimBroker)

**Files:**
- Create: `engine/internal/uihubtest/e2e_test.go`

**Interfaces:**
- Consumes: `uihub`, `store`, `exec`, `broker/sim`, `md`, `replay`, `feed`, `clock`, `github.com/coder/websocket`, `net/http/httptest`. This leaf package imports the hub + store + broker together (no cycle inside `uihub`).

**What it proves:** the full WS contract end-to-end against the real hub/conn/mirror/coalescer/commands/query — (1) an order lifecycle through `exec.Core` + `SimBroker` streams `exec.orders` `SUBMITTED`→`FILLED` deltas and a `QueryFills` round-trip returns the fill; (2) a recorded journal replayed through `replay.Feed` → `md.Core` → hub streams `md.quote` snapshot-then-delta. This is the mode the UI Plan 6 Playwright E2E boots on.

- [ ] **Step 1: Write the capstone tests**

`engine/internal/uihubtest/e2e_test.go`:

```go
package uihubtest

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/replay"
	"github.com/earlisreal/eTape/engine/internal/store"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "e2e.db"), Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// dialWS opens a client, subscribes to topics, and returns a read helper.
func dialWS(t *testing.T, ctx context.Context, url string, topics ...wsmsg.Topic) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tp := range topics {
		b, _ := json.Marshal(wsmsg.SubscribeMsg{Kind: "subscribe", Topic: tp})
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// waitFrame reads until a frame satisfies pred or the deadline passes.
func waitFrame(t *testing.T, ctx context.Context, c *websocket.Conn, pred func(m map[string]any) bool) map[string]any {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(rctx)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		var m map[string]any
		if json.Unmarshal(data, &m) == nil && pred(m) {
			return m
		}
	}
}

func TestE2EExecLifecycleOverWS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	clk := clock.System{}

	simBroker := sim.New("sim", clk)
	mdCore := md.New(md.Config{TapeRing: 1024, AnchorSecs: 34200})
	go func() { _ = mdCore.Run(ctx) }()

	execCore := exec.NewCore(exec.CoreConfig{
		Venues: []exec.VenueID{"sim"},
		Gate: exec.GateConfig{
			Global: exec.GlobalLimits{MaxDayLoss: 1e9, MaxSymbolPositionValue: 1e9, MaxSymbolPositionShares: 1e9},
			Venue:  map[exec.VenueID]exec.VenueLimits{"sim": {MaxOrderValue: 1e9, MaxPositionValue: 1e9, MaxPositionShares: 1e9, MaxOpenOrders: 100}},
		},
		Store: st, Brokers: map[exec.VenueID]exec.Broker{"sim": simBroker},
		Clock: clk, IDGen: exec.NewOrderIDGen(clk, deterministicReader()),
	})
	if err := execCore.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { _ = execCore.Run(ctx) }()

	hub, srv := uihub.New(clk, uihub.Config{
		Venues:  []uihub.VenueMeta{{ID: "sim", Broker: "alpaca", Gate: uihub.GateLimits{MaxOrderValue: 1e9}}},
		Global:  uihub.GlobalLimits{MaxDayLoss: 1e9},
		MD:      20 * time.Millisecond, Account: 50 * time.Millisecond, Position: 30 * time.Millisecond,
		Buf:     4096, TapeCap: 100, NewsCap: 100, FillsCap: 100, EventsCap: 100, OutBuf: 256,
	}, execCore, st, mdCore)
	go func() { _ = hub.Run(ctx) }()
	go forwardExec(ctx, execCore, hub)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	c := dialWS(t, ctx, url, wsmsg.TopicExecOrders, wsmsg.TopicExecStatus)
	defer c.Close(websocket.StatusNormalClosure, "")

	// arm master + venue so the gate lets orders through
	sendCommand(t, ctx, c, "Arm", map[string]any{})
	sendCommand(t, ctx, c, "Arm", map[string]any{"venue": "sim"})

	// give the sim a mark so a market order is marketable, then submit
	simBroker.SetMark("US.AAPL", 3.50)
	corr := sendCommand(t, ctx, c, "SubmitOrder", map[string]any{
		"venue": "sim", "symbol": "US.AAPL", "side": "BUY", "type": "MARKET", "tif": "DAY", "qty": 10,
	})

	// ack accepted with an orderId
	ack := waitFrame(t, ctx, c, func(m map[string]any) bool { return m["kind"] == "ack" && m["corrId"] == corr })
	if ack["status"] != "accepted" || ack["orderId"] == "" {
		t.Fatalf("submit should be accepted with an orderId: %v", ack)
	}

	// an exec.orders delta with status FILLED must arrive
	filled := waitFrame(t, ctx, c, func(m map[string]any) bool {
		if m["kind"] != "delta" || m["topic"] != "exec.orders" {
			return false
		}
		o, _ := m["payload"].(map[string]any)
		return o != nil && o["status"] == "FILLED"
	})
	o := filled["payload"].(map[string]any)
	if o["symbol"] != "US.AAPL" || o["executedQty"] != float64(10) {
		t.Fatalf("filled order wrong: %v", o)
	}

	// QueryFills returns the fill (persisted via exec AppendExecEvent -> store)
	qcorr := sendQuery(t, ctx, c, "QueryFills", map[string]any{"symbol": "US.AAPL", "fromMs": 0, "toMs": time.Now().Add(time.Hour).UnixMilli()})
	res := waitFrame(t, ctx, c, func(m map[string]any) bool { return m["kind"] == "result" && m["corrId"] == qcorr })
	fills, _ := res["payload"].([]any)
	if len(fills) == 0 {
		t.Fatalf("QueryFills should return the fill, got %v", res["payload"])
	}
}

func TestE2EReplayMarketDataOverWS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)

	// record a couple of feed events, then read the day back to replay them
	base := time.Date(2026, 7, 6, 13, 31, 0, 0, time.UTC)
	day := base.Format("2006-01-02")
	st.RecordEvent(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.47, TsMs: base.UnixMilli()}}, base.UnixMilli())
	st.RecordEvent(feed.QuoteEvent{Quote: feed.Quote{Symbol: "US.AAPL", Last: 3.50, TsMs: base.Add(time.Second).UnixMilli()}}, base.Add(time.Second).UnixMilli())
	st.Flush()
	rows, err := st.ReadJournalDay(day)
	if err != nil || len(rows) < 2 {
		t.Fatalf("recorded rows unavailable: %v (%d rows)", err, len(rows))
	}

	mdCore := md.New(md.Config{TapeRing: 1024, AnchorSecs: 34200})
	go func() { _ = mdCore.Run(ctx) }()

	// exec core with no venues (md-only test) still constructs a valid hub
	execCore := exec.NewCore(exec.CoreConfig{Store: st, Brokers: map[exec.VenueID]exec.Broker{}, Clock: clock.System{}, IDGen: exec.NewOrderIDGen(clock.System{}, deterministicReader())})
	_ = execCore.Recover(ctx)
	go func() { _ = execCore.Run(ctx) }()

	hub, srv := uihub.New(clock.System{}, uihub.Config{
		MD: 15 * time.Millisecond, Account: 50 * time.Millisecond, Position: 30 * time.Millisecond,
		Buf: 4096, TapeCap: 100, NewsCap: 100, FillsCap: 100, EventsCap: 100, OutBuf: 256,
	}, execCore, st, mdCore)
	go func() { _ = hub.Run(ctx) }()
	go forwardMD(ctx, mdCore, hub)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	// replay the recorded day into md.Core
	simClk := replay.NewClock(time.UnixMilli(rows[0].TsExch))
	fd := replay.NewFeed(replay.FeedOptions{Rows: rows, Sim: simClk, Speed: 0})
	go func() { _ = fd.Run(ctx) }()
	go func() {
		for ev := range fd.Events() {
			mdCore.Feed(ev)
		}
	}()

	c := dialWS(t, ctx, url, wsmsg.TopicQuote)
	defer c.Close(websocket.StatusNormalClosure, "")

	// a md.quote frame for US.AAPL must arrive (snapshot or delta)
	q := waitFrame(t, ctx, c, func(m map[string]any) bool {
		if m["topic"] != "md.quote" {
			return false
		}
		p, _ := m["payload"].(map[string]any)
		return p != nil && p["symbol"] == "US.AAPL"
	})
	p := q["payload"].(map[string]any)
	if _, ok := p["last"]; !ok {
		t.Fatalf("md.quote payload missing last: %v", p)
	}
}

// forwardExec/forwardMD mirror main's fan-in goroutines (the capstone reconstructs
// the wiring main does, since it can't import package main).
func forwardExec(ctx context.Context, execCore *exec.Core, hub *uihub.Hub) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-execCore.Updates():
			hub.PublishExec(u)
		}
	}
}

func forwardMD(ctx context.Context, mdCore *md.Core, hub *uihub.Hub) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-mdCore.Updates():
			hub.PublishMD(u)
		}
	}
}

// helpers: sendCommand/sendQuery write a frame and return its corrId.
func sendCommand(t *testing.T, ctx context.Context, c *websocket.Conn, name string, args map[string]any) string {
	t.Helper()
	corr := "c-" + name
	raw, _ := json.Marshal(args)
	b, _ := json.Marshal(wsmsg.CommandMsg{Kind: "command", CorrID: corr, Name: name, Args: raw})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	return corr
}

func sendQuery(t *testing.T, ctx context.Context, c *websocket.Conn, name string, args map[string]any) string {
	t.Helper()
	corr := "q-" + name
	raw, _ := json.Marshal(args)
	b, _ := json.Marshal(wsmsg.QueryMsg{Kind: "query", CorrID: corr, Name: name, Args: raw})
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	return corr
}

// deterministicReader supplies the exec OrderIDGen with a non-crypto seed for tests.
func deterministicReader() *strings.Reader { return strings.NewReader(strings.Repeat("etape-seed-0123456789", 64)) }
```

> **Verify at execution:** (1) `exec.NewOrderIDGen(clk, io.Reader)` — the second arg is an `io.Reader`; a `*strings.Reader` satisfies it, but if the ULID entropy source needs `≥` some bytes per `Next()`, use a longer/`io.Repeat`-style reader (Go 1.26 has no `io.Repeat`; the `strings.Repeat` above yields ~1.3 KB, enough for many IDs — extend if the capstone submits many orders). (2) `sim.Broker.SetMark(symbol, price)` and its immediate-fill-on-marketable-market-order behavior (Plan 4 §Task 10) — if a market order needs the mark set *before* submit to fill, the ordering above is correct; confirm. (3) `feed.QuoteEvent` journal round-trips through `store.RecordEvent`/`ReadJournalDay` (Plan 3 codec) — if the day-bucketing derives from `TsExch`, the `day` string must match `session.DayMs`'s ET bucket; adjust `base` to a clearly-in-session ET time if the recorded day and `ReadJournalDay(day)` disagree.

- [ ] **Step 2: Run the capstone under -race**

Run: `cd engine && go test ./internal/uihubtest/ -race -v`
Expected: PASS — both subtests green.

- [ ] **Step 3: Full-module gate**

Run: `cd engine && go build ./... && go vet ./... && go test -race ./... && golangci-lint run && make gen-ts-check`
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add engine/internal/uihubtest/
git commit -m "test(engine/uihub): capstone E2E — order lifecycle + replay md over a real WebSocket"
```

---

## Self-Review (run before handing off)

**1. Spec coverage** — every go-engine-design §uihub / §Pollers / §Boot requirement maps to a task:
- WS server on `127.0.0.1:8686`, static `ui/dist` + `/ws` → Task 10 (+ config Task 1).
- Snapshot-then-delta per topic → Tasks 5 (snapshot) + 6 (delta/broadcast).
- Per-class coalescing (30 Hz md, 4 Hz account, 100 ms positions, batched tape, event-driven orders/fills) → Task 6.
- Correlation-ID commands, sync ack accepted|blocked, outcomes as topic events → Tasks 8 + 6/7.
- `query`/`result` (QueryFills) → Task 9.
- App ping/pong RTT → Task 7 (pong) + Task 13 (surfaced in sys.health).
- Slow-client drop + force re-sync → Task 6 (overflow → close → reconnect).
- `wsmsg` + tygo, drift fails build → Tasks 2 + 4.
- `scan`/`news`/`health` pollers → Tasks 11/12/13.
- Full boot order + independence (dead OpenD never blocks kill switch) → Task 15.
- md↔exec mark bridge → Task 15 (`markBridge`).
- Ordered shutdown (store writers before Close) → Task 15.

**2. Placeholder scan** — the plan contains no `TODO`/"handle errors appropriately"; each "verify at execution" note is a concrete instruction with the `go doc` command to run, targeting exactly the proto2-generated-name and Plan-5-constructor-name uncertainties that can only be resolved against merged code (the plan's own verification pass, per phase-router, does this).

**3. Type consistency** — checked across tasks: `staged{Topic,Key,Payload,Snap}` (Task 5) is consumed identically in Task 6; the `client` interface (`id/enqueue/close`, Task 6) is implemented by `conn` (Task 7); `commandHandler`/`queryHandler` (Task 7) are produced by `newCommands`/`newQueries` (Tasks 8/9) and wired by `NewServer`/`New` (Tasks 10/14); `uihub.New` (Task 14) is called by `main` (Task 15) with the exec/store/md cores that satisfy `ExecCore`/`Stores`/`Indicators`; the wire field names in `wsmsg` (Task 2) are the same ones the mappers (Task 3) and mirror (Task 5) populate and that tygo (Task 4) emits.

**4. Known deferrals (recorded, not gaps):** scanner 3203 float fallback (Task 11); health `min/avg/max` rolling window + app-ping RTT source (Task 13/15); `VenueStatus.reconcilePending`/`lastReconcileMs` default to false/null (exec.Core doesn't surface reconcile timing granularly — Task 5); news unofficial HTTP enrichment (v1.x). Each is flagged inline and belongs in the execution ledger.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-06-engine-uihub-pollers-wiring.md`.

**Verification pass status (2026-07-06):** three `sonnet` verifiers checked the plan against the merged Plans 1–4 tree — engine API signatures, poller protobuf bindings, and wsmsg↔`contract.ts` parity + cross-task consistency. The Plan-5-independent portion (Tasks 1–13, 15's md/exec/store wiring, 16) is **verified and its findings applied**: every OpenD request now uses the required `Request{C2S:...}` wrapper (was a bare `C2S` — wire-format bug); `qotstockfilter.Market` and `getglobalstate.C2S.UserID` required fields added (the latter fixed a hard marshal failure on every health probe); `RetType` error checks added to pollers; Design Decision #5 reconciled with the code (`md.indicator` is event-driven); the `kind`/`status`/`link` tygo literal-union handling documented in Task 4. Confirmed correct as-written (my own "verify" flags resolved): `config.Load` merges onto `Default()`, `exec.FillRow.Side` = `Side.String()`, `SubscribeIndicator` args, `sim.SetMark`→submit fill ordering, `AccountSnapshot.DayPnL`, all channel directions, and full wsmsg↔contract field/enum/nullability parity.

**Plan-5 seam re-verified (2026-07-06) — both gates now cleared.** Plan 5 is merged+pushed to `origin/main` (`c753f05`) with all adapter/creds code present. The seam was re-checked directly against the merged tree: `exec.TypeStop`/`TypeStopLimit` (String→`"STOP"`/`"STOP_LIMIT"`), `exec.Broker.Flatten(ctx)`, all 5 `exec.Update` variants, `creds.Load`/`File.Get`/`Pair`, both adapter `New(Config)(*Adapter,error)` + `Config` field sets, `config.Venue` (unchanged), and the `coder/websocket` v1.8.15 API (`Accept`/`Dial`/`Conn.Read`/`Write`/`Close`/`MessageText`/`StatusCode`/`AcceptOptions.InsecureSkipVerify`) — **all match Plan 6's assumptions.** **One compile bug found and fixed in the plan:** the adapters' `Run(ctx)` returns no error, so `venueBroker.Run` is `func(context.Context)` (was `func(...) error`) — Tasks 14 & 15 corrected.

**Ready to execute.** Branch the Plan 6 worktree fresh from `main`/`origin/main` @ `c753f05` (which has Plans 1–5). The full-module gate (`go build && go vet && go test -race ./... && golangci-lint run && make gen-ts-check`) should be green at each task's end.

Two execution options:

1. **Subagent-Driven (recommended)** — one fresh subagent per task, two-stage review between tasks (matches how Plans 1–5 were executed; use [[subagent-worktree-verification]] and [[plan-mandated-coverage-gaps-add-tests]]).
2. **Inline Execution** — batch with checkpoints via superpowers:executing-plans.

Which approach? (And confirm Plan 5 has merged.)
