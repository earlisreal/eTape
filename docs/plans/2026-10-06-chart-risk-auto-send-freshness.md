# Chart Risk Entry auto-send freshness

Status: implemented — Earl requested implementation on 2026-10-06.
Local checks: [validation record](../../.scratch/chart-risk-auto-send-freshness/validation.md).

## Goal and non-goals

Make initial Chart Risk Entry auto-send refresh and validate market data when
placement completes, just as Enter does. A setup taking longer than two seconds
must send once when the refreshed data and all other gates allow it. Genuine
unavailable or stale data must still block with a visible reason.

Keep the existing two-second freshness limit, engine admission checks, reviewed
quantity cap, default-off auto-send setting, and initial-placement-only behavior.
No engine or generated-contract changes, new dependencies, polling, automatic
retries, live orders, or changes to risk sizing, custody, and submitted-order edits.
Continuous freshness of the advisory preview while choosing prices is outside
this fix; refresh at submission resolves the reported false block.

## Pre-fix code evidence

- [ChartRiskEntry.tsx](../../ui/src/chrome/panels/tv/ChartRiskEntry.tsx):
  `initiate()` queries `QueryStopLimitRoute` once and saves the preview in
  `draft.route`. `invalid()` rejects a missing/untrusted eligible print or one
  whose timestamp is over 2,000 ms old. The one-second paint timer does not
  refresh that preview.
- The placement `up()` handler calls `submit()` only when auto-send is enabled
  **and the cached preview passes `invalid()`**. An expired initial preview
  therefore stops auto-send before it can refresh market data.
- Enter calls `submit()` directly. That function sets `busy`, fetches a new
  preview, checks draft identity, and then validates and submits. This is the
  existing path to reuse.
- The refresh catch currently sets an error and then continues validation. If
  an earlier preview remains valid, validation can permit a send despite a
  failed refresh. The shared submission path must return on refresh failure.
- [ChartRiskEntry.test.tsx](../../ui/src/chrome/panels/tv/ChartRiskEntry.test.tsx)
  already exercises placement, Enter, auto-send, cancellation, and uncertain
  outcomes. Its successful auto-send test completes while the initial preview
  is fresh, so it does not catch the reported timing bug.
- Diagnosis reproduced the exact sequence using the committed component in an
  in-memory test: advance the clock by 2,001 ms after the initial preview,
  finish placement, observe the freshness warning and no auto-send, then press
  Enter and observe a new query followed by one accepted submission. Focused
  engine checks also confirmed that a stale print is rejected and a new eligible
  print restores admission. No production code was changed for diagnosis.

## Design decisions

1. Both Enter and initial auto-send use the existing `submit()` function. Remove
   the cached `invalid()` prerequisite from the auto-send call site; retain
   validation after a successful refresh inside `submit()`.
2. A refresh failure ends that submission attempt: restore the current draft's
   non-busy state, display `Engine preview unavailable.`, and return without
   issuing `SubmitRiskEntry`. Keep the draft-identity check so cancellation or
   context changes during the query cannot revive an obsolete setup.
3. Clear an earlier preview error after a successful refresh of the current
   draft. A stale saved error must not remain beside the current result.
4. Retain synchronous `busy` protection, the `unknown` guard, all current
   account/venue/position/focus checks, and
   `Math.min(current.maxQty, s.qty)`. Auto-send performs one attempt at initial
   completion; a failed attempt remains available for an explicit Enter retry.

## File-level steps

1. Extend the existing component test fixture with a controlled clock and
   independently configured initial/submission previews. Add the delayed
   auto-send regression first and confirm it fails on the current code.
2. In `ChartRiskEntry.tsx`, make the small call-site change above and make the
   shared preview refresh fail closed. Reuse the existing imperative rendering,
   draft identity, and submission state; introduce no new controller or API.
3. Complete the focused safety checks below in `ChartRiskEntry.test.tsx`, then
   run the existing component suite to verify preserved behavior.
4. Update [the chart integration guide](../../ui/src/chrome/panels/tv/README.md)
   and [the UI guide](../../ui/README.md) to state that Enter and initial
   auto-send recheck market data before submitting and preserve the draft when
   that check fails.

The separate [measure-preview change](2026-10-06-chart-risk-measure-preview.md)
was integrated in `fa302a5e` before this implementation. Preserve it and keep this
fix scoped to submission timing, tests, and relevant documentation.

## Validation and acceptance

- **Reported regression:** for drag/release and click/click placement, expire
  the initial preview by 2,001 ms while the account stays fresh. A fresh
  completion-time query produces exactly one `SubmitRiskEntry`, preserves the
  chosen prices/quantity cap, and clears the draft without Enter.
- **Unavailable current data:** a refreshed preview with no trusted eligible
  print, or a print still over two seconds old, shows the freshness blocker and
  issues no submission. New data alone must not silently retry the setup.
- **Refresh failure:** reject the completion-time query while the previous
  preview is still fresh. Show the query error, restore an editable draft, and
  issue no submission. An explicit Enter retry can succeed after a fresh query.
- **In-flight ownership:** delay the refresh and press Enter; at most one
  submission is possible. Cancellation, focus loss, or context changes before
  the refresh resolves leave no submission from the discarded draft.
- **Preserved gates:** keep existing coverage for manual Enter, auto-send off,
  endpoint edits without auto-send, invalid sizing, quantity caps, cancellation,
  and non-retryable unknown outcomes. Verify locked/disconnected execution and
  stale account data still block after refresh.

Run the focused component check from `ui`:

```powershell
npx vitest run --project chart-panel src/chrome/panels/tv/ChartRiskEntry.test.tsx
```

After implementation of the approved plan, run the full
[Windows CI-equivalent checklist](../../README.md#ci-equivalent-validation-on-windows),
using [the workflow](../../.github/workflows/ci.yml) as the source of truth.
Exercise a delayed setup with simulated data only. The component tests cover the
pointer-to-command timing; add no separate browser harness for this logic fix.
Record all results and any skipped required check with its reason.

Commit the executed plan and only the fix's code/tests/docs, fetch and integrate
`origin/main`, merge into local `main`, push, and verify that local/remote main
contain the task commit and hosted CI passes, as required by
[AGENTS.md](../../AGENTS.md#git).

## Rollout, rollback, and risks

The fix uses the existing setting and wire commands; no configuration or data
migration is needed. Roll back by reverting the focused fix commit.

The main risk is treating removal of the early cached gate as permission to
submit without successful revalidation. Requiring a successful current query,
retaining all post-query/engine checks, and testing failed refreshes addresses
that risk. Asynchronous cancellation and duplicate inputs remain covered by
the existing draft-identity and busy/unknown guards.
