# Hotkey Deck Rows and Settings Drag-and-Drop

Status: Approved on 2026-08-14.

## Goal

Evolve the existing embedded Hotkey Deck from one filtered, wrapping list into
a saved **Deck Layout** of ordered rows. Configure it in Settings with
move-only drag-and-drop, retain accessible non-drag controls, and add one
global, default-off preference for showing bound hotkeys as the existing keycap
badge on Deck Buttons. Deck Buttons remain references to Action Templates and
continue to use the current execution path and engine-side safety gates.

## Non-goals

- Do not make the Hotkey Deck a dockable panel, toolbar, or separate execution
  surface.
- Do not add deck-only actions, macros/scripts, duplicate button placements,
  row labels, row styling, empty-row spacers, or touch-first drag support.
- Do not add a drag-and-drop dependency; use native browser drag events and
  the existing settings-control style.
- Do not change hotkey capture, target resolution, arm/risk checks, client
  `gateArm` behavior, order submission, or button colors.
- Do not change the engine, WebSocket contract, generated sources, workspace
  layout, or active-venue import rule.

## Current-code evidence

- [`actionTemplate.ts`](../../ui/src/chrome/exec/actionTemplate.ts) owns the
  persisted `orderConfig`; each Action Template has optional `deck` and
  `deckColor` fields, while `normalizeOrderConfig` is the existing migration
  entry point.
- [`HotkeyDeck.tsx`](../../ui/src/chrome/panels/HotkeyDeck.tsx) filters
  `config.templates` by `deck`, follows the global template-array order, wraps
  one flex list, always renders a `Keycap` for a bound hotkey, and dispatches
  through the shared `fireTemplate(..., { gateArm: false })` path.
- [`OrderTicketPanel.tsx`](../../ui/src/chrome/panels/OrderTicketPanel.tsx)
  renders the deck only as Strip 5 beneath the manual BUY/SELL/SHORT/COVER
  row.
- [`OrderSettingsSection.tsx`](../../ui/src/chrome/exec/OrderSettingsSection.tsx)
  keeps staged template edits locally, exposes the current `Show as button`
  checkbox and colors, and can only reorder the complete template array with
  up/down buttons. Its existing Save commits the whole template list.
- [`backup.ts`](../../ui/src/chrome/backup.ts) exports only templates under the
  `hotkeys` envelope and regenerates all template IDs on import; the new row
  references must therefore be remapped at that boundary.
- [`ui/package.json`](../../ui/package.json) has no drag-and-drop library.
  The deck is independent of Dockview, which is still at 7.x in this checkout.

## Design decisions

### Embedded, template-driven deck

The Hotkey Deck remains in the Order Ticket. A Deck Button is the sole
placement of one existing Action Template, never a new action definition. A
click preserves the current shared firing path, including the deck's deliberate
client `gateArm: false` behavior and the authoritative engine-side arm/risk
checks.

### Independent, normalized Deck Layout

Add an optional persisted `hotkeyDeck` value to `OrderConfig`, with ordered
template-ID rows and `showHotkeyLabels`. `normalizeOrderConfig` returns the
canonical shape: no empty rows, no missing IDs, no duplicate IDs across rows,
and a false label preference unless explicitly true. It keeps the first valid
placement when untrusted stored/imported input repeats an ID.

When `hotkeyDeck` is absent, normalize the old `template.deck === true`
members into one ordered row in current template-array order. New rendering and
editing use `hotkeyDeck` only. Keep `template.deck` as a derived compatibility
projection on save so an immediate rollback can still show the same members as
the old flat deck; it must never again determine row order or current layout.
This durable boundary is recorded in [ADR 0002](../adr/0002-hotkey-deck-layout.md).

### Settings workflow and accessibility

Keep the existing `Show as button` control on every Action Template card. On
enable, append the template to the last Deck Row, creating the first row when
needed. On disable or template deletion, remove its sole placement and remove
any now-empty row. Existing `deckColor` stays on the template card and applies
to the placed button.

