# Account order price and compact columns

Status: implemented on 2026-10-01.

## Goal

Merge the Account panel's Price and Stop Limit columns. For STOP_LIMIT orders, Price displays the limit price; Stop Price remains the separate trigger. Use compact uppercase column headers such as QTY.

## Current-code evidence

- [AccountPanel.tsx](../../ui/src/chrome/panels/AccountPanel.tsx) shares `ORDER_PRICE_COLUMNS`, `ORDER_PRICE_ACCESSORS`, and `OrderPriceCells` between Open Orders and Closed Orders. PRICE displays the limit price for LIMIT and STOP_LIMIT; STOP displays the trigger. Sorting uses those same accessors.
- Open quantities use remaining shares when available; closed quantities use original shares alongside Filled and Avg Fill. Prices use `formatPrice(value, 3)` and irrelevant price fields display `—`.
- Open and closed sorts persist in `ordersSort` and `closedOrdersSort`. Both readers map `stopLimitPrice` to `price` and validate against current sortable columns.
- [ResizableColumns.tsx](../../ui/src/chrome/panels/ResizableColumns.tsx) reads widths by stable column ID, ignores removed IDs, preserves user resizing, and fits columns to available width subject to minimum widths. Open and closed widths use separate settings keys.
- [Account tests](../../ui/src/chrome/panels/AccountPanel.test.tsx) and [browser checks](../../ui/e2e/account-columns.spec.ts) cover merged prices, compact headers, sorting, saved widths, and timestamps. [AppShell tests](../../ui/src/chrome/AppShell.test.tsx) target the Orders symbol column directly when checking settings persistence.
- [Panels guide](../../ui/src/chrome/panels/README.md) documents the merged columns. [ADR 0005](../adr/0005-link-group-owned-execution-venue.md) and the [held-order plan](2026-09-30-chart-order-markers-and-engine-held-stop-limit.md) preserve Execution Venue ownership and one STOP_LIMIT parent row through the linked LIMIT lifecycle.

## Settled price behavior

Apply the merge to both Open Orders and Closed Orders, using the existing `price` column ID.

| Order type | PRICE | STOP |
| --- | --- | --- |
| LIMIT / LMT | `limitPrice` | `—` |
| STOP_LIMIT / STPLMT | `limitPrice` | `stopPrice` |
| STOP / STP | `—` | `stopPrice` |
| MARKET / MKT | `—` | `—` |

PRICE is the order instruction's limit price. Closed Orders retains a distinct average-fill column for actual executions. Formatting, timestamps, quantity semantics, statuses, and order actions retain their current behavior.

For example, a STOP_LIMIT with stop 2.07 and limit 2.37 displays PRICE `2.370`, STOP `2.070`, and TYPE `STPLMT` in either table.

## Agreed decisions

1. Price merge: use one limit-price column and retain the stop trigger.
2. Compact-header scope: Open Orders and Closed Orders only.
3. Order header vocabulary: exact sequences:
   - Open: `TIME | SYM | SIDE | QTY | PRICE | STOP | TYPE | STATE | [actions]`.
   - Closed: `TIME | SYM | SIDE | QTY | FILLED | PRICE | STOP | TYPE | AVG FILL | STATE | REASON`.
   - Timestamp header tooltips identify Submitted versus Closed; STOP identifies the trigger price. Keep full meanings available on abbreviated headers.
4. Saved sorting: map `stopLimitPrice` to `price`, retaining ascending/descending direction in both tables. Validate other saved order sort keys against surviving sortable columns; invalid keys fall back to the existing newest-order sort for their table.
5. Compact sizing and existing widths: reduce defaults/minimums where shorter headers permit, preserving widths for surviving column IDs, including `price`. Ignore the orphan `stopLimitPrice` width through the existing reader. Keep space for prices, STPLMT, status chips, actions, and existing date-aware timestamp widening; narrow panels retain horizontal scrolling.

## Non-goals

Changing Positions, Fills, Trade History, order-entry fields, stop or limit calculations, order execution, broker adapters, engine custody, WebSocket contracts, exported trade data, or unrelated application headers. No new dependency or table abstraction is needed.

## File-level implementation steps

1. In [AccountPanel.tsx](../../ui/src/chrome/panels/AccountPanel.tsx), remove `stopLimitPrice` from the shared price columns/accessors and open-sort accessors. Extend `price` to LIMIT and STOP_LIMIT so display and numeric sorting agree in both tables, including the single Engine-Held Stop-Limit parent row before and after activation. Apply the agreed labels, header descriptions, and widths; normalize saved sorts in the existing readers.
2. Update [AccountPanel.test.tsx](../../ui/src/chrome/panels/AccountPanel.test.tsx) for the price matrix, exact headers, mixed LIMIT/STOP_LIMIT price sorting, and old sort settings. Assert surviving saved widths still work and the removed column does not render. Update affected [AppShell selectors](../../ui/src/chrome/AppShell.test.tsx) to target the Orders symbol column. Reuse current fixtures.
3. Update [account-columns.spec.ts](../../ui/e2e/account-columns.spec.ts) for merged prices, compact headers, and changed minimum widths. Retain the historical-date, narrow-panel resize, and overflow checks. Inspect screenshots from both order tabs.
4. Update [Panels README](../../ui/src/chrome/panels/README.md) to describe the merged PRICE column, separate STOP trigger, and agreed compact headers. Retain surviving column IDs and existing table-resizing helpers.

## Validation and handoff

- Focused UI: from `ui`, run `npx vitest run --project chrome-regressions src/chrome/panels/AccountPanel.test.tsx src/chrome/panels/ResizableColumns.test.tsx`, `npm run typecheck`, and `npx playwright test e2e/account-columns.spec.ts`.
- After implementation of the approved plan, run the complete [CI-equivalent Windows checklist](../../README.md#ci-equivalent-validation-on-windows), including engine tests/race/vet/lint, generated-contract drift, UI install/lint/tests/build, and `git diff --check`. Check Go LF files and absence of sensitive runtime data; report each skipped required check and its reason.
- After implementation, include this plan in the task commit, integrate with current `origin/main`, merge into local main, push, and verify task inclusion plus hosted CI under [AGENTS.md](../../AGENTS.md#git). Preserve unrelated local changes.

## Rollout, rollback, and risks

Ship as an Account display update. Validate using fixtures and demo orders. Roll back by reverting the UI and documentation commit; underlying orders and contracts need no migration.

Risks: confusing the trigger with the limit price, silently losing an old sort, or shrinking widths until real values clip. The explicit price matrix, sort remap, header meanings, and browser width/date checks address these. Compact defaults will not override widths a user already saved.
