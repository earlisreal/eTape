# Extended-hours market orders — marketable-limit conversion

**Date:** 2026-07-11
**Status:** Approved

## Problem

US brokers do not accept market orders outside regular trading hours: TradeZero
hard-rejects them (R78), Alpaca silently queues them until the next 09:30 open —
arguably worse, because the trader thinks nothing happened. eTape's existing
safety net (`ui/src/chrome/exec/preChecks.ts:36`) coerces MARKET → LIMIT at the
**last trade price with no buffer**. Limit-at-last does not guarantee a fill: on
a pre-market gapper that has ticked up since the last print, a buy rests
unfilled and the entry is missed.

Goal: a MARKET order placed outside RTH should behave like a market order —
immediate fill against the current book — while keeping a bounded worst case,
which a naked market order in a thin extended-hours book cannot offer.

## Decisions

- **No new `OrderType`.** MARKET stays the user-facing intent; the conversion to
  an aggressive marketable limit is behavior. Brokers have no native
  "marketable" type anyway — it would always decompose to a limit on the wire.
- **Percentage buffer, global setting, default 1.0%.** A % buffer scales with
  price (the venue-latency-benchmark lesson already encoded in
  `priceSource.ts`); a flat cent offset is too tight on a $200 stock and too
  loose on a $2 one.
- **Extended hours only.** During RTH, MARKET orders pass through unchanged.
- **UI converts, engine backstops.** The UI has bid/ask at order-build time
  (`Quote` in the resolve context); the engine's exec path has no bid/ask seam
  (`MarkSource` exposes last-trade only). Conversion happens UI-side; the
  engine blocks any raw MARKET that reaches it outside RTH so nothing silently
  queues at Alpaca.

## Design

### 1. UI conversion (`ui/src/chrome/exec/preChecks.ts`)

Upgrade the existing coercion in place. Trigger is unchanged and stays keyed on
the **actual clock** (`sessionAt(nowMs) !== "rth"`), never on `order.session` —
the existing rationale comment (broker-safety net independent of the trader's
session choice) still applies verbatim and stays.

Signature change: `preCheck(draft, last, nowMs)` becomes

```ts
preCheck(draft: DraftOrder, quote: { bid: number; ask: number; last: number },
         nowMs: number, extBufferPct: number): PreCheckResult
```

(pure, as today). Both call sites already hold a full `Quote`:
`resolveTemplate.ts:25` (has `ctx.quote`) and `OrderTicketPanel.tsx:82` (has
`useThrottledQuote`'s quote).

Conversion math, for `type === "MARKET"` outside RTH:

- **Buyish (BUY/COVER):** base = `ask` if `ask > 0`, else `last`;
  `limitPrice = roundUpToTick(base × (1 + extBufferPct/100))`.
- **Sellish (SELL/SHORT):** base = `bid` if `bid > 0`, else `last`;
  `limitPrice = roundDownToTick(base × (1 − extBufferPct/100))`.
- **Tick rounding:** $0.01 for prices ≥ $1.00, $0.0001 below $1.00 (SEC
  sub-penny rule). Buys round **up**, sells round **down**, so a converted
  price is never rejected for an invalid increment and never loses its
  marketability to rounding. New small helper in `preChecks.ts` (no existing
  tick-rounding utility in the codebase).
- **One-sided book** (no ask on a buy / no bid on a sell): fall back to `last`
  as the base; the notice says so.
- **No usable base at all** (relevant side and last both ≤ 0): blocking error,
  same as today's no-last error.

Notices (existing non-blocking notice mechanism):

- Normal: `Market outside RTH → Limit @ 12.47 (ask +1%).`
- Fallback: `Market outside RTH → Limit @ 12.47 (no ask; last +1%).`

The resolved flash string (`resolveTemplate.ts:31`) already reads the coerced
order, so hotkey flashes show the concrete limit price automatically.

### 2. Engine backstop (`engine/internal/exec/core.go`)

New `Capabilities` field (`engine/internal/exec/broker.go:7`):

```go
MarketOutsideRTH bool // sim only — real brokers require limits outside RTH
```

`broker/sim` sets it true; alpaca, tradezero, and stub leave it false.

In `Core.handleSubmit`, beside the existing overnight-capability gate
(`core.go:356`), after `b := c.brokers[req.Venue]`:

```go
if req.Type == TypeMarket && session.PhaseAt(c.clk.Now()) != session.RTH &&
    (b == nil || !b.Capabilities().MarketOutsideRTH) {
    // → OrderBlocked "market order outside regular hours (UI converts these
    //   to marketable limits)" — same appendAndFold pattern as the overnight gate
}
```

- Block, don't convert — the engine has no bid/ask, and a correct client never
  sends a raw MARKET outside RTH. The backstop exists so a bug or bypassing
  client gets a loud `OrderBlocked` instead of Alpaca's silent queue-to-open.
- **Sim venues are exempt** (via the capability): replay/practice sessions run
  at night by definition and sim fills market orders fine. UI conversion still
  applies to sim orders, but a marketable limit crosses the sim book the same
  way, so practice behavior is unchanged.
- `exec` already imports `session`; the Core's injectable `clock.Clock`
  (`core.go:80,103`) makes the check testable.

### 3. Buffer setting

- New optional field on `OrderConfig` (`ui/src/chrome/exec/actionTemplate.ts:23`):
  `extHoursMarketBufferPct?: number`, default **1.0**, clamped to **[0.1, 10]**.
  Defaulting/clamping happens in `normalizeOrderConfig` — the single migration
  point already applied wherever a config is loaded — so existing stored
  configs pick it up with no migration step.
- Editor: one numeric `StepField` (step 0.1) labeled **"Ext-hours market
  buffer %"** in Settings → Orders & hotkeys, at the config level (next to the
  active-venue control, not per-template).
