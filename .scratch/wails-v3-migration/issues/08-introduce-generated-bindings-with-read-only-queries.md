# 08 — Introduce generated bindings with read-only queries

**What to build:** Establish the small generated service boundary and migrate read-only chart, fills, locate, and export queries end to end, giving the frontend typed results and mocks without changing subscription traffic or retaining generic query dispatch for the migrated set.

**Blocked by:** 05 — Put engine lifecycle behind admission and drain; 07 — Harden Stream parity and the test-only server path.

**Status:** complete

- [x] One EngineService and one WorkspaceService are registered as concrete singleton services, and every method enters the shared lifecycle admission gate before touching engine or storage state.
- [x] Go remains the source of truth for service models; Wails bindings and existing Stream DTOs regenerate together, committed generated TypeScript is treated as read-only, and a clean regeneration plus frontend typecheck reports no drift.
- [x] Explicit generated methods cover chart-window queries, fills and cycle fills, locate eligibility, quotes and records, and export-data queries with no generic name switch or correlation identifier.
- [x] Every frontend caller for the migrated queries uses the generated client and typed return model, and test doubles implement that generated surface without casting unknown payloads.
- [x] Typed round-trip tests cover values, optional values, enum values, expected business outcomes, and internal failures, reserving rejected calls for bridge or internal errors.
- [x] Generic query frames and dispatch cases are removed for the migrated set; subscriptions, demands, indicators, snapshots, and updates remain on the Workspace Stream.
- [x] Existing query behavior and UI projection tests remain green through the generated binding boundary and test-only Wails server.
- [x] Binding-generation, contract ownership, query surface, and validation commands are documented without duplicating generated API details.

## Comments

Ticket-08 implementation is complete on `codex/wails-v3-migration`. Focused Go
unit, Wails-tagged, test-only server binding, and targeted race checks pass.
UI typecheck and affected chart/locate/wire, Account, and export tests pass.
Clean tygo and Wails regeneration is deterministic with no generated-contract
drift, and generated TypeScript was not hand-edited.

Per `AGENTS.md`, the following merge-gate checks remain deferred and were not
removed or weakened: synth/demo tests, `golangci-lint`, Playwright E2E,
packaged/native Wails smoke, 100 WebView reloads, 100 lifecycle start/stop
cycles, the four-Workspace-Stream ten-second stall soak, unrelated UI
golden/panel suites, and the full-repository race suite.
