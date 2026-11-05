# Yahoo History

`ScreenerDailyBars` reuses this transport for the end-of-day Screener. It keeps
quote OHLC without the dividend-adjusted-close multiplier and supplies close ×
volume for the average dollar-volume estimate. Chart `DailyBars` retains its
existing adjusted behavior. Screener requests explicit daily ranges from
listing and validates coverage separately from chart archive length. The Yahoo
enable switch applies.

Unauthenticated daily-bar fallback. Never intraday/live source. Normalize ordered dates and deliberate adjusted/raw choice. Test: `go test ./internal/hist/yahoo`.
