# Stock shorts in Chart Risk Entry

Support US stock shorts in Simulator, Alpaca Paper, and Alpaca Live. The Auto
preset uses the chart for the instrument and the second price for direction.
The existing Auto-send setting applies to these venues, including Live.
The first release covers easy-to-borrow Alpaca stocks. Hard-to-borrow support
depends on access to the quantity still usable on an approved locate.

## Entry and protection

- For a short, the higher point is the COVER trigger. The SHORT triggers on an
  eligible trade at or below entry. Its limit uses the entry trigger minus its
  cushion. The COVER limit uses its trigger plus its cushion.
- Size whole shares using the distance between the two limits, the applicable
  account cap, and the reviewed size. Simulator uses 1× equity after existing
  stock exposure and pending opening orders. Alpaca checks the exact account
  and short eligibility at arming and activation. Use broker buying power for
  the opening estimate. Reject an armed entry if the reviewed size is no
  longer available.
- Open from flat or add to an existing Chart Risk Entry position in the same
  direction with healthy protection. An add may keep or lower the short's
  shared COVER stop on its first fill. Support Fixed, dollar, percent and
  Candle Trail with the usual stock expiry, Resume and engine-run rules.
- Reconcile fills, manual covers and late fills using signed position ownership.
  Protect confirmed shares and account for any working closing orders.

## Implementation and acceptance

1. Generalize [stock entries](../../engine/internal/exec/risk_entry.go) and
   [adds](../../engine/internal/exec/risk_add.go). Preserve direction from the
   order side on replay. Regenerate the WebSocket contract from the Go types.
2. Add the [Alpaca opening-capacity check](../../engine/internal/broker/alpaca/stock_short.go)
   and the simulated equity cap. Verify shorting permission and fresh symbol
   eligibility. Size against a conservative estimate of Alpaca's buying-power
   check and reconcile the broker response.
3. Update the [chart gesture](../../ui/src/chrome/panels/tv/ChartRiskEntry.tsx)
   and pending order edits with directional prices and labels. Existing saved
   stock-long presets continue to choose BUY.
4. Run engine and browser interaction tests for both directions, plus targeted
   stock-short fill, cover, add and eligibility scenarios. Verify broker fills
   in Simulator and Alpaca Paper and live account inputs with read-only checks.
   Before downgrading the engine, resolve working stock-short Chart Risk Entry
   orders and verify protection for any remaining short position.

[Alpaca borrow rules](https://docs.alpaca.markets/us/docs/margin-and-short-selling) ·
[Alpaca buying power](https://docs.alpaca.markets/us/docs/orders-at-alpaca) ·
[Trailing stop lifecycle](../adr/0009-trailing-stop-position-and-engine-lifetime.md)
