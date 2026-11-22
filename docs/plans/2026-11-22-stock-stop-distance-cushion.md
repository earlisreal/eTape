# Stock Chart Risk Entry cushion as a percent of stop distance

## Goal and scope

Add **% of stop distance** to the Limit Cushion settings for stock Chart Risk
Entry. This gives tighter setups proportionally smaller cushions while eTape
continues sizing from the two execution limits. Cover entries from flat and
same-side adds, for longs and shorts on the venues that already permit each
side. Offer the new unit only on the stock side of Chart Risk Entry, including
stock charts using an Auto preset.

## Current behavior

- [Preset settings](../../ui/src/chrome/exec/OrderSettingsSection.tsx) offer
  dollar and percent-of-price cushions. Auto presets share the entry cushion
  with perpetuals. Stock trail presets also carry a protection cushion in their
  trail settings.
- The [chart sizing function](../../ui/src/chrome/exec/riskEntry.ts) sizes from
  the rounded limits and available funds. The
  [armed order markers](../../ui/src/chrome/panels/tv/ChartOrderMarkers.tsx)
  preview edits using the stored cushion values.
- The [execution engine](../../engine/internal/exec/risk_entry.go) recomputes
  limits and quantity at admission and enforces the reviewed quantity. It
  journals the entry and protection together. A
  [same-side add](../../engine/internal/exec/risk_add.go) inherits the shared
  stop's cushion and may tighten that stop on its first fill.

## Behavior

**Preset settings.** Allow dollars, **% of price**, and **% of stop distance**
independently for stock entry and stock protection. The new mode is opt-in on
each leg and accepts finite nonnegative percentages, including zero. For an
Auto preset using a stop-distance entry cushion, show which entry cushion
applies to perpetuals. Give each stock risk preset a single protection cushion
field used by Fixed, Trail Price, Trail Percent, and Candle Trail.

**Sizing.** Use the absolute difference between the two tick-aligned chart
triggers. Multiply that gap by the selected percentage divided by 100 to get
each leg's dollar cushion. Add the cushion for BUY or COVER and subtract it
for SHORT or SELL. Round the resulting limits outward with eTape's existing
stock tick rules. Size from the rounded limit-to-limit distance, the saved
risk budget, current funding and eligibility, and the reviewed quantity.
Keep the applicable whole-share or fractional-share rounding.

**Chart preview.** Show the rounded limits and the risk based on those limits.
For setups using the new unit, also show **Loss at drawn prices** while drawing
or previewing an edit before entry dispatch. Multiply the displayed quantity
by the trigger gap before fees. For an add, this readout covers the new shares.
The effective cushion can jump by a tick on a small price change. A narrow
limit can leave a triggered order resting.

**Armed edits and dispatch.** Before the entry is dispatched, an allowed edit
to either trigger recalculates all stop-distance cushions from the proposed
pair. Recompute both limits and check quantity against the original risk
budget and the amount just reviewed. Journal the updated triggers, limits,
resolved cushions, quantity, and any pending trail settings together before
the entry can trigger. Use the existing phase checks to reject an edit that
races activation. Once dispatched, use the last accepted dollar cushion
for later protective price moves and trail updates.

**Trail edits.** For setups using the new unit, a pre-dispatch Edit Trail
action uses stop-relative pricing so the engine can size the entry. An
explicit dollar or price-percent protection cushion replaces that leg's
pending recipe and triggers the same sizing checks with the last approved
quantity as the cap. A trail-distance-only edit keeps the cushion recipe.
After dispatch, apply the existing trail edit rules.

**Adds.** Calculate the add's stop-distance entry cushion from its entry
trigger and the proposed shared stop. Use the shared stop's current cushion
for the exit estimate, including the resolved dollar cushion from a
stop-distance preset. If the add's entry trigger is edited while armed,
calculate with the tighter of the current shared stop and the add's saved
stop proposal, and apply the existing add quantity cap. Dispatch uses the
last accepted entry limit and the existing shared protection checks. Apply
any proposed tightening on first fill using the shared stop's own cushion.

## Implementation

