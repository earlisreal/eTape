# Tick recording for wick investigations and future order-flow charts

Status: implemented on 2026-10-09 — approved on 2026-10-08

Approved implementation plan:
[tick recording plan](../../docs/plans/2026-10-08-tick-recording.md).

## Goal and accepted decisions

Retain local market-data evidence so Earl can later ask an agent to investigate
which Reported Prints produced suspicious candle wicks, compare those reports
with available bid/ask observations, and refine filters after the evidence is
understood. Design the retained data to support future footprints, delta, and
volume profiles.

1. **Recording only in this phase.** No investigation UI, export command,
   automatic anomaly detector, or candle-filter change. Earl will request the
   investigation separately.
2. **Future charts are trade-based.** Footprints, delta, and volume profiles are
   the target. Historical Level 2 heatmaps and depth replay are not a capture
   requirement for this phase.
3. **Capture every existing TICKER subscription**, including the Scanner warm
   pool. Recording does not create additional TICKER demand.
4. **Start automatically while the engine runs.** An unexpected wick should
   already have evidence without a manual start action.
5. **Batch asynchronously and keep the live feed responsive.** During healthy
   operation target commits within one second. An uncommitted crash tail is
   acceptable; recording failures and missing intervals must be explicit.
6. **Best-effort BOOK for all recorded symbols.** Retain top bid/ask only.
   Ordinary demand has priority; missing book coverage is explicit. Earl
   accepted this recommendation and asked for clarification of best-effort:
   admission depends on available capacity and existing BOOK can be shared.
   One-minute minimum hold prevents an unconditional immediate-yield promise.
7. **Automatic oldest-first retention.** Keep up to 30 calendar days within a
   10 GiB total archive cap and a 2 GiB free-disk reserve. The cap may shorten
   history. Preserve an investigation copy outside managed retention.
8. **Follow existing subscription lifetimes.** Retain recorded history after
   switching symbols without adding session-long TICKER demand.

The attached AIXI screenshot shows repeated deep lower wicks on 10-second bars
around 04:06 ET and a substantially different one-minute range. It does not
show bid/ask observations at those trades' times and does not establish a root
cause. Do not treat a report below the displayed bid as automatically invalid.

## Settled design tree

- Purpose: recording now -> investigation later -> evidence-based filters later.
- Future consumer: trade-based charts -> retain exact reports, volume and
  Aggressor Direction; full depth storage is not required.
- Recording universe: all existing TICKER -> representative Scanner/panel
  throughput validation; follow existing symbol subscription lifetimes.
- Activation: automatic -> startup, disable behavior and demo-data isolation.
- Durability: asynchronous/batched -> bounded queues, crash tail, failure
  reporting and orderly shutdown.
- Bid/ask evidence: best-effort auxiliary BOOK -> separate optional admission,
  top-of-book observations and explicit clock/coverage limits.
- Retention: 30 days / 10 GiB / 2 GiB reserve -> oldest-first closed-segment
  deletion, physical WAL budgets and safe investigation copies.
- Evidence model: approved below -> capture boundary, raw provenance,
  engine acceptance/eligibility, K_1M context, and queryable indexes.
- Implementation plan and acceptance checks: [approved plan](../../docs/plans/2026-10-08-tick-recording.md).
  Earl confirmed shared understanding in Q9 ("looks good"). The interview is
  complete; implementation remains a separate request.

## Current-code evidence

- [feed.Tick](../../engine/internal/feed/feed.go) retains symbol, provider
  sequence, exchange and provider-receipt milliseconds, price, size, turnover,
  Aggressor Direction, normalized/raw conditions, delivery provenance and the
  independent Range-/Last-/Volume-Eligible permissions. It has no eTape ingress
  receipt time. Normalization omits some optional fields and their presence.
- [OpenD Client](../../engine/internal/feed/opend/client.go) receives raw frames
  before a bounded 1,024-frame push queue which can drop. An observer after that
  queue cannot recover those frames. Cache replies also pass through this reader
  before request resolution.
- [Ticker cache seeding](../../engine/internal/feed/opend/backfill.go) sorts by
  exchange time and sequence. [OpenDFeed](../../engine/internal/feed/opend/opendfeed.go)
  holds live ticker batches until the cache seed is emitted. Delivery order
  downstream of this gate is different from socket receipt order.
- [MD core](../../engine/internal/md/core.go) performs per-symbol ET-day sequence
  high-water deduplication before stamping eligibility and applying bars. Tape,
  UI-update, and browser ring outputs are bounded and cannot own raw capture.
- [Tick aggregation](../../engine/internal/md/tickagg.go) uses independent
  range, last-price and volume permissions; a Range-Eligible Print can make a
  wick without moving the close. Already-finalized tick buckets reject late
  arrivals. None of these price rules compare a report to the current bid/ask.
