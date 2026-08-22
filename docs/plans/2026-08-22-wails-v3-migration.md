# Wails v3 native desktop migration

Status: Approved on 2026-08-22.

Specification: [Wails v3 native desktop runtime](../specs/2026-08-22-wails-v3-native-runtime.md).
Decision: [ADR 0005](../adr/0005-replace-browser-host-with-wails-v3.md).

## Goal

Replace eTape's localhost HTTP/WebSocket and external-browser product runtime
with a Windows 11 x64 Wails v3 desktop application. Preserve the engine, market
projection and rendering invariants while moving commands, queries, continuous
traffic, Native Windows, cross-window state, native I/O, lifecycle, testing and
release packaging onto the accepted Wails-native boundaries.

Work takes place in the isolated worktree at
C:\Users\ching\Programs\eTape-wails-v3 on branch
codex/wails-v3-migration. Main remains the rollback point until every gate in
this plan passes.

## Non-goals

- Do not create one Native Window per Panel or implement native Dockview popouts.
- Do not retain a localhost server, WebSocket product path, browser fallback,
  portable ZIP, Program Files installer, or parallel legacy desktop host.
- Do not send high-frequency market traffic through ordinary Wails events.
- Do not move Dockview, React forms, canvas controllers, animation scheduling,
  DOM focus handling, or imperative rendering stores into Go.
- Do not consolidate tygo and Wails generation during this migration.
- Do not add in-process engine restart, code signing, auto-update, Windows 10,
  ARM64, macOS, or Linux packaging.
- Do not place, modify, or cancel a real order. Validation is demo, replay,
  sim, live-data/read-only, or paper only unless a separate current-session
  authorization and reconfirmation explicitly permits a live leg.

## Current-code evidence

- [engine/cmd/etape/main.go](../../engine/cmd/etape/main.go) composes the
  complete engine, constructs uihub, starts the HTTP server, opens the browser,
  and owns the ordered drain. The Wails event loop cannot simply wrap this
  blocking function without separating composition, readiness, and shutdown.
- [engine/cmd/etape/run_tray.go](../../engine/cmd/etape/run_tray.go) and
  [engine/internal/openbrowser](../../engine/internal/openbrowser/README.md)
  implement the current Fyne tray and owned-browser lifecycle that Wails will
  replace.
- [engine/internal/uihub/server.go](../../engine/internal/uihub/server.go) is
  the HTTP/WebSocket/static shell. [hub.go](../../engine/internal/uihub/hub.go),
  [conn.go](../../engine/internal/uihub/conn.go), and
  [coalesce.go](../../engine/internal/uihub/coalesce.go) also contain valuable
  transport-independent snapshot, session, outbox, and coalescing behavior.
- [engine/internal/uihub/commands.go](../../engine/internal/uihub/commands.go)
  and [query.go](../../engine/internal/uihub/query.go) dispatch JSON names for
  every command/query. Their typed domain calls and tests are the parity source
  for explicit Wails methods.
- [engine/internal/uihub/wsmsg](../../engine/internal/uihub/wsmsg) and
  [engine/tygo.yaml](../../engine/tygo.yaml) own the current Go-to-TypeScript
  stream DTO contract and hand-tuned discriminated unions.
- [ui/src/App.tsx](../../ui/src/App.tsx) constructs one WsClient, store set,
  Scheduler, DemandRegistry, LinkGroups, and drawing bus for each browser
  document. [WsClient.ts](../../ui/src/wire/WsClient.ts) owns reconnect,
  correlation, subscription, and message routing.
- [ui/src/chrome/workspace.ts](../../ui/src/chrome/workspace.ts),
  [catalogs.ts](../../ui/src/chrome/catalogs.ts), and
  [windows.ts](../../ui/src/chrome/windows.ts) persist Workspaces and coordinate
  named browser windows using config, BroadcastChannel, Web Locks, localStorage,
  and window.open.
- [ui/src/chrome/linkGroups.ts](../../ui/src/chrome/linkGroups.ts),
  [hotkeyTarget.ts](../../ui/src/chrome/hotkeyTarget.ts), and
  [drawings/store.ts](../../ui/src/render/chart/drawings/store.ts) implement
  browser-to-browser state protocols that must acquire one Go authority.
- [ui/src/chrome/AppShell.tsx](../../ui/src/chrome/AppShell.tsx) and
  [PanelFrame.tsx](../../ui/src/chrome/PanelFrame.tsx) are the Dockview,
  Panel Group, local focus, type-to-load, and local modal boundary that remains
  inside each WebView.
- Workspace and drawing persistence currently debounce in the frontend, so an
  immediate native close cannot rely on React cleanup or a queued SetConfig ACK
  as proof of durability.
- The current demo transition keeps pre-demo Workspace state in an AppShell
  React ref. A whole-process Wails restart therefore requires a durable mode-
  transition journal before StartDemo/Go Live is migrated.
- [engine/Makefile](../../engine/Makefile) currently copies ui/dist into
  internal/webui and cross-compiles CGO-free portable artifacts.
  [.github/workflows/release.yml](../../.github/workflows/release.yml) builds
  Windows and macOS archives on Linux. Wails requires a different application
  build and Windows NSIS release path.