1. In [actionTemplate.ts](../../ui/src/chrome/exec/actionTemplate.ts), add
   optional stock stop-distance percentages per leg alongside the existing
   dollar or price-percent recipe. Preserve a distinction between an omitted
   percentage and zero. Wire the stock controls in
   [OrderSettingsSection.tsx](../../ui/src/chrome/exec/OrderSettingsSection.tsx)
   and use the preset's single protection cushion field with
   [TrailEditor](../../ui/src/chrome/exec/TrailEditor.tsx).
2. Use the existing stock sizing module to resolve both limits from the
   proposed trigger pair for
   [ChartRiskEntry](../../ui/src/chrome/panels/tv/ChartRiskEntry.tsx) and
   [ChartOrderMarkers](../../ui/src/chrome/panels/tv/ChartOrderMarkers.tsx).
   Reuse the existing stock tick helper and imperative preview updates.
3. Add optional stock percentages to the Go-owned
   [command and order payloads](../../engine/internal/uihub/wsmsg/payloads.go),
   [command mapping](../../engine/internal/uihub/commands.go), and
   [order projection](../../engine/internal/uihub/map.go). Carry the reviewed
   dollar cushions in the existing fields for legs using the new unit.
   Regenerate the [TypeScript contract](../../ui/src/gen/wsmsg.ts).
4. Resolve the new amounts in the engine's existing stock admission and
   amendment paths. Store the optional recipe with the order for pending
   edits and keep the resolved dollars in the ordinary Limit Cushion fields
   used by fixed and trailing protection. Route relevant
   [trail edits](../../engine/internal/exec/trailing_commands.go) through the
   same pending sizing checks. Use the existing activation marker to determine
   when the recipe stops recalculating. Exercise replay through the
   [existing execution events](../../engine/internal/exec/events.go).
5. Update the [UI guide](../../ui/README.md),
   [execution guide](../../engine/internal/exec/README.md), and
   [Limit Cushion glossary entry](../../CONTEXT.md#order-entry) with the
   shipped settings, calculation, and edit behavior.

## Acceptance and validation

Use these examples for the chart preview and engine admission with a $100
risk budget, whole shares, and sufficient funding. Each rate is per leg.

| Side | Entry / stop triggers | % of stop distance | Rounded entry / protection limits | Shares | Limit-based risk | Loss at drawn prices |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Long | $100 / $99 | 5% | $100.05 / $98.95 | 90 | $99.00 | $90.00 |
| Short | $100 / $101 | 5% | $99.95 / $101.05 | 90 | $99.00 | $90.00 |
| Long | $21 / $20.79 | 5% | $21.02 / $20.77 | 400 | $100.00 | $84.00 |
| Long | $1 / $0.99 | 5% | $1.01 / $0.9895 | 4,878 | $100.00 | $48.78 |
| Long | $500 / $487 | 10% | $501.30 / $485.70 | 6 | $93.60 | $78.00 |

- Check both directions, mixed units, zero, the $1 tick transition, fractional
  sizing, invalid limits, and a funding cap. Check legacy preset and workspace
  loading, save and reload of the new unit, and Auto presets on stock and
  perpetual charts.
- Exercise drag, keyboard, and trail edits before activation, a crossing
  while an edit is in flight, partial fills, post-dispatch protection edits,
  and fixed and trailing shared-stop adds. Replay armed and activated orders
  and verify the applicable cushion and the protected share count.
- Verify a controlled Simulator workflow through settings, chart preview,
  order dispatch, and the linked stop. Run focused engine and UI tests,
  lifecycle race tests, `gen-ts-check`, and the applicable lint, type, and build
  checks from the [Windows checklist](../../README.md#ci-equivalent-validation-on-windows).
  Check Markdown links and `git diff --check`. After implementation is pushed,
  verify full hosted CI on the pushed main commit.

## Rollout and rollback

Ship the matching UI and engine together. Saved presets and orders with an
absent stop-distance recipe load from their existing fields. New orders journal
their resolved dollar cushions so protection can still be read through the
ordinary cushion fields. Before downgrading, resolve pending stop-distance
entries and any working remainders, then verify protection for any open
position. Older releases read the saved dollar recipe when managing those
orders.
