# 10 — Make Workspace catalog and Native Window registry canonical

**What to build:** Make Go the single revisioned owner of the Workspace catalog, current Workspace documents, open-window set, and Native Window registry so catalog actions and open/focus behavior converge across all Workspace windows without browser-owned coordination.

**Blocked by:** 03 — Establish Native Window, tray, and frameless behavior; 08 — Introduce generated bindings with read-only queries.

**Status:** complete

- [x] A canonical, concurrency-safe store owns the catalog, latest bounded Workspace document and revision, open-window set, and mapping from Workspace identity to one Native Window while reusing the existing persistence layer.
- [x] Generated Workspace operations provide catalog and document snapshots, create, rename, delete, load, ordinary save, open, and focus with typed outcomes and resulting revisions.
- [x] Dockview layout version 8 remains frontend-owned opaque data; Go enforces identity, size, and revision bounds without interpreting Panel Group layout.
- [x] An ordinary save updates canonical in-memory state and revision immediately while disk persistence may be debounced; the service exposes an explicit durable-flush acknowledgement for later close, Quit, restart, and mode-change tickets to wire into their lifecycle flows.
- [x] Opening an already-open Workspace focuses its registered Native Window, opening a closed Workspace creates exactly one, and unknown, stale, or conflicting operations fail deterministically.
- [x] Main and Monitoring retain their reserved identities and behavior, Monitoring cannot be renamed or deleted, closing remains distinct from deletion, and deletion of any open Workspace is rejected.
- [x] Catalog and window projections register invalidation listeners before fetching snapshots, ignore stale revisions, recover from gaps by snapshot, and use the owning Stream for targeted notification; ordinary app-wide events remain optional hints.
- [x] Workspace catalog, document, open, and focus paths no longer depend on browser window naming, `window.open`, BroadcastChannel, Web Locks, or durable local-storage coordination.
- [x] Concurrent catalog mutation, idempotent open/focus, duplicate prevention, reserved Workspace rules, delete-while-open rejection, revision gaps, and four-window convergence are covered through the public Workspace service seam.
- [x] Workspace, window-registry, persistence, and frontend coordination documentation records the new authority and explicitly defers durable close acknowledgement, geometry, and crash restoration to their later tickets.

## Comments

- Go owns the catalog/document/open-window authority through `uistate.Store`; Wails bindings expose typed business outcomes and the durable `FlushWorkspace` barrier.
- The native UI uses generated WorkspaceService calls and the owning `workspace` Stream. The HTTP/browser adapter remains only as a compatibility fallback.
- Checks: full Go tests, affected Wails Go race tests, generated-contract check, UI typecheck, and targeted Workspace/Window/New Window Vitest suites passed.
- Migration-gate deferrals: full repository race, golangci-lint, Playwright E2E, packaged/native smoke, golden/panel suites, and high-volume soak checks remain for the final migration merge.
- Deferred product work: durable close wiring, window geometry, and crash restoration remain later lifecycle tickets.
