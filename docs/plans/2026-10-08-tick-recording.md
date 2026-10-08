# Tick recording for wick investigations and future order-flow charts

Status: implemented on 2026-10-09 (approved on 2026-10-08).

Runtime contract: [tick archive](../../engine/internal/tickstore/README.md).
Measured capacity: [performance evidence](../performance.md).

Decision record and code/provider evidence:
[tick recording spec](../../.scratch/tick-storage/spec.md).

## Goal and scope

Automatically retain the market-data evidence eTape receives so Earl can later
ask an agent to explain a suspicious wick and then refine candle filters.
The retained reports must also support future footprints, delta and volume
profiles. This phase delivers recording only: no investigation UI/CLI, export
feature, order-flow renderer, automatic anomaly detection or filter change.

Accepted product decisions:

- Record all existing TICKER subscriptions, including Scanner warm symbols.
- Start automatically with the real OpenD market-data engine. Sim/paper/live
  execution venue selection does not change capture; synthetic `-demo` data
  does not enter this archive.
- Use asynchronous batches with a healthy-operation commit target within one
  second. An uncommitted crash tail is acceptable; missing evidence is explicit.
- Request best-effort BOOK for recorded US symbols; persist top bid/ask only.
- Retain up to 30 calendar days within a 10 GiB total archive cap and a 2 GiB
  free-disk reserve. Delete the oldest eligible capture segments first.
- Follow existing TICKER subscription lifetimes and unsubscribe grace periods.
  Do not keep visited symbols subscribed through the session for recording.

## Current-code evidence and consequences

1. [OpenD receipt](../../engine/internal/feed/opend/client.go) precedes a bounded,
   droppable push queue. [Cache seeding](../../engine/internal/feed/opend/backfill.go)
   sorts ticks, and [OpenDFeed](../../engine/internal/feed/opend/opendfeed.go)
   holds live ticks behind their cache. Capture and identify observations before
   these steps, including request replies as well as pushes.
2. [MD processing](../../engine/internal/md/core.go) deduplicates before stamping
   condition eligibility. [Tick buckets](../../engine/internal/md/tickagg.go)
   reject finalized intervals independently for 10s and the shadow minute.
   Record actual decisions rather than inferring them from a UI tape snapshot.
3. [Bar reconciliation](../../engine/internal/md/bars.go) can trim finalized 10s
   ranges to K_1M. Raw reports, bucket basis/finalization and before/after
   corrections are needed; the final bar archive can erase the original wick.
4. [Chart/Tape demand](../../engine/internal/feed/feed.go) does not include BOOK.
   [Subscription admission](../../engine/internal/feed/opend/subman.go) currently
   combines a symbol's subtypes. Recorder BOOK must be optional and separate
   from ordinary profiles to avoid denying their TICKER subscriptions.
5. [Execution persistence](../../engine/internal/store/store.go) shares a writer
   queue with bar archives. Use a separate archive and writer; retain the
   installed [SQLite dependency](../../engine/go.mod).

## Capture and evidence contract

Stamp a run ID, local connection generation, ingress ordinal and eTape receipt
timestamp immediately after a frame is read, before response resolution or
push-queue admission. Local identity must work before the OpenD handshake
supplies its connection ID. Retain the original list index before cache
sorting; carry that reference on normalized inputs into the MD core.

Configure the observer on the market-data client only. Allowlist TICKER
push/cache, BOOK push/cache and K_1M push/cache messages. GetKL responses do not
identify their timeframe: retain the matching request's symbol, timeframe and
adjustment descriptor. An unmatched response remains unclassified rather than
being guessed to be K_1M. Keep protocol parsing in the OpenD adapter; persistence
receives normalized recording records and opaque source evidence.

Use a small queryable SQLite schema, with the following logical records. Table
and column spelling can follow the implementation's existing conventions.

