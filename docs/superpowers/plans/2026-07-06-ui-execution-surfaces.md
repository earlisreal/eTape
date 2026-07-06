# eTape UI — Plan 5 of 6: Execution Surfaces & Order Entry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fill the four execution panels the Trading workspace already seeds but renders as placeholders — **account bar, positions, open orders, order ticket** — plus the whole "Order entry & hotkeys" surface (customizable action templates, arm-gated hotkeys, sizing/price resolution, client pre-checks, gate ack + toast handling, kill switch, cancel/replace) and the live **fills-on-chart** wire deferred from Plan 2 — all typed against a hand-authored interim exec contract that mirrors the engine's Go structs, developed and tested against the mock engine.

**Architecture:** Execution data is the **low-rate React case** (account ~4 Hz, positions ~100 ms batches, orders event-driven), so account/positions/orders live in a retyped `ExecStore` (`ReactStore`, `useSyncExternalStore`) and render as React tables in `chrome/panels/`. Fills are chart markers, not a table, so they get a dedicated `FillStore` (`PaintStore`, `getRev()` cursor) feeding the existing `ChartController.setFills`. All order logic is **pure, DOM-free, and heavily unit-tested** — sizing math, price-source resolution, client pre-checks, template resolution, hotkey matching, display-status derivation — living in `chrome/exec/*.ts` (pure `.ts`, no React import), with the panels/hooks/modal (`.tsx`) as thin wiring on top. The command path is one `OrderCommands` class taking an injected command adapter + `ExecStore` + toast API + `now()`; every submit registers an optimistic `PendingNew` row keyed by the ack's `orderId`, reconciled when the real `exec.orders` event lands. Because the Go engine does not exist yet, this plan hand-authors the exec payload interfaces in `wire/contract.ts` (field-for-field with the engine's `exec` package structs) so tygo-generated `ui/src/gen/*` is a later drop-in.

**Tech Stack:** TypeScript 5, React 18, Vite 5, Vitest (unit + jsdom component tests), `@testing-library/react`, dockview, lightweight-charts v5 (fills-on-chart only, via the existing `ChartController`). **No new runtime or test dependencies.** Playwright smoke + `ui/dist` static serving remain Plan 6.

## Global Constraints

- **Hard rule:** high-frequency data (chart/ladder/tape/book/quote) never flows through React state. Execution surfaces are the **low-rate exception** (declared `ReactStore`-based since Plan 1): account/positions/orders render as React tables via `useSyncExternalStore`. The order ticket's live bid/ask is a market-data read, so it is throttled to ≤ ~6 Hz through `useThrottledQuote` (an rAF-gated projection of `QuoteStore`), never a raw per-tick React update. Fills feed a canvas surface (the chart) via a `PaintStore` + `getRev()` cursor, never React.
- **Dependency direction:** `chrome → render → data → wire`, never backwards. Pure order logic lives in `chrome/exec/*.ts`; it may import `wire/contract` types and `render/*` pure helpers (`render/format.ts`, `render/chart/sessions.ts`), and must never import from `chrome/panels/*` React components. `data/FillStore.ts` imports only `wire/contract` + `render/chart/diamondMarker` types.
- **Honesty policy:** never render stale as live, never render in-flight as done. A submitted-but-unconfirmed order shows `PendingNew` (client-optimistic) until the first `exec.orders` event lands — never as `Filled`/`Accepted`. Missing account fields render `—`, never a fabricated `$0`. A blocked or rejected order raises a **prominent toast with the verbatim reason** and never disappears silently. A venue with `reconcilePending` shows the `StreamGap` badge ("state reconciled — verify before acting") on the orders panel.
- **Wire format:** WebSocket + JSON; each topic delivers a full snapshot on subscribe, then deltas. `exec.account` is keyed by venue (one `AccountRow` per key, upsert). `exec.positions` and `exec.status` are full-replace (low rate). `exec.orders` is keyed by order id (upsert). `exec.fills` is **append-only, deduped by fill identity** (a reconnect re-snapshot merges, never wipes backfilled history) — the one deliberate deviation from snapshot-replaces. The UI requests logical topics and never reasons about moomoo/broker quota.
- **Type source of truth:** `ui/src/wire/contract.ts` is the interim hand-authored contract (tygo target `ui/src/gen/*`). New exec types added this plan (`Order`, `Fill`, `PositionRow`, `AccountRow`, `ExecStatus`, `VenueStatus`, enums, command-arg interfaces) keep field names identical to the engine's `exec` package (`docs/superpowers/plans/2026-07-05-engine-execution-core.md`) so regeneration is a drop-in.
- **Correlation IDs & ack shape:** every command carries a `corrId`; the synchronous ack is `accepted | blocked(reason)`. `AckMsg` is extended this plan with `orderId?` (returned on a `SubmitOrder` accept, keying the optimistic row) and `value?` (already relied on by config gets, now typed). Outcomes arrive asynchronously on `exec.orders`/`exec.fills`/`exec.status`.
- **Order lifecycle — the 9 domain states + 2 UI-derived states.** The wire `OrderStatus` is exactly the engine's 9: `SUBMITTED, ACCEPTED, PARTIALLY_FILLED, FILLED, CANCELED, REJECTED, EXPIRED, BLOCKED, REPLACED`. The UI displays two additional derived states that are **not wire values**: `PendingNew` (client-optimistic, before the first order event) and `Replacing` (derived: `order.replacesId !== "" && isWorking(status)` — a TZ-adapter cosmetic, never emitted by native-replace venues). "Working" = `{SUBMITTED, ACCEPTED, PARTIALLY_FILLED}`.
- **Every order names its venue.** `VenueID` is a free-form config slug (e.g. `"tradezero-live"`, `"alpaca-paper"`, `"moomoo-live"`); `broker` is the 3-value enum `tradezero|alpaca|moomoo`. Broker-selection *UX* is deferred (UI design), but every command carries an explicit venue: the ticket + hotkeys resolve it from a shared `activeVenue` config value (defaulting to the first venue in `exec.status`).
- **Arm gating (safety-critical):** order-placing actions (ticket submit, place-hotkeys, per-row flatten, position close) fire **only while armed** (`ExecStatus.masterArmed && venue.venueArmed`) **and only when the eTape window has OS focus** (`document.hasFocus()`); a blocked place-hotkey flashes "disarmed" and sends nothing. **Management actions (cancel-last, cancel-all, kill switch) fire regardless of armed state** — closing exposure is never gated. The kill switch **never places orders** (cancel-all + disarm only; flatten is a separate, explicit command).
- **Gate reason strings render verbatim.** The engine's gate emits plain-English reasons; the UI shows them unaltered in the block toast + orders panel: `"master disarmed"`, `"venue disarmed"`, `"duplicate order id"`, `"no mark to value market order"`, `"order value exceeds venue cap"`, `"resulting venue position exceeds share cap"`, `"resulting venue position exceeds value cap"`, `"max open orders on venue"`, `"resulting symbol position exceeds global share cap"`, `"resulting symbol position exceeds global value cap"`. Broker rejects (TZ R-codes like `R78`, `R114`) arrive in `order.rejectReason` and are shown verbatim too.
- **Palette:** all colors come from Plan 2's `ui/src/render/palette.ts` via `useTheme()`; React chrome reads `palette.*` in inline styles. This plan adds **no new palette tokens** (reuses `up/down/accent/ok/warn/danger/text/textMuted/surface/border/bg/link*`).
- **ET timezone / US-only:** session math (RTH-coercion pre-check) is US Eastern via the existing `render/chart/sessions.ts`. US stocks only.
- **Clocks in tests:** pure logic takes `nowMs`/`now()` as a parameter (deterministic, no `Date.now()` inside pure modules); only the thin React hooks/command client read the real clock, via an injected `now: () => number` so they stay testable.

---

## File Structure

**Wire (`ui/src/wire/`)** — `contract.ts` MODIFY (exec types, command-arg interfaces, `AckMsg` extension, `QueryMsg`/`ResultMsg`); `codec.ts` MODIFY (decode `result`); `WsClient.ts` MODIFY (`sendQuery`).

**Data (`ui/src/data/`)** — `ExecStore.ts` MODIFY (typed account/positions/orders/status + optimistic rows); `FillStore.ts` NEW (`PaintStore`, per-symbol fills → `FillMarker[]`); `registry.ts` MODIFY (add `fills`, re-route `exec.fills`).

**Pure order logic (`ui/src/chrome/exec/`, all `.ts`, no React)** — `orderStatus.ts` (display-status derivation, labels), `sizing.ts`, `priceSource.ts`, `preChecks.ts`, `actionTemplate.ts` (types + defaults + storage), `resolveTemplate.ts`, `hotkeys.ts` (pure key-combo matcher), `commands.ts` (`OrderCommands`).

**React chrome (`ui/src/chrome/`)** — `Toast.tsx` NEW (provider + host + `useToasts`); `exec/useThrottledQuote.ts`, `exec/useOrderCommands.ts`, `exec/useOrderConfig.ts`, `exec/useHotkeys.ts` (hooks); `exec/OrderSettingsModal.tsx` NEW; `panels/AccountBarPanel.tsx`, `panels/PositionsPanel.tsx`, `panels/OpenOrdersPanel.tsx`, `panels/OrderTicketPanel.tsx` NEW; `panels/registry.tsx` MODIFY (register 4 panels, widen `commands` type); `PanelFrame.tsx`/`AppShell.tsx`/`App.tsx` MODIFY (thread `sendQuery`, mount `ToastProvider` + `useHotkeys`); `panels/ChartPanel.tsx` MODIFY (fills wire); `render/chart/sessions.ts` MODIFY (export `sessionAt`); `render/ladder/ladderState.ts` MODIFY (typed `workingOrderMarks`).

**Fixtures / mock (`ui/`)** — `fixtures/exec-session.json` NEW; `mock-engine/server.ts` MODIFY (command → orderId ack + follow-up events; `QueryFills` result).

---

## Task 1: Exec wire contract + order-status helpers (pure)

**Files:**
- Modify: `ui/src/wire/contract.ts`
- Create: `ui/src/wire/orderStatus.ts` (data-layer predicates over wire types)
- Create: `ui/src/chrome/exec/orderStatus.ts` (display helpers; re-exports the predicates)
- Test: `ui/src/chrome/exec/orderStatus.test.ts`

**Layering note:** `data/ExecStore.ts`, `data/FillStore.ts`, and `render/ladder/ladderState.ts` all need `isWorking`/`sideIsSell`, but the dependency rule forbids `data`/`render` from importing `chrome`. So the **pure predicates over wire types live in `wire/orderStatus.ts`** (the only layer `data` may import); the **display helpers** (`displayStatus`, labels, `bareSymbol`, `abbrevType`) live in `chrome/exec/orderStatus.ts` and re-export the predicates for chrome consumers.

**Interfaces:**
- Consumes: existing `AckMsg`, `ServerMessage`/`ClientMessage` unions, `Quote` (contract.ts).
- Produces: `VenueID`, `Broker`, `Side`, `OrderType`, `TIF`, `OrderStatus`, `Order`, `Fill`, `PositionRow`, `AccountRow`, `GateLimitsView`, `VenueStatus`, `ExecStatus`, `SubmitOrderArgs`, `CancelOrderArgs`, `ReplaceOrderArgs`, `FlattenArgs`, `KillSwitchArgs`, `ArmArgs`; extended `AckMsg` (`orderId?`, `value?`). From `wire/orderStatus.ts`: `isWorking(OrderStatus)`, `isTerminal(OrderStatus)`, `sideIsSell(Side)`. From `chrome/exec/orderStatus.ts`: `type DisplayStatus`, `displayStatus(order, optimistic)`, `STATUS_LABEL`, `sideLabel(Side)`, `bareSymbol(string)`, `abbrevType(OrderType)` + re-exported predicates.

- [ ] **Step 1: Add exec payload + command types to the interim contract**

In `ui/src/wire/contract.ts`, extend `AckMsg` and append an exec section (place after the `news` section, before the server→client block):

```ts
// ---- server → client (extend AckMsg) ----
export interface AckMsg {
  kind: "ack"; corrId: string;
  status: "accepted" | "blocked"; reason?: string;
  orderId?: string;    // returned on a SubmitOrder accept — keys the optimistic PendingNew row
  value?: unknown;     // returned on a GetConfig accept (already relied on by ThemeProvider/WorkspaceStore)
}
```

```ts
// ---- execution (Plan 5) ----
// Field names mirror engine/internal/exec structs (engine-execution-core plan) so
// tygo-generated ui/src/gen/* is a drop-in. Venue is a free-form config slug.
export type VenueID = string;
export type Broker = "tradezero" | "alpaca" | "moomoo";
export type Side = "BUY" | "SELL" | "SHORT" | "COVER";
export type OrderType = "MARKET" | "LIMIT" | "STOP" | "STOP_LIMIT";
export type TIF = "DAY" | "GTC" | "IOC" | "FOK";
export type OrderStatus =
  | "SUBMITTED" | "ACCEPTED" | "PARTIALLY_FILLED" | "FILLED"
  | "CANCELED" | "REJECTED" | "EXPIRED" | "BLOCKED" | "REPLACED";

// exec.orders — keyed by `id`; snapshot payload = Order[], delta payload = Order (upsert).
export interface Order {
  venue: VenueID; id: string; symbol: string;
  side: Side; type: OrderType; tif: TIF;
  qty: number; limitPrice: number; stopPrice: number;
  status: OrderStatus; executedQty: number; leavesQty: number; avgFillPrice: number;
  rejectReason: string; replacesId: string;
  createdMs: number; updatedMs: number;
}
// exec.fills — append-only, keyed by symbol; snapshot payload = Fill[], delta payload = Fill.
export interface Fill { venue: VenueID; orderId: string; symbol: string; side: Side; qty: number; price: number; tsMs: number }
// exec.positions — full-replace; payload = PositionRow[]. venue===null => cross-venue net row.
export interface PositionRow { venue: VenueID | null; symbol: string; qty: number; avgPrice: number; unrealizedPnl: number }
// exec.account — keyed by venue; payload = AccountRow (upsert).
export interface AccountRow {
  venue: VenueID;
  equity: number; buyingPower: number; availableCash: number;
  sodEquity: number; realized: number; dayPnl: number; leverage: number;
  tsMs: number;
}
// exec.status — full-replace; payload = ExecStatus.
export interface GateLimitsView { maxOrderValue: number; maxPositionValue: number; maxPositionShares: number; maxOpenOrders: number }
export interface VenueStatus {
  venue: VenueID; broker: Broker; connected: boolean; venueArmed: boolean;
  reconcilePending: boolean; note: string; lastReconcileMs: number | null; gate: GateLimitsView;
}
export interface ExecStatus {
  masterArmed: boolean;
  global: { maxDayLoss: number; maxSymbolPositionValue: number; maxSymbolPositionShares: number };
  venues: VenueStatus[];
}

// ---- command args (UI → engine via CommandMsg.args) ----
export interface SubmitOrderArgs { venue: VenueID; symbol: string; side: Side; type: OrderType; tif: TIF; qty: number; limitPrice: number; stopPrice: number }
export interface CancelOrderArgs { venue: VenueID; orderId: string }
export interface ReplaceOrderArgs { venue: VenueID; orderId: string; qty: number; limitPrice: number; stopPrice: number }
export interface FlattenArgs { venue: VenueID }
export interface KillSwitchArgs { venue?: VenueID }   // omitted/empty => all venues
export interface ArmArgs { venue?: VenueID }          // omitted/empty => master
```

- [ ] **Step 2: Write the failing test for the order-status helpers**

Create `ui/src/chrome/exec/orderStatus.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { displayStatus, isWorking, isTerminal, STATUS_LABEL, sideIsSell, bareSymbol, abbrevType } from "./orderStatus";
import type { Order } from "../../wire/contract";

const base: Order = {
  venue: "alpaca-paper", id: "ET1", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY",
  qty: 10, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 10,
  avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1000, updatedMs: 1000,
};

describe("orderStatus", () => {
  it("optimistic order shows PendingNew regardless of wire status", () => {
    expect(displayStatus(base, true)).toBe("PendingNew");
  });
  it("derives Replacing from a working order with a replacesId", () => {
    expect(displayStatus({ ...base, replacesId: "ET0", status: "ACCEPTED" }, false)).toBe("Replacing");
    expect(displayStatus({ ...base, replacesId: "ET0", status: "FILLED" }, false)).toBe("FILLED"); // terminal wins
  });
  it("passes through domain status when not optimistic and no replace", () => {
    expect(displayStatus({ ...base, status: "PARTIALLY_FILLED" }, false)).toBe("PARTIALLY_FILLED");
  });
  it("classifies working vs terminal", () => {
    expect(isWorking("SUBMITTED")).toBe(true);
    expect(isWorking("FILLED")).toBe(false);
    expect(isTerminal("CANCELED")).toBe(true);
    expect(isTerminal("ACCEPTED")).toBe(false);
  });
  it("labels every display status", () => {
    for (const k of ["PendingNew","Replacing","SUBMITTED","ACCEPTED","PARTIALLY_FILLED","FILLED","CANCELED","REJECTED","EXPIRED","BLOCKED","REPLACED"] as const)
      expect(STATUS_LABEL[k]).toBeTruthy();
  });
  it("side sell-ness, bare symbol, type abbreviation", () => {
    expect(sideIsSell("SELL")).toBe(true);
    expect(sideIsSell("SHORT")).toBe(true);
    expect(sideIsSell("BUY")).toBe(false);
    expect(bareSymbol("US.AAPL")).toBe("AAPL");
    expect(abbrevType("STOP_LIMIT")).toBe("STPLMT");
  });
});
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/exec/orderStatus.test.ts`
Expected: FAIL — `Cannot find module './orderStatus'`.

- [ ] **Step 4: Implement the wire-layer predicates**

Create `ui/src/wire/orderStatus.ts` (pure predicates over wire types — importable by `data`/`render`/`chrome`):

```ts
// Pure predicates over the wire's OrderStatus/Side. Lives in wire/ so the data and
// render layers can import it (they must not import chrome/). Display concerns live
// in chrome/exec/orderStatus.ts.
import type { OrderStatus, Side } from "./contract";

const WORKING: ReadonlySet<OrderStatus> = new Set(["SUBMITTED", "ACCEPTED", "PARTIALLY_FILLED"]);
export function isWorking(status: OrderStatus): boolean { return WORKING.has(status); }
export function isTerminal(status: OrderStatus): boolean { return !WORKING.has(status); }
export function sideIsSell(side: Side): boolean { return side === "SELL" || side === "SHORT"; }
```

- [ ] **Step 5: Implement the display helpers**

Create `ui/src/chrome/exec/orderStatus.ts` (display derivation; re-exports the predicates so chrome consumers import from one place):

```ts
// Pure derivation of what the UI shows for an order. The wire carries the 9 domain
// OrderStatus values; the UI adds two derived states that are NOT wire values:
//   PendingNew — client-optimistic (submitted, no order event yet)
//   Replacing  — derived (replacesId set && still working; a TZ-adapter cosmetic)
import type { Order, OrderStatus, OrderType, Side } from "../../wire/contract";
import { isWorking, isTerminal, sideIsSell } from "../../wire/orderStatus";

export { isWorking, isTerminal, sideIsSell };

export type DisplayStatus = "PendingNew" | "Replacing" | OrderStatus;

export function displayStatus(order: Order, optimistic: boolean): DisplayStatus {
  if (optimistic) return "PendingNew";
  if (order.replacesId !== "" && isWorking(order.status)) return "Replacing";
  return order.status;
}

export const STATUS_LABEL: Record<DisplayStatus, string> = {
  PendingNew: "Pending", Replacing: "Replacing",
  SUBMITTED: "Submitted", ACCEPTED: "Accepted", PARTIALLY_FILLED: "Part. Filled",
  FILLED: "Filled", CANCELED: "Canceled", REJECTED: "Rejected",
  EXPIRED: "Expired", BLOCKED: "Blocked", REPLACED: "Replaced",
};

export function sideLabel(side: Side): string { return side; } // BUY/SELL/SHORT/COVER already display-ready
export function bareSymbol(symbol: string): string { const i = symbol.indexOf("."); return i >= 0 ? symbol.slice(i + 1) : symbol; }
export function abbrevType(t: OrderType): string {
  return t === "MARKET" ? "MKT" : t === "LIMIT" ? "LMT" : t === "STOP" ? "STP" : "STPLMT";
}
```

The Task-1 test (Step 2) imports `isWorking`/`isTerminal`/`sideIsSell` from `./orderStatus` — the re-export keeps it passing.

