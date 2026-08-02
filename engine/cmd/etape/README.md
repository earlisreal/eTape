# eTape Command

Production/demo/replay entry point. Boot resolves mode and paths, opens store/feed/brokers, starts schedulers and UI hub, then coordinates shutdown. Inputs: flags and `~/.eTape/`; outputs: local app, persistence, venue traffic. Entry: `main.go`. Test: `go test ./cmd/etape`.
