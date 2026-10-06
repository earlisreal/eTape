# Compact chart order chips

Status: approved — Earl selected all six design recommendations on 2026-10-06.

Replace routine left-edge chart order labels with `B price ×` / `S price ×`
controls anchored to the right price axis, for Chart Risk Entry drafts, Chart
Order Gesture previews and working LIMIT/STOP_LIMIT/LIT orders.

- BUY/COVER uses existing chart green; SELL/SHORT uses chart red for line and chip.
- Hover shows shares first, then type, execution limit and current status/custody.
  Risk amounts and notional stay off the chart, including tooltips.
- Waiting linked protection shows planned shares; Deferred Position Sizing shows
  percentage intent until trigger-time shares exist.
- Actionable blockers, uncertain outcomes, missing protection and above-budget
  warnings stay visible. Initial risk setup keeps one short instruction.
- Opposing sides at one price remain separate. Same-side orders of the same type
  reuse the existing count chooser. Nearby chips remain independently clickable
  without moving price lines or changing the chart viewport.
- Preserve order actions: working marker drag/release and arrow-key replacement,
  modifier-click gesture submission, risk Enter/auto-send, and existing cancel
  targets. Either risk draft × discards both prices; gesture preview × consumes
  the modifier press. Cancel Protection still leaves acquired shares open.

Reuse the current overlays, chart chrome palette and native title tooltips.
Keep high-frequency updates imperative; no engine or wire-contract change.
Validate focused regressions, the simulated real-chart layout scenario and the
full Windows CI-equivalent checklist, then commit, integrate main and verify CI.
