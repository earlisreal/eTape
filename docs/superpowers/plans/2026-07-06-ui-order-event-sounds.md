# Order & Scanner Event Sounds — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add four Web-Audio event sounds to the eTape UI — order placed (click), order filled (buy/sell two-tone), order rejected (alert beeps), and scanner hit (arpeggio) — driven imperatively off existing data-store events and the order-command ack path, with a settings section and per-sound preview.

**Architecture:** A new dependency-free `ui/src/sound/` module. `patches.ts` holds Web-Audio synthesis functions ported 1:1 from `prototypes/fill-sounds.html` plus a real `PatchPlayer` that owns the `AudioContext`. `SoundEngine` is a module singleton holding config + all play-decision logic (per-channel 200 ms coalescing, fill freshness guard, config gating, `volume²` master gain); it delegates actual sound to an injected `PatchPlayer`, so its logic is unit-tested with a fake player and an injected `now()`. Three existing stores (`FillStore`, `ExecStore`, `ScannerStore`) gain discrete-event listener hooks; `OrderCommands` gains an optional `sound` dep; a `SoundConfigProvider` mirrors `OrderConfigProvider` for KV persistence; a `SoundsSection` extends the settings modal; and a `useSoundWiring` hook (called once in `AppShell`) subscribes the stores to the engine and registers the one-time `AudioContext`-resume listener.

**Tech Stack:** TypeScript + React (Vite app under `ui/`), Web Audio API, Vitest 1.6 (`node` env by default, `// @vitest-environment jsdom` per-file for React tests), `@testing-library/react` v16 with `fireEvent` (no `userEvent`), ESLint. No new dependencies.

## Global Constraints

Copied from the approved spec (`docs/superpowers/specs/2026-07-06-order-event-sounds-design.md`) and CLAUDE.md. Every task's requirements implicitly include this section.

- **Scope: UI-only, zero engine changes.** Config persists through the existing generic `GetConfig`/`SetConfig` KV commands; every trigger event already streams to the UI. Do not touch `engine/`, the wire contract in `ui/src/wire/contract.ts`, or `ui/src/wire/gen`.
- **Audio never flows through React state** (CLAUDE.md imperative-canvas rule). The `SoundEngine` is a plain class/singleton driven by imperative calls and store subscriptions — never `useState`/`useReducer` for the audio path. Config *is* React state (it's low-frequency), pushed into the engine via an effect.
- **Port synthesis 1:1.** Every patch in `patches.ts` is copied verbatim from `prototypes/fill-sounds.html` — do not alter any frequency, gain, timing, or filter constant. The only permitted changes are the TypeScript signature and module packaging.
- **Coalescing window is a constant `200` ms, not a setting.** Channels: `place:buy`, `place:sell`, `fill:buy`, `fill:sell`, `reject`, `scanner`.
- **Fill freshness guard:** play a fill only when `tsMs >= now() - 10_000`. Reject/place/scanner have no freshness guard.
- **Repo is PUBLIC.** No credentials or account identifiers in any file or test fixture.
- **Every task ends green:** `cd ui && npm run typecheck && npm run lint && npm test` all pass. Commit only when green.
- **Test conventions:** default vitest env is `node`; any file that renders React or touches `document` must start with `// @vitest-environment jsdom`. Use `fireEvent` (not `userEvent`). Inject collaborators via constructor/params + a numeric/`now()` value; drive time with the injected `now()` (SoundEngine) or `vi.useFakeTimers()`/`vi.setSystemTime()` (only where a real timer is used). No global test-setup file exists — do not add one.

---

## Plan sequence context

This feature is independent of the pending engine Plan 6 (uihub) and UI Plan 6 (E2E + packaging): every seam it consumes (the four trigger events, the `GetConfig`/`SetConfig` KV commands) already exists in the merged UI (Plans 1–5) and its frozen wire contract. It can land before or after UI Plan 6. Execute it in a **UI worktree** to stay isolated from the engine Plan 6 worktree that is concurrently generating `ui/src/gen`.

## Authoritative references (read before implementing)

- `docs/superpowers/specs/2026-07-06-order-event-sounds-design.md` — the approved design (sound vocabulary, decision logic, settings, trigger wiring, testing, non-goals).
- `prototypes/fill-sounds.html` — the audition page. Synthesis functions are ported 1:1 from here. Shared helpers `env`/`tone`/`noiseBurst` at lines 107–140 (the `audio()` lifecycle at 88–97 is **not** ported — `WebAudioPatchPlayer` owns the context); fill patches 145–224; place-click + reject patches 228–310; scanner patches 314–374; the coalescing gate at 378–389.
- `ui/src/chrome/exec/useOrderConfig.tsx` — the provider to clone for `SoundConfigProvider`.
- `ui/src/chrome/exec/commands.ts` + `ui/src/chrome/exec/useOrderCommands.ts` — the order-command layer to thread the `sound` dep through.
- `ui/src/data/FillStore.ts`, `ui/src/data/ExecStore.ts`, `ui/src/data/ScannerStore.ts` — the three stores gaining listener hooks; `ui/src/data/store.ts` for the `PaintStore`/`ReactStore` base classes.
- `ui/src/chrome/exec/OrderSettingsModal.tsx` (+ `.test.tsx`) — the modal the Sounds section is appended to; the inline-`style` idiom and `data-testid` test pattern.
- `ui/src/chrome/AppShell.tsx` + `ui/src/chrome/exec/useHotkeys.ts` — the once-mounted-hook + window-listener + cleanup pattern to mirror for `useSoundWiring`.
- `ui/src/wire/orderStatus.ts` — `sideIsSell(side: Side): boolean`.

## Design decisions (locked; rationale for the non-obvious ones)

1. **`AckMsg.status` has no `"rejected"` value — it is `"accepted" | "blocked"`.** The spec's "ack `rejected`" means the local-gate rejection, which arrives on the wire as `status === "blocked"`. So: **ack `blocked` → `orderRejected()`; ack `accepted` → `orderPlaced()`.** (Reconciles spec §Trigger wiring with `ui/src/wire/contract.ts:110`.)
2. **`SoundEngine` delegates to an injected `PatchPlayer`; time comes from an injected `now: () => number`.** No `AudioContext` and no `Clock` class exist in the codebase, and jsdom has no Web Audio. The fake player records calls and the fake `now` is a mutable counter, so all decision logic (coalescing, freshness, gating, variant, double-fire absorption) is deterministically unit-tested in the `node` env with zero audio. This mirrors the existing `OrderCommandsDeps.now: () => number` and `WorkspaceStore(client, debounceMs)` injection idioms.
   - **New precedent — the module-level singleton `export const soundEngine = new SoundEngine()` is deliberate.** The spec mandates a module singleton (spec §Module), and the codebase has no existing bare `export const x = new Class()` (stores are built via `makeStores()` / `useMemo`). The singleton is the right call here because three unrelated consumers (`OrderCommands` defaults, `useSoundWiring`, `SoundConfigProvider`) must all reach the *same* engine with no natural common owner, and the audio path is explicitly outside React state. Tests never touch the singleton's audio — they construct `new SoundEngine(fakePlayer, fakeNow)`; only `SoundConfigProvider`/`SoundsSection` tests that assert the push spy on the singleton (`vi.spyOn(soundEngine, …)` + `vi.restoreAllMocks()`).
3. **`FillStore` gets the codebase's first discrete-event callback hook.** It extends `PaintStore` (poll-based, no `subscribe`), so `onNewFill` is a new `Set<(fill: Fill) => void>` fired inside `ingest()` right after a fill passes dedup — for **both** snapshot and delta (the SoundEngine freshness guard, not a delta-only gate, is what silences the morning's backfilled fills). `ExecStore`/`ScannerStore` extend `ReactStore` but its `subs` set is `private`, so they each also get their own separate typed listener set.
4. **`orderPlaced` and `orderFilled` take a `Side`; the engine converts to a `"buy"|"sell"` variant via `sideIsSell`.** Flatten (risk-off) passes `"SELL"` so it gets the falling pitch. Keeps one type-safe input across all callers.
5. **The Sounds settings UI is a self-contained `SoundsSection` component** consuming `useSoundConfig()` directly and calling `SoundEngine.preview()`. `OrderSettingsModal` stays generic (pure props for `OrderConfig`); it only adds `<SoundsSection />`, and its config never touches `orderConfig`'s schema (separate `"soundConfig"` KV key), exactly as the spec requires.
6. **Patches are verified by ear (spec §Testing), so `patches.ts` has no audio unit test** — its verification gate is `typecheck` (the `Record<…SoundId, PatchFn>` types enforce completeness) + a node-safe no-op test proving `PatchPlayer` never throws when Web Audio is absent/suspended (the "drop, never queue" behavior).

## File Structure

```
ui/src/sound/
  SoundConfig.ts              CREATE  Task 1  types, defaults, id/label tables, sanitizer
  SoundConfig.test.ts         CREATE  Task 1  sanitizer / defaults
  patches.ts                  CREATE  Task 2  synthesis fns (1:1 port) + PatchPlayer + registries
  patches.test.ts             CREATE  Task 2  node-safe no-op / registry completeness
  SoundEngine.ts              CREATE  Task 3  singleton + decision logic; SoundApi/SoundSink interfaces
  SoundEngine.test.ts         CREATE  Task 3  coalescing, freshness, gating, variant, double-fire
  SoundConfigProvider.tsx     CREATE  Task 8  clone of useOrderConfig + push-to-engine effect
  SoundConfigProvider.test.tsx CREATE Task 8  load/default/save round-trip, engine push
  SoundsSection.tsx           CREATE  Task 9  settings UI section
  SoundsSection.test.tsx      CREATE  Task 9  render + save + preview
  useSoundWiring.ts           CREATE  Task 11 store→engine subscriptions + resume listener
  useSoundWiring.test.ts       CREATE  Task 11 subscribes, forwards, cleans up

ui/src/data/FillStore.ts            MODIFY  Task 4  + onNewFill
ui/src/data/FillStore.test.ts       MODIFY  Task 4
ui/src/data/ExecStore.ts            MODIFY  Task 5  + onOrderRejected
ui/src/data/ExecStore.test.ts       MODIFY  Task 5
ui/src/data/ScannerStore.ts         MODIFY  Task 6  + onNewHit
ui/src/data/ScannerStore.test.ts    MODIFY  Task 6
ui/src/chrome/exec/commands.ts      MODIFY  Task 7  + sound dep, accept/blocked triggers
ui/src/chrome/exec/commands.test.ts MODIFY  Task 7
ui/src/chrome/exec/useOrderCommands.ts MODIFY Task 7  default sound → singleton
ui/src/chrome/exec/OrderSettingsModal.tsx      MODIFY Task 10 render <SoundsSection/>
ui/src/chrome/exec/OrderSettingsModal.test.tsx MODIFY Task 10 wrap in SoundConfigProvider
ui/src/App.tsx                      MODIFY  Task 11 mount <SoundConfigProvider>
ui/src/chrome/AppShell.tsx          MODIFY  Task 11 call useSoundWiring(stores)
```

