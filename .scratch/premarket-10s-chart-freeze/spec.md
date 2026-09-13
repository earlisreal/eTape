# Keep 10-Second Charts Live Through Premarket Opening Bursts

Status: ready-for-agent

## Problem Statement

At the 04:00 ET premarket open, a burst of Reported Prints can arrive in sequence order while their exchange timestamps cross adjacent 10-second buckets out of order. When a Chart Panel has an engine-computed Chart Indicator such as VWAP, the 10-second chart can stop repainting after the first premarket bar even though Time & Sales, the DOM Ladder, the one-minute chart, and market-data persistence continue updating.

The frozen chart remains visibly stale until the trader refreshes eTape. This is especially hazardous at the premarket open, when prices can move quickly and the trader expects Live View to represent current market activity without manual recovery.

The browser currently sorts full Chart Indicator snapshots but assumes every live indicator delta is chronological. A late delta is appended behind a newer timestamp. The Chart Controller can then pass a backward timestamp to Lightweight Charts, which rejects it. Repeated paint failures cause the Scheduler to stop scheduling that Chart Panel, leaving its last successful frame on screen.

## Solution

Keep every browser-side Chart Indicator series strictly chronological and unique by timestamp regardless of the order in which live deltas arrive. A newer value for an existing timestamp replaces that point, a genuinely older missing timestamp is inserted at its chronological position, and the normal newest-point path remains incremental.

When a late insert or correction changes already-applied history, the Chart Controller must replace the affected Lightweight Charts series through its monotonic-safe full-data path. It must use incremental updates only for a current-tail revision or a genuinely newer point. This prevents any backward chart-library update while preserving the low-cost path for ordinary live traffic.

After the change, a 10-second Chart Panel remains in Live View through an out-of-order premarket opening burst, continues accepting later bars and Chart Indicator values, and never requires refresh as recovery.

## User Stories

1. As an active trader, I want my 10-second Chart Panel to keep updating at the 04:00 ET premarket open, so that I can follow the first volatile move without refreshing eTape.
2. As an active trader, I want a late Reported Print from an earlier 10-second bucket to be incorporated safely, so that valid exchange data cannot freeze my chart.
3. As an active trader, I want the current candle and Chart Indicators to continue advancing after a late print, so that the whole Chart Panel remains current.
4. As an active trader, I want VWAP points displayed in chronological order, so that the line represents the correct progression through time.
5. As an active trader, I want EMA, SMA, MACD, and other engine-computed Chart Indicators to receive the same ordering protection, so that the fix is not limited to one indicator type.
6. As an active trader, I want a corrected value for an existing timestamp to replace the old value, so that the chart does not retain duplicate or stale points.
7. As an active trader, I want a missing older point inserted at its correct time, so that a late bucket is not discarded merely to keep the display moving.
8. As an active trader, I want subsequent newer points to resume normal live updates after a correction, so that recovery is automatic and permanent.
9. As an active trader, I want the 10-second chart to remain synchronized with Time & Sales and the DOM Ladder, so that one stale panel cannot silently contradict live panels.
10. As an active trader, I want refresh to remain optional rather than a workaround, so that I do not lose chart context during the opening move.
11. As an active trader, I want Live View and its current zoom preserved while a late indicator point is reconciled, so that data correction does not move my chart unexpectedly.
12. As an active trader, I want Historical View to remain fixed while late data is reconciled, so that examining earlier price action is not interrupted.
13. As an active trader, I want Future Buffer behavior preserved, so that indicator ordering cannot consume or reset the empty space I deliberately created.
14. As an active trader, I want No-Trade Bars and Data Gaps to retain their existing meanings, so that fixing a Chart Indicator cannot alter candle construction.
15. As an active trader, I want the same protection on every timeframe, so that another fast or delayed stream cannot trigger the same chart-library failure.
16. As an active trader, I want multiple Chart Panels sharing browser stores to continue updating independently, so that one panel's indicator correction cannot starve another panel.
17. As an active trader, I want each MACD output to remain isolated and chronological, so that correcting one output cannot change its sibling series.
18. As an active trader, I want symbol and timeframe changes to retain their existing stale-generation protection, so that this fix does not reintroduce old-series data.
19. As an active trader, I want indicator parameter changes to rebuild safely, so that re-specifying an indicator cannot be mistaken for a late live delta.
20. As a maintainer, I want one shared browser data boundary to normalize indicator ordering, so that every chart and legend consumer receives the same safe series.
21. As a maintainer, I want ordinary tail appends and in-progress tail revisions to remain constant-time, so that the 30 Hz market-data path does not begin sorting a full series on every update.
22. As a maintainer, I want late insertion to reuse the established Bar Store approach, so that bars and Chart Indicators follow the same chronological merge rule.
23. As a maintainer, I want the Chart Controller never to call the chart library's incremental update with a timestamp older than the series tail, so that the freeze condition is impossible at the rendering boundary.
24. As a maintainer, I want high-frequency indicator data to remain outside React state, so that the existing imperative rendering invariant is preserved.
25. As a maintainer, I want the regression covered by the default chart test project, so that ordinary UI validation cannot silently omit it.
26. As a maintainer, I want no special-case timer or 04:00 ET refresh logic, so that the repair addresses ordering rather than masking the symptom.

