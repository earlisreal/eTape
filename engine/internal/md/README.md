# Market Data Core

## Execution evidence

With `ExecutionData` enabled, feed ingress and a dedicated execution event lane
apply backpressure. Eligible live prints, verified BBO replacements and completed
candles share that lane; chart/display channels remain independent. Candle
updates precede the live print that observes completion, and execution rejects
an automatic update against a causally earlier cached print.

OpenD subscription acknowledgements carry coverage revisions independently of
optional recording. Connection changes, transport losses and coverage changes
invalidate readiness. Book evidence must match the current acknowledged lease;
an unchanged quote remains usable during healthy continuity. Basic Quote
timestamps do not establish execution BBO readiness.

Execution candles use continuously observed real 10-second ranges and live
authoritative one-minute K-lines; contiguous verified minutes form larger
intraday references only after provider minute rollover proves completion.
Seeds, display synthesis, volume-only or incomplete ranges
do not establish execution references. After a new subscription or gap, Candle
Trail may wait for a fully observed qualifying candle. Once used, a reference's
identity and boundary snapshot cannot be rewritten by corrections or reloads.

## Market-data projections

An optional recorder observes existing MD decisions: dedup rejection, stamped
eligibility, independent 10s/shadow lateness, sparse bucket basis/finalization
and actual K_1M clamps. Evidence includes source references and engine-history
origins; it does not change price, candle, eligibility or dedup rules. Inbox
loss and shutdown boundaries keep missing processing coverage distinct from
rejected reports. See [tick archive](../tickstore/README.md).

`EstimatedLULD` is a display-only derived value. It uses only stamped
Last-Eligible prints, a bounded five-minute local window, the injected core
clock, Moomoo's previous close for the documented price bucket, and the dated
allowlist in `luld_registry.json`. It is available only during scheduled RTH;
unknown or expired symbols are unavailable, and provider/transport interruptions
freeze the last local result. It never gates orders, risk, trading state, or
official LULD claims. `EstimatedLULDUpdate` is coalesced by visible value, so
ordinary ticks do not create a derived update for every print.

Builds books, quotes, bars, ticks, and engine-computed indicators from normalized events. Chart Volume is a UI-local indicator derived from the displayed bar stream; it is not subscribed or calculated here. TICKER creates exchange-time 10-second bars after one centralized Trade-Report Condition policy stamps Range-Eligible, Last-Eligible, and Volume-Eligible permissions; one-minute K-lines support larger intraday resolutions; daily history supports daily/weekly/monthly. Same ordered events must reproduce state. Volume-Only Bars use eligible volume and a trusted prior last-eligible close; unanchored buckets remain held, and older history cannot replace a newer trusted anchor. `DropStats` distinguishes inbox/live-event drops from outgoing UI-update drops; keep-latest marks/books are intentionally not counted. Test: `go test ./internal/md`.

Engine-held stop-limit and Limit-if-Touched triggers consume a separate bounded
execution lane of normalized Last-Eligible prints, preserving sequence and gap
provenance. A detected gap invalidates the cached trigger mark and pauses
pretrigger orders; coalesced chart bars and stale/replayed prices are never
used to trigger orders.

`CalculateVolumeProfile` is a pure captured-report calculation. It reuses the Volume-Eligible condition matrix independently of live candle acceptance. Symbol + ET date + positive int64 sequence identifies a report; identical time/price/size/eligibility repeats count once, direction changes do not matter, and conflicts or missing identities are excluded with Partial reasons. Equally spaced raw-price rows conserve safe-integer share volume. POC ties choose the nearest price midpoint then the lower row; a contiguous Value Area expands by larger adjacent volume, then nearest POC/lower price. Flat prices use one row; empty evidence has no levels. Tests: `go test ./internal/md -run TestVolumeProfile`.
