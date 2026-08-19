# Scanner Reported Short Interest

Status: Approved on 2026-08-18.

## Goal

Add a nullable, sortable **Short Int** column to the current sticky Scanner
board. Populate it asynchronously from Moomoo OpenD's reported US short
interest (`sharesShort`), display the provider's reporting date on hover, and
let Monitoring Scanner Sync follow a Scanner sorted by that field.

## Non-goals

- Do not add a Short Interest filter, change Scanner admissions, or expand the
  rank candidates into a global screener.
- Do not fetch data for symbols that have not entered the sticky Scanner
  board, subscribe to a new feed, or make ranking wait for enrichment.
- Do not substitute FINRA daily short-sale volume, borrow availability,
  shortability, Rule 201, short percentage, or days-to-cover for Reported
  Short Interest.
- Do not normalize the provider number for stock splits, persist the cache,
  add a setting, add a dependency, or expose a split-adjustment switch.
- Do not add a partial Scanner-row WebSocket event, React market-data state,
  or a second Scanner Sync path.

## Research and current-code evidence

- Moomoo's [Get Short Interest documentation](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-short-interest.html)
  defines OpenD protocol 3249. It accepts one security per request, returns US
  `timestampStr` and `sharesShort`, supports US stocks/funds, and permits at
  most 30 requests in 30 seconds. FINRA reports short interest twice monthly,
  rather than as a live position ([schedule](https://www.finra.org/filing-reporting/regulatory-filing-systems/short-interest)).
- Live 3249 reads on 2026-08-18 identified the reference metric as raw
  `sharesShort` and the expected two-decimal compact display:

  | Symbol | Moomoo report date | Raw `sharesShort` | Expected display |
  |---|---|---:|---:|
  | XOS | 2026-07-31 | 547,619 | 547.62K |
  | SGLY | 2026-07-31 | 9,067 | 9.07K |
  | SXTC | 2026-07-31 | 4,613,535 | 4.61M |

  `SHLY` was not the intended symbol; the user corrected it to `SGLY`.
  PFSA proved that a client-side split rule is unsafe: Moomoo's latest raw
  value was 59,866 while the observed reference display was 14.97K, whereas
  SXTC's observed display matched its raw 4.61M despite a later corporate
  action. The accepted behavior is therefore to show Moomoo's reported value
  exactly, with its report date, rather than infer a normalization rule.
- The generated 3249 protobuf already exists at
  [`Qot_GetShortInterest.pb.go`](../../engine/internal/feed/opend/pb/qotgetshortinterest/Qot_GetShortInterest.pb.go);
  only the engine's protocol constant and caller are missing.
- [`scan.go`](../../engine/internal/scan/scan.go) builds the sticky board from
  session rank candidates, publishes complete `scanner.rank` payloads, and
  already has a `poke` path for serially refreshing that board. Snapshot 3203
  is batched, but 3249 is one-symbol-per-request and needs its own bounded
  asynchronous path.
- [`payloads.go`](../../engine/internal/uihub/wsmsg/payloads.go) owns the
  Scanner wire contract. Its TypeScript projection
  [`ui/src/gen/wsmsg.ts`](../../ui/src/gen/wsmsg.ts) is generated and must not
  be hand-edited.
- The Scanner panel and Monitoring Sync share
  [`rankScannerRows`](../../ui/src/chrome/scannerSync.ts); the generic sorter
  already keeps `null` last in both ascending and descending order. AppShell
  recomputes Scanner Sync from the complete Scanner store payload and
  coalesces resulting chart patches once per second.
- [`formatCompactShares`](../../ui/src/chrome/format.ts) intentionally uses
  lower precision for Float and Volume (`547,619` would become `548K`). Short
  Interest needs a separate two-decimal formatter so those existing displays
  do not change.

## Design decisions

### Meaning, source, and scope

Use **Reported Short Interest** as the domain term and `Short Int` as the
compact table header. It is Moomoo's raw US `sharesShort` value at the paired
`timestampStr` settlement date. A zero value is valid when it is explicitly
reported; absent, malformed, or unsupported data is unavailable.

Request only Moomoo/OpenD 3249 for symbols after they have entered the
current sticky Scanner board. Request `num = 1`, use the newest returned US
record, and carry only `sharesShort` plus its `timestampStr`. Reject a record
without an explicit share-count field, a valid nonempty ISO report date, or a
JavaScript-safe share count; do not fabricate zero or a date. Do not request
the historical pages or use `shortPercent` and `daysToCover`.

Preserve the provider amount exactly. In particular, do not apply split data,
post-report share-count ratios, or any other local adjustment. The report date
makes the period and any stale corporate-action basis visible to the user.

### Asynchronous cache and publication

Keep a small process-local, Poller-owned cache keyed by scanner symbol. A
successful record stores its raw numeric value, report date, and successful
fetch time. It remains fresh for 24 hours. Deduplicate queued/in-flight work;
only one worker request runs at a time, paced no faster than one request per
second with the existing injectable engine clock. This stays within Moomoo's
30-per-30-second limit and gives a 30-row board a bounded, progressive initial
load rather than blocking a rank poll.

Start that worker from `Poller.Run` and stop it with the poller's context. On
each normal board refresh, copy cached values onto the rows and enqueue only
new or stale board symbols. On a result, update the cache and signal the
existing `poke` path so the serial poller republishes the **complete**
`scanner.rank` payload. No per-row event or UI-side merge is needed; the
existing store replacement makes the updated field visible and lets Scanner
Sync recalculate normally.

On a request, decode, provider, or malformed-record failure, retain the last
successful value and date. If no real record has ever succeeded, render `—`.
Do not retry in a tight loop; leave the symbol eligible for a later
rate-limited retry. A successful empty/no-record response may be cached as
unavailable for the same 24-hour period only when there is no prior successful
record, so it does not erase useful information.

The cache is intentionally memory-only. A restart begins with `—` values and
refills in the background; no database/schema/config migration is warranted.

### Wire, display, sort, and Sync

Add nullable `shortInterest` and `shortInterestAsOf` fields to the
engine-owned `ScannerRow` contract, and regenerate the TypeScript contract.
Use `number | null` for the amount only after validating its safe conversion
from the provider's `uint64`; use `string | null` for the report date. The
Scanner Store normalizes missing fields from an older engine to `null`, as it
already does for optional Scanner fields.

Append `Short Int` to the existing table so current Scanner columns do not
move. Add a narrowly scoped `formatShortInterest` helper beside the existing
table formatters, with two decimals per suffix: `547619 → 547.62K`,
`9067 → 9.07K`, and `4613535 → 4.61M`. Keep the Float/Volume formatter
unchanged. An unavailable value renders `—`; a valid zero renders `0`. The
cell's tooltip contains only `as of YYYY-MM-DD`. It must not surface short
percent, days-to-cover, borrow, or a derived explanation.

Add the `shortInterest` accessor to the shared Scanner sorter. The existing
generic sorting rule places unavailable values last for both directions and
retains stable tie order. Persisting `{ col: "shortInterest", dir }` in a
Scanner panel's existing settings is sufficient: AppShell already consumes
that same sort to rank Monitoring Scanner Sync, and it will coalesce the
background result updates normally.

No ADR is needed: this is a reversible, localized provider-field enrichment;
the lasting terminology and no-split rule live in [`CONTEXT.md`](../../CONTEXT.md).

## File-level implementation

1. In [`engine/internal/feed/opend/protoid.go`](../../engine/internal/feed/opend/protoid.go), add the named 3249 `ProtoQotGetShortInterest` constant next to the other quote protocol IDs. Reuse the checked-in generated short-interest protobuf; do not regenerate or edit generated protobuf code.

2. In [`engine/internal/scan/scan.go`](../../engine/internal/scan/scan.go):
   - import the generated short-interest protobuf and add the minimal
     Poller-owned cache, pending-work bookkeeping, and one-at-a-time worker;
   - construct a US-security 3249 request with `num: 1`, validate/decode its
     newest record, and store raw shares plus the report date without a split
     transformation;
   - make cache reads/writes race-safe, use `p.clk` for the request pace and
     freshness/retry timing, and preserve a prior success after a failed
     refresh;
   - schedule work only after sticky-board admission, overlay cached values
     when projecting rows, and signal `p.poke` after a changed result so the
     normal complete-payload publication path refreshes the UI; and
   - keep rank polling, filtering, snapshot batching, pool demand, and Scanner
     hit semantics non-blocking and otherwise unchanged.

3. In [`engine/internal/uihub/wsmsg/payloads.go`](../../engine/internal/uihub/wsmsg/payloads.go), add nullable `ShortInterest` and `ShortInterestAsOf` to `ScannerRow` with explicit generated TypeScript null annotations. Regenerate [`ui/src/gen/wsmsg.ts`](../../ui/src/gen/wsmsg.ts) from this Go owner.

4. In [`engine/internal/synth/requester.go`](../../engine/internal/synth/requester.go), implement a deterministic 3249 demo response from the existing synthetic symbol/fundamental data and quote date. Do not introduce a separate synthetic short-interest model. Extend [`requester_test.go`](../../engine/internal/synth/requester_test.go) to prove the response obeys the 3249 request/response shape.

5. In [`engine/internal/scan/scan_test.go`](../../engine/internal/scan/scan_test.go), extend the existing fake requester and add focused tests for:
   - the exact 3249 US request (`num = 1`) and raw value/date projection;
   - an explicitly reported zero, missing/malformed data, non-zero provider
     responses, and values outside the JavaScript-safe range;
   - no split adjustment (the raw SXTC-style value is emitted unchanged);
   - queue deduplication, one-per-second pacing, only-board-symbol scheduling,
     24-hour success freshness, and a later rate-limited retry;
   - retaining the last success through a refresh failure, with `—` only before
     any record succeeds; and
   - a worker completion causing the normal full Scanner payload to contain the
     new fields without blocking rank publication.

6. In [`ui/src/data/ScannerStore.ts`](../../ui/src/data/ScannerStore.ts) and
   its tests, normalize omitted `shortInterest` and `shortInterestAsOf` fields
   to `null` while retaining the existing full-row replacement semantics.

7. In [`ui/src/chrome/format.ts`](../../ui/src/chrome/format.ts) and
   [`format.test.ts`](../../ui/src/chrome/format.test.ts), add and test the
   dedicated two-decimal Short Interest formatter. Leave
   `formatCompactShares` unchanged.

8. In [`ui/src/chrome/panels/ScannerPanel.tsx`](../../ui/src/chrome/panels/ScannerPanel.tsx) and its tests:
   - append the sortable `Short Int` header/cell and update the empty-table
     column span;
   - render the exact compact values, `—` for unavailable data, and only the
     report-date tooltip; and
   - prove header sorting persists `shortInterest` and all typed row fixtures
     carry the regenerated nullable fields.

9. In [`ui/src/chrome/scannerSync.ts`](../../ui/src/chrome/scannerSync.ts) and
   its tests, add the short-interest accessor and cover finite values ahead of
   `null` in both sort directions. Use an AppShell/Scanner Sync integration
   test only to prove a later full Scanner payload updates the source ordering;
   do not add a new sync mechanism.

10. Update durable documentation when the code lands:
    - [`CONTEXT.md`](../../CONTEXT.md) now defines Reported Short Interest and
      its As-of Date, including the no-split-adjustment rule;
    - [`engine/internal/scan/README.md`](../../engine/internal/scan/README.md)
      will describe the board-only 3249 worker, one-day cache, delayed report
      date, raw source basis, and non-blocking publication;
    - [`docs/external-apis.md`](../../docs/external-apis.md) will document
      protocol 3249, its per-symbol/rate-limit constraints, and exact source
      fields;
    - [`ui/src/chrome/README.md`](../../ui/src/chrome/README.md) will state
      that Short Int is a Scanner Source sort that Monitoring Sync follows; and
    - [`README.md`](../../README.md) will list reported short interest among
      Scanner context fields, not filters.

## Validation

Run focused tests while implementing, then the Windows CI-equivalent checklist
required for a substantial engine-and-UI change:

```powershell
Set-Location engine
go test ./...
go test -race -short ./...
go vet ./...
golangci-lint run  # pinned v2.12.2
Set-Location ..
mingw32-make -C engine gen-ts-check
Set-Location ui
npm ci
npm run lint
npm test
npm run build
npm run e2e
Set-Location ..
git diff --check
```

Also verify the generated TypeScript has no drift, Scanner payloads remain in
the imperative store rather than React state, no live-order path changes, and
the reference examples render exactly as `547.62K`, `9.07K`, and `4.61M`.

## Rollout and rollback

The normal local build rolls this out with no setting, database, provider
configuration, or entitlement migration. The first process starts with `—`
until its current board is progressively enriched; a 30-row board takes at
most roughly 30 seconds at the deliberate one-request-per-second pace. Later
sort changes may repopulate Monitoring charts through the already coalesced
Scanner Sync behavior.

Rollback is a scoped revert. Older UIs ignore the added JSON fields; the new
UI treats omitted fields from an older engine as unavailable. The memory cache
disappears on restart and leaves no durable state to migrate or remove.

## Risks

- Reported Short Interest is delayed and may be based on a pre-corporate-action
  settlement date. Showing raw shares plus the date is intentional; users must
  not read it as a live float, daily short volume, borrow signal, or live
  short-selling permission.
- Moomoo entitlement, provider errors, missing records, and the per-symbol
  rate limit can leave values temporarily unavailable. They must never delay
  scanner rankings or erase a previously good value.
- Enrichment can change a Short Int sort after the initial board appears, so
  Monitoring Scanner Sync can move charts as values arrive. This is the
  accepted normal shared-sort behavior and remains throttled by the existing
  one-second chart-patch coalescer.
