# Engine-Held Trailing Stops

Status: Approved design — Earl confirmed all decisions Q1–Q33 on 2026-10-20.
Grilling is complete; the decision frontier is empty. Implementation has not
been requested or started.

## Outcome and scope

Provide engine-owned trailing exits for existing long and short positions,
with dollar-distance, percentage-distance and completed-candle modes. Place
them from the Order Ticket or an Action Template hotkey/Deck Button, track the
derived stop on matching charts, convert existing Chart Risk Entry protection,
or select trailing protection in the Chart Risk Entry preset itself.

A stop hit activates an ordinary LIMIT, with either stop-relative Limit Cushion
or Bid/Ask plus Quote Offset. A triggered LIMIT may remain unfilled or partially
filled; there is no automatic chasing, retry or rearm.

Standalone trails reduce an existing Open Position. Chart Risk Entry remains
long BUY plus linked SELL protection; adding short COVER trails does not add
short Chart Risk Entry. The first version excludes overnight/GTC trails, fixed
share sizing, stacked trailing strategies, profit-threshold activation and
switching between trailing modes or back to Fixed after placement.

## Language and decision record

Use [CONTEXT.md](../../CONTEXT.md) for Position Cycle, Protection Mode, Initial
Stop Price, Engine-Held Trailing Stop, HWM/LWM, Trail Distance, Candle Trail,
Limit Cushion and Quote Offset. These are distinct concepts: the distance to
a trailing trigger is separate from the distance/reference used for execution.

The [discussion record](discussion.md) preserves all four confirmed rounds and
the code research. The [lifetime ADR](../../docs/adr/0009-trailing-stop-position-and-engine-lifetime.md)
records the deliberate departure from ordinary held-stop restart/Resume.
The separate [Dynamic Limit Cushion draft](../dynamic-limit-cushion/spec.md)
supplies no approved policy for this feature.

## Position ownership and quantity

- Bind each standalone trail to its original Execution Venue account,
  symbol, direction and Position Cycle. Require a current broker-confirmed,
  ready Open Position at admission. Changing a Link Group, chart symbol,
  workspace or browser cannot retarget it.
- Allow one managed trailing/protection strategy per Position Cycle. If Chart
  Risk Entry already provides protection, use conversion rather than placing
  another standalone trail. Multiple cleanup children from one linked entry
  are still one strategy.
- Standalone sizing is a percentage of the current confirmed position when
  the stop triggers, default 100%. Resolve abs(position shares) times percentage,
  floor to whole shares, then freeze the quantity. Position additions and
  confirmed partial exits before trigger affect that calculation.
- Entry-owned trailing protection covers this entry's confirmed fills only.
  Later partial fills of the same entry increase coverage without resetting
  its watermark. Independent buys do not increase entry-owned coverage.
- Confirmed manual/external reductions retire entry-owned protected shares
  first. Once retired, independent additions cannot restore that coverage.
  Account for linked exit fills separately and apply each fill/effect/snapshot
  only once; a position update must not count a linked fill a second time.
- Before trigger, manual exits remain permitted under the shared availability
  guard. Once a trigger reserves quantity, another overlapping exit must wait
  for authoritative amendment/cancellation confirmation.
- Enforce equivalent SELL and COVER reduction and commitment guards. Include
  local, external, submitting, waiting-quote and uncertain exit commitments.
  Reject conflicting admission/activation visibly; never cancel unrelated
  orders or silently clip the requested percentage.
- Recheck identity, Position Cycle, direction, readiness, quantity availability,
  execution gates and deadline immediately before every broker submit.
  An external reduction making a frozen quantity unsafe causes visible
  rejection rather than resizing it. Independent additions do not enlarge it.
- Flat, reversal or loss of verifiable position continuity permanently retires
  the trail. A later position cannot revive it. A same-direction reconnect
  snapshot alone cannot rule out a hidden close-and-re-entry.