- [ ] **Step 6: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/exec/orderStatus.test.ts`
Expected: PASS (6 tests).

- [ ] **Step 7: Typecheck the contract change**

Run: `cd ui && npm run typecheck`
Expected: PASS — extending `AckMsg` with optional fields does not break existing readers (`ThemeProvider`/`WorkspaceStore` read `.value`; `WsClient` returns `AckMsg`).

- [ ] **Step 8: Commit**

```bash
cd ui && git add src/wire/contract.ts src/wire/orderStatus.ts src/chrome/exec/orderStatus.ts src/chrome/exec/orderStatus.test.ts
git commit -m "feat(ui/exec): typed exec contract + order-status helpers (wire predicates + chrome display)"
```

---

## Task 2: Retype ExecStore (account/positions/orders/status + optimistic rows)

**Files:**
- Modify: `ui/src/data/ExecStore.ts`
- Modify: `ui/src/render/ladder/ladderState.ts` (typed `workingOrderMarks`, `buildLadderState.orders: Order[]`)
- Modify: `ui/src/chrome/panels/LadderPanel.tsx` (typed working-orders source, link-following symbol)
- Test: `ui/src/data/ExecStore.test.ts`
- Test: `ui/src/render/ladder/ladderState.test.ts` (update the `workingOrderMarks` describe block)
- Test: `ui/test/golden/ladder.golden.test.ts` (retype the inline `workingOrders` fixture + `base.orders` cast to `Order[]` — this is the file that actually feeds `buildLadderState`; the goldens must stay pixel-identical)
- (`ui/src/data/registry.ts` needs **no change** here — it already routes `exec.status`→`stores.exec`; `exec.fills` is re-routed to `FillStore` in Task 15.)

**Interfaces:**
- Consumes: `Order`, `PositionRow`, `AccountRow`, `ExecStatus`, `SubmitOrderArgs`, `VenueID` (contract.ts); `isWorking`, `sideIsSell` (orderStatus.ts).
- Produces: `interface OptimisticOrder { args: SubmitOrderArgs; id: string; createdMs: number }`, `interface OrderView { order: Order; optimistic: boolean }`, `interface ExecState`, and `ExecStore` methods `accounts(): AccountRow[]`, `positions(): PositionRow[]`, `orders(): OrderView[]`, `status(): ExecStatus | null`, `addOptimistic(o: OptimisticOrder)`, `workingOrdersFor(symbol?): Order[]`.

- [ ] **Step 1: Write the failing test**

Replace `ui/src/data/ExecStore.test.ts` (create if absent):

```ts
import { describe, it, expect } from "vitest";
import { ExecStore } from "./ExecStore";
import type { Order, AccountRow, ExecStatus } from "../wire/contract";

const snap = (topic: string, payload: unknown, key?: string) => ({ kind: "snapshot" as const, topic: topic as never, key, payload });
const delta = (topic: string, payload: unknown, key?: string) => ({ kind: "delta" as const, topic: topic as never, key, payload });

const order = (id: string, over: Partial<Order> = {}): Order => ({
  venue: "alpaca-paper", id, symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY",
  qty: 10, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 10,
  avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, ...over,
});
const acct = (venue: string, dayPnl: number): AccountRow => ({
  venue, equity: 100, buyingPower: 400, availableCash: 50, sodEquity: 100, realized: 0, dayPnl, leverage: 4, tsMs: 1,
});
const status: ExecStatus = {
  masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }],
};

