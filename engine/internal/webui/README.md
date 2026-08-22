# Embedded Web UI

Owns the narrow embedded-distribution contract shared by the legacy browser host
and the Wails shell. Input: generated `ui/dist`; output: an `fs.FS` from `Dist()`
for Wails' asset handler. Never hand-edit embedded build output. Test:
`go test ./internal/webui`.