- [ui/playwright.config.ts](../../ui/playwright.config.ts) and
  [ui/e2e](../../ui/e2e/README.md) boot the legacy engine/static server.
  Accepted server-mode tests must use the Wails test build instead.

## Locked design decisions

1. One primary Native Window per Workspace; Dockview remains inside.
2. Selected native Panel/Panel Group detachment is deferred.
3. Wails fully replaces the product host; breakage inside this worktree is
   acceptable during migration.
4. Generated bindings carry discrete requests, one Wails Stream per WebView
   carries continuous/session and targeted traffic, and Wails events are only
   low-rate app-wide lifecycle/invalidation hints.
5. Go becomes canonical for cross-window/persisted coordination while the
   WebView retains view-local and high-frequency rendering state.
6. Whole-process restart restores open Workspace windows; unclean restart opens
   Main only.
7. The final window is frameless with lightweight non-client drag regions;
   experimental WebView2 composition hosting stays off.
8. Existing ~/.eTape data is backed up and migrated; Main wins legacy Link
   Group conflicts; minor browser-only dismissal flags reset.
9. App-scoped hotkeys target only the OS-focused Native Window. Background
   fallback and global shortcuts are forbidden.
10. Windows 11 x64, unsigned per-user NSIS under LocalAppData, four Workspaces
    by twelve Panels, and test-only Wails server mode define the first release.

## Implementation strategy

Keep commits green at the stated exit gates, but do not preserve a second
product architecture merely to keep the app usable between gates. Temporary
generic Stream command/query frames may exist only until their binding phase;
the final deletion phase must prove none remain.

Pin the Wails Go module, Wails CLI tool, frontend runtime, and build assets to
v3.0.0-beta.11-compatible versions. Never use latest in scripts or CI. Before
final cutover, review newer Wails releases; any upgrade is a separate commit
followed by the complete suite.

Every development, test, prototype, and server-mode command defaults to an
isolated data root. Touching the real `%USERPROFILE%\.eTape` is a separate,
explicit migration run; a worktree alone does not isolate runtime data.

### Phase 0: baseline and beta capability gates

1. In [docs/performance.md](../performance.md) and a repeatable fixture under
   [prototypes](../../prototypes/README.md), record the current release-build
   baseline on one Windows 11 x64 machine:
   - fixed demo seed/replay and symbol set;
   - four browser Workspace windows, twelve representative Panels each;
   - five-minute warm-up and three fifteen-minute measurement runs;
   - process-tree CPU/private memory, startup, bridge/store latency, frame
     intervals, queue/coalesce/drop counters, and window open/close recovery.

2. Pin Wails beta.11 in [engine/go.mod](../../engine/go.mod) and pin the CLI as
   a Go tool or equivalently repository-owned invocation. Add the matching
   [@wailsio/runtime](../../ui/package.json) version and lockfile entry. Do not
   add Wails to engine-domain packages.

3. Add the smallest compile/test spike beside
   [engine/cmd/etape](../../engine/cmd/etape) and the future Wails build files
   to prove, on the exact pinned source rather than documentation alone:
   - an existing Go module/main-package layout with the sibling ui directory;
   - production embedded assets and Vite development assets;
   - a named second Native Window and focused-existing behavior;
   - Wails system tray and keep-running-after-last-window behavior;
   - lightweight frameless drag/no-drag/resize regions at 100%, 150%, 200% and
     mixed-monitor DPI, with composition hosting disabled;
   - Wails binding cancellation and cleanup ordering, including calls still
     running when application shutdown begins;
   - Stream handler access to its owning window in desktop builds, the nil-
     window server case, ordered close on reload/window close, buffer ownership,
     combined queue limits, bounded send behavior, and handler lifetime;
   - global event delivery, per-window queue saturation, and confirmation that
     no targeted or order-critical guarantee depends on Wails events;
   - typed service generation into a chosen ui/src/gen/wails directory;
   - service-call association with the calling window or, if Wails does not
     expose it, the opaque Stream-session capability design;
   - Wails server-mode Stream behavior for Playwright; and
   - a per-user NSIS package rooted under LocalAppData.

4. Turn each accepted spike into one focused automated check; delete throwaway
   UI. Record any beta API caveat next to the owning module rather than copying
   an implementation diary into the repository.

Exit gate:

- Current baseline is reproducible and recorded.
- The exact pinned Wails module/CLI builds on Windows.
- Stream backpressure/close, server-mode testing, frameless input/focus,
  bindings, named windows, tray, and NSIS have no blocking mismatch.
- A concrete application-owned admission/in-flight design can join bindings and
  Stream handlers before engine drain and store close.
- If a locked capability fails, stop and revise the decision with the user.
  Do not substitute generic events, localhost transport, or experimental
  composition hosting silently.

Suggested commit: build(wails): pin and prove native primitives

### Phase 1: Wails composition root, assets, and frameless shell

