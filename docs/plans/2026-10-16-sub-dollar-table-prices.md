# Sub-dollar prices in tables

Status: implemented on 2026-10-16.

## Goal

Show exactly four fractional digits for positive per-share prices below $1 in every eTape table, including the canvas DOM Ladder and Time & Sales. Preserve the underlying numeric values and use existing formatting helpers.

## Confirmed scope

- Apply throughout eTape, rather than only the Account panel.
- Limit the change to per-share prices. Account balances, P&L, fees (including Locates Fee/sh), totals, and Scanner Dollar Turnover retain their existing formats.
- Account coverage: Positions Average Entry Price; Open and Closed Orders PRICE and STOP; Closed Orders AVG FILL; Fills Price; Trade History Entry, Exit, and signed per-share Diff.
- Other coverage: Scanner Last, Watchlist Last, DOM Ladder price rows (including the Average-Entry Row and LULD Boundary Rows), and Time & Sales print prices.
- At $1 and above, retain each table's existing two- or three-decimal price format.
- Include Trade History Diff when its absolute magnitude is below $1; preserve its sign and Exit-minus-Entry calculation. Total Realized P&L retains its existing format.
- DOM Ladder and Time & Sales choose precision from each displayed price, independent of the latest trade. A mixed-price column can show `0.2500` and `2.500` together, including paused/filtered history and absent latest-trade data.

## Agreed display rules

1. Format positive prices below $1 with exactly four fractional digits, including trailing zeros. Select precision from the raw value before ordinary `toFixed` rounding. Exactly $1 uses the table's current normal precision.
2. Retain the current normal precision for numeric zero (`0.00` or `0.000`, depending on the table), and retain existing `—` placeholders for missing/inapplicable prices or no fill. Preserve existing invalid-value validation and fallbacks.
3. Use absolute magnitude to select precision for signed Trade History Diff and Ladder spread amounts; preserve the signed values and their calculations. Only nonzero magnitudes below $1 get four digits.
4. Apply the per-value rule to the Ladder's existing bid/ask and spread readout. Make LULD accessibility values match their displayed rows. No new readout is added.

| Raw value or state | Normal precision: two | Normal precision: three |
| --- | --- | --- |
| `0.0001` | `0.0001` | `0.0001` |
| `0.5` | `0.5000` | `0.5000` |
| `0.9999` | `0.9999` | `0.9999` |
| `0.99996` | `1.0000` | `1.0000` |
| `0.00001` | `0.0000` | `0.0000` |
| `1` | `1.00` | `1.000` |
| `2.5` | `2.50` | `2.500` |
| Numeric zero | `0.00` | `0.000` |
| Inapplicable/missing price or no fill | Existing `—` | Existing `—` |
| Signed Diff/spread `-0.0025` | `-0.0025` | `-0.0025` |
| Signed Diff/spread `-1.25` | `-1.25` | `-1.250` |

## Starting-code evidence

| Surface | Price columns | Current format |
| --- | --- | --- |
| Account Positions | Avg | Two decimals |
| Account Open Orders | PRICE, STOP | Three decimals |
| Account Closed Orders | PRICE, STOP, AVG FILL | Three decimals; no fill displays `—` |
| Account Fills | Price | Two decimals |
| Account Trade History | Entry, Exit | Two decimals; Diff is separately calculated as Exit minus Entry |
| Scanner and Watchlist | Last | Two decimals; unavailable displays `—` |
| DOM Ladder | Bid/ask and display-only price rows | Four decimals when the latest trade is below $1, otherwise three |
| Time & Sales | Print price | Four decimals when the latest trade is below $1, otherwise three, including paused history |

