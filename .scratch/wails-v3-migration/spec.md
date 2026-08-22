# Wails v3 Native Desktop Migration

Status: ready-for-agent

## Problem Statement

eTape currently presents its desktop experience through a localhost HTTP and
WebSocket server opened in an external browser. Workspaces are coordinated as
browser windows, while cross-window state relies on browser facilities such as
BroadcastChannel, Web Locks, local storage, and generic configuration messages.
That arrangement makes native window lifecycle, focus-sensitive order safety,
durable close behavior, crash recovery, file dialogs, installation, and
multi-window ownership harder to reason about as one application.

The trader wants eTape to behave as one Windows desktop application without
giving up the existing Workspace and Dockview model. Each Workspace should have
one manageable Native Window, closing a window must not delete the Workspace,
the engine should remain available from the tray, and restart or crash recovery
must restore only safe, durable state. The migration must preserve market-data
ordering, imperative rendering performance, existing configuration and
database contents, and the execution gates that prevent an unfocused or stale
window from acting on an order hotkey.

The maintainer also needs one supportable runtime rather than a permanent
browser/native split. Commands and queries need explicit typed APIs, continuous
traffic needs bounded and testable delivery, shared state needs one canonical
owner, and shutdown must not let an in-flight native call write after storage
has closed. The result must be reproducible, testable in CI and on native
Windows, recoverable through verified backups, and distributable as a normal
per-user installer.

## Solution

Replace the browser-hosted product runtime with a Windows 11 x64 Wails v3
application pinned initially to v3.0.0-beta.11. One Native Window hosts one
Workspace, while Dockview remains inside that window and continues to own Panel
Group layout. Closing a Native Window preserves the Workspace, and closing the
last visible window leaves the process and engine available through the Wails
system tray. Native Panel or Panel Group detachment remains deferred.

Use explicit generated Wails bindings for discrete commands and queries, one
Wails Stream per Workspace WebView for subscriptions and continuous or targeted
traffic, and ordinary Wails events only for low-rate app-wide lifecycle and
revision hints. Preserve the existing UI Hub ordering, snapshot, outbox,
coalescing, and cleanup rules behind a Wails adapter. Keep the existing Go DTOs
and tygo generation as the Stream schema while Wails generates service models
and bindings.

Make Go authoritative for application lifecycle, the Workspace catalog and
documents, Native Window registry, global Link Group focus, Chart Drawing
operations, durable preferences, revisions, and focused-order capabilities.
Keep React, Dockview, imperative stores, canvas and chart controllers,
animation scheduling, forms, DOM focus checks, and local modal presentation in
each WebView. Put every binding and Stream handler behind one application-owned
admission and in-flight gate so shutdown joins native work before the existing
engine drain and store close.

Preserve `%USERPROFILE%\.eTape` through a verified, additive migration and
rollback backup. Ship one unsigned per-user NSIS installer under
`%LOCALAPPDATA%\Programs\eTape`, including WebView2 bootstrap handling, Start
menu integration, upgrade, and uninstall that leaves user data intact. The
finished desktop product exposes no eTape localhost listener and no browser
fallback; a loopback Wails server build exists only for automated tests.

## User Stories

### Trader experience

1. As a trader, I want each Workspace to open in its own Native Window, so that I can organize trading contexts independently.
2. As a trader, I want reopening an already-open Workspace to focus its existing Native Window, so that duplicate windows are never created.
3. As a trader, I want closing a Native Window to preserve its Workspace, Panel Groups, Panel identities, settings, and drawings, so that closing a window is not destructive.
4. As a trader, I want Dockview to remain the sole owner of Panel Group layout, so that tabs, Panel Headers, and panel content retain their established behavior.
5. As a trader, I want the final closed Workspace window to leave eTape running in the system tray, so that engine services remain available without visible windows.
6. As a trader, I want the tray to reopen Main, so that I can resume work without starting another engine.
7. As a trader, I want Quit to flush durable state and drain the engine exactly once, so that leaving the application cannot corrupt storage.
8. As a trader, I want a second application launch to focus Main in the existing process, so that duplicate engines cannot contend for my data.
9. As a trader, I want intentional application restart to restore every previously open Workspace, so that a planned restart returns my desktop context.
10. As a trader, I want intentional restart to restore each window's valid monitor, normal bounds, and maximised state, so that the desktop returns where I left it.
11. As a trader, I want minimized state excluded from restoration, so that restored Workspaces are visible and usable.
12. As a trader, I want an unclean exit to open Main only and offer an explicit restore choice, so that eTape cannot enter an automatic crash loop.
13. As a trader, I want to decline crash restoration without losing saved Workspaces, so that recovery remains under my control.
14. As a trader, I want one corrupt Workspace layout quarantined independently, so that it cannot prevent other Workspaces from opening.
15. As a trader, I want missing or invalid monitor geometry clamped into the current work area, so that restored windows are always reachable.
16. As a trader, I want the frameless Top Bar to provide accessible minimise, maximise or restore, close, drag, and resize behavior, so that the custom shell remains practical.
17. As a trader, I want interactive Top Bar controls and Dockview surfaces excluded from Native Window drag regions, so that controls and Panel Group manipulation remain reliable.
18. As a trader, I want normal Windows keyboard and snapping behavior such as Win+Arrow and Win+Z, so that the frameless application still fits standard desktop workflows.
19. As a trader, I want multiple Workspace windows to remain independently selectable through Alt+Tab, so that switching trading contexts remains predictable.

