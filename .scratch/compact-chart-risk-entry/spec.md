# Compact Chart Risk Entry

Status: approved — Earl confirmed the complete layout and requested implementation on 2026-10-06.

## Goal

Keep Chart Risk Entry readable while leaving price action visible.

## Settled decisions

- Float over the bottom Volume area of the main chart pane, above the time axis or any lower indicator panes.
- Use a transparent background with no box border or action buttons.
- Enter submits; Escape cancels. Preserve the existing entry gestures and validation.
- Keep chart dimensions and the trader's viewport unchanged. The overlay passes pointer input through to the chart.
- Match light and dark chart text, with a small text halo for readability over Volume.

## Confirmed contents

Use two compact text rows at ordinary chart widths, wrapping when needed:

1. Preset, venue and symbol; whole shares, buy/sell limits, notional and estimated risk once both prices exist. Before that, one short instruction to select BUY and SELL.
2. eTape custody, DAY deadline, fees/execution-risk caveat and Enter/Escape shortcuts. Blocking errors, immediate-trigger warnings and uncertain submission outcomes remain visible.

No ADR is needed for this reversible presentation change. The existing Chart Risk Entry sizing, routing, custody, acknowledgements and cancellation rules remain authoritative.

## Validation

Use the existing component tests for Enter/Escape, auto-send and visible states. Extend the simulated chart layout browser check to verify floating placement, transparent styling, absence of buttons, unchanged host geometry and readable light/dark/narrow layouts. Run the repository's required validation before committing, integrating into main and verifying hosted CI.
