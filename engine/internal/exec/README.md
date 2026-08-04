# Execution Core

Broker-neutral lifecycle, gates, routing, reconciliation, and round-trip tracking. The Account projection uses scheduled NYSE close-to-close cycles: closing fills accumulate cycle P&L, open symbols retain partial-exit realization, and a close rebases carried positions to their latest marks. SQLite checkpoints plus fill replay recover the projection; no-checkpoint boots rebase broker positions at zero displayed P&L. Broker `DayPnL` remains untouched and continues to drive max-day-loss gates. Test: `go test ./internal/exec`.