- [AccountPanel.tsx](../../ui/src/chrome/panels/AccountPanel.tsx) shares `OrderPriceCells` between Open and Closed Orders. Existing order applicability and executed-quantity guards own the `—` placeholders. Its other `formatPrice` callers include P&L, which is outside scope.
- [TradeHistoryTable.tsx](../../ui/src/chrome/panels/TradeHistoryTable.tsx) keeps Entry, Exit, Diff, and Realized separate. Sorting uses raw numbers.
- [ScannerPanel.tsx](../../ui/src/chrome/panels/ScannerPanel.tsx) and [WatchlistPanel.tsx](../../ui/src/chrome/panels/WatchlistPanel.tsx) format Last with `toFixed(2)`.
- [render/format.ts](../../ui/src/render/format.ts) provides explicit-precision `formatPrice` and positive-sub-dollar `quoteDecimals`. Chart and Order Ticket callers share these helpers; changing every `formatPrice` call would also affect unrelated monetary amounts.
- [ladderState.ts](../../ui/src/render/ladder/ladderState.ts) selects one precision from the latest trade; [paintLadder.ts](../../ui/src/render/ladder/paintLadder.ts) applies it to all rows and the bid/ask/spread strip. [tapeState.ts](../../ui/src/render/tape/tapeState.ts) similarly formats every retained print using the latest price. Existing tests explicitly expect uniform precision.
- [LadderPanel.tsx](../../ui/src/chrome/panels/LadderPanel.tsx) also passes the shared precision to LULD accessibility text. [TapePanel.tsx](../../ui/src/chrome/panels/TapePanel.tsx) supplies `latestPrice` to both paint and hover projections; that option is formatting-only. Price-format cleanup must preserve the tape baseline/reseed logic that drives new-trade flashes and symbol switches.
- [vitest.config.ts](../../ui/vitest.config.ts) includes formatter, Account, and Scanner tests in `chrome-regressions`, and ladder/tape state tests in `chart-core`. Existing Watchlist and Trade History test files are absent from every project include list.
- [Trade CSV export](../../engine/internal/exec/export.go) formats raw values in Go, independently of table strings. No export change is needed.

## Implementation

1. Reuse `quoteDecimals` in [render/format.ts](../../ui/src/render/format.ts), adding a caller-supplied normal precision defaulting to the existing `QUOTE_DECIMALS` value of three. Retain its positive/finite guard and preserve existing one-argument callers. Continue to use `formatPrice(value, decimals)` for the final string. Do not alter `formatPrice` globally or add a separate formatting framework.
2. Update price cells in [AccountPanel.tsx](../../ui/src/chrome/panels/AccountPanel.tsx), [TradeHistoryTable.tsx](../../ui/src/chrome/panels/TradeHistoryTable.tsx), [ScannerPanel.tsx](../../ui/src/chrome/panels/ScannerPanel.tsx), and [WatchlistPanel.tsx](../../ui/src/chrome/panels/WatchlistPanel.tsx), passing each cell's current normal precision to `quoteDecimals`. Keep their existing missing-value guards, numeric sorting, column IDs, and saved widths. For Diff, select precision using `quoteDecimals(Math.abs(diff), 2)` and format the signed `diff` value; preserve the calculation.
3. Update [paintLadder.ts](../../ui/src/render/ladder/paintLadder.ts) and [tapeState.ts](../../ui/src/render/tape/tapeState.ts) to choose precision from each displayed price, using three decimals at $1 and above. Cover real depth rows, the Average-Entry Row, LULD Boundary Rows and offscreen-boundary fallback rows. Format bid/ask readouts per value; format the signed spread with `quoteDecimals(Math.abs(spread))` and the original signed number. Keep canvas updates imperative and coalesced. Update LULD accessibility formatting in [ladderState.ts](../../ui/src/render/ladder/ladderState.ts) and its [LadderPanel.tsx caller](../../ui/src/chrome/panels/LadderPanel.tsx) to format each bound separately. Remove the formatting-only `latestPrice` option and both [TapePanel.tsx](../../ui/src/chrome/panels/TapePanel.tsx) arguments so hover and paint agree. Remove obsolete precision fields/call arguments and adjust affected tests together. Preserve tape subscriptions, generation/baseline/reseed behavior, flashes, and symbol-switch handling, including the baseline update currently performed by `seedLast()`.
4. Extend the existing formatter, Account, Trade History, Scanner, Watchlist, ladder/tape, and browser fixtures. Include [WatchlistPanel.test.tsx](../../ui/src/chrome/panels/WatchlistPanel.test.tsx) and [TradeHistoryTable.test.tsx](../../ui/src/chrome/panels/TradeHistoryTable.test.tsx) in `chrome-regressions` so regular `npm test` runs their affected coverage. Inspect normal and minimum-width price columns before changing any widths; adjust only where four digits clip.
5. Update the [Panels guide](../../ui/src/chrome/panels/README.md), [Renderer guide](../../ui/src/render/README.md), and affected [Ladder](../../ui/src/render/ladder/README.md) / [Tape](../../ui/src/render/tape/README.md) guides with the confirmed policy. Mark the plan implemented in the [plans index](README.md) after implementation.

