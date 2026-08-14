# US Stock News Provider Research

Reviewed 2026-08-14. This began as a research note only. After this review, an
off-by-default experimental Yahoo headline supplement was added at the user's
direction; the terms and supportability findings below remain unchanged.

## Recommendation

Keep moomoo OpenD as the primary feed. Its current eTape poller already has
quota-aware scheduling and a stable normalized item contract. If a second
provider is wanted, rank the choices as follows:

Repository context: [current news flow](../engine/internal/news/README.md) and
[symbol contract](external-apis.md).

1. **Alpaca News — best replacement/failover candidate.** It is an official
   REST and WebSocket API, returns fields that directly cover headline, source,
   author, URL, timestamp, and related symbols, and Alpaca says its historical
   news is supplied by Benzinga. Its free Basic account exists, but entitlement
   to current news must be confirmed with the actual account before relying on
   it: the news endpoint explicitly applies a 15-minute delay when the account
   lacks real-time access and returns `403`/`429` where applicable.
   [Historical news](https://docs.alpaca.markets/us/docs/historical-news-data),
   [stream schema](https://docs.alpaca.markets/us/docs/streaming-real-time-news),
   [REST reference](https://docs.alpaca.markets/us/reference/news-3), and
   [Basic-plan terms](https://docs.alpaca.markets/us/docs/about-market-data-api).
2. **SEC EDGAR — best free, authoritative supplement, not a news-wire
   replacement.** It provides real-time filing/submission data, including 8-K
   and other disclosures, with no API key; filings are often available on the
   website within 1–3 minutes. It is the clean way to surface official company
   events alongside commercial headlines.
   [EDGAR APIs](https://www.sec.gov/search-filings/edgar-application-programming-interfaces)
   and [SEC developer FAQ](https://www.sec.gov/about/webmaster-frequently-asked-questions).
3. **Marketaux — optional low-volume breadth supplement.** It has an official,
   symbol-filtered global-news REST feed and a $0 tier, but its 100 daily
   requests and three articles per response make it unsuitable for a
   high-frequency active-symbol poller. Use it only for sparse refreshes or a
   manual watchlist, and obtain written display/redistribution confirmation
   before showing publisher text beyond metadata and an original link.
   [API documentation](https://www.marketaux.com/documentation) and
   [pricing](https://www.marketaux.com/pricing).

Do **not** use Yahoo Finance scraping as a source. The private/personal scope
does not change this: Yahoo's automated-collection prohibition is framed as
applying “for any purpose” without express permission. Yahoo also has no
Finance market-data/news API in its current published catalog. An undocumented
endpoint would therefore be technically unsupported as well as a Terms risk.
[Yahoo API catalog](https://developer.yahoo.com/api/) and
[Yahoo US Terms of Service](https://legal.yahoo.com/us/en/yahoo/terms/otos/index.html).

Treat **Investing.com** differently but do not rank it as a native provider:
it has no public API and its Terms also prohibit automated extraction “for any
purpose.” Its documented alternatives are its supplied Webmaster Tools and
RSS-reader feed, rather than page scraping or a raw ticker-news API. A native
eTape integration still needs written permission covering retrieval, retention,
and in-app display. [Investing.com API support](https://www.investing-support.com/hc/en-us/articles/115005473825-Do-You-Offer-API-Access-at-Investing-com)
and [Terms](https://cdn.investing.com/about-us/terms_and_conditions.pdf).

Do **not** use **MarketWatch** as a native news source. Its governing Dow Jones
Terms cover `marketwatch.com` and prohibit automated retrieval—including with
an API client—without prior written consent; its own `robots.txt` also says
automated collection is prohibited and disallows all paths for the general
crawler. Private use does not create an eTape-ingestion exception.
[Dow Jones Terms](https://www.dowjones.com/terms-of-use?mod=mw) and
[MarketWatch robots.txt](https://www.marketwatch.com/robots.txt).

## Fit and constraints

| Provider | Coverage and latency | Free limit / transport | Symbols and eTape fit | Attribution and licensing conclusion |
| --- | --- | --- | --- | --- |
| **Alpaca News** | Stock and crypto news; historical data from 2015, average 130+ articles/day, supplied by Benzinga. A dedicated WebSocket carries real-time news. [docs](https://docs.alpaca.markets/us/docs/historical-news-data) | Basic is $0 and documented at 200 Market Data API calls/minute; the current News endpoint itself exposes rate-limit headers and access-dependent real-time delay, so measure entitlement rather than assume full real-time availability. [plan](https://docs.alpaca.markets/us/docs/about-market-data-api) [endpoint](https://docs.alpaca.markets/us/reference/news-3) | REST accepts comma-separated bare symbols such as `AAPL,TSLA`; map eTape `US.AAPL` by removing `US.` and validate share classes against the provider catalog. REST is low-complexity; using the WebSocket is medium-complexity because eTape news is currently polling-only. [reference](https://docs.alpaca.markets/us/reference/news-3) | The payload includes `source`, `author`, and original `url`; preserve and display them. Treat full article text as licensed content: use headline/summary plus original link until Alpaca/Benzinga confirms the intended display and retention rights. [schema](https://docs.alpaca.markets/us/docs/streaming-real-time-news) |
| **SEC EDGAR** | Official US company filings, not general press/news. JSON updates as submissions are disseminated; the SEC says filing documents are often on `sec.gov` in 1–3 minutes. [APIs](https://www.sec.gov/search-filings/edgar-application-programming-interfaces) [FAQ](https://www.sec.gov/about/webmaster-frequently-asked-questions) | No key; maximum 10 requests/second, declared `User-Agent` required for scripted downloads. RSS is available for some searches. [FAQ](https://www.sec.gov/about/webmaster-frequently-asked-questions) | Convert the bare ticker to CIK using SEC's `company_tickers.json`; do not assume the mapping is complete or permanent. Moderate complexity: an event/notice adapter rather than a news adapter. [FAQ](https://www.sec.gov/about/webmaster-frequently-asked-questions) | SEC says government-created content and public EDGAR filing content are free to access and reuse. Attribute as SEC/issuer and retain the filing URL. [FAQ](https://www.sec.gov/about/webmaster-frequently-asked-questions) |
| **Marketaux** | Global financial news from 5,000+ sources, 80+ markets, 30+ languages; `symbols`, `countries=us`, source domain, URL, UTC publication time, and entity metadata are documented. It advertises “instant news access,” but gives no delivery-latency SLA. [docs](https://www.marketaux.com/documentation) | $0: 100 requests/day and three articles/request; REST polling only. [pricing](https://www.marketaux.com/pricing) | Bare ticker lists (`symbols=TSLA,AMZN,MSFT`) fit after removing `US.`; retain only results with an identified US equity. Low code complexity, but the free quota requires a much slower schedule than OpenD's current lane. [docs](https://www.marketaux.com/documentation) | The response supplies source and original URL. Public docs reviewed here do not state a display/redistribution grant, so link out and seek written confirmation before product display of third-party text. [docs](https://www.marketaux.com/documentation) |
| **Finnhub** | Company News is for North American companies, with one year of history and new updates on the free tier. It returns headline, summary, source, original URL, related symbols, and timestamp. Its news WebSocket is premium-only. [API docs](https://finnhub.io/docs/api/company-news) [pricing](https://finnhub.io/pricing) | $0, 60 API calls/minute for the Personal plan; REST polling fits the present OpenD rate technically. [pricing](https://finnhub.io/pricing) | Bare `symbol=AAPL`; low REST complexity. [API docs](https://finnhub.io/docs/api/company-news) | **Do not adopt for eTape without written approval.** Finnhub says plans are personal-use only, cannot be used by a business even internally without approval, and data/derived results may not be redistributed. [terms](https://finnhub.io/terms-of-service) |
| **Alpha Vantage** | `NEWS_SENTIMENT` returns live and historical market news/sentiment for stocks, crypto, FX, and topic filters; ticker examples use bare tickers. No news-stream/SLA is documented, so it is polling only. [docs](https://www.alphavantage.co/documentation/) | 25 requests/day for the general free service, so it cannot support active-symbol refresh. [support](https://www.alphavantage.co/support/) | `US.AAPL` becomes `AAPL`; technically low complexity but the quota is the blocker. [docs](https://www.alphavantage.co/documentation/) | **Do not adopt without a commercial agreement.** The published license is personal, non-commercial by default and expressly treats investment analysis, research, testing, and monitoring beyond personal use as commercial. [terms](https://www.alphavantage.co/terms_of_service/) |
| **MarketWatch (Dow Jones)** | No public MarketWatch API, item schema, ticker-tagged news feed, widget, or news-latency SLA was located in this official-source review. The Terms mention RSS feeds only as Service content for individual/personal/non-commercial use. [Terms](https://www.dowjones.com/terms-of-use?mod=mw) | No public credential or rate limit is documented; the general-crawler rule is `Disallow: /`, and the file says automatic collection needs written permission. [robots.txt](https://www.marketwatch.com/robots.txt) | Consumer quote URLs use bare ticker paths, for example [`aapl`](https://www.marketwatch.com/investing/stock/aapl), but there is no public symbol-mapping or related-symbol contract. Do not infer `US.AAPL` normalization or article association. | **Do not adopt.** Terms permit only occasional personal storage, prohibit redistribution, automated trading, and text/data mining, and bar automated retrieval/processing/storage absent written consent. [Terms](https://www.dowjones.com/terms-of-use?mod=mw) |

## Personal/private-use reassessment

### Yahoo Finance

**Technical status.** Yahoo's current developer catalog lists Fantasy Sports
and Sign in with Yahoo, but no documented Finance market-data or news API.
[Yahoo Developer Network](https://developer.yahoo.com/api/). An automated
integration could only depend on page parsing or an undocumented endpoint; that
may be technically callable, but it is unsupported and can change without
notice. This review did not probe undocumented endpoints.

**Terms status.** Yahoo's US Terms section 5 prohibits using robots, spiders,
scrapers, data-mining, or other automated collection tools **for any purpose**
without express prior permission. It separately prohibits making data feeds,
databases, widgets, aggregate sources, or competing/substitute services from
the content. Thus “personal/private use” is not a scraping exception.
[Yahoo Terms](https://legal.yahoo.com/us/en/yahoo/terms/otos/index.html).
Its API Terms likewise prohibit automated collection outside an actual Yahoo
API and security bypasses; they do not authorize undocumented Finance
endpoints. [Yahoo API Terms](https://legal.yahoo.com/us/en/yahoo/terms/product-atos/apitnc/index.html).

Yahoo's [robots.txt](https://finance.yahoo.com/robots.txt) is not relied on
here: crawl directives do not supply permission that the Terms withhold.

**Recommendation.** Normal interactive use is distinct from an automated
integration. Do not have eTape scrape Yahoo pages, poll undocumented Yahoo
news endpoints, or retain/display extracted news—even for a private setup—
unless Yahoo gives express written permission. Use an official licensed API
instead. This conclusion does not evaluate the repository's existing Yahoo
daily-history fallback.

### Investing.com

**API and scraping.** Investing.com says it offers **no public API** because
of its data-provider contracts; it directs users to free webmaster widgets or
special requests through `tools@investing.com`.
[official API support](https://www.investing-support.com/hc/en-us/articles/115005473825-Do-You-Offer-API-Access-at-Investing-com).
Although its Terms permit ordinary platform use solely for personal use, the
same Terms prohibit automated extraction, including scraping, data mining,
robots, spiders, and similar tools, **for any purpose**. They also restrict
copying, storing, distributing, or making Market Information (including news)
available in an app without prior written consent. Personal/private use is
therefore not a scraping or native-ingestion exception.
[Terms, sections 10 and 14](https://cdn.investing.com/about-us/terms_and_conditions.pdf).
The current [robots.txt](https://www.investing.com/robots.txt) contains
path-specific crawl rules, including some `/news/` paths; it does not override
those Terms or grant permission to automate other news pages.

**Only documented self-service route.** Its Webmaster Tools and RSS reader are
the supported consumer-facing routes; a special request through
`tools@investing.com` is the alternative for other uses. The Terms grant a
revocable, personal/non-commercial license for the provided HTML tool, require
its ads and links to remain intact, and do not guarantee that its data is
real-time, current, or accurate.
[Webmaster Tool terms](https://cdn.investing.com/about-us/terms_and_conditions.pdf)
and [Real Time News Feed](https://www.investing.com/webmaster-tools/real-time-news-feed).
The separate [RSS page](https://www.investing.com/webmaster-tools/rss) says an
RSS reader can automatically retrieve categories such as Stock Market News,
Company News, earnings, analyst ratings, and SEC filings; official feed URLs
include [all news](https://www.investing.com/rss/news.rss) and
[stock-market news](https://www.investing.com/rss/news_25.rss). The published
pages do not document an item-field schema, a latency SLA, a US-equity
coverage guarantee, or ticker/symbol filtering/mapping. The news widget says
it covers global equities among other markets, but that is not a US ticker
feed. [Real Time News Feed](https://www.investing.com/webmaster-tools/real-time-news-feed).

**Recommendation.** Do not add Investing.com as an eTape news adapter. For a
private experiment, use only the supplied widget or an ordinary external RSS
reader in its documented form; do not crawl pages or reverse-engineer
endpoints. Before native RSS parsing, caching, or in-app display, obtain
written permission from `tools@investing.com` that specifically covers those
uses. A written provider agreement/permission is needed for a raw integration.

### MarketWatch

**Public versus licensed routes.** The current Dow Jones Terms explicitly cover
`marketwatch.com`. Section 9.1 limits the Services to individual, personal,
non-commercial use and specifically includes Content supplied through RSS
feeds. In the official sources reviewed, there is no public MarketWatch API,
API key, rate limit, ticker-news schema, or embeddable-widget documentation.
The RSS reference is not an eTape ingestion or display license.
[Dow Jones Terms](https://www.dowjones.com/terms-of-use?mod=mw).

**Automation and private use.** Section 9.4.1 forbids retrieving, refreshing,
scraping, indexing, processing, storing, harvesting, or ingesting Content with
automated means, a crawler, script, bot, browser automation, **API client**, or
AI agent without prior written consent. Section 9.4.3 also bars autonomous or
semi-autonomous software without that consent. The Terms say exclusionary
protocols such as `robots.txt` do not expand any rights. MarketWatch's own
robots file is even clearer: its notice prohibits automated collection absent
express written permission and its general rule is `Disallow: /`.
[Dow Jones Terms](https://www.dowjones.com/terms-of-use?mod=mw) and
[MarketWatch robots.txt](https://www.marketwatch.com/robots.txt).

**Retention, display, and symbols.** Section 9.3 allows only occasional
download/print/storage for individual personal, non-commercial use while
preserving notices; it otherwise prohibits selling, publishing, distributing,
retransmitting, or providing access, and prohibits using stored articles for
automated trading or text/data mining. That does not authorize a private eTape
cache or UI populated through automated ingestion. A consumer quote URL such as
[`/investing/stock/aapl`](https://www.marketwatch.com/investing/stock/aapl)
does not supply a documented symbol-normalization or related-news contract;
there is no basis to map eTape `US.<ticker>` or claim ticker-complete US news.
[Dow Jones Terms](https://www.dowjones.com/terms-of-use?mod=mw).

**Partner alternative and recommendation.** For programmatic news, Dow Jones
advertises sales-contact **Newswires Content Feeds & APIs** and licensed
**Factiva Feeds & APIs**. Those are enterprise products, not a free
MarketWatch API; confirm whether any desired MarketWatch content, retention,
display, symbol metadata, and latency are included before contracting.
[Newswires Content Feeds & APIs](https://www.dowjones.com/business-intelligence/newswires/products/content-feeds-and-apis/)
and [Factiva Feeds & APIs](https://www.dowjones.com/business-intelligence/factiva/products/feeds-and-apis/).

**Result:** do not scrape MarketWatch, poll an undocumented endpoint, or add a
MarketWatch RSS parser to eTape—even for private use. Treat ordinary interactive
reading as separate from an application integration. Only proceed after written
Dow Jones permission or a license that expressly covers the intended use.

## Explicit exclusions

- **Financial Modeling Prep:** its $0 plan is for testing/EOD/reference data;
  Financial Market News begins on its $22/month Starter plan. It also says
  displaying or redistributing FMP data requires a specific data-display and
  licensing agreement. [FMP pricing](https://site.financialmodelingprep.com/pricing-plans)
- **GDELT:** useful only as an unranked discovery/research tool. It is a free,
  open global news index updated every 15 minutes, but it is not ticker-native,
  its APIs are rate-limited, and it cannot grant rights to republish the
  third-party articles it indexes. [GDELT data](https://www.gdeltproject.org/data.html)
  and [API-rate note](https://blog.gdeltproject.org/ukraine-api-rate-limiting-web-ngrams-3-0/).

## Practical guardrails for any future provider

- Preserve provider article ID, source, author/publisher, original URL,
  provider timestamp, and provider symbol relation; deduplicate across OpenD
  and the supplemental feed by canonical URL/headline/time rather than claiming
  the sources are interchangeable.
- Never treat a vendor's $0 **personal** tier as a license to redistribute data
  through an application. Written provider approval or a business/display plan
  is required where the terms say so or are silent.
- Normalization should stay at the boundary: eTape uses `US.<ticker>`, whereas
  every candidate above documents bare ticker input. Resolve special share
  classes through an authoritative provider/SEC catalog rather than guessing a
  punctuation transformation.