1. Add Wails-owned build configuration under engine:
   - engine/Taskfile.yml and engine/build platform/common files;
   - frontend generation/build commands that run in ../ui;
   - production asset copy into the Go module tree; and
   - deterministic build, dev, binding-generation, server-test, Windows x64,
     and NSIS tasks.

2. Replace the entrypoint split in
   [engine/cmd/etape/main.go](../../engine/cmd/etape/main.go),
   [run_default.go](../../engine/cmd/etape/run_default.go), and
   [run_tray.go](../../engine/cmd/etape/run_tray.go) with a Wails application
   composition root. During this phase the UI may show engine-disconnected boot
   state; do not start the legacy HTTP server inside Wails as a compatibility
   layer.

3. Extract the current blocking boot/drain ownership into a concrete
   engineRuntime in engine/cmd/etape or the smallest adjacent internal package:
   - Start returns promptly and publishes boot readiness/failure;
   - Run owns engine goroutines under the Wails application context;
   - every binding and Stream handler enters one admission/in-flight gate;
   - Stop rejects new work, cancels long work, joins admitted transport work,
     then performs the existing ordered drain and store close exactly once;
   - restart intent returns to its binding caller before an asynchronous quit,
     and replacement spawn occurs only after Wails post-shutdown; and
   - the type is not made restartable in-process.

4. Reuse [engine/internal/webui](../../engine/internal/webui/README.md) and its
   existing narrow Dist filesystem API for Wails' application-level Asset
   Server. Keep the ui/dist copy/embed pattern because Go embed cannot reach
   outside the engine module tree; remove its HTTP-only assumptions and rename
   it only if the surviving API later proves misleading. Development points
   Wails at Vite without adding a product listener.

5. Add engine/internal/desktop with the concrete Wails Host:
   - create hidden named Workspace windows and show after frontend readiness;
   - maintain one window handle per Workspace ID;
   - create the Wails tray with Open Main and Quit;
   - configure Windows to remain alive after the last window closes; and
   - use Wails single-instance signalling for activation while retaining the
     existing resolved-data-root/DB lock as the integrity guard.

6. In [ui/src/main.tsx](../../ui/src/main.tsx),
   [AppShell.tsx](../../ui/src/chrome/AppShell.tsx), the Top Bar components, and
   [ui/src/global.css](../../ui/src/global.css):
   - obtain Workspace identity from the native window bootstrap;
   - add accessible minimise, maximise/restore, and close controls;
   - mark only unused Top Bar surface as draggable;
   - mark all inputs/buttons/selectors and Dockview surfaces non-draggable; and
   - retain Panel Header drag behavior independently.

7. Add shell tests for window-name validation, idempotent open/focus, close-map
   cleanup, last-window tray behavior, single-instance/data-lock ordering,
   accessible/no-drag caption controls, and binding/Stream work racing shutdown.

Exit gate:

- A production Wails build embeds and displays the current React app in Main.
- A second named Workspace can be created, focused, closed, and reopened.
- Frameless movement, resizing and caption controls pass the Windows matrix
  without composition hosting.
- Engine shutdown tests still prove the existing drain order, no store write
  occurs after close, and restart cannot wait on its own binding response.

Suggested commit: feat(desktop): establish Wails workspace shell

### Phase 2: replace HTTP/WebSocket with one Wails Stream per WebView

1. Split [engine/internal/uihub/api.go](../../engine/internal/uihub/api.go) so
   transport-neutral Hub, mirror, command/query dependencies, and client
   session construction no longer require Server.

2. Add a Wails adapter at the existing connection socket seam in
   [engine/internal/uihub/conn.go](../../engine/internal/uihub/conn.go), keeping
   Hub, session, outbox, ordering, and coalescing unchanged initially:
   - require a first-frame protocol/Workspace/session handshake;
   - in desktop builds, validate it against the stable Native Window registry;
   - in server builds, validate it against the isolated test registry because
     StreamConn has no Native Window;
   - allocate one Hub client/session behind the admission/in-flight gate;
   - read the existing JSON control frames initially;
   - route snapshots/deltas through the existing outbox/coalescer;
   - copy or transfer ownership of every buffer sent to Wails;
   - close reads/writes on cancellation, preserve stop/restart with an explicit
     protocol frame, and never use TrySend to drop a lossless frame;
   - bound and measure the combined eTape/Wails queues without blocking Hub.Run;
   - join the handler with eTape's own in-flight tracker; and
   - release subscriptions, demands and indicators exactly once on every close.

3. Port and extend [conn_test.go](../../engine/internal/uihub/conn_test.go) and
   coalescing/outbox tests for:
   - snapshot-before-delta;
   - lossless FIFO and explicit overflow disconnect;
   - newest-value convergence for coalescible keys;
   - immutable send-buffer ownership and explicit stop/restart framing;
   - an independently stalled second Workspace;
   - a renderer stalled for ten seconds and a declared stale-frame budget;
   - malformed control frames;
   - reload/close races and late demand rejection; and
   - race-clean goroutine/resource teardown.

