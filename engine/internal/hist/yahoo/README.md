# Yahoo History

`ScreenerDailyBars` reuses this transport for the end-of-day Screener. It keeps
quote OHLC without the dividend-adjusted-close multiplier and supplies close ×
volume for the average dollar-volume estimate. Chart `DailyBars` retains its
existing adjusted behavior. Screener requests explicit daily ranges from
listing and validates coverage separately from chart archive length. The Yahoo
enable switch applies.

When OpenD discovery is unavailable, the Screener also uses the
[Yahoo Finance equity catalog](https://finance.yahoo.com/research-hub/screener/equity/)
for listed symbols, first-trade dates and market-cap estimates. It uses the same
anonymous cookie and crumb handshake as Stock Info, paginates under the Yahoo
history budget and fails the discovery if a page is missing.

Daily history requires no credentials. Test: `go test ./internal/hist/yahoo`.