describe("ExecStore", () => {
  it("keyed account upsert by venue", () => {
    const s = new ExecStore();
    s.apply(snap("exec.account", acct("alpaca-paper", 5), "alpaca-paper"));
    s.apply(delta("exec.account", acct("tradezero-live", -3), "tradezero-live"));
    s.apply(delta("exec.account", acct("alpaca-paper", 9), "alpaca-paper")); // upsert, not append
    expect(s.accounts()).toHaveLength(2);
    expect(s.accounts().find((a) => a.venue === "alpaca-paper")?.dayPnl).toBe(9);
  });
  it("keyed order upsert by id (snapshot replaces, delta upserts)", () => {
    const s = new ExecStore();
    s.apply(snap("exec.orders", [order("ET1"), order("ET2")]));
    s.apply(delta("exec.orders", order("ET1", { status: "FILLED", executedQty: 10, leavesQty: 0 }), "ET1"));
    const views = s.orders();
    expect(views).toHaveLength(2);
    expect(views.find((v) => v.order.id === "ET1")?.order.status).toBe("FILLED");
  });
  it("positions + status full-replace", () => {
    const s = new ExecStore();
    s.apply(snap("exec.positions", [{ venue: "alpaca-paper", symbol: "US.AAPL", qty: 5, avgPrice: 3, unrealizedPnl: 1 }]));
    s.apply(delta("exec.positions", [])); // full replace to empty
    expect(s.positions()).toHaveLength(0);
    s.apply(snap("exec.status", status));
    expect(s.status()?.masterArmed).toBe(true);
  });
  it("optimistic row appears then reconciles when the real order event lands", () => {
    const s = new ExecStore();
    s.addOptimistic({ args: { venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0 }, id: "ET9", createdMs: 100 });
    expect(s.orders()).toHaveLength(1);
    expect(s.orders()[0].optimistic).toBe(true);
    s.apply(delta("exec.orders", order("ET9", { status: "SUBMITTED" }), "ET9"));
    expect(s.orders()).toHaveLength(1);          // reconciled, not doubled
    expect(s.orders()[0].optimistic).toBe(false);
  });
  it("workingOrdersFor filters by symbol and working status", () => {
    const s = new ExecStore();
    s.apply(snap("exec.orders", [order("ET1", { status: "ACCEPTED" }), order("ET2", { status: "FILLED" }), order("ET3", { symbol: "US.NVDA", status: "SUBMITTED" })]));
    expect(s.workingOrdersFor("US.AAPL").map((o) => o.id)).toEqual(["ET1"]);
    expect(s.workingOrdersFor().map((o) => o.id).sort()).toEqual(["ET1", "ET3"]);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/data/ExecStore.test.ts`
Expected: FAIL — `accounts is not a function` (the old `ExecStore` exposes only untyped `account/positions/orders`).

- [ ] **Step 3: Implement the typed ExecStore**

Replace `ui/src/data/ExecStore.ts`:

```ts
import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, Order, PositionRow, AccountRow, ExecStatus, SubmitOrderArgs } from "../wire/contract";
import { isWorking } from "../wire/orderStatus";

export interface OptimisticOrder { args: SubmitOrderArgs; id: string; createdMs: number }
export interface OrderView { order: Order; optimistic: boolean }

interface ExecState {
  accounts: Map<string, AccountRow>;
  positions: PositionRow[];
  orders: Map<string, Order>;
  optimistic: Map<string, OptimisticOrder>;
  status: ExecStatus | null;
}

function synthOptimistic(o: OptimisticOrder): Order {
  const a = o.args;
  return {
    venue: a.venue, id: o.id, symbol: a.symbol, side: a.side, type: a.type, tif: a.tif,
    qty: a.qty, limitPrice: a.limitPrice, stopPrice: a.stopPrice,
    status: "SUBMITTED", executedQty: 0, leavesQty: a.qty, avgFillPrice: 0,
    rejectReason: "", replacesId: "", createdMs: o.createdMs, updatedMs: o.createdMs,
  };
}

export class ExecStore extends ReactStore<ExecState> {
  constructor() { super({ accounts: new Map(), positions: [], orders: new Map(), optimistic: new Map(), status: null }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    switch (m.topic) {
      case "exec.account": {
        const row = m.payload as AccountRow;
        const accounts = new Map(cur.accounts); accounts.set(row.venue, row);
        this.set({ ...cur, accounts });
        return;
      }
      case "exec.positions":
        this.set({ ...cur, positions: m.payload as PositionRow[] }); // full replace (snapshot & delta)
        return;
      case "exec.orders": {
        const orders = new Map(cur.orders);
        const optimistic = new Map(cur.optimistic);
        const list = m.kind === "snapshot" ? (m.payload as Order[]) : [m.payload as Order];
        if (m.kind === "snapshot") orders.clear();
        for (const o of list) { orders.set(o.id, o); optimistic.delete(o.id); } // real event reconciles the optimistic row
        this.set({ ...cur, orders, optimistic });
        return;
      }
      case "exec.status":
        this.set({ ...cur, status: m.payload as ExecStatus }); // full replace
        return;
      default:
        return; // exec.fills is routed to FillStore (Task 14)
    }
  }

  addOptimistic(o: OptimisticOrder): void {
    const cur = this.getSnapshot();
    if (cur.orders.has(o.id)) return; // real order already arrived — no phantom
    const optimistic = new Map(cur.optimistic); optimistic.set(o.id, o);
    this.set({ ...cur, optimistic });
  }

  accounts(): AccountRow[] { return [...this.getSnapshot().accounts.values()]; }
  positions(): PositionRow[] { return this.getSnapshot().positions; }
  status(): ExecStatus | null { return this.getSnapshot().status; }

  // Real orders + not-yet-confirmed optimistic rows, newest first.
  orders(): OrderView[] {
    const cur = this.getSnapshot();
    const views: OrderView[] = [...cur.orders.values()].map((order) => ({ order, optimistic: false }));
    for (const o of cur.optimistic.values()) if (!cur.orders.has(o.id)) views.push({ order: synthOptimistic(o), optimistic: true });
    return views.sort((a, b) => b.order.createdMs - a.order.createdMs);
  }

  workingOrdersFor(symbol?: string): Order[] {
    return [...this.getSnapshot().orders.values()]
      .filter((o) => isWorking(o.status) && (symbol === undefined || o.symbol === symbol));
  }
}
```

- [ ] **Step 4: Run the ExecStore test to verify it passes**

Run: `cd ui && npx vitest run src/data/ExecStore.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Update `workingOrderMarks` to the typed Order**

In `ui/src/render/ladder/ladderState.ts`, replace the untyped `WORKING_STATUSES`/`workingOrderMarks` (and the `orders: unknown[]` field on `buildLadderState`) with the typed version. Change the import and the mapper:

```ts
import type { Book, BookLevel, TickDirection, Order } from "../../wire/contract";
import { isWorking, sideIsSell } from "../../wire/orderStatus";
```

```ts
/**
 * Display-only projection of working orders onto the ladder: an order marks the
 * ladder iff it names this symbol, is in a working state, and carries a positive
 * price at its relevant level (limit price for limit/stop-limit, stop price for
 * stop) and remaining quantity. Sell/Short → sell.
 */
export function workingOrderMarks(orders: Order[], symbol: string): OrderMark[] {
  const marks: OrderMark[] = [];
  for (const o of orders) {
    if (o.symbol !== symbol || !isWorking(o.status)) continue;
    const price = o.type === "STOP" ? o.stopPrice : o.limitPrice;
    if (!Number.isFinite(price) || price <= 0) continue;
    const qty = o.leavesQty > 0 ? o.leavesQty : o.qty;
    if (!Number.isFinite(qty) || qty <= 0) continue;
    marks.push({ price, side: sideIsSell(o.side) ? "sell" : "buy", qty });
  }
  return marks;
}
```

Change the `orders` field type on the `buildLadderState` args object and the `LadderPaintState`-building call from `orders: unknown[]` to `orders: Order[]`. (The `LadderPanel` already passes `stores.exec.getSnapshot().orders` — Task 3 of Plan 3; in Task 10 below we switch that to `stores.exec.workingOrdersFor(symbol)`, but for now keep `buildLadderState` accepting `Order[]`.)

Update the `buildLadderState` signature field:

```ts
export function buildLadderState(args: {
  symbol: string;
  book: Book | undefined;
  orders: Order[];
  flash: TradeFlash | null;
  last: LastTrade | null;
  nowMs: number;
  width: number;
  height: number;
  palette: Palette;
}): LadderPaintState {
```

- [ ] **Step 6: Update the ladderState test's working-orders block**

In `ui/src/render/ladder/ladderState.test.ts`, update the `describe("workingOrderMarks ...")` block to build typed `Order` objects instead of untyped rows. Replace that block with:

```ts
import type { Order } from "../../wire/contract";

const ord = (over: Partial<Order>): Order => ({
  venue: "v", id: "1", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY",
  qty: 100, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 100,
  avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, ...over,
});

describe("workingOrderMarks (typed Order, Plan 5)", () => {
  it("marks working limit orders for this symbol; sell/short → sell", () => {
    const marks = workingOrderMarks(
      [ord({ id: "1", side: "BUY", limitPrice: 3.5 }),
       ord({ id: "2", side: "SELL", limitPrice: 3.6 }),
       ord({ id: "3", side: "SHORT", limitPrice: 3.7 })],
      "US.AAPL");
    expect(marks).toEqual([
      { price: 3.5, side: "buy", qty: 100 },
      { price: 3.6, side: "sell", qty: 100 },
      { price: 3.7, side: "sell", qty: 100 },
    ]);
  });
  it("excludes filled/terminal, other symbols, and uses stop price for STOP", () => {
    expect(workingOrderMarks([ord({ status: "FILLED" })], "US.AAPL")).toEqual([]);
    expect(workingOrderMarks([ord({ symbol: "US.NVDA" })], "US.AAPL")).toEqual([]);
    expect(workingOrderMarks([ord({ type: "STOP", stopPrice: 3.0, limitPrice: 0, leavesQty: 50 })], "US.AAPL"))
      .toEqual([{ price: 3.0, side: "buy", qty: 50 }]);
  });
});
```

- [ ] **Step 7: Run the two unit tests (typecheck deferred to Step 10)**

Run: `cd ui && npx vitest run src/render/ladder/ladderState.test.ts src/data/ExecStore.test.ts`
Expected: PASS. (Do NOT run `typecheck` yet — `LadderPanel.tsx` and the ladder golden test still feed the old order shapes into the retyped `buildLadderState`, so they must be updated first, in Steps 8–9.)

- [ ] **Step 8: Fix LadderPanel's order source (typed, link-following)**

In `ui/src/chrome/panels/LadderPanel.tsx`, the current effect holds `let orders: unknown[] = stores.exec.getSnapshot().orders;` and reassigns it in an `exec.subscribe` callback (both now type-broken, since `ExecState.orders` is a `Map`). Replace that pair of lines:

```tsx
    let orders: unknown[] = stores.exec.getSnapshot().orders;
    const offExec = stores.exec.subscribe(() => {
      orders = stores.exec.getSnapshot().orders;
      forceRef.current++;
    });
```

with a subscription that only bumps the repaint force (the typed working orders are read fresh at paint time):

```tsx
    const offExec = stores.exec.subscribe(() => { forceRef.current++; });
```

and change the `orders` field in the `buildLadderState({ … })` paint call from `orders,` to the typed accessor using the **closure `symbol`** (which already follows the link group — not `config.settings.symbol`, which is the static seed and would go stale on a link change):

```tsx
          orders: stores.exec.workingOrdersFor(symbol),
```

(The `offExec()` teardown in the effect's cleanup is unchanged.)

- [ ] **Step 9: Retype the ladder golden fixture**

`ui/test/golden/ladder.golden.test.ts` is the file that actually feeds `buildLadderState` (the `fixtures/ladder-tape.json` file is only the mock-engine dev-replay input, consumed by no test). Update it so it still produces the **identical** `OrderMark[]` (`{price:3.47,side:"buy",qty:100}`, `{price:3.53,side:"sell",qty:50}`) and the goldens don't move. Add the `Order` import, retype `base.orders`, and rewrite `workingOrders` as typed orders:

```ts
import type { Book, Order } from "../../src/wire/contract";
```
```ts
const ord = (o: Partial<Order>): Order => ({
  venue: "alpaca-paper", id: "x", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY",
  qty: 100, limitPrice: 0, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 0,
  avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 0, updatedMs: 0, ...o,
});
const workingOrders: Order[] = [
  ord({ id: "o1", side: "BUY", limitPrice: 3.47, qty: 100, leavesQty: 100, status: "ACCEPTED" }),
  ord({ id: "o2", side: "SHORT", limitPrice: 3.53, qty: 80, leavesQty: 50, status: "PARTIALLY_FILLED" }),
];
```

and change the shared `base` object's cast from `orders: [] as unknown[]` to `orders: [] as Order[]`.

- [ ] **Step 10: Typecheck + full test + goldens**

Run: `cd ui && npm run typecheck && npx vitest run src/data/ExecStore.test.ts src/render/ladder/ladderState.test.ts && npm run test:golden`
Expected: all PASS — the retyped mapper produces the same marks, so `ladder-full-{light,dark}` goldens match byte-for-byte (if they don't, the fixture drifted; do not update the PNGs — fix the fixture).

- [ ] **Step 11: Commit**

```bash
cd ui && git add src/data/ExecStore.ts src/data/ExecStore.test.ts src/render/ladder/ladderState.ts src/render/ladder/ladderState.test.ts src/chrome/panels/LadderPanel.tsx test/golden/ladder.golden.test.ts
git commit -m "feat(ui/exec): typed ExecStore (keyed upserts + optimistic rows); typed ladder order marks"
```

(Optionally also update `fixtures/ladder-tape.json`'s `exec.orders` payload to the typed `Order` shape so the dev-replay ladder still shows marks — not required for any test.)

---

## Task 3: Sizing resolution (pure)

**Files:**
- Create: `ui/src/chrome/exec/sizing.ts`
- Test: `ui/src/chrome/exec/sizing.test.ts`

**Interfaces:**
- Produces: `type SizingMode = "Dollar" | "BuyingPowerPct" | "Shares" | "PositionFraction"`, `interface SizingSpec`, `interface SizingContext`, `resolveShares(spec, ctx): number`.

- [ ] **Step 1: Write the failing test**

Create `ui/src/chrome/exec/sizing.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { resolveShares, type SizingSpec } from "./sizing";

const ctx = { price: 3.5, buyingPower: 10_000, positionQty: 428 };

describe("resolveShares", () => {
  it("Dollar → floor($/price)", () => {
    expect(resolveShares({ mode: "Dollar", dollar: 5000 }, ctx)).toBe(1428); // floor(5000/3.5)
  });
  it("BuyingPowerPct → floor(BP*pct%/price)", () => {
    expect(resolveShares({ mode: "BuyingPowerPct", pct: 50 }, ctx)).toBe(1428); // floor(5000/3.5)
  });
  it("Shares → explicit floor, never negative", () => {
    expect(resolveShares({ mode: "Shares", shares: 300 }, ctx)).toBe(300);
    expect(resolveShares({ mode: "Shares", shares: -5 }, ctx)).toBe(0);
  });
  it("PositionFraction all/half of |held|", () => {
    expect(resolveShares({ mode: "PositionFraction", fraction: "all" }, ctx)).toBe(428);
    expect(resolveShares({ mode: "PositionFraction", fraction: "half" }, ctx)).toBe(214);
    expect(resolveShares({ mode: "PositionFraction", fraction: "all" }, { ...ctx, positionQty: -100 })).toBe(100);
  });
  it("guards a zero/negative price (no division blowup)", () => {
    expect(resolveShares({ mode: "Dollar", dollar: 5000 }, { ...ctx, price: 0 })).toBe(0);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/exec/sizing.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

Create `ui/src/chrome/exec/sizing.ts`:

```ts
// Pure sizing resolution — resolves at trigger time against a live quote/account.
// (ui-design §Order entry: Dollar → floor($/price); BuyingPowerPct → floor(BP×pct/price);
//  PositionFraction → from the live position.)
export type SizingMode = "Dollar" | "BuyingPowerPct" | "Shares" | "PositionFraction";
export interface SizingSpec {
  mode: SizingMode;
  dollar?: number;                 // Dollar
  pct?: number;                    // BuyingPowerPct (0–100)
  shares?: number;                 // Shares
  fraction?: "all" | "half";       // PositionFraction
}
export interface SizingContext { price: number; buyingPower: number; positionQty: number }

export function resolveShares(spec: SizingSpec, ctx: SizingContext): number {
  switch (spec.mode) {
    case "Dollar":
      return ctx.price > 0 ? Math.floor((spec.dollar ?? 0) / ctx.price) : 0;
    case "BuyingPowerPct":
      return ctx.price > 0 ? Math.floor((ctx.buyingPower * (spec.pct ?? 0) / 100) / ctx.price) : 0;
    case "Shares":
      return Math.max(0, Math.floor(spec.shares ?? 0));
    case "PositionFraction": {
      const held = Math.abs(ctx.positionQty);
      return spec.fraction === "half" ? Math.floor(held / 2) : Math.floor(held);
    }
  }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/exec/sizing.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/exec/sizing.ts src/chrome/exec/sizing.test.ts
git commit -m "feat(ui/exec): pure sizing resolution (dollar/BP%/shares/position-fraction)"
```

---

## Task 4: Price-source resolution + client pre-checks (pure)

**Files:**
- Create: `ui/src/chrome/exec/priceSource.ts`
- Create: `ui/src/chrome/exec/preChecks.ts`
- Modify: `ui/src/render/chart/sessions.ts` (export `sessionAt`)
- Test: `ui/src/chrome/exec/priceSource.test.ts`
- Test: `ui/src/chrome/exec/preChecks.test.ts`

**Interfaces:**
- Consumes: `Quote`, `Side`, `OrderType`, `TIF` (contract.ts); `sessionAt` (sessions.ts).
- Produces: `type PriceSource = "Bid" | "Ask" | "Last" | "Mid"`, `resolvePrice(source, offset, quote): number`; `interface DraftOrder`, `interface PreCheckResult`, `preCheck(draft, last, nowMs): PreCheckResult`.

- [ ] **Step 1: Export `sessionAt` from sessions.ts**

In `ui/src/render/chart/sessions.ts`, change `function sessionAt` to `export function sessionAt` (it is currently module-private). No other change.

- [ ] **Step 2: Write the failing tests**

Create `ui/src/chrome/exec/priceSource.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { resolvePrice } from "./priceSource";
import type { Quote } from "../../wire/contract";

const q: Quote = { symbol: "US.AAPL", bid: 3.40, ask: 3.50, last: 3.45, ts: "" };

describe("resolvePrice", () => {
  it("resolves each source and applies the signed offset", () => {
    expect(resolvePrice("Bid", 0, q)).toBeCloseTo(3.40);
    expect(resolvePrice("Ask", 0, q)).toBeCloseTo(3.50);
    expect(resolvePrice("Last", 0, q)).toBeCloseTo(3.45);
    expect(resolvePrice("Mid", 0, q)).toBeCloseTo(3.45);
    expect(resolvePrice("Ask", 0.02, q)).toBeCloseTo(3.52);
    expect(resolvePrice("Bid", -0.01, q)).toBeCloseTo(3.39);
  });
});
```

Create `ui/src/chrome/exec/preChecks.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { preCheck, type DraftOrder } from "./preChecks";

// ET: 2026-07-06 is a Monday. 14:00 UTC = 10:00 ET (RTH). 08:00 UTC = 04:00 ET (pre).
const RTH = Date.parse("2026-07-06T14:00:00Z");
const PRE = Date.parse("2026-07-06T08:00:00Z");
const draft = (o: Partial<DraftOrder>): DraftOrder =>
  ({ symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0, ...o });

describe("preCheck", () => {
  it("blocks non-positive quantity", () => {
    const r = preCheck(draft({ qty: 0 }), 3.5, RTH);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/greater than 0/);
  });
  it("passes a clean RTH limit", () => {
    expect(preCheck(draft({}), 3.5, RTH).ok).toBe(true);
  });
  it("coerces Market outside RTH to Limit-at-last with a notice", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), 3.44, PRE);
    expect(r.ok).toBe(true);
    expect(r.order.type).toBe("LIMIT");
    expect(r.order.limitPrice).toBeCloseTo(3.44);
    expect(r.notices.join(" ")).toMatch(/coerced to Limit/);
  });
  it("leaves a Market during RTH alone", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), 3.44, RTH);
    expect(r.order.type).toBe("MARKET");
    expect(r.notices).toHaveLength(0);
  });
  it("rejects an inverted buy stop-limit (limit below stop)", () => {
    const r = preCheck(draft({ type: "STOP_LIMIT", side: "BUY", stopPrice: 3.6, limitPrice: 3.5 }), 3.5, RTH);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/Inverted buy stop-limit/);
  });
  it("rejects an inverted sell stop-limit (limit above stop)", () => {
    const r = preCheck(draft({ type: "STOP_LIMIT", side: "SELL", stopPrice: 3.4, limitPrice: 3.5 }), 3.5, RTH);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/Inverted sell stop-limit/);
  });
  it("accepts a coherent sell stop-limit", () => {
    expect(preCheck(draft({ type: "STOP_LIMIT", side: "SELL", stopPrice: 3.5, limitPrice: 3.4 }), 3.5, RTH).ok).toBe(true);
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd ui && npx vitest run src/chrome/exec/priceSource.test.ts src/chrome/exec/preChecks.test.ts`
Expected: FAIL — modules not found.

- [ ] **Step 4: Implement `priceSource.ts`**

Create `ui/src/chrome/exec/priceSource.ts`:

```ts
import type { Quote } from "../../wire/contract";

export type PriceSource = "Bid" | "Ask" | "Last" | "Mid";

// base = the chosen quote leg; the template's signed offset is added on top
// (ui-design §Order entry: "price: Bid|Ask|Last|Mid ± offset").
export function resolvePrice(source: PriceSource, offset: number, quote: Quote): number {
  const base =
    source === "Bid" ? quote.bid :
    source === "Ask" ? quote.ask :
    source === "Last" ? quote.last :
    (quote.bid + quote.ask) / 2;
  return base + offset;
}
```

- [ ] **Step 5: Implement `preChecks.ts`**

Create `ui/src/chrome/exec/preChecks.ts`:

```ts
// Client-side pre-checks before the wire (ui-design §Trigger flow step 2):
//   qty > 0; stop/stop-limit price coherence (TZ does not validate — inverted
//   stop-limits sit unfilled); Market outside RTH auto-coerced to Limit-at-last
//   + a visible notice (avoids TZ R78). Pure; nowMs decides the ET session.
import type { OrderType, Side, TIF } from "../../wire/contract";
import { sessionAt } from "../../render/chart/sessions";

export interface DraftOrder {
  symbol: string; side: Side; type: OrderType; tif: TIF;
  qty: number; limitPrice: number; stopPrice: number;
}
export interface PreCheckResult {
  ok: boolean;
  order: DraftOrder;    // possibly coerced (Market→Limit outside RTH)
  errors: string[];     // blocking
  notices: string[];    // non-blocking (coercions applied)
}

export function preCheck(draft: DraftOrder, last: number, nowMs: number): PreCheckResult {
  const errors: string[] = [];
  const notices: string[] = [];
  let order: DraftOrder = { ...draft };

  if (!(order.qty > 0)) errors.push("Quantity must be greater than 0.");

  if (order.type === "MARKET" && sessionAt(nowMs) !== "rth") {
    if (last > 0) { order = { ...order, type: "LIMIT", limitPrice: last }; notices.push(`Market outside RTH coerced to Limit @ ${last.toFixed(2)}.`); }
    else errors.push("Market order outside RTH and no last price to coerce to.");
  }

  if (order.type === "STOP" && !(order.stopPrice > 0)) errors.push("Stop price must be greater than 0.");
  if (order.type === "LIMIT" && !(order.limitPrice > 0)) errors.push("Limit price must be greater than 0.");
  if (order.type === "STOP_LIMIT") {
    if (!(order.stopPrice > 0)) errors.push("Stop price must be greater than 0.");
    if (!(order.limitPrice > 0)) errors.push("Limit price must be greater than 0.");
    if (order.stopPrice > 0 && order.limitPrice > 0) {
      const buyish = order.side === "BUY" || order.side === "COVER";
      if (buyish && order.limitPrice < order.stopPrice) errors.push("Inverted buy stop-limit: limit is below stop (would sit unfilled).");
      if (!buyish && order.limitPrice > order.stopPrice) errors.push("Inverted sell stop-limit: limit is above stop (would sit unfilled).");
    }
  }

  return { ok: errors.length === 0, order, errors, notices };
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd ui && npx vitest run src/chrome/exec/priceSource.test.ts src/chrome/exec/preChecks.test.ts src/render/chart/sessions.test.ts`
Expected: PASS (existing sessions tests still pass after the export-only change).

- [ ] **Step 7: Commit**

```bash
cd ui && git add src/chrome/exec/priceSource.ts src/chrome/exec/priceSource.test.ts src/chrome/exec/preChecks.ts src/chrome/exec/preChecks.test.ts src/render/chart/sessions.ts
git commit -m "feat(ui/exec): price-source resolution + client pre-checks (stop coherence, RTH coercion)"
```

---

## Task 5: Action templates + template resolution (pure)

**Files:**
- Create: `ui/src/chrome/exec/actionTemplate.ts`
- Create: `ui/src/chrome/exec/resolveTemplate.ts`
- Test: `ui/src/chrome/exec/actionTemplate.test.ts`
- Test: `ui/src/chrome/exec/resolveTemplate.test.ts`

**Interfaces:**
- Consumes: `Side`, `OrderType`, `TIF`, `Quote`, `VenueID`, `SubmitOrderArgs` (contract.ts); `resolveShares`/`SizingSpec` (sizing.ts); `resolvePrice`/`PriceSource` (priceSource.ts); `preCheck`/`PreCheckResult` (preChecks.ts); `sideLabel`/`bareSymbol`/`abbrevType` (orderStatus.ts).
- Produces: `interface PlaceOrderTemplate`, `type ManagementAction`, `interface ManagementTemplate`, `type ActionTemplate`, `DEFAULT_TEMPLATES`, `interface OrderConfig`, `DEFAULT_ORDER_CONFIG`, `ORDER_CONFIG_KEY`; `interface ResolveContext`, `interface ResolvedPlace`, `resolvePlaceTemplate(t, ctx): ResolvedPlace`.

- [ ] **Step 1: Write the failing test for templates + config**

Create `ui/src/chrome/exec/actionTemplate.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { DEFAULT_TEMPLATES, DEFAULT_ORDER_CONFIG, ORDER_CONFIG_KEY, type ActionTemplate } from "./actionTemplate";

describe("action templates", () => {
  it("ships defaults with unique ids and a mix of place + manage kinds", () => {
    const ids = DEFAULT_TEMPLATES.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(DEFAULT_TEMPLATES.some((t) => t.kind === "place")).toBe(true);
    expect(DEFAULT_TEMPLATES.some((t) => t.kind === "manage")).toBe(true);
  });
  it("every place default carries a complete sizing + price recipe", () => {
    for (const t of DEFAULT_TEMPLATES.filter((t): t is Extract<ActionTemplate, { kind: "place" }> => t.kind === "place")) {
      expect(t.sizing.mode).toBeTruthy();
      expect(["Bid", "Ask", "Last", "Mid"]).toContain(t.priceSource);
    }
  });
  it("default order config wraps templates + an empty active venue", () => {
    expect(ORDER_CONFIG_KEY).toBe("orderConfig");
    expect(DEFAULT_ORDER_CONFIG.templates).toEqual(DEFAULT_TEMPLATES);
    expect(DEFAULT_ORDER_CONFIG.activeVenue).toBe("");
  });
});
```

- [ ] **Step 2: Write the failing test for resolution**

Create `ui/src/chrome/exec/resolveTemplate.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { resolvePlaceTemplate } from "./resolveTemplate";
import type { PlaceOrderTemplate } from "./actionTemplate";
import type { Quote } from "../../wire/contract";

const RTH = Date.parse("2026-07-06T14:00:00Z");
const q: Quote = { symbol: "US.AAPL", bid: 3.49, ask: 3.50, last: 3.50, ts: "" };
const tmpl = (o: Partial<PlaceOrderTemplate> = {}): PlaceOrderTemplate => ({
  kind: "place", id: "p1", label: "Buy $5k", side: "BUY", type: "LIMIT", tif: "DAY",
  priceSource: "Ask", priceOffset: 0, sizing: { mode: "Dollar", dollar: 5000 }, ...o,
});

describe("resolvePlaceTemplate", () => {
  it("resolves price+qty and builds a venue-tagged SubmitOrderArgs + flash string", () => {
    const r = resolvePlaceTemplate(tmpl(), { venue: "alpaca-paper", symbol: "US.AAPL", quote: q, buyingPower: 10_000, positionQty: 0, nowMs: RTH });
    expect(r.args.venue).toBe("alpaca-paper");
    expect(r.args.qty).toBe(1428);          // floor(5000/3.50)
    expect(r.args.limitPrice).toBeCloseTo(3.50);
    expect(r.preCheck.ok).toBe(true);
    expect(r.flash).toBe("BUY 1,428 AAPL @ 3.50 LMT");
  });
  it("PositionFraction=all resolves from the live position (flatten)", () => {
    const r = resolvePlaceTemplate(
      tmpl({ side: "SELL", sizing: { mode: "PositionFraction", fraction: "all" } }),
      { venue: "alpaca-paper", symbol: "US.AAPL", quote: q, buyingPower: 0, positionQty: 300, nowMs: RTH });
    expect(r.args.qty).toBe(300);
    expect(r.args.side).toBe("SELL");
  });
  it("surfaces pre-check failure without throwing (qty 0 → not ok)", () => {
    const r = resolvePlaceTemplate(
      tmpl({ sizing: { mode: "Dollar", dollar: 1 } }),
      { venue: "alpaca-paper", symbol: "US.AAPL", quote: { ...q, ask: 100 }, buyingPower: 0, positionQty: 0, nowMs: RTH });
    expect(r.args.qty).toBe(0);
    expect(r.preCheck.ok).toBe(false);
  });
  it("MARKET keeps limitPrice 0 in args and flashes MKT", () => {
    const r = resolvePlaceTemplate(tmpl({ type: "MARKET" }), { venue: "v", symbol: "US.AAPL", quote: q, buyingPower: 10_000, positionQty: 0, nowMs: RTH });
    expect(r.args.type).toBe("MARKET");
    expect(r.args.limitPrice).toBe(0);
    expect(r.flash).toContain("MKT");
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd ui && npx vitest run src/chrome/exec/actionTemplate.test.ts src/chrome/exec/resolveTemplate.test.ts`
Expected: FAIL — modules not found.

- [ ] **Step 4: Implement `actionTemplate.ts`**

Create `ui/src/chrome/exec/actionTemplate.ts`:

```ts
// Action templates: one saved recipe, two triggers (a hotkey binding and a ticket
// preset button), edited in one settings screen and stored engine-side under the
// config key `orderConfig`. (ui-design §Order entry & hotkeys.)
import type { Side, OrderType, TIF, VenueID } from "../../wire/contract";
import type { SizingSpec } from "./sizing";
import type { PriceSource } from "./priceSource";

export interface PlaceOrderTemplate {
  kind: "place";
  id: string; label: string;
  side: Side; type: OrderType; tif: TIF;
  priceSource: PriceSource; priceOffset: number;
  sizing: SizingSpec;
  hotkey?: string;   // normalized combo, e.g. "Ctrl+1" (see hotkeys.ts)
}
export type ManagementAction = "CancelLast" | "CancelAllFocused" | "CancelAllEverything" | "KillSwitch";
export interface ManagementTemplate { kind: "manage"; id: string; label: string; action: ManagementAction; hotkey?: string }
export type ActionTemplate = PlaceOrderTemplate | ManagementTemplate;

// The whole editable order-entry config; persisted as one blob (fewer round-trips).
export interface OrderConfig { templates: ActionTemplate[]; activeVenue: VenueID }
export const ORDER_CONFIG_KEY = "orderConfig";

export const DEFAULT_TEMPLATES: ActionTemplate[] = [
  { kind: "place", id: "buy-5k", label: "Buy $5k", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Dollar", dollar: 5000 }, hotkey: "Ctrl+1" },
  { kind: "place", id: "buy-25pct", label: "Buy 25% BP", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "BuyingPowerPct", pct: 25 }, hotkey: "Ctrl+2" },
  { kind: "place", id: "sell-half", label: "Sell ½", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", fraction: "half" }, hotkey: "Ctrl+3" },
  { kind: "place", id: "flatten", label: "Flatten", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", fraction: "all" }, hotkey: "Ctrl+4" },
  { kind: "manage", id: "cancel-last", label: "Cancel Last", action: "CancelLast", hotkey: "Ctrl+Backspace" },
  { kind: "manage", id: "cancel-all", label: "Cancel All (focused)", action: "CancelAllFocused", hotkey: "Ctrl+Shift+Backspace" },
  { kind: "manage", id: "kill", label: "KILL", action: "KillSwitch", hotkey: "Ctrl+Shift+K" },
];

export const DEFAULT_ORDER_CONFIG: OrderConfig = { templates: DEFAULT_TEMPLATES, activeVenue: "" };
```

- [ ] **Step 5: Implement `resolveTemplate.ts`**

Create `ui/src/chrome/exec/resolveTemplate.ts`:

```ts
// Resolve a PlaceOrderTemplate against a live quote/account/position into a concrete
// venue-tagged SubmitOrderArgs + a human-readable flash string ("BUY 1,428 AAPL @ 3.50 LMT")
// + the pre-check result. Pure; nowMs decides the ET session for RTH coercion.
import type { Quote, SubmitOrderArgs, VenueID } from "../../wire/contract";
import type { PlaceOrderTemplate } from "./actionTemplate";
import { resolveShares } from "./sizing";
import { resolvePrice } from "./priceSource";
import { preCheck, type PreCheckResult, type DraftOrder } from "./preChecks";
import { sideLabel, bareSymbol, abbrevType } from "./orderStatus";

export interface ResolveContext {
  venue: VenueID; symbol: string; quote: Quote;
  buyingPower: number; positionQty: number; nowMs: number;
}
export interface ResolvedPlace { args: SubmitOrderArgs; flash: string; preCheck: PreCheckResult }

export function resolvePlaceTemplate(t: PlaceOrderTemplate, ctx: ResolveContext): ResolvedPlace {
  const price = resolvePrice(t.priceSource, t.priceOffset, ctx.quote);
  const qty = resolveShares(t.sizing, { price, buyingPower: ctx.buyingPower, positionQty: ctx.positionQty });
  const draft: DraftOrder = {
    symbol: ctx.symbol, side: t.side, type: t.type, tif: t.tif, qty,
    limitPrice: t.type === "MARKET" ? 0 : price,
    stopPrice: t.type === "STOP" || t.type === "STOP_LIMIT" ? price : 0,
  };
  const pc = preCheck(draft, ctx.quote.last, ctx.nowMs);
  const o = pc.order;
  const args: SubmitOrderArgs = {
    venue: ctx.venue, symbol: ctx.symbol, side: o.side, type: o.type, tif: o.tif,
    qty: o.qty, limitPrice: o.limitPrice, stopPrice: o.stopPrice,
  };
  const tail = o.type === "MARKET" ? "MKT" : `${o.limitPrice.toFixed(2)} ${abbrevType(o.type)}`;
  const flash = `${sideLabel(o.side)} ${o.qty.toLocaleString("en-US")} ${bareSymbol(ctx.symbol)} @ ${tail}`;
  return { args, flash, preCheck: pc };
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd ui && npx vitest run src/chrome/exec/actionTemplate.test.ts src/chrome/exec/resolveTemplate.test.ts`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd ui && git add src/chrome/exec/actionTemplate.ts src/chrome/exec/actionTemplate.test.ts src/chrome/exec/resolveTemplate.ts src/chrome/exec/resolveTemplate.test.ts
git commit -m "feat(ui/exec): action templates + template resolution (concrete order + flash string)"
```

---

## Task 6: Toast / notification infrastructure

No toast system exists yet. Blocked/rejected orders, resolved-order flashes, and disarmed flashes all surface here.

**Files:**
- Create: `ui/src/chrome/Toast.tsx`
- Test: `ui/src/chrome/Toast.test.tsx`

**Interfaces:**
- Produces: `type ToastLevel = "info" | "success" | "warn" | "danger"`, `interface ToastApi { push(t: { level: ToastLevel; text: string; sticky?: boolean }): void; dismiss(id: number): void }`, `ToastProvider({ now?, autoDismissMs?, children })`, `useToasts(): ToastApi`, `ToastHost` (rendered by the provider).

- [ ] **Step 1: Write the failing test**

Create `ui/src/chrome/Toast.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { ToastProvider, useToasts } from "./Toast";

function Raiser({ onApi }: { onApi: (api: ReturnType<typeof useToasts>) => void }) {
  const api = useToasts();
  onApi(api);
  return null;
}

function setup() {
  let api!: ReturnType<typeof useToasts>;
  render(
    <ThemeProvider>
      <ToastProvider autoDismissMs={4000}>
        <Raiser onApi={(a) => (api = a)} />
      </ToastProvider>
    </ThemeProvider>,
  );
  return () => api;
}

describe("Toast", () => {
  it("renders a pushed toast with its verbatim text", () => {
    const api = setup();
    act(() => api().push({ level: "danger", text: "Blocked: venue disarmed" }));
    expect(screen.getByText("Blocked: venue disarmed")).toBeTruthy();
  });
  it("auto-dismisses a non-sticky toast after the interval; sticky stays", () => {
    vi.useFakeTimers();
    const api = setup();
    act(() => { api().push({ level: "info", text: "flash-order" }); api().push({ level: "danger", text: "stay", sticky: true }); });
    act(() => vi.advanceTimersByTime(4001));
    expect(screen.queryByText("flash-order")).toBeNull();
    expect(screen.getByText("stay")).toBeTruthy();
    vi.useRealTimers();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/Toast.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `Toast.tsx`**

Create `ui/src/chrome/Toast.tsx`:

```tsx
import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";
import { useTheme } from "./ThemeProvider";

export type ToastLevel = "info" | "success" | "warn" | "danger";
export interface Toast { id: number; level: ToastLevel; text: string; sticky?: boolean }
export interface ToastApi { push(t: Omit<Toast, "id">): void; dismiss(id: number): void }

const Ctx = createContext<ToastApi | null>(null);

export function ToastProvider(
  { children, autoDismissMs = 4000 }: { children: ReactNode; autoDismissMs?: number },
): JSX.Element {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const seq = useRef(0);

  const dismiss = useCallback((id: number) => setToasts((ts) => ts.filter((t) => t.id !== id)), []);
  const push = useCallback((t: Omit<Toast, "id">) => {
    const id = ++seq.current;   // monotonic per-provider id
    setToasts((ts) => [...ts, { ...t, id }]);
    if (!t.sticky) setTimeout(() => dismiss(id), autoDismissMs);
  }, [autoDismissMs, dismiss]);

  const api = useMemo<ToastApi>(() => ({ push, dismiss }), [push, dismiss]);
  return <Ctx.Provider value={api}><>{children}<ToastHost toasts={toasts} onDismiss={dismiss} /></></Ctx.Provider>;
}

export function useToasts(): ToastApi {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useToasts must be used within a ToastProvider");
  return ctx;
}

function ToastHost({ toasts, onDismiss }: { toasts: Toast[]; onDismiss: (id: number) => void }): JSX.Element {
  const { palette } = useTheme();
  const color = (l: ToastLevel) => (l === "danger" ? palette.danger : l === "warn" ? palette.warn : l === "success" ? palette.ok : palette.accent);
  return (
    <div style={{ position: "fixed", right: 12, bottom: 12, display: "flex", flexDirection: "column", gap: 6, zIndex: 9999, maxWidth: 380 }}>
      {toasts.map((t) => (
        <div key={t.id} role="alert" onClick={() => onDismiss(t.id)}
          style={{ background: palette.surface, border: `1px solid ${color(t.level)}`, borderLeft: `4px solid ${color(t.level)}`,
            color: palette.text, padding: "6px 10px", fontSize: 12, borderRadius: 4, cursor: "pointer", boxShadow: "0 2px 8px rgba(0,0,0,.25)" }}>
          {t.text}
        </div>
      ))}
    </div>
  );
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/Toast.test.tsx`
Expected: PASS.

- [ ] **Step 5: Mount `ToastProvider` app-wide**

The exec panels (Tasks 9–12) call `useToasts()`, so a provider must wrap `AppShell` at runtime. In `ui/src/App.tsx`, add the import and wrap inside `ThemeProvider` (so `ToastHost` can `useTheme`):

```tsx
import { ToastProvider } from "./chrome/Toast";
```
```tsx
  return (
    <ThemeProvider commands={commands}>
      <ToastProvider>
        <ReconnectOverlay state={state}>
          <AppShell workspaceName={workspaceName} stores={stores} scheduler={scheduler}
            workspaceStore={workspaceStore} linkGroups={linkGroups} commands={commands} />
        </ReconnectOverlay>
      </ToastProvider>
    </ThemeProvider>
  );
```

Run `cd ui && npm run typecheck` — expected PASS.

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/chrome/Toast.tsx src/chrome/Toast.test.tsx src/App.tsx
git commit -m "feat(ui/chrome): toast infrastructure (provider + host + useToasts), mounted app-wide"
```

---

## Task 7: Order command client (submit/cancel/replace/flatten/arm/disarm/kill)

**Files:**
- Create: `ui/src/chrome/exec/commands.ts`
- Test: `ui/src/chrome/exec/commands.test.ts`

**Interfaces:**
- Consumes: `AckMsg`, `SubmitOrderArgs`, `CancelOrderArgs`, `ReplaceOrderArgs`, `FlattenArgs`, `KillSwitchArgs`, `ArmArgs`, `VenueID` (contract.ts); `ExecStore` (data); `ToastApi` (Toast.tsx).
- Produces: `interface CommandAdapter { sendCommand(name: string, args: unknown): Promise<AckMsg> }`, `interface OrderCommandsDeps { cmd: CommandAdapter; exec: ExecStore; toast: ToastApi; now: () => number }`, `class OrderCommands` with `submit(args, flash)`, `cancel(venue, orderId)`, `replace(args)`, `flatten(venue)`, `arm(venue?)`, `disarm(venue?)`, `kill(venue?)`, `cancelLast(symbol?)`, `cancelAll(scope, symbol?)`.

Cancel-all/cancel-last are **UI-composed** from `CancelOrder` over `ExecStore.workingOrdersFor(...)` — no dedicated engine command; the engine's per-venue token buckets pace the burst (ui-design: "respect TZ rate limits via the engine's token buckets").

- [ ] **Step 1: Write the failing test**

Create `ui/src/chrome/exec/commands.test.ts`:

```ts
import { describe, it, expect, vi } from "vitest";
import { OrderCommands, type CommandAdapter } from "./commands";
import { ExecStore } from "../../data/ExecStore";
import type { AckMsg, Order, SubmitOrderArgs } from "../../wire/contract";

function fakes(ack: Partial<AckMsg> = {}) {
  const sent: Array<{ name: string; args: unknown }> = [];
  const cmd: CommandAdapter = { sendCommand: vi.fn(async (name, args) => { sent.push({ name, args }); return { kind: "ack", corrId: "c1", status: "accepted", ...ack } as AckMsg; }) };
  const exec = new ExecStore();
  const pushed: Array<{ level: string; text: string }> = [];
  const toast = { push: (t: { level: string; text: string }) => pushed.push(t), dismiss: () => {} };
  const oc = new OrderCommands({ cmd, exec, toast: toast as never, now: () => 100 });
  return { sent, cmd, exec, pushed, oc };
}
const args: SubmitOrderArgs = { venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0 };
const snap = (payload: Order[]) => ({ kind: "snapshot" as const, topic: "exec.orders" as never, payload });
const order = (id: string, over: Partial<Order> = {}): Order => ({ venue: "alpaca-paper", id, symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 10, avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, ...over });

describe("OrderCommands", () => {
  it("submit accepted → registers optimistic row + info flash", async () => {
    const { sent, exec, pushed, oc } = fakes({ orderId: "ET7" });
    await oc.submit(args, "BUY 10 AAPL @ 3.50 LMT");
    expect(sent[0]).toEqual({ name: "SubmitOrder", args });
    expect(exec.orders().find((v) => v.order.id === "ET7")?.optimistic).toBe(true);
    expect(pushed).toContainEqual({ level: "info", text: "BUY 10 AAPL @ 3.50 LMT" });
  });
  it("submit blocked → danger toast with verbatim reason, no optimistic row", async () => {
    const { exec, pushed, oc } = fakes({ status: "blocked", reason: "venue disarmed" });
    await oc.submit(args, "flash");
    expect(exec.orders()).toHaveLength(0);
    expect(pushed).toContainEqual({ level: "danger", text: "Blocked: venue disarmed" });
  });
  it("cancel / arm / disarm / kill send the right command + args", async () => {
    const { sent, oc } = fakes();
    await oc.cancel("alpaca-paper", "ET7");
    await oc.arm(); await oc.disarm("alpaca-paper"); await oc.kill();
    expect(sent.map((s) => s.name)).toEqual(["CancelOrder", "Arm", "Disarm", "KillSwitch"]);
    expect(sent[0].args).toEqual({ venue: "alpaca-paper", orderId: "ET7" });
    expect(sent[1].args).toEqual({});                       // Arm master (no venue)
    expect(sent[2].args).toEqual({ venue: "alpaca-paper" });
    expect(sent[3].args).toEqual({});                       // KillSwitch all
  });
  it("cancelLast cancels the newest working order; cancelAll(focused) cancels only that symbol's working orders", async () => {
    const { sent, exec, oc } = fakes();
    exec.apply(snap([order("ET1", { createdMs: 1 }), order("ET2", { createdMs: 2 }), order("ET3", { symbol: "US.NVDA", venue: "alpaca-paper", createdMs: 3 })]));
    await oc.cancelLast("US.AAPL");
    expect(sent.at(-1)?.args).toEqual({ venue: "alpaca-paper", orderId: "ET2" }); // newest AAPL working
    sent.length = 0;
    await oc.cancelAll("focused", "US.AAPL");
    expect(sent.map((s) => (s.args as { orderId: string }).orderId).sort()).toEqual(["ET1", "ET2"]);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/exec/commands.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `commands.ts`**

Create `ui/src/chrome/exec/commands.ts`:

```ts
// Typed order-command client. Every method wraps the correlated command adapter;
// submit registers the optimistic PendingNew row (keyed by the ack's orderId) and
// raises the flash/block toast. Cancel-all/last are composed from CancelOrder over
// the working set — the engine's token buckets pace the burst.
import type { AckMsg, SubmitOrderArgs, ReplaceOrderArgs, VenueID } from "../../wire/contract";
import type { ExecStore } from "../../data/ExecStore";
import type { ToastApi } from "../Toast";

export interface CommandAdapter { sendCommand(name: string, args: unknown): Promise<AckMsg> }
export interface OrderCommandsDeps { cmd: CommandAdapter; exec: ExecStore; toast: ToastApi; now: () => number }

export class OrderCommands {
  constructor(private readonly d: OrderCommandsDeps) {}

  async submit(args: SubmitOrderArgs, flash: string): Promise<void> {
    const ack = await this.d.cmd.sendCommand("SubmitOrder", args);
    if (ack.status === "blocked") { this.d.toast.push({ level: "danger", text: `Blocked: ${ack.reason ?? "unknown"}` }); return; }
    if (ack.orderId) this.d.exec.addOptimistic({ args, id: ack.orderId, createdMs: this.d.now() });
    this.d.toast.push({ level: "info", text: flash });
  }

  async cancel(venue: VenueID, orderId: string): Promise<void> { await this.d.cmd.sendCommand("CancelOrder", { venue, orderId }); }
  async replace(args: ReplaceOrderArgs): Promise<void> { await this.d.cmd.sendCommand("ReplaceOrder", args); }
  async flatten(venue: VenueID): Promise<void> { await this.d.cmd.sendCommand("Flatten", { venue }); }

  async arm(venue?: VenueID): Promise<void> { await this.d.cmd.sendCommand("Arm", venue ? { venue } : {}); }
  async disarm(venue?: VenueID): Promise<void> { await this.d.cmd.sendCommand("Disarm", venue ? { venue } : {}); }
  async kill(venue?: VenueID): Promise<void> { await this.d.cmd.sendCommand("KillSwitch", venue ? { venue } : {}); }

  async cancelLast(symbol?: string): Promise<void> {
    const working = this.d.exec.workingOrdersFor(symbol);
    if (working.length === 0) return;
    const last = working.reduce((a, b) => (b.createdMs > a.createdMs ? b : a));
    await this.cancel(last.venue, last.id);
  }

  async cancelAll(scope: "focused" | "everything", symbol?: string): Promise<void> {
    const working = this.d.exec.workingOrdersFor(scope === "focused" ? symbol : undefined);
    await Promise.all(working.map((o) => this.cancel(o.venue, o.id)));
  }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/exec/commands.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/exec/commands.ts src/chrome/exec/commands.test.ts
git commit -m "feat(ui/exec): typed order-command client (submit optimistic, cancel/replace/flatten/arm/kill)"
```

---

## Task 8: Shared exec hooks + widen the `commands` prop type

Widen `PanelProps.commands.sendCommand` to return `AckMsg` (panels need `orderId`/`reason`) and add the three hooks the exec panels/ticket/hotkeys reuse.

**Files:**
- Modify: `ui/src/chrome/panels/registry.tsx` (widen `PanelProps.commands` → `Promise<AckMsg>`)
- Create: `ui/src/chrome/exec/useOrderCommands.ts`
- Create: `ui/src/chrome/exec/useOrderConfig.tsx` (a shared **context provider** + hook — NOT per-call-site state, so the ticket and the hotkey engine see the same edited config)
- Create: `ui/src/chrome/exec/useThrottledQuote.ts`
- Modify: `ui/src/App.tsx` (mount `OrderConfigProvider` inside `ToastProvider`)
- Modify (test stubs): `ui/src/chrome/panels/ChartPanel.test.tsx`, `LadderPanel.test.tsx`, `TapePanel.test.tsx`, `NewsPanel.test.tsx` — their `commands.sendCommand` stubs must now return a full `AckMsg` (the widened return type; these pre-existing tests otherwise fail `typecheck`).
- Test: `ui/src/chrome/exec/useOrderConfig.test.tsx`
- Test: `ui/src/chrome/exec/useThrottledQuote.test.tsx`

**Interfaces:**
- Consumes: `AckMsg`, `Quote`, `VenueID` (contract.ts); `OrderCommands`/`CommandAdapter` (commands.ts); `OrderConfig`/`DEFAULT_ORDER_CONFIG`/`ORDER_CONFIG_KEY` (actionTemplate.ts); `ExecStore` (data); `QuoteStore` (data); `ToastApi` (Toast.tsx).
- Produces: widened `PanelProps.commands`; `useOrderCommands(commands, exec, toast, now?): OrderCommands`; `OrderConfigProvider({ commands, children })` + `useOrderConfig(): { config: OrderConfig; loaded: boolean; save(next): void; setActiveVenue(v): void }` (reads context — no argument); `useThrottledQuote(quotes, symbol, hz?): Quote | undefined`.

- [ ] **Step 1: Widen `PanelProps.commands`**

In `ui/src/chrome/panels/registry.tsx`, add the `AckMsg` import and change the `commands` field type:

```ts
import type { AckMsg, TopicName } from "../../wire/contract";
```

```ts
  commands: { sendCommand(name: string, args: unknown): Promise<AckMsg> };
```

Do NOT run `typecheck` yet: `App` already passes `client.sendCommand` (returns `Promise<AckMsg>`) and `AppShell`/`PanelFrame` reference `PanelProps["commands"]` so they follow automatically, and `ThemeProvider`/`WorkspaceStore`/`ChartController.CommandSender` keep their own narrower `{status, value?}` types (which `AckMsg` satisfies) — BUT the pre-existing panel tests (`ChartPanel`/`Ladder`/`Tape`/`News`) stub `commands.sendCommand` to return a bare `{status:"accepted"}`, which no longer satisfies `Promise<AckMsg>`. Those stubs are fixed in Step 6; typecheck runs clean in Step 7.

- [ ] **Step 2: Write the failing tests for the config + quote hooks**

Create `ui/src/chrome/exec/useOrderConfig.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { OrderConfigProvider, useOrderConfig } from "./useOrderConfig";
import { DEFAULT_ORDER_CONFIG, type OrderConfig } from "./actionTemplate";
import type { AckMsg } from "../../wire/contract";

function cmds(getValue?: unknown) {
  const calls: Array<{ name: string; args: unknown }> = [];
  return {
    calls,
    sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => {
      calls.push({ name, args });
      if (name === "GetConfig") return { kind: "ack", corrId: "c", status: "accepted", value: getValue };
      return { kind: "ack", corrId: "c", status: "accepted" };
    }),
  };
}
const wrapper = (c: ReturnType<typeof cmds>) => ({ children }: { children: ReactNode }) =>
  <OrderConfigProvider commands={c}>{children}</OrderConfigProvider>;

describe("useOrderConfig", () => {
  it("falls back to defaults when the store has no value", async () => {
    const c = cmds(undefined);
    const { result } = renderHook(() => useOrderConfig(), { wrapper: wrapper(c) });
    await waitFor(() => expect(result.current.loaded).toBe(true));
    expect(result.current.config).toEqual(DEFAULT_ORDER_CONFIG);
  });
  it("loads a persisted config, and setActiveVenue persists via SetConfig", async () => {
    const persisted: OrderConfig = { templates: [], activeVenue: "alpaca-paper" };
    const c = cmds(persisted);
    const { result } = renderHook(() => useOrderConfig(), { wrapper: wrapper(c) });
    await waitFor(() => expect(result.current.config.activeVenue).toBe("alpaca-paper"));
    act(() => result.current.setActiveVenue("tradezero-live"));
    expect(result.current.config.activeVenue).toBe("tradezero-live");
    const set = c.calls.find((x) => x.name === "SetConfig");
    expect(set?.args).toMatchObject({ key: "orderConfig" });
  });
});
```

Create `ui/src/chrome/exec/useThrottledQuote.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useThrottledQuote } from "./useThrottledQuote";
import { QuoteStore } from "../../data/QuoteStore";

describe("useThrottledQuote", () => {
  it("reads the current quote on mount and refreshes on the throttle interval", () => {
    vi.useFakeTimers();
    const qs = new QuoteStore();
    qs.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 1, ask: 2, last: 1.5, ts: "" } });
    const { result } = renderHook(() => useThrottledQuote(qs, "US.AAPL", 10));
    expect(result.current?.ask).toBe(2);
    qs.apply({ kind: "delta", topic: "md.quote" as never, payload: { symbol: "US.AAPL", ask: 3 } });
    act(() => vi.advanceTimersByTime(120)); // > 1000/10ms
    expect(result.current?.ask).toBe(3);
    vi.useRealTimers();
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd ui && npx vitest run src/chrome/exec/useOrderConfig.test.tsx src/chrome/exec/useThrottledQuote.test.tsx`
Expected: FAIL — modules not found.

- [ ] **Step 4: Implement the hooks**

Create `ui/src/chrome/exec/useOrderCommands.ts`:

```ts
import { useMemo } from "react";
import { OrderCommands, type CommandAdapter } from "./commands";
import type { ExecStore } from "../../data/ExecStore";
import type { ToastApi } from "../Toast";

export function useOrderCommands(cmd: CommandAdapter, exec: ExecStore, toast: ToastApi, now: () => number = () => Date.now()): OrderCommands {
  return useMemo(() => new OrderCommands({ cmd, exec, toast, now }), [cmd, exec, toast, now]);
}
```

Create `ui/src/chrome/exec/useOrderConfig.tsx` — a **shared context provider**. The order-entry config is edited in the settings modal (rendered by the ticket) but also read by the hotkey engine (mounted separately in `AppShell`); per-call-site `useState` would leave the hotkey engine on a stale copy after an edit. One provider = one source of truth, one `GetConfig` per window.

```tsx
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import type { AckMsg, VenueID } from "../../wire/contract";
import { DEFAULT_ORDER_CONFIG, ORDER_CONFIG_KEY, type OrderConfig } from "./actionTemplate";

interface Cmd { sendCommand(name: string, args: unknown): Promise<AckMsg> }
export interface OrderConfigApi { config: OrderConfig; loaded: boolean; save(next: OrderConfig): void; setActiveVenue(v: VenueID): void }

const Ctx = createContext<OrderConfigApi | null>(null);

export function OrderConfigProvider({ commands, children }: { commands: Cmd; children: ReactNode }): JSX.Element {
  const [config, setConfig] = useState<OrderConfig>(DEFAULT_ORDER_CONFIG);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let live = true;
    void commands.sendCommand("GetConfig", { key: ORDER_CONFIG_KEY }).then((ack) => {
      if (!live) return;
      if (ack.status === "accepted" && ack.value && typeof ack.value === "object") setConfig(ack.value as OrderConfig);
      setLoaded(true);
    });
    return () => { live = false; };
  }, [commands]);

  const save = useCallback((next: OrderConfig) => {
    setConfig(next);
    void commands.sendCommand("SetConfig", { key: ORDER_CONFIG_KEY, value: next });
  }, [commands]);
  const setActiveVenue = useCallback((v: VenueID) => setConfig((c) => { const next = { ...c, activeVenue: v }; void commands.sendCommand("SetConfig", { key: ORDER_CONFIG_KEY, value: next }); return next; }), [commands]);

  return <Ctx.Provider value={{ config, loaded, save, setActiveVenue }}>{children}</Ctx.Provider>;
}

export function useOrderConfig(): OrderConfigApi {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useOrderConfig must be used within an OrderConfigProvider");
  return ctx;
}
```

Create `ui/src/chrome/exec/useThrottledQuote.ts`:

```ts
import { useEffect, useState } from "react";
import type { Quote } from "../../wire/contract";
import type { QuoteStore } from "../../data/QuoteStore";

// A capped-rate React projection of QuoteStore for the ticket's bid/ask display.
// The hard rule forbids raw per-tick React updates; this polls at ≤ hz (default 6),
// only re-rendering when the store's revision advanced. Interval (not rAF) keeps it
// deterministic under fake timers.
export function useThrottledQuote(quotes: QuoteStore, symbol: string, hz = 6): Quote | undefined {
  const [q, setQ] = useState<Quote | undefined>(() => quotes.get(symbol));
  useEffect(() => {
    setQ(quotes.get(symbol));
    let lastRev = quotes.getRev();
    const id = setInterval(() => {
      const rev = quotes.getRev();
      if (rev !== lastRev) { lastRev = rev; setQ(quotes.get(symbol)); }
    }, Math.max(1, Math.floor(1000 / hz)));
    return () => clearInterval(id);
  }, [quotes, symbol, hz]);
  return q;
}
```

- [ ] **Step 5: Mount `OrderConfigProvider` app-wide**

In `ui/src/App.tsx`, add the import and wrap `AppShell` (inside `ToastProvider`, so both the ticket and the AppShell-mounted hotkey engine share one config):

```tsx
import { OrderConfigProvider } from "./chrome/exec/useOrderConfig";
```
```tsx
    <ThemeProvider commands={commands}>
      <ToastProvider>
        <OrderConfigProvider commands={commands}>
          <ReconnectOverlay state={state}>
            <AppShell workspaceName={workspaceName} stores={stores} scheduler={scheduler}
              workspaceStore={workspaceStore} linkGroups={linkGroups} commands={commands} />
          </ReconnectOverlay>
        </OrderConfigProvider>
      </ToastProvider>
    </ThemeProvider>
```

- [ ] **Step 6: Fix the pre-existing panel test command stubs**

Widening `PanelProps.commands.sendCommand` to `Promise<AckMsg>` breaks four pre-existing panel tests whose stub returns a bare `{status:"accepted"}`. In each of `ui/src/chrome/panels/ChartPanel.test.tsx`, `LadderPanel.test.tsx`, `TapePanel.test.tsx`, `NewsPanel.test.tsx`, change the `commands` stub's `sendCommand` to return a full `AckMsg`:

```ts
// before: sendCommand: vi.fn(async () => ({ status: "accepted" }))
// after:
sendCommand: vi.fn(async (): Promise<AckMsg> => ({ kind: "ack", corrId: "c", status: "accepted" }))
```

Add `import type { AckMsg } from "../../wire/contract";` to any of those files that lacks it. (Do NOT add `sendQuery` yet — `PanelProps.commands` gains it only in Task 15, which updates these stubs again.)

- [ ] **Step 7: Run the tests + typecheck**

Run: `cd ui && npx vitest run src/chrome/exec/useOrderConfig.test.tsx src/chrome/exec/useThrottledQuote.test.tsx && cd ui && npm run typecheck`
Expected: PASS (the four stub fixes clear the widening's fallout).

- [ ] **Step 8: Commit**

```bash
cd ui && git add src/chrome/panels/registry.tsx src/App.tsx src/chrome/exec/useOrderCommands.ts src/chrome/exec/useOrderConfig.tsx src/chrome/exec/useThrottledQuote.ts src/chrome/exec/useOrderConfig.test.tsx src/chrome/exec/useThrottledQuote.test.tsx src/chrome/panels/ChartPanel.test.tsx src/chrome/panels/LadderPanel.test.tsx src/chrome/panels/TapePanel.test.tsx src/chrome/panels/NewsPanel.test.tsx
git commit -m "feat(ui/exec): widen commands→AckMsg + shared exec hooks (OrderConfigProvider/commands/quote)"
```

---

## Task 9: Account bar panel

**Files:**
- Create: `ui/src/chrome/panels/AccountBarPanel.tsx`
- Modify: `ui/src/chrome/panels/registry.tsx` (register `"account-bar"`)
- Test: `ui/src/chrome/panels/AccountBarPanel.test.tsx`

**Interfaces:**
- Consumes: `PanelProps` (registry); `ExecStore` (via `stores.exec`); `AccountRow`/`ExecStatus`/`VenueStatus` (contract.ts); `useOrderCommands` (hook); `useTheme`; `useToasts`; `formatPrice` (render/format.ts).
- Produces: `AccountBarPanel(props: PanelProps)`; registry entry `"account-bar"` with `topics: ["exec.account", "exec.status"]`.

Behavior: aggregate equity / buying power / day P&L / realized across venues (summed), plus per-venue rows; **armed/disarmed state + master arm control** (toggle sends `Arm`/`Disarm`); per-venue armed dots; connection dots from `ExecStatus.venues[].connected`. Missing/absent account → `—`, never `$0`. (Opening the Connection Status panel on dot-click needs dockview-api threading and is deferred; the dots are informational with a `title` tooltip.)

- [ ] **Step 1: Write the failing test**

Create `ui/src/chrome/panels/AccountBarPanel.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { AccountBarPanel } from "./AccountBarPanel";
import { makeStores } from "../../data/registry";
import type { AckMsg, AccountRow, ExecStatus } from "../../wire/contract";
import type { PanelProps } from "./registry";

function mkProps(over: Partial<PanelProps> = {}) {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => { sent.push({ name, args }); return { kind: "ack", corrId: "c", status: "accepted" }; }) };
  const props = { config: { id: "t-account", panelId: "account-bar", group: null, settings: {} }, stores, scheduler: {} as never, width: 800, height: 60, linkGroups: {} as never, commands, onConfigChange: () => {}, ...over } as PanelProps;
  return { props, stores, sent };
}
const acct = (venue: string, o: Partial<AccountRow> = {}): AccountRow => ({ venue, equity: 100, buyingPower: 400, availableCash: 50, sodEquity: 100, realized: 0, dayPnl: 0, leverage: 4, tsMs: 1, ...o });
const status = (masterArmed: boolean): ExecStatus => ({ masterArmed, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });

function wrap(props: PanelProps) {
  return render(<ThemeProvider><ToastProvider><AccountBarPanel {...props} /></ToastProvider></ThemeProvider>);
}

describe("AccountBarPanel", () => {
  it("shows — for equity before any account snapshot arrives", () => {
    const { props } = mkProps();
    wrap(props);
    expect(screen.getByTestId("acct-equity").textContent).toBe("—");
  });
  it("aggregates day P&L across venues and shows armed state", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.account" as never, key: "alpaca-paper", payload: acct("alpaca-paper", { dayPnl: 12 }) });
      stores.exec.apply({ kind: "delta", topic: "exec.account" as never, key: "tradezero-live", payload: acct("tradezero-live", { dayPnl: -5 }) });
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
    });
    expect(screen.getByTestId("acct-daypnl").textContent).toContain("7.00");
    expect(screen.getByTestId("arm-toggle").textContent).toMatch(/ARMED/i);
  });
  it("arm toggle sends Disarm when currently armed", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) }));
    fireEvent.click(screen.getByTestId("arm-toggle"));
    expect(sent.map((s) => s.name)).toContain("Disarm");
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/AccountBarPanel.test.tsx`
Expected: FAIL — module not found (and `"account-bar"` still a placeholder).

- [ ] **Step 3: Implement `AccountBarPanel.tsx`**

Create `ui/src/chrome/panels/AccountBarPanel.tsx`:

```tsx
import { useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { formatPrice } from "../../render/format";

const money = (n: number | null): string => (n === null ? "—" : (n < 0 ? "−$" : "$") + formatPrice(Math.abs(n), 2));

export function AccountBarPanel({ stores, commands }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());

  const accounts = stores.exec.accounts();
  const status = stores.exec.status();
  const sum = (pick: (a: (typeof accounts)[number]) => number) => (accounts.length ? accounts.reduce((s, a) => s + pick(a), 0) : null);
  const equity = sum((a) => a.equity);
  const bp = sum((a) => a.buyingPower);
  const dayPnl = sum((a) => a.dayPnl);
  const realized = sum((a) => a.realized);
  const armed = status?.masterArmed ?? false;

  const cell = (label: string, testid: string, value: string, tone?: number) => (
    <div style={{ display: "flex", flexDirection: "column", padding: "2px 10px" }}>
      <span style={{ fontSize: 10, color: palette.textMuted }}>{label}</span>
      <span data-testid={testid} style={{ fontSize: 13, color: tone === undefined ? palette.text : tone >= 0 ? palette.up : palette.down }}>{value}</span>
    </div>
  );
  const dot = (ok: boolean, title: string) => (
    <span title={title} style={{ width: 8, height: 8, borderRadius: 8, background: ok ? palette.ok : palette.danger, display: "inline-block" }} />
  );

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 4, height: "100%", padding: "0 8px", background: palette.surface, color: palette.text, fontFamily: "inherit" }}>
      {cell("Equity", "acct-equity", money(equity))}
      {cell("Buying Power", "acct-bp", money(bp))}
      {cell("Day P&L", "acct-daypnl", money(dayPnl), dayPnl ?? 0)}
      {cell("Realized", "acct-realized", money(realized), realized ?? 0)}
      <div style={{ flex: 1 }} />
      <div style={{ display: "flex", gap: 6, alignItems: "center", padding: "0 8px" }}>
        {(status?.venues ?? []).map((v) => (
          <span key={v.venue} style={{ display: "flex", gap: 3, alignItems: "center", fontSize: 10, color: palette.textMuted }}>
            {dot(v.connected, `${v.venue}: ${v.connected ? "connected" : "disconnected"}`)}{v.venue}{v.venueArmed ? " ●" : " ○"}
          </span>
        ))}
      </div>
      <button data-testid="arm-toggle" onClick={() => (armed ? oc.disarm() : oc.arm())}
        style={{ fontWeight: 700, padding: "4px 12px", borderRadius: 4, border: `1px solid ${armed ? palette.up : palette.warn}`,
          background: armed ? palette.up : "transparent", color: armed ? palette.bg : palette.warn, cursor: "pointer" }}>
        {armed ? "ARMED" : "DISARMED"}
      </button>
    </div>
  );
}
```

- [ ] **Step 4: Register the panel**

In `ui/src/chrome/panels/registry.tsx`, add the import and entry:

```ts
import { AccountBarPanel } from "./AccountBarPanel";
```
```ts
  "account-bar": {
    component: AccountBarPanel,
    topics: ["exec.account", "exec.status"],
  },
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/panels/AccountBarPanel.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/chrome/panels/AccountBarPanel.tsx src/chrome/panels/registry.tsx src/chrome/panels/AccountBarPanel.test.tsx
git commit -m "feat(ui/exec): account bar panel (aggregate P&L + master arm control + venue dots)"
```

---

## Task 10: Positions panel

**Files:**
- Create: `ui/src/chrome/panels/PositionsPanel.tsx`
- Modify: `ui/src/chrome/panels/registry.tsx` (register `"positions"`)
- Test: `ui/src/chrome/panels/PositionsPanel.test.tsx`

**Interfaces:**
- Consumes: `PanelProps`; `PositionRow` (contract.ts); `resolvePlaceTemplate` + `PlaceOrderTemplate`; `useOrderCommands`, `useToasts`, `useTheme`; `formatPrice`/`formatSize` (render/format.ts); `bareSymbol` (orderStatus.ts); `stores.quote.get`.
- Produces: `PositionsPanel(props)`; registry entry `"positions"` with `topics: ["exec.positions", "md.quote"]`.

Behavior: one row per `PositionRow` (per-venue rows + the cross-venue net row where `venue === null`), each with symbol, qty (signed), avg price, live unrealized P&L (colored). A **flatten button per real-venue row** composes a closing order (`side = qty > 0 ? SELL : COVER`, `MARKET` → RTH-coerced by the pre-check, sizing `PositionFraction:all`) and routes it through `OrderCommands.submit` — the gate treats it like any order. Net rows show no flatten button (no single venue). No quote for the symbol → a danger toast, never a mispriced order.

- [ ] **Step 1: Write the failing test**

Create `ui/src/chrome/panels/PositionsPanel.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { PositionsPanel } from "./PositionsPanel";
import { makeStores } from "../../data/registry";
import type { AckMsg, PositionRow, SubmitOrderArgs } from "../../wire/contract";
import type { PanelProps } from "./registry";

const RTH = Date.parse("2026-07-06T14:00:00Z");
function mkProps() {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => { sent.push({ name, args }); return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX" }; }) };
  const props = { config: { id: "t-positions", panelId: "positions", group: null, settings: {} }, stores, scheduler: {} as never, width: 400, height: 200, linkGroups: {} as never, commands, onConfigChange: () => {} } as PanelProps;
  return { props, stores, sent };
}
const pos = (o: Partial<PositionRow>): PositionRow => ({ venue: "alpaca-paper", symbol: "US.AAPL", qty: 300, avgPrice: 3.4, unrealizedPnl: 30, ...o });
const wrap = (p: PanelProps) => render(<ThemeProvider><ToastProvider><PositionsPanel {...p} /></ToastProvider></ThemeProvider>);

describe("PositionsPanel", () => {
  it("renders per-venue and net rows with colored unrealized P&L", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.positions" as never, payload: [pos({}), pos({ venue: null, unrealizedPnl: 30 })] }));
    expect(screen.getAllByText("AAPL").length).toBe(2);
    expect(screen.getByTestId("pos-net").textContent).toMatch(/NET/);
  });
  it("flatten on a long row submits a SELL for the full qty (priced from the quote)", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    act(() => {
      stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.5, ask: 3.51, last: 3.5, ts: "" } });
      stores.exec.apply({ kind: "snapshot", topic: "exec.positions" as never, payload: [pos({ qty: 300 })] });
    });
    fireEvent.click(screen.getByTestId("flatten-alpaca-paper-US.AAPL"));
    const submit = sent.find((s) => s.name === "SubmitOrder");
    const args = submit?.args as SubmitOrderArgs;
    expect(args.side).toBe("SELL");
    expect(args.qty).toBe(300);
    expect(args.venue).toBe("alpaca-paper");
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/PositionsPanel.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `PositionsPanel.tsx`**

Create `ui/src/chrome/panels/PositionsPanel.tsx`:

```tsx
import { useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { PositionRow } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { resolvePlaceTemplate } from "../exec/resolveTemplate";
import type { PlaceOrderTemplate } from "../exec/actionTemplate";
import { formatPrice, formatSize } from "../../render/format";
import { bareSymbol } from "../exec/orderStatus";

export function PositionsPanel({ stores, commands }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const rows = stores.exec.positions();

  const flatten = (row: PositionRow) => {
    if (row.venue === null) return; // net rows have no single venue to route to (button is hidden anyway)
    const venue = row.venue;        // narrowed to VenueID
    const quote = stores.quote.get(row.symbol);
    if (!quote) { toast.push({ level: "danger", text: `No quote to price the close for ${bareSymbol(row.symbol)}.` }); return; }
    const long = row.qty > 0;
    const t: PlaceOrderTemplate = {
      kind: "place", id: "flatten", label: "Flatten", side: long ? "SELL" : "COVER",
      type: "MARKET", tif: "DAY", priceSource: long ? "Bid" : "Ask", priceOffset: 0,
      sizing: { mode: "PositionFraction", fraction: "all" },
    };
    const r = resolvePlaceTemplate(t, { venue, symbol: row.symbol, quote, buyingPower: 0, positionQty: row.qty, nowMs: Date.now() });
    if (!r.preCheck.ok) { toast.push({ level: "danger", text: r.preCheck.errors.join(" ") }); return; }
    void oc.submit(r.args, r.flash);
  };

  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <thead><tr style={{ color: palette.textMuted, textAlign: "right" }}>
          <th style={{ textAlign: "left", padding: "2px 8px" }}>Symbol</th><th>Venue</th><th>Qty</th><th>Avg</th><th>Unreal</th><th></th>
        </tr></thead>
        <tbody>
          {rows.map((r, i) => {
            const net = r.venue === null;
            return (
              <tr key={`${r.venue ?? "NET"}-${r.symbol}-${i}`} data-testid={net ? "pos-net" : undefined}
                style={{ textAlign: "right", borderTop: `1px solid ${palette.border}`, fontWeight: net ? 700 : 400 }}>
                <td style={{ textAlign: "left", padding: "2px 8px" }}>{bareSymbol(r.symbol)}</td>
                <td style={{ color: palette.textMuted }}>{net ? "NET" : r.venue}</td>
                <td style={{ color: r.qty >= 0 ? palette.up : palette.down }}>{formatSize(r.qty)}</td>
                <td>{formatPrice(r.avgPrice, 2)}</td>
                <td style={{ color: r.unrealizedPnl >= 0 ? palette.up : palette.down }}>{formatPrice(r.unrealizedPnl, 2)}</td>
                <td>{net ? null : (
                  <button data-testid={`flatten-${r.venue}-${r.symbol}`} onClick={() => flatten(r)}
                    style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.border}`, background: "transparent", color: palette.text, cursor: "pointer" }}>Flatten</button>
                )}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
