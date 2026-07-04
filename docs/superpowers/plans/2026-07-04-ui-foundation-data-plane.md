# eTape UI — Plan 1 of 6: Foundation & Data Plane

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the eTape SPA shell and its complete data plane — two browser windows that connect to the engine over WebSocket, subscribe to the topics their visible panels declare, hydrate plain-TS stores via snapshot-then-delta, repaint canvas surfaces through one rAF scheduler per window, host dockview panels that survive drag/resize with auto-saved layouts, and share link-group focus across windows — all developed and tested against a mock engine and recorded fixtures.

**Architecture:** One Vite + React + TS SPA in `ui/`, four layers with a strict one-way dependency direction `chrome → render → data → wire` (never backwards). `wire/` owns the WS connection and decode; `data/` holds plain-TypeScript stores (zero React) that mark a dirty flag on update; `render/` holds pure canvas painters plus one rAF scheduler that paints every dirty surface once per frame; `chrome/` is React — the dockview shell, panel frames, and low-rate tables. High-frequency market data never flows through React state. Because the Go engine does not exist yet, this plan builds against a **mock engine** (a small Node WS server that replays fixtures) and an **interim hand-authored wire contract** that mirrors the engine design's `uihub/wsmsg` structs field-for-field, so the future tygo-generated `ui/src/gen/*` is a drop-in replacement.

**Tech Stack:** TypeScript 5, React 18, Vite 5, Vitest (unit + jsdom component tests), `ws` (Node mock engine), dockview (dockable panels). Lightweight Charts v5 (≥5.2.0), node-canvas golden images, and Playwright arrive in later plans.

## Global Constraints

Copied verbatim from the three approved specs. Every task's requirements implicitly include this section.

