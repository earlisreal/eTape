# Significant Prints in Time & Sales

Status: ready-for-agent

## Problem Statement

The Time & Sales panel currently bolds every print of at least 10,000 shares. That fixed threshold does not adapt to the symbol or trading session: it can miss meaningful activity in a thin stock and overstate routine activity in a highly liquid stock. During a fast tape, the trader primarily watches price and needs unusually large prints to stand out immediately without implying that eTape has identified an actual market participant.

The market-data feed provides Aggressor Direction, which describes whether a buyer or seller actively crossed the spread, but it does not identify the buyer, seller, order, venue, or intent. The panel therefore needs an adaptive and honest definition of a Significant Print, stable visual levels, and enough explanation in settings for the trader to understand why a row was emphasized.

## Solution

Replace the fixed 10,000-share rule with an automatic, symbol-relative Significant Print classifier owned by the Go UI hub. The classifier maintains separate rolling share-size baselines for RTH and Extended activity within each US trading cycle, classifies each print once before allowing that print to influence later thresholds, and assigns one of three stable levels: none, Large, or Exceptional.

Large prints make price and size bold. Exceptional prints use heavier bold text, a stronger background tint based on Moomoo Aggressor Direction, and a thin directional edge marker. No price-movement or inferred-side fallback is introduced: all direction coloring continues to mean Aggressor Direction only, and neutral prints remain neutral.

The Tape Settings dialog exposes the current learned Large and Exceptional thresholds, baseline count, session pool, provisional state, and closed/warming status as read-only information. The existing Minimum Trade Size setting remains a display filter and does not alter baseline learning.

## User Stories

1. As an active trader, I want unusually large prints to stand out relative to the current symbol, so that I can notice meaningful tape activity without relying on a universal share threshold.
2. As an active trader, I want significance to adapt separately for each symbol, so that a normal print in a liquid stock does not have the same meaning as a normal print in a thin stock.
3. As an active trader, I want RTH and Extended activity to use separate baselines, so that different liquidity regimes do not distort one another.
4. As an active trader, I want overnight, premarket, and postmarket activity to share the Extended baseline for one trading cycle, so that extended-hours behavior is evaluated consistently.
5. As an active trader, I want the significance baseline to reset for each US trading cycle, so that yesterday's liquidity regime does not determine today's highlights.
6. As an active trader, I want Large and Exceptional levels, so that I can distinguish prints worth noticing from genuinely rare prints.
7. As an active trader, I want both price and size bolded for a Large print, so that the price remains visible while I watch a fast-moving tape.
8. As an active trader, I want Exceptional prints to use heavier bold text, a stronger background, and an edge marker, so that the rarest prints are recognizable at a glance.
9. As an active trader, I want the timestamp to remain normal-weight, so that visual emphasis stays on price and size.
10. As an active trader, I want row and text coloring to retain its existing Aggressor Direction meaning, so that significance does not overload the color vocabulary.
11. As an active trader, I want neutral prints to remain neutral-colored even when significant, so that eTape does not invent a buy or sell side.
12. As an active trader, I want eTape to call the event a Significant Print rather than a big buyer or seller, so that the interface does not claim participant identity the feed cannot provide.
13. As an active trader, I want significance based on share size only in the first iteration, so that the rule remains easy to understand and does not hide meaningful low-priced-stock activity behind a notional floor.
14. As an active trader, I want the latest 2,000 eligible prints to define the baseline, so that percentiles are stable while still adapting to recent activity.
15. As an active trader, I want a print classified against only preceding activity, so that an extreme print cannot raise the threshold used to judge itself.
16. As an active trader, I want Large classification to activate progressively after 200 eligible prior prints, so that useful highlighting begins before the full baseline is populated.
17. As an active trader, I want Exceptional classification to activate after 1,000 eligible prior prints, so that the rarest level has enough tail observations to be credible.
18. As an active trader, I want settings to show when thresholds are provisional, so that I know the 2,000-print window is still warming.
19. As an active trader, I want the first prints after a reset to use normal styling rather than the old fixed fallback, so that bold always has one unambiguous meaning.
20. As an active trader, I want regular and odd-lot continuous trades to teach the baseline, so that the model reflects the tape actually visible for the symbol.
21. As an active trader, I want intermarket sweeps evaluated for significance without teaching the baseline, so that they can stand out without redefining normal activity.
22. As an active trader, I want auctions and unusual or derived transaction types excluded from scoring and learning, so that exceptional market events do not contaminate ordinary thresholds.
23. As an active trader, I want unknown future transaction types excluded conservatively, so that a feed change cannot silently corrupt the baseline.
24. As an active trader, I want excluded prints to remain visible with ordinary direction styling, so that significance filtering does not remove tape data.
25. As an active trader, I want each print's assigned level to remain stable while I pause or scroll, so that historical rows do not change meaning as new prints arrive.
26. As an active trader, I want cached startup prints processed chronologically, so that recently classified rows reflect the same rules as live rows.
27. As an active trader, I want classifications to survive a UI reconnect while the engine continues running, so that reopening the connection does not relabel visible history.
28. As an active trader, I want a restarted engine to warm from the cached OpenD tape before live prints arrive, so that highlighting becomes useful as quickly as the available data permits.
29. As an active trader, I want changing Minimum Trade Size to affect visibility only, so that hiding small rows does not silently redefine what counts as significant.
30. As an active trader, I want the settings dialog to show the learned Large threshold in shares, so that I can understand the current emphasis rule.
31. As an active trader, I want the settings dialog to show the learned Exceptional threshold in shares, so that I can understand the stronger emphasis rule.
32. As an active trader, I want settings to show the current baseline count out of 2,000, so that I can judge threshold maturity.
33. As an active trader, I want settings to identify whether the displayed thresholds belong to RTH or Extended activity, so that I can interpret them in the correct liquidity regime.
34. As an active trader, I want the latest thresholds retained and labeled closed outside active hours, so that the dialog remains informative without implying that learning is active.
35. As an active trader, I want settings to return to a clear warming state after the trading-cycle reset, so that stale thresholds are not presented as current.
36. As an active trader, I want the fixed 10,000-share behavior removed, so that bold text never has competing definitions.
37. As an active trader, I do not want price colored by comparison with the preceding trade, bid, or bar open, so that the first iteration has one direction signal only.
38. As a maintainer, I want high-frequency classification and tape storage to remain outside React state, so that the feature preserves the panel's rendering performance invariant.
39. As a maintainer, I want classification performed once in the Go engine and carried on the tick contract, so that the browser does not recompute or relabel adaptive history.
40. As a maintainer, I want threshold status published at low frequency, so that explanatory settings do not add per-print React churn or unnecessarily enlarge every tick.
41. As a maintainer, I want generated TypeScript to continue deriving from the Go WebSocket contract, so that the client and engine cannot drift.
42. As a maintainer, I want deterministic percentile and eligibility rules, so that cached, live, and test event sequences produce the same classifications.
43. As a maintainer, I want the classifier to retain only a bounded 2,000-size window per symbol and pool, so that memory use remains predictable.
44. As a maintainer, I want the UI-hub mirror to remain the primary test seam, so that tests verify externally visible annotations and status instead of internal data structures.

