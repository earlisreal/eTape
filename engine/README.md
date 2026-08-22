# Engine

Go process owns external connections, normalized market state, persistence, scanning, execution, and UI transport.

Flow: OpenD/broker/history inputs enter `internal`; `cmd/etape` composes services; `uihub` emits JSON topics and accepts commands. Focused history warms OpenD K_1M then K_DAY first; optional Alpaca/Yahoo providers fill only persisted uncovered ranges before those seams. OpenD cache seeds enter the market-data core losslessly, ticker pushes wait behind an in-flight ticker seed per symbol, and finalized bars archive before the droppable UI update stream. The core preserves Reported Print evidence, stamps condition eligibility once after deduplication, and protects bars/marks from ineligible prices; execution recovery folds the existing event log into live orders plus a targeted 20:00 ET closed-order projection; the UI-hub mirror publishes both read-only order views. Inputs: TCP/HTTP/WebSocket, config, SQLite. Outputs: UI server, broker requests, durable state, logs.

Invariants: one normalized domain boundary; high-rate paths avoid UI framework state; live orders pass execution gates. Children: [commands](cmd/README.md), [internal packages](internal/README.md), [scripts](scripts/README.md). Test: `go test ./...`; build: `go build ./cmd/etape`.

## Wails v3 desktop shell

The native shell is pinned as Wails `v3.0.0-beta.11` in `go.mod` and
`@wailsio/runtime` `3.0.0-beta.11` in `ui/package.json`. Use the Go-module-owned
CLI; do not install or resolve a global `wails3` executable.

From this directory on Windows 11 x64:

```text
go tool wails3 task dev
go tool wails3 task build
go tool wails3 task generate:bindings
go tool wails3 task generate:wsmsg
go tool wails3 task update:build-assets
go tool wails3 task server-test
go tool wails3 task package
```

`build` runs the locked UI build, copies `ui/dist` through the existing
`internal/webui.Dist` contract, embeds it, and produces `bin/eTape.exe`.
`dev` serves the same UI through Wails' Vite integration. `package` creates the
unsigned per-user NSIS smoke installer at `bin/eTape-amd64-installer.exe`, whose
default install location is `%LOCALAPPDATA%\Programs\eTape`.

The Wails composition root is selected by the `wails` build tag and creates one
frameless Native Window per `workspace:<id>` identity without calling the legacy
browser/HTTP boot path. The desktop host owns idempotent open/focus/close cleanup,
the tray Open Main/Quit menu, and second-launch activation. The final window close
leaves the Wails process in the tray; Workspace documents are not deleted.
Wails beta upgrades are a single reviewed change: update the Go module, npm runtime,
lockfile, generated Wails assets, and these commands together. The existing
`go test ./...` and `go build ./cmd/etape` commands remain the legacy engine path
until its later engine-service migration ticket.

### Beta.11 capability proof

`cmd/etape/wails_service.go` is the small pinned-runtime capability seam. Its
generated read-only bindings live under `ui/src/gen/wails` and its
`etape.runtime` Stream proves the real desktop and server transport paths.
`internal/wailsruntime` owns the application admission gate, opaque
window/session registry, and bounded/coalescing ordinary-event hint queue.
After the runtime handshake, `cmd/etape` passes the admitted stream to
`internal/uihub.Server.HandleWailsStream`; that adapter uses the existing Hub
connection for Workspace subscriptions, snapshots, outbox ordering,
coalescing, and disconnect cleanup. The UI wrapper exposes it as the existing
`WsClient` socket seam, so snapshot/delta delivery remains imperative and
high-frequency data does not use React state or Wails events.

The focused checks are:

```text
go test -tags wails ./cmd/etape
go test -tags "wails,server" ./cmd/etape
go test ./internal/wailsruntime
go test ./internal/uihub
go test -race -tags wails ./internal/uihub ./internal/wailsruntime ./cmd/etape
go tool wails3 generate bindings -ts -i -clean=true -d ../ui/src/gen/wails -f "-tags wails" ./cmd/etape
```

Beta.11 exposes the binding caller as `application.WindowKey` and the Stream
owner as `StreamConn.Window`; the latter is intentionally nil in server mode.
`TrySend` retains the supplied slice and exposes bounded-send failure, so the
eTape Wails adapter copies each frame before handing it off. The eTape queue
keeps `OutBuf` lossless frames, at most 256 unique latest-wins keys, and at most
8 MiB; beta.11 adds a 256-frame/8 MiB per-StreamConn queue. Replacements keep
their original position, while overflow closes explicitly instead of dropping
ordered data. Wails events are app-wide broadcasts and beta.11's internal event
mailbox is not a correctness queue, so the runtime-owned bounded/coalescing
hint queue and its single dispatcher are the only path to low-rate invalidation
events. The queue keeps the newest revision per identity. Quotes, targeted
Workspace updates, persistence, and order-critical work remain outside
ordinary events.

The server test is authoritative for the test-only path: it uses the same
services and Stream handler, `ETAPE_PROFILE=server`, a temporary data root,
loopback random-port binding, `/health`, and `Capabilities.EnginePhase=ready`.
Packaged/native smoke, Playwright, 100-reload/100-cycle cleanup, and the
four-Stream ten-second soak remain merge-gate checks; do not substitute the
legacy HTTP bridge for them.

Shutdown registers the gate's non-blocking stop hook before Wails service
shutdown. Admitted gate contexts are canceled, session capabilities are
revoked, tracked Streams are closed, and `ServiceShutdown` waits before the
next lifecycle phase drains the engine.
