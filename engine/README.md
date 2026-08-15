# Engine

Go process owns external connections, normalized market state, persistence, scanning, execution, and UI transport.

Flow: OpenD/broker/history inputs enter `internal`; `cmd/etape` composes services; `uihub` emits JSON topics and accepts commands. Focused history warms OpenD K_1M then K_DAY first; optional Alpaca/Yahoo providers fill only persisted uncovered ranges before those seams. OpenD cache seeds enter the market-data core losslessly, ticker pushes wait behind an in-flight ticker seed per symbol, and finalized bars archive before the droppable UI update stream. The core preserves Reported Print evidence, stamps condition eligibility once after deduplication, and protects bars/marks from ineligible prices; execution recovery folds the existing event log into live orders plus a targeted 20:00 ET closed-order projection; the UI-hub mirror publishes both read-only order views. Inputs: TCP/HTTP/WebSocket, config, SQLite. Outputs: UI server, broker requests, durable state, logs.

Invariants: one normalized domain boundary; high-rate paths avoid UI framework state; live orders pass execution gates. Children: [commands](cmd/README.md), [internal packages](internal/README.md), [scripts](scripts/README.md). Test: `go test ./...`; build: `go build ./cmd/etape`.
