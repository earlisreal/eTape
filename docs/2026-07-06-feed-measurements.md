# Feed measurements — 2026-07-06 pre-market (Monday checklist §5)

Measured ~04:53–05:05 ET with live pre-market movers (FXHO/ZCMD/LHSW/MSS/YHC —
Earl asked for the movers specifically) alongside mega-caps. Scripts:
`prototypes/push_cadence_measure.py`, engine `cmd/etape` run, and
`prototypes/tz_ws_staleness_observe.py`. Raw captures in `prototypes/captures/`.
Pre-market caveat: hot gappers are at peak activity (representative for the
scanner/day-trade case); mega-caps are far quieter than they will be at RTH.

## Push cadences (per symbol, OpenD → client)

- **ORDER_BOOK: hard ~300 ms cadence** (median gap 297–304 ms on every active
  symbol; p95 ≈ 305–560 ms) — this is OpenD's push-frequency setting, ~3.3
  pushes/s/symbol ceiling. DOM repaint faster than 300 ms buys nothing unless
  the OpenD setting is lowered (it's configurable in OpenD — decide after
  living with the DOM).
- **QUOTE / TICKER: event-driven**, no fixed floor — hottest observed
  (FXHO) 6.9 pushes/s with **87 ms median gap**; ~9.5 ticks/s on the tape.
  TICKER batches ticks per push: ~1.4–3 ticks/push on hot small caps, up to
  ~36 ticks/push on quiet-cadence mega-caps.
- **K_1M**: up to ~3.8 pushes/s on hot symbols (every in-progress bar update).
- Aggregate: 7-symbol hot watchlist ≈ **58 events/s**; single hottest symbol
  ≈ 19 pushes/s across subtypes.
- **UI consequence: the 30 Hz coalescing default has ample headroom** — even
  the hottest symbol's combined push rate is under it, and per-surface
  keep-latest coalescing makes rate spikes irrelevant. No change needed.

## L2 depth on the movers (DOM verification)

FXHO / ZCMD / LHSW all delivered the **full 10×10 book** on US LV3 — penny
spreads on ZCMD (0.31%) and LHSW (0.29%), FXHO wider (1.35%). The ladder works
on exactly the class of stock eTape targets.

## Journal volume (engine run, 12-symbol watchlist, 2 focused)

`cmd/etape` live for ~11.1 min (5 hot movers + 7 liquid names; focus =
YHC + AAPL): **+37.7 MB → ~3.4 MB/min ≈ 204 MB/hour**.
Naive extrapolation: RTH-only ≈ 1.3 GB, full 04:00–20:00 ≈ 3.3 GB/day —
**~6× above the design's 100–500 MB/day estimate** for a hot watchlist.

Composition (journal table, 21,297 rows):

| kind | rows | bytes | avg/row |
|---|---|---|---|
| book | 3,019 | 21.4 MB | **7,098 B** |
| ticks | 8,659 | 3.9 MB | 453 B |
| bars1m | 6,500 | 2.5 MB | 391 B |
| quote | 3,115 | 0.5 MB | 175 B |

**Book snapshots are 75% of all journal bytes** (full 10-level JSON per push,
only 2 focused symbols). Encoding decision input: leave ticks/quotes/bars as
JSON (they're small); the win is entirely in the book payload — top candidates:
compress book JSON (books compress 10–20×), delta-encode levels, or truncate
archived depth. Decide at the next engine design touch; nothing blocks v1
(30-day retention ≈ ~40 GB worst case, tolerable but wasteful).

Also confirmed: engine's `seed bars1m` races the K_1M subscription on startup
(proto 3006 "please subscribe first" WARN, non-fatal — backfill covers it).
Minor engine bug, carried.

## Quota accounting under load

Exactly as documented, verified live: engine watchlist = 12×(TICKER+K_1M) +
2×(QUOTE+ORDER_BOOK) = **28 slots**; a second connection subscribing
overlapping pairs added only its *new* (code,subtype) pairs; closing a
connection released its pairs immediately (72/100 remained free throughout).
Historical quota: 4/100 used (the depth probes). No surprises.

## TZ Portfolio WS staleness (checklist §4 carry-over, closed)

10-min passive observation: **WebSocket protocol pings every ~54 s** when the
account is idle (11 pings / 600 s; no app-level frames). Staleness rule for the
Go adapter: read deadline ~120 s (two missed pings) → reconnect + REST
re-snapshot. Side-find: subscription name `Account` is rejected
(`INVALID_DATA`) — valid subscriptions are `Order` and `Position`.