| Record | Required evidence |
| --- | --- |
| Run/segment metadata | Run and local connection identity; schema and eligibility-policy versions; engine/build identity and available VCS revision/modified state; session anchor; receipt interval; last committed progress; clean/incomplete closure. Store only recording-related configuration. |
| Source messages | Ingress ordinal, local receipt time, protocol/format/serial, push/cache origin and request descriptor. Preserve the original TICKER and K_1M payload once per message, including unknown fields. Retain BOOK metadata without persisting full depth bodies. |
| Reported Prints | Source reference and original list index; symbol; exact 64-bit provider sequence; raw time text/timestamps and optional-field presence; price, integer size, optional high-precision size and turnover; raw/normalized Aggressor Direction, condition/type, type sign and delivery provenance. Preserve duplicate/replay deliveries and differing reports with the same sequence. |
| Observed BBO | Source/receipt identity; optional top bid/ask prices, sizes and order counts; book type; independent original bid/ask server receipt times and their presence. Empty, zero, missing or stale sides remain distinguishable. |
| Authoritative minutes | Original source time and normalized K_1M bucket/OHLCV; request descriptor, revision/source identity and processing context. Retain the exact minute used when an archive or alternate history source supplies the basis instead. |
| Tick processing | MD processing ordinal; source reference; actual dedup acceptance/rejection; seed/live context; actual stamped Range-/Last-/Volume-Eligible flags; independent 10s/shadow-minute late dispositions. Not-processed and missing-trace are distinct from rejected. |
| Sparse bucket records | Bucket creation and effective anchor changes, including late-supplied quote/history/archive anchors; finalization with its trigger/watermark and OHLCV/delta/tick count; changed clamp before/after with the exact authoritative minute and live/archive context. |
| Coverage/failures | TICKER and BOOK admission/release boundaries, reconnects, transport/routing/decode/inbox loss, recorder overflow/commit failure, disabled/paused intervals and incomplete restart. Identify the affected lane and counts/ranges where known. |

Prices retain the feed's precision; do not round to cents or pre-bin by price.
Use INTEGER for source identifiers/counts and native REAL values plus original
source payloads for feed doubles. Do not invent execution-venue identifiers,
trade-cancel links or correction semantics the source did not supply.

Index reports by symbol/exchange time and by source/receipt identity; index BBO
by symbol/receipt order, processing by source and processing ordinal, and bucket
records by symbol/timeframe/bucket. Source references must remain comparable
across archive segments. Commit each source-message/indexed-record group
atomically. A late MD decision may be in a later transaction or segment.

The archive is immutable observation evidence, not a live tape-restoration
source. Future charts must explicitly deduplicate deliveries and select a
volume/condition policy; current bar acceptance is evidence, not that future
policy. Preserve neutral/unknown direction rather than forcing a buy/sell side.
Do not associate a newly fetched book with older cached ticks as their trade-time
bid/ask. Receipt-order comparisons are available; exact NBBO or exchange-time
synchronization is not promised.

## Recorder lifecycle, durability and retention

Add one focused `engine/internal/tickstore` package using the installed SQLite
driver, with its own writer under `~/.eTape/ticks/`. Acquire recorder-directory
ownership using the existing [single-instance lock](../../engine/internal/singleinstance/singleinstance.go):
engines using different execution DB paths must not prune one shared archive
concurrently. Lock/open/recovery failure leaves trading available and reports
recording unavailable. Do not migrate or add tick tables to `etape.db`.

Bound queued work by both bytes and item count. Initial internal limits: 32 MiB
of retained input work and 4,096 queued items, with a 250 ms flush cadence and
bounded transaction groups. Include in-flight indexing/transaction memory in
the bound; reject an oversized observation with an explicit capture gap. These
are engineering defaults to validate under load, not new settings/UI controls.

Capture callbacks only stamp/enqueue fixed records or immutable frame bodies.
No SQL, protobuf indexing, file maintenance, warning publication or waiting for
queue space occurs on socket/feed/MD threads. Related raw observation, routing
and processing lanes have separate loss accounting. A pending-loss latch/control
path must survive a full data queue. Missing trace cannot mean engine rejection.

Use WAL and synchronous FULL for committed batches; the accepted crash window
is uncommitted work, not an intentionally unsynced committed tail. Return and
record transaction errors; do not copy the current bar writer's log-and-clear
failure policy. Keep retries bounded, pause as necessary, and resume automatically
after recovery with an explicit gap. If disk failure prevents writing a marker,
report through logs/system events and conservatively mark the missing interval
once writing resumes. Never claim an exact lost count that was not measured.

Rotate at ET calendar-day boundaries or around a 256 MiB segment target. Age
comes from recording receipt time, not a delayed print's historical trade time.
Before pruning predecessors, retain self-contained active-bucket basis and the
authoritative/extreme values needed by newer records. If that checkpoint or a
predecessor is unavailable, mark attribution incomplete; do not imply deleted
evidence was recovered.

