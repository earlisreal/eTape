# Venue Administration

Coordinates venue configuration, discovery, validation, and lifecycle-visible state. It serializes venue, data-settings, and auto-seed writes through one lock; data edits reject stale file snapshots and apply on restart. Inputs: settings/credentials; outputs: typed admin results. Never sends orders. Test: `go test ./internal/venueadmin`.
