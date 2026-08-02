# moomoo Broker Adapter

Native OpenD `Trd_*` execution adapter. Inputs: account selection and normalized orders; outputs: normalized pushes/snapshots. Trade unlock stays in OpenD GUI. Verify DayPnL semantics before relying on global loss enforcement. Test: `go test ./internal/broker/moomoo`.
