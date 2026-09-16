# Scanner rolling comparisons, repeat alerts and one-second polling

## Status and handoff

Draft implementation plan for **Codex Luna Max** in a separate session.
Earl approved the [product specification](../../.scratch/scanner-rolling-change/spec.md)
on 2026-09-16; specification commit: `ee28cbb2`. This plan is not yet approved.
Keep this plan uncommitted until Earl explicitly approves its final version.
No feature code was changed while preparing it.

Read the current AGENTS.md first. The explicit user Git instructions override
the older "no push by agents" line in this directory's README. Following final
plan approval and implementation, commit scoped changes and immediately push
to main as those instructions require. Do not infer live-order authorization.
Do not start another task automatically; Earl intends to open the new session.

## Goal and scope

Implement the approved Scanner behavior end to end:

- Previous close / rolling 1 minute / 5 minutes / 1 hour comparisons, shared
  by percentage filtering, display and gainers/losers ranking.
- One-second target polling across supported sessions, with shared snapshot
  pacing, bounded retries and slower refresh when capacity requires it.
- 100 candidates per ranking; startup discovery includes earlier sessions in
  the same close-to-close trading cycle, then refreshes current prices.
- Repeated threshold-crossing alerts for already-listed symbols, a 60-second
  cooldown, current filter eligibility, and explicit silent baseline rules.
- Full-window sampled-history warmup; five-second continuity threshold and
  immediate invalidation on confirmed disconnects.
- Outside-click/Escape dismissal of unapplied Scanner settings.

Non-goals: market-wide momentum scanning, historical backfill for rolling
values, custom durations, new subscriptions, per-panel filters, a new settings
framework, new chart aggregation, real orders or automatic restart of Earl's
running eTape. Keep raw sample histories and calculations out of React state.

The approved spec remains the product source of truth. Implementation details
below are concrete proposals for plan approval, not additional product promises.

## Evidence to retain

Read [verification.md](../../.scratch/scanner-rolling-change/verification.md),
[capacity results](../../.scratch/scanner-rolling-change/capacity-probe.json), and
[polling results](../../.scratch/scanner-rolling-change/polling-probe.json).

- Live 100-row rank requests succeeded in both directions for four sessions.
  Merged premarket snapshots returned 241 gainers / 216 losers in 161 / 108 ms.
- A subsequent fixed 243-symbol probe completed 61 one-second cycles without
  errors or overruns; median complete rank/snapshot cycle was 176 ms.
- Earl's eTape was running concurrently. Its traffic and UI latency were not
  instrumented; these are short premarket results, not an RTH stress guarantee.
- Many merged symbols lacked a usable premarket price. An inactive-session
  price must not be treated as current simply because the snapshot succeeded.
- Snapshot limits: 400 codes/request, 60 requests/30 seconds. Existing per-poll
  retry ceilings do not enforce the shared limit.
- Rank retention was observed through one overnight/premarket transition.
  Rank endpoints lack historical date selectors; dates cannot be invented.

## Current owners and reuse points

| Area | Existing owner / behavior |
| --- | --- |
| Polling, board, enrichment | `engine/internal/scan/scan.go`; sticky board, single premarket bootstrap flag, per-session arrival detection |
| Subscription pool | `engine/internal/scan/pool.go`; separate top-10 / cap-30 pool, not complete Scanner coverage |
| Cycle calendar | `engine/internal/session/calendar.go`: `TradingCycleStart`, `Schedule`, previous/next trading dates |
| Shared OpenD requests | `engine/internal/feed/opend/client.go`: `Client.Request`, existing injected clock |
| Snapshot retries | Scanner `snapshotBatch`, Watchlist `poller.go`, Stock Info `stockinfo.go` |
| Feed lifecycle | `opendfeed.go:stateLoop` consumes `Client.State`; UI hub receives `md.ConnUpdate` |
| Contract | `engine/internal/uihub/wsmsg/payloads.go`; generates `ui/src/gen/wsmsg.ts` |
| Filter persistence | `engine/internal/uihub/commands.go`, `engine/cmd/etape/main.go`; `scanner.filters.v2` |
| Defaults | `engine/internal/config/config.go`; existing 2000/3000 ms defaults |
| UI alert state | `ui/src/data/ScannerStore.ts`; ignores `scanner.hit`, derives alerts from new rank rows |
| UI settings and rows | `ui/src/chrome/panels/ScannerPanel.tsx`, `scannerFilter.ts` |
| Sort / Monitoring | `ui/src/chrome/scannerSync.ts`, `AppShell.tsx` |
| Sound | `ui/src/sound/useSoundWiring.ts`, `SoundEngine.ts`; reuse existing callbacks |
| Dismissal pattern | `ExportTradesPopover.tsx`, `tv/IndicatorPickerPopover.tsx` |

