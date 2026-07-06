# Order & Scanner Event Sounds — Design

**Date:** 2026-07-06
**Status:** Approved (Earl, 2026-07-06)
**Scope:** UI-only. Zero engine changes — config persists through the existing generic
`GetConfig`/`SetConfig` KV commands; all trigger events already stream to the UI.
**Audition reference:** `prototypes/fill-sounds.html` — every patch below was chosen by
ear from that page; port the synthesis functions 1:1.

## Decision summary

Four events get sounds. The three order events share one pitch language (higher = buy,
lower = sell; rejects are side-agnostic); the scanner alert is deliberately shaped so
it can never read as an order event — no two-note pairs (fill), no double beeps
(reject), no bare clicks (place):

| Event | Sound (default) | Fires on | Options in settings |
|---|---|---|---|
| Order placed | **Click** — 25 ms bandpass-noise tick | `SubmitOrder`/`Flatten` ack `accepted` | on/off |
| Order filled | **Two-Tone** — buy C5→G5 rising, sell falling | new fill ingested by `FillStore` | 7 sounds + off |
| Order rejected | **Alert Beeps** — two urgent B5 pings | order-command ack `rejected`, or order status → `REJECTED` | 5 sounds + off |
| Scanner hit | **Arpeggio** — three rising marimba notes C5-E5-G5 | new symbol in scanner ranking (`ScannerStore` new-hit or `scanner.hit` force-flash) | 5 sounds + off |

Rationale: in a hotkey-driven workflow the click answers "did my keypress register?"
without looking away from the tape; the fill chime confirms execution; and once
placement makes a sound, silence becomes a contract — the reject sound is what breaks
it when an order you think is working isn't. Reject defaults to the most
attention-grabbing candidate because rejects arrive exactly when eyes are off the
order panel.

All 7 fill, 5 reject, and 5 scanner patches ship (each is ~10 lines of Web Audio
synthesis, no asset files), so changing sounds later is a settings dropdown pick.

Sound IDs — fill: `softBlip`, `twoTone` (default), `marimba`, `cashBell`, `tick`,
`glassPing`, `pop`; reject: `buzz`, `dunDun`, `doubleKnock`, `alertBeeps` (default),
`powerDown`; scanner: `sonarPing`, `arpeggio` (default), `chirp`, `highChime`,
`singingBowl`.

## Module: `ui/src/sound/`

- **`patches.ts`** — pure synthesis functions ported 1:1 from the audition page, each
  `(ctx: AudioContext, out: AudioNode, variant: "buy" | "sell", when: number) => void`.
  No imports from `data/` or `chrome/`. Reject and scanner patches ignore `variant`.
- **`SoundEngine.ts`** — module singleton owning the `AudioContext`, a master
  `GainNode`, config, and all play-decision logic. Public surface:
  - `orderPlaced(side)`, `orderFilled(side, tsMs)`, `orderRejected()`, `scannerHit()`
  - `setConfig(cfg: SoundConfig)` — pushed from the settings provider
  - `preview(kind: "fill" | "place" | "reject" | "scanner", id: string)` — for settings UI
- Audio plays straight from imperative event hooks — never through React state,
  consistent with the high-frequency-data rule.

**AudioContext lifecycle (autoplay policy):** created lazily and resumed on a one-time
`pointerdown`/`keydown` capture listener registered by `AppShell`. If a play request
arrives while the context is missing or suspended, the sound is dropped (never queued —
a late confirmation sound is misinformation).

**Volume:** master gain = `volume²` (perceptual taper on the 0–1 slider).

## Play-decision logic (in `SoundEngine`)

- **Coalescing:** a play is suppressed if the same channel played within the last
  200 ms (constant, not a setting). Channels: `place:buy`, `place:sell`, `fill:buy`,
  `fill:sell`, `reject`, `scanner`. A 6-print burst fill sounds ~3 times; buy and
  sell fills never mask each other; several symbols landing in one scanner refresh
  play once.
- **Freshness guard (fills only):** play only when `tsMs ≥ now − 10 s`. Cold-open and
  reconnect snapshot merges of the morning's fill history stay silent, while a fill
  that lands during a brief disconnect still chimes when the merge arrives. 10 s
  absorbs venue/client clock skew. Scanner hits need no freshness guard: the trigger
  is delta-only by construction, so it can't replay history.
- **Config gating:** master `enabled` off silences everything; per-event `"off"` /
  `placeClick: false` silences that channel. Preview bypasses gating (except volume).

