# TradingView Chart Integration

Lightweight Charts adapter and chart-specific UI integration. Inputs: bar/indicator/drawing stores plus current Free Float and Alpaca borrow classification from the low-frequency Stock Detail store; these values are independent of crosshair/bar updates. Volume is the single local bar-derived Chart Indicator, labeled `Vol` directly below Float; other indicator values come from the engine store. Indicator corrections use a monotonic-safe full-data replacement, while tail growth/revision stays incremental. Outputs: imperative series/marker mutations and live legend writes. Visible labels use primary text, hidden labels are muted, and hidden values stay mounted but blank. Preserve stable controller ownership and dispose subscriptions on unmount. Test: `npm test -- tv`.

An unacknowledged live Engine-Held Stop-Limit chart gesture stays blocked and
announces **Settings → Orders & hotkeys → Review / enable live accounts** in the
preview alongside its eTape custody label. It does not open a chart disclosure,
acknowledge the account, or replay the gesture after enablement; a fresh gesture
is required after Settings shows authoritative acknowledgement status.
