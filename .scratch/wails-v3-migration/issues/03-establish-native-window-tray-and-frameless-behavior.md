# 03 — Establish Native Window, tray, and frameless behavior

**What to build:** Make the Wails shell behave as a manageable Windows desktop application: Main and one additional Workspace have stable Native Windows, the final closed window leaves the application in the tray, a second launch activates the existing process, and the frameless Top Bar retains accessible Windows window controls.

**Blocked by:** 02 — Pin Wails and boot a packaged Main shell.

**Status:** ready-for-agent

- [ ] Opening an already-open Workspace focuses its existing Native Window; closing and reopening it does not create a duplicate identity or stale registry entry.
- [ ] Closing the final visible Workspace window leaves the process available in the Wails system tray, and the tray can reopen or focus Main without starting another engine.
- [ ] Single-instance activation focuses Main while the independent resolved-data-root and database lock remains the storage-integrity authority.
- [ ] The frameless Top Bar provides keyboard-accessible minimise, maximise or restore, and close controls with correct accessible names and focus behavior.
- [ ] Only unused Top Bar surface is draggable; interactive Top Bar controls and every Dockview surface are non-draggable, while Panel Header dragging remains functional.
- [ ] Dragging, every resize edge and corner, caption actions, Alt+F4, Alt+Space, Win+Arrow, Win+Z, snapping, and cross-monitor movement work at 100%, 150%, 200%, and mixed-monitor DPI with experimental composition hosting disabled.
- [ ] Automated policy tests cover window identity validation, idempotent open/focus, close cleanup, last-window tray behavior, and single-instance/data-lock ordering; a packaged Windows smoke covers behavior that server mode cannot prove.
- [ ] Native-shell documentation records the supported behavior and any accepted beta limitation without promising Panel or Panel Group detachment.