## Implementation order

### 1. Verify field provenance and establish the normalization boundary

Do this before relying on provider fields in rolling or previous-close math.
Use one small normalization helper in Scanner (a separate `observations.go`
is appropriate if it keeps the already-large poller readable).

1. Return a value that distinguishes usable current-session price, successful
   observation time, verified regular-close baseline and its cycle, and an
   unavailable/failed observation. Preserve nullable values in the row contract.
2. Use `session.TradingCycleStart(now)` for the baseline's close instant. Do not
   use ET midnight or `PoolDay`, whose 20:00 anchor represents a different cycle.
3. Confirm phase-specific price and close-field precedence with the installed
   protobufs, official provider definitions, and bounded read-only snapshots.
   RTH currently uses `LastClosePrice`; after-hours needs the just-completed
   regular close instead. Inspect regular-price and active-session change
   fields as evidence, not an unconditional reconstruction from rounded ratios.
4. Add recorded, minimal public-market fixtures for premarket, RTH, after-hours
   and overnight with known session/close dates. Reuse earlier probe evidence
   where sufficient. A date/time transition not currently observable can be
   covered by provider-documented fixtures plus a clearly recorded live gap.
5. Extended blocks have no independent timestamp. Write an explicit validity
   predicate supported by evidence; positive price alone or assigning today's
   date to an inactive rank is insufficient. If provenance cannot be established,
   return unavailable rather than substituting a historical session's price.
6. For an eligible unchanged current-session quote, use successful polling
   observation time; do not describe it as a fresh exchange trade timestamp.

Exit: pure normalization tests cover finite positive prices, zero/null/missing
fields, invalid baselines, stale blocks, corporate-action/date mismatches and
correct regular-close selection. If no reliable provider rule supports an
essential session, report that concrete limitation before claiming completion.

### 2. Pace shared requests and bound failure retries

Files: OpenD `client.go` and `client_test.go`; a small adjacent pacing helper
only if needed; Scanner `scan.go`, Watchlist `poller.go`, Stock Info `stockinfo.go`
and their existing tests.

1. Gate protocol 3203 in `Client.Request` before pending-request registration.
   Reuse the injected clock and context cancellation. Use one shared, smooth
   send-start gate, initially **550 ms minimum spacing**, with no burst credit.
   This is below 60/30s and leaves some margin. Waiting must not hold socket
   send locks or block order, keepalive, subscription or other protocols.
2. Preserve pacing across reconnects. Canceled waiters must not strand later
   callers. Pace actual send starts; delayed callers must not bunch together.
   Test with concurrent fake-clock callers, not wall-clock sleeps.
3. This gate covers this engine's shared Client, not independently running SDK
   clients or another engine process. Document that boundary honestly.
4. Split batches only for positively identified symbol-specific failures.
   Throttling, connection problems, permission-wide failures and unknown errors
   terminate that refresh and back off. Do not guess numeric error-code meaning
   or rely on one English error string without verified fixtures.
5. Reuse Scanner's eight-request per-refresh budget and apply a finite shared
   budget to recursive Watchlist/Stock Info attempts too. Count failed attempts.
   Never mark float data permanently bad because a global request failed.
