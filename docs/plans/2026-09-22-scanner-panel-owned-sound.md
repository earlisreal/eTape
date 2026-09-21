# Scanner Panel-Owned Sound

## Goal

Play Scanner Sound only from workspace windows that contain at least one
unmuted Scanner Panel. Move Scanner cue ownership out of the window-global
sound wiring and into the Scanner Panel lifecycle, then add a persisted
mute/unmute control to the Scanner Panel Header.

Each eligible workspace window plays at most one cue for a Scanner hit. A
Scanner Panel remains eligible while its tab is inactive or its host window is
backgrounded or minimized. Closing the last eligible Scanner Panel silences
that window immediately.

## Non-goals

- Do not coordinate a single cue across all eTape windows. Separate eligible
  windows may each play their local cue.
- Do not change Scanner hit detection, baseline/seen behavior, filtering,
  ranking, Scanner Sync, or the `scanner.rank`/`scanner.hit` wire contracts.
- Do not move fill, reject, placement, or Session Transition Announcement
  ownership into panels.
- Do not replace the global Enable sounds control, Scanner sound selection,
  master volume, or General-settings preview behavior.
- Do not gate sound on the active tab, focused window, visibility, or minimized
  state.
- Do not queue or replay hits received while a panel is muted or absent.
- Do not add engine state, a cross-window presence protocol, a layout version,
  a storage migration, a dependency, or a shared alert framework.
- Do not add panel cloning; any future operation that copies panel settings will
  naturally copy the mute preference.

## Current-code evidence

- [`AppShell.tsx`](../../ui/src/chrome/AppShell.tsx) mounts
  `useSoundWiring(stores)` once in every workspace window.
  [`useSoundWiring.ts`](../../ui/src/sound/useSoundWiring.ts) currently
  subscribes that window to fills, rejects, and `ScannerStore.onNewHit`
  unconditionally. This is the ownership bug: a window can produce Scanner
  Sound without containing a Scanner Panel.
- [`ScannerPanel.tsx`](../../ui/src/chrome/panels/ScannerPanel.tsx) is the
  per-panel lifecycle boundary and already owns the Scanner Panel Header
  controls through `PanelHeaderSlotContext`. Its existing `onConfigChange`
  callback persists panel-specific settings without adding another store.
- [`ScannerStore.ts`](../../ui/src/data/ScannerStore.ts) exposes the imperative
  `onNewHit` subscription needed by a panel. Using it preserves the invariant
  that high-frequency Scanner data does not flow through new React state.
- [`SoundEngine.ts`](../../ui/src/sound/SoundEngine.ts) already gates Scanner
  cues through global sound preferences and coalesces the `scanner` channel for
  200 ms. Because each workspace window has its own JavaScript context and
  `SoundEngine` singleton, synchronous callbacks from multiple unmuted Scanner
  Panels collapse to one cue within that window while separate windows remain
  independent.
- [`SoundConfigProvider.tsx`](../../ui/src/sound/SoundConfigProvider.tsx) keeps
  the existing global sound configuration synchronized across windows. A
  panel-local mute does not need to read or rewrite that configuration.
- [`AppShell.tsx`](../../ui/src/chrome/AppShell.tsx) merges settings patches into
  the matching `PanelConfig` and saves the workspace.
  [`backup.ts`](../../ui/src/chrome/backup.ts) preserves all non-symbol panel
  settings during layout export/import, so a mute field needs no custom export
  path.
- Dockview's installed default `onlyWhenVisible` renderer detaches an inactive
  tab's element without disposing its React part. The Scanner Panel subscription
  therefore remains mounted for inactive tabs and is disposed only when the
  panel itself is removed or its workspace closes.
- [`tvIcons.tsx`](../../ui/src/chrome/panels/tv/tvIcons.tsx) is the existing
  hand-rolled, `currentColor` icon set used by Scanner Panel Header controls; it
  has no speaker/mute icon today.

## Design decisions

### Panel lifecycle owns eligibility

Remove only the Scanner subscription from `useSoundWiring`. Keep fill/reject
subscriptions and the first-gesture audio unlock there so every window retains
the existing shared `AudioContext` lifecycle.