- [Bars](../../engine/internal/md/bars.go) can conservatively trim completed
  10-second H/L to finalized authoritative K_1M H/L when O/C are inside that
  range. Existing [tests](../../engine/internal/md/bars_test.go) cover AEHL upper
  and PPCB lower wicks. Final bar archives can therefore lose the original wick
  extent and are insufficient evidence alone.
- Chart demand is TICKER + K_1M + K_DAY; Tape and Scanner warm demand are TICKER.
  DOM demand includes BOOK. Watchlist polls snapshots without TICKER demand.
  Engine-held orders obtain TICKER demand; Open Positions do not independently
  guarantee a live tick subscription. See [demand profiles](../../engine/internal/feed/feed.go),
  [UI commands](../../engine/internal/uihub/commands.go),
  [Scanner](../../engine/internal/scan/scan.go), and
  [engine composition](../../engine/cmd/etape/main.go).
- BasicQot has no BBO. The first BOOK levels supply bid/ask. Existing
  [decoding](../../engine/internal/feed/opend/decode.go) combines separate bid/ask
  server-receipt timestamps using their maximum. These are not trade exchange
  timestamps; optional missing/zero values must remain distinguishable.
- [Subscriptions](../../engine/internal/feed/opend/subman.go) union demands by
  symbol/subtype. An additional BOOK costs one slot per symbol unless BOOK is
  already subscribed. Admission currently handles the whole symbol profile:
  simply adding BOOK can deny TICKER too. Auxiliary capture must preserve
  foreground subscriptions, quota headroom and unsubscribe hysteresis.
- [Current SQLite store](../../engine/internal/store/store.go) shares a single
  writer queue between bar archives and synchronous execution persistence. Its
  asynchronous batch failure policy only logs and clears the batch. It has no
  raw-tick schema. Do not reuse that failure policy for recording evidence.
- The existing [Go dependencies](../../engine/go.mod) include modernc.org/sqlite.
  An isolated recorder database can use it without introducing a new dependency.
  Ordinary SQLite tools can inspect local evidence later; a new query product
  is outside this phase.

## Provider evidence checked on 2026-10-08

