# Ladder Renderer

The fixed chrome strip always shows the best bid × best ask and spread when
both real book sides exist. A valid priced `estimatedLuld` state adds a
display-only `LULD` Boundary Row to the lower bid sequence and upper ask
sequence. Frozen values add `FROZEN`; warming, unavailable, invalid, unknown,
and unpriced values add no visual row. Boundary rows use the warning palette,
have no depth bar, size, order mark, or trade flash, and are never dashed
markers or order/risk controls.

Each price row and each bid/ask readout chooses precision from its own value:
four decimal places for positive prices below $1, otherwise three. LULD
Boundary Rows, bottom fallbacks, and accessible LULD values follow the same
rule without requiring a latest trade. Spread uses four places for nonzero
sub-dollar magnitudes, preserving its sign; zero remains `0.000`.

When a Link Group has a selected Execution Venue, a non-flat Open Position for
the ladder symbol with a finite positive Average Entry Price can project an
Average-Entry Row cue. The cue uses exact numeric equality against ordinary
real bid or ask rows, so both sides may be bold during a crossed or stale book;
pinned ladders, NET/null-venue rows, invalid or flat positions, and prices not
present in the real book produce no cue. The existing 11px mono price and size
text is bold, and each matching real bid/ask half-row has a neutral one-pixel
inset border; depth bars, colors, flashes, working-order marks, ordering, and
scroll position are unchanged. LULD Boundary Rows never qualify.

Configured Depth counts real price levels only. An in-range boundary is inserted
at its sorted price and scrolls with that side. A boundary beyond Configured
Depth is shown as a plain bottom-slot fallback on its own side; that slot can
extend the shared logical scroll range so the final configured real level stays
reachable. The canvas accessible name stays imperative and includes the symbol,
LULD state, values, tier, registry date, reason, and frozen status whenever the
state changes. Book and LULD data never passes through React state.

Canvas DOM ladder painter. Inputs: order-book/quote state and viewport; output:
price-level canvas. U.S. books can project 1–60 configured real levels plus
virtual boundary rows onto a viewport-sized canvas with a logical row offset;
render directly from store snapshots, coalesced per frame. The Average-Entry Row
cue follows the same logical scroll window: the canvas accessible name includes
`Average-Entry Row visible` only while an exact matching real row is visible;
offscreen and nonmatching bases add no accessibility text. Test:
`npm test -- ladder`.
