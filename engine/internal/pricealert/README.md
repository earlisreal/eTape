# Price alerts

Engine-owned, durable one-shot notifications on the market-data core's fresh
Last-Eligible Price lane. This package has no execution or broker dependency.
Definitions and independent trigger snapshots are stored through the existing
single SQLite writer, atomically saving a trigger with its definition's state.

Definitions optionally retain `sourceGroup`, the red/green/blue/yellow Link
Group captured at creation. Upsert validates supplied colors and preserves the
saved source on edits and Rearm. Each trigger snapshots that source for popup
navigation after definition changes/deletion. Missing metadata in old JSON or
an ungrouped source remains valid; it affects UI navigation only, never monitoring.
The existing JSON payload storage needs no table migration.

`Run` consumes the bounded `Feed` queue and publishes only lifecycle changes;
ordinary trades never become React updates. Queue overflow and feed gaps invalidate
queued prices. Every create, edit, resume, restart, and selected-session transition
requires a subsequent eligible print. Quiet symbols do not time out solely because
they have not traded. Explicit pause and triggered states survive restart.

The engine application owns one background ticker demand per enabled symbol in
its selected sessions. OpenD admits background demands after ordinary demands,
shares acknowledged ticker coverage, and preserves minimum subscription holds.
Waiting alerts expose data/capacity/session states and recover automatically.

Every trigger has a stable snapshot independent of later editing, rearming, or
deleting its definition. History has no automatic purge. A failed trigger write
is retained in memory and retried with a visible storage error; durable recovery
updates history silently. Normal triggers create short-lived burst claims for
one popup owner and one sound play across the engine's WebSocket connections.
Those live claims are never persisted or replayed on subscribe/restart.

Shutdown joins `Run` before closing the store. Test with
`go test ./internal/pricealert` and `go test -race ./internal/pricealert`.