## Trailing algorithms

Use fresh Last-Eligible Prints for watermark and trigger evaluation. Start the
watermark when trailing actually begins, not from entry price or the day's
earlier high/low. Retain its favorable extremum across parameter edits, later
entry fills and an eligible manual Resume.

| Mode | Long SELL candidate | Short COVER candidate |
| --- | --- | --- |
| Trail Price | HWM minus dollar Trail Distance | LWM plus dollar Trail Distance |
| Trail Percent | HWM times (1 minus percent / 100) | LWM times (1 plus percent / 100) |
| Candle Trail | Reference candle low minus buffer | Reference candle high plus buffer |

For a percentage candle buffer, use that candle's low/high respectively.
Favorable movement alone changes HWM/LWM. Every applied stop is monotonic:
SELL takes the greater of its prior stop, any protection floor and its new
candidate; COVER takes the lesser of its prior stop/ceiling and new candidate.
Edits cannot loosen the existing stop.

Use eTape's current price grid: $0.01 at/above $1 and $0.0001 below $1.
Round derived SELL triggers down and COVER triggers up, then preserve any
existing protection bound. This is outward rounding for derived triggers;
ordinary fixed chart stop dragging keeps its existing nearest-tick behavior.

A fresh eligible price at or below a SELL stop, or at or above a COVER stop,
triggers it. Exact touch counts; a crossing from the other side is unnecessary.
Retain canonical decimal trigger values and real print precision so floating
point noise cannot prevent an exact touch.

## Candle reference policy

Offer 10s, 1m, 5m, 15m, 30m and 60m, default 1m, saved with the trail. Chart
timeframe changes do not alter it. Buffer is optional, nonnegative dollars
or percentage, default zero.

Select the latest trustworthy completed real candle wholly within today's
current continuous enabled-session window. Adjacent enabled PRE/RTH/POST
phases form one window. A disabled phase separates windows. Use normal
exchange-aligned buckets; a bar spanning excluded time is ineligible.

Exclude display-only synthetic candles, volume-only candles, known gaps and
unverified evidence. History may initialize a qualifying reference in the
current window, but history/seeds/corrections never trigger an order. If no
reference is ready, a standalone trail visibly Waits for Data; linked preset
protection keeps its initial fixed stop.

Retain the identity and boundary snapshot actually used. Later corrections,
history reload or Resume cannot turn that same candle into a second new
reference or rewrite its earlier trading decision. A distinct newly completed
eligible candle may tighten the stop. Across a disabled-session interval,
preserve the prior stop/watermark, then await a qualifying new-window candle
before moving a Candle Trail.

Automatic candle advancement must not evaluate against a causally earlier
cached print. Order candle updates and live eligible prints authoritatively,
including batches whose receive timestamps tie. Only eligible live evidence
at/after the relevant automatic update may trigger the resulting level.
Admission, deliberate amendment and Resume retain the existing level-trigger
semantics while checking fresh current evidence and observed phases.

## LIMIT execution recipe

| Recipe | Calculation and default |
| --- | --- |
| Quote Offset | Selected Bid or Ask plus signed dollar offset, or selected reference times (1 plus signed percent / 100). Standalone default: SELL Bid / COVER Ask, zero offset. |
| Limit Cushion | SELL stop minus cushion; COVER stop plus cushion. Percentage uses the frozen trigger price. Manual conversion inherits its existing Sell Cushion by default. |
| New Chart Risk Entry trailing preset | Retains its stop-relative Sell Limit Cushion; entry sizing has a concrete initial limit basis. |

Either quote side may be deliberately selected; no hidden marketability
adjustment or fallback applies. Freeze the stop and exit quantity at trigger.
When the reference is usable, calculate the execution limit once and round
outward: SELL down, COVER up.

