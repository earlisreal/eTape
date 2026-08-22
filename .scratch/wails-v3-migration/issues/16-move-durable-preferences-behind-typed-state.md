# 16 — Move durable preferences behind typed state

**What to build:** Persist the agreed durable preference inventory through typed canonical state so settings survive every Workspace and restart without retaining a generic configuration or browser-storage bridge.

**Blocked by:** 09 — Migrate non-execution mutations to typed bindings; 10 — Make Workspace catalog and Native Window registry canonical; 13 — Add verified profile backup and additive migrations.

**Status:** ready-for-agent

- [ ] Theme, order settings, sound preferences, Drawing Tool Style, and durable hints have explicit typed snapshot and mutation operations owned by Go.
- [ ] Each accepted mutation is validated, persisted atomically, and returns a monotonic revision that frontends use to ignore stale projections.
- [ ] Opening, reloading, or switching Workspace windows produces the same canonical durable preferences, including after application restart.
- [ ] Existing supported preference values migrate additively from the legacy profile and remain unchanged when migration is retried.
- [ ] Browser-only dismissal flags may reset as approved; app-session dismissals may remain in memory, and temporary forms, drafts, toasts, and modal state remain local to the WebView.
- [ ] The migrated preference inventory no longer reads or writes durable local storage and no longer depends on a generic configuration envelope.
- [ ] Tests cover validation, typed round trips, revision ordering, concurrent windows, migration of existing values, approved dismissal reset, restart persistence, and failed writes without partial state.
- [ ] User, configuration, shared-state, and generated-contract documentation lists the durable inventory and distinguishes durable, session-only, and view-local state.
