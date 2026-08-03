# Chart Renderer

Chart controllers, history loading, indicators, sessions, drawings, and primitives. Bar history is shared across panels, while each chart preserves its own logical viewport and scale when older data arrives; visible-range demand may request older history. Preserve chronological merge/dedupe and controller disposal. Test: `npm test -- chart`.
