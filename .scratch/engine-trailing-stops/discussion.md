# Engine-Held Trailing Stops — discussion record

Status: Q1–Q33 confirmed by Earl on 2026-10-20; grilling is complete and the
decision frontier is empty. No implementation requested. The consolidated
[specification](spec.md) is the approved design; this file preserves its research
and decision history.

## Requested behavior

- Engine-owned dollar-distance (trail_price) and percentage-distance
  (trail_percent) trails against a favorable price watermark.
- A candle-based trail using a break of the last candle.
- Trigger a LIMIT order using configurable Bid/Ask pricing and an offset or
  cushion comparable to an Action Template.
- Show a horizontal chart line tracking the current stop price.
- Placement through an Order Ticket and a hotkey.
- Retire the trail permanently when its Position Cycle ends; remove its chart
  line and retain audit history.
- Remove trails when the engine terminates; already submitted broker children
  and crash recovery need distinct handling.
- Convert Chart Risk Entry's protective stop-sell into a trail.
- Choose trailing protection directly in a Chart Risk Entry preset, so
  protection can begin trailing without a later manual conversion.

## Verified existing behavior

- Ordinary held Stop-Limits use fresh Last-Eligible prints, with sequence,
  receipt-age and feed-health checks. Historical seeds do not trigger orders.
  A stop is a level condition, rather than requiring a crossing.
- Ordinary held parents do not generally retire on observed flat/reversed
  positions. Deferred Position Sizing deliberately permits admission while
  flat and rejects permanently if triggered without a long position. Its
  percentage applies to current confirmed shares, not a particular entry.
- Before trigger, ordinary held parents reserve no broker shares. SELL
  activation checks current shares against existing exit commitments;
  oversized results reject rather than silently shrink.
- Clean shutdown pauses pretrigger parents for manual Resume. It persists
  cancellation intent and attempts cancellation for Activating/Working
  children. Restart reconciles uncertain children without reposting them.
  The requested retirement on termination differs from current pause/Resume.
- Chart Risk Entry's protection is linked to its entry and covers that
  entry's confirmed fills. Its first protective activation cancels remaining
  buying; later entry fills receive additional linked exits. Canceling a linked
  protective exit currently cancels the entire protection and remaining buy.
  Conversion therefore needs an explicit transition rather than ordinary
  cancel-and-submit behavior.
- Chart Risk Entry currently uses engine-held protection through DataClose.
  Its pretrigger edits carry observed entry/held phases. A submitted child is
  already a LIMIT and accepts different edits from a pretrigger stop.
- Hotkey Action Templates support Bid/Ask/Last/Mid with signed dollar or
  percentage offsets. Conditional templates currently resolve concrete stop
  and limit prices at submission. The manual Order Ticket uses separate
  entered fields; trigger-time quote pricing would be new engine behavior.
- Existing chart order marks are scoped to Execution Venue and symbol and
  already expose scoped cancellation and phase-checked amendments. There is
  no trailing order type or Chart Risk Entry conversion command today.
- Candle inputs and execution inputs have different guarantees. Existing held
  orders demand Ticker only; charts currently own candle subscriptions/history.
  Candle trails will need their own candle demand when no chart is open. The
  finalized-bar callback can also see seed-derived aggregation, so it cannot
  be treated as an inherently live-only execution signal. Synthetic display
  bars are not an engine candle source.

