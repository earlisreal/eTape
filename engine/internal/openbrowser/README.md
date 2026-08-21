# Browser Launch

Platform adapter opening the local UI URL after server readiness. Windows startup
uses an isolated Chrome app profile whose PID/start token is handed across
engine restarts and closed on final clean exit; the tray's manual “Open eTape”
action remains unowned. If Chrome is unavailable, startup falls back to the
Windows URL handler; macOS uses `open`, and other Unix systems use `xdg-open`.
Failure is non-fatal and logged. Test: `go test ./internal/openbrowser`.
