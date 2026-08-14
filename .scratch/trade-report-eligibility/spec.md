# Trade-Report Eligibility for Tape, 10-Second Bars, and Marks

Status: ready-for-agent

## Problem Statement

The NVDA 10-second chart can show a long wick caused by a single Reported Print that should not form consolidated price statistics. In the observed case, Time & Sales showed `223.123 × 10` at 10:07:03 among prints near `222.93–222.96`. The print raised the 10-second high even though the visible DOM was much lower.

OpenD supplies a primary trade-report condition for every ticker update, and eTape already distinguishes continuous trades from unusual or derived reports. However, all accepted Reported Prints currently enter the same OHLC builder and execution-mark path regardless of their condition. A volume-only odd lot, average-price report, prior-reference report, or unknown future condition can therefore create a false chart wick and move the mark consumed by simulated execution.

The Time & Sales panel also omits the exact condition, OpenD type symbol, and delivery source. After a suspicious print has passed, a trader cannot tell whether it was a regular execution, odd lot, cached report, delayed report, or another condition. eTape does not persist raw prints, so the missing evidence cannot be recovered later from its archived 10-second bars.

## Solution

Preserve OpenD's exact primary trade-report condition, raw type symbol, and delivery source on each Reported Print. Evaluate the documented primary condition once in the market-data core to stamp three independent permissions: range eligibility, last eligibility, and volume eligibility.

Time & Sales continues to show every Reported Print. Regular prints retain the current compact appearance; non-regular prints receive a compact condition badge and a detailed tooltip containing the exact condition, statistical permissions, raw type symbol, and delivery source. Unknown or unsupported conditions remain visible but fail closed for all statistical effects.

Only Range-Eligible Prints may update candle high and low. Only Last-Eligible Prints may establish candle open and close or move the execution mark. Only Volume-Eligible Prints may contribute candle volume, Aggressor Direction volume, and tick count. A bucket containing eligible volume but no Price-Forming Print becomes a flat Volume-Only Bar at the previous trusted last-eligible close, retaining its real eligible volume, delta, and tick count.

Eligibility is session-relative where consolidated rules alone would erase meaningful extended-hours charts. Known Form T, premarket, and extended-hours reports may form prices in their exchange-timestamped extended session, while known odd lots and other price-ineligible conditions remain unable to create wicks. Official OpenD one-minute K-lines remain authoritative for larger intraday timeframes.

## User Stories