6. Bound/review static-info splitting as well; do not invent its rate limit.
   Preserve existing StockFilter pacing for Most active; one-second scheduling
   must not bypass that endpoint's independent budget.
7. Start with simple capped backoff for transient global errors using existing
   patterns; no generalized retry framework or new dependency.

Exit: deterministic tests show spacing across concurrent snapshot callers,
cancellation, reconnect, non-snapshot protocols unaffected, no catch-up bursts,
global errors not split, finite bad-symbol isolation, and later recovery.

### 3. Extend the contract and preserve saved filters

Files: `wsmsg/payloads.go`, `scan.go`, `commands.go`, `main.go`, existing command
and restore tests, generated TS, and affected mock/synthetic payload producers.

Proposed minimal fields (adjust Go names to repository conventions):

- `ScannerFilters.changeBasis`: `previous_close | 1m | 5m | 1h`.
- Keep `ScannerRow.changePct` as the selected comparison's nullable percentage;
  avoid a competing percentage used by sorting elsewhere.
- `ScannerRow.changeStatus`: ready / warming / unavailable, only as needed to
  distinguish UI states. Preserve other enrichment fields independently.
- `ScannerRow.alertSeq`: per-symbol monotonically increasing process-local
  alert revision; zero means no emitted alert. This survives repeated rank
  publication and ordinary session switches. It is not a trade or arrival ID.
- `ScannerRankPayload.warmingCount`: number of tracked non-admitted candidates
  waiting for history; supports visible warmup when no rows qualify yet.

Keep `scanner.filters.v2` if an additive field is sufficient. Normalize a missing
basis to Previous close for legacy saved filters/commands before validation;
reject unknown nonempty values. Update Defaults, ValidateFilters, sameFilters,
v2/legacy restore and acknowledgement payloads consistently. Preserve other
saved thresholds and panel sorts. No runtime config/database data in Git.

Generate TS with `mingw32-make -C engine gen-ts`; never edit it manually.
Search all ScannerRow/ScannerFilters producers and consumers, including tests,
mock engine, synthetic feed and capture fixtures. Use optional/default handling
where justified; do not blindly rewrite unrelated fixture fields.

Exit: old saved configurations restore correctly, malformed new values fail
at the boundary, engine and UI agree on null/state/revision semantics, and
regeneration is deterministic.

### 4. Implement sampled history and the alert state machine

Prefer two focused Scanner files, `rolling.go` and `alerts.go`, each with
meaningful table-driven tests. Keep state under the poller's ownership; no
generic event framework or separate history service.

History:

1. Keep a bounded per-symbol time-ordered sample deque/ring covering one hour
   plus five seconds. Prune old entries without copying the full window each
   second. One fresh observation per scheduled refresh is enough.
2. Track current rank candidates, bootstrap candidates awaiting admission, and
   retained board rows independently of the small subscription pool. Keep
   bootstrap candidates through their cycle so a currently unavailable symbol
   can become eligible later. Retire other unadmitted candidates once they leave
   the current discovery set; a later return with a real gap must warm up again.
   This bounds active tracking by explicit discovery sets plus the sticky board,
   not every US symbol ever encountered.
3. Query the latest valid sample at or before `now - duration`, with at most
   five seconds of sampling offset. Never interpolate across a known gap or use
   a later sample to shorten the selected window. Compute `(now/base - 1)*100`.
4. A failed current refresh immediately suppresses alerts and yields unavailable
   current calculation. More than five seconds without a usable observation,
   or confirmed disconnect, starts a new continuity segment. Exactly five
   seconds does not trigger the strictly-greater boundary.
5. After a gap, rebuild the entire selected window; its first ready value is a
   silent recovery baseline. Healthy initial warmup remains a distinct origin
   and can produce one new-arrival alert. Track that distinction explicitly.