- [Tick-by-tick fields](https://openapi.moomoo.com/moomoo-api-doc/en/quote/quote.html)
  include a sequence identifier, timestamp, receipt timestamp, direction, type,
  type sign, delivery type and optional high-precision quantity. Preserve what
  was actually supplied rather than inventing unavailable execution-venue IDs
  or correction/cancel links.
- [Cached tick access](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-ticker.html)
  is limited to the latest 1,000 reports, not arbitrary historical tick backfill.
  [Reconnect pushes](https://openapi.moomoo.com/moomoo-api-doc/en/quote/update-ticker.html)
  supply at most 50 recent reports. An interrupted/unsubscribed interval is not
  proven complete merely because cached reports arrive afterward.
- [BOOK fields](https://openapi.moomoo.com/moomoo-api-doc/en/quote/update-order-book.html)
  expose separate server bid/ask receipt times which can be missing or zero.
  US availability is not uniformly described in the protocol comments and
  examples. Retain optional-field presence and eTape ingress chronology; do not
  promise exact exchange-time synchronization.
- [Provider quote FAQ](https://openapi.moomoo.com/moomoo-api-doc/en/qa/quote.html)
  describes US data sourced from Nasdaq and NYSE Arca. Captured top-of-book is
  the observed provider book, not a promise of consolidated NBBO coverage.
- [Quota rules](https://openapi.moomoo.com/moomoo-api-doc/en/intro/authority.html)
  charge each symbol/subtype separately. Recorder subscriptions must go through
  existing quota arbitration.

## Approved design constraints

- One isolated local SQLite recorder under `~/.eTape/`, separate from execution
  persistence, with bounded asynchronous capture and explicit recording health.
- Accepted budget: 30 calendar days, 10 GiB including database/WAL
  files, and a 2 GiB free-disk reserve. Delete the oldest closed capture segments
  first. Rotate bounded segments so a busy current day cannot create an
  indefinitely growing active file. Preserve an investigation by copying its
  closed files outside the recorder-managed directory; do not add pinning UI.
- Stamp connection/run identity, ingress ordinal and eTape receipt time at the
  OpenD socket reader, before raw push loss, cache/live reordering or MD dedup.
  Capture an allowlist of market-data messages, including cache replies;
  exclude trading/account/credential protocol families.
- Keep queryable individual Reported Print observations, including duplicate
  and replay deliveries, exact source fields and optional presence. Preserve
  source payload once per tick message when needed to retain unknown fields.
  Do not use provider sequence as the observation primary key or overwrite a
  differing report with the same sequence.
- Preserve the original list index before cached ticker sorting. Identify an
  observation by run, local connection, ingress ordinal and original list
  index, independently of provider sequence. Carry that source reference into
  normalized Tick/Bar inputs without exposing it through WebSocket messages.
- For GetKL cache responses, capture the matching request's symbol, timeframe
  and adjustment descriptor. The response itself does not identify whether its
  list is K_1M or K_DAY. Do not infer timeframe from the payload alone.
- Retain the engine's actual processing order, acceptance disposition and
  stamped eligibility with a policy/build version, separately from raw
  observations. These explain what affected the live candle without forcing
  future order-flow calculations to inherit current bar deduplication rules.
- Retain a separate time series of observed best bid/ask prices and sizes with
  ingress chronology and original optional side receipt times. A startup cache
  book must never be attached to older cache ticks as their historical BBO.
- Retain authoritative K_1M revisions and sparse engine records for bucket
  creation/anchor changes, independent 10s/shadow-minute late dispositions,
  finalization and clamp-before/after. Include the authoritative minute actually
  used and its source, including an engine-origin basis record for archive or
  alternate-history inputs. Existing final bar history cannot substitute for
  this evidence. Do not mirror every in-progress bar, indicator, larger
  timeframe or full historical seed batch.
- Persist coverage boundaries, reconnects, queue drops, decoding failures,
  recorder failures and incomplete shutdowns. Missing evidence is a Data Gap,
  not proof of no trades. A gap marker cannot depend on the same full data queue
  it needs to report.
- Keep recorded evidence out of React state and out of execution decisions.
  This phase does not hydrate live tape/marks or replay stored ticks on startup.
- Indexed SQLite inspection by symbol, exchange-time range and receipt-order
  range is sufficient for the later agent investigation. Future chart rollups,
  price binning and direction inference remain separate work.
- Auxiliary BOOK is separate from ordinary symbol demand. Admit
  ordinary subscriptions first and BOOK last; keep RPC batches, entitlement
  failures, reconnect replay and cache seed work separate so BOOK failure cannot
  quarantine or delay ordinary TICKER processing. Existing BOOK ownership is
  shared without another slot. Record BOOK starvation separately from TICKER
  coverage. Preserve ordinary hysteresis and fresh account-wide quota readings.
- Optional BOOK cannot promise zero impact on future demand: provider minimum
  hold is 60 seconds. Reserve headroom, decline optional admissions while
  ordinary work is pending, surrender optional ownership under pressure when
  legal, and request an authoritative quota refresh after release. Never
  manufacture local quota credit or unsubscribe a DOM-owned BOOK.
- Use existing low-frequency `sys.events` messages and persisted system events
  for recording warnings/recovery. Publishing to the hub and appending the
  event store are separate operations and stay off ingestion/MD threads. Add
  no new recording UI, per-print wire topic or generated contract fields.

## Approved implementation outline

1. Add recorder schema/writer/lifecycle and failure-aware health/coverage records.
2. Add early OpenD ingress metadata and allowlisted tick/cache/book/K_1M capture.
3. Add minimal MD processing provenance needed to explain actual bar input and
   finalization, without changing current eligibility, deduplication or filtering.
4. Wire startup/shutdown, configuration, retention and decided BOOK demand.
5. Update engine/feed/storage/config guides and main operational documentation.
6. Validate observation fidelity, seed/live overlap, missing timestamps,
   coverage gaps, crash/restart, writer failures and bounded hot-path behavior.
   Include existing wick examples and a sustained-load check for execution and
   chart latency. Run the full Windows CI-equivalent checklist after execution
   of the approved plan; any wire changes regenerate from Go owners.
7. Follow the commit/main-integration/hosted-CI workflow in
   [AGENTS.md](../../AGENTS.md). Earl approved this plan on 2026-10-08; commit its
   documentation now. Implementing the recorder requires a separate request.

## Remaining risks

- No recorder can recover old tick/BBO evidence already lost before this feature.
- Full-market completeness and exact NBBO alignment are not available guarantees
  from this feed or from a bounded local recorder.
- A separate database isolates the writer queue but still shares disk and CPU;
  measured throughput and latency must determine safe retention/capacity.
- Optional BOOK can temporarily consume non-releasable slots after a symbol
  switch. A missing book or missing source timestamp must remain unknown, not
  be converted into a made-up historical bid/ask match.
- Below-bid reports and the screenshot's root cause remain investigation
  questions. Recording must preserve them rather than applying new filters.

## Planning validation

Read-only code and official provider-document inspection. No live account/feed
requests, orders, implementation changes, runtime capture or tests performed.
Local Markdown links and whitespace passed. Q1–Q9 are settled, and the plan is
approved. This planning task adds documentation only; the approved recorder
acceptance tests apply when implementation is requested.
