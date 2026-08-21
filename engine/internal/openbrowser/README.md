# Browser Launch

Platform adapter opening the local UI URL after server readiness. Windows startup
uses an isolated Chrome app profile whose PID/start token and startup URL are
handed across engine restarts. Final clean exit closes only the startup page
through Chrome's loopback DevTools endpoint; child workspace pages remain open.
The temporary profile is removed after the owned Chrome process exits. The
tray's manual “Open eTape” action remains unowned. If Chrome is unavailable,
startup falls back to the Windows URL handler; macOS uses `open`, and other
Unix systems use `xdg-open`. Failure is non-fatal and logged. Test:
`go test ./internal/openbrowser`.
