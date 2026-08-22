# 25 — Remove the legacy product host and make CI Wails-native

**What to build:** Delete the browser-hosted product runtime after native parity and make the repository's normal development and CI workflow build and test the Wails product plus its isolated test-only server variant.

**Blocked by:** 07 — Harden Stream parity and the test-only server path; 24 — Contract generic bridges and browser coordination.

**Status:** ready-for-agent

- [ ] Product startup contains no legacy HTTP/static server, WebSocket endpoint, browser opener or adoption lifecycle, Fyne tray, browser restart arguments, or browser-host flags and configuration.
- [ ] The narrow embedded frontend distribution capability remains available to Wails without retaining HTTP-specific product behavior or forcing an otherwise unnecessary package rename.
- [ ] Legacy runtime dependencies and unreachable compatibility code are removed; a shared WebSocket dependency remains only when a repository search proves a broker adapter still requires it.
- [ ] Supported developer entrypoints use repository-pinned Wails tasks and fail clearly when a pinned prerequisite is missing; no global or latest Wails tool silently controls output.
- [ ] CI retains the complete Go test, race, vet, and lint floor; UI install, lint, test, and build; both generated-contract drift checks; and diff cleanliness.
- [ ] CI also tests the isolated server-tag variant, runs Playwright through the real services and Workspace Stream handler, and builds the Windows 11 x64 Wails product on a Windows runner.
- [ ] The test-only server binds loopback with an isolated profile, is enabled only by its build target, and cannot enter a packaged product artifact.
- [ ] A packaged-product check proves there is no eTape product listener, browser window, or console, including the former default port.
- [ ] Architecture, engine, UI, wire, configuration, build, script, performance, and agent guides are updated wherever ownership, commands, dependencies, invariants, or operations changed.
- [ ] A repository-wide stale-claim audit leaves browser host, WebSocket route, old flags, portable archive, and cross-platform release wording only where explicitly historical or test-server-specific.
- [ ] A clean checkout regenerates both contracts and passes the documented CI-equivalent Windows workflow with the product and server variants using pinned tools.
