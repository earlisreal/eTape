# US-Listed Stock Issuer and Classification Metadata Research

Reviewed 2026-08-17. The research pass itself changed no code or generated
contracts; the product decision below was subsequently implemented.

## Product decision after research

The Stock Info requirement intentionally accepts Yahoo's undocumented profile
endpoint as a best-effort source: Yahoo supplies Operating Country and Sector,
and its Industry value is used only when Moomoo's Industry plate is blank.
The implementation keeps this path asynchronous, cached, optional via
`[stockinfo].yahoo_metadata`, and non-blocking for Moomoo fundamentals. The
source-quality risks and alternatives below remain part of the decision record.

## Recommendation

Keep Moomoo OpenD as the Stock Info runtime source for the fields it already
provides: display name, US-market/exchange identity, and one broad
industry-plate label. Do not call that label GICS or infer sector, industry
group, sub-industry, or issuer country from it.

For a small, useful improvement, add a separate slow metadata cache backed by
SEC EDGAR submissions: CIK/ticker resolution, legal issuer name, reporting
exchange/ticker, SIC code/description, state/country of incorporation, and
business/mailing address where present. SEC is authoritative for filing
identity and SIC, but SIC is not GICS and SEC does not publish a GICS sector
hierarchy.

If Stock Info must display actual GICS Sector → Industry Group → Industry →
Sub-Industry, use a licensed GICS security-classification feed from MSCI/S&P
or another vendor that explicitly licenses those fields. Do not create a
home-grown GICS mapping from SIC or from Moomoo's plate names.

Repository context: [Stock Info flow](../engine/internal/stockinfo/README.md),
[current Moomoo/Alpaca implementation](../engine/internal/stockinfo/stockinfo.go),
and [external API conventions](external-apis.md).

## Terminology: GICS hierarchy

GICS is a four-tier hierarchy, from broadest to most specific:

1. Sector
2. Industry Group
3. Industry
4. Sub-Industry

