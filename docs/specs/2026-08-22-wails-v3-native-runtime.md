# Wails v3 native desktop runtime

Status: Approved on 2026-08-22.

Related decision: [ADR 0005](../adr/0005-replace-browser-host-with-wails-v3.md).

## Goal

Replace eTape's localhost HTTP/WebSocket and browser-hosted product runtime with
a Windows-first Wails v3 desktop application without weakening market-data
ordering, rendering performance, persistence safety, or order-entry gates.

The finished application has one primary Native Window per Workspace, keeps
Dockview inside that window, runs from the Wails system tray when all Workspace
windows are closed, and ships as a per-user Windows 11 x64 NSIS installer.

## Product contract

- A Workspace is persisted independently of its Native Window.
- Opening an already-open Workspace focuses its existing Native Window.
- Closing a Workspace window releases its runtime resources but does not delete
  the Workspace.
- Closing the final Workspace window leaves the engine running in the tray.
- An intentional application restart restores the open Workspace set and each
  window's valid monitor, bounds, and maximised state.
- After an unclean exit, eTape opens Main only and offers to restore the prior
  Workspace set. It must not enter an automatic crash loop.
- Dockview remains the sole owner of Panel Group layout and panel-body mounting.
- Native Panel or Panel Group detachment is not included. It may be reconsidered
  after the Wails migration is stable and measured.
- Hotkeys remain application-scoped. Only the OS-focused Native Window's active,
  eligible Panel may own a scoped order action. There is no background-window
  fallback and no system-wide order shortcut.
- Validated remote news URLs open in the system browser, never in a privileged
  eTape WebView.

## Supported platform and distribution

The first supported Wails product target is Windows 11 x64. The product
artifact is an unsigned, per-user NSIS installer that installs without
administrator rights under `%LOCALAPPDATA%\Programs\eTape`. Application state
remains under `%USERPROFILE%\.eTape`; install, upgrade, and uninstall must not
delete or relocate it.

The installer includes Wails' WebView2 bootstrapper path for machines missing
the runtime plus Start menu integration and predictable upgrade/uninstall.
Those are its benefits over a portable ZIP; the ZIP's only material advantage
here is no-install portability. Windows 10, ARM64, macOS, Linux, code signing,
an auto-updater, machine-wide Program Files installation, and a public portable
archive are deferred. A later Program Files installer would require elevation
but would still keep all state under `%USERPROFILE%\.eTape`.

## Runtime architecture

    Wails application
      |
      +-- desktop.Host
      |     Native Windows, frameless chrome, tray, single instance,
      |     restart/crash markers, geometry, dialogs, external URLs
      |
      +-- uiapi.EngineService
      |     typed command and query bindings
      |
      +-- uistate.Store / uiapi.WorkspaceService
      |     Workspace catalog and documents, Link Groups, drawings,
      |     preferences, focus/hotkey ownership and revisions
      |
      +-- uihub.Hub
            market projection, snapshots, deltas, coalescing,
            connection-scoped demands and one Wails Stream per WebView

Each Workspace WebView retains React, Dockview, imperative data stores,
canvas/chart controllers, one animation Scheduler, DOM focus/editability,
toasts, local forms, and local modal presentation. High-frequency data never
enters React state.

### Module boundaries

The implementation uses concrete modules rather than a generic host abstraction:

- engine/cmd/etape remains the composition root. A concrete engineRuntime owns
  startup, transport admission, in-flight work, the existing ordered drain, and
  store close. It is not restartable in-process.
- engine/internal/desktop owns Wails-specific lifecycle and Native Window
  operations. Engine, panel, and persistence code do not import Wails.
- engine/internal/uihub keeps its existing mirror, coalescing, outbox, topic,
  snapshot-before-delta, and per-client cleanup behavior. Its HTTP/static and
  WebSocket-specific shell is removed.
- engine/internal/uiapi exposes a small number of deep Wails services rather
  than one service per UI feature. EngineService owns engine commands and
  queries; WorkspaceService owns low-rate application/workspace operations.
- engine/internal/uistate owns canonical cross-window state, revisioning, and
  persistence coordination.
- engine/internal/webui keeps its existing narrow Dist filesystem API and
  production UI embedding for Wails. Its HTTP-oriented documentation/build
  assumptions are removed, but the package is renamed only if native use later
  leaves its name materially misleading.

The exact package names may be adjusted during the first compile spike, but
these ownership boundaries and dependency directions are invariant.

## Native host and window contract

Go owns a registry from Workspace ID to a Wails Native Window named
workspace:<id>. Creation validates the existing Workspace ID rules and is
idempotent. A first close request is held while the frontend serializes its
current Dockview document and a save transaction commits. The frontend then
calls a typed CompleteClose method. A bounded timeout offers an explicit
force-close path for a hung renderer; it is never an implicit successful save.
Final close unregisters the window, revokes focus, closes the Stream, waits for
its tracked handler to return, and releases demands, indicators, and watchers
exactly once.