4. Replace [ui/src/wire/WsClient.ts](../../ui/src/wire/WsClient.ts) with a
   StreamClient/UpdateClient using the Wails Stream runtime. Preserve the
   subscription/store-facing API, fresh-snapshot reattach, and bounded retry
   behavior; remove URL, TCP/WebSocket, and browser-online assumptions.

5. Update [ui/src/App.tsx](../../ui/src/App.tsx),
   [DemandRegistry.ts](../../ui/src/wire/DemandRegistry.ts), indicator control,
   and wire tests so every WebView owns one Stream and reannounces only after a
   newly attached session is ready.

6. Add the test-only Wails server build and replace the legacy E2E boot in
   [ui/playwright.config.ts](../../ui/playwright.config.ts) and
   [ui/e2e](../../ui/e2e/README.md). Use an isolated temporary eTape data root,
   loopback/random test port, health readiness, and the exact product services
   and Stream handler. No server tag or plugin enters the packaged binary.

7. Keep the legacy adapter only as a branch-local development oracle until the
   native parity gate passes; it is never started by the Wails product. Then
   delete:
   - engine/internal/uihub/server.go and its HTTP/static tests;
   - HTTP-only dependencies, and coder/websocket only if repository search
     proves no broker adapter still uses it;
   - the product HTTP listener, static route, browser URL and open logic;
   - ui Vite /ws proxying and ws:// location construction; and
   - UIHub host, port, and dist configuration that has no engine meaning.

Exit gate:

- The Wails product opens no legacy listener, including port 8686.
- Four Workspace Streams remain isolated under a deterministic slow-client
  test.
- A native WebView2 run proves snapshot ordering, lossless FIFO, final latest-
  wins state, bounded ten-second-stall recovery, and 100 reloads with no leaked
  handler, demand, indicator, or backfill ownership.
- Existing store-routing, snapshot, demand and generated wsmsg tests pass.
- Playwright runs through Wails server mode, while a native smoke proves the
  desktop Stream path.

Suggested commit: refactor(uihub): move realtime traffic to Wails Stream

### Phase 3: generated service bindings for non-execution commands and queries

1. Add engine/internal/uiapi with one EngineService and one WorkspaceService.
   Register concrete singleton services in the Wails composition root. Guard
   mutable service state because every Native Window calls the same instances,
   and admit every method through engineRuntime before it touches engine/store.

2. Generate TypeScript interfaces/bindings into ui/src/gen/wails and add one
   repository target that:
   - regenerates Wails bindings/models;
   - regenerates tygo Stream DTOs;
   - fails on any committed drift; and
   - runs UI typecheck against both outputs.

3. Extract typed functions from the generic branches in
   [engine/internal/uihub/query.go](../../engine/internal/uihub/query.go) for
   chart windows, fills/cycle fills, locate eligibility/quotes/records, and
   export data. Expose explicit Wails methods and migrate their UI call sites.

4. Extract non-execution typed functions from
   [commands.go](../../engine/internal/uihub/commands.go):
   - scanner filter get/set;
   - watchlist add/remove;
   - venue setup and credential management;
   - connection testing. Demo/replay mode changes wait for the durable journal
     in Phase 5.

   Leave unmigrated generic configuration frames on the temporary Stream until
   their typed Workspace/preference methods land in Phases 4-5; do not create a
   generic GetConfig/SetConfig Wails binding merely to delete it later.

5. Keep Subscribe/Unsubscribe, Ensure/ReleaseSymbol, and indicator
   ensure/release on the per-window Stream. Do not invent binding session IDs.

6. Replace [ui/src/chrome/exec/commands.ts](../../ui/src/chrome/exec/commands.ts)
   with typed service clients and migrate panels/providers incrementally.
   Mocks implement the generated method surface; no generic sendCommand or
   sendQuery compatibility API remains after Phase 6.

7. For each method, port the existing accepted/blocked/value/error tests before
   removing its JSON case. Bindings reject internal failures; normal business
   outcomes remain typed return data. Mutations return a revision because a
   resulting Stream update may arrive before or after the binding result.

Exit gate:

- Every query and non-execution operation in this phase uses an explicit
  generated method.
- No correlation ID, query result frame, or generic dispatch remains for the
  migrated set.
- Generated output is byte-identical from a clean checkout.
- Existing behavior tests pass without casting unknown result payloads.

Suggested commit: refactor(uiapi): bind typed queries and settings

### Phase 4: native Workspace windows, persistence, geometry, and recovery

1. Implement engine/internal/uistate with an in-memory, mutexed Workspace
   catalog/document store and monotonic revisions. Continue using the existing
   config persistence beneath it; do not replace storage merely because the
   caller changes.

2. Implement WorkspaceService methods for catalog snapshot/create/rename/delete,
   document load/save, open/focus/close, geometry updates, open-window snapshot,
   and restoration. Ordinary saves update Go memory immediately and may
   debounce disk I/O; close/quit/restart acknowledgements mean the SQLite
   transaction committed. Bound and validate opaque Dockview JSON without
   interpreting it.

