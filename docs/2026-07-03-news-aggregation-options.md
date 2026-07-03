# News aggregation options for eTape

Written 2026-07-03, after live-verifying both moomoo news channels.
Purpose in eTape: **catalyst awareness** — answer "why is this symbol moving?"
(press releases, halts/resumptions, analyst ratings, earnings) for the focused
chart and, ideally, the day's watchlist. News is not latency-critical the way
ticks are; seconds-level freshness is fine, minutes is marginal for PR-driven moves.

## Verified facts (2026-07-03)

- **No news push exists** in the OpenD subscription model — every option below is
  polling. No subscription quota is involved either.
- **`Qot_GetSearchNews`** (OpenD wire protocol, SDK `get_search_news`): keyword
  search; ticker keyword returns relevant items (verified with "AAPL"). Subtypes
  NEWS / NOTICE / RATING / ALL, max 100 items, **10 requests / 30 s**. Returns
  title, source, publish_time, view_count, related_securities, url.
  Rough edges: `related_securities` observed **empty**; `publish_time` is coarse
  (`"7/3"`, no time-of-day) — bad for "what just crossed" ordering.
- **`https://ai-news-search.moomoo.com/news_search`** (unofficial HTTP GET, no
  auth): params keyword / size≤50 / news_type (1 news, 2 notice, 3 research) /
  lang / sort_type. Returns `news_id` (stable dedup key), **epoch-seconds
  publish_time**, title, url, img_url. Not in the official OpenAPI docs — no
  stated rate limits, could change or vanish without notice.
- Neither returns article bodies — title + link to moomoo's site only.
- TradeZero exposes no market data and no news.

## Options

### A. OpenD `Qot_GetSearchNews`, polled from the Go engine

Same TCP/protobuf connection eTape already maintains; the proto ships with the
SDK's 167, so the Go client gets it for ~free (one more request/response pair).

- Focused symbol: poll every 15–30 s — well inside the limit.
- Watchlist sweep: the 10/30 s limit means an 80-symbol watchlist takes
  **~4 min per full rotation**. Fine for ambient awareness, too slow to be the
  thing that tells you a PR just dropped on a non-focused symbol.
- Timestamp coarseness means ordering within a day relies on first-seen time
  (engine stamps arrival) rather than true publish time.

### B. Unofficial moomoo HTTP endpoint

Cleaner data (epoch timestamps, `news_id`), covers notices + research, no OpenD
dependency. But undocumented: no SLA, no known rate limit, silently breakable.
Acceptable as an **enrichment source** (resolve precise timestamps for items
found via A), risky as the primary pipe.

### C. External sources (unverified candidates — evaluate before committing)

For US-stocks day trading the actual market movers are press releases, filings,
and halts, which have authoritative primary feeds:

- **SEC EDGAR** — free, official; 8-K/filing feeds per ticker. Authoritative but
  filings-only, and public dissemination has minutes-level lag vs wire services.
- **PR wires** (GlobeNewswire / PRNewswire / BusinessWire RSS) — free-ish, the
  origin of most catalyst PRs; needs per-symbol filtering built by us.
- **Commercial news APIs** (Benzinga — the source moomoo itself displays;
  Polygon; Finnhub; Alpha Vantage) — proper per-symbol endpoints, real
  timestamps, sometimes bodies + sentiment. Monthly cost; another integration
  and another credential to manage.
- **Nasdaq/NYSE halt feeds** — halts are the one "news" event that is genuinely
  latency-critical for a day trader; worth treating as its own tiny feed
  regardless of which news option wins.

### D. Phased hybrid (recommended)

1. **v1: Option A only.** Go engine polls `Qot_GetSearchNews` for the focused
   symbol (15–30 s) + slow watchlist rotation; dedup by URL/title hash; engine
   stamps first-seen time; broker-agnostic `NewsItem` event (symbol, headline,
   source, url, seen_at) pushed to the UI over the existing WebSocket like any
   other feed. No new connections, no credentials, no cost.
2. **v1.x if timestamp coarseness hurts:** enrich A's items via B (match by URL
   → `news_id`), keeping A as the discovery pipe so B breaking degrades
   gracefully instead of blanking the pane.
3. **v2 if watchlist-wide catalyst detection matters:** add one external
   per-symbol source (Benzinga/Polygon-class) or EDGAR+PR-wire ingestion, and a
   halt feed. Decide only after v1 usage shows the gap is real.

## Decision status

Recommendation D (start with A). Not yet decided — revisit when the UI news
pane gets designed. Whatever the source, the engine-side shape stays the same
broker-agnostic `NewsItem` event, so switching sources later doesn't touch the UI.
