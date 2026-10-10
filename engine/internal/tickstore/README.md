# Tick archive

Automatic recording for enabled real OpenD runs; `-demo` and disabled recording
do not open this archive or add recorder BOOK ownership. All existing TICKER
subscriptions are captured, including Scanner warm-pool symbols. No additional
TICKER demand is created. Candle policies and execution persistence are unchanged.

Runtime files live under `~/.eTape/ticks/`. A directory lock excludes a second
recorder even when engines use different execution database paths. The archive
uses the already installed modernc SQLite dependency, a separate writer and
separate SQLite files. Never commit runtime captures.

## Evidence and queries

Each segment has `tickstore_meta`, immutable `observations`, and bounded
`active_basis` checkpoints. Query `kind`, `symbol`, `timeframe`, exchange
`time_ms`, source-local `receipt_ms`, recorder submission `observation_ms`, or
the source tuple `(run_id, connection, ingress,
list_index)`. Sequence is SQLite INTEGER: use an int64-capable client rather
than a JavaScript Number for exact provider sequences. Prices preserve the
provider float64 representation and sub-cent values.

`source` stores TICKER and one-minute K-line protobuf bodies once, including
unknown fields. Header/request metadata is JSON; cached GetKL classification
uses its matched request descriptor. Unmatched replies remain unclassified.
`print` and `minute` indexes retain normalized values and original optional
field presence. Duplicate deliveries remain separate rows. BOOK source bodies
are discarded: `bbo` retains only each top side's price, size, order count,
optional fields, and independent provider side clocks. Empty/absent sides stay
absent. These are provider BOOK observations, not certified historical NBBO.
Earlier cached prints have no fabricated contemporaneous book observation.

`routing` distinguishes held cache-overlap events, feed deliveries and canceled
delivery. `processing` records the actual MD dedup result, processing ordinal,
stamped eligibility and independent 10s/shadow lateness. A missing processing
row is unknown processing coverage, never an inferred rejection.
`bucket_basis`/`bucket_final` retain the contributing extreme/first/last reports,
anchor value/time/origin, volume totals, finalization trigger and watermark.
Dirty open bases also checkpoint at the existing one-second MD clock and shutdown,
so rotation can retain the latest last report without logging every progress bar.
`clamp` retains
before/after and the exact authoritative minute. History/archive inputs carry
engine origins; a supplied source reference does not guarantee that its raw
payload remains available. Basis checkpoints copy into new segments; metadata
marks incomplete attribution after deletion or exhausted checkpoint capacity.

SQL files are the investigation interface in this milestone; no inspector,
export command, filter refinement, footprint, delta, or profile renderer is added.
Future trade-based charts can derive those views from these retained reports.

## Failure and lifecycle

Source ingress runs before the raw push queue and cache sorting. It performs
bounded, nonblocking reservations, with no filesystem, SQL or protobuf decoding.
The default work budget is 32 MiB / 4,096 submissions, including writer in-flight
reservations; one quarter of bytes is reserved for basis checkpoints. This is
an accounted work budget, not a promise that process RSS is 32 MiB. Decoding and
indexing occur on the archive writer, with 250 ms flushes and batch wakeups.
Source and indexes commit atomically in WAL transactions with synchronous FULL.

The healthy receipt-to-commit target is one second at the measured workload.
Queue overflow, commit failure and disk pressure lose recording work while
keeping the feed responsive. Independent source/routing/processing/coverage
loss latches survive a full data queue. `capture_gap` counts missing submissions
or events, not necessarily missing prints; time bounds are conservative.
Known references are examples, not continuous missing ingress ranges.
Failure pauses recording; successful later commits carry recovery gaps.
Existing sys.events presents a low-frequency warning and recovery notice.

`connection_up`, transport/request gaps and `processing_stop` describe lifecycle
boundaries. On restart, committed WAL is recovered; unclean predecessors are
marked incomplete and the new run gets `restart_gap`. An uncommitted crash tail
cannot be counted precisely. Producers, socket readers and reconnect workers
join before recorder drain. Clean closure follows final committed batches;
timeout/failure reports an incomplete tail instead.

## Retention and operation

Defaults: 30 ET calendar dates including today, 10 GiB physical archive cap,
2 GiB free-disk reserve, and 256 MiB segment goal. Date or combined DB/WAL/SHM
size rotates a segment. All regular files in the root count conservatively
toward the cap. Growth and checkpoint reserves are checked before writes.
Oldest eligible retired segments are removed first. Retention only removes
validated recorder-owned filenames/metadata inside the resolved root; unknown
files and symlinked segment/sidecar paths are not deletion candidates.
Held readers, failed checkpoints/deletions or insufficient recovery space can
pause or prevent recording startup; committed evidence is retained.

The size cap can shorten retained history dramatically under heavy traffic.
Copy closed investigation segments outside this managed directory to preserve
them. For active files use SQLite's backup API; copying just the live database
can omit committed WAL data. Stop recording by disabling `[tick_recording]`
and relaunching the engine; existing captures remain. No automatic live-engine
restart is part of rollout.

Tests: `go test ./internal/tickstore`; capacity comparison:
`go test ./internal/tickstore -run TestRecordingLoadComparison -count=1 -v`.
See [performance evidence](../../../docs/performance.md).

History and archive anchors embed the full input bar (including source reference)
in bucket basis snapshots, even when no raw OpenD source payload exists. A later
report-based anchor clears that history evidence.

## Volume Profile reads

`ProfileReader` reads captured normalized prints from separate read-only SQLite connections. The writer publishes immutable committed row-ID boundaries after each batch; receipt filenames never filter exchange-time selection. Reads release every handle and the owner read lock after at most 1,024 rows, with a 50 ms chunk deadline. Writer maintenance takes the exclusive lock before writes, checkpointing, rotation or pruning; readers wait without SQLite handles for at most 100 ms, then yield Busy if maintenance still owns it. Two reads, a two-second overall deadline and 96,000 retained reports bound work; large selections fail explicitly without a prefix result. Source references are discarded from the working projection.

With recording disabled, the reader takes the archive owner lock and reads only validated clean sealed files with immutable connections. It never recovers, checkpoints or writes archive evidence. Another active owner yields Busy. Missing, evicted, unreadable and uncoordinated segments remain Partial evidence. Existing subscription history cannot prove continuous capture, so every result carries `coverage_unproven`; gap markers and archive attribution failures add reasons. First/last print times describe observed evidence, not a coverage percentage. All relevant ET dates are checked for identity conflicts, including reports outside the viewport; a very busy exchange date can exceed the bound even for a narrow viewport.