## Implementation Decisions

- Treat out-of-order Reported Prints across adjacent buckets as valid input. The market-data core intentionally supports multiple open tick buckets during a burst; do not discard, reorder, or special-case those prints in the feed or bar aggregator.
- Make the Indicator Store the owner of the browser-side series invariant. Every series it returns must be strictly ascending by timestamp and contain at most one point for each timestamp.
- Apply that invariant consistently to full snapshots, live deltas, and chart-window merges. For repeated timestamps, the latest received value wins.
- Preserve the constant-time common path: append a point newer than the tail and replace a point matching the tail directly. Only a genuinely late insert or non-tail correction may scan or splice the existing series.
- Reuse the Bar Store's established chronological insert/upsert pattern rather than introducing a generic collection abstraction or a new dependency.
- Preserve existing per-instance revision and dirty-state behavior. A delta that changes an existing non-tail point must still invalidate every Chart Panel consuming that indicator instance.
- Use the Indicator Store's existing per-instance revision as the minimal signal for a correction that does not change series length or tail identity. The Chart Controller must remember the revision associated with the applied series.
- Keep the Chart Controller's incremental chart-library update only when the stored series is a true continuation: a same-timestamp tail revision or one or more newer tail points.
- When a point is inserted or replaced before the already-applied tail, use the existing full-data replacement path with the store's sorted series. Never replay a historical point through the chart library's incremental update operation.
- Reset the controller's applied count, tail identity, and remembered indicator revision together on symbol changes, timeframe changes, indicator removal/re-addition, parameter re-specification, and disposal.
- Preserve the current stale-generation guard for asynchronous snapshots. Chronological normalization must not make a snapshot from an old symbol or timeframe look like a valid continuation.
- Apply the invariant independently to each indicator series key, including MACD output suffixes. Do not combine or coordinate sibling output arrays.
- Keep legend reads unchanged; the legend should benefit from the stronger sorted-and-unique Indicator Store contract without gaining correction logic of its own.
- Do not change the Scheduler's retry/removal policy. The Scheduler is containing a persistent painter failure correctly; the data path must stop producing the invalid chart-library operation.
- Do not add a premarket timer, automatic page refresh, reconnect, resubscribe, polling loop, or session-boundary special case.
- Keep the work in the UI data/controller path. No Go WebSocket type, generated TypeScript contract, database schema, market-data calculation, or execution behavior changes are required.
- Keep high-frequency data and correction state in the imperative stores and Chart Controller. Do not route indicator points or repaint recovery through React state.
- Add the Indicator Store tests to the existing default chart-core test project so the store's ordering contract runs under the normal UI test command.
- Update the data-store and chart-renderer documentation to state the sorted-and-unique indicator invariant and the monotonic-safe controller fallback.
- Add no new dependency, abstraction layer, configuration option, ADR, or user-facing setting.

## Testing Decisions