---

## Task 1: SoundConfig — types, defaults, labels, sanitizer

**Files:**
- Create: `ui/src/sound/SoundConfig.ts`
- Test: `ui/src/sound/SoundConfig.test.ts`

**Interfaces:**
- Produces: `SoundConfig`, `FillSoundId`, `RejectSoundId`, `ScannerSoundId`, `SOUND_CONFIG_KEY`, `DEFAULT_SOUND_CONFIG`, `FILL_SOUND_IDS`/`REJECT_SOUND_IDS`/`SCANNER_SOUND_IDS`, `FILL_SOUND_LABELS`/`REJECT_SOUND_LABELS`/`SCANNER_SOUND_LABELS`, `sanitizeSoundConfig(raw: unknown): SoundConfig`.

- [ ] **Step 1: Write the failing test**

`ui/src/sound/SoundConfig.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { DEFAULT_SOUND_CONFIG, sanitizeSoundConfig } from "./SoundConfig";

describe("sanitizeSoundConfig", () => {
  it("returns defaults for absent / non-object input", () => {
    expect(sanitizeSoundConfig(undefined)).toEqual(DEFAULT_SOUND_CONFIG);
    expect(sanitizeSoundConfig(null)).toEqual(DEFAULT_SOUND_CONFIG);
    expect(sanitizeSoundConfig("nope")).toEqual(DEFAULT_SOUND_CONFIG);
  });

  it("keeps valid fields and falls back per-field on invalid ones", () => {
    const out = sanitizeSoundConfig({
      enabled: false,
      volume: 2,               // out of range -> clamp to default
      fillSound: "marimba",    // valid
      placeClick: "yes",       // wrong type -> default
      rejectSound: "bogus",    // invalid id -> default
      scannerSound: "off",     // valid ("off" allowed)
    });
    expect(out.enabled).toBe(false);
    expect(out.volume).toBe(DEFAULT_SOUND_CONFIG.volume);
    expect(out.fillSound).toBe("marimba");
    expect(out.placeClick).toBe(DEFAULT_SOUND_CONFIG.placeClick);
    expect(out.rejectSound).toBe(DEFAULT_SOUND_CONFIG.rejectSound);
    expect(out.scannerSound).toBe("off");
  });

  it("clamps volume to [0,1]", () => {
    expect(sanitizeSoundConfig({ volume: -1 }).volume).toBe(0);
    expect(sanitizeSoundConfig({ volume: 0.3 }).volume).toBe(0.3);
  });

  it("DEFAULT_SOUND_CONFIG matches the approved spec", () => {
    expect(DEFAULT_SOUND_CONFIG).toEqual({
      enabled: true, volume: 0.6, fillSound: "twoTone",
      placeClick: true, rejectSound: "alertBeeps", scannerSound: "arpeggio",
    });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/sound/SoundConfig.test.ts`
Expected: FAIL — cannot find module `./SoundConfig`.

- [ ] **Step 3: Write the implementation**

`ui/src/sound/SoundConfig.ts`:

```ts
// Sound settings. Persisted engine-side under the generic KV command as key
// "soundConfig" (separate from "orderConfig" so each schema stays independent).
export type FillSoundId = "softBlip" | "twoTone" | "marimba" | "cashBell" | "tick" | "glassPing" | "pop";
export type RejectSoundId = "buzz" | "dunDun" | "doubleKnock" | "alertBeeps" | "powerDown";
export type ScannerSoundId = "sonarPing" | "arpeggio" | "chirp" | "highChime" | "singingBowl";

export interface SoundConfig {
  enabled: boolean;                     // master
  volume: number;                       // 0..1
  fillSound: FillSoundId | "off";
  placeClick: boolean;
  rejectSound: RejectSoundId | "off";
  scannerSound: ScannerSoundId | "off";
}

export const SOUND_CONFIG_KEY = "soundConfig";

export const DEFAULT_SOUND_CONFIG: SoundConfig = {
  enabled: true,
  volume: 0.6,
  fillSound: "twoTone",
  placeClick: true,
  rejectSound: "alertBeeps",
  scannerSound: "arpeggio",
};

export const FILL_SOUND_IDS: readonly FillSoundId[] = ["softBlip", "twoTone", "marimba", "cashBell", "tick", "glassPing", "pop"];
export const REJECT_SOUND_IDS: readonly RejectSoundId[] = ["buzz", "dunDun", "doubleKnock", "alertBeeps", "powerDown"];
export const SCANNER_SOUND_IDS: readonly ScannerSoundId[] = ["sonarPing", "arpeggio", "chirp", "highChime", "singingBowl"];

// Dropdown labels — match the audition-page (prototypes/fill-sounds.html) names verbatim.
export const FILL_SOUND_LABELS: Record<FillSoundId, string> = {
  softBlip: "Soft Blip", twoTone: "Two-Tone", marimba: "Marimba", cashBell: "Cash Bell",
  tick: "Tick", glassPing: "Glass Ping", pop: "Pop",
};
export const REJECT_SOUND_LABELS: Record<RejectSoundId, string> = {
  buzz: "Reject 1 — Buzz", dunDun: "Reject 2 — Dun-Dun", doubleKnock: "Reject 3 — Double Knock",
  alertBeeps: "Reject 4 — Alert Beeps", powerDown: "Reject 5 — Power-Down",
};
export const SCANNER_SOUND_LABELS: Record<ScannerSoundId, string> = {
  sonarPing: "Scan 1 — Sonar Ping", arpeggio: "Scan 2 — Arpeggio", chirp: "Scan 3 — Chirp",
  highChime: "Scan 4 — High Chime", singingBowl: "Scan 5 — Singing Bowl",
};

function oneOf<T extends string>(v: unknown, allowed: readonly T[], fallback: T): T {
  return typeof v === "string" && (allowed as readonly string[]).includes(v) ? (v as T) : fallback;
}
function optionOrOff<T extends string>(v: unknown, allowed: readonly T[], fallback: T | "off"): T | "off" {
  if (v === "off") return "off";
  return typeof v === "string" && (allowed as readonly string[]).includes(v) ? (v as T) : fallback;
}

export function sanitizeSoundConfig(raw: unknown): SoundConfig {
  if (!raw || typeof raw !== "object") return { ...DEFAULT_SOUND_CONFIG };
  const r = raw as Record<string, unknown>;
  const volume = typeof r.volume === "number" && r.volume >= 0 && r.volume <= 1 ? r.volume : DEFAULT_SOUND_CONFIG.volume;
  return {
    enabled: typeof r.enabled === "boolean" ? r.enabled : DEFAULT_SOUND_CONFIG.enabled,
    volume,
    fillSound: optionOrOff(r.fillSound, FILL_SOUND_IDS, DEFAULT_SOUND_CONFIG.fillSound),
    placeClick: typeof r.placeClick === "boolean" ? r.placeClick : DEFAULT_SOUND_CONFIG.placeClick,
    rejectSound: optionOrOff(r.rejectSound, REJECT_SOUND_IDS, DEFAULT_SOUND_CONFIG.rejectSound),
    scannerSound: optionOrOff(r.scannerSound, SCANNER_SOUND_IDS, DEFAULT_SOUND_CONFIG.scannerSound),
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/sound/SoundConfig.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Gate + commit**

Run: `cd ui && npm run typecheck && npm run lint`
Expected: clean.

```bash
git add ui/src/sound/SoundConfig.ts ui/src/sound/SoundConfig.test.ts
git commit -m "feat(ui/sound): SoundConfig types, defaults, labels, sanitizer"
```

---

## Task 2: patches.ts — synthesis functions (1:1 port) + PatchPlayer

**Files:**
- Create: `ui/src/sound/patches.ts`
- Test: `ui/src/sound/patches.test.ts`

**Interfaces:**
- Consumes: `FillSoundId`/`RejectSoundId`/`ScannerSoundId` from `./SoundConfig`.
- Produces:
  - `type Variant = "buy" | "sell"`
  - `type PatchFn = (ctx: AudioContext, out: AudioNode, variant: Variant, when: number) => void`
  - `FILL_PATCHES: Record<FillSoundId, PatchFn>`, `REJECT_PATCHES: Record<RejectSoundId, PatchFn>`, `SCANNER_PATCHES: Record<ScannerSoundId, PatchFn>`, `PLACE_CLICK: PatchFn`
  - `interface PatchPlayer { unlock(): void; setMasterVolume(v: number): void; play(fn: PatchFn, variant: Variant): void }`
  - `class WebAudioPatchPlayer implements PatchPlayer`
  - `resolvePatch(kind: "fill" | "place" | "reject" | "scanner", id: string): PatchFn | undefined`

- [ ] **Step 1: Port the shared helpers and patches verbatim from the prototype**

Open `prototypes/fill-sounds.html`. Copy these into `ui/src/sound/patches.ts` **without changing any numeric constant**:

1. The three shared synthesis helpers at lines **107–140** (`env`, `tone`, `noiseBurst`). Convert them to module-scope TypeScript functions. Their first parameter is the `AudioContext`; keep every argument and every constant exactly. Do **not** port `audio()` (lines 88–97) — the `AudioContext`/master-gain lifecycle is owned by `WebAudioPatchPlayer` below, not by the patches.
2. The 7 fill patch `play` bodies (lines 145–224), the placement-click `play` body (`EVENT_SOUNDS[0]`, lines 230–238), the 5 reject `play` bodies (lines 240–309), and the 5 scanner `play` bodies (lines 316–373).

Adapt each patch to the typed signature `PatchFn = (ctx, out, variant, when) => void`:
- Fill and place patches use `variant` (`"buy" | "sell"`) exactly where the prototype's `v` is used.
- Reject and scanner patches ignore `variant` (they are side-agnostic — the prototype gives them `variants: ['play']` / no variant).
- `when` replaces the prototype's `t` (schedule time in `ctx.currentTime` seconds).

Assemble into the exported registries. Skeleton (fill in every body by porting — the three shown are the exact verbatim targets, ported to TS):

```ts
import type { FillSoundId, RejectSoundId, ScannerSoundId } from "./SoundConfig";

