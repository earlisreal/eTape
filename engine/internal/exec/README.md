# Execution Core

Broker-neutral lifecycle, gates, routing, reconciliation, and round-trip
tracking. Link Groups choose the execution venue; there is no runtime global
venue fallback. The account poller requests every configured live venue for
risk and each venue demanded by an open Account panel, deduplicated per venue.
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

An EXTENDED DAY STOP_LIMIT submitted during pre/postmarket is persisted as one
engine-held parent order; no venue order or buying-power reservation exists
before trigger. Only fresh Last-Eligible prints can trigger it. On trigger, the
same order row becomes a linked venue LIMIT child, with a durable child client
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
