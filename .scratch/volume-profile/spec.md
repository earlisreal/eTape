# Volume Profile Indicator

Status: ready-for-agent — plan approved on 2026-10-10; implementation is a separate request.

Approved implementation plan: [Volume Profile](../../docs/plans/2026-10-10-volume-profile-indicator.md).

## Goal

Add a Visible Range Volume Profile Chart Indicator showing total traded share
volume by price, Point of Control (POC), and Value Area High/Low (VAH/VAL).
The decisions below define the first version. Implementation remains a separate request.

## Existing constraints and evidence

- The [tick-recording spec](../tick-storage/spec.md) explicitly chose trade-based
  future volume profiles. Treat this as the existing product direction; an
  OHLCV approximation would require a conscious change of that decision.
- The [tick archive](../../engine/internal/tickstore/README.md) retains individual
  Reported Prints, delivery provenance, processing eligibility and coverage
  evidence. It follows existing TICKER subscriptions and retention limits.
  Duplicate deliveries are retained; unknown processing is not rejection.
- SQL files are currently the archive's investigation interface. Recording did
  not introduce a chart query service or a profile renderer.
- The [chart renderer](../../ui/src/render/chart/README.md) already supports
  imperative indicators and main-series primitives. High-frequency values
  and viewport changes must remain outside React state.
- Go owns WebSocket contracts; generated TypeScript must never be hand-edited.

## Design tree

### Settled — round 1

1. **Range modes:** visible range only. Session and fixed-range modes are out
   of scope for the first version.
2. **Displayed information:** total share volume, POC, VAH and VAL, with a
   default 70% value area. Buy/sell and delta analysis are out of scope.

### Settled — round 2

3. **Missing tick history:** use captured prints only and explicitly label
   partial coverage. POC and Value Area describe captured prints. Show no
   histogram or levels when no usable prints exist; no OHLCV estimation.
4. **Price rows:** configurable row count, default 100.
5. **Placement:** configurable left/right, default **left**. Earl overrode the
   suggested right-only placement. Width/guides/style are settled in round 5.

### Settled — round 3

6. **Supported charts/session filter — settled:** all existing intraday
   timeframes (10s, 1m, 5m, 15m, 30m, 60m), all captured sessions within the
   selected range. Daily/weekly/monthly are excluded. Session shading remains
   visual and does not filter contributions.
7. **Trade selection:** all independently Volume-Eligible Prints at their
   reported price, after deduplication. Price-forming eligibility and later
   candle clamps do not filter contributions. Unknown conditions and invalid
   time/price/positive size are excluded. Preserve chart scaling even when
   retained volume prices extend beyond displayed candles.
8. **Indicator management:** opt-in through the existing picker, one profile
   per Chart Panel, usual hide/settings/remove controls. Persist rows, placement
   and style. Legend values are range-wide Captured Volume, POC, VAH/VAL and
   coverage status, independent of crosshair position.

### Settled — round 4

9. **Duplicate/conflict policy:** count identical reports once per symbol +
   ET exchange date + positive provider sequence. Exclude identities with
   conflicting time, price, size or volume eligibility; exclude missing or
   unusable sequences. Such exclusions make the profile Partial with a reason.
   Evaluate captured reports independently of live MD acceptance/high-water
   dedup, using the existing Volume Eligibility condition policy.
10. **Visible time boundaries:** whole displayed buckets intersecting the
    viewport, including clipped edge bars. Use a half-open UTC interval from
    the first selected bucket start to the final selected bucket end; evaluate
    the live bucket only through now. Empty Future Buffer/padding adds no time.
    Gaps inside the selected span remain explicit, rather than zero volume.
11. **Row/value-area math:** integer row count 1–200, default 100; equal-width
    rows across included raw prices; flat price ranges use one row. Configurable
    Value Area percentage 1–100, default 70. Start at POC and repeatedly add the
    larger adjacent row until volume reaches or exceeds the target; whole-row
    overshoot is accepted. POC ties choose the row center closest to the profile
    price midpoint, then the lower row. Equal adjacent candidates choose the
    row closer to POC, then the lower row. POC guide uses its row midpoint;
    VAH/VAL use the Value Area's outer edges. Preserve sub-cent precision.

### Settled — round 5

12. **Presentation:** histogram extends inward from the selected edge, at most
    20% of plot width. Muted total-volume rows with stronger Value Area
    highlighting; distinct POC/VAH/VAL guides across the plot, small labels
    inside the price pane, existing color/line-style controls. No per-row text
    or order-style price-axis controls. Passive; no autoscale contribution.
13. **Freshness/failure:** coalesce viewport changes and refresh committed data
    about once per second while enabled, including late prints in historical
    ranges. New selections show Loading. Failures for the same selection retain
    the previous result with Stale status. Coverage omissions show Partial;
    no usable data means no histogram or levels. Bounded reads return a zoom-in
    instruction for oversized/timed-out ranges, never a truncated calculation.
    Accepted limitation: the profile may lag the live tape.
14. **Demo:** unavailable. Do not read real captures or build a synthetic
    profile source in demo mode. Earl overrode synthetic-print support.

### Shared understanding confirmed

15. Earl approved the written spec and implementation plan in Q15. Approval
    is for the plan only; application implementation remains a separate request.

## Reference terminology

[TradingView's concepts](https://www.tradingview.com/support/solutions/43000502040-volume-profile-indicators-basic-concepts/)
define POC as the highest-volume price row and value area as a selected share
of profile volume, commonly 70%. Its bar-direction up/down volume must not be
confused with eTape's Aggressor Direction. This reference does not commit eTape
to TradingView's calculation algorithm or data source.

## Comments

- 2026-10-10: Earl requested a grill-with-docs planning session. Round 1 is
  answered: visible range only and total volume + POC + VAH/VAL. Definitions
  captured in the root glossary. Read-only research found no profile renderer
  or archive query facade; no application code has changed. Keep this spec and
  its plan uncommitted while being grilled.
- Round 2: Earl chose captured prints with explicit partial coverage, 100
  configurable price rows, and configurable left/right placement defaulting to
  left. The 20% width and guide styling were not independently approved.
- Round 3, Q6: Earl chose intraday only, all sessions within the selected range.
- Round 3, Q7–Q8: Earl accepted the recommendations: all Volume-Eligible Prints
  after deduplication and one opt-in profile per panel with existing indicator
  controls and a range-wide legend. Captured Volume terminology was recorded
  in the root glossary.
- Round 4: Earl accepted conservative duplicate/conflict handling, whole
  intersecting buckets with no future-padding time, and the proposed row/Value
  Area calculation rules. Volume Profile Row terminology was recorded inline.
- Round 5: Earl accepted the 20% visual treatment and once-per-second bounded
  refresh/error behavior; chose unavailable in demo mode. All product branches
  are settled.
- Q15: Earl answered "Approve plan only". Publish the planning documents under
  repository Git rules; application implementation is not part of this task.
