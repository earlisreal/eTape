# DOM Ladder stripes for Engine-Held Stop-Limits

Approved and implemented on 2026-11-13.

## Goal

Show the stop price and planned limit price together so the distance between
them shows the Limit Cushion. When the linked Limit Order starts working,
show its limit price with the existing working-order stripe. The change is
limited to the DOM Ladder's display of fixed Engine-Held Stop-Limits.

## Prior behavior

- [LadderPanel](../../ui/src/chrome/panels/LadderPanel.tsx) reads working orders
  for the displayed symbol across venues and repaints on execution updates.
  [ExecStore](../../ui/src/data/ExecStore.ts) filters out optimistic rows and
  pending or unknown cancels.
- [The order projection](../../ui/src/render/ladder/ladderState.ts) previously
  used `limitPrice` for Stop-Limits even before activation and required a
  positive share quantity. The [painter](../../ui/src/render/ladder/paintLadder.ts)
  draws a bronze stripe at an exact matching price on either book side.
- [The order contract](../../engine/internal/uihub/wsmsg/payloads.go) already
  carries the stop price, limit price, Held Phase, and Deferred Position Sizing.
  The [engine](../../engine/internal/exec/core.go) sets `WORKING` on child
  acceptance or successful recovery. A child client ID can exist while the
  order is still `ACTIVATING`.

## Behavior

| Held Phase | Stop price | Limit price |
| --- | --- | --- |
| `WAITING` or `ARMED` | Segmented cyan | Outlined cyan |
| `PAUSED` | Dimmed segmented cyan | Dimmed outlined cyan |
| `WORKING` | | Solid bronze at the confirmed child limit |
| `ACTIVATING`, `UNKNOWN`, or `CANCEL_REQUESTED` | | |

- For a buy stop at $1.680 with a planned limit of $1.700, the two stripes
  show a $0.020 Limit Cushion when both levels are visible.
- Use the engine's reported phase and prices. During a pending edit, draw the
  stripes at the last confirmed prices. Terminal orders drop their stripes.
  Open Orders shows the phase while both stripes are hidden during activation,
  an unknown outcome, or a pending cancel.
- Use the displayed symbol's working orders across venues. Draw the stripe at
  the center edge of every visible real bid or ask row with an exact price
  match. Match the two prices independently so either stripe can appear by
  itself when the other price is missing from the visible book. The displayed
  size remains market depth.
- If both held stripes match one row, including a zero Limit Cushion, draw the
  segmented stop in the top half and the outlined limit in the bottom half of
  the same three-pixel column. If a working order also matches that row, keep
  bronze at the divider and draw cyan immediately beside it. When several
  orders share a stripe at one price, use full intensity if any are Waiting or
  Armed.
- Include Deferred Position Sizing before the engine resolves its share
  quantity. A linked protective stop waiting for entry fills first qualifies
  once it has shares to protect. Preserve the existing projection for native
  stops, Limit-if-Touched orders, trailing stops, and ordinary limits.

## Implementation

1. In [ladderState.ts](../../ui/src/render/ladder/ladderState.ts), classify orders
   with `type === "STOP_LIMIT"` and held metadata without a trail. Project a
   stop mark from `stopPrice` and a planned limit mark from `limitPrice`
   before activation, then a working mark from `limitPrice` in `WORKING`.
   Allow a valid deferred position percentage to qualify the pretrigger marks
   with zero shares.
2. In [paintLadder.ts](../../ui/src/render/ladder/paintLadder.ts), use the
   existing three-pixel stripe width with segmented and outlined variants and
   a cyan color in both [palettes](../../ui/src/render/palette.ts). Keep the
   held column inside the space between the divider and price text when it
   shares a row with bronze.
3. Use the existing imperative canvas accessible name in
   [LadderPanel.tsx](../../ui/src/chrome/panels/LadderPanel.tsx) to include the
   visible held prices with stop and planned limit labels and identify paused
   orders. Document both stripes in the [ladder guide](../../ui/src/render/ladder/README.md).

## Validation

- Extend the [ladder state and painter tests](../../ui/src/render/ladder/ladderState.test.ts)
  with both prices visible, one price missing or scrolled away, a zero
  Limit Cushion at a price shared with a working order, and LULD Boundary Rows.
  Verify pause and resume, activation before child confirmation, partial fills,
  cancellation, Deferred Position Sizing, and native Stop-Limit price selection.
- Cover accessible names for both prices and repaint on order updates in the
  [LadderPanel test](../../ui/src/chrome/panels/LadderPanel.test.tsx). Check
  both cyan stripes against both themes using fixture data.
- Run the following from `ui/`. Require `git diff --check` and green hosted CI
  on pushed `main` per the [agent guide](../../AGENTS.md#validation).

  ```powershell
  npx vitest run --project chart-core src/render/ladder/ladderState.test.ts
  npx vitest run --project ladder src/chrome/panels/LadderPanel.test.tsx
  npx vitest run --project golden test/golden/ladder.golden.test.ts
  npm run lint
  npm run build
  ```

## Rollout and rollback

Ship with the UI and read existing order snapshots and deltas. Each stripe is
only as visible as its matching book level. Open Orders provides both prices
and the order phase when depth is sparse. A scoped UI revert restores the
previous markers.
