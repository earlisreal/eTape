# ADR 0005: Link Group-owned execution venues

Status: Accepted

## Context

eTape can run several broker accounts and environments at once. A persisted
global `activeVenue` made an Account panel that visibly selected one venue poll
another account, which made broker Day P&L and account balances misleading. It
also made hotkeys depend on hidden global state instead of the panel the trader
was using.

## Decision

- Each colour Link Group remembers its last crypto exchange and the focused
  symbol and Execution Venue for stocks and each supported perpetual exchange.
  Account can select the market before a symbol is focused. The Account header
  filters configured Execution Venues by Stocks or Crypto, and a crypto account
  selection sets its venue and market together. Opening a symbol also selects
  its market. Account, Order Ticket, chart, DOM Ladder, locate, and hotkey
  actions share that context. Symbol-specific actions require a matching symbol.
- A Crypto Scanner set to Follow Account reads the exchange of its Link Group
  and pauses in Stocks mode or when that exchange lacks a configured account.
  Explicit Scanner exchanges and the app-default
  Scanner setting support independent monitoring. Scanner data does not select
  an Execution Venue.
- A legacy single saved venue resolves only for the market supported by its
  configured broker. A missing or incompatible selection leaves trading
  unavailable for the focused market.
- A pinned panel has no venue. It prompts the user to choose a Link Group and
  cannot submit or request venue-scoped work.
- The legacy global venue value may remain in old configuration for migration,
  but runtime routing ignores it and never falls back to the first configured
  venue.
- Returning to stocks restores the group's saved stock account. Explicitly
  selecting a live account from paper requires confirmation. When a symbol
  change would restore a live perpetual account after using paper, order entry
  becomes available once the account is reselected and confirmed. Existing
  orders and positions belong to their original venue. The UI keeps a warning
  while working orders remain there.
- The engine polls every configured live venue for risk and polls a paper (or
  otherwise non-risk) venue only while an Account panel or a grouped Chart Risk
  Entry panel demands it for account display or sizing. Demands are
  connection/panel scoped and deduplicated by venue.
- Alpaca's broker Day P&L is authoritative. Moomoo's Day P&L is calculated
  from current equity minus the persisted prior-close equity and signed
  deposits/withdrawals/transfers. The UI shows one Day P&L field and labels its
  source.
- The header Realized value is eTape's local cycle ledger, retained after flat
  and reset with the scheduled trading-day cycle. Outside-broker trades affect
  broker Day P&L but not this local Realized value.
- Paper venues are excluded from Max Day Loss. After five failed account polls,
  stale live account data blocks new opening orders globally; reductions and
  cancellations remain allowed. Fresh data does not silently re-arm trading.

## Consequences

Account display, hotkeys, and order entry now agree on the same visible Link
Group context. A user must deliberately group a pinned panel before using it,
and an account or Chart Risk Entry panel's close/unmount releases its polling demand. The
legacy field and compatibility command remain only to read old state safely;
new code must not use them for routing or risk selection.
