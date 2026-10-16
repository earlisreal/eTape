# Renderers

Imperative rendering for high-rate surfaces. Children: [chart](chart/README.md), [ladder](ladder/README.md), [tape](tape/README.md). Inputs: stores and viewport/config; outputs: canvas/chart mutations. Coalesce paints per animation frame. Test: `npm test -- render`.

Shared [price formatting](format.ts) uses `quoteDecimals(price, normalDecimals)`
to select four digits for a finite positive price below $1, otherwise the
caller's normal precision (three by default). Selection uses the raw value
before rounding, so `0.99996` displays as `1.0000`. Table callers preserve
their existing two- or three-digit fallback, including zero. Signed per-share
differences select precision by absolute magnitude and format the original
signed value. `formatPrice` itself remains an explicit-precision formatter
for other values such as total P&L.
