# Implement price alerts with sound

Status: ready-for-agent
Implementation: complete; validation and review in progress
Spec: [Approved price alerts](../spec.md)
Review baseline: `f319de945cb99284929b9765eb4b71f7783cebd0`

## Agreed test seams

Earl confirmed these boundaries on 2026-10-19 before tests were written:

1. Engine alert commands, eligible-trade input, durable persistence, and history.
2. WebSocket commands/snapshots, availability, and engine-wide notification claims.
3. Chart/editor/panel actions and sound controls through public UI interfaces.

Use vertical red/green slices, with focused tests and regular UI typechecking.

## Implementation sequence

- Durable alert definitions and independent trigger records; one-shot lifecycle,
  session/freshness/gap handling, and explicit pause/rearm.
- Background ticker demand and visible capacity/readiness states, preserving
  chart/execution priority; application wiring independent of open windows.
- Go-owned WebSocket DTOs and generated TypeScript; snapshot/live trigger topics
  and engine-instance notification claims without historical replay.
- Imperative alert store, dockable Alerts / History panel, shared editor and
  safe explicit chart navigation.
- Chart bell/menu creation, dashed levels, grouped tags, drag preview and Save.
- Beacon sound/preferences, counted nonblocking popup, and burst delivery.
- Relevant README updates, focused checks, full local suite once, two-axis
  code review, fixes, merge/push, and successful full hosted CI on main.

## Validation

Focused engine lifecycle, persistence, history, storage-recovery, capacity,
WebSocket, and notification-claim tests pass. Focused race tests pass for the
changed engine seams. Focused UI editor, panel/history, drag/save, notification,
sound, chart/order-marker, and AppShell tests pass. UI typecheck, lint, and build
pass; scoped Go lint passes. Demo verification confirms saved monitoring and a
fresh-trade trigger retained in History. Full local validation and hosted CI
remain pending.

## Review against f319de94

### Standards

No hard documented-standard violations. One judgment call: duplicated chart
price snapping/display logic could drift. Resolved by sharing alert price helpers
based on the existing `quoteDecimals` convention.

### Spec

Two P2 findings, both resolved and verified by the Spec reviewer:

- Chart bell/menu/drag creation rounded dollar targets to two decimals while
  the application uses three. A failing bell-creation regression now passes;
  shared snapping/display retains existing dollar and sub-dollar precision.
- Alert graphics could cover execution graphics at the same price. A failing
  combined overlay test now passes; passive alerts insert below execution
  graphics and stay there on hover/focus.

Standards: 0 hard violations, 1 optional smell resolved. Spec: 2 findings resolved;
no remaining finding in the follow-up review.

Full validation additionally caught a stale sound-default fixture (updated for
the new enabled flag) and an unintended shortening of ordinary subscription
hysteresis (fixed; the existing hysteresis test and alert-reclamation tests pass).

## Comments

### 2026-10-19

Implementation authorized through the implement skill. Test seams and fixed
review baseline confirmed separately. Preserve unrelated untracked files.
