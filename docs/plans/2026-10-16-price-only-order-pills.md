# Price-only Order Price Pills

Status: implemented on 2026-10-16. Design recommendations accepted on 2026-10-16.

## Goal and accepted decisions

Remove the visible `B` and `S` prefixes from every Order Price Pill on the
chart's price axis. A single order changes from `B 1.17 ×` to `1.17 ×`.

- Apply to existing orders and selected Chart Risk Entry draft endpoints,
  including drag proposals, restoration after cancellation, and pending updates.
- Keep the separate × action and its existing target. Either draft × still
  discards the whole setup; submitted-order cancellation retains its meaning.
- Keep grouped counts: `B 1.17 (2)` becomes `1.17 (2)`. Preserve the individual
  order chooser and separate opposing sides at the same price.
- Let natural content width shrink while the right edge stays anchored. Keep
  existing height, padding, spacing and interaction priority.
- Retain buy/sell colors, full side descriptions in tooltips and accessible
  names, and price precision: four decimals below $1, two otherwise.

## Current-code evidence

- [ChartOrderMarkers](../../ui/src/chrome/panels/tv/ChartOrderMarkers.tsx)
  writes the prefix in its resting/grouped JSX and imperative `paintProposal`
  path. The latter also restores the confirmed price when a drag is canceled.
- [ChartRiskEntry](../../ui/src/chrome/panels/tv/ChartRiskEntry.tsx) writes
  the prefix while painting selected draft endpoints and their edits.
- [orderPillLayer](../../ui/src/chrome/panels/tv/orderPillLayer.ts) mirrors
  source controls into passive graphics, including measured bounds. Both pill
  owners already use natural width and right alignment, so removing text needs
  no new layout code. Side helpers still drive colors and semantic details.
- Gesture and risk candidate Preview Price Labels already contain price only.
  [CONTEXT.md](../../CONTEXT.md) and the [UI guide](../../ui/README.md)
  describe the old visible side-and-price presentation.

## Implementation

1. Remove only the side prefix at the three formatting sites above, retaining
   existing price formatters and grouped-count suffixes. Keep the existing
   imperative updates and visual synchronization.
2. Update expectations in
   [ChartOrderMarkers.test.tsx](../../ui/src/chrome/panels/tv/ChartOrderMarkers.test.tsx)
   and [ChartRiskEntry.test.tsx](../../ui/src/chrome/panels/tv/ChartRiskEntry.test.tsx).
   Extend their existing interaction checks to cover price-only text in both
   the real control and passive graphic during drag, after cancellation, and
   after updates. Cover both sides and sub-dollar precision. Retain assertions
   for side colors, semantic names, grouped counts, chooser and cancel targets.
3. Update the existing B/S assertions in the simulated
   [chart layout harness](../../ui/e2e/chart-order-layout.mjs). Verify narrower
   right-anchored pills have matching visible/control bounds and reachable
   price and × targets. Reuse its light/dark, grouped/nearby, risk-draft,
   priority and narrow multi-pane scenarios; inspect the resulting screenshots.
4. Update the Order Price Pill definition in CONTEXT.md and presentation in the
   UI guide. Describe price-only text, retained counts/actions and side colors.
   Leave historical implementation plans and specs as history.

## Validation and handoff

Run from `ui/`:

```bash
npx vitest run --project chart-panel src/chrome/panels/tv/ChartOrderMarkers.test.tsx src/chrome/panels/tv/ChartRiskEntry.test.tsx
npm run lint
npm run build
npm run e2e:chart-layout
```

The build includes UI type checking. Validate changed Markdown links and run
`git diff --check` again. No engine, concurrency, generated contract, dependency
or persisted-data changes are needed; focused local engine and contract checks
are unnecessary for this presentation change. Use simulated orders throughout.

Commit the executed plan with its implementation, integrate upstream, merge and
push main, verify local/remote commit containment, and wait for successful full
hosted CI under the repository's [validation rules](../../AGENTS.md#validation).
Update the [plan index](README.md) when the implementation is complete.

## Boundaries, rollout and rollback

Keep existing order sizing, submission, replacement, cancellation, custody,
grouping, price snapping and preview behavior. No new setting, dependency,
shared formatter or pill-layer API is required. Ship as the existing UI update;
no migration is needed. Reverting the scoped change restores the letters.

Color becomes the persistent visible side cue; tooltips and accessible names
retain explicit side descriptions. The main regression risks are a prefix
reappearing during imperative updates and mismatched graphic/input bounds
after the pill shrinks. The focused interaction and browser checks cover these.
