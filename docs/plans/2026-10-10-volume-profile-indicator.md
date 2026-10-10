# Volume Profile Indicator

Status: implemented on 2026-10-10 following explicit implementation authorization.

Decision source: [Volume Profile spec](../../.scratch/volume-profile/spec.md).

## Goal and non-goals

Add a Visible Range Volume Profile Chart Indicator with total share volume,
POC and VAH/VAL. The existing product direction is trade-based volume at price.
Session/fixed-range modes, buy/sell/delta analysis, daily/weekly/monthly support
and demo profiles are outside this release. No new provider, dependency,
recording subscription, persistent aggregate table or capture-schema rewrite.

Implementation, broker operations, new providers, and changes to candle or
execution policy are outside this planning session.

## Current-code evidence

- [Indicator catalog](../../ui/src/render/chart/indicatorSeries.ts) currently
  exposes VWAP, EMA, SMA, MACD and Volume.
- [Chart rendering guide](../../ui/src/render/chart/README.md) documents the
  imperative controller, locally rendered Volume and main-series primitives.
- [Tick archive](../../engine/internal/tickstore/README.md) retains reports and
  coverage evidence for future trade-based views, with SQL investigation as
  its current reader interface. Coverage and duplicate reports require explicit
  handling before charts can consume them.
- [Tick-recording decisions](../../.scratch/tick-storage/spec.md) constrain
  retention, subscription lifetimes and the distinction between recording
  evidence and live-market state.
- [WebSocket queries](../../ui/src/wire/WsClient.ts) already return correlated
  result payloads; [chart hydration](../../ui/src/chrome/panels/ChartPanel.tsx)
  already rejects results from obsolete generations. A profile snapshot does
  not inherently require a broadcast topic or global IndicatorStore changes.
- [Archive implementation](../../engine/internal/tickstore/store.go) has a
  writer-owned segment map and global committed-observation count, but no
  reader lease, manifest revision or per-symbol profile cache. Long readers
  can prevent rotation's WAL checkpoint. [Performance evidence](../performance.md)
  covers recording, not profile-query latency.

## Design decisions

Confirmed: visible range only; total volume, POC and VAH/VAL; captured prints
with explicit Partial coverage; configurable 100 price rows; configurable
left/right placement defaulting to left. Default Value Area is 70%.
All six existing intraday timeframes are supported; all captured sessions
within the selected range count, independent of session shading.
Daily/weekly/monthly charts are excluded because their adjusted price basis
differs from raw ticks. All independently Volume-Eligible Prints count at their
reported price after deduplication, regardless of price-forming eligibility or
subsequent candle clamps; unknown conditions and invalid inputs are excluded.

The indicator is opt-in, with at most one instance per Chart Panel and the
existing hide/settings/remove controls. Rows, placement and style persist with
the panel. Captured Volume, POC, VAH/VAL and coverage are range-wide legend
values, independent of the crosshair.

Identical reports count once per symbol + ET exchange date + positive provider
sequence. Conflicting time/price/size/volume eligibility and unusable sequences
are excluded with explicit Partial reasons. Captured-report selection is
independent of live MD high-water acceptance.

Select whole displayed buckets intersecting the viewport, including clipped
edges. Use a half-open interval ending at the final bucket's end, limited to
now for a live bucket. Empty Future Buffer/padding adds no time.

Rows are configurable integers 1–200, default 100, with equal widths across
included raw prices; a flat range uses one row. Value Area is configurable
1–100%, default 70%. Expand contiguously from POC by the higher-volume adjacent
row until reaching at least the target. Whole-row overshoot is accepted.
POC ties use distance from the profile price midpoint, then lower price;
equal adjacent candidates use distance from POC, then lower price. POC is
displayed at its row midpoint; VAH/VAL at the Value Area's outer edges.
Keep sub-cent precision.

The histogram extends inward from the chosen edge, at most 20% of plot width,
with muted total-volume rows and stronger Value Area highlighting. POC/VAH/VAL
guides span the visible plot with compact labels inside the price pane. Use
existing color/line-style controls; add no per-row text or order-style price-axis
controls. The primitive is passive and contributes nothing to autoscaling.

Coalesce viewport changes and refresh committed data about once per second
while enabled, including late prints affecting Historical View. A new selection
shows Loading; a failure for the same selection retains the previous result
with Stale status. Missing or uncertain coverage is Partial. No usable prints
means no histogram/levels. Oversized/timed-out reads ask the trader to zoom in
instead of calculating from a truncated prefix. Demo shows unavailable and
never consults the real archive. The accepted cadence can lag the live tape.

