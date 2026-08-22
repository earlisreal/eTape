# 24 — Contract generic bridges and browser coordination

**What to build:** Remove the migration-era generic bridge and browser coordination after all callers have reached typed bindings, Workspace Streams, canonical Go state, and Native Window operations, leaving one explicit and supportable application contract.

**Blocked by:** 09 — Migrate non-execution mutations to typed bindings; 10 — Make Workspace catalog and Native Window registry canonical; 14 — Centralize global Link Groups; 15 — Centralize Chart Drawings as revisioned operations; 16 — Move durable preferences behind typed state; 17 — Move Scanner Sync off browser coordination; 18 — Journal demo and replay transitions transactionally; 21 — Migrate order management and emergency controls; 22 — Move files, dialogs, exports, and external links native; 23 — Complete tray Quit, restart, and boot-failure lifecycle.

**Status:** ready-for-agent

- [ ] Generic command, query, configuration, acknowledgement, result, and correlation-ID envelopes and dispatchers have no remaining product caller and are removed.
- [ ] The Workspace Stream carries only session control, subscriptions/demands, snapshots, continuous updates, targeted invalidation, lifecycle framing, and health traffic.
- [ ] Every discrete product command and query uses an explicit generated binding with typed business outcomes and revisions where applicable.
- [ ] BroadcastChannel, Web Locks, browser-window naming/adoption, random browser-window ownership IDs, and browser-window creation are absent from product coordination.
- [ ] Durable local-storage coordination is absent; accepted transient dismissal resets and view-local form/modal state do not reintroduce cross-window authority.
- [ ] Global Link Groups, Chart Drawings, preferences, Scanner Sync, Workspace lifecycle, and demo/replay transitions continue to converge through canonical state after the old coordination paths are removed.
- [ ] High-frequency and targeted traffic remains outside React state and ordinary Wails events, and app-wide events remain bounded revision hints rather than correctness paths.
- [ ] Contract regeneration, type checking, engine/UI tests, server-mode end-to-end tests, and repository searches prove there is no hidden compatibility caller.
- [ ] Current architecture, transport, UI, and agent documentation describes only generated bindings, Workspace Streams, canonical state, and Native Window ownership outside explicitly historical text.
