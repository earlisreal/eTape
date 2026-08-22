# 09 — Migrate non-execution mutations to typed bindings

**What to build:** Move the current non-execution scanner, watchlist, venue, credential, and connection operations onto explicit generated methods while retaining useful React forms and leaving only not-yet-migrated shared-state and execution work on the temporary generic bridge.

**Blocked by:** 08 — Introduce generated bindings with read-only queries.

**Status:** complete

- [x] Explicit typed methods cover scanner filter retrieval and mutation, watchlist add and remove, venue setup, credential management, and connection testing.
- [x] Each mutation validates its input at the service boundary and returns a typed business outcome plus resulting revision where state changes; internal failures reject the call distinctly.
- [x] Every corresponding frontend provider, Panel, or settings flow uses the generated methods while existing React text-entry and complex settings forms remain intact.
- [x] Frontend projections ignore stale revisions and remain correct whether the resulting Stream update arrives before or after the binding result.
- [x] Existing accepted, blocked, validation, connection, and internal-error behavior tests pass through generated clients and mocks.
- [x] Generic command cases are removed for this migrated set; shared configuration and execution operations remain temporarily generic.
- [x] Demo and replay mode changes and all order execution or management methods remain deferred to their safety prerequisites rather than receiving temporary generic Wails bindings.
- [x] Service ownership, revision behavior, generated-contract checks, credential-handling safety, and affected user workflows are reflected in durable documentation.

## Implementation notes

- `EngineService` owns the typed mutation methods and enters the same Wails runtime admission gate as the read-only methods.
- Scanner rank and watchlist rows carry source revisions; `ScannerStore` and `WatchlistStore` reject stale cross-lane updates. Credential results never include secret material.
- Deferred validation: full CI-equivalent Windows, Playwright/native Wails smoke, golden/panel suites unrelated to this ticket, full-repository race, and migration ticket-07 soak checks remain deferred by the temporary Wails migration gate.
