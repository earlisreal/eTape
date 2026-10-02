# Pre-position position-percentage stop-sell

Status: implemented — Q1–Q11 confirmed; no live-order activity is authorized.

## Goal

Allow a SELL STOP_LIMIT Action Template using position-percentage sizing to be
placed through a Chart Order Gesture before its symbol has an open position,
so the trader can prepare an exit before a separate stop-buy fills. When the
sell trigger is reached without a position, provide explicit feedback rather
than submitting an unintended opening order.

## Current-code evidence

- Chart entry reads positions for the chart's resolved Execution Venue and
  symbol, then uses the shared template sizing and prechecks. Position sizing
  while flat produces zero shares and blocks entry.
- Action Templates currently resolve their percentage to a fixed numeric
  quantity when invoked. The engine does not receive the percentage.
- Engine-Held Stop-Limits currently apply only to DAY orders with an effective
  EXTENDED session during pre/postmarket. RTH uses broker-native stops.
- Engine-held activation submits the parent's remaining numeric quantity and
  reruns the risk gate. Trigger gate failure already rejects the parent
  permanently; no automatic retry exists.
- The current risk gate caps exposure but does not enforce a SELL-only long
  reduction. Alpaca/moomoo map SELL and SHORT to the same broker side; sim can
  sell through zero. Deferred Position Sizing needs its own authoritative
  long-position and pending-exit checks rather than relying on broker behavior.
- The sell trigger is a price level, not a crossing from above: Last-Eligible
  Price at or below the stop triggers it. A protective stop above today's price
  is already hit, even if a separate stop-buy has not yet filled.
- Cached engine positions can lag fills: moomoo only emits position snapshots
  at startup/reconnect, account polling discards positions, and Alpaca live
  fill position events contain one symbol although Core replaces the venue
  position map. Earl rejected a trigger-time broker check because it adds
  execution latency: maintain the existing local cache from confirmed broker
  events and reconcile it in the background instead.
- Link Groups own Execution Venues; an order remains with its original venue.
  Live held custody requires the existing account acknowledgement.

Sources: [glossary](../../CONTEXT.md),
[venue ownership](../../docs/adr/0005-link-group-owned-execution-venue.md),
[existing stop-limit design](../../docs/plans/2026-09-30-chart-order-markers-and-engine-held-stop-limit.md),
[chart entry](../../ui/src/chrome/panels/tv/ChartStopLimitEntry.tsx),
[sizing](../../ui/src/chrome/exec/sizing.ts),
[custody resolution](../../engine/internal/exec/held_route.go), and
[held lifecycle](../../engine/internal/exec/held_lifecycle.go).

## Design tree

### Round one — confirmed by Earl

1. Use Deferred Position Sizing: the percentage applies to the current long
   position in the original venue+symbol at trigger, independently of any buy.
2. If the trigger is reached while flat, reject permanently with a visible
   no-open-position toast. A later buy does not revive the rejected stop.

### Round two — confirmed by Earl

3. Preserve immediate trigger/rejection if the stop is already hit at placement,
   rather than wait for a long position before starting evaluation. **Confirmed.**
   Show the existing level trigger and its immediate-rejection warning.
4. Extend local custody to DAY RTH as well as pre/postmarket. **Confirmed.**
   Retain premarket expiry at 09:30 ET and postmarket expiry at DataClose;
   new RTH custody expires at regular close, including early closes. Deferred
   GTC/overnight combinations are unsupported.
5. Apply the dynamic sizing rule to all eligible percentage stop-sells, not
   only those created flat. **Confirmed.**
   Activation freezes the child quantity; later buy fills need another stop.
6. Entry methods: Chart Order Gesture, hotkey and Deck Button. **Confirmed.**
   They share Action Template behavior; manual Order Ticket retains current sizing.
7. Reject conflicting exits. **Confirmed.** Size the requested percentage of
   total long position and reject conflicts rather than shrink or cancel another
   exit. Activating sell quantities count before broker ACK.

### Round three — confirmed by Earl

8. **Revised and confirmed after Earl's latency concern:** size instantly from a maintained
   local position cache, with no broker query at the trigger. Confirmed fills
   update it immediately; startup/reconnect and background repair reconcile it.
   Ready flat still rejects. If restart/disconnect/gaps make the cache unreliable,
   pause until reconciliation plus manual Resume. Unknown data is different from
   confirmed flat; unavailable cache blocks new deferred admission and Resume.
9. Extend the available-long-shares guard to every engine SELL submission and
   held SELL activation. **Confirmed.** Share the guard so a later ordinary sell
   cannot overlap an activated stop.
   SHORT remains explicit and separate; unresolved deferred admission while flat
   remains allowed. The revised guard uses the same ready cache, adding no
   broker read to ordinary SELL submission.
10. Block explicitly RTH deferred placement outside RTH. **Confirmed.**
    AUTO/EXTENDED can use the active pre/postmarket session and its deadline.