## Trigger wiring (in `AppShell`, which already wires stores and hotkeys)

- **Fill:** `FillStore` gains `onNewFill(listener)`, invoked once per newly-ingested
  fill (after its existing dedup, so reconnect re-snapshots can't double-play).
  Variant via the existing `sideIsSell()` mapping.
- **Place:** `OrderCommands` (`chrome/exec/commands.ts`) gains an optional `sound` dep.
  On `SubmitOrder` ack `accepted` → `orderPlaced(args.side)`. On `Flatten` ack
  `accepted` → `orderPlaced("sell")` — flatten is risk-off, and the falling pitch
  matches. `Arm`/`Disarm`/`KillSwitch` stay silent: they're deliberate button actions
  with visible feedback where the user is already looking.
- **Reject, ack path (local gate):** ack `rejected` on `SubmitOrder`, `CancelOrder`,
  `ReplaceOrder`, or `Flatten` → `orderRejected()`. A rejected cancel/replace is
  included deliberately — "I think I canceled but didn't" is a dangerous silent
  failure.
- **Reject, stream path (venue):** `ExecStore` gains `onOrderRejected(listener)`,
  fired on an observed status *transition* to `REJECTED`. The initial snapshot seeds
  state silently; unchanged `REJECTED` rows on re-snapshot don't re-fire.
- **Double-fire absorption:** if the engine both nacks a command and journals a
  `REJECTED` order row, the two triggers land within the 200 ms coalescing window on
  the `reject` channel and play once. No cross-path bookkeeping needed.
- **Scanner hit:** `ScannerStore` gains `onNewHit(listener)`, fired alongside its
  existing new-hit computation — a delta row whose symbol isn't in the per-session
  seen-set, or a `scanner.hit` force-flash (which sounds even for an already-seen
  symbol; re-flagging attention is its purpose). Snapshots seed silently, and the
  seen-set's midnight-reset dedup means a symbol that already hit today won't
  re-chime on later refreshes. The sound fires exactly where the visual flash does.

## Settings

**`SoundConfig`** (new file in `ui/src/sound/` alongside `SoundEngine.ts`; persisted
engine-side under KV key `"soundConfig"`, following the `"orderConfig"` pattern):

```ts
interface SoundConfig {
  enabled: boolean;                      // default true (master)
  volume: number;                        // 0..1, default 0.6
  fillSound: FillSoundId | "off";        // default "twoTone"
  placeClick: boolean;                   // default true
  rejectSound: RejectSoundId | "off";    // default "alertBeeps"
  scannerSound: ScannerSoundId | "off";  // default "arpeggio"
}
```

- **`SoundConfigProvider`** mirrors `OrderConfigProvider`: `GetConfig("soundConfig")`
  on mount (defaults on absent/malformed value), `SetConfig` on save, and an effect
  that pushes every config change into `SoundEngine.setConfig`. Separate key keeps
  `orderConfig`'s schema untouched.
- **UI:** a **Sounds** section appended to the existing `OrderSettingsModal` — master
  enable toggle, fill-sound dropdown, placement-click toggle, reject-sound dropdown,
  scanner-sound dropdown, volume slider. Every sound row gets a ▶ preview button
  (buy variant, current volume). Dropdown labels match the audition page names.

## Testing

- `SoundEngine` decision logic with an injected clock and a fake patch player:
  coalescing per channel, freshness guard, config gating, side→variant mapping, and
  ack+stream reject double-fire absorbed to one play.
- `FillStore.onNewFill`: fires once per new fill, never for deduped re-ingests; fires
  for snapshot-merged fills (downstream freshness guard is what silences stale ones).
- `ExecStore.onOrderRejected`: transition-only; initial seed silent; no re-fire on
  unchanged rows.
- `ScannerStore.onNewHit`: fires for delta new-hits and `scanner.hit` force-flashes
  (including already-seen symbols); silent on snapshots and for seen symbols on
  ordinary refreshes.
- `OrderSettingsModal` Sounds section: render + save round-trip, following the
  modal's existing test patterns.
- Patches themselves are verified by ear (audition page now, preview buttons after) —
  no audio-output unit tests.

## Non-goals (v1)

- Custom user sound files (the patch registry leaves room for a `file:` source later).
- Sounds for cancels, partial-vs-complete fills, news, price alerts, or connection
  events.
- Per-symbol or per-venue sound variation; configurable coalescing window.
