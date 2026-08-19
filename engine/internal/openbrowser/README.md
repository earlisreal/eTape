# Browser Launch

Platform adapter opening the local UI URL after server readiness. Windows uses
Chrome `--app=<url> --start-maximized` when Chrome is found, falling back to the Windows URL
handler; macOS uses `open`, and other Unix systems use `xdg-open`. Failure is
non-fatal and logged. Test: `go test ./internal/openbrowser`.