Enforce the 10 GiB physical cap over recorder-owned DB, WAL, SHM, metadata and
temporary files. Reserve conservative growth for each bounded write, checkpoint
and rotation before starting it. A segment target and SQLite journal settings
are not a total-size limit; a long reader can pin WAL growth. Pause when a safe
operation budget is unavailable. [SQLite WAL](https://www.sqlite.org/wal.html#avoiding_excessively_large_wal_files),
[journal-size pragma](https://www.sqlite.org/pragma.html#pragma_journal_size_limit).

Delete oldest expired or size-evicted, retired recorder segments only. Validate
resolved ownership/path boundaries and leave unknown files, active DB/WAL and
the execution DB untouched. Inspect checkpoint results before sealing; locked
files or deletion failures remain counted and may force a pause. Retention uses
whole-file removal, not VACUUM. Recover an unclean SQLite/WAL segment before
considering pruning it.

Add a recorder-specific TOML section with enabled (default true), retention_days
(30), max_bytes (10 GiB) and min_free_bytes (2 GiB). Validate limits and preserve
defaults for old configs. These bootstrap values apply on engine restart; add
no settings panel. Disabling capture also disables recorder-owned BOOK demand.
The free-space reserve governs recorder-controlled writes; other applications
can consume disk space between checks.

Publish failure/recovery transitions through existing low-frequency `sys.events`
and persist their operational summaries. Hub publication and store append are
separate operations, both off ingestion threads. Avoid repetitive per-print
alerts. Stop/join capture producers and MD callbacks before draining/closing the
writer; mark clean closure only after successful final commits. A failed or
timed-out drain reports an incomplete tail instead of silently succeeding.

Closed, checkpointed segments can be copied outside the managed archive before
an investigation. Use SQLite backup for an active database; copying its DB
alone may omit committed WAL contents. Document this procedure without adding
an export feature. [SQLite WAL files](https://www.sqlite.org/wal.html#the_wal_file).

## Best-effort BOOK admission

Keep recorder BOOK ownership separate from ordinary demand in the existing
subscription manager. Candidate symbols already have admitted TICKER; no
recorder TICKER or QUOTE/K-line demand is added. Reuse an existing ordinary BOOK
without another quota slot.

Complete ordinary admission first. Admit recorder BOOK from remaining local and
fresh account-wide capacity above headroom, only when no ordinary admission is
waiting. Keep optional BOOK out of ordinary symbol recency, focus priority and
starvation results. It may not evict ordinary/lingering subscriptions to fit.
Separate ordinary and BOOK-only request batches, entitlement failures and
reconnect replay; BOOK rejection must not quarantine TICKER. Optional background
cache seeding cannot block the ordinary admission loop when its queue is full.

Under pressure, relinquish recorder ownership and unsubscribe only when no
ordinary caller owns that BOOK and the provider permits release. Request an
authoritative quota refresh after release; do not manufacture quota credit.
OpenD's minimum hold is one minute, and already-running requests/refreshes can
also delay new admissions. Foreground priority reduces interference; it is not
a guarantee of immediate capacity for every future demand.
[OpenD subscription restrictions](https://openapi.moomoo.com/moomoo-api-doc/en/quote/sub.html).

BOOK unavailability is a bid/ask coverage gap, not a TICKER gap. Keep recording
reports and provider Aggressor Direction when BOOK is missing. The stored book
is the provider's observed liquidity, not an assertion that a below-bid print
is invalid. No quote-based trade filter or direction inference is introduced.

## File-level implementation steps

1. **Archive:** add `engine/internal/tickstore` schema, bounded writer, physical
   budget/segment lifecycle, ownership and recording health. Reuse SQLite,
   clock, atomic-file and single-instance utilities where their behavior fits.
2. **Source boundary:** update `feed/opend/client.go`, `frame.go`, `pending.go`,
   `decode.go`, `backfill.go` and `opendfeed.go`, plus a focused capture decoder
   if needed. Capture allowlisted ingress and request context; carry original
   source references through cache sorting/gating; expose route/drop evidence.
3. **Engine evidence:** extend neutral input provenance in `feed/feed.go` and
   add optional nonblocking recording hooks in `md/core.go`, `tickagg.go` and
   `bars.go`. Record actual decisions and sparse bucket evidence before UI
   loss/reconciliation. Keep provenance out of price/bar equality and preserve
   existing deduplication, eligibility, indicators and execution marks.
4. **Demand:** update `feed/opend/subman.go` and its activation/reconnect wiring
   for optional BOOK ownership and ordinary-first admission/seeds.
5. **Composition/config:** update `cmd/etape/main.go`, configuration/defaults
   and orderly shutdown. Reuse generic SysEvent transport/toasts. No per-tick
   WebSocket fields or generated TypeScript changes are expected.
6. **Documentation:** update root runtime-file/operations guidance, engine,
   feed/OpenD, MD and config guides; add the archive package README and update
   external-API coverage/quote-clock guidance. Update plan/spec indexes when
   approved/executed as appropriate.

## Acceptance checks and validation

Use temporary archives, injected small limits, deterministic clocks, protocol
fixtures and failure injection; never fill a real disk or submit orders.

- Persist regular, odd-lot, range-only, corrected/late/unknown and sub-cent
  reports with original optional fields and 64-bit sequences intact. Preserve
  duplicates and changed payloads with the same sequence without overwriting.
- Exercise cache sorting/held live overlap, reconnect replay, request descriptor
  matching/timeouts, malformed/error responses and downstream push/inbox drops.
  Raw capture surviving a downstream drop is marked observed-but-not-processed.
- Cover independent 10s/shadow lateness, fully ineligible finalization triggers,
  unanchored then anchored buckets, history/quote basis changes and live/archive
  K_1M clamps. Use existing AEHL/PPCB regressions to attribute original range
  reports and later corrections; distinguish missing trace from rejection.
- Preserve separate BBO side timestamps and absence, empty sides, cached/replayed
  data and recording gaps. Earlier cached ticks receive no fabricated book match.
- Saturate byte/item queues and fail commits; verify bounded memory, responsive
  ingestion, independent loss reporting, atomic groups and recovery gaps.
- Test crash with active WAL, graceful/incomplete shutdown, segment rotation,
  self-contained retained basis, 30-day boundaries and oldest-first deletion.
  Cover held readers, failed checkpoints/deletion/free-space lookup, disk-full
  errors, scaled physical caps including WAL/SHM/temp overhead, and archive lock
  contention/path ownership. Committed evidence survives; active WAL is retained.
- Test optional BOOK under exhausted/stale/reduced quota, shared DOM ownership,
  BOOK-only entitlement failure, reconnect, pressure before/after minimum hold,
  new ordinary demand during an optional RPC and a full seed queue. Ordinary
  profiles are neither enlarged nor quarantined by recorder-only BOOK failures.
- Run representative sustained multi-symbol and burst loads with recording on
  and off. Measure receipt-to-commit lag, queue occupancy, drops, CPU/memory and
  chart/execution-persistence latency. Meet the one-second commit target during
  the declared healthy workload; publish measured sustainable capacity and
  retained-history span. Do not claim all possible market bursts are lossless.
- Verify disabled/`-demo` runs create no capture or recorder BOOK demand, and the
  execution DB/schema, existing UI/wire contract and live-order behavior remain
  compatible.

After implementation, run the complete
[Windows CI-equivalent checklist](../../README.md#ci-equivalent-validation-on-windows)
and engine build; use focused subsystem tests throughout. Add proportional
UI/demo checks for existing warning presentation if that behavior is touched.
Validate links, generated-contract drift and `git diff --check`. Commit only
task files, integrate/push main and verify hosted CI per
[AGENTS.md](../../AGENTS.md#git). Earl approved the completed recording-only plan
after accepting Q1–Q8 and confirming shared understanding in Q9. Implementation
remains a separate request; commit this approved plan and its decision record
under the repository's standing documentation workflow.

## Rollout, rollback and remaining limits

Use the normal build and user-controlled engine relaunch. No live-engine restart
or live-order run is required for validation. Capture begins at the next enabled
real-feed launch; older tick/BBO evidence cannot be recovered by this change.

Disable the recorder section and relaunch to stop capture and its auxiliary
BOOK demand while retaining stored evidence. Revert the task commit for code
rollback; do not automatically delete runtime captures. No execution-database
migration needs reversal.

The size cap can retain fewer than 30 days. Existing subscription gaps,
provider coverage/clock limits and recorder capacity can make investigations
incomplete. Exact UI pixels are outside this evidence contract. Separate SQLite
queues still share disk/CPU, and optional BOOK's minimum hold can briefly affect
future subscription capacity. These limits must be visible in coverage records
and operational documentation.