6. Preserve continuous history across session/board-cycle transitions. Filter
   changes reset qualification/baseline state but need not erase still-valid
   observations. Never turn a close-baseline rollover into a price-movement alert.

Alerts:

1. For gainers, crossing means previous valid percentage below T and new valid
   percentage at least T. For losers, above -T to at most -T. Stay-qualified
   updates do not alert. Store percentage qualification separately from other
   filters so a Relative Volume change alone cannot manufacture a price crossing.
2. Require all current enabled filters to pass at emission time. A rejected
   crossing updates threshold state but creates no delayed notification.
3. Enforce 60 seconds since last emitted alert for the symbol. Discard a crossing
   during cooldown and still update its threshold state; expiry alone emits
   nothing. Preserve last-alert time across filter changes, board/cycle resets,
   candidate eviction/re-entry and reconnects within the engine process. Keep
   cooldown state independently until its 60 seconds expire. Do not reset it
   when the user marks a row read. Process restarts establish a silent baseline.
4. Healthy first readiness/new admission can emit once when fully eligible;
   initial startup/filter baseline and gap recovery remain silent. Test these
   separately instead of representing every null -> number as a new alert.
5. Most active and zero percentage thresholds use new-arrival alerts only.
   Preserve the existing mode-specific filter behavior; do not introduce a
   percentage gate into Most active. Keep retained rows visible after failure.
6. Increment alertSeq only for an actual eligible emitted alert. Re-publication,
   enrichment completion, sort changes, reconnect replay and marking read must
   not increment it. Counter state must not collide after ordinary board resets.

Exit: tests exercise normal crossings, equality, signed losers, cooldown at
59.999/60 seconds, suppressed crossings, warmup vs recovery, missing values,
other filters, retained rows, mode exceptions and read-state independence.

### 5. Integrate cycle bootstrap, freshness and scheduling

Files: `scan.go`, its tests, config defaults/tests, `uihub/commands.go` and
`hub.go` only for a minimal existing-lifecycle connection; main wiring as needed.

1. Replace the single premarketBootstrapped flag with per-cycle completion for
   earlier-session sources. Discovery order is after-hours -> overnight ->
   premarket -> RTH. Bootstrap only stages preceding the current stage; poll
   the current stage continuously. At after-hours the new cycle starts with
   that source alone. At early-morning overnight startup, include prior
   after-hours; the same overnight endpoint is the current source.
2. Request 100 rows in each gainers/losers rank call. Most active continues its
   existing source strategy and endpoint limits; extended gainers/losers union
   may yield more than 100 total candidates. Deduplicate before static info,
   snapshots and sampling. Do not reinterpret 100 as a sticky-board cap.
3. Use cycle date and current-session normalization to guard bootstrap. Rank
   endpoints provide candidates, not proof of their session dates. For closed
   market periods, preserve read-only display without synthesizing fresh trades
   or gap-free rolling history. Do not redesign global session classification.
4. A failing prior-stage fetch must not block current discovery. Retry only the
   failed stage with bounded pacing/backoff. Ignore results that finish after
   the filter generation or trading cycle changed.
5. Refresh bootstrap/current candidates and retained rows with fresh snapshots,
   then normalize, update history, evaluate admission/alerts and publish. Never
   append cached prior-row values on failure or budget exhaustion. Preserve
   unrelated float, short-interest, SSR and REL VOL behavior.
6. Sort the refresh universe and rotate batch starting position when budget or
   time prevents a complete sweep. This avoids starvation from map iteration.
   Falling behind the five-second continuity rule must honestly show unavailable,
   not silently expand the allowed gap.
7. Replace base-ticker rounding with one serial deadline scheduler. Target
   one-second poll starts, schedule forward after slow work, and coalesce pokes.
   Prefer republishing completed enrichment without issuing another full rank
   and snapshot cycle; do not reevaluate price crossings from stale samples.
8. Set fresh-install premarket/RTH defaults to 1000 ms. Preserve explicit
   operator config values; do not silently edit Earl's runtime config. The
   rollout section specifies changing the existing 2000/3000 values once the
   implementation is approved for use.