```

- [ ] **Step 4: Register the panel**

In `ui/src/chrome/panels/registry.tsx`:

```ts
import { PositionsPanel } from "./PositionsPanel";
```
```ts
  "positions": {
    component: PositionsPanel,
    topics: ["exec.positions", "md.quote"],
  },
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/panels/PositionsPanel.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/chrome/panels/PositionsPanel.tsx src/chrome/panels/registry.tsx src/chrome/panels/PositionsPanel.test.tsx
git commit -m "feat(ui/exec): positions panel (per-venue + net rows, gate-routed per-row flatten)"
```

---

## Task 11: Open orders panel

**Files:**
- Create: `ui/src/chrome/panels/OpenOrdersPanel.tsx`
- Modify: `ui/src/chrome/panels/registry.tsx` (register `"open-orders"`)
- Test: `ui/src/chrome/panels/OpenOrdersPanel.test.tsx`

**Interfaces:**
- Consumes: `PanelProps`; `OrderView` (ExecStore); `displayStatus`/`STATUS_LABEL`/`sideLabel`/`bareSymbol`/`abbrevType`/`isWorking` (orderStatus.ts); `useOrderCommands`, `useToasts`, `useTheme`; `formatPrice`/`formatSize`.
- Produces: `OpenOrdersPanel(props)`; registry entry `"open-orders"` with `topics: ["exec.orders", "exec.status"]`.

Behavior: newest-first rows over `stores.exec.orders()` showing the derived display status (Pending / Replacing / domain label). Working rows carry a **cancel** button; a **Cancel All** header button cancels every working order. Rejected rows show `rejectReason` **verbatim**. A **StreamGap reconcile badge** ("state reconciled — verify before acting") shows whenever any venue in `ExecStatus` has `reconcilePending`.

- [ ] **Step 1: Write the failing test**

Create `ui/src/chrome/panels/OpenOrdersPanel.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OpenOrdersPanel } from "./OpenOrdersPanel";
import { makeStores } from "../../data/registry";
import type { AckMsg, ExecStatus, Order } from "../../wire/contract";
import type { PanelProps } from "./registry";