### Workspace data and shared state

20. As a trader, I want Workspace documents saved through one canonical Go owner with revisions, so that competing windows cannot lose catalog or layout changes.
21. As a trader, I want ordinary Workspace saves to remain responsive, so that persistence does not make layout editing feel sluggish.
22. As a trader, I want close, quit, restart, and mode-transition acknowledgements to mean the SQLite transaction committed, so that acknowledged state survives immediate termination.
23. As a trader, I want a close request to wait for the current Dockview document to save, so that an immediate Alt+F4 does not lose my last layout edit.
24. As a trader, I want an explicit force-close choice when a renderer is hung, so that one broken WebView cannot trap the entire engine process.
25. As a trader, I want Workspace layout documents treated as bounded opaque data by Go, so that Dockview remains the only layout interpreter.
26. As a trader, I want layout version 8, Panel identities, settings, and catalog entries preserved during migration, so that my existing Workspaces continue to mean the same thing.
27. As a trader, I want red, green, blue, and yellow Link Group focus shared globally across Workspaces, so that linked Panels follow one symbol and venue per group.
28. As a trader, I want Panel Link Group membership to remain part of its Workspace document, so that layout membership and global focus retain distinct scopes.
29. As a trader, I want global Link Group focus persisted across restart, so that linked Panels resume their prior context.
30. As a trader, I want Main to win deterministic legacy Link Group conflicts during migration, so that conversion never chooses an arbitrary window.
31. As a trader, I want missing or malformed Main Link Group state to become an empty group rather than another Workspace's value, so that migration fails safely.
32. As a trader, I want Chart Drawings persisted as revisioned operations, so that concurrent edits do not overwrite unrelated drawings.
33. As a trader, I want drawing upsert, remove, and clear operations reconciled across Workspace windows, so that each WebView converges on accepted drawing state.
34. As a trader, I want theme, sound, order settings, Drawing Tool Style, and durable hints persisted through typed settings, so that preferences survive restarts.
35. As a trader, I want Scanner Sync and Monitoring Workspace coordination to consume canonical revisioned state, so that cross-window scanner behavior remains consistent.
36. As a trader, I want minor browser-only dismissal flags allowed to reset, so that obsolete browser storage does not hold back the native migration.
37. As a trader, I want demo and replay transitions to journal my live Workspace documents, Link Group focus, mode, and phase before restart, so that returning live restores the exact prior state.
38. As a trader, I want mode-transition recovery to be idempotent after a crash at any phase, so that live and demo state can never become mixed.

### Market data and native transport

39. As a trader, I want discrete commands and queries exposed as explicit typed operations, so that application behavior no longer depends on string command names or untyped results.
40. As a trader, I want expected business outcomes returned as typed accepted, blocked, or ambiguous data, so that a normal rejection is distinguishable from a bridge failure.
41. As a trader, I want ambiguous execution outcomes never retried automatically, so that an unknown broker result cannot become a duplicate order.
42. As a trader, I want one Workspace Stream per WebView, so that subscriptions, symbol demands, indicators, snapshots, and updates belong to the correct Workspace session.
43. As a trader, I want a newly attached or reloaded WebView to receive fresh snapshots before deltas, so that it starts from a valid market and account baseline.
44. As a trader, I want lossless Stream frames delivered FIFO without silent loss or reordering, so that order, fill, tape, status, and other lossless state remains trustworthy.
45. As a trader, I want latest-wins topics to converge to their newest sequence under load, so that quotes, books, bars, accounts, positions, scanner ranks, and health do not remain stale.
46. As a trader, I want one stalled Workspace Stream bounded and isolated, so that a slow renderer cannot block the Hub or another Workspace.
47. As a trader, I want reload and close to release subscriptions, demands, indicators, watchers, and backfill ownership exactly once, so that repeated use does not leak resources.
48. As a trader, I want explicit stop and restart meaning preserved across Stream closure, so that the frontend can distinguish lifecycle transitions from an unexplained disconnect.
49. As a trader, I want targeted shared-state notifications delivered through the owning Workspace Stream, so that correctness does not depend on a global event queue.
50. As a trader, I want revisioned app-wide hints to recover by snapshot after a missed event, so that lossy lifecycle notification cannot corrupt state.
51. As a trader, I want the UI to tolerate a Stream update arriving before or after its binding result, so that independent native lanes cannot produce stale projections.
52. As a trader, I want high-frequency data to remain outside ordinary Wails events and React state, so that native migration preserves imperative rendering performance.
53. As a trader, I want chart, tape, scanner, account, and execution projections to retain their current store and canvas behavior, so that moving the host does not redesign trading surfaces.

### Order safety

