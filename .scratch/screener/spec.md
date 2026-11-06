# End-of-day Screener

Status: retired

The end-of-day Screener was removed after stock filtering proved too slow.
The implementation details below are retained as project history.

## Approved scope

A separate dockable Screener Panel for after-close swing research. High
lookbacks: All-time (default), six calendar months, one calendar year, or
1–2520 custom trading sessions. Conditions: Near, New, or Either. Near compares
the completed close with the selected high. New strictly exceeds the preceding
lookback's high, excluding the signal session; optional confirmation requires
the close above that previous high. A retest is not new. Either combines the
two conditions with OR; all other filters use AND.

Optional filters: price, 20-session average close × volume, estimated market
cap, price above SMA 20/50/200, maximum SMA extension, 21-session price change,
and 14-session mean true range as a percentage of close. Blank bounds disable
them; momentum accepts zero and negative bounds.

Built-in Near ATH / ATH Breakouts presets, up to 20 saved presets per panel,
sorting, Link Group navigation and global Watchlist actions are included.
Filters/presets persist in the Workspace document. Results stay stable until
another run and survive browser reconnects within the same engine run. No
orders, execution changes or alerts are in scope.

## Implementation

The engine evaluates the latest completed NYSE regular session, including
half days and holidays, and labels its date. Calendar subtraction clamps month
ends. OpenD supplies listed US equities, excluding OTC/Pink and funds, with
paginated post-close high discovery and full-universe fallback. Yahoo's existing
daily transport supplies split-adjusted quote OHLC without the dividend-close
multiplier. The Yahoo enable switch applies. History is independent of chart
windows; missing or invalid coverage never becomes a shorter substitute for
ATH. Actual old sessions remain eligible where modern holiday rules differ.

Average dollar volume estimates daily close × volume. Cap estimates completed
close × latest issued shares; the UI states both. RunScreener / CancelScreener
control serial background work with progress, partial cancellation/error states,
bounded result retention, 15-minute caching and stale-run rejection. Incomplete
history stays retryable. Demo reads only synthetic archive data.

## Validation

Tests cover strict records/retests, proximity/lookback bounds, missing coverage,
historical holidays, optional filters, completed-date selection, pagination,
fallback, cancellation/replacement/shutdown, caching, settings, presets, sorting,
chart navigation, Watchlist actions and coverage status. The browser test runs
the real demo service, verifies preset persistence and captures wide/narrow
layouts. TypeScript is regenerated from Go owners and drift checked.
