# Tape Renderer

Canvas time-and-sales painter over bounded tick ring. Inputs: ordered ticks and viewport; output: buy/sell-colored rows. Avoid allocations and React updates on tick path. Test: `npm test -- tape`.
