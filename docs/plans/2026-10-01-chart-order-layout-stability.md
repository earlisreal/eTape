# Chart order layout and cursor-price stability

Status: implemented; local validation passed; hosted CI pending.

## Goal

Fix the reported Shift Chart Order Gesture price mismatch, chart movement when
the first order marker appears or the last disappears, and clipped time-axis
labels. Order overlays must preserve the chart's position and zoom. Ordinary
market-driven autoscaling and the existing Live View, Future Buffer, and
Historical View behavior continue normally.

## Evidence and reproduction

Earl supplied a 12.77-second recording and a screenshot on 2026-10-01. Before
submission, the recording shows cancellation announcement text at the top of
the chart and clipped time labels. Around 3 seconds, the preview reports a stop
near 4.94 at a cursor whose chart crosshair price is near 5.00. Around 7 seconds,
the first confirmed marker appears, the candles move upward, and the time axis
becomes visible. The recording does not show cancellation of that order; the
reported downward movement and subsequent clipping are supported by Earl's
description and the separate screenshot.

Source evidence:

- [ChartOrderMarkers](../../ui/src/chrome/panels/tv/ChartOrderMarkers.tsx)
  returns an unstyled, in-flow announcement span when there are no groups
  (line 242). The nonempty branch places its announcement outside normal flow
  and clips it visually (line 300). Cancel writes announcement text (line 219),
  and terminal orders are removed by the
  [marker projection](../../ui/src/render/chart/orderMarkers.ts).
- [ChartPanel](../../ui/src/chrome/panels/ChartPanel.tsx) creates Lightweight
  Charts inside the same host used by the React overlays (lines 272 and
  1108–1132). Its ResizeObserver sizes the chart from the host dimensions;
  [PanelFrame](../../ui/src/chrome/PanelFrame.tsx) clips panel-body overflow.
- [ChartStopLimitEntry](../../ui/src/chrome/panels/tv/ChartStopLimitEntry.tsx)
  converts pointer coordinates relative to the host, then projects the stop
  through the price series. Marker dragging, chart drawings, and context-menu
  price lookup share the assumption that the native pane starts at the host
  origin.
- Order markers and entry previews are DOM overlays. They do not request chart
  autoscale or viewport changes. The
  [implemented order plan](2026-09-30-chart-order-markers-and-engine-held-stop-limit.md)
  already requires markers not to affect autoscale.

The deterministic browser reproduction confirmed the in-flow announcement
displaces the native chart inside its fixed-size host. It uses the real
ChartOrderMarkers, ChartStopLimitEntry, Lightweight Charts, and stores, with
fixed bars and simulated command acknowledgements; it opens no engine socket.

| State | Native offset from host | Time-axis clipping | Preview stop / actual cursor price |
| --- | ---: | ---: | --- |
| Initial / first marker | 0 px | 0 px | 5.25 / 5.25 |
| Last marker canceled / next preview | 16 px | 16 px | 5.25 / 5.30 |
| Second order submitted | 0 px | 0 px | Submitted stop 5.25 |
| Second cancellation | 16 px | 16 px | 5.25 / 5.30 |
| Same state, announcement changed to absolute positioning | 0 px | 0 px | 5.25 / 5.25 |

The host stayed at y=126–660. Cancellation moved native chart bounds to
y=142–676 and the axis bottom to y=676. The price selected by the preview
would project on the displaced pane at y=296.72 for a pointer at y=280.
The visible logical range (0–79) and pane-local candle coordinate (315.26)
never changed, ruling out an autoscale change in this reproduction. A
one-variable browser-only positioning probe restored alignment and axis
containment without changing source files.

Runnable diagnostic command (temporary local artifact outside the repository):

```powershell
node 'C:/Users/ching/.codex/visualizations/2026/10/01/01a0f703-d807-79f3-b673-5f46a16e35e3/stop-limit-repro/run.mjs'
```

It intentionally exits 1 against current source. The native-origin drift,
time-axis containment, and cursor-price assertions all fail; unchanged scale
and range assertions and the absolute-positioning diagnostic probe pass.
The adjacent `measurements.json` and PNGs preserve the failing state and probe.
This isolates the source defect; the final regression must also reach the
production ChartPanel and panel clipping boundary.

## Design decisions

