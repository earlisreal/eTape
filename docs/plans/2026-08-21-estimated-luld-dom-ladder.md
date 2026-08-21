# Estimated LULD Band in DOM Ladder

Status: Approved on 2026-08-21.

## Goal

Show a compact **Estimated LULD Band** in the Level 2 DOM ladder without an
Alpaca real-time SIP subscription. The display gives a clearly labelled local
approximation during regular trading hours, plus bounded price markers when
they are in the visible ladder range.

The output is a visual aid only. It never influences an order, a risk check,
or a trading-state decision.

## Non-goals

- Do not claim or infer an official LULD price band, Limit State, Straddle
  State, Trading Pause, reopening, or regulatory halt.
- Do not buy, proxy, scrape, or otherwise depend on SIP data, including an
  Alpaca SIP entitlement. Alpaca's free live data is IEX-only, not SIP:
  [Market Data FAQ](https://docs.alpaca.markets/us/docs/market-data-faq).
- Do not calculate bands outside regular trading hours, use a bar or prior
  close as a substitute for a fresh initial reference, or support the future
  overnight-LULD regime in this change.
- Do not infer a symbol's LULD tier, ETP status, or leverage from its name,
  Moomoo security type, or any unstated default. A symbol absent from the
  checked-in registry has no displayed band.
- Do not add a runtime registry fetcher, a new dependency, a new WebSocket
  topic, a second UI market-data store, or React state for high-frequency DOM
  data.

## Research and current-code evidence

- The [current LULD Plan overview](https://www.luldplan.com/) defines the
  reference price as the arithmetic mean of eligible reported transactions
  over the preceding five minutes. It updates after at least 30 seconds and
  only when the proposed reference differs by at least 1%; it also specifies
  the regular-hours coverage and current percentage buckets.
- The Plan's bands are calculated and disseminated by the SIPs. A local
  calculation fed by Moomoo cannot be official, especially around openings,
  reopenings, trade eligibility, or feed differences. The
  [current proposed overnight amendment](https://cdn.luldplan.com/plan-amendments/LULD_Overnight_Amendment_Final.pdf)
  confirms that early closes are regular-hours schedule changes and that the
  overnight regime is future scope.
- Tier 1 consists of S&P 500, Russell 1000, and selected ETPs; Tier 2 is the
  remaining covered NMS universe, excluding rights and warrants. Nasdaq
  publishes a free dated Tier 1 ETP workbook and documents its update cadence
  in [ETA2026-34](https://www.nasdaqtrader.com/TraderNews.aspx?id=ETA2026-34).
- Moomoo documents Basic Quote suspension and security status as generic
  provider fields, not official LULD fields:
  [real-time quote fields](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-stock-quote.html)
  and [security status values](https://openapi.moomoo.com/moomoo-api-doc/en/quote/quote.html).
- In eTape, BasicQot already reaches
  [decodeBasicQot](../../engine/internal/feed/opend/decode.go), but
  [feed.Quote](../../engine/internal/feed/feed.go) currently discards both
  isSuspended and optional secStatus. Existing
  [stampEligibility](../../engine/internal/md/eligibility.go) is the single
  place that produces a Last-Eligible Print from ticker input.
- [md.Core](../../engine/internal/md/core.go) is the single-writer market
  state loop, and the existing [session](../../engine/internal/session)
  calendar already handles RTH and early closes. The existing injected
  [clock](../../engine/internal/clock) package makes a time-driven derived
  state testable without calling wall time from a reducer.
- [wsmsg.Book](../../engine/internal/uihub/wsmsg/payloads.go) owns the
  WebSocket contract. The mirror caches and republishes it on the existing
  **md.book** topic; [BookStore](../../ui/src/data/BookStore.ts) already
  replaces that object imperatively. The ladder painter is consequently the
  smallest UI seam.

## Agreed design

### Meaning, scope, and tier coverage

Use **Estimated LULD Band** everywhere in product copy and wire semantics.
The normal compact readout is:

~~~text
EST LULD 3.32–3.68 · T1 · ESTIMATED · REG 2026-07-01
~~~

It is available only during RTH according to the existing exchange calendar,
including the calendar's early close. Outside RTH the readout says it is
unavailable; it does not retain a prior day's band.

Use an embedded, reviewed, dated symbol allowlist. Its first snapshot contains
the Tier 1 S&P 500/Russell 1000 constituent snapshot, Nasdaq's official Tier 1
ETP list, and explicit Tier 2/leveraged-ETP overrides needed by eTape users.
Every supported symbol has an explicit record. Unknown, expired, malformed,
rights, warrants, and unlisted symbols display:

~~~text
EST LULD — · TIER UNKNOWN
~~~

There is deliberately no “all other symbols are Tier 2” fallback.

The registry records as-of date, valid-through date, source provenance,
explicit tier, and an explicit Plan multiplier where required for a leveraged
Tier 2 ETP. It is loaded through Go's standard-library embedding and validated
at startup. No runtime classification or network fetch is needed. Once the
valid-through date passes, all of its bands are unavailable rather than stale.

### Local reference and percentage calculation

Only eTape's existing **Last-Eligible Print** is eligible input. Quote last,
book changes, one-minute bars, daily bars, and previous close never substitute
for an eligible print in the reference calculation. Moomoo PrevClose is used
only as the clearly estimated daily price-bucket input; a missing, non-finite,
or non-positive value makes the band unavailable.

Maintain a bounded deque of Last-Eligible prices and exchange timestamps for
the immediately preceding five minutes. Use its arithmetic mean as the
candidate reference. Apply the Plan cadence locally:

1. Establish a five-minute uninterrupted local coverage epoch after initial
   subscription, reconnect, or recovery from an abnormal provider status.
   Require at least one accepted Last-Eligible Print in that epoch before
   showing a band; do not seed the first reference from a bar or previous
   close.
2. After warm-up, replace the effective reference only when the candidate
   mean is at least 1% from it and at least 30 seconds have elapsed. Recompute
   when an eligible print arrives and when the five-minute window advances.
3. If the connected feed goes quiet after an effective reference exists,
   retain the existing reference and bands. Quiet input alone is not a feed
   failure.
4. Derive upper and lower bands from that effective reference, round each to
   the nearest cent in one tested Go helper, and keep the reference and both
   bands together as one derived value.

Use a table-driven implementation of the current Plan percentage rules:

| Previous-close bucket | Tier 1, and Tier 2 at or below $3 | Tier 2 above $3 |
| --- | ---: | ---: |
| Above $3 | 5% | 10% |
| $0.75 through $3, inclusive | 20% | not applicable |
| Below $0.75 | lesser of $0.15 or 75% | not applicable |

During the final 25 minutes of the scheduled RTH session, double the
applicable percentage for every Tier 1 symbol and for Tier 2 symbols at or
below $3. Apply a Plan-defined leveraged-ETP multiplier only when the registry
explicitly supplies it. The existing session schedule, not a hard-coded
16:00 ET cutoff, decides this interval.

The local calculation intentionally does not emulate the Plan's official
opening/reopening reference rules. It displays **WARMING** for five minutes
instead, which is safer than pretending a non-SIP feed knows the official
opening or reopening price.

### Provider-status and connection handling

decodeBasicQot is the correct ingestion seam for both fields:

- Preserve isSuspended as an affirmative provider-suspension signal.
- Preserve presence of optional secStatus; do not collapse absence by using
  its protobuf getter. Map it at the feed boundary to a small semantic state:
  **unknown** when absent, **normal** when explicitly normal, and
  **nonnormal** for every explicitly non-normal or unrecognized value.
- An affirmative suspension or nonnormal provider status freezes the latest
  local band and labels the readout **ESTIMATE FROZEN — PROVIDER STATUS**.
  This wording must not imply a LULD pause.
- isSuspended=false with absent secStatus is neutral: it neither freezes a
  healthy estimate nor clears an existing provider-status freeze. An explicit
  normal status starts a fresh coverage epoch.
- A ConnDown likewise freezes the last estimate. Fresh post-reconnect ticker
  seed/live events count toward a new epoch; the subsequent Resynced event
  must not discard that fresh seed or make the estimate appear ready.

The visible state is one of:

| State | Meaning | DOM treatment |
| --- | --- | --- |
| unavailable | Outside RTH, invalid price bucket, unknown/expired registry, or no usable daily bracket | Show EST LULD — and a concise reason; no markers |
| warming | RTH is eligible but the new five-minute coverage epoch is incomplete | Show EST LULD — · WARMING; no markers |
| estimated | Local input satisfies the stated approximation rules | Show both values, tier, ESTIMATED, and registry date |
| frozen | Last local result is retained through a provider-status or transport interruption | Show retained values and a non-regulatory warning; draw retained markers only when values exist |

### DOM presentation and accessibility

Keep the existing DOM geometry. The compact LULD readout shares the current
fixed spread/chrome strip; it does not consume a price row or move the book.
At narrow widths, abbreviate secondary metadata before omitting the explicit
EST qualifier.

Draw dashed, text-labelled L and U markers only when a current estimated or
frozen band falls inside the visible price range. Place them at the
corresponding price or interpolate between adjacent visible ladder levels.
Do not auto-scroll, re-center, or manufacture an out-of-range marker. Use the
existing canvas palette and mono font; labels and status text, not color alone,
carry the meaning.

Update the ladder canvas accessible name (or its existing equivalent) when
the LULD revision changes so assistive technology receives the state, values,
tier, and registry date without receiving every book repaint.

## File-level implementation

1. Extend [engine/internal/feed/feed.go](../../engine/internal/feed/feed.go)
   with the smallest source-neutral quote-status representation: suspension
   plus unknown/normal/nonnormal. In
   [engine/internal/feed/opend/decode.go](../../engine/internal/feed/opend/decode.go),
   populate it from BasicQot.isSuspended and the *presence* and value of
   BasicQot.secStatus. Keep Moomoo protobuf enum values out of the rest of the
   engine. Add focused cases in
   [decode_test.go](../../engine/internal/feed/opend/decode_test.go) for
   absent status, explicit normal, each abnormal path, and suspension.

2. Add **engine/internal/md/luld_registry.json**,
   **engine/internal/md/luld_registry.go**, and
   **engine/internal/md/luld_registry_test.go**.
   Embed the reviewed registry, validate date/tier/multiplier/provenance and
   symbol keys, reject expired records at read time, and make the lookup an
   allowlist. Document the manual source-and-review process beside the data;
   do not add a downloader or a classifier.

3. Add the isolated local calculator in **engine/internal/md/luld.go** and
   focused tests in **engine/internal/md/luld_test.go**.
   Give it the deque, coverage epoch, effective-reference cadence, tier
   table, rounding, state/reason, and output equality needed to suppress
   unchanged publications. It consumes the already stamped Last-Eligible
   path, never reimplements eligibility.

4. Wire the calculator through
   [engine/internal/md/core.go](../../engine/internal/md/core.go) and
   [update.go](../../engine/internal/md/update.go). Reuse the injected clock
   with one core-level time event so sliding-window expiration, warm-up,
   registry expiry, and session transitions are deterministic. Feed quote
   status, connection events, and accepted ticks into the same per-symbol
   state. Publish an EstimatedLULDUpdate only when its visible derived state
   changes; do not create an update for every tick.

5. Add an optional nested EstimatedLULD value to
   [engine/internal/uihub/wsmsg/payloads.go](../../engine/internal/uihub/wsmsg/payloads.go)
   with lower/upper/reference values, tier, state/reason, and registry
   as-of metadata. Regenerate
   [ui/src/gen/wsmsg.ts](../../ui/src/gen/wsmsg.ts) from this Go owner; never
   edit the generated file.

6. In [engine/internal/uihub/map.go](../../engine/internal/uihub/map.go) and
   [mirror.go](../../engine/internal/uihub/mirror.go), cache the derived value
   by symbol and merge it into the cached Book before publishing the ordinary
   **md.book** replacement. Handle a derived update that precedes the first
   book update, and ensure snapshots include it. Add mapping/mirror tests for
   ordering, changed-state publication, and snapshot recovery. Do not
   introduce a topic or client-side merge path.

7. Extend [ui/src/render/ladder/ladderState.ts](../../ui/src/render/ladder/ladderState.ts),
   [paintLadder.ts](../../ui/src/render/ladder/paintLadder.ts), and
   [LadderPanel.tsx](../../ui/src/chrome/panels/LadderPanel.tsx) to derive and
   paint the fixed-strip readout, bounded markers, and revised accessible text
   from the existing imperative BookStore subscription. Preserve the current
   scheduler/coalescing path and do not route DOM data through React state.
   Extend the existing ladder/panel tests for all four states, marker range
   handling, exact labels, and accessibility text.

8. Update durable documentation with the implementation:
   [engine/internal/md/README.md](../../engine/internal/md/README.md) for
   input, coverage, and state semantics;
   [engine/internal/feed/opend/README.md](../../engine/internal/feed/opend/README.md)
   for the source-neutral provider-status mapping;
   [engine/internal/uihub/README.md](../../engine/internal/uihub/README.md)
   for the nested md.book contract;
   [ui/src/render/ladder/README.md](../../ui/src/render/ladder/README.md) for
   display/accessibility behavior;
   [docs/external-apis.md](../../docs/external-apis.md) for the Moomoo and
   registry-source caveats; and [README.md](../../README.md) for the
   display-only limitation. The accepted term is recorded in
   [CONTEXT.md](../../CONTEXT.md) and the durable boundary in
   [ADR 0004](../adr/0004-estimated-luld-display-only.md).

## Validation

Run the focused tests while implementing, including:

- OpenD decode presence semantics for both source fields.
- Registry validation, unknown symbols, and expiration.
- Pure calculator tables for every bucket, closing multiplier, early close,
  registered leveraged multiplier, cent rounding, 1% threshold, 30-second
  cadence, quiet connected input, warm-up, missing previous close, provider
  status recovery, disconnect/reconnect, and RTH transitions.
- Core determinism with the fake clock: different tick batching produces the
  same state sequence, cache seed and live ticks share the same eligibility
  path, and Resynced cannot prematurely produce an estimate.
- Wire/mirror order and snapshot behavior, plus ladder state/painter/panel
  coverage for compact text, in-range and out-of-range markers, frozen state,
  and accessible text.

Then run the required Windows CI-equivalent checklist for this engine-and-UI
change:

~~~powershell
Set-Location engine
go build ./cmd/etape
go test ./...
go test -race -short ./...
go vet ./...
golangci-lint run
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
~~~

Finally, document at least 20 manual public-LULD spot checks across Tier 1,
Tier 2, sub-$3, open, close, and reopening examples. Use public NYSE halt
history and, where an entitled comparison is available, official SIP bands.
Record input coverage, estimated/official difference in ticks and dollars,
and the reason for every mismatch. This is a confidence check, not proof that
the result is official; do not relax the display-only language if the numbers
happen to match.

## Rollout and rollback

The feature rolls out with a normal engine/UI release and the checked-in
registry. There is no user setting, migration, entitlement, or provider
subscription. The first live session is intentionally conservative: supported
symbols warm for five minutes before any band appears.

An expired registry safely removes bands until a reviewed update ships. A
provider interruption safely freezes the most recent local value with its
warning. Rollback is a scoped code revert; clients that do not understand the
optional nested field continue to receive their normal book.

## Risks and future boundary

- Moomoo data can differ from consolidated eligible-print input. Openings,
  reopenings, trade corrections, and feed timing can make the local result
  diverge materially from the SIP calculation.
- Provider isSuspended and secStatus are useful health signals but do not
  identify why a symbol stopped trading. They must remain labelled as provider
  status, never as an LULD event.
- The registry is intentionally conservative. Missing or expired coverage
  reduces UI availability rather than creating a wrong Tier 2 default.
- A future licensed SIP integration may replace the calculator's input with
  official band dissemination, but that is a separate product and architecture
  decision. It must retain the explicit distinction until then.
