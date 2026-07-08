# eTape

A local trading platform ("reading the tape"): consumes broker market-data feeds and
renders candlestick charts, a Level 2 DOM ladder, and time & sales. Go engine, React/TS
UI. Full design rationale lives in `docs/` and `CLAUDE.md`; this file is the practical
"how do I run it" reference.

## Prerequisites

- **Go** (≥ 1.26.4, pinned by `engine/go.mod`) and **Node.js** (LTS 22.x) toolchains
  installed and on `PATH`.
- For live mode: [moomoo OpenD](https://openapi.moomoo.com/) running locally and logged
  in (default `127.0.0.1:11111`).
- macOS/Linux use `./run.sh`; Windows uses `run.cmd` (see [Running on Windows](#running-on-windows)).
  Go, Node.js, and OpenD all ship Windows builds, so the same from-source workflow applies.

### Installing the Go and Node.js toolchains

Download and run the official installers (Windows and macOS both offered there):

- **Go:** https://go.dev/dl/ — on Windows grab the amd64 `.msi`; it adds Go to `PATH`
  automatically. Latest stable satisfies the `≥ 1.26.4` pin (modern Go auto-fetches the
  exact toolchain on first build if the installed one is older).
- **Node.js:** https://nodejs.org/ — take the **LTS** installer, which bundles `npm`.
  On Windows keep the default "Add to PATH".

After installing, **open a new terminal** (installers don't update already-open shells)
and confirm:

```
go version      # >= 1.26.4
node --version  # 22.x LTS
npm --version
```

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

## Running on Windows

`run.cmd` is the Windows equivalent of `run.sh` — **same three modes, same
arguments**. Just substitute `run.cmd` for `./run.sh`:

```
run.cmd dev ladder-tape
run.cmd demo
run.cmd demo 2026-01-02 0
run.cmd live
run.cmd live -watch=AAPL,TSLA -focus=AAPL
```

`run.cmd` is a thin shim over `run.ps1`; it sets the PowerShell execution policy
for that one invocation (`-ExecutionPolicy Bypass`), so there's nothing to
configure first. It targets the built-in Windows PowerShell 5.1 — no extra
install. Setup on the Windows machine:

1. `git clone` the repo.
2. Install the Go and Node.js toolchains (ensure both are on `PATH`).
3. For live mode, install and launch **moomoo OpenD for Windows**, logged in
   (still `127.0.0.1:11111`), and put your config at
   `%USERPROFILE%\.eTape\config.toml` (the Windows home dir — same layout as
   `~/.eTape/` on macOS; see [Configuring live mode](#configuring-live-mode)).
4. `run.cmd live` (add `-focus=SYM` for the DOM ladder, as on macOS).

Nothing else differs: the engine is pure Go (no cgo) and resolves the home
directory per-OS, so charts, ladder, and tape behave identically.

## Configuring live mode

The engine reads `~/.eTape/config.toml` at boot (on Windows,
`%USERPROFILE%\.eTape\config.toml`). **A missing file is not an error** —
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
