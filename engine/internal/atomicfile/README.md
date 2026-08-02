# Atomic Files

Recoverable file replacement for config/credential writes. Writes temp sibling, syncs, renames; callers own serialization/permissions. Test: `go test ./internal/atomicfile`.
