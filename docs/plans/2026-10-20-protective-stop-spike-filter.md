# Protective-stop spike filtering

Status: **Implemented.** Earl accepted decisions
Q1-Q11 and approved implementation on 2026-10-20. No live orders are authorized.

## Goal

Reduce protective-stop activations and price/percentage trail adjustments
caused by unsupported Last-Eligible Prints, including short clusters. Use a separate
protective-stop evidence decision, preserving existing trade-report
eligibility and the original Reported Prints.

Normal and corroborated prices remain actionable immediately. A suspicious
price may incur one 100 ms confirmation window. This is an intentional
waiting budget, not a guarantee of feed latency, execution-loop scheduling,
broker submission time, or a fill.

## Scope

Apply to engine-held protective Stop-Limit exits, Chart Risk Entry protection
and Engine-Held Trailing Stop triggers. Guard favorable dollar/percentage
watermarks as well as adverse trigger prices. Determine protective exits by
linked protection, trailing intent or confirmed position reduction; this
covers long SELL and short COVER, including generic BUY used to reduce a
short. Deferred Position Sizing protection can still exist before entry fills.
Existing position-readiness, sizing and reservation checks continue to apply.

Guard Candle Trail's print-based trigger. Leave its completed-candle reference
calculations and candle-derived stop adjustments unchanged in this version.

Broker-held stops retain broker custody and trigger rules. Raw tape, chart
OHLC, global Last-Eligible Price, simulated market marks, alerts, entry
triggers and LIT retain their existing behavior. Manual MARKET/LIMIT exits,
Flatten and cancellation receive no confirmation delay. This change adds no
provider, subscription tier, order chasing, new order type, per-order tuning
controls or chart filtering.

## Current-code evidence

The source baseline inspected includes trailing-stop commit `ef2f0179`.
Other work is in progress in this checkout; preserve it and refresh references
before implementation.

- [Held routing](../../engine/internal/exec/held_route.go) distinguishes
  native and engine custody. A local guard cannot control a native trigger.
- [Held lifecycle](../../engine/internal/exec/held_lifecycle.go),
  `handleEligiblePrint`, accepts a fresh sequenced print and caches it before
  evaluating stops. Its current freshness clock is provider `RecvTsMs`.
- [Execution Core](../../engine/internal/exec/core.go),
  [Chart Risk Entry](../../engine/internal/exec/risk_entry.go),
  [trailing prices](../../engine/internal/exec/trailing.go) and
  [trailing commands](../../engine/internal/exec/trailing_commands.go) also
  evaluate cached prices on placement, fills, edits/conversion and Resume.
  Guarding only the live-print loop would leave bypasses.
- [Market-data execution events](../../engine/internal/md/execution.go)
  already order prints, books and candles independently of chart output.
  [EligiblePrint](../../engine/internal/md/update.go) and the execution book
  projection currently omit local arrival time and independent book clocks.
- [OpenD ingress](../../engine/internal/feed/opend/client.go) stamps
  `SourceRef.ReceiptMs` before queueing, even with recording disabled.
  [Decoding](../../engine/internal/feed/opend/decode.go) preserves that
  provenance, while ticker `RecvTsMs` comes from provider `recvTime`.
- [Book decoding](../../engine/internal/feed/opend/decode.go) and
  [book snapshots](../../engine/internal/feed/opend/backfill.go) collapse
  bid/ask server clocks into their maximum. The
  [archive decoder](../../engine/internal/feed/opend/capture.go) already
  preserves independent optional clocks.
- [Core scheduling](../../engine/internal/exec/core.go) currently has a
  one-second maintenance tick; [Clock](../../engine/internal/clock/clock.go)
  supports a separate short deadline. Synchronous handlers and persistence
  can delay when the loop services that deadline.

## SAIQ incident used for validation

