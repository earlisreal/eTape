# Chart Preview Price Label styling

Status: implemented on 2026-10-07 after Earl confirmed the complete design.

Results: [validation](validation.md).

## Requested behavior

During Chart Order Gesture and Chart Risk Entry price selection or adjustment,
the Preview Price Label matches the Native Crosshair Price Label's readable
font, height, padding, and left alignment within the right price axis.
The label background uses chart green for BUY/COVER and chart red for SELL/SHORT.
It remains read-only and follows the existing snapped prospective trigger price.

## Confirmed detail

Use white price text on the colored background in light and dark chart themes.
Apply the same presentation to every configured modifier gesture and both
Chart Risk Entry endpoints, including endpoint edits.
Selected draft and submitted Order Price Pills keep their current presentation
and actions. Order Preview Lines, pointer ownership, crosshair restoration,
sizing, validation, and submission retain their existing behavior.

## Current-code evidence

Both overlay labels use a right-anchored, bold 10px monospace style on a dark
background. The installed chart renderer uses a normal 12px system/Trebuchet
font and an approximately 21px-tall Native Crosshair Price Label, anchored at
the left edge of the price axis. Its price text starts 10px inside that edge.
Match those metrics while preserving content-sized label width and existing
axis-width bounds. Keep the taller label inside the main price pane.

The owning components are `ChartConditionalOrderEntry.tsx` and
`ChartRiskEntry.tsx`; both already paint prospective prices imperatively.
Change their existing label presentation and placement directly.

## Documentation and validation

The glossary's Preview Price Label definition now covers both entry methods,
matching the existing implementation. This reversible styling change needs no ADR.
Reuse existing component and simulated-browser checks to verify both sides,
themes, native label geometry, tick precision, and read-only preview behavior.
Earl confirmed the complete design in Q1 and requested implementation. This
spec supersedes the earlier preview presentation, including the historical
gesture-label omission in `.scratch/chart-order-preview/spec.md`.
