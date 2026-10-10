# End-to-End Tests

The chart layout regression verifies compact side-colored axis chips, hover
retention, separate BUY/SELL groups, same-side choosers and nearby-price spacing.
Risk setup covers native tooltips without risk amounts, theme colors, axis
dragging, Enter submission and draft cancellation in light/dark and narrow
multi-pane charts. Screenshots are saved in `.report/`.

Crosshair Sync's browser scenario opens two same-origin Trading workspaces and verifies cursor delivery, clear-on-leave, and a usable narrow-header toggle. ChartPanel tests verify that receiver callbacks are not republished.

Playwright scenarios exercise the browser against controlled engine/demo state. Inputs: fixtures and built services; outputs: behavioral assertions/screenshots. Avoid live venues and nondeterministic external feeds. Run: `npm run e2e`. The chart-order layout regression uses fixed bars and sim-only order commands: `npm run e2e:chart-layout`. It tests both the order components with a real Lightweight Charts instance and production `PanelFrame` / `ChartPanel` inside the actual clipped panel body. It checks cursor-price alignment, viewport stability, time-axis visibility, LIMIT and STOP_LIMIT overlays, and cancel outcomes without a broker or engine connection. Sim E2E scenarios cover engine-held STOP_LIMIT custody and LIT activation into a single child LIMIT, including child cancellation and fill. Feed outage/reconnect and exact session-deadline behavior remain deterministic engine tests because the in-process demo feed has no outage control and the E2E engine uses the host clock. The isolated ticketless cross-window scenario uses the deterministic sim-only harness: `npm run e2e:ticketless`.

`volume-profile.spec.ts` intercepts the existing sim/demo server's transport with fixed chart bars, captured-profile DTOs and a live-mode UI fixture; no real archive or broker is accessed. It exercises production ChartPanel, placement settings, passive wheel zoom, range-wide Partial legend, subscription bypass, daily unavailability and demo unavailability. The live-mode onboarding dialog is dismissed locally. Run on a spare port: `$env:ETAPE_UIHUB_PORT='18687'; npx playwright test e2e/volume-profile.spec.ts`. A screenshot is retained under the test's output directory.