- Threading: `ResolveContext` (`resolveTemplate.ts:11`) gains the buffer value;
  the ticket and hotkey paths read it from the loaded `OrderConfig`
  (`useOrderConfig`).
- Engine-side config: none. The backstop blocks; it never prices.

### 4. What does not change

- `OrderType` / `TIF` / `OrderSession` enums and the generated TS.
- Broker adapters: once the type is LIMIT, TradeZero's `tifWire` already emits
  `Day_Plus`/`GTC_Plus` in extended hours and Alpaca's `submitOrder` already
  sets `extended_hours: true` for limit day/gtc — zero adapter changes.
- Gate valuation (`gate.go`): a converted order is a limit, valued off its
  limit price as today.
- Sim fill logic.
- RTH behavior: MARKET passes through untouched.

## Error handling

- No quote at all → order blocked with an error (unchanged pattern).
- One-sided book → last-price fallback + explicit notice.
- Market fully closed (weekend/holiday-as-weekday gap): conversion still
  applies (`sessionAt` returns non-`rth`); the order rests broker-side as an
  extended-hours limit — existing semantics, unchanged.
- Raw MARKET reaching the engine outside RTH → `OrderBlocked`, visible in the
  UI like any other gate rejection.

## Testing

**`preChecks.test.ts` (extend):**
- Buy converts to ask × 1.01 rounded up to tick; sell to bid × 0.99 rounded
  down.
- Sub-$1 price uses $0.0001 tick.
- One-sided book falls back to last with the fallback notice.
- No ask/bid/last → blocking error.
- During RTH, MARKET passes through unconverted.
- Buffer parameter respected (e.g. 2% vs 1%).

**`resolveTemplate.test.ts` (extend):** MARKET template outside RTH resolves to
LIMIT args with the buffered price; flash shows the limit price.

**`actionTemplate.test.ts` (extend):** `normalizeOrderConfig` defaults a
missing `extHoursMarketBufferPct` to 1.0 and clamps out-of-range values.

**Go `exec` core test (new case):** with a fake clock set outside RTH, a
MARKET submit to a non-capable broker is blocked with an `OrderBlocked`
event; the same submit during RTH passes; a sim (capable) venue passes
outside RTH.

## Files touched

- `ui/src/chrome/exec/preChecks.ts` (+ test) — conversion math, tick rounding
- `ui/src/chrome/exec/resolveTemplate.ts` (+ test) — pass quote + buffer
- `ui/src/chrome/panels/OrderTicketPanel.tsx` — pass quote + buffer
- `ui/src/chrome/exec/actionTemplate.ts` (+ test) — `extHoursMarketBufferPct`
  + normalize default/clamp
- `ui/src/chrome/exec/OrderSettingsSection.tsx` (+ test) — buffer StepField
- `engine/internal/exec/broker.go` — `Capabilities.MarketOutsideRTH`
- `engine/internal/exec/core.go` (+ test) — backstop block in `handleSubmit`
- `engine/internal/broker/sim/sim.go` — capability true