1. As an active trader, I want volume-only reports excluded from 10-second OHLC, so that an odd lot cannot create a false wick.
2. As an active trader, I want every Reported Print to remain visible in Time & Sales, so that statistical eligibility does not become hidden tape filtering.
3. As an active trader, I want a compact condition badge on non-regular prints, so that I can recognize unusual reports while watching a fast tape.
4. As an active trader, I want regular prints to retain their current uncluttered appearance, so that the common path remains easy to scan.
5. As an active trader, I want a condition tooltip with the full condition name, so that compact badges do not obscure their meaning.
6. As an active trader, I want the tooltip to state whether a print may update range, last, and volume, so that I can understand exactly how eTape treated it.
7. As an active trader, I want a price-ineligible but volume-eligible print labeled `volume only`, so that I know why it appears on tape without changing the candle price.
8. As an active trader, I want the raw OpenD type symbol visible diagnostically, so that an undocumented composite condition can be investigated rather than guessed.
9. As an active trader, I want the OpenD delivery source visible diagnostically, so that I can distinguish realtime, reconnect-backfill, and cached delivery.
10. As an active trader, I want delivery source to leave statistical eligibility unchanged, so that the same transaction is treated consistently whether live or replayed from OpenD cache.
11. As an active trader, I want odd-lot share volume retained in candle volume when market rules allow it, so that removing a false wick does not erase real volume.
12. As an active trader, I want Aggressor Direction volume to use only Volume-Eligible Prints, so that buy/sell delta follows the same statistical boundary as total volume.
13. As an active trader, I want tick count to count only Volume-Eligible Prints, so that unknown or statistically ineligible reports do not inflate candle activity.
14. As an active trader, I want a bucket containing only eligible odd lots to appear as a Volume-Only Bar, so that genuine activity remains visible without inventing a price move.
15. As an active trader, I want a Volume-Only Bar flat at the prior last-eligible close, so that its shape does not imply an ineligible execution price.
16. As an active trader, I want the chart hover or legend to label a Volume-Only Bar, so that I can distinguish it from an ordinary flat-price candle.
17. As an active trader, I want Volume-Only Bars to retain ordinary candle styling, so that eligibility does not introduce another color vocabulary.
18. As an active trader, I want a No-Trade Bar to mean that no statistically eligible price or volume activity occurred, so that it remains distinct from a Volume-Only Bar.
19. As an active trader, I want a Range-Eligible Print to update high or low even when it cannot update last, so that candles follow the market's condition matrix accurately.
20. As an active trader, I want open and close to follow only Last-Eligible Prints, so that conditional range reports do not become the displayed last price.
21. As an active trader, I want the displayed execution mark to follow only Last-Eligible Prints, so that a volume-only outlier cannot move the application's actionable market reference.
22. As a simulated trader, I want simulated execution marks protected from price-ineligible reports, so that false tape outliers do not distort practice fills or risk checks.
23. As an active trader, I want legitimate thin-liquidity extended-hours prints to remain capable of forming long wicks, so that the premarket and postmarket chart is not artificially flattened.
24. As an active trader, I want known odd lots to remain price-ineligible in extended hours, so that the extended-session exception does not override an explicit volume-only condition.
25. As an active trader, I want Form T, premarket, and extended-hours conditions evaluated using exchange timestamp and the US session calendar, so that cached delivery time cannot move a report into the wrong session.
26. As an active trader, I want early closes and holidays to use the repository's exchange calendar, so that session-relative eligibility remains correct on nonstandard days.
27. As an active trader, I want conditional last-sale rules such as first eligible report of the day honored, so that prior-reference and out-of-sequence reports are not flattened into an inaccurate Boolean.
28. As an active trader, I want applicable market cutoffs honored for conditional last eligibility, so that a late report cannot change last after its permitted window.
29. As an active trader, I want an unknown primary condition shown with an `UNKNOWN` badge, so that a feed change is visible rather than silently treated as regular.
30. As an active trader, I want unknown and unsupported conditions to affect neither price nor volume, so that future feed values fail closed.
31. As an active trader, I want raw type-symbol values retained without guessed semantics, so that undocumented OpenD behavior cannot silently change market statistics.
32. As an active trader, I want the documented primary condition to remain authoritative even for a small print, so that eTape does not infer odd-lot status from size alone.
33. As an active trader, I want a tiny print tagged regular to remain price-forming, so that a hard-coded share threshold does not suppress a legitimate execution.
34. As an active trader, I do not want distance from the current DOM used as a filter, so that asynchronous book and tape delivery cannot discard legitimate reports.
35. As an active trader, I want condition eligibility deterministic rather than configurable, so that identical market events produce identical candles across workspaces.
36. As an active trader, I want cached startup prints and live prints to use the same rules, so that reconnecting or restarting does not change their statistical meaning.
37. As an active trader, I want sequence deduplication to remain effective across cached and live overlap, so that a recovered print cannot contribute twice.
38. As an active trader, I want paused and scrolled Time & Sales rows to retain their original condition metadata and eligibility, so that later state changes do not relabel history.
39. As an active trader, I want recent 10-second bars rebuilt from available raw OpenD cache to use the corrected rules, so that recoverable recent history improves automatically.
40. As an active trader, I want unreconstructable archived wicks left untouched, so that eTape does not fabricate historical corrections without raw evidence.
41. As an active trader, I want old unreconstructable 10-second bars to age out through normal retention, so that this fix does not require destructive database surgery.
42. As an active trader, I want official OpenD one-minute K-lines left unchanged, so that larger intraday timeframes retain their authoritative source.
43. As an active trader, I want the tick-derived one-minute validation shadow to follow the new eligibility rules, so that its comparison with official K-lines remains meaningful.
44. As an active trader, I want condition-based one-minute disagreements to use the existing mismatch diagnostic, so that eTape reports differences without mutating authoritative data.
45. As an active trader, I want the Significant Print classifier to retain its existing scoring and learning behavior, so that wick correction does not redefine unusual-size emphasis.
46. As an active trader, I want excluded and unknown reports to remain visible with ordinary significance when they are not eligible for scoring, so that condition handling and size emphasis remain separate concepts.
47. As an active trader, I want the existing Minimum Trade Size display filter preserved, so that condition badges do not change my chosen tape visibility threshold.
48. As a maintainer, I want the feed adapter to preserve exact OpenD evidence, so that downstream policy does not depend on a lossy coarse category.
49. As a maintainer, I want one market-data state machine to stamp eligibility exactly once, so that bars, marks, and the UI cannot disagree.
50. As a maintainer, I want range, last, and volume represented independently, so that UTP conditions with mixed permissions are expressible without special-case callers.
51. As a maintainer, I want PushDataType treated as provenance rather than condition, so that reconnect mechanics do not leak into statistical policy.
52. As a maintainer, I want undocumented type symbols retained but semantically inert, so that diagnostics improve without reverse-engineering an unsupported protocol.
53. As a maintainer, I want high-frequency tick processing and condition rendering to remain outside React state, so that the existing performance invariant remains intact.
54. As a maintainer, I want the Go WebSocket contract to own all new tick and bar metadata, so that generated TypeScript cannot drift from the engine.
55. As a maintainer, I want eligibility logic tested through observable market-data-core outputs, so that tests survive internal refactoring.
56. As a maintainer, I want raw protobuf mapping tested only at the adapter boundary, so that protocol fidelity is proven without duplicating market policy.
57. As a maintainer, I want browser tests limited to transport and presentation, so that TypeScript never reimplements trade-condition eligibility.
58. As a maintainer, I want deterministic replay and chunking tests, so that cached batching cannot change final bars or marks.
59. As a maintainer, I want documentation to use Reported Print, Trade-Report Condition, Price-Forming Print, Range-Eligible Print, Last-Eligible Print, Volume-Eligible Print, Volume-Only Bar, and No-Trade Bar consistently.
60. As a maintainer, I want no raw-tick journal added by this change, so that a focused correctness fix does not become a persistence project.

