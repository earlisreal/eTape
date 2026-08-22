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
