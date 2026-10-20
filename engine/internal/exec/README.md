# Execution Core

## Trailing stops

`SubmitTrail` admits one reducing SELL/COVER strategy per venue and symbol.
Price and percent modes retain a favorable high/low watermark; Candle Trail
uses a trustworthy completed 10s, 1m, 5m, 15m, 30m or 60m reference. Stops only
tighten, with SELL rounded down and COVER up on the existing price grid.
Standalone sizing is a position percentage resolved to whole shares at trigger,
then frozen. A quote recipe latches in `WAITING_QUOTE` and reserves those shares
until a current acknowledged Book is usable. There is no market fallback,
repricing, chasing, automatic rearming or broker-native trailing order.

Trails select today's PRE/RTH/POST windows and expire at the scheduled last
selected close, including early closes. Feed gaps pause pretrigger evaluation
until explicit Resume. Flat, reversal, lost broker continuity, expiry and engine
termination retire the position-cycle-bound intent permanently. Graceful
shutdown requests child cancellation; crash recovery reconciles uncertain
children and never reposts a trail. Ordinary Disarm/day-loss controls leave
healthy reducing protection enabled; Cancel Protection and KillSwitch cancel
it without flattening.

Chart Risk Entry defaults to Fixed protection. A trailing preset retains the
chart-selected initial stop as a floor, sizes from the original cushion-adjusted
limits and reviewed quantity, and begins trailing after confirmed fills. Missing
trail references leave that fixed floor active. Partial fills grow only the
entry's coverage; confirmed unrelated reductions retire owned shares before
independent additions. Triggering cancels remaining buying. Healthy uncovered
late fills receive separate linked LIMIT cleanup, with frozen main-child
quantities and quote reservations counted once.

An absolute position reduction arriving before a working linked exit's fill
temporarily blocks further exits and cleanup while a combined order/position
snapshot establishes ownership. The existing LIMIT and reservation remain.
Raced, failed or incomplete snapshots leave a visible attribution-pending
reason and require venue reconciliation; they never infer a match by quantity.
Confirmed terminal exits may be absent from open-order snapshots. Late fills
discovered during reconciliation require manual recovery.

`ConvertProtectionToTrail` changes healthy filled primary WAITING/ARMED
protection atomically, retaining its owner, current confirmed stop bound and
deadline. Missing data, stale observed state, paused/uncertain/working children
and zero-fill setups reject conversion. `EditTrail` changes parameters within
the existing mode and cannot loosen the bound. Trailing presets, including
zero-fill preparation and fixed fallback, expire across engine runs.

The execution writer owns prices, reservations, position cycles and durable
submission intent. Engine-owned Ticker/Book/K_1M demand survives chart closure;
Go-owned WebSocket projections expose state and reference snapshots. Validate
through `go test ./internal/exec` and lifecycle race tests; use sim/paper only.

## Protective price evidence

Engine-held protective stops use separate price evidence. Charts, alerts,
entries, simulation marks, manual exits and native broker stops retain their
reported prices. A price more than 1% (or two order-price increments) from the
accepted protective reference, or outside a coherent fresh book plus tolerance,
requires corroboration. Supporting book evidence admits a genuine gap immediately;
a distinct nearby eligible print can corroborate only without a usable contrary book.

An unresolved adverse crossing activates through the existing sizing,
reservation, session and execution gates after 100 ms from local frame receipt.
Repeats and cached placement/edit/fill checks cannot restart that deadline.
A recovery refutes a crossing only with usable agreeing book evidence.
The execution loop services overdue work before later evidence; this budget
does not guarantee feed, scheduling, submission or fill latency. Missing receipt
metadata grants no extra wait. Cancellation, pause, expiry and position-cycle
changes invalidate pending decisions.

Favorable unconfirmed prints never initialize or tighten PRICE/PERCENT
watermarks, including at timeout. Standalone trails wait for a supporting book
or second nearby print; Chart Risk Entry retains its fixed fallback. Completed
candle calculations are unchanged, with their print trigger guarded.

Book proof requires acknowledged current coverage/connection, positive separate
side clocks, no clock regression, at most 250 ms of local residence for both
sides, positive non-crossed prices and spread at most 1% of midpoint. Unchanged
side clocks retain their original local age. Provider clocks are not subtracted
from local time. Book support allows one spread or two order-price increments;
print proximity allows 0.25% or that same increment floor. These are uncertainty
tolerances, not legal trade tick sizes or a claim of NBBO coverage. Each protected
order requests optional background BOOK demand; missing quota never blocks its
required TICKER lease. Candidate decisions use `exec.protection_price` logs.

See the [approved plan](../../../docs/plans/2026-10-20-protective-stop-spike-filter.md)
and [calibration evidence](../../../.scratch/protective-stop-spike-filter/calibration.md).
Validate with `go test ./internal/exec` and the lifecycle race suite.

## Existing execution lifecycle

Broker-neutral lifecycle, gates, routing, reconciliation, and round-trip
tracking. Link Groups choose the execution venue; there is no runtime global
venue fallback. The account poller requests every configured live venue for
risk and each venue demanded by an open Account or grouped Chart Risk Entry
panel, deduplicated per venue.
Account failures retain the last snapshot and become stale after five
intervals; stale live data blocks new openings until the user unlocks again,
while reductions remain allowed. Max Day Loss aggregates configured live
venues only. The Account projection uses scheduled NYSE close-to-close cycles:
closing fills accumulate cycle P&L, open symbols retain partial-exit
realization, and a close rebases carried positions to their latest marks.
Broker-reconciled positions also carry an opening-fill timestamp when it is
known from persisted/live fills; carried or externally opened positions remain
unknown rather than using reconciliation time.
Alpaca keeps broker-authoritative Day P&L in the display; Moomoo calculates it
from its persisted equity baseline and cash-flow adjustment. Realized P&L is
the local cycle ledger and remains visible after flattening. Test:
`go test ./internal/exec`.