54. As a trader, I want order hotkeys to remain application-scoped, so that no system-wide shortcut can submit an order outside eTape.
55. As a trader, I want only the OS-focused Native Window's active eligible Panel to own a scoped order action, so that a background Workspace cannot act on my keystroke.
56. As a trader, I want a focused Workspace with no eligible Panel to block symbol-scoped and risk-increasing actions, so that the globally last-clicked Panel is never a fallback.
57. As a trader, I want background, minimized, closed, stale, or interaction-blocked windows rejected immediately before execution, so that focus races fail closed.
58. As a trader, I want editable controls, type-to-load, Deck interactions, and React modals to keep their local key filtering, so that typing does not trigger an order action.
59. As a trader, I want native dialogs to suspend order capabilities until they close, so that a dialog cannot leave a stale execution target.
60. As a trader, I want direct order management to require a focused eTape session and the relevant order identity and revision, so that cancel or replace cannot use stale context.
61. As a trader, I want risk-reducing global actions such as Disarm and Kill Switch available from a focused eTape window without a symbol Panel, so that emergency controls remain reachable.
62. As a trader, I want each focus capability tied to a Native Window, Stream session, Panel, generation, and target revision, so that copied or stale identifiers cannot authorize execution.
63. As a trader, I want focus capabilities and hotkey targets excluded from persistence and restoration, so that restart cannot revive execution authority.
64. As a trader, I want each process launch to begin disarmed, so that crash or restart restoration cannot silently arm trading.
65. As a trader, I want any initial Arm or auto-unlock policy owned once per process, so that restoring four Workspace windows cannot issue four requests.
66. As a trader, I want focusing another application to invalidate eTape order capabilities, so that order hotkeys do nothing while I work elsewhere.
67. As a trader, I want native caller identity verified rather than trusting a JavaScript Workspace identifier, so that one WebView cannot impersonate another.
68. As a trader, I want migration validation restricted to demo, replay, simulated, paper, or read-only live-data modes, so that no real order is placed, modified, or cancelled without separate authorization and reconfirmation.

### Native operating-system integration

69. As a trader, I want Workspace import and export to use dialogs owned by the calling Native Window, so that file operations have clear desktop context.
70. As a trader, I want file imports bounded and schema-validated before application, so that malformed or oversized input cannot corrupt state.
71. As a trader, I want exported settings, trades, and images written atomically, so that a failed write does not leave a misleading partial file.
72. As a trader, I want validated HTTP and HTTPS news links opened in the system browser, so that remote content never receives eTape bindings.
73. As a trader, I want complex text-entry and settings forms to remain useful React interfaces, so that native integration does not replace good application UI with shallow dialogs.
74. As a trader, I want the installed application to open no browser product window, console, or localhost listener, so that eTape behaves as one native desktop product.

### Installation, migration, and rollback

75. As a trader, I want the supported installer to run per-user without administrator rights, so that installing eTape does not require machine-wide access.
76. As a trader, I want the executable installed under my LocalAppData application directory, so that the first native release has a predictable per-user location.
77. As a trader, I want all configuration, credentials, and databases to remain under my eTape user-data directory, so that executable location does not move or expose my state.
78. As a trader, I want install, upgrade, and uninstall to preserve my eTape user-data directory, so that application maintenance is not destructive.
79. As a trader, I want migration to create and verify a consistent timestamped backup before writers or migration begin, so that rollback has a trustworthy source.
80. As a trader, I want migration to preserve credentials and engine or store data without logging sensitive payloads, so that conversion does not leak private information.
81. As a trader, I want a failed migration to abort startup without marking success or silently resetting data, so that the prior profile remains usable.
82. As a trader, I want migration to be idempotent, so that retrying after an interruption does not apply changes twice.
83. As a trader, I want a clean reset to require explicit confirmation and another backup, so that recovery cannot erase state automatically.
84. As a trader, I want rollback documented as installing the prior build and restoring the verified backup when necessary, so that reverting is a deliberate supported procedure.
85. As a trader, I want WebView2 bootstrap handling included in the installer, so that a missing runtime has a guided installation path.
86. As a trader, I want an offline missing-WebView2 failure to be actionable and leave no partial installation, so that installation failure is recoverable.
87. As a trader, I want Start menu integration and predictable upgrade and uninstall behavior, so that eTape behaves like a normal Windows application.
88. As a trader, I want the initial unsigned SmartScreen warning documented, so that the personal-use release has an honest security expectation.

### Maintainer, developer, and release outcomes

