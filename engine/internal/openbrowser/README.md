# Browser Launch

Platform adapter opening the local UI URL after server readiness. Windows startup
uses an isolated Chrome app profile whose PID/start token and startup URL are
handed across engine restarts. Final clean exit terminates that private Chrome
process tree, closing the startup, workspace, and News Reader windows together.
The owned adapter maximizes only the startup app window after launch; it does not
use Chrome's process-wide maximize flag, so child News Reader popups honor their size.
On a cold Windows launch it may then forward saved workspace app URLs through the
same private profile with initial position/size flags, and focuses the captured main
window last. State restoration is not attempted for fallback browsers, tray launches,
non-Windows platforms, or adopted self-restarts.
The temporary profile is removed after the owned Chrome process exits. The
tray's manual “Open eTape” action remains unowned. If Chrome is unavailable,
startup falls back to the Windows URL handler; macOS uses `open`, and other
Unix systems use `xdg-open`. The UI closes its own eTape windows on a clean
engine-stop signal; fallback-browser control is best effort. Failure is
non-fatal and logged. Test: `go test ./internal/openbrowser`.