MSCI and S&P state that each company receives one classification at each tier
according to its principal business activity, with revenue a key factor and
earnings and market perception also considered. The current methodology
describes 11 sectors, 25 industry groups, 74 industries, and 163 sub-industries
and represents a full classification as an eight-digit code. The counts and
names are versioned, so a provider's generic `industry` string must not be
silently relabeled as a GICS tier.
[MSCI GICS overview](https://www.msci.com/indexes/index-resources/gics)
and [MSCI GICS methodology, August 2024](https://www.msci.com/downloads/web/msci-com/indexes/index-resources/gics/MSCI_Global_Industry_Classification_Standard_%28GICS%C2%AE%29_Methodology_20240801.pdf).

## Provider comparison

| Source | Country / issuer identity | Sector / industry metadata | Supportability and eTape fit |
| --- | --- | --- | --- |
| **Moomoo OpenD, already used** | `Qot_GetStaticInfo` returns security code, name, listing date, delisting flag, security type, and exchange type. The `US.` market prefix identifies the trading market, not incorporation country or issuer domicile. | `Qot_GetOwnerPlate` returns `PlateInfo` rows with a name and `PlateSetType`; the documented `Industry` type is the only direct match to the current repo field. Moomoo documents US regional plates as temporarily empty, and it does not document the result as GICS or expose the four GICS tiers. | **Documented/supported.** Already connected through raw OpenD protobuf. Current code caches industry and exchange for process lifetime and retries transport/decode failures on later ticks. Good primary source for the existing `Industry` display; insufficient as a complete classification source. [Get Owner Plate](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-owner-plate.html), [quotation definitions](https://openapi.moomoo.com/moomoo-api-doc/en/quote/quote.html), [Get Static Info](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-static-info.html). |
| **Alpaca Assets API/SDK** | Asset `symbol`, official `name`, `class`, `exchange`, `status`, and an internal asset ID. This is broker asset identity/listing venue, not issuer country. | No `sector`, `industry`, GICS code, industry group, sub-industry, SIC, or incorporation-country field is in the documented Asset object. Existing eTape use correctly limits it to borrow/shortable/marginable/tradable metadata. | **Documented/supported, credentialed.** `GET /v2/assets?status=active` is an official master asset list and requires Alpaca API headers. It is a useful symbol/exchange/tradability supplement only; it cannot fill the requested classification fields. [Alpaca Get Assets](https://docs.alpaca.markets/us/reference/get-v2-assets-1), [official Asset object](https://github.com/alpacahq/alpaca-docs/blob/master/content/api-references/broker-api/assets.md), [repo adapter](../engine/internal/broker/alpaca/rest.go). |
| **Yahoo Finance** | Consumer quote pages may display company facts, but Yahoo's published developer catalog does not list a Finance market-data/classification API. | Finance page data and endpoints commonly used by third-party clients are not a documented Yahoo Finance API contract. Treat any `query1.finance.yahoo.com`/`query2.finance.yahoo.com` quoteSummary or page JSON endpoint as **unofficial/undocumented**, even if it currently responds. No supported contract should be assumed for country, sector, industry, or sub-industry. | **Not recommended.** No new Yahoo credential solves the missing published Finance API. An undocumented endpoint can change or disappear and should not become a reliability dependency. [Yahoo Developer API catalog](https://developer.yahoo.com/api/). |
| **SEC EDGAR submissions (useful alternative)** | `data.sec.gov/submissions/CIK##########.json` provides filer identity metadata, including current/former name, exchange/ticker data, addresses, and incorporation fields when present. Resolve ticker to CIK using the SEC's official company-ticker file, then fetch the CIK submission JSON. | SEC disseminated filings carry the company's SIC and the SEC publishes the SIC code list and description. SIC is an official business classification, but it is a different taxonomy from GICS and should be stored as `sic_code`/`sic_description`, not `industry`. | **Documented/supported, no API key.** SEC says the JSON APIs are unauthenticated, updated throughout the day as filings are disseminated, and require a declared descriptive `User-Agent` plus rate discipline. Excellent slow fallback for issuer identity and SIC; not a real-time quote source or GICS source. [SEC EDGAR APIs](https://www.sec.gov/search-filings/edgar-application-programming-interfaces), [company tickers](https://www.sec.gov/file/company-tickers), [SEC SIC list](https://www.sec.gov/search-filings/standard-industrial-classification-sic-code-list). |
| **Licensed GICS feed (useful alternative when exact GICS is required)** | Depends on the licensed vendor's security master and identifier mapping. | Supplies the actual four GICS tiers and codes, subject to the current licensed GICS version and vendor coverage. MSCI/S&P own and maintain the standard; the public GICS pages explain the hierarchy but are not a free per-ticker production API. | **Best semantic source, commercial integration.** Obtain a license that covers programmatic retrieval, caching, display, and updates. This adds vendor credentials/agreement and ongoing refresh handling; it is justified only if GICS-level filtering or display is a real product requirement. [MSCI GICS](https://www.msci.com/indexes/index-resources/gics), [MSCI data licensing](https://www.msci.com/data-and-analytics). |

## Field-level conclusions

### Issuer country

There are several different meanings that should not be collapsed into one
`country` field:

- `listing_market`: Moomoo's `US` market / Alpaca's `us_equity` asset class.
- `listing_exchange`: Moomoo `exchType` or Alpaca `exchange`.
- `incorporation_country`: SEC filer metadata when available.
- `operating_country` or `revenue_country`: a separate business fact that
  these asset endpoints do not establish.

For Stock Info, name the field by its meaning. If the UI only needs to say
that a security is US-listed, derive that from the existing symbol/listing
contract. If it needs issuer domicile, use SEC and allow it to be blank or
stale when the filer record does not provide a clean value.

### Sector, industry, and sub-sector

The repo's current Moomoo value is a vendor plate label selected from the
`Industry` plate type; it is not evidence of GICS. Alpaca supplies none of
these fields. SEC supplies SIC, which is useful and authoritative but should be
shown with its taxonomy name. The phrase “sub-sector” is ambiguous here: for
GICS the formal levels are Sector, Industry Group, Industry, and Sub-Industry;
store `industry_group` or `sub_industry` explicitly rather than inventing
`sub_sector`.

## Practical Stock Info design

### Recommended source and fields

Keep the existing Moomoo payload and add fields only when the UI requirement
exists:

- `listing_exchange`: Moomoo static info, with Alpaca as optional venue
  confirmation.
- `industry_label` plus `industry_source="moomoo_plate"`: current broad label.
- Optional SEC fields: `issuer_cik`, `issuer_name`,
  `incorporation_country`, `sic_code`, `sic_description`.
- Optional licensed fields: `gics_version`, `gics_sector`,
  `gics_industry_group`, `gics_industry`, `gics_sub_industry`, and codes.

Do not merge SEC SIC and Moomoo industry into one normalized `industry`
string. Keep source and taxonomy visible in the model so later classification
changes are auditable.

### Cache and refresh

Use a disk-backed or process-start metadata cache keyed by stable CIK when
available, with ticker/symbol mappings refreshed separately. Refresh SEC
identity/SIC on startup and at most daily or on an explicit manual refresh;
filings can change throughout the day, but this is not Stock Info's quote-rate
path. Cache successful blanks with a timestamp and retry transient failures on
a longer backoff. Preserve the last good record during an outage.

Keep GICS refresh at the licensed vendor's version/update cadence, normally
daily or on announced classification changes, and retain the source version.
Do not request SEC, GICS, Alpaca, or Yahoo metadata on every Stock Info tick.
The current Moomoo industry/exchange lifetime caches are already aligned with
this low-frequency behavior; the existing snapshot/fundamental polling should
remain unchanged. [Current Stock Info README](../engine/internal/stockinfo/README.md).

### Fallback behavior

1. Render the last successful value with its source/timestamp.
2. If no external metadata exists, render the existing Moomoo industry label
   and listing exchange only.
3. If Moomoo industry resolution fails, show unknown rather than mapping from
   SIC, Alpaca asset name, or Yahoo page text.
4. If SEC is unavailable, do not block quotes or Stock Info fundamentals.
5. If a licensed GICS feed is absent or expired, leave GICS fields blank and
   label them unavailable; never claim a lower-quality proxy is GICS.

### Dependencies and credentials

No new dependency or credential is required to keep the current behavior.
SEC EDGAR needs no API key, but it does require a descriptive User-Agent and
reasonable request pacing. Alpaca already requires credentials in eTape and
adds no classification value. Yahoo would add no supported capability.
A real GICS integration requires a commercial data agreement and whatever
authentication the selected licensed provider specifies; it should not be
added speculatively.

## Bottom line

For the requested fields, the smallest reliable stack is **Moomoo for live
listing/existing plate data + SEC for slow issuer/SIC identity**. Exact
Sector/Industry Group/Industry/Sub-Industry requires **licensed GICS data**.
Alpaca remains useful for execution eligibility, and Yahoo Finance should be
excluded from automated metadata ingestion because its official developer
catalog does not document a Finance API.
