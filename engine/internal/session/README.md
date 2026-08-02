# Sessions

US Eastern pre-market, RTH, post-market, and closed classification. The embedded 2000+ NYSE calendar covers weekends, observed recurring holidays, Good Friday, Juneteenth (2022+), recurring early closes, and explicit exceptional closures. `Schedule`, `IsTradingDay`, and adjacent-session helpers are offline and DST-correct; unknown future emergency closures may be learned by one empty history fetch. Test: `go test ./internal/session`.