9. Forward confirmed feed-down through the existing `md.ConnUpdate` handling
   in the hub to a minimal Scanner lifecycle method/mailbox. Do not add another
   reader to `Client.State()`, which would steal events from OpenDFeed. Ensure
   hub callbacks never perform provider calls or mutate poller state unsafely.
   Set a connection generation/invalidation flag synchronously on confirmed
   disconnect; a queued cleanup message alone is insufficient while a request
   is in flight. Reject observations and alert publication from the older
   generation even if that request subsequently returns a successful response.
   Perform full history cleanup on the poller owner thread.
10. A UI websocket reconnect only resets that UI's notification baseline; an
    actual market-feed disconnect invalidates engine continuity immediately.

Exit: fake-clock integration tests cover one-second cadence, old custom
three-second cadence without rounding to four, pokes, slow work, cancellation,
fair batching, all bootstrap stages, partial failure, filter races and lifecycle.
Include an alert followed by a filter/cycle reset and another crossing inside
60 seconds, plus disconnect during snapshot acquisition followed by an old
successful response; neither may bypass its cooldown/invalidation rule.
Calendar fixtures include Monday/Friday, holidays, DST and early-close sessions.

### 6. Update UI notification and settings behavior

Files: ScannerStore and tests; ScannerPanel and tests; scannerFilter and tests;
scannerSync and tests; AppShell tests; wire/mock fixtures if needed.

1. Replace new-row-based alert emission with alertSeq advancement on ordinary
   deltas. Snapshots and explicit baseline payloads seed revisions silently.
   Handle new rows with positive revisions after baseline as new alerts.
2. Keep revision tracking independent of per-session row containers and the
   unread set. Duplicate/enrichment rank payloads must not repeat sound. Remove
   the panel's midnight notification-reset timer as an alert authority; reading
   and highlighting must not rearm the backend crossing/cooldown state.
3. On a repeat revision, mark the row unread/highlighted and call the existing
   sound callback once. Never auto-select it or change its linked symbol.
   Keep existing sound preferences/coalescing. Avoid also consuming a duplicate
   scanner.hit event; rank revisions are the single UI notification authority.
4. Add the native comparison select and default normalization. Use labels such
   as `DAY %`, `1 MIN %`, `5 MIN %`, `1 HOUR %` with an accessible description of
   the exact baseline. Include the basis in the filter summary even at zero.
5. Keep changePct as the sorting accessor so Scanner Sync inherits the selected
   metric. Basis changes preserve explicit alternate sorts; default gainers/
   losers ordering remains based on the chosen percentage. Verify cross-window
   Monitoring behavior and null values sorting last.
6. Show a compact warming count for candidates not yet admitted; retained rows
   with unavailable history show a dash and explanatory state. Do not display
   a shorter-window percentage. No per-row high-frequency React timer/history.
7. Dismiss settings on outside click and Escape using the existing popover
   pattern. Exclude both gear and popover nodes from outside detection and use
   their ownerDocument for docked/popout windows. Clean up listeners on close.
   Escape returns focus to the gear; outside clicks keep their natural target.
   Reopening copies applied filters; dismissal sends no command and preserves
   unsaved state only until it is discarded. Retain explicit Apply and Reset.

Exit: store tests distinguish replay, new arrivals, repeat hits, first readiness,
gap recovery, unread transitions and duplicate payloads. Panel tests cover all
four choices, labels, global filter acknowledgements, warmup, keyboard/outside
dismissal, clicking inside/gear, popout ownership, and no unintended selection.

### 7. End-to-end checks, documentation and release

Update the Scanner subsystem README, engine/UI guides and external-API guide
where flow, endpoint budgeting, baselines, filter fields or operations change.
Update CONTEXT.md only for domain vocabulary, not implementation detail. Keep
the approved spec and dated probe evidence as history; do not rewrite measured
claims into guarantees. Add one focused simulated end-to-end Scanner flow to
existing UI integration/E2E coverage; do not add a parallel test framework.

