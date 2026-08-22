# Replace the browser host with Wails v3

Status: Accepted

eTape will replace its browser-hosted product runtime with a Windows-first Wails
v3 desktop application. One Native Window will host each Workspace and Dockview
will continue to own Panel Group layout. Closing a Workspace window will preserve
the Workspace and leave the engine available from the Wails system tray. Selected
native Panel or Panel Group detachment remains a later product decision, not part
of the migration.

Wails will own application assets, Native Window lifecycle, OS integration, and
cross-window coordination. Go will be authoritative for the Workspace catalog,
globally persisted Link Group focus, drawings, preferences, and focused order-
hotkey target. Each WebView will retain Dockview, imperative rendering stores,
canvas controllers, animation scheduling, forms, and other view-local state. A
background Workspace must never receive a scoped order hotkey when its Native
Window is not focused.

Generated Wails bindings will carry typed commands and queries. One Wails Stream
per Workspace will carry subscriptions, demands, indicators, snapshots, and
continuous market updates while preserving eTape's existing ordering,
coalescing, and lossless versus latest-wins rules. Wails events are reserved for
low-rate app-wide lifecycle and invalidation hints; targeted notifications use
the owning Workspace Stream. The existing Go DTOs and tygo generation remain
the Stream schema; this is a build-time choice, not a browser runtime dependency.

All bindings and Stream handlers pass through an eTape-owned admission and
in-flight gate. Shutdown rejects new work and joins admitted transport work
before the existing engine drain and store close. Restart returns its binding
result before asynchronously beginning that sequence. Wails' event queues,
binding cancellation, and Stream cleanup are not treated as persistence or
ordering guarantees.

The shipped application will have no localhost HTTP/WebSocket transport or
browser fallback. It will use a frameless Top Bar with lightweight native drag
regions, avoiding experimental WebView2 composition hosting. Process restarts
will restore the open Workspace set and native window geometry. Existing
`~/.eTape` data will be preserved through explicit migration and backup; minor
browser-local dismissal state may reset. The initial Windows distribution will
be an unsigned per-user NSIS installer under LocalAppData. Workspace close
acknowledgements are durable, demo/replay transitions use a transactional
recovery journal, and Wails single-instance activation does not replace the
data-root/DB integrity lock.
