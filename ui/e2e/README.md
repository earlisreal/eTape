# End-to-End Tests

Playwright scenarios exercise the browser against controlled engine/demo state.
The launcher creates a fresh temporary `server` profile for every run; it never
reads or writes `%USERPROFILE%\.eTape`. Inputs: fixtures and built services;
outputs: behavioral assertions/screenshots. Avoid live venues and
nondeterministic external feeds. Run: `npm run e2e`. The isolated ticketless
cross-window scenario uses the same deterministic sim-only harness:
`npm run e2e:ticketless`.

The Wails server capability path is exercised by the engine-owned integration
test, not by the legacy `serve.mjs` launcher:
`go test -tags "wails,server" ./cmd/etape`. That test uses the same generated
bindings and `etape.runtime` handler on a loopback random port, an isolated
temporary profile, and binding-level `EnginePhase=ready` after HTTP health.
Browser Playwright coverage for reconnect, snapshot-before-delta, reload, and
imperative-store routing is a merge-gate check and must use this Wails server
path; it must not silently fall back to the legacy `/ws` listener.
