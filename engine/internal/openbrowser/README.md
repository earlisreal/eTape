# Browser Launch

Platform adapter opening the local UI URL after server readiness. Windows startup
uses an isolated Chrome app profile whose PID/start token and startup URL are
handed across engine restarts. Final clean exit terminates that private Chrome
process tree, closing the startup, workspace, and News Reader windows together.
The owned adapter maximizes only the startup app window after launch; it does not
use Chrome's process-wide maximize flag, so child News Reader popups honor their size.
On a cold Windows launch it forwards each saved workspace app URL through the
same private profile, captures the new native window, reapplies its saved bounds
with `SetWindowPos` after Chrome's initial position/size flags, and maximizes it
on that monitor. It then focuses the
captured main window last. State restoration is not attempted for fallback browsers, tray launches,
non-Windows platforms, or adopted self-restarts.
The temporary profile is removed after the owned Chrome process exits. The
tray's manual “Open eTape” action remains unowned. If Chrome is unavailable,
startup falls back to the Windows URL handler; macOS uses `open`, and other
Unix systems use `xdg-open`. The UI closes its own eTape windows on a clean
engine-stop signal; fallback-browser control is best effort. Failure is
non-fatal and logged. Test: `go test ./internal/openbrowser`.

Native Scanner focus retains and validates the main HWND across engine
self-restarts, restores minimized state without changing geometry, requests
foreground activation, flashes the taskbar when Windows declines ownership, and
opens exactly one replacement in the same profile when main was closed. Fallback
browsers, tray launches, and non-Windows platforms keep best-effort browser
focus.

Owned Windows Chrome windows use the `eTape.Desktop` AppUserModelID and an explicit
taskbar icon resource matching the `.exe`/tray's tiled 75% mark. Setting only the
native BIG icon is insufficient: Explorer can still display Chrome's transparent
favicon. The SMALL title-bar favicon remains transparent; the native BIG icon
also supplies the tiled Alt+Tab image. A 500 ms check verifies the process's
PID/start token, finds new owned windows, and restores shell properties and BIG
icons if Chrome rewrites them. Native ICO entries follow each window's DPI.
Messages have a 200 ms timeout; unrelated processes are never targeted.
The icon file is cached by content under `%LOCALAPPDATA%/eTape/icons/` so engine
handoff and taskbar pins retain a readable resource. Relaunch metadata preserves
the config, UI directory, log path, and demo/live mode, and opens a fresh UI.
Pin release builds rather than `go run`'s temporary executable.
The release callback is deferred by boot for fresh and adopted browsers, so it
stops and joins the monitor before engine exit or restart. It restores only shell
properties that still match its own values and clears its own BIG handles before
destroying them, without restoring stale Chrome-owned handles. Chrome clears its
window property store when a window closes.
Unowned/manual browser launches keep Chrome's normal favicon behavior.

Focused checks: `go test -race ./internal/openbrowser`. The opt-in native smoke
test uses a separate temporary Chrome profile and checks Explorer's explicit
identity/icon resource, the actual BIG/SMALL bitmaps, favicon refresh, and restart
adoption:

```bash
go test -tags taskbar_smoke ./internal/openbrowser -run 'Test(ChromeTaskbarIcons|WindowsExecutableIcon)' -v
```

Set `ETAPE_ICON_CAPTURE_DIR` to save the extracted native icon PNGs. The test
never attaches to an existing eTape or personal Chrome session. The executable
check reads the built `dist/etape-windows-amd64.exe`; build it first to include
native PE resource verification.
