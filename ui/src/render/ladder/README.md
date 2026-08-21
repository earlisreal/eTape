# Ladder Renderer

The fixed chrome strip may show the Go-owned `estimatedLuld` value as
`EST LULD`; it keeps the explicit estimate wording at narrow widths. Estimated
or frozen lower/upper values receive dashed, labelled markers only when they
fall inside visible ladder prices. The canvas accessible name includes the
symbol, state, values, tier, registry date, and reason, but it is updated only
when the LULD value changes. Book data stays in the imperative store and never
passes through React state; the value is not an order, halt signal, or risk
control.

Canvas DOM ladder painter. Inputs: order-book/quote state and viewport; output: price-level canvas. U.S. books can project 1–60 configured levels onto a viewport-sized canvas with a logical row offset; render directly from store snapshots, coalesced per frame. Test: `npm test -- ladder`.