export type Variant = "buy" | "sell";
export type PatchFn = (ctx: AudioContext, out: AudioNode, variant: Variant, when: number) => void;

// --- shared helpers: ported verbatim from prototypes/fill-sounds.html:107-140 ---
function env(c: AudioContext, when: number, peak: number, attack: number, decay: number): GainNode { /* port lines 107-113 verbatim */ }
function tone(c: AudioContext, type: OscillatorType, freq: number, when: number, peak: number, attack: number, decay: number, dest: AudioNode): void { /* port lines 115-124 verbatim */ }
function noiseBurst(c: AudioContext, when: number, peak: number, decay: number, filterType: BiquadFilterType, freq: number, q: number, dest: AudioNode): void { /* port lines 126-140 verbatim */ }

// --- fill patches (ported from lines 145-224) ---
const softBlip: PatchFn = (c, out, v, t) => { /* port lines 147-156 */ };
const twoTone: PatchFn = (c, out, v, t) => {                 // verbatim from lines 158-168
  const seq = v === "buy" ? [523.25, 783.99] : [783.99, 523.25];
  for (let i = 0; i < 2; i++) {
    const at = t + i * 0.095;
    tone(c, "sine", seq[i], at, 0.45, 0.004, 0.09, out);
    tone(c, "sine", seq[i] * 2, at, 0.08, 0.004, 0.06, out);
  }
};
const marimba: PatchFn = (c, out, v, t) => { /* port lines 170-177 */ };
const cashBell: PatchFn = (c, out, v, t) => { /* port lines 179-188 */ };
const tick: PatchFn = (c, out, v, t) => { /* port lines 190-197 */ };
const glassPing: PatchFn = (c, out, v, t) => { /* port lines 199-207 */ };
const pop: PatchFn = (c, out, v, t) => { /* port lines 209-223 */ };

// --- placement click (ported from EVENT_SOUNDS[0], lines 230-238) ---
export const PLACE_CLICK: PatchFn = (c, out, v, t) => { /* port lines 232-237 */ };

// --- reject patches (ported from lines 240-309); ignore variant ---
const buzz: PatchFn = (c, out, _v, t) => { /* port lines 242-255 */ };
const dunDun: PatchFn = (c, out, _v, t) => { /* port lines 260-264 */ };
const doubleKnock: PatchFn = (c, out, _v, t) => { /* port lines 269-280 */ };
const alertBeeps: PatchFn = (c, out, _v, t) => {            // verbatim from lines 283-293
  const lp = c.createBiquadFilter();
  lp.type = "lowpass"; lp.frequency.value = 2500;
  lp.connect(out);
  tone(c, "square", 987.77, t, 0.16, 0.003, 0.045, lp);
  tone(c, "square", 987.77, t + 0.075, 0.16, 0.003, 0.045, lp);
};
const powerDown: PatchFn = (c, out, _v, t) => { /* port lines 297-308 */ };

// --- scanner patches (ported from lines 316-373); ignore variant ---
const sonarPing: PatchFn = (c, out, _v, t) => { /* port lines 318-327 */ };
const arpeggio: PatchFn = (c, out, _v, t) => {              // verbatim from lines 330-340
  const notes = [523.25, 659.25, 783.99];
  notes.forEach((f, i) => {
    const at = t + i * 0.065;
    tone(c, "sine", f, at, 0.4, 0.0015, i === 2 ? 0.22 : 0.13, out);
    tone(c, "sine", f * 3.9, at, 0.1, 0.0015, 0.05, out);
  });
};
const chirp: PatchFn = (c, out, _v, t) => { /* port lines 344-354 */ };
const highChime: PatchFn = (c, out, _v, t) => { /* port lines 357-363 */ };
const singingBowl: PatchFn = (c, out, _v, t) => { /* port lines 366-372 */ };

export const FILL_PATCHES: Record<FillSoundId, PatchFn> = { softBlip, twoTone, marimba, cashBell, tick, glassPing, pop };
export const REJECT_PATCHES: Record<RejectSoundId, PatchFn> = { buzz, dunDun, doubleKnock, alertBeeps, powerDown };
export const SCANNER_PATCHES: Record<ScannerSoundId, PatchFn> = { sonarPing, arpeggio, chirp, highChime, singingBowl };

export function resolvePatch(kind: "fill" | "place" | "reject" | "scanner", id: string): PatchFn | undefined {
  if (kind === "place") return PLACE_CLICK;
  if (kind === "fill") return FILL_PATCHES[id as FillSoundId];
  if (kind === "reject") return REJECT_PATCHES[id as RejectSoundId];
  return SCANNER_PATCHES[id as ScannerSoundId];
}
```

> The `Record<…SoundId, PatchFn>` types make the compiler reject a missing or misspelled id — that is the completeness gate for the port.

- [ ] **Step 2: Add the real player (owns the AudioContext; node-safe)**

Append to `ui/src/sound/patches.ts`:

```ts
export interface PatchPlayer {
  unlock(): void;                       // lazily create + resume the AudioContext (call from a user gesture)
  setMasterVolume(v: number): void;     // master gain = v*v (perceptual taper)
  play(fn: PatchFn, variant: Variant): void; // schedule immediately; drop if context isn't running
}

export class WebAudioPatchPlayer implements PatchPlayer {
  private ctx: AudioContext | null = null;
  private master: GainNode | null = null;
  private volume = 0.6;

