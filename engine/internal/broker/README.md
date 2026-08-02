# Broker Adapters

Normalize venues behind `exec.Broker`. Upstream: execution core. Downstream: REST/WebSocket/OpenD or simulator. Children: [Alpaca](alpaca/README.md), [moomoo](moomoo/README.md), [TradeZero](tradezero/README.md), [sim](sim/README.md), [network helpers](netx/README.md). Adapters never bypass gates; ambiguous transport reconciles by stable IDs. Test: `go test ./internal/broker/...`.