Main and Monitoring retain their current special Workspace semantics.
Workspace deletion remains separate from window closing and is rejected while
the Workspace is open.

Window geometry is stored in logical screen coordinates with display identity,
normal bounds, and maximised state. Restore clamps missing or off-screen
displays into the current work area. Minimized state is never restored.

The frameless shell uses Wails/WebView2 lightweight non-client drag support and
normal frontend caption-button actions. The existing Top Bar supplies a clear
drag region plus accessible minimise, maximise/restore, and close buttons.
Interactive Top Bar controls are explicitly non-draggable. Experimental
WebView2 composition hosting is disabled, so native hover Snap Layouts on the
custom maximise button are not required; Win+Z, edge snapping, resizing, and
normal button actions remain required.

The application uses one instance per resolved eTape data root. Wails
single-instance signalling handles activation, while the existing data-root/DB
lock remains the integrity guard. A second launch focuses Main rather than
starting a second engine against the same data. Development, tests, and replay
default to isolated data roots and may run independently.

## Engine lifecycle

Wails owns the process event loop. Engine startup runs asynchronously so the
Main window can display boot status instead of blocking the native UI thread.
Every binding and Stream handler enters an application-owned admission gate.
Stopping first rejects new work, revokes capabilities, cancels long-running
work, and waits for every admitted call and Stream handler. Only then may the
existing ordered engine drain run and the store close. eTape does not rely on
Wails cancellation or cleanup order to join this work.

Restart Engine becomes a whole-application restart. Its binding records the
intent and returns; only a subsequent asynchronous lifecycle task begins quit,
so the initiating call cannot deadlock the in-flight gate.

1. Flush the current Workspace documents, global shared state, open-window set,
   and geometry.
2. Mark the shutdown as an intentional restart.
3. Cancel the engine context and wait for the existing ordered drain.
4. Close Wails and release the single-instance/data locks.
5. From post-shutdown, launch the replacement executable with an opaque restore
   marker.
6. Restore every previously open Workspace window.

Normal Quit follows the same drain without relaunch. An external kill cannot
write the clean marker, causing the next launch to use the Main-only crash path.

## Go to frontend transport

The native bridge has three deliberately separate lanes.

### Typed service bindings

Generated Wails service bindings carry discrete commands and queries. Public
operations are explicit methods such as SubmitOrder, CancelOrder,
ReplaceOrder, QueryChartWindow, QueryFills, SetVenueSetup, TestConnection,
OpenWorkspace, SaveWorkspace, SetTheme, and SetSoundPreferences. No generic
configuration binding, command/query name switch, or correlation-ID protocol
exists in the end state.

Expected business outcomes are returned as typed data. An execution result
distinguishes accepted, blocked, ambiguous, reason, and broker order identity
where applicable. Internal failures in the service or bridge reject the
Promise. The frontend must never automatically retry an ambiguous execution
request.

Authorization follows the action's risk. Hotkey-origin and symbol-scoped or
risk-increasing actions carry an opaque focus capability and target revision
tied to the calling Stream session and active eligible Panel. Direct order
management carries the relevant order identity/revision and still requires a
focused eTape session. Risk-reducing global actions such as Disarm and Kill
Switch remain reachable from a focused eTape window without a symbol Panel.
Immediately before execution, Go verifies the Wails calling-window context (or
the session capability when Wails cannot expose it), synchronous OS focus, and
the canonical generation. Background, minimized, closed, stale, or
interaction-blocked callers fail closed. Focus capabilities are never
persisted or restored.

### Wails Stream

One named Wails Stream connection belongs to each Workspace WebView. It carries:

- topic subscribe and unsubscribe control;
- symbol demand ensure and release;
- indicator ensure and release;
- initial and requested snapshots;
- continuous deltas and updates; and
- protocol heartbeat/health where still useful.

Commands and queries leave the Stream after their typed binding reaches parity.

The Stream's first frame declares protocol version, Workspace, and an opaque
session nonce. Desktop builds validate that declaration against the Wails
Native Window registry; server-test builds, where no Native Window is
available, validate it against their isolated test registry. A bare query
string or JavaScript Workspace ID is never authoritative. Closing or reloading
the WebView closes the Stream and releases every session-owned resource. The
replacement WebView opens a new Stream, reattaches listeners, subscribes, and
receives fresh snapshots before deltas.

