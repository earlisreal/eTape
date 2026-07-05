# eTape UI — Plan 4 of 6: Monitoring Surfaces (Scanner · Movers · News)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the Monitoring workspace's three data surfaces — a session-parameterized rank table (Scanner for pre-market, Movers for RTH, one component) and a per-symbol News panel — as low-rate React tables backed by the already-declared `ScannerStore`/`NewsStore` and the already-routed `scanner.rank` / `scanner.hit` / `news.item` topics, all developed and tested against the mock engine and a new recorded fixture.

**Architecture:** Scanner/Movers/News are the **allowed React-state case** (low-rate, event-driven — not tick-rate market data), so they live entirely in `chrome/` (React panels) + `data/` (`ReactStore` subclasses consumed via `useSyncExternalStore`), with pure, DOM-free logic modules (`chrome/format.ts`, `chrome/panels/scannerFilter.ts`) for formatting/filtering/sorting. No canvas, no `render/` painters, no golden images. Scanner and Movers are **one** `ScannerPanel` component parameterized by a `session` prop; the wire distinguishes sessions via the message `key` (`"premarket"` / `"rth"` / `"afterhours"`), exactly as `BarStore` keys by `symbol:timeframe`. New-hit flash + midnight-reset dedup are computed **UI-side** from a per-session seen-set (the engine's `scanner.hit` is honored as an explicit force-flash). Because the Go engine still does not exist, this plan builds against the mock engine and the interim hand-authored `wire/contract.ts`, adding the `ScannerRow`/`ScanHitPayload`/`NewsItem` payload types field-for-field so the future tygo-generated `ui/src/gen/*` is a drop-in.

**Tech Stack:** TypeScript 5, React 18, Vite 5, Vitest (unit + jsdom component tests), `@testing-library/react`, dockview. **No new test tooling** — node-canvas golden images stay confined to the canvas painters (Plan 3), and Playwright still arrives only in Plan 6. **No new palette tokens** — every color comes from Plan 2's `render/palette.ts` via `useTheme()`.

## Global Constraints

Inherited verbatim from Plan 1 (`docs/superpowers/plans/2026-07-04-ui-foundation-data-plane.md` §Global Constraints) — every task implicitly includes these. Restated here are the ones Plan 4 touches most, plus Plan-4-specific additions.

- **Hard rule (and its Plan-4 exception):** high-frequency data (chart/ladder/tape/book/quote) never flows through React state. Scanner/Movers/News are the **explicitly-allowed low-rate exception** — declared `ReactStore`-based in Plan 1 alongside Health/Exec — because a ~2 s rank refresh and an event-driven news feed are low enough for React. They render as React tables via `useSyncExternalStore`; they register **nothing** with the rAF scheduler and touch **no** `PaintStore`/`getRev()`/`consumeDirty()` machinery.
- **Dependency direction:** `chrome → render → data → wire`, never backwards. Panels (`chrome/`) may import pure formatters from `render/format.ts` (`chrome → render` is legal); `data/` stores import only from `wire/contract.ts`.
- **Honesty policy:** never render stale as live; never render in-flight as done. A symbol with no pre-market print yet has `changePct: null` / `last: null` and renders `"—"` — **never a fabricated `0%` or `$0`**. An unknown float renders `"—"`, never `0`.
- **Wire format:** WebSocket + JSON; full snapshot on subscribe, then deltas. A `scanner.rank` **snapshot** is a baseline ranking (initial subscribe or reconnect — seeds the seen-set, never flashes); a `scanner.rank` **delta** is a full-replace refresh (diffed against the seen-set to flash newcomers). The UI requests logical topics and never reasons about moomoo quota.
- **Type source of truth:** `wire/contract.ts` is the interim hand-authored contract (tygo target `ui/src/gen/*`). New payload types added this plan (`ScannerRow`, `ScannerRankPayload`, `ScanHitPayload`, `NewsItem`) keep field names identical to the specs so regeneration is a drop-in.
- **Link bus:** `BroadcastChannel("etape.link")`, payload `{group, symbol}`, focus events only. Panels read `linkGroups.symbolFor(group)` / `linkGroups.subscribe(cb)` and publish via `linkGroups.focus(group, symbol)`.
- **Panel isolation:** each panel is wrapped by `PanelFrame`'s per-panel `ErrorBoundary`; a thrown render → inline error card, rest of the workspace keeps running.
- **Reconnect contract:** on reconnect every subscribed topic re-runs snapshot-then-delta and stores rebuild deterministically (a re-snapshot rebuilds, never doubles). The Scanner seen-set treats a re-snapshot as a fresh baseline (no flash storm); the News store rebuilds its dedup set from the snapshot.

### Design decisions (Plan 4)

These resolve ambiguities between the UI spec (§Panels) and the current seed; record them here so implementers don't re-litigate:

1. **One component, two panels.** `movers` is `ScannerPanel` with `session="rth"`; `scanner` is `ScannerPanel` with `session="premarket"`. Both register in `PANELS` with topics `["scanner.rank", "scanner.hit"]`. The session travels on the message `key`; `ScannerStore` partitions rows into independent per-session buckets (each with its own seen-set).
2. **Display-pinned, click-publishing.** Scanner/Movers always show the **whole ranked universe** — they never filter by link focus, so their `config.group` stays `null` (pinned, does not *consume* focus). Their row-clicks *publish* to a configurable `settings.targetGroup` (default `"green"`, matching the seed News panel's group and one seed chart's group), set via a small swatch selector in the panel header. This keeps `config.group` semantics ("the symbol I follow") clean and unoverloaded, and satisfies the spec's "row click focuses its link group."
3. **UI-authoritative flash + dedup.** New-hit detection and the ET-midnight dedup reset are UI state (a per-session `Set<symbol>` in `ScannerStore`), because "dedup resets at ET midnight" is inherently a client concern. `scanner.hit` from the engine is honored as an *additional* explicit force-flash for a symbol already in the current ranking.
4. **Thresholds are per-panel settings.** `min-%-change` / `float cap` (nullable = off) / `volume floor` live in `config.settings.thresholds`, persisted through the existing `onConfigChange` → `WorkspaceStore` path (the `TapePanel.minSize` precedent). Filtering is applied at view time by a pure helper, so Scanner and Movers can carry different thresholds.

---

## File Structure (Plan 4)

```
ui/
  src/
    wire/
      contract.ts                 MODIFY — add ScannerSession/ScannerRow/ScannerRankPayload/ScanHitPayload/NewsItem
    data/
      ScannerStore.ts             MODIFY — session buckets, new-hit flash, midnight-reset dedup, view()/resetSeen()
      NewsStore.ts                MODIFY — typed NewsItem, url dedup, itemsFor(symbol) newest-first
    chrome/
      format.ts                   NEW    — formatChangePct / formatCompactShares / msUntilEtMidnight (pure, DOM-free)
      format.test.ts              NEW
      panels/
        scannerFilter.ts          NEW    — ScannerThresholds + applyScannerFilters + sortByChangeDesc (pure)
        scannerFilter.test.ts     NEW
        ScannerPanel.tsx          NEW    — session-aware rank table (Scanner + Movers)
        ScannerPanel.test.tsx     NEW    (jsdom)
        NewsPanel.tsx             NEW    — per-symbol news list, follows link-group focus
        NewsPanel.test.tsx        NEW    (jsdom)
        registry.tsx              MODIFY — register scanner / movers / news; update the ledger comment
        registry.test.tsx         NEW    — assert the three entries + topics + seed wiring
    data/ScannerStore.test.ts     NEW
    data/NewsStore.test.ts        NEW
    seeds/workspaces.ts           MODIFY — scanner/movers settings.targetGroup + thresholds (group stays null)
    monitoringReplay.test.ts      NEW    — replay-invariant test over the monitoring fixture
  mock-engine/
    run.ts                        MODIFY — document the `monitoring` fixture in the selection comment
  fixtures/
    monitoring.json               NEW    — scanner.rank (premarket + rth) + scanner.hit + news.item, snapshot + deltas
    monitoring.test.ts            NEW    — fixture self-check (payloads conform to the contract)
```

`data/registry.ts` needs **no change** — `makeStores()` already constructs `ScannerStore`/`NewsStore`, and `routeToStore()` already routes `scanner.rank`/`scanner.hit` → `stores.scanner.apply` and `news.item` → `stores.news.apply`. `App.tsx` needs **no change** — its static topic union already picks up `scanner.rank`/`scanner.hit`/`news.item` because scanner/movers/news are in the seed monitoring workspace and their new `PANELS` entries declare those topics.

---

## Task 1: Monitoring table formatters (pure)

**Files:**
- Create: `ui/src/chrome/format.ts`
- Test: `ui/src/chrome/format.test.ts`

**Interfaces:**
- Consumes: nothing (pure, zero imports).
- Produces: `formatChangePct(pct: number | null): string`, `formatCompactShares(n: number | null): string`, `msUntilEtMidnight(now: Date): number` — consumed by `ScannerPanel` (Task 5). `formatChangePct`/`formatCompactShares` also usable by later plans' tables.

- [ ] **Step 1: Write the failing test**

`ui/src/chrome/format.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { formatChangePct, formatCompactShares, msUntilEtMidnight } from "./format";

describe("formatChangePct — 3-digit-safe, never fabricates 0%", () => {
  it("signs and rounds to one decimal", () => {
    expect(formatChangePct(4.23)).toBe("+4.2%");
    expect(formatChangePct(-12.35)).toBe("−12.3%"); // U+2212 minus
    expect(formatChangePct(234.5)).toBe("+234.5%"); // 3-digit
  });
  it("renders null (no print yet) as em dash, not 0%", () => {
    expect(formatChangePct(null)).toBe("—");
    expect(formatChangePct(NaN)).toBe("—");
  });
  it("zero is unsigned", () => {
    expect(formatChangePct(0)).toBe("0.0%");
  });
});

describe("formatCompactShares", () => {
  it("compacts K/M/B", () => {
    expect(formatCompactShares(2_100_000)).toBe("2.1M");
    expect(formatCompactShares(950_000)).toBe("950K");
    expect(formatCompactShares(3_200_000_000)).toBe("3.2B");
    expect(formatCompactShares(640)).toBe("640");
  });
  it("null (unknown) is em dash, but 0 is a real 0", () => {
    expect(formatCompactShares(null)).toBe("—");
    expect(formatCompactShares(0)).toBe("0");
  });
});

describe("msUntilEtMidnight (deterministic in EDT/July, UTC−4)", () => {
  it("09:30 ET → 14.5h remaining", () => {
    // 2026-07-06T13:30:00Z == 09:30:00 America/New_York (EDT)
    expect(msUntilEtMidnight(new Date("2026-07-06T13:30:00Z"))).toBe(52_200_000);
  });
  it("23:59:59 ET → 1s remaining", () => {
    // 2026-07-06T03:59:59Z == 2026-07-05 23:59:59 America/New_York
    expect(msUntilEtMidnight(new Date("2026-07-06T03:59:59Z"))).toBe(1000);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/format.test.ts`
Expected: FAIL — `Cannot find module './format'`.

- [ ] **Step 3: Write the implementation**

`ui/src/chrome/format.ts`:
```ts
// Chrome-layer formatters for the monitoring tables (scanner/movers/news).
// The canvas surfaces format via render/format.ts; these are the React-table
// equivalents: 3-digit-safe %, compact float/volume, and ET-midnight math for
// the scanner's dedup reset. Pure and DOM-free.

/** Signed, one-decimal, 3-digit-safe percent: 4.23 → "+4.2%", -12.35 → "−12.3%",
 *  234.5 → "+234.5%". null / NaN (no print yet) → "—" (never a fabricated 0%). */
export function formatChangePct(pct: number | null): string {
  if (pct === null || Number.isNaN(pct)) return "—";
  const sign = pct > 0 ? "+" : pct < 0 ? "−" : ""; // U+2212 for negatives
  return `${sign}${Math.abs(pct).toFixed(1)}%`;
}

/** Compact share/volume count: 2_100_000 → "2.1M", 950_000 → "950K",
 *  3_200_000_000 → "3.2B", 640 → "640". null (unknown) → "—"; 0 → "0". */
export function formatCompactShares(n: number | null): string {
  if (n === null || Number.isNaN(n)) return "—";
  const abs = Math.abs(n);
  if (abs >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
  if (abs >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (abs >= 1e3) return `${(n / 1e3).toFixed(0)}K`;
  return `${Math.round(n)}`;
}

/** Milliseconds from `now` until the next 00:00 America/New_York — the dedup
 *  reset boundary. Uses Intl so it tracks EST/EDT automatically. */
export function msUntilEtMidnight(now: Date): number {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: "America/New_York", hour12: false,
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  }).formatToParts(now);
  const get = (t: string) => Number(parts.find((p) => p.type === t)?.value);
  let h = get("hour");
  if (h === 24) h = 0; // Intl can emit "24" at midnight
  const sinceMidnightMs = ((h * 60 + get("minute")) * 60 + get("second")) * 1000 + now.getMilliseconds();
  const dayMs = 24 * 60 * 60 * 1000;
  const rem = dayMs - sinceMidnightMs;
  return rem <= 0 ? dayMs : rem;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/format.test.ts`
Expected: PASS — 3 describe blocks green, including both ET-midnight fixed-date assertions.

- [ ] **Step 5: Commit**

```bash
git add ui/src/chrome/format.ts ui/src/chrome/format.test.ts
git commit -m "feat(ui/chrome): monitoring table formatters — 3-digit % , compact shares, ET-midnight"
```

---

## Task 2: Scanner wire types + threshold filter/sort (pure)

**Files:**
- Modify: `ui/src/wire/contract.ts` (add the scanner payload types)
- Create: `ui/src/chrome/panels/scannerFilter.ts`
- Test: `ui/src/chrome/panels/scannerFilter.test.ts`

**Interfaces:**
- Consumes: `ScannerRow` from `wire/contract.ts` (added in this task).
- Produces:
  - Contract types: `ScannerSession = "premarket" | "rth" | "afterhours"`, `ScannerRow { symbol; changePct: number|null; last: number|null; floatShares: number|null; volume: number }`, `ScannerRankPayload { refreshedAt: string; rows: ScannerRow[] }`, `ScanHitPayload { symbol: string; at: string }` — consumed by `ScannerStore` (Task 3), `ScannerPanel` (Task 5), the fixture (Task 8).
  - `ScannerThresholds { minChangePct: number; floatCapShares: number|null; minVolume: number }`, `applyScannerFilters<T extends ScannerRow>(rows: T[], t: ScannerThresholds): T[]`, `sortByChangeDesc<T extends ScannerRow>(rows: T[]): T[]` — consumed by `ScannerPanel` (Task 5).

- [ ] **Step 1: Write the failing test**

`ui/src/chrome/panels/scannerFilter.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import type { ScannerRow } from "../../wire/contract";
import { applyScannerFilters, sortByChangeDesc, type ScannerThresholds } from "./scannerFilter";

const row = (symbol: string, changePct: number | null, floatShares: number | null, volume: number): ScannerRow =>
  ({ symbol, changePct, last: 1, floatShares, volume });

const OFF: ScannerThresholds = { minChangePct: 0, floatCapShares: null, minVolume: 0 };

describe("applyScannerFilters", () => {
  const rows: ScannerRow[] = [
    row("A", 12, 5_000_000, 800_000),
    row("B", 3, 200_000_000, 50_000),
    row("C", null, 5_000_000, 0),      // no print yet
    row("D", -8, 5_000_000, 900_000),
  ];

  it("passes everything when thresholds are off", () => {
    expect(applyScannerFilters(rows, OFF).map((r) => r.symbol)).toEqual(["A", "B", "C", "D"]);
  });
  it("min %-change filters by magnitude and drops no-print rows", () => {
    expect(applyScannerFilters(rows, { ...OFF, minChangePct: 5 }).map((r) => r.symbol)).toEqual(["A", "D"]);
  });
  it("float cap excludes above the cap but keeps unknown-float rows", () => {
    const withNullFloat = [...rows, row("E", 20, null, 100_000)];
    expect(applyScannerFilters(withNullFloat, { ...OFF, floatCapShares: 10_000_000 }).map((r) => r.symbol))
      .toEqual(["A", "C", "D", "E"]); // B (200M) dropped; E (null float) kept
  });
  it("volume floor excludes below the floor", () => {
    expect(applyScannerFilters(rows, { ...OFF, minVolume: 100_000 }).map((r) => r.symbol)).toEqual(["A", "D"]);
  });
});

describe("sortByChangeDesc", () => {
  it("highest change first, no-print rows last, without mutating input", () => {
    const input = [row("A", 3, 1, 1), row("B", null, 1, 1), row("C", 42, 1, 1)];
    const out = sortByChangeDesc(input);
    expect(out.map((r) => r.symbol)).toEqual(["C", "A", "B"]);
    expect(input.map((r) => r.symbol)).toEqual(["A", "B", "C"]); // input untouched
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/scannerFilter.test.ts`
Expected: FAIL — `Cannot find module './scannerFilter'` (and the `ScannerRow` type import errors until contract.ts is updated).

- [ ] **Step 3: Write the implementation**

Add to `ui/src/wire/contract.ts`, in the `// ---- payloads` section (after the `SysEvent` line, before `// ---- server → client ----`):
```ts
// ---- scanner (Plan 4) ----
// Session travels on the message `key` ("premarket" | "rth" | "afterhours").
export type ScannerSession = "premarket" | "rth" | "afterhours";
export interface ScannerRow {
  symbol: string;
  changePct: number | null;   // % change; null = no print yet (never a fabricated 0)
  last: number | null;        // last trade price; null = no print yet
  floatShares: number | null; // true free float in ACTUAL shares (engine already
                              // converts moomoo's thousands unit); null = unknown
  volume: number;             // session cumulative volume (0 is legitimate)
}
export interface ScannerRankPayload { refreshedAt: string; rows: ScannerRow[] } // one full ranking
export interface ScanHitPayload { symbol: string; at: string }                  // explicit new-qualifier event

// ---- news (Plan 4) ----
export interface NewsItem { symbol: string; headline: string; source: string; url: string; seen_at: string }
```

> `NewsItem` is added here in Task 2 (one contract edit) even though `NewsStore` consumes it in Task 4 — the contract file is the single source of truth and this avoids a second edit to the same region.

`ui/src/chrome/panels/scannerFilter.ts`:
```ts
import type { ScannerRow } from "../../wire/contract";

export interface ScannerThresholds {
  minChangePct: number;          // magnitude floor on % change (0 = off)
  floatCapShares: number | null; // max float in shares (null = off)
  minVolume: number;             // min session volume (0 = off)
}

/** Client-side filter atop the engine's coarse server filters. A row with no
 *  print yet (null changePct) fails any positive min-%-change floor. A row with
 *  unknown float (null) is never excluded by the float cap. */
export function applyScannerFilters<T extends ScannerRow>(rows: T[], t: ScannerThresholds): T[] {
  return rows.filter((r) => {
    if (r.volume < t.minVolume) return false;
    if (t.floatCapShares !== null && r.floatShares !== null && r.floatShares > t.floatCapShares) return false;
    if (t.minChangePct > 0 && (r.changePct === null || Math.abs(r.changePct) < t.minChangePct)) return false;
    return true;
  });
}

/** Highest % change first; no-print rows (null) sort last. Pure (copies input). */
export function sortByChangeDesc<T extends ScannerRow>(rows: T[]): T[] {
  return [...rows].sort((a, b) => (b.changePct ?? -Infinity) - (a.changePct ?? -Infinity));
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/panels/scannerFilter.test.ts`
Expected: PASS — both describe blocks green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/wire/contract.ts ui/src/chrome/panels/scannerFilter.ts ui/src/chrome/panels/scannerFilter.test.ts
git commit -m "feat(ui/wire): scanner rank/hit + news payload types; scanner threshold filter/sort"
```

---

## Task 3: ScannerStore — session buckets, new-hit flash, midnight-reset dedup

**Files:**
- Modify: `ui/src/data/ScannerStore.ts` (replace the Plan-1 placeholder)
- Modify: `ui/src/data/reactStores.test.ts` (drop the obsolete Scanner/News placeholder block — it asserts the old untyped shape)
- Test: `ui/src/data/ScannerStore.test.ts`

**Interfaces:**
- Consumes: `ReactStore` from `data/store.ts`; `ScannerRow`, `ScannerRankPayload`, `ScanHitPayload`, `ScannerSession` from `wire/contract.ts` (Task 2); `SnapshotMsg`/`DeltaMsg` from `wire/contract.ts`.
- Produces: `ScannerRowView extends ScannerRow { isNewHit: boolean; muted: boolean }`, `ScannerSessionView { rows: ScannerRowView[]; refreshedAt: string | null }`, and `class ScannerStore extends ReactStore<…>` with `apply(m)`, `view(session): ScannerSessionView`, `resetSeen(session?)`. Consumed by `ScannerPanel` (Task 5) and the replay test (Task 9). `data/registry.ts` already constructs `new ScannerStore()` and routes to it — unchanged.

- [ ] **Step 1: Write the failing test**

`ui/src/data/ScannerStore.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { ScannerStore } from "./ScannerStore";
import type { ScannerRankPayload, ScanHitPayload, SnapshotMsg, DeltaMsg } from "../wire/contract";

const rank = (kind: "snapshot" | "delta", session: string, payload: ScannerRankPayload) =>
  ({ kind, topic: "scanner.rank", key: session, payload } as SnapshotMsg | DeltaMsg);
const hit = (session: string, payload: ScanHitPayload) =>
  ({ kind: "delta", topic: "scanner.hit", key: session, payload } as DeltaMsg);
const r = (symbol: string, changePct: number) =>
  ({ symbol, changePct, last: 1, floatShares: 1_000_000, volume: 1000 });

describe("ScannerStore", () => {
  it("snapshot seeds the baseline without flashing", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5), r("B", 3)] }));
    const v = s.view("premarket");
    expect(v.refreshedAt).toBe("t0");
    expect(v.rows.every((row) => !row.isNewHit && !row.muted)).toBe(true);
  });

  it("delta flashes newcomers and mutes carried-over rows", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [r("A", 6), r("B", 9)] }));
    const byId = Object.fromEntries(s.view("premarket").rows.map((row) => [row.symbol, row]));
    expect(byId.B.isNewHit).toBe(true);
    expect(byId.A.isNewHit).toBe(false);
    expect(byId.A.muted).toBe(true);
  });

  it("a second delta no longer flashes a now-seen symbol", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [r("A", 6), r("B", 9)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t2", rows: [r("A", 6), r("B", 9)] }));
    expect(s.view("premarket").rows.find((row) => row.symbol === "B")?.isNewHit).toBe(false);
  });

  it("scanner.hit force-flashes a symbol present in the current ranking", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(hit("premarket", { symbol: "A", at: "t0.5" }));
    expect(s.view("premarket").rows.find((row) => row.symbol === "A")?.isNewHit).toBe(true);
  });

  it("resetSeen re-flashes everything on the next delta (ET-midnight behavior)", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [r("A", 6)] })); // A now seen → muted
    s.resetSeen("premarket");
    s.apply(rank("delta", "premarket", { refreshedAt: "t2", rows: [r("A", 6)] }));
    expect(s.view("premarket").rows[0].isNewHit).toBe(true);
  });

  it("sessions are isolated", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "rth", { refreshedAt: "t1", rows: [r("A", 2)] })); // A never seen in rth
    expect(s.view("rth").rows[0].isNewHit).toBe(true);
    expect(s.view("premarket").rows[0].isNewHit).toBe(false);
  });

  it("a reconnect re-snapshot is a clean baseline (no flash, no stale mute)", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [r("A", 6)] })); // A seen → muted
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t2", rows: [r("A", 6), r("B", 3)] })); // reconnect
    expect(s.view("premarket").rows.every((row) => !row.isNewHit && !row.muted)).toBe(true);
  });

  it("view of an unknown session is empty", () => {
    expect(new ScannerStore().view("afterhours")).toEqual({ rows: [], refreshedAt: null });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/ScannerStore.test.ts`
Expected: FAIL — the placeholder store has no `view`/`resetSeen` and applies untyped rows (`TypeError` / assertion failures).

- [ ] **Step 3: Write the implementation**

Replace `ui/src/data/ScannerStore.ts` entirely:
```ts
import { ReactStore } from "./store";
import type {
  SnapshotMsg, DeltaMsg, ScannerRow, ScannerRankPayload, ScanHitPayload, ScannerSession,
} from "../wire/contract";

export interface ScannerRowView extends ScannerRow { isNewHit: boolean; muted: boolean }
export interface ScannerSessionView { rows: ScannerRowView[]; refreshedAt: string | null }
interface ScannerState { sessions: Partial<Record<ScannerSession, ScannerSessionView>> }

// Session-parameterized rank store. Rows arrive per session on the message `key`.
// New-hit flash + midnight-reset dedup are UI-authoritative: a per-session
// seen-set drives isNewHit/muted. A snapshot is a baseline (seed the seen-set,
// no flash); a delta is a refresh (flash symbols not yet seen). scanner.hit is an
// explicit force-flash for a symbol already in the current ranking.
export class ScannerStore extends ReactStore<ScannerState> {
  private readonly seen = new Map<ScannerSession, Set<string>>();
  constructor() { super({ sessions: {} }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const session = (m.key ?? "premarket") as ScannerSession;
    if (m.topic === "scanner.hit") { this.applyHit(session, m.payload as ScanHitPayload); return; }
    const { refreshedAt, rows } = m.payload as ScannerRankPayload;
    const seen = this.seenFor(session);
    if (m.kind === "snapshot") seen.clear(); // a (re)snapshot is a fresh baseline: no flash, no stale mute
    const view: ScannerRowView[] = rows.map((row) => {
      const isNewHit = m.kind === "delta" && !seen.has(row.symbol);
      const muted = m.kind === "delta" && seen.has(row.symbol);
      return { ...row, isNewHit, muted };
    });
    for (const row of rows) seen.add(row.symbol);
    this.setSession(session, { rows: view, refreshedAt });
  }

  view(session: ScannerSession): ScannerSessionView {
    return this.getSnapshot().sessions[session] ?? { rows: [], refreshedAt: null };
  }

  resetSeen(session?: ScannerSession): void {
    if (session) this.seenFor(session).clear();
    else this.seen.clear();
  }

  private applyHit(session: ScannerSession, hit: ScanHitPayload): void {
    this.seenFor(session).add(hit.symbol);
    const cur = this.getSnapshot().sessions[session];
    if (!cur) return;
    const rows = cur.rows.map((row) =>
      row.symbol === hit.symbol ? { ...row, isNewHit: true, muted: false } : row);
    this.setSession(session, { rows, refreshedAt: cur.refreshedAt });
  }

  private seenFor(session: ScannerSession): Set<string> {
    let s = this.seen.get(session);
    if (!s) { s = new Set(); this.seen.set(session, s); }
    return s;
  }

  private setSession(session: ScannerSession, view: ScannerSessionView): void {
    this.set({ sessions: { ...this.getSnapshot().sessions, [session]: view } });
  }
}
```

> Note the `muted`/`isNewHit` logic: a **snapshot** first clears the session's `seen` set and computes both flags as `false` (`m.kind === "delta"` is false) — the whole baseline renders normally, including on a reconnect re-snapshot (the seen-set is rebuilt from the snapshot's own rows, so no stale mute and no flash storm). On a **delta**, a symbol not yet seen is `isNewHit: true`, a symbol seen before this refresh is `muted: true` (dimmed). `getSnapshot()` returns a stable reference between `set()` calls, so it drives `useSyncExternalStore` correctly.

Also retire the obsolete placeholder coverage in `ui/src/data/reactStores.test.ts` — its `describe("ScannerStore / NewsStore", …)` block asserts the old untyped `{ rows }`/`{ items }` shape and will fail to compile against the new state. Replace the whole file with just the still-valid `ExecStore` block (this drops the now-unused `ScannerStore`/`NewsStore` imports, which `noUnusedLocals` would otherwise flag); `ScannerStore.test.ts` (this task) and `NewsStore.test.ts` (Task 4) provide the real coverage:

```ts
import { describe, it, expect } from "vitest";
import { ExecStore } from "./ExecStore";

describe("ExecStore", () => {
  it("replaces account on snapshot", () => {
    const s = new ExecStore();
    s.apply({ kind: "snapshot", topic: "exec.account", payload: { equity: 1000, armed: false } });
    expect(s.getSnapshot().account).toMatchObject({ equity: 1000, armed: false });
  });
});
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/data/ScannerStore.test.ts src/data/reactStores.test.ts`
Expected: PASS — all 8 `ScannerStore` cases green, and `reactStores.test.ts` still green (now `ExecStore`-only).

- [ ] **Step 5: Commit**

```bash
git add ui/src/data/ScannerStore.ts ui/src/data/ScannerStore.test.ts ui/src/data/reactStores.test.ts
git commit -m "feat(ui/data): ScannerStore — session buckets, new-hit flash, midnight-reset dedup"
```

---

## Task 4: NewsStore — typed NewsItem, url dedup, per-symbol view

**Files:**
- Modify: `ui/src/data/NewsStore.ts` (replace the Plan-1 placeholder)
- Test: `ui/src/data/NewsStore.test.ts`

**Interfaces:**
- Consumes: `ReactStore` from `data/store.ts`; `NewsItem`, `SnapshotMsg`/`DeltaMsg` from `wire/contract.ts` (Task 2).
- Produces: `class NewsStore extends ReactStore<{ items: NewsItem[] }>` with `apply(m)`, `itemsFor(symbol): NewsItem[]` (newest first, by `seen_at`). Consumed by `NewsPanel` (Task 7) and the replay test (Task 9). `data/registry.ts` already constructs `new NewsStore()` and routes to it — unchanged.

- [ ] **Step 1: Write the failing test**

`ui/src/data/NewsStore.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { NewsStore } from "./NewsStore";
import type { NewsItem, SnapshotMsg, DeltaMsg } from "../wire/contract";

const item = (symbol: string, url: string, seen_at: string, headline = "h"): NewsItem =>
  ({ symbol, headline, source: "src", url, seen_at });
const snap = (payload: NewsItem[]) => ({ kind: "snapshot", topic: "news.item", payload } as SnapshotMsg);
const delta = (payload: NewsItem | NewsItem[]) => ({ kind: "delta", topic: "news.item", payload } as DeltaMsg);

describe("NewsStore", () => {
  it("snapshot replaces and dedupes by url", () => {
    const s = new NewsStore();
    s.apply(snap([item("US.AAPL", "u1", "t2"), item("US.AAPL", "u1", "t2")])); // dup url
    expect(s.itemsFor("US.AAPL")).toHaveLength(1);
  });

  it("delta appends a single item or an array, skipping already-seen urls", () => {
    const s = new NewsStore();
    s.apply(snap([item("US.AAPL", "u1", "t1")]));
    s.apply(delta(item("US.AAPL", "u2", "t2")));
    s.apply(delta([item("US.AAPL", "u2", "t2"), item("US.AAPL", "u3", "t3")])); // u2 dup
    expect(s.itemsFor("US.AAPL").map((i) => i.url)).toEqual(["u3", "u2", "u1"]); // newest seen_at first
  });

  it("itemsFor filters by symbol", () => {
    const s = new NewsStore();
    s.apply(snap([item("US.AAPL", "u1", "t1"), item("US.NVDA", "n1", "t2")]));
    expect(s.itemsFor("US.NVDA").map((i) => i.url)).toEqual(["n1"]);
    expect(s.itemsFor("US.TSLA")).toEqual([]);
  });

  it("a reconnect snapshot rebuilds rather than doubling", () => {
    const s = new NewsStore();
    s.apply(snap([item("US.AAPL", "u1", "t1")]));
    s.apply(delta(item("US.AAPL", "u2", "t2")));
    s.apply(snap([item("US.AAPL", "u1", "t1")])); // reconnect re-snapshot
    expect(s.itemsFor("US.AAPL").map((i) => i.url)).toEqual(["u1"]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/NewsStore.test.ts`
Expected: FAIL — the placeholder has no `itemsFor` and does not dedupe.

- [ ] **Step 3: Write the implementation**

Replace `ui/src/data/NewsStore.ts` entirely:
```ts
import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, NewsItem } from "../wire/contract";

interface NewsState { items: NewsItem[] }

// Broker-agnostic news feed. Snapshot replaces (and rebuilds the dedup set);
// delta appends one item or an array. Dedup by url (fallback: symbol|headline|
// seen_at). itemsFor(symbol) returns that symbol's items newest-first by seen_at.
export class NewsStore extends ReactStore<NewsState> {
  private readonly seenKeys = new Set<string>();
  constructor(private readonly cap = 500) { super({ items: [] }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.kind === "snapshot") {
      this.seenKeys.clear();
      this.set({ items: this.dedupe(this.asArray(m.payload)).slice(-this.cap) });
      return;
    }
    const fresh = this.dedupe(this.asArray(m.payload));
    if (fresh.length === 0) return;
    this.set({ items: [...this.getSnapshot().items, ...fresh].slice(-this.cap) });
  }

  itemsFor(symbol: string): NewsItem[] {
    return this.getSnapshot().items
      .filter((it) => it.symbol === symbol)
      .sort((a, b) => b.seen_at.localeCompare(a.seen_at)); // ISO strings sort chronologically
  }

  private asArray(p: unknown): NewsItem[] { return Array.isArray(p) ? (p as NewsItem[]) : [p as NewsItem]; }
  private keyOf(it: NewsItem): string { return it.url || `${it.symbol}|${it.headline}|${it.seen_at}`; }
  private dedupe(items: NewsItem[]): NewsItem[] {
    const out: NewsItem[] = [];
    for (const it of items) {
      const k = this.keyOf(it);
      if (this.seenKeys.has(k)) continue;
      this.seenKeys.add(k);
      out.push(it);
    }
    return out;
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/data/NewsStore.test.ts`
Expected: PASS — all 4 cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/data/NewsStore.ts ui/src/data/NewsStore.test.ts
git commit -m "feat(ui/data): NewsStore — typed NewsItem, url dedup, per-symbol newest-first view"
```

---

## Task 5: ScannerPanel — session-aware rank table (Scanner + Movers)

**Files:**
- Create: `ui/src/chrome/panels/ScannerPanel.tsx`
- Test: `ui/src/chrome/panels/ScannerPanel.test.tsx` (jsdom)

**Interfaces:**
- Consumes: `PanelProps` from `panels/registry.tsx`; `ScannerSession` from `wire/contract.ts` (Task 2); `LinkGroup` from `chrome/linkGroups.ts`; `useTheme` from `chrome/ThemeProvider.tsx`; `formatTapeTime` from `render/format.ts`; `formatChangePct`/`formatCompactShares`/`msUntilEtMidnight` from `chrome/format.ts` (Task 1); `applyScannerFilters`/`sortByChangeDesc`/`ScannerThresholds` from `panels/scannerFilter.ts` (Task 2).
- Produces: `ScannerPanel(props: PanelProps & { session: ScannerSession }): JSX.Element`. Consumed by `registry.tsx` (Task 6) via `session`-injecting wrappers for both `scanner` and `movers`.

- [ ] **Step 1: Write the failing test**

`ui/src/chrome/panels/ScannerPanel.test.tsx`:
```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { LinkGroups } from "../linkGroups";
import { makeStores } from "../../data/registry";
import { ScannerPanel } from "./ScannerPanel";
import type { PanelProps } from "./registry";
import type { PanelConfig } from "../workspace";

function fakeBus() {
  const subs = new Set<(m: unknown) => void>();
  return { post: (m: unknown) => subs.forEach((cb) => cb(m)), onMessage: (cb: (m: unknown) => void) => { subs.add(cb); return () => subs.delete(cb); }, close: () => {} };
}

function renderPanel(over: Partial<PanelConfig> = {}) {
  const stores = makeStores();
  const scanner = stores.scanner;
  const focus = vi.fn();
  const linkGroups = new LinkGroups(fakeBus() as never, () => {});
  vi.spyOn(linkGroups, "focus").mockImplementation(focus);
  const onConfigChange = vi.fn();
  const config: PanelConfig = { id: "m-scanner", panelId: "scanner", group: null,
    settings: { targetGroup: "green" }, ...over };
  const props = { config, stores, linkGroups, onConfigChange, scheduler: {} as never,
    width: 400, height: 300, commands: { sendCommand: async () => ({ status: "accepted" }) } } as PanelProps & { session: "premarket" };
  render(<ThemeProvider><ScannerPanel {...props} session="premarket" /></ThemeProvider>);
  return { scanner, focus, onConfigChange };
}

describe("ScannerPanel", () => {
  it("waits before data, then renders ranked rows", () => {
    const { scanner } = renderPanel();
    expect(screen.getByText(/waiting/i)).toBeTruthy();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-06T13:30:00Z", rows: [
        { symbol: "US.KO", changePct: 18.4, last: 62.1, floatShares: 4_300_000_000, volume: 1_250_000 },
        { symbol: "US.WXYZ", changePct: null, last: null, floatShares: 21_000_000, volume: 0 },
      ] } }));
    expect(screen.getByText("US.KO")).toBeTruthy();
    expect(screen.getByText("+18.4%")).toBeTruthy();
  });

  it("renders no-print rows as em dash, never 0", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "t", rows: [{ symbol: "US.WXYZ", changePct: null, last: null, floatShares: null, volume: 0 }] } }));
    const rowCells = screen.getByText("US.WXYZ").closest("tr")!.querySelectorAll("td");
    expect([...rowCells].map((c) => c.textContent)).toContain("—");
    expect([...rowCells].some((c) => c.textContent === "0%")).toBe(false);
  });

  it("applies the min-%-change threshold", () => {
    const { scanner } = renderPanel({ settings: { targetGroup: "green", thresholds: { minChangePct: 10, floatCapShares: null, minVolume: 0 } } });
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "t", rows: [
        { symbol: "US.KO", changePct: 18.4, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.LOW", changePct: 2, last: 1, floatShares: 1, volume: 1 },
      ] } }));
    expect(screen.queryByText("US.KO")).toBeTruthy();
    expect(screen.queryByText("US.LOW")).toBeNull();
  });

  it("row click publishes focus to the target group", () => {
    const { scanner, focus } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "t", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    fireEvent.click(screen.getByText("US.KO"));
    expect(focus).toHaveBeenCalledWith("green", "US.KO");
  });

  it("editing a threshold persists via onConfigChange", () => {
    const { onConfigChange } = renderPanel();
    fireEvent.change(screen.getByLabelText(/min change/i), { target: { value: "7" } });
    expect(onConfigChange).toHaveBeenCalledWith(expect.objectContaining({
      thresholds: expect.objectContaining({ minChangePct: 7 }) }));
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/ScannerPanel.test.tsx`
Expected: FAIL — `Cannot find module './ScannerPanel'`.

- [ ] **Step 3: Write the implementation**

`ui/src/chrome/panels/ScannerPanel.tsx`:
```tsx
import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { ScannerSession } from "../../wire/contract";
import type { LinkGroup } from "../linkGroups";
import { useTheme } from "../ThemeProvider";
import { formatTapeTime } from "../../render/format";
import { formatChangePct, formatCompactShares, msUntilEtMidnight } from "../format";
import { applyScannerFilters, sortByChangeDesc, type ScannerThresholds } from "./scannerFilter";

const SESSION_LABEL: Record<ScannerSession, string> = {
  premarket: "Pre-market", rth: "RTH movers", afterhours: "After-hours",
};
const GROUPS: Exclude<LinkGroup, null>[] = ["red", "green", "blue", "yellow"];

function readThresholds(s: Record<string, unknown>): ScannerThresholds {
  const t = (s.thresholds ?? {}) as Partial<ScannerThresholds>;
  return {
    minChangePct: typeof t.minChangePct === "number" ? t.minChangePct : 0,
    floatCapShares: typeof t.floatCapShares === "number" ? t.floatCapShares : null,
    minVolume: typeof t.minVolume === "number" ? t.minVolume : 0,
  };
}

export function ScannerPanel(
  { config, stores, linkGroups, onConfigChange, session }: PanelProps & { session: ScannerSession },
): JSX.Element {
  const { palette } = useTheme();
  const snap = useSyncExternalStore((cb) => stores.scanner.subscribe(cb), () => stores.scanner.getSnapshot());
  const sv = useMemo(() => stores.scanner.view(session), [snap, session, stores.scanner]);
  const [thresholds, setThresholds] = useState<ScannerThresholds>(() => readThresholds(config.settings));
  const targetGroup = ((config.settings.targetGroup as LinkGroup) ?? "green") as Exclude<LinkGroup, null>;

  // ET-midnight dedup reset: clear the per-session seen-set so the next session's
  // first prints flash fresh. Re-arms after each fire.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>;
    const arm = () => { timer = setTimeout(() => { stores.scanner.resetSeen(session); arm(); }, msUntilEtMidnight(new Date())); };
    arm();
    return () => clearTimeout(timer);
  }, [stores.scanner, session]);

  const rows = useMemo(() => sortByChangeDesc(applyScannerFilters(sv.rows, thresholds)), [sv.rows, thresholds]);

  const updateThreshold = (patch: Partial<ScannerThresholds>) => {
    const next = { ...thresholds, ...patch };
    setThresholds(next);
    onConfigChange({ ...config.settings, thresholds: next });
  };
  const swatch = (g: Exclude<LinkGroup, null>): string =>
    ({ red: palette.linkRed, green: palette.linkGreen, blue: palette.linkBlue, yellow: palette.linkYellow }[g]);
  const header = sv.refreshedAt
    ? `${SESSION_LABEL[session]} · updated ${formatTapeTime(sv.refreshedAt)}`
    : `Waiting for ${SESSION_LABEL[session].toLowerCase()} data…`;

  const th = { padding: "2px 8px", position: "sticky" as const, top: 0, background: palette.surface };
  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 8px", borderBottom: `1px solid ${palette.border}` }}>
        <span style={{ fontWeight: 600 }}>{header}</span>
        <span style={{ flex: 1 }} />
        {GROUPS.map((g) => (
          <button key={g} title={`Send clicks to ${g}`} aria-label={`send clicks to ${g}`}
            onClick={() => onConfigChange({ ...config.settings, targetGroup: g })}
            style={{ width: 14, height: 14, borderRadius: 3, background: swatch(g), padding: 0, cursor: "pointer",
              border: targetGroup === g ? `2px solid ${palette.text}` : `1px solid ${palette.border}` }} />
        ))}
      </div>
      <div style={{ display: "flex", gap: 10, padding: "4px 8px", color: palette.textMuted, borderBottom: `1px solid ${palette.border}` }}>
        <label>min change % <input aria-label="min change %" type="number" value={thresholds.minChangePct}
          onChange={(e) => updateThreshold({ minChangePct: Number(e.target.value) || 0 })} style={{ width: 52 }} /></label>
        <label>float ≤ <input aria-label="float cap" type="number" value={thresholds.floatCapShares ?? ""}
          onChange={(e) => updateThreshold({ floatCapShares: e.target.value === "" ? null : Number(e.target.value) })} style={{ width: 90 }} /></label>
        <label>vol ≥ <input aria-label="min volume" type="number" value={thresholds.minVolume}
          onChange={(e) => updateThreshold({ minVolume: Number(e.target.value) || 0 })} style={{ width: 80 }} /></label>
      </div>
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <thead>
          <tr style={{ color: palette.textMuted, textAlign: "right" }}>
            <th style={{ ...th, textAlign: "left" }}>Symbol</th><th style={th}>%</th><th style={th}>Last</th><th style={th}>Float</th><th style={th}>Vol</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.symbol} onClick={() => linkGroups.focus(targetGroup, r.symbol)}
              style={{ cursor: "pointer", textAlign: "right", opacity: r.muted ? 0.55 : 1,
                background: r.isNewHit ? palette.accent + "33" : "transparent" }}>
              <td style={{ textAlign: "left", padding: "2px 8px" }}>{r.symbol}</td>
              <td style={{ padding: "2px 8px", color: r.changePct === null ? palette.textMuted : r.changePct > 0 ? palette.up : r.changePct < 0 ? palette.down : palette.text }}>{formatChangePct(r.changePct)}</td>
              <td style={{ padding: "2px 8px" }}>{r.last === null ? "—" : r.last.toFixed(2)}</td>
              <td style={{ padding: "2px 8px" }}>{formatCompactShares(r.floatShares)}</td>
              <td style={{ padding: "2px 8px" }}>{formatCompactShares(r.volume)}</td>
            </tr>
          ))}
          {rows.length === 0 && sv.refreshedAt && (
            <tr><td colSpan={5} style={{ padding: 12, color: palette.textMuted, textAlign: "center" }}>No symbols match the current filters.</td></tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
```

> New-hit rows carry a translucent `palette.accent` highlight and seen rows dim to `opacity 0.55` — a state-driven highlight rather than a CSS keyframe, so it is deterministic and jsdom-testable. A subtle animated pulse is deferred polish. All colors come from `useTheme()`/`palette` — this panel does **not** copy `ConnectionStatusPanel`'s hardcoded hex.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/panels/ScannerPanel.test.tsx`
Expected: PASS — all 5 cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/chrome/panels/ScannerPanel.tsx ui/src/chrome/panels/ScannerPanel.test.tsx
git commit -m "feat(ui/chrome): ScannerPanel — session-aware rank table, thresholds, row-click focus"
```

---

## Task 6: Register Scanner + Movers panels; seed target group + thresholds

**Files:**
- Modify: `ui/src/chrome/panels/registry.tsx` (register `scanner` + `movers`; update the ledger comment)
- Modify: `ui/src/seeds/workspaces.ts` (scanner/movers `settings.targetGroup` + thresholds)
- Test: `ui/src/chrome/panels/registry.test.tsx`

**Interfaces:**
- Consumes: `ScannerPanel` (Task 5).
- Produces: `PANELS["scanner"]` (`session="premarket"`) and `PANELS["movers"]` (`session="rth"`), both `topics: ["scanner.rank", "scanner.hit"]`. Seed monitoring panels `m-scanner`/`m-movers` gain `settings.targetGroup` + `thresholds` (group stays `null`).

- [ ] **Step 1: Write the failing test**

`ui/src/chrome/panels/registry.test.tsx`:
```tsx
import { describe, it, expect } from "vitest";
import { PANELS } from "./registry";
import { SEED_WORKSPACES } from "../../seeds/workspaces";

describe("panel registry — monitoring surfaces", () => {
  it("registers scanner and movers with the scanner topics", () => {
    for (const id of ["scanner", "movers"]) {
      expect(PANELS[id]).toBeDefined();
      expect(PANELS[id].topics).toEqual(["scanner.rank", "scanner.hit"]);
    }
  });
});

describe("seed monitoring — scanner/movers publish target + thresholds", () => {
  const panels = Object.fromEntries(SEED_WORKSPACES.monitoring.panels.map((p) => [p.id, p]));
  it("scanner stays display-pinned but targets a group and carries thresholds", () => {
    expect(panels["m-scanner"].group).toBeNull();
    expect(panels["m-scanner"].settings.targetGroup).toBe("green");
    expect(panels["m-scanner"].settings.thresholds).toBeDefined();
  });
  it("movers stays display-pinned and targets a group", () => {
    expect(panels["m-movers"].group).toBeNull();
    expect(panels["m-movers"].settings.targetGroup).toBe("green");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/registry.test.tsx`
Expected: FAIL — `PANELS["scanner"]` is `undefined`; seed `settings.targetGroup` is missing.

- [ ] **Step 3: Write the implementation**

In `ui/src/chrome/panels/registry.tsx`, add the import and the two entries, and rewrite the ledger comment:
```tsx
import { ScannerPanel } from "./ScannerPanel";
```
Replace the comment block above `export const PANELS` with:
```tsx
// Plan 1 registered the two stack-proving panels; Plan 2 added the chart panel;
// Plan 3 added the L2 ladder + time & sales; Plan 4 adds scanner / movers / news
// below. Plan 5 still owes account-bar / positions / open-orders / order-ticket.
```
Add these entries inside the `PANELS` object (after `"tape"`):
```tsx
  "scanner": {
    component: (p) => <ScannerPanel {...p} session="premarket" />,
    topics: ["scanner.rank", "scanner.hit"],
  },
  "movers": {
    component: (p) => <ScannerPanel {...p} session="rth" />,
    topics: ["scanner.rank", "scanner.hit"],
  },
```

In `ui/src/seeds/workspaces.ts`, replace the `m-scanner` and `m-movers` lines:
```ts
      { id: "m-scanner", panelId: "scanner", group: null,
        settings: { targetGroup: "green", thresholds: { minChangePct: 5, floatCapShares: null, minVolume: 50000 } } },
      { id: "m-movers", panelId: "movers", group: null,
        settings: { targetGroup: "green", thresholds: { minChangePct: 3, floatCapShares: null, minVolume: 0 } } },
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/panels/registry.test.tsx`
Expected: PASS — both describe blocks green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/chrome/panels/registry.tsx ui/src/seeds/workspaces.ts ui/src/chrome/panels/registry.test.tsx
git commit -m "feat(ui/chrome): register scanner + movers panels; seed target group + thresholds"
```

---

## Task 7: NewsPanel — per-symbol list, follows link-group focus

**Files:**
- Create: `ui/src/chrome/panels/NewsPanel.tsx`
- Modify: `ui/src/chrome/panels/registry.tsx` (register `news`)
- Test: `ui/src/chrome/panels/NewsPanel.test.tsx` (jsdom)

**Interfaces:**
- Consumes: `PanelProps`; `useTheme`; `formatTapeTime` from `render/format.ts`; `stores.news` (Task 4); `linkGroups.symbolFor`/`subscribe`.
- Produces: `NewsPanel(props: PanelProps): JSX.Element`; `PANELS["news"]` with `topics: ["news.item"]`. (`m-news` is already in the seed with `group: "green"`.)

- [ ] **Step 1: Write the failing test**

`ui/src/chrome/panels/NewsPanel.test.tsx`:
```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { LinkGroups } from "../linkGroups";
import { makeStores } from "../../data/registry";
import { NewsPanel } from "./NewsPanel";
import type { PanelProps } from "./registry";
import type { PanelConfig } from "../workspace";

function fakeBus() {
  const subs = new Set<(m: unknown) => void>();
  return { post: (m: unknown) => subs.forEach((cb) => cb(m)), onMessage: (cb: (m: unknown) => void) => { subs.add(cb); return () => subs.delete(cb); }, close: () => {} };
}

function renderPanel() {
  const stores = makeStores();
  const news = stores.news;
  const linkGroups = new LinkGroups(fakeBus() as never, () => {});
  const config: PanelConfig = { id: "m-news", panelId: "news", group: "green", settings: {} };
  const props = { config, stores, linkGroups, onConfigChange: vi.fn(), scheduler: {} as never,
    width: 400, height: 300, commands: { sendCommand: async () => ({ status: "accepted" }) } } as PanelProps;
  render(<ThemeProvider><NewsPanel {...props} /></ThemeProvider>);
  return { news, linkGroups };
}

describe("NewsPanel", () => {
  it("shows a reserved halt-banner slot and a no-symbol header before focus", () => {
    renderPanel();
    expect(screen.getByTestId("halt-slot")).toBeTruthy();
    expect(screen.getByText(/no symbol focused/i)).toBeTruthy();
  });

  it("follows the group's focused symbol and lists its news newest-first", () => {
    const { news, linkGroups } = renderPanel();
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        { symbol: "US.AAPL", headline: "Older AAPL", source: "R", url: "u1", seen_at: "2026-07-06T13:28:00Z" },
        { symbol: "US.AAPL", headline: "Newer AAPL", source: "R", url: "u2", seen_at: "2026-07-06T13:31:00Z" },
        { symbol: "US.NVDA", headline: "NVDA news", source: "R", url: "n1", seen_at: "2026-07-06T13:30:00Z" },
      ] });
      linkGroups.focus("green", "US.AAPL");
    });
    const links = screen.getAllByRole("link");
    expect(links.map((a) => a.textContent)).toEqual(["Newer AAPL", "Older AAPL"]); // newest first, NVDA excluded
    expect(screen.getByText(/seen/i)).toBeTruthy();
  });

  it("clicking a headline opens its url", () => {
    const { news, linkGroups } = renderPanel();
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    act(() => {
      news.apply({ kind: "snapshot", topic: "news.item", payload: [
        { symbol: "US.AAPL", headline: "H", source: "R", url: "https://x/a", seen_at: "t" }] });
      linkGroups.focus("green", "US.AAPL");
    });
    fireEvent.click(screen.getByText("H"));
    expect(open).toHaveBeenCalledWith("https://x/a", "_blank", "noopener,noreferrer");
  });

  it("shows an empty state when the focused symbol has no news", () => {
    const { linkGroups } = renderPanel();
    act(() => linkGroups.focus("green", "US.TSLA"));
    expect(screen.getByText(/no news for US.TSLA/i)).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/NewsPanel.test.tsx`
Expected: FAIL — `Cannot find module './NewsPanel'`.

- [ ] **Step 3: Write the implementation**

`ui/src/chrome/panels/NewsPanel.tsx`:
```tsx
import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { formatTapeTime } from "../../render/format";

export function NewsPanel({ config, stores, linkGroups }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const snap = useSyncExternalStore((cb) => stores.news.subscribe(cb), () => stores.news.getSnapshot());
  const [symbol, setSymbol] = useState<string | undefined>(() => linkGroups.symbolFor(config.group));
  useEffect(() => {
    setSymbol(linkGroups.symbolFor(config.group));
    return linkGroups.subscribe(() => setSymbol(linkGroups.symbolFor(config.group)));
  }, [linkGroups, config.group]);
  const items = useMemo(() => (symbol ? stores.news.itemsFor(symbol) : []), [snap, symbol, stores.news]);

  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      {/* Reserved slot for high-salience halt banners (v2 feed) — empty in v1. */}
      <div data-testid="halt-slot" />
      <div style={{ padding: "6px 8px", fontWeight: 600, borderBottom: `1px solid ${palette.border}` }}>
        {symbol ? `News · ${symbol}` : "News · no symbol focused"}
      </div>
      {symbol && items.length === 0 && (
        <div style={{ padding: 12, color: palette.textMuted }}>No news for {symbol}.</div>
      )}
      {items.map((it, i) => (
        <div key={it.url || `${it.headline}-${i}`} style={{ padding: "6px 8px", borderBottom: `1px solid ${palette.border}` }}>
          <a href={it.url} onClick={(e) => { e.preventDefault(); window.open(it.url, "_blank", "noopener,noreferrer"); }}
            style={{ color: palette.accent, textDecoration: "none", cursor: "pointer" }}>{it.headline}</a>
          <div style={{ color: palette.textMuted, marginTop: 2 }}>{it.source} · seen {formatTapeTime(it.seen_at)}</div>
        </div>
      ))}
    </div>
  );
}
```

In `ui/src/chrome/panels/registry.tsx` add the import and the entry:
```tsx
import { NewsPanel } from "./NewsPanel";
```
```tsx
  "news": {
    component: NewsPanel,
    topics: ["news.item"],
  },
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/panels/NewsPanel.test.tsx`
Expected: PASS — all 4 cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/chrome/panels/NewsPanel.tsx ui/src/chrome/panels/registry.tsx ui/src/chrome/panels/NewsPanel.test.tsx
git commit -m "feat(ui/chrome): NewsPanel — follows link-group symbol, seen-time, halt slot"
```

---

## Task 8: Monitoring fixture + self-check

**Files:**
- Create: `ui/fixtures/monitoring.json`
- Modify: `ui/mock-engine/run.ts` (document the fixture in the selection comment)
- Test: `ui/fixtures/monitoring.test.ts`

**Interfaces:**
- Consumes: `ScannerRankPayload`/`ScanHitPayload`/`NewsItem` (Task 2), `ScannerSession`.
- Produces: `ui/fixtures/monitoring.json` conforming to the mock engine's `Fixture` shape (`snapshots[]` / `deltas[]`), serving `scanner.rank` (keys `premarket` + `rth`), `scanner.hit`, and `news.item`. Consumed by the dev app (`npm run mock-engine -- monitoring`) and the replay test (Task 9).

- [ ] **Step 1: Write the failing test**

`ui/fixtures/monitoring.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const fx = JSON.parse(readFileSync(join(here, "monitoring.json"), "utf8")) as {
  snapshots: Array<{ topic: string; key?: string; payload: unknown }>;
  deltas: Array<{ afterMs: number; topic: string; key?: string; payload: unknown }>;
};

const isNum = (v: unknown) => typeof v === "number";
const isNumOrNull = (v: unknown) => v === null || typeof v === "number";

describe("monitoring fixture conforms to the contract", () => {
  const all = [...fx.snapshots, ...fx.deltas];

  it("has both scanner sessions, a scanner.hit, and news", () => {
    const keys = new Set(all.filter((e) => e.topic === "scanner.rank").map((e) => e.key));
    expect(keys.has("premarket")).toBe(true);
    expect(keys.has("rth")).toBe(true);
    expect(all.some((e) => e.topic === "scanner.hit")).toBe(true);
    expect(all.some((e) => e.topic === "news.item")).toBe(true);
  });

  it("scanner.rank rows are typed correctly (nullable change/last/float, numeric volume)", () => {
    for (const e of all.filter((x) => x.topic === "scanner.rank")) {
      const p = e.payload as { refreshedAt: string; rows: Record<string, unknown>[] };
      expect(typeof p.refreshedAt).toBe("string");
      for (const row of p.rows) {
        expect(typeof row.symbol).toBe("string");
        expect(isNumOrNull(row.changePct)).toBe(true);
        expect(isNumOrNull(row.last)).toBe(true);
        expect(isNumOrNull(row.floatShares)).toBe(true);
        expect(isNum(row.volume)).toBe(true);
      }
    }
  });

  it("news items carry all five string fields", () => {
    for (const e of all.filter((x) => x.topic === "news.item")) {
      const items = Array.isArray(e.payload) ? e.payload : [e.payload];
      for (const it of items as Record<string, unknown>[]) {
        for (const f of ["symbol", "headline", "source", "url", "seen_at"]) expect(typeof it[f]).toBe("string");
      }
    }
  });

  it("scanner.hit carries string symbol + at", () => {
    for (const e of all.filter((x) => x.topic === "scanner.hit")) {
      const p = e.payload as Record<string, unknown>;
      expect(typeof p.symbol).toBe("string");
      expect(typeof p.at).toBe("string");
    }
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run fixtures/monitoring.test.ts`
Expected: FAIL — `monitoring.json` does not exist (`ENOENT`).

- [ ] **Step 3: Write the fixture and update the runner**

`ui/fixtures/monitoring.json`:
```json
{
  "snapshots": [
    { "topic": "scanner.rank", "key": "premarket", "payload": { "refreshedAt": "2026-07-06T08:30:00Z", "rows": [
      { "symbol": "US.DJT",  "changePct": 42.1, "last": 31.50, "floatShares": 110000000,  "volume": 3400000 },
      { "symbol": "US.KO",   "changePct": 18.4, "last": 62.10, "floatShares": 4300000000, "volume": 1250000 },
      { "symbol": "US.ABCD", "changePct": 7.2,  "last": 2.15,  "floatShares": 8500000,    "volume": 640000 },
      { "symbol": "US.WXYZ", "changePct": null, "last": null,  "floatShares": 21000000,   "volume": 0 }
    ] } },
    { "topic": "scanner.rank", "key": "rth", "payload": { "refreshedAt": "2026-07-06T14:00:00Z", "rows": [
      { "symbol": "US.NVDA", "changePct": 3.4,  "last": 130.20, "floatShares": 2400000000, "volume": 5200000 },
      { "symbol": "US.SPY",  "changePct": 1.2,  "last": 560.10, "floatShares": 900000000,  "volume": 11000000 },
      { "symbol": "US.TSLA", "changePct": -2.1, "last": 240.50, "floatShares": 2800000000, "volume": 8000000 }
    ] } },
    { "topic": "news.item", "payload": [
      { "symbol": "US.AAPL", "headline": "Apple ships record quarter",  "source": "Reuters",   "url": "https://ex.com/a1", "seen_at": "2026-07-06T13:29:50Z" },
      { "symbol": "US.AAPL", "headline": "Analysts raise AAPL targets", "source": "Bloomberg", "url": "https://ex.com/a2", "seen_at": "2026-07-06T13:28:10Z" },
      { "symbol": "US.NVDA", "headline": "NVDA unveils new GPU line",    "source": "CNBC",      "url": "https://ex.com/n1", "seen_at": "2026-07-06T13:25:00Z" }
    ] }
  ],
  "deltas": [
    { "afterMs": 1500, "topic": "news.item", "payload":
      { "symbol": "US.AAPL", "headline": "Apple schedules fall event", "source": "WSJ", "url": "https://ex.com/a3", "seen_at": "2026-07-06T13:31:00Z" } },
    { "afterMs": 2000, "topic": "scanner.rank", "key": "premarket", "payload": { "refreshedAt": "2026-07-06T08:30:02Z", "rows": [
      { "symbol": "US.DJT",  "changePct": 40.5, "last": 30.90, "floatShares": 110000000, "volume": 3600000 },
      { "symbol": "US.KO",   "changePct": 19.1, "last": 62.60, "floatShares": 4300000000, "volume": 1300000 },
      { "symbol": "US.GHIJ", "changePct": 12.5, "last": 4.40,  "floatShares": 6000000,    "volume": 900000 },
      { "symbol": "US.ABCD", "changePct": 7.0,  "last": 2.10,  "floatShares": 8500000,    "volume": 660000 }
    ] } },
    { "afterMs": 2500, "topic": "scanner.hit", "key": "premarket", "payload": { "symbol": "US.KO", "at": "2026-07-06T08:30:02.5Z" } },
    { "afterMs": 3000, "topic": "scanner.rank", "key": "rth", "payload": { "refreshedAt": "2026-07-06T14:00:03Z", "rows": [
      { "symbol": "US.AMD",  "changePct": 6.8,  "last": 168.30, "floatShares": 1600000000, "volume": 4100000 },
      { "symbol": "US.NVDA", "changePct": 3.6,  "last": 130.60, "floatShares": 2400000000, "volume": 5400000 },
      { "symbol": "US.SPY",  "changePct": 1.3,  "last": 560.40, "floatShares": 900000000,  "volume": 11400000 }
    ] } }
  ]
}
```

In `ui/mock-engine/run.ts`, add a line to the fixture-selection comment (after the `ladder-tape` line):
```ts
//   npm run mock-engine -- monitoring         (scanner rank/hit + news, Plan 4)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run fixtures/monitoring.test.ts`
Expected: PASS — all 3 cases green.

- [ ] **Step 5: Commit**

```bash
git add ui/fixtures/monitoring.json ui/fixtures/monitoring.test.ts ui/mock-engine/run.ts
git commit -m "feat(ui/mock-engine): monitoring fixture — scanner rank/hit + news"
```

---

## Task 9: Monitoring replay invariant

**Files:**
- Create: `ui/src/monitoringReplay.test.ts`

**Interfaces:**
- Consumes: `makeStores`/`routeToStore` from `data/registry.ts`; the `monitoring.json` fixture (Task 8); `ScannerStore`/`NewsStore` behavior (Tasks 3–4).
- Produces: nothing (test only). This is the UI twin of the engine's `replay(log) == state` invariant for the monitoring surfaces.

- [ ] **Step 1: Write the failing test**

`ui/src/monitoringReplay.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { makeStores, routeToStore } from "./data/registry";
import type { SnapshotMsg, DeltaMsg } from "./wire/contract";

const here = dirname(fileURLToPath(import.meta.url));
const fx = JSON.parse(readFileSync(join(here, "..", "fixtures", "monitoring.json"), "utf8")) as {
  snapshots: Array<{ topic: string; key?: string; payload: unknown }>;
  deltas: Array<{ topic: string; key?: string; payload: unknown }>;
};

function replay() {
  const stores = makeStores();
  const asMsg = (kind: "snapshot" | "delta", e: { topic: string; key?: string; payload: unknown }) =>
    ({ kind, topic: e.topic, key: e.key, payload: e.payload } as SnapshotMsg | DeltaMsg);
  for (const s of fx.snapshots) routeToStore(stores, asMsg("snapshot", s));
  for (const d of fx.deltas) routeToStore(stores, asMsg("delta", d));
  return stores;
}

describe("monitoring replay invariant", () => {
  it("scanner premarket: newcomer flashes, carried-over rows mute, scanner.hit re-flashes", () => {
    const v = replay().scanner.view("premarket");
    const byId = Object.fromEntries(v.rows.map((r) => [r.symbol, r]));
    expect(v.refreshedAt).toBe("2026-07-06T08:30:02Z");
    expect(byId["US.GHIJ"].isNewHit).toBe(true);  // introduced in the delta
    expect(byId["US.KO"].isNewHit).toBe(true);     // forced by scanner.hit
    expect(byId["US.DJT"].isNewHit).toBe(false);
    expect(byId["US.DJT"].muted).toBe(true);
    expect(byId["US.WXYZ"]).toBeUndefined();       // fell off the ranking
  });

  it("scanner rth is isolated: its delta newcomer flashes", () => {
    const byId = Object.fromEntries(replay().scanner.view("rth").rows.map((r) => [r.symbol, r]));
    expect(byId["US.AMD"].isNewHit).toBe(true);
    expect(byId["US.NVDA"].muted).toBe(true);
  });

  it("news: itemsFor(AAPL) is deduped and newest-first by seen_at", () => {
    const urls = replay().news.itemsFor("US.AAPL").map((i) => i.url);
    expect(urls).toEqual(["https://ex.com/a3", "https://ex.com/a1", "https://ex.com/a2"]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails (then passes)**

Run: `cd ui && npx vitest run src/monitoringReplay.test.ts`
Expected: PASS immediately — Tasks 3–4 and 8 already implement the behavior this asserts. (If any assertion fails, it means a store or the fixture drifted from the contract — fix the offending store/fixture, not the test.)

- [ ] **Step 3: Commit**

```bash
git add ui/src/monitoringReplay.test.ts
git commit -m "test(ui): monitoring replay invariant — scanner flash/dedup + news view"
```

---

## Task 10: Integration sweep + plan close-out

**Files:**
- Verify only (no new source): full suite, typecheck, lint, build, and a manual two-terminal smoke of the Monitoring workspace.

**Interfaces:**
- Consumes: everything from Tasks 1–9.
- Produces: a green `npm run build && npx vitest run && npm run lint`, a confirmed manual smoke, and the completed self-review.

- [ ] **Step 1: Full verification**

Run: `cd ui && npm run build && npx vitest run && npm run lint`
Expected: `tsc` clean (both `tsconfig.json` and `tsconfig.node.json`), Vite build succeeds, **all** tests pass (new Plan-4 tests plus the entire pre-existing suite — no regressions), ESLint clean.

- [ ] **Step 2: Manual smoke of the Monitoring workspace**

Two terminals:
```bash
# terminal 1
cd ui && npm run mock-engine -- monitoring
# terminal 2
cd ui && npm run dev
```
Open `http://127.0.0.1:5173/?workspace=monitoring` and confirm:
- The **Scanner** panel shows the pre-market ranking sorted by % (DJT, KO, ABCD), 3-digit-safe %, compact float (`4.3B`, `110M`), and `US.WXYZ`'s no-print row rendering `—` (not `0%`/`$0`). After ~2 s the refresh introduces `US.GHIJ` (highlighted) and dims the carried rows; `US.KO` re-highlights from the `scanner.hit`.
- The **Movers** panel shows the RTH ranking (NVDA, SPY, TSLA), then `US.AMD` appears highlighted after ~3 s.
- Editing a threshold input filters rows live; the header swatch selector changes the target group.
- Clicking a Scanner row focuses the target group (`green`) → the green chart and the **News** panel follow that symbol.
- The **News** panel shows the focused symbol's headlines with `source · seen HH:MM:SS`; clicking a headline opens its URL; the halt-banner slot is present but empty.
- No panel crashes; each is inside its error boundary.

- [ ] **Step 3: Self-review against the roadmap scope**

Confirm each Plan-4 roadmap-line-34 deliverable maps to a task (check every box):
- [ ] one **session-parameterized** rank table (Scanner + Movers, one component) — Tasks 3, 5, 6
- [ ] **3-digit-safe %** — Task 1 (`formatChangePct`), asserted
- [ ] **compact float** — Task 1 (`formatCompactShares`), asserted
- [ ] **"no print yet" state** (never fabricated 0) — Tasks 1, 5, asserted
- [ ] **new-hit flash + midnight-reset dedup** — Tasks 3 (`resetSeen`, seen-set), 5 (ET-midnight timer)
- [ ] **configurable thresholds** — Tasks 2 (`applyScannerFilters`), 5 (inputs + `onConfigChange`), 6 (seed)
- [ ] news panel with **`NewsItem` shape** — Tasks 2, 4, 7
- [ ] **seen-time labeling** (not publish time) — Task 7 (`seen HH:MM:SS`)
- [ ] **halt-banner slot reserved** — Task 7 (`data-testid="halt-slot"`)
- [ ] Deliverable: the **Monitoring workspace** renders live — Task 10 smoke
- [ ] **Out of scope** confirmed absent: no execution surfaces (Plan 5), no Playwright (Plan 6), no new palette tokens, no canvas/golden work.

- [ ] **Step 4: Commit the close-out**

```bash
git add -A
git commit -m "chore(ui/chrome): Plan 4 integration sweep — registry ledger, monitoring workspace close-out"
```

---

## Self-Review (run after drafting — completed 2026-07-05)

**Spec coverage.** Every UI-spec §Panels "Monitoring workspace" bullet and every roadmap-line-34 clause maps to a task (see Task 10 Step 3). Scanner: Tasks 2/3/5/6. Movers (RTH parameterization of the same component, sorted by %): Tasks 5/6. News (follows group focus, seen-time, halt slot, `NewsItem`-only): Tasks 4/7. Session-aware header + last-refresh: Task 5. New-hit flash + midnight-reset dedup + configurable thresholds: Tasks 3/5 + 2/5/6. Deferred by design and NOT in this plan: execution surfaces/order entry (Plan 5), Playwright smoke + `ui/dist` serving (Plan 6), halt feed content (v2 — only the slot is reserved), news enrichment/epoch timestamps (out of v1). Dynamic per-mount topic subscription (the `App.tsx` "Plan 4/5 make this dynamic" comment) is **intentionally left to Plan 5** — the static seed-derived topic union already subscribes `scanner.rank`/`scanner.hit`/`news.item` because scanner/movers/news are seed panels, so Plan 4 needs no `App.tsx` change (noted in File Structure).

**Placeholder scan.** No `TBD`/"add error handling"/"handle edge cases"/"similar to Task N" placeholders. Every code step contains complete, runnable code and every test step contains full assertions. The one deliberate elision is the animated flash *pulse* (v1 uses a deterministic, testable state-driven highlight + dim; a CSS pulse is explicitly deferred polish, called out in Task 5).

**Type consistency.** `ScannerRow`/`ScannerRankPayload`/`ScanHitPayload`/`NewsItem`/`ScannerSession` are defined once in `wire/contract.ts` (Task 2) and imported unchanged by `ScannerStore` (Task 3), `NewsStore` (Task 4), the fixture self-check (Task 8), and the replay test (Task 9). `ScannerRowView`/`ScannerSessionView` are defined in `ScannerStore.ts` (Task 3) and consumed by `ScannerPanel` (Task 5). `ScannerThresholds` is defined in `scannerFilter.ts` (Task 2) and consumed identically by `ScannerPanel` (Task 5, `readThresholds`) and seeded in `workspaces.ts` (Task 6). `applyScannerFilters`/`sortByChangeDesc` signatures match their call sites. `view(session)`/`resetSeen(session?)`/`itemsFor(symbol)` names are used identically at every call site (panels + replay test). `PANELS` entries use the `PanelDef { component: FC<PanelProps>; topics: TopicName[] }` shape; the `scanner`/`movers` wrappers are `FC<PanelProps>` arrow functions injecting `session`, matching `ConnectionStatusPanel`'s wrapping precedent.

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-05-ui-monitoring-surfaces.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — REQUIRED SUB-SKILL `superpowers:subagent-driven-development`: dispatch a fresh subagent per task with two-stage review between tasks. Matches how Plans 2 and 3 were executed (worktree + per-task review). Route implementers on Sonnet (`/model sonnet`) per phase-router; keep the final whole-branch review on the stronger model.

**2. Inline Execution** — REQUIRED SUB-SKILL `superpowers:executing-plans`: run the tasks in this session with batch checkpoints.

**Which approach?**
