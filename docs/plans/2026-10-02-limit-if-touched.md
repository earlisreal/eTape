# Limit-if-Touched orders

Status: accepted and implemented. Earl accepted recommendations Q1–Q11 and
authorized implementation on 2026-10-02. This plan authorizes no live-order
activity.

## Goal

Add conventional Limit-if-Touched (LIT) orders for BUY, SELL, SHORT, and COVER
through Order Ticket, Action Templates, hotkeys, Deck Buttons, and Chart Order
Gestures. All eTape-created LIT orders use engine custody and the existing
stable-parent/linked-LIMIT-child lifecycle.

BUY/COVER activates at or below the trigger; SELL/SHORT activates at or above.
A qualifying price can pass beyond the trigger without an exact-price print.
Activation submits a limit order; it does not guarantee execution.

## Non-goals

Native LIT submission, trailing LIT, market-if-touched, OCO/brackets, new broker
adapters, Deferred Position Sizing for LIT, GTC/overnight custody, queueing for a
later session, automatic resume, buying-power reservation, and a second order
store or child row.

## Current-code evidence

- [Execution types](../../engine/internal/exec/types.go) own order intent and
  validation. Preserve STOP_LIMIT identity and behavior while adding LIT.
- [Core](../../engine/internal/exec/core.go) limits held admission to STOP_LIMIT;
  held replacement currently constructs TypeStopLimit rather than preserving
  the parent type. Both must dispatch by the actual order type.
- [Held routing](../../engine/internal/exec/held_route.go) owns session deadlines
  and stop comparisons. Its deferred-stop resolver already models current
  PRE/RTH/POST segment custody, but LIT has no deferred sizing.
- [Held lifecycle](../../engine/internal/exec/held_lifecycle.go) already provides
  Last-Eligible Print freshness, risk rechecks, linked LIMIT children, durable
  activation, pause/Resume, cancellation, expiry, and conservative recovery.
  Admission and trigger edits may activate from a fresh cached eligible print;
  Resume waits for a new eligible print.
- [Route payloads](../../engine/internal/uihub/wsmsg/payloads.go) currently take
  TIF/session/symbol/deferred sizing, with no order type. Go owns this contract;
  generated TypeScript must never be edited manually.
- [Template resolution](../../ui/src/chrome/exec/resolveTemplate.ts) and chart
  entry currently restrict trigger/cushion behavior to STOP_LIMIT. Existing
  [cushion calculation](../../ui/src/chrome/exec/priceSource.ts) supports dollar
  or percent distance and outward tick rounding; reuse it for LIT.
- [Chart markers](../../ui/src/render/chart/orderMarkers.ts) omit LIT; the
  Ladder currently treats only STOP as a trigger-price order.