  unlock(): void {
    const Ctor = typeof AudioContext !== "undefined" ? AudioContext
      : typeof (globalThis as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext !== "undefined"
        ? (globalThis as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
        : null;
    if (!Ctor) return;                  // no Web Audio (SSR / node test env) -> no-op
    if (!this.ctx) {
      this.ctx = new Ctor();
      this.master = this.ctx.createGain();
      this.master.gain.value = this.volume * this.volume;
      this.master.connect(this.ctx.destination);
    }
    if (this.ctx.state === "suspended") void this.ctx.resume();
  }

  setMasterVolume(v: number): void {
    this.volume = v;
    if (this.master) this.master.gain.value = v * v;
  }

  play(fn: PatchFn, variant: Variant): void {
    if (!this.ctx || !this.master || this.ctx.state !== "running") return; // drop, never queue
    fn(this.ctx, this.master, variant, this.ctx.currentTime);
  }
}
```

- [ ] **Step 3: Write the node-safe test**

`ui/src/sound/patches.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { WebAudioPatchPlayer, FILL_PATCHES, REJECT_PATCHES, SCANNER_PATCHES, resolvePatch } from "./patches";
import { FILL_SOUND_IDS, REJECT_SOUND_IDS, SCANNER_SOUND_IDS } from "./SoundConfig";

describe("WebAudioPatchPlayer (node env, no Web Audio)", () => {
  it("unlock / setMasterVolume / play are safe no-ops when AudioContext is undefined", () => {
    const p = new WebAudioPatchPlayer();
    expect(() => p.unlock()).not.toThrow();
    expect(() => p.setMasterVolume(0.5)).not.toThrow();
    expect(() => p.play(() => { throw new Error("must not run"); }, "buy")).not.toThrow();
  });
});

describe("patch registries", () => {
  it("has a patch for every configured sound id", () => {
    for (const id of FILL_SOUND_IDS) expect(typeof FILL_PATCHES[id]).toBe("function");
    for (const id of REJECT_SOUND_IDS) expect(typeof REJECT_PATCHES[id]).toBe("function");
    for (const id of SCANNER_SOUND_IDS) expect(typeof SCANNER_PATCHES[id]).toBe("function");
    expect(typeof resolvePatch("place", "x")).toBe("function");
    expect(resolvePatch("fill", "bogus")).toBeUndefined();
  });
});
```

- [ ] **Step 4: Run the test + gates**

Run: `cd ui && npx vitest run src/sound/patches.test.ts && npm run typecheck && npm run lint`
Expected: tests PASS; typecheck clean (proves every id has a patch). Manually confirm no numeric constant differs from the prototype.

- [ ] **Step 5: Commit**

```bash
git add ui/src/sound/patches.ts ui/src/sound/patches.test.ts
git commit -m "feat(ui/sound): synthesis patches (1:1 port) + WebAudioPatchPlayer"
```

---

## Task 3: SoundEngine — decision logic + singleton

**Files:**
- Create: `ui/src/sound/SoundEngine.ts`
- Test: `ui/src/sound/SoundEngine.test.ts`

**Interfaces:**
- Consumes: `SoundConfig`/`DEFAULT_SOUND_CONFIG` from `./SoundConfig`; `PatchPlayer`/`WebAudioPatchPlayer`/`Variant`/`resolvePatch` from `./patches`; `Side` from `../wire/contract`; `sideIsSell` from `../wire/orderStatus`.
- Produces:
  - `interface SoundApi { orderPlaced(side: Side): void; orderRejected(): void }` (the subset `OrderCommands` needs)
  - `interface SoundSink { orderFilled(side: Side, tsMs: number): void; orderRejected(): void; scannerHit(): void; unlock(): void }` (the subset `useSoundWiring` needs)
  - `class SoundEngine` implementing both, plus `setConfig(cfg: SoundConfig): void` and `preview(kind: "fill" | "place" | "reject" | "scanner", id: string): void`
  - `const soundEngine: SoundEngine` (module singleton)

- [ ] **Step 1: Write the failing test**

`ui/src/sound/SoundEngine.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from "vitest";
import { SoundEngine } from "./SoundEngine";
import type { PatchPlayer, PatchFn, Variant } from "./patches";
import { DEFAULT_SOUND_CONFIG } from "./SoundConfig";

interface Played { fn: PatchFn; variant: Variant }
function fakePlayer() {
  const played: Played[] = [];
  const player: PatchPlayer & { played: Played[] } = {
    played,
    unlock: vi.fn(),
    setMasterVolume: vi.fn(),
    play: (fn, variant) => { played.push({ fn, variant }); },
  };
  return player;
}
function make(now: () => number) {
  const player = fakePlayer();
  const eng = new SoundEngine(player, now);
  eng.setConfig({ ...DEFAULT_SOUND_CONFIG });
  return { eng, player };
}

describe("SoundEngine", () => {
  let t = 0;
  const now = () => t;
  beforeEach(() => { t = 100_000; });

  it("orderFilled plays; buy and sell are separate channels (never mask)", () => {
    const { eng, player } = make(now);
    eng.orderFilled("BUY", t);
    eng.orderFilled("SELL", t);   // same instant, different channel -> both play
    expect(player.played).toHaveLength(2);
    expect(player.played[0].variant).toBe("buy");
    expect(player.played[1].variant).toBe("sell");
  });

  it("coalesces the same channel within 200ms and plays again after", () => {
    const { eng, player } = make(now);
    eng.orderFilled("BUY", t);            // plays
    t += 150; eng.orderFilled("BUY", t);  // suppressed (<200)
    t += 100; eng.orderFilled("BUY", t);  // 250ms since last play -> plays
    expect(player.played).toHaveLength(2);
  });

  it("freshness guard: a fill older than 10s is silent, a fresh one chimes", () => {
    const { eng, player } = make(now);
    eng.orderFilled("BUY", t - 10_001);   // stale
    eng.orderFilled("SELL", t - 5_000);   // fresh (within 10s)
    expect(player.played).toHaveLength(1);
    expect(player.played[0].variant).toBe("sell");
  });

  it("config gating: master off silences everything; per-event off silences that channel", () => {
    const { eng, player } = make(now);
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, enabled: false });
    eng.orderFilled("BUY", t); eng.orderRejected(); eng.scannerHit(); eng.orderPlaced("BUY");
    expect(player.played).toHaveLength(0);
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, fillSound: "off", placeClick: false });
    t += 1000; eng.orderFilled("BUY", t);   // fill off
    eng.orderPlaced("BUY");                  // placeClick off
    eng.orderRejected();                     // still on -> plays
    expect(player.played).toHaveLength(1);
  });

  it("orderPlaced maps SELL/SHORT to the sell variant, BUY/COVER to buy", () => {
    const { eng, player } = make(now);
    eng.orderPlaced("SELL");
    t += 300; eng.orderPlaced("BUY");
    expect(player.played.map((p) => p.variant)).toEqual(["sell", "buy"]);
  });

  it("double-fire absorption: ack-reject + stream-reject within 200ms play once", () => {
    const { eng, player } = make(now);
    eng.orderRejected();          // ack path (blocked)
    t += 50; eng.orderRejected(); // stream path (REJECTED) shortly after
    expect(player.played).toHaveLength(1);
  });

  it("preview bypasses gating but not availability", () => {
    const { eng, player } = make(now);
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, enabled: false, scannerSound: "off" });
    eng.preview("scanner", "arpeggio");   // gating bypassed
    expect(player.played).toHaveLength(1);
  });

  it("setConfig pushes volume into the player", () => {
    const { eng, player } = make(now);
    eng.setConfig({ ...DEFAULT_SOUND_CONFIG, volume: 0.4 });
    expect(player.setMasterVolume).toHaveBeenLastCalledWith(0.4);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/sound/SoundEngine.test.ts`
Expected: FAIL — cannot find module `./SoundEngine`.

- [ ] **Step 3: Write the implementation**

`ui/src/sound/SoundEngine.ts`:

```ts
import type { Side } from "../wire/contract";
import { sideIsSell } from "../wire/orderStatus";
import { DEFAULT_SOUND_CONFIG, type SoundConfig } from "./SoundConfig";
import { WebAudioPatchPlayer, resolvePatch, type PatchPlayer, type Variant } from "./patches";

const COALESCE_MS = 200;
const FILL_FRESHNESS_MS = 10_000;

export interface SoundApi {
  orderPlaced(side: Side): void;
  orderRejected(): void;
}
export interface SoundSink {
  orderFilled(side: Side, tsMs: number): void;
  orderRejected(): void;
  scannerHit(): void;
  unlock(): void;
}

export class SoundEngine implements SoundApi, SoundSink {
  private cfg: SoundConfig = { ...DEFAULT_SOUND_CONFIG };
  private readonly lastPlay = new Map<string, number>();

  constructor(
    private readonly player: PatchPlayer = new WebAudioPatchPlayer(),
    private readonly now: () => number = () => Date.now(),
  ) {}

  unlock(): void { this.player.unlock(); }

  setConfig(cfg: SoundConfig): void {
    this.cfg = cfg;
    this.player.setMasterVolume(cfg.volume);
  }

  orderPlaced(side: Side): void {
    if (!this.cfg.enabled || !this.cfg.placeClick) return;
    const variant: Variant = sideIsSell(side) ? "sell" : "buy";
    this.fire(`place:${variant}`, "place", "click", variant);
  }

  orderFilled(side: Side, tsMs: number): void {
    if (!this.cfg.enabled || this.cfg.fillSound === "off") return;
    if (tsMs < this.now() - FILL_FRESHNESS_MS) return; // stale backfill stays silent
    const variant: Variant = sideIsSell(side) ? "sell" : "buy";
    this.fire(`fill:${variant}`, "fill", this.cfg.fillSound, variant);
  }

  orderRejected(): void {
    if (!this.cfg.enabled || this.cfg.rejectSound === "off") return;
    this.fire("reject", "reject", this.cfg.rejectSound, "buy");
  }

  scannerHit(): void {
    if (!this.cfg.enabled || this.cfg.scannerSound === "off") return;
    this.fire("scanner", "scanner", this.cfg.scannerSound, "buy");
  }

  preview(kind: "fill" | "place" | "reject" | "scanner", id: string): void {
    const fn = resolvePatch(kind, id);
    if (fn) this.player.play(fn, "buy"); // bypasses gating + coalescing; master volume still applies
  }

  // Per-channel 200ms coalescing gate, then delegate to the player.
  private fire(channel: string, kind: "fill" | "place" | "reject" | "scanner", id: string, variant: Variant): void {
    const t = this.now();
    const last = this.lastPlay.get(channel);
    if (last !== undefined && t - last < COALESCE_MS) return;
    this.lastPlay.set(channel, t);
    const fn = resolvePatch(kind, id);
    if (fn) this.player.play(fn, variant);
  }
}

export const soundEngine = new SoundEngine();
```

> Note: for `kind === "place"`, `resolvePatch` returns `PLACE_CLICK` regardless of the `id` arg, so `"click"` is a harmless placeholder.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/sound/SoundEngine.test.ts`
Expected: PASS (8 tests).

- [ ] **Step 5: Gate + commit**

Run: `cd ui && npm run typecheck && npm run lint`

```bash
git add ui/src/sound/SoundEngine.ts ui/src/sound/SoundEngine.test.ts
git commit -m "feat(ui/sound): SoundEngine decision logic + singleton"
```

---

## Task 4: FillStore.onNewFill

**Files:**
- Modify: `ui/src/data/FillStore.ts`
- Test: `ui/src/data/FillStore.test.ts`

**Interfaces:**
- Consumes: existing `Fill` type; `ingest()` dedup loop.
- Produces: `onNewFill(cb: (fill: Fill) => void): () => void` on `FillStore`. Fires once per newly-ingested (dedup-passing) fill, for **both** snapshot and delta payloads.

- [ ] **Step 1: Write the failing test**

**Do not paste a fresh import/builder block.** `ui/src/data/FillStore.test.ts` already has `import { describe, it, expect } from "vitest"`, imports `FillStore`, and defines a `fill(o: Partial<Fill>): Fill` builder (its arg is **required**). Reconcile before adding tests:
1. Add `vi` to the existing vitest import line → `import { describe, it, expect, vi } from "vitest";` (do **not** add a second import).
2. Reuse the existing `fill(...)` builder — pass `{}` or overrides (never zero args).
3. Append only the new `describe` block below.

```ts
describe("FillStore.onNewFill", () => {
  it("fires once per newly-ingested fill and never for deduped re-ingests", () => {
    const s = new FillStore();
    const cb = vi.fn();
    s.onNewFill(cb);
    s.apply({ kind: "delta", topic: "exec.fills", payload: fill({}) });
    s.apply({ kind: "delta", topic: "exec.fills", payload: fill({}) }); // identical -> deduped
    expect(cb).toHaveBeenCalledTimes(1);
    expect(cb.mock.calls[0][0]).toMatchObject({ orderId: expect.any(String) });
  });

  it("fires for snapshot-merged fills (freshness is the downstream concern)", () => {
    const s = new FillStore();
    const cb = vi.fn();
    s.onNewFill(cb);
    s.apply({ kind: "snapshot", topic: "exec.fills", payload: [fill({ orderId: "a" }), fill({ orderId: "b" })] });
    expect(cb).toHaveBeenCalledTimes(2);
  });

  it("returns an unsubscribe that stops further calls", () => {
    const s = new FillStore();
    const cb = vi.fn();
    const off = s.onNewFill(cb);
    off();
    s.apply({ kind: "delta", topic: "exec.fills", payload: fill({}) });
    expect(cb).not.toHaveBeenCalled();
  });
});
```

> The existing `fill()` builder's default field values are unknown here, so assert structurally (`toMatchObject({ orderId: expect.any(String) })`), not on specific values. `fill({ orderId: "a" })` / `fill({ orderId: "b" })` force distinct dedup keys for the snapshot test. The `{ kind, topic, payload }` message literals were verified to typecheck against `SnapshotMsg | DeltaMsg`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/FillStore.test.ts`
Expected: FAIL — `s.onNewFill is not a function`.

- [ ] **Step 3: Add the hook**

In `ui/src/data/FillStore.ts`, add a listener set and fire it inside the dedup loop, right after a fill is confirmed new:

```ts
export class FillStore extends PaintStore {
  private readonly bySymbol = new Map<string, Fill[]>();
  private readonly seen = new Set<string>();
  private readonly fillListeners = new Set<(fill: Fill) => void>();

  /** Fires once per newly-ingested fill (snapshot or delta), after dedup. */
  onNewFill(cb: (fill: Fill) => void): () => void {
    this.fillListeners.add(cb);
    return () => { this.fillListeners.delete(cb); };
  }

  // ...apply() unchanged...

  ingest(fills: Fill[]): void {
    let changed = false;
    for (const f of fills) {
      const k = key(f);
      if (this.seen.has(k)) continue;
      this.seen.add(k);
      const arr = this.bySymbol.get(f.symbol) ?? [];
      arr.push(f);
      arr.sort((a, b) => a.tsMs - b.tsMs);
      this.bySymbol.set(f.symbol, arr);
      changed = true;
      for (const cb of this.fillListeners) cb(f); // notify per new fill
    }
    if (changed) this.markDirty();
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/data/FillStore.test.ts`
Expected: PASS (existing tests + 3 new).

- [ ] **Step 5: Gate + commit**

Run: `cd ui && npm run typecheck && npm run lint`

```bash
git add ui/src/data/FillStore.ts ui/src/data/FillStore.test.ts
git commit -m "feat(ui/data): FillStore.onNewFill discrete-event hook"
```

---

## Task 5: ExecStore.onOrderRejected

**Files:**
- Modify: `ui/src/data/ExecStore.ts`
- Test: `ui/src/data/ExecStore.test.ts`

**Interfaces:**
- Consumes: existing `Order` type; the `case "exec.orders"` handler.
- Produces: `onOrderRejected(cb: (order: Order) => void): () => void`. Fires only on an observed status *transition* to `"REJECTED"` on a **delta** frame (snapshots seed silently; unchanged `REJECTED` rows do not re-fire).

- [ ] **Step 1: Write the failing test**

**Do not paste a fresh import/builder block.** `ui/src/data/ExecStore.test.ts` already has `import { describe, it, expect } from "vitest"`, imports `ExecStore`, and defines an `order(id: string, over: Partial<Order> = {}): Order` builder (**id-first, 2-arg**). Reconcile before adding tests:
1. Add `vi` to the existing vitest import line (do **not** add a second import).
2. Reuse the existing `order("id", { ... })` builder — id is the first positional arg, overrides second.
3. Construct `ExecStore` exactly as the existing tests in the file do (the plan assumes a no-arg `new ExecStore()`; if the file uses a helper/constructor arg, match it).
4. Append only the new `describe` block below.

```ts
describe("ExecStore.onOrderRejected", () => {
  it("fires on a delta transition into REJECTED", () => {
    const s = new ExecStore();
    const cb = vi.fn();
    s.onOrderRejected(cb);
    s.apply({ kind: "delta", topic: "exec.orders", payload: order("o1", { status: "SUBMITTED" }) });
    s.apply({ kind: "delta", topic: "exec.orders", payload: order("o1", { status: "REJECTED", rejectReason: "no shares" }) });
    expect(cb).toHaveBeenCalledTimes(1);
    expect(cb).toHaveBeenCalledWith(expect.objectContaining({ id: "o1", status: "REJECTED" }));
  });

  it("does not fire when a REJECTED row seeds via snapshot", () => {
    const s = new ExecStore();
    const cb = vi.fn();
    s.onOrderRejected(cb);
    s.apply({ kind: "snapshot", topic: "exec.orders", payload: [order("o1", { status: "REJECTED" })] });
    expect(cb).not.toHaveBeenCalled();
  });

  it("does not re-fire when an already-REJECTED row is re-sent unchanged (delta)", () => {
    const s = new ExecStore();
    const cb = vi.fn();
    s.onOrderRejected(cb);
    s.apply({ kind: "delta", topic: "exec.orders", payload: order("o1", { status: "REJECTED" }) }); // no prior row -> fires once
    s.apply({ kind: "delta", topic: "exec.orders", payload: order("o1", { status: "REJECTED" }) }); // unchanged -> silent
    expect(cb).toHaveBeenCalledTimes(1);
  });
});
```

> The `{ kind, topic, payload }` message literals were verified to typecheck against `SnapshotMsg | DeltaMsg`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/ExecStore.test.ts`
Expected: FAIL — `s.onOrderRejected is not a function`.

- [ ] **Step 3: Add the hook + transition detection**

In `ui/src/data/ExecStore.ts`, add the listener set and diff previous status before the map overwrite:

```ts
export class ExecStore extends ReactStore<ExecState> {
  private readonly rejectListeners = new Set<(order: Order) => void>();

  onOrderRejected(cb: (order: Order) => void): () => void {
    this.rejectListeners.add(cb);
    return () => { this.rejectListeners.delete(cb); };
  }

  // ...inside apply(), the case "exec.orders" block:
  //   case "exec.orders": {
  //     const orders = new Map(cur.orders);
  //     const optimistic = new Map(cur.optimistic);
  //     const list = m.kind === "snapshot" ? (m.payload as Order[]) : [m.payload as Order];
  //     if (m.kind === "snapshot") orders.clear();
  //     for (const o of list) {
  //       if (m.kind === "delta" && o.status === "REJECTED" && cur.orders.get(o.id)?.status !== "REJECTED") {
  //         for (const cb of this.rejectListeners) cb(o);   // transition into REJECTED (never on snapshot)
  //       }
  //       orders.set(o.id, o);
  //       optimistic.delete(o.id);
  //     }
  //     this.set({ ...cur, orders, optimistic });
  //     return;
  //   }
}
```

Apply the transition check inside the existing loop exactly as shown in the comment (read `cur.orders.get(o.id)?.status` — the pre-update map — before `orders.set`). Snapshots are excluded by the `m.kind === "delta"` guard, and snapshots also `orders.clear()` so there is no prior row anyway.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/data/ExecStore.test.ts`
Expected: PASS (existing + 3 new).

- [ ] **Step 5: Gate + commit**

Run: `cd ui && npm run typecheck && npm run lint`

```bash
git add ui/src/data/ExecStore.ts ui/src/data/ExecStore.test.ts
git commit -m "feat(ui/data): ExecStore.onOrderRejected transition hook"
```

---

## Task 6: ScannerStore.onNewHit

**Files:**
- Modify: `ui/src/data/ScannerStore.ts`
- Test: `ui/src/data/ScannerStore.test.ts`

**Interfaces:**
- Consumes: existing `apply()` rank + `applyHit()` force-flash paths; the per-session `seen` set.
- Produces: `onNewHit(cb: (symbol: string) => void): () => void`. Fires for a delta rank row whose symbol isn't yet in the session seen-set, and for every `scanner.hit` force-flash (even already-seen symbols). Silent on snapshots and for already-seen symbols on ordinary refreshes.

- [ ] **Step 1: Write the failing test**

**Do not paste a fresh import/builder block.** `ui/src/data/ScannerStore.test.ts` already has `import { describe, it, expect } from "vitest"`, imports `ScannerStore`, and defines a `rank(kind, session, payload)` builder (**3-arg**). To avoid colliding with it, the new tests define a distinctly-named, self-contained `rankMsg` helper (it does **not** depend on the existing `rank`). Reconcile before adding tests:
1. Add `vi` to the existing vitest import line (do **not** add a second import).
2. Append the `rankMsg` helper + the `describe` block below.

```ts
// Distinct name to avoid colliding with the file's existing `rank(kind, session, payload)`.
const rankMsg = (kind: "snapshot" | "delta", symbols: string[]) => ({
  kind, topic: "scanner.rank" as const, key: "premarket",
  payload: { refreshedAt: "2026-07-06T13:00:00Z", rows: symbols.map((symbol) => ({ symbol, changePct: 5, last: 1, floatShares: 1, volume: 1 })) },
});

describe("ScannerStore.onNewHit", () => {
  it("fires for a delta row whose symbol is not yet seen", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rankMsg("delta", ["AAA"]));            // new -> fires
    s.apply(rankMsg("delta", ["AAA", "BBB"]));     // AAA seen (silent), BBB new (fires)
    expect(cb.mock.calls.map((c) => c[0])).toEqual(["AAA", "BBB"]);
  });

  it("is silent on snapshots and for already-seen symbols", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rankMsg("snapshot", ["AAA"]));  // seeds silently
    s.apply(rankMsg("delta", ["AAA"]));     // already seen
    expect(cb).not.toHaveBeenCalled();
  });

  it("fires on a scanner.hit force-flash even for an already-seen symbol", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rankMsg("snapshot", ["AAA"]));  // AAA now seen, silent
    s.apply({ kind: "delta", topic: "scanner.hit", key: "premarket", payload: { symbol: "AAA", at: "2026-07-06T13:01:00Z" } });
    expect(cb).toHaveBeenCalledWith("AAA");
  });
});
```

> The `rankMsg` literal and the `scanner.hit` message were verified to typecheck against `SnapshotMsg | DeltaMsg`. Cross-check the existing `rank(kind, session, payload)` builder's actual signature when you touch the file, in case it has drifted.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/ScannerStore.test.ts`
Expected: FAIL — `s.onNewHit is not a function`.

- [ ] **Step 3: Add the hook at both new-hit sites**

In `ui/src/data/ScannerStore.ts`:

```ts
export class ScannerStore extends ReactStore<ScannerState> {
  private readonly hitListeners = new Set<(symbol: string) => void>();

  onNewHit(cb: (symbol: string) => void): () => void {
    this.hitListeners.add(cb);
    return () => { this.hitListeners.delete(cb); };
  }

  // In apply(), the rank branch — fire alongside the existing isNewHit computation,
  // BEFORE `seen.add`, only on delta:
  //   const view = rows.map((row) => {
  //     const isNewHit = m.kind === "delta" && !seen.has(row.symbol);
  //     const muted = m.kind === "delta" && seen.has(row.symbol);
  //     if (isNewHit) for (const cb of this.hitListeners) cb(row.symbol);
  //     return { ...row, isNewHit, muted };
  //   });

  // In applyHit(), fire unconditionally (force-flash sounds even for seen symbols):
  //   private applyHit(session, hit) {
  //     for (const cb of this.hitListeners) cb(hit.symbol);
  //     this.seenFor(session).add(hit.symbol);
  //     ...unchanged...
  //   }
}
```

Apply the two firing sites exactly as commented. In the rank branch, fire inside the `rows.map` where `isNewHit` is true (before the `for (const row of rows) seen.add(...)` line). In `applyHit`, fire at the top, before the `seen.add`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/data/ScannerStore.test.ts`
Expected: PASS (existing + 3 new).

- [ ] **Step 5: Gate + commit**

Run: `cd ui && npm run typecheck && npm run lint`

```bash
git add ui/src/data/ScannerStore.ts ui/src/data/ScannerStore.test.ts
git commit -m "feat(ui/data): ScannerStore.onNewHit hook (rank + force-flash)"
```

---

## Task 7: OrderCommands sound triggers (place / reject via ack)

**Files:**
- Modify: `ui/src/chrome/exec/commands.ts`
- Modify: `ui/src/chrome/exec/useOrderCommands.ts`
- Test: `ui/src/chrome/exec/commands.test.ts`

**Interfaces:**
- Consumes: `SoundApi` from `../../sound/SoundEngine`; `soundEngine` singleton; existing `AckMsg` (`status: "accepted" | "blocked"`).
- Produces: `OrderCommandsDeps` gains `sound?: SoundApi`. `submit` accept → `orderPlaced(args.side)`, blocked → `orderRejected()`; `flatten` accept → `orderPlaced("SELL")`, blocked → `orderRejected()`; `cancel`/`replace` blocked → `orderRejected()`. `arm`/`disarm`/`kill` stay silent. `useOrderCommands` defaults `sound` to `soundEngine`.

- [ ] **Step 1: Write the failing test**

Append to `ui/src/chrome/exec/commands.test.ts`, reusing its existing `fakes()` / `CommandAdapter` helper. Add a fake `sound` and assert:

```ts
import { vi, describe, it, expect } from "vitest";
import { OrderCommands } from "./commands";
import type { SoundApi } from "../../sound/SoundEngine";
// reuse the file's existing fakes() for cmd/exec/toast; if it doesn't accept ack overrides
// for every command, extend it minimally as shown in the file.

function soundSpy(): SoundApi & { placed: string[]; rejected: number } {
  const s = { placed: [] as string[], rejected: 0,
    orderPlaced: (side: string) => { s.placed.push(side); },
    orderRejected: () => { s.rejected += 1; } };
  return s as SoundApi & { placed: string[]; rejected: number };
}

describe("OrderCommands sound triggers", () => {
  it("submit accepted -> orderPlaced(side); blocked -> orderRejected", async () => {
    const sound = soundSpy();
    const okCmd = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", orderId: "x" })) };
    const oc = new OrderCommands({ cmd: okCmd as never, exec: { addOptimistic: vi.fn() } as never, toast: { push: vi.fn() } as never, now: () => 0, sound });
    await oc.submit({ venue: "alpaca", symbol: "AAPL", side: "SELL", type: "LIMIT", tif: "DAY", qty: 1, limitPrice: 1, stopPrice: 0 }, "flash");
    expect(sound.placed).toEqual(["SELL"]);

    const blockCmd = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "blocked", reason: "disarmed" })) };
    const oc2 = new OrderCommands({ cmd: blockCmd as never, exec: {} as never, toast: { push: vi.fn() } as never, now: () => 0, sound });
    await oc2.submit({ venue: "alpaca", symbol: "AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 1, limitPrice: 1, stopPrice: 0 }, "flash");
    expect(sound.rejected).toBe(1);
  });

  it("flatten accepted -> orderPlaced('SELL'); cancel/replace blocked -> orderRejected", async () => {
    const sound = soundSpy();
    const okCmd = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted" })) };
    const oc = new OrderCommands({ cmd: okCmd as never, exec: {} as never, toast: { push: vi.fn() } as never, now: () => 0, sound });
    await oc.flatten("alpaca");
    expect(sound.placed).toEqual(["SELL"]);

    const blockCmd = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "blocked" })) };
    const oc2 = new OrderCommands({ cmd: blockCmd as never, exec: {} as never, toast: { push: vi.fn() } as never, now: () => 0, sound });
    await oc2.cancel("alpaca", "o1");
    await oc2.replace({ venue: "alpaca", orderId: "o1", qty: 1, limitPrice: 1, stopPrice: 0 });
    expect(sound.rejected).toBe(2);
  });
});
```

> Prefer the file's own `fakes()` helper where it fits; the inline deps above are the minimum shape if the helper doesn't cover ack overrides for `cancel`/`replace`/`flatten`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/exec/commands.test.ts`
Expected: FAIL — `sound` not in deps / no sound calls.

