# Extended-hours market orders — marketable-limit conversion — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A MARKET order placed outside regular trading hours behaves like a market order — an immediate fill against the current book with a bounded worst case — by converting UI-side to an aggressive marketable limit (ask/bid ± a configurable % buffer, tick-rounded), backed by an engine block that loudly rejects any raw MARKET that reaches it outside RTH on a real venue.

**Architecture:** Two layers. (1) **UI conversion** in `preChecks.ts`: outside RTH, a MARKET is coerced to a LIMIT at `ask×(1+pct)` (buys) / `bid×(1−pct)` (sells), rounded to a valid tick, with a last-price fallback for a one-sided book and an existing non-blocking notice. (2) **Engine backstop** in `exec.Core.handleSubmit`: a new `Capabilities.MarketOutsideRTH` gate blocks raw MARKET orders outside RTH for non-sim venues (sim is exempt so replay/practice at night still fills). A new global `OrderConfig.extHoursMarketBufferPct` (default 1.0, clamp [0.1, 10]) drives the buffer, edited in Settings → Orders & hotkeys.

**Tech Stack:** TypeScript + React + Vitest (UI); Go + `go test` (engine). No generated-type (`tygo`) changes — `OrderType`/`TIF`/`OrderSession` enums are unchanged.

## Global Constraints