Wails first plugs into the existing connection socket seam; uihub retains its
outbox, lossless/latest-wins classifications, and coalescing. Wails' own queue
is transport buffering, not the business policy. A write never blocks Hub.Run,
sent buffers have immutable ownership, handlers are tracked by eTape's
in-flight gate, and an explicit protocol frame carries stop/restart meaning
before close. Load tests must bound the combined eTape/Wails queue so it cannot
hide coalescing behind stale frames. No lossless frame may be silently dropped
or reordered; a coalescible topic converges to its newest sequence.

### Wails events

Wails events carry only low-rate Native Window lifecycle and app-wide
invalidation hints. They do not carry quote, book, tape, bar, account,
position, scanner, targeted Workspace, or order-critical traffic.

Beta.11 events are treated as global broadcasts, even when emitted from a
window object. Every hint includes identity and a monotonic revision, and each
frontend filters it and refetches on a gap. Events are emitted only from a
bounded/coalescing desktop dispatcher, never from engine, Hub, store-writer, or
order-critical goroutines. Targeted shared-state notifications use the owning
Workspace Stream. Event delivery is never required for correctness.

Bindings, Streams, and events have no total cross-lane order. A mutation
returns its resulting revision; frontends ignore stale projections and tolerate
the corresponding Stream update arriving before the binding result.

## Generated contracts

Go remains the source of truth.

- Wails generates TypeScript bindings and service-reachable models into a
  committed, read-only directory under ui/src/gen.
- The existing wsmsg Go DTOs and tygo continue to generate the Stream envelope
  and payload types in ui/src/gen/wsmsg.ts.
- The wsmsg package name remains during this migration to avoid a mechanical
  repository-wide rename. Its package documentation changes from WebSocket to
  engine-to-UI Stream contract.
- CI regenerates and checks both outputs. Generated TypeScript is never edited
  directly.

Using two generators is accepted because Wails Streams are byte protocols and
do not infer their payload schema from bound service methods. Consolidation is
deferred until Wails supports typed Streams directly or the second generator
causes measurable maintenance cost.

## Canonical shared state

Go replaces BroadcastChannel, Web Locks, and browser-local coordination.

### Workspace catalog and documents

WorkspaceService serializes atomic catalog mutations and owns the latest
document and revision for every Workspace. Ordinary saves may debounce disk
persistence, but a save acknowledged for close, quit, restart, or mode change
means its SQLite transaction committed. Closing a WebView therefore cannot
strand a frontend-only timer or report queued state as durable.

Go treats Dockview layout JSON as opaque, size-bounded data. The frontend owns
layout interpretation and continues to enforce layout version 8.

### Link Groups

Red, green, blue, and yellow Link Group symbol and venue focus become one
globally persisted state shared by every Workspace. Focus mutations are
serialized, revisioned, validated, persisted, and broadcast by Go.

During migration, Main Workspace values win when legacy saved Workspaces
disagree. Conflicts are logged without symbols, account data, or other
sensitive payloads. Legacy per-Workspace values are retained in the
pre-migration backup for rollback.

### Drawings

Go stores canonical drawing operations and per-symbol revisions. Frontends
retain imperative optimistic DrawingStore projections but send upsert, remove,
and clear operations instead of racing whole-symbol snapshots. Go persists and
broadcasts accepted operations.

### Focus and hotkeys

Wails OS focus events are authoritative for the focused Native Window. The
frontend reports user activation of an eligible Dockview Panel. Go issues a
revisioned focus capability only for that pair.

If the focused Workspace has no eligible Panel, scoped order actions are
blocked. The globally last-clicked Panel is never used as a fallback. Local
DOM key filtering, editable-element checks, type-to-load, and local modal
tracking remain in the WebView. Each launch starts without a restored hotkey
target and disarmed; any initial Arm/auto-unlock policy has one process-level
owner rather than running once per Workspace WebView.

### Preferences

Durable hints and settings move through typed Go persistence. App-session-only
dismissals may live in Go memory. The old browser-local dismissal flags reset.
No compatibility browser is retained solely to read them.

### Mode transitions

Demo/replay transitions use a durable, transactional journal containing the
pre-transition Workspace documents, global Link Group focus, mode, and phase.
The journal commits before a restart is requested and recovery is idempotent at
every phase. Entering and leaving demo must restore the exact live state; a
React ref or default symbols are never the rollback source.

## Native dialogs, files, and external content

Workspace import/export, trade export, confirmations, and other file operations
use Wails native dialogs attached to the calling Native Window. Go performs
bounded file reads and atomic writes. Existing schema validation still occurs
before imported data is applied.

Native dialog ownership temporarily revokes the focused order capability until
the dialog closes. React text-entry and complex settings modals remain React
components; Wails has no reason to replace view-local forms.

External URLs accept only validated http and https schemes and open in the
system browser. No remote page receives Wails bindings or shares an eTape
WebView.

