# eTape Command

Production/demo/replay entry point. Boot resolves mode and paths, opens store/feed/brokers, starts schedulers and UI hub, then coordinates shutdown. INFO lifecycle includes `etape ready` and `shutdown complete`; the existing drop watcher reports source-specific MD and execution backpressure through `sys.events` with rate-limited engine WARNs. Inputs: flags and `~/.eTape/`; outputs: local app, persistence, venue traffic. Entry: `main.go`. Test: `go test ./cmd/etape`.
