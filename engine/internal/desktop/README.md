# Native Workspace host

`desktop.Host` adapts Wails WebView windows to `uistate.Store`. The store's
`WindowRegistry` is the one-Window-per-Workspace authority: opening an existing
identity activates it, opening a closed identity creates it, and the final
close reveals the tray. Native workspace actions do not use browser popup
names or cross-tab coordination; those remain only in the legacy HTTP/browser
fallback.

Window geometry and crash restoration are intentionally deferred. Close is a
durable handshake: the Wails `WindowClosing` hook cancels caption, Alt+F4, and
Top Bar close requests, emits a typed request to the owning WebView, and waits
for `WorkspaceStore` to save the live Dockview document and complete the Go
`FlushWorkspace` barrier. `CompleteWorkspaceClose` then allows disposal. A
three-second timeout offers Keep open or explicit Force close; force close
disposes without claiming the unsaved document was durable. The final native
event removes the open identity once and revokes the Workspace runtime stream
and session-owned resources without deleting the persisted Workspace.