Earl supplied the SAIQ chart/closed-trade screenshot while this draft was
being prepared. Bounded read-only archive and execution-history queries on
2026-10-20 found the following. Times below are local frame/event receipt
expressed in ET, not the provider's transaction clock.

- The Chart Risk Entry used fixed engine-held SELL protection: trigger $6.00,
  execution limit $5.94. No trailing state or stop-price adjustment was found.
  $6.32 was the eventual fill price, not the stop threshold.
- Last preceding eligible trade: $6.34 at 04:01:01.419. Last captured book
  before the crossing: $6.30/$6.34 at 04:01:01.427.
- A regular realtime Last-Eligible $5.23 print arrived at 04:01:01.572,
  exactly matching the logged protective ACTIVATING transition. The execution
  event does not retain a triggering-print identity, so this is a strong
  timestamp association rather than a separately persisted causal link.
- The same frame also contained $5.24; a different frame delivered another
  eligible $5.24 at +55 ms. At +98 ms a frame contained $5.24 followed by
  $6.2708. The first captured later book at +154 ms was still $6.30/$6.33.
- The screenshot's $5.00 regular print arrived at 04:01:02.373, 801 ms after
  activation. It did not initiate this stop's activation.

Several distinct low-price trades therefore cannot be treated as adequate
confirmation while usable book evidence contradicts them. Rejection before
the agreed deadline would require treating the earlier book as usable when
the $6.2708 recovery arrives at +98 ms; that book is then 243 ms old by local
receipt. Calibrate this assumption across other data, not just this example.
There is only 2 ms of receipt-time headroom before the 100 ms deadline, so
processing latency and evidence availability must be measured too.

The archive reports incomplete attribution and conservative source-gap bounds;
absence of a captured book update does not prove that no update arrived.
Transaction/broker clocks were roughly 2.2 seconds ahead of local receipt in
this window. The cause of that discrepancy is unproven. Do not substitute
those timestamps for local deadline timing or assert a guaranteed prevention
of this incident before a replay and coverage check.

Use a small synthetic regression based on these prices and relative arrival
times. Include both the hypothesis that the prior book remains usable and
the unknown/stale-book case, which must retain the accepted adverse fallback.
Keep raw runtime captures outside the repository.

An out-of-repository Go-overlay diagnostic has been run against the real
execution Core with the existing fake broker and fake clock. It reproduces
the $6 stop activating zero milliseconds after the synthetic $5.23 candidate,
despite supplied $6.30/$6.34 book evidence and recovery at +98 ms. The test's
no-activation assertion currently fails in three repeated runs, as expected
before implementation.
This is a policy reproduction under the stated usable-book assumption, not
a complete archived replay or proof that the historical book was usable.
Port the scenario into repository regression coverage during implementation.

## Accepted design

### Suspicion, corroboration and refutation

Keep a bounded, accepted protective price reference separate from raw
`lastEligible`. A finalized verdict must also govern cached-price consumers;
a pending or rejected print must not become actionable through another path.

Flag suspicion when either a price jumps unusually far from the accepted
protective reference or lies materially outside usable fresh bid/ask.
Fresh coherent book support may clear a large jump immediately. A large
move alone is never a permanent rejection rule.

Corroborate using either a subsequent distinct eligible print near the new
level, without usable contrary book evidence, or a usable fresh coherent book
supporting the candidate price. A duplicate, seed, replayed cache report or
unverified delivery is not a second observation. Preserve actual delivery
order and batch identity; exchange-time sorting alone is insufficient.

Refute only when a subsequent eligible print returns to the prior normal
range and usable fresh bid/ask agrees. Missing, stale, crossed, implausibly
wide or otherwise unusable book evidence supplies neither confirmation nor
refutation. Quote-quality, proximity and age tolerances are calibration
outputs, not numerical assumptions in this draft.

