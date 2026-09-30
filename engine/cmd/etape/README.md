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

On clean shutdown, pretrigger engine-held stop-limits are durably paused and
working children receive persisted cancel intent before the process exits. If
the venue cannot confirm cancellation within the bounded shutdown window, the
console/tray asks whether to force exit or restart eTape so recovery can
reconcile the order. A crash or force exit is not proof that a live child was
canceled.
