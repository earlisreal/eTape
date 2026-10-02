# Deferred Position Sizing for stop-sells

Status: implemented — Q1–Q11 confirmed; no live-order activity is authorized.

## Goal

Prepare a percentage-sized SELL STOP_LIMIT before a separate stop-buy fills.
Resolve shares from the current long position in the original venue+symbol
when the sell stop triggers. If flat, permanently reject the parent and show a
clear no-open-position toast. Earl confirmed these two rules on 2026-10-02.

The [specification](../../.scratch/position-percent-stop-sell/spec.md) records
the confirmed decisions and implementation outcome.

## Confirmed scope

- Apply Deferred Position Sizing to eligible position-percentage SELL
  STOP_LIMIT Action Templates through chart, hotkey and Deck entry, whether
  placement starts flat or holding. Manual ticket sizing remains immediate.
- Support local DAY custody in RTH and pre/postmarket. Preserve premarket
  expiry at regular open and postmarket expiry at DataClose; RTH expiry is the
  scheduled regular close. Reject unsupported deferred TIF/session combinations
  rather than silently falling back to a broker-native fixed-quantity stop.
- Preserve level triggers. A sell stop already above the trusted current price
  can reject while flat before a higher stop-buy fills; preview must say so.
- Size from actual filled long shares, not the pending buy's intended size.
  Freeze numeric quantity at activation; later fills require another stop.
- Reject conflicts with other outstanding exits rather than silently shrink
  the requested percentage or cancel another order.
- Confirmed revised Q8: use the existing engine position cache at the hit,
  without a broker query or a wait for reconciliation on the trigger path.
  Maintain it from confirmed broker execution/position events and repair it in
  the background. Pause affected held stops when position data becomes unreliable;
  reconciliation restores readiness, then manual Resume is required.
  A ready cache reporting flat still rejects permanently with the agreed toast.
- All engine SELL submissions and held SELL activations
  share the long-share availability guard. A later ordinary SELL cannot consume
  shares already committed to an activated stop. SHORT remains a separate action.
- Deferred RTH requests are supported only during RTH;
  no deferred staging for a future session is added.
- Coordinate broker-native account Flatten with sell commitments,
  pausing held sell stops and blocking new sells while its outcome is pending.

No entry/exit pairing, bracket/OCO system, automatic retry, GTC/overnight local
custody, short-position exits, or new dependencies are proposed. Existing
fixed-share stops and legacy persisted orders retain their sizing semantics.

## Current-code evidence

- [Shared template resolution](../../ui/src/chrome/exec/resolveTemplate.ts)
  calls [sizing](../../ui/src/chrome/exec/sizing.ts) at invocation. Chart entry
  shares that resolver; [prechecks](../../ui/src/chrome/exec/preChecks.ts)
  block zero quantity. Hotkeys and Deck Buttons use `fireTemplate`; the manual
  ticket has its own immediate sizing path.
- [Wire payloads](../../engine/internal/uihub/wsmsg/payloads.go),
  [engine types](../../engine/internal/exec/types.go) and adapters accept only
  a fixed numeric quantity. Concrete broker requests reject nonpositive size.
- [Route resolution](../../engine/internal/exec/held_route.go) currently holds
  EXTENDED DAY stops only in pre/postmarket. Route preview has no side/sizing
  context. [Activation](../../engine/internal/exec/held_lifecycle.go) hardcodes
  EXTENDED DAY and submits the parent's numeric remaining quantity.
- [Risk gates](../../engine/internal/exec/gate.go) cap exposure but do not
  prohibit a SELL from opening a short. Existing working-exposure accounting
  is not a sellable-share reservation ledger.
- Broker-bound replacements bypass the existing gate. Native account Flatten
  calls the adapter directly; per-symbol Account panel Flatten uses normal
  SELL/COVER submission. Native submit transport errors currently become
  terminal rejections even when an order may have reached the broker, which is
  incompatible with preserving a share commitment through uncertainty.
- [State replay](../../engine/internal/exec/state.go) can retain a working
  zero-quantity parent, but current stop replacement rejects such quantity.
  Resolving initial size through `OrderReplaced` would create a misleading
  replacement history leg in [closed orders](../../engine/internal/exec/closed.go).
