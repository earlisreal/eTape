# Ladder Renderer

Canvas DOM ladder painter. Inputs: order-book/quote state and viewport; output: price-level canvas. U.S. books can project 1–60 configured levels onto a viewport-sized canvas with a logical row offset; render directly from store snapshots, coalesced per frame. Test: `npm test -- ladder`.