## Implementation Decisions

- Use the domain terms **Aggressor Direction** and **Significant Print**. Aggressor Direction is an inferred liquidity-taking side and never participant identity.
- The Go UI-hub mirror owns an in-process Significant Print module. Its external seam remains the mirror's existing application of normalized market-data updates; classifier state and quantile mechanics stay internal.
- Key classifier state by canonical symbol, US trading-cycle key, and session pool. The two pools are RTH and Extended.
- Use the repository's DST-aware US session calendar and exchange timestamp for pool assignment. A trading cycle runs from 20:00 ET to 20:00 ET. Extended combines overnight, premarket, and postmarket; RTH respects the scheduled close, including early-close days.
- Retain OpenD transaction type during feed normalization instead of discarding it. Map feed-specific enum values into stable semantic categories used by the classifier. Unrecognized values map to an unknown category.
- Regular and odd-lot continuous trades are scored and, after scoring, inserted into the learning baseline.
- Intermarket sweeps, including their odd-lot equivalent when supplied, are scored against the current threshold but never inserted into the learning baseline.
- Auctions, delayed or out-of-sequence reports, average-price trades, opening- or closing-price trades, other unusual or derived trades, and unknown types are neither scored nor inserted. They remain visible with ordinary styling.
- Maintain the latest 2,000 eligible learning sizes per symbol and pool. Baseline state is bounded and lives for the engine-process lifetime only.
- Classify a print before inserting it into the baseline. The print can influence only subsequent classifications.
- Use nearest-rank order statistics. For a sorted baseline of count N, the percentile value is the item at the one-based position ceiling(percentile multiplied by N). The median uses the same rule at 50 percent.
- The Large threshold is the greater of the 95th-percentile size and three times the median size.
- The Exceptional threshold is the greater of the 99th-percentile size and eight times the median size.
- A print meeting the Exceptional threshold receives only the Exceptional level, not both levels. Otherwise a print meeting the Large threshold receives Large; all others receive none.
- Large classification requires at least 200 preceding eligible learning prints. Exceptional classification requires at least 1,000 preceding eligible learning prints. A print that reaches either count is inserted first; the newly activated threshold applies to the following print.
- Recalculate cached threshold values whenever a baseline reaches 200 or 1,000 prints and after each subsequent group of 64 eligible learning insertions. A full-window eviction counts as an insertion for this cadence. Prints between recalculations use the last published thresholds.
- Process OpenD cached seed prints in chronological order through the same classifier used for live prints. Do not retroactively classify the pre-warmup rows after a threshold activates.
- Extend the Go-owned tick WebSocket contract with normalized transaction type and a significance level whose values are none, Large, and Exceptional. Regenerate the TypeScript contract; generated output is never edited manually.
- Publish a separate low-frequency, per-symbol significance-status read model for settings. It contains the active pool, baseline count, Large threshold availability/value, Exceptional threshold availability/value, provisional/full state, and active/closed/warming state. Do not repeat this status on every tick.
- Emit significance-status changes only at reset, activation, threshold recalculation, pool transition, and active/closed transition. The browser stores this read model imperatively and exposes React notifications only at this low cadence.
- Stamp each wire tick once with its assigned significance. The UI tape ring stores the annotation as part of the retained print, so pause, scroll, filtering, repaint, and reconnect snapshots never recompute it.
- Keep the existing bounded tape snapshots. Do not enlarge snapshots to 2,000 prints solely to rebuild the classifier in the browser.
- Preserve the complete classifier state across WebSocket reconnects for the lifetime of the engine. A reconnect snapshot carries already annotated retained prints.
- Add no raw-tick persistence in this iteration. After an engine restart, OpenD may seed at most 1,000 cached prints immediately; later live prints complete the 2,000-print baseline.
- Remove the fixed 10,000-share block threshold and its derived UI row flag. There is no fixed-size fallback during warmup.
- Minimum Trade Size remains a browser visibility filter only. The Go classifier receives and learns from eligible prints independently of whether the current panel would display them.
- Retain current Aggressor Direction foreground and row-tint behavior for ordinary prints. Do not add price-movement coloring or infer a side for neutral prints.
- Render Large price and size at font weight 600. Render Exceptional price and size at font weight 700, with a stronger direction-based row tint and a thin edge marker. Keep time at normal weight.
- Use the neutral palette for neutral Significant Prints. Exceptional neutral prints receive stronger neutral-gray background and edge treatments, never buy or sell colors.
- Show the current symbol's status read model in Tape Settings as read-only values. Keep Minimum Trade Size as the only editable significance-adjacent control.
- During warmup, distinguish unavailable thresholds from provisional available thresholds. Once 2,000 eligible prints populate the pool, remove the provisional label.
- Outside active data hours, preserve the latest thresholds and label the pool closed. At the 20:00 ET trading-cycle rollover, reset both pools and show a zero-count warming state until eligible prints arrive.
- Update the Time & Sales renderer documentation, UI guide, engine guide, external-feed field documentation, and any durable specification index whose described interfaces or behavior change.