## Data migration and rollback

Migration is additive and versioned. Development and automated tests use a
separate data root by default; the migration cannot touch the real
`%USERPROFILE%\.eTape` accidentally.

- acquire the existing single-instance/data-root lock before inspection;
- create and verify a timestamped pre-Wails backup before starting writers or
  applying migration, using a closed-store or SQLite backup path rather than
  raw-copying an active WAL database;
- retain Workspace layout version 8, Panel identities, settings, drawings,
  catalog entries, credentials, and engine/store data;
- create the global Link Group state using Main as the deterministic conflict
  winner;
- create native window/session state separately from Workspace documents;
- reset only the documented browser-local dismissal flags; and
- write the new migration marker only after every step succeeds.

Migration failure aborts startup with a diagnostic and leaves the source data
and backup intact. eTape never silently resets ~/.eTape. If a later discovery
makes conversion impossible, a clean reset requires a separate explicit user
decision and must preserve the backup.

Rollback before release is branch removal because main remains unchanged.
Rollback after release installs the previous build and restores the
pre-migration backup. New config keys must not make the prior executable mutate
or reject otherwise compatible engine data.

## Development and testing

The Wails module, CLI tool, frontend runtime, and generated build assets are
pinned to one reviewed beta version, initially v3.0.0-beta.11. The Go tool
directive or an equivalently repository-pinned invocation must prevent a
developer-global Wails CLI from silently changing generated output. A Wails
upgrade is a separate scoped change followed by the complete validation suite.

Production has no HTTP listener or browser UI. A test-only Wails server build is
retained for Playwright and CI. It uses the same bound services and Stream
handler, with the Stream exposed as a real WebSocket by Wails. This test build
is never packaged or enabled by product configuration.

Vitest continues to exercise React, Dockview, stores, and controllers with
generated binding/runtime mocks. Go tests cover services, state revisioning,
transport policy, lifecycle, migration, and window-manager policy without
starting live feeds. A small native Windows smoke covers the shell behavior
that server mode cannot prove.

All automated migration and soak work uses deterministic demo, replay, sim, or
paper modes. Live order placement, modification, and cancellation are excluded
unless separately authorized and reconfirmed for that session.

## Performance and acceptance gates

Before replacing the transport, capture a reproducible current-build baseline.
The final comparison uses four simultaneous Workspace windows with up to twelve
Panels each and a documented representative panel mix.

Release requires:

- no silent loss or reordering of lossless Stream frames;
- latest-wins topics converge to the newest published sequence;
- snapshot-before-delta remains true after attach, reload, and window reopen;
- p95 bridge-to-store and order-intent-to-result latency no worse than 10%
  above the recorded baseline unless the measured difference is explained and
  explicitly accepted;
- CPU and working-set memory no worse than 20% above baseline during the
  representative multiwindow soak, with no monotonic growth across repeated
  window open/close cycles;
- the focused window and stale-capability order tests pass under rapid
  Alt-Tab, Workspace focus, modal, minimize, and close sequences;
- acknowledged close/restart saves survive immediate process termination, and
  100 reload/start-stop cycles leak no Stream handlers or store writes;
- demo/replay transition fault injection restores the exact pre-transition
  live Workspaces and Link Groups without mixed state;
- intentional restart restores all valid windows while crash recovery opens
  Main only;
- install, upgrade, launch, tray reopen, Quit, and uninstall pass on a clean
  Windows 11 x64 VM with user data preserved; and
- the complete repository CI-equivalent checklist and hosted CI pass.

Measured outcomes are recorded in docs/performance.md. They are evidence for
the tested hardware and fixture, not universal latency guarantees.

## Explicit non-goals

- Native Window per Panel or Dockview popout integration.
- Ordinary Wails events for high-frequency market data.
- Moving Dockview, canvas rendering, animation, forms, or DOM focus into Go.
- Retaining the old localhost server or a browser product fallback.
- Preserving minor browser-local dismissal flags.
- In-process engine restart.
- Windows 10, ARM64, macOS, Linux, portable archives, Program Files installs,
  code signing, auto-update, and experimental WebView2 composition hosting.

## Wails references

- [Wails v3 beta.11 release](https://github.com/wailsapp/wails/releases/tag/v3.0.0-beta.11)
- [Method bindings](https://v3.wails.io/features/bindings/methods/)
- [Streams](https://v3.wails.io/guides/streams/)
- [Events](https://v3.wails.io/reference/events/)
- [Multiple windows](https://v3.wails.io/features/windows/multiple/)
- [Frameless windows](https://v3.wails.io/features/windows/frameless/)
- [Application lifecycle](https://v3.wails.io/concepts/lifecycle/)
- [Windows packaging](https://v3.wails.io/guides/build/windows/)