Sources: [glossary](../../CONTEXT.md#order-entry),
[held lifecycle](../../engine/internal/exec/held_lifecycle.go),
[execution Core](../../engine/internal/exec/core.go),
[exit commitments](../../engine/internal/exec/reconcile.go),
[Risk Entry lifecycle](../../engine/internal/exec/risk_entry.go),
[execution guide](../../engine/internal/exec/README.md),
[price sources](../../ui/src/chrome/exec/priceSource.ts),
[template resolution](../../ui/src/chrome/exec/resolveTemplate.ts), and
[chart marks](../../ui/src/chrome/panels/tv/ChartOrderMarkers.tsx).

Candle provenance: [market-data Core](../../engine/internal/md/core.go),
[bar aggregation](../../engine/internal/md/bars.go),
[engine wiring](../../engine/cmd/etape/main.go), and
[feed subscriptions](../../engine/internal/feed/feed.go).

The existing [deferred sizing ADR](../../docs/adr/0006-deferred-position-stop-sell-custody.md)
does not define position-cycle ownership for ordinary stops.
The [engine custody ADR](../../docs/adr/0007-limit-if-touched-engine-custody.md)
provides a precedent for a common parent/LIMIT-child lifecycle across venues.

The unrelated [Dynamic Limit Cushion draft](../dynamic-limit-cushion/spec.md)
is still under discussion; it supplies no approved policy for this feature.
[Alpaca's order documentation](https://docs.alpaca.markets/us/docs/orders-at-alpaca#trailing-stop-orders)
defines trail_price/trail_percent against a favorable watermark but its native
trail activates a market order. The requested eTape child is explicitly LIMIT.

## Settled vocabulary and lifetime decision

The [glossary](../../CONTEXT.md) now defines Position Cycle, Engine-Held
Trailing Stop, High-Water Mark (HWM), Low-Water Mark (LWM), Trail Distance,
Candle Trail and Quote Offset. Limit Cushion retains its trigger-to-limit
meaning; it is distinct from both Trail Distance and a signed Quote Offset.

The [draft lifetime ADR](../../docs/adr/0009-trailing-stop-position-and-engine-lifetime.md)
records the confirmed choice to retire trails across position changes and
engine runs instead of inheriting ordinary held-stop restart/Resume semantics.
The full specification and all task documentation remain uncommitted while
grilling continues.

## Design tree

### Round one — confirmed by Earl

Earl accepted all seven recommendations on 2026-10-20.

1. Standalone trails require an existing Open Position. Bind to the original
   Execution Venue, symbol and Position Cycle. Flat/reversal permanently
   retires the trail, removes its chart line and retains history. Re-entry
   requires a new trail.
2. Start the watermark when trailing becomes active. Use fresh Last-Eligible
   Prices for favorable-watermark updates and trigger evaluation, rather than
   the entry price or an earlier day high.
3. Candle Trails evaluate intrabar against the previous completed real candle
   on a timeframe saved with the trail. The stop only tightens; changing a
   displayed chart timeframe cannot change the trail.
4. Offer Bid/Ask plus signed Quote Offset, or stop price plus/minus Limit
   Cushion. Calculate the execution limit when triggered and submit one
   ordinary LIMIT. Automatic repricing is not included by this decision.
5. Converting Chart Risk Entry protection preserves its entry ownership and
   confirmed/late entry-fill coverage. It is its own operation rather than
   Cancel Protection followed by an unrelated order.
6. Retire untriggered trails across engine termination/restart. Graceful
   shutdown also cancels already triggered working LIMIT children using the
   existing cancellation/reconciliation lifecycle. A crash cannot guarantee
   cancellation of an order already at the broker.
7. Support selectable PRE/RTH/POST sessions, excluding overnight initially.
   Candle demand continues independently of open charts. Exact expiry is open.

### Round two — confirmed by Earl

Earl accepted all eleven recommendations on 2026-10-20.

8. Support long SELL and short COVER trails with mirrored price rules and
   authoritative reduction/commitment caps for both sides. Chart Risk Entry
   remains a long BUY plus SELL-protection tool; this does not add short risk
   entry.
9. Standalone trails use position-percentage sizing initially, default 100%,
   allowing smaller percentages. Resolve against current confirmed position
   shares when triggered; additions and partial exits affect that quantity,
   which is then frozen. Converted protection retains entry-owned sizing.
10. Permit one trailing/protection strategy per Position Cycle. Existing Chart
    Risk Entry protection must be converted rather than overlapped by a new
    standalone trail. Ordinary manual exit conflicts remain to be specified.
11. Support intraday 10s/1m/5m/15m/30m/60m, default 1m. Allow an optional
    nonnegative dollar/percentage buffer beyond the candle boundary, default
    zero; touching the buffered boundary triggers it.
12. Standalone trails expire at the end of today's last selected daytime
    session, using the exchange calendar. Converted protection retains its
    original sessions and deadline.
13. Manual conversion requires at least one confirmed entry fill and healthy,
    untriggered protection. Partial entry fills are allowed. Zero-fill, Paused,
    Activating, Working and unresolved protection cannot manually convert.
14. Conversion never loosens the original protective stop. For a long, retain
    the greater of the original stop and the calculated trail stop as a floor.
15. Standalone trails can visibly Wait for Data. Manual conversion requires
    ready price/candle data and otherwise leaves existing protection unchanged.
16. Market-feed interruptions pause trailing, retaining the watermark/stop
    until manual Resume. Retire a trail if broker/position continuity cannot
    establish the original Position Cycle; a same-direction snapshot cannot
    prove an unobserved flat/reopen did not occur. Do not replay historical
    triggers.
17. If a selected Bid/Ask is unavailable at trigger, latch that trigger,
    reserve its resolved exit quantity and visibly wait for a fresh valid
    quote without fallback. Recheck position and execution gates before POST.
18. Partial child fills leave its ordinary LIMIT remainder working. Confirmed
    completion, cancellation or rejection ends the trail; uncertain outcomes
    remain unresolved. No automatic retry/rearm; a new trail needs an explicit
    new instruction.

### Chart Risk Entry preset branch — confirmed by Earl

Earl proposed selecting trailing protection when configuring Chart Risk Entry,
in addition to the already requested manual conversion of existing protection.
This is compatible with Q13's manual-conversion restriction: a preset can save
future trailing intent while the actual protective quantity still depends on
confirmed entry fills. Saving that intent does not authorize a zero-fill exit.

### Round three — confirmed by Earl

Earl accepted Q19–Q22 on 2026-10-20.

19. Save Protection Mode as Fixed (default), Trail Price, Trail Percent or
    Candle Trail. Retain both chart clicks for entry plus Initial Stop Price.
    Size from those selected prices and existing Limit Cushions. The Initial
    Stop Price is an enduring protection floor; subsequent tightening never
    increases the reviewed entry quantity.
20. Begin trailing after the first confirmed entry fill, using fresh
    Last-Eligible Price. Further partial entry fills increase covered quantity
    without resetting the watermark. No profit threshold initially, and no
    expansion for independent position additions.
21. Keep initial fixed protection until required trailing data is ready, then
    switch in place. Feed interruptions still follow pause/manual-Resume.
22. New risk-entry trailing presets retain the stop-relative Sell Limit
    Cushion, measured from the current stop when triggered. Standalone trails
    and manual conversions retain the accepted Bid/Ask-offset option.

The new preset's run-bound lifetime includes its saved trailing intent and
initial fixed fallback, not only an initialized watermark. Fixed-mode presets
retain their existing lifecycle; trailing-mode presets cannot recreate a
retired trail automatically after restart.

Additional verified constraints:

- The reviewed whole-share quantity uses the two cushion-adjusted execution
  limits, rather than just the trigger gap. UI sends MaxQty and Core recomputes
  without exceeding that ceiling. The current readout is estimated risk.
- The zero-fill protective parent already retains the selected stop and limit
  but cannot activate with zero quantity. A first fill grows coverage and may
  immediately trigger protection against a fresh cached Last-Eligible Price.
- A zero-fill setup's held phase can already become Armed, so a phase label
  alone is not proof of filled exposure. Trail initialization needs confirmed
  fills, and current zero-fill synchronization can reconstruct held metadata.
- After a crash/restart, a previously submitted BUY may still fill while its
  recovered protection is Paused. Trailing-preset intent must not silently
  recreate or restart a retired trail for those fills. Uncertain BUY/SELL
  children still require reconciliation and visible protection recovery.
- Existing template import/export carries saved risk-template fields, and
  existing presets need to normalize to Fixed when no new mode is present.

Sources: [risk sizing and fill synchronization](../../engine/internal/exec/risk_entry.go),
[held activation/recovery](../../engine/internal/exec/held_lifecycle.go),
[engine recovery](../../engine/internal/exec/core.go),
[UI sizing](../../ui/src/chrome/exec/riskEntry.ts),
[risk setup and preview](../../ui/src/chrome/panels/tv/ChartRiskEntry.tsx),
[templates](../../ui/src/chrome/exec/actionTemplate.ts), and
[settings backup](../../ui/src/chrome/backup.ts).

### Round four — confirmed by Earl

Earl accepted Q23–Q33 on 2026-10-20. Recommendations below are confirmed.

23. Manual partial reductions: recommend allowing them. Confirmed manual or
    external reductions retire entry-owned protected shares first, so 100
    protected shares minus a confirmed 40-share manual sale leaves 60. An
    unrelated later buy does not restore retired coverage; new linked entry
    fills can add coverage. Once an exit child reserves shares, another
    overlapping exit must wait for confirmed child amendment/cancellation.
24. Other working exits: recommend rejecting conflicting trail admission or
    activation with a visible exit-conflict reason. Do not cancel unrelated
    orders or silently clip the requested percentage. Ordinary manual exits
    remain allowed before activation under the shared availability guard.
25. Disarm/day-loss: recommend retaining healthy, verified reducing trails
    while canceling remaining entry buying. Explicit Cancel Protection,
    Cancel All and KillSwitch still cancel their scoped protection; KillSwitch
    does not imply a flattening trade. Data/readiness gates remain mandatory.
26. Late entry fills after a trigger: recommend immediate linked LIMIT cleanup
    for only the newly uncovered confirmed fills, using the selected execution
    recipe at that late-fill event. Do not start another watermark or resize
    an uncertain/working child. Reconciled/interrupted/out-of-session cases
    require visible manual recovery, not automatic trail recreation.
27. Eligible candles: recommend the latest trustworthy completed real candle
    in today's current continuous enabled-session window. History may seed a
    reference but never trigger execution. Exclude synthetic, volume-only,
    gap/unverified candles and bars spanning excluded sessions. Across a
    disabled session, keep the existing stop/watermark, then await an eligible
    new-window candle before moving a Candle Trail.
28. Candle corrections: recommend retaining the candle boundary snapshot used
    for each trail update, ignoring later corrections to that candle for
    trading. Newly completed eligible candles may tighten the stop; only fresh
    live Last-Eligible prints evaluate the resulting trigger. No retroactive
    execution or replay.
29. Quote readiness: recommend coherent positive, non-crossed Bid/Ask from a
    verified current-connection Book subscription. A quiet unchanged quote
    remains usable while feed/subscription continuity is healthy. Invalidate
    on gaps/reconnect/lost coverage and reacquire current evidence; do not
    infer freshness from the UI's Basic Quote timestamp. A latched trigger
    waits visibly until valid data, its existing deadline or explicit cancel.
30. Retirement with working children: recommend durable retirement/cancel
    intent on flat, reversal, deadline and engine exit. Cancel relevant working
    children and remaining linked entry buying. Remove the retired trailing
    stop line, while ordinary working/unconfirmed LIMIT children retain their
    order rows/lines until their broker outcomes are established.
31. Chart management: recommend a derived stop line with mode/state details
    and scoped Edit, Resume and Cancel controls. Disable stop-line dragging;
    edit the trail's recipe instead, preserving its current stop as a floor.
    Offer Convert to Trail on an eligible protection pill and a scoped hotkey
    resolving the single eligible protection owner by venue+symbol. Reject
    missing/ambiguous targets. Parameter editing stays within the current
    trailing mode; no reverse conversion to Fixed or between-trail-mode switch
    initially.
32. Defaults and numeric rules: recommend default standalone quote pricing of
    Bid for SELL / Ask for COVER with zero Quote Offset. Manual conversion
    initially inherits Sell Limit Cushion; risk presets retain Q22. Require
    explicit positive trail distance rather than guessing one. Use eTape's
    current price grid; round derived triggers away from the favorable
    watermark and execution limits outward. Touch counts, whole-share sizing
    floors, and zero-sized/nonpositive/nonfinite results reject explicitly.
33. Live custody acknowledgement: recommend reusing Live Held Stop-Limit
    Acknowledgement for the same live Execution Venue account identity rather
    than adding another acknowledgement. Explain trailing's run-bound lifetime
    in its setup/details. This enables custody only, not an order submission.

### Verified constraints for the final policy round

- Execution Core currently receives no Book/BBO, while Basic Quote has no
  Bid/Ask. Held demand is Ticker-only. Book data cached for display/sim is not
  currently an execution-readiness contract; recording-only provenance and a
  combined book timestamp do not prove both sides are current. Execution BBO
  demand, continuity and loss handling must work with recording disabled.
- Existing linked reducing protection bypasses master-disarmed/day-loss;
  ordinary held stops do not. KillSwitch cancels orders and protection without
  placing a flattening order.
- A command ACK for cancellation is not authoritative broker cancellation.
  There is no generic durable cancel-confirm-dependent-exit coordinator today.
- Existing chart markers support drag/arrow and cancel, but no trail parameter
  editor or durable selected-order target. Scoped hotkeys have venue+symbol.
- Current chart prices use a $0.01 grid at/above $1 and $0.0001 below $1;
  ordinary chart dragging uses nearest tick and cushions round outward. The
  proposed derived-trigger rounding direction is an explicit new trail rule.

Sources: [book definitions](../../engine/internal/feed/feed.go),
[book storage](../../engine/internal/md/book.go),
[feed forwarding](../../engine/cmd/etape/main.go),
[disarm and exit gates](../../engine/internal/exec/held_lifecycle.go),
[command acknowledgements](../../ui/src/chrome/exec/commands.ts), and
[chart markers](../../ui/src/chrome/panels/tv/ChartOrderMarkers.tsx).

### Additional verified constraints for round two

- COVER has adapter/trigger support but lacks SELL's position-availability and
  exit-commitment checks. A COVER label alone does not prevent opening a long
  position. Short trails need equivalent authoritative reduction guards.
- Position OpenedMs is not a stable cycle identity. A same-direction snapshot
  after reconnect cannot reveal an unobserved flat/reopen during the gap.
- Pretrigger risk protection reserves no broker shares and does not prevent
  an ordinary standalone exit from consuming its expected shares. Linked
  protective sizing currently follows cumulative entry fills, rather than
  subtracting independent manual partial exits.
- Intraday aggregation can span session boundaries; larger candle flags do
  not attest absence of gaps/volume-only inputs. Finalized candles may later
  be corrected. Selected-session candle boundaries, provenance and correction
  policy remain explicit downstream decisions.

Sources: [submission/position handling](../../engine/internal/exec/core.go),
[commitments](../../engine/internal/exec/reconcile.go),
[position opening tracking](../../engine/internal/exec/roundtrip.go),
[risk protection](../../engine/internal/exec/risk_entry.go),
[calendar and buckets](../../engine/internal/session/session.go), and
[bar aggregation](../../engine/internal/md/bars.go).

### Completion frontier at consolidation

- Q23–Q33 are confirmed. Consolidated the behavior, examples and lifecycle in
  spec.md; all shared design decisions are settled.
- Review creation/Resume/candle-update ordering so cached historical or
  causally older evidence cannot trigger a newly advanced automatic stop.
- Record the normal finite/range validations and exact percentage bases:
  Trail Percent uses HWM/LWM, candle buffer uses its boundary, Quote Offset
  uses its selected quote, and Limit Cushion uses its trigger. Distances are
  positive, buffers/cushions nonnegative, position percentage is (0,100],
  trailing/buffer percentages must keep protective prices valid, and sizes
  floor to whole shares. Require a valid final stop/limit rather than fabricate
  a positive price or share quantity.
- Preserve original venue ownership, pin/group rules and mode configuration
  across ticket/hotkey/Deck, template import/export, chart context changes and
  multiple browser windows. Engine data demand outlives chart mounts.
- All shared design decisions are confirmed. This task remains documentation;
  implementation requires a separate request.

## Comments

- 2026-10-20: Earl requested grill-with-docs brainstorming and added conversion
  of Chart Risk Entry protection. Repository/skill instructions require this
  spec to remain an uncommitted draft during grilling. No runtime settings,
  orders, glossary definitions or accepted ADRs were changed in round one.
- 2026-10-20: Earl accepted all seven round-one recommendations. Recorded the
  settled glossary terms and a draft ADR for position-cycle/engine-run lifetime.
  Round two asks sizing, candle controls, conversion and data-loss behavior.
  Implementation remains unapproved; all task documentation remains uncommitted
  while the design is being grilled.
- 2026-10-20: Earl accepted Q8–Q18 and proposed offering trailing protection
  directly in Chart Risk Entry setup. Added the confirmed short-position
  Low-Water Mark definition and the new preset branch. No new preset-specific
  recommendations were confirmed in that round.
- 2026-10-20: Earl accepted Q19–Q22. Recorded Protection Mode and Initial Stop
  Price in the glossary. Remaining policy questions cover manual exits, candle
  evidence/corrections, BBO readiness, retirement and chart management. Runtime
  code is unchanged; documentation remains an uncommitted design draft.
- 2026-10-20: Earl accepted Q23–Q33. Preserve this discussion record and
  consolidate the complete behavior in spec.md. No further product-decision
  blocker was found; all shared design decisions are settled. Finalize and
  publish documentation under repository standing Git authorization.
- 2026-10-20: After implementation, Earl removed the protection-pill Trail
  button and the conversion feature entirely. Cancel protection and submit a
  separate standalone trail manually. This supersedes the earlier conversion
  decisions; Chart Risk Entry presets can still select trailing protection.
