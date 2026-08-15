# UI

Named workspaces are cataloged in engine config (`windows.v1`) by stable UUID; `main` is unnamed and immutable. `monitoring` is the reserved permanent Monitoring Workspace: the catalog opens or reuses its named browser window, and its first missing document is seeded with four unassigned charts, a Scanner, and unassigned Stock Info. Browser Web Locks serialize catalog edits and prevent deletion while a workspace is open.

React/Vite shell around imperative market-data stores and renderers. Wire messages enter `src/wire`, route into `src/data`, then panels/controllers schedule chart or canvas work. The Account panel keeps live Open Orders and the read-only Closed Orders projection in the imperative execution store; the upper tab is session-local while each table's sort preference is persisted. Time & Sales ticks retain the engine-stamped Significant Print level in the imperative tape ring; the separate `md.tape.status` read model feeds read-only settings text. React owns layout/settings, never high-frequency payload state.

The 10-second chart renders missing weekday 04:00–20:00 ET buckets as client-only synthetic flat bars at the preceding real close, with empty volume. These marked display bars are not stored or included in indicators, and generation pauses at the session close until another real bar arrives.

Children: [application source](src/README.md), [mock engine](mock-engine/README.md), [E2E](e2e/README.md), [test support](test/README.md). Fixtures/assets/generated output excluded from guide leaves. Commands: `npm test`, `npm run typecheck`, `npm run lint`, `npm run e2e`.