A book update can resolve an existing eligible candidate. It cannot
originate a stop crossing or a favorable watermark without such a print.
Use independent book-side clocks, local arrival, connection continuity and
subscription coverage. Preserve absent/zero clocks as unknown. Do not claim
NBBO coverage, infer clock synchronization, or fabricate a contemporaneous
book for an older print.

Use local receipt to measure time spent in local queues and book residence.
Track independent provider clocks for presence, progression and provenance;
do not subtract them from local wall time without a verified clock relation.
Receiving a book recently does not prove national coverage or a fresh value
at its upstream source. Calibration must state exactly which evidence is
usable under those limitations.

### Timing and asymmetric timeout

Start an absolute deadline at the candidate frame's local receipt time plus
100 ms. Carry local receipt through execution independently of recording;
do not substitute exchange time or provider `RecvTsMs`. A candidate that
already spent its budget queued receives no new full confirmation window.

Wake independently of subsequent market events. Service due candidates when
the single-writer loop regains control, without allowing busy input to starve
expiry. Repeated suspect prints, cache reads and automatic retries must not
extend an unresolved adverse crossing's deadline. Record scheduling overshoot
separately from the intended waiting budget.

Only evidence available in the ordered evaluation before expiry may resolve
the candidate. At or after the deadline, resolve the due action first; later
evidence cannot retroactively undo activation. Test equal-time ordering.

| Outcome | Adverse protective crossing | Favorable price/percentage watermark |
| --- | --- | --- |
| Ordinary or corroborated print | Evaluate immediately | May initialize/advance |
| Refuted suspicious print | Discard that crossing | Do not initialize/advance |
| Unresolved at 100 ms | Permit activation through existing gates | Do not initialize/advance |

Recovery in trade price without an agreeing usable book does not meet the
refutation rule. If that crossing remains unresolved, it may still activate
at expiry even when the newest trade has recovered. A 100 ms delay by itself
does not eliminate false activations.

Adverse timeout permission is specific to the protective action. It does
not promote the print into a trusted reference for favorable watermark
changes. One symbol can simultaneously be long at one Execution Venue and
short at another; a downward candidate may permit the long exit while
remaining forbidden as a favorable short LWM.

Activation still creates the existing linked LIMIT child. This policy
neither changes the Limit Cushion nor guarantees execution after a gap.

### Lifecycle and admission

Route live prints, new protection, cached admission/amendment, confirmed
entry fills, trail creation/edit/conversion and untriggered Resume through
the same protective evidence policy. Preserve ordinary raw-price consumers.
Continuations of an already authorized trigger, including late-fill cleanup
and an existing triggered trail waiting for quotes, do not start another window.

Cancel, pause, session expiry, flat/reversal, lost Position Cycle continuity
and real feed gaps invalidate pending actions according to existing lifecycle
rules. Existing gap pause/manual-Resume and run-bound trailing retirement
remain authoritative; a timeout cannot override an unavailable feed or
resurrect retired protection. A suspicious print is not itself a feed Gap.

Amendments invalidate the old instruction's trigger authorization. Reevaluate
the current instruction against current evidence, preserving the original
deadline if the same unresolved crossing still applies. Refuting one
candidate must not erase a later independent crossing.

Request engine-owned BOOK demand for protected symbols while needed, within
existing quota and subscription ownership. Missing capacity or missing book
data leaves corroboration unavailable; it does not disable fixed-stop
protection or prevent its existing trigger-time safety checks.
Do not add a blocking BOOK request to the confirmation path or stall active
protection while waiting for optional book admission. Reuse demand ownership
and make optional acquisition/readiness best effort outside the hot path.

## Bootstrap decision (Q11)

**Accepted:** without an accepted prior protective price or usable fresh book,
treat the first print as unverified. Establish an accepted reference only
from supporting fresh book or a second nearby distinct eligible print under
the same corroboration rule.

