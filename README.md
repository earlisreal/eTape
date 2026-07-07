# eTape

A local trading platform ("reading the tape"): consumes broker market-data feeds and
renders candlestick charts, a Level 2 DOM ladder, and time & sales. Go engine, React/TS
UI. Full design rationale lives in `docs/` and `CLAUDE.md`; this file is the practical
"how do I run it" reference.

## Prerequisites

- Go and Node.js toolchains installed.
- For live mode: [moomoo OpenD](https://openapi.moomoo.com/) running locally and logged
  in (default `127.0.0.1:11111`).

## Running

`./run.sh <mode>` has three modes:

```
./run.sh dev [FIXTURE]      Mock WS engine + Vite dev server, hot reload, for UI work.
                             FIXTURE selects ui/fixtures/<name>.json (default: session-basic).

./run.sh demo [DAY] [SPEED] Real engine replaying a synthetic day. No OpenD/broker needed.
                             DAY defaults to 2026-01-02, SPEED to 1 (real-time; 0 = as fast as possible).

./run.sh live [ENGINE_FLAGS] Real engine against ~/.eTape/config.toml — live OpenD feed
                             (+ real venues, if configured). Requires OpenD already
                             running and unlocked. See "Configuring live mode" below.
```

Examples:

```
./run.sh dev ladder-tape
./run.sh demo
./run.sh demo 2026-01-02 0
./run.sh live
./run.sh live -watch=AAPL,TSLA -focus=AAPL
```

All three modes build the UI first and serve it from the engine at
`http://127.0.0.1:8686`.

## Configuring live mode

The engine reads `~/.eTape/config.toml` at boot. **A missing file is not an error** —
it silently falls back to built-in defaults (see `engine/internal/config/config.go`:
`Default()`), which have an **empty watchlist and no execution venues**. That means with
no config file, OpenD is never told to subscribe to anything — the UI will connect fine,
but every chart/ladder/tape panel stays empty.

Minimal config to get market data flowing:

```toml
[feed]
watchlist = ["US.AAPL", "US.TSLA", "US.NVDA", "US.SPY"]
```

Any section you omit falls back to its default — you don't need to repeat the whole
schema, just the fields you want to change.

### Watch vs. focus — two different entitlement levels

Symbols get subscribed to OpenD in one of two profiles:

| Profile | Source | Grants | Missing |
|---|---|---|---|
| **Watch** | `[feed] watchlist` in config.toml, or `-watch=SYM1,SYM2` | tape, 10s/1m bars | **no order-book depth** |
| **Focus** | `-focus=SYM1,SYM2` CLI flag only (no config.toml equivalent) | full depth + tape + bars | — |

The **DOM Ladder panel needs Focus** (it renders the order book). Watch alone is enough
for charts and time & sales. There is currently no way to set a boot-time focus list from
config.toml — pass `-focus` on the command line every time, e.g.:

```
./run.sh live -focus=AAPL
```

`-watch`/`-focus` values are merged with (not a replacement for) `[feed] watchlist` at
boot; `run.sh live` passes any extra arguments straight through to the engine binary.

### Execution venues (opt-in, off by default)

With no `[[venue]]` section, execution stays fully disabled — the UI will show
`0 venues configured`  and every order is blocked. Adding a venue is a separate,
deliberate step (see `docs/superpowers/specs/2026-07-04-multi-broker-execution-design.md`
for the multi-broker execution design) — don't add one without meaning to, since a
`live`-env venue trades with real funds.

## Where to look next

- `CLAUDE.md` — architecture, stack decisions, moomoo/OpenD protocol notes, safety rules.
- `docs/superpowers/specs/` — approved design specs (UI, execution, Go engine).
- `docs/superpowers/plans/` — implementation plans for completed feature work.