3. Replace [ui/src/chrome/catalogs.ts](../../ui/src/chrome/catalogs.ts),
   [workspace.ts](../../ui/src/chrome/workspace.ts),
   [windows.ts](../../ui/src/chrome/windows.ts), and
   [NewWindowModal.tsx](../../ui/src/chrome/NewWindowModal.tsx) browser
   coordination with generated bindings and revisioned projections. Treat Wails
   events as app-wide hints carrying Workspace identity/revision; use the owning
   Stream for targeted invalidation and register listeners before the snapshot.

4. Add a two-step native close handshake. Hold the first close, serialize and
   durably save the current Dockview document, then call CompleteClose. On a
   bounded timeout, offer an explicit force-close path for a hung WebView.

5. Persist normal bounds, display identity, and maximised state. Test negative
   coordinates, monitor unplug/reorder, taskbar work areas, 100/150/200% DPI,
   off-screen/corrupt values, and never restore minimized state.

6. Add atomic clean/unclean session markers:
   - mark a new run unclean before creating any Workspace window;
   - normal and intentional-restart shutdown mark clean only after state flush;
   - forced termination leaves the prior open set marked unclean;
   - intentional restart restores all windows;
   - unclean launch opens Main only and offers restore/decline; and
   - decline preserves every Workspace without opening it; validate each saved
     document independently and quarantine one bad layout instead of looping.

7. Replace the existing process relaunch/browser-adoption flow with Wails
   process restart. The request binding records intent and returns; asynchronous
   shutdown then closes admission, joins work, drains the engine, releases DB
   and single-instance resources, and spawns from post-shutdown. Reuse only the
   proven process-spawn logic; delete browser PID/profile/token arguments.

8. Test idempotent open, delete-while-open rejection, immediate Alt+F4/custom-
   close/tray-quit/restart after layout mutation, save/close races,
   revision gaps, corrupt geometry, monitor removal, clean/unclean markers,
   intentional restore, crash restore choices, hung-WebView force close, and
   100 restart cycles without ghost trays, duplicate windows, or DB-lock races.

Exit gate:

- No Workspace/catalog operation uses BroadcastChannel, Web Locks, localStorage,
  or window.open.
- Close preserves documents, releases runtime resources, and leaves the tray.
- A successful close/restart save survives immediate forced process termination.
- Intentional and crash restoration behave differently and deterministically.
- Four restored windows return to valid geometry without duplicate identities.

Suggested commit: feat(workspace): own native windows and restoration in Go

### Phase 5: global Link Groups, drawings, and preferences

1. Add versioned global Link Group state to uistate:
   - one symbol/venue per red, green, blue, and yellow group;
   - serialized focus mutation and validation;
   - monotonic revision and persistence; and
   - targeted Stream invalidation plus optional app-wide revision hint.

2. Add the additive migration:
   - acquire the data-root lock;
   - create and verify a timestamped pre-Wails backup before starting writers or
     applying migration, using a closed-store or SQLite backup path for WAL
     consistency;
   - seed global focus from valid Main values when legacy Workspaces disagree;
     missing/malformed Main falls back to a documented empty group, never an
     arbitrary other Workspace;
   - log only Workspace/group identifiers and never symbols, credentials,
     account data, or other sensitive payloads;
   - leave the old files/backup usable for rollback; and
   - write the migration marker last.

3. Replace [ui/src/chrome/linkGroups.ts](../../ui/src/chrome/linkGroups.ts) with
   a local projection backed by WorkspaceService snapshot/mutation methods and
   revisions. Preserve Panel Link Group membership inside each Workspace;
   migrate only the shared group focus.

4. Add canonical drawing operations and per-symbol revisions in Go. Replace
   [ui/src/render/chart/drawings/store.ts](../../ui/src/render/chart/drawings/store.ts)
   whole-symbol BroadcastChannel writes with optimistic upsert/remove/clear
   operations, accepted-state reconciliation, persistence, and broadcast.

5. Move the explicit durable preference inventory—theme, order settings, sound,
   Drawing Tool Style and durable hints—through atomic typed Go methods. Reset
   accepted browser-only dismissals and remove legacy etape.windows localStorage
   migration code. Keep local form/modal state local.

6. Update Scanner Sync/source notification paths to consume revisioned Workspace
   state rather than assuming browser-window broadcasts. Any ordinary Wails
   event is emitted from a bounded/coalescing desktop dispatcher, includes
   identity/revision, and is never required for correctness.

7. Add a transactional demo/replay mode-transition journal before migrating
   StartDemo/Go Live. Commit pre-transition Workspace documents, global Link
   focus, mode and phase before restart; recover idempotently after a crash at
   every phase. Do not use a React ref or default symbols as rollback state.
   Then expose typed StartDemo/GoLive methods that can only use this journal.

8. Add migration fixtures for normal/current/already-migrated/corrupt profiles,
   conflicting Link Groups, WAL/SHM presence, missing optional files, and backup
   or disk failure. Fixtures contain no real credentials or runtime data.

Exit gate:

- Repository product code contains no BroadcastChannel, navigator.locks, or
  durable localStorage coordination.