- Positions are broker-reconciled. Fill fold does not update the position map;
  an eligible-print event can run before the corresponding position update.
  [Reconciliation](../../engine/internal/exec/reconcile.go) replaces the whole
  venue position map, although Alpaca's live fill event contains only one symbol.
  Moomoo emits positions at startup/reconnect but not on a live fill; the
  [account poller](../../engine/internal/exec/account_poller.go) discards snapshot
  positions. `State.VenuePositionShares` is already a constant-time lookup;
  cache maintenance is the prerequisite, not a second cache or trigger-time I/O.
- Startup/reconnect installs position snapshots before emitting catch-up fills.
  Applying every existing `OrderFilled` to positions would double-count fills
  already represented by a snapshot. TradeZero replacements use separate raw
  broker legs with one domain order ID; moomoo's current fill decoder synthesizes
  cumulative execution and documents reconnect overcount. Cache effects need
  adapter execution identity/baselines, not generic arithmetic on domain orders.
- Moomoo's account push subscription includes external orders, but domain-order
  correlation discards untracked orders/fills. Position maintenance must include
  confirmed account executions regardless of eTape ownership while preserving
  the existing eTape-only trade ledger. Its existing `getPositionList` provides
  a narrower background refresh than a full account/order `Snapshot`.
  External open orders with empty `Remark` also lack stable domain identity;
  account-wide commitment tracking must use raw broker identity, not an empty
  client-order key. Keep that metadata separate from eTape's local trade ledger.
- [ExecStore](../../ui/src/data/ExecStore.ts) already emits a delta-only,
  deduplicated rejection callback used by sound. There is no visible delayed
  trigger-rejection toast attached to that callback today.

## Implemented behavior

### Position cache prerequisite

Reuse `VenueState.Positions`, owned by the existing Core single writer. Include
positions opened outside eTape, consistent with sizing from the current account
position. Update quantity when a confirmed execution or absolute broker position
update arrives; pending BUY orders and accepted BUY stops never create shares.
Reuse existing signed-quantity math and adapter deduplication where correct.

- Give full snapshots and per-symbol absolute updates explicit semantics; fix
  Alpaca's one-symbol update replacing sibling positions. Do not add a fill after
  applying an absolute update that already includes that fill.
- For execution-derived quantity, preserve raw broker execution/leg identity
  and trustworthy cumulative baselines. Do not use moomoo's synthetic cumulative
  sum or a domain-order cumulative watermark across TradeZero replacement legs.
  Repeated, older and snapshot-covered executions must change cached shares once.
- Apply a confirmed fill's position effect and the corresponding order's remaining
  quantity in the same Core turn. A price event must not see reduced order
  commitment alongside the position quantity from before that fill. Correlate
  adapter position effects with their execution when both describe one fill.
- Seed at startup and reconcile in the background on reconnect, known gaps and
  scheduled repair. Install positions and execution baselines only after the
  adapter establishes a coherent boundary; buffer/deduplicate racing updates.
  Preserve the separate history processing of catch-up fills.
- Track venue readiness at the adapters' startup/reconnect and known-gap
  position/open-order baseline. Pause affected pretrigger stops and require both
  readiness and manual Resume before evaluating them again. An unreliable cache
  must never be treated as a zero-share position. Block new deferred admissions
  while the venue cache is unready with a distinct position-data-unavailable
  reason. The current adapter contract has no safe periodic snapshot cutover, so
  Core does not start overlapping timed position snapshots; repairs follow a
  startup/reconnect/gap baseline or the explicit post-Flatten snapshot.
- Maintain broker-side open sell commitments off the trigger path, including
  external orders. Correlate raw broker legs with local intents so one child
  counts once, and do not collapse unrelated external orders under an empty ID.
  Position readiness alone is insufficient if pending exits are unknown; mark
  share availability unready until the account order baseline is reconciled.
- Publish zero quantities for symbols removed by a full snapshot so the UI also
  clears flattened positions. Preserve broker cost-basis authority and existing
  P&L/trade-history scope while maintaining execution quantity.

Broker fills not yet delivered to eTape cannot appear in any local cache.
The trigger uses the last broker-confirmed quantity already processed by Core;
it does not wait for a future BUY fill or query the broker to bridge that delay.

### Deferred order flow

