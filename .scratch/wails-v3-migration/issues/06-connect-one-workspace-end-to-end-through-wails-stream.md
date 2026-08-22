# 06 — Connect one Workspace end-to-end through Wails Stream

**What to build:** Connect one native Workspace to the existing UI Hub through one Wails Stream so a newly attached or reloaded WebView receives a valid snapshot before continuous deltas and drives the current imperative stores without moving high-frequency data into React state or ordinary events.

**Blocked by:** 05 — Put engine lifecycle behind admission and drain.

**Status:** ready-for-agent

- [x] The Wails adapter plugs into the existing UI Hub connection boundary and reuses its session, mirror, snapshot, outbox, ordering, coalescing, and cleanup behavior rather than reimplementing them.
- [x] The Stream's first frame declares protocol version, Workspace, and opaque session identity; desktop validation binds it to the Native Window registry and rejects an invalid declaration before engine state changes.
- [x] One admitted Hub session owns the Workspace's subscriptions, symbol demands, indicators, snapshots, updates, and cleanup for the lifetime of the WebView.
- [x] The frontend Stream client preserves the existing subscription/store-facing seam and routes snapshots and deltas into the current imperative stores, Scheduler, canvas, and chart controllers.
- [x] Initial attach and WebView reload both produce a fresh snapshot before any delta, and the frontend reannounces demands only after the new session is ready.
- [x] Closing or reloading the WebView terminates the old handler and releases its session-owned subscriptions, demands, indicators, watchers, and backfill ownership exactly once in the basic end-to-end case.
- [x] A deterministic end-to-end check proves one Workspace renders live demo or replay projections through Wails Stream with no ordinary event or React-state high-frequency path.
- [x] Wire and UI Hub documentation describes the Stream connection, handshake, ownership, and preserved ordering seam.
