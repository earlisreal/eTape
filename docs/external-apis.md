# External API Dependencies

## Runtime dependencies

- **moomoo OpenD:** primary US quote, ticker, order-book, K-line, scanner, news, stock-info, quota, and moomoo execution gateway. Engine uses raw TCP framing plus protobuf at `127.0.0.1:11111`; `InitConnect` and keepalive establish session. Trade unlock stays in OpenD GUI. See [OpenD package](../engine/internal/feed/opend/README.md).
- **Alpaca:** paper/live REST and trade-update WebSocket execution, plus
  read-only asset eligibility/borrow metadata used by Stock Info. The first
  configured Alpaca adapter loads the active asset directory once at engine
  startup with `GET /v2/assets?status=active`; Stock Info then uses in-memory
  lookups for the rest of the process. Paper credentials may also provide
  daily and one-minute history; live credentials are not reused for history.
  Alpaca SIP one-minute history also supplies the Scanner's process-local
  15-session REL VOL profiles when configured; IEX and unavailable clients
  leave that column unavailable. See [adapter](../engine/internal/broker/alpaca/README.md)
  and [history provider](../engine/internal/hist/alpaca/README.md).
- **TradeZero:** live REST execution plus portfolio WebSocket events. No market-data dependency. See [adapter](../engine/internal/broker/tradezero/README.md).
- **Yahoo Finance:** unauthenticated fallback daily history plus an
  off-by-default, experimental headline supplement (`[news].yahoo_enabled`),
  plus the optional Stock Info profile metadata path
  (`[stockinfo].yahoo_metadata`, enabled by default). The profile endpoint is
  undocumented and best-effort: Country and Sector are cached daily, and
  Industry is used only when Moomoo's Industry plate is blank. It follows the
  existing `US.<ticker>` symbol convention and is never part of the quote
  path. See [news](../engine/internal/news/README.md), [history provider](../engine/internal/hist/yahoo/README.md), and [Stock Info](../engine/internal/stockinfo/README.md).

## Contract facts

- Symbols crossing OpenD use `US.<ticker>` form.
- OpenD 3203 `SnapshotBasicData.volumeRatio` remains an optional provider field at the feed boundary; the Scanner does not consume or expose it. Scanner REL VOL is calculated from live phase-cumulative Moomoo snapshot volume and a separate Alpaca SIP 1-minute profile; chart/archive history is not an input.
- OpenD 3249 `Qot_GetShortInterest` is the source for Reported Short Interest. It accepts one US security per request; eTape requests `num = 1`, uses the newest returned `timestampStr`/`sharesShort` record, validates a JavaScript-safe share count, and preserves the raw value without split adjustment. The Scanner worker only requests admitted board symbols, caches results in memory for 24 hours, runs one request at a time at least one second apart, and stays within OpenD's 30-requests-per-30-seconds limit.
- TICKER ticks drive time-and-sales and live exchange-time-bucketed 10-second bars. K-line data drives one-minute and larger intraday bars. Once both versions of a minute finalize, a mismatch remains diagnostic and the K_1M range becomes a conservative envelope for its completed 10-second bars: highs/lows outside it are trimmed only when that candle's open and close already lie inside it. Open, close, volume, and unsafe candles remain untouched. The same rule applies when archived 10-second history loads after finalized K_1M history. Daily history is fetched; weekly/monthly derive from daily.
- OpenD `TickerType` is preserved as an exact raw value plus a stable `Trade-Report Condition` enum at the feed boundary. The single-writer market-data core stamps independent Range-Eligible, Last-Eligible, and Volume-Eligible permissions from the US condition matrix; unknown and unsupported values fail closed while remaining visible. `typeSign` and `PushDataType` are retained as diagnostics/provenance, and delivery source never changes eligibility. The UI-hub Significant Print classifier keeps its existing transaction-category rules; Aggressor Direction remains the feed's liquidity-taking side, not participant identity.
- Cached OpenD ticker seeds are decoded and ordered chronologically before entering the same normalized tick path as live pushes. The startup seed is capped at 1,000 prints.
- Stock subscription and historical-K-line quotas are separate and tiered. A live subscription consumes one slot per symbol and subtype, so `K_1M` and `K_DAY` for the same symbol use separate slots. Stock history consumes one slot per symbol across periods in a rolling seven-day window; repeated requests within that window do not add slots. The documented stock tiers are 100, 300, 1,000, and 2,000 slots for each quota; option quotas are separate at 20, 60, 200, and 400. OpenD's account-wide counters are authoritative, while eTape's separately configurable `feed.quota_slots` admission cap defaults to the observed 300-slot entitlement. See moomoo's [quota rules](https://openapi.moomoo.com/moomoo-api-doc/en/intro/authority.html), [subscription status](https://openapi.moomoo.com/moomoo-api-doc/en/quote/query-subscription.html), and [historical quota](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-history-kl-quota.html).
- Broker adapters normalize venue payloads into `exec` domain types. Risk gates and venue arming run before adapter submission.
- Historical chart/backfill requests use completed offline-NYSE-calendar horizons and persisted explored-range coverage. Scanner REL VOL is separate: for sticky-pool symbols it requests one bounded Alpaca SIP range covering exactly the prior 15 trading dates, including each day's 04:00-to-`DataClose` window. Alpaca's one-minute `now - 16m` safety cap remains in force, although Scanner requests never include the current day. Raw Scanner bars are not persisted in `bars_1m` or another table; only the compact in-memory profile survives until the ET-day cache expires.
- Focused charts combine the configured archive windows with concurrent OpenD K_1M and K_DAY cache reads in one ordered core seed and one UI snapshot. Scanner/watch warming is archive-only and cannot occupy the focused foreground slot. Alpaca fills uncovered ranges into the archive asynchronously; Yahoo is the optional daily fallback. `intraday_days` applies to focused charts and generic scanner/watch warming, not REL VOL; it and `daily_years` are plain calendar spans, including weekends and holidays, and newly archived bars appear on the next symbol open.

## Estimated LULD boundary

The DOM's Estimated LULD Band is a local, display-only approximation. It does
not use Alpaca SIP data, does not call an official LULD band endpoint, and must
not be interpreted as a Limit State, Straddle State, Trading Pause, reopening,
or order/risk signal. It consumes OpenD's normalized Last-Eligible prints and
provider-health fields plus Moomoo's previous close; feed differences around
openings, corrections, reopenings, and halts can produce different values from
the SIPs.

Tier and ETP treatment come only from the dated checked-in registry at
`engine/internal/md/luld_registry.json`. The registry records its review date,
valid-through date, provenance, and explicit multipliers. The initial snapshot
is reviewed against the [LULD Plan](https://www.luldplan.com/), Nasdaq's [Tier 1
ETP section](https://www.nasdaqtrader.com/Trader.aspx?id=ETPSection1), and the
dated Nasdaq [ETP update notice](https://www.nasdaqtrader.com/TraderNews.aspx?id=ETA2026-34).
It is intentionally an allowlist: unknown or expired symbols show no band,
and there is no runtime fetch or name-based Tier 2 fallback. Moomoo's
[real-time quote fields](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-stock-quote.html)
and [security status values](https://openapi.moomoo.com/moomoo-api-doc/en/quote/quote.html)
are provider signals only.

## Research-only alternatives

Tiger, Polygon, Finnhub, Alpha Vantage, FMP, Benzinga-class feeds, and direct EDGAR/press-wire ingestion were evaluated only. No production runtime depends on them. Historical research remains at `41aa9993777cab4ea59e711775094c516032ebf2^:docs/`.