## Implementation Decisions

- Use the repository glossary terms **Reported Print**, **Trade-Report Condition**, **Price-Forming Print**, **Range-Eligible Print**, **Last-Eligible Print**, **Volume-Eligible Print**, **Volume-Only Bar**, **No-Trade Bar**, and **Aggressor Direction** throughout code, contracts, tests, and documentation.
- Preserve OpenD's exact primary `TickerType`, raw `typeSign`, and `PushDataType` when decoding both pushed and cached ticker data. Preserve existing sequence, exchange timestamp, receive timestamp, price, volume, turnover, and Aggressor Direction fields.
- Represent the primary condition as a stable feed-domain enum rather than exposing raw protocol numbers to market policy. Preserve an unrecognized raw value diagnostically and map it to the domain's unknown condition.
- Represent delivery source independently as unknown, realtime, disconnect backfill, or cache. Delivery source never changes eligibility.
- Preserve raw `typeSign` as an integer diagnostic field. It has no eligibility effect because OpenD documents its presence but does not publish a semantic mapping.
- Evaluate eligibility in the single-writer market-data core after sequence deduplication. Stamp every accepted Reported Print once with range, last, and volume permissions before publishing tape output or deriving bars and marks.
- Keep the existing normalized transaction category used by the Significant Print classifier, or derive its equivalent from the exact primary condition. Do not change which categories score or teach significance as part of this feature.
- Use the current official UTP consolidated trade-condition matrix as the authority for US statistical eligibility. Keep the mapping centralized, exhaustive over supported OpenD conditions, and table-tested.
- Apply the following approved condition policy:

  | OpenD primary conditions | Range | Last | Volume | Notes |
  | --- | --- | --- | --- | --- |
  | Automatic match, intermarket sweep, auction, bunched trade, Rule 127/155, market-center opening trade, reopening price, closing price | Always | Always | Always | Unambiguously price- and volume-forming conditions |
  | Odd lot, odd-lot intermarket sweep | Never | Never | Always | Volume-only regardless of session |
  | Cash sale, price-variation trade, next-day settlement, seller, contingent trade, average-price trade | Never | Never | Always | Visible volume reports that cannot form consolidated price |
  | Bunched sold, delayed/out-of-sequence, prior-reference price, OTC sold, derivatively priced | Always | Conditional | Always | Conditional last follows UTP first/only-eligible and cutoff rules |
  | Market-center official close, market-center official open | Never | Never | Never | Market-center statistics do not form eTape's consolidated chart |
  | Corrected comprehensive late price | Always | Always | Never | Price correction with no report volume when its documented mapping is available |
  | Premarket/late, Form T, extended-hours | Extended session only | Extended session only | Always | Session is determined from exchange timestamp; these do not form RTH prices |
  | Non-automatic match variants, same-broker variants, overseas, unknown, unrecognized | Never | Never | Never | Fail closed for US until an authoritative US mapping is available |

