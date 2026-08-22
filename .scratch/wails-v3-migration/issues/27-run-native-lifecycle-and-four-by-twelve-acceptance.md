# 27 — Run native lifecycle and four-by-twelve acceptance

**What to build:** Prove the packaged Wails application meets its native lifecycle, order-safety, transport, installer, and same-machine performance gates with four Workspaces containing twelve representative Panels each.

**Blocked by:** 01 — Record browser baseline and isolate migration profiles; 26 — Ship the per-user NSIS installer.

**Status:** ready-for-agent

- [ ] The complete documented CI-equivalent Windows checklist passes for engine, race, vet, lint, UI, generated contracts, server-tag tests, Wails server end-to-end tests, native build, packaging, and diff cleanliness; every skipped required check has a recorded reason.
- [ ] Four simultaneous Native Windows with twelve representative Panels each preserve Dockview identities, snapshot-before-delta, lossless FIFO, latest-wins convergence, imperative rendering behavior, and isolated bounded slow clients.
- [ ] Alt+Tab, click focus, minimize/restore, blank Workspace, native dialog, React modal, editable controls, close, and another foreground application produce zero background or stale simulated or paper execution.
- [ ] Top Bar drag and no-drag regions, caption controls, every resize edge/corner, keyboard accessibility, Alt+F4, Alt+Space, Win+Arrow, Win+Z, Dockview drag, and cross-monitor movement pass at 100%, 125%, 150%, 200%, and mixed-monitor DPI where applicable.
- [ ] Intentional restart, forced crash, restore acceptance and decline, missing/reordered monitor, corrupt/off-screen geometry, last-window-to-tray, reopen, second launch, and Quit match the approved lifecycle contract without duplicate identities or crash loops.
- [ ] Fresh install, upgrade, launch, WebView2-present and missing-runtime paths, uninstall, and reinstall pass on clean standard-user Windows 11 x64 environments with user data preserved.
- [ ] One hundred WebView reload/start-stop cycles and one hundred whole-process restarts produce zero leaked handlers or demands, writes after store close, ghost trays, duplicate windows, lost restart intent, stale processes, or database-lock collisions.
- [ ] The final test repeats the browser baseline on the same machine, fixture, display setup, warm-up, and three-run protocol and records raw method, hardware, startup, latency, CPU, memory, frame, queue, and recovery measurements.
- [ ] p95 bridge-to-store and order-intent-to-result latency is no more than ten percent above baseline unless the measured difference is explicitly accepted.
- [ ] Steady CPU and working-set memory are no more than twenty percent above baseline, with no monotonic growth across repeated window cycles.
- [ ] Any lossless gap, ordering violation, unexpected disconnect, leaked demand, background execution, write after close, unrecoverable blank WebView, restoration loop, corruption, or unbounded memory growth blocks completion.
- [ ] Performance and validation documentation contains results, conclusions, evidence locations, and every accepted exception; no real order is placed, modified, or cancelled.