- [ ] **Step 3: Thread the sound dep through commands.ts**

Edit `ui/src/chrome/exec/commands.ts`:

```ts
import type { SoundApi } from "../../sound/SoundEngine";

export interface OrderCommandsDeps { cmd: CommandAdapter; exec: ExecStore; toast: ToastApi; now: () => number; sound?: SoundApi }

export class OrderCommands {
  constructor(private readonly d: OrderCommandsDeps) {}

  async submit(args: SubmitOrderArgs, flash: string): Promise<void> {
    const ack = await this.d.cmd.sendCommand("SubmitOrder", args);
    if (ack.status === "blocked") {
      this.d.toast.push({ level: "danger", text: `Blocked: ${ack.reason ?? "unknown"}` });
      this.d.sound?.orderRejected();
      return;
    }
    if (ack.orderId) this.d.exec.addOptimistic({ args, id: ack.orderId, createdMs: this.d.now() });
    this.d.sound?.orderPlaced(args.side);
    this.d.toast.push({ level: "info", text: flash });
  }

  async cancel(venue: VenueID, orderId: string): Promise<void> {
    const ack = await this.d.cmd.sendCommand("CancelOrder", { venue, orderId });
    if (ack.status === "blocked") this.d.sound?.orderRejected();
  }
  async replace(args: ReplaceOrderArgs): Promise<void> {
    const ack = await this.d.cmd.sendCommand("ReplaceOrder", args);
    if (ack.status === "blocked") this.d.sound?.orderRejected();
  }
  async flatten(venue: VenueID): Promise<void> {
    const ack = await this.d.cmd.sendCommand("Flatten", { venue });
    if (ack.status === "blocked") this.d.sound?.orderRejected();
    else this.d.sound?.orderPlaced("SELL"); // risk-off: falling pitch
  }

  // arm / disarm / kill unchanged — deliberately silent.
}
```

