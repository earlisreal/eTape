# eTape — Stack Decision

**Date:** 2026-07-03
**Status:** Decided

## What is eTape

A personal trading platform ("electronic tape" / "Earl's tape" — *reading the tape*).
Local application that consumes several websocket feeds from one or two brokers plus
multiple REST API calls, and renders candlestick charts (OHLCV + indicators), a Level 2
DOM ladder, and a time & sales window.

**Priorities:** runtime speed, execution, stability. All code will be AI-written
(Claude), so reviewability and compiler-enforced safety weigh heavily.

## Decisions

| Concern | Decision |
|---|---|
| Engine language | Go |
| Frontend language | TypeScript |
| Frontend framework | React + Vite |
| Candlestick charts | TradingView Lightweight Charts |
| L2 ladder / DOM | Custom canvas rendering |
| Time & sales | Virtualized list over a ring buffer |
| Engine ↔ UI integration | WebSocket + JSON over localhost; TS types generated from Go structs (`tygo`) |
| Packaging | Browser tab at localhost first; wrap with Wails v3 later (Electron fallback) |

## Rationale

### Go for the engine

- Latency reality: retail broker feeds arrive over TLS websockets tens of milliseconds
  away. Any compiled language saturates this; C++/Rust's headroom is unusable. The real
  differentiators are stability under long uptimes and concurrency ergonomics.
- Goroutines/channels fit the workload exactly: N websocket connections, reconnection
  logic, order submission, and REST polling running concurrently.
- Stability by construction: memory safety, explicit error handling, single static
  binary, sub-millisecond GC pauses.
- AI-written code: Go is the most reviewable mainstream language (gofmt uniformity, one
  idiomatic way, no clever abstractions) and its near-instant compile/test loop makes
  the AI iteration cycle fast. C++ was ruled out specifically because undefined
  behavior compiles cleanly — neither the compiler nor a non-expert reviewer catches
  AI-introduced memory bugs.

### TypeScript + React for the UI

- Lightweight Charts is a JS library; the web ecosystem owns trading UI rendering.
- React renders the app *chrome* only (layout, order entry, watchlists, settings).
  **Rule: high-frequency data never flows through framework state.** Chart, ladder, and
  tape are canvas surfaces mounted once via refs and painted imperatively.
- Rendering model: book/tape updates are applied to in-memory state at message rate;
  painting is coalesced to one repaint per `requestAnimationFrame` tick (frame rate =
  monitor refresh rate, provided each repaint fits the frame budget). No data is lost —
  the latest state is what gets drawn. Throttling to 30–60fps is a one-line choice.
- L2 + T&S at retail feed rates are comfortably within browser capability (this is how
  Binance/Kraken/Bybit web terminals work). Full-depth Bookmap-style heatmaps would
  need WebGL — out of scope for now.

### Integration

- Go and TS are separate runtimes; the boundary is message passing. The
  performance-critical path (feed parsing, book building, indicators, order logic)
  never crosses it — only coalesced, already-throttled UI state does.
- Start: WebSocket + JSON over loopback (microseconds), types kept in sync by
  generating TS from Go structs with `tygo`. Upgrade path if the contract grows:
  Connect RPC/protobuf. Under Wails: generated bindings replace the socket.

### Packaging

- Browser-first: fastest dev loop, Chrome DevTools, multi-monitor via multiple windows.
  Known drawbacks: background-tab rAF throttling, no global hotkeys, accidental
  close/refresh.
- Wails (Go-native desktop shell, OS webview, single binary, generated Go↔TS bindings)
  is the target wrapper. **Caveat:** Wails v2 (stable) is single-window; v3 has
  multi-window but is still alpha (v3.0.0-alpha.96, May 2026). Decision deferred
  safely: the engine stays UI-agnostic, so wrapping is a packaging step, not a rewrite.
  If multi-window is needed before v3 stabilizes, Electron is the fallback
  (identical Chromium everywhere, `backgroundThrottling: false`).
- macOS note: Wails renders in WKWebView (Safari engine), not Chrome — test in it.

## Rejected alternatives

- **C++**: speed advantage unusable behind retail latency; stability priority cuts
  against it (UB, memory bugs) especially with AI-written code; slow toolchain.
- **Rust**: same performance class as C++ with safety, and AI authorship closes most of
  its dev-cost gap — but still pays slower build cycles for unusable headroom.
- **Full TypeScript (Node/Bun) engine**: fast enough, but single-threaded event loop
  couples indicator computation to websocket handling; loses to Go on stated priorities.
- **Python engine**: best indicator/backtesting ecosystem, slowest runtime.
- **Native GUI toolkits (Qt, Fyne, Flutter)**: forfeit Lightweight Charts and the web
  charting ecosystem.

## Open questions (for the design phase)

- Which brokers/feeds (affects auth, feed formats, rate limits)
- Historical OHLCV storage (likely SQLite; depends on backtesting ambitions)
- Backtesting scope
- Order-management safety rules (kill switch, max position, duplicate-order guards)
- Indicator set for v1

## Related local projects

`~/Projects/lightweight-charts` and `~/Projects/earlisreal-lightweight-charts`
(existing forks), `~/Projects/wickplot`, `~/Projects/premarket-alert` — prior art and
possible reuse.
