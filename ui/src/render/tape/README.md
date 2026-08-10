# Tape Renderer

Canvas time-and-sales painter over bounded tick ring. Inputs: ordered ticks and viewport; output: buy/sell-colored rows. Avoid allocations and React updates on tick path. `tapeLayout.ts` is the shared responsive Price/Size/Time geometry used by the canvas and header: Time appears at 208px and Dockview keeps the panel at 148px minimum. Test: `npm test -- tape`.
