# Moomoo Volume Ratio Research for Scanner

Reviewed 2026-08-18. Research only; no product decision or implementation is
made here.

## What Moomoo/Futu means by `volume_ratio`

For stocks, the vendor's definition is:

```text
volume ratio =
  (current total traded lots / cumulative market-open minutes today)
  / average volume per minute over the prior five trading days
```

That is Moomoo/Futu's own formula and description: it compares today's
average volume rate since the market opened with the average per-minute rate
over the prior five trading days. [Futubull support: Basic concepts of a
stock](https://support.futunn.com/en/topic120?lang=en-us) (the Chinese
original explicitly says “five trading days”:
[股票摘要字段介绍](https://support.futunn.com/topic120)).

Consequences of that formula:

- It is a **unitless multiplier**, not a percentage: `1.0` means the same
  rate as the stated baseline, `2.0` twice the rate, and `0.5` half the rate.
  This is a direct inference from the vendor formula.
- It is elapsed-session normalized, but it is **not documented as a
  same-time-of-day historical-volume comparison**. In particular, the
  published formula uses one five-day average-per-minute baseline rather than
  prior days' volume at the current clock time.
- “Lots” is the vendor's wording. The ratio itself needs no lot-to-share
  conversion because the numerator and denominator use the same volume unit.
- Do not confuse this stock field with the similarly named options
  `VOLUME_RATIO`, which Moomoo defines as a **put/call volume ratio** in its
  option-underlying ranks. [Quotation definitions](https://openapi.moomoo.com/moomoo-api-doc/en/quote/quote.html).

## Availability in the relevant APIs

| API | US availability / field | Extended-hours behavior documented by the API | Planning consequence |
| --- | --- | --- | --- |
| **Get Top Movers Rank** (`3413`) | Supports `Market.US`; each RTH rank item includes `volume_ratio`. The documented item also contains price, change, turnover, volume, market cap, and amplitude. [API doc](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-top-movers-rank.html) | It is documented as an intraday top-movers rank; the response does not describe a pre/post/overnight volume-ratio definition. | This is the current Scanner's RTH rank source, so carrying its returned field needs no per-symbol snapshot fan-out. Its documented server filters are price, market cap, and P/E—not volume ratio—so a Scanner min-RVOL filter would be applied after this rank source unless the source changes. |
| **Get US Pre-Market / After-Hours / Overnight Rank** (`3410` / `3411` / `3412`) | Their documented result schemas contain the respective session price, change, turnover, and volume, but **no `volume_ratio` field**. [Pre-market](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-us-pre-market-rank.html), [after-hours](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-us-after-hours-rank.html), [overnight](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-us-overnight-rank.html). | Each API is explicitly session-specific, but none supplies a session-relative volume ratio. | The existing extended-hours Scanner source cannot truthfully display provider RVOL. Prefer an unavailable value (`—`) there unless a separately defined eTape metric is approved. |
| **Get Market Snapshot** (`3203`) | The documented example accepts `US.AAPL`, returns `volume_ratio`, and permits up to 400 symbols per request. [API doc](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-market-snapshot.html) | Snapshot separately exposes `pre_volume`, `after_volume`, and `overnight_volume`; its `volume_ratio` documentation does **not** state whether any of those sessions enter the ratio. | It can batch-enrich known symbols, but it adds a request and must not be treated as an extended-hours RVOL contract without provider confirmation. |
| **Get Real-time Quote / Real-time Quote Callback** (`3004` / `3005`) | The documented subscribed pull and push schemas list ordinary and separate extended-session volume fields, but no `volume_ratio`. [Pull API](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-stock-quote.html), [callback API](https://openapi.moomoo.com/moomoo-api-doc/en/quote/update-stock-quote.html). | They carry separate `pre_*`, `after_*`, and `overnight_*` fields rather than a relative-volume field. | Do not expect a quote subscription to continuously update this provider metric. RTH Scanner rank data or an explicit snapshot poll is required. |
| **Legacy Stock Filter** (`3215`) | It exposes `VOLUME_RATIO` as a simple stock field; the API documents three-decimal truncation and gives a raw range example such as `[0.5, 30]`. [Quotation definitions](https://openapi.moomoo.com/moomoo-api-doc/en/quote/quote.html), [API doc](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-stock-filter.html) | The API explicitly says it does not support irregular trading hours and that all results use regular-trading-hours data. | This establishes a provider RTH boundary, but is not a reason to replace Scanner's established rank API. |
| **Get Period Change Rank** (`3416`) | Supports `Market.US`, returns `volume_ratio`, and documents `VolumeRatio` as a range-filter indicator. [API doc](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-period-change-rank.html) | No extended-hours RVOL semantics are documented. | It is an alternate provider-side filtering path, but it changes the upstream rank semantics; do not substitute it accidentally for Top Movers. |

## Live pre-market spot check

At 2026-08-18 04:14 ET, a direct `Get Market Snapshot` read returned a
finite `volume_ratio` alongside pre-market volume for each of the current US
pre-market leaders below. This proves the snapshot field is populated before
RTH for these symbols; it does **not** establish the vendor's undocumented
extended-hours aggregation formula.

| Symbol | Provider `volume_ratio` | Pre-market volume |
| --- | ---: | ---: |
| PFSA | 19.466 | 537,558 |
| XOS | 0.489 | 926,876 |
| WETO | 2.447 | 100,124 |
| EJH | 5.150 | 869,856 |
| WFF | 58.061 | 1,461,676 |

## Session and edge-case limits

The formula divides by cumulative market-open minutes. Moomoo/Futu does not
publish how `volume_ratio` behaves when that time is zero (before the open),
when there is no usable five-day history, during a halt, or across early
closes/holidays. It also does not publish whether snapshot `volume_ratio`
includes US pre-market, after-hours, or overnight prints. The snapshot's
separate session-volume fields show that the sessions exist as distinct quote
data, not how the ratio aggregates them. [Market snapshot](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-market-snapshot.html),
[quotation definitions](https://openapi.moomoo.com/moomoo-api-doc/en/quote/quote.html).

Therefore the plan should preserve three distinct states:

1. a finite provider value (including a valid `0` if one is supplied);
2. unavailable/missing; and
3. not applicable for the current Scanner session.

Do not locally recompute a value from raw volume as a fallback: doing so would
silently choose unspecified session, early-close, and five-day-baseline rules.
The snapshot/protobuf field is an optional floating-point value, so absence
must not be coerced to zero. [Get Market Snapshot](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-market-snapshot.html).

## Decisions the Scanner plan must make

1. **Meaning:** call the provider value `Volume Ratio` (or `Moomoo Volume
   Ratio`), not a generic eTape-computed “relative volume,” unless a different
   formula and data source are deliberately specified.
2. **Session policy:** show/filter it only in RTH and render `—` in premarket,
   after-hours, and overnight; or explicitly fund a distinct, documented
   eTape extended-hours metric. Do not relabel raw extended-hours volume as
   this five-day ratio.
3. **Filtering scope:** RTH Top Movers already returns the field but cannot
   filter it server-side. A local threshold only filters the upstream
   top-movers candidate set; switching to Period Change Rank would change that
   candidate set and needs a product decision.
4. **Presentation:** show it as a multiplier (for example `1.54×`), never as
   `154%`. Retain provider precision internally; the vendor only promises
   three-decimal truncation for the legacy Stock Filter field, not every
   endpoint above.