An existing fixed stop can still receive adverse activation permission at
100 ms. An unverified first print cannot initialize a new HWM/LWM. Retain
Chart Risk Entry's initial fixed fallback. A new standalone trail remains
in the existing Waiting phase until its starting price is corroborated;
it has no initialized trailing protection before that point. An established
trail retains its accepted watermark and stop while further favorable prices
are unresolved. No new order state or confirmation dialog is proposed.

The implemented policy and read-only sample results are recorded in
[calibration evidence](../../.scratch/protective-stop-spike-filter/calibration.md)
and the [execution guide](../../engine/internal/exec/README.md#protective-price-evidence).

## Calibration before enablement

Use read-only tick archive investigation and deterministic execution replay
to choose jump, book-distance, proximity, quote-coherence and freshness
tolerances. Record the chosen formulas and numerical defaults, rationale,
sample coverage and results before activating the guard. Keep them as one
engine policy initially, without new per-order settings.

The [tick archive](../../engine/internal/tickstore/README.md) retains local
`receipt_ms`, connection/ingress/list identity, print eligibility and optional
independent BBO clocks. Replay local delivery order and frame boundaries.
`ProfileReader` sorts by exchange time and omits BBO/provenance; do not reuse
that projection as a timing study or broaden Volume Profile's API.

Use representative active and quiet symbols, RTH and extended sessions,
sub-dollar and higher prices, supported real jumps and unsupported outliers.
Inspect actual OpenD push-frequency configuration during calibration:
a 100 ms local budget does not promise another delivery within that window.
Archive gaps, absent BBO, cache seeds and unknown MD processing are explicit
unknown outcomes, never evidence that a spike was invalid.

Enable only after measurements demonstrate useful rejection of isolated
unsupported prints, immediate handling of normal/corroborated crossings,
expiry of unresolved adverse crossings without deadline extension, and no
unsupported favorable watermark changes. Report confirmation/refutation/
timeout counts, added waiting and scheduling overshoot. If coverage or
benefit is insufficient, retain existing production behavior and report the
gap; do not invent safe thresholds or silently increase the 100 ms budget.
Follow recorded-data validation with paper scenarios; no live orders are
authorized by this planning task.

Commit only aggregate calibration findings and synthetic regression cases.
Runtime captures, databases, configuration and credentials stay outside Git.

## Implementation sequence and files

1. Inspect/calibrate using existing SQLite evidence and small arrival-ordered
   analysis or test helpers. Avoid a general replay framework or new dependency.
2. Preserve optional independent BOOK clocks in `feed.Book` and OpenD push/
   snapshot decoders. Carry local receipt and side clocks through
   `md.EligiblePrint`, `md.ExecutionBook`, execution equivalents and mappings
   in `engine/cmd/etape/main.go`. Update affected synth/test builders.
   No ingress instrumentation or recorder dependency is required.
3. Add a Core-owned protective evidence evaluator and bounded pending state
   next to `engine/internal/exec/held_lifecycle.go`. Keep raw price caches
   available for unaffected consumers. Add short-clock deadline handling in
   `core.go`; preserve existing activation persistence and broker gates.
4. Wire every protected live/cached path in `core.go`, `risk_entry.go`,
   `trailing.go`, `trailing_commands.go` and `held_lifecycle.go`. Cover long
   and short action permissions without sharing timeout trust accidentally.
5. Record candidate outcomes and reasons through existing execution diagnostics
   with candidate identity/price, reference, book evidence/ages, local receipt,
   deadline and elapsed time. Log candidates/outcomes, not every normal tick.
   Diagnostic persistence remains best effort; ordinary activation retains
   its existing durable lifecycle. Add no UI or new pre-submission DB barrier.
6. Update engine, execution, market-data, feed and external-API guides where
   behavior or metadata changes. Explain the protection-only policy alongside
   the unchanged global statistical eligibility rules. Register the completed
   plan in the plans index without altering unrelated drafts.

## Validation

Use fake-clock tests through real Core inputs and simulated/fake external
broker/feed boundaries, not tests that duplicate the evaluator implementation.
Cover:

- Normal exact-touch crossings and sub-cent prints; large genuine moves
  supported by book; isolated low and high prints; corroboration and strict
  refutation; unresolved recovery with missing BBO.
- The SAIQ-derived contradictory low-price cluster and recovery at +98 ms,
  both with a still-usable prior book and with unusable/unknown book evidence.
  The latter must exercise adverse timeout, not assume prevention.
- 99/100/101 ms boundaries; same-time event ordering; already-aged queued
  candidates; expiry with no more events; repeated-print storms; duplicate
  and batched delivery; bounded pending memory and ordinary-path throughput.
- Favorable long HWM/short LWM, adverse long/short exits and opposite positions
  at different venues. Timeout authorization must never initialize/ratchet
  an unverified favorable watermark.
- Bootstrap; missing/zero/independently old book clocks; crossed/wide books;
  stale anchors; subscription quota denial; recording disabled; reconnect
  and real feed gaps. No invented clock or historical-book match.
- Placement, amendment, confirmed/late fills, conversion and Resume cache
  paths; cancellation/pause/expiry/flat/reversal during confirmation; original
  trigger continuations without a second waiting window.
- Unchanged LIT, entries, manual exits, global marks, charts and alerts.
  Candle Trail reference values remain unchanged while its trigger is guarded.

Run focused feed/OpenD, MD, execution and any changed tickstore/tool tests;
race tests for lifecycle/concurrency; applicable vet/lint and engine builds.
Run contract generation/drift checks if exported contract dependencies
change; regenerate Go-owned contracts rather than editing TypeScript.
Local UI checks are needed only if UI/contract callers change. Validate
Markdown links and `git diff --check`.

After implementation, commit only task changes, integrate/push main and wait
for successful full hosted CI on that exact pushed main commit, including
its engine, race/vet/lint/contract, Windows and UI jobs, per
[AGENTS.md](../../AGENTS.md) and [CI](../../.github/workflows/ci.yml).
For this prose-only draft, validate links/whitespace; engine/UI and hosted
CI gates do not apply.

## Rollout, rollback and limits

Calibrate first, implement and validate the selected defaults, then adopt
the policy consistently for in-scope protection. Do not automatically restart
a running engine or change live orders. Roll back by reverting the guard
change and returning to the prior eligible-print trigger policy; deployment/
restart must respect run-bound trailing retirement and existing protection.

Partial provider book coverage, asynchronous arrival and repeated bad reports
can still misclassify a print. Adverse timeout deliberately favors retaining
protection over eliminating every false activation. Existing LIMIT children
can remain unfilled after a genuine gap. Candle-reference spikes and
broker-native trigger behavior remain outside this change.

The [prior eligibility spec](../../.scratch/trade-report-eligibility/spec.md)
deliberately excluded DOM-distance filtering for statistical prices. This
new policy is explicitly limited to protective decisions; do not silently
change that earlier contract. The
[display-only LULD decision](../adr/0004-estimated-luld-display-only.md)
continues to prohibit using estimated LULD to gate orders.

## Upstream evidence

- [Moomoo US quote coverage](https://openapi.moomoo.com/moomoo-api-doc/en/qa/quote.html):
  provider data is not a certified NBBO contract.
- [Ticker callbacks](https://openapi.moomoo.com/moomoo-api-doc/en/quote/update-ticker.html)
  and [book callbacks](https://openapi.moomoo.com/moomoo-api-doc/en/quote/update-order-book.html):
  asynchronous delivery, with no documented 100 ms corroboration guarantee.
- [Book fields](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-order-book.html):
  independent bid/ask server clocks, including unknown/zero cases.
- [OpenD configuration](https://openapi.moomoo.com/moomoo-api-doc/en/opend/opend-cmd.html):
  configurable subscription push frequency; timestamp precision is not a
  delivery-latency promise.