## Testing Decisions

- A good test asserts observable behavior through a module's interface: annotated tape output, snapshot stability, published threshold status, rendered rows, and settings text. Tests must not assert the classifier's internal ring representation, sorting implementation, or private helper calls.
- Use the Go UI-hub mirror's normalized market-data update interface as the primary and highest test seam. Feed it chronological tape batches and observe staged annotated ticks, bounded snapshots, and significance-status updates. This single seam covers classification, state transitions, stable stamping, and reconnect-visible behavior.
- Through the mirror seam, verify Large and Exceptional threshold formulas with deterministic distributions, including common-size ties and whole-share nearest-rank results.
- Through the mirror seam, verify classification-before-insertion by sending an extreme print and proving that it is judged against the preceding threshold and affects only later prints.
- Through the mirror seam, verify the 200-print Large activation and 1,000-print Exceptional activation. The print that completes a warmup count remains classified under the preceding state; the next print sees the activated threshold.
- Through the mirror seam, verify threshold recalculation at activation points and every 64 eligible learning insertions, including after the 2,000-item window begins evicting old sizes.
- Through the mirror seam, verify that Large and Exceptional levels remain stamped on historical prints after later threshold changes.
- Through the mirror seam, verify independent state for symbols, RTH versus Extended pools, and successive 20:00-to-20:00 trading cycles.
- Reuse the existing session-calendar test style for DST transitions, standard trading days, early closes, closed days, and the 20:00 ET cycle boundary. Assert classifier-visible pool and reset outcomes rather than duplicating calendar implementation details.
- Through the mirror seam, verify that regular and odd-lot trades score and learn; intermarket sweeps score but do not learn; excluded and unknown types neither score nor learn.
- Through the mirror seam, verify that excluded prints remain in tape output with a none significance level rather than disappearing.
- Through the mirror seam, replay cached seed batches followed by overlapping live updates and verify chronological classification, sequence-deduplicated learning, and deterministic results across different batch chunking.
- Through the mirror seam, verify that a same-engine reconnect snapshot preserves each retained print's original annotation and the latest significance status without rebuilding a 2,000-print client baseline.
- Extend existing OpenD decoder tests to verify every supported transaction-type mapping and the conservative unknown mapping. These are focused adapter tests; classifier behavior remains at the mirror seam.
- Extend generated-contract checks to prove the Go tick and significance-status contracts regenerate to the expected TypeScript unions and fields.
- Extend browser tape-ring tests to verify significance annotations survive append, wrap, per-symbol isolation, snapshot replacement, pause, and scroll. Do not duplicate the adaptive algorithm in browser tests.
- Replace fixed-block row-state assertions with assertions that supplied significance levels map to the expected row presentation state. Keep Minimum Trade Size filtering tests and add proof that it changes only visible rows.
- Extend existing canvas golden tests for Large and Exceptional buy, sell, and neutral rows in light and dark themes, including narrow layouts. Goldens should prove price/size weight, stronger Exceptional tint, edge marker, and unchanged timestamp emphasis.
- Extend Tape Settings dialog tests for warming, provisional, full, RTH, Extended, and closed read-only statuses while preserving Minimum Trade Size editing behavior.
- Extend panel tests to prove status updates occur without placing per-print tape data in React state and that symbol changes display the matching symbol's read model.
- Add a focused performance test or benchmark around sustained tape batches to establish that threshold recomputation occurs at the specified cadence and does not turn the 30 Hz market-data path into per-print sorting.
- Remove or rewrite tests whose only contract is the fixed 10,000-share threshold; do not retain competing legacy expectations.