89. As a maintainer, I want one concrete application lifecycle owner, so that startup, admission, ordered drain, store close, and restart have one authority.
90. As a maintainer, I want shutdown to reject new bindings and Streams before joining admitted work, so that no native call can write after storage closes.
91. As a maintainer, I want RestartApplication to return before asynchronous shutdown begins, so that the initiating binding cannot deadlock on its own admission gate.
92. As a maintainer, I want replacement-process launch to occur after Wails releases its single-instance resources, so that restart cannot collide with the old process.
93. As a maintainer, I want Wails single-instance activation supplemented by the existing data-root and database integrity lock, so that focus behavior cannot weaken storage safety.
94. As a maintainer, I want boot failure visible through native diagnostics rather than a ghost tray process, so that startup failure is supportable.
95. As a developer, I want Wails adapted at the existing UI Hub connection seam, so that proven snapshot, ordering, outbox, coalescing, and cleanup behavior is reused.
96. As a developer, I want Wails transport buffers to have immutable ownership and bounded queues, so that asynchronous sends cannot corrupt data or hide unbounded stale frames.
97. As a developer, I want explicit EngineService and WorkspaceService operations, so that the final application has no generic command, query, configuration, or correlation-ID bridge.
98. As a developer, I want Go to remain the source of truth for service models and Stream DTOs, so that contract ownership stays explicit.
99. As a developer, I want Wails bindings and tygo Stream contracts regenerated and checked together, so that committed TypeScript cannot drift from Go.
100. As a developer, I want generated TypeScript treated as read-only, so that contract changes occur only at their source.
101. As a developer, I want Wails module, CLI, frontend runtime, and build assets pinned to one reviewed beta, so that global or latest tooling cannot change behavior unexpectedly.
102. As a developer, I want every Wails beta upgrade isolated and followed by the full native suite, so that API drift cannot hide inside feature work.
103. As a developer, I want development, replay, prototypes, server tests, and automated migration tests to default to isolated data roots, so that they cannot mutate the real user profile.
104. As a developer, I want the Wails server test build to use the same services and Stream handler as the native product, so that Playwright does not validate a parallel legacy architecture.
105. As a developer, I want native Windows smoke tests in addition to server tests, so that focus, WebView2, tray, dialogs, geometry, DPI, and installer behavior are proved on the real host.
106. As a developer, I want existing Go, UI, generated-contract, execution-risk, and end-to-end checks retained, so that migration does not lower the repository's validation floor.
107. As a release operator, I want the supported artifact limited to one Windows 11 x64 per-user NSIS installer, so that unsupported binaries are not mistaken for releases.
108. As a release operator, I want raw executables and the Wails server build retained only for development and CI, so that users receive one supported installation path.
109. As a release operator, I want the release workflow to use pinned Windows-native tooling, so that Wails and NSIS packaging is reproducible.
110. As a release operator, I want the packaged product to contain no legacy browser host, HTTP server, WebSocket endpoint, browser window coordination, or Fyne tray, so that only one desktop runtime ships.
111. As a release operator, I want a recorded browser-host baseline compared with four Wails Workspaces containing twelve representative Panels each, so that capacity and regressions are measured rather than assumed.
112. As a release operator, I want p95 bridge and order-result latency no worse than ten percent above baseline unless explicitly accepted, so that native migration does not silently slow critical interaction.
113. As a release operator, I want CPU and working-set memory no worse than twenty percent above baseline with no monotonic growth, so that multi-window native hosting remains bounded.
114. As a release operator, I want one hundred reload or start-stop cycles and one hundred whole-process restarts without leaks or lock collisions, so that lifecycle stability is demonstrated.
115. As a release operator, I want an eight-hour deterministic demo soak plus full-session read-only live-data and paper evidence, so that disconnect, sleep, restart, and crash recovery are exercised safely.
116. As a release operator, I want hosted CI, native smoke evidence, installer checks, performance measurements, rollback instructions, and known beta limitations presented together, so that final cutover remains an explicit approval decision.

### Reserved Workspace behavior

117. As a trader, I want Main and Monitoring to retain their reserved identities and existing special behavior, so that native window migration does not turn them into ordinary user-created Workspaces.
118. As a trader, I want closing a Workspace window separate from deleting the Workspace and deletion rejected while that Workspace is open, so that window lifecycle cannot accidentally erase active state.

## Implementation Decisions