Render one staged Deck Layout editor in the Orders & hotkeys settings section.
It has one global `Show hotkey labels` checkbox, initially false, and a
temporary `+ Add row` target. A placed button may be dragged within its row or
to any other row; drag is move-only, never copy. A temporary empty row is
dropped on Save if it receives no button. Keep clearly labelled controls for
removing a placement and moving it left/right or to adjacent rows, so keyboard
and assistive-technology users have the full operation set without drag.

All template changes, layout changes, and the global label preference stay
local until the existing Save action. Reset to defaults clears templates,
Deck Layout, and label visibility.

### Rendering and import behavior

`HotkeyDeck` resolves each saved row against the current templates, renders
rows independently in saved order, and only shows the existing `Keycap` when
the global label preference is on and the template has a bound hotkey. Each
row uses natural-width buttons, does not flex-wrap, and scrolls horizontally
when an Order Ticket is narrow.

The existing hotkeys backup remains envelope version 1 because the new fields
are optional and backward-compatible. Export `hotkeyDeck` with templates,
never `activeVenue`. Import an old file as one normalized row; import a new
file by mapping every exported template ID to its newly generated local ID
before normalization. Malformed, stale, and duplicate row references never
produce a live Deck Button.

## File-level implementation

1. In [`ui/src/chrome/exec/actionTemplate.ts`](../../ui/src/chrome/exec/actionTemplate.ts):

   - add the small `HotkeyDeckConfig` / `hotkeyDeck` schema (rows plus global
     label visibility) to `OrderConfig`, keeping it optional at the type
     boundary so legacy stored values and existing focused fixtures remain
     valid;
   - centralize canonicalization in `normalizeOrderConfig`: migrate legacy
     `deck` flags only when no explicit layout exists, validate references
     against the template IDs, retain first placement only, remove empty rows,
     and default labels to false; and
   - derive compatible per-template `deck` membership from the canonical layout
     when persisting an edited configuration, rather than retaining two layout
     sources. Leave `deckColor` and all execution-template fields unchanged.

2. In [`ui/src/chrome/exec/OrderSettingsSection.tsx`](../../ui/src/chrome/exec/OrderSettingsSection.tsx):

   - keep the existing template-card order controls for template editing; they
     must no longer move Deck Buttons as a side effect;
   - hold a cloned, normalized Deck Layout beside the existing staged templates
     and resynchronize it when a hotkey import replaces configuration, without
     resetting an in-progress edit merely because the active venue changes;
   - change `Show as button` to read/write Deck Placement membership, retain
     color selection only for placed templates, remove a placement when its
     template is deleted, and clear all deck state on Reset;
   - add a module-scope, passive layout-editor component in this file. Use
     native `draggable`, `dragstart`, `dragover`, and `drop` handlers to move
     IDs, not live buttons or order actions. Provide `+ Add row`, remove, and
     labelled keyboard-accessible directional controls; and
   - stage the global label checkbox with the layout and include it in the one
     existing Save call and success path. Do not auto-save a drop.

3. In [`ui/src/chrome/panels/HotkeyDeck.tsx`](../../ui/src/chrome/panels/HotkeyDeck.tsx)
   and [`ui/src/chrome/panels/OrderTicketPanel.tsx`](../../ui/src/chrome/panels/OrderTicketPanel.tsx):

   - resolve Deck Rows from `config.hotkeyDeck`, using a template-ID map and
     preserving the normalized row/within-row order;
   - render one non-wrapping, horizontally scrollable row per Deck Row, with
     stable row/button test IDs; and
   - gate the already-existing `Keycap` on global Hotkey Label Visibility.
     Keep `deckToneClass`, the ticket divider, quote/account props, and shared
     `fireTemplate` invocation intact; only show Strip 5 when at least one
     resolved row has a button.

4. In [`ui/src/chrome/backup.ts`](../../ui/src/chrome/backup.ts) and
   [`ui/src/chrome/BackupPanel.tsx`](../../ui/src/chrome/BackupPanel.tsx):

   - extend the hotkeys export payload with optional Deck Layout and label
     visibility, retaining the active-venue scrub and the v1 envelope;
   - harden the existing shape guard so an old file remains valid and malformed
     optional layout data reaches safe normalization rather than throwing;
   - create an old-ID → new-ID map while regenerating imported templates, then
     remap row references before calling `normalizeOrderConfig`; and
   - update the visible import/export copy and shared-window note to say that
     the deck configuration travels with hotkeys.