1. Extend Go-owned order submission and durable held metadata with explicit
   Deferred Position Sizing. Validate finite percentage in `(0, 100]`, SELL,
   STOP_LIMIT and supported DAY/session combinations. Keep adapter-bound
   `OrderRequest.Validate` strict. Unresolved quantity is explicit metadata,
   never a fabricated one-share order; reject ambiguous requests supplying both
   deferred intent and fixed shares. Regenerate TypeScript from Go. Carry the
   percentage/resolution metadata through existing open/closed wire projections.
2. Make route preview and admission use the same sizing-aware custody decision.
   Extend preview query inputs and the client cache key to include the new
   route discriminator. Preserve custody expectation checks at session races,
   live-account acknowledgement, venue ownership and feed-demand admission.
   Define admission gates that do not require resolved notional: retain arm,
   venue, account/risk availability and open-order limits; run quantity/value
   caps when the stop activates.
   Supported deferred routes:

   | Current phase | Requested session | Effective session | Deadline |
   | --- | --- | --- | --- |
   | Premarket | AUTO / EXTENDED | EXTENDED | Scheduled regular open |
   | RTH | AUTO / RTH | RTH | Scheduled regular close |
   | RTH | EXTENDED | EXTENDED | Scheduled regular close |
   | Postmarket | AUTO / EXTENDED | EXTENDED | DataClose |
   | Pre/postmarket | RTH | Unsupported deferred route | No admission |
   | Overnight / closed | Any | Unsupported deferred route | No admission |

   All supported deferred routes require DAY; all deadlines use the authoritative
   calendar, including holidays and early closes. Only this explicit deferred
   discriminator changes RTH custody; ordinary fixed-quantity routing stays intact.
3. On the first eligible trigger, check venue position readiness and resolve
   `floor(longQty * pct / 100)` from the original venue+symbol cache on the Core
   loop. Make no broker read before submission. An unreliable cache pauses the
   held parent for reconciliation and manual Resume; ready flat, short or
   rounded-zero size rejects permanently with a specific reason and no broker
   submission. Check arm, venue identity, connection and deadline, then pending
   sell commitments and the normal child gate against the same state. Pending
   commitments include
   activating/working local intents and broker orders maintained off the trigger
   path, with the same parent/child counted once. Persist
   numeric parent quantity and child activation identity atomically before
   asynchronous POST. Reuse `HeldOrderChanged` with resolution metadata and fold
   it into both live state and the existing closed-order projection; do not
   create a fake replacement history leg. Resolve and reserve quantity before
   another trigger can reuse those shares. Replay/restart never trusts a replayed
   position cache, resizes an activating child or reposts it. Child session and
   deadline match the held route.
4. Preserve unresolved intent through waiting, pause/Resume and stop-price
   edits. Preserve the existing stable parent row, drag/X behavior, demand
   release, cancellation, disarm, restart and unknown-child reconciliation.
   Pretrigger deferred parents count as open orders but reserve no shares;
   activating/working exits must count while their outcomes remain pending.
   Reuse the ready position cache and the same availability
   check for every new SELL commitment, including quantity-increasing replaces;
   exclude the order's own existing commitment when checking it. Add no broker
   read to ordinary SELL submission either. Preserve numeric size for ordinary
   SELLs and price-only chart replacement behavior.

   Compute available long shares from the ready cache minus other broker-bound
   sell commitments. Resolve the percentage from total long shares, then reject
   if the result exceeds availability; do not shrink it. Include native stops,
   submitted intents, activating/working children, partial remainders and any
   uncertain submit/replace/cancel outcome. Exclude pretrigger local held parents
   without a child from share commitments; they still count as open intentions
   and existing risk exposure.

   On broker-bound replacement, check resulting remaining quantity and reserve
   the larger of old and requested remaining size until confirmation. Handle
   partial fills during that request on the same Core loop. Check the quantity
   actually sent by each adapter: TradeZero's cancel/new-leg replacement must
   submit only the unfilled remainder, preserving cumulative domain execution.
   A price-only replacement must not create extra shares. Positively confirmed
   rejection frees a commitment; a transport error alone does not. Use the
   existing uncertain-action/reconciliation pattern for native SELL submission
   instead of recording a possibly submitted order as terminal Rejected.

   Reject native account Flatten while other broker-bound
   sell commitments are unresolved. Before an allowed native Flatten starts,
   pause held sell parents and durably mark its pending venue exit so subsequent
   SELLs, activations and quantity increases cannot compete with it. Confirm its
   outcome and rebuild share availability from a follow-up snapshot; failed or
   ambiguous results keep the lock until a ready snapshot confirms all positions
   flat. Held parents require manual Resume afterward. Preserve the
   existing per-symbol Flatten path through ordinary guarded submission.
