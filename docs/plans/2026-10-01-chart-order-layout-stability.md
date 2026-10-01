# Chart order layout and cursor-price stability

Status: implemented; local validation passed.

## Goal

Keep the chart viewport, price mapping, and time axis stable when chart orders
are previewed, submitted, replaced, or canceled. Ordinary market-driven
autoscaling and existing viewport behavior continue unchanged.

## Evidence and decisions

`ChartOrderMarkers` rendered the empty-state accessibility announcement as a
normal-flow child of the chart host. Once the final working order disappeared,
the cancellation message gained height, displaced Lightweight Charts inside
its fixed host, and made host-relative pointer prices disagree with the chart
pane. The displaced time axis could also extend beyond the host and panel.

Keep the clipped `aria-live` region mounted inside the same absolute overlay in
both empty and nonempty states. STOP remains the snapped price under the
pointer; LIMIT remains the Action Template's cushion-adjusted price. Order
overlays do not participate in price autoscaling.

## Non-goals

No changes to engine routing, order-pricing policy, WebSocket contracts, live
order activity, or high-frequency data flow. Ordinary market-driven autoscaling
continues as before.

## Implementation

- `ui/src/chrome/panels/tv/ChartOrderMarkers.tsx` keeps the overlay and clipped
  announcement mounted when no order markers remain.
- `ui/src/chrome/panels/ChartPanel.tsx` defers empty volume-scale margin updates
  until the volume series creates that scale.
- `ui/e2e/chart-order-layout.mjs` and `ui/e2e/chart-order-layout/main.tsx`
  cover a fixed-data Lightweight Charts harness and production `PanelFrame` /
  `ChartPanel` with simulated commands only. Scenarios exercise Shift preview/submit,
  price-only replacement, LIMIT and STOP_LIMIT overlays, multiple-to-empty
  transitions, manual zoom, panel resize, and pending/rejected/unknown cancels.
- `ui/src/chrome/panels/tv/ChartOrderMarkers.test.tsx` checks the empty-state
  absolute overlay and clipped live region.
- `ui/src/chrome/panels/README.md` documents the stable host-origin invariant;
  `ui/e2e/README.md` documents the browser regression command.

## Validation

Run `cd ui && npm run e2e:chart-layout` for the simulated overlay and production
ChartPanel scenarios; run `npm test`, `npm run lint`, and `npm run build` for the
UI suite. Run `go test ./...`, `go test -race -short ./...`, `go vet ./...`,
`golangci-lint run`, and `mingw32-make -C engine gen-ts-check` from their
documented working directories.

## Risks

Moving the accessibility region outside the absolute overlay could reintroduce
the layout shift; the component and browser regressions cover empty-state and
order transitions. The production browser scenario also exercises the volume
scale initialization guard.

## Rollout and rollback

Run the browser scenario against simulated orders; no engine socket or broker
connection is used. Revert the overlay/layout changes if the chart host or
time-axis geometry regresses. No persisted-data or order-contract migration is
involved.