> `cancelLast`/`cancelAll` call `this.cancel(...)`, so a blocked cancel there also plays the reject sound (coalesced) — no extra code.

- [ ] **Step 4: Default the singleton in useOrderCommands.ts**

Edit `ui/src/chrome/exec/useOrderCommands.ts` (keeps all 5 existing call sites unchanged):

```ts
import { useMemo } from "react";
import { OrderCommands, type CommandAdapter } from "./commands";
import type { ExecStore } from "../../data/ExecStore";
import type { ToastApi } from "../Toast";
import { soundEngine, type SoundApi } from "../../sound/SoundEngine";

export function useOrderCommands(
  cmd: CommandAdapter, exec: ExecStore, toast: ToastApi,
  now: () => number = () => Date.now(), sound: SoundApi = soundEngine,
): OrderCommands {
  return useMemo(() => new OrderCommands({ cmd, exec, toast, now, sound }), [cmd, exec, toast, now, sound]);
}
```

- [ ] **Step 5: Run test + full suite to verify no regressions**

Run: `cd ui && npx vitest run src/chrome/exec/commands.test.ts && npm run typecheck && npm run lint`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ui/src/chrome/exec/commands.ts ui/src/chrome/exec/useOrderCommands.ts ui/src/chrome/exec/commands.test.ts
git commit -m "feat(ui/exec): OrderCommands place/reject sound triggers via ack"
```

---

## Task 8: SoundConfigProvider (KV persistence + push into engine)

**Files:**
- Create: `ui/src/sound/SoundConfigProvider.tsx`
- Test: `ui/src/sound/SoundConfigProvider.test.tsx`

**Interfaces:**
- Consumes: `SoundConfig`/`DEFAULT_SOUND_CONFIG`/`SOUND_CONFIG_KEY`/`sanitizeSoundConfig` from `./SoundConfig`; `soundEngine` from `./SoundEngine`; `AckMsg` from `../wire/contract`.
- Produces: `SoundConfigProvider({ commands, children })`, `useSoundConfig(): SoundConfigApi` where `SoundConfigApi = { config: SoundConfig; loaded: boolean; save(next: SoundConfig): void }`.

- [ ] **Step 1: Write the failing test**

`ui/src/sound/SoundConfigProvider.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor, fireEvent, act } from "@testing-library/react";
import { SoundConfigProvider, useSoundConfig } from "./SoundConfigProvider";
import { soundEngine } from "./SoundEngine";

function Probe() {
  const { config, loaded, save } = useSoundConfig();
  return (
    <div>
      <span data-testid="loaded">{String(loaded)}</span>
      <span data-testid="fill">{config.fillSound}</span>
      <button data-testid="save" onClick={() => save({ ...config, fillSound: "marimba" })}>save</button>
    </div>
  );
}

afterEach(() => vi.restoreAllMocks());

describe("SoundConfigProvider", () => {
  it("loads config from GetConfig and defaults on a malformed value", async () => {
    const commands = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", value: { fillSound: "marimba", volume: 0.5, enabled: true, placeClick: true, rejectSound: "buzz", scannerSound: "chirp" } })) };
    render(<SoundConfigProvider commands={commands as never}><Probe /></SoundConfigProvider>);
    await waitFor(() => expect(screen.getByTestId("loaded").textContent).toBe("true"));
    expect(screen.getByTestId("fill").textContent).toBe("marimba");
  });

  it("save() writes SetConfig and pushes the config into the engine", async () => {
    const setSpy = vi.spyOn(soundEngine, "setConfig");
    const commands = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", value: undefined })) };
    render(<SoundConfigProvider commands={commands as never}><Probe /></SoundConfigProvider>);
    await waitFor(() => expect(screen.getByTestId("loaded").textContent).toBe("true"));
    act(() => { fireEvent.click(screen.getByTestId("save")); });
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ fillSound: "marimba" }) });
    expect(setSpy).toHaveBeenLastCalledWith(expect.objectContaining({ fillSound: "marimba" }));
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/sound/SoundConfigProvider.test.tsx`
Expected: FAIL — cannot find module `./SoundConfigProvider`.

- [ ] **Step 3: Write the provider (clone of useOrderConfig + engine-push effect)**

`ui/src/sound/SoundConfigProvider.tsx`:

```tsx
// Sound settings provider — mirrors chrome/exec/useOrderConfig.tsx, plus an effect
// that pushes every config change into the SoundEngine singleton.
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import type { AckMsg } from "../wire/contract";
import { DEFAULT_SOUND_CONFIG, SOUND_CONFIG_KEY, sanitizeSoundConfig, type SoundConfig } from "./SoundConfig";
import { soundEngine } from "./SoundEngine";

interface Cmd { sendCommand(name: string, args: unknown): Promise<AckMsg> }
export interface SoundConfigApi { config: SoundConfig; loaded: boolean; save(next: SoundConfig): void }

const Ctx = createContext<SoundConfigApi | null>(null);

export function SoundConfigProvider({ commands, children }: { commands: Cmd; children: ReactNode }): JSX.Element {
  const [config, setConfig] = useState<SoundConfig>(DEFAULT_SOUND_CONFIG);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let live = true;
    void commands.sendCommand("GetConfig", { key: SOUND_CONFIG_KEY }).then((ack) => {
      if (!live) return;
      if (ack.status === "accepted") setConfig(sanitizeSoundConfig(ack.value));
      setLoaded(true);
    });
    return () => { live = false; };
  }, [commands]);

  // Push config into the imperative engine whenever it changes (incl. the initial load).
  useEffect(() => { soundEngine.setConfig(config); }, [config]);

  const save = useCallback((next: SoundConfig) => {
    setConfig(next);
    void commands.sendCommand("SetConfig", { key: SOUND_CONFIG_KEY, value: next });
  }, [commands]);

  return <Ctx.Provider value={{ config, loaded, save }}>{children}</Ctx.Provider>;
}