## Out of Scope

- Identifying an actual buyer, seller, institution, account, or order behind a print.
- Treating Aggressor Direction as proof of participant identity or intent.
- Coloring price by comparison with the preceding print, current bid or ask, bar open, or bar close.
- Reclassifying neutral prints with a tick test, quote test, or any other inferred direction algorithm.
- Time-aligning quotes or order-book state with prints.
- Combining nearby prints into a Significant Burst or inferring that split executions belong to one order.
- Dollar-notional floors, turnover-based scoring, volatility adjustment, average-daily-volume adjustment, or cross-symbol normalization.
- User-editable percentile, median-multiple, window-size, warmup, or color controls.
- Audible alerts, desktop notifications, flashing animations, or automated trading actions based on significance.
- Raw-tick persistence, journal restoration of the 2,000-print baseline, or more than the OpenD cached-ticker limit after a full engine restart.
- Increasing reconnect tape-snapshot capacity solely for classifier hydration.
- Hiding, filtering, or specially labeling auctions and unusual transaction types beyond assigning them no significance.
- Changing the existing Minimum Trade Size setting or its persistence semantics.
- Supporting non-US session calendars or defining significance for non-US instruments in this iteration.
- Changing bar construction, directional-volume accounting, market-data demand, order execution, or live-trading behavior.

## Further Notes

- Moomoo defines BUY as an active buyer executing at the then-offer or higher, SELL as an active seller executing at the then-bid or lower, and NEUTRAL as a trade between bid and ask. eTape preserves this as Aggressor Direction.
- The existing panel already applies direction-based row coloring and a fixed 10,000-share bold rule. This feature replaces only the fixed significance rule and strengthens presentation for Exceptional prints.
- The UI-hub classifier is intentionally UI-facing derived state rather than a market-data-core invariant. Raw normalized ticks remain the source event, while significance is a deterministic annotation for this presentation workflow.
- No ADR is required: the thresholds, cadence, and presentation are explicit product decisions but remain inexpensive to tune or reverse after replay and live-session evaluation.
- The repository domain glossary defines Aggressor Direction and Significant Print and must remain aligned with this specification.