- Use the glossary terms **Workspace**, **Native Window**, **Panel Group**, **Panel Header**, **Top Bar**, **Link Group**, **Monitoring Workspace**, **Scanner Sync**, and **Drawing Tool Style** with their established meanings.
- Pin the Wails Go module, CLI, generated runtime, frontend runtime, and build assets to compatible v3.0.0-beta.11 versions. Never resolve a build or generator from `latest`. Treat every Wails upgrade as a separate reviewed change followed by the complete suite.
- Support Windows 11 x64 only for the first Wails release.
- Give each Workspace one primary Native Window. Opening an already-open Workspace focuses its existing window. Closing a window never deletes its Workspace.
- Preserve Main and Monitoring's existing special Workspace semantics. Monitoring remains protected from rename and deletion while its Panel Groups remain editable. Keep Workspace deletion separate from window closing and reject deletion while that Workspace is open.
- Keep Dockview as the sole Panel Group layout and panel-mounting owner. Keep React, imperative stores, Scheduler, canvas and chart controllers, forms, DOM focus handling, toasts, and local modal state inside each WebView.
- Keep high-frequency data out of React state and ordinary Wails events.
- Fully replace the browser product host. The final desktop build has no eTape localhost HTTP or WebSocket listener, browser fallback, or hidden compatibility mode.
- Keep a test-only Wails server variant for Playwright. It binds loopback only, uses an isolated data root, shares the product services and Stream handler, and is absent from product artifacts.
- Keep the composition root responsible for constructing the engine, Wails application, services, UI Hub, and orderly shutdown.
- Introduce one concrete engine lifecycle owner rather than a generic host abstraction. It starts asynchronously, owns transport admission and in-flight work, invokes the existing ordered engine drain, and closes storage exactly once. It is not restartable in-process.
- Put every generated binding and Stream handler behind the lifecycle admission gate. Stopping rejects new work, revokes capabilities, cancels long-running work, joins admitted calls and handlers, and only then drains the engine and closes storage.
- Make RestartApplication record intent and return its binding result before asynchronous quit begins. Spawn the replacement process only after Wails releases process and single-instance resources.
- Let Wails single-instance signalling focus Main, but retain the resolved data-root and database lock as the data-integrity authority. Isolated test or replay data roots may run independently.
- Keep the existing narrow embedded-distribution filesystem API for Wails assets. Reuse the current build-time UI distribution copy and embedding pattern; remove HTTP-specific assumptions without forcing an otherwise unnecessary package rename.
- Add a concrete desktop host responsible for the Native Window registry, frameless shell, tray, focus events, geometry, clean or unclean run markers, restart restoration, dialogs, external URLs, and Windows-specific lifecycle.
- Keep one small EngineService for engine commands and queries and one WorkspaceService for low-rate Workspace and application operations. Do not create one service per feature.
- Use explicit generated binding methods for commands and queries. Remove the generic name dispatcher, generic configuration binding, correlation-ID protocol, and generic frontend command/query client from the final state.
- Return expected business outcomes as typed data and reserve rejected binding Promises for internal or bridge failures. Preserve accepted, blocked, ambiguous, reason, deferred, broker-identity, and idempotency semantics. Never retry ambiguous execution automatically.
- Return a resulting revision from shared-state mutations. Bindings, Streams, and events have no total cross-lane order, so frontends ignore stale projections and tolerate updates arriving before or after method results.
- Use one Wails Stream per Workspace WebView for subscription control, symbol and indicator demand, snapshots, continuous updates, targeted invalidation, and session health.
- Require the Stream's first frame to declare protocol version, Workspace, and an opaque session nonce. Desktop builds validate it against the Native Window registry; server builds validate it against an isolated test registry. A query string or JavaScript Workspace identifier is never authoritative.
- Adapt Wails at the existing UI Hub connection boundary. Retain the current mirror, snapshot-before-delta behavior, outbox, lossless and latest-wins classifications, coalescing, and per-session cleanup rather than reimplementing them.
- Treat Wails' internal queue as transport buffering rather than the business delivery policy. Copy or transfer ownership of each sent buffer, preserve an explicit stop or restart protocol frame, keep Hub execution non-blocking, and bound the combined eTape and Wails queues.
- Release every Stream-owned subscription, symbol demand, indicator, watcher, and backfill exactly once on close or reload. Track handler lifetime in the application admission gate rather than relying on Wails cleanup order.
- Treat Wails events as lossy app-wide hints even when emitted from a window. Include identity and monotonic revision, emit only through a bounded coalescing desktop dispatcher, and never invoke them from engine, Hub, store-writer, or order-critical goroutines. Use the owning Stream when targeting matters.
- Make Go canonical for the Workspace catalog, latest Workspace documents, Native Window set, geometry, global Link Group focus, Chart Drawing operations, durable preferences, focus capabilities, and revisions.
- Serialize Workspace catalog mutations. Keep Dockview layout JSON opaque and size-bounded, with layout version 8 interpreted only by the frontend.
- Allow ordinary saves to update Go memory immediately and debounce disk persistence. For close, quit, restart, or mode change, acknowledge only after the SQLite transaction commits.
- Hold the first native close request while the frontend serializes and durably saves its Dockview document. Complete close through an explicit method. If the renderer does not respond within a bounded interval, offer a visible force-close path rather than claiming a successful save.
- Persist open Workspace identities, display identity, normal bounds, and maximised state. Never restore minimized state. Clamp corrupt, missing-monitor, negative-coordinate, taskbar-work-area, and off-screen geometry into a usable display.
- Mark a run unclean before creating Workspace windows. Mark clean or intentional restart only after durable state flush. An intentional restart restores every valid prior window; an unclean launch opens Main only and offers restore or decline. Validate and quarantine saved Workspace documents independently.
- Make global red, green, blue, and yellow Link Group symbol and venue focus serialized, revisioned, validated, and persisted in Go. Preserve Panel Link Group membership inside each Workspace document.
- During migration, seed global Link Group focus from valid Main values. When Main is missing or malformed, use an empty group rather than another Workspace. Log conflicts without symbols, credentials, account data, or sensitive payloads.
- Store Chart Drawings as operation-based upsert, remove, and clear mutations with per-symbol revisions. WebViews may remain optimistic projections but reconcile accepted Go state.
- Move the explicit durable preference inventory—theme, order settings, sound, Drawing Tool Style, and durable hints—behind typed persistence methods. Browser-only dismissal flags may reset; local forms and temporary modal state remain local.
- Replace cross-window BroadcastChannel, Web Locks, durable local-storage coordination, and browser window naming with canonical Go state, generated bindings, revisioned projections, and Native Window operations.
- Add a transactional demo and replay transition journal containing pre-transition Workspace documents, global Link Group focus, mode, and phase. Commit it before restart, recover idempotently at every phase, and expose mode-change methods only through the journal.
- Make OS focus events authoritative for the focused Native Window. Let a WebView report its active eligible Panel and issue an opaque capability bound to its Stream session, window generation, Panel, and target revision.
- Authorize execution by risk. Hotkey-origin and symbol-scoped or risk-increasing actions require the current focused Panel capability. Direct order management requires a focused application session plus the relevant order identity and revision. Disarm and Kill Switch remain available from a focused eTape window without a symbol Panel.
- Verify native caller identity and synchronous OS focus immediately before execution. Reject background, minimized, closed, stale-generation, stale-capability, blank, modal-blocked, or otherwise interaction-blocked callers. Never trust a bare Workspace identifier.
- Keep focus capabilities ephemeral and unpersisted. Start each launch without a restored hotkey target and disarmed. Own any initial Arm or auto-unlock policy once per process rather than once per WebView.
- Keep native dialogs and file operations attached to the calling Native Window. Suspend order capabilities while a native dialog is open. Use bounded reads, schema validation, and atomic writes; do not expose generic filesystem access to JavaScript.
- Retain React for rename, settings, practice, venue, and other complex text-entry forms where native dialogs provide no benefit.
- Accept only validated HTTP and HTTPS external URLs and open them in the system browser. Never navigate a privileged eTape WebView to remote content.
- Preserve `%USERPROFILE%\.eTape` as the user-scoped configuration, credentials, and database location independently of where the executable is installed.
- Default development, tests, prototypes, replay, and server mode to isolated data roots. Access to the real user profile requires an explicit migration run.
- Acquire the data-root and database locks before migration. Before starting normal writers or applying changes, create and verify a timestamped consistent backup using a closed-store or SQLite backup path rather than raw-copying an active WAL database.
- Keep migration additive and versioned. Preserve Workspace layout version 8, Panel identities, settings, drawings, catalog entries, credentials, and engine or store data. Write the migration marker only after every step succeeds.
- Abort migration on validation, backup, or transaction failure without mutating or silently resetting source data. A clean reset is a separate confirmed recovery action that creates another backup first.
- Support rollback before cutover by leaving main unchanged. After release, rollback uses the prior installer and the verified pre-migration backup when the data version requires it; there is no browser-mode fallback.
- Use a frameless shell with lightweight drag and resize regions, accessible caption controls, and explicit no-drag regions on every interactive Top Bar and Dockview surface. Keep experimental WebView2 composition hosting disabled; native hover Snap Layouts on the custom maximise control are not required.
- Package only an unsigned, per-user Windows 11 x64 NSIS installer under `%LOCALAPPDATA%\Programs\eTape`. Include WebView2 bootstrap handling, version metadata, Start menu integration, upgrade, and uninstall that preserves `%USERPROFILE%\.eTape`.
- Keep the raw executable and Wails server build as development or CI artifacts only. Do not publish a portable ZIP, server build, macOS artifact, or raw developer executable.
- Remove the legacy browser host, product HTTP and WebSocket server, browser lifecycle flags, browser window coordination, Fyne tray, transport-only configuration, generic command envelopes, and unreachable compatibility code only after native parity gates pass. Remove a shared networking dependency only if no broker adapter still imports it.
- Build and package on Windows with pinned Go, Node, Wails, and NSIS tooling. Regenerate both Wails service bindings and tygo Stream DTOs in CI and fail on committed drift. Generated TypeScript is read-only.
- Update every affected architecture, engine, UI, wire, configuration, build, release, performance, script, and agent guide when its flow, interface, dependency, invariant, or operation changes.
- Perform the work only in the isolated Wails migration worktree and branch until final cutover. The application may be temporarily broken there, but no release or final branch state may contain both product runtimes.