Preserve high-frequency imperative rendering, Go-owned wire contracts, feed
precision, explicit data gaps, existing subscription arbitration, and isolation
of recording from execution. The existing time/value IndicatorStore cannot
represent price rows and coverage; the profile needs a separate imperative
projection, using the existing indicator management and primitive lifecycle.

## File-level implementation steps

1. **Read retained reports safely.** Add a focused range reader under
   `engine/internal/tickstore/`, using its existing filename/ownership/schema
   validation. Enumerate retained segments through archive-owner coordination;
   do not race private writer maps or borrow the writer's DB connection. Use
   separate read-only connections and short, bounded read chunks, coordinated
   with rotation/checkpoint/pruning so reads cannot pause recording. Close
   SQLite rows, transactions and file handles promptly; caches must not retain
   them. Include later receipt-day segments containing earlier exchange-time
   prints. Freeze a segment/commit boundary for each read, and report missing,
   evicted or unreadable evidence conservatively. With recording disabled,
   retained sealed evidence remains readable; active files require coordinated
   ownership. Do not open, recover or mutate an uncoordinated active archive.

2. **Select and aggregate independently of candle state.** Add a pure profile
   calculation file under `engine/internal/md/`; expose/reuse the existing
   Volume Eligibility condition policy without duplicating its matrix or
   mutating live high-water state. Consume the archived normalized print,
   rather than interpreting the raw provider condition column as the normalized
   enum. Validate finite positive price, valid exchange time and positive
   integer shares. Deduplicate with exact int64 sequences and ET exchange-date
   keys; direction differences alone do not conflict for this total-volume
   feature. Detect alternate reports for candidate identities on relevant
   exchange dates even if a conflicting timestamp falls outside the viewport.
   Exclude conflicts before applying the final time selection. Preserve feed
   precision; conserve included integer volume through binning; guard sum
   overflow and JavaScript-safe result totals. Bin bounds are half-open except
   the last row includes the maximum; empty rows remain for contiguous Value
   Area expansion. A flat range has one row and coincident levels.

3. **Return a targeted typed snapshot.** Add `QueryVolumeProfile` arguments and
   result structs to [Go payload owners](../../engine/internal/uihub/wsmsg/payloads.go),
   register asynchronous handling in [queries](../../engine/internal/uihub/query.go),
   and inject the real archive reader through the existing uihub composition in
   [engine startup](../../engine/cmd/etape/main.go). Demo supplies unavailable.
   Requests contain symbol, timeframe, half-open bounds, rows and Value Area
   percentage. Results echo the selection and include row bounds/volumes,
   Captured Volume, optional POC/VAH/VAL, source/commit freshness, availability,
   coverage evidence and reason codes. Loading/Stale are owning-panel UI states.
   Empty/unavailable results never encode false zero levels. No new broadcast
   topic or ordinary indicator subscription. Regenerate TypeScript with the
   existing generator; never edit generated output by hand.

   Initial internal reader limits: two concurrent reads, a two-second overall
   deadline and 64 MiB retained working data per read; read in bounded chunks
   of at most 1,024 observations with context checks during processing. Return
   explicit busy/too-large/error results rather than queue unbounded work or
   silently truncate. Verify actual cancellation and maintenance coordination
   in fixtures; a QueryContext call alone is insufficient proof. Tune chunking
   within these limits from the load check, without adding user-facing knobs.

4. **Manage one primitive-backed indicator.** Extend the
   [catalog/normalizer](../../ui/src/render/chart/indicatorSeries.ts),
   [picker](../../ui/src/chrome/panels/tv/IndicatorPickerPopover.tsx) and
   [settings](../../ui/src/chrome/panels/tv/IndicatorSettingsDialog.tsx) with
   `VOLUME_PROFILE`, numeric rows/Value Area and an explicit left/right select.
   Default placement is left; reuse persisted styles for histogram/Value Area
   colors and POC/VAH/VAL guides. Normalize imported/saved parameters and
   duplicate profile instances at the panel boundary; reject invalid query
   parameters in Go. Do not rely on HTML min/max. Do not insert a profile into
   existing/new panels automatically or change the default Volume Indicator.
   Exclude this type from ordinary SubscribeIndicator/hydration/IndicatorStore
   paths. Placement and styling changes repaint without a new data query.