If Bid/Ask is not usable at trigger, latch the trigger and durably reserve its
resolved exit quantity, visibly Waiting for Quote. Stop trailing at that point.
Use the first valid permitted-session reference when ready, subject to the
pre-submit checks; wait only until the existing deadline or explicit cancel.
Do not retry the price condition, substitute another reference or recalculate
the reserved percentage after position changes.

Book evidence must be positive and non-crossed, from a verified subscription
on the current connection with reliable continuity/loss detection. Unchanged
quotes remain usable while that continuity is healthy. Gap, reconnect or lost
coverage invalidates readiness and requires current evidence again. Basic
Quote's timestamp and recording-only provenance cannot establish this contract.
Readiness must work with optional recording disabled.

Persist child identity and submission intent before broker submission.
Ambiguous outcomes retain commitments and require reconciliation; never blindly
POST another child. Partial fills leave the same LIMIT remainder working.
Confirmed completion/cancellation/rejection ends that trail, while uncertain
outcomes remain visible. A new trail needs an explicit new instruction.

## Chart Risk Entry trailing presets

Save Protection Mode as Fixed (default), Trail Price, Trail Percent or Candle
Trail. Existing presets without the new field normalize to Fixed.

Keep both chart-selected prices: BUY trigger and Initial Stop Price. Continue
sizing from the initial cushion-adjusted BUY/SELL execution limits and funding,
with reviewed MaxQty as a ceiling. Later tightening never increases that
reviewed entry quantity. The Initial Stop Price remains the protection floor
for a preset that starts with a trailing Protection Mode.

Create no executable SELL quantity before confirmed entry fills. Begin trailing
after the first confirmed fill, using fresh Last-Eligible Price at that point.
If the trailing reference is not ready, retain fixed protection at the Initial
Stop Price until it is ready, then switch in place. The fallback remains
subject to normal feed/readiness gates; it does not bypass a required Resume.

Later partial entry fills add coverage without resetting trailing progress.
On first protective trigger, cancel remaining entry buying and cover only
confirmed filled exposure. Newly uncovered late fills receive separate linked
LIMIT cleanup using the selected recipe when healthy, in-session and within
the deadline. Count waiting-quote reservations as coverage too. Do not resize
a frozen/uncertain child or start a second watermark.

Late fills found during reconciliation, interruption, expired/out-of-session
conditions or retired protection require visible manual recovery. Cleanup
obligations do not authorize recreating a retired trail. A zero-fill setup
whose entry terminates also retires its unused protection.

The complete trailing-preset intent, including its zero-fill preparation and
initial fixed fallback, belongs to one engine run. Restart cannot downgrade
it to an ordinary resumable Fixed preset and restore trailing indirectly.
Uncertain BUY and SELL children still require broker reconciliation. Fixed-mode
presets retain their existing behavior.

## Manual conversion

Offer Convert to Trail for healthy primary protection after at least one
confirmed entry fill, while it remains pretrigger Waiting/Armed. Allow partial
entry fills. Reject zero-fill, Paused, Activating, Working and unresolved
protection, stale observed phases and missing/ambiguous targets.

Conversion requires ready price/candle data; otherwise leave the existing
protection unchanged. Preserve entry ownership, remaining confirmed coverage,
late-fill handling, sessions and original deadline. Convert atomically in
place rather than invoking Cancel Protection and submitting a separate exit.

Use the currently confirmed protective stop, including prior edits, as the
conversion floor. A trailing preset also retains its enduring Initial Stop
Price floor. Seed new trailing progress from fresh current evidence; never
from an earlier day high/low.

Inheritance of a stop bound is not inheritance of the old execution limit or
an assurance of unchanged dollar risk. A trader choosing Bid/Ask execution
during conversion deliberately changes that execution-pricing recipe. Keep
the existing reviewed entry quantity; do not resize or buy more shares.

## Sessions, data loss and retirement

- Standalone trails select PRE/RTH/POST, expire at the end of today's last
  selected daytime session and never carry overnight. Reject admission with
  no remaining eligible window. Use the authoritative exchange calendar,
  including early closes.