- A good test observes the public series returned by the Indicator Store and the chart-library calls made by the Chart Controller. It must not assert a particular search loop, splice index, private map, or helper-call order.
- Use the existing Chart Controller fake-facade harness with a real Indicator Store as the primary and highest regression seam. This covers live message application, chronological storage, controller branch selection, and the final chart-library operation without adding a new seam.
- Reproduce the observed transition with an existing prior-session point followed by a `04:00:10 ET` delta and then a late `04:00:00 ET` delta. Use fixed timestamps rather than the wall clock.
- Make the fake indicator series reject a backward incremental update, matching Lightweight Charts' monotonic-time requirement. The regression must complete without throwing or unregistering the Chart Panel.
- Assert that the late missing point causes a full-data replacement containing a strictly ascending series, not a backward incremental update.
- Follow the late insert with a second value for the earlier timestamp. Assert that the correction replaces the existing point, leaves only one point at that timestamp, and reaches the chart through a monotonic-safe full-data replacement.
- Follow the correction with a newer `04:00:20 ET` point. Assert that the controller returns to the incremental path and the newest displayed indicator value advances, proving the panel did not merely avoid the first exception.
- Extend the Indicator Store's existing public-interface tests to cover an older missing delta, a non-tail replacement, duplicate timestamps in a snapshot, and an ordinary tail append/revision. Every resulting series must be sorted and unique, with the latest received value retained.
- Preserve existing tests for rapid symbol/timeframe generation changes, same-length tail revisions, coalesced tail revision plus growth, per-instance revisions, visible-window merges, and independent MACD series.
- Do not add a Scheduler test for this bug; existing Scheduler coverage already proves persistent painter failures are removed and is not the faulty seam.
- Do not change or duplicate the engine's tick-aggregator policy tests. They already establish that Reported Prints may arrive out of order within a burst; the UI regression should accept that condition as input.
- Ensure the Indicator Store suite is included in the chart-core project, then run the focused chart-core project, UI type checking, and the full UI unit suite before handoff.
- This is a small UI-only correction. The CI-equivalent engine/generated-contract checklist is not required unless implementation expands into Go, generated contracts, dependencies, or build configuration beyond registering the existing test suite.
- Report every validation command and result at handoff, together with any skipped required check and its reason.

## Out of Scope

- Changing how OpenD orders, timestamps, batches, or delivers Reported Prints.
- Dropping a valid late Reported Print or suppressing its eligible contribution to bars and Chart Indicators.
- Changing VWAP, EMA, SMA, MACD, or other indicator formulas.
- Changing 10-second bar aggregation, one-minute K-lines, daily history, No-Trade Bars, Volume-Only Bars, or Data Gaps.
- Adding a 04:00 ET session-transition hook, scheduled refresh, chart watchdog, reconnect, or automatic reload.
- Weakening chart-library time validation or catching and ignoring its backward-update exception.
- Increasing the Scheduler failure threshold or automatically re-registering a persistently failing painter.
- Moving chart data into React state or forcing a React remount after a correction.
- Changing Live View, Historical View, Future Buffer, zoom, autoscaling, or Reset Chart View behavior.
- Changing WebSocket messages, generated TypeScript, database records, archive queries, or market-data persistence.
- Changing DOM Ladder, Time & Sales, account, execution, or live-order behavior.
- Adding telemetry, alerts, settings, or user-facing controls for late indicator corrections.
- Refactoring unrelated store/controller code or creating a general ordered-series framework.

## Further Notes

- In the supplied before-refresh screenshot, eTape showed approximately `04:00:38 ET`. Time & Sales and the one-minute Chart Panel were current, while the 10-second Chart Panel still showed the first `04:00:00 ET` bar with approximately `O 2.60 / H 3.00 / L 2.60 / C 2.90` and `58.1K` volume. The after-refresh screenshot showed the missing 10-second bars and current price.
- The local bar archive contained continuous LBGJ 10-second bars at `04:00:00`, `04:00:10`, `04:00:20`, and `04:00:30 ET`, and no persisted market-data drop event was present in the observed window. This localizes the failure downstream of bar ingestion and persistence.
- A focused market-data rollover test passed across the prior postmarket session and the next day's 04:00 ET boundary. A replay of the captured LBGJ bars through the Bar Store and Chart Controller also passed, ruling out the session boundary, bar ordering, and chart-window query as the cause.
- A focused engine diagnostic using a realistic out-of-order opening burst emitted VWAP delta timestamps in the order `04:00:10 ET` then `04:00:00 ET`. This behavior follows the tick aggregator's documented support for multiple open buckets and is valid input for the UI.
- A focused Indicator Store diagnostic retained those timestamps in arrival order rather than chronological order. This reached the Chart Controller path already documented to reject backward incremental updates.
- Full indicator snapshots are currently sorted, which explains why refreshing the application reconstructs a working chart. Live deltas currently lack the equivalent insert/upsert protection.
- The existing Indicator Store unit suite is not currently included in the chart-core project's default include list. Registering it is part of this work so the new regression remains active.
- The original browser console was unavailable during diagnosis, so the exact production exception text was not captured. The deterministic engine and browser-store repros reach the same backward-timestamp condition already documented in the Chart Controller and Scheduler.
- No production fix had been applied when this spec was written.
