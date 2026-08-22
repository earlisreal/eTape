# Wails runtime admission

`Runtime` is the single application-owned boundary for native transport work.
Every Wails binding that touches runtime state calls `EnterContext`, and every
Wails Stream handler holds the same gate for its full connection lifetime.
The returned context is canceled when shutdown begins; handlers must honor it
and always release their admission slot.

Shutdown is ordered by the concrete `engineRuntime` in `engine/cmd/etape`:

1. Wails invokes `BeginStop`, which rejects new admissions, revokes opaque
   sessions, cancels admitted contexts, and closes tracked Streams.
2. `ServiceShutdown` waits for the gate to reach zero.
3. The engine context is canceled, allowing the existing ordered drain to join
   Hub, feed, backfill, execution, and transport workers before `Store.Close`.

The Wails build does not start the legacy localhost HTTP listener. Boot state
is `loading`, `ready`, or `failure`; only low-rate state hints use ordinary
Wails events. The lifecycle owner is deliberately concrete and can start only
once. Restart intent is recorded before the binding returns, quit is delayed
for the binding acknowledgement, and replacement launch belongs to Wails
`PostShutdown` after application and data-root resources are released.

## Stream protocol and readiness

`etape.runtime` accepts exactly one first frame containing protocol `1`, the
Workspace ID, and the opaque session issued by `OpenStreamSession`. Server
mode resolves the Workspace against the per-runtime registry and ignores any
browser-supplied window identity; desktop mode additionally checks
`StreamConn.Window` against the native Workspace owner. Malformed JSON,
unsupported protocol, unknown/mismatched Workspace, stale session, and native
window mismatch receive an explicit `rejected` reply before the handler can
touch Hub state. Sessions are revoked when their handler returns.

Shutdown sends `stopping / engine stopped` before closing a terminal Stream;
self-restart sends `restarting / restarting`, so the UI reconnects rather than
entering its terminal state. Transport overflow may send `disconnected` as a
best-effort protocol frame and always closes the Stream; it is never converted
into silent loss. The handler and the Hub own cleanup of their respective
registrations, while the shared admission gate prevents late bindings or
Streams from mutating state after stop begins.

The server test waits for both Wails `/health` and the binding-level
`Capabilities.EnginePhase == "ready"`. It uses a loopback random port,
`ETAPE_PROFILE=server`, a temporary data root, and a fresh identity registry.
Those settings are test-only; packaged/native Wails smoke and browser stress
checks remain merge-gate validation.
