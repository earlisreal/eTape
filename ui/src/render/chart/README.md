# Chart Renderer

On `10s`, the controller derives live, historical, and future-detached behavior from the time-scale scroll position so synthetic bars consume user-created future space without snapping the viewport; other timeframes retain their existing follow behavior.

Chart controllers, indicators, sessions, drawings, and primitives. A symbol open waits for the engine's `chart-ready` barrier, queries the complete prepared archive/seed once, and calls `setData` once; pan and zoom never request history. Older provider backfill is archive-only and appears on the next symbol open. Main-pane indicators autoscale against visible candles, while live bars continue through imperative store/controller updates. Snapshot and `setData` timings are logged for tuning acceptable startup delay. Preserve chronological merge/dedupe and controller disposal. Test: `npm test -- chart`.