Each unmuted `ScannerPanel` subscribes directly to
`stores.scanner.onNewHit` and forwards future callbacks to
`soundEngine.scannerHit()`. Muting or unmounting the panel disposes that
subscription. Unmuting starts a fresh subscription and does not inspect or
replay earlier store state.

This yields the accepted per-window rule without another coordinator:

- no Scanner Panel, or all local Scanner Panels muted: zero subscribers and no
  Scanner Sound in that window;
- one or more unmuted Scanner Panels in one window: callbacks reach the same
  `SoundEngine`, whose existing scanner-channel coalescing emits at most one
  cue for the synchronous hit fan-out; and
- unmuted Scanner Panels in different windows: each window may emit its own
  cue.

Do not add `BroadcastChannel`, Web Locks, leader election, or engine state for
this behavior.

### Persisted panel-local mute

Store one strict boolean setting, `scannerSoundMuted`, in the Scanner Panel's
existing `settings` object. Only literal `true` means muted; missing, false, or
malformed values normalize to unmuted. This makes legacy and newly added panels
audible by default without migration.

The header button updates local component state immediately and persists only
`{ scannerSoundMuted: next }` through `onConfigChange`; never spread the frozen
`config.settings` object. Imported panels retain the setting because layout
export already preserves non-symbol panel settings. Deleting a panel deletes
the preference with the panel.

Global Enable sounds, Scanner sound selection, and master volume remain the
master gate inside `SoundEngine`. The header button continues to show and edit
only its panel-local mute state even while a global gate is off. General
settings previews continue to bypass normal event gating and are unaffected by
panel mute.

### Header control

The Header contains one compact icon-only toggle before the existing Filters
and Columns controls. It uses the established header-button styling and the
existing icon module's minimum speaker and muted-speaker glyphs. The control
has state-specific `aria-label` and `title` text (`Mute Scanner sounds` /
`Unmute Scanner sounds`) plus `aria-pressed` reflecting the muted state. Muted
styling remains legible in both themes, with no popover, toast, or second
settings surface.

## File-level steps

- `useSoundWiring.ts` remains the window-global owner for fill/reject cues and
  audio unlock; Scanner hits are owned by mounted Scanner Panels.
- `ScannerPanel.tsx` stores the strict `scannerSoundMuted` preference, manages
  the imperative hit subscription, and renders the accessible Header toggle.
- `tvIcons.tsx` supplies the two `currentColor` speaker states without a new
  icon dependency.
- Sound, panel, and engine tests cover the ownership boundary, mute lifecycle,
  persistence patch, unmount cleanup, and existing scanner-channel coalescing.
- Sound/chrome READMEs and `CONTEXT.md` carry the durable ownership and
  terminology; no engine, wire, generated-contract, or external-API surface
  changes are part of this behavior.

## Tests

The focused seams are the global sound-wiring test, Scanner Panel lifecycle
tests, and the existing fake-player SoundEngine test. They cover default
unmuted behavior, future-hit-only delivery, narrow settings patches, mute and
unmount cleanup, accessible state labels, and one-cue-per-window coalescing.
Existing layout-backup coverage remains the source of truth for preservation of
arbitrary non-symbol panel settings, including `scannerSoundMuted`.

## Rollout and rollback

The behavior is UI-only: it has no engine/UI version coupling, generated
contract, dependency, database migration, or cross-window protocol. Existing
and newly added Scanner Panels default unmuted; global sound settings remain
the master gate, and layout export/import already carries the panel setting.

Rollback is limited to removing the Panel subscription/toggle, the matching
global-hook removal, their tests, and the associated documentation. Persisted
`scannerSoundMuted` fields are harmless unknown panel settings if an older UI
reads the layout.

## Risks

- Multiple unmuted panels can synchronously reach one window-local
  `SoundEngine`; its existing 200 ms Scanner channel coalescing bounds duplicate
  cues.
- Inactive-tab eligibility depends on Dockview retaining the mounted React
  part; a renderer change would require an explicit presence lifecycle.
- A full settings replacement could erase unrelated panel fields; the toggle
  persists only its narrow patch through `onConfigChange`.
- The local button can differ from global sound state; explicit labels and the
  documentation distinguish Panel Mute from Enable sounds, Scanner Off, and
  zero volume.
- The pre-existing `scanner.hit`/`onNewHit` mismatch remains outside this
  ownership boundary and requires a separate approved change.