- Link Groups converge globally across four Workspaces and restart.
- Concurrent drawing edits cannot lose an unrelated operation.
- Demo/replay entry, crash, restart and return to live restore the exact original
  Workspaces and Link Groups with no mixed transition state.
- Migration is idempotent; any failure leaves original data usable and the
  marker absent.

Suggested commit: refactor(state): centralize cross-window state in Go

### Phase 6: focused-window capabilities and typed execution bindings

1. Implement a Go FocusCoordinator using Wails Native Window focus/lost-focus
   events and stable window generations. The frontend reports active eligible
   Panel changes; Go issues an opaque, revisioned capability tied to the
   focused window/session/panel. It is ephemeral, starts empty/disarmed after
   every launch, and is never included in window restoration.

2. Replace [ui/src/chrome/hotkeyTarget.ts](../../ui/src/chrome/hotkeyTarget.ts)
   with a local projection. Keep DOM editable checks, type-to-load, Deck
   interactions, and local modal checks; remove Lamport/replay/tombstone browser
   coordination and random window IDs.

3. Define execution authorization by risk:
   - symbol-scoped or risk-increasing actions require the current focused Panel
     capability and target revision;
   - direct order actions require a current focused application/session and
     the relevant order identity/revision;
   - risk-reducing global actions such as Disarm and Kill Switch must remain
     reachable from a focused eTape window even when no symbol Panel is active;
   - no action is accepted from a background, minimized, closed, stale, or
     interaction-blocked window.

   Every hotkey-origin binding obtains the source WindowKey from Wails context
   when available, synchronously verifies native IsFocused immediately before
   exec.Do, and compares the session/window generation. The server-test fallback
   uses the opaque Stream-session capability; neither path trusts Workspace ID.
   Button/ticket and hotkey origins remain distinguishable.

4. Wrap every Wails native dialog/file operation so FocusCoordinator suspends
   order capabilities until it closes. A local React modal continues to block
   through modalTracker and the active frontend capability.

5. Extract and expose explicit typed methods for SubmitOrder, CancelOrder,
   ReplaceOrder, Flatten, ResetBalance, RequestLocate, KillSwitch, Arm, Disarm,
   and RestartApplication. Preserve gate, disarmed, idempotency, broker-ack,
   deferred result, and ambiguous outcome semantics from existing tests.

6. Migrate every Order Ticket, Hotkey Deck, Account, Locate, Venue and command
   call site. Delete generic CommandMsg/AckMsg/ResultMsg command handling and
   sendCommand after the final parity case passes.

7. Add coordinator/service tests for rapid/out-of-order focus, Alt-Tab,
   minimized/closed windows, blank Workspace, stale generation/capability,
   native and React modals, editable controls, order ambiguity, and no fallback.
   Move auto-unlock/initial Arm to one process-level owner so four restored
   windows cannot issue four requests.

Exit gate:

- Focusing another application invalidates every eTape order capability.
- A focused Workspace with no eligible Panel blocks scoped orders.
- Risk-reducing global controls remain available under their explicit focused-
  app rule.
- No generic command/query transport remains; the Stream contains only
  session-control and server update frames.
- Existing execution/risk tests plus new stale-focus tests pass under race.
- One thousand adversarial four-window focus changes produce zero background or
  stale simulated/paper commands, and only one initial Arm request is observed.

Suggested commit: feat(exec): gate typed bindings by native focus

### Phase 7: native dialogs, file I/O, external URLs, and complete lifecycle

1. Replace hidden file inputs, FileReader, Blob downloads, window.confirm,
   window.prompt where avoidable, and ad hoc browser downloads with
   WorkspaceService/Desktop host methods that:
   - attach Wails native dialogs to the calling Native Window;
   - validate size/schema before applying imports;
   - use atomic bounded Go writes for exports; and
   - never expose arbitrary filesystem access through a generic binding.

2. Keep React rename/settings/practice/venue forms where text entry or complex
   UI makes native dialogs shallower. Native capability does not require
   replacing useful WebView UI.

3. Replace [ui/src/chrome/windows.ts](../../ui/src/chrome/windows.ts) news
   handling with a validated http/https system-browser operation. Reject every
   other scheme.

4. Finish tray and shutdown behavior:
   - Open Main focuses or creates it;
   - optional Workspace submenu reflects the canonical catalog/open set;
   - Quit flushes state, closes transport admission, joins admitted bindings and
     Streams, cancels engine work, waits for ordered drain, then closes storage;
   - window close never deletes the Workspace; and
   - a boot failure remains visible through a native diagnostic/boot UI rather
     than leaving a ghost tray process.

5. Verify data backup/restore instructions against a copied real-shaped,
   redacted profile. Never inspect or copy live credentials into the repository.

Exit gate:

- Import/export/confirmation flows use native Wails I/O and preserve validation.
- Remote content never loads in an application WebView.
- Quit/restart cannot close the database ahead of engine writers.
- No browser-owned file/window/process behavior remains in product code.

Suggested commit: feat(desktop): finish native I/O and lifecycle

### Phase 8: delete legacy host and replace build, CI, and release

