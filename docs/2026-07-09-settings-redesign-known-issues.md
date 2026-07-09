# Settings redesign — known issues (not blocking)

Three items surfaced during the settings-redesign plan's task reviews and final
whole-branch review (`docs/superpowers/plans/2026-07-09-settings-redesign.md`,
worktree `worktree-settings-redesign`). All three were judged non-blocking for
merge; recorded here so they aren't lost. None involve live venues, real
credentials, or order placement.

## Tracked follow-ups

- [ ] **`venueadmin.Admin` has no internal locking.**
      `engine/internal/venueadmin/venueadmin.go`'s `Admin` does an unguarded
      read-modify-write against `config.toml`/`credentials.json` in each of
      `GetVenueSetup`/`SetVenueSetup`/`PutCredential`/`DeleteCredential`.
      `uihub` dispatches WS commands per-connection, each in its own
      goroutine, against one shared `Admin` instance — verified during
      Task 4's review: no per-hub serialization exists for these four
      commands specifically (the hub's own single-goroutine loop doesn't
      carry them).
      **Impact:** two connections (two browser tabs, a stale tab + a fresh
      one, a reconnect overlap) saving venue/credential settings at the
      exact same moment could lose one edit — a lost update, never file
      corruption (each write is still atomic temp+rename, and `config.toml`
      additionally keeps its one-time `.bak`).
      **Why not fixed now:** eTape is a single-user local app, typically one
      browser tab; the race requires two concurrent Saves within the same
      moment. Low real-world probability.
      **Fix if picked up:** one `sync.Mutex` in `Admin`, or route the four
      commands through a single goroutine.

- [ ] **Sounds section's "restore on re-check" can silently revert to the
      default sound instead of the user's real persisted choice.**
      `ui/src/sound/SoundsSection.tsx` seeds `lastPick` (the value restored
      when a sound row is unchecked then re-checked) from a `useState` lazy
      initializer that runs once at first mount. If `SoundsSection` mounts
      before `SoundConfigProvider`'s async `GetConfig` resolves, `lastPick`
      is seeded from `DEFAULT_SOUND_CONFIG` rather than the user's actual
      saved sound. Inherited verbatim from the settings-redesign plan's own
      sketch (Task 9), not introduced by an implementer shortcut.
      **Why not fixed now:** the final whole-branch review traced the real
      race window — `SoundsSection` only mounts when a user navigates to the
      Sounds tab of an already-opened Settings modal, which is many seconds
      and several interactions after the boot-time `GetConfig` round-trip
      resolves. Not achievable without an artificial backend stall; not
      worth a fix given the effectively-unreachable window and low-severity
      symptom (silent wrong-value restore, never a crash).

- [ ] **Applying the Trading preset doesn't seed `LinkGroups`' shared focus,
      so the first default place-order hotkey after a fresh preset apply can
      silently no-op.**
      Pre-existing bug, unrelated to the settings-redesign plan's own scope
      — discovered incidentally by Task 12's E2E work, confirmed to live
      entirely in code this branch never touched (`presets.ts`,
      `linkGroups.ts`, the preset-apply path in `AppShell.tsx`).
      `useHotkeys.ts` reads `linkGroups.symbolFor(group)` with no per-panel
      fallback, while every blue-group panel (chart/ladder/tape/ticket) has
      its own *local*, creation-time symbol fallback that's never written
      back into the shared `LinkGroups` map. Result: right after applying
      Trading, every blue panel visibly shows a live AAPL quote, but
      Ctrl+1..4 resolves an empty symbol/venue and toasts "no venue/quote for
      hotkey" — until the user manually commits a symbol into any blue panel
      (type-to-load), after which hotkeys work normally for the rest of the
      session.
      **Why not fixed here:** a naive fix (seed `linkGroups.hydrate()` on
      preset apply) was tried and reverted — it broke Monitoring's
      *intentional* behavior, where a chart panel and a news panel with no
      default symbol are deliberately co-grouped in the same `"blue"` link
      group (`presets.ts`), so seeding blue from the chart's default would
      force-focus the news panel and silently kill its tested "no symbol
      focused" empty state.
      **Needs:** its own design pass — likely a per-preset decision about
      which panel (if any) should seed a link group's initial focus on
      apply, distinct from Monitoring's deliberate no-default-symbol case.
      Should get its own plan/spec, not be bolted onto a future unrelated
      branch.