5. Update shared Action Template resolution and chart submission to send the
   percentage intent rather than block a flat preview. Ordinary immediate
   orders keep their existing sizing. Before trigger, preview, chart labels
   and Account order rows show `SELL 100% position` (or configured percentage),
   custody and deadline; after resolution show actual shares. Preserve exact
   preview-to-submit snapshots and keep preview updates imperative.
   Explain unavailable position data separately from a confirmed flat position;
   do not imply that a paused stop is active. Once shares resolve, the same
   row/marker advances through the existing Activating and Working phases.
6. Attach delayed rejection feedback to the existing ExecStore rejection
   callback, filtered to these stops. Include symbol, venue and reason; do not
   replay toasts on snapshots/reconnects. Reuse existing terminal marker removal
   and durable rejected-history reason. Update engine/UI execution and chart
   panel READMEs for supported custody, sizing and failure behavior.

### Implemented areas

| Area | Existing owners and minimum changes |
| --- | --- |
| Position cache | `engine/internal/exec/{broker,state,reconcile,core}.go`: full/delta update semantics, execution effects, readiness, atomic fill/commitment updates and coherent reconnect/gap baselines. |
| Moomoo events | `engine/internal/broker/moomoo/{normalize,moomoo,trd}.go`: account-wide execution identity/baselines, immediate cached quantity updates and reconnect/gap position repair. |
| Other venue events | `engine/internal/broker/alpaca/{normalize,alpaca}.go` and `engine/internal/broker/tradezero/{normalize,tradezero}.go`: absolute position updates, raw replacement-leg identity and coherent reconnect cutover; sim uses the same cache contract. |
| Held orders and guards | `engine/internal/exec/{types,events,gate,core,held_route,held_lifecycle,closed}.go`: percent intent, deferred validation, session deadlines, share commitments, uncertain submits/replaces, durable quantity resolution and history; native Flatten coordination. |
| UI contract | `engine/internal/uihub/{commands,query,mirror}.go` and `engine/internal/uihub/wsmsg/payloads.go`: percent submission, sizing-aware route preview, cache readiness and order projections; regenerate `ui/src/gen/wsmsg.ts`. |
| Template entry | `ui/src/chrome/exec/{resolveTemplate,resolveChartStopLimit,preChecks,commands}.ts` and `ui/src/chrome/panels/tv/ChartStopLimitEntry.tsx`: deferred intent, exact preview snapshot and consistent chart/hotkey/Deck behavior. |
| Order presentation | `ui/src/chrome/panels/{AccountPanel.tsx,tv/ChartOrderMarkers.tsx}` and existing ExecStore/toast wiring: percentage before resolution, actual shares afterward, distinct unavailable/flat feedback and delta-only rejection toasts. |
| Guides | Relevant engine execution, UI execution and chart panel READMEs: cache trust, supported custody, sizing, expiry and Resume behavior. |

Use existing test suites alongside these owners. Add no separate position store,
execution ledger, cache framework or dependency. Internal adapter contract changes
must preserve existing broker-cost and local P&L semantics.

## Acceptance criteria

- Flat placement succeeds only for supported deferred intent; invalid or
  malformed percent, wrong side/type, unsupported session/TIF and route races
  fail before broker work. Ordinary zero-quantity broker requests still fail.
- A fresh eligible hit with 40 long shares resolves 100% to 40. Position changes
  before trigger affect size; position changes after activation do not.
- A hit while flat rejects permanently, emits one visible toast and removes the
  stop marker. Later buy fills neither retry nor revive it. Short and fractional
  whole-share rounding failures have distinct reasons.
- Pending exits and two simultaneous percentage stops cannot submit overlapping
  shares. Uncertain or cancel-requested children retain their pending-exit
  exposure until authoritative reconciliation resolves them.