## Validation checklist

During development, use existing fake clock/requester/publisher helpers. Focused
checks should run after the corresponding phases, not only at the end:

```powershell
# From engine
go test ./internal/feed/opend ./internal/scan ./internal/session ./internal/watchlist ./internal/stockinfo ./internal/uihub ./cmd/etape
# From ui
npx vitest run src/data/ScannerStore.test.ts src/chrome/panels/ScannerPanel.test.tsx src/chrome/scannerSync.test.ts
```

Before handoff, run the locally applicable CI-equivalent suite. Re-read
`.github/workflows/ci.yml` then; it is authoritative if these commands drift.

```powershell
Set-Location engine
go build ./cmd/etape
go test ./...
go test -race -short ./...
go vet ./...
golangci-lint version
# Use the installed binary only if it is v2.12.2; otherwise:
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run
Set-Location ..
mingw32-make -C engine gen-ts
# Inspect and stage the intentional generated output before gen-ts-check:
# its implementation compares the regenerated worktree against the Git index.
mingw32-make -C engine gen-ts-check
Set-Location ui
npm ci
npm run lint
npm test
npm run build
npm run e2e
Set-Location ..
git diff --check
git ls-files --eol '*.go'
```

Use the documented MSYS2 UCRT64 setup for Windows race checks. UI build already
includes typecheck. E2E is additional targeted validation for the settings and
alert flow, not a current hosted-CI step. Confirm new tests are discovered by
the existing Vitest projects. Keep Go/generated files LF. Review staged and
unstaged changes; no runtime data, credentials or account data in the commit.

Use simulator/fake provider data for thresholds and disconnect/retry tests.
Do not induce throttling or disconnect Earl's live OpenD to prove error handling.
Optional bounded live validation may rerun the committed read-only probes,
accounting for other running clients; log actual phase, batch counts, timing,
missing data and failures. A full one-hour ready state can be tested with the
fake clock; no one-hour real wait is required. Record any unverified live phase.

## Rollout, rollback and completion

1. Keep Earl's running engine unchanged during implementation. Validate a
   separate demo/test instance and matching engine/UI builds first. Do not mix
   old UI alert semantics with the new engine as a supported deployment.
2. On an explicitly authorized rollout, back up the relevant local config,
   change existing `scan.premarket_ms` and `scan.rth_ms` to 1000 while preserving
   unrelated values, and restart only when Earl authorizes disrupting his
   running app. New defaults alone do not migrate his saved 2000/3000 settings.
3. Start with Previous close; verify silent startup, earlier-session candidates,
   current price availability and normal repeat alerts. Then verify rolling
   warmup, one-second targeting, and honest slower/unavailable states under load.
4. For data/provider limitations, use the specified unavailable state and report
   affected scope. Do not silently switch baseline/window, remove freshness
   guards or revert to once-per-session alerts to make tests pass.
5. If rollout regresses, restore the prior matching engine/UI build and backed-up
   cadence config. Preserve existing filter values; do not delete the runtime
   database or broadly reset user settings. Revert source with a scoped commit,
   not a destructive reset of unrelated work.
6. Final handoff lists changed behavior, test results, skipped required checks
   with reasons, live-test limits and remaining operational rollout steps.
   After completing the approved plan, commit scoped implementation/docs and
   push immediately to main under current AGENTS.md; confirm hosted CI. Do not
   bypass hooks unless the task explicitly authorizes their escape mechanism.

## Suggested new-session prompt

> I approve the implementation plan at
> docs/plans/2026-09-16-scanner-rolling-alerts.md. Implement it with Codex Luna
> Max, following the approved spec it links. Read current AGENTS.md first.
> Preserve unrelated work and my running eTape/OpenD. Complete the required
> validation and documentation, then follow the repository's commit/push rules.
> Do not place, modify or cancel orders.
