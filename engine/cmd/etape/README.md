# eTape Command

Production/demo entry point. Boot resolves mode and paths, opens the
store/feed/brokers, starts one engine-wide account poller, and serves the UI
hub. Live venues are always polled for risk; non-risk accounts are polled only
when an Account panel demands them. Link Groups own order routing; persisted
legacy `activeVenue` values are ignored. INFO lifecycle includes `etape ready`
and `shutdown complete`; the existing drop watcher reports source-specific MD
and execution backpressure through `sys.events` with rate-limited engine
WARNs. Inputs: flags and `~/.eTape/`; outputs: local app, persistence, venue
traffic. Entry: `main.go`. Test: `go test ./cmd/etape`.
