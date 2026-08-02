# Simulator Broker

Paper execution against current book state. Models resting orders, book walking, partial fills, slippage, and latency. Output uses real-adapter lifecycle types; deterministic tests inject clock/randomness. Test: `go test ./internal/broker/sim`.
