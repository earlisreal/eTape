# Engine Broker Adapters (TradeZero + Alpaca) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the exec core its real broker legs — `broker/tradezero` and `broker/alpaca` adapters that implement Plan 4's `exec.Broker`/`Capabilities`, translate all four order types (Market/Limit/Stop/StopLimit) to/from each broker's wire format, normalize broker events into domain `BrokerEvent`s, and are proven driving the real `exec.Core` through a scripted lifecycle (limit → replace → cancel → kill) — while closing the stop-order gap the UI plan flagged (`OrderType` gains Stop/StopLimit; the gate values them; SimBroker simulates their triggers).

**Architecture:** Two new adapter packages sit at the outer edge behind Plan 4's `exec.Broker` interface: `broker/tradezero` (REST + Portfolio WebSocket, HTTP-200-but-rejected trap, no-modify → emulated replace via cancel→resubmit with a stable domain order ID, per-endpoint token buckets) and `broker/alpaca` (REST + `trade_updates` WebSocket, structured JSON errors, native `PATCH` replace + `DELETE /positions` flatten, single 200/min token pool). Each runs its own I/O goroutines (a REST caller pool and one WS reader/reconnect loop) and pushes typed `exec.BrokerEvent`s into the Core's inbox through `Events()`; adapters guard only their own state with their own mutexes and never touch domain state, so `go test -race` still proves the single-writer invariant. Shared network plumbing — jittered backoff, a clock-injected token-bucket limiter, and a keep-alive HTTP client — lives in `broker/netx`; both adapters follow the same buffer→REST-snapshot→replay startup/reconnect sequence and diff-then-synthesize reconcile. The stop-order fix is domain-first: three small changes to `exec`/`gate`/`sim` land before any adapter so the adapters have real `TypeStop`/`TypeStopLimit` constants to map.

**Tech Stack:** Go 1.26.4 (module from Plans 1–4); `github.com/coder/websocket` (WS client — added here; context-first, reads text **and** binary frames, needed for Alpaca paper's binary frames); stdlib `net/http` (REST, keep-alive pool) + `net/http/httptest` (mock servers) + `encoding/json`; `github.com/oklog/ulid/v2` (via Plan 4's `exec`); `modernc.org/sqlite` (via Plan 3's `store`, used only by the capstone); `go test -race` + `golangci-lint`. **No msgpack dependency** — the WS decoder JSON-decodes the frame payload regardless of the frame's text/binary opcode.

## Global Constraints

- Module path: `github.com/earlisreal/eTape/engine`. Go 1.26.4.
- **Branch dependency:** builds on **Plan 4** (`internal/exec`, `internal/broker/sim`), **merged to local `main` at `fb6ca3d`** (2026-07-06; not yet pushed to origin — origin/main is behind). Create the Plan 5 worktree branch from `main@fb6ca3d`. (Note: main advanced past this commit for later plans; branch from a commit that contains the merged Plan 4 exec/sim code.)
- **Dependency rule:** `broker/tradezero`, `broker/alpaca`, and `broker/netx` import only `exec`, `session`, `clock`, `creds`, `broker/netx`, and stdlib/third-party HTTP+WS libs. They **never** import `store`, `md`, `uihub`, `feed/opend`, or each other. `exec` never imports an adapter (adapters implement `exec.Broker`). The capstone test (Task 16) is the sole place `store` and the adapters meet, and it lives under a package that may import both.
- **Single-writer core unchanged:** adapters push `exec.BrokerEvent`s into `Core.Events()`-fed inbox; the Core is the only writer of domain state. Adapters hold their own mutexes for their own maps (order-id mapping, dedup sets, pending replaces) and never mutate `exec.State`. `go test -race ./...` must stay green.
- **Order IDs:** `"ET"`+ULID minted by `exec.OrderIDGen` in the Core. TradeZero's emulated replace derives suffixed IDs `<id>-r1`, `<id>-r2`, … (each ≤36 chars, TZ's recommended max; `"ET"`+ULID is 28 chars so suffixes stay well under it; suffix-stripping recovers the stable domain ID with no durable map). Alpaca uses the domain ID verbatim as `client_order_id` (≤128 cap).
- **Safety rule (standing, hard — CLAUDE.md):** **never place, modify, or cancel real orders on TradeZero.** The only TradeZero keys that exist are **LIVE** (real funds); paper keygen failed 2026-07-04. Therefore **every automated TradeZero test in this plan runs against an in-process `httptest` mock server** — no test ever opens a socket to `webapi.tradezero.com`. TradeZero order-lifecycle golden fixtures are **hand-authored** from `docs/2026-07-03-tradezero-api.md` + `docs/tradezero/tradezero-openapi.json` and are validated/augmented against real frames only in a live session Earl explicitly authorizes.
- **Alpaca paper is safe for automated order placement** (verified keys, account `PA3IC96WKTXD`, $100k paper). Its live-paper integration test (Task 16) is **opt-in** behind env var `ETAPE_ALPACA_PAPER=1`, places only tiny far-from-market limit orders, and cancels them immediately; CI without the env var skips it. No Alpaca **live** account exists — no code path targets `api.alpaca.markets` with real money.
- **Credentials:** `~/.eJournal/credentials.json`, JSON object keyed `tradeZero` / `alpaca`, each `{ "keyId": ..., "secretKey": ... }`. Loaded by `internal/creds`; **never logged, never committed**. Repo is **PUBLIC** — run a sensitive-sweep before every commit (no keys, tokens, account numbers, or captured-frame files containing real account IDs).
- **Persisted timestamps** are `INTEGER` epoch ms (Plan 3/4 convention), set from `clock.Clock`.
- **CI gates (every task ends green):** `cd engine && go build ./... && go vet ./... && go test -race ./... && golangci-lint run`.

## Plan sequence context (6 engine plans)

1. Foundation & OpenD Protocol Client — **done** (`feed/opend`).
2. Market-Data Core — **done** (`feed`, `session`, `md`).
3. Store, Journal & Replay — **done** (`store`, `replay`).
4. Execution Core (multi-venue) — **done, merged to `main` (`fb6ca3d`)** (`exec` domain + fold + two-layer gate + `Broker` interface + `SimBroker` + `Core` coordinator; 4 review findings hardened).
5. **Broker Adapters (this plan)** — `broker/tradezero` + `broker/alpaca` behind `exec.Broker`, plus the stop-order gap closure. `broker/moomoo` is designed (multi-broker spec) but **deferred to v1.x** — out of scope here.
6. uihub, Pollers & Main Wiring — the `uihub` WS server + `wsmsg`/tygo, `scan`/`news`/`health` pollers, and `cmd/etape` full boot sequence. Not this plan.

**Deliverable:** TradeZero (mock-server) + Alpaca (mock-server, plus opt-in real paper) adapters drive the real `exec.Core` through a scripted lifecycle with golden-corpus adapter tests, and the engine can place/cancel/replace Market, Limit, Stop, and Stop-Limit orders end-to-end against SimBroker.

## Authoritative references (read before implementing an adapter)

- `docs/superpowers/specs/2026-07-04-multi-broker-execution-design.md` — venue model, `Broker`/`Capabilities`, gate layering, per-adapter design, error tables.
- `docs/superpowers/specs/2026-07-03-portfolio-orders-design.md` — exec domain, event log, TZ adapter internals, error table.
- `docs/2026-07-03-tradezero-api.md` + `docs/tradezero/tradezero-openapi.json` — TZ REST/WS wire detail, R-codes, rate limits, field quirks.
- `docs/2026-07-03-alpaca-api.md` — Alpaca REST/WS wire detail, order model, `trade_updates`, rate limits, validation gotchas.

## File Structure

```
engine/
  go.mod                                    MODIFY  + github.com/coder/websocket
  internal/
    exec/
      types.go                              MODIFY  Task 1  OrderType += Stop/StopLimit; Validate
      gate.go                               MODIFY  Task 2  orderValue/markOr value stops
      broker.go                             MODIFY  Task 4  Broker interface += Flatten(ctx)
      core.go                               MODIFY  Task 4  handleFlatten -> b.Flatten
    broker/
      sim/sim.go                            MODIFY  Task 3  stop-trigger sim; Task 4 Flatten
      netx/
        backoff.go        backoff_test.go   CREATE  Task 5  jittered exponential backoff
        ratelimit.go      ratelimit_test.go CREATE  Task 5  clock-injected token bucket
        httpclient.go     httpclient_test.go CREATE Task 5  keep-alive pooled *http.Client
      tradezero/
        mapping.go        mapping_test.go   CREATE  Task 6  domain <-> TZ wire enums
        normalize.go      normalize_test.go CREATE  Task 7  WS/REST field quirks -> domain
        rest.go           rest_test.go      CREATE  Task 8  REST client (200-rejected trap, R-codes)
        ws.go             ws_test.go        CREATE  Task 9  Portfolio WS handshake/read/staleness
        tradezero.go      tradezero_test.go CREATE  Task 10 assemble exec.Broker; emulated replace; reconcile
        testdata/*.json                     CREATE  Tasks 7-10 golden fixtures (authored)
      alpaca/
        mapping.go        mapping_test.go   CREATE  Task 11 domain <-> Alpaca wire enums
        normalize.go      normalize_test.go CREATE  Task 12 trade_updates -> domain
        rest.go           rest_test.go      CREATE  Task 13 REST client (JSON errors, PATCH, flatten)
        ws.go             ws_test.go        CREATE  Task 14 trade_updates WS (binary/text)
        alpaca.go         alpaca_test.go    CREATE  Task 15 assemble exec.Broker; native replace; reconcile
        testdata/*.json                     CREATE  Tasks 12-15 golden fixtures (real paper + authored)
    creds/
      creds.go            creds_test.go     CREATE  Task 5  ~/.eJournal/credentials.json loader
  internal/exectest/
      lifecycle_test.go                     CREATE  Task 16 scripted lifecycle through exec.Core
      alpacapaper_test.go                   CREATE  Task 16 opt-in real-paper integration
```

`internal/exectest` is a new leaf test package (imports `exec`, `store`, both adapters, `sim`) — it exists so the capstone can import `store` + adapters without violating the domain dependency rule inside `exec` itself.

---

# Part A — Close the stop-order gap (domain / gate / sim)

Three small changes so the engine understands the four order types the UI ticket sends (`MARKET | LIMIT | STOP | STOP_LIMIT`). Both real brokers accept Stop and StopLimit natively (TZ `Stop|StopLimit`, Alpaca `stop|stop_limit`), so once the domain models them the adapters just map them.

### Task 1: `OrderType` gains Stop + StopLimit; `Validate` stop coherence

**Files:**
- Modify: `engine/internal/exec/types.go` (the `OrderType` const block ~line 40, its `String()` ~line 48, and `OrderRequest.Validate` ~line 200)
- Test: `engine/internal/exec/types_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `exec.TypeStop`, `exec.TypeStopLimit` (new `OrderType` constants, values 2 and 3 — appended, existing 0/1 unchanged); `OrderType.String()` → `"STOP"` / `"STOP_LIMIT"`; `OrderRequest.Validate()` requires `StopPrice > 0` for Stop and both `StopPrice > 0` and `LimitPrice > 0` for StopLimit.

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/exec/types_test.go`:

```go
func TestOrderType_String_Stops(t *testing.T) {
	cases := map[exec.OrderType]string{
		exec.TypeMarket:    "MARKET",
		exec.TypeLimit:     "LIMIT",
		exec.TypeStop:      "STOP",
		exec.TypeStopLimit: "STOP_LIMIT",
	}
	for ot, want := range cases {
		if got := ot.String(); got != want {
			t.Errorf("OrderType(%d).String() = %q, want %q", uint8(ot), got, want)
		}
	}
}

func TestOrderRequest_Validate_Stops(t *testing.T) {
	base := exec.OrderRequest{Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Qty: 10}
	tests := []struct {
		name    string
		mutate  func(*exec.OrderRequest)
		wantErr bool
	}{
		{"stop without stop price", func(r *exec.OrderRequest) { r.Type = exec.TypeStop }, true},
		{"stop ok", func(r *exec.OrderRequest) { r.Type = exec.TypeStop; r.StopPrice = 5 }, false},
		{"stop-limit missing limit", func(r *exec.OrderRequest) { r.Type = exec.TypeStopLimit; r.StopPrice = 5 }, true},
		{"stop-limit missing stop", func(r *exec.OrderRequest) { r.Type = exec.TypeStopLimit; r.LimitPrice = 5 }, true},
		{"stop-limit ok", func(r *exec.OrderRequest) { r.Type = exec.TypeStopLimit; r.StopPrice = 5; r.LimitPrice = 6 }, false},
		{"limit still requires price", func(r *exec.OrderRequest) { r.Type = exec.TypeLimit }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.mutate(&r)
			if err := r.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd engine && go test ./internal/exec/ -run 'OrderType_String_Stops|Validate_Stops' -v`
Expected: FAIL — `undefined: exec.TypeStop` / `exec.TypeStopLimit` (compile error).

- [ ] **Step 3: Extend the enum, `String()`, and `Validate`**

In `engine/internal/exec/types.go`, extend the `OrderType` const block (append — do not reorder; existing 0/1 must stay stable for the persisted-event codec):

```go
const (
	TypeMarket OrderType = iota
	TypeLimit
	TypeStop
	TypeStopLimit
)
```

Add the two cases to `OrderType.String()`:

```go
	case TypeStop:
		return "STOP"
	case TypeStopLimit:
		return "STOP_LIMIT"
```

Replace the single limit check in `OrderRequest.Validate()` (currently `if r.Type == TypeLimit && r.LimitPrice <= 0 { ... }`) with a per-type switch:

```go
	switch r.Type {
	case TypeLimit:
		if r.LimitPrice <= 0 {
			return errors.New("exec: limit order missing limit price")
		}
	case TypeStop:
		if r.StopPrice <= 0 {
			return errors.New("exec: stop order missing stop price")
		}
	case TypeStopLimit:
		if r.StopPrice <= 0 {
			return errors.New("exec: stop-limit order missing stop price")
		}
		if r.LimitPrice <= 0 {
			return errors.New("exec: stop-limit order missing limit price")
		}
	}
```

> **Design note (record in a code comment on the switch):** `Validate` is *structural* — it checks that a type's required prices are present, not that they are directionally coherent (a buy stop-limit whose limit sits below its stop). Directional coherence is a UI pre-check (`ui/src/chrome/exec/preChecks.ts`), and TradeZero itself does not validate it (an inverted stop-limit "sits unfilled" — `docs/2026-07-03-tradezero-api.md`). Keeping the engine's gate structural mirrors broker behaviour and avoids rejecting an order a broker would accept.

- [ ] **Step 4: Run to verify it passes**

Run: `cd engine && go test ./internal/exec/ -run 'OrderType_String_Stops|Validate_Stops' -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Full package + race**

Run: `cd engine && go test -race ./internal/exec/`
Expected: PASS (the codec/fold/gate tests still pass — enum values 0/1 unchanged).

- [ ] **Step 6: Commit**

```bash
cd engine && git add internal/exec/types.go internal/exec/types_test.go
git commit -m "feat(engine/exec): OrderType += Stop/StopLimit + Validate stop-price coherence