- Confirmed by Earl: preview, submit, replace, and cancel preserve the current
  chart position and zoom; order overlays do not expand the price scale.
- Confirmed by Earl: keep the snapped cursor price as the STOP trigger,
  and calculate LIMIT using the existing Action Template Limit Cushion.
  This preserves the current documented contract.
- Keep accessibility announcements mounted and available to assistive
  technology in both empty and nonempty states, without adding layout height
  or intercepting pointer input.
- Fix the shared layout cause. Do not compensate with a hard-coded pixel
  offset, freeze price autoscaling, reset the viewport, or enlarge the chart
  to hide clipping.

## Implementation

- `ChartOrderMarkers` now keeps its existing absolute overlay and clipped
  announcement mounted when no working orders remain. No order-state change
  alters chart host layout.
- Added an empty-state live-region component check and a reusable browser
  scenario with actual Lightweight Charts, ChartOrderMarkers, and
  ChartStopLimitEntry. Fixed bars and simulated orders drive two submit/cancel
  cycles without an engine or broker connection.
- Documented the stable chart origin in the [panel guide](../../ui/src/chrome/panels/README.md)
  and the new test command in the [E2E guide](../../ui/e2e/README.md).

## Acceptance checks

- With fixed market data, native chart top, pane dimensions, candle position,
  visible logical range, and price mapping remain unchanged across preview,
  first-marker creation, pending cancellation, and last-marker removal.
- The whole time-axis row remains inside the chart host and panel body,
  including in a narrow panel matching the supplied capture.
- STOP is the valid-tick rounding of the price actually under the pointer.
  Its projected line may differ from the pointer only by the screen distance
  caused by tick rounding, plus at most one CSS pixel of rendering rounding.
  The submitted STOP matches the last displayed preview; LIMIT retains the
  Action Template's directional dollar/percent Limit Cushion.
- A second gesture after cancellation remains correctly aligned. Repeat the
  cycle to catch stale announcement state.
- LIMIT and STOP_LIMIT overlays, multiple-order → single-order → empty
  transitions, manual price zoom, and panel resizing retain alignment. Test
  pending/rejected/unknown cancel feedback without premature marker removal.
- Cancellation announcements remain accessible and visually hidden whether or
  not an order marker remains. No hidden element consumes chart layout space
  or steals pointer input.
- Ordinary live data still updates/autoscales the chart through its existing
  imperative controller; this fix introduces no high-frequency React state.

## Validation and delivery

Planning baseline: the existing `ChartOrderMarkers.test.tsx` and
`ChartStopLimitEntry.test.tsx` passed together: 2 files, 7 tests. They mock
geometry and cannot detect this defect. The first sandboxed invocation failed
to start Vite with `spawn EPERM`; the identical invocation passed with the
required subprocess permissions.

Before the fix, `npm run e2e:chart-layout` failed at the measured native chart
offset, time-axis clipping, and cursor-price assertions. After the fix, it
passed with zero offset and clipping through both submit/cancel cycles; the
submitted stop matched its displayed preview each time.

Passed: `npm ci`, `go test ./...`, `go test -race -short ./...`, `go vet ./...`,
`golangci-lint run` (2.12.2), `mingw32-make -C engine gen-ts-check`,
`npm run lint`, `npm test`, `npm run build`, and `npm run e2e:chart-layout`.
The first sandboxed generated-contract check hit a Cygwin signal-pipe
permission error; the same check passed with elevated permissions. Hosted CI
remains pending the push. Verify Go LF files, no generated drift, no runtime
data or secrets, and report any skipped required check before handoff.

Commit this plan with the fix and relevant guides, integrate upstream, merge
into local main, push, and verify the task commit on both main branches and a
successful hosted CI run under the standing repository authorization.

## Scope, rollout, rollback, and risks

This is a UI layout correction with browser regression coverage. Reuse the
installed React, Lightweight Charts, and Playwright tooling. No engine order
routing, generated contract, pricing policy, new dependency, workspace
migration, or live-order activity is required.

Validate using simulated state first. A component-only browser harness must
preserve production DOM ownership/order and clipping; verify the final fix in
the real ChartPanel as well. Do not remove screen-reader feedback to solve
visual clipping. Roll back the scoped overlay fix if needed; there is no
persisted-data migration to reverse.