5. Extend focused tests rather than adding a drag library or a second config
   store:

   - [`actionTemplate.test.ts`](../../ui/src/chrome/exec/actionTemplate.test.ts):
     legacy flat migration, default-off labels, valid multi-row preservation,
     duplicate/missing/empty-row cleanup, idempotence, and compatibility
     membership projection;
   - [`OrderSettingsSection.test.tsx`](../../ui/src/chrome/exec/OrderSettingsSection.test.tsx):
     add/remove placement, row creation/removal, cross-row and same-row moves,
     non-drag controls, global label staging/save, template-deletion cleanup,
     reset, and import resynchronization while preserving the existing hotkey
     capture safety test;
   - [`HotkeyDeck.test.tsx`](../../ui/src/chrome/panels/HotkeyDeck.test.tsx):
     row order, no-wrap row structure, default-hidden and enabled keycaps,
     unknown IDs omitted, and unchanged place/manage click dispatch; and
   - [`backup.test.ts`](../../ui/src/chrome/backup.test.ts) plus
     [`BackupPanel.test.tsx`](../../ui/src/chrome/BackupPanel.test.tsx): exported layout
     fields, old-file import, ID remapping, active-venue preservation, and
     malformed layout safety.

6. Update the durable documentation after implementation:

   - extend [`ui/src/chrome/exec/README.md`](../../ui/src/chrome/exec/README.md)
     with the Action Template/Deck Layout ownership, import behavior, and
     unchanged execution safety contract; and
   - extend [`ui/src/chrome/panels/README.md`](../../ui/src/chrome/panels/README.md)
     with the Order Ticket's embedded, row-preserving Hotkey Deck behavior.
     The agreed glossary is already recorded in [`CONTEXT.md`](../../CONTEXT.md).

## Validation

Start with the focused execution, settings, deck, and backup tests. Then run
the CI-equivalent Windows checklist required for this substantial persisted UI
change, plus the proportional browser flow:

```powershell
Set-Location engine
go test ./...
go test -race -short ./...
go vet ./...
golangci-lint run
Set-Location ..
mingw32-make -C engine gen-ts-check
Set-Location ui
npm ci
npm run lint
npm test
npm run build
npm run e2e
Set-Location ..
git diff --check
```

Manually verify a narrow Order Ticket with multiple Deck Rows; dragging within
and across rows; temporary-row cancellation; keyboard-only movement; a deck
click while disarmed; a legacy saved configuration; and an old/new hotkeys
JSON import. Confirm a global label toggle changes every visible Deck Button
only after Save and does not fire an action while editing Settings.

## Rollout and rollback

- This is a UI-only, backward-compatible `orderConfig` extension. A legacy
  flat deck loads as one row and an old export remains importable; no engine or
  database migration is needed.
- New exports carry the row layout and global label preference in the existing
  hotkeys envelope without carrying `activeVenue`.
- The compatibility projection keeps the flat membership current so a rollback
  to the prior UI still exposes the same buttons, albeit as its former single
  wrapping list and without the new global label preference.
- If a release regression occurs, revert the UI change as one unit. Do not
  fall back to two independent live layout sources.

## Risks

- Native HTML drag events can be less discoverable than a dedicated library;
  visible targets and complete non-drag controls prevent drag from becoming a
  required interaction.
- A stale imported row ID could otherwise render the wrong or no action. The
  importer remaps IDs and normalization drops invalid/duplicate references
  before rendering.
- Settings already has careful staged-state resynchronization. Deck state must
  follow the same import boundary without clobbering unsaved edits caused by an
  unrelated active-venue update.
- A wrapping row would erase the trader's configured grouping at narrow widths.
  The renderer must preserve rows with horizontal overflow and needs manual
  narrow-width verification.
- Deck clicks are trading controls. The implementation must remain a rendering
  and layout change only, retaining the shared fire path and engine validation.