## Acceptance and validation

Use the agreed example table as assertions. Cover positive and negative Diff/spread, zero, near-$1 rounding, missing/inapplicable prices, and unfilled versus filled AVG FILL. Test sub-dollar rows while the latest trade is above $1 or absent, and mixed-price live/paused/filtered tape windows. Verify ordinary, Average-Entry, LULD, fallback, and accessible Ladder prices agree with the per-value rule; Average-Entry matching stays numeric. Regression checks must also prove totals/P&L/fees and numeric ordering retain their existing behavior. Keep chart and other one-argument `quoteDecimals` consumers unchanged.

For implementation, run from `ui`:

- `npx vitest run --project chrome-regressions src/render/format.test.ts src/chrome/panels/AccountPanel.test.tsx src/chrome/panels/TradeHistoryTable.test.tsx src/chrome/panels/ScannerPanel.test.tsx src/chrome/panels/WatchlistPanel.test.tsx`
- `npx vitest run --project chart-core src/render/ladder/ladderState.test.ts src/render/tape/tapeState.test.ts` (the Ladder suite includes recording-context painter assertions), then `npm run test:golden`. Inspect sub-dollar and mixed-price canvas output. The Ladder spread change from `0.020` to `0.0200` intentionally affects existing above-$1 golden scenes in both themes; regenerate and inspect only affected baselines. Add deterministic mixed-price/sub-dollar visual coverage.
- `npx vitest run --project ladder src/chrome/panels/LadderPanel.test.tsx` and `npx vitest run --project tape src/chrome/panels/TapePanel.test.tsx` for affected canvas callers, hover, accessibility, and flash/reseed regressions.
- `npm run lint`, `npm run build`, and `npx playwright test e2e/account-columns.spec.ts` with deterministic sub-dollar fixtures across Account tabs and normal/narrow widths.
- Validate Markdown links and run `git diff --check`.

Before handoff, commit only task changes, fetch/integrate `origin/main`, merge into local `main`, push, and wait for the successful complete [CI workflow](../../.github/workflows/ci.yml) on the pushed commit, as required by [AGENTS.md](../../AGENTS.md#validation). Preserve unrelated working changes and report every skipped required check with its reason.

## Non-goals

Order-entry fields, chart axes/labels, Stock Info summary values, account summary strips, price arithmetic, tick sizes, order execution, broker adapters, WebSocket contracts, CSV exports, clipboard behavior, and new settings or dependencies.

## Rollout, rollback, and risks

Ship as a UI display update without a settings or data migration. Validate through seeded fixtures and the isolated demo server. Roll back by reverting the task commit.

Main risks are applying price precision to P&L through a shared helper, missing a table or canvas row path, changing uniform canvas formatting unintentionally, or clipping longer values at saved widths. Explicit call-site scope, mixed-price and boundary fixtures, test discovery, and browser/canvas inspection address those risks. Formatting can round very small positive prices to `0.0000`; it must never round the stored value or values used by execution and sorting.
