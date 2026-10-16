# Chart Risk Entry error toasts

Status: approved — Earl confirmed shared understanding on 2026-10-16;
implementation pending.

Implementation: [plan](../../docs/plans/2026-10-16-chart-risk-entry-error-toasts.md).

## Confirmed behavior

- Show a reason-specific toast for every failed Chart Risk Entry submission,
  including unsupported market sessions, route-query failures, local admission
  blockers, engine rejection, and later entry activation/broker rejection.
- Follow the existing Auto-send setting. When enabled, completing the SELL point
  or a completed draft price correction attempts submission and shows a toast on
  failure. When disabled, Enter attempts submission. Merely selecting prices,
  repainting, and background revalidation do not produce failure toasts.
- Emit one error toast per failed send attempt. A deliberate retry may show the
  same reason again. Preserve completed draft prices and the inline reason after
  a definite rejection so the trader can correct the draft or retry.
- An uncertain submission shows a distinct status-unknown warning and preserves
  the existing prohibition on resending. Never claim that an uncertain order was
  rejected or add an automatic retry.
- A failed attempt still reports its result after the originating draft was
  dismissed or its chart context changed. Identify the submitted symbol and
  Execution Venue, and never mutate a replacement draft.
- Include later entry activation/broker rejection after conditional-pair
  acceptance. Include later activation whose broker outcome is unknown, using a
  warning rather than a rejection message.
- Initial submission feedback appears in the submitting workspace window.
  Later rejection or activation-unknown feedback appears once in each open
  workspace window, even without an open chart for that symbol. Use the entry
  parent to avoid duplicating linked-order reports.
- Notify on new outcomes only. Repeated order deltas, chart repaints, and initial
  or reconnect snapshots must not replay old failures. A later confirmed rejection
  can produce an error toast after an earlier unknown warning; they communicate
  different outcomes.

## Presentation

Reuse standard toasts: danger for definite failure, warn for unknown outcome,
four-second automatic dismissal, and click dismissal. Preserve the existing
inline draft reason and order status after the toast disappears.

Include the submitted symbol, Execution Venue, Chart Risk Entry context, and the
actual available reason. For example:

- `XYZ (paper) Chart Risk Entry not sent: risk entry is unavailable outside PRE/RTH/POST`
- `XYZ (paper) Chart Risk Entry rejected: <engine or broker reason>`
- `XYZ (paper) Chart Risk Entry status unknown. Verify Orders; do not resubmit.`

Use a clear fallback when no reason is supplied. An unavailable route query means
the order was not sent; a thrown or ambiguous order command means its outcome is
unknown. Successful admission and ordinary waiting for market data stay silent.

## Existing boundaries

Retain the current PRE/RTH/POST admission gates, account and funding checks,
custody, quantity caps, cancellation, and unknown-outcome guards. Missing or stale
Last-Eligible Prints continue to permit an accepted pair to wait for market data.
Use the existing toast component and its accessibility/dismissal behavior.

This request covers entry submission and entry activation. It does not request
changes to protective-stop management, amendments, cancellation, or trading hours.
No live orders are part of planning or validation.

## Comments

- Q1: Earl chose every failed Chart Risk Entry submission, rather than only
  unsupported trading hours.
- Q2: Earl chose a distinct warning for unknown outcomes.
- Q3: Earl requested one toast per failed send and automatic rejection feedback
  after placing SELL.
- Q4: Earl confirmed the existing Auto-send setting controls completion timing.
- Q5: Earl included later entry activation/broker rejection.
- Q6: Earl included late failure replies after context changes, with submitted
  symbol and Execution Venue in the message.
- Q7: Earl chose each open workspace window for later rejection feedback.
- Q8: Earl included later activation whose broker outcome is unknown, with the
  existing pause/reconciliation behavior retained.
- Q9: Earl confirmed the plan and shared understanding. Approval covers the
  planning documents; implementation remains a separate request.