function mkProps() {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => { sent.push({ name, args }); return { kind: "ack", corrId: "c", status: "accepted" }; }) };
  const props = { config: { id: "t-orders", panelId: "open-orders", group: null, settings: {} }, stores, scheduler: {} as never, width: 500, height: 200, linkGroups: {} as never, commands, onConfigChange: () => {} } as PanelProps;
  return { props, stores, sent };
}
const order = (id: string, o: Partial<Order> = {}): Order => ({ venue: "alpaca-paper", id, symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 10, avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, ...o });
const statusReconciling = (): ExecStatus => ({ masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: true, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });
const wrap = (p: PanelProps) => render(<ThemeProvider><ToastProvider><OpenOrdersPanel {...p} /></ToastProvider></ThemeProvider>);

describe("OpenOrdersPanel", () => {
  it("shows an optimistic order as Pending", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.addOptimistic({ args: { venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0 }, id: "ET9", createdMs: 100 }));
    expect(screen.getByText("Pending")).toBeTruthy();
  });
  it("shows a reject reason verbatim and no cancel button on a terminal row", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1", { status: "REJECTED", rejectReason: "R78: market order in extended hours" })] }));
    expect(screen.getByText(/R78: market order in extended hours/)).toBeTruthy();
    expect(screen.queryByTestId("cancel-ET1")).toBeNull();
  });
  it("cancel on a working row sends CancelOrder; Cancel All cancels every working order", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1"), order("ET2", { status: "SUBMITTED" }), order("ET3", { status: "FILLED" })] }));
    fireEvent.click(screen.getByTestId("cancel-ET1"));
    expect(sent.at(-1)).toEqual({ name: "CancelOrder", args: { venue: "alpaca-paper", orderId: "ET1" } });
    fireEvent.click(screen.getByTestId("cancel-all"));
    expect(sent.filter((s) => s.name === "CancelOrder").map((s) => (s.args as { orderId: string }).orderId).sort()).toEqual(["ET1", "ET1", "ET2"]);
  });
  it("shows the StreamGap reconcile badge when a venue is reconciling", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: statusReconciling() }));
    expect(screen.getByText(/verify before acting/i)).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/OpenOrdersPanel.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `OpenOrdersPanel.tsx`**

Create `ui/src/chrome/panels/OpenOrdersPanel.tsx`:

```tsx
import { useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { displayStatus, STATUS_LABEL, sideLabel, bareSymbol, abbrevType, isWorking } from "../exec/orderStatus";
import { formatPrice, formatSize } from "../../render/format";

export function OpenOrdersPanel({ stores, commands }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());

  const views = stores.exec.orders();
  const reconciling = (stores.exec.status()?.venues ?? []).some((v) => v.reconcilePending);

  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "3px 8px", background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
        <span style={{ fontWeight: 600 }}>Open Orders</span>
        <button data-testid="cancel-all" onClick={() => void oc.cancelAll("everything")}
          style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.warn}`, background: "transparent", color: palette.warn, cursor: "pointer" }}>Cancel All</button>
        {reconciling && (
          <span data-testid="reconcile-badge" style={{ marginLeft: "auto", fontSize: 10, color: palette.bg, background: palette.warn, padding: "1px 6px", borderRadius: 3 }}>
            state reconciled — verify before acting
          </span>
        )}
      </div>
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <tbody>
          {views.map(({ order, optimistic }) => {
            const ds = displayStatus(order, optimistic);
            const working = !optimistic && isWorking(order.status);
            const priceStr = order.type === "MARKET" ? "MKT" : formatPrice(order.type === "STOP" ? order.stopPrice : order.limitPrice, 2);
            return (
              <tr key={order.id} style={{ borderTop: `1px solid ${palette.border}` }}>
                <td style={{ padding: "2px 8px", color: order.side === "BUY" || order.side === "COVER" ? palette.up : palette.down }}>{sideLabel(order.side)}</td>
                <td>{formatSize(order.leavesQty > 0 ? order.leavesQty : order.qty)}</td>
                <td>{bareSymbol(order.symbol)}</td>
                <td style={{ textAlign: "right" }}>{priceStr} {abbrevType(order.type)}</td>
                <td style={{ color: palette.textMuted }}>{STATUS_LABEL[ds]}</td>
                <td style={{ color: palette.danger, fontSize: 10 }}>{order.rejectReason}</td>
                <td style={{ color: palette.textMuted, fontSize: 10 }}>{order.venue}</td>
                <td>{(working || optimistic) ? (
                  <button data-testid={`cancel-${order.id}`} onClick={() => void oc.cancel(order.venue, order.id)}
                    style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.border}`, background: "transparent", color: palette.text, cursor: "pointer" }}>Cancel</button>
                ) : null}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
```

- [ ] **Step 4: Register the panel**

In `ui/src/chrome/panels/registry.tsx`:

```ts
import { OpenOrdersPanel } from "./OpenOrdersPanel";
```
```ts
  "open-orders": {
    component: OpenOrdersPanel,
    topics: ["exec.orders", "exec.status"],
  },
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/panels/OpenOrdersPanel.test.tsx`
Expected: PASS (4 tests). Note the third test expects two `CancelOrder` for `ET1` (per-row click + the Cancel-All sweep that includes it) plus one for `ET2`.

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/chrome/panels/OpenOrdersPanel.tsx src/chrome/panels/registry.tsx src/chrome/panels/OpenOrdersPanel.test.tsx
git commit -m "feat(ui/exec): open orders panel (9-state + PendingNew/Replacing, cancel, reconcile badge)"
```

---

## Task 12: Order ticket panel

**Files:**
- Create: `ui/src/chrome/panels/OrderTicketPanel.tsx`
- Modify: `ui/src/chrome/panels/registry.tsx` (register `"order-ticket"`)
- Test: `ui/src/chrome/panels/OrderTicketPanel.test.tsx`

**Interfaces:**
- Consumes: `PanelProps`; `Side`/`OrderType`/`TIF`/`SubmitOrderArgs` (contract.ts); `SizingMode`/`SizingSpec`/`resolveShares`; `preCheck`; `resolvePlaceTemplate`; `PlaceOrderTemplate` (from the loaded `OrderConfig.templates`); `useThrottledQuote`, `useOrderCommands`, `useOrderConfig`, `useToasts`, `useTheme`; `sideLabel`/`bareSymbol`/`abbrevType`; `formatPrice`.
- Produces: `OrderTicketPanel(props)`; registry entry `"order-ticket"` with `topics: ["md.quote", "exec.account", "exec.positions", "exec.status"]`.

Behavior: follows the link-group focused symbol; shows throttled bid/ask (click a quote to seed the price field); side / type (default Limit) / price / stop (when STOP·STOP_LIMIT) / amount + sizing-mode dropdown / TIF; a **venue selector** bound to `OrderConfig.activeVenue`; a **preset button row** from the loaded place-templates; **Cancel All**; and a distinct, deliberately-ugly, always-visible **kill switch**. Submit and presets resolve → `preCheck` → `OrderCommands.submit` (the gate blocks if disarmed, surfaced as a toast — no confirm dialog). Cancel-all and kill fire regardless of armed state.

- [ ] **Step 1: Write the failing test**

Create `ui/src/chrome/panels/OrderTicketPanel.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "../exec/useOrderConfig";
import { OrderTicketPanel } from "./OrderTicketPanel";
import { makeStores } from "../../data/registry";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { AckMsg, ExecStatus, SubmitOrderArgs } from "../../wire/contract";
import type { PanelProps } from "./registry";

function mkProps() {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => { sent.push({ name, args }); return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX", value: undefined }; }) };
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const props = { config: { id: "t-ticket", panelId: "order-ticket", group: "green", settings: {} }, stores, scheduler: {} as never, width: 320, height: 400, linkGroups, commands, onConfigChange: () => {} } as PanelProps;
  return { props, stores, sent, linkGroups };
}
const status = (): ExecStatus => ({ masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });
const wrap = (p: PanelProps) => render(
  <ThemeProvider><ToastProvider><OrderConfigProvider commands={p.commands}><OrderTicketPanel {...p} /></OrderConfigProvider></ToastProvider></ThemeProvider>,
);

describe("OrderTicketPanel", () => {
  it("follows the link-group symbol and shows bid/ask", async () => {
    const { props, stores, linkGroups } = mkProps();
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
      linkGroups.focus("green", "US.AAPL");
    });
    wrap(props);
    expect(await screen.findByText("AAPL")).toBeTruthy();
    expect(screen.getByTestId("bid").textContent).toContain("3.40");
    expect(screen.getByTestId("ask").textContent).toContain("3.50");
  });
  it("manual Shares submit sends a venue-tagged SubmitOrder", async () => {
    const { props, stores, linkGroups, sent } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } }); linkGroups.focus("green", "US.AAPL"); });
    wrap(props);
    fireEvent.change(screen.getByTestId("amount"), { target: { value: "100" } });
    fireEvent.change(screen.getByTestId("price"), { target: { value: "3.5" } });
    fireEvent.click(screen.getByTestId("submit"));
    await waitFor(() => expect(sent.some((s) => s.name === "SubmitOrder")).toBe(true));
    const args = sent.find((s) => s.name === "SubmitOrder")?.args as SubmitOrderArgs;
    expect(args).toMatchObject({ venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", qty: 100, limitPrice: 3.5 });
  });
  it("kill switch fires KillSwitch even without arming logic", () => {
    const { props, sent } = mkProps();
    wrap(props);
    fireEvent.click(screen.getByTestId("kill"));
    expect(sent.some((s) => s.name === "KillSwitch")).toBe(true);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/OrderTicketPanel.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `OrderTicketPanel.tsx`**

Create `ui/src/chrome/panels/OrderTicketPanel.tsx`:

```tsx
import { useEffect, useMemo, useState } from "react";
import { useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { Side, OrderType, TIF, SubmitOrderArgs, VenueID } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { useOrderConfig } from "../exec/useOrderConfig";
import { useThrottledQuote } from "../exec/useThrottledQuote";
import { resolveShares, type SizingMode } from "../exec/sizing";
import { preCheck, type DraftOrder } from "../exec/preChecks";
import { resolvePlaceTemplate } from "../exec/resolveTemplate";
import type { PlaceOrderTemplate } from "../exec/actionTemplate";
import { sideLabel, bareSymbol, abbrevType } from "../exec/orderStatus";
import { formatPrice } from "../../render/format";

const SIDES: Side[] = ["BUY", "SELL", "SHORT", "COVER"];
const TYPES: OrderType[] = ["LIMIT", "MARKET", "STOP", "STOP_LIMIT"];
const TIFS: TIF[] = ["DAY", "GTC", "IOC", "FOK"];
const MODES: SizingMode[] = ["Shares", "Dollar", "BuyingPowerPct", "PositionFraction"];

export function OrderTicketPanel({ config, stores, commands, linkGroups }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  const { config: orderCfg, setActiveVenue } = useOrderConfig(); // shared context (Task 8)
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());

  const [symbol, setSymbol] = useState<string>(() => linkGroups.symbolFor(config.group) ?? (config.settings.symbol as string) ?? "US.AAPL");
  useEffect(() => {
    const apply = () => setSymbol(linkGroups.symbolFor(config.group) ?? (config.settings.symbol as string) ?? "US.AAPL");
    apply();
    return linkGroups.subscribe(apply);
  }, [linkGroups, config.group, config.settings.symbol]);

  const quote = useThrottledQuote(stores.quote, symbol);
  const status = stores.exec.status();
  const venues = status?.venues.map((v) => v.venue) ?? [];
  const venue: VenueID = orderCfg.activeVenue || venues[0] || "";

  const [side, setSide] = useState<Side>("BUY");
  const [type, setType] = useState<OrderType>("LIMIT");
  const [tif, setTif] = useState<TIF>("DAY");
  const [mode, setMode] = useState<SizingMode>("Shares");
  const [amount, setAmount] = useState("100");
  const [price, setPrice] = useState("");
  const [stop, setStop] = useState("");

  const account = stores.exec.accounts().find((a) => a.venue === venue);
  const buyingPower = account?.buyingPower ?? 0;
  const positionQty = stores.exec.positions().filter((p) => p.symbol === symbol && p.venue === venue).reduce((s, p) => s + p.qty, 0);

  const presets = useMemo(() => orderCfg.templates.filter((t): t is PlaceOrderTemplate => t.kind === "place"), [orderCfg.templates]);

  const submitManual = () => {
    if (venue === "") { toast.push({ level: "danger", text: "No venue configured." }); return; }
    const px = Number(price) || 0;
    const spec = mode === "Shares" ? { mode, shares: Number(amount) || 0 }
      : mode === "Dollar" ? { mode, dollar: Number(amount) || 0 }
      : mode === "BuyingPowerPct" ? { mode, pct: Number(amount) || 0 }
      : { mode, fraction: "all" as const };
    const qty = resolveShares(spec, { price: px, buyingPower, positionQty });
    const draft: DraftOrder = { symbol, side, type, tif, qty, limitPrice: type === "MARKET" ? 0 : px, stopPrice: type === "STOP" || type === "STOP_LIMIT" ? Number(stop) || 0 : 0 };
    const pc = preCheck(draft, quote?.last ?? 0, Date.now());
    for (const n of pc.notices) toast.push({ level: "warn", text: n });
    if (!pc.ok) { toast.push({ level: "danger", text: pc.errors.join(" ") }); return; }
    const o = pc.order;
    const args: SubmitOrderArgs = { venue, symbol, side: o.side, type: o.type, tif: o.tif, qty: o.qty, limitPrice: o.limitPrice, stopPrice: o.stopPrice };
    const tail = o.type === "MARKET" ? "MKT" : `${o.limitPrice.toFixed(2)} ${abbrevType(o.type)}`;
    const flash = `${sideLabel(o.side)} ${o.qty.toLocaleString("en-US")} ${bareSymbol(symbol)} @ ${tail}`;
    void oc.submit(args, flash);
  };

  const firePreset = (t: PlaceOrderTemplate) => {
    if (venue === "" || !quote) { toast.push({ level: "danger", text: "No venue/quote for preset." }); return; }
    const r = resolvePlaceTemplate(t, { venue, symbol, quote, buyingPower, positionQty, nowMs: Date.now() });
    for (const n of r.preCheck.notices) toast.push({ level: "warn", text: n });
    if (!r.preCheck.ok) { toast.push({ level: "danger", text: r.preCheck.errors.join(" ") }); return; }
    void oc.submit(r.args, r.flash);
  };

  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, fontSize: 12, padding: "2px 4px" } as const;
  const quoteBtn = (label: string, testid: string, value: number | undefined, tone: string) => (
    <button data-testid={testid} onClick={() => value !== undefined && setPrice(String(value))}
      style={{ ...inp, borderColor: tone, color: tone, cursor: "pointer", flex: 1 }}>{label} {value === undefined ? "—" : formatPrice(value, 2)}</button>
  );

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 4, padding: 8, height: "100%", background: palette.surface, color: palette.text, fontSize: 12, overflow: "auto" }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline" }}>
        <strong>{bareSymbol(symbol)}</strong>
        <select data-testid="venue" value={venue} onChange={(e) => setActiveVenue(e.target.value)} style={inp}>
          {venues.map((v) => <option key={v} value={v}>{v}</option>)}
        </select>
      </div>
      <div style={{ display: "flex", gap: 4 }}>
        {quoteBtn("Bid", "bid", quote?.bid, palette.up)}
        {quoteBtn("Ask", "ask", quote?.ask, palette.down)}
      </div>
      <div style={{ display: "flex", gap: 4 }}>
        <select value={side} onChange={(e) => setSide(e.target.value as Side)} style={inp}>{SIDES.map((s) => <option key={s}>{s}</option>)}</select>
        <select value={type} onChange={(e) => setType(e.target.value as OrderType)} style={inp}>{TYPES.map((t) => <option key={t}>{t}</option>)}</select>
        <select value={tif} onChange={(e) => setTif(e.target.value as TIF)} style={inp}>{TIFS.map((t) => <option key={t}>{t}</option>)}</select>
      </div>
      <label>Price <input data-testid="price" value={price} onChange={(e) => setPrice(e.target.value)} disabled={type === "MARKET"} style={inp} /></label>
      {(type === "STOP" || type === "STOP_LIMIT") && <label>Stop <input data-testid="stop" value={stop} onChange={(e) => setStop(e.target.value)} style={inp} /></label>}
      <div style={{ display: "flex", gap: 4 }}>
        <input data-testid="amount" value={amount} onChange={(e) => setAmount(e.target.value)} style={{ ...inp, flex: 1 }} />
        <select data-testid="mode" value={mode} onChange={(e) => setMode(e.target.value as SizingMode)} style={inp}>{MODES.map((m) => <option key={m}>{m}</option>)}</select>
      </div>
      <button data-testid="submit" onClick={submitManual} style={{ ...inp, background: palette.accent, color: palette.bg, fontWeight: 700, padding: "6px", cursor: "pointer" }}>
        Submit {side} {symbol && bareSymbol(symbol)}
      </button>
      {presets.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
          {presets.map((t) => (
            <button key={t.id} data-testid={`preset-${t.id}`} onClick={() => firePreset(t)}
              style={{ ...inp, cursor: "pointer" }}>{t.label}</button>
          ))}
        </div>
      )}
      <div style={{ display: "flex", gap: 4, marginTop: "auto" }}>
        <button data-testid="cancel-all" onClick={() => void oc.cancelAll("everything")} style={{ ...inp, flex: 1, borderColor: palette.warn, color: palette.warn, cursor: "pointer" }}>Cancel All</button>
        <button data-testid="kill" onClick={() => void oc.kill()}
          style={{ flex: 1, background: palette.danger, color: "#fff", border: "2px solid #000", fontWeight: 800, letterSpacing: 1, padding: "6px", cursor: "pointer" }}>KILL</button>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Register the panel**

In `ui/src/chrome/panels/registry.tsx`:

```ts
import { OrderTicketPanel } from "./OrderTicketPanel";
```
```ts
  "order-ticket": {
    component: OrderTicketPanel,
    topics: ["md.quote", "exec.account", "exec.positions", "exec.status"],
  },
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/panels/OrderTicketPanel.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/chrome/panels/OrderTicketPanel.tsx src/chrome/panels/registry.tsx src/chrome/panels/OrderTicketPanel.test.tsx
git commit -m "feat(ui/exec): order ticket panel (link-follow, sizing, presets, cancel-all, kill switch)"
```

---

## Task 13: Hotkey engine (pure matcher + arm-gated `useHotkeys`)

**Files:**
- Create: `ui/src/chrome/exec/hotkeys.ts` (pure)
- Create: `ui/src/chrome/exec/useHotkeys.ts`
- Modify: `ui/src/chrome/AppShell.tsx` (mount `useHotkeys`)
- Test: `ui/src/chrome/exec/hotkeys.test.ts`
- Test: `ui/src/chrome/exec/useHotkeys.test.tsx`

**Interfaces:**
- Consumes: `ActionTemplate`/`PlaceOrderTemplate`/`ManagementTemplate` (actionTemplate.ts); `resolvePlaceTemplate`; `OrderCommands` (via `useOrderCommands`); `useOrderConfig`, `useToasts`; `Stores`, `LinkGroups`, `LinkGroup`.
- Produces: `normalizeCombo(e): string`, `matchTemplate(templates, combo): ActionTemplate | undefined` (pure); `useHotkeys({ stores, commands, linkGroups, group? }): void`.

Safety (per Global Constraints): **place**-hotkeys fire only when armed (`masterArmed && venueArmed` for the active venue) **and** the window has OS focus (`document.hasFocus()`); a blocked place-hotkey flashes "disarmed" and sends nothing. **Management** hotkeys (cancel-last / cancel-all / kill) fire regardless of armed state. Every place-fire flashes the resolved order (via the submit toast).

- [ ] **Step 1: Write the failing test for the pure matcher**

Create `ui/src/chrome/exec/hotkeys.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { normalizeCombo, matchTemplate } from "./hotkeys";
import { DEFAULT_TEMPLATES } from "./actionTemplate";

describe("hotkey matcher", () => {
  it("normalizes modifiers into a canonical combo string", () => {
    expect(normalizeCombo({ ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, key: "1" })).toBe("Ctrl+1");
    expect(normalizeCombo({ ctrlKey: true, shiftKey: true, altKey: false, metaKey: false, key: "k" })).toBe("Ctrl+Shift+K");
    expect(normalizeCombo({ ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, key: "Backspace" })).toBe("Ctrl+Backspace");
  });
  it("returns empty for a bare modifier keypress", () => {
    expect(normalizeCombo({ ctrlKey: true, shiftKey: false, altKey: false, metaKey: false, key: "Control" })).toBe("");
  });
  it("matches a template by its hotkey field", () => {
    expect(matchTemplate(DEFAULT_TEMPLATES, "Ctrl+1")?.id).toBe("buy-5k");
    expect(matchTemplate(DEFAULT_TEMPLATES, "Ctrl+Shift+K")?.id).toBe("kill");
    expect(matchTemplate(DEFAULT_TEMPLATES, "Ctrl+9")).toBeUndefined();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/exec/hotkeys.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement the pure matcher `hotkeys.ts`**

Create `ui/src/chrome/exec/hotkeys.ts`:

```ts
import type { ActionTemplate } from "./actionTemplate";

export interface KeyLike { ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean; key: string }
const MODIFIER_KEYS = new Set(["Control", "Shift", "Alt", "Meta"]);

// Canonical combo, modifiers in fixed order Ctrl+Alt+Shift+Meta+Key; single letters
// upper-cased. A bare modifier keypress → "" (never matches a binding).
export function normalizeCombo(e: KeyLike): string {
  if (MODIFIER_KEYS.has(e.key)) return "";
  const parts: string[] = [];
  if (e.ctrlKey) parts.push("Ctrl");
  if (e.altKey) parts.push("Alt");
  if (e.shiftKey) parts.push("Shift");
  if (e.metaKey) parts.push("Meta");
  parts.push(e.key.length === 1 ? e.key.toUpperCase() : e.key);
  return parts.join("+");
}

export function matchTemplate(templates: ActionTemplate[], combo: string): ActionTemplate | undefined {
  if (combo === "") return undefined;
  return templates.find((t) => t.hotkey === combo);
}
```

- [ ] **Step 4: Write the failing test for `useHotkeys`**

Create `ui/src/chrome/exec/useHotkeys.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "./useOrderConfig";
import { useHotkeys } from "./useHotkeys";
import { makeStores } from "../../data/registry";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { AckMsg, ExecStatus } from "../../wire/contract";

// Real parameter type — casting a function's own param as `never` fails typecheck.
function Harness(props: Parameters<typeof useHotkeys>[0]) { useHotkeys(props); return null; }
const status = (masterArmed: boolean): ExecStatus => ({ masterArmed, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });

function setup(masterArmed: boolean) {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (n: string, a: unknown): Promise<AckMsg> => { sent.push({ name: n, args: a }); return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX", value: undefined }; }) };
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(masterArmed) });
  stores.exec.apply({ kind: "snapshot", topic: "exec.account" as never, key: "alpaca-paper", payload: { venue: "alpaca-paper", equity: 100, buyingPower: 100000, availableCash: 100, sodEquity: 100, realized: 0, dayPnl: 0, leverage: 4, tsMs: 1 } });
  stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
  linkGroups.focus("green", "US.AAPL");
  render(
    <ThemeProvider><ToastProvider><OrderConfigProvider commands={commands}>
      <Harness stores={stores} commands={commands} linkGroups={linkGroups} group="green" />
    </OrderConfigProvider></ToastProvider></ThemeProvider>,
  );
  return { sent };
}

beforeEach(() => vi.spyOn(document, "hasFocus").mockReturnValue(true));
afterEach(() => vi.restoreAllMocks());

describe("useHotkeys", () => {
  it("fires a place-hotkey when armed", async () => {
    const { sent } = setup(true);
    await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "SubmitOrder")).toBe(true);
  });
  it("blocks a place-hotkey when disarmed (no send)", async () => {
    const { sent } = setup(false);
    await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "SubmitOrder")).toBe(false);
  });
  it("fires a management hotkey (kill) even when disarmed", async () => {
    const { sent } = setup(false);
    await act(async () => { fireEvent.keyDown(window, { key: "k", ctrlKey: true, shiftKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "KillSwitch")).toBe(true);
  });
});
```

- [ ] **Step 5: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/exec/useHotkeys.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 6: Implement `useHotkeys.ts`**

Create `ui/src/chrome/exec/useHotkeys.ts`:

```ts
import { useEffect } from "react";
import type { AckMsg } from "../../wire/contract";
import type { Stores } from "../../data/registry";
import type { LinkGroup, LinkGroups } from "../linkGroups";
import { useToasts } from "../Toast";
import { useOrderCommands } from "./useOrderCommands";
import { useOrderConfig } from "./useOrderConfig";
import { normalizeCombo, matchTemplate } from "./hotkeys";
import { resolvePlaceTemplate } from "./resolveTemplate";
import type { PlaceOrderTemplate, ManagementTemplate } from "./actionTemplate";

interface Cmd { sendCommand(name: string, args: unknown): Promise<AckMsg> }

export function useHotkeys(opts: { stores: Stores; commands: Cmd; linkGroups: LinkGroups; group?: LinkGroup }): void {
  const { stores, commands, linkGroups, group = "green" } = opts;
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  const { config } = useOrderConfig(); // shared context (mounted in App via OrderConfigProvider)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = matchTemplate(config.templates, normalizeCombo(e));
      if (!t) return;
      e.preventDefault();
      const status = stores.exec.status();
      const venue = config.activeVenue || status?.venues[0]?.venue || "";
      const symbol = linkGroups.symbolFor(group) ?? "";

      if (t.kind === "place") {
        const armed = !!status?.masterArmed && !!status.venues.find((v) => v.venue === venue)?.venueArmed;
        if (!document.hasFocus()) return;
        if (!armed) { toast.push({ level: "warn", text: "disarmed — hotkey blocked" }); return; }
        const quote = stores.quote.get(symbol);
        if (!quote || venue === "") { toast.push({ level: "danger", text: "no venue/quote for hotkey" }); return; }
        const account = stores.exec.accounts().find((a) => a.venue === venue);
        const positionQty = stores.exec.positions().filter((p) => p.symbol === symbol && p.venue === venue).reduce((s, p) => s + p.qty, 0);
        const r = resolvePlaceTemplate(t as PlaceOrderTemplate, { venue, symbol, quote, buyingPower: account?.buyingPower ?? 0, positionQty, nowMs: Date.now() });
        for (const n of r.preCheck.notices) toast.push({ level: "warn", text: n });
        if (!r.preCheck.ok) { toast.push({ level: "danger", text: r.preCheck.errors.join(" ") }); return; }
        void oc.submit(r.args, r.flash);
        return;
      }

      // management — fires regardless of armed state (closing exposure is never gated)
      switch ((t as ManagementTemplate).action) {
        case "CancelLast": void oc.cancelLast(symbol || undefined); break;
        case "CancelAllFocused": void oc.cancelAll("focused", symbol || undefined); break;
        case "CancelAllEverything": void oc.cancelAll("everything"); break;
        case "KillSwitch": void oc.kill(); toast.push({ level: "warn", text: "KILL — cancel-all + disarm" }); break;
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [stores, linkGroups, group, oc, toast, config]);
}
```

- [ ] **Step 7: Mount `useHotkeys` in AppShell**

In `ui/src/chrome/AppShell.tsx`, import and call the hook (AppShell is inside `ToastProvider` after Task 6, and has `stores`/`commands`/`linkGroups`):

```ts
import { useHotkeys } from "./exec/useHotkeys";
```

Inside the `AppShell` component body, before the `if (!ws)` early return:

```ts
  useHotkeys({ stores, commands, linkGroups });
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `cd ui && npx vitest run src/chrome/exec/hotkeys.test.ts src/chrome/exec/useHotkeys.test.tsx && cd ui && npm run typecheck`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
cd ui && git add src/chrome/exec/hotkeys.ts src/chrome/exec/hotkeys.test.ts src/chrome/exec/useHotkeys.ts src/chrome/exec/useHotkeys.test.tsx src/chrome/AppShell.tsx
git commit -m "feat(ui/exec): hotkey engine (pure matcher + arm-gated, OS-focus-gated useHotkeys)"
```

---

## Task 14: Order settings modal (templates + hotkey bindings + gate view)

**Files:**
- Create: `ui/src/chrome/exec/OrderSettingsModal.tsx`
- Modify: `ui/src/chrome/panels/OrderTicketPanel.tsx` (a ⚙ button that opens the modal)
- Test: `ui/src/chrome/exec/OrderSettingsModal.test.tsx`

**Interfaces:**
- Consumes: `OrderConfig`/`ActionTemplate`/`PlaceOrderTemplate`/`ManagementTemplate`/`DEFAULT_TEMPLATES` (actionTemplate.ts); `normalizeCombo` (hotkeys.ts); `ExecStatus` (contract.ts); `useTheme`.
- Produces: `OrderSettingsModal({ config, status, onSave, onClose })`.

Behavior: edit each template's label + hotkey (captured by focusing a field and pressing the combo) + place recipe fields (side/type/tif/priceSource/offset/sizing); add a template; remove a template; a **read-only gate panel** listing the active per-venue caps + global limits from `ExecStatus` (`0` = unset → shown as "off"). Save persists the whole `OrderConfig` blob.

- [ ] **Step 1: Write the failing test**

Create `ui/src/chrome/exec/OrderSettingsModal.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { OrderSettingsModal } from "./OrderSettingsModal";
import { DEFAULT_ORDER_CONFIG } from "./actionTemplate";
import type { ExecStatus } from "../../wire/contract";

const status: ExecStatus = { masterArmed: true, global: { maxDayLoss: 500, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 1000, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 5 } }] };

function wrap(onSave = vi.fn(), onClose = vi.fn()) {
  render(<ThemeProvider><OrderSettingsModal config={DEFAULT_ORDER_CONFIG} status={status} onSave={onSave} onClose={onClose} /></ThemeProvider>);
  return { onSave, onClose };
}

describe("OrderSettingsModal", () => {
  it("lists templates and saves an edited label", () => {
    const { onSave } = wrap();
    const label = screen.getByTestId("tmpl-label-buy-5k") as HTMLInputElement;
    fireEvent.change(label, { target: { value: "Buy big" } });
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").label).toBe("Buy big");
  });
  it("captures a hotkey combo from a keypress", () => {
    const { onSave } = wrap();
    const cap = screen.getByTestId("tmpl-hotkey-buy-5k");
    fireEvent.keyDown(cap, { key: "7", ctrlKey: true, altKey: true });
    fireEvent.click(screen.getByTestId("save"));
    const saved = onSave.mock.calls[0][0];
    expect(saved.templates.find((t: { id: string }) => t.id === "buy-5k").hotkey).toBe("Ctrl+Alt+7");
  });
  it("adds and removes a template", () => {
    const { onSave } = wrap();
    fireEvent.click(screen.getByTestId("add-template"));
    fireEvent.click(screen.getByTestId("save"));
    expect(onSave.mock.calls[0][0].templates.length).toBe(DEFAULT_ORDER_CONFIG.templates.length + 1);
  });
  it("shows the active gate caps read-only (0 → off)", () => {
    wrap();
    expect(screen.getByText(/alpaca-paper/)).toBeTruthy();
    expect(screen.getByText(/max order value/i).textContent).toMatch(/1000/);
    expect(screen.getByText(/max position value/i).textContent).toMatch(/off/i);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/exec/OrderSettingsModal.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `OrderSettingsModal.tsx`**

Create `ui/src/chrome/exec/OrderSettingsModal.tsx`:

```tsx
import { useState } from "react";
import type { ExecStatus, Side, OrderType, TIF } from "../../wire/contract";
import type { ActionTemplate, OrderConfig, PlaceOrderTemplate } from "./actionTemplate";
import type { PriceSource } from "./priceSource";
import type { SizingMode } from "./sizing";
import { normalizeCombo } from "./hotkeys";
import { useTheme } from "../ThemeProvider";

const SIDES: Side[] = ["BUY", "SELL", "SHORT", "COVER"];
const TYPES: OrderType[] = ["LIMIT", "MARKET", "STOP", "STOP_LIMIT"];
const TIFS: TIF[] = ["DAY", "GTC", "IOC", "FOK"];
const SOURCES: PriceSource[] = ["Bid", "Ask", "Last", "Mid"];
const MODES: SizingMode[] = ["Shares", "Dollar", "BuyingPowerPct", "PositionFraction"];
const cap = (n: number) => (n === 0 ? "off" : String(n));

export function OrderSettingsModal(
  { config, status, onSave, onClose }: { config: OrderConfig; status: ExecStatus | null; onSave: (next: OrderConfig) => void; onClose: () => void },
): JSX.Element {
  const { palette } = useTheme();
  const [templates, setTemplates] = useState<ActionTemplate[]>(() => config.templates.map((t) => ({ ...t })));

  const patch = (id: string, over: Partial<ActionTemplate>) =>
    setTemplates((ts) => ts.map((t) => (t.id === id ? ({ ...t, ...over } as ActionTemplate) : t)));
  const addTemplate = () =>
    setTemplates((ts) => [...ts, { kind: "place", id: `tmpl-${ts.length + 1}-${ts.reduce((s) => s + 1, 0)}`, label: "New", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Shares", shares: 100 } } as PlaceOrderTemplate]);
  const removeTemplate = (id: string) => setTemplates((ts) => ts.filter((t) => t.id !== id));

  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, fontSize: 12, padding: "1px 4px" } as const;

  return (
    <div style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.5)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 10000 }} onClick={onClose}>
      <div onClick={(e) => e.stopPropagation()} style={{ background: palette.surface, color: palette.text, border: `1px solid ${palette.border}`, borderRadius: 6, padding: 16, width: 640, maxHeight: "80vh", overflow: "auto", fontSize: 12 }}>
        <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 8 }}>
          <strong>Order Settings — Action Templates & Hotkeys</strong>
          <button onClick={onClose} style={inp}>✕</button>
        </div>

        {templates.map((t) => (
          <div key={t.id} style={{ display: "flex", gap: 4, alignItems: "center", padding: "3px 0", borderTop: `1px solid ${palette.border}`, flexWrap: "wrap" }}>
            <input data-testid={`tmpl-label-${t.id}`} value={t.label} onChange={(e) => patch(t.id, { label: e.target.value })} style={{ ...inp, width: 110 }} />
            <span style={{ color: palette.textMuted }}>{t.kind}</span>
            {t.kind === "place" && (
              <>
                <select value={t.side} onChange={(e) => patch(t.id, { side: e.target.value as Side })} style={inp}>{SIDES.map((s) => <option key={s}>{s}</option>)}</select>
                <select value={t.type} onChange={(e) => patch(t.id, { type: e.target.value as OrderType })} style={inp}>{TYPES.map((x) => <option key={x}>{x}</option>)}</select>
                <select value={t.tif} onChange={(e) => patch(t.id, { tif: e.target.value as TIF })} style={inp}>{TIFS.map((x) => <option key={x}>{x}</option>)}</select>
                <select value={t.priceSource} onChange={(e) => patch(t.id, { priceSource: e.target.value as PriceSource })} style={inp}>{SOURCES.map((x) => <option key={x}>{x}</option>)}</select>
                <select value={t.sizing.mode} onChange={(e) => patch(t.id, { sizing: { ...t.sizing, mode: e.target.value as SizingMode } })} style={inp}>{MODES.map((x) => <option key={x}>{x}</option>)}</select>
              </>
            )}
            <input data-testid={`tmpl-hotkey-${t.id}`} readOnly value={t.hotkey ?? ""} placeholder="press keys"
              onKeyDown={(e) => { e.preventDefault(); const c = normalizeCombo(e); if (c) patch(t.id, { hotkey: c }); }} style={{ ...inp, width: 110 }} />
            <button onClick={() => removeTemplate(t.id)} style={{ ...inp, color: palette.danger, cursor: "pointer" }}>remove</button>
          </div>
        ))}

        <button data-testid="add-template" onClick={addTemplate} style={{ ...inp, marginTop: 8, cursor: "pointer" }}>+ Add template</button>

        <div style={{ marginTop: 12, borderTop: `1px solid ${palette.border}`, paddingTop: 8 }}>
          <div style={{ color: palette.textMuted, marginBottom: 4 }}>Gate limits in effect (read-only; edited engine-side)</div>
          <div>Global: max day loss <b>{cap(status?.global.maxDayLoss ?? 0)}</b> · symbol value <b>{cap(status?.global.maxSymbolPositionValue ?? 0)}</b> · symbol shares <b>{cap(status?.global.maxSymbolPositionShares ?? 0)}</b></div>
          {(status?.venues ?? []).map((v) => (
            <div key={v.venue}>{v.venue}: max order value <b>{cap(v.gate.maxOrderValue)}</b> · max position value <b>{cap(v.gate.maxPositionValue)}</b> · max position shares <b>{cap(v.gate.maxPositionShares)}</b> · max open orders <b>{cap(v.gate.maxOpenOrders)}</b></div>
          ))}
        </div>

        <div style={{ display: "flex", justifyContent: "flex-end", gap: 6, marginTop: 12 }}>
          <button onClick={onClose} style={inp}>Cancel</button>
          <button data-testid="save" onClick={() => { onSave({ ...config, templates }); onClose(); }} style={{ ...inp, background: palette.accent, color: palette.bg, fontWeight: 700, cursor: "pointer" }}>Save</button>
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Wire the modal into the ticket**

In `ui/src/chrome/panels/OrderTicketPanel.tsx`, add the import, a `showSettings` state, a ⚙ button in the header row, and render the modal. Add:

```tsx
import { OrderSettingsModal } from "../exec/OrderSettingsModal";
```

Pull `save` from the existing `useOrderConfig` call: change `const { config: orderCfg, setActiveVenue } = useOrderConfig();` to also destructure `save`:

```tsx
  const { config: orderCfg, setActiveVenue, save } = useOrderConfig(); // shared context (Task 8)
  const [showSettings, setShowSettings] = useState(false);
```

Add a gear button next to the venue selector (in the header `<div>`):

```tsx
        <button data-testid="open-settings" onClick={() => setShowSettings(true)} style={inp}>⚙</button>
```

And render the modal at the end of the component's returned tree (before the closing `</div>`):

```tsx
      {showSettings && (
        <OrderSettingsModal config={orderCfg} status={status} onSave={save} onClose={() => setShowSettings(false)} />
      )}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd ui && npx vitest run src/chrome/exec/OrderSettingsModal.test.tsx src/chrome/panels/OrderTicketPanel.test.tsx && cd ui && npm run typecheck`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/chrome/exec/OrderSettingsModal.tsx src/chrome/exec/OrderSettingsModal.test.tsx src/chrome/panels/OrderTicketPanel.tsx
git commit -m "feat(ui/exec): order settings modal (template/hotkey CRUD + read-only gate view)"
```

---

## Task 15: Fills-on-chart — query/result wire primitive + FillStore + ChartPanel wire

Closes Plan 2's deferred live fills→chart wire. Adds a correlated request/response (`query`/`result`) to the wire for chart-open fill backfill.

**Files:**
- Modify: `ui/src/wire/contract.ts` (`QueryMsg`, `ResultMsg`, add to unions)
- Modify: `ui/src/wire/codec.ts` (decode `result`)
- Modify: `ui/src/wire/WsClient.ts` (`sendQuery`)
- Create: `ui/src/data/FillStore.ts`
- Modify: `ui/src/data/registry.ts` (add `fills`, route `exec.fills` → `FillStore`)
- Modify: `ui/src/chrome/panels/registry.tsx` (widen `commands` with `sendQuery`); `ui/src/App.tsx` (provide `sendQuery`); `ui/src/chrome/panels/ChartPanel.tsx` (fills wire)
- Test: `ui/src/data/FillStore.test.ts`
- Test: `ui/src/wire/WsClient.test.ts` (add a `sendQuery` case)

**Interfaces:**
- Consumes: `Fill` (contract.ts); `sideIsSell` (wire/orderStatus.ts); `ChartController.setFills` (accepts the structurally-identical `FillPoint`).
- Produces: `interface QueryMsg`, `interface ResultMsg`; `WsClient.sendQuery(name, args): Promise<unknown>`; `interface FillPoint` + `class FillStore extends PaintStore` with `apply(m)`, `ingest(fills: Fill[])`, `forSymbol(symbol): FillPoint[]`; widened `PanelProps.commands` (`sendQuery`).

- [ ] **Step 1: Add the query/result contract + codec decode**

In `ui/src/wire/contract.ts`, add to the server→client and client→server sections:

```ts
export interface ResultMsg { kind: "result"; corrId: string; payload: unknown }
export type ServerMessage = SnapshotMsg | DeltaMsg | AckMsg | PongMsg | ResultMsg;
```
```ts
export interface QueryMsg { kind: "query"; corrId: string; name: string; args: unknown }
export type ClientMessage = SubscribeMsg | UnsubscribeMsg | CommandMsg | QueryMsg | PingMsg;
```

In `ui/src/wire/codec.ts`, add `"result"` to the accepted server kinds:

```ts
const SERVER_KINDS = new Set(["snapshot", "delta", "ack", "pong", "result"]);
```

- [ ] **Step 2: Write the failing FillStore test**

Create `ui/src/data/FillStore.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { FillStore } from "./FillStore";
import type { Fill } from "../wire/contract";

const fill = (o: Partial<Fill>): Fill => ({ venue: "alpaca-paper", orderId: "ET1", symbol: "US.AAPL", side: "BUY", qty: 10, price: 3.5, tsMs: 1000, ...o });
const snap = (payload: Fill[]) => ({ kind: "snapshot" as const, topic: "exec.fills" as never, payload });
const delta = (payload: Fill) => ({ kind: "delta" as const, topic: "exec.fills" as never, payload });

describe("FillStore", () => {
  it("buckets fills by symbol and maps to buy/sell FillMarkers", () => {
    const s = new FillStore();
    s.apply(snap([fill({ tsMs: 1000, price: 3.5, side: "BUY" }), fill({ symbol: "US.NVDA", tsMs: 1100, price: 9, side: "SELL" })]));
    expect(s.forSymbol("US.AAPL")).toEqual([{ timeMs: 1000, price: 3.5, side: "buy" }]);
    expect(s.forSymbol("US.NVDA")).toEqual([{ timeMs: 1100, price: 9, side: "sell" }]);
    expect(s.forSymbol("US.TSLA")).toEqual([]);
  });
  it("SHORT/COVER map to sell/buy", () => {
    const s = new FillStore();
    s.apply(delta(fill({ side: "SHORT", orderId: "ET2" })));
    s.apply(delta(fill({ side: "COVER", orderId: "ET3", tsMs: 1200 })));
    expect(s.forSymbol("US.AAPL").map((m) => m.side)).toEqual(["sell", "buy"]);
  });
  it("append-only, deduped by identity (a re-snapshot never doubles or wipes)", () => {
    const s = new FillStore();
    const f = fill({ orderId: "ET1", tsMs: 1000, price: 3.5, qty: 10 });
    s.ingest([f]);
    s.ingest([f]);                                  // duplicate — ignored
    s.ingest([fill({ orderId: "ET4", symbol: "US.MSFT", tsMs: 1300 })]);
    s.apply(snap([f]));                             // reconnect re-snapshot merges, doesn't wipe MSFT
    expect(s.forSymbol("US.AAPL")).toHaveLength(1);
    expect(s.forSymbol("US.MSFT")).toHaveLength(1);
  });
  it("keeps each symbol's markers sorted by time", () => {
    const s = new FillStore();
    s.ingest([fill({ orderId: "b", tsMs: 2000 }), fill({ orderId: "a", tsMs: 1000 })]);
    expect(s.forSymbol("US.AAPL").map((m) => m.timeMs)).toEqual([1000, 2000]);
  });
});
```

- [ ] **Step 3: Run the FillStore test to verify it fails**

Run: `cd ui && npx vitest run src/data/FillStore.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 4: Implement `FillStore.ts`**

Create `ui/src/data/FillStore.ts`:

```ts
import { PaintStore } from "./store";
import type { Fill, SnapshotMsg, DeltaMsg } from "../wire/contract";
import { sideIsSell } from "../wire/orderStatus";

// A fill marker as consumed by the chart. Declared structurally here (not imported
// from render/chart/diamondMarker) so data/ never imports render/ — the shape is
// identical to render's FillMarker, so ChartController.setFills accepts it.
export interface FillPoint { timeMs: number; price: number; side: "buy" | "sell" }

// Fills are append-only events (not a replaceable snapshot): a reconnect
// re-snapshot MERGES + dedupes rather than wiping backfilled history. Bucketed by
// symbol; forSymbol() maps to the chart's fill-marker shape.
const key = (f: Fill) => `${f.venue}|${f.orderId}|${f.tsMs}|${f.price}|${f.qty}`;

export class FillStore extends PaintStore {
  private readonly bySymbol = new Map<string, Fill[]>();
  private readonly seen = new Set<string>();

  apply(m: SnapshotMsg | DeltaMsg): void {
    this.ingest(m.kind === "snapshot" ? (m.payload as Fill[]) : [m.payload as Fill]);
  }

  ingest(fills: Fill[]): void {
    let changed = false;
    for (const f of fills) {
      const k = key(f);
      if (this.seen.has(k)) continue;
      this.seen.add(k);
      const arr = this.bySymbol.get(f.symbol) ?? [];
      arr.push(f);
      arr.sort((a, b) => a.tsMs - b.tsMs);
      this.bySymbol.set(f.symbol, arr);
      changed = true;
    }
    if (changed) this.markDirty();
  }

  forSymbol(symbol: string): FillPoint[] {
    return (this.bySymbol.get(symbol) ?? []).map((f) => ({ timeMs: f.tsMs, price: f.price, side: sideIsSell(f.side) ? "sell" : "buy" }));
  }
}
```

- [ ] **Step 5: Register FillStore + route `exec.fills`**

In `ui/src/data/registry.ts`: import `FillStore`, add `fills: FillStore` to `Stores`/`makeStores()`, and change the `exec.fills` route to `stores.fills.apply(m)` (remove it from the `exec.account/positions/orders/status → stores.exec` group):

```ts
import { FillStore } from "./FillStore";
```
```ts
  fills: FillStore;
```
```ts
    fills: new FillStore(),
```
```ts
    case "exec.account":
    case "exec.positions":
    case "exec.orders":
    case "exec.status": stores.exec.apply(m); return;
    case "exec.fills": stores.fills.apply(m); return;
```

- [ ] **Step 6: Add `sendQuery` to WsClient (+ test)**

In `ui/src/wire/WsClient.ts`: import `ResultMsg`; add a `pendingQueries` map and a `sendQuery` method; handle the `result` case in `onMessage`; buffer queries like commands while closed. Add:

```ts
import type { AckMsg, ClientMessage, DeltaMsg, ResultMsg, ServerMessage, SnapshotMsg, TopicName } from "./contract";
```
```ts
  private readonly pendingQueries = new Map<string, (payload: unknown) => void>();
```
```ts
  sendQuery(name: string, args: unknown): Promise<unknown> {
    const corrId = `q${++this.corr}`;
    return new Promise<unknown>((resolve) => {
      this.pendingQueries.set(corrId, resolve);
      this.sendRaw({ kind: "query", corrId, name, args });
    });
  }
```

In `onMessage`, add a case:

```ts
      case "result": {
        const resolve = this.pendingQueries.get(msg.corrId);
        if (resolve) { this.pendingQueries.delete(msg.corrId); resolve(msg.payload); }
        return;
      }
```

In `sendRaw`, buffer queries too (so a query fired while reconnecting flushes on open):

```ts
    if (msg.kind === "command" || msg.kind === "query") this.outbox.push(encodeClientMessage(msg));
```

In `ui/src/wire/WsClient.test.ts`, add a case using the file's existing `FakeSocket` helper (from `ui/test/fakes.ts` — `FakeSocket.last()` returns the most-recent socket; `.open()`, `.sent`, `.emit(raw)`). Read the top of the file for the exact `makeClient()` helper; a faithful case:

```ts
it("sendQuery resolves with the correlated result payload", async () => {
  const { client } = makeClient();       // the file's existing helper (WsClient + FakeSocket factory)
  client.start();
  FakeSocket.last().open();
  const p = client.sendQuery("QueryFills", { symbol: "US.AAPL", fromMs: 0, toMs: 9 });
  const sent = JSON.parse(FakeSocket.last().sent.at(-1)!);
  expect(sent.kind).toBe("query");
  FakeSocket.last().emit(JSON.stringify({ kind: "result", corrId: sent.corrId,
    payload: [{ venue: "v", orderId: "ET1", symbol: "US.AAPL", side: "BUY", qty: 1, price: 3.5, tsMs: 5 }] }));
  await expect(p).resolves.toHaveLength(1);
});
```

(Read the corrId back from the sent frame — `sent.corrId` — rather than hardcoding `"q1"`, so the test doesn't couple to the shared `corr` counter's state.)

- [ ] **Step 7: Provide `sendQuery` through the panel props**

In `ui/src/chrome/panels/registry.tsx`, widen `PanelProps.commands`:

```ts
  commands: { sendCommand(name: string, args: unknown): Promise<AckMsg>; sendQuery(name: string, args: unknown): Promise<unknown> };
```

In `ui/src/App.tsx`, extend the `commands` object:

```ts
  const commands = {
    sendCommand: (name: string, args: unknown) => client.sendCommand(name, args),
    sendQuery: (name: string, args: unknown) => client.sendQuery(name, args),
  };
```

(`ThemeProvider`/`WorkspaceStore`/`ChartController.CommandSender` keep their narrower `{ sendCommand }` types — the widened object still satisfies them structurally.)

Now that `commands` requires `sendQuery`, the pre-existing panel tests that pass `commands` **as a direct JSX prop** (`ChartPanel.test.tsx`, `LadderPanel.test.tsx`, `TapePanel.test.tsx`) fail typecheck for the missing field — and `ChartPanel` fires `commands.sendQuery("QueryFills", …)` on mount (Step 8), so `ChartPanel.test.tsx` would also crash at runtime. Add `sendQuery` to each of those three stubs:

```ts
// alongside the existing sendCommand in each stub:
sendQuery: vi.fn(async () => []),
```

(The whole-object `as PanelProps` casts elsewhere — e.g. `NewsPanel.test.tsx`, `ScannerPanel.test.tsx` — tolerate the missing `sendQuery`, so they need no change here.) Also ensure `ChartPanel.test.tsx`'s `stores` come from `makeStores()` (Task 15 added `fills` to it) so `stores.fills` is defined at paint time; if that test builds a partial stores object by hand, add a `FillStore` to it.

- [ ] **Step 8: Wire fills into ChartPanel**

In `ui/src/chrome/panels/ChartPanel.tsx`: track a fills-rev cursor in the scheduler surface, push `stores.fills.forSymbol(currentSymbol)` into the controller each dirty frame, and fire a backfill query when the symbol (re)points. In the `useEffect` that registers the surface, keep a mutable closure variable `currentSymbol` updated by `applySymbol`, add a fills cursor to `isDirty`, and call `setFills` in `paint`:

```tsx
    let currentSymbol = linkGroups.symbolFor(config.group) ?? symbol;
    const backfillFills = (sym: string) => {
      controller.setFills(stores.fills.forSymbol(sym));
      void commands.sendQuery("QueryFills", { symbol: sym, fromMs: 0, toMs: Date.now() })
        .then((payload) => { stores.fills.ingest((payload as Parameters<typeof stores.fills.ingest>[0]) ?? []); });
    };
    const applySymbol = () => {
      currentSymbol = linkGroups.symbolFor(config.group) ?? symbol;
      controller.setSymbol(currentSymbol);
      backfillFills(currentSymbol);
    };
    applySymbol();
    const offLink = linkGroups.subscribe(applySymbol);
```

Change the surface to include the fills cursor and push markers on paint:

```tsx
    let lastBarsRev = -1;
    let lastIndicatorsRev = -1;
    let lastFillsRev = -1;
    const off = scheduler.register({
      id: `chart:${config.id}`,
      isDirty: () => {
        const barsRev = stores.bars.getRev();
        const indicatorsRev = stores.indicators.getRev();
        const fillsRev = stores.fills.getRev();
        const changed = barsRev !== lastBarsRev || indicatorsRev !== lastIndicatorsRev || fillsRev !== lastFillsRev;
        lastBarsRev = barsRev; lastIndicatorsRev = indicatorsRev; lastFillsRev = fillsRev;
        return changed;
      },
      paint: () => { controller.sync(); controller.setFills(stores.fills.forSymbol(currentSymbol)); },
    });
```

(`ChartController` already imports `FillMarker` and exposes `setFills`; no controller change is needed. The `commands` prop now carries `sendQuery`.)

- [ ] **Step 9: Run the tests to verify they pass**

Run: `cd ui && npx vitest run src/data/FillStore.test.ts src/wire/WsClient.test.ts src/data/registry.test.ts && cd ui && npm run typecheck`
Expected: PASS. Then `cd ui && npm run test:golden` — expected PASS (ChartPanel changes are imperative; no painter-fixture change).

- [ ] **Step 10: Commit**

```bash
cd ui && git add src/wire/contract.ts src/wire/codec.ts src/wire/WsClient.ts src/wire/WsClient.test.ts src/data/FillStore.ts src/data/FillStore.test.ts src/data/registry.ts src/chrome/panels/registry.tsx src/App.tsx src/chrome/panels/ChartPanel.tsx src/chrome/panels/ChartPanel.test.tsx src/chrome/panels/LadderPanel.test.tsx src/chrome/panels/TapePanel.test.tsx
git commit -m "feat(ui/exec): fills-on-chart — query/result primitive + FillStore + live chart wire"
```

---

## Task 16: Exec fixture + mock-engine command/query handling + dev-app checklist

**Files:**
- Create: `ui/fixtures/exec-session.json`
- Modify: `ui/mock-engine/server.ts` (optional `onCommand`/`onQuery` hooks)
- Modify: `ui/mock-engine/run.ts` (exec responders: order-lifecycle walk, arm toggles, `QueryFills`)
- Test: `ui/mock-engine/server.test.ts` (add command-orderId + query cases)

**Interfaces:**
- Consumes: existing `Fixture`/`startMockEngine`.
- Produces: extended `startMockEngine(opts)` with optional `onCommand?(msg, send)` / `onQuery?(msg, send)`, where `send(serverMsg, afterMs?)` emits a frame.

- [ ] **Step 1: Write the exec fixture**

Create `ui/fixtures/exec-session.json` (one venue, an armed status, an account, a working order + a fill so the chart shows a diamond on load):

```json
{
  "snapshots": [
    { "topic": "exec.status", "payload": { "masterArmed": true, "global": { "maxDayLoss": 500, "maxSymbolPositionValue": 0, "maxSymbolPositionShares": 0 }, "venues": [{ "venue": "alpaca-paper", "broker": "alpaca", "connected": true, "venueArmed": true, "reconcilePending": false, "note": "", "lastReconcileMs": null, "gate": { "maxOrderValue": 1000, "maxPositionValue": 0, "maxPositionShares": 0, "maxOpenOrders": 5 } }] } },
    { "topic": "exec.account", "key": "alpaca-paper", "payload": { "venue": "alpaca-paper", "equity": 25000, "buyingPower": 100000, "availableCash": 25000, "sodEquity": 25000, "realized": 0, "dayPnl": 0, "leverage": 4, "tsMs": 0 } },
    { "topic": "exec.positions", "payload": [{ "venue": "alpaca-paper", "symbol": "US.AAPL", "qty": 300, "avgPrice": 3.40, "unrealizedPnl": 30 }, { "venue": null, "symbol": "US.AAPL", "qty": 300, "avgPrice": 3.40, "unrealizedPnl": 30 }] },
    { "topic": "exec.orders", "payload": [{ "venue": "alpaca-paper", "id": "ET-seed-1", "symbol": "US.AAPL", "side": "BUY", "type": "LIMIT", "tif": "DAY", "qty": 100, "limitPrice": 3.45, "stopPrice": 0, "status": "ACCEPTED", "executedQty": 0, "leavesQty": 100, "avgFillPrice": 0, "rejectReason": "", "replacesId": "", "createdMs": 0, "updatedMs": 0 }] },
    { "topic": "exec.fills", "payload": [{ "venue": "alpaca-paper", "orderId": "ET-seed-0", "symbol": "US.AAPL", "side": "BUY", "qty": 300, "price": 3.40, "tsMs": 0 }] }
  ],
  "deltas": [
    { "afterMs": 2000, "topic": "exec.account", "key": "alpaca-paper", "payload": { "venue": "alpaca-paper", "equity": 25030, "buyingPower": 100000, "availableCash": 25000, "sodEquity": 25000, "realized": 0, "dayPnl": 30, "leverage": 4, "tsMs": 2000 } }
  ]
}
```

- [ ] **Step 2: Write failing mock-engine tests**

`ui/mock-engine/server.test.ts` uses the real `ws` `WebSocket` against a live port plus a `collect(ws, n, timeoutMs)` helper and an `afterEach(() => handle?.close())`. Read the file's existing `"acks a command"` test for the exact open/collect plumbing, then add (following that shape):

```ts
it("routes commands through onCommand (orderId ack + emitted event)", async () => {
  handle = startMockEngine({ port: PORT, fixture: { snapshots: [], deltas: [] },
    onCommand: (msg, send) => {
      send({ kind: "ack", corrId: msg.corrId, status: "accepted", orderId: "ET-mock-1" });
      send({ kind: "delta", topic: "exec.orders", key: "ET-mock-1", payload: { id: "ET-mock-1", status: "SUBMITTED" } }, 5);
    } });
  const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
  await new Promise((r) => ws.on("open", r));
  const got = collect(ws, 2);
  ws.send(JSON.stringify({ kind: "command", corrId: "c1", name: "SubmitOrder", args: {} }));
  const msgs = await got;
  expect(msgs[0]).toMatchObject({ kind: "ack", corrId: "c1", orderId: "ET-mock-1" });
  expect(msgs[1]).toMatchObject({ kind: "delta", topic: "exec.orders", key: "ET-mock-1" });
});

it("answers a query via onQuery with a correlated result", async () => {
  handle = startMockEngine({ port: PORT, fixture: { snapshots: [], deltas: [] },
    onQuery: (msg, send) => send({ kind: "result", corrId: msg.corrId, payload: [] }) });
  const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
  await new Promise((r) => ws.on("open", r));
  const got = collect(ws, 1);
  ws.send(JSON.stringify({ kind: "query", corrId: "q1", name: "QueryFills", args: {} }));
  expect((await got)[0]).toMatchObject({ kind: "result", corrId: "q1" });
});

it("defaults an unhandled query to an empty result (no dangling promise)", async () => {
  handle = startMockEngine({ port: PORT, fixture: { snapshots: [], deltas: [] } }); // no onQuery
  const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
  await new Promise((r) => ws.on("open", r));
  const got = collect(ws, 1);
  ws.send(JSON.stringify({ kind: "query", corrId: "q9", name: "QueryFills", args: {} }));
  expect((await got)[0]).toMatchObject({ kind: "result", corrId: "q9", payload: [] });
});
```

(Match `PORT`/`collect`/`handle` to the file's existing declarations.)

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd ui && npx vitest run mock-engine/server.test.ts`
Expected: FAIL — `onCommand`/`onQuery` not honored yet.

- [ ] **Step 4: Extend the mock engine**

In `ui/mock-engine/server.ts`, add optional hooks and a `send` helper. Change the `opts` type and the message handler:

```ts
export function startMockEngine(opts: {
  port: number; fixture: Fixture;
  onCommand?: (msg: { kind: "command"; corrId?: string; name?: string; args?: unknown }, send: (m: unknown, afterMs?: number) => void) => void;
  onQuery?: (msg: { kind: "query"; corrId?: string; name?: string; args?: unknown }, send: (m: unknown, afterMs?: number) => void) => void;
}): { close: () => Promise<void> } {
```

Inside `wss.on("connection", …)`, define a `send` bound to this socket and route commands/queries through the hooks (keeping the default ack when no hook is given):

```ts
    const send = (m: unknown, afterMs = 0) => {
      if (afterMs <= 0) { if (ws.readyState === ws.OPEN) ws.send(JSON.stringify(m)); return; }
      track(() => { if (!dropped && ws.readyState === ws.OPEN) ws.send(JSON.stringify(m)); }, afterMs);
    };
```
```ts
      if (msg.kind === "query") {
        if (opts.onQuery) opts.onQuery(msg as never, send);
        else send({ kind: "result", corrId: msg.corrId, payload: [] }); // default: empty result, never a dangling promise
        return;
      }
      if (msg.kind === "command") {
        if (opts.onCommand) opts.onCommand(msg as never, send);
        else ws.send(JSON.stringify({ kind: "ack", corrId: msg.corrId, status: "accepted" }));
        return;
      }
```

(The default empty-result matters: Task 15's `ChartPanel` fires `QueryFills` on every chart open, including under the Plan 2–4 fixtures that never wire `onQuery` — without a default the `sendQuery` promise would dangle forever.)

(The `msg` local's type union must gain `name?: string`; widen it to `{ kind?: string; topic?: string; corrId?: string; name?: string; args?: unknown; t?: number }`.)

- [ ] **Step 5: Wire the exec responders in run.ts**

In `ui/mock-engine/run.ts`, when the selected fixture is the exec session, pass `onCommand`/`onQuery` **inline in the `startMockEngine({ … })` call** so they pick up contextual typing (free-standing `const onCommand = (msg, send) => …` would trip `noImplicitAny` under the repo's `strict` tsconfig). Use a module-scoped counter for ids (`Date.now` is unavailable to reason about deterministically; a counter is enough). The responders: (a) ack `SubmitOrder` with a counter `orderId` then walk `SUBMITTED → ACCEPTED → FILLED` on `exec.orders` + one `exec.fills` delta; (b) ack `Arm`/`Disarm`/`Cancel`/`KillSwitch`; (c) answer `QueryFills` with a small canned `Fill[]`. Gate the responders to the exec fixture (other fixtures keep the default ack + default empty result):

```ts
let seq = 0;
const isExec = /* however run.ts selects the fixture, true when it is exec-session */;
startMockEngine({
  port, fixture,
  onCommand: !isExec ? undefined : (msg, send) => {
    if (msg.name === "SubmitOrder") {
      const id = `ET-mock-${++seq}`;
      const a = msg.args as { venue: string; symbol: string; side: string; type: string; tif: string; qty: number; limitPrice: number; stopPrice: number };
      send({ kind: "ack", corrId: msg.corrId, status: "accepted", orderId: id });
      const mk = (status: string, over: Record<string, unknown> = {}) => ({ kind: "delta", topic: "exec.orders", key: id,
        payload: { venue: a.venue, id, symbol: a.symbol, side: a.side, type: a.type, tif: a.tif, qty: a.qty, limitPrice: a.limitPrice, stopPrice: a.stopPrice, status, executedQty: 0, leavesQty: a.qty, avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 0, updatedMs: 0, ...over } });
      send(mk("SUBMITTED"), 150);
      send(mk("ACCEPTED"), 400);
      send(mk("FILLED", { executedQty: a.qty, leavesQty: 0, avgFillPrice: a.limitPrice }), 900);
      send({ kind: "delta", topic: "exec.fills", payload: { venue: a.venue, orderId: id, symbol: a.symbol, side: a.side, qty: a.qty, price: a.limitPrice, tsMs: 900 } }, 900);
      return;
    }
    send({ kind: "ack", corrId: msg.corrId, status: "accepted" }); // Arm/Disarm/Cancel/Kill
  },
  onQuery: !isExec ? undefined : (msg, send) => {
    if (msg.name === "QueryFills") { const a = msg.args as { symbol: string }; send({ kind: "result", corrId: msg.corrId, payload: [{ venue: "alpaca-paper", orderId: "ET-seed-0", symbol: a.symbol, side: "BUY", qty: 300, price: 3.40, tsMs: 0 }] }); return; }
    send({ kind: "result", corrId: msg.corrId, payload: [] });
  },
});
```
(Adapt the `startMockEngine({ … })` call to `run.ts`'s existing fixture-selection code; the point is the responders are inline properties, not untyped free consts.)

- [ ] **Step 6: Run the mock-engine tests + typecheck**

Run: `cd ui && npx vitest run mock-engine/server.test.ts && cd ui && npm run typecheck`
Expected: PASS.

- [ ] **Step 7: Dev-app manual checklist**

Run the mock engine on the exec fixture + Vite, open the Trading workspace, and confirm by eye (record results in the commit body):

1. Account bar shows aggregate equity/BP/day-P&L, the **ARMED** toggle reflects `exec.status`, and per-venue dots are green.
2. Positions shows the seeded AAPL row + the NET row; **Flatten** on the AAPL row submits a SELL (watch the orders panel).
3. Order ticket follows the green group's symbol, shows live bid/ask (click a quote → price field seeds), and **Submit** walks a new order `Pending → Submitted → Accepted → Filled` in the orders panel.
4. A blocked submit (disarm first via the account bar toggle, then Submit) raises a **danger toast with the verbatim gate reason** and sends nothing new.
5. Hotkey `Ctrl+1` fires the "Buy $5k" preset only while armed; while disarmed it flashes "disarmed"; `Ctrl+Shift+K` (kill) fires regardless of armed.
6. The chart shows a **buy diamond** at 3.40 (seeded fill) and a new diamond after a filled ticket order.
7. ⚙ opens the settings modal; edit a label + capture a hotkey + Save; reopen → persisted (mock config round-trip is best-effort; verify the in-session state at minimum).
8. Break one panel (throw in a panel body) → its inline error card shows; the rest of the workspace keeps trading (error-boundary isolation).

- [ ] **Step 8: Commit**

```bash
cd ui && git add fixtures/exec-session.json mock-engine/server.ts mock-engine/server.test.ts mock-engine/run.ts
git commit -m "test(ui/exec): exec fixture + mock-engine command/query handling (order-walk + QueryFills)"
```

---

## Task 17: Integration sweep + plan close-out

**Files:**
- Modify: `ui/src/chrome/panels/registry.tsx` (registry ledger comment)
- Modify: `ui/src/seeds/workspaces.ts` (seed comment)

- [ ] **Step 1: Full verification suite**

Run each and confirm PASS:

```bash
cd ui && npm run typecheck
cd ui && npm run lint
cd ui && npm run test
cd ui && npm run test:golden
cd ui && npm run build
```

Expected: all PASS. If lint flags the exec files, fix in place (no new `eslint-disable`).

- [ ] **Step 2: Confirm no stray inline hex (palette single-source rule)**

Run: `cd ui && grep -rnE "#[0-9a-fA-F]{3,6}" src/chrome/exec src/chrome/panels/AccountBarPanel.tsx src/chrome/panels/PositionsPanel.tsx src/chrome/panels/OpenOrdersPanel.tsx src/chrome/panels/OrderTicketPanel.tsx src/chrome/Toast.tsx`
Expected: the only matches are the kill switch's deliberate `#fff`/`#000` in `OrderTicketPanel.tsx` (a "deliberately ugly, always visible" control, per the UI spec). Everything else draws from `palette.*`. `Toast.tsx`'s drop-shadow uses `rgba(0,0,0,.25)` (not a palette token, but `rgba` — it won't match this hex grep; leave it). Any *other* hex must be moved onto `palette.*`.

- [ ] **Step 3: Update the registry ledger + seed comments**

In `ui/src/chrome/panels/registry.tsx`, rewrite the ledger comment:

```ts
// Plan 1 registered the two stack-proving panels; Plan 2 added the chart panel;
// Plan 3 added the L2 ladder + time & sales; Plan 4 added scanner / movers / news;
// Plan 5 adds the execution surfaces (account-bar / positions / open-orders /
// order-ticket). Plan 6 owns Playwright smoke E2E + ui/dist static serving.
```

In `ui/src/seeds/workspaces.ts`, update the header comment: all seeded panelIds are now registered (no remaining "coming soon" placeholders in the Trading workspace).

- [ ] **Step 4: Confirm the Trading workspace renders every panel live**

Run the mock engine (exec fixture) + `npm run dev`, open `?workspace=trading`, and confirm all ten seeded panels render real components (no "coming in a later plan" placeholder), both light and dark theme (toggle in the header), across a drag/resize (panels keep their canvas/state — stable keys).

- [ ] **Step 5: Self-review against the spec**

Check the UI design spec's "Order entry & hotkeys" + the four Trading panels against the built code:

- Account bar: equity / BP / day-P&L / realized / armed+arm control / connection dots — **present** (Task 9). Note deferred: click-dot-opens-panel (needs dockview api threading).
- Positions: live unrealized P&L + per-row flatten routed through the gate — **present** (Task 10).
- Open orders: 9 domain states + `PendingNew`/`Replacing` display, per-row cancel, cancel-all, verbatim reject reasons, StreamGap badge — **present** (Task 11).
- Order ticket: focused symbol, live bid/ask + click-to-seed, side/type/price/qty+sizing/TIF, presets, cancel-all, kill switch — **present** (Task 12).
- Hotkeys: armed-gated + OS-focus-gated place, always-fire management, resolved-order flash — **present** (Task 13).
- Sizing math, price-source, pre-checks (RTH coercion, inverted stop-limit), template resolution — **present + densely tested** (Tasks 3–5).
- Fills-on-chart — **present** (Task 15).

Fix any gap found inline; add a task if a spec requirement has no code.

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/chrome/panels/registry.tsx src/seeds/workspaces.ts
git commit -m "chore(ui/exec): Plan 5 close-out — registry ledger + seed comments; full-suite green"
```

---

## Notes for the executor

- **Engine does not exist yet.** Every exec type in `wire/contract.ts` is interim, hand-authored to match `engine/internal/exec` field names from `docs/superpowers/plans/2026-07-05-engine-execution-core.md`. When the engine + `uihub/wsmsg` + tygo land, `ui/src/gen/*` supersedes these; keep field names identical so it is a drop-in.
- **`OrderType` includes STOP / STOP_LIMIT** per the UI design's ticket + pre-checks, even though the engine execution-core plan's `OrderType` extraction listed only Market/Limit. The `Order` struct already carries `StopPrice`; the UI contract models all four and the engine must match (flag to the engine author if it does not).
- **Dynamic per-mount topic subscription** (the `App.tsx` "Plan 4/5 make this dynamic" note) stays deferred: v1 has no Add-Panel menu, so the seed-derived static topic union is sufficient once the four exec panels are registered (their topics flow into the union automatically). Do not add dynamic subscription in this plan.
- **Cancel-all / cancel-last are UI-composed** from `CancelOrder` over the working set; the engine's per-venue token buckets pace the burst. There is no dedicated `CancelAll` engine command.
- **Kill never flattens** — cancel-all + disarm only. Per-row/position flatten is a separate gate-routed order.
- **Hotkey link group is fixed to `"green"`** in v1 (`useHotkeys` default; the trading seed's ticket + charts are on `green`). It is not derived from the visible ticket panel's group, so if a future workspace puts the ticket on a different group, pass that group to `useHotkeys` (or thread it from the ticket) — flagged, not handled, this plan.
- **`OrderConfig` is one shared context per window** (`OrderConfigProvider` in `App.tsx`). The settings modal, the ticket presets, and the hotkey engine all read/write the same instance, so a newly-bound hotkey or edited template takes effect immediately (no reload). Do not revert `useOrderConfig` to per-call-site `useState` — that silently desyncs the hotkey engine from edits.

