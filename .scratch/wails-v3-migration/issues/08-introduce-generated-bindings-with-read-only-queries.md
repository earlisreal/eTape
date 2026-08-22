# 08 — Introduce generated bindings with read-only queries

**What to build:** Establish the small generated service boundary and migrate read-only chart, fills, locate, and export queries end to end, giving the frontend typed results and mocks without changing subscription traffic or retaining generic query dispatch for the migrated set.

**Blocked by:** 05 — Put engine lifecycle behind admission and drain; 07 — Harden Stream parity and the test-only server path.

**Status:** ready-for-agent

- [ ] One EngineService and one WorkspaceService are registered as concrete singleton services, and every method enters the shared lifecycle admission gate before touching engine or storage state.
- [ ] Go remains the source of truth for service models; Wails bindings and existing Stream DTOs regenerate together, committed generated TypeScript is treated as read-only, and a clean regeneration plus frontend typecheck reports no drift.
- [ ] Explicit generated methods cover chart-window queries, fills and cycle fills, locate eligibility, quotes and records, and export-data queries with no generic name switch or correlation identifier.
- [ ] Every frontend caller for the migrated queries uses the generated client and typed return model, and test doubles implement that generated surface without casting unknown payloads.
- [ ] Typed round-trip tests cover values, optional values, enum values, expected business outcomes, and internal failures, reserving rejected calls for bridge or internal errors.
- [ ] Generic query frames and dispatch cases are removed for the migrated set; subscriptions, demands, indicators, snapshots, and updates remain on the Workspace Stream.
- [ ] Existing query behavior and UI projection tests remain green through the generated binding boundary and test-only Wails server.
- [ ] Binding-generation, contract ownership, query surface, and validation commands are documented without duplicating generated API details.
