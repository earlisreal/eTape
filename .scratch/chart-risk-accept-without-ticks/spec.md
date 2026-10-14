# Chart Risk Entry acceptance without a recent trade

Status: approved — Earl confirmed the design on 2026-10-15.

## Behavior

With auto-send enabled, completing either a drag or two-click placement submits
the valid Chart Risk Entry pair without Enter. A completed draft price correction
also submits on mouse release; selecting or editing only the first BUY does not
complete the pair. A click without a price movement does not submit a correction.
Keyboard draft adjustments retain Enter submission.

A missing, untrusted, or stale last eligible trade does not block acceptance.
After successful ticker subscriptions, eTape durably accepts the linked pair.
If a trusted trade is still fresh and meets BUY at acceptance, activation can
start immediately. Otherwise the entry waits for a new eligible trade; missing
market data displays `Waiting for market data`. The next qualifying fresh trade
activates without another UI action. The normal DataClose expiry and cancellation
remain in effect. There is no three-second UI retry window or polling loop.

Fresh account data, sizing/funding limits, reviewed quantity caps, flat/no-working-
order checks, live acknowledgement, trading locks, and successful ticker
subscription remain required. Failed route queries and definite rejections stay
visible; uncertain submissions cannot be resent. Existing feed/broker gaps and
restart recovery retain their pause and manual Resume behavior.

## Acceptance

- Missing or stale trade data allows exactly one durable pair, with no broker
  child until a qualifying fresh trade; a fresh already-satisfied trigger can
  activate immediately.
- A cached trade that expires during ticker acquisition cannot activate a pair.
- Fresh account data and ticker acquisition failures still block acceptance.
- Auto-send submits a corrected complete draft once on release, with its updated
  quantity; incomplete selection, an unchanged click, cancellation, and uncertain
  outcomes cannot create an extra submission.
- The waiting status is visible on the accepted chart marker and clears when a
  fresh trade arms or activates the entry.