- An ordinary SELL placed after a deferred child activation cannot reuse its
  shares. Pending quantity increases reserve their maximum possible remainder;
  unknown replacements/cancels/submits do not free it. Partial-fill TradeZero
  price replacement submits the correct remainder and retains cumulative fills.
- External broker sell commitments, including several moomoo orders without
  Remarks, remain distinct and count toward availability without entering local
  trade P&L. Unavailable commitment data blocks new exits rather than implies
  all long shares are uncommitted.
- Native Flatten cannot overlap an existing sell commitment
  or a later new SELL/held activation. Failure, partial outcome, timeout and
  restart retain its unresolved exit until authoritative reconciliation; no
  automatic held-order Resume follows it.
- With 40 long shares and a 40-share child, a 20-share fill updates long quantity
  to 20 and the child commitment to 20 atomically. A second trigger between
  broker notifications cannot observe 40 long shares with only 20 committed.
- A confirmed moomoo entry fill updates cached shares before a following trigger
  is processed; no broker snapshot/query occurs between that hit and the child
  POST. The same is true for ordinary SELL submissions.
- Cache checks cover partial fills, duplicate/older executions, external orders,
  raw replacement legs, snapshot-covered catch-up fills, two-symbol Alpaca
  updates, removed-symbol zero updates, stale in-flight snapshot rollback,
  startup/reconnect overlap and readiness after restart/disconnect/gaps. A
  background repair failure cannot make an unknown cache look flat or ready.
- An unready venue pauses affected pretrigger stops, blocks new deferred
  admissions and blocks Resume. Repair restores readiness but does not resume
  orders or replay blocked entries automatically. Ordinary
  SELL submissions also block on unavailable position data rather than query
  the broker on the submission path.
- Percent metadata and resolved activation quantity survive event replay.
  Restart, feed gaps, manual Resume, stop drag/cancel, deadline expiry, live
  acknowledgement and ambiguous child recovery retain existing guarantees.
- Cover RTH, pre/postmarket, early-close deadlines and custody preview races.
  Chart/hotkey/Deck behavior, unresolved labels, rejection deduplication and
  legacy fixed-quantity behavior have focused existing-suite coverage.
## Validation record

- After merging `origin/main` into the task branch, `go test ./...`,
  `go test -race -short ./...`, `go vet ./...`, and `golangci-lint run`
  (v2.12.2) passed.
- `npm ci`, `npm run typecheck`, `npm run lint`, `npm test`, and
  `npm run build` passed. The install reported 5 dependency audit findings;
  no dependencies were changed. The build emitted a large-chunk advisory.
- After upstream integration, `npm run e2e:chart-layout` passed all 17
  simulated overlay states and 3 `PanelFrame`/`ChartPanel` states. The full
  `npm run e2e` run had 11 passes, 17 failures, and 1 stop-limit skip; failures
  timed out across Settings, reconnect, and demo-workspace scenarios while
  Yahoo metadata requests timed out.
- `go tool tygo generate` was run twice and produced no tracked change to
  `ui/src/gen/wsmsg.ts`. The `mingw32-make -C engine gen-ts-check` wrapper could
  not finish because Cygwin `sh.exe` failed to create its signal pipe with
  Win32 error 5; direct generator and drift checks passed.
- `git diff --check` and the tracked-Go LF check passed. Final plan commit,
  merge to local `main`, push, and hosted CI verification remain in progress
  under the repository handoff workflow.

## Rollout, rollback and known limits

Validate in sim/paper without live orders. Preserve per-account live held
acknowledgement and make new RTH local custody explicit. Rollback disables new
deferred admissions while retaining replay, visibility and cancel/reconciliation
of existing parents/children until terminal.

Main risks are delayed broker events, simultaneous sell intents, persisted
unresolved quantities, and confusing a prepared sell stop with a paired
protective bracket. There is no position-query round trip at trigger. A hit
processed before its entry fill is known locally can still see confirmed flat
and reject, rather than wait for that fill. Known unreliable cache data pauses
the stop instead of implying confirmed flat. Broker-side trades outside eTape
remain subject to broker validation. There is no scheduled position-poll repair:
the current adapter contract cannot make an overlapping timed snapshot safe
against newer stream fills. Repairs use startup/reconnect/known-gap baselines and
the follow-up snapshot after Flatten.