- OpenD exposes native LIT in its bundled protocol and
  [official trading definitions](https://openapi.moomoo.com/moomoo-api-doc/en/trade/trade.html),
  with aux_price required by [placement documentation](https://openapi.moomoo.com/moomoo-api-doc/en/trade/place-order.html).
  eTape has no outbound LIT mapping, and moomoo inbound decoding currently
  defaults unknown types to MARKET. Native capability does not change the
  accepted engine-custody policy.
- [ADR 0005](../adr/0005-link-group-owned-execution-venue.md) fixes Execution Venue
  ownership to the submitting Link Group; [ADR 0006](../adr/0006-deferred-position-stop-sell-custody.md)
  governs existing deferred stop-sells. [ADR 0007](../adr/0007-limit-if-touched-engine-custody.md)
  records the accepted LIT custody trade-off.

## Accepted behavior

### Prices, sizing, and activation

The Order Ticket exposes separate Trigger Price and Limit Price inputs. Both
must be finite, positive, and tick-valid; either price relationship is allowed.
Do not reuse STOP_LIMIT's directional price-coherence restriction for LIT.
Reuse the existing stopPrice contract slot for the trigger, with LIT-specific
user-facing labels; avoid duplicating the trigger field.

Templates resolve their price source/offset as the trigger; chart gestures use
the clicked snapped chart price. Limit Cushion is nonnegative, in dollars or
percent, default zero: BUY/COVER adds it; SELL/SHORT subtracts it. Reuse outward
tick rounding ($0.01 at/above $1, $0.0001 below $1). Reject nonpositive results.
Cash/buying-power sizing uses the resulting limit price. All sizing is fixed at
submission, including position percentages; a SELL without available long
shares cannot create a deferred LIT exit. Recheck available shares and risk at
activation rather than silently resizing.

Use the execution lane's trusted Last-Eligible Price, never chart candles, bid/
ask touches, or Last Reported Price. Preserve existing real-time provenance,
sequence/gap handling, and the two-second receipt-age freshness ceiling.
Fresh already-satisfied prices activate immediately after durable admission or
an accepted trigger edit. Show “Will trigger now” before submission/release.
Without a fresh eligible mark, an admitted order remains Waiting. Resume
requires a new eligible print; there is no requirement for a new directional
crossing. UI preview is informational; Core revalidates against current data.

### Sessions and deadlines

LIT supports DAY only during the currently active PRE/RTH/POST segment.
AUTO becomes RTH in RTH and EXTENDED in PRE/POST. Explicit RTH is admitted only
in RTH; explicit EXTENDED is admitted in any of the three active segments.
Reject GTC, IOC, FOK, OVERNIGHT, CLOSED/OVERNIGHT placement, and queue-for-later.

| Current phase | Admitted effective session | Parent and child deadline |
| --- | --- | --- |
| PRE | EXTENDED | Scheduled RTH open |
| RTH | RTH or EXTENDED | Scheduled RTH close |
| POST | EXTENDED | Scheduled DataClose |

Core's authoritative calendar handles holidays and early closes. The preview
shows custody, effective session, and exact deadline. Submit carries sufficient
preview expectations to reject a session/deadline boundary race; ENGINE_HELD
alone cannot distinguish two adjacent held segments. An expired pretrigger
parent is terminal. An activating/working child is canceled at its inherited
deadline; an unconfirmed cancellation remains pending/unknown until broker
reconciliation. Broker fills settle deadline races authoritatively.

### Safety, custody acknowledgement, and recovery

Before activation there is no broker order or broker protection. Admit execution
feed demand before accepting the parent; reject capacity failure. Count the
parent once in open-order/risk exposure and run existing full gates both at
admission and activation, excluding the parent from duplicate trigger checks.
SHORT/COVER remain subject to existing broker/locate/account rules.

Reuse existing held lifecycle behavior: pause pretrigger orders on feed gaps,
owned-venue loss, Master Disarm, or restart; reconnect never resumes them.
Manual per-order Resume requires a valid deadline, healthy prerequisites, and a
new eligible print. No Resume All. Persist stable child identity before POST;
adopt found children after restart, and reconcile absent/ambiguous activation
without blindly submitting again. Terminal activation failures do not retry.

Preserve existing Disarm behavior for working broker children, Kill Switch,
venue deletion, graceful-shutdown warnings/cancellation, and crash recovery.
Order venue is immutable. Fills and cancel/replace confirmations remain
broker-authoritative; asynchronous launch acknowledgements are not success.

Live LIT requires its own one-time acknowledgement per actual venue/account
identity. Existing stop-limit acknowledgement does not enable LIT. Show
“Held by eTape — no broker order before activation” and identify the primary
OpenD trigger source. Credential/account identity changes invalidate it;
prevent disabling custody while nonterminal LIT intent remains. Share this
state across ticket, chart, hotkey, and Deck entry; paper/sim require no ack.

### Order display and editing

Retain one permanent LIT parent ID and one Orders/History row through child
fills and terminal state. Show trigger → limit, custody, phase, pending/unknown
actions, deadline, venue, and explicit LIT identity. Before activation, chart
and Ladder project the trigger; after durable activation, project the child
limit, with ACTIVATING until venue acceptance.

Reuse cyan trigger and yellow limit styling, with explicit LIT labels in chips
and previews. Pretrigger drag changes only the trigger; the fixed limit can
remain on either side. Reuse current phase/action restrictions: paused and
unknown/pending orders do not become editable or resume through drag. Working
child drag changes only its limit; activating children wait for broker state.
Reuse existing cancel priority, partial-fill rules, chooser, keyboard access,
pinned-chart exclusion, and Link Group venue/symbol filtering. Never add an
optimistic permanent marker before durable acceptance.

Extend exact Chart Order Gesture bindings to LIT using the same one-submission-
per-modifier-press behavior. Binding uniqueness is shared across STOP_LIMIT and
LIT; no new default binding overrides a trader's saved one. Preserve existing
import policy: first duplicate binding wins, later duplicates clear with a
warning. Existing templates, backups, and STOP_LIMIT behavior remain compatible.

## File-level implementation steps

1. **Domain and Core:** append LIMIT_IF_TOUCHED without renumbering existing
   enums in engine/internal/exec/types.go. Extend validation and type-aware
   trigger/routing in held_route.go, core.go, and held_lifecycle.go. Audit all
   held callers, replacement construction, event replay, gate classification,
   cancellation, expiry, and recovery in events.go/state.go/gate.go. Reuse held
   metadata and lifecycle; do not clone the scheduler or simulator trigger loop.
2. **Contracts and account enablement:** update Go wsmsg/wsmsg.go and
   wsmsg/payloads.go, uihub/map.go, commands.go, mirror.go, api.go, and existing
   execution acknowledgement/config plumbing. Extend the current route preview
   with type and segment/deadline expectations, retaining STOP_LIMIT defaults
   for old callers. Add separate LIT acknowledgement persistence; old event/
   config data defaults it off. Regenerate ui/src/gen/wsmsg.ts from Go.
3. **Templates and submission:** update ui/src/chrome/exec/actionTemplate.ts,
   resolveTemplate.ts, preChecks.ts, commands.ts, resolveChartConditionalOrder.ts,
   OrderSettingsSection.tsx, and chrome/backup.ts. Share cushion calculation and
   entry machinery, preserving STOP_LIMIT deferred sizing without enabling it
   for LIT. Extend labels/formatting in chrome/exec/orderStatus.ts and existing
   Account/History formatters. Rename stop-specific helpers only where needed
   to make actual shared semantics clear.
4. **Ticket/chart/Ladder:** extend chrome/panels/OrderTicketPanel.tsx and
   tv/ChartConditionalOrderEntry.tsx for type-aware trigger inputs, preview, disclosure,
   and shared submission. Extend render/chart/orderMarkers.ts,
   tv/ChartOrderMarkers.tsx, and render/ladder/ladderState.ts for LIT identity and
   phase-dependent prices. Keep high-frequency data out of React state.
5. **Adapter fidelity and compatibility:** new eTape orders reach all adapters
   only as LIMIT children. Preserve true LIT type/auxiliary price on inbound
   moomoo account snapshots instead of mislabelling native orders as MARKET;
   externally created native LIT remains native, never adopted as engine-held,
   and gets no inferred local activation state. Cover this read-only mapping
   without adding native submission or native trigger inference.
6. **Docs:** update engine/README.md, ui/README.md, relevant exec/uihub/market-
   data and panels guides, docs/external-apis.md, and CONTEXT.md for the final
   implemented behavior. Broaden Held Phase wording to include LIT once the
   lifecycle is implemented; preserve separate live acknowledgements. Commit
   the executed plan and custody ADR with the implementation.

## Tests and acceptance criteria

Extend existing subsystem suites rather than creating a parallel framework.

- Go table tests cover all four trigger directions, equality and beyond-trigger
  gaps; independent price validation; fixed sizing; no LIT deferred percentage;
  full gates and changed SELL availability at activation.
- Verify all admitted/rejected session/TIF combinations, AUTO resolution,
  holiday/early close deadlines, preview-to-submit boundary races, and child
  remainder cancellation versus authoritative fill/unknown cancel outcomes.
- Exercise fresh already-touched submit and trigger edit, stale/untrusted marks,
  Waiting and Resume, duplicate/out-of-order prints, feed gaps/overflow,
  subscription capacity/release, and shared demand with STOP_LIMIT.
- Reuse lifecycle tests for pause/Disarm/Kill/shutdown, each crash/POST boundary,
  stable child identity, adopt/absent/ambiguous recovery, no duplicate POST,
  pending/unknown replace/cancel, partial fills, and cancellation scopes.
- Verify separate live acknowledgement, identity invalidation, paper/sim bypass,
  old events/config, preserved enum values, and native moomoo inbound mapping.
- UI tests cover ticket and all template entry methods, fixed percentage sizing,
  limit-based cash sizing, cushion/ticks, shared gesture import uniqueness,
  no default binding changes, exact preview snapshot, deadline race feedback,
  live disclosure, phase/marker/Ladder prices, drag/cancel/chooser accessibility,
  one-row history, and unchanged STOP_LIMIT/deferred-stop behavior.
- Deterministic Core tests prove BUY dip and SELL rise each activate exactly one
  child LIMIT, and cover feed-gap recovery plus restart requiring manual Resume.
- Sim/paper E2E covers held-parent submission/cancel and activation into a child
  that can be canceled or filled. Its synthetic feed has no controlled-print
  injection, so directional crossings stay in deterministic Core tests.
- Component tests cover the exact chart-gesture route snapshot and chart custody
  disclosure. No live orders for validation.

After implementation run the full [Windows CI-equivalent checklist](../../README.md#ci-equivalent-validation-on-windows):
Go full tests, race-short tests, vet, pinned golangci-lint; generated-contract
check; npm ci, lint, tests, and build (includes typecheck). Also run the engine
build and UI E2E. Verify Go LF endings, clean diffs, generated drift, and no
runtime secrets. Report every result and every skipped required check/reason.
Fetch/integrate origin/main, commit only task files, merge/push local main per
AGENTS.md, then verify the task commit on local/remote main and hosted CI passes.

## Rollout, rollback, and risks

Validate sim/paper first across active session segments, early close, outages,
and restart. Live LIT stays disabled until its explicit identity-scoped ack.
Rollback stops new admissions while retaining LIT event replay, order visibility,
child reconciliation/cancellation, and account safety until all intents are
terminal; do not downgrade a binary that cannot replay outstanding LIT orders.

Main risks are reversed trigger direction, stop-specific guards leaking into
LIT, same-custody session races, stale prints, ambiguous broker POST/cancel,
misleading native-versus-local display, and configuration migration enabling
live custody. The tests and explicit preview/acknowledgement rules above are
release gates. Custody never reserves buying power or guarantees execution.

## Validation results

Implementation completed on 2026-10-03. The Windows CI-equivalent checks passed:
`go build ./cmd/etape`, `go test ./...`, `go test -race -short ./...`,
`go vet ./...`, `golangci-lint run` with v2.12.2, `mingw32-make -C engine
gen-ts-check`, `npm ci`, `npm run lint`, `npm test`, and `npm run build`
(including both TypeScript typechecks). `git diff --check` passed and tracked Go
files retain LF line endings. The final `chrome-regressions` run passed all 440
tests after the route-price preview change. `npm ci` reported five existing
audit advisories; the build reported the existing large-bundle warning.

The focused demo Playwright spec passed both tests, including pretrigger LIT
parent cancellation and child activation/cancel/fill. An earlier 31-test run
reported failures in non-LIT reconnect, Settings/import, OpenD-account, and smoke
scenarios. No live orders were used. E2E is not a required CI workflow job;
hosted CI on `main` must still pass before handoff.
