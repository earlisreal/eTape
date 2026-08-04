# UI

Named workspaces are cataloged in engine config (`windows.v1`) by stable UUID; `main` is unnamed and immutable. Browser Web Locks serialize catalog edits and prevent deletion while a workspace is open.

React/Vite shell around imperative market-data stores and renderers. Wire messages enter `src/wire`, route into `src/data`, then panels/controllers schedule chart or canvas work. React owns layout/settings, never high-frequency payload state.

The 10-second chart renders missing weekday 04:00–20:00 ET buckets as client-only whitespace after the first real bar in each continuous session. These slots are not stored or included in indicators, and generation pauses at the session close until another real bar arrives.

Children: [application source](src/README.md), [mock engine](mock-engine/README.md), [E2E](e2e/README.md), [test support](test/README.md). Fixtures/assets/generated output excluded from guide leaves. Commands: `npm test`, `npm run typecheck`, `npm run lint`, `npm run e2e`.
