# External API Dependencies

## Runtime dependencies

- **moomoo OpenD:** primary US quote, ticker, order-book, K-line, scanner, news, stock-info, quota, and moomoo execution gateway. Engine uses raw TCP framing plus protobuf at `127.0.0.1:11111`; `InitConnect` and keepalive establish session. Trade unlock stays in OpenD GUI. See [OpenD package](../engine/internal/feed/opend/README.md).
- **Alpaca:** paper/live REST and trade-update WebSocket execution, plus
  read-only asset eligibility/borrow metadata used by Stock Info. The first
  configured Alpaca adapter loads the active asset directory once at engine
  startup with `GET /v2/assets?status=active`; Stock Info then uses in-memory
  lookups for the rest of the process. Paper credentials may also provide
  daily and one-minute history; live credentials are not reused for history.
  See [adapter](../engine/internal/broker/alpaca/README.md) and [history
  provider](../engine/internal/hist/alpaca/README.md).
- **TradeZero:** live REST execution plus portfolio WebSocket events. No market-data dependency. See [adapter](../engine/internal/broker/tradezero/README.md).
- **Yahoo Finance:** unauthenticated fallback daily history only. Intraday bars never come from Yahoo. See [provider](../engine/internal/hist/yahoo/README.md).

## Contract facts

- Symbols crossing OpenD use `US.<ticker>` form.
- TICKER ticks drive time-and-sales and exchange-time-bucketed 10-second bars. K-line data drives one-minute and larger intraday bars. Daily history is fetched; weekly/monthly derive from daily.
- OpenD `TickerType` is preserved as an exact raw value plus a stable `Trade-Report Condition` enum at the feed boundary. The single-writer market-data core stamps independent Range-Eligible, Last-Eligible, and Volume-Eligible permissions from the US condition matrix; unknown and unsupported values fail closed while remaining visible. `typeSign` and `PushDataType` are retained as diagnostics/provenance, and delivery source never changes eligibility. The UI-hub Significant Print classifier keeps its existing transaction-category rules; Aggressor Direction remains the feed's liquidity-taking side, not participant identity.
- Cached OpenD ticker seeds are decoded and ordered chronologically before entering the same normalized tick path as live pushes. The startup seed is capped at 1,000 prints.
- Subscription and historical-K-line quotas are separate. Multiple K-line periods for one symbol share one subscription slot; code centralizes demand and quota tracking.
- Broker adapters normalize venue payloads into `exec` domain types. Risk gates and venue arming run before adapter submission.
- Historical requests use completed offline-NYSE-calendar horizons and persisted explored-range coverage. A complete archive therefore makes no Alpaca/Yahoo request on weekends, holidays, or unchanged completed sessions; successful provider-empty intervals are also remembered.
- Focused charts combine the configured archive windows with concurrent OpenD K_1M and K_DAY cache reads in one ordered core seed and one UI snapshot. Scanner/watch warming is archive-only and cannot occupy the focused foreground slot. Alpaca fills uncovered ranges into the archive asynchronously; Yahoo is the optional daily fallback. `intraday_days` applies to focused charts and scanner/watch warming; it and `daily_years` are plain calendar spans, including weekends and holidays, and newly archived bars appear on the next symbol open.

## Research-only alternatives

Tiger, Polygon, Finnhub, Alpha Vantage, FMP, Benzinga-class feeds, and direct EDGAR/press-wire ingestion were evaluated only. No production runtime depends on them. Historical research remains at `41aa9993777cab4ea59e711775094c516032ebf2^:docs/`.