- Converted protection retains its original sessions/deadline. New trailing
  risk presets inherit Chart Risk Entry's PRE/RTH/POST through DataClose.
- Evaluate eligible-print exchange time and current engine time against the
  permitted sessions. Disabled sessions do not update watermark/candle stops
  or submit new children; existing limits retain their stated venue/session
  restrictions. Keep progress through a disabled interval without replaying
  missed historical movement.
- Market-feed interruption pauses pretrigger trailing with its prior stop
  and watermark retained. Resume is explicit, requires restored reliable
  position/feed data and rechecks the original cycle and deadline. Do not
  automatically reset progress or replay historical extrema.
- Healthy verified reducing trails remain enabled through ordinary Disarm
  and Max Day Loss; remaining entry buying is canceled. Data and share
  availability checks still apply. Explicit Cancel Protection, scoped Cancel
  All and KillSwitch cancel their protection; KillSwitch does not flatten.
- Flat, reversal, lost cycle continuity, expiry or engine termination retire
  the trail. Remove its trailing line and cancel applicable working children
  plus remaining linked entry buying. A broker-bound order with unconfirmed
  outcome retains its normal order row/line until reconciled.
- Graceful shutdown records cancellation intent and attempts confirmation.
  A crash cannot guarantee broker cancellation. Before startup monitoring or
  fill synchronization can act, retire prior-run trail intent and reconcile
  children. No pretrigger trail or preset intent resumes across engine runs.
- Retain audit records, reasons, trigger/reference snapshots and broker child
  linkage. Retirement means terminal instructions, not erasure of evidence.

## Entry and chart controls

Order Ticket and hotkey/Deck entry share the same validated configuration:
side, Position Percentage, trail mode/value or candle timeframe/buffer,
sessions and execution recipe. Require explicit positive trail distance;
do not invent a dollar/percentage default.

Use existing Link Group venue resolution and pinned-panel restrictions.
All matching grouped charts for the same venue+symbol reflect engine state;
closing charts, tickets, workspaces or browser windows does not retire a trail
or release data it still needs.

Draw a horizontal line at the actual derived stop, with mode/state and details
for watermark or candle reference, distance/buffer, execution recipe, quantity
intent, protection floor and deadline. Waiting states must not imply protection
is already monitoring. A trail with no initial derived price has no fabricated
stop line; a preset's valid fixed fallback still has its protective line.

Provide scoped Edit, Resume and Cancel through the pill/order controls.
Disable dragging or arrow-moving a derived trailing price; parameter edits
preserve the SELL floor or COVER ceiling. Edit only within the current trailing
mode and before trigger; pending/uncertain/working children use their ordinary
order-management rules. No reverse conversion or between-trail-mode switch
initially.

Offer conversion on the eligible protection pill and a scoped hotkey resolving
the single eligible owner in its venue+symbol context. Never guess an order
from a different venue or an ambiguous selection.

Reuse Live Held Stop-Limit Acknowledgement for the same live account identity,
with clear engine-held/run-bound lifetime details. Do not add another per-order
acknowledgement. Enabling custody does not itself submit an order.

## Numeric and engineering constraints

Percentages use their named reference: Trail Percent uses HWM/LWM, candle
buffer uses its low/high boundary, Quote Offset uses its selected Bid/Ask,
and Limit Cushion uses the frozen trigger. Validate finite values and positive
prices, Trail Distance greater than zero, buffer/cushion at least zero, and
Position Percentage in (0,100]. Reject invalid resulting stop/limit or
rounded-zero quantity rather than fabricating a price or share.

Use the existing execution Core, single-writer lifecycle, durable events,
position cache and chart/order projections. Trigger sizing must not add a
broker position-query round trip. Share reduction/commitment checks across
ordinary SELL/COVER and trail/cleanup paths so later orders cannot bypass a
reservation.

The existing implementation lacks reliable Position Cycle binding, COVER
commitment parity, execution-grade Book readiness and trustworthy candle
provenance. Implement those contracts explicitly rather than inferring them
from opening timestamps, UI bars, optional tick recording or lossy display
streams. Acquire engine-owned Ticker, Book and candle demand only as needed,
independent of mounted charts and within existing quota management.

Go owns WebSocket contracts; regenerate TypeScript rather than edit it.
Keep high-frequency data out of React state. Carry new preset settings through
normalization/import/export, and reject unsupported execution modes explicitly.
Update relevant READMEs when the behavior is implemented.

## Concrete acceptance examples

| Scenario | Required result |
| --- | --- |
| Long HWM $10, dollar trail $0.30 | Stop $9.70; HWM $10.20 advances it to $9.90; a decline cannot lower it. |
| Long HWM $10, 2% trail | Stop $9.80; HWM $11 advances it to $10.78. |
| Short LWM $10, dollar trail $0.30 | Stop $10.30; LWM $9.50 lowers it to $9.80; a rebound cannot raise it. |
| Long completed-candle low $9.80, $0.02 buffer | Stop candidate $9.78; a later lower candle cannot loosen it. |
| Trailing preset Initial Stop $9.80; fresh HWM $10; $0.30 trail | Initial protection stays $9.80; HWM $10.20 raises the stop to $9.90. |
| Linked entry fills 100; confirmed manual SELL fills 40 | Owned protection becomes 60; an unrelated BUY of 20 does not restore it to 80. |
| Standalone 50% trail, position grows from 100 to 140 before trigger | Reserve 70 shares at trigger; an addition after trigger does not enlarge it. |
| Trigger reserves 70; quote unavailable; external reduction leaves only 50 available | Do not submit 70 or clip to 50; show the confirmed conflict/rejection outcome. |
| Stop $10.20; chosen SELL Bid $9.70 with zero offset | Quote recipe yields $9.70; the stop bound does not preserve an older execution limit. |
| Previously used candle boundary later corrected | Keep the boundary snapshot used for trading; do not replay or re-evaluate that candle as new. |
| Only PRE and POST enabled | RTH neither advances nor triggers the trail; POST needs a qualifying new-window candle to advance Candle Trail. |
| Position closes, reopens, or engine restarts | Retired trail never resumes; reconcile any broker child and require explicit new protection. |
| LIMIT partially fills | Keep the same remainder working; no chasing or automatic new trail. |
| Linked BUY fills late after the main exit triggered | Cover only newly uncovered shares through cleanup/manual recovery, never a restarted trail. |

## Validation and implementation boundary

This task has changed design documentation only. Validate local Markdown
targets/anchors, whitespace and git diff --check. Engine/UI checks and hosted
CI are not required for prose-only design documentation.

Implementation must cover the examples above and meaningful lifecycle/race
cases: partial/late fills, deduplication, external reductions, competing exits,
pending quotes, same-side reconnect ambiguity, feed/candle causality, restart
before first fill, manual conversion races, cancellation uncertainty, disarm,
session boundaries and early closes, quota/readiness without charts or
recording, chart scoping and settings import/export. Run focused engine/UI
tests and required build/type/lint/race/generated-contract checks; before code
handoff satisfy full hosted CI on the pushed main commit per AGENTS.md.
Use sim/paper for runtime validation; this session authorizes no live orders.

## Comments

- 2026-10-20: Earl accepted recommendations Q1–Q7, Q8–Q18, Q19–Q22 and Q23–Q33.
  Preserved the [discussion](discussion.md), consolidated this specification,
  and reviewed consistency without finding a new product-decision blocker.
- All decision rounds are confirmed and the frontier is empty. Finalized the
  shared design for documentation handoff; implementation remains a separate
  request. Repository standing Git authorization applies to these documents.
