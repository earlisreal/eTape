# 23 — Complete tray Quit, restart, and boot-failure lifecycle

**What to build:** Finish the native application lifecycle so tray Open Main, Quit, intentional restart, and boot failure all behave predictably while every acknowledged durable state change survives and no admitted work can outlive storage.

**Blocked by:** 11 — Save Workspace durably before closing; 12 — Restore geometry and distinguish restart from crash; 14 — Centralize global Link Groups; 15 — Centralize Chart Drawings as revisioned operations; 16 — Move durable preferences behind typed state; 17 — Move Scanner Sync off browser coordination; 18 — Journal demo and replay transitions transactionally; 19 — Establish native focus capabilities without execution; 21 — Migrate order management and emergency controls; 22 — Move files, dialogs, exports, and external links native.

**Status:** ready-for-agent

- [ ] Tray Open Main focuses the existing Main Native Window or creates it when closed, without starting another engine or duplicating a Workspace window.
- [ ] Closing the last visible Workspace leaves the engine and tray alive, while tray Quit performs one final process shutdown rather than deleting any Workspace.
- [ ] Quit first durably flushes Workspace documents, shared state, preferences, open-window state, geometry, and any active mode-transition journal.
- [ ] Shutdown then rejects new bindings and Streams, revokes focus capabilities, cancels long-running work, joins all admitted calls and Stream handlers, drains the engine in the established order, and closes storage exactly once.
- [ ] RestartApplication records and durably flushes intent, returns its binding result before asynchronous shutdown begins, and cannot deadlock on its own admission slot.
- [ ] Replacement launch occurs only after Wails and the old process release single-instance and data locks, then restores the intentional open-window set through the established restart path.
- [ ] Normal Quit, intentional restart, and external termination leave distinct durable markers; no success acknowledgement precedes its required database commit.
- [ ] Engine boot failure remains visible in a native diagnostic or boot surface with a working exit path, instead of leaving a blank window or ghost tray process.
- [ ] Lifecycle tests race new and long-running calls, Stream teardown, window close, Quit, restart, engine drain, and store close and prove no write occurs after close under the race detector.
- [ ] Repeated tray reopen, Quit, and restart integration checks produce no ghost tray, duplicate process/window, lost restore intent, stale handler, or database-lock collision.
- [ ] Operations and recovery documentation records the authoritative Quit/restart order and visible boot-failure behavior.