## Testing Decisions

- A good test asserts an externally observable contract: delivered frame order, resulting revision, durable saved state, accepted or blocked business result, visible window behavior, execution count, installer result, or released resource ownership. It must not assert Wails internals, private goroutine structure, React implementation details, or Dockview internals.
- Use the existing UI Hub connection boundary as the highest transport seam. Exercise the Wails adapter through the same session, outbox, ordering, coalescing, and cleanup behavior already covered for the browser transport.
- Use the application admission gate together with the public service and Stream entry points as the highest lifecycle seam. Race new work, long-running work, close, Quit, restart, cancellation, engine drain, and store close; prove no work writes after close and restart cannot deadlock its initiating binding.
- Use the FocusCoordinator and public execution-service boundary as the order-safety seam. Verify caller window, OS focus, session generation, Panel capability, target revision, action origin, and modal state immediately before simulated or paper execution.
- Use public WorkspaceService operations as the shared-state and persistence seam. Test catalog mutation, document revision, durable save acknowledgement, Link Group convergence, drawing operations, preferences, geometry, run markers, and mode-transition recovery without asserting storage implementation details.
- Through the WorkspaceService seam, verify Main and Monitoring retain their reserved behavior, Monitoring rename and delete remain unavailable, close never deletes a Workspace, and deletion of any open Workspace is rejected.
- Reuse existing Go tests for UI Hub sessions, outbox/coalescing, engine shutdown, store behavior, execution gates, idempotency, and broker-result semantics.
- Reuse existing UI tests for React, Dockview, imperative stores, Scheduler, panels, focus and editable-element filtering, modals, drawings, Link Groups, Scanner Sync, and generated-client mocks.
- Prove snapshot-before-delta on initial attach, WebView reload, and reopened Workspace.
- Prove every lossless frame is FIFO with no gap, duplicate, or silent drop, including exact-capacity and overflow behavior.
- Prove each latest-wins key converges to the final revision while interleaved lossless frames retain order.
- Stall one renderer for ten seconds and prove bounded memory, no Hub blockage, no effect on another Workspace, and recovery inside the declared stale-frame budget.
- Verify sent buffer ownership by detecting mutation or reuse after asynchronous send.
- Reload each WebView one hundred times and prove old handlers return and subscription, demand, indicator, watcher, and backfill ownership releases exactly once.
- Run transport and lifecycle tests under the Go race detector and require zero goroutine leaks or store writes after close across one hundred start-stop cycles.
- Verify the first Stream frame rejects malformed protocol, unknown Workspace, stale session, or mismatched Native Window identity without mutating engine state.
- Verify server mode uses its isolated registry because no Native Window is present, while desktop tests verify real caller-window association.
- For every generated service method, test typed round trips, optional values, enum values, business outcomes, internal errors, and resulting revisions. Regeneration of Wails and tygo output must leave a clean tree.
- Preserve existing order risk, disarmed, idempotency, broker acknowledgement, deferred-result, and ambiguity tests. Never retry an ambiguous order result.
- Run at least one thousand adversarial four-window focus transitions across keydown and binding dispatch. Require zero background or stale simulated or paper commands.
- Cover focus moving between eTape windows and another application; minimized, closed, blank, stale-generation, stale-capability, native-dialog, React-modal, and editable-control states; and risk-reducing controls without a symbol Panel.
- Verify each process launch starts disarmed, restores no hotkey target, and issues no more than one initial Arm or auto-unlock request when four Workspaces restore.
- Mutate a Dockview layout or Chart Drawing and immediately invoke Alt+F4, the custom close control, tray Quit, and RestartApplication. Reopening must reproduce the last acknowledged state.
- Force termination immediately after a successful durable save acknowledgement and verify the acknowledged state survives.
- Simulate a hung WebView and verify the timeout offers an explicit force-close path without hanging the engine or falsely reporting a durable save.
- Test intentional restart, normal Quit, forced crash, restore acceptance, restore decline, a corrupt Workspace document, a missing monitor, off-screen and negative geometry, monitor reorder, taskbar work areas, and normal versus maximised bounds.
- Run one hundred whole-process restart cycles and require no ghost tray, duplicate window, lost restore argument, stale process, or database-lock collision.
- Fault-inject every phase of demo or replay entry, restart, recovery, and return live. Require exact restoration of the pre-transition Workspaces and Link Groups with no mixed state and idempotent retry.
- Use redacted migration fixtures for normal, current, already-migrated, corrupt, conflicting-Link-Group, missing-optional-file, backup-failure, disk-failure, and WAL or SHM profiles. Fixtures must contain no real credentials or runtime data.
- Verify migration backup readability and consistency, Main-wins conflict behavior, missing or malformed Main behavior, marker-last semantics, idempotence, restrictive handling of sensitive data, and byte-usable source state after failure.
- Run Playwright against the test-only Wails server using an isolated temporary profile. Cover generated bindings, Stream reconnect, snapshot ordering, Workspace save and load, and canonical shared state.
- Treat server-mode tests as insufficient for WebView2, OS focus, tray, native dialogs, system browser, geometry, DPI, frameless input, and installer behavior. Cover those with packaged Windows 11 native smoke tests.
- Test the frameless shell at 100%, 150%, 200%, and mixed-monitor DPI. Cover every resize edge and corner, caption controls, drag and no-drag regions, double-click maximise where supported, keyboard focus names, Alt+F4, Alt+Space, Win+Arrow, Win+Z, Dockview drag, and cross-monitor movement.
- Test single-instance activation and the independent data-root/database lock so a second launch focuses Main and cannot start a competing engine.
- Test native dialog cancel, validation failure, I/O failure, and success. Verify only HTTP and HTTPS external links reach the system browser and no remote page enters an eTape WebView.
- On clean Windows 11 x64 standard-user virtual machines, test fresh install, launch, upgrade, uninstall, reinstall, WebView2 present, WebView2 missing with network, and WebView2 missing offline. Require no elevation for the per-user path, no partial offline install, and preserved user data.
- Preserve the current CI floor: complete Go, race, vet and lint suites; UI install, lint, test and build; generated-contract checks; and diff cleanliness. Add server-tag tests, Wails server E2E, a Windows native build, and NSIS packaging.
- Capture the browser-host release baseline before deleting it using a fixed Windows 11 x64 machine, deterministic data, symbols, display setup, and twelve representative Panels in each of four Workspaces.
- Use five minutes of warm-up and three fifteen-minute measurement runs. Record startup, bridge-to-store and order-intent latency, process-tree CPU and private memory, frame intervals, queue high-water marks, coalesces, overflows, disconnects, drops, and open-close recovery.
- Block release on any lossless gap, ordering violation, unexpected disconnect, leaked demand, background hotkey execution, store write after close, unrecoverable blank WebView, restoration loop, data corruption, or unbounded memory growth.
- Require p95 bridge-to-store and order-intent-to-result latency no worse than ten percent above the recorded baseline unless explicitly accepted.
- Require steady CPU and working-set memory no worse than twenty percent above baseline with no monotonic growth across repeated window cycles.
- Run an eight-hour deterministic demo soak with four Workspaces and twelve Panels each, scripted focus and layout activity, and complete simulated order lifecycles.
- Run one full regular-hours live-data and simulated-execution session plus one full regular-hours paper-broker session. Exercise disconnect and reconnect, sleep and wake, tray close and reopen, clean restart, and forced crash.
- Do not place, modify, or cancel a real order during migration or validation unless separately authorized and reconfirmed for that current session.
- Before cutover, run the complete Windows CI-equivalent checklist, obtain one green hosted CI run, audit generated and sensitive files, record measurements, and list every skipped required check with its reason.