- Treat the table as a consolidated-statistics policy, not a statement that the underlying transaction did not occur. Every condition remains eligible for Time & Sales visibility.
- Support last-eligibility modes of always, first eligible of the US business day, applicable cutoff, and never. Maintain only the bounded per-symbol/day state required to evaluate them deterministically.
- Use exchange timestamp and the repository's DST-aware US session calendar for session, business-day, early-close, and cutoff decisions. Never use OpenD receive time or UI arrival time for eligibility.
- Trust the documented primary condition. Do not infer or override condition from share size, decimal precision, distance from DOM, direction, delivery source, or raw type symbol.
- Publish all accepted Reported Prints to the tape, including fully ineligible and unknown reports. A statistical permission controls downstream contributions, not visibility.
- Continue sequence deduplication before eligibility and aggregation. Cached seeds must remain chronological and share the same evaluation path as live pushes.
- Update 10-second OHLC as follows: Range-Eligible prices may extend high/low; the first Last-Eligible price establishes open; every later Last-Eligible price updates close; open and close never use a last-ineligible report.
- Keep candle OHLC internally valid when range and last permissions differ. The prior trusted last-eligible close anchors open/close and is included in the range needed to contain those values.
- Update execution marks only from Last-Eligible Prints. The existing market-data-to-execution bridge and simulated brokers continue consuming the resulting mark stream without their own eligibility logic.
- Add to candle total volume, Aggressor Direction volume, and tick count only for Volume-Eligible Prints.
- When a 10-second bucket has eligible volume but no Price-Forming Print, create a Volume-Only Bar flat at the prior trusted last-eligible close while retaining eligible volume, buy volume, sell volume, and tick count.
- When a bucket has Range-Eligible Prints but no Last-Eligible Print, anchor open and close at the prior trusted last-eligible close and allow eligible range prices to extend high and low.
- Seed the last-eligible anchor from prior accepted state or a trusted prior close already supplied by the quote/history path. If no trustworthy anchor exists, hold the bucket rather than using an ineligible price or zero.
- A bucket containing no eligible price and no eligible volume contributes no real bar. The existing chart fill behavior may represent the completed interval as a No-Trade Bar when feed health and session rules allow it.
- Apply the same eligibility policy to the tick-derived one-minute validation shadow. Do not alter official OpenD one-minute K-lines, their archive, or the larger intraday bars derived from them.
- Preserve the existing mismatch diagnostic when the eligibility-correct tick shadow differs from the official one-minute K-line.
- Extend the Go-owned tick wire contract with exact primary condition, raw type symbol, delivery source, and the three stamped eligibility permissions. Regenerate TypeScript; never edit generated output directly.
- Extend the bar contract with the minimum metadata needed to identify a Volume-Only Bar in chart hover or legend. Do not infer the state in TypeScript from volume or candle shape.
- Keep the existing transaction-category and significance fields required by current tape presentation and Significant Print behavior.
- Render regular prints without a new badge. Render every non-regular condition with a compact badge. Price-ineligible but volume-eligible reports must communicate `volume only`; unknown conditions must communicate `unknown`.
- Provide a detailed per-row hover surface containing the full condition name, range/last/volume permissions, raw type symbol, and delivery source. The compact row must remain readable at the panel's current narrow width.
- Keep current Aggressor Direction foreground colors, row tints, significance weights, pause behavior, ordering, and Minimum Trade Size filtering.
- Keep a Volume-Only Bar's normal flat candle appearance and real volume rendering. Add only a concise `volume only` chart hover or legend label; introduce no new candle color or glyph.
- Keep all tick flow, tape storage, badge data, and canvas painting imperative and outside React state. Hover interaction may use low-rate user-event state but must not route streaming ticks through React.
- Add no user setting for eligibility and no raw-print candle mode.
- Apply the corrected rules to new live bars and to bars rebuilt from raw cached prints. Do not clamp, delete, or heuristically rewrite archived bars whose source prints are unavailable.
- Add no raw-tick persistence or correction journal. Existing unreconstructable false wicks leave through normal 10-second retention.
- Update the market-data feed, market-data core, UI-hub contract, Time & Sales renderer, chart renderer, and relevant engine/UI documentation. Preserve the repository invariant that Go owns the WebSocket contract.
- No ADR is required. The condition policy is explicit, centralized, and reversible without changing an architectural boundary.

