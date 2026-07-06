# TradeZero normalization fixtures

`order_partial_fill.json` and `order_short_new.json` are **authored** from the
documented Portfolio-WS order shape (`docs/2026-07-03-tradezero-api.md`), not
captured from real order-flow frames — capturing those needs real orders,
which the eTape safety rule (never place/modify/cancel real orders without
Earl's explicit go-ahead in the running conversation) forbids doing casually.

Replace/augment with real frames captured in an authorized live session when
one runs. Keep the `tzOrder` decoder tolerant of unknown fields so it survives
whatever extra fields a real capture reveals.