1. Delete all now-dead legacy product code and dependencies:
   - engine/internal/openbrowser;
   - Fyne systray and tray build tags;
   - HTTP-specific webui handlers/build assumptions, while retaining the narrow
     embedded Dist filesystem used by Wails;
   - remaining uihub HTTP/WebSocket code and tests;
   - browser ownership/adoption and old restart arguments;
   - -dist, -no-open and UI listener configuration;
   - browser window helpers/channels/locks/storage fallbacks; and
   - generic command/query/correlation envelopes no longer used by Stream.

   Remove `github.com/coder/websocket` only if repository search proves no
   broker adapter still imports it.

2. Consolidate commands:
   - engine/Makefile retains engine test/lint/tygo ownership where useful;
   - engine/Taskfile.yml owns Wails dev/build/package/server tasks;
   - top-level run.cmd/run.ps1/run.sh call the pinned Wails workflow for
     supported Windows development modes; and
   - every script fails clearly when a pinned prerequisite is missing.

3. Update [.github/workflows/ci.yml](../../.github/workflows/ci.yml):
   - retain Go full/race/vet/lint and UI lint/test/build;
   - regenerate/check tygo and Wails bindings;
   - build/test the Wails server tag and run Playwright against it;
   - build the Windows x64 Wails product on windows-latest; and
   - never install Wails or another dependency from latest.

4. Replace [.github/workflows/release.yml](../../.github/workflows/release.yml)
   with a Windows runner that:
   - installs pinned Go, Node 24, Wails and NSIS tooling;
   - builds production UI and generated bindings;
   - runs Wails Windows x64 packaging;
   - produces only the versioned NSIS installer;
   - retains the unsigned/SmartScreen warning; and
   - never publishes the server build, raw developer executable, macOS archive,
     or portable ZIP.

5. Configure NSIS for per-user LocalAppData installation, WebView2 bootstrap,
   version metadata, Start menu shortcut, upgrade, and uninstall that preserves
   %USERPROFILE%\.eTape. Verify offline-missing-WebView2 failure is actionable
   and does not leave a partial install.

6. Update all affected durable documentation:
   - [README.md](../../README.md) and README-FIRST.txt/remove it;
   - [AGENTS.md](../../AGENTS.md) invariants and validation commands;
   - [engine/README.md](../../engine/README.md);
   - engine/cmd/etape and internal uihub/webui/desktop/uiapi/uistate guides;
   - [ui/README.md](../../ui/README.md), Chrome/wire/E2E guides;
   - [docs/specs/README.md](../specs/README.md) durable transport statements;
   - [docs/performance.md](../performance.md); and
   - script/prototype guides for retained commands.

7. Search current documentation and code for stale product claims: browser
   tabs/windows, 127.0.0.1:8686, /ws, ws://, -no-open, -dist, embedded HTTP UI,
   portable ZIP, CGO-free cross-platform release, and macOS release artifact.
   Keep only historical/explicitly test-server references.

Exit gate:

- A clean checkout regenerates both contracts, passes CI, builds product and
  server variants, and creates the installer using repository-pinned tools.
- The installed product opens no console, browser, or product listener.
- Uninstall preserves user data.
- No legacy runtime dependency or unreachable compatibility code remains.

Suggested commits:

- build(wails): replace development and CI workflow
- build(release): package per-user Windows installer
- docs: document native desktop operations

### Phase 9: acceptance, soak, and cutover

1. Run the repository's updated CI-equivalent Windows checklist, including:

       Set-Location engine
       go test ./...
       go test -race -short ./...
       go test -tags server ./...
       go test -tags server -race -short ./...
       go vet ./...
       golangci-lint run
       mingw32-make gen-ts-check
       go tool wails3 build
       go tool wails3 package
       Set-Location ..\ui
       npm ci
       npm run lint
       npm test
       npm run build
       npm run e2e:wails
       Set-Location ..
       git diff --check

   Final task/target names may differ, but README and CI must expose one exact
   authoritative command for each listed gate.

2. Run native Windows 11 checks:
   - four Workspace windows and twelve Panels each;
   - Alt-Tab/click/minimize/restore and another foreground application;
   - no order action from background/stale/blank/modal state;
   - Top Bar drag, every resize edge, caption controls, Win+Arrow and Win+Z at
     100%, 125%, 150%, and mixed-monitor DPI;
   - intentional restart, forced crash, both crash-restore choices, missing
     monitor and corrupt geometry;
   - last-window-to-tray, reopen, second launch, Quit; and
   - clean install, upgrade, launch, WebView2 bootstrap path and uninstall.

   Repeat 100 WebView reloads/start-stop cycles and 100 whole-process restarts;
   require zero leaked handler/demand ownership, store write after close, ghost
   tray, duplicate window, lost argument, or DB-lock collision.

3. Repeat the Phase 0 performance protocol with the same fixture and machine.
   Block release for any unexplained violation of the specification's latency,
   CPU, memory, ordering, resource-recovery, or focus gates. Record raw method,
   hardware, measurements, and conclusion in docs/performance.md.