Closes the stop-order gap flagged by the UI execution-surfaces plan: the UI
ticket sends MARKET|LIMIT|STOP|STOP_LIMIT; the domain now models all four.
Validate stays structural (price presence), not directional coherence."
```

### Task 2: Gate values Stop / Stop-Limit orders

**Files:**
- Modify: `engine/internal/exec/gate.go` (`orderValue` ~line 40, `markOr` ~line 55)
- Test: `engine/internal/exec/gate_test.go`

**Interfaces:**
- Consumes: `exec.TypeStop`, `exec.TypeStopLimit` (Task 1).
- Produces: `orderValue` values Stop at `Qty*StopPrice` (no mark required — a stop always carries a price), StopLimit at `Qty*LimitPrice`; `markOr` falls back to `StopPrice` when there is no mark and no limit price.

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/exec/gate_test.go` (these exercise the value cap via `Evaluate`; adapt the venue/gate-config helper already used by the file — if the file has a helper like `armedState(...)`, reuse it; otherwise build a minimal armed two-venue `State` inline as the existing tests do):

```go
func TestGate_ValuesStopAtStopPrice_NoMarkNeeded(t *testing.T) {
	s := armedGateState(t, "v") // master+venue armed, empty positions (see existing helper)
	cfg := exec.GateConfig{Venue: map[exec.VenueID]exec.VenueLimits{"v": {MaxOrderValue: 1000}}}
	marks := markStub{} // no marks at all
	// 10 * 90 = 900 <= 1000 -> allowed; a market order here would be blocked ("no mark").
	ok, reason := exec.Evaluate(s, cfg, exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeStop,
		Qty: 10, StopPrice: 90, ClientOrderID: "ET-stop",
	}, marks)
	if !ok {
		t.Fatalf("stop order should value at stop price without a mark: %q", reason)
	}
	// 20 * 90 = 1800 > 1000 -> blocked on venue value cap.
	if ok, _ := exec.Evaluate(s, cfg, exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeStop,
		Qty: 20, StopPrice: 90, ClientOrderID: "ET-stop2",
	}, marks); ok {
		t.Fatalf("20*90 should exceed the 1000 venue cap")
	}
}

func TestGate_ValuesStopLimitAtLimitPrice(t *testing.T) {
	s := armedGateState(t, "v")
	cfg := exec.GateConfig{Venue: map[exec.VenueID]exec.VenueLimits{"v": {MaxOrderValue: 1000}}}
	marks := markStub{}
	// stop-limit valued at limit (101), 10*101 = 1010 > 1000 -> blocked.
	if ok, _ := exec.Evaluate(s, cfg, exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeStopLimit,
		Qty: 10, StopPrice: 100, LimitPrice: 101, ClientOrderID: "ET-sl",
	}, marks); ok {
		t.Fatalf("stop-limit should value at limit price (10*101 > 1000)")
	}
}
```

> `gate_test.go` already has equivalent helpers — `fakeMarks` (a `map[string]float64` with `LastTrade`) and `armedState()` (a master+venue-armed `*State` over venues `sim-1`/`sim-2`). **If these new tests live in the same test package as those helpers, reuse them** (use venue `"sim-1"` and `fakeMarks{}` instead of `armedGateState(t,"v")`/`markStub{}`). If you write these in a *different* test package (`exec_test`) where those unexported helpers are not visible, add the self-contained equivalents shown here:
> ```go
> type markStub map[string]float64
> func (m markStub) LastTrade(s string) (float64, bool) { v, ok := m[s]; return v, ok }
>
> func armedGateState(t *testing.T, venues ...exec.VenueID) *exec.State {
> 	t.Helper()
> 	s := exec.NewState(venues)
> 	s.SetMasterArmed(true)
> 	for _, v := range venues {
> 		s.SetVenueArmed(v, true)
> 	}
> 	return s
> }
> ```

- [ ] **Step 2: Run to verify it fails**

Run: `cd engine && go test ./internal/exec/ -run 'Gate_ValuesStop' -v`
Expected: FAIL — the first test's non-mark stop is currently blocked (`orderValue` returns `Qty*LimitPrice = 0` for a Stop, and position valuation via `markOr` also mishandles it), so assertions trip.

- [ ] **Step 3: Implement type-aware valuation**

Replace `orderValue` and `markOr` in `engine/internal/exec/gate.go`:

```go
// orderValue values an order for the max-order-value / position-value checks:
//   Limit      -> limit price
//   StopLimit  -> limit price (it triggers into a limit at that price)
//   Stop       -> stop price (triggers into a market ~at the stop; always priced)
//   Market     -> last-trade mark (ok=false when there is no mark -> must block)
func orderValue(req OrderRequest, marks MarkSource) (float64, bool) {
	switch req.Type {
	case TypeMarket:
		m, ok := marks.LastTrade(req.Symbol)
		if !ok {
			return 0, false
		}
		return req.Qty * m, true
	case TypeStop:
		return req.Qty * req.StopPrice, true
	default: // Limit, StopLimit
		return req.Qty * req.LimitPrice, true
	}
}

// markOr returns the last-trade mark for resulting-position valuation, falling
// back to the order's own price when no mark exists (limit/stop-limit -> limit
// price; bare stop -> stop price). A market order always has a mark here (the
// order-value check above already blocked it otherwise).
func markOr(req OrderRequest, marks MarkSource) float64 {
	if m, ok := marks.LastTrade(req.Symbol); ok {
		return m
	}
	if req.LimitPrice > 0 {
		return req.LimitPrice
	}
	return req.StopPrice
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd engine && go test ./internal/exec/ -run 'Gate_ValuesStop' -v`
Expected: PASS.

- [ ] **Step 5: Full package + race**

Run: `cd engine && go test -race ./internal/exec/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd engine && git add internal/exec/gate.go internal/exec/gate_test.go
git commit -m "feat(engine/exec): gate values Stop (at stop price) and Stop-Limit (at limit)

A stop always carries a price, so it values without a mark; stop-limit values
at its limit like a limit order."
```

### Task 3: SimBroker simulates stop triggers

**Files:**
- Modify: `engine/internal/broker/sim/sim.go`
- Test: `engine/internal/broker/sim/sim_test.go` (add cases; keep existing tests green)

**Interfaces:**
- Consumes: `exec.TypeStop`, `exec.TypeStopLimit`.
- Produces: SimBroker rests a stop/stop-limit until the mark crosses its `StopPrice` in the trigger direction; a triggered `TypeStop` fills at the current mark; a triggered `TypeStopLimit` becomes a resting limit at `LimitPrice` and fills when marketable. Trigger direction: Buy/Cover trigger when `mark >= stop`; Sell/Short trigger when `mark <= stop`.

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/broker/sim/sim_test.go` (mirror the existing test style — construct `sim.New(venue, clk)`, drain `b.Events()`):

```go
// helper: drain all currently-buffered broker events without blocking.
func drain(ch <-chan exec.BrokerEvent) []exec.BrokerEvent {
	var out []exec.BrokerEvent
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func filledAt(t *testing.T, evs []exec.BrokerEvent) (exec.OrderFilled, bool) {
	t.Helper()
	for _, e := range evs {
		if f, ok := e.(exec.OrderFilled); ok {
			return f, true
		}
	}
	return exec.OrderFilled{}, false
}

func TestSim_BuyStop_TriggersOnMarkAtOrAboveStop(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	b := sim.New("v", clk)
	b.SetMark("AAPL", 95)
	drain(b.Events())
	_, err := b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeStop,
		Qty: 10, StopPrice: 100, ClientOrderID: "ET-bstop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := filledAt(t, drain(b.Events())); ok {
		t.Fatal("buy stop must rest while mark (95) < stop (100)")
	}
	b.SetMark("AAPL", 101) // crosses the stop
	f, ok := filledAt(t, drain(b.Events()))
	if !ok {
		t.Fatal("buy stop must fill once mark reaches the stop")
	}
	if f.AvgPrice != 101 {
		t.Fatalf("stop-market fills at the mark: got %v want 101", f.AvgPrice)
	}
}

func TestSim_SellStop_TriggersOnMarkAtOrBelowStop(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	b := sim.New("v", clk)
	b.SetMark("AAPL", 105)
	drain(b.Events())
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeStop,
		Qty: 10, StopPrice: 100, ClientOrderID: "ET-sstop",
	})
	if _, ok := filledAt(t, drain(b.Events())); ok {
		t.Fatal("sell stop must rest while mark (105) > stop (100)")
	}
	b.SetMark("AAPL", 99)
	if f, ok := filledAt(t, drain(b.Events())); !ok || f.AvgPrice != 99 {
		t.Fatalf("sell stop should fill at mark 99; ok=%v px=%v", ok, f.AvgPrice)
	}
}

