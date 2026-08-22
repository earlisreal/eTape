# eTape Command

Production/demo/replay entry point. Boot resolves mode and runtime-profile paths, opens store/feed/brokers, resolves the persisted global active venue with a running-venue fallback, starts the active Alpaca account poller and UI hub, then coordinates shutdown. `orderConfig` writes update that runtime selection without adding a wire command. INFO lifecycle includes `etape ready` and `shutdown complete`; the existing drop watcher reports source-specific MD and execution backpressure through `sys.events` with rate-limited engine WARNs. Inputs: flags and the selected runtime profile; outputs: local app, persistence, venue traffic. The real `~/.eTape/` profile requires explicit `-profile user -allow-real-profile` opt-in. Entry: `main.go`. Test: `go test ./cmd/etape`.

With the `wails` build tag, `newWailsApp` owns one concrete `engineRuntime`. Wails service startup returns immediately after publishing `loading`; the runtime calls `bootWithOptions` asynchronously with `noLegacyHTTP`, then publishes `ready` or `failure` through the low-rate application hint queue. `RuntimeService` bindings and `Runtime.HandleStream` share `wailsruntime.Gate`. Wails `OnShutdown` closes admission and revokes sessions/Streams; service shutdown waits for the gate, cancels the engine context, and lets `bootWithOptions` perform its existing Hub, worker, and store drain once. Restart records intent synchronously, schedules quit after the binding acknowledgement window, and relaunches only from `PostShutdown` after Wails and data-lock cleanup.

The `wails,server` build is an automated-test composition only. Its capability
test starts a fresh app in a temporary `server` profile, binds Wails to
`127.0.0.1` on a random port, waits for `/health` and then the binding-level
`EnginePhase=ready`, and exercises the real generated binding and
`etape.runtime` WebSocket handler. It must not be used as the packaged product
entry point: the desktop lifecycle always boots with `noLegacyHTTP=true`, so
the historical UI Hub listener and its default port are not started.
