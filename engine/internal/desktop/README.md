# Native Workspace host

`desktop.Host` adapts Wails WebView windows to `uistate.Store`. The store's
`WindowRegistry` is the one-Window-per-Workspace authority: opening an existing
identity activates it, opening a closed identity creates it, and the final
close reveals the tray. Native workspace actions do not use browser popup
names or cross-tab coordination; those remain only in the legacy HTTP/browser
fallback.

Window geometry and crash restoration are intentionally deferred. Close is a
runtime registry operation and does not delete the persisted Workspace.