5. **Own selection, query state and drawing imperatively.** Extend
   [Chart Controller](../../ui/src/render/chart/ChartController.ts) and
   [Chart Panel](../../ui/src/chrome/panels/ChartPanel.tsx) with one panel-private
   profile projection and a new main-series primitive following
   [visible extrema](../../ui/src/render/chart/visibleExtremaPrimitive.ts).
   Reuse visible logical-range rounding, cached exchange timestamps and
   [intraday bucket durations](../../ui/src/render/chart/drawings/geometry.ts).
   Select whole intersecting displayed buckets, including synthetic display
   slots for time selection only; never use synthetic or real bar volume as
   profile inputs. Clamp empty padding away; use the final bucket's nominal
   end, never the next loaded timestamp. Pure vertical price-scale changes only
   repaint. Clip offscreen price rows/guides without dropping their volume from
   calculations or changing candle autoscaling. Merge coincident guide labels
   while keeping all three numerical legend values.

   Coalesce effective selection changes before sending; allow one request in
   flight per panel and keep only the latest desired selection. Skip offline
   sends so obsolete requests never enter the transport outbox. Use generation
   and disposal guards for symbol/timeframe/parameter changes, reconnects,
   hiding/removal and unmount. Clear prior-selection graphics while Loading;
   preserve a same-selection result only with explicit Stale status on failure.
   Poll about once per second while visible, including historical selections;
   pause when hidden, unavailable in demo or on D/W/M. Resume on re-enable or
   supported timeframe. Reattach on main-series/chart-type replacement.

   Update [legend projection](../../ui/src/chrome/panels/tv/legendView.ts) and
   [legend rendering](../../ui/src/chrome/panels/tv/TVLegend.tsx) with imperative,
   range-wide values/status. Explain known capture gaps/conflicts and as-of
   context in accessible text; first/last print timestamps alone do not prove
   continuous coverage. Do not show a coverage percentage without evidence or
   imply full consolidated-market volume. Keep all market values outside React
   state and omit primitive hit testing/autoscale hooks.

6. **Verify and document the delivered feature.** Add behavioral tests at the
   reader, pure aggregation, async-query, controller, recording-canvas primitive
   and settings/legend seams. Register new files in the explicit
   [Vitest projects](../../ui/vitest.config.ts). Add one sim-only browser query
   fixture to exercise the real LWC attachment and pan/zoom lifecycle; this is
   test coverage, not demo-profile support. Update owning engine/tickstore/md,
   uihub, UI/chart/tv READMEs and [external APIs](../external-apis.md); record
   measured query overhead in [performance evidence](../performance.md).

## Tests and acceptance

Required behavioral coverage:

- Exact included-volume conservation, sub-cent prices, flat ranges, maximum
  price inclusion, zero-volume rows, POC ties, Value Area expansion/overshoot,
  1%/100% targets, neutral direction and overflow/resource rejection.
- Cached/live/reconnect duplicates, int64 sequences beyond JS integer precision,
  sequence reuse on another ET date/DST boundaries, conflicting price/time/size
  or eligibility, missing sequences and captured reports rejected by live MD.
- Volume-only reports count; price-only/unsupported conditions and invalid
  inputs do not. No K_1M clamp, candle delta, bar volume or tape-ring fallback.
- Cross-segment/late receipt history, source/index gaps, absent processing
  traces, recording disabled, retention loss, rotation/pruning, reader deadlines
  and coordinated cancellation. Missing traces are not inferred rejection.
- Reader concurrency/load while recording 50 symbols at the existing 500-print/s
  fixture rate and querying four charts once per second. No reader-caused
  recorder pause/loss; measure query latency, working memory and core/barrier
  overhead. Include a busy symbol/long retained range and a deterministic
  too-large response. Current recording evidence is not a profile benchmark.
- Clipped edge buckets, exact half-open end, all sessions/shading toggle,
  synthetic display slots, Future Buffer-only views and no usable prints.
- New-range Loading, same-range Stale, Partial reasons, disconnect/reconnect,
  late historical prints, obsolete responses, hiding/removal and disposal.
- Singleton opt-in, saved/default parameters and left/right placement, malformed
  imports, ordinary indicator subscription bypass, crosshair-independent legend,
  unsupported D/W/M, demo unavailable, chart-type changes, theme/DPR changes,
  passive gestures and no price-scale or viewport movement.