Chart Risk Entry admits a linked BUY/SELL STOP_LIMIT pair as one durable local
event. Both triggers stay engine-held through PRE/RTH/POST until scheduled
DataClose. `SubmitRiskEntry` uses fixed dollars, Cash %, or BP % as a loss budget;
whole-share sizing uses cushion-adjusted execution limits and the funding cap,
and `MaxQty` prevents increasing the approved preview. Admission requires fresh
account data, confirmed flat positions, no other working orders, opening gates,
the live held-stop acknowledgement, and successful ticker subscriptions. Missing
or stale eligible trades do not block acceptance: the durable entry stays
WAITING with `Waiting for market data` until a fresh trade arms or activates it.
Immediate activation requires a healthy, fresh eligible trade checked after
subscription acquisition; account age and DataClose are rechecked after that
acquisition too. Existing DataClose expiry and feed/restart pauses
still apply; no UI retry is needed for a quiet symbol.

The exit covers only this entry's confirmed fills. Stop activation cancels the
remaining buy; late fills receive separate durable exit children, including
reservations for paused protection. Disarm/day-loss cancel further buying while
healthy reducing linked exits remain enabled. Cancel Protection cancels every
linked leg and leaves shares open. Restart/feed/broker gaps pause local custody
for reconciliation and manual Resume; ambiguous children are never reposted.
Snapshots and recovery update protection from confirmed fills; newly uncovered
late fills discovered by reconciliation require manual Resume even with healthy quotes.
Marker edits retain cushions, resize only before buy activation against the
original dollar budget with the reviewed quantity as a ceiling, and lock quantities thereafter. Commands carry observed
held and entry phases so activation during a drag rejects a stale amendment.
Protection failures and DAY expiry can leave shares open; the UI reports them.

Protection warnings live in the selected Account panel, once per symbol.
They use current broker-verified positions, rather than the historical entry
minus linked-exit sum. Unresolved problems trigger a five-second, read-only
broker snapshot, coalesced per venue and retried/rechecked every 15 seconds.
Concurrent order/position changes invalidate a result; failed checks remain
unverified. These observational snapshots never synchronize, resume, or
activate risk protection or restore a disrupted broker stream's readiness gate.
A confirmed flat or reversed position durably records
the entry's cumulative filled quantity, preserving its failure details while
retiring that exposure's warning. A later trade cannot revive it; additional
late entry fills can create a fresh warning.

An EXTENDED DAY STOP_LIMIT submitted during pre/postmarket is persisted as one
engine-held parent order; no venue order or buying-power reservation exists
before trigger. Only fresh Last-Eligible prints can trigger it. The engine compares
tick-aligned triggers at their canonical decimal value so chart rounding noise
cannot prevent an exact touch; market prints retain sub-cent precision. On trigger,
the same order row becomes a linked venue LIMIT child, with a durable child client
ID written before submission. Restart recovery adopts a found child but never
reposts an absent or ambiguous activation. Feed loss, restart, and clean exit
pause pretrigger custody for manual Resume; clean exit also persists cancel
intent for working children and asks before forcing exit if cancellation cannot
be confirmed. Live custody requires explicit per-account acknowledgement.

DAY Limit-if-Touched uses the same held-parent lifecycle in whichever PRE, RTH,
or POST segment is currently active. BUY/COVER activates at or below its
trigger; SELL/SHORT at or above. The trigger and limit are independent, sizing
is fixed at submission, and the child is always LIMIT. A previewed segment
deadline is checked again at submit; live LIT requires a separate identity-
scoped acknowledgement.

Venue cancel/replace results remain pending until an authoritative broker
event confirms them. Rejections restore the prior confirmed price; ambiguous
outcomes retain the requested value and block another replace until
reconciliation. Chart price changes update price only; allowable remaining
quantity stays governed by Core and the adapter. `Cancel Last Order` treats a
held parent and its child as one intention in the selected venue+symbol scope.

Position-% SELL STOP_LIMIT Action Templates from chart gestures, hotkeys and
Deck Buttons use a local DAY engine-held parent in RTH and pre/postmarket. The
parent holds no broker quantity until an eligible trigger; Core resolves the
configured percentage against the current long position in its ready venue
cache, then freezes that share count for the child LIMIT. A ready flat position
rejects the parent permanently; unavailable position or open-order data pauses
it until a fresh broker baseline and manual Resume. Every SELL submission and
held SELL activation reserves shares against local and external working exits.
Ambiguous SELL submits remain reserved until an ACK, lifecycle event or broker
snapshot resolves them. Account Flatten pauses held SELL parents and blocks new
SELLs until a ready snapshot confirms the venue flat. Explicit RTH is accepted
only during RTH; supported parents expire at regular open, scheduled regular
close (including early close), or DataClose for premarket, RTH, and postmarket
custody respectively. Deferred sizing is not available for GTC/overnight routes;
the manual ticket retains immediate sizing.
