# Chart Risk Entry error toasts

Status: approved — Earl confirmed shared understanding on 2026-10-16;
implemented.

Design: [specification](../../.scratch/chart-risk-entry-error-toasts/spec.md).

## Goal

Show a reason-specific toast when Chart Risk Entry cannot submit, including a
closed session, and when an accepted entry later fails at activation. Distinguish
definite failure from an unknown broker outcome. Emit one toast for a new failure
or unknown outcome, rather than for each repaint or repeated order update.

## Current-code evidence

- [ChartRiskEntry](../../ui/src/chrome/panels/tv/ChartRiskEntry.tsx) routes manual
  Enter and Auto-send completion through `submit()`. Its query, validation,
  rejection, and unknown branches update only inline status. Definite failures
  retain the draft; draft-identity checks currently suppress late replies.
- [Risk admission](../../engine/internal/exec/risk_entry.go) and the
  [route query](../../engine/internal/uihub/query.go) already reject outside
  PRE/RTH/POST. Session authority stays in the engine.
- [ToastProvider](../../ui/src/chrome/Toast.tsx) supplies danger/warn levels,
  four-second dismissal, click dismissal, and an accessible alert role.
- [AppShell](../../ui/src/chrome/AppShell.tsx) already subscribes to order
  rejection in each workspace, currently for deferred-position stop-sells only.
- [ExecStore](../../ui/src/data/ExecStore.ts) emits rejection callbacks only on a
  delta transition into REJECTED, suppressing snapshots and repeated deltas.
- [Held activation](../../engine/internal/exec/held_lifecycle.go) submits with
  the entry parent's client ID. A definite rejection updates that parent;
  uncertain submission instead sets held phase UNKNOWN with an activation-outcome
  reason. Existing order payloads expose both outcomes.

## Design decisions

- Auto-send ON: completing SELL or a completed price correction attempts
  submission and reports failure immediately. Auto-send OFF: Enter does so.
- Show one error per failed attempt, including a deliberate retry with the same
  reason. Preview-only queries and background validation remain silent.
- Retain draft prices and inline error after rejection. Unknown outcomes keep
  the existing resend prohibition and pause/reconciliation behavior.
- Capture symbol and Execution Venue for each attempt. Report a failed late
  reply even after the draft is dismissed or chart context changes, while
  retaining identity guards around draft mutation and further command dispatch.
- Initial attempt feedback stays in the submitting window. Later entry rejection
  and activation-unknown warnings appear once per open workspace window.
- Reuse the standard toast presentation and actual engine/broker reason. A later
  confirmed rejection may follow an earlier unknown warning; duplicate delivery
  of the same outcome stays silent. Initial/reconnect snapshots never replay
  historical alerts.

## Implementation

1. In `ChartRiskEntry.tsx`, consume the existing toast API. Capture attempt
   context before awaiting the route query. Report route-query failure, local
   blockers, engine rejection, and ambiguous/thrown submission results at their
   existing branches. Keep UI updates conditional on the original draft still
   being current, and never issue a new order after its draft was canceled while
   awaiting preflight. Do not produce toasts from `paint()` or start retries.
2. In `ExecStore.ts`, add a narrow listener for new held activation-unknown
   outcomes, following the existing rejection-listener lifecycle and ingestion
   error isolation. Match a working risk entry parent whose held phase is UNKNOWN
   and whose paused reason starts with `activation outcome unknown:`. Notify only
   when a live delta enters that condition. The working-status guard excludes
   terminal rows that retain old held metadata; the reason excludes uncertainty
   from cancellation or recovery. Suppress snapshot history and repeated deltas.
   Reuse existing payload fields; do not change the generated contract or add a
   generic notification framework. Add one `shortcut:` comment explaining the
   dependency on engine reason text and when to replace it with a typed outcome.
3. In `AppShell.tsx`, extend the existing rejection callback with an entry-parent
   branch (`riskEntry`, rather than the protective stop's `riskEntryId`) and
   subscribe to activation-unknown warnings. Include symbol, Execution Venue, and
   available reason. Preserve existing deferred stop-sell notifications.
4. Extend existing component/store/shell tests. Add ToastProvider to the
   `ChartRiskEntry.test.tsx` and `ChartPanel.test.tsx` harnesses that mount the
   component. Update [UI behavior](../../ui/README.md) for the new feedback and
   window scope; record implementation in the plans index when executed.

No new dependencies, settings, engine behavior, or WebSocket fields are needed.
Do not change entry sizing, allowed sessions, trade freshness, protective-stop
management, amendments, cancellation, or broker routing.

## Validation

- Component tests: outside-session failures through manual Enter and both
  Auto-send placement gestures; local blocker/query/ack failures; reason fallback;
  unknown warning and resend guard; retained prices and successful retry; repeated
  attempts versus repaint silence; late replies retain original context without
  mutating a replacement draft; success and market-data waiting remain silent.
- Store/shell tests: later gate/broker rejection; activation UNKNOWN; one alert
  for repeated pair/order deltas; snapshot/reconnect silence; parent-only routing;
  unrelated cancellation/recovery UNKNOWN and protective-stop updates excluded;
  terminal rows retaining UNKNOWN metadata excluded; UNKNOWN followed by a new
  definite rejection gets its distinct outcome message;
  each mounted workspace receives one alert; subscription cleanup and existing
  deferred stop-sell behavior remain correct.
- Run focused chart, execution-store, and AppShell suites, UI typecheck, lint,
  and build. Validate Markdown links and run `git diff --check`. Use the existing
  simulated fixtures; do not place live orders.
- Follow repository Git rules: commit the implemented plan, spec, code, tests,
  and relevant guides together; integrate and push main; wait for successful full
  hosted CI on that main commit before code handoff. No Go or generated-contract
  changes are planned, so Go race/gen-ts checks are not locally applicable unless
  implementation scope changes; hosted CI still runs its full suite.

## Rollout, rollback, and risks

Ship with the existing UI; no migration or engine change is required. Revert the
scoped UI change if notification count or routing regresses.

Toasts dismiss after four seconds; persistent draft/order status remains the
source for follow-up. Windows opened after an outcome do not replay it. An
unknown broker outcome can still represent a real order: the warning must not
imply rejection, safe resubmission, or automatic recovery.
Activation-unknown classification uses the engine's existing reason prefix;
changes to that text must update the UI discriminator and its regression cases.
