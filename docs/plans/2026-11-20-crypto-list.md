# Crypto List and symbol search

Replace Crypto Scanner with an exchange-specific Crypto List. Show every supported
USDT perpetual from the selected exchange's contract catalog and let the trader
search and sort it as soon as the catalog loads.

## Panel behavior

- Put the exchange selector and a symbol search field above the table. Keep
  **App default**, **Follow Account**, and fixed exchanges, including Binance
  Futures Demo. Follow Account uses the list's Link Group market and pauses when
  the group is on Stocks or lacks a configured account for the exchange.
  [Link Group decision](../adr/0005-link-group-owned-execution-venue.md)
- Show **Market**, **Last**, **Change %**, and **24h Turnover (USDT)**. Sort any
  column in either direction. Default to highest turnover first. Show missing
  values as `—` and sort them last in either direction. Keep the table usable
  before tickers arrive and while they are unavailable.
- Search the selected exchange's catalog locally by coin, pair or exact contract
  identifier. Keep the selected sort while searching. Double-click a row or
  activate its symbol with Enter. Enter in the search field opens a sole match.
  Apply the exact exchange contract through the list's chosen Link Group using
  checked symbol focus. An unlinked list prompts the trader to choose a Link
  Group color. Opening a contract uses the existing execution venue safeguards.
- Move the Chart's **Browse USDT perpetuals** control into the list as inline
  search. Keep Chart type-to-load and the browse controls on DOM Ladder,
  Time & Sales, and Order Ticket. For a chart-only layout, add a Crypto List in
  any window and assign the list and chart the same Link Group color.
- Remove Gainers, Losers, and Most Active modes, threshold settings, 5m %,
  Spread, and the scanner-hit chime. Use the column headers for ranking.
- Keep Crypto Monitoring's Following control and source selection. The source
  list's saved sort ranks the full set of contracts with fresh quotes and a usable
  sort value. Search affects only the table. Pause assignments while quotes are
  stale and leave unmatched pinned charts on their current symbols when too few
  rows qualify.

## Market data

The current Gainers and Losers modes hide rows until the engine has a usable
five-minute comparison. The Chart picker can find a contract through the catalog
even if the quote snapshot omits it. Load the catalog separately from ticker
metrics and join them by exact exchange-qualified contract ID.
[Snapshot cache](../../engine/internal/feed/cryptoscanner/cache.go) ·
[Chart picker](../../ui/src/chrome/PerpetualMarketPicker.tsx)

- Use the exchange ticker's `lastPrice` for Last. For MEXC, use `amount24` for
  USDT turnover and display its fractional `riseFallRate` in percentage units.
  The documented example ties this rate to the UTC+8 reference. Label it
  **Change %** and call out the exchange's reference in the tooltip.
  [MEXC ticker](https://www.mexc.com/api-docs/futures/market-endpoints/get-ticker-contract-market-data)
- For Binance, Binance Futures Demo, and BingX, use `quoteVolume` for USDT
  turnover and `priceChangePercent` in its reported percentage units. The
  change column tooltip describes the rolling 24-hour window on those feeds.
  [Binance ticker](https://developers.binance.com/docs/derivatives/usds-margined-futures/market-data/rest-api/24hr-Ticker-Price-Change-Statistics) ·
  [BingX ticker](https://github.com/BingX-API/api-ai-skills/blob/main/skills/swap-market/api-reference.md#8-24h-ticker-price-change-statistics)

## Implementation and rollout

1. In the ticker adapters and shared cache, publish exchange-reported metrics
   for catalogued contracts. Remove the rolling price history and the extra
   Binance book-ticker request used for Spread. Keep the paced, per-exchange
   quote cache and freshness checks. Give reported change a distinct Go wire
   field from the old 5m %, then regenerate the TypeScript contract.
   [MEXC adapter](../../engine/internal/feed/mexc/scanner.go) ·
   [Binance and BingX adapter](../../engine/internal/feed/linearperp/scanner.go) ·
   [Go payloads](../../engine/internal/uihub/wsmsg/payloads.go)
2. Add an exchange argument to `ListPerpetualMarkets` for the list. Keep the
   argument optional for existing callers. Reuse the catalog caches and share
   loads by exchange. Let catalog and ticker results arrive independently.
   A change of exchange must discard late results for the previous selection.
   [Command handler](../../engine/internal/uihub/commands.go) ·
   [Exchange router](../../engine/cmd/etape/perpetual.go) ·
   [Following controller](../../ui/src/chrome/useCryptoFollowing.ts)
3. Build the list from the existing panel, switch the Chart header control, and
   use the same sort for the table and Following. Update the crypto source
   control to say **Use this List**. Keep search text local to each list. Use
   memoized local sorting and search so keystrokes only change visible rows.
   [Panel](../../ui/src/chrome/panels/CryptoScannerPanel.tsx) ·
   [Sort and data](../../ui/src/chrome/cryptoScanner.ts) ·
   [Chart header](../../ui/src/chrome/PanelFrame.tsx)
4. Keep the saved `crypto-scanner` panel type and existing instance IDs. Rename
   the panel in the catalog, presets, settings, venue prompts, and restored or
   imported tab titles. Retain the layout,
   Link Group, exchange, active Following source, and sorts on Market, Last, or
   Turnover. Fall back to turnover descending when a saved sort used 5m % or
   Spread. Let legacy filter settings remain inert in saved documents to support
   rollback. Update the guides and the Crypto List name in ADR 0005.
   [Panel registry](../../ui/src/chrome/panels/registry.tsx) ·
   [Presets](../../ui/src/chrome/presets.ts) ·
   [Layout restore](../../ui/src/chrome/AppShell.tsx)

Ship the engine and UI together and reload open workspaces. Roll back by
reverting both together. The saved panel IDs and layout version make existing
workspaces readable by either build. The compatibility risks are mixing live
and demo contract IDs and applying the wrong percentage units across exchanges.

## Verification

- Fixture-test ticker units, zero change and turnover, missing values, isolated exchange
  loads, and a cold catalog that renders while tickers are unavailable. Verify
  a same-symbol production and demo pair opens the selected exact contract.
- Cover default and saved sorting, offline quote placeholders, local search
  with zero requests per keystroke, checked Link Group focus, and the group
  prompt. Exercise Following with sort changes, search text, quote loss, and
  concurrent stock Monitoring.
- Restore and import an old layout containing a tabbed Crypto Scanner. Check
  its new title, saved exchange and sorting, adjacent panel settings, and
  Following source. In a controlled browser, open Crypto List and a linked
  Chart in separate windows, search for `BTC`, and load the correct contract.
  Check Chart stock type-to-load and the three retained crypto browse controls.
- Run focused engine and UI tests, applicable race, lint, type and build checks,
  `gen-ts-check`, and `git diff --check`. Verify full CI after merging.

[UI guide](../../ui/README.md) · [Engine Scanner guide](../../engine/internal/scan/README.md)