4. Soak without real orders:
   - eight-hour deterministic demo with scripted four-by-twelve window/panel
     activity and complete simulated order lifecycles;
   - one full RTH live-data/read-only plus simulated-execution session;
   - one full RTH paper-broker session with deliberately bounded paper orders;
   - OpenD/network disconnect/reconnect, sleep/wake, two clean restarts and one
     forced crash; and
   - post-soak log/counter review for panic, blank windows, lossless gaps,
     duplicate execution, Stream overflow, leaked demand, restoration loop,
     corruption, or sustained memory growth.

5. Run a source/diff audit:
   - no generated file hand edits;
   - no credentials, account identifiers, balances, keys or captured runtime
     data;
   - LF Go files and clean git diff check;
   - every changed flow/interface/dependency/invariant/operation documented;
   - every required check listed with result and every skip justified; and
   - one complete hosted CI run green.

6. Review any Wails release newer than beta.11. Do not upgrade inside the final
   cutover commit. If an upgrade is required for a blocker, make it a scoped
   commit and repeat Phases 0 capability gates and 9 validation.

7. Present the final branch, migration backup/rollback instructions, installer,
   measurements, native smoke evidence, checks, and known beta limitations for
   cutover approval. Do not partially merge the migration. After approval,
   integrate the scoped branch commits as one coordinated main-branch cutover
   and follow the repository's commit/push rule.

## Validation ownership by layer

| Layer | Required proof |
|---|---|
| Engine/core | Existing full, race, vet, lint and execution/risk suites |
| Stream | Snapshot ordering, lossless FIFO, latest convergence, bounded slow clients, teardown |
| Bindings | Generated drift, typed round trips, business result versus internal error |
| Shared state | Revisions, conflict serialization, Main-wins migration, drawing operations |
| Focus/order | OS focus capability, no background/global shortcut, stale/modal/close rejection |
| WebView UI | Vitest, Dockview body identity, Scheduler/store invariants, accessible frameless controls |
| Server test build | Playwright through the same services and Stream handler; isolated profile |
| Native Windows | Window/tray/single-instance/DPI/geometry/crash/dialog/system-browser behavior |
| Data | Backup-before-write, idempotent migration, failure leaves source usable, rollback restore |
| Release | Windows runner, pinned tooling, NSIS/WebView2, install/upgrade/uninstall, no legacy artifact |
| Performance | Same-machine browser baseline versus four-by-twelve Wails result and soak |

## Rollout and rollback

- Until final cutover, the original main worktree and main branch remain the
  functional rollback; no migration phase is pushed partially to main.
- The product has one runtime after cutover. Rollback is not an environment
  variable or hidden browser mode.
- Every first-run data mutation is preceded by a timestamped consistent backup.
  Installing the prior release and restoring that backup is the supported data
  rollback.
- Additive config keys and retained layout version 8 minimize the need to
  restore. The old app must not be allowed to open a partially migrated live
  profile concurrently.
- If a blocking Wails beta defect appears before cutover, stop on the migration
  branch, update or revert the pinned Wails change, and rerun the capability
  gates. Do not weaken order safety or transport semantics to ship around it.
- The first Wails release is unsigned and may trigger SmartScreen, as the
  current release does. Broader public distribution requires a separate
  signing decision.

## Risks

- **Wails beta/API drift:** pin module, CLI, runtime and assets together; compile
  exact source; isolate Wails imports in desktop/uiapi; upgrade separately.
- **New Stream implementation:** preserve the eTape outbox ahead of Wails,
  own sent buffers and handler lifetime, test combined queue saturation and
  close races, and forbid generic events for HFT.
- **Service caller identity:** prove native caller association in Phase 0 or use
  an opaque Stream-session capability; never trust a bare Workspace ID.
- **Shutdown/data corruption:** reject new transport work, join admitted binding
  and Stream work, retain one ordered engine drain, wait before store
  close/relaunch, backup before migration, and write markers last.
- **Order focus regression:** make Go's OS-focus capability authoritative and
  fail closed for stale/background/scoped targets while retaining explicit
  risk-reducing global actions.
- **Cross-window lost updates:** use one Go owner, revisions and operation-based
  drawings; frontend snapshots are projections, not writers.
- **Mode transition loss:** journal live Workspace/Link state transactionally
  before demo/replay restart and fault-test every recovery phase.
- **Wails global events:** treat them as lossy app-wide hints from a dedicated
  dispatcher; use revisions/snapshots and the Workspace Stream for targeting.
- **Frameless input/DPI defects:** avoid experimental composition, test
  non-client regions and Dockview dragging on supported DPI/monitor layouts.
- **Crash loops/off-screen windows:** distinguish clean/unclean session state,
  start Main-only after crash, and clamp geometry.
- **Test-server false confidence:** keep a real native smoke and soak because
  Wails server mode cannot prove WebView2, focus, tray, dialogs or geometry.
- **Installer/runtime failure:** test WebView2-present/missing/offline VMs,
  per-user upgrade and data-preserving uninstall.
- **Migration scope:** delete temporary generic frames and legacy host code at
  explicit gates; reject a permanently dual architecture.