- **No new `OrderType`/`TIF`/`OrderSession` enum values**, no `gen/wsmsg.ts` edits, no broker-adapter changes (once type is LIMIT, TZ's `tifWire` already emits `Day_Plus`/`GTC_Plus` and Alpaca's `submitOrder` already sets `extended_hours: true`).
- **Buffer:** `extHoursMarketBufferPct`, default **1.0**, clamped to **[0.1, 10]**, defaulted/clamped in `normalizeOrderConfig` (the single migration point) so stored configs pick it up with no migration step.
- **Conversion trigger stays keyed on the ACTUAL clock** (`sessionAt(nowMs) !== "rth"`), never on `order.session`. The existing rationale comment in `preChecks.ts` (broker-safety net independent of the trader's session choice) stays verbatim.
- **Tick rounding:** $0.01 for prices ≥ $1.00, $0.0001 below $1.00 (SEC sub-penny rule). Buys round **up**, sells round **down**.
- **Sim is exempt** from the engine backstop (via `Capabilities.MarketOutsideRTH: true`); UI conversion still runs for sim orders (a marketable limit crosses the sim book identically).
- Engine backstop **blocks, never converts** (the exec path has no bid/ask — `MarkSource` is last-trade only).
- Commit after every task; no `Co-Authored-By:` trailer.

## File Structure

**Engine (Task 1):**
- `engine/internal/exec/broker.go` — add `MarketOutsideRTH bool` to `Capabilities`.
- `engine/internal/exec/core.go` — add the backstop gate in `handleSubmit`.
- `engine/internal/broker/sim/sim.go` — set `MarketOutsideRTH: true`.
- `engine/internal/exec/core_test.go` — `capStub` field, `buildCoreWithClock` helper, `submitSettledMarket` helper, 3 test cases.

**UI config (Task 2):**
- `ui/src/chrome/exec/actionTemplate.ts` (+ `.test.ts`) — `extHoursMarketBufferPct` field + normalize default/clamp.

**UI conversion + threading (Task 3):**
- `ui/src/chrome/exec/preChecks.ts` (+ `.test.ts`) — signature widened to a quote + buffer; conversion math; tick-rounding helpers.
- `ui/src/chrome/exec/resolveTemplate.ts` (+ `.test.ts`) — `ResolveContext` gains the buffer; `preCheck` call updated.
- `ui/src/chrome/exec/useHotkeys.ts` — pass the buffer into the resolve context.
- `ui/src/chrome/panels/OrderTicketPanel.tsx` — read the buffer from `useOrderConfig`; pass the quote + buffer to `preCheck`.
- `ui/src/chrome/panels/AccountPanel.tsx:244` — the Flatten button also builds a `ResolveContext` (MARKET flatten template) and must supply the buffer too.

**UI settings editor (Task 4):**
- `ui/src/chrome/exec/OrderSettingsSection.tsx` (+ `.test.tsx`) — config-level buffer `StepField`.

**Dependencies:** Task 1 is independent. Tasks 3 and 4 both depend on Task 2 (they read `config.extHoursMarketBufferPct`). Recommended order: 1 → 2 → 3 → 4.

---

### Task 1: Engine backstop for raw MARKET outside RTH

**Files:**
- Modify: `engine/internal/exec/broker.go:5-12`
- Modify: `engine/internal/exec/core.go:350-363`
- Modify: `engine/internal/broker/sim/sim.go:105-107`
- Test: `engine/internal/exec/core_test.go`

**Interfaces:**
- Produces: `exec.Capabilities.MarketOutsideRTH bool` (fifth field; every existing named-field constructor leaves it `false`). New backstop reason string `"market order outside regular hours (UI converts these to marketable limits)"`.
- Consumes: existing `session.PhaseAt(t time.Time) session.Phase` / `session.RTH` (exec already imports `session`), `c.clk clock.Clock`, `OrderBlocked{V,OID,Req,Reason,Ts}`, `appendAndFold(ev, SrcLocal)`, gate reason `"no mark to value market order"` (gate.go:101).

**Context for the implementer:** `handleSubmit` calls `Evaluate` (core.go:343) *before* the capability gates. A `TypeMarket` order with no mark is blocked by `Evaluate` with `"no mark to value market order"` and never reaches the backstop — so tests must `FeedMark` first. `FeedMark` is async (keep-latest, drop-on-full) over an **unbuffered** `cmds` channel, so a mark fed just before a submit can race; the `submitSettledMarket` helper below retries past the transient `"no mark"` reason. No existing test submits `TypeMarket` through the Core, so the backstop breaks nothing; `sim` gets the capability so its market orders stay exempt.

- [ ] **Step 1: Write the failing tests** in `engine/internal/exec/core_test.go`.

Add the `marketOutsideRTH` field to `capStub` (struct at line 188) and include it in `Capabilities()` (line 202):

```go
type capStub struct {
	flatten          bool // value Capabilities().FlattenAll reports
	resetBalance     bool // value Capabilities().ResetBalance reports
	overnight        bool // value Capabilities().OvernightSession reports
	marketOutsideRTH bool // value Capabilities().MarketOutsideRTH reports

	mu          sync.Mutex
	called      bool
	resetCalled bool
	resetAmount float64
	ev          chan exec.BrokerEvent
}
```

```go
func (c *capStub) Capabilities() exec.Capabilities {
	return exec.Capabilities{FlattenAll: c.flatten, ResetBalance: c.resetBalance, OvernightSession: c.overnight, MarketOutsideRTH: c.marketOutsideRTH}
}
```

Refactor `buildCoreWith` (line 284) to delegate to a clock-parametrized variant, and add the new helper (place both where `buildCoreWith` currently lives):

```go
func buildCoreWith(t *testing.T, b *capStub, startingBalance map[exec.VenueID]float64) (*exec.Core, *capStub) {
	return buildCoreWithClock(t, b, startingBalance, clock.NewFake(time.UnixMilli(1_700_000_000_000)))
}

// buildCoreWithClock is buildCoreWith with an explicit clock, so a test can
// place the Core inside or outside RTH (the default fake clock, 2023-11-14
// 17:13 ET, is PostMarket).
func buildCoreWithClock(t *testing.T, b *capStub, startingBalance map[exec.VenueID]float64, clk *clock.Fake) (*exec.Core, *capStub) {
	t.Helper()
	b.ev = make(chan exec.BrokerEvent)
	st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "e.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := exec.CoreConfig{
		Venues: []exec.VenueID{"v"},
		Gate: exec.GateConfig{
			Global: exec.GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionShares: 1000, MaxSymbolPositionValue: 1_000_000},
			Venue:  map[exec.VenueID]exec.VenueLimits{"v": {MaxOrderValue: 100000, MaxPositionValue: 1_000_000, MaxPositionShares: 1000, MaxOpenOrders: 10}},
		},
		Store:           st,
		Brokers:         map[exec.VenueID]exec.Broker{"v": b},
		Clock:           clk,
		IDGen:           exec.NewOrderIDGen(clk, rand.New(rand.NewSource(1))),
		StartingBalance: startingBalance,
	}
	c := exec.NewCore(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Recover(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() { _ = c.Run(ctx) }()
	t.Cleanup(cancel)
	return c, b
}

// submitSettledMarket retries cm until the risk gate stops reporting a missing
// mark. FeedMark is async (keep-latest, drop-on-full) over an unbuffered cmds
// channel, so a mark fed just before the submit may not be folded into the
// Core's mark map yet; "no mark to value market order" is the observable signal
// that it hasn't. Bounded; every pre-mark attempt is a harmless blocked order.
func submitSettledMarket(t *testing.T, c *exec.Core, cm exec.SubmitOrder) exec.CmdAck {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		ack := c.Do(cm)
		if ack.Reason != "no mark to value market order" {
			return ack
		}
		if time.Now().After(deadline) {
			t.Fatalf("mark never applied to the Core (last ack %+v)", ack)
		}
		time.Sleep(time.Millisecond)
	}
}
```

Add the three test cases (append near the overnight-gate tests, ~line 675):

```go
const marketOutsideRTHReason = "market order outside regular hours (UI converts these to marketable limits)"

// A raw MARKET reaching the engine outside RTH on a venue whose broker does not
// support it must be loudly blocked (Alpaca would otherwise silently queue it
// to the next open). 2023-11-14 22:00 UTC = 17:00 ET (Tue) → PostMarket.
func TestCore_SubmitOrder_MarketOutsideRTH_BlockedOnRealVenue(t *testing.T) {
	post := clock.NewFake(time.Date(2023, 11, 14, 22, 0, 0, 0, time.UTC))
	c, _ := buildCoreWithClock(t, &capStub{}, nil, post)
	c.Do(exec.Arm{})
	c.FeedMark(exec.Mark{Symbol: "AAPL", Price: 100})
	ack := submitSettledMarket(t, c, exec.SubmitOrder{
		Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 10,
	})
	if ack.Accepted || ack.Reason != marketOutsideRTHReason {
		t.Fatalf("raw MARKET outside RTH on a non-capable venue must block, got %+v", ack)
	}
	u := waitFor(t, c, func(u exec.Update) bool { _, ok := u.(exec.OrderUpdate); return ok })
	if u.(exec.OrderUpdate).Order.Status != exec.StatusBlocked {
		t.Fatalf("expected blocked order update, got %+v", u)
	}
}

// During RTH the same raw MARKET must NOT hit the backstop — it is accepted on a
// non-capable venue. 2023-11-14 15:00 UTC = 10:00 ET (Tue) → RTH.
func TestCore_SubmitOrder_MarketDuringRTH_NotBackstopped(t *testing.T) {
	rth := clock.NewFake(time.Date(2023, 11, 14, 15, 0, 0, 0, time.UTC))
	c, _ := buildCoreWithClock(t, &capStub{}, nil, rth)
	c.Do(exec.Arm{})
	c.FeedMark(exec.Mark{Symbol: "AAPL", Price: 100})
	ack := submitSettledMarket(t, c, exec.SubmitOrder{
		Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 10,
	})
	if !ack.Accepted {
		t.Fatalf("MARKET during RTH must not be capability-blocked, got %+v", ack)
	}
}

// A sim (capable) venue is exempt: a MARKET outside RTH is accepted and fills
// off the seeded book (replay/practice sessions run at night by definition).
func TestCore_SubmitOrder_MarketOutsideRTH_SimExempt(t *testing.T) {
	c, _, _ := newTestCore(t, "sim-1") // default fake clock = PostMarket (outside RTH)
	c.Do(exec.Arm{})
	c.FeedMark(exec.Mark{Symbol: "AAPL", Price: 100})
	ack := submitSettledMarket(t, c, exec.SubmitOrder{
		Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFDay, Qty: 10,
	})
	if !ack.Accepted {
		t.Fatalf("MARKET outside RTH on a sim venue must be accepted (exempt), got %+v", ack)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd engine && go test ./internal/exec/ -run 'TestCore_SubmitOrder_MarketOutsideRTH|TestCore_SubmitOrder_MarketDuringRTH' -v`
Expected: compile error (`MarketOutsideRTH` unknown field on `Capabilities`) or, once the field is added, `TestCore_SubmitOrder_MarketOutsideRTH_BlockedOnRealVenue` fails because nothing blocks the order.

- [ ] **Step 3: Add the capability field** in `engine/internal/exec/broker.go`:

```go
type Capabilities struct {
	NativeReplace    bool // Alpaca PATCH, moomoo ModifyOrder-Normal; TZ false
	FlattenAll       bool // Alpaca DELETE /v2/positions only
	OvernightSession bool // Alpaca (Blue Ocean), moomoo (OVERNIGHT); TZ false
	ResetBalance     bool // sim only — a real venue's account can't be reset
	MarketOutsideRTH bool // sim only — real brokers require limits outside RTH
}
```

- [ ] **Step 4: Set the capability on sim** in `engine/internal/broker/sim/sim.go:106`:

```go
func (b *Broker) Capabilities() exec.Capabilities {
	return exec.Capabilities{NativeReplace: true, FlattenAll: true, OvernightSession: false, ResetBalance: true, MarketOutsideRTH: true}
}
```

- [ ] **Step 5: Add the backstop gate** in `engine/internal/exec/core.go`, immediately after the overnight-capability gate block (after line 363, before the `OrderSubmitted` append comment at line 364):

```go
	// A raw MARKET order outside regular hours cannot be placed on a real venue
	// (TradeZero hard-rejects with R78; Alpaca silently queues it to the next
	// open — worse). The UI converts these to marketable limits before they get
	// here; this is the backstop for a bug or a bypassing client. Sim venues are
	// exempt (Capabilities.MarketOutsideRTH) so replay/practice at night fill.
	if req.Type == TypeMarket && session.PhaseAt(c.clk.Now()) != session.RTH &&
		(b == nil || !b.Capabilities().MarketOutsideRTH) {
		reason := "market order outside regular hours (UI converts these to marketable limits)"
		ev := OrderBlocked{V: req.Venue, OID: req.ClientOrderID, Req: req, Reason: reason, Ts: c.now()}
		if err := c.appendAndFold(ev, SrcLocal); err != nil {
			slog.Error("exec: append OrderBlocked failed", "err", err)
		}
		return CmdAck{Accepted: false, Reason: reason, OrderID: req.ClientOrderID}
	}
```

- [ ] **Step 6: Run the new tests to verify they pass**

Run: `cd engine && go test ./internal/exec/ -run 'TestCore_SubmitOrder_MarketOutsideRTH|TestCore_SubmitOrder_MarketDuringRTH' -v`
Expected: PASS (all 3).

- [ ] **Step 7: Run the full engine suite (no regressions)**

Run: `cd engine && go build ./... && go vet ./... && go test ./internal/exec/... ./internal/broker/...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add engine/internal/exec/broker.go engine/internal/exec/core.go engine/internal/broker/sim/sim.go engine/internal/exec/core_test.go
git commit -m "feat(exec): block raw market orders outside RTH on non-sim venues"
```

---

### Task 2: `extHoursMarketBufferPct` config field + normalize default/clamp

**Files:**
- Modify: `ui/src/chrome/exec/actionTemplate.ts:23,48-50`
- Test: `ui/src/chrome/exec/actionTemplate.test.ts`

**Interfaces:**
- Produces: `OrderConfig.extHoursMarketBufferPct?: number`; `normalizeOrderConfig` now always returns a defined `extHoursMarketBufferPct` in **[0.1, 10]** (default 1.0). Tasks 3 and 4 consume this field.

- [ ] **Step 1: Write the failing tests** — append to the `normalizeOrderConfig` describe block in `ui/src/chrome/exec/actionTemplate.test.ts`:

```ts
  it("defaults a missing extHoursMarketBufferPct to 1.0", () => {
    const raw: OrderConfig = { activeVenue: "", templates: [] };
    expect(normalizeOrderConfig(raw).extHoursMarketBufferPct).toBe(1.0);
  });
  it("clamps extHoursMarketBufferPct to [0.1, 10]", () => {
    expect(normalizeOrderConfig({ activeVenue: "", templates: [], extHoursMarketBufferPct: 0 }).extHoursMarketBufferPct).toBe(0.1);
    expect(normalizeOrderConfig({ activeVenue: "", templates: [], extHoursMarketBufferPct: 50 }).extHoursMarketBufferPct).toBe(10);
  });
  it("preserves an in-range extHoursMarketBufferPct", () => {
    expect(normalizeOrderConfig({ activeVenue: "", templates: [], extHoursMarketBufferPct: 2.5 }).extHoursMarketBufferPct).toBe(2.5);
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && npx vitest run src/chrome/exec/actionTemplate.test.ts`
Expected: FAIL (`extHoursMarketBufferPct` is `undefined`) — likely a `tsc`/type error first since the field isn't on `OrderConfig`.

- [ ] **Step 3: Add the field** to `OrderConfig` in `ui/src/chrome/exec/actionTemplate.ts:23`:

```ts
export interface OrderConfig {
  templates: ActionTemplate[];
  activeVenue: VenueID;
  extHoursMarketBufferPct?: number;   // absent => 1.0; clamped [0.1, 10] in normalizeOrderConfig
}
```

- [ ] **Step 4: Default + clamp in `normalizeOrderConfig`** (`ui/src/chrome/exec/actionTemplate.ts:48-50`):

```ts
export function normalizeOrderConfig(config: OrderConfig): OrderConfig {
  const raw = config.extHoursMarketBufferPct;
  const extHoursMarketBufferPct = raw === undefined || Number.isNaN(raw) ? 1.0 : Math.min(10, Math.max(0.1, raw));
  return { ...config, extHoursMarketBufferPct, templates: config.templates.map(normalizeTemplate) };
}
```

Also extend the doc comment above `normalizeTemplate` (line 33-38) to mention it "defaults a missing `extHoursMarketBufferPct` to 1.0 (clamped [0.1, 10])".

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd ui && npx vitest run src/chrome/exec/actionTemplate.test.ts`
Expected: PASS (existing idempotency + new tests). Also confirm `useOrderConfig.test.tsx` still passes (its `toEqual(normalizeOrderConfig(DEFAULT_ORDER_CONFIG))` assertion normalizes both sides, so it stays green): `npx vitest run src/chrome/exec/useOrderConfig.test.tsx`.

- [ ] **Step 6: Commit**

```bash
git add ui/src/chrome/exec/actionTemplate.ts ui/src/chrome/exec/actionTemplate.test.ts
git commit -m "feat(exec-ui): add extHoursMarketBufferPct order config field (default 1%)"
```

---

### Task 3: Marketable-limit conversion in `preChecks` + thread quote & buffer

**Files:**
- Modify: `ui/src/chrome/exec/preChecks.ts` (signature, conversion, tick helpers, header comment)
- Test: `ui/src/chrome/exec/preChecks.test.ts` (rewrite market cases + signature migration)
- Modify: `ui/src/chrome/exec/resolveTemplate.ts:11-14,25` (+ `.test.ts`)
- Modify: `ui/src/chrome/exec/useHotkeys.ts:38`
- Modify: `ui/src/chrome/panels/OrderTicketPanel.tsx:13,82` (+ read config)
- Modify: `ui/src/chrome/panels/AccountPanel.tsx:244` (+ read config)

**Interfaces:**
- Produces: `preCheck(draft: DraftOrder, quote: { bid: number; ask: number; last: number }, nowMs: number, extBufferPct: number): PreCheckResult`. `ResolveContext` gains **required** `extHoursMarketBufferPct: number`.
- Consumes: `OrderConfig.extHoursMarketBufferPct` (Task 2), `sessionAt(nowMs)`, existing `PreCheckResult { ok, order, errors, notices }`.

**Context:** The signature change is breaking, and there are **four** `preCheck`/`ResolveContext` construction sites — all must move to the new form in this one task so the build stays green: `preCheck` is called directly in `resolveTemplate.ts:25` and `OrderTicketPanel.tsx:82`; `ResolveContext` is built in `useHotkeys.ts:38` and `AccountPanel.tsx:244` (a MARKET flatten — exactly the feature's target, so easy to miss). Neither `OrderTicketPanel` nor `AccountPanel` calls `useOrderConfig` today (both reach venue state via `useVenueSelection`, which calls it internally, so both already have an `OrderConfigProvider` ancestor — confirmed, and their tests already wrap one). Add a direct `useOrderConfig()` read in each for the buffer.

- [ ] **Step 1: Rewrite the tests** — replace the whole body of `ui/src/chrome/exec/preChecks.test.ts` with:

```ts
import { describe, it, expect } from "vitest";
import { preCheck, type DraftOrder } from "./preChecks";

// ET: 2026-07-06 is a Monday. 14:00 UTC = 10:00 ET (RTH). 08:00 UTC = 04:00 ET (pre).
const RTH = Date.parse("2026-07-06T14:00:00Z");
const PRE = Date.parse("2026-07-06T08:00:00Z");
const draft = (o: Partial<DraftOrder>): DraftOrder =>
  ({ symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO", qty: 10, limitPrice: 3.5, stopPrice: 0, ...o });
const q = (o: Partial<{ bid: number; ask: number; last: number }> = {}) =>
  ({ bid: 3.49, ask: 3.5, last: 3.5, ...o });

describe("preCheck", () => {
  it("blocks non-positive quantity", () => {
    const r = preCheck(draft({ qty: 0 }), q(), RTH, 1);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/greater than 0/);
  });
  it("passes a clean RTH limit", () => {
    expect(preCheck(draft({}), q(), RTH, 1).ok).toBe(true);
  });
  it("converts a buy Market outside RTH to ask + buffer, rounded up to tick", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q({ ask: 3.5 }), PRE, 1);
    expect(r.ok).toBe(true);
    expect(r.order.type).toBe("LIMIT");
    expect(r.order.limitPrice).toBeCloseTo(3.54, 2); // ceil(3.535 / 0.01) * 0.01
    expect(r.notices.join(" ")).toMatch(/Limit @ 3\.54 \(ask \+1%\)/);
  });
  it("converts a sell Market outside RTH to bid - buffer, rounded down to tick", () => {
    const r = preCheck(draft({ side: "SELL", type: "MARKET", limitPrice: 0 }), q({ bid: 3.49 }), PRE, 1);
    expect(r.order.type).toBe("LIMIT");
    expect(r.order.limitPrice).toBeCloseTo(3.45, 2); // floor(3.4551 / 0.01) * 0.01
    expect(r.notices.join(" ")).toMatch(/Limit @ 3\.45 \(bid -1%\)/);
  });
  it("uses the $0.0001 tick below $1", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q({ ask: 0.5 }), PRE, 1);
    expect(r.order.limitPrice).toBeCloseTo(0.505, 4); // 0.5 * 1.01, already on a 0.0001 tick
  });
  it("falls back to last (with a notice) when the relevant book side is empty", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q({ ask: 0, last: 3.44 }), PRE, 1);
    expect(r.order.type).toBe("LIMIT");
    expect(r.order.limitPrice).toBeCloseTo(3.48, 2); // ceil(3.4744 / 0.01) * 0.01
    expect(r.notices.join(" ")).toMatch(/no ask; last \+1%/);
  });
  it("blocks a Market outside RTH with no usable price (side and last both 0)", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q({ ask: 0, bid: 0, last: 0 }), PRE, 1);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/no price to coerce/);
  });
  it("respects the buffer percentage (2% vs 1%)", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q({ ask: 3.5 }), PRE, 2);
    expect(r.order.limitPrice).toBeCloseTo(3.57, 2); // 3.5 * 1.02
  });
  it("leaves a Market during RTH unconverted", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q(), RTH, 1);
    expect(r.order.type).toBe("MARKET");
    expect(r.notices).toHaveLength(0);
  });
  // Broker-safety net keyed on the ACTUAL clock: must apply regardless of the
  // trader's explicit session choice (a chosen session only affects a Limit
  // order's wire TIF/extended_hours flag downstream, never a Market's ability
  // to submit right now).
  it("still converts a Market outside RTH even when the trader explicitly chose RTH", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0, session: "RTH" }), q(), PRE, 1);
    expect(r.order.type).toBe("LIMIT");
    expect(r.notices.join(" ")).toMatch(/Limit @/);
  });
  it("leaves Market alone during actual RTH even when the trader chose EXTENDED/OVERNIGHT", () => {
    for (const session of ["EXTENDED", "OVERNIGHT"] as const) {
      const r = preCheck(draft({ type: "MARKET", limitPrice: 0, session }), q(), RTH, 1);
      expect(r.order.type).toBe("MARKET");
      expect(r.notices).toHaveLength(0);
    }
  });
  it("passes the chosen session through unchanged (never overwritten by the conversion)", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0, session: "OVERNIGHT" }), q(), PRE, 1);
    expect(r.order.session).toBe("OVERNIGHT");
  });
  it("rejects an inverted buy stop-limit (limit below stop)", () => {
    const r = preCheck(draft({ type: "STOP_LIMIT", side: "BUY", stopPrice: 3.6, limitPrice: 3.5 }), q(), RTH, 1);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/Inverted buy stop-limit/);
  });
  it("rejects an inverted sell stop-limit (limit above stop)", () => {
    const r = preCheck(draft({ type: "STOP_LIMIT", side: "SELL", stopPrice: 3.4, limitPrice: 3.5 }), q(), RTH, 1);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/Inverted sell stop-limit/);
  });
  it("accepts a coherent sell stop-limit", () => {
    expect(preCheck(draft({ type: "STOP_LIMIT", side: "SELL", stopPrice: 3.5, limitPrice: 3.4 }), q(), RTH, 1).ok).toBe(true);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && npx vitest run src/chrome/exec/preChecks.test.ts`
Expected: FAIL / type error (`preCheck` still takes `(draft, last, nowMs)`).

- [ ] **Step 3: Rewrite `preChecks.ts`** — update the header comment (lines 1-14) to describe marketable-limit conversion instead of "Limit-at-last" (keep the ACTUAL-clock rationale paragraph verbatim), add tick helpers before `preCheck`, and replace the signature + coercion block:

Add above `preCheck` (after the `PreCheckResult` interface):

```ts
// SEC sub-penny rule: $0.01 tick at/above $1.00, $0.0001 below. Buys round UP
// and sells round DOWN so a converted marketable limit never lands on an
// invalid price increment and never loses marketability to the rounding.
function tickOf(price: number): number {
  return price >= 1 ? 0.01 : 0.0001;
}
function roundUpToTick(price: number): number {
  const t = tickOf(price);
  return Number((Math.ceil(price / t) * t).toFixed(t === 0.01 ? 2 : 4));
}
function roundDownToTick(price: number): number {
  const t = tickOf(price);
  return Number((Math.floor(price / t) * t).toFixed(t === 0.01 ? 2 : 4));
}
```

Replace the function signature + the market block (lines 29-39):

```ts
export function preCheck(
  draft: DraftOrder,
  quote: { bid: number; ask: number; last: number },
  nowMs: number,
  extBufferPct: number,
): PreCheckResult {
  const errors: string[] = [];
  const notices: string[] = [];
  let order: DraftOrder = { ...draft };

  if (!(order.qty > 0)) errors.push("Quantity must be greater than 0.");

  // Market outside RTH → aggressive marketable limit (ask×(1+pct) buys /
  // bid×(1−pct) sells), tick-rounded. Falls back to last for a one-sided book.
  if (order.type === "MARKET" && sessionAt(nowMs) !== "rth") {
    const buyish = order.side === "BUY" || order.side === "COVER";
    const leg = buyish ? quote.ask : quote.bid;
    const usedFallback = !(leg > 0);
    const base = usedFallback ? quote.last : leg;
    if (base > 0) {
      const mult = buyish ? 1 + extBufferPct / 100 : 1 - extBufferPct / 100;
      const limitPrice = buyish ? roundUpToTick(base * mult) : roundDownToTick(base * mult);
      order = { ...order, type: "LIMIT", limitPrice };
      const legName = buyish ? "ask" : "bid";
      const sign = buyish ? "+" : "-";
      const shown = limitPrice >= 1 ? limitPrice.toFixed(2) : limitPrice.toFixed(4);
      notices.push(
        usedFallback
          ? `Market outside RTH → Limit @ ${shown} (no ${legName}; last ${sign}${extBufferPct}%).`
          : `Market outside RTH → Limit @ ${shown} (${legName} ${sign}${extBufferPct}%).`,
      );
    } else {
      errors.push("Market order outside RTH and no price to coerce to.");
    }
  }
```

(The rest of the function — STOP / LIMIT / STOP_LIMIT checks and the `return` — is unchanged.)

- [ ] **Step 4: Update `resolveTemplate.ts`** — add the buffer to `ResolveContext` (line 11-14) and pass the quote + buffer into `preCheck` (line 25):

```ts
export interface ResolveContext {
  venue: VenueID; symbol: string; quote: Quote;
  buyingPower: number; positionQty: number; nowMs: number;
  extHoursMarketBufferPct: number;
}
```

```ts
  const pc = preCheck(draft, ctx.quote, ctx.nowMs, ctx.extHoursMarketBufferPct);
```

- [ ] **Step 5: Update `useHotkeys.ts`** — pass the buffer when building the resolve context (line 38):

```ts
        const r = resolvePlaceTemplate(t as PlaceOrderTemplate, {
          venue, symbol, quote,
          buyingPower: account?.buyingPower ?? 0, positionQty, nowMs: Date.now(),
          extHoursMarketBufferPct: config.extHoursMarketBufferPct ?? 1,
        });
```

- [ ] **Step 6: Update `OrderTicketPanel.tsx`** — import `useOrderConfig`, read the buffer, and pass the quote + buffer to `preCheck`.

Add the import (near line 10-16):

```ts
import { useOrderConfig } from "../exec/useOrderConfig";
```

Inside the component (near line 57, next to `useVenueSelection`):

```ts
  const { config: orderConfig } = useOrderConfig();
  const extBufferPct = orderConfig.extHoursMarketBufferPct ?? 1;
```

Replace the `preCheck` call (line 82):

```ts
    const pc = preCheck(draft, quote ?? { bid: 0, ask: 0, last: 0 }, Date.now(), extBufferPct);
```

- [ ] **Step 7: Update `AccountPanel.tsx`** — import `useOrderConfig`, read the buffer, and pass it into the `ResolveContext` built for the Flatten button (line 244).

Add the import (near line 8-9):

```ts
import { useOrderConfig } from "../exec/useOrderConfig";
```

Inside the component (near line 304, next to `useVenueSelection`):

```ts
  const { config: orderConfig } = useOrderConfig();
  const extBufferPct = orderConfig.extHoursMarketBufferPct ?? 1;
```

Update the `resolvePlaceTemplate` call (line 244):

```ts
    const r = resolvePlaceTemplate(t, { venue, symbol: row.symbol, quote, buyingPower: 0, positionQty: row.qty, nowMs: Date.now(), extHoursMarketBufferPct: extBufferPct });
```

- [ ] **Step 8: Update `resolveTemplate.test.ts`** — add `extHoursMarketBufferPct: 1` to every `ResolveContext` literal (the six `resolvePlaceTemplate(..., { ... })` calls), add a `PRE` clock const, and add one conversion test:

Add near line 6: `const PRE = Date.parse("2026-07-06T08:00:00Z");`

New test inside the describe:

```ts
  it("converts a MARKET template outside RTH to a buffered LIMIT and flashes the limit price", () => {
    const r = resolvePlaceTemplate(
      tmpl({ type: "MARKET", priceSource: "Ask" }),
      { venue: "v", symbol: "US.AAPL", quote: q, buyingPower: 10_000, positionQty: 0, nowMs: PRE, extHoursMarketBufferPct: 1 });
    expect(r.args.type).toBe("LIMIT");
    expect(r.args.limitPrice).toBeCloseTo(3.54, 2); // ask 3.50 * 1.01 → tick-up
    expect(r.flash).toContain("3.54 LMT");
    expect(r.preCheck.notices.join(" ")).toMatch(/ask \+1%/);
  });
```

- [ ] **Step 9: Run the touched UI tests + typecheck**

Run: `cd ui && npx vitest run src/chrome/exec/preChecks.test.ts src/chrome/exec/resolveTemplate.test.ts && npx tsc --noEmit`
Expected: PASS; `tsc` clean (confirms `OrderTicketPanel.tsx` / `useHotkeys.ts` / `AccountPanel.tsx` compile against the new signature — `tsc` is what catches a missed `ResolveContext` call site, since `extHoursMarketBufferPct` is required).

- [ ] **Step 10: Run the full UI suite (catch panel/hotkey tests)**

Run: `cd ui && npx vitest run`
Expected: PASS. (`OrderTicketPanel.test.tsx` and `AccountPanel.test.tsx` already wrap an `OrderConfigProvider` — confirmed — so no test scaffolding changes are needed.)

- [ ] **Step 11: Commit**

```bash
git add ui/src/chrome/exec/preChecks.ts ui/src/chrome/exec/preChecks.test.ts ui/src/chrome/exec/resolveTemplate.ts ui/src/chrome/exec/resolveTemplate.test.ts ui/src/chrome/exec/useHotkeys.ts ui/src/chrome/panels/OrderTicketPanel.tsx ui/src/chrome/panels/AccountPanel.tsx
git commit -m "feat(exec-ui): convert market orders outside RTH to marketable limits"
```

---

### Task 4: Ext-hours market buffer editor field

**Files:**
- Modify: `ui/src/chrome/exec/OrderSettingsSection.tsx`
- Test: `ui/src/chrome/exec/OrderSettingsSection.test.tsx`

**Interfaces:**
- Consumes: `OrderConfig.extHoursMarketBufferPct` (Task 2), existing `StepField`, `clampNum`, `rawEdits` staging pattern.
- Produces: `onSave` now includes `extHoursMarketBufferPct` from local editor state.

**Note on placement:** the spec says "at the config level (next to the active-venue control)". `OrderSettingsSection` has **no** active-venue control (that lives in the ticket header, `OrderTicketPanel.tsx:118-129`) and no config-level controls today — everything is per-template. This field is therefore added as a **new config-level row in the settings section**, above the `TEMPLATES` label, which is the intent ("config level, not per-template").

- [ ] **Step 1: Write the failing tests** — append to `ui/src/chrome/exec/OrderSettingsSection.test.tsx` (SAMPLE_ORDER_CONFIG has no `extHoursMarketBufferPct`, so the editor defaults to 1.0):

```ts
  it("defaults the ext-hours market buffer to 1.0 and saves it", () => {
    const { onSave } = wrap();
    fireEvent.click(screen.getByTestId("save"));
    expect(onSave.mock.calls[0][0].extHoursMarketBufferPct).toBe(1.0);
  });
  it("nudges the ext-hours market buffer up by 0.1", () => {
    const { onSave } = wrap();
    fireEvent.click(screen.getByTestId("ext-buffer-up"));
    fireEvent.click(screen.getByTestId("save"));
    expect(onSave.mock.calls[0][0].extHoursMarketBufferPct).toBeCloseTo(1.1);
  });
  it("caps a typed ext-hours buffer at 10 on save", () => {
    const { onSave } = wrap();
    fireEvent.change(screen.getByLabelText("ext-buffer"), { target: { value: "50" } });
    fireEvent.click(screen.getByTestId("save"));
    expect(onSave.mock.calls[0][0].extHoursMarketBufferPct).toBe(10);
  });
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && npx vitest run src/chrome/exec/OrderSettingsSection.test.tsx`
Expected: FAIL (no `ext-buffer` field; `extHoursMarketBufferPct` undefined in saved config).

- [ ] **Step 3: Add a step constant + round helper** in `ui/src/chrome/exec/OrderSettingsSection.tsx` near `OFFSET_STEP` (line 40) and `round2` (line 49):

```ts
const EXT_BUFFER_STEP = 0.1;
```

```ts
function round1(n: number): number {
  return Math.round(n * 10) / 10;
}
```

- [ ] **Step 4: Add editor state** inside `OrderSettingsSection` (after the `templates` state, ~line 265):

```ts
  const [extBufferPct, setExtBufferPct] = useState<number>(() => config.extHoursMarketBufferPct ?? 1.0);
```

Reset it in `doReset` (line 306) — add `setExtBufferPct(1.0);` alongside `setRawEdits({})`:

```ts
  const doReset = () => { setTemplates(normalizeOrderConfig({ ...config, templates: DEFAULT_TEMPLATES.map((t) => ({ ...t })) }).templates); setExtBufferPct(1.0); setRawEdits({}); setConfirmReset(false); };
```

- [ ] **Step 5: Render the config-level field** — insert before the `TEMPLATES` label (line 335):

```tsx
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 10 }}>
        <span style={{ fontSize: 11, color: palette.text }}>Ext-hours market buffer %</span>
        <StepField
          ariaLabel="ext-buffer"
          testid="ext-buffer"
          value={rawEdits["config:ext-buffer"] ?? String(extBufferPct)}
          onType={(v) => {
            setRawEdit("config:ext-buffer", v);
            const n = Number(v);
            if (!Number.isNaN(n)) setExtBufferPct(clampNum(n, 0.1, 10));
          }}
          onStep={(dir) => {
            setExtBufferPct((p) => clampNum(round1(p + dir * EXT_BUFFER_STEP), 0.1, 10));
            clearRawEdit("config:ext-buffer");
          }}
          onBlur={() => clearRawEdit("config:ext-buffer")}
          style={{ width: 84 }}
        />
      </div>
```

- [ ] **Step 6: Include the buffer in the saved config** — update the Save button `onClick` (line 359):

```tsx
          className="btn" data-testid="save" disabled={hasConflict} onClick={() => onSave({ ...config, templates, extHoursMarketBufferPct: extBufferPct })}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd ui && npx vitest run src/chrome/exec/OrderSettingsSection.test.tsx && npx tsc --noEmit`
Expected: PASS; `tsc` clean.

- [ ] **Step 8: Commit**

```bash
git add ui/src/chrome/exec/OrderSettingsSection.tsx ui/src/chrome/exec/OrderSettingsSection.test.tsx
git commit -m "feat(exec-ui): add ext-hours market buffer % field to order settings"
```

---

## Verification (end-to-end)

**Automated:**
- Engine: `cd engine && go build ./... && go vet ./... && go test ./internal/exec/... ./internal/broker/...` → all pass.
- UI: `cd ui && npx tsc --noEmit && npx vitest run` → all pass. (These files aren't canvas-touching, so the batched-vitest quirk noted in prior work doesn't apply.)

**Manual smoke (replay / demo mode, no live venue):**
1. Launch the app in replay/demo mode (`-demo`) so `sim` is the active venue.
2. In Settings → Orders & hotkeys, confirm the **Ext-hours market buffer %** field shows `1`, change it to `2`, Save, reopen Settings → it persists as `2` (round-trips through `SetConfig`/`GetConfig` + `normalizeOrderConfig`).
3. The UI conversion trigger uses the real wall clock (`Date.now()`), so to observe it either run during actual pre/post-market **or** temporarily verify via the unit tests. When outside RTH: place a MARKET order from the ticket and via a MARKET hotkey template → expect a warn toast `Market outside RTH → Limit @ <px> (ask +1%)` (buy) / `(bid -1%)` (sell), the order goes out as a **LIMIT** at the buffered, tick-valid price, and the flash shows the concrete limit price (not `MKT`).
4. Confirm RTH is untouched: during RTH a MARKET order submits as MARKET with no notice.

**Backstop:** exercised by the Go tests in Task 1 (the UI always converts, so the engine block is a safety net that's hard to trigger from the happy-path UI). The `_BlockedOnRealVenue` test proves a raw MARKET outside RTH on a non-capable venue yields `OrderBlocked` (StatusBlocked), `_MarketDuringRTH_NotBackstopped` proves RTH passes, and `_SimExempt` proves sim is exempt.

## Self-Review notes (spec coverage)

- Spec §1 (UI conversion, tick rounding, fallback, notices, blocking error) → Task 3. §2 (engine backstop, sim exempt, clock-injectable) → Task 1. §3 (buffer setting: field, normalize default/clamp, editor, threading) → Tasks 2 + 4 + 3. §4 (no enum/adapter/gate/sim-fill/RTH changes) → honored by construction (no such files touched). Error handling + Testing sections → covered across tasks.
- Type consistency: `preCheck(draft, quote, nowMs, extBufferPct)` is called in `resolveTemplate.ts` and `OrderTicketPanel.tsx`; `ResolveContext.extHoursMarketBufferPct` (required) is built in `useHotkeys.ts` and `AccountPanel.tsx` — all four sites updated in Task 3 (`tsc` enforces the fourth). `Capabilities.MarketOutsideRTH` used identically in `broker.go`, `sim.go`, `core.go`, `core_test.go`.
- Two intentional deviations from the spec's literal wording, both surfaced above: (a) `preCheck` widens from a scalar `last` to a quote object (the spec assumed a signature it didn't have); (b) the buffer field is placed as a config-level row in the settings section (no active-venue control exists there to sit beside).
