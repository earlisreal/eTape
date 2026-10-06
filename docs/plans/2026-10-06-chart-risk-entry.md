# Chart Risk Entry implementation

Approved design: [spec](../../.scratch/chart-risk-entry/spec.md).

1. Extend the execution domain/event fold with durable pair metadata and atomic local pair admission/editing; reuse held trigger/child identity, reservations, venue adapter replacement and recovery. Add command-boundary tests for risk sizing, partial/late fills, cancellation, disarm, expiry and recovery in vertical slices.
2. Expose typed pair submission and metadata through Go-owned wsmsg; regenerate TypeScript. Preserve existing independent deferred stops and segment deadlines.
3. Extend Action Templates/settings with risk presets and default-off auto-send. Initiate only the active grouped chart in the focused window; acquire account demand for chart-only sizing. Test settings persistence and hotkey routing.
4. Implement imperative two-point preview using existing chart facade/tick/cushion helpers. Reuse chart order markers for pair-aware dragging, live risk feedback, cancellation and pending/unknown display. Test actual pointer/keyboard-to-wire behavior.
5. Update relevant guides; run full Windows CI-equivalent validation plus proportionate E2E, review standards/spec against the confirmed baseline, fix findings, commit scoped changes, integrate origin/main, merge/push main and verify hosted CI.

No new dependencies. No live orders. Existing feed/restart custody pauses and venue cancel/replace uncertainty remain explicit.