- **Hard rule:** high-frequency data (chart/ladder/tape/book/quote) never flows through React state. These are canvas surfaces mounted once via refs, painted imperatively, coalesced to one repaint per rAF tick. React renders chrome only. (stack-decision, ui-design §Architecture)
- **Dependency direction:** `chrome → render → data → wire`, never backwards. A layer never imports from a layer above it. (ui-design §Architecture)
- **Honesty policy:** never render stale as live; never render in-flight as done. Staleness is judged from health topics only — never from "no messages lately" (a quiet symbol is not a stale symbol). (ui-design §Error handling)
- **Engine address:** the engine's `uihub` serves HTTP + `/ws` on `127.0.0.1:8686` (config). In dev, the Vite dev server proxies `/ws` to it. (go-engine-design §uihub)
- **Wire format:** WebSocket + JSON. Each topic delivers a full snapshot on subscribe, then deltas. The UI requests logical topics and never reasons about moomoo subscription quota — the engine owns all quota decisions. (ui-design §Architecture, go-engine-design §uihub)
- **Type source of truth:** engine `uihub/wsmsg` Go structs → tygo → `ui/src/gen/*`. Until the engine exists, `ui/src/wire/contract.ts` is the hand-authored interim contract; keep every field name identical to the specs so regeneration is a drop-in. (go-engine-design §uihub)
- **Link bus channel name:** `BroadcastChannel("etape.link")`; payload `{group, symbol}`; link-group focus events only — never market data. (ui-design §Architecture)
- **Correlation IDs:** every command (subscribe/unsubscribe, config CRUD, and — later plans — order actions) carries a correlation ID; the synchronous ack is `accepted | blocked(reason)`; outcomes arrive asynchronously as topic events. (portfolio-orders-design §Commands, go-engine-design §uihub)
- **Reconnect contract:** on WS drop, canvases dim + show a "reconnecting" overlay; on reconnect every subscribed topic re-runs snapshot-then-delta and stores rebuild to exactly one clean repaint. (ui-design §Error handling)
- **Panel isolation:** each panel has an error boundary; a thrown render → inline error card + reload button; a painter that throws → unregistered from the scheduler + same card; the rest of the workspace keeps running. (ui-design §Panels, §Error handling)
- **Test invariant (UI twin of the engine's):** feeding a store the recorded WS log (snapshot + deltas + a reconnect mid-stream) yields a deterministic final state. Fixtures are captured from real engine output so contract drift fails tests. (ui-design §Testing)

## Plan sequence (6 plans)

Each plan produces working, independently-testable software and depends only on the ones before it.

1. **Foundation & Data Plane** (this plan) — scaffold, wire client, mock engine + fixtures, all stores, rAF scheduler, dockview shell, workspace persistence, link groups, error/reconnect handling, Connection Status panel. Deliverable: a live shell that connects, subscribes, hydrates, repaints, and persists layout across two linked windows.
2. **Charting** — Lightweight Charts **v5 unforked** (≥5.2.0) owns *all* candlestick viewport math (pan, zoom, price/time axes, crosshair, auto-follow right edge): do **not** port wickplot's `BarWindow`/`ChartViewport`/`priceGrid`/`niceAxisStep` — they re-implement LWC's job (YAGNI). Anything drawn on the chart (markers, overlays) plugs in as an LWC v5 plugin and asks LWC for pixels (`priceToCoordinate`/`timeToCoordinate`, native `atPrice*` positions). Scope: candles + volume + MACD in a native v5 sub-pane; custom diamond fill-marker plugin ported from `~/Projects/earlisreal-lightweight-charts` commit `069fa855` (`drawDiamond`/`hitTestDiamond`, Manhattan hit test, 0.8 size factor) + the v3.7.1 `borderWidth` pattern (`a25e7dc0`); a bar-bucketing test-mirror of the engine; indicator instances (VWAP/EMA/SMA/MACD/volume/buy-sell delta) as streamed series; ET session shading; wickplot's interaction *conventions* (crosshair snap, cursor-anchored wheel zoom, drag pan, jump-to-live) mapped onto LWC options; cold-symbol / in-progress-bar states. **Palette (cross-cutting, decided 2026-07-04):** Plan 2 opens by deciding a **new eTape palette with Earl** — explicitly *not* seeded from wickplot's `ChartColors` (the UI spec's theme line is updated accordingly). Two variants, **light is the app default**, dark selectable via a settings toggle persisted in the config store. It lands as `ui/src/render/palette.ts`, the single color source of truth: the LWC chart theme derives from it, and every custom painter and chrome style consumes it — painters take the palette as part of their paint state (never read a global), so golden-image tests render both variants. All panels stay visually consistent with the LWC charts. Deliverable: the Chart panel in both workspaces.
3. **L2 ladder & Time & Sales** — ladder canvas painter (10 levels/side, price/size/cumulative, last-trade flash, working-order marks display-only, non-US "no depth entitlement" state) + tape canvas painter over `TapeRing` (BUY/SELL/NEUTRAL coloring, min-size filter, pause-on-scroll with auto-resume). **Wickplot ports — the complete list; nothing else in wickplot applies to these panels:** `accumulatePan` → `scrollAccumulate(remainder, deltaPx, rowPx)` for the tape's sub-row wheel-scroll carry (port with its table-driven tests); the `volumeToHeight` normalization idiom (`value / max` with zero-max guard) for the ladder's cumulative-size depth bars; `axisDecimals` as the shared price-decimals formatter (ladder + tape here, order ticket reuses it in Plan 5); and the golden-image harness *shape* from `CanvasChartSampleRenderTest` (fixture-state generators → render the real painter offscreen at 2× → PNGs to a samples dir for eyeballing), upgraded from wickplot's size-only assertion to strict pixel-diff against checked-in goldens (node-canvas). No viewport/window classes: ladder rows are indexed by book level, tape rows by ring index — layout is `y = rowIndex × rowHeight`. All colors come from Plan 2's `palette.ts`, not wickplot. Golden fixture states: full book, empty book, flash mid-decay, min-size-filtered tape, "no depth entitlement" — each rendered in both light (default) and dark palette variants. Deliverable: ladder + tape panels.
4. **Monitoring surfaces** — scanner + movers (one session-parameterized rank table: 3-digit-safe %, compact float, "no print yet" state, new-hit flash + midnight-reset dedup, configurable thresholds) + news panel (`NewsItem` shape, seen-time labeling, halt-banner slot reserved). Deliverable: the Monitoring workspace.
5. **Execution surfaces & order entry** — account bar (equity/BP/day-P&L, armed state + arm control, connection dots), positions (live unrealized P&L, flatten-through-gate), open orders (9-state lifecycle incl. `PendingNew`/`Replacing`, per-row + cancel-all, verbatim R-codes, `StreamGap` badge), order ticket, action templates, customizable hotkeys with sizing modes, trigger flow (resolve → client pre-checks → engine gate ack → `PendingNew`), kill switch. Densest safety tests. Deliverable: the Trading workspace order path against SimBroker/mock.
6. **E2E, packaging & polish** — Playwright smoke (engine in replay/sim mode → both workspaces → link focus → charts populate, ladder paints, paper order walks `PendingNew → New → Filled`), production build served by the engine from `ui/dist`, final error-handling matrix sweep. Deliverable: shippable v1 UI.

**Cross-plan dependencies to flag:**

- **Wire contract:** Plans 2–5 consume fixtures conforming to the wire contract. This plan establishes the interim contract + mock engine + fixture format. When the engine's `uihub/wsmsg` + tygo pipeline lands, regenerate `ui/src/gen/*` and re-capture fixtures from real engine output; the store/replay tests will then fail loudly on any contract drift.
- **Palette & theme:** Plan 2 establishes `ui/src/render/palette.ts` (a new eTape palette decided with Earl — not wickplot's) with light and dark variants: **light is the default**, dark behind a settings toggle persisted in the config store (per-workspace theming stays out of v1). The LWC chart theme and all custom painters/chrome derive from it; painters receive the palette in their paint state, so goldens cover both variants. Plans 3–5 take their colors exclusively from that module. Plan 1's inline hex colors (shell chrome, smoke painter, `dockview-theme-dark` class — currently dark-tinted placeholders) are swept onto the light-default palette as Plan 2's final step.

---

## File Structure (Plan 1)

```
ui/
  package.json, tsconfig.json, tsconfig.node.json, vite.config.ts, vitest.config.ts, index.html
  .eslintrc.cjs, .gitignore
  src/
    main.tsx                      chrome  — entry; reads ?workspace=, mounts <App/>
    App.tsx                       chrome  — wires WsClient + stores + scheduler + shell for one window
    wire/
      contract.ts                 wire    — interim topic/message/command types (tygo target: src/gen)
      codec.ts                    wire    — tolerant decode/encode of WS frames
      WsClient.ts                 wire    — connect, backoff reconnect, subscribe refcount, RTT, state
    data/
      store.ts                    data    — PaintStore (dirty flag) + ReactStore (useSyncExternalStore) bases
      QuoteStore.ts               data    — latest bid/ask/last per symbol
      BookStore.ts                data    — full 10-level book per symbol (replace)
      TapeRing.ts                 data    — fixed-size ring of ticks per symbol
      BarStore.ts                 data    — bars per (symbol,timeframe): in-progress upsert + watermark finalize
      ExecStore.ts                data    — account/positions/orders (React-observable)
      ScannerStore.ts             data    — rank rows + hit events (React-observable)
      NewsStore.ts                data    — NewsItem list (React-observable)
      HealthStore.ts              data    — per-link latency + event log (React-observable)
      registry.ts                 data    — maps topic → store.apply, drives WsClient dispatch
    render/
      Scheduler.ts                render  — one rAF loop; registers Surfaces; paints dirty once/frame
      surface.ts                  render  — Surface interface + canvas mount helper
    chrome/
      AppShell.tsx                chrome  — dockview shell, panel registry, workspace load/save
      PanelFrame.tsx              chrome  — header (link swatch), error boundary, ResizeObserver
      ErrorBoundary.tsx           chrome  — per-panel React error boundary → inline card
      ReconnectOverlay.tsx        chrome  — dim + "reconnecting" overlay bound to WsClient state
      linkGroups.ts               chrome  — LinkBus interface + BroadcastChannelBus + focus state
      workspace.ts                chrome  — workspace document type + config-CRUD persistence client
      panels/
        registry.tsx              chrome  — panelId → React component + declared topics
        ConnectionStatusPanel.tsx chrome  — HealthStore view: latency rows + event log
        SmokePainterPanel.tsx     chrome  — trivial canvas panel proving wire→store→scheduler→canvas
    seeds/
      workspaces.ts               data    — seed "Monitoring" / "Trading" documents
  mock-engine/
    server.ts                     Node WS server: replays a fixture session, acks commands, ping/pong
    run.ts                        CLI entry: `npm run mock-engine`
  fixtures/
    session-basic.json            snapshot + deltas + a mid-stream reconnect for stores/replay tests
  test/
    fakes.ts                      FakeSocket, FakeBus, fake rAF, fake ResizeObserver
```

---

## Task 1: Scaffold the `ui/` project

**Files:**
- Create: `ui/package.json`
- Create: `ui/tsconfig.json`, `ui/tsconfig.node.json`
- Create: `ui/vite.config.ts`
- Create: `ui/vitest.config.ts`
- Create: `ui/index.html`
- Create: `ui/.eslintrc.cjs`, `ui/.gitignore`, `ui/.npmrc`
- Create: `ui/src/main.tsx` (placeholder)
- Test: `ui/src/sanity.test.ts`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: a working `npm test` / `npm run dev` / `npm run build` toolchain in `ui/`, TS strict mode, and a Vite `/ws` proxy to `127.0.0.1:8686`.

- [ ] **Step 1: Write the failing test**

`ui/src/sanity.test.ts`:
```ts
import { describe, it, expect } from "vitest";

describe("toolchain sanity", () => {
  it("runs typed TypeScript under vitest", () => {
    const n: number = [1, 2, 3].reduce((a, b) => a + b, 0);
    expect(n).toBe(6);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npm test`
Expected: FAIL — `npm` errors because there is no `package.json` / dependencies yet.

- [ ] **Step 3: Create the project files**

`ui/package.json`:
```json
{
  "name": "etape-ui",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "npm run typecheck && vite build",
    "preview": "vite preview",
    "test": "vitest run",
    "test:watch": "vitest",
    "lint": "eslint . --ext .ts,.tsx",
    "typecheck": "tsc -p tsconfig.json --noEmit && tsc -p tsconfig.node.json --noEmit",
    "mock-engine": "tsx mock-engine/run.ts"
  },
  "dependencies": {
    "dockview": "^4.0.0",
    "react": "^18.3.1",
    "react-dom": "^18.3.1"
  },
  "devDependencies": {
    "@testing-library/react": "^16.0.0",
    "@types/react": "^18.3.3",
    "@types/react-dom": "^18.3.0",
    "@types/ws": "^8.5.10",
    "@typescript-eslint/eslint-plugin": "^7.13.0",
    "@typescript-eslint/parser": "^7.13.0",
    "@vitejs/plugin-react": "^4.3.1",
    "eslint": "^8.57.0",
    "jsdom": "^24.1.0",
    "tsx": "^4.15.0",
    "typescript": "^5.4.5",
    "vite": "^5.3.1",
    "vitest": "^1.6.0",
    "ws": "^8.17.0"
  }
}
```

`ui/tsconfig.json`:
```json
{
  "compilerOptions": {
    "target": "ES2022",
    "useDefineForClassFields": true,
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "exactOptionalPropertyTypes": true,
    "skipLibCheck": true,
    "types": ["vitest/globals"],
    "noEmit": true
  },
  "include": ["src", "test", "mock-engine"],
  "references": [{ "path": "./tsconfig.node.json" }]
}
```
`noEmit` is required here: with no `outDir` set, a plain `tsc` invocation
(as `npm run build`'s old `tsc -b && vite build` performed) would otherwise
write compiled `.js`/`.d.ts` files directly next to their `.ts`/`.tsx`
sources — none of which `.gitignore` covers — since Vite/esbuild does the
actual bundling and type-checking is the only thing `tsc` needs to do here.
Note `tsconfig.node.json` (below) must stay emit-capable — it is a
`composite` project referenced from here, and TS6310 rejects a referenced
composite project with `noEmit` set — so `npm run build` type-checks via
the same non-build `tsc -p ... --noEmit` invocations as `npm run typecheck`
(see `build` script above) rather than `tsc -b`.

`ui/tsconfig.node.json`:
```json
{
  "compilerOptions": {
    "composite": true,
    "skipLibCheck": true,
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "strict": true
  },
  "include": ["vite.config.ts", "vitest.config.ts"]
}
```

`ui/vite.config.ts`:
```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev server proxies /ws to the engine (or the mock engine) on 127.0.0.1:8686.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/ws": { target: "ws://127.0.0.1:8686", ws: true },
    },
  },
});
```

`ui/vitest.config.ts`:
```ts
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Default node environment for pure logic; chrome/*.test.tsx opt into jsdom
// per-file with `// @vitest-environment jsdom`.
export default defineConfig({
  plugins: [react()],
  test: {
    globals: true,
    environment: "node",
  },
});
```

`ui/index.html`:
```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>eTape</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

`ui/src/main.tsx` (placeholder, fleshed out in Task 14):
```tsx
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <div>eTape — booting…</div>
  </StrictMode>,
);
```

`ui/.eslintrc.cjs`:
```cjs
module.exports = {
  root: true,
  parser: "@typescript-eslint/parser",
  plugins: ["@typescript-eslint"],
  extends: ["eslint:recommended", "plugin:@typescript-eslint/recommended"],
  env: { browser: true, node: true, es2022: true },
  ignorePatterns: ["dist", "node_modules", "src/gen"],
};
```

`ui/.gitignore`:
```gitignore
node_modules
dist
*.local
*.tsbuildinfo
```

`ui/.npmrc`:
```ini
registry=https://registry.npmjs.org/
```
eTape is a public, personal repo unrelated to any employer — pin the public
registry here so `npm install`/`npm ci` always resolve packages from it,
regardless of a machine-level `~/.npmrc` override (e.g. a corporate
Artifactory mirror). Without this, installing on a machine with such an
override bakes its private hostname into every `"resolved"` URL in
`package-lock.json`, and `npm ci` fails to resolve packages anywhere that
mirror isn't reachable (CI, a fresh clone, a non-corporate machine).

- [ ] **Step 4: Install and run the test to verify it passes**

Run: `cd ui && npm install && npm test`
Expected: PASS — `sanity.test.ts` green; `npm run dev` and `npm run build` are wired but not yet exercised.
Verify `package-lock.json`'s `"resolved"` URLs all point at
`registry.npmjs.org` (not a private mirror) before committing.

- [ ] **Step 5: Commit**

```bash
git add ui/package.json ui/package-lock.json ui/tsconfig.json ui/tsconfig.node.json \
  ui/vite.config.ts ui/vitest.config.ts ui/index.html ui/.eslintrc.cjs ui/.gitignore \
  ui/.npmrc ui/src/main.tsx ui/src/sanity.test.ts
git commit -m "chore(ui): scaffold Vite + React + TS + Vitest project"
```

---

## Task 2: Wire contract + tolerant frame codec

**Files:**
- Create: `ui/src/wire/contract.ts`
- Create: `ui/src/wire/codec.ts`
- Test: `ui/src/wire/codec.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `contract.ts`: `type TopicName` (union of topic strings); `interface ServerMessage` variants `{kind:"snapshot"|"delta", topic, key?, payload}`, `{kind:"ack", corrId, status:"accepted"|"blocked", reason?}`, `{kind:"pong", t}`; `interface ClientMessage` variants `{kind:"subscribe"|"unsubscribe", topic}`, `{kind:"command", corrId, name, args}`, `{kind:"ping", t}`; payload interfaces `Quote`, `BookLevel`, `Book`, `Tick`, `Bar`, `HealthLink`, `HealthSnapshot`, `SysEvent`.
  - `codec.ts`: `decodeServerMessage(raw: string): ServerMessage | null` (returns `null` on malformed, never throws); `encodeClientMessage(msg: ClientMessage): string`.

- [ ] **Step 1: Write the failing test**

`ui/src/wire/codec.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { decodeServerMessage, encodeClientMessage } from "./codec";

describe("decodeServerMessage", () => {
  it("decodes a snapshot frame", () => {
    const raw = JSON.stringify({
      kind: "snapshot",
      topic: "md.quote",
      key: "US.AAPL",
      payload: { symbol: "US.AAPL", bid: 3.49, ask: 3.51, last: 3.5, ts: "t" },
    });
    const msg = decodeServerMessage(raw);
    expect(msg?.kind).toBe("snapshot");
    if (msg?.kind === "snapshot") {
      expect(msg.topic).toBe("md.quote");
      expect((msg.payload as { bid: number }).bid).toBe(3.49);
    }
  });

  it("tolerates unknown fields without throwing", () => {
    const raw = JSON.stringify({
      kind: "delta", topic: "md.quote", key: "US.AAPL",
      payload: { last: 3.6 }, futureField: 42,
    });
    expect(decodeServerMessage(raw)?.kind).toBe("delta");
  });

  it("returns null on malformed JSON", () => {
    expect(decodeServerMessage("{not json")).toBeNull();
  });

  it("returns null on a frame with no known kind", () => {
    expect(decodeServerMessage(JSON.stringify({ kind: "bogus" }))).toBeNull();
  });
});

describe("encodeClientMessage", () => {
  it("round-trips a subscribe", () => {
    const s = encodeClientMessage({ kind: "subscribe", topic: "md.book" });
    expect(JSON.parse(s)).toEqual({ kind: "subscribe", topic: "md.book" });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/wire/codec.test.ts`
Expected: FAIL — `Cannot find module './codec'`.

- [ ] **Step 3: Write the implementation**

`ui/src/wire/contract.ts`:
```ts
// INTERIM CONTRACT — hand-authored to mirror the engine's uihub/wsmsg Go structs.
// Superseded by tygo-generated `ui/src/gen/*` once the engine lands. Keep every
// field name identical to the approved specs so regeneration is a drop-in.

export type TopicName =
  | "md.quote" | "md.book" | "md.tape" | "md.bars" | "md.indicator"
  | "scanner.rank" | "scanner.hit"
  | "news.item"
  | "exec.account" | "exec.positions" | "exec.orders" | "exec.fills" | "exec.status"
  | "sys.health" | "sys.events"
  | "config";

// ---- payloads (extend as later plans need them) ----
export interface Quote { symbol: string; bid: number; ask: number; last: number; ts: string }
export interface BookLevel { price: number; size: number }
export interface Book { symbol: string; bids: BookLevel[]; asks: BookLevel[]; ts: string }
export type TickDirection = "BUY" | "SELL" | "NEUTRAL";
export interface Tick { symbol: string; price: number; size: number; direction: TickDirection; ts: string }
export interface Bar {
  symbol: string; timeframe: string; bucketStart: string;
  o: number; h: number; l: number; c: number; v: number;
  inProgress: boolean; gap?: boolean;
}
export interface HealthLink {
  link: "ui-engine" | "engine-moomoo" | "engine-tz";
  ms: number | null; min: number | null; avg: number | null; max: number | null;
  status: "ok" | "degraded" | "down";
}
export interface HealthSnapshot { links: HealthLink[] }
export interface SysEvent { seq: number; ts: string; kind: string; detail: string }

// ---- server → client ----
export interface SnapshotMsg { kind: "snapshot"; topic: TopicName; key?: string; payload: unknown }
export interface DeltaMsg { kind: "delta"; topic: TopicName; key?: string; payload: unknown }
export interface AckMsg { kind: "ack"; corrId: string; status: "accepted" | "blocked"; reason?: string }
export interface PongMsg { kind: "pong"; t: number }
export type ServerMessage = SnapshotMsg | DeltaMsg | AckMsg | PongMsg;

// ---- client → server ----
export interface SubscribeMsg { kind: "subscribe"; topic: TopicName }
export interface UnsubscribeMsg { kind: "unsubscribe"; topic: TopicName }
export interface CommandMsg { kind: "command"; corrId: string; name: string; args: unknown }
export interface PingMsg { kind: "ping"; t: number }
export type ClientMessage = SubscribeMsg | UnsubscribeMsg | CommandMsg | PingMsg;
```

`ui/src/wire/codec.ts`:
```ts
import type { ClientMessage, ServerMessage } from "./contract";

const SERVER_KINDS = new Set(["snapshot", "delta", "ack", "pong"]);

// Never throws: a malformed or unknown frame yields null so the reader loop
// can drop-and-count rather than crash (honesty policy + burst tolerance).
export function decodeServerMessage(raw: string): ServerMessage | null {
  let obj: unknown;
  try {
    obj = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof obj !== "object" || obj === null) return null;
  const kind = (obj as { kind?: unknown }).kind;
  if (typeof kind !== "string" || !SERVER_KINDS.has(kind)) return null;
  return obj as ServerMessage;
}

export function encodeClientMessage(msg: ClientMessage): string {
  return JSON.stringify(msg);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/wire/codec.test.ts`
Expected: PASS — all five cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/wire/contract.ts ui/src/wire/codec.ts ui/src/wire/codec.test.ts
git commit -m "feat(ui/wire): interim WS contract + tolerant frame codec"
```

---

## Task 3: WsClient — connect, reconnect, subscribe refcount, RTT

**Files:**
- Create: `ui/src/wire/WsClient.ts`
- Create: `ui/test/fakes.ts` (FakeSocket only in this task; extended later)
- Test: `ui/src/wire/WsClient.test.ts`

**Interfaces:**
- Consumes: `ServerMessage`, `ClientMessage`, `TopicName` from `contract.ts`; `decodeServerMessage`/`encodeClientMessage` from `codec.ts`.
- Produces `class WsClient`:
  - `constructor(opts: { url: string; socketFactory: (url: string) => ISocket; now: () => number; setTimeout: SetTimeoutLike; backoff?: (attempt: number) => number })`
  - `subscribe(topic: TopicName, onMessage: (m: SnapshotMsg | DeltaMsg) => void): () => void` — refcounted; returns an unsubscribe fn. First subscriber sends `subscribe`; last unsubscriber sends `unsubscribe`.
  - `sendCommand(name: string, args: unknown): Promise<AckMsg>` — correlation-ID matched.
  - `onState(cb: (s: ConnState) => void): void` where `type ConnState = "connecting" | "open" | "reconnecting"`.
  - `rttMs(): number | null` — last app-level ping/pong RTT.
  - `start(): void`, `stop(): void`.
- Also produces `interface ISocket` (send/close/onopen/onmessage/onclose) and `type SetTimeoutLike` so tests inject fakes.

- [ ] **Step 1: Write the failing test**

`ui/test/fakes.ts`:
```ts
import type { ISocket } from "../src/wire/WsClient";

export class FakeSocket implements ISocket {
  static instances: FakeSocket[] = [];
  sent: string[] = [];
  closed = false;
  onopen: (() => void) | null = null;
  onmessage: ((data: string) => void) | null = null;
  onclose: (() => void) | null = null;

  constructor(public url: string) {
    FakeSocket.instances.push(this);
  }
  send(data: string): void { this.sent.push(data); }
  close(): void { this.closed = true; this.onclose?.(); }

  // test helpers
  open(): void { this.onopen?.(); }
  emit(raw: string): void { this.onmessage?.(raw); }
  dropFromServer(): void { this.onclose?.(); }
  static last(): FakeSocket { return FakeSocket.instances[FakeSocket.instances.length - 1]; }
  static reset(): void { FakeSocket.instances = []; }
}
```

`ui/src/wire/WsClient.test.ts`:
```ts
import { describe, it, expect, beforeEach } from "vitest";
import { WsClient } from "./WsClient";
import { FakeSocket } from "../../test/fakes";

function makeClient() {
  const timers: Array<() => void> = [];
  const setTimeoutLike = (fn: () => void) => { timers.push(fn); return timers.length; };
  const client = new WsClient({
    url: "ws://x/ws",
    socketFactory: (u) => new FakeSocket(u),
    now: () => 1000,
    setTimeout: setTimeoutLike as unknown as typeof setTimeout,
    backoff: () => 5,
  });
  return { client, flushTimers: () => { const t = timers.splice(0); t.forEach((f) => f()); } };
}

beforeEach(() => FakeSocket.reset());

describe("WsClient", () => {
  it("sends subscribe on first subscriber and unsubscribe on last", () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();

    const off1 = client.subscribe("md.quote", () => {});
    const off2 = client.subscribe("md.quote", () => {});
    const subs = FakeSocket.last().sent.map((s) => JSON.parse(s));
    expect(subs.filter((m) => m.kind === "subscribe" && m.topic === "md.quote")).toHaveLength(1);

    off1();
    expect(FakeSocket.last().sent.map((s) => JSON.parse(s))
      .some((m) => m.kind === "unsubscribe")).toBe(false);
    off2();
    expect(FakeSocket.last().sent.map((s) => JSON.parse(s))
      .some((m) => m.kind === "unsubscribe" && m.topic === "md.quote")).toBe(true);
  });

  it("dispatches snapshot then delta to the subscriber", () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    const got: string[] = [];
    client.subscribe("md.quote", (m) => got.push(m.kind));
    FakeSocket.last().emit(JSON.stringify({ kind: "snapshot", topic: "md.quote", payload: {} }));
    FakeSocket.last().emit(JSON.stringify({ kind: "delta", topic: "md.quote", payload: {} }));
    // a message for another topic is ignored by this subscriber
    FakeSocket.last().emit(JSON.stringify({ kind: "delta", topic: "md.book", payload: {} }));
    expect(got).toEqual(["snapshot", "delta"]);
  });

  it("re-subscribes all live topics after a reconnect", () => {
    const { client, flushTimers } = makeClient();
    client.start();
    FakeSocket.last().open();
    client.subscribe("md.quote", () => {});
    client.subscribe("md.book", () => {});

    FakeSocket.last().dropFromServer();  // server drops
    flushTimers();                        // backoff fires → new socket
    FakeSocket.last().open();             // reconnected

    const resent = FakeSocket.last().sent.map((s) => JSON.parse(s));
    expect(resent.filter((m) => m.kind === "subscribe").map((m) => m.topic).sort())
      .toEqual(["md.book", "md.quote"]);
  });

  it("reports state transitions", () => {
    const { client, flushTimers } = makeClient();
    const states: string[] = [];
    client.onState((s) => states.push(s));
    client.start();
    FakeSocket.last().open();
    FakeSocket.last().dropFromServer();
    flushTimers();
    FakeSocket.last().open();
    expect(states).toEqual(["connecting", "open", "reconnecting", "connecting", "open"]);
  });

  it("resolves sendCommand when the matching ack arrives", async () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    const p = client.sendCommand("Subscribe", { topic: "x" });
    const sent = JSON.parse(FakeSocket.last().sent.at(-1)!);
    expect(sent.kind).toBe("command");
    FakeSocket.last().emit(JSON.stringify({ kind: "ack", corrId: sent.corrId, status: "accepted" }));
    await expect(p).resolves.toMatchObject({ status: "accepted" });
  });

  it("measures RTT from ping/pong", () => {
    const { client } = makeClient();
    client.start();
    FakeSocket.last().open();
    client.sendPing();
    const ping = JSON.parse(FakeSocket.last().sent.at(-1)!);
    FakeSocket.last().emit(JSON.stringify({ kind: "pong", t: ping.t }));
    expect(client.rttMs()).toBe(0); // now() is fixed at 1000 in the fake
  });

  it("buffers a command issued before open and flushes it on connect", () => {
    const { client } = makeClient();
    client.start();                 // connecting, socket not open yet
    const p = client.sendCommand("GetConfig", { key: "workspace.trading" });
    expect(FakeSocket.last().sent).toHaveLength(0); // nothing sent while connecting
    FakeSocket.last().open();       // onopen flushes the outbox
    const sent = FakeSocket.last().sent.map((s) => JSON.parse(s));
    expect(sent.some((m) => m.kind === "command" && m.name === "GetConfig")).toBe(true);
    void p; // promise stays pending until an ack arrives — not awaited here
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/wire/WsClient.test.ts`
Expected: FAIL — `Cannot find module './WsClient'`.

- [ ] **Step 3: Write the implementation**

`ui/src/wire/WsClient.ts`:
```ts
import type {
  AckMsg, ClientMessage, DeltaMsg, ServerMessage, SnapshotMsg, TopicName,
} from "./contract";
import { decodeServerMessage, encodeClientMessage } from "./codec";

export interface ISocket {
  send(data: string): void;
  close(): void;
  onopen: (() => void) | null;
  onmessage: ((data: string) => void) | null;
  onclose: (() => void) | null;
}
export type SetTimeoutLike = (fn: () => void, ms: number) => unknown;
export type ConnState = "connecting" | "open" | "reconnecting";
type TopicHandler = (m: SnapshotMsg | DeltaMsg) => void;

interface Opts {
  url: string;
  socketFactory: (url: string) => ISocket;
  now: () => number;
  setTimeout: SetTimeoutLike;
  backoff?: (attempt: number) => number;
}

const DEFAULT_BACKOFF = (attempt: number) => {
  const base = Math.min(30_000, 1000 * 2 ** attempt);
  return base / 2 + Math.random() * (base / 2); // jittered 1s → 30s
};

export class WsClient {
  private socket: ISocket | null = null;
  private state: ConnState = "connecting";
  private attempt = 0;
  private corr = 0;
  private lastRtt: number | null = null;
  private readonly handlers = new Map<TopicName, Set<TopicHandler>>();
  private readonly stateCbs = new Set<(s: ConnState) => void>();
  private readonly pending = new Map<string, (ack: AckMsg) => void>();
  private readonly outbox: string[] = []; // commands buffered while not open
  private readonly backoff: (attempt: number) => number;

  constructor(private readonly opts: Opts) {
    this.backoff = opts.backoff ?? DEFAULT_BACKOFF;
  }

  start(): void { this.connect(); }
  stop(): void { this.socket?.close(); this.socket = null; }

  onState(cb: (s: ConnState) => void): void { this.stateCbs.add(cb); cb(this.state); }
  rttMs(): number | null { return this.lastRtt; }

  subscribe(topic: TopicName, onMessage: TopicHandler): () => void {
    let set = this.handlers.get(topic);
    if (!set) {
      set = new Set();
      this.handlers.set(topic, set);
      this.sendRaw({ kind: "subscribe", topic }); // first subscriber
    }
    set.add(onMessage);
    return () => {
      const s = this.handlers.get(topic);
      if (!s) return;
      s.delete(onMessage);
      if (s.size === 0) {
        this.handlers.delete(topic);
        this.sendRaw({ kind: "unsubscribe", topic }); // last unsubscriber
      }
    };
  }

  sendCommand(name: string, args: unknown): Promise<AckMsg> {
    const corrId = `c${++this.corr}`;
    return new Promise<AckMsg>((resolve) => {
      this.pending.set(corrId, resolve);
      this.sendRaw({ kind: "command", corrId, name, args });
    });
  }

  sendPing(): void { this.sendRaw({ kind: "ping", t: this.opts.now() }); }

  // ---- internals ----
  private setState(s: ConnState): void {
    if (s === this.state) return; // dedupe consecutive identical states
    this.state = s;
    this.stateCbs.forEach((cb) => cb(s));
  }

  private connect(): void {
    this.setState("connecting");
    const sock = this.opts.socketFactory(this.opts.url);
    this.socket = sock;
    sock.onopen = () => {
      this.attempt = 0;
      this.setState("open");
      // Re-run snapshot-then-delta for every live topic on (re)connect, then flush
      // any commands buffered while the socket was down.
      for (const topic of this.handlers.keys()) this.sendRaw({ kind: "subscribe", topic });
      this.flushOutbox();
    };
    sock.onmessage = (raw) => this.onMessage(raw);
    sock.onclose = () => {
      if (this.socket !== sock) return;
      this.socket = null;
      this.setState("reconnecting");
      const delay = this.backoff(this.attempt++);
      this.opts.setTimeout(() => this.connect(), delay);
    };
  }

  private onMessage(raw: string): void {
    const msg = decodeServerMessage(raw);
    if (!msg) return; // drop-and-count malformed frames
    switch (msg.kind) {
      case "snapshot":
      case "delta": {
        const set = this.handlers.get(msg.topic);
        set?.forEach((h) => h(msg));
        return;
      }
      case "ack": {
        const resolve = this.pending.get(msg.corrId);
        if (resolve) { this.pending.delete(msg.corrId); resolve(msg); }
        return;
      }
      case "pong": {
        this.lastRtt = this.opts.now() - msg.t;
        return;
      }
    }
  }

  private sendRaw(msg: ClientMessage): void {
    if (this.state === "open" && this.socket) {
      this.socket.send(encodeClientMessage(msg));
      return;
    }
    // Not open: buffer commands (each carries a pending promise); drop subscribe/
    // unsubscribe (reconstructed from handlers on open) and pings (re-fired on interval).
    if (msg.kind === "command") this.outbox.push(encodeClientMessage(msg));
  }

  private flushOutbox(): void {
    if (!this.socket) return;
    for (const raw of this.outbox.splice(0)) this.socket.send(raw);
  }
}
```

> Note the `ServerMessage` import stays exhaustive so `noFallthroughCasesInSwitch` + the union force a compile error if a new server `kind` is added without handling — contract drift fails the build.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/wire/WsClient.test.ts`
Expected: PASS — all six cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/wire/WsClient.ts ui/test/fakes.ts ui/src/wire/WsClient.test.ts
git commit -m "feat(ui/wire): WsClient with refcounted subscribe, reconnect resubscribe, RTT"
```

---

## Task 4: Mock engine WS server + fixture format

**Files:**
- Create: `ui/fixtures/session-basic.json`
- Create: `ui/mock-engine/server.ts`
- Create: `ui/mock-engine/run.ts`
- Test: `ui/mock-engine/server.test.ts`

**Interfaces:**
- Consumes: the wire contract (topic/message shapes) — kept structural so the mock never imports UI source.
- Produces:
  - Fixture format: `{ snapshots: Array<{topic,key?,payload}>, deltas: Array<{afterMs:number, topic, key?, payload}>, reconnectAtMs?: number }`.
  - `startMockEngine(opts: { port: number; fixture: Fixture }): { close(): Promise<void> }` — on client `subscribe`, sends that topic's snapshot immediately then schedules its deltas; replies to `ping` with `pong`; acks `command` frames `accepted`; if `reconnectAtMs` is set, force-closes the socket once at that time to exercise UI reconnect.

- [ ] **Step 1: Write the failing test**

`ui/mock-engine/server.test.ts`:
```ts
import { describe, it, expect, afterEach } from "vitest";
import { WebSocket } from "ws";
import { startMockEngine, type Fixture } from "./server";

const PORT = 8699;
let handle: { close: () => Promise<void> } | null = null;
afterEach(async () => { await handle?.close(); handle = null; });

const fixture: Fixture = {
  snapshots: [{ topic: "md.quote", key: "US.AAPL", payload: { symbol: "US.AAPL", last: 3.5 } }],
  deltas: [{ afterMs: 10, topic: "md.quote", key: "US.AAPL", payload: { last: 3.6 } }],
};

function collect(ws: WebSocket, n: number, timeoutMs = 1000): Promise<any[]> {
  return new Promise((resolve, reject) => {
    const out: any[] = [];
    const timer = setTimeout(() => reject(new Error(`only got ${out.length}/${n}`)), timeoutMs);
    ws.on("message", (d) => {
      out.push(JSON.parse(d.toString()));
      if (out.length === n) { clearTimeout(timer); resolve(out); }
    });
  });
}

describe("mock engine", () => {
  it("sends snapshot then delta for a subscribed topic", async () => {
    handle = startMockEngine({ port: PORT, fixture });
    const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
    await new Promise<void>((r) => ws.on("open", () => r()));
    const msgs = collect(ws, 2);
    ws.send(JSON.stringify({ kind: "subscribe", topic: "md.quote" }));
    const [snap, delta] = await msgs;
    expect(snap.kind).toBe("snapshot");
    expect(delta.kind).toBe("delta");
    expect(delta.payload.last).toBe(3.6);
    ws.close();
  });

  it("acks a command", async () => {
    handle = startMockEngine({ port: PORT, fixture });
    const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
    await new Promise<void>((r) => ws.on("open", () => r()));
    const msgs = collect(ws, 1);
    ws.send(JSON.stringify({ kind: "command", corrId: "c1", name: "Noop", args: {} }));
    const [ack] = await msgs;
    expect(ack).toMatchObject({ kind: "ack", corrId: "c1", status: "accepted" });
    ws.close();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run mock-engine/server.test.ts`
Expected: FAIL — `Cannot find module './server'`.

- [ ] **Step 3: Write the implementation and fixture**

`ui/fixtures/session-basic.json`:
```json
{
  "snapshots": [
    { "topic": "md.quote", "key": "US.AAPL", "payload": { "symbol": "US.AAPL", "bid": 3.49, "ask": 3.51, "last": 3.50, "ts": "2026-07-06T13:30:00Z" } },
    { "topic": "sys.health", "payload": { "links": [
      { "link": "ui-engine", "ms": 1, "min": 1, "avg": 1, "max": 1, "status": "ok" },
      { "link": "engine-moomoo", "ms": 12, "min": 8, "avg": 12, "max": 20, "status": "ok" },
      { "link": "engine-tz", "ms": null, "min": null, "avg": null, "max": null, "status": "down" }
    ] } },
    { "topic": "sys.events", "payload": { "seq": 1, "ts": "2026-07-06T13:30:00Z", "kind": "boot", "detail": "engine started" } }
  ],
  "deltas": [
    { "afterMs": 50,  "topic": "md.quote", "key": "US.AAPL", "payload": { "symbol": "US.AAPL", "bid": 3.50, "ask": 3.52, "last": 3.51, "ts": "2026-07-06T13:30:01Z" } },
    { "afterMs": 120, "topic": "md.quote", "key": "US.AAPL", "payload": { "symbol": "US.AAPL", "bid": 3.51, "ask": 3.53, "last": 3.52, "ts": "2026-07-06T13:30:02Z" } },
    { "afterMs": 200, "topic": "sys.events", "payload": { "seq": 2, "ts": "2026-07-06T13:30:03Z", "kind": "reconnect", "detail": "engine-tz reconnecting" } }
  ],
  "reconnectAtMs": 300
}
```

`ui/mock-engine/server.ts`:
```ts
import { WebSocketServer, type WebSocket } from "ws";

export interface Fixture {
  snapshots: Array<{ topic: string; key?: string; payload: unknown }>;
  deltas: Array<{ afterMs: number; topic: string; key?: string; payload: unknown }>;
  reconnectAtMs?: number;
}

export function startMockEngine(opts: { port: number; fixture: Fixture }): { close: () => Promise<void> } {
  const wss = new WebSocketServer({ port: opts.port, path: "/ws" });
  const timers = new Set<ReturnType<typeof setTimeout>>();
  const track = (fn: () => void, ms: number) => {
    const t = setTimeout(() => { timers.delete(t); fn(); }, ms);
    timers.add(t);
  };

  wss.on("connection", (ws: WebSocket) => {
    const live = new Set<string>();
    let dropped = false;

    ws.on("message", (raw) => {
      let msg: { kind?: string; topic?: string; corrId?: string; t?: number };
      try { msg = JSON.parse(raw.toString()); } catch { return; }

      if (msg.kind === "ping") { ws.send(JSON.stringify({ kind: "pong", t: msg.t })); return; }
      if (msg.kind === "command") {
        ws.send(JSON.stringify({ kind: "ack", corrId: msg.corrId, status: "accepted" }));
        return;
      }
      if (msg.kind === "unsubscribe" && msg.topic) { live.delete(msg.topic); return; }
      if (msg.kind === "subscribe" && msg.topic) {
        live.add(msg.topic);
        for (const s of opts.fixture.snapshots.filter((s) => s.topic === msg.topic)) {
          ws.send(JSON.stringify({ kind: "snapshot", topic: s.topic, key: s.key, payload: s.payload }));
        }
        for (const d of opts.fixture.deltas.filter((d) => d.topic === msg.topic)) {
          track(() => {
            if (!dropped && live.has(d.topic) && ws.readyState === ws.OPEN) {
              ws.send(JSON.stringify({ kind: "delta", topic: d.topic, key: d.key, payload: d.payload }));
            }
          }, d.afterMs);
        }
      }
    });

    if (opts.fixture.reconnectAtMs !== undefined) {
      track(() => { dropped = true; ws.close(); }, opts.fixture.reconnectAtMs);
    }
  });

  return {
    close: () =>
      new Promise<void>((resolve) => {
        timers.forEach(clearTimeout);
        timers.clear();
        wss.clients.forEach((c) => c.terminate());
        wss.close(() => resolve());
      }),
  };
}
```

`ui/mock-engine/run.ts`:
```ts
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { startMockEngine, type Fixture } from "./server";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = JSON.parse(
  readFileSync(join(here, "..", "fixtures", "session-basic.json"), "utf8"),
) as Fixture;

const port = 8686;
startMockEngine({ port, fixture });
console.log(`mock engine listening on ws://127.0.0.1:${port}/ws`);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run mock-engine/server.test.ts`
Expected: PASS — both cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/fixtures/session-basic.json ui/mock-engine/server.ts ui/mock-engine/run.ts ui/mock-engine/server.test.ts
git commit -m "feat(ui/mock-engine): fixture-replay WS server for dev + tests"
```

---

## Task 5: Store bases — PaintStore (dirty flag) and ReactStore (observable)

**Files:**
- Create: `ui/src/data/store.ts`
- Test: `ui/src/data/store.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `abstract class PaintStore` (for painter-fed data): `protected markDirty()`; `isDirty(): boolean`; `consumeDirty(): boolean` (returns then clears). Concrete stores call `markDirty()` from their apply methods.
  - `abstract class ReactStore<S>` (for React-observable data): `subscribe(cb: () => void): () => void`; `getSnapshot(): S` (stable reference until change — safe for `useSyncExternalStore`); `protected set(next: S)`; `protected emit()`.

- [ ] **Step 1: Write the failing test**

`ui/src/data/store.test.ts`:
```ts
import { describe, it, expect, vi } from "vitest";
import { PaintStore, ReactStore } from "./store";

class P extends PaintStore { bump() { this.markDirty(); } }
class R extends ReactStore<number> {
  constructor() { super(0); }
  inc() { this.set(this.getSnapshot() + 1); }
}

describe("PaintStore", () => {
  it("tracks and consumes the dirty flag", () => {
    const p = new P();
    expect(p.isDirty()).toBe(false);
    p.bump();
    expect(p.isDirty()).toBe(true);
    expect(p.consumeDirty()).toBe(true);
    expect(p.isDirty()).toBe(false);
    expect(p.consumeDirty()).toBe(false);
  });
});

describe("ReactStore", () => {
  it("notifies subscribers and returns a stable snapshot", () => {
    const r = new R();
    const cb = vi.fn();
    const off = r.subscribe(cb);
    const before = r.getSnapshot();
    r.inc();
    expect(cb).toHaveBeenCalledTimes(1);
    expect(r.getSnapshot()).toBe(before + 1);
    off();
    r.inc();
    expect(cb).toHaveBeenCalledTimes(1); // no longer subscribed
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/store.test.ts`
Expected: FAIL — `Cannot find module './store'`.

- [ ] **Step 3: Write the implementation**

`ui/src/data/store.ts`:
```ts
// Two store flavors, one per data plane:
//  - PaintStore feeds canvas painters at message rate; the scheduler polls isDirty().
//  - ReactStore feeds low-rate chrome via useSyncExternalStore.
// Neither imports React; ReactStore is React-shaped but React-free.

export abstract class PaintStore {
  private dirty = false;
  protected markDirty(): void { this.dirty = true; }
  isDirty(): boolean { return this.dirty; }
  consumeDirty(): boolean { const d = this.dirty; this.dirty = false; return d; }
}

export abstract class ReactStore<S> {
  private snapshot: S;
  private readonly subs = new Set<() => void>();
  constructor(initial: S) { this.snapshot = initial; }

  subscribe(cb: () => void): () => void { this.subs.add(cb); return () => this.subs.delete(cb); }
  getSnapshot(): S { return this.snapshot; }
  protected set(next: S): void { this.snapshot = next; this.emit(); }
  protected emit(): void { this.subs.forEach((cb) => cb()); }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/data/store.test.ts`
Expected: PASS — both blocks green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/data/store.ts ui/src/data/store.test.ts
git commit -m "feat(ui/data): PaintStore + ReactStore bases"
```

---

## Task 6: QuoteStore + BookStore

**Files:**
- Create: `ui/src/data/QuoteStore.ts`
- Create: `ui/src/data/BookStore.ts`
- Test: `ui/src/data/QuoteStore.test.ts`, `ui/src/data/BookStore.test.ts`

**Interfaces:**
- Consumes: `PaintStore` from `store.ts`; `Quote`, `Book`, `SnapshotMsg`, `DeltaMsg` from `contract.ts`.
- Produces:
  - `class QuoteStore extends PaintStore`: `apply(m: SnapshotMsg | DeltaMsg): void`; `get(symbol: string): Quote | undefined`. Delta merges fields onto the existing quote for that symbol.
  - `class BookStore extends PaintStore`: `apply(m: SnapshotMsg | DeltaMsg): void`; `get(symbol: string): Book | undefined`. Book is always a full replace (moomoo pushes the full 10-level book; replace is cheaper than diff).

- [ ] **Step 1: Write the failing test**

`ui/src/data/QuoteStore.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { QuoteStore } from "./QuoteStore";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

const snap = (p: unknown): SnapshotMsg => ({ kind: "snapshot", topic: "md.quote", key: "US.AAPL", payload: p });
const delta = (p: unknown): DeltaMsg => ({ kind: "delta", topic: "md.quote", key: "US.AAPL", payload: p });

describe("QuoteStore", () => {
  it("hydrates from snapshot and merges deltas, marking dirty", () => {
    const s = new QuoteStore();
    s.apply(snap({ symbol: "US.AAPL", bid: 3.49, ask: 3.51, last: 3.5, ts: "t0" }));
    expect(s.isDirty()).toBe(true);
    s.consumeDirty();
    s.apply(delta({ symbol: "US.AAPL", last: 3.6, ts: "t1" }));
    expect(s.get("US.AAPL")).toEqual({ symbol: "US.AAPL", bid: 3.49, ask: 3.51, last: 3.6, ts: "t1" });
    expect(s.isDirty()).toBe(true);
  });
});
```

`ui/src/data/BookStore.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { BookStore } from "./BookStore";
import type { SnapshotMsg } from "../wire/contract";

const book = (bids: number[][], asks: number[][]): SnapshotMsg => ({
  kind: "snapshot", topic: "md.book", key: "US.AAPL",
  payload: {
    symbol: "US.AAPL", ts: "t",
    bids: bids.map(([price, size]) => ({ price, size })),
    asks: asks.map(([price, size]) => ({ price, size })),
  },
});

describe("BookStore", () => {
  it("replaces the whole book on each apply", () => {
    const s = new BookStore();
    s.apply(book([[3.49, 100]], [[3.51, 200]]));
    expect(s.get("US.AAPL")?.bids).toHaveLength(1);
    s.apply({ ...book([[3.48, 50], [3.47, 75]], [[3.5, 10]]) });
    expect(s.get("US.AAPL")?.bids).toHaveLength(2);
    expect(s.get("US.AAPL")?.asks[0]).toEqual({ price: 3.5, size: 10 });
    expect(s.isDirty()).toBe(true);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/QuoteStore.test.ts src/data/BookStore.test.ts`
Expected: FAIL — modules not found.

- [ ] **Step 3: Write the implementations**

`ui/src/data/QuoteStore.ts`:
```ts
import { PaintStore } from "./store";
import type { Quote, SnapshotMsg, DeltaMsg } from "../wire/contract";

export class QuoteStore extends PaintStore {
  private readonly quotes = new Map<string, Quote>();

  apply(m: SnapshotMsg | DeltaMsg): void {
    const p = m.payload as Partial<Quote> & { symbol: string };
    const prev = this.quotes.get(p.symbol);
    // snapshot replaces; delta merges onto the prior quote for that symbol.
    const next: Quote = m.kind === "snapshot"
      ? (p as Quote)
      : { ...(prev ?? { symbol: p.symbol, bid: 0, ask: 0, last: 0, ts: "" }), ...p };
    this.quotes.set(p.symbol, next);
    this.markDirty();
  }

  get(symbol: string): Quote | undefined { return this.quotes.get(symbol); }
}
```

`ui/src/data/BookStore.ts`:
```ts
import { PaintStore } from "./store";
import type { Book, SnapshotMsg, DeltaMsg } from "../wire/contract";

export class BookStore extends PaintStore {
  private readonly books = new Map<string, Book>();

  // The engine pushes the full 10-level book every time; snapshot and delta are
  // both full replaces (replace is cheaper than diff at ~20 rows).
  apply(m: SnapshotMsg | DeltaMsg): void {
    const b = m.payload as Book;
    this.books.set(b.symbol, b);
    this.markDirty();
  }

  get(symbol: string): Book | undefined { return this.books.get(symbol); }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/data/QuoteStore.test.ts src/data/BookStore.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/src/data/QuoteStore.ts ui/src/data/BookStore.ts \
  ui/src/data/QuoteStore.test.ts ui/src/data/BookStore.test.ts
git commit -m "feat(ui/data): QuoteStore (merge) + BookStore (full replace)"
```

---

## Task 7: TapeRing — fixed-size ring buffer of ticks

**Files:**
- Create: `ui/src/data/TapeRing.ts`
- Test: `ui/src/data/TapeRing.test.ts`

**Interfaces:**
- Consumes: `PaintStore` from `store.ts`; `Tick`, `SnapshotMsg`, `DeltaMsg` from `contract.ts`.
- Produces `class TapeRing extends PaintStore`:
  - `constructor(capacity = 65536)`
  - `apply(m: SnapshotMsg | DeltaMsg): void` — snapshot payload is `Tick[]` (rebuilds the ring); delta payload is `Tick[]` (batch append, burst-tolerant).
  - `size(): number`; `at(i: number): Tick` where `i=0` is oldest retained; `latest(n: number): Tick[]` (newest-last, capped at retained size).

- [ ] **Step 1: Write the failing test**

`ui/src/data/TapeRing.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { TapeRing } from "./TapeRing";
import type { SnapshotMsg, DeltaMsg, Tick } from "../wire/contract";

const tick = (price: number): Tick => ({ symbol: "US.AAPL", price, size: 100, direction: "BUY", ts: `t${price}` });
const snap = (ticks: Tick[]): SnapshotMsg => ({ kind: "snapshot", topic: "md.tape", key: "US.AAPL", payload: ticks });
const delta = (ticks: Tick[]): DeltaMsg => ({ kind: "delta", topic: "md.tape", key: "US.AAPL", payload: ticks });

describe("TapeRing", () => {
  it("appends batches and preserves order", () => {
    const r = new TapeRing(4);
    r.apply(snap([tick(1), tick(2)]));
    r.apply(delta([tick(3)]));
    expect(r.size()).toBe(3);
    expect(r.latest(3).map((t) => t.price)).toEqual([1, 2, 3]);
    expect(r.isDirty()).toBe(true);
  });

  it("overwrites oldest when capacity is exceeded (burst-proof)", () => {
    const r = new TapeRing(3);
    r.apply(snap([tick(1), tick(2), tick(3)]));
    r.apply(delta([tick(4), tick(5)]));  // exceeds capacity by 2
    expect(r.size()).toBe(3);
    expect(r.latest(3).map((t) => t.price)).toEqual([3, 4, 5]);
    expect(r.at(0).price).toBe(3); // index 0 = oldest retained
  });

  it("snapshot rebuilds the ring from scratch", () => {
    const r = new TapeRing(4);
    r.apply(delta([tick(1), tick(2)]));
    r.apply(snap([tick(9)]));  // reconnect re-snapshot
    expect(r.size()).toBe(1);
    expect(r.latest(1)[0].price).toBe(9);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/TapeRing.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Write the implementation**

`ui/src/data/TapeRing.ts`:
```ts
import { PaintStore } from "./store";
import type { Tick, SnapshotMsg, DeltaMsg } from "../wire/contract";

// Fixed-size ring so a burst never grows unbounded; the painter reads the
// newest N each frame. Snapshot rebuilds (reconnect re-sync); delta appends.
export class TapeRing extends PaintStore {
  private readonly buf: Tick[];
  private head = 0;   // index of next write
  private count = 0;  // number of retained ticks (≤ capacity)

  constructor(private readonly capacity = 65536) {
    super();
    this.buf = new Array<Tick>(capacity);
  }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const ticks = m.payload as Tick[];
    if (m.kind === "snapshot") { this.head = 0; this.count = 0; }
    for (const t of ticks) {
      this.buf[this.head] = t;
      this.head = (this.head + 1) % this.capacity;
      if (this.count < this.capacity) this.count++;
    }
    this.markDirty();
  }

  size(): number { return this.count; }

  at(i: number): Tick {
    if (i < 0 || i >= this.count) throw new RangeError(`tape index ${i} out of [0,${this.count})`);
    const start = (this.head - this.count + this.capacity) % this.capacity;
    return this.buf[(start + i) % this.capacity];
  }

  latest(n: number): Tick[] {
    const take = Math.min(n, this.count);
    const out: Tick[] = new Array(take);
    for (let k = 0; k < take; k++) out[k] = this.at(this.count - take + k);
    return out;
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/data/TapeRing.test.ts`
Expected: PASS — all three cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/data/TapeRing.ts ui/src/data/TapeRing.test.ts
git commit -m "feat(ui/data): TapeRing fixed-size burst-proof tick buffer"
```

---

## Task 8: BarStore — per (symbol, timeframe), in-progress + watermark finalize

**Files:**
- Create: `ui/src/data/BarStore.ts`
- Test: `ui/src/data/BarStore.test.ts`

**Interfaces:**
- Consumes: `PaintStore` from `store.ts`; `Bar`, `SnapshotMsg`, `DeltaMsg` from `contract.ts`.
- Produces `class BarStore extends PaintStore`:
  - `apply(m: SnapshotMsg | DeltaMsg): void` — snapshot payload is `Bar[]` (backfill; burst-tolerant); delta payload is a single `Bar`. Bars keyed by `(symbol, timeframe, bucketStart)`. An `inProgress` bar for a bucket upserts in place; a bar for the same bucket with `inProgress:false` finalizes it. Applies to the correct `(symbol, timeframe)` series only.
  - `series(symbol: string, timeframe: string): Bar[]` — bucket-start ascending.
  - `inProgressBar(symbol: string, timeframe: string): Bar | undefined` — the last bar iff it is in progress.

- [ ] **Step 1: Write the failing test**

`ui/src/data/BarStore.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { BarStore } from "./BarStore";
import type { Bar, SnapshotMsg, DeltaMsg } from "../wire/contract";

const bar = (bucketStart: string, c: number, inProgress: boolean): Bar => ({
  symbol: "US.AAPL", timeframe: "1m", bucketStart,
  o: 3.5, h: 3.6, l: 3.4, c, v: 1000, inProgress,
});
const snap = (bars: Bar[]): SnapshotMsg => ({ kind: "snapshot", topic: "md.bars", key: "US.AAPL:1m", payload: bars });
const delta = (b: Bar): DeltaMsg => ({ kind: "delta", topic: "md.bars", key: "US.AAPL:1m", payload: b });

describe("BarStore", () => {
  it("seeds from a burst snapshot in bucket order", () => {
    const s = new BarStore();
    s.apply(snap([bar("09:31", 3.5, false), bar("09:30", 3.4, false)]));
    expect(s.series("US.AAPL", "1m").map((b) => b.bucketStart)).toEqual(["09:30", "09:31"]);
    expect(s.isDirty()).toBe(true);
  });

  it("upserts the in-progress bar in place, then finalizes it", () => {
    const s = new BarStore();
    s.apply(delta(bar("09:30", 3.5, true)));
    s.apply(delta(bar("09:30", 3.55, true)));  // same bucket updates in place
    expect(s.series("US.AAPL", "1m")).toHaveLength(1);
    expect(s.inProgressBar("US.AAPL", "1m")?.c).toBe(3.55);
    s.apply(delta(bar("09:30", 3.58, false))); // finalize
    expect(s.inProgressBar("US.AAPL", "1m")).toBeUndefined();
    expect(s.series("US.AAPL", "1m")[0].c).toBe(3.58);
  });

  it("keeps series for different timeframes separate", () => {
    const s = new BarStore();
    s.apply(delta(bar("09:30", 3.5, false)));
    s.apply({ kind: "delta", topic: "md.bars", key: "US.AAPL:10s",
      payload: { ...bar("09:30:10", 3.5, false), timeframe: "10s" } });
    expect(s.series("US.AAPL", "1m")).toHaveLength(1);
    expect(s.series("US.AAPL", "10s")).toHaveLength(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/BarStore.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Write the implementation**

`ui/src/data/BarStore.ts`:
```ts
import { PaintStore } from "./store";
import type { Bar, SnapshotMsg, DeltaMsg } from "../wire/contract";

// One ordered series per (symbol, timeframe). The last bar may be in-progress
// (updates in place on every push); it finalizes only when a bar with the same
// bucketStart arrives with inProgress:false, or a later bucket appears. Quiet
// symbols may hold a partial past its wall-clock end — never a "closed" bar.
export class BarStore extends PaintStore {
  private readonly series_ = new Map<string, Bar[]>();

  private key(symbol: string, timeframe: string): string { return `${symbol}:${timeframe}`; }

  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.kind === "snapshot") {
      const bars = (m.payload as Bar[]).slice().sort((a, b) => a.bucketStart.localeCompare(b.bucketStart));
      if (bars.length > 0) this.series_.set(this.key(bars[0].symbol, bars[0].timeframe), bars);
      this.markDirty();
      return;
    }
    const b = m.payload as Bar;
    const k = this.key(b.symbol, b.timeframe);
    const arr = this.series_.get(k) ?? [];
    const last = arr[arr.length - 1];
    if (last && last.bucketStart === b.bucketStart) {
      arr[arr.length - 1] = b; // upsert in place (in-progress update or finalize)
    } else {
      arr.push(b);             // new bucket
    }
    this.series_.set(k, arr);
    this.markDirty();
  }

  series(symbol: string, timeframe: string): Bar[] {
    return this.series_.get(this.key(symbol, timeframe)) ?? [];
  }

  inProgressBar(symbol: string, timeframe: string): Bar | undefined {
    const arr = this.series_.get(this.key(symbol, timeframe));
    const last = arr?.[arr.length - 1];
    return last?.inProgress ? last : undefined;
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/data/BarStore.test.ts`
Expected: PASS — all three cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/data/BarStore.ts ui/src/data/BarStore.test.ts
git commit -m "feat(ui/data): BarStore in-progress upsert + watermark finalize per series"
```

---

## Task 9: React-observable stores — Health, Exec, Scanner, News

**Files:**
- Create: `ui/src/data/HealthStore.ts`
- Create: `ui/src/data/ExecStore.ts`
- Create: `ui/src/data/ScannerStore.ts`
- Create: `ui/src/data/NewsStore.ts`
- Test: `ui/src/data/HealthStore.test.ts` (representative; Exec/Scanner/News share the pattern and get one test file each)
- Test: `ui/src/data/reactStores.test.ts`

**Interfaces:**
- Consumes: `ReactStore` from `store.ts`; `HealthSnapshot`, `SysEvent`, `SnapshotMsg`, `DeltaMsg` from `contract.ts`.
- Produces (all extend `ReactStore<S>`, all expose `apply(m: SnapshotMsg | DeltaMsg)` and read via `getSnapshot()`/`subscribe()`):
  - `class HealthStore extends ReactStore<{ links: HealthLink[]; events: SysEvent[] }>` — routes `sys.health` snapshots/deltas to `links` (replace), `sys.events` snapshots/deltas to `events` (append, newest last, capped at 500). `apply` dispatches on `m.topic`.
  - `class ExecStore extends ReactStore<{ account: Record<string, unknown> | null; positions: unknown[]; orders: unknown[] }>` — routes `exec.account`/`exec.positions`/`exec.orders` (fleshed out in Plan 5; here: snapshot replaces the relevant slice, delta upserts by key). Minimal but real so the shell can observe exec status.
  - `class ScannerStore extends ReactStore<{ rows: unknown[] }>` and `class NewsStore extends ReactStore<{ items: unknown[] }>` — snapshot replaces, delta appends (dedup by key deferred to Plan 4).

- [ ] **Step 1: Write the failing test**

`ui/src/data/HealthStore.test.ts`:
```ts
import { describe, it, expect, vi } from "vitest";
import { HealthStore } from "./HealthStore";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

const healthSnap = (): SnapshotMsg => ({
  kind: "snapshot", topic: "sys.health",
  payload: { links: [{ link: "ui-engine", ms: 1, min: 1, avg: 1, max: 1, status: "ok" }] },
});
const eventDelta = (seq: number): DeltaMsg => ({
  kind: "delta", topic: "sys.events",
  payload: { seq, ts: `t${seq}`, kind: "reconnect", detail: `attempt ${seq}` },
});

describe("HealthStore", () => {
  it("routes health links and appends events, notifying subscribers", () => {
    const s = new HealthStore();
    const cb = vi.fn();
    s.subscribe(cb);
    s.apply(healthSnap());
    s.apply(eventDelta(1));
    s.apply(eventDelta(2));
    const snap = s.getSnapshot();
    expect(snap.links[0].link).toBe("ui-engine");
    expect(snap.events.map((e) => e.seq)).toEqual([1, 2]);
    expect(cb).toHaveBeenCalledTimes(3);
  });

  it("caps the event log at 500 entries", () => {
    const s = new HealthStore();
    for (let i = 0; i < 600; i++) s.apply(eventDelta(i));
    const snap = s.getSnapshot();
    expect(snap.events).toHaveLength(500);
    expect(snap.events[0].seq).toBe(100); // oldest 100 dropped
  });
});
```

`ui/src/data/reactStores.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { ExecStore } from "./ExecStore";
import { ScannerStore } from "./ScannerStore";
import { NewsStore } from "./NewsStore";

describe("ExecStore", () => {
  it("replaces account on snapshot", () => {
    const s = new ExecStore();
    s.apply({ kind: "snapshot", topic: "exec.account", payload: { equity: 1000, armed: false } });
    expect(s.getSnapshot().account).toMatchObject({ equity: 1000, armed: false });
  });
});

describe("ScannerStore / NewsStore", () => {
  it("replace on snapshot and append on delta", () => {
    const sc = new ScannerStore();
    sc.apply({ kind: "snapshot", topic: "scanner.rank", payload: [{ symbol: "US.AAA" }] });
    sc.apply({ kind: "delta", topic: "scanner.rank", payload: { symbol: "US.BBB" } });
    expect(sc.getSnapshot().rows).toHaveLength(2);

    const n = new NewsStore();
    n.apply({ kind: "snapshot", topic: "news.item", payload: [] });
    n.apply({ kind: "delta", topic: "news.item", payload: { symbol: "US.AAA", headline: "x" } });
    expect(n.getSnapshot().items).toHaveLength(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/HealthStore.test.ts src/data/reactStores.test.ts`
Expected: FAIL — modules not found.

- [ ] **Step 3: Write the implementations**

`ui/src/data/HealthStore.ts`:
```ts
import { ReactStore } from "./store";
import type { HealthLink, HealthSnapshot, SysEvent, SnapshotMsg, DeltaMsg } from "../wire/contract";

interface HealthState { links: HealthLink[]; events: SysEvent[] }
const MAX_EVENTS = 500;

export class HealthStore extends ReactStore<HealthState> {
  constructor() { super({ links: [], events: [] }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    if (m.topic === "sys.health") {
      this.set({ ...cur, links: (m.payload as HealthSnapshot).links });
      return;
    }
    if (m.topic === "sys.events") {
      const incoming = m.kind === "snapshot" ? (m.payload as SysEvent[]) : [m.payload as SysEvent];
      const events = [...cur.events, ...incoming];
      this.set({ ...cur, events: events.slice(Math.max(0, events.length - MAX_EVENTS)) });
    }
  }
}
```

`ui/src/data/ExecStore.ts`:
```ts
import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

interface ExecState {
  account: Record<string, unknown> | null;
  positions: unknown[];
  orders: unknown[];
}

// Minimal in Plan 1 — Plan 5 replaces the payload shapes with typed Order/Position/
// Account and adds keyed upserts + the 9-state order lifecycle.
export class ExecStore extends ReactStore<ExecState> {
  constructor() { super({ account: null, positions: [], orders: [] }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    switch (m.topic) {
      case "exec.account":
        this.set({ ...cur, account: m.payload as Record<string, unknown> });
        return;
      case "exec.positions":
        this.set({ ...cur, positions: m.kind === "snapshot" ? (m.payload as unknown[]) : [...cur.positions, m.payload] });
        return;
      case "exec.orders":
        this.set({ ...cur, orders: m.kind === "snapshot" ? (m.payload as unknown[]) : [...cur.orders, m.payload] });
        return;
      default:
        return;
    }
  }
}
```

`ui/src/data/ScannerStore.ts`:
```ts
import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

// Plan 4 adds session parameterization, dedup + new-hit flash, threshold config.
export class ScannerStore extends ReactStore<{ rows: unknown[] }> {
  constructor() { super({ rows: [] }); }
  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    this.set({ rows: m.kind === "snapshot" ? (m.payload as unknown[]) : [...cur.rows, m.payload] });
  }
}
```

`ui/src/data/NewsStore.ts`:
```ts
import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

// Plan 4 adds NewsItem typing, seen-time labeling, per-symbol filtering, dedup.
export class NewsStore extends ReactStore<{ items: unknown[] }> {
  constructor() { super({ items: [] }); }
  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    this.set({ items: m.kind === "snapshot" ? (m.payload as unknown[]) : [...cur.items, m.payload] });
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/data/HealthStore.test.ts src/data/reactStores.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/src/data/HealthStore.ts ui/src/data/ExecStore.ts ui/src/data/ScannerStore.ts \
  ui/src/data/NewsStore.ts ui/src/data/HealthStore.test.ts ui/src/data/reactStores.test.ts
git commit -m "feat(ui/data): React-observable Health/Exec/Scanner/News stores"
```

---

## Task 10: rAF Scheduler + Surface registration + painter error isolation

**Files:**
- Create: `ui/src/render/surface.ts`
- Create: `ui/src/render/Scheduler.ts`
- Extend: `ui/test/fakes.ts` (add fake rAF)
- Test: `ui/src/render/Scheduler.test.ts`

**Interfaces:**
- Consumes: nothing from other layers (render depends only on data at runtime via the surface's own closure; the Scheduler is data-agnostic).
- Produces:
  - `surface.ts`: `interface Surface { id: string; isDirty(): boolean; paint(): void }` and `interface RafLike { request(cb: () => void): number; cancel(id: number): void }`.
  - `Scheduler.ts`: `class Scheduler` with `constructor(raf: RafLike, onPainterError: (id: string, err: unknown) => void)`; `register(s: Surface): () => void`; `start()`; `stop()`. Each frame: for every registered surface, if `isDirty()` call `paint()`; a surface whose `paint()` throws is unregistered and reported via `onPainterError` (matches spec: painter crash → unregistered + inline card), the rest keep painting.

- [ ] **Step 1: Write the failing test**

Add to `ui/test/fakes.ts`:
```ts
import type { RafLike } from "../src/render/surface";

export class FakeRaf implements RafLike {
  private cbs = new Map<number, () => void>();
  private id = 0;
  request(cb: () => void): number { const id = ++this.id; this.cbs.set(id, cb); return id; }
  cancel(id: number): void { this.cbs.delete(id); }
  // test helper: run one frame (snapshots callbacks so re-registration lands next frame)
  tick(): void { const batch = [...this.cbs.values()]; this.cbs.clear(); batch.forEach((cb) => cb()); }
}
```

`ui/src/render/Scheduler.test.ts`:
```ts
import { describe, it, expect, vi } from "vitest";
import { Scheduler } from "./Scheduler";
import { FakeRaf } from "../../test/fakes";
import type { Surface } from "./surface";

function surf(id: string, dirty: () => boolean, paint: () => void): Surface {
  return { id, isDirty: dirty, paint };
}

describe("Scheduler", () => {
  it("paints only dirty surfaces, once per frame", () => {
    const raf = new FakeRaf();
    const sched = new Scheduler(raf, () => {});
    let dirtyA = true;
    const paintA = vi.fn(() => { dirtyA = false; });
    const paintB = vi.fn();
    sched.register(surf("a", () => dirtyA, paintA));
    sched.register(surf("b", () => false, paintB));
    sched.start();
    raf.tick();
    expect(paintA).toHaveBeenCalledTimes(1);
    expect(paintB).not.toHaveBeenCalled();
    raf.tick();
    expect(paintA).toHaveBeenCalledTimes(1); // no longer dirty
  });

  it("unregisters a painter that throws and reports it, others survive", () => {
    const raf = new FakeRaf();
    const onErr = vi.fn();
    const sched = new Scheduler(raf, onErr);
    const good = vi.fn();
    sched.register(surf("bad", () => true, () => { throw new Error("boom"); }));
    sched.register(surf("good", () => true, good));
    sched.start();
    raf.tick();
    expect(onErr).toHaveBeenCalledWith("bad", expect.any(Error));
    expect(good).toHaveBeenCalledTimes(1);
    raf.tick();
    expect(good).toHaveBeenCalledTimes(2); // bad no longer scheduled; good keeps painting
  });

  it("stops requesting frames after stop()", () => {
    const raf = new FakeRaf();
    const sched = new Scheduler(raf, () => {});
    const paint = vi.fn();
    sched.register(surf("a", () => true, paint));
    sched.start();
    raf.tick();
    sched.stop();
    raf.tick();
    expect(paint).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/render/Scheduler.test.ts`
Expected: FAIL — modules not found.

- [ ] **Step 3: Write the implementations**

`ui/src/render/surface.ts`:
```ts
// A paintable surface polled by the scheduler. Backed by a PaintStore's dirty flag
// in practice; kept as a minimal interface so the render layer never imports data.
export interface Surface {
  id: string;
  isDirty(): boolean;
  paint(): void;
}

export interface RafLike {
  request(cb: () => void): number;
  cancel(id: number): void;
}

// Production RafLike over window.requestAnimationFrame.
export const browserRaf: RafLike = {
  request: (cb) => requestAnimationFrame(cb),
  cancel: (id) => cancelAnimationFrame(id),
};
```

`ui/src/render/Scheduler.ts`:
```ts
import type { RafLike, Surface } from "./surface";

// One loop per window. Every frame, paint each dirty surface exactly once, then
// clear (the surface clears its own dirty flag inside paint()). A painter that
// throws is removed and reported — one broken panel never stalls the frame.
export class Scheduler {
  private readonly surfaces = new Map<string, Surface>();
  private running = false;
  private frame: number | null = null;

  constructor(
    private readonly raf: RafLike,
    private readonly onPainterError: (id: string, err: unknown) => void,
  ) {}

  register(s: Surface): () => void {
    this.surfaces.set(s.id, s);
    return () => { this.surfaces.delete(s.id); };
  }

  start(): void {
    if (this.running) return;
    this.running = true;
    this.schedule();
  }

  stop(): void {
    this.running = false;
    if (this.frame !== null) { this.raf.cancel(this.frame); this.frame = null; }
  }

  private schedule(): void {
    this.frame = this.raf.request(() => {
      this.frame = null;
      this.paintFrame();
      if (this.running) this.schedule();
    });
  }

  private paintFrame(): void {
    for (const s of [...this.surfaces.values()]) {
      if (!s.isDirty()) continue;
      try {
        s.paint();
      } catch (err) {
        this.surfaces.delete(s.id);
        this.onPainterError(s.id, err);
      }
    }
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/render/Scheduler.test.ts`
Expected: PASS — all three cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/surface.ts ui/src/render/Scheduler.ts ui/test/fakes.ts ui/src/render/Scheduler.test.ts
git commit -m "feat(ui/render): rAF Scheduler with painter error isolation"
```

---

## Task 11: Topic→store registry + WsClient wiring

**Files:**
- Create: `ui/src/data/registry.ts`
- Test: `ui/src/data/registry.test.ts`

**Interfaces:**
- Consumes: all stores (Quote/Book/Tape/Bar/Health/Exec/Scanner/News); `WsClient` (only its `subscribe` signature); `TopicName`, `SnapshotMsg`, `DeltaMsg`.
- Produces:
  - `interface Stores { quote: QuoteStore; book: BookStore; tape: TapeRing; bars: BarStore; health: HealthStore; exec: ExecStore; scanner: ScannerStore; news: NewsStore }`
  - `function makeStores(): Stores`
  - `function routeToStore(stores: Stores, m: SnapshotMsg | DeltaMsg): void` — dispatches a decoded topic message to the right store's `apply`.
  - `interface TopicSubscriber { subscribe(topic: TopicName, cb: (m: SnapshotMsg | DeltaMsg) => void): () => void }` (structural subset of `WsClient`).
  - `function connectStores(client: TopicSubscriber, stores: Stores, topics: TopicName[]): () => void` — subscribes each topic, routing to stores; returns a disposer that unsubscribes all.

- [ ] **Step 1: Write the failing test**

`ui/src/data/registry.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { makeStores, routeToStore, connectStores } from "./registry";
import type { SnapshotMsg, DeltaMsg, TopicName } from "../wire/contract";

describe("routeToStore", () => {
  it("dispatches each topic to its store", () => {
    const stores = makeStores();
    routeToStore(stores, { kind: "snapshot", topic: "md.quote", key: "US.AAPL",
      payload: { symbol: "US.AAPL", bid: 1, ask: 2, last: 1.5, ts: "t" } });
    expect(stores.quote.get("US.AAPL")?.last).toBe(1.5);

    routeToStore(stores, { kind: "snapshot", topic: "sys.health",
      payload: { links: [{ link: "ui-engine", ms: 1, min: 1, avg: 1, max: 1, status: "ok" }] } });
    expect(stores.health.getSnapshot().links).toHaveLength(1);
  });
});

describe("connectStores", () => {
  it("subscribes requested topics and routes their messages", () => {
    const stores = makeStores();
    const handlers = new Map<string, (m: SnapshotMsg | DeltaMsg) => void>();
    const fakeClient = {
      subscribe(topic: TopicName, cb: (m: SnapshotMsg | DeltaMsg) => void) {
        handlers.set(topic, cb);
        return () => handlers.delete(topic);
      },
    };
    const dispose = connectStores(fakeClient, stores, ["md.quote", "sys.health"]);
    expect([...handlers.keys()].sort()).toEqual(["md.quote", "sys.health"]);

    handlers.get("md.quote")!({ kind: "snapshot", topic: "md.quote", key: "US.X",
      payload: { symbol: "US.X", bid: 1, ask: 2, last: 3, ts: "t" } });
    expect(stores.quote.get("US.X")?.last).toBe(3);

    dispose();
    expect(handlers.size).toBe(0);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/registry.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Write the implementation**

`ui/src/data/registry.ts`:
```ts
import type { SnapshotMsg, DeltaMsg, TopicName } from "../wire/contract";
import { QuoteStore } from "./QuoteStore";
import { BookStore } from "./BookStore";
import { TapeRing } from "./TapeRing";
import { BarStore } from "./BarStore";
import { HealthStore } from "./HealthStore";
import { ExecStore } from "./ExecStore";
import { ScannerStore } from "./ScannerStore";
import { NewsStore } from "./NewsStore";

export interface Stores {
  quote: QuoteStore;
  book: BookStore;
  tape: TapeRing;
  bars: BarStore;
  health: HealthStore;
  exec: ExecStore;
  scanner: ScannerStore;
  news: NewsStore;
}

export function makeStores(): Stores {
  return {
    quote: new QuoteStore(),
    book: new BookStore(),
    tape: new TapeRing(),
    bars: new BarStore(),
    health: new HealthStore(),
    exec: new ExecStore(),
    scanner: new ScannerStore(),
    news: new NewsStore(),
  };
}

export function routeToStore(stores: Stores, m: SnapshotMsg | DeltaMsg): void {
  switch (m.topic) {
    case "md.quote": stores.quote.apply(m); return;
    case "md.book": stores.book.apply(m); return;
    case "md.tape": stores.tape.apply(m); return;
    case "md.bars": stores.bars.apply(m); return;
    case "md.indicator": return; // Plan 2 adds an IndicatorStore
    case "scanner.rank":
    case "scanner.hit": stores.scanner.apply(m); return;
    case "news.item": stores.news.apply(m); return;
    case "exec.account":
    case "exec.positions":
    case "exec.orders":
    case "exec.fills":
    case "exec.status": stores.exec.apply(m); return;
    case "sys.health":
    case "sys.events": stores.health.apply(m); return;
    case "config": return; // handled by workspace.ts, not a store
  }
}

export interface TopicSubscriber {
  subscribe(topic: TopicName, cb: (m: SnapshotMsg | DeltaMsg) => void): () => void;
}

export function connectStores(client: TopicSubscriber, stores: Stores, topics: TopicName[]): () => void {
  const offs = topics.map((t) => client.subscribe(t, (m) => routeToStore(stores, m)));
  return () => offs.forEach((off) => off());
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/data/registry.test.ts`
Expected: PASS — both blocks green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/data/registry.ts ui/src/data/registry.test.ts
git commit -m "feat(ui/data): topic→store registry + WsClient wiring"
```

---

## Task 12: Link groups — LinkBus + BroadcastChannel + focus state

**Files:**
- Create: `ui/src/chrome/linkGroups.ts`
- Extend: `ui/test/fakes.ts` (add FakeBus)
- Test: `ui/src/chrome/linkGroups.test.ts`

**Interfaces:**
- Consumes: nothing from lower layers (chrome-only, message-passing).
- Produces:
  - `type LinkGroup = "red" | "green" | "blue" | "yellow" | null` (null = pinned).
  - `interface LinkBus { post(msg: { group: LinkGroup; symbol: string }): void; onMessage(cb: (msg: { group: LinkGroup; symbol: string }) => void): () => void; close(): void }`.
  - `class BroadcastChannelBus implements LinkBus` — wraps `new BroadcastChannel("etape.link")`.
  - `class LinkGroups` — holds per-group focused symbol; `focus(group, symbol)` sets local state, publishes on the bus, and invokes an `onEcho(group, symbol)` callback (engine persistence + pre-subscription); incoming bus messages update local state without re-publishing (no echo storm). `symbolFor(group): string | undefined`; `subscribe(cb): () => void`.

- [ ] **Step 1: Write the failing test**

Add to `ui/test/fakes.ts`:
```ts
import type { LinkBus } from "../src/chrome/linkGroups";

// Shared in-memory bus simulating BroadcastChannel across "windows".
export class FakeBusHub {
  private buses = new Set<FakeBus>();
  join(b: FakeBus): void { this.buses.add(b); }
  leave(b: FakeBus): void { this.buses.delete(b); }
  broadcast(from: FakeBus, msg: { group: unknown; symbol: string }): void {
    this.buses.forEach((b) => { if (b !== from) b.deliver(msg); });
  }
}
export class FakeBus implements LinkBus {
  private cb: ((msg: { group: any; symbol: string }) => void) | null = null;
  constructor(private hub: FakeBusHub) { hub.join(this); }
  post(msg: { group: any; symbol: string }): void { this.hub.broadcast(this, msg); }
  onMessage(cb: (msg: { group: any; symbol: string }) => void): () => void { this.cb = cb; return () => { this.cb = null; }; }
  deliver(msg: { group: any; symbol: string }): void { this.cb?.(msg); }
  close(): void { this.hub.leave(this); }
}
```

`ui/src/chrome/linkGroups.test.ts`:
```ts
import { describe, it, expect, vi } from "vitest";
import { LinkGroups } from "./linkGroups";
import { FakeBus, FakeBusHub } from "../../test/fakes";

describe("LinkGroups", () => {
  it("focus updates local state, publishes on the bus, and echoes to the engine", () => {
    const hub = new FakeBusHub();
    const onEcho = vi.fn();
    const lg = new LinkGroups(new FakeBus(hub), onEcho);
    lg.focus("green", "US.AAPL");
    expect(lg.symbolFor("green")).toBe("US.AAPL");
    expect(onEcho).toHaveBeenCalledWith("green", "US.AAPL");
  });

  it("propagates focus across windows without an echo storm", () => {
    const hub = new FakeBusHub();
    const echoA = vi.fn();
    const echoB = vi.fn();
    const a = new LinkGroups(new FakeBus(hub), echoA);
    const b = new LinkGroups(new FakeBus(hub), echoB);
    a.focus("red", "US.TSLA");
    expect(b.symbolFor("red")).toBe("US.TSLA"); // B received it
    expect(echoB).not.toHaveBeenCalled();       // B does not re-echo remote focus
  });

  it("notifies subscribers on any focus change", () => {
    const hub = new FakeBusHub();
    const lg = new LinkGroups(new FakeBus(hub), () => {});
    const cb = vi.fn();
    lg.subscribe(cb);
    lg.focus("blue", "US.NVDA");
    expect(cb).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/linkGroups.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Write the implementation**

`ui/src/chrome/linkGroups.ts`:
```ts
export type LinkGroup = "red" | "green" | "blue" | "yellow" | null; // null = pinned
export interface LinkMsg { group: LinkGroup; symbol: string }

export interface LinkBus {
  post(msg: LinkMsg): void;
  onMessage(cb: (msg: LinkMsg) => void): () => void;
  close(): void;
}

export class BroadcastChannelBus implements LinkBus {
  private ch = new BroadcastChannel("etape.link");
  post(msg: LinkMsg): void { this.ch.postMessage(msg); }
  onMessage(cb: (msg: LinkMsg) => void): () => void {
    const handler = (e: MessageEvent) => cb(e.data as LinkMsg);
    this.ch.addEventListener("message", handler);
    return () => this.ch.removeEventListener("message", handler);
  }
  close(): void { this.ch.close(); }
}

// Per-group focused symbol. Local focus publishes cross-window + echoes to the
// engine; remote focus (from the bus) updates state but never re-publishes.
export class LinkGroups {
  private readonly focused = new Map<Exclude<LinkGroup, null>, string>();
  private readonly subs = new Set<() => void>();

  constructor(
    private readonly bus: LinkBus,
    private readonly onEcho: (group: Exclude<LinkGroup, null>, symbol: string) => void,
  ) {
    this.bus.onMessage((msg) => { if (msg.group) this.setLocal(msg.group, msg.symbol); });
  }

  focus(group: Exclude<LinkGroup, null>, symbol: string): void {
    this.setLocal(group, symbol);
    this.bus.post({ group, symbol });
    this.onEcho(group, symbol);
  }

  private setLocal(group: Exclude<LinkGroup, null>, symbol: string): void {
    this.focused.set(group, symbol);
    this.subs.forEach((cb) => cb());
  }

  symbolFor(group: LinkGroup): string | undefined {
    return group ? this.focused.get(group) : undefined;
  }

  subscribe(cb: () => void): () => void { this.subs.add(cb); return () => this.subs.delete(cb); }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/linkGroups.test.ts`
Expected: PASS — all three cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/chrome/linkGroups.ts ui/test/fakes.ts ui/src/chrome/linkGroups.test.ts
git commit -m "feat(ui/chrome): link groups over BroadcastChannel with engine echo"
```

---

## Task 13: Workspace document + config-CRUD persistence + seed workspaces

**Files:**
- Create: `ui/src/chrome/workspace.ts`
- Create: `ui/src/seeds/workspaces.ts`
- Test: `ui/src/chrome/workspace.test.ts`

**Interfaces:**
- Consumes: `WsClient.sendCommand` (structural: `{ sendCommand(name: string, args: unknown): Promise<{status: string}> }`); `WsClient.subscribe` for the `config` topic.
- Produces:
  - `interface PanelConfig { id: string; panelId: string; group: LinkGroup; settings: Record<string, unknown> }`
  - `interface Workspace { name: string; panels: PanelConfig[]; layout: unknown /* dockview JSON */ }`
  - `class WorkspaceStore` — `load(name): Promise<Workspace>` (reads `config` doc `workspace.<name>`, falls back to a seed if absent); `save(ws): void` (debounced config CRUD write); `flush(): Promise<void>` (force pending save — for tests/shutdown).
  - `seeds/workspaces.ts`: `SEED_WORKSPACES: Record<"monitoring" | "trading", Workspace>` — the two seed layouts (monitoring: 4 charts + scanner + movers + news + connection-status; trading: 4 charts + ladder + tape + account bar + positions + orders + ticket). Panels reference `panelId`s registered in Task 14; layout is a minimal dockview grid.

- [ ] **Step 1: Write the failing test**

`ui/src/chrome/workspace.test.ts`:
```ts
import { describe, it, expect, vi } from "vitest";
import { WorkspaceStore } from "./workspace";
import { SEED_WORKSPACES } from "../seeds/workspaces";

function fakeClient() {
  const calls: Array<{ name: string; args: any }> = [];
  return {
    calls,
    sendCommand: vi.fn(async (name: string, args: unknown) => { calls.push({ name, args }); return { status: "accepted" }; }),
  };
}

describe("WorkspaceStore", () => {
  it("falls back to the seed when no saved doc exists", async () => {
    const client = fakeClient();
    // getConfig returns null (nothing saved yet)
    client.sendCommand.mockImplementationOnce(async () => ({ status: "accepted", value: null }));
    const store = new WorkspaceStore(client as any, 10);
    const ws = await store.load("monitoring");
    expect(ws.name).toBe("Monitoring");
    expect(ws.panels.length).toBe(SEED_WORKSPACES.monitoring.panels.length);
  });

  it("debounces saves into a single config write", async () => {
    vi.useFakeTimers();
    const client = fakeClient();
    const store = new WorkspaceStore(client as any, 50);
    store.save({ ...SEED_WORKSPACES.trading });
    store.save({ ...SEED_WORKSPACES.trading });
    store.save({ ...SEED_WORKSPACES.trading });
    expect(client.calls.filter((c) => c.name === "SetConfig")).toHaveLength(0);
    vi.advanceTimersByTime(60);
    await store.flush();
    expect(client.calls.filter((c) => c.name === "SetConfig")).toHaveLength(1);
    vi.useRealTimers();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/workspace.test.ts`
Expected: FAIL — modules not found.

- [ ] **Step 3: Write the implementations**

`ui/src/seeds/workspaces.ts`:
```ts
import type { Workspace } from "../chrome/workspace";

// Seed documents. Panel ids match the registry in chrome/panels/registry.tsx.
// Later plans add chart/ladder/tape/etc. panelIds; Plan 1 registers only
// connection-status and smoke-painter, so unknown panelIds render a "coming soon"
// placeholder frame rather than crashing (see PanelFrame).
const chart = (id: string, symbol: string, timeframe: string, group: NonNullable<Workspace["panels"][number]["group"]>): Workspace["panels"][number] =>
  ({ id, panelId: "chart", group, settings: { symbol, timeframe } });

export const SEED_WORKSPACES: Record<"monitoring" | "trading", Workspace> = {
  monitoring: {
    name: "Monitoring",
    panels: [
      chart("m-c1", "US.AAPL", "1m", "green"),
      chart("m-c2", "US.NVDA", "1m", "blue"),
      chart("m-c3", "US.TSLA", "1m", "red"),
      chart("m-c4", "US.SPY", "1m", "yellow"),
      { id: "m-scanner", panelId: "scanner", group: null, settings: {} },
      { id: "m-movers", panelId: "movers", group: null, settings: {} },
      { id: "m-news", panelId: "news", group: "green", settings: {} },
      { id: "m-conn", panelId: "connection-status", group: null, settings: {} },
    ],
    layout: { grid: "seed-monitoring" },
  },
  trading: {
    name: "Trading",
    panels: [
      chart("t-c1", "US.AAPL", "1m", "green"),
      chart("t-c2", "US.AAPL", "10s", "green"),
      chart("t-c3", "US.AAPL", "5m", "green"),
      chart("t-c4", "US.AAPL", "60m", "green"),
      { id: "t-ladder", panelId: "ladder", group: "green", settings: {} },
      { id: "t-tape", panelId: "tape", group: "green", settings: {} },
      { id: "t-account", panelId: "account-bar", group: null, settings: {} },
      { id: "t-positions", panelId: "positions", group: null, settings: {} },
      { id: "t-orders", panelId: "open-orders", group: null, settings: {} },
      { id: "t-ticket", panelId: "order-ticket", group: "green", settings: {} },
    ],
    layout: { grid: "seed-trading" },
  },
};
```

`ui/src/chrome/workspace.ts`:
```ts
import type { LinkGroup } from "./linkGroups";
import { SEED_WORKSPACES } from "../seeds/workspaces";

export interface PanelConfig {
  id: string;
  panelId: string;
  group: LinkGroup;
  settings: Record<string, unknown>;
}
export interface Workspace {
  name: string;
  panels: PanelConfig[];
  layout: unknown; // dockview serialized layout JSON
}

interface CommandClient {
  sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }>;
}

// Auto-saves the dockview layout + panel configs to the engine's config store
// (config key `workspace.<name>`), debounced. Loads the saved doc or a seed.
export class WorkspaceStore {
  private pending: Workspace | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(private readonly client: CommandClient, private readonly debounceMs = 500) {}

  async load(name: "monitoring" | "trading"): Promise<Workspace> {
    const ack = await this.client.sendCommand("GetConfig", { key: `workspace.${name}` });
    if (ack.status === "accepted" && ack.value) return ack.value as Workspace;
    return structuredClone(SEED_WORKSPACES[name]);
  }

  save(ws: Workspace): void {
    this.pending = ws;
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => { void this.writeNow(); }, this.debounceMs);
  }

  async flush(): Promise<void> {
    if (this.timer) { clearTimeout(this.timer); this.timer = null; }
    await this.writeNow();
  }

  private async writeNow(): Promise<void> {
    if (!this.pending) return;
    const ws = this.pending;
    this.pending = null;
    this.timer = null;
    const key = `workspace.${ws.name.toLowerCase()}`;
    await this.client.sendCommand("SetConfig", { key, value: ws });
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/workspace.test.ts`
Expected: PASS — both cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/chrome/workspace.ts ui/src/seeds/workspaces.ts ui/src/chrome/workspace.test.ts
git commit -m "feat(ui/chrome): workspace document + debounced config-CRUD persistence + seeds"
```

---

## Task 14: Integrate — shell, panel frame, Connection Status panel, smoke painter, reconnect overlay + store/replay test

This task assembles the pieces into a running window and proves the whole stack end-to-end against the mock engine, then locks the reconnect invariant with a replay test.

**Files:**
- Create: `ui/src/chrome/ErrorBoundary.tsx`
- Create: `ui/src/chrome/ReconnectOverlay.tsx`
- Create: `ui/src/chrome/PanelFrame.tsx`
- Create: `ui/src/chrome/panels/registry.tsx`
- Create: `ui/src/chrome/panels/ConnectionStatusPanel.tsx`
- Create: `ui/src/chrome/panels/SmokePainterPanel.tsx`
- Create: `ui/src/chrome/AppShell.tsx`
- Create: `ui/src/App.tsx`
- Modify: `ui/src/main.tsx`
- Test: `ui/src/chrome/ConnectionStatusPanel.test.tsx` (jsdom)
- Test: `ui/src/chrome/ErrorBoundary.test.tsx` (jsdom)
- Test: `ui/src/replay.test.ts` (store/replay invariant against the fixture)

**Interfaces:**
- Consumes: `WsClient`, `makeStores`/`connectStores`, `Scheduler`, `LinkGroups`/`BroadcastChannelBus`, `WorkspaceStore`, `HealthStore`, all panels.
- Produces:
  - `ErrorBoundary` (React class boundary → inline error card + reload button).
  - `ReconnectOverlay` (dims children + shows "reconnecting" when `state !== "open"`).
  - `PanelFrame` (header with link swatch; wraps a panel in an ErrorBoundary; ResizeObserver → passes `{width,height}` to the panel; unknown panelId → "coming soon" placeholder).
  - `PANELS: Record<string, { component: React.FC<PanelProps>; topics: TopicName[] }>` and `type PanelProps = { config: PanelConfig; stores: Stores; scheduler: Scheduler; width: number; height: number }`.
  - `ConnectionStatusPanel` (subscribes to `HealthStore` via `useSyncExternalStore`; renders latency rows + event log).
  - `SmokePainterPanel` (mounts a canvas via ref, registers a Surface that paints the latest quote from `QuoteStore` — proving wire→store→scheduler→canvas with zero React re-render).
  - `AppShell` (dockview host; loads the workspace; renders a `PanelFrame` per panel; auto-saves on layout change).
  - `App` (constructs WsClient + stores + scheduler + link bus for one window from `?workspace=`, wires reconnect overlay, subscribes the union of visible panels' topics).

- [ ] **Step 1: Write the failing tests**

`ui/src/chrome/ErrorBoundary.test.tsx`:
```tsx
// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ErrorBoundary } from "./ErrorBoundary";

function Boom(): JSX.Element { throw new Error("panel exploded"); }

describe("ErrorBoundary", () => {
  it("renders an inline error card when a child throws", () => {
    render(<ErrorBoundary label="Chart"><Boom /></ErrorBoundary>);
    expect(screen.getByText(/Chart/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /reload/i })).toBeTruthy();
  });
});
```

`ui/src/chrome/ConnectionStatusPanel.test.tsx`:
```tsx
// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { HealthStore } from "../data/HealthStore";
import { ConnectionStatusPanel } from "./panels/ConnectionStatusPanel";

describe("ConnectionStatusPanel", () => {
  it("renders latency rows and appends events from the store", () => {
    const health = new HealthStore();
    render(<ConnectionStatusPanel health={health} />);
    act(() => {
      health.apply({ kind: "snapshot", topic: "sys.health",
        payload: { links: [{ link: "engine-moomoo", ms: 12, min: 8, avg: 12, max: 20, status: "ok" }] } });
      health.apply({ kind: "delta", topic: "sys.events",
        payload: { seq: 1, ts: "t1", kind: "boot", detail: "engine started" } });
    });
    expect(screen.getByText(/engine-moomoo/)).toBeTruthy();
    expect(screen.getByText(/12/)).toBeTruthy();
    expect(screen.getByText(/engine started/)).toBeTruthy();
  });
});
```

`ui/src/replay.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { makeStores, routeToStore } from "./data/registry";
import type { SnapshotMsg, DeltaMsg } from "./wire/contract";

// UI twin of the engine's replay(log) == state invariant: feed the recorded
// snapshot + deltas (including the mid-stream reconnect re-snapshot) and assert
// the final store state. A reconnect re-snapshot must rebuild, not double-apply.
const here = dirname(fileURLToPath(import.meta.url));
const fixture = JSON.parse(readFileSync(join(here, "..", "fixtures", "session-basic.json"), "utf8")) as {
  snapshots: Array<{ topic: string; key?: string; payload: unknown }>;
  deltas: Array<{ topic: string; key?: string; payload: unknown }>;
};

describe("store replay invariant", () => {
  it("reaches a deterministic final state from snapshot + deltas", () => {
    const stores = makeStores();
    const asMsg = (kind: "snapshot" | "delta", e: { topic: string; key?: string; payload: unknown }) =>
      ({ kind, topic: e.topic, key: e.key, payload: e.payload } as SnapshotMsg | DeltaMsg);
    for (const s of fixture.snapshots) routeToStore(stores, asMsg("snapshot", s));
    for (const d of fixture.deltas) routeToStore(stores, asMsg("delta", d));
    // last md.quote delta in the fixture is last=3.52
    expect(stores.quote.get("US.AAPL")?.last).toBe(3.52);
    // sys.events accumulated (boot snapshot + reconnect delta)
    expect(stores.health.getSnapshot().events.map((e) => e.kind)).toEqual(["boot", "reconnect"]);
  });

  it("a re-snapshot rebuilds rather than doubling", () => {
    const stores = makeStores();
    routeToStore(stores, { kind: "snapshot", topic: "md.tape", key: "US.AAPL",
      payload: [{ symbol: "US.AAPL", price: 1, size: 1, direction: "BUY", ts: "t1" }] });
    routeToStore(stores, { kind: "delta", topic: "md.tape", key: "US.AAPL",
      payload: [{ symbol: "US.AAPL", price: 2, size: 1, direction: "SELL", ts: "t2" }] });
    routeToStore(stores, { kind: "snapshot", topic: "md.tape", key: "US.AAPL",
      payload: [{ symbol: "US.AAPL", price: 9, size: 1, direction: "BUY", ts: "t9" }] });
    expect(stores.tape.size()).toBe(1);
    expect(stores.tape.latest(1)[0].price).toBe(9);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && npx vitest run src/chrome/ErrorBoundary.test.tsx src/chrome/ConnectionStatusPanel.test.tsx src/replay.test.ts`
Expected: FAIL — component modules not found (`replay.test.ts` may already pass since it only uses Task 11 code; that is acceptable — it's the regression lock).

- [ ] **Step 3: Write the implementations**

`ui/src/chrome/ErrorBoundary.tsx`:
```tsx
import { Component, type ReactNode } from "react";

interface Props { label: string; children: ReactNode }
interface State { error: Error | null }

// Per-panel boundary: one broken panel never takes down the workspace.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };
  static getDerivedStateFromError(error: Error): State { return { error }; }

  render(): ReactNode {
    if (this.state.error) {
      return (
        <div style={{ padding: 12, background: "#2a1416", color: "#f5b5b5", height: "100%", overflow: "auto" }}>
          <strong>{this.props.label} failed</strong>
          <pre style={{ whiteSpace: "pre-wrap", fontSize: 12 }}>{this.state.error.message}</pre>
          <button onClick={() => this.setState({ error: null })}>Reload panel</button>
        </div>
      );
    }
    return this.props.children;
  }
}
```

`ui/src/chrome/ReconnectOverlay.tsx`:
```tsx
import type { ReactNode } from "react";
import type { ConnState } from "../wire/WsClient";

// Honesty policy: while not "open", dim the surfaces and say so — never present
// stale canvases as live.
export function ReconnectOverlay({ state, children }: { state: ConnState; children: ReactNode }): JSX.Element {
  return (
    <div style={{ position: "relative", height: "100%" }}>
      <div style={{ height: "100%", opacity: state === "open" ? 1 : 0.4, transition: "opacity 120ms" }}>
        {children}
      </div>
      {state !== "open" && (
        <div style={{ position: "absolute", inset: 0, display: "grid", placeItems: "center",
          background: "rgba(15,17,21,0.35)", color: "#cbd5e1", pointerEvents: "none" }}>
          {state === "connecting" ? "connecting…" : "reconnecting…"}
        </div>
      )}
    </div>
  );
}
```

`ui/src/chrome/panels/registry.tsx`:
```tsx
import type { FC } from "react";
import type { TopicName } from "../../wire/contract";
import type { PanelConfig } from "../workspace";
import type { Stores } from "../../data/registry";
import type { Scheduler } from "../../render/Scheduler";
import { ConnectionStatusPanel } from "./ConnectionStatusPanel";
import { SmokePainterPanel } from "./SmokePainterPanel";

export interface PanelProps {
  config: PanelConfig;
  stores: Stores;
  scheduler: Scheduler;
  width: number;
  height: number;
}
export interface PanelDef { component: FC<PanelProps>; topics: TopicName[] }

// Plan 1 registers the two panels needed to prove the stack. Plans 2–5 register
// chart / ladder / tape / scanner / movers / news / account-bar / positions /
// open-orders / order-ticket here.
export const PANELS: Record<string, PanelDef> = {
  "connection-status": {
    component: ({ stores }) => <ConnectionStatusPanel health={stores.health} />,
    topics: ["sys.health", "sys.events"],
  },
  "smoke-painter": {
    component: SmokePainterPanel,
    topics: ["md.quote"],
  },
};
```

`ui/src/chrome/panels/ConnectionStatusPanel.tsx`:
```tsx
import { useSyncExternalStore } from "react";
import type { HealthStore } from "../../data/HealthStore";

const dot = (status: string) => (status === "ok" ? "#4ade80" : status === "degraded" ? "#fbbf24" : "#f87171");

export function ConnectionStatusPanel({ health }: { health: HealthStore }): JSX.Element {
  const state = useSyncExternalStore((cb) => health.subscribe(cb), () => health.getSnapshot());
  return (
    <div style={{ padding: 10, fontSize: 12, color: "#cbd5e1", height: "100%", overflow: "auto" }}>
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <tbody>
          {state.links.map((l) => (
            <tr key={l.link}>
              <td><span style={{ color: dot(l.status) }}>●</span> {l.link}</td>
              <td style={{ textAlign: "right" }}>{l.ms === null ? "—" : `${l.ms} ms`}</td>
              <td style={{ textAlign: "right", opacity: 0.6 }}>
                {l.min === null ? "" : `${l.min}/${l.avg}/${l.max}`}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div style={{ marginTop: 10, borderTop: "1px solid #1f2430", paddingTop: 6 }}>
        {state.events.slice(-50).reverse().map((e) => (
          <div key={e.seq} style={{ display: "flex", gap: 8 }}>
            <span style={{ opacity: 0.5 }}>{e.ts}</span>
            <span style={{ opacity: 0.7 }}>{e.kind}</span>
            <span>{e.detail}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
```

`ui/src/chrome/panels/SmokePainterPanel.tsx`:
```tsx
import { useEffect, useRef } from "react";
import type { PanelProps } from "./registry";

// Proves wire → store → scheduler → canvas with zero React re-render: the canvas
// is mounted once; the Surface reads QuoteStore each dirty frame and paints.
export function SmokePainterPanel({ config, stores, scheduler, width, height }: PanelProps): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const symbol = (config.settings.symbol as string) ?? "US.AAPL";

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d")!;
    const off = scheduler.register({
      id: `smoke:${config.id}`,
      isDirty: () => stores.quote.consumeDirty(),
      paint: () => {
        const q = stores.quote.get(symbol);
        ctx.fillStyle = "#0F1115";
        ctx.fillRect(0, 0, canvas.width, canvas.height);
        ctx.fillStyle = "#e2e8f0";
        ctx.font = "14px monospace";
        ctx.fillText(q ? `${symbol}  ${q.last}  (${q.bid}/${q.ask})` : `${symbol}  waiting…`, 10, 24);
      },
    });
    return off;
  }, [config.id, symbol, scheduler, stores]);

  return <canvas ref={canvasRef} width={width} height={height} style={{ display: "block" }} />;
}
```

`ui/src/chrome/PanelFrame.tsx`:
```tsx
import { useEffect, useRef, useState } from "react";
import { ErrorBoundary } from "./ErrorBoundary";
import { PANELS, type PanelProps } from "./panels/registry";
import type { PanelConfig } from "./workspace";
import type { Stores } from "../data/registry";
import type { Scheduler } from "../render/Scheduler";
import type { LinkGroup } from "./linkGroups";

const swatch = (g: LinkGroup) =>
  g === null ? "transparent" : { red: "#ef4444", green: "#22c55e", blue: "#3b82f6", yellow: "#eab308" }[g];

export function PanelFrame(
  { config, stores, scheduler }: { config: PanelConfig; stores: Stores; scheduler: Scheduler },
): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });

  useEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const r = entries[0].contentRect;
      setSize({ width: Math.floor(r.width), height: Math.floor(r.height) });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const def = PANELS[config.panelId];
  const Body = def?.component;
  const props: PanelProps = { config, stores, scheduler, width: size.width, height: size.height };

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "2px 8px",
        background: "#141821", borderBottom: "1px solid #1f2430", fontSize: 12 }}>
        <span style={{ width: 8, height: 8, borderRadius: 2, background: swatch(config.group) as string }} />
        <span>{config.panelId}</span>
      </div>
      <div ref={hostRef} style={{ flex: 1, minHeight: 0 }}>
        <ErrorBoundary label={config.panelId}>
          {Body ? <Body {...props} /> : <div style={{ padding: 12, color: "#64748b" }}>“{config.panelId}” — coming in a later plan</div>}
        </ErrorBoundary>
      </div>
    </div>
  );
}
```

`ui/src/chrome/AppShell.tsx`:
```tsx
import { useEffect, useState } from "react";
import { DockviewReact, type DockviewReadyEvent, type IDockviewPanelProps } from "dockview";
import "dockview/dist/styles/dockview.css";
import { PanelFrame } from "./PanelFrame";
import type { Workspace } from "./workspace";
import { WorkspaceStore } from "./workspace";
import type { Stores } from "../data/registry";
import type { Scheduler } from "../render/Scheduler";

interface Props {
  workspaceName: "monitoring" | "trading";
  stores: Stores;
  scheduler: Scheduler;
  workspaceStore: WorkspaceStore;
}

export function AppShell({ workspaceName, stores, scheduler, workspaceStore }: Props): JSX.Element {
  const [ws, setWs] = useState<Workspace | null>(null);
  useEffect(() => { void workspaceStore.load(workspaceName).then(setWs); }, [workspaceName, workspaceStore]);
  if (!ws) return <div style={{ padding: 12 }}>loading workspace…</div>;

  // Stable React keys: panels are keyed by config.id so dockview drag/resize
  // never remounts them (canvas keeps its context).
  const components = Object.fromEntries(
    ws.panels.map((p) => [
      p.id,
      (_props: IDockviewPanelProps) => <PanelFrame config={p} stores={stores} scheduler={scheduler} />,
    ]),
  );

  const onReady = (event: DockviewReadyEvent) => {
    // Restore a previously saved dockview layout if present; otherwise seed the grid
    // from the panel list (first run — the seed's `layout` is a placeholder string).
    let restored = false;
    const layout = ws.layout as { grid?: unknown } | null;
    try {
      if (layout && typeof layout.grid === "object" && layout.grid !== null) {
        event.api.fromJSON(layout as Parameters<typeof event.api.fromJSON>[0]);
        restored = true;
      }
    } catch {
      restored = false;
    }
    if (!restored) {
      ws.panels.forEach((p, i) => {
        event.api.addPanel({ id: p.id, component: p.id, title: p.panelId,
          position: i === 0 ? undefined : { direction: i % 2 ? "right" : "below" } });
      });
    }
    event.api.onDidLayoutChange(() => {
      workspaceStore.save({ ...ws, layout: event.api.toJSON() });
    });
  };

  return <DockviewReact components={components} onReady={onReady} className="dockview-theme-dark" />;
}
```

`ui/src/App.tsx`:
```tsx
import { useEffect, useMemo, useState } from "react";
import { WsClient, type ConnState, type ISocket } from "./wire/WsClient";
import { browserRaf } from "./render/surface";
import { Scheduler } from "./render/Scheduler";
import { makeStores, connectStores } from "./data/registry";
import { BroadcastChannelBus, LinkGroups } from "./chrome/linkGroups";
import { WorkspaceStore } from "./chrome/workspace";
import { SEED_WORKSPACES } from "./seeds/workspaces";
import { PANELS } from "./chrome/panels/registry";
import { AppShell } from "./chrome/AppShell";
import { ReconnectOverlay } from "./chrome/ReconnectOverlay";
import type { TopicName } from "./wire/contract";

export function App({ workspaceName }: { workspaceName: "monitoring" | "trading" }): JSX.Element {
  const [state, setState] = useState<ConnState>("connecting");

  const { client, stores, scheduler, workspaceStore, linkGroups } = useMemo(() => {
    const client = new WsClient({
      url: `ws://${location.host}/ws`,
      socketFactory: (url) => {
        // The real WebSocket delegates to whatever handlers WsClient assigns to
        // sock.onopen/onmessage/onclose (set just after this returns).
        const ws = new WebSocket(url);
        const sock: ISocket = { send: (d) => ws.send(d), close: () => ws.close(), onopen: null, onmessage: null, onclose: null };
        ws.onopen = () => sock.onopen?.();
        ws.onmessage = (e) => sock.onmessage?.(String(e.data));
        ws.onclose = () => sock.onclose?.();
        return sock;
      },
      now: () => performance.now(),
      setTimeout: (fn, ms) => window.setTimeout(fn, ms),
    });
    const stores = makeStores();
    const scheduler = new Scheduler(browserRaf, (id, err) => console.error("painter crashed", id, err));
    const workspaceStore = new WorkspaceStore(client);
    const linkGroups = new LinkGroups(new BroadcastChannelBus(), (group, symbol) => {
      void client.sendCommand("FocusGroup", { group, symbol });
    });
    return { client, stores, scheduler, workspaceStore, linkGroups };
  }, []);

  useEffect(() => {
    client.onState(setState);
    client.start();
    scheduler.start();
    // Subscribe the union of the seed workspace's panels' topics (Plan 4/5 make
    // this dynamic as panels mount/unmount).
    const topics = new Set<TopicName>();
    for (const p of SEED_WORKSPACES[workspaceName].panels) {
      PANELS[p.panelId]?.topics.forEach((t) => topics.add(t));
    }
    const disposeStores = connectStores(client, stores, [...topics]);
    const ping = window.setInterval(() => client.sendPing(), 2000);
    return () => { window.clearInterval(ping); disposeStores(); scheduler.stop(); client.stop(); };
  }, [client, stores, scheduler, workspaceName]);

  void linkGroups; // wired into panel headers in Plan 2+ (chart/ladder follow groups)

  return (
    <ReconnectOverlay state={state}>
      <AppShell workspaceName={workspaceName} stores={stores} scheduler={scheduler} workspaceStore={workspaceStore} />
    </ReconnectOverlay>
  );
}
```

`ui/src/main.tsx`:
```tsx
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";

const params = new URLSearchParams(location.search);
const workspaceName = params.get("workspace") === "trading" ? "trading" : "monitoring";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App workspaceName={workspaceName} />
  </StrictMode>,
);
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd ui && npx vitest run src/chrome/ErrorBoundary.test.tsx src/chrome/ConnectionStatusPanel.test.tsx src/replay.test.ts`
Expected: PASS — error card renders, health panel shows rows + events, replay invariant holds.

- [ ] **Step 5: Full suite + typecheck + manual smoke**

Run: `cd ui && npm test && npm run typecheck`
Expected: entire suite green, no type errors.

Manual smoke (two terminals):
```bash
# terminal 1
cd ui && npm run mock-engine     # → mock engine listening on ws://127.0.0.1:8686/ws
# terminal 2
cd ui && npm run dev             # → Vite on http://127.0.0.1:5173
```
Open `http://127.0.0.1:5173/?workspace=trading` and `?workspace=monitoring` in two windows. Verify: dockview panels render; Connection Status shows the three link rows (engine-tz down) + the boot/reconnect events; the overlay flips to "reconnecting…" ~300 ms in (fixture `reconnectAtMs`) then clears as the client reconnects and re-snapshots. Confirm no market-data value ever appears in React DevTools component state (smoke painter writes only to canvas).

- [ ] **Step 6: Commit**

```bash
git add ui/src/chrome/ErrorBoundary.tsx ui/src/chrome/ReconnectOverlay.tsx ui/src/chrome/PanelFrame.tsx \
  ui/src/chrome/AppShell.tsx ui/src/App.tsx ui/src/main.tsx \
  ui/src/chrome/panels/registry.tsx ui/src/chrome/panels/ConnectionStatusPanel.tsx \
  ui/src/chrome/panels/SmokePainterPanel.tsx \
  ui/src/chrome/ErrorBoundary.test.tsx ui/src/chrome/ConnectionStatusPanel.test.tsx ui/src/replay.test.ts
git commit -m "feat(ui): assemble shell — dockview, panels, reconnect overlay, Connection Status, replay invariant"
```

---

## Definition of done (Plan 1)

- `cd ui && npm test` is green (unit + jsdom component + mock-engine + replay invariant).
- `cd ui && npm run typecheck` and `npm run lint` are clean.
- With the mock engine running, both `?workspace=monitoring` and `?workspace=trading` windows connect, subscribe, hydrate, host dockview panels that survive drag/resize (canvas not remounted), auto-save layout, and share link-group focus across windows.
- WS drop → "reconnecting" overlay → reconnect re-runs snapshot-then-delta → one clean repaint (verified by the replay test's re-snapshot case and the manual smoke).
- No high-frequency data path touches React state (smoke painter writes only to canvas; only Health/Exec/Scanner/News — all low-rate — use `useSyncExternalStore`).
- Interim `wire/contract.ts` field names match the engine design's `uihub/wsmsg` spec so the future tygo output drops in.

## Self-review notes (author checklist, completed)

- **Spec coverage (foundation slice):** four-layer architecture ✓ (Tasks 2–14), strict dependency direction ✓ (registry/scheduler take structural interfaces, never import up), per-window WS + snapshot-then-delta ✓ (Task 3), plain-TS stores with dirty flags ✓ (Tasks 5–9), one rAF scheduler ✓ (Task 10), dockview shell + stable keys + ResizeObserver + auto-save ✓ (Tasks 13–14), link groups over BroadcastChannel + engine echo ✓ (Task 12), Connection Status panel ✓ (Task 14), error boundary + painter isolation + reconnect overlay ✓ (Tasks 10, 14), store/replay invariant ✓ (Task 14). Chart/ladder/tape/scanner/movers/news/exec panels, indicators, hotkeys, order entry, golden-image + Playwright tests → Plans 2–6 (roadmap above).
- **Placeholder scan:** no TBD/"add error handling"/"similar to Task N" — every step carries complete code.
- **Type consistency:** `apply(m: SnapshotMsg | DeltaMsg)` is uniform across all stores; `Stores`/`PanelProps`/`Workspace`/`PanelConfig`/`ConnState`/`LinkGroup`/`Surface`/`RafLike` names are used identically wherever referenced; `WsClient.subscribe`/`sendCommand`/`onState`/`rttMs`/`sendPing` signatures match their call sites in `App.tsx`, `registry.ts`, and `workspace.ts`.

---

## Execution options

**Plan 1 complete and saved to `docs/superpowers/plans/2026-07-04-ui-foundation-data-plane.md`.** Plans 2–6 are scoped in the roadmap above and can each be written when their turn comes.

Two execution options for this plan:

1. **Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session with batch checkpoints for review.