## Out of Scope

- One Native Window per Panel.
- Native Panel or Panel Group detachment and Dockview popout integration.
- Retaining the localhost HTTP or WebSocket server as a product path.
- A browser product fallback, hidden compatibility switch, or permanently dual runtime.
- Sending high-frequency market, account, position, scanner, or order-critical traffic through ordinary Wails events.
- Moving Dockview, React forms, canvas or chart controllers, animation scheduling, DOM focus, toasts, local modal state, or imperative rendering stores into Go.
- Replacing React text-entry and complex settings forms merely because Wails offers native dialogs.
- Consolidating tygo Stream generation with Wails service generation during this migration.
- Renaming the existing Stream DTO package solely to remove its historical WebSocket name.
- In-process engine restart.
- Restoring minimized windows, hotkey targets, Arm state, modals, pending UI commands, or other unsafe transient state.
- Preserving minor browser-local dismissal flags.
- Native hover Snap Layouts that require experimental WebView2 composition hosting.
- Enabling experimental WebView2 composition hosting.
- Windows 10, Windows ARM64, macOS, Linux, or other platform releases.
- A machine-wide Program Files installer for the first release.
- A public portable ZIP or raw executable release.
- Code signing, automatic update, or broader public-distribution work.
- Publishing the test-only Wails server build.
- Real order placement, modification, or cancellation during validation without separate current-session authorization and reconfirmation.
- Weakening order focus, transport ordering, persistence, or migration safety to work around a Wails beta defect.