### Final scope check — confirmed by Earl

11. Coordinate broker-native account Flatten with the shared exit guard.
    **Confirmed.** Block it while other broker-bound sell commitments remain
    unresolved. Before an allowed Flatten, pause held sell parents and durably
    mark its pending venue exit; block new SELLs, activations and quantity
    increases until its outcome and share availability are reconciled in the
    background. Failed or ambiguous outcomes retain that commitment; an HTTP
    response alone does not release it. Held parents require manual Resume.
    Per-symbol Flatten already uses Q9's guarded path.

### Review defaults with the final draft

- Show percentage intent before resolution, then numeric shares after activation.
  Preserve current whole-share flooring; short positions and rounded-zero
  quantities reject with specific reasons.
- Preserve existing live acknowledgement, pause/Resume, restart, expiry, route
  races, price-only drag, cancel and legacy fixed-quantity sizing semantics.
- Maintain cache identity, readiness and coherent snapshot/execution baselines;
  apply account fills exactly once, including orders placed outside eTape. An
  undelivered broker fill cannot be inferred from an accepted BUY order.
- Persist resolved activation safely; do not fabricate shares,
  replay trigger toasts, retry an interrupted activation or introduce a paired
  buy/sell order model.

## Constraints

Reuse the existing Action Template, execution Core, durable held parent,
single-writer lifecycle, notifications and chart marker paths. Store sizing
intent explicitly rather than fabricate a share quantity. Never silently
submit a deferred sizing request through a native route. No new dependency,
buy/sell pairing system, or automatic retry is implied by this specification.

Wire contracts are owned by Go; regenerate TypeScript from its owners. Keep
high-frequency market data out of React state. Implementation requires the
full Windows validation checklist for an engine/UI contract change, targeted
sim/paper behavior checks and hosted CI after integration. No live-order
activity is authorized.

## Comments

- 2026-10-02: Earl requested grill-with-docs planning for a chart stop-sell sized
  as a percentage of position before a stop-buy opens the position. Round one
  asks trigger-time sizing and flat-at-trigger lifecycle decisions. Drafts
  remain uncommitted under the repository's spec/plan approval gate.
- 2026-10-02: Earl accepted both round-one recommendations. Round two covers
  already-hit stops, sessions/deadlines, dynamic sizing scope, entry methods,
  and conflicting exits.
- 2026-10-02: Earl confirmed Q3's immediate trigger/rejection and Q4's DAY
  RTH/pre/postmarket custody with the stated session deadlines. The
  [draft custody ADR](../../docs/adr/0006-deferred-position-stop-sell-custody.md)
  records the broker-native quantity trade-off.
- 2026-10-02: Read-only investigation confirmed stale/missing streaming positions
  can break pre-entry sizing, especially on moomoo. The
  [draft implementation plan](../../docs/plans/2026-10-02-deferred-position-stop-sell.md)
  proposes one asynchronous broker snapshot at trigger, with lifecycle checks
  and no broker order until the quantity is confirmed and persisted.
- 2026-10-02: Earl confirmed Q5's uniform dynamic sizing and Q6's shared
  chart/hotkey/Deck scope, then confirmed Q7's rejection of conflicting exits.
  Round three asks verification failure, shared SELL guard and inactive-session
  placement decisions.
- 2026-10-02: Earl challenged Q8's broker round trip at trigger and requested
  cached open shares for immediate sizing. The plan now reuses Core's position
  map, with confirmed execution updates, background repair, deduplication and
  snapshot cutover safeguards. Revised Q8 asks only the unreliable-cache
  lifecycle policy; Q9 and Q10 remain pending. The earlier trigger-time snapshot
  proposal is superseded.
- 2026-10-02: Earl accepted revised Q8: use the maintained cache and pause
  affected stops when it is unreliable. Reconciliation restores readiness, with
  manual Resume required. Confirmed flat still rejects; no trigger-time or
  ordinary SELL position query is proposed. Q9 and Q10 remain open.
- 2026-10-02: Earl confirmed Q9's shared guard for all SELL submissions and
  held SELL activations, and Q10's block on deferred RTH placement outside RTH.
  All interview decisions are settled; final review confirms shared understanding
  before the draft is approved. No implementation has been requested.
- 2026-10-02: Final caller review found native account Flatten bypasses ordinary
  SELL admission. Q11 makes its scope explicit. The plan also specifies pending
  replacement reservations, native submit uncertainty and actual TradeZero
  replacement remainder so the agreed shared guard has no quantity-path bypass.
- 2026-10-02: Earl accepted Q11's recommendation to coordinate native account
  Flatten with pending exits and held stops. Q1–Q11 are confirmed; the complete
  draft awaits final shared-understanding confirmation. No implementation has
  been requested.
- 2026-10-02: Earl confirmed the complete plan and requested implementation.
  Implementation is recorded in
  [the completed plan](../../docs/plans/2026-10-02-deferred-position-stop-sell.md).