func TestSim_BuyStopLimit_TriggersThenRestsAsLimit(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	b := sim.New("v", clk)
	b.SetMark("AAPL", 95)
	drain(b.Events())
	// stop 100, limit 100.5 buy: on trigger it is a limit buy @100.5.
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeStopLimit,
		Qty: 10, StopPrice: 100, LimitPrice: 100.5, ClientOrderID: "ET-bsl",
	})
	b.SetMark("AAPL", 102) // triggers (>=100) but 100.5 limit is NOT marketable at 102 -> rests
	if _, ok := filledAt(t, drain(b.Events())); ok {
		t.Fatal("stop-limit must not fill above its limit")
	}
	b.SetMark("AAPL", 100) // now 100.5 >= 100 -> marketable
	if f, ok := filledAt(t, drain(b.Events())); !ok || f.AvgPrice != 100.5 {
		t.Fatalf("stop-limit should fill at its limit 100.5; ok=%v px=%v", ok, f.AvgPrice)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd engine && go test ./internal/broker/sim/ -run 'Sim_.*Stop' -v`
Expected: FAIL — current sim treats a stop as a limit with `LimitPrice==0`, so a buy stop looks non-marketable forever and a sell stop fills immediately at the wrong price.

- [ ] **Step 3: Implement stop-trigger simulation**

In `engine/internal/broker/sim/sim.go`, add the trigger predicate and a per-order "does this mark act on this order" routine, and route stops through it in both `SubmitOrder` and `crossRestingLocked`.

Add near `marketable`:

```go
// stopTriggered reports whether a stop/stop-limit's trigger has been hit.
// Buy/Cover stops trigger at or above the stop; Sell/Short stops at or below.
func stopTriggered(side exec.Side, stop, mark float64) bool {
	switch side {
	case exec.SideBuy, exec.SideCover:
		return mark >= stop
	default: // Sell, Short
		return mark <= stop
	}
}
```

Replace `crossRestingLocked` with a version that understands stops. A triggered `TypeStopLimit` is *converted in place* to a `TypeLimit` (its stop has done its job); after conversion it fills iff marketable. This keeps the resting set uniform. Caller holds `mu`.

```go
// actOnMarkLocked applies a new mark to one resting order and returns the fill
// events it produces (empty if it stays resting). Caller holds mu.
func (b *Broker) actOnMarkLocked(o *exec.Order, mark float64) []exec.BrokerEvent {
	switch o.Type {
	case exec.TypeStop:
		if stopTriggered(o.Side, o.StopPrice, mark) {
			return b.fillLocked(o, mark) // stop-market fills at the mark
		}
	case exec.TypeStopLimit:
		if stopTriggered(o.Side, o.StopPrice, mark) {
			o.Type = exec.TypeLimit // triggered: becomes a resting limit
			if marketable(o.Side, o.LimitPrice, mark) {
				return b.fillLocked(o, o.LimitPrice)
			}
		}
	default: // TypeLimit, TypeMarket(resting shouldn't happen)
		if marketable(o.Side, o.LimitPrice, mark) {
			return b.fillLocked(o, o.LimitPrice)
		}
	}
	return nil
}

// crossRestingLocked applies a new mark to every resting order on a symbol,
// in deterministic id order. Caller holds mu.
func (b *Broker) crossRestingLocked(symbol string, mark float64) []exec.BrokerEvent {
	var ids []string
	for id, o := range b.orders {
		if o.Symbol == symbol {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []exec.BrokerEvent
	for _, id := range ids {
		o, ok := b.orders[id]
		if !ok { // filled earlier in this pass
			continue
		}
		out = append(out, b.actOnMarkLocked(o, mark)...)
	}
	return out
}
```

In `SubmitOrder`, replace the marketability decision so a freshly-submitted stop/stop-limit rests unless the current mark already acts on it. Change the fill-decision block (after the market-order-no-mark guard) to:

```go
	// Market orders fill at the mark immediately.
	if req.Type == exec.TypeMarket {
		post = append(post, b.fillLocked(o, mark)...)
		b.mu.Unlock()
		for _, e := range post {
			b.emit(e)
		}
		return exec.OrderAck{OrderID: o.ID, Accepted: true, Message: brokerID}, nil
	}
	// Limit / Stop / StopLimit: apply the current mark if we have one; whatever
	// does not fill stays resting until a later SetMark acts on it.
	if hasMark {
		post = append(post, b.actOnMarkLocked(o, mark)...)
	}
```

(Delete the old `fillPx`/`doFill` lines that this replaces. `actOnMarkLocked` handles Limit marketability, so the standalone limit path is subsumed. Leave the `TypeMarket && !hasMark` rejection guard above untouched.)

- [ ] **Step 4: Run to verify it passes**

Run: `cd engine && go test ./internal/broker/sim/ -run 'Sim' -v`
Expected: PASS — new stop tests **and** the existing market/limit tests (limit marketability now flows through `actOnMarkLocked`).

- [ ] **Step 5: Full package + race**

Run: `cd engine && go test -race ./internal/broker/sim/ ./internal/exec/`
Expected: PASS (the exec capstone's SimBroker-driven flows are unaffected — no stops there yet).

- [ ] **Step 6: Commit**

```bash
cd engine && git add internal/broker/sim/sim.go internal/broker/sim/sim_test.go
git commit -m "feat(engine/broker): SimBroker simulates stop and stop-limit triggers

Stops rest until the mark crosses the stop; stop-market fills at the mark,
stop-limit converts to a resting limit. Keeps replay/E2E honest for all four
order types."
```

### Task 4: `Broker.Flatten` — interface method + SimBroker + Core wiring

**Files:**
- Modify: `engine/internal/exec/broker.go` (add `Flatten` to the `Broker` interface)
- Modify: `engine/internal/broker/sim/sim.go` (implement `Flatten`)
- Modify: `engine/internal/exec/core.go` (`handleFlatten` calls `b.Flatten`)
- Test: `engine/internal/broker/sim/sim_test.go`, `engine/internal/exec/core_test.go`

**Interfaces:**
- Consumes: `exec.Broker`, `exec.Capabilities.FlattenAll`.
- Produces: `exec.Broker` gains `Flatten(ctx context.Context) error`. SimBroker's `Flatten` zeroes all positions and emits `BrokerPositions`. `Core.handleFlatten` calls `b.Flatten(ctx)` (native primitive) for `FlattenAll` venues instead of the previous `CancelAll("")` stand-in. TradeZero (Task 10) returns "unsupported" (its `FlattenAll` is `false`, so the Core never calls it); Alpaca (Task 15) issues `DELETE /v2/positions`.

- [ ] **Step 1: Write the failing tests**

Add to `engine/internal/broker/sim/sim_test.go`:

```go
func TestSim_Flatten_ZeroesPositions(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	b := sim.New("v", clk)
	b.SetMark("AAPL", 100)
	drain(b.Events())
	// build a long position via a market buy
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET-x",
	})
	drain(b.Events())
	if err := b.Flatten(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, pos, _, _ := b.Snapshot(context.Background())
	for _, p := range pos {
		if p.Qty != 0 {
			t.Fatalf("Flatten should zero %s, got %v", p.Symbol, p.Qty)
		}
	}
}
```

Add to `engine/internal/exec/core_test.go` (reuse the file's existing single-venue Core harness; if it has none, this mirrors the capstone's `buildMultiCore` pattern with one sim venue):

```go
func TestCore_Flatten_RequiresFlattenCapability(t *testing.T) {
	// A venue whose broker advertises FlattenAll=false must reject Flatten.
	c, _ := buildCoreWith(t, capStub{flatten: false})
	if ack := c.Do(exec.Flatten{Venue: "v"}); ack.Accepted {
		t.Fatal("Flatten must be rejected when FlattenAll is false")
	}
	// FlattenAll=true is accepted.
	c2, b := buildCoreWith(t, capStub{flatten: true})
	if ack := c2.Do(exec.Flatten{Venue: "v"}); !ack.Accepted {
		t.Fatalf("Flatten should be accepted: %q", ack.Reason)
	}
	if !b.flattenCalled() {
		t.Fatal("Core should have invoked Broker.Flatten")
	}
}
```

> `buildCoreWith` and `capStub` are small test doubles local to `core_test.go`. Note: `core_test.go` currently wires **real `sim.Broker`** instances via its `newTestCore` helper, and SimBroker always reports `FlattenAll:true` — so it cannot exercise the *reject* branch. Author a minimal fake `exec.Broker` (`capStub`) that implements all 8 interface methods, records whether `Flatten` was called, and lets the test set `Capabilities.FlattenAll`. Keep it faithful to the interface (the compiler enforces the 8 methods).

- [ ] **Step 2: Run to verify it fails**

Run: `cd engine && go test ./internal/broker/sim/ ./internal/exec/ -run 'Flatten' -v`
Expected: FAIL — `b.Flatten` undefined (sim) / `exec.Broker` has no `Flatten` (compile error in stubs).

- [ ] **Step 3: Add `Flatten` to the interface, sim, and Core**

In `engine/internal/exec/broker.go`, add to the `Broker` interface (after `CancelAll`):

```go
	// Flatten closes all open positions on the venue via the broker's native
	// close-all primitive (Alpaca DELETE /v2/positions). Venues whose
	// Capabilities.FlattenAll is false return an "unsupported" error and the
	// Core never calls it.
	Flatten(ctx context.Context) error
```

In `engine/internal/broker/sim/sim.go`, add:

```go
// Flatten zeroes every position and emits a reconcile. (Real brokers close via
// market orders that arrive back as fills; the sim shortcuts to a flat
// reconcile — sufficient for E2E/practice.)
func (b *Broker) Flatten(_ context.Context) error {
	b.mu.Lock()
	for _, p := range b.pos {
		p.Qty = 0
		p.AvgPrice = 0
	}
	post := []exec.BrokerEvent{exec.BrokerPositions{V: b.venue, Positions: b.positionsLocked()}}
	b.mu.Unlock()
	for _, e := range post {
		b.emit(e)
	}
	return nil
}
```

In `engine/internal/exec/core.go`, change `handleFlatten`'s goroutine from `b.CancelAll(ctx, "")` to the native primitive:

```go
	go func() {
		if err := b.Flatten(ctx); err != nil {
			slog.Warn("exec: flatten failed", "venue", cm.Venue, "err", err)
		}
	}()
```

(Delete the now-stale comment about "modeled as cancel-all here.")

- [ ] **Step 4: Run to verify it passes**

Run: `cd engine && go test ./internal/broker/sim/ ./internal/exec/ -run 'Flatten' -v`
Expected: PASS.

- [ ] **Step 5: Full build + race (interface change ripples to all implementers)**

Run: `cd engine && go build ./... && go test -race ./internal/exec/ ./internal/broker/...`
Expected: PASS — the only `exec.Broker` implementer so far is SimBroker; TZ/Alpaca (later tasks) will be required by the compiler to add `Flatten`.

- [ ] **Step 6: Commit**

```bash
cd engine && git add internal/exec/broker.go internal/exec/core.go internal/broker/sim/sim.go internal/broker/sim/sim_test.go internal/exec/core_test.go
git commit -m "feat(engine/exec): Broker.Flatten native primitive; Core uses it for FlattenAll venues

Replaces the cancel-all stand-in in handleFlatten. SimBroker flattens by zeroing
positions; TZ will report unsupported, Alpaca will DELETE /v2/positions."
```

---

# Part B — Shared adapter foundation

### Task 5: `broker/netx` (backoff, token bucket, HTTP client) + `creds` loader + WS dependency

**Files:**
- Create: `engine/internal/broker/netx/backoff.go` + `backoff_test.go`
- Create: `engine/internal/broker/netx/ratelimit.go` + `ratelimit_test.go`
- Create: `engine/internal/broker/netx/httpclient.go` + `httpclient_test.go`
- Create: `engine/internal/creds/creds.go` + `creds_test.go`
- Modify: `engine/go.mod`, `engine/go.sum` (add `github.com/coder/websocket`)

**Interfaces:**
- Produces:
  - `netx.Backoff{Min, Max time.Duration}`; `(*Backoff).Reset()`; `(*Backoff).Next() time.Duration` (full-jitter exponential in `[Min, cur]`, cur doubling up to `Max`).
  - `netx.TokenBucket`; `netx.NewTokenBucket(clk clock.Clock, ratePerSec float64, burst int) *TokenBucket`; `(*TokenBucket).Allow() bool` (non-blocking: lazy-refill by elapsed time, take one if available); `(*TokenBucket).Take(ctx context.Context) error` (blocks via `clk.After` until a token or ctx done).
  - `netx.NewHTTPClient(timeout time.Duration) *http.Client` (keep-alive pool: `MaxIdleConnsPerHost` raised, `ForceAttemptHTTP2`, idle-conn reuse; the cold-TLS-avoidance the Alpaca doc requires).
  - `creds.Pair{KeyID, SecretKey string}`; `creds.File` (`map[string]Pair`); `creds.Load(path string) (File, error)`; `(File).Get(key string) (Pair, error)`; `creds.DefaultPath() string` (→ `~/.eJournal/credentials.json`).

- [ ] **Step 1: Add the WebSocket dependency**

Run:
```bash
cd engine && go get github.com/coder/websocket@latest && go mod tidy
```
Expected: `go.mod` gains `github.com/coder/websocket vX.Y.Z` (latest v1.8.x). Verify no other unexpected modules were added.

- [ ] **Step 2: Write failing tests for `Backoff`**

`engine/internal/broker/netx/backoff_test.go`:

```go
package netx

import (
	"testing"
	"time"
)

func TestBackoff_ExponentialWithinBounds(t *testing.T) {
	b := Backoff{Min: time.Second, Max: 30 * time.Second}
	for i := 0; i < 10; i++ {
		d := b.Next()
		if d < b.Min || d > b.Max {
			t.Fatalf("delay %v out of [%v,%v]", d, b.Min, b.Max)
		}
	}
}

func TestBackoff_ResetReturnsToMin(t *testing.T) {
	b := Backoff{Min: time.Second, Max: 30 * time.Second}
	for i := 0; i < 5; i++ {
		b.Next()
	}
	b.Reset()
	if d := b.Next(); d != b.Min { // first Next after reset returns exactly Min (span==0)
		t.Fatalf("after reset first delay = %v, want %v", d, b.Min)
	}
}
```

- [ ] **Step 3: Implement `Backoff`**

`engine/internal/broker/netx/backoff.go`:

```go
// Package netx holds the network plumbing shared by broker adapters: jittered
// backoff, a clock-injected token-bucket rate limiter, and a keep-alive HTTP
// client factory. It imports only clock + stdlib and must never import exec or
// an adapter, so both adapters can depend on it without cycles.
package netx

import (
	"math/rand/v2"
	"time"
)

// Backoff yields full-jitter exponential delays in [Min, cur], cur doubling from
// Min up to Max. Mirrors the feed/opend reconnect policy.
type Backoff struct {
	Min, Max time.Duration
	cur      time.Duration
}

func (b *Backoff) Reset() { b.cur = 0 }

func (b *Backoff) Next() time.Duration {
	if b.cur == 0 {
		b.cur = b.Min
	} else {
		b.cur *= 2
		if b.cur > b.Max {
			b.cur = b.Max
		}
	}
	span := b.cur - b.Min
	if span <= 0 {
		return b.Min
	}
	return b.Min + rand.N(span)
}
```

- [ ] **Step 4: Write failing tests for `TokenBucket`**

`engine/internal/broker/netx/ratelimit_test.go`:

```go
package netx

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func TestTokenBucket_BurstThenRefill(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	tb := NewTokenBucket(clk, 2, 2) // 2/sec, burst 2

	if !tb.Allow() || !tb.Allow() {
		t.Fatal("first two Allow() should succeed (full burst)")
	}
	if tb.Allow() {
		t.Fatal("third Allow() should fail (bucket empty)")
	}
	clk.Advance(500 * time.Millisecond) // +1 token at 2/sec
	if !tb.Allow() {
		t.Fatal("after 500ms one token should be available")
	}
	if tb.Allow() {
		t.Fatal("only one token refilled")
	}
}

func TestTokenBucket_CapsAtBurst(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	tb := NewTokenBucket(clk, 10, 3)
	clk.Advance(10 * time.Second) // would refill 100, but caps at burst=3
	n := 0
	for tb.Allow() {
		n++
	}
	if n != 3 {
		t.Fatalf("bucket should cap at burst 3, drained %d", n)
	}
}
```

- [ ] **Step 5: Implement `TokenBucket`**

`engine/internal/broker/netx/ratelimit.go`:

```go
package netx

import (
	"context"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// TokenBucket is a clock-injected token bucket. Allow() is non-blocking;
// Take(ctx) blocks (via clk.After) until a token frees or ctx ends.
type TokenBucket struct {
	clk    clock.Clock
	rate   float64 // tokens per second
	burst  float64
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func NewTokenBucket(clk clock.Clock, ratePerSec float64, burst int) *TokenBucket {
	return &TokenBucket{clk: clk, rate: ratePerSec, burst: float64(burst), tokens: float64(burst), last: clk.Now()}
}

func (tb *TokenBucket) refillLocked() {
	now := tb.clk.Now()
	if elapsed := now.Sub(tb.last).Seconds(); elapsed > 0 {
		tb.tokens += elapsed * tb.rate
		if tb.tokens > tb.burst {
			tb.tokens = tb.burst
		}
		tb.last = now
	}
}

func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refillLocked()
	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

// waitLocked returns how long until the next whole token; 0 if one is ready.
func (tb *TokenBucket) waitLocked() time.Duration {
	tb.refillLocked()
	if tb.tokens >= 1 {
		return 0
	}
	need := 1 - tb.tokens
	return time.Duration(need / tb.rate * float64(time.Second))
}

func (tb *TokenBucket) Take(ctx context.Context) error {
	for {
		tb.mu.Lock()
		wait := tb.waitLocked()
		if wait == 0 {
			tb.tokens--
			tb.mu.Unlock()
			return nil
		}
		tb.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tb.clk.After(wait):
		}
	}
}
```

- [ ] **Step 6: Write + implement the HTTP client factory**

`engine/internal/broker/netx/httpclient_test.go`:

```go
package netx

import (
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPClient_KeepAliveTuned(t *testing.T) {
	c := NewHTTPClient(5 * time.Second)
	if c.Timeout != 5*time.Second {
		t.Fatalf("timeout = %v", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatal("transport is not *http.Transport")
	}
	if tr.MaxIdleConnsPerHost < 4 || tr.IdleConnTimeout == 0 {
		t.Fatalf("keep-alive not tuned: idlePerHost=%d idleTimeout=%v", tr.MaxIdleConnsPerHost, tr.IdleConnTimeout)
	}
}
```

`engine/internal/broker/netx/httpclient.go`:

```go
package netx

import (
	"net"
	"net/http"
	"time"
)

// NewHTTPClient returns an *http.Client with a warm keep-alive connection pool.
// Alpaca's cold TLS is ~430ms vs ~210ms warm, so reuse is mandatory; TZ benefits
// equally.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        32,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}
```

- [ ] **Step 7: Write + implement `creds`**

`engine/internal/creds/creds_test.go`:

```go
package creds_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/creds"
)

func TestLoadAndGet(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(p, []byte(`{"tradeZero":{"keyId":"K1","secretKey":"S1"},"alpaca":{"keyId":"K2","secretKey":"S2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := creds.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	tz, err := f.Get("tradeZero")
	if err != nil || tz.KeyID != "K1" || tz.SecretKey != "S1" {
		t.Fatalf("tradeZero pair wrong: %+v err=%v", tz, err)
	}
	if _, err := f.Get("nope"); err == nil {
		t.Fatal("Get of missing key should error")
	}
}

func TestLoad_MissingFileErrors(t *testing.T) {
	if _, err := creds.Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("missing credentials file should error")
	}
}
```

`engine/internal/creds/creds.go`:

```go
// Package creds reads eTape's broker credentials from
// ~/.eJournal/credentials.json (shared with eJournal). Values are secrets:
// never log them, never commit them.
package creds

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Pair struct {
	KeyID     string `json:"keyId"`
	SecretKey string `json:"secretKey"`
}

type File map[string]Pair

// DefaultPath is ~/.eJournal/credentials.json.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "credentials.json"
	}
	return filepath.Join(home, ".eJournal", "credentials.json")
}

// Load reads and parses the credentials file. A missing file IS an error here
// (unlike bootstrap config): an adapter asked for creds because it needs them.
func Load(path string) (File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("creds: %w", err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("creds %s: %w", path, err)
	}
	return f, nil
}

func (f File) Get(key string) (Pair, error) {
	p, ok := f[key]
	if !ok || p.KeyID == "" || p.SecretKey == "" {
		return Pair{}, fmt.Errorf("creds: no usable %q entry", key)
	}
	return p, nil
}
```

- [ ] **Step 8: Run all Task-5 tests + race**

Run: `cd engine && go test -race ./internal/broker/netx/ ./internal/creds/ -v`
Expected: PASS (all).

- [ ] **Step 9: Commit**

```bash
cd engine && git add go.mod go.sum internal/broker/netx/ internal/creds/
git commit -m "feat(engine/broker): netx (backoff, token bucket, keep-alive http) + creds loader

Shared adapter plumbing; add github.com/coder/websocket. creds reads
~/.eJournal/credentials.json (never logged/committed)."
```

---

# Part C — TradeZero adapter (`internal/broker/tradezero`)

REST base `https://webapi.tradezero.com`, WS `wss://webapi.tradezero.com/stream`; key pair selects paper vs live. **All tests here use an `httptest` mock — never the real endpoint** (only LIVE keys exist; safety rule).

### Task 6: TradeZero wire mapping

**Files:**
- Create: `engine/internal/broker/tradezero/mapping.go` + `mapping_test.go`

**Interfaces:**
- Consumes: `exec.OrderType/Side/TIF`, `session.PhaseAt`, `clock.Clock`.
- Produces (all pure, in package `tradezero`):
  - `orderTypeWire(exec.OrderType) (string, error)` → `"Market"|"Limit"|"Stop"|"StopLimit"`.
  - `sideWire(exec.Side) (side, openClose string)` → Buy=`Buy/Open`, Sell=`Sell/Close`, Short=`Sell/Open`, Cover=`Buy/Close`.
  - `sideDomain(wireSide, openClose string) exec.Side` (un-enriches `SellShort` via `openClose`).
  - `tifWire(t exec.TIF, extendedHours bool, ot exec.OrderType) string` → `Day`→`Day_Plus` / `GTC`→`GTC_Plus` **only for plain `TypeLimit` during ext-hours** (TZ's `_Plus` TIFs are Limit-only); `IOC`→`ImmediateOrCancel`; `FOK`→`FillOrKill`; else `Day`/`GoodTillCancel`.
  - `isExtendedHours(clk clock.Clock) bool` → true when `session.PhaseAt(clk.Now())` ∈ {PreMarket, PostMarket}.

- [ ] **Step 1: Write the failing tests**

`engine/internal/broker/tradezero/mapping_test.go`:

```go
package tradezero

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

func TestOrderTypeWire(t *testing.T) {
	cases := map[exec.OrderType]string{
		exec.TypeMarket: "Market", exec.TypeLimit: "Limit",
		exec.TypeStop: "Stop", exec.TypeStopLimit: "StopLimit",
	}
	for ot, want := range cases {
		got, err := orderTypeWire(ot)
		if err != nil || got != want {
			t.Errorf("orderTypeWire(%v) = %q,%v want %q", ot, got, err, want)
		}
	}
}

func TestSideWireAndBack(t *testing.T) {
	cases := []struct {
		s              exec.Side
		side, openClse string
	}{
		{exec.SideBuy, "Buy", "Open"},
		{exec.SideSell, "Sell", "Close"},
		{exec.SideShort, "Sell", "Open"},
		{exec.SideCover, "Buy", "Close"},
	}
	for _, c := range cases {
		gs, go_ := sideWire(c.s)
		if gs != c.side || go_ != c.openClse {
			t.Errorf("sideWire(%v) = %q/%q want %q/%q", c.s, gs, go_, c.side, c.openClse)
		}
		// round trip: responses enrich short to SellShort; derive via openClose.
		wire := gs
		if c.s == exec.SideShort {
			wire = "SellShort"
		}
		if back := sideDomain(wire, c.openClse); back != c.s {
			t.Errorf("sideDomain(%q,%q) = %v want %v", wire, c.openClse, back, c.s)
		}
	}
}

func TestTifWire_ExtendedHoursCoercion(t *testing.T) {
	if got := tifWire(exec.TIFDay, true, exec.TypeLimit); got != "Day_Plus" {
		t.Errorf("ext-hours Day limit -> %q want Day_Plus", got)
	}
	if got := tifWire(exec.TIFDay, false, exec.TypeLimit); got != "Day" {
		t.Errorf("RTH Day -> %q want Day", got)
	}
	if got := tifWire(exec.TIFGTC, true, exec.TypeLimit); got != "GTC_Plus" {
		t.Errorf("ext-hours GTC limit -> %q want GTC_Plus", got)
	}
	if got := tifWire(exec.TIFGTC, true, exec.TypeStopLimit); got != "GoodTillCancel" {
		t.Errorf("ext-hours GTC stop-limit keeps base TIF (_Plus is Limit-only) -> %q want GoodTillCancel", got)
	}
}

func TestIsExtendedHours(t *testing.T) {
	// 08:00 ET (pre-market) on a weekday.
	et := time.Date(2026, 7, 6, 8, 0, 0, 0, mustET(t))
	if !isExtendedHours(clock.NewFake(et)) {
		t.Fatal("08:00 ET should be extended hours")
	}
	// 10:00 ET (RTH).
	et = time.Date(2026, 7, 6, 10, 0, 0, 0, mustET(t))
	if isExtendedHours(clock.NewFake(et)) {
		t.Fatal("10:00 ET should not be extended hours")
	}
}
```

Add a small helper `mustET` (shared by TZ tests) in `mapping_test.go`:

```go
func mustET(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	return loc
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd engine && go test ./internal/broker/tradezero/ -run 'Mapping|Wire|Tif|ExtendedHours' -v`
Expected: FAIL — undefined mapping functions (compile error).

- [ ] **Step 3: Implement `mapping.go`**

`engine/internal/broker/tradezero/mapping.go`:

```go
package tradezero

import (
	"fmt"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func orderTypeWire(t exec.OrderType) (string, error) {
	switch t {
	case exec.TypeMarket:
		return "Market", nil
	case exec.TypeLimit:
		return "Limit", nil
	case exec.TypeStop:
		return "Stop", nil
	case exec.TypeStopLimit:
		return "StopLimit", nil
	default:
		return "", fmt.Errorf("tradezero: unsupported order type %v", t)
	}
}

// sideWire maps a trader action to TZ side+openClose. Never sends "SellShort".
func sideWire(s exec.Side) (side, openClose string) {
	switch s {
	case exec.SideBuy:
		return "Buy", "Open"
	case exec.SideSell:
		return "Sell", "Close"
	case exec.SideShort:
		return "Sell", "Open"
	case exec.SideCover:
		return "Buy", "Close"
	default:
		return "Buy", "Open"
	}
}

// sideDomain un-enriches a TZ response side (which may be "SellShort") using
// openClose. Buy/Open=Buy, Sell/Close=Sell, Sell(or SellShort)/Open=Short,
// Buy/Close=Cover.
func sideDomain(wireSide, openClose string) exec.Side {
	buyish := wireSide == "Buy"
	if buyish {
		if openClose == "Close" {
			return exec.SideCover
		}
		return exec.SideBuy
	}
	if openClose == "Open" {
		return exec.SideShort
	}
	return exec.SideSell
}

// tifWire maps domain TIF to TZ, coercing Day/GTC to their _Plus variants for
// extended-hours limit/stop-limit orders (avoids TZ rejecting a plain Day limit
// placed outside RTH).
func tifWire(t exec.TIF, extendedHours bool, ot exec.OrderType) string {
	// TZ's _Plus TIFs are documented Limit-ONLY (docs/2026-07-03-tradezero-api.md).
	// Extended-hours stop-limit behaviour on TZ is unverified (Monday-live item);
	// keep a stop-limit's base TIF rather than sending a Limit-only _Plus it may
	// reject.
	limitish := ot == exec.TypeLimit
	switch t {
	case exec.TIFDay:
		if extendedHours && limitish {
			return "Day_Plus"
		}
		return "Day"
	case exec.TIFGTC:
		if extendedHours && limitish {
			return "GTC_Plus"
		}
		return "GoodTillCancel"
	case exec.TIFIOC:
		return "ImmediateOrCancel"
	case exec.TIFFOK:
		return "FillOrKill"
	default:
		return "Day"
	}
}

func isExtendedHours(clk clock.Clock) bool {
	switch session.PhaseAt(clk.Now()) {
	case session.PreMarket, session.PostMarket:
		return true
	default:
		return false
	}
}
```

> **Market-outside-RTH note (code comment):** eTape does *not* coerce a Market order to Limit-at-last inside the adapter — the adapter has no last-trade price (that lives in the Core's `MarkSource`), and the UI already coerces Market→Limit outside RTH (`preChecks.ts`). If a Market order still reaches TZ outside RTH, TZ replies `R78`, which the adapter surfaces as `OrderRejected` (Task 8). This is the deliberate division of responsibility.

- [ ] **Step 4: Run to verify it passes + race**

Run: `cd engine && go test -race ./internal/broker/tradezero/ -run 'Wire|Tif|ExtendedHours' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/broker/tradezero/mapping.go internal/broker/tradezero/mapping_test.go
git commit -m "feat(engine/broker/tradezero): domain<->wire mapping (side+openClose, order type, TIF, ext-hours)

All four order types map to TZ Market/Limit/Stop/StopLimit; ext-hours coerces
Day/GTC to _Plus for limit/stop-limit."
```

### Task 7: TradeZero normalization (WS/REST field quirks → domain)

**Files:**
- Create: `engine/internal/broker/tradezero/normalize.go` + `normalize_test.go`
- Create: `engine/internal/broker/tradezero/testdata/order_*.json`, `position.json` (authored fixtures)

**Interfaces:**
- Consumes: mapping (Task 6), `exec` types.
- Produces:
  - `type tzOrder struct { … }` — a decode-tolerant struct covering both REST and Portfolio-WS field spellings (`account`/`accountId`, `userOrderId`, `status`/`orderStatus`, `lastQty`, `orderQuantity`, `executed`, `priceAvg`, `limitPrice`, `stopPrice`, `side`, `openClose`, `orderType`, `text`). Fills derive from `executed`/`lastQty`, so `cancelledQuantity` is not decoded; add it only if a real capture shows it is needed.
  - `splitUserOrderID(userOrderID string) (accountID, clientOrderID string)` (split on first `:`).
  - `statusDomain(tzStatus string) exec.OrderStatus`.
  - `func (a *Adapter) normalizeOrder(venue exec.VenueID, o tzOrder) []exec.BrokerEvent` — turns one order object into the domain events it implies (status transition + any new fill), using the fill-dedup set. **Fill derivation:** when `executed` (cumulative) increased since the last seen value for this order and `lastQty > 0`, emit one `OrderFilled` with `Qty=lastQty`, `CumQty=executed`, `LeavesQty=orderQuantity-executed`, `AvgPrice=priceAvg`; dedup key `(clientOrderID, executed)`.

- [ ] **Step 1: Author golden fixtures**

Create `testdata/order_partial_fill.json` (authored from the TZ docs' Portfolio order shape — field names verbatim from `docs/2026-07-03-tradezero-api.md`):

```json
{
  "action": "update",
  "account": "2TZ00001",
  "userOrderId": "2TZ00001:ET01J000000000000000000001",
  "symbol": "AAPL",
  "orderType": "Limit",
  "side": "Buy",
  "openClose": "Open",
  "orderQuantity": 100,
  "executed": 40,
  "lastQty": 40,
  "limitPrice": 190.50,
  "stopPrice": 0,
  "priceAvg": 190.48,
  "orderStatus": "PartiallyFilled"
}
```

Create `testdata/order_short_new.json` (response enriches side to `SellShort`):

```json
{
  "action": "update",
  "accountId": "2TZ00001",
  "userOrderId": "2TZ00001:ET01J000000000000000000002",
  "symbol": "TSLA",
  "orderType": "Limit",
  "side": "SellShort",
  "openClose": "Open",
  "orderQuantity": 50,
  "executed": 0,
  "lastQty": 0,
  "limitPrice": 250.00,
  "stopPrice": 0,
  "priceAvg": 0,
  "status": "New"
}
```

> Note in a `testdata/README.md`: these are **authored** from documented shapes, not real captures (TZ order-flow frames need real orders, which the safety rule forbids). Replace/augment with real frames captured in an authorized live session; keep decoders tolerant of unknown fields.

- [ ] **Step 2: Write the failing tests**

`engine/internal/broker/tradezero/normalize_test.go`:

```go
package tradezero

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

func loadOrder(t *testing.T, name string) tzOrder {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var o tzOrder
	if err := json.Unmarshal(b, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestSplitUserOrderID(t *testing.T) {
	acct, cid := splitUserOrderID("2TZ00001:ET01J000000000000000000001")
	if acct != "2TZ00001" || cid != "ET01J000000000000000000001" {
		t.Fatalf("split = %q,%q", acct, cid)
	}
	// clientOrderId may itself contain ':' in the derived replace suffix — split only on the first.
	_, cid = splitUserOrderID("2TZ00001:ET...-r1")
	if cid != "ET...-r1" {
		t.Fatalf("cid = %q", cid)
	}
}

func TestNormalizeOrder_PartialFillEmitsOneFill(t *testing.T) {
	a := newTestAdapter(t, "tz") // constructor stub; see Task 10 (or a minimal local one)
	evs := a.normalizeOrder("tz", loadOrder(t, "order_partial_fill.json"))
	var fills, updates int
	for _, e := range evs {
		switch f := e.(type) {
		case exec.OrderFilled:
			fills++
			if f.F.Qty != 40 || f.AvgPrice != 190.48 || f.CumQty != 40 || f.LeavesQty != 60 {
				t.Fatalf("fill fields wrong: %+v", f)
			}
			if f.F.Side != exec.SideBuy {
				t.Fatalf("side = %v", f.F.Side)
			}
		}
	}
	_ = updates
	if fills != 1 {
		t.Fatalf("want 1 fill, got %d", fills)
	}
	// Re-applying the same frame must NOT re-emit the fill (dedup on (id, executed)).
	if evs2 := a.normalizeOrder("tz", loadOrder(t, "order_partial_fill.json")); len(fills2(evs2)) != 0 {
		t.Fatal("duplicate frame re-emitted a fill")
	}
}

func TestNormalizeOrder_ShortUnenriched(t *testing.T) {
	a := newTestAdapter(t, "tz")
	o := loadOrder(t, "order_short_new.json")
	if got := sideDomain(o.Side, o.OpenClose); got != exec.SideShort {
		t.Fatalf("short side = %v", got)
	}
	_ = a
}

func fills2(evs []exec.BrokerEvent) []exec.OrderFilled {
	var out []exec.OrderFilled
	for _, e := range evs {
		if f, ok := e.(exec.OrderFilled); ok {
			out = append(out, f)
		}
	}
	return out
}
```

> `newTestAdapter` is defined once (Task 10) alongside the adapter; for this task add a minimal local constructor if Task 10 isn't landed yet, e.g. `func newTestAdapter(t *testing.T, v exec.VenueID) *Adapter { return &Adapter{venue: v, seenExecuted: map[string]float64{}} }`. Keep it consistent with the real struct fields.

- [ ] **Step 3: Implement `normalize.go`**

`engine/internal/broker/tradezero/normalize.go`:

```go
package tradezero

import (
	"strings"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

// tzOrder is a decode-tolerant view of a TZ order object. It covers both REST
// and Portfolio-WS spellings; unknown fields are ignored by encoding/json.
type tzOrder struct {
	Account       string  `json:"account"`
	AccountID     string  `json:"accountId"`
	UserOrderID   string  `json:"userOrderId"`
	Symbol        string  `json:"symbol"`
	OrderType     string  `json:"orderType"`
	Side          string  `json:"side"`
	OpenClose     string  `json:"openClose"`
	OrderQuantity float64 `json:"orderQuantity"`
	Executed      float64 `json:"executed"`
	LastQty       float64 `json:"lastQty"`
	LimitPrice    float64 `json:"limitPrice"`
	StopPrice     float64 `json:"stopPrice"`
	PriceAvg      float64 `json:"priceAvg"`
	Status        string  `json:"status"`
	OrderStatus   string  `json:"orderStatus"`
	Text          string  `json:"text"`
}

func (o tzOrder) status() string {
	if o.OrderStatus != "" {
		return o.OrderStatus
	}
	return o.Status
}

func splitUserOrderID(u string) (accountID, clientOrderID string) {
	if i := strings.IndexByte(u, ':'); i >= 0 {
		return u[:i], u[i+1:]
	}
	return "", u
}

func statusDomain(s string) exec.OrderStatus {
	switch s {
	case "PendingNew", "New":
		return exec.StatusAccepted
	case "PartiallyFilled":
		return exec.StatusPartiallyFilled
	case "Filled":
		return exec.StatusFilled
	case "Canceled":
		return exec.StatusCanceled
	case "PendingCancel":
		// Non-terminal (TZ: PendingCancel -> Canceled). Must NOT fire the
		// terminal-cancel path — the emulated replace (Task 10) awaits the real
		// Canceled, and a premature signal would resubmit the new leg while the
		// old leg is still resting. Falls through to no domain event.
		return exec.StatusSubmitted
	case "Rejected":
		return exec.StatusRejected
	case "Expired", "DoneForDay":
		return exec.StatusExpired
	default:
		return exec.StatusSubmitted
	}
}

// normalizeOrder turns one order object into the domain events it implies. The
// domain client-order-id is recovered by splitting userOrderId and stripping any
// "-rN" replace suffix (Task 10) so a replace-chain reports as one domain order.
func (a *Adapter) normalizeOrder(venue exec.VenueID, o tzOrder) []exec.BrokerEvent {
	_, tzCID := splitUserOrderID(o.UserOrderID)
	oid := a.domainID(tzCID) // strips "-rN"; identity if no suffix
	ts := a.now()
	var out []exec.BrokerEvent

	// Fill derivation: cumulative executed rose and this slice reported lastQty.
	a.mu.Lock()
	prev := a.seenExecuted[tzCID]
	newFill := o.LastQty > 0 && o.Executed > prev
	if newFill {
		a.seenExecuted[tzCID] = o.Executed
	}
	a.mu.Unlock()
	if newFill {
		out = append(out, exec.OrderFilled{
			F: exec.Fill{
				Venue: venue, OrderID: oid, Symbol: o.Symbol,
				Side: sideDomain(o.Side, o.OpenClose), Qty: o.LastQty, Price: o.PriceAvg, TsMs: ts,
			},
			CumQty: o.Executed, LeavesQty: o.OrderQuantity - o.Executed, AvgPrice: o.PriceAvg,
		})
	}

	switch statusDomain(o.status()) {
	case exec.StatusAccepted:
		out = append(out, exec.OrderAccepted{V: venue, OID: oid, BrokerOrderID: tzCID, Ts: ts})
	case exec.StatusCanceled:
		out = append(out, a.onCanceled(venue, oid, ts)...) // Task 10 hook: swallow-during-replace
	case exec.StatusRejected:
		out = append(out, exec.OrderRejected{V: venue, OID: oid, Reason: rejectText(o.Text), Ts: ts})
	case exec.StatusExpired:
		out = append(out, exec.OrderExpired{V: venue, OID: oid, Ts: ts})
	}
	return out
}

func rejectText(t string) string {
	if t == "" {
		return "rejected"
	}
	return t
}
```

> `a.domainID`, `a.onCanceled`, `a.now`, `a.mu`, `a.seenExecuted` are `Adapter` members introduced in Task 10; this task compiles once `Adapter` exists with those fields. If implementing strictly in order, add the `Adapter` struct skeleton (fields only) at the top of `tradezero.go` in this task and flesh its methods in Tasks 8–10 — the subagent-driven flow reviews per task, so declaring the struct early is fine as long as this task's tests pass.

- [ ] **Step 4: Run to verify passes + race**

Run: `cd engine && go test -race ./internal/broker/tradezero/ -run 'Normalize|SplitUserOrderID' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/broker/tradezero/normalize.go internal/broker/tradezero/normalize_test.go internal/broker/tradezero/testdata/
git commit -m "feat(engine/broker/tradezero): order normalization + fill derivation + dedup

Handles WS/REST field-spelling quirks, splits userOrderId, un-enriches SellShort,
derives one OrderFilled per execution slice with (id,executed) dedup. Golden
fixtures authored from docs (real captures pending an authorized live session)."
```

### Task 8: TradeZero REST client

**Files:**
- Create: `engine/internal/broker/tradezero/rest.go` + `rest_test.go`
- Create: `engine/internal/broker/tradezero/testdata/{accounts,positions,routes,order_reject_r78,order_reject_r114,order_accept}.json`

**Interfaces:**
- Consumes: `netx.TokenBucket`, `netx.NewHTTPClient`, `creds.Pair`, mapping/normalize.
- Produces (methods on `*restClient` or `*Adapter`):
  - `submitOrder(ctx, req exec.OrderRequest, tzClientOrderID, route string) (brokerAccepted bool, reason string, err error)` — POSTs `/v1/api/accounts/{id}/order`; **reads `orderStatus` from the HTTP-200 body** (TZ never non-200s a semantic rejection); parses `R##:` text; the **transport-failure retry-once-same-ID + R114 probe** lives here.
  - `cancelOrder(ctx, tzClientOrderID string) error` — `DELETE …/orders/{clientOrderId}`; a `404` is resolved via a `GET …/orders` truth poll, never assumed.
  - `cancelAll(ctx, symbol string) error` — `DELETE /v1/api/accounts/orders` with form `account=` (+ optional `?symbol=`).
  - `snapshot(ctx) (exec.AccountSnapshot, []exec.Position, []exec.Order, error)` — `GET /account/{id}` + `/pnl` + `/positions` + `/orders`.
  - `fetchRoutes(ctx) ([]route, error)` and `pickRoute(secType string) string`.

- [ ] **Step 1: Author fixtures**

`testdata/order_reject_r114.json`:
```json
{ "orderStatus": "Rejected", "text": "R114: Duplicate clientOrderId" }
```
`testdata/order_reject_r78.json`:
```json
{ "orderStatus": "Rejected", "text": "R78: Market order not allowed outside regular trading hours" }
```
`testdata/order_accept.json`:
```json
{ "orderStatus": "New", "userOrderId": "2TZ00001:ET01J000000000000000000001" }
```
`testdata/accounts.json` (superset-tolerant; real payload has extra fields):
```json
[{ "accountId": "2TZ00001", "accountType": "Live", "equity": 100000, "buyingPower": 200000, "availableCash": 100000, "sodEquity": 99000, "leverage": 2 }]
```
`testdata/positions.json`:
```json
[{ "positionId": "p1", "symbol": "AAPL", "side": "Long", "shares": 100, "priceAvg": 189.90 }]
```
`testdata/routes.json`:
```json
[{ "route": "SMART", "securityTypes": ["Stock"], "orderTypes": ["Limit","Market","Stop","StopLimit"] }]
```

- [ ] **Step 2: Write the failing tests (against an `httptest` mock — NEVER the real endpoint)**

`engine/internal/broker/tradezero/rest_test.go`:

```go
package tradezero

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

func serveFile(t *testing.T, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
}

func TestSubmit_HTTP200Rejected_R114(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001/order", serveFile(t, "order_reject_r114.json"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	accepted, reason, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100,
		ClientOrderID: "ET-dup",
	}, "ET-dup", "SMART")
	if err != nil {
		t.Fatalf("submit returned transport err: %v", err)
	}
	if accepted {
		t.Fatal("HTTP 200 with orderStatus Rejected must NOT be treated as accepted")
	}
	if reason == "" || reason[:4] != "R114" {
		t.Fatalf("reason should carry the R-code, got %q", reason)
	}
}

func TestSnapshot_ParsesAccountsPositions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/accounts/2TZ00001", serveFile(t, "accounts.json"))            // GET /account/{id} not used; simplify
	mux.HandleFunc("/v1/api/accounts/2TZ00001/pnl", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"dayPnl":-25.5,"realized":10}`)) })
	mux.HandleFunc("/v1/api/accounts/2TZ00001/positions", serveFile(t, "positions.json"))
	mux.HandleFunc("/v1/api/accounts/2TZ00001/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rc := newRESTClient(srv.URL, "2TZ00001", "K", "S", clock.NewFake(time.UnixMilli(0)))
	acct, pos, _, err := rc.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acct.DayPnL != -25.5 {
		t.Fatalf("dayPnL = %v", acct.DayPnL)
	}
	if len(pos) != 1 || pos[0].Qty != 100 || pos[0].Symbol != "AAPL" {
		t.Fatalf("positions = %+v", pos)
	}
}
```

> **Adjust the account path/handlers to match the exact endpoints you implement** (the reconstructed OpenAPI in `docs/tradezero/tradezero-openapi.json` is authoritative for path shapes). The point of the test is: 200-with-Rejected is not an accept, and snapshot parses. Set `TZ-API-KEY-ID`/`TZ-API-SECRET-KEY` headers on every request.

- [ ] **Step 3: Implement `rest.go`**

`engine/internal/broker/tradezero/rest.go` — key structure (full submit method shown; the GET helpers follow the same `do`/decode pattern against the endpoints in the table):

```go
package tradezero

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
	"github.com/earlisreal/eTape/engine/internal/exec"
)

type restClient struct {
	base      string
	accountID string
	keyID     string
	secret    string
	hc        *http.Client
	clk       clock.Clock

	// per-endpoint token buckets (TZ documented limits).
	bOrder  *netx.TokenBucket // POST /order: 10/s
	bCancel *netx.TokenBucket // DELETE /orders/{id}: 15/s
	bCanAll *netx.TokenBucket // DELETE /orders: 3/s
	bGet    *netx.TokenBucket // GET orders/order: 2/s
	bAcct   *netx.TokenBucket // GET positions/pnl/account: 3/s
	bRoutes *netx.TokenBucket // GET routes: 1/s
}

func newRESTClient(base, accountID, keyID, secret string, clk clock.Clock) *restClient {
	return &restClient{
		base: base, accountID: accountID, keyID: keyID, secret: secret,
		hc: netx.NewHTTPClient(10 * time.Second), clk: clk,
		bOrder:  netx.NewTokenBucket(clk, 10, 10),
		bCancel: netx.NewTokenBucket(clk, 15, 15),
		bCanAll: netx.NewTokenBucket(clk, 3, 3),
		bGet:    netx.NewTokenBucket(clk, 2, 2),
		bAcct:   netx.NewTokenBucket(clk, 3, 3),
		bRoutes: netx.NewTokenBucket(clk, 1, 1),
	}
}

func (rc *restClient) do(ctx context.Context, method, path string, body io.Reader, bucket *netx.TokenBucket) (*http.Response, error) {
	if err := bucket.Take(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, rc.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("TZ-API-KEY-ID", rc.keyID)
	req.Header.Set("TZ-API-SECRET-KEY", rc.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return rc.hc.Do(req)
}

type orderResp struct {
	OrderStatus string `json:"orderStatus"`
	UserOrderID string `json:"userOrderId"`
	Text        string `json:"text"`
}

// submitOrder POSTs an order. TZ returns HTTP 200 even for a semantic rejection,
// so we read orderStatus, not the HTTP code. On a transport failure (no HTTP
// response) it retries ONCE with the same client-order-id: an R114 (duplicate)
// on the retry means the original landed; a clean accept means it did not.
func (rc *restClient) submitOrder(ctx context.Context, req exec.OrderRequest, tzClientOrderID, route string) (bool, string, error) {
	ot, err := orderTypeWire(req.Type)
	if err != nil {
		return false, "", err
	}
	side, openClose := sideWire(req.Side)
	payload := map[string]any{
		"symbol":        req.Symbol,
		"orderQuantity": int(req.Qty),
		"orderType":     ot,
		"timeInForce":   tifWire(req.TIF, isExtendedHours(rc.clk), req.Type),
		"securityType":  "Stock",
		"side":          side,
		"openClose":     openClose,
		"clientOrderId": tzClientOrderID,
		"route":         route,
	}
	if req.Type == exec.TypeLimit || req.Type == exec.TypeStopLimit {
		payload["limitPrice"] = req.LimitPrice
	}
	if req.Type == exec.TypeStop || req.Type == exec.TypeStopLimit {
		payload["stopPrice"] = req.StopPrice
	}
	buf, _ := json.Marshal(payload)

	attempt := func() (orderResp, bool, error) { // (parsed, transportOK, err)
		resp, err := rc.do(ctx, http.MethodPost, "/v1/api/accounts/"+rc.accountID+"/order", strings.NewReader(string(buf)), rc.bOrder)
		if err != nil {
			return orderResp{}, false, err // transport failure — no HTTP response
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusBadRequest {
			return orderResp{OrderStatus: "Rejected", Text: "HTTP 400 schema: " + string(b)}, true, nil
		}
		var or orderResp
		_ = json.Unmarshal(b, &or)
		return or, true, nil
	}

	or, ok, err := attempt()
	if !ok { // transport failure -> retry once with the SAME id (R114 probe)
		or, ok, err = attempt()
		if !ok {
			return false, "", fmt.Errorf("tradezero: submit transport failed twice: %w", err)
		}
		if strings.HasPrefix(or.Text, "R114") {
			// duplicate -> the ORIGINAL landed; treat as accepted.
			return true, "", nil
		}
	}
	if or.OrderStatus == "Rejected" {
		return false, or.Text, nil
	}
	return true, "", nil
}
```

Cancel / cancel-all / snapshot / routes follow the same `rc.do(...)` shape against the endpoints below; implement each and its decode:

| Method | HTTP | Path | Bucket | Notes |
|---|---|---|---|---|
| `cancelOrder(id)` | DELETE | `/v1/api/accounts/{acct}/orders/{id}` | `bCancel` | `404` → `GET …/orders` truth poll, don't assume terminal |
| `cancelAll(sym)` | DELETE | `/v1/api/accounts/orders` (+`?symbol=`), form `account={acct}` | `bCanAll` | 3/s, no burst |
| `snapshot` | GET×4 | `/account/{acct}`, `/accounts/{acct}/pnl`, `/positions`, `/orders` | `bAcct`,`bGet` | positions `side==Short` → negative `Qty`; `dayPnl`→`AccountSnapshot.DayPnL` |
| `fetchRoutes` | GET | `/accounts/{acct}/routes` | `bRoutes` | `pickRoute` returns the config default validated to exist; paper auto-assigns |

Position sign: `Qty = shares` when `side=="Long"`, `-shares` when `"Short"`.

- [ ] **Step 4: Run + race**

Run: `cd engine && go test -race ./internal/broker/tradezero/ -run 'Submit|Snapshot|Cancel|Routes' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/broker/tradezero/rest.go internal/broker/tradezero/rest_test.go internal/broker/tradezero/testdata/
git commit -m "feat(engine/broker/tradezero): REST client — 200-rejected trap, R-codes, retry-once probe

POST reads orderStatus not HTTP code; transport-failure retry-once-same-id with
R114 dup-probe; per-endpoint token buckets; snapshot parses accounts/pnl/positions."
```

### Task 9: TradeZero Portfolio WebSocket client

**Files:**
- Create: `engine/internal/broker/tradezero/ws.go` + `ws_test.go`

**Interfaces:**
- Consumes: `github.com/coder/websocket`, `netx.Backoff`, `clock.Clock`, `creds.Pair`, `normalizeOrder` (Task 7).
- Produces:
  - `type wsClient struct { … }`; `newWSClient(wsURL, accountID, keyID, secret string, clk clock.Clock, onOrder func(tzOrder), onPosition func(tzPosition), onConn func(up bool)) *wsClient`.
  - `(*wsClient).run(ctx)` — the connect→handshake→subscribe→read loop with reconnect. Handshake: read `{"@system":true,"status":"PENDING_AUTH"}` → send `{"key":…,"secret":…}` → expect `CONNECTED` → send `{"accountId":…,"subscriptions":["Order","Position"]}`. `FAILED_AUTH` → stop (never reconnect). `TERMINATED`/`INVALID_DATA` → keep the connection, log, resend subscribe. No server ping → own staleness timer + reconnect on read error/timeout with `netx.Backoff`.
  - Parses `action:"update"` frames into `tzOrder`/`tzPosition` and dispatches via the callbacks.

- [ ] **Step 1: Write the failing test (mock WS server via `coder/websocket` on an `httptest` server)**

`engine/internal/broker/tradezero/ws_test.go`:

```go
package tradezero

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/clock"
)

// mockTZ serves the 3-step handshake then pushes one order update.
func mockTZ(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"PENDING_AUTH"}`))
		_, auth, _ := c.Read(ctx) // {"key":..,"secret":..}
		if !json.Valid(auth) {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"@system":true,"status":"CONNECTED"}`))
		_, _, _ = c.Read(ctx) // subscribe payload
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"action":"update","userOrderId":"2TZ00001:ETx","symbol":"AAPL","orderStatus":"New","orderQuantity":10,"executed":0,"orderType":"Limit","side":"Buy","openClose":"Open"}`))
		<-ctx.Done()
	}))
}

func TestWS_HandshakeAndOrderDispatch(t *testing.T) {
	srv := mockTZ(t)
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):] // http->ws

	var mu sync.Mutex
	var got []tzOrder
	ws := newWSClient(wsURL, "2TZ00001", "K", "S", clock.System{},
		func(o tzOrder) { mu.Lock(); got = append(got, o); mu.Unlock() },
		func(tzPosition) {}, func(bool) {})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ws.run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 1 {
			if got[0].Symbol != "AAPL" {
				t.Fatalf("order symbol = %q", got[0].Symbol)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("did not receive the order update within timeout")
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd engine && go test ./internal/broker/tradezero/ -run 'TestWS_' -v`
Expected: FAIL — `newWSClient` undefined.

- [ ] **Step 3: Implement `ws.go`**

Sketch the real structure (full file); use `websocket.Dial`, read frames opcode-agnostically, run reconnect via `netx.Backoff`. Key methods:

```go
package tradezero

import (
	"context"
	"encoding/json"
	"time"

	"github.com/coder/websocket"
	"github.com/earlisreal/eTape/engine/internal/broker/netx"
	"github.com/earlisreal/eTape/engine/internal/clock"
)

type tzPosition struct {
	Symbol   string  `json:"symbol"`
	Side     string  `json:"side"`
	Shares   float64 `json:"shares"`
	PriceAvg float64 `json:"priceAvg"`
}

type wsClient struct {
	url, accountID, keyID, secret string
	clk                           clock.Clock
	onOrder                       func(tzOrder)
	onPosition                    func(tzPosition)
	onConn                        func(up bool)
}

func newWSClient(url, accountID, keyID, secret string, clk clock.Clock, onOrder func(tzOrder), onPosition func(tzPosition), onConn func(bool)) *wsClient {
	return &wsClient{url: url, accountID: accountID, keyID: keyID, secret: secret, clk: clk, onOrder: onOrder, onPosition: onPosition, onConn: onConn}
}

func (w *wsClient) run(ctx context.Context) {
	bo := netx.Backoff{Min: time.Second, Max: 30 * time.Second}
	for ctx.Err() == nil {
		err := w.session(ctx)
		if err == errFailedAuth {
			w.onConn(false)
			return // never reconnect on bad keys
		}
		w.onConn(false)
		select {
		case <-ctx.Done():
			return
		case <-w.clk.After(bo.Next()):
		}
	}
}

var errFailedAuth = errorString("tradezero: FAILED_AUTH")

type errorString string

func (e errorString) Error() string { return string(e) }

// session runs one connection: handshake, subscribe, read until error.
func (w *wsClient) session(ctx context.Context) error {
	c, _, err := websocket.Dial(ctx, w.url, nil)
	if err != nil {
		return err
	}
	defer c.CloseNow()
	// 1) PENDING_AUTH
	if err := w.awaitStatus(ctx, c, "PENDING_AUTH"); err != nil {
		return err
	}
	// 2) send key/secret
	auth, _ := json.Marshal(map[string]string{"key": w.keyID, "secret": w.secret})
	if err := c.Write(ctx, websocket.MessageText, auth); err != nil {
		return err
	}
	// 3) CONNECTED (or FAILED_AUTH)
	if err := w.awaitStatus(ctx, c, "CONNECTED"); err != nil {
		return err
	}
	sub, _ := json.Marshal(map[string]any{"accountId": w.accountID, "subscriptions": []string{"Order", "Position"}})
	if err := c.Write(ctx, websocket.MessageText, sub); err != nil {
		return err
	}
	w.onConn(true)
	return w.readLoop(ctx, c)
}
```

Add `awaitStatus` (reads frames until it sees `{"@system":true,"status":X}`; returns `errFailedAuth` on `FAILED_AUTH`; on `TERMINATED`/`INVALID_DATA` logs and continues within the same connection) and `readLoop` (per-frame: JSON-decode; if it has `action:"update"` and an order shape → `onOrder`; a position shape → `onPosition`; apply a read deadline via `ctx` + a staleness timer so a silent dead socket triggers reconnect). Set a per-read timeout with `context.WithTimeout` around `c.Read`.

- [ ] **Step 4: Run + race**

Run: `cd engine && go test -race ./internal/broker/tradezero/ -run 'TestWS_' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/broker/tradezero/ws.go internal/broker/tradezero/ws_test.go
git commit -m "feat(engine/broker/tradezero): Portfolio WS — 3-step handshake, subscribe, reconnect

PENDING_AUTH -> key/secret -> CONNECTED -> subscribe Order+Position; FAILED_AUTH
never reconnects; no server ping so staleness+jittered-backoff owns liveness."
```

### Task 10: Assemble the TradeZero `exec.Broker` — emulated replace + reconcile

**Files:**
- Create: `engine/internal/broker/tradezero/tradezero.go` + `tradezero_test.go`

**Interfaces:**
- Consumes: everything in Tasks 6–9, `exec.Broker`, `creds`.
- Produces:
  - `tradezero.New(cfg Config) (*Adapter, error)` where `Config{Venue exec.VenueID; AccountID, RESTBase, WSURL, Route string; Creds creds.Pair; Clock clock.Clock}` (defaults: `RESTBase="https://webapi.tradezero.com"`, `WSURL="wss://webapi.tradezero.com/stream"`, `Route="SMART"`).
  - `*Adapter` implements `exec.Broker`: `Capabilities()`→`{NativeReplace:false, FlattenAll:false, OvernightSession:false}`; `SubmitOrder`; `ReplaceOrder` (emulated); `CancelOrder`; `CancelAll`; `Flatten` (returns `errUnsupported`); `Snapshot`; `Events()`.
  - `(*Adapter).Run(ctx)` — starts the WS client and the buffer→snapshot→replay reconcile; emits `BrokerConnUp/Down`, `StreamGap` on reconnect.
  - Helper members used by `normalize.go`: `domainID(tzCID) string` (strip trailing `-rN`), `onCanceled(...)` (swallow the cancel while a replace is mid-flight), `now()`, `mu`, `seenExecuted`.

**Emulated replace (the crux):** TZ has no modify and IDs are single-use (reuse → R114). `ReplaceOrder(ctx, domainOID, req)`:
1. Look up the current TZ client-order-id backing `domainOID` (starts equal to `domainOID`; becomes `domainOID-r1`, `-r2`, … across replaces).
2. Mark `domainOID` "replacing" so the inbound `OrderCanceled` for the old TZ id is **swallowed** (not surfaced to the domain).
3. `cancelOrder(oldTZID)`; await its `Canceled` (via a per-order channel the WS callback signals, timeout ~3 s → abort, clear the replacing flag, emit nothing/log — the domain order stays working).
4. Mint the next suffix `newTZID = domainOID + "-r" + N`; update both maps (`domainID(newTZID)==domainOID`).
5. `submitOrder(ctx, reqWithNewPrices, newTZID, route)`.
6. On broker accept, emit `exec.OrderReplaced{V, OID: domainOID, NewQty, NewLimit, NewStop, Ts}`; clear the replacing flag.

Because `domainID` **derives** the domain id by stripping the `-rN` suffix, the linkage survives a crash with **no durable map**: after reboot, any inbound frame for `domainOID-r2` still resolves to `domainOID`.

- [ ] **Step 1: Write the failing tests (mock server drives the full lifecycle)**

`engine/internal/broker/tradezero/tradezero_test.go` — one test builds a mock HTTP+WS server that: accepts a POST (records the clientOrderId), on a DELETE emits a `Canceled` WS frame for that id, and on the replace POST records the new suffixed id, then emit a `New` for it. Assert the adapter emits `OrderAccepted(oid)`, then on replace emits **`OrderReplaced(oid)`** (not a bare Cancel), and that the second POST's clientOrderId is `oid + "-r1"`.

```go
func TestAdapter_EmulatedReplace_StableDomainID(t *testing.T) {
	// mock records POSTed clientOrderIds and, on DELETE, pushes a Canceled frame.
	rec := newMockTZFull(t) // helper: HTTP mux + WS hub, see below
	defer rec.Close()

	a, err := New(Config{
		Venue: "tz", AccountID: "2TZ00001", RESTBase: rec.httpURL, WSURL: rec.wsURL,
		Route: "SMART", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go a.Run(ctx)

	oid := "ET01J0000000000000000000AA"
	if _, err := a.SubmitOrder(ctx, exec.OrderRequest{
		Venue: "tz", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay,
		Qty: 10, LimitPrice: 100, ClientOrderID: oid,
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		oa, ok := e.(exec.OrderAccepted)
		return ok && oa.OID == oid
	})

	if err := a.ReplaceOrder(ctx, oid, exec.ReplaceRequest{Qty: 10, LimitPrice: 101}); err != nil {
		t.Fatal(err)
	}
	// domain sees a replace on the SAME id; no bare cancel leaks.
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool {
		or, ok := e.(exec.OrderReplaced)
		return ok && or.OID == oid && or.NewLimit == 101
	})
	if last := rec.lastClientOrderID(); last != oid+"-r1" {
		t.Fatalf("resubmit clientOrderId = %q, want %q", last, oid+"-r1")
	}
}
```

> Provide `newMockTZFull` (HTTP mux for `/order` POST + `/orders/{id}` DELETE + snapshot GETs returning empty, plus a WS hub that emits handshake frames and lets the HTTP handlers push order frames) and `waitFor(t, ch, pred)` (drains with a timeout). These are test infrastructure — write them fully; they never touch the real endpoint.

- [ ] **Step 2: Run to verify it fails** — `New`/`Adapter` undefined.

Run: `cd engine && go test ./internal/broker/tradezero/ -run 'Adapter_' -v` → FAIL.

- [ ] **Step 3: Implement `tradezero.go`**

Provide the `Adapter` struct (venue, restClient, wsClient, events chan, `mu`, `seenExecuted map[string]float64`, `replacing map[string]*replaceState`, `tzIDByDomain map[string]string`, `clk`, `route`), `New`, `Run` (starts `wsClient.run` with callbacks that funnel into `normalizeOrder`/position-reconcile/`onConn`; runs the initial buffer→snapshot→replay: buffer WS frames, call `restClient.snapshot`, emit a `BrokerAccount`+`BrokerPositions`, then apply buffered frames; on reconnect diff order states and synthesize `StreamGap`), the `exec.Broker` methods, and the emulated-replace state machine described above. `domainID` strips a trailing `-r<digits>`:

```go
var reReplaceSuffix = regexp.MustCompile(`-r\d+$`)

func (a *Adapter) domainID(tzCID string) string {
	return reReplaceSuffix.ReplaceAllString(tzCID, "")
}
```

`Flatten` returns `fmt.Errorf("tradezero: flatten unsupported")` (its `Capabilities.FlattenAll` is false so the Core never calls it — defense in depth).

- [ ] **Step 4: Run + race**

Run: `cd engine && go test -race ./internal/broker/tradezero/`
Expected: PASS (all TZ tests).

- [ ] **Step 5: `go vet` + lint the package**

Run: `cd engine && go vet ./internal/broker/tradezero/ && golangci-lint run ./internal/broker/tradezero/...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
cd engine && git add internal/broker/tradezero/tradezero.go internal/broker/tradezero/tradezero_test.go
git commit -m "feat(engine/broker/tradezero): assemble exec.Broker — emulated replace + reconcile

ReplaceOrder = cancel -> await -> resubmit with derived <id>-rN (stable domain id,
suffix-stripping recovers linkage without durable state); buffer->snapshot->replay
startup/reconnect; Capabilities{false,false,false}."
```

---

# Part D — Alpaca adapter (`internal/broker/alpaca`)

Paper base `https://paper-api.alpaca.markets`, live `https://api.alpaca.markets`, WS `wss://{paper-}api.alpaca.markets/stream`; **separate key pairs per env**. Paper keys are verified and safe for automated orders. Structured JSON errors (no 200-but-rejected trap), native PATCH replace, `DELETE /positions` flatten, single 200/min token pool.

### Task 11: Alpaca wire mapping

**Files:**
- Create: `engine/internal/broker/alpaca/mapping.go` + `mapping_test.go`

**Interfaces:**
- Produces (pure):
  - `orderTypeWire(exec.OrderType) (string, error)` → `"market"|"limit"|"stop"|"stop_limit"`.
  - `sideWire(exec.Side) string` → Buy/Cover=`"buy"`, Sell/Short=`"sell"` (Alpaca infers open/close from position; short = a sell that opens/increases a short — the account must allow it).
  - `tifWire(exec.TIF) (string, error)` → `day`/`gtc`; `IOC`/`FOK` → error `"alpaca: tif requires Elite"` (standard account: day+gtc only per the doc).
  - `roundPrice(p float64) float64` — sub-penny rule: `>= $1` → 2 dp, `< $1` → 4 dp (avoids reject code 42210000).
  - `sideDomain(alpacaSide string, positionQtyBefore float64) exec.Side` — a `sell` that takes position negative is a Short; a `buy` that reduces a short is a Cover. (Used only where position context is available; order-event normalization keeps the domain side from the original order where possible.)

- [ ] **Step 1: Write the failing tests**

`engine/internal/broker/alpaca/mapping_test.go`:

```go
package alpaca

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

func TestOrderTypeWire(t *testing.T) {
	cases := map[exec.OrderType]string{
		exec.TypeMarket: "market", exec.TypeLimit: "limit",
		exec.TypeStop: "stop", exec.TypeStopLimit: "stop_limit",
	}
	for ot, want := range cases {
		if got, err := orderTypeWire(ot); err != nil || got != want {
			t.Errorf("orderTypeWire(%v)=%q,%v want %q", ot, got, err, want)
		}
	}
}

func TestSideWire(t *testing.T) {
	if sideWire(exec.SideBuy) != "buy" || sideWire(exec.SideCover) != "buy" {
		t.Fatal("buy/cover -> buy")
	}
	if sideWire(exec.SideSell) != "sell" || sideWire(exec.SideShort) != "sell" {
		t.Fatal("sell/short -> sell")
	}
}

func TestRoundPrice_SubPenny(t *testing.T) {
	if roundPrice(190.5049) != 190.50 {
		t.Fatalf("got %v", roundPrice(190.5049))
	}
	if roundPrice(0.12345) != 0.1235 { // sub-$1 -> 4dp
		t.Fatalf("got %v", roundPrice(0.12345))
	}
}

func TestTifWire_RejectsElite(t *testing.T) {
	if _, err := tifWire(exec.TIFIOC); err == nil {
		t.Fatal("IOC should error on a standard account")
	}
	if got, _ := tifWire(exec.TIFDay); got != "day" {
		t.Fatalf("day -> %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — undefined functions.

Run: `cd engine && go test ./internal/broker/alpaca/ -run 'Wire|RoundPrice' -v` → FAIL.

- [ ] **Step 3: Implement `mapping.go`**

```go
package alpaca

import (
	"fmt"
	"math"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

func orderTypeWire(t exec.OrderType) (string, error) {
	switch t {
	case exec.TypeMarket:
		return "market", nil
	case exec.TypeLimit:
		return "limit", nil
	case exec.TypeStop:
		return "stop", nil
	case exec.TypeStopLimit:
		return "stop_limit", nil
	default:
		return "", fmt.Errorf("alpaca: unsupported order type %v", t)
	}
}

func sideWire(s exec.Side) string {
	switch s {
	case exec.SideBuy, exec.SideCover:
		return "buy"
	default: // Sell, Short
		return "sell"
	}
}

func tifWire(t exec.TIF) (string, error) {
	switch t {
	case exec.TIFDay:
		return "day", nil
	case exec.TIFGTC:
		return "gtc", nil
	default:
		return "", fmt.Errorf("alpaca: TIF %v requires an Elite account (standard is day/gtc)", t)
	}
}

// roundPrice applies Alpaca's sub-penny rule: >= $1 -> 2 dp, < $1 -> 4 dp.
func roundPrice(p float64) float64 {
	if p >= 1 {
		return math.Round(p*100) / 100
	}
	return math.Round(p*10000) / 10000
}

func sideDomain(alpacaSide string, positionQtyBefore float64) exec.Side {
	if alpacaSide == "buy" {
		if positionQtyBefore < 0 {
			return exec.SideCover
		}
		return exec.SideBuy
	}
	if positionQtyBefore > 0 {
		return exec.SideSell
	}
	return exec.SideShort
}
```

- [ ] **Step 4: Run + race** → PASS.

Run: `cd engine && go test -race ./internal/broker/alpaca/ -run 'Wire|RoundPrice'`

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/broker/alpaca/mapping.go internal/broker/alpaca/mapping_test.go
git commit -m "feat(engine/broker/alpaca): domain<->wire mapping (type, side, TIF, sub-penny rounding)"
```

### Task 12: Alpaca `trade_updates` normalization

**Files:**
- Create: `engine/internal/broker/alpaca/normalize.go` + `normalize_test.go`
- Create: `engine/internal/broker/alpaca/testdata/{fill,partial_fill,new,canceled,replaced,rejected}.json` (captured from **real paper** where possible; authored otherwise)

**Interfaces:**
- Produces:
  - `type tradeUpdate struct { Event string; Order auOrder; Price/Qty/Timestamp/ExecutionID/PositionQty … }`.
  - `func (a *Adapter) normalizeUpdate(venue exec.VenueID, tu tradeUpdate) []exec.BrokerEvent` — maps `new`→`OrderAccepted`; `fill`/`partial_fill`→`OrderFilled` (dedup key `execution_id`, `CumQty=filled_qty`, `AvgPrice=filled_avg_price`) + `BrokerPositions` (one symbol, `Qty=position_qty`); `canceled`→`OrderCanceled`; `expired`/`done_for_day`→`OrderExpired`; `replaced`→`OrderReplaced`; `rejected`→`OrderRejected`; rare `pending_*`/`stopped`/`suspended`/`calculated`→ignored (logged).

- [ ] **Step 1: Author/capture fixtures** — `testdata/fill.json`:

```json
{
  "event": "fill",
  "execution_id": "e-1",
  "price": "190.48",
  "qty": "40",
  "position_qty": "40",
  "timestamp": "2026-07-06T13:45:00Z",
  "order": {
    "id": "b-1", "client_order_id": "ET01J0000000000000000000BB",
    "symbol": "AAPL", "side": "buy", "order_type": "limit",
    "qty": "100", "filled_qty": "40", "filled_avg_price": "190.48",
    "limit_price": "190.50", "status": "partially_filled"
  }
}
```

(Plus `new.json`, `canceled.json`, `replaced.json`, `rejected.json` in the same shape with the matching `event`. Note in `testdata/README.md` which are real paper captures vs authored.)

- [ ] **Step 2: Write the failing tests**

```go
func TestNormalizeUpdate_FillDedupOnExecutionID(t *testing.T) {
	a := newTestAdapter(t, "alpaca")
	tu := loadUpdate(t, "fill.json")
	evs := a.normalizeUpdate("alpaca", tu)
	if len(fills(evs)) != 1 {
		t.Fatalf("want 1 fill, got %d", len(fills(evs)))
	}
	f := fills(evs)[0]
	if f.F.Qty != 40 || f.AvgPrice != 190.48 || f.CumQty != 40 || f.F.OrderID != "ET01J0000000000000000000BB" {
		t.Fatalf("fill = %+v", f)
	}
	// same execution_id again -> no duplicate fill
	if len(fills(a.normalizeUpdate("alpaca", tu))) != 0 {
		t.Fatal("duplicate execution_id re-emitted a fill")
	}
	// a BrokerPositions with position_qty=40 accompanies the fill
	if !hasPosition(evs, "AAPL", 40) {
		t.Fatal("expected BrokerPositions position_qty=40")
	}
}
```

(`loadUpdate`, `fills`, `hasPosition` are small local test helpers — write them.)

- [ ] **Step 3: Implement `normalize.go`** — string→float parsing (Alpaca sends numbers as JSON strings), `execution_id` dedup set on the `Adapter`, event switch as specified. Domain side comes from the original submitted order where the adapter tracks it (`a.sideByID`), falling back to `sideDomain(order.side, positionQtyBefore)`.

- [ ] **Step 4: Run + race** → PASS.

Run: `cd engine && go test -race ./internal/broker/alpaca/ -run 'Normalize' -v`

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/broker/alpaca/normalize.go internal/broker/alpaca/normalize_test.go internal/broker/alpaca/testdata/
git commit -m "feat(engine/broker/alpaca): trade_updates normalization + execution_id fill dedup + position_qty reconcile"
```

### Task 13: Alpaca REST client

**Files:**
- Create: `engine/internal/broker/alpaca/rest.go` + `rest_test.go`

**Interfaces:**
- Produces (methods on `*restClient`, single `netx.TokenBucket` at 200/min ≈ 3.33/s burst 5):
  - `submitOrder(ctx, req, clientOrderID string) (brokerID string, err error)` — `POST /v2/orders`; structured JSON error `{code,message}` → typed error.
  - `replaceOrder(ctx, brokerID string, rr exec.ReplaceRequest) error` — `PATCH /v2/orders/{id}` (qty/limit/stop).
  - `cancelOrder(ctx, brokerID) error` — `DELETE /v2/orders/{id}`.
  - `cancelAll(ctx, symbol string) error` — `DELETE /v2/orders` (symbol-scoped: list + cancel each).
  - `flatten(ctx) error` — `DELETE /v2/positions`.
  - `snapshot(ctx) (exec.AccountSnapshot, []exec.Position, []exec.Order, error)` — `GET /v2/account` (`DayPnL = equity − last_equity`; `last_equity` = prior-close equity — a real `/v2/account` field per Alpaca's live docs, **not yet captured in `docs/2026-07-03-alpaca-api.md`**, so confirm the field name against a real paper `/v2/account` response before freezing the struct), `GET /v2/positions`, `GET /v2/orders?status=open`.
  - `orderByClientID(ctx, clientOrderID) (auOrder, bool, error)` — `GET /v2/orders:by_client_order_id` (transport-failure ambiguity resolution).

- [ ] **Step 1: Write the failing tests (against `httptest` mock)**

```go
func TestSubmit_StructuredError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"code":42210000,"message":"sub-penny increment"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	if _, err := rc.submitOrder(context.Background(), exec.OrderRequest{
		Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 1.001, ClientOrderID: "ET-x",
	}, "ET-x"); err == nil {
		t.Fatal("422 structured error must surface as an error")
	}
}

func TestAccount_DayPnLFromEquityDelta(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/account", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"equity":"100050","last_equity":"100000","buying_power":"200000","cash":"50000","multiplier":"4"}`))
	})
	mux.HandleFunc("/v2/positions", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/v2/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rc := newRESTClient(srv.URL, "K", "S", clock.NewFake(time.UnixMilli(0)))
	acct, _, _, err := rc.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acct.DayPnL != 50 { // 100050 - 100000
		t.Fatalf("DayPnL = %v want 50", acct.DayPnL)
	}
}
```

- [ ] **Step 2: Run → FAIL** (`newRESTClient` undefined).

Run: `cd engine && go test ./internal/broker/alpaca/ -run 'Submit|Account' -v`

- [ ] **Step 3: Implement `rest.go`** — headers `APCA-API-KEY-ID`/`APCA-API-SECRET-KEY`; parse string-numbers; `DayPnL = equity - last_equity`; position `Qty` signed (Alpaca `qty` is signed for shorts, or `side:"short"` → negative). Use the single 200/min bucket on every call. `POST` body sends `limit_price`/`stop_price` only for the relevant types (rounded via `roundPrice`); `client_order_id` = the domain id.

- [ ] **Step 4: Run + race** → PASS.

Run: `cd engine && go test -race ./internal/broker/alpaca/ -run 'Submit|Account|Cancel|Replace|Flatten' -v`

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/broker/alpaca/rest.go internal/broker/alpaca/rest_test.go
git commit -m "feat(engine/broker/alpaca): REST client — structured errors, PATCH replace, DELETE flatten, day-P&L via equity delta"
```

### Task 14: Alpaca `trade_updates` WebSocket client

**Files:**
- Create: `engine/internal/broker/alpaca/ws.go` + `ws_test.go`

**Interfaces:**
- Produces: `newWSClient(wsURL, keyID, secret string, clk clock.Clock, onUpdate func(tradeUpdate), onConn func(bool)) *wsClient`; `(*wsClient).run(ctx)` — auth `{"action":"auth",...}` → `{"action":"listen","data":{"streams":["trade_updates"]}}` → read loop that **JSON-decodes the payload regardless of the frame opcode** (paper=binary, live=text); `{"action":"error"}` → close + reconnect with `netx.Backoff`.

- [ ] **Step 1: Write the failing test (mock WS emits a BINARY-frame trade update to prove opcode-agnostic decode)**

```go
func TestWS_DecodesBinaryFramePayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		_, _, _ = c.Read(ctx) // auth
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"stream":"authorization","data":{"status":"authorized"}}`))
		_, _, _ = c.Read(ctx) // listen
		// paper sends BINARY frames; payload is still JSON.
		_ = c.Write(ctx, websocket.MessageBinary, []byte(`{"stream":"trade_updates","data":{"event":"new","order":{"client_order_id":"ET-z","symbol":"AAPL","side":"buy","order_type":"limit","qty":"1","status":"new"}}}`))
		<-ctx.Done()
	}))
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]

	var mu sync.Mutex
	var got []tradeUpdate
	ws := newWSClient(wsURL, "K", "S", clock.System{}, func(tu tradeUpdate) { mu.Lock(); got = append(got, tu); mu.Unlock() }, func(bool) {})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go ws.run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 1 {
			if got[0].Event != "new" {
				t.Fatalf("event = %q", got[0].Event)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("binary-frame trade update not decoded")
}
```

- [ ] **Step 2: Run → FAIL.**

Run: `cd engine && go test ./internal/broker/alpaca/ -run 'TestWS_' -v`

- [ ] **Step 3: Implement `ws.go`** — envelope `{stream, data}`; on `stream=="trade_updates"` decode `data` into `tradeUpdate` and call `onUpdate`; ignore other streams; `c.Read` returns `(MessageType, []byte)` — **ignore the type, `json.Unmarshal` the bytes**. On any read error or `{"action":"error"}`/`{"stream":"error"}` frame → return and let `run` reconnect via `netx.Backoff`.

- [ ] **Step 4: Run + race** → PASS.

Run: `cd engine && go test -race ./internal/broker/alpaca/ -run 'TestWS_'`

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/broker/alpaca/ws.go internal/broker/alpaca/ws_test.go
git commit -m "feat(engine/broker/alpaca): trade_updates WS — auth/listen, opcode-agnostic JSON decode (paper binary/live text), reconnect"
```

### Task 15: Assemble the Alpaca `exec.Broker` — native replace + flatten + reconcile

**Files:**
- Create: `engine/internal/broker/alpaca/alpaca.go` + `alpaca_test.go`

**Interfaces:**
- Produces:
  - `alpaca.New(cfg Config) (*Adapter, error)` where `Config{Venue exec.VenueID; Env string; RESTBase, WSURL string; Creds creds.Pair; Clock clock.Clock}`; when `RESTBase`/`WSURL` empty they default from `Env` (`paper`→`paper-api…`, `live`→`api…`).
  - `*Adapter` implements `exec.Broker`: `Capabilities()`→`{NativeReplace:true, FlattenAll:true, OvernightSession:true}`; `SubmitOrder` (records `sideByID`, resolves transport ambiguity via `orderByClientID`); `ReplaceOrder`→`PATCH` (adapter emits nothing; the WS `replaced` event carries the `OrderReplaced`); `CancelOrder`; `CancelAll`; `Flatten`→`restClient.flatten`; `Snapshot`; `Events()`.
  - `(*Adapter).Run(ctx)` — buffer→snapshot→replay + reconnect (re-snapshot, synthesize `StreamGap`).

- [ ] **Step 1: Write the failing test (mock HTTP+WS: submit → WS `new`; replace via PATCH → WS `replaced`)**

```go
func TestAdapter_NativeReplaceEmitsOrderReplaced(t *testing.T) {
	rec := newMockAlpacaFull(t) // HTTP mux (POST/PATCH/GET) + WS hub
	defer rec.Close()
	a, err := New(Config{Venue: "alpaca", Env: "paper", RESTBase: rec.httpURL, WSURL: rec.wsURL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go a.Run(ctx)

	oid := "ET01J0000000000000000000CC"
	_, _ = a.SubmitOrder(ctx, exec.OrderRequest{Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100, ClientOrderID: oid})
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { oa, ok := e.(exec.OrderAccepted); return ok && oa.OID == oid })

	if err := a.ReplaceOrder(ctx, oid, exec.ReplaceRequest{Qty: 10, LimitPrice: 101}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a.Events(), func(e exec.BrokerEvent) bool { or, ok := e.(exec.OrderReplaced); return ok && or.OID == oid })
}

func TestAdapter_Capabilities(t *testing.T) {
	a, _ := New(Config{Venue: "alpaca", Env: "paper", RESTBase: "http://x", WSURL: "ws://x", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clock.System{}})
	c := a.Capabilities()
	if !c.NativeReplace || !c.FlattenAll {
		t.Fatalf("caps = %+v", c)
	}
}
```

- [ ] **Step 2: Run → FAIL** (`New`/`Adapter` undefined).

- [ ] **Step 3: Implement `alpaca.go`** — the `Adapter` (venue, restClient, wsClient, events chan, `mu`, `seenExec map[string]bool`, `sideByID map[string]exec.Side`, `brokerIDByClientID map[string]string`, `clk`), `New`, `Run` (WS callbacks → `normalizeUpdate` → events; buffer→`snapshot`→replay; reconnect re-snapshot + `StreamGap`), and the `exec.Broker` methods. `ReplaceOrder` maps `oid→brokerID` then `PATCH`; the resulting WS `replaced` produces the `OrderReplaced` (do not double-emit from the method).

- [ ] **Step 4: Run + race + vet + lint**

Run: `cd engine && go test -race ./internal/broker/alpaca/ && go vet ./internal/broker/alpaca/ && golangci-lint run ./internal/broker/alpaca/...`
Expected: clean, PASS.

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/broker/alpaca/alpaca.go internal/broker/alpaca/alpaca_test.go
git commit -m "feat(engine/broker/alpaca): assemble exec.Broker — native PATCH replace, DELETE flatten, reconcile

Capabilities{NativeReplace,FlattenAll,OvernightSession}=true; buffer->snapshot->replay;
transport-ambiguity resolved via GET by client_order_id."
```

---

# Part E — Integration capstone

### Task 16: Scripted lifecycle through `exec.Core` + opt-in Alpaca-paper run

**Files:**
- Create: `engine/internal/exectest/lifecycle_test.go`
- Create: `engine/internal/exectest/alpacapaper_test.go`

**Interfaces:**
- Consumes: `exec.Core`/`CoreConfig`, `store.Open`, `broker/tradezero`, `broker/alpaca`, `broker/sim`, `creds`.
- Produces: the plan's final verification gate — the two real adapters (against mock servers) plus SimBroker drive the real `exec.Core` through arm → limit → replace → cancel → kill, asserting `Core.Updates()`; and an opt-in real-Alpaca-paper run behind `ETAPE_ALPACA_PAPER=1`.

- [ ] **Step 1: Write the mock-server lifecycle test**

`engine/internal/exectest/lifecycle_test.go` builds a `Core` over two venues — a TradeZero adapter (mock HTTP+WS) and an Alpaca adapter (mock HTTP+WS) — with a real `store.Open` on a temp DB, arms master+both venues, and walks the lifecycle. Because both adapters run their WS loops, the test asserts on drained `Core.Updates()`:

```go
func TestLifecycle_LimitReplaceCancelKill_ThroughCore(t *testing.T) {
	clk := clock.System{} // adapters use real WS timing; store uses it too
	st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "life.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tzMock := tzmock.Start(t)   // reuse the mock helpers from Task 10 (exported to a testhelper pkg or duplicated minimally)
	defer tzMock.Close()
	alMock := almock.Start(t)   // from Task 15
	defer alMock.Close()

	tz, err := tradezero.New(tradezero.Config{Venue: "tz", AccountID: "2TZ00001", RESTBase: tzMock.HTTPURL, WSURL: tzMock.WSURL, Route: "SMART", Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clk})
	if err != nil { t.Fatal(err) }
	al, err := alpaca.New(alpaca.Config{Venue: "alpaca", Env: "paper", RESTBase: alMock.HTTPURL, WSURL: alMock.WSURL, Creds: creds.Pair{KeyID: "K", SecretKey: "S"}, Clock: clk})
	if err != nil { t.Fatal(err) }

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go tz.Run(ctx)
	go al.Run(ctx)

	c := exec.NewCore(exec.CoreConfig{
		Venues: []exec.VenueID{"tz", "alpaca"},
		Gate: exec.GateConfig{
			Global: exec.GlobalLimits{MaxSymbolPositionShares: 10_000},
			Venue: map[exec.VenueID]exec.VenueLimits{
				"tz":     {MaxOrderValue: 1_000_000, MaxOpenOrders: 50},
				"alpaca": {MaxOrderValue: 1_000_000, MaxOpenOrders: 50},
			},
		},
		Store: st, Brokers: map[exec.VenueID]exec.Broker{"tz": tz, "alpaca": al},
		Clock: clk, IDGen: exec.NewOrderIDGen(clk, rand.New(rand.NewSource(7))),
	})
	if err := c.Recover(ctx); err != nil { t.Fatal(err) }
	go c.Run(ctx)

	c.Do(exec.Arm{})
	c.Do(exec.Arm{Venue: "alpaca"})

	// far-from-market limit on Alpaca (mock keeps it working)
	ack := c.Do(exec.SubmitOrder{Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 1})
	if !ack.Accepted { t.Fatalf("submit blocked: %q", ack.Reason) }
	oid := ack.OrderID
	waitUpdate(t, c, func(u exec.Update) bool { o, ok := u.(exec.OrderUpdate); return ok && o.Order.ID == oid && o.Order.Status == exec.StatusAccepted })

	c.Do(exec.ReplaceOrder{Venue: "alpaca", OrderID: oid, Qty: 10, LimitPrice: 2})
	waitUpdate(t, c, func(u exec.Update) bool { o, ok := u.(exec.OrderUpdate); return ok && o.Order.ID == oid && o.Order.LimitPrice == 2 })

	c.Do(exec.CancelOrder{Venue: "alpaca", OrderID: oid})
	waitUpdate(t, c, func(u exec.Update) bool { o, ok := u.(exec.OrderUpdate); return ok && o.Order.ID == oid && o.Order.Status == exec.StatusCanceled })

	// kill switch: master disarm + cancel-all on both venues
	c.Do(exec.KillSwitch{})
	if c.Do(exec.SubmitOrder{Venue: "alpaca", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 1}).Accepted {
		t.Fatal("submit after kill must be blocked (master disarmed)")
	}
}
```

> `waitUpdate(t, c, pred)` drains `c.Updates()` with a timeout. Package the Task-10/15 mock servers as small exported helpers (`tzmock`, `almock`) under `internal/broker/tradezero/tztest` and `internal/broker/alpaca/altest`, or duplicate the minimal handlers in `exectest` — either is fine as long as no real endpoint is contacted.

- [ ] **Step 2: Run the mock lifecycle + race**

Run: `cd engine && go test -race ./internal/exectest/ -run 'Lifecycle' -v`
Expected: PASS.

- [ ] **Step 3: Write the opt-in real-paper Alpaca test**

`engine/internal/exectest/alpacapaper_test.go` — skips unless `ETAPE_ALPACA_PAPER=1`; loads real creds; places a **tiny far-from-market** limit buy (1 share, limit well below market), asserts it reaches `Accepted`, then cancels it. **Paper only — never live.**

```go
func TestAlpacaPaper_SubmitCancel_OptIn(t *testing.T) {
	if os.Getenv("ETAPE_ALPACA_PAPER") != "1" {
		t.Skip("set ETAPE_ALPACA_PAPER=1 to run the real Alpaca paper integration test")
	}
	f, err := creds.Load(creds.DefaultPath())
	if err != nil { t.Fatal(err) }
	pair, err := f.Get("alpaca")
	if err != nil { t.Fatal(err) }

	al, err := alpaca.New(alpaca.Config{Venue: "alpaca-paper", Env: "paper", Creds: pair, Clock: clock.System{}})
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go al.Run(ctx)

	oid := exec.NewOrderIDGen(clock.System{}, rand.New(rand.NewSource(time.Now().UnixNano()))).Next()
	if _, err := al.SubmitOrder(ctx, exec.OrderRequest{
		Venue: "alpaca-paper", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay,
		Qty: 1, LimitPrice: 1.00, ClientOrderID: oid, // $1 limit: never marketable -> rests
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, al.Events(), func(e exec.BrokerEvent) bool { oa, ok := e.(exec.OrderAccepted); return ok && oa.OID == oid })
	if err := al.CancelOrder(ctx, oid); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitFor(t, al.Events(), func(e exec.BrokerEvent) bool { oc, ok := e.(exec.OrderCanceled); return ok && oc.OID == oid })
}
```

> **No TradeZero real-integration test exists in this plan** — only LIVE TZ keys exist and the safety rule forbids order placement. The TZ live-paper/live lifecycle is a manual step in an authorized Monday session, captured into the golden corpus then.

- [ ] **Step 4: Run the opt-in test locally (Earl, optional), then the full suite**

Optional (Earl, with OpenD/Alpaca paper reachable): `cd engine && ETAPE_ALPACA_PAPER=1 go test ./internal/exectest/ -run 'AlpacaPaper' -v`
Always: `cd engine && go build ./... && go vet ./... && go test -race ./... && golangci-lint run`
Expected: full suite PASS; the opt-in test SKIPs without the env var.

- [ ] **Step 5: Commit**

```bash
cd engine && git add internal/exectest/
git commit -m "test(engine/exec): scripted lifecycle through Core over TZ+Alpaca mocks + opt-in Alpaca-paper

limit -> replace -> cancel -> kill through the real exec.Core against mock servers;
real Alpaca paper submit/cancel gated behind ETAPE_ALPACA_PAPER=1 (paper only)."
```

---

## Self-Review

**Spec coverage:**
- Multi-broker spec `broker/tradezero` (deltas, emulated replace) → Tasks 6–10. ✅
- Multi-broker spec `broker/alpaca` (REST + trade_updates WS, binary/text, native replace, CancelAll, GET by client_order_id, day-P&L via equity delta) → Tasks 11–15. ✅
- `broker/moomoo` — explicitly out of scope (v1.x); stated in Global Constraints + Plan sequence. ✅
- `Broker.Flatten` native primitive (Alpaca) — Task 4 (interface) + Task 15 (impl). ✅
- Stop / Stop-Limit order-type gap (UI plan flag) — Tasks 1–3 (domain/gate/sim) + mapped in both adapters (Tasks 6, 11). ✅
- Startup/reconnect buffer→snapshot→replay + StreamGap synthesis — Tasks 10, 15. ✅
- Rate limits (TZ per-endpoint buckets; Alpaca 200/min pool) — Tasks 8, 13 via `netx.TokenBucket`. ✅
- Transport-failure resolution (TZ retry-once + R114 probe; Alpaca GET by client_order_id) — Tasks 8, 13/15. ✅
- Golden-corpus + mock-server tests; TZ authored fixtures (safety), Alpaca real-paper captures — Tasks 7–15. ✅
- Scripted lifecycle through the exec core (limit→replace→cancel→kill) — Task 16. ✅
- Error tables (200-rejected trap, R-codes, structured JSON, pending_replace) — Tasks 8, 13. ✅

**Placeholder scan:** No "TBD/TODO/handle errors appropriately." Two deliberate, explicit deferrals are stated as scope, not placeholders: (a) TZ order-flow golden fixtures are *authored* pending an authorized live capture (safety rule), and (b) the Market-outside-RTH coercion is upstream (UI), documented in Task 6. Where a member is introduced by a later task (e.g. `Adapter.domainID` used in Task 7's `normalize.go`), the plan says to declare the struct skeleton early — a real instruction, not a gap.

**Type consistency (checked against the merged Plan 4 code on `main@fb6ca3d`):** `exec.Broker` gains exactly one method (`Flatten(ctx context.Context) error`) — SimBroker/TZ/Alpaca all implement it; `Capabilities{NativeReplace,FlattenAll,OvernightSession}` used verbatim; adapters emit only the real broker events (`OrderAccepted{V,OID,BrokerOrderID,Ts}`, `OrderRejected{V,OID,Reason,Ts}`, `OrderFilled{F Fill,CumQty,LeavesQty,AvgPrice}`, `OrderCanceled{V,OID,Ts}`, `OrderExpired{V,OID,Ts}`, `OrderReplaced{V,OID,NewQty,NewLimit,NewStop,Ts}`, `StreamGap{V,Ts}`, `BrokerAccount{Account}`, `BrokerPositions{V,Positions}`, `BrokerConnUp{V}`, `BrokerConnDown{V}`); `OrderType` values 0/1 unchanged, Stop/StopLimit appended as 2/3 (persisted-codec-safe); `AccountSnapshot.DayPnL` populated by both adapters; `store.Open(store.Options{Path,Clock})`, `exec.NewOrderIDGen(clk, io.Reader)`, `session.PhaseAt`, `clock.NewFake`/`clock.System` all match. The Core needs **no** replace/kill surgery: `handleReplace` already calls `b.ReplaceOrder` (TZ emulates internally; Alpaca native), `handleKill` already cancel-all + disarms; only `handleFlatten` changes (Task 4).

**Known-hard edge, stated honestly:** emulated-replace across a *process crash* mid-flight — the derived `<id>-rN` scheme makes the domain↔TZ linkage reconstructible from IDs alone (no durable map), and boot `Recover` re-snapshots from REST; a truly interrupted resubmit resolves as a dangling working order surfaced on reconcile, matching Plan 4's crash+restart+reconcile philosophy. Not a placeholder — a documented v1 boundary.

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-06-engine-broker-adapters.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. (Reminder from prior runs: each implementer must verify `pwd`/branch is the Plan 5 worktree before committing.)

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints for review.

**Which approach?**