export function useSoundConfig(): SoundConfigApi {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useSoundConfig must be used within a SoundConfigProvider");
  return ctx;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/sound/SoundConfigProvider.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Gate + commit**

Run: `cd ui && npm run typecheck && npm run lint`

```bash
git add ui/src/sound/SoundConfigProvider.tsx ui/src/sound/SoundConfigProvider.test.tsx
git commit -m "feat(ui/sound): SoundConfigProvider KV persistence + engine push"
```

---

## Task 9: SoundsSection settings component

**Files:**
- Create: `ui/src/sound/SoundsSection.tsx`
- Test: `ui/src/sound/SoundsSection.test.tsx`

**Interfaces:**
- Consumes: `useSoundConfig` from `./SoundConfigProvider`; the id/label tables from `./SoundConfig`; `soundEngine` from `./SoundEngine`; `useTheme` from `../chrome/ThemeProvider` (verify the exact import path/name against `OrderSettingsModal.tsx`).
- Produces: `SoundsSection(): JSX.Element` — master enable toggle, fill/reject/scanner dropdowns (each with an `"off"` option), placement-click toggle, volume slider, and a `▶` preview button per sound row. Every control persists via `useSoundConfig().save`; previews call `soundEngine.preview(kind, id)`.

- [ ] **Step 1: Write the failing test**

`ui/src/sound/SoundsSection.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { SoundConfigProvider } from "./SoundConfigProvider";
import { SoundsSection } from "./SoundsSection";
import { soundEngine } from "./SoundEngine";
import { ThemeProvider } from "../chrome/ThemeProvider"; // verify path against OrderSettingsModal.tsx

function wrap() {
  const commands = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", value: undefined })) };
  render(<ThemeProvider><SoundConfigProvider commands={commands as never}><SoundsSection /></SoundConfigProvider></ThemeProvider>);
  return { commands };
}

afterEach(() => vi.restoreAllMocks());

describe("SoundsSection", () => {
  it("saves a changed fill sound via SetConfig", async () => {
    const { commands } = wrap();
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledWith("GetConfig", { key: "soundConfig" }));
    fireEvent.change(screen.getByTestId("sound-fill"), { target: { value: "marimba" } });
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ fillSound: "marimba" }) });
  });

  it("preview button calls the engine", () => {
    const spy = vi.spyOn(soundEngine, "preview");
    wrap();
    fireEvent.click(screen.getByTestId("sound-preview-fill"));
    expect(spy).toHaveBeenCalledWith("fill", expect.any(String));
  });

  it("toggling master enable persists", async () => {
    const { commands } = wrap();
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId("sound-enabled"));
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ enabled: false }) });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/sound/SoundsSection.test.tsx`
Expected: FAIL — cannot find module `./SoundsSection`.

- [ ] **Step 3: Write the component**

`ui/src/sound/SoundsSection.tsx` — follow the `OrderSettingsModal.tsx` inline-`style`/`inp`/`useTheme().palette` idiom (no new UI-kit). Every control writes through `save(...)`; each dropdown has an `off` option; each sound row has a `▶` preview button. `data-testid`s: `sound-enabled`, `sound-fill`, `sound-reject`, `sound-scanner`, `sound-place`, `sound-volume`, `sound-preview-fill`, `sound-preview-reject`, `sound-preview-scanner`.

```tsx
import { useSoundConfig } from "./SoundConfigProvider";
import { soundEngine } from "./SoundEngine";
import {
  FILL_SOUND_IDS, REJECT_SOUND_IDS, SCANNER_SOUND_IDS,
  FILL_SOUND_LABELS, REJECT_SOUND_LABELS, SCANNER_SOUND_LABELS,
} from "./SoundConfig";
import { useTheme } from "../chrome/ThemeProvider"; // verify exact export/path

export function SoundsSection(): JSX.Element {
  const { config, save } = useSoundConfig();
  const { palette } = useTheme();
  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, fontSize: 12, padding: "1px 4px" } as const;
  const row = { display: "flex", gap: 6, alignItems: "center", padding: "3px 0", borderTop: `1px solid ${palette.border}` } as const;
  const preview = (kind: "fill" | "reject" | "scanner", id: string) => () => soundEngine.preview(kind, id === "off" ? defaultFor(kind) : id);

  return (
    <div>
      <div style={{ fontWeight: 700, marginTop: 8 }}>Sounds</div>

      <label style={row}>
        <input data-testid="sound-enabled" type="checkbox" checked={config.enabled} onChange={(e) => save({ ...config, enabled: e.target.checked })} />
        <span>Enable sounds</span>
      </label>

      <div style={row}>
        <span style={{ width: 90 }}>Fill</span>
        <select data-testid="sound-fill" value={config.fillSound} style={inp} onChange={(e) => save({ ...config, fillSound: e.target.value as typeof config.fillSound })}>
          <option value="off">off</option>
          {FILL_SOUND_IDS.map((id) => <option key={id} value={id}>{FILL_SOUND_LABELS[id]}</option>)}
        </select>
        <button data-testid="sound-preview-fill" style={{ ...inp, cursor: "pointer" }} onClick={() => soundEngine.preview("fill", config.fillSound === "off" ? "twoTone" : config.fillSound)}>▶</button>
      </div>

      <label style={row}>
        <input data-testid="sound-place" type="checkbox" checked={config.placeClick} onChange={(e) => save({ ...config, placeClick: e.target.checked })} />
        <span>Placement click</span>
      </label>

      <div style={row}>
        <span style={{ width: 90 }}>Reject</span>
        <select data-testid="sound-reject" value={config.rejectSound} style={inp} onChange={(e) => save({ ...config, rejectSound: e.target.value as typeof config.rejectSound })}>
          <option value="off">off</option>
          {REJECT_SOUND_IDS.map((id) => <option key={id} value={id}>{REJECT_SOUND_LABELS[id]}</option>)}
        </select>
        <button data-testid="sound-preview-reject" style={{ ...inp, cursor: "pointer" }} onClick={() => soundEngine.preview("reject", config.rejectSound === "off" ? "alertBeeps" : config.rejectSound)}>▶</button>
      </div>

      <div style={row}>
        <span style={{ width: 90 }}>Scanner</span>
        <select data-testid="sound-scanner" value={config.scannerSound} style={inp} onChange={(e) => save({ ...config, scannerSound: e.target.value as typeof config.scannerSound })}>
          <option value="off">off</option>
          {SCANNER_SOUND_IDS.map((id) => <option key={id} value={id}>{SCANNER_SOUND_LABELS[id]}</option>)}
        </select>
        <button data-testid="sound-preview-scanner" style={{ ...inp, cursor: "pointer" }} onClick={() => soundEngine.preview("scanner", config.scannerSound === "off" ? "arpeggio" : config.scannerSound)}>▶</button>
      </div>

      <div style={row}>
        <span style={{ width: 90 }}>Volume</span>
        <input data-testid="sound-volume" type="range" min={0} max={1} step={0.05} value={config.volume} onChange={(e) => save({ ...config, volume: Number(e.target.value) })} />
      </div>
    </div>
  );
}
```

> Delete the unused `preview`/`defaultFor` helper sketch above — the per-button inline handlers are the implementation. Keep only what compiles cleanly under lint (no unused vars).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/sound/SoundsSection.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 5: Gate + commit**

Run: `cd ui && npm run typecheck && npm run lint`

```bash
git add ui/src/sound/SoundsSection.tsx ui/src/sound/SoundsSection.test.tsx
git commit -m "feat(ui/sound): SoundsSection settings UI + previews"
```

---

## Task 10: Render SoundsSection inside OrderSettingsModal

**Files:**
- Modify: `ui/src/chrome/exec/OrderSettingsModal.tsx`
- Modify: `ui/src/chrome/exec/OrderSettingsModal.test.tsx`

**Interfaces:**
- Consumes: `SoundsSection` from `../../sound/SoundsSection`; `SoundConfigProvider` (needed to wrap the modal in its test).
- Produces: no signature change to `OrderSettingsModal` — it just renders `<SoundsSection />` after its existing content.

- [ ] **Step 1: Update the existing modal test's wrapper first (it will fail)**

In `ui/src/chrome/exec/OrderSettingsModal.test.tsx`, wrap the render in `SoundConfigProvider` (with a fake `commands`) so the modal's new `<SoundsSection>` has its context. Update the `wrap()` helper:

```tsx
import { SoundConfigProvider } from "../../sound/SoundConfigProvider";
import { vi } from "vitest";

const soundCommands = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", value: undefined })) };

function wrap(onSave = vi.fn(), onClose = vi.fn()) {
  render(
    <ThemeProvider>
      <SoundConfigProvider commands={soundCommands as never}>
        <OrderSettingsModal config={DEFAULT_ORDER_CONFIG} status={status} onSave={onSave} onClose={onClose} />
      </SoundConfigProvider>
    </ThemeProvider>,
  );
  return { onSave, onClose };
}
```

Add one assertion that the section renders:

```tsx
it("renders the Sounds section", () => {
  wrap();
  expect(screen.getByTestId("sound-fill")).toBeTruthy();
});
```

**Also patch the second render site.** This test file has a *separate* render of `OrderSettingsModal` inside a local `Harness` component (the "does not leak a captured keydown to the global hotkey engine" test, ~lines 60–100 — it does **not** go through `wrap()`). Once the modal unconditionally renders `<SoundsSection>`, that render will throw `"useSoundConfig must be used within a SoundConfigProvider"`. Wrap the `Harness`'s `OrderSettingsModal` (or the whole `Harness` tree) in `<SoundConfigProvider commands={soundCommands as never}>` too, nested alongside the existing `OrderConfigProvider`. Grep the file for every `<OrderSettingsModal` occurrence and confirm each has a `SoundConfigProvider` ancestor before moving on.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/exec/OrderSettingsModal.test.tsx`
Expected: FAIL — `sound-fill` not found (modal doesn't render the section yet).

- [ ] **Step 3: Render the section in the modal**

In `ui/src/chrome/exec/OrderSettingsModal.tsx`, import and render `<SoundsSection />` just before the Save/Close button row (so it sits inside the existing scroll body):

```tsx
import { SoundsSection } from "../../sound/SoundsSection";
// ...within the returned JSX, after the templates block and before the save row:
<SoundsSection />
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/exec/OrderSettingsModal.test.tsx`
Expected: PASS (existing template tests + the new render assertion).

- [ ] **Step 5: Gate + commit**

Run: `cd ui && npm run typecheck && npm run lint`

```bash
git add ui/src/chrome/exec/OrderSettingsModal.tsx ui/src/chrome/exec/OrderSettingsModal.test.tsx
git commit -m "feat(ui/exec): render Sounds section in OrderSettingsModal"
```

---

## Task 11: Wire the engine — useSoundWiring hook, AppShell, App provider

**Files:**
- Create: `ui/src/sound/useSoundWiring.ts`
- Test: `ui/src/sound/useSoundWiring.test.ts`
- Modify: `ui/src/chrome/AppShell.tsx`
- Modify: `ui/src/App.tsx`

**Interfaces:**
- Consumes: `Stores` (`stores.fills`/`stores.exec`/`stores.scanner`) from `../data/registry`; `SoundSink` + `soundEngine` from `./SoundEngine`.
- Produces: `useSoundWiring(stores: Stores, engine?: SoundSink): void` — subscribes fill/reject/scanner store hooks to the engine and registers a one-time `pointerdown`/`keydown` capture listener that calls `engine.unlock()`; cleans everything up on unmount. `AppShell` calls it once (unconditionally, above the loading early-return). `App` mounts `<SoundConfigProvider>`.

- [ ] **Step 1: Write the failing test**

`ui/src/sound/useSoundWiring.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useSoundWiring } from "./useSoundWiring";
import { FillStore } from "../data/FillStore";
import { ExecStore } from "../data/ExecStore";
import { ScannerStore } from "../data/ScannerStore";
import type { SoundSink } from "./SoundEngine";

function stubStores() {
  return { fills: new FillStore(), exec: new ExecStore(), scanner: new ScannerStore() } as never;
}
function sink(): SoundSink & { calls: string[] } {
  const s = { calls: [] as string[],
    orderFilled: () => s.calls.push("fill"),
    orderRejected: () => s.calls.push("reject"),
    scannerHit: () => s.calls.push("scanner"),
    unlock: () => s.calls.push("unlock") };
  return s;
}

describe("useSoundWiring", () => {
  it("forwards store events to the engine and unlocks on first gesture", () => {
    const stores = stubStores();
    const engine = sink();
    renderHook(() => useSoundWiring(stores, engine));

    stores.fills.apply({ kind: "delta", topic: "exec.fills", payload: { venue: "alpaca", orderId: "o1", symbol: "AAPL", side: "BUY", qty: 1, price: 1, tsMs: 1 } });
    stores.exec.apply({ kind: "delta", topic: "exec.orders", payload: { venue: "alpaca", id: "o1", symbol: "AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 1, limitPrice: 1, stopPrice: 0, status: "REJECTED", executedQty: 0, leavesQty: 1, avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1 } });
    window.dispatchEvent(new Event("pointerdown"));

    expect(engine.calls).toContain("fill");
    expect(engine.calls).toContain("reject");
    expect(engine.calls).toContain("unlock");
  });

  it("unsubscribes on unmount (no calls after)", () => {
    const stores = stubStores();
    const engine = sink();
    const { unmount } = renderHook(() => useSoundWiring(stores, engine));
    unmount();
    stores.fills.apply({ kind: "delta", topic: "exec.fills", payload: { venue: "alpaca", orderId: "z", symbol: "AAPL", side: "BUY", qty: 1, price: 1, tsMs: 1 } });
    expect(engine.calls).not.toContain("fill");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/sound/useSoundWiring.test.ts`
Expected: FAIL — cannot find module `./useSoundWiring`.

- [ ] **Step 3: Write the hook**

`ui/src/sound/useSoundWiring.ts`:

```ts
import { useEffect } from "react";
import type { Stores } from "../data/registry";
import { soundEngine, type SoundSink } from "./SoundEngine";

// Subscribes the imperative SoundEngine to the three trigger stores and resumes the
// AudioContext on the first user gesture. Mount once (AppShell), never conditionally.
export function useSoundWiring(stores: Stores, engine: SoundSink = soundEngine): void {
  useEffect(() => {
    const offFill = stores.fills.onNewFill((f) => engine.orderFilled(f.side, f.tsMs));
    const offReject = stores.exec.onOrderRejected(() => engine.orderRejected());
    const offHit = stores.scanner.onNewHit(() => engine.scannerHit());

    const unlock = () => engine.unlock();
    window.addEventListener("pointerdown", unlock, { once: true, capture: true });
    window.addEventListener("keydown", unlock, { once: true, capture: true });

    return () => {
      offFill(); offReject(); offHit();
      window.removeEventListener("pointerdown", unlock, true);
      window.removeEventListener("keydown", unlock, true);
    };
  }, [stores, engine]);
}
```

- [ ] **Step 4: Run the hook test to verify it passes**

Run: `cd ui && npx vitest run src/sound/useSoundWiring.test.ts`
Expected: PASS (2 tests).

- [ ] **Step 5: Call the hook in AppShell + mount the provider in App**

In `ui/src/chrome/AppShell.tsx`, add the call next to `useHotkeys`, **above** the `if (!ws) return ...` early return (Rules of Hooks):

```tsx
import { useSoundWiring } from "../sound/useSoundWiring";
// ...alongside the existing useHotkeys call, before the loading early-return:
useSoundWiring(stores);
```

In `ui/src/App.tsx`, wrap `AppShell` in `SoundConfigProvider` (nested inside `OrderConfigProvider`, reusing the same `commands` adapter):

```tsx
import { SoundConfigProvider } from "./sound/SoundConfigProvider";
// ...
<OrderConfigProvider commands={commands}>
  <SoundConfigProvider commands={commands}>
    <ReconnectOverlay state={state}>
      <AppShell workspaceName={workspaceName} stores={stores} scheduler={scheduler}
        workspaceStore={workspaceStore} linkGroups={linkGroups} commands={commands} />
    </ReconnectOverlay>
  </SoundConfigProvider>
</OrderConfigProvider>
```

- [ ] **Step 6: Full suite + gates**

Run: `cd ui && npm run typecheck && npm run lint && npm test`
Expected: entire UI suite PASS (confirms no regression in AppShell/App consumers).

- [ ] **Step 7: Manual verification (real audio, by ear)**

Run the app against the mock engine and confirm sounds fire and settings persist:

```bash
cd ui && npm run mock-engine   # in one terminal
cd ui && npm run dev           # in another; open the app, click once to unlock audio
```
Confirm by ear: a placed order clicks; a fill two-tones (buy rises / sell falls); a blocked/rejected order alert-beeps; a new scanner symbol arpeggios; the settings dropdowns change the sound and the `▶` previews play; reload preserves settings. (Mock-engine trigger availability depends on `ui/mock-engine`; if a trigger isn't scriptable there, note it and defer that leg to the UI Plan 6 replay E2E.)

- [ ] **Step 8: Commit**

```bash
git add ui/src/sound/useSoundWiring.ts ui/src/sound/useSoundWiring.test.ts ui/src/chrome/AppShell.tsx ui/src/App.tsx
git commit -m "feat(ui/sound): wire SoundEngine into stores + AppShell + App provider"
```

---

## Self-Review

**Spec coverage** (`docs/superpowers/specs/2026-07-06-order-event-sounds-design.md`):
- Four events + default sounds → Tasks 2 (patches), 3 (engine mapping), 7/11 (triggers). ✔
- Sound IDs (7 fill / 5 reject / 5 scanner) → Task 1 unions + Task 2 registries. ✔
- `patches.ts` ported 1:1, no asset files → Task 2. ✔
- `SoundEngine` public surface (`orderPlaced`/`orderFilled`/`orderRejected`/`scannerHit`/`setConfig`/`preview`) → Task 3. ✔
- AudioContext lazy + resume on one-time gesture; drop when suspended → Task 2 (`WebAudioPatchPlayer`) + Task 11 (resume listener). ✔
- `volume²` master gain → Task 2/3. ✔
- Coalescing 200 ms per channel → Task 3. ✔
- Fill freshness guard (`tsMs ≥ now − 10s`), scanner none → Task 3. ✔
- Config gating (master/per-event; preview bypass) → Task 3. ✔
- Trigger wiring: fill (`onNewFill`), place (ack accepted), reject (ack blocked + stream transition), scanner (`onNewHit` + force-flash), double-fire absorption → Tasks 4/5/6/7/11. ✔
- `SoundConfig` type + defaults; `SoundConfigProvider` mirroring `OrderConfigProvider`; separate `"soundConfig"` key → Tasks 1/8. ✔
- Sounds section appended to `OrderSettingsModal` with previews → Tasks 9/10. ✔
- Testing section (engine decision logic, store hooks, modal round-trip; patches by ear) → per-task tests + Task 11 manual. ✔
- Non-goals (custom files, cancels/partials/news sounds, per-symbol variation) → not implemented. ✔

**Placeholder scan:** the only deliberate "port from lines X–Y" markers are in Task 2, which is a verbatim code port from `prototypes/fill-sounds.html` (three functions shown verbatim as exemplars; the rest are exact-line references, not vague TODOs) — appropriate for a 1:1 port. The `preview`/`defaultFor` sketch in Task 9 Step 3 is explicitly flagged for deletion.

**Type consistency:** `SoundApi` (Task 3, used by Task 7) = `{ orderPlaced, orderRejected }`; `SoundSink` (Task 3, used by Task 11) = `{ orderFilled, orderRejected, scannerHit, unlock }`; `SoundEngine` implements both. `sanitizeSoundConfig`/`SOUND_CONFIG_KEY`/`DEFAULT_SOUND_CONFIG` (Task 1) reused verbatim in Tasks 8. `resolvePatch(kind, id)` signature consistent across Tasks 2/3. `onNewFill`/`onOrderRejected`/`onNewHit` all return `() => void` unsubscribers, consumed by Task 11. `Side` (`"BUY"|"SELL"|"SHORT"|"COVER"`) → variant via `sideIsSell` consistently.

**Assumptions to verify at execution time** (flagged for the implementer): the exact `apply()`/message-builder shapes in the three store `*.test.ts` files (match, don't invent); the `useTheme`/`palette` import path used by `OrderSettingsModal.tsx`; the `Stores` field names in `ui/src/data/registry.ts` (`fills`/`exec`/`scanner`); and whether `@testing-library/react`'s `renderHook` is exported in the installed v16 (fallback: a tiny test component). The recon that produced this plan confirmed all of these, but re-check before relying on any literal above.