## Further Notes

- This specification synthesizes the user-approved [Wails v3 native desktop runtime](../../docs/specs/2026-08-22-wails-v3-native-runtime.md), [ADR 0005](../../docs/adr/0005-replace-browser-host-with-wails-v3.md), and [Wails v3 native desktop migration plan](../../docs/plans/2026-08-22-wails-v3-migration.md). Those documents remain authoritative for rationale and execution order.
- The repository glossary is authoritative for Workspace, Native Window, Panel Group, Top Bar, Link Group, Monitoring Workspace, Scanner Sync, Chart Drawing, and related domain language.
- The two highest test seams are intentionally existing or high-level boundaries: the UI Hub connection boundary for transport behavior, and the application admission plus focus-authorisation boundary for lifecycle and order safety.
- The user confirmed the two test seams by approving the authoritative design and directing synthesis without redesign or another interview. No prototype is required; Phase 0 contains the runnable Wails beta capability checks that must resolve implementation uncertainty before dependent work continues.
- Development occurs in the isolated `codex/wails-v3-migration` branch and Wails migration worktree. Temporary breakage is acceptable there; the original main worktree remains the rollback point until cutover.
- A separate worktree does not isolate runtime data. Development and automated work must continue using an isolated eTape data root until an explicit real-profile migration run.
- No partial product cutover is accepted. The legacy transport may exist temporarily as a branch-local oracle, but the release and final migration state contain one Wails runtime.
- The first release is an unsigned personal-use build and may trigger SmartScreen. Code signing becomes necessary before wider distribution, but it is not part of this specification.
- Installation location and data location remain independent. A later Program Files installer could continue using `%USERPROFILE%\.eTape`, but the accepted first release is per-user under LocalAppData.
- The next workflow step is to split this ready-for-agent specification into blocker-aware tracer-bullet tickets and implement them blockers-first. Order bindings move last, after native transport, persistence, lifecycle, and focus capability gates pass.