## Testing Decisions

- A good test asserts externally observable behavior through a module interface: decoded feed evidence, stamped Tape Updates, finalized 10-second Bars, execution Marks, generated wire payloads, retained tape rows, chart labels, and canvas output. Tests must not assert private map layout, helper call order, or duplicated TypeScript eligibility calculations.
- Use the market-data core as the primary and highest deterministic seam. Feed chronological `TicksEvent` batches and observe only `TapeUpdate`, finalized `BarUpdate`, and `Mark` outputs.
- At the primary seam, replay the observed NVDA shape: ordinary prints around `222.93–222.96`, a `223.123 × 10` Reported Print in the same bucket, and a print in the following bucket to finalize it.
- Prove that the suspicious Reported Print remains in Tape Updates with its exact evidence and permissions.
- Prove that an odd-lot `223.123` cannot change high, low, open, close, or execution mark, while its ten shares contribute to eligible candle volume and tick count.
- Prove that the same `223.123 × 10` tagged as a regular automatic match is allowed to change price, demonstrating that size alone is not a filter.
- Prove that a fully ineligible unknown condition remains visible on tape but affects neither price, volume, tick count, delta, significance learning, nor execution mark.
- Prove Volume-Only Bar construction, including prior-close anchoring, volume, buy/sell volume, tick count, finalization, and the no-trusted-anchor hold behavior.
- Prove range-only construction: eligible range prices change high/low while open, close, and mark remain anchored to Last-Eligible state.
- Prove mixed buckets where range-, last-, volume-, and fully ineligible reports arrive in different orders produce the same correct result.
- Prove first-eligible-of-day and cutoff-dependent last behavior across normal days, early closes, holidays, and the US day boundary using the existing session-calendar style.
- Prove known Form T/premarket/extended-hours reports may form extended-session bars but not RTH bars, using exchange timestamp rather than receive or delivery time.
- Prove odd-lot and unknown conditions remain price-ineligible during extended hours.
- Prove cached, disconnect-backfill, and realtime delivery sources produce identical statistical outputs for the same condition.
- Prove sequence-overlap deduplication occurs before eligibility, so cached/live duplicates cannot add volume or move marks twice.
- Extend the existing replay/chunking determinism coverage to prove different batching produces identical finalized bars, Tape annotations, and mark sequences.
- Extend focused OpenD adapter tests to construct raw ticker protobufs and verify exact preservation of every supported primary condition, raw `typeSign`, `PushDataType`, exchange timestamp, receive timestamp, size, direction, and sequence.
- Table-test every supported OpenD primary condition against the centralized eligibility policy, including conservative treatment of unknown numeric values and unsupported US mappings.
- Extend UI-hub mapping and generated-contract checks to prove exact condition, raw type symbol, delivery source, permissions, and Volume-Only Bar metadata cross the Go-to-TypeScript boundary unchanged.
- Extend tape-ring tests to prove the new evidence survives append, per-symbol isolation, wraparound, pause, scroll, cached snapshot replacement, and reconnect snapshot replacement.
- Extend tape-state tests to prove regular rows remain unchanged, non-regular rows receive the intended compact badge data, and Minimum Trade Size remains a visibility-only filter.
- Extend canvas golden tests for volume-only, price-conditional, and unknown badges in light and dark themes, including the narrow layout shown by the reported NVDA screenshot. Preserve existing direction and significance treatments.
- Add focused hover tests proving the full condition name, three permissions, raw type symbol, and delivery source are available for the hovered row without placing streamed ticks in React state.
- Extend chart state and presentation tests to prove Volume-Only Bars retain real volume, ordinary flat styling, and a concise `volume only` hover or legend label while No-Trade Bars remain zero-volume synthetic bars.
- Prove official one-minute input is not altered and that condition-correct tick-shadow disagreements still surface through the existing mismatch diagnostic.
- Prove already seeded archived bars are not heuristically clamped or deleted when raw source prints are unavailable.
- Run generated-contract validation after changing Go wire types, plus focused market-data, OpenD adapter, UI-hub, tape, and chart test suites.
- Because this feature spans the engine and UI, changes generated contracts, and affects execution marks, complete the repository's CI-equivalent Windows validation checklist before handoff. Report every result and any required check that was skipped.

