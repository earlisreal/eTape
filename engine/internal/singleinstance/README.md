# Single Instance

Cross-platform process mutex preventing duplicate app instances against same local state/ports. Acquire before mutable services; always release on clean shutdown. Test: `go test ./internal/singleinstance`.
