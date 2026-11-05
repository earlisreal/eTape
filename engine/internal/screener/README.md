# Screener

End-of-day swing screens are independent of the live [Scanner](../scan/README.md).
The trader runs a screen explicitly. No live subscriptions or orders are created.

`RunScreener` accepts a workspace/panel identity and validated filters;
`CancelScreener` cancels provider work. `screener.state` carries full progress/
results snapshots. The service freezes the latest completed regular-session
date, serializes runs, joins work on shutdown and rejects superseded
publications. Up to 32 panel results survive browser reconnects during one
engine run, with oldest completed results evicted as needed. Results do not
persist across engine restarts; filters/presets persist in Workspace config.

OpenD static info supplies listed US equities (NYSE, Nasdaq, AMEX), excluding
OTC/Pink, delisted symbols and non-equities. On the represented session's
post-close calendar date only, paginated Stock Screening V2 supplies a
rolling-high candidate prefilter with a window at most 60 sessions and never
longer than a custom lookback. Exact qualification always uses daily history.
A failed/incomplete prefilter falls back to the full universe with a warning.
A prior-session run skips today's prefilter. Snapshots supply issued shares
for estimated cap: completed close × latest reported issued shares. OpenD
requests yield to foreground work and consume no subscription/history slots.

Yahoo's [daily transport](../hist/yahoo/README.md) fetches quote-price history
from listing. Screener keeps split-adjusted quote OHLC without the
dividend-adjusted-close multiplier. It respects `backfill.yahoo.enabled`, while
remaining independent of chart `daily_years` and cached archive contents.
Unknown listing dates, missing history boundaries/expected sessions, invalid
bars or an absent completed session leave the selected high unavailable. Old
provider sessions remain eligible where modern holiday rules differ. No
52-week or chart-cache substitute is labelled ATH. Results depend on provider
coverage; the unofficial Yahoo endpoint can fail or change. The first screen
can take several minutes under pacing. Progress and Cancel remain available.
Verified histories are cached for 15 minutes; missing histories retry on the
next run. Demo uses synthetic archive data only.

High lookbacks: All-time, six calendar months, one calendar year, or custom
1–2520 trading sessions. Calendar subtraction clamps month ends. Previous High
excludes the signal session. Displayed High includes it; its tooltip shows
Previous High. Near is `(high − close) / high × 100 ≤ threshold`. New requires
session high strictly above Previous High. Close confirmation requires close
strictly above Previous High. Either applies OR to these conditions; additional
filters apply AND. An intraday breakout can qualify after fading when close
confirmation is off, with its status shown explicitly.

Average dollar volume uses 20 consecutive sessions of close × volume, not exact
turnover. 1M change compares with 21 sessions earlier. SMA 20/50/200 and
14-session simple mean true range require consecutive sessions; ATR % divides
the mean by completed close. Missing optional metrics fail active bounds.
Zero disables non-negative bounds; optional momentum bounds can be zero or
negative. This low-frequency read model uses useSyncExternalStore in the UI.

Tests: `go test ./internal/screener`; `go test -race ./internal/screener`.
