# Decision record: eTape owns its own credential store

Status: **done** (not an open issue — see
`docs/2026-07-09-settings-redesign-known-issues.md` for outstanding follow-ups from
the same settings-redesign work; this is a separate, completed decision reversal).

## What changed

Credential storage moved from a file **shared with eJournal**
(`~/.eJournal/credentials.json`) to a file **owned by eTape**
(`~/.eTape/credentials.json`, alongside `~/.eTape/config.toml`). This reverses the
choice made in `docs/superpowers/specs/2026-07-09-settings-redesign-design.md` §6,
which deliberately shared eJournal's file and preserved its entries byte-for-byte.

`engine/internal/creds.DefaultPath()` now returns `~/.eTape/credentials.json`
(commit `301eae5`, "repoint credential store from ~/.eJournal to ~/.eTape").

## Why

eTape's credentials are no longer shared with eJournal — the product decision is for
eTape to own its own credential store outright, rather than depend on another app's
file layout and lifecycle.

## What it means for the UI

Venues and credentials are now merged into a single card per venue: each venue owns
exactly one credential (Key ID + Secret entered inline in its own card), under an
opaque internal name the user never sees. There is no more shared named-key
indirection between multiple venues.

- `ui/src/chrome/exec/VenuesSection.tsx` — rewritten as the card-per-venue layout
  (commit `e1ba342`, plus a follow-up fix in `4bba56b` for minting a credential name
  when a venue's broker switches off sim).
- `ui/src/chrome/VenueSetupPrompt.tsx` — a new first-run prompt shown when no venues
  are configured, offering "Configure venues" / "I'll do it later" plus a "Don't show
  this again" checkbox persisted to localStorage (commit `4fe0680`, tests hardened in
  `360de97`).

## Migration

None — this was a deliberate "start empty" change, not a migration. Any credentials
that were previously in `~/.eJournal/credentials.json` (including Earl's live
TradeZero and `alpaca-live` keys) are **not** carried over automatically and must be
re-entered through the new Venues & credentials settings UI before those venues will
boot.