## Out of Scope

- Inferring a Trade-Report Condition from share size, price precision, price distance, DOM state, Aggressor Direction, or neighboring prints.
- A distance-from-DOM sanity filter, statistical outlier clamp, or user-configurable raw-print candle mode.
- Determining the executing exchange or venue; OpenD ticker messages do not expose a per-print exchange field.
- Assigning semantics to OpenD's undocumented raw `typeSign`. This feature preserves it for future evidence only.
- Reconstructing composite conditions that OpenD does not document or expose unambiguously.
- Raw-tick journaling, permanent Reported Print persistence, cancel/correct reconstruction, or historical trade-correction replay.
- Heuristic repair, deletion, or database rewriting of existing archived 10-second bars.
- Changing official OpenD one-minute K-lines, daily bars, or larger intraday aggregation authority.
- Replacing moomoo/OpenD with a direct SIP feed or introducing another market-data provider.
- Redesigning the Significant Print classifier, thresholds, learning eligibility, or visual hierarchy.
- Changing Aggressor Direction inference or comparing prices with the current DOM to assign direction.
- Adding new candle colors, wick suppression animation, audible alerts, or notifications for conditional prints.
- Changing the existing Minimum Trade Size setting or hiding price-ineligible reports from Time & Sales.
- Supporting non-US statistical matrices in this iteration. Market-specific OpenD conditions without authoritative US semantics fail closed.
- Placing, modifying, or canceling any live order.

## Further Notes

- The observed `223.123 × 10` Reported Print is consistent with an odd lot but cannot be proven from size alone. Its exact historical condition is unrecoverable because eTape archives 10-second bars rather than raw ticker events.
- OpenD exposes one primary `TickerType`. It also exposes `typeSign`, described only as a ticker type symbol, with no published value mapping. Preserve it but do not use it for eligibility.
- OpenD `PushDataType` distinguishes realtime delivery, disconnect backfill, and cache. It is provenance rather than a trade condition.
- The visible DOM and Time & Sales are separate asynchronous streams. A trade may legitimately appear away from the book visible in a screenshot, so book distance is not a reliable eligibility rule.
- The UTP trade-condition matrix explicitly separates high/low, last, and volume updates. In particular, an odd-lot trade updates volume but not consolidated high, low, or last. See the [UTP Trade Data Feed specification](https://www.nasdaqtrader.com/content/technicalsupport/specifications/utp/utdfspecification.pdf).
- Moomoo documents the primary ticker conditions and exposes condition and delivery source on both ticker queries and realtime ticker callbacks. See [Moomoo quotation definitions](https://openapi.moomoo.com/futu-api-doc/en/quote/quote.html) and [realtime tick-by-tick callbacks](https://openapi.moomoo.com/moomoo-api-doc/en/quote/update-ticker.html).
- The repository glossary is the source of truth for the terms used by this spec. Keep it aligned if implementation reveals a genuinely new domain distinction.