Manual acceptance: add from picker; switch left/right; pan and zoom; inspect
Partial coverage; vary rows/Value Area; hide/re-enable/remove; reload; test both
themes and candle/bar/line/area; verify D/W/M and demo show unavailable. No live
order, account mutation or broker action is required.

Implementation must run focused subsystem tests and the repository's
[CI-equivalent Windows checklist](../../README.md#ci-equivalent-validation-on-windows)
for an executed approved plan, with hosted CI verified before handoff.
Planning documents require Markdown link/reference validation and whitespace checks.

## Rollout and rollback

Ship engine and UI together with the feature opt-in. Persist only panel settings;
leave archive schema, raw evidence, candle policy and execution unchanged.
Query failures remain local to the profile. Roll back the feature commit to
remove the reader/query/primitive; existing captures remain usable. An older
UI may drop an unknown profile entry when it normalizes saved indicators, but
other panel settings and normal indicators must remain intact.

Runtime captures stay under `~/.eTape/` and outside version control. No running
engine restart is part of planning or automatic deployment. No ADR is needed
for these reversible read/display decisions; the spec records their rationale.

## Risks

- A retained trade-based profile is only as complete as its capture coverage;
  existing candles do not prove that the underlying prints were recorded.
- Reading active archive segments can pin WAL growth and interfere with
  retention unless reader lifetimes are bounded.
- Duplicate/cache/reconnect observations cannot be summed indiscriminately.
- Sub-cent price precision and neutral Aggressor Direction must survive any
  future aggregation.
- The cap can retain far less than 30 days: existing fixture bytes imply about
  2.1 hours at a continuous 500-print/s mix. Do not promise 30-day profile history.
- Raw multi-day ranges can cross a stock split even on intraday charts. Their
  price basis is explicitly raw; do not synthesize adjusted continuity.
- No reliable cancellation/correction ledger exists. Conservative conflict
  exclusions and current eligibility can differ from provider volume totals.

## Completion criteria

Every decision branch is settled, terminology is captured in the glossary,
necessary ADRs are recorded, the implementation reach and checks are concrete,
and Earl confirmed shared understanding in Q15 ("Approve plan only"). Plan
approval does not authorize application implementation. Publish the planning
documents according to repository Git rules. Implementation later must satisfy
the specified tests, README updates, generated-contract checks and main/hosted-CI
handoff; this planning task validates and publishes the approved documents.

## Implementation validation (2026-10-10)

Implemented captured-profile reader/calculator/query and panel-private controller/primitive, settings normalization, singleton picker and range-wide legend. Relevant engine/UI/query/archive guides were updated; no dependency, archive-schema, ordinary subscription or execution-policy change. Local validation: full Go tests, race/short tests, vet, golangci-lint 2.12.2, generated-contract drift check, npm ci, UI lint, all 1,324 UI tests including goldens, production build/typechecks and the sim-only Volume Profile browser scenario. Focused follow-up checks covered bounded maintenance waits and compact calculation memory. [Performance evidence](../performance.md#captured-volume-profile-2026-10-10-windows) records writer/read measurements and the busy-date limit.

The browser fixture verifies production chart rendering, settings, wheel interaction, subscription bypass, daily and demo unavailability; a screenshot was inspected. Exhaustive manual combinations of every chart type/theme and live-provider captures were not run. No live order or broker action was performed. Standards/Spec review and hosted CI remain required before handoff under repository rules.

Review closure: Standards found one POC tie-rounding error; a failing sub-cent regression test reproduced it and exact row-unit midpoint distances fixed it. Spec found one serialized-load validation gap; the fixture now runs four independent workers and records populated/Busy/cancelled results. Both reviewers confirmed zero residual findings. The revised concurrent rotation fixture and focused race/vet/lint checks passed. Hosted CI is the remaining publication gate, verified before final handoff.
Hosted validation adjustment: the artificial 1 MiB rotation throughput fixture exceeded the Windows runner's capacity twice while Linux and UI passed. The default four-worker throughput comparison now holds production 256 MiB segment sizes constant; forced 1 MiB stress remains explicitly runnable and recorded in performance evidence. A separate deterministic public-reader test exercises concurrent rotation/pruning and passes with the race detector. Spec review confirmed this separation preserves the planned coverage; runtime behavior was not relaxed.