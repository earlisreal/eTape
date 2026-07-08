# Settings modal redesign: venues & credentials, full template editing, hotkey tooling

**Date:** 2026-07-09 · **Status:** approved
**Revises:** UI design (`2026-07-03-ui-design.md`) §Order entry & hotkeys settings
screen; multi-broker design (`2026-07-04-multi-broker-execution-design.md`) venue
configuration workflow (TOML stays the source of truth but gains a UI writer);
order-ticket design (`2026-07-08-order-ticket-venue-arm-design.md`) §6 — the manual
"fill in the venue TOML" step (Task 12) gets a UI.

Three goals, decided in one design pass:

1. The unified settings modal is redesigned (Daylight Ledger treatment, ~920px,
   four sections) and the Orders & hotkeys section becomes a dense **hotkey grid**
   with a **keycap cheat-sheet strip** — every template parameter editable,
   including the two that today have no input at all: price offset and sizing
   amount.
2. The template model grows two capabilities: **offset unit `$` or `%`** (percent
   scales with price — the marketable-limit lesson from the venue benchmarks) and
   **arbitrary position-percent sizing** (replacing the hardcoded `all | half`).
3. A new **Venues & credentials** section edits `[[venue]]` + `[gate]` config and
   broker API keys from the UI. The engine rewrites `~/.eTape/config.toml` and
   `~/.eJournal/credentials.json` behind four new validated commands; changes
   apply on the next engine restart (no hot-apply). Decided interactively:
   secrets ARE entered in the UI (masked, write-only, never echoed back).

## 1. Modal shell (`SettingsModal.tsx`)

Same open paths (TopBar settings button, order-ticket ⚙ jumping to the orders
section). The shell grows from 680px / 3 tabs to **920 × min(640px, 85vh)** with a
180px left nav:

```
┌──────────────────────────────────────────────────────────────┐
│ Settings          │  Orders & hotkeys                        │
│ ─────────────     │  ────────────────────────────────────    │
│ Appearance        │                                          │
│ ▌Orders & hotkeys │  (section body, scrolls independently;   │
│ Venues & creds    │   section header + footer actions are    │
│ Sounds            │   sticky)                                │
│                   │                                          │
└──────────────────────────────────────────────────────────────┘
```

- Nav: Plex Sans items; the active item gets a **bronze left rule**
  (`palette.accent`) instead of today's background/border swap. Serif "Settings"
  wordmark. Hairline column divider (`palette.border`).
- `SettingsSection` union becomes
  `"appearance" | "orders" | "venues" | "sounds"`.
- Section bodies own their Save/Reset actions (Save lives with the data it
  saves); the shell stays a dumb frame. Overlay-click-to-close unchanged.
- All colors from the existing `Palette`; both themes must render correctly. The
  one new visual primitive is the **keycap chip** (see §2), derived from existing
  tokens (`surface` fill, `borderStrong` border, mono face, 1px bottom shadow in
  `borderStrong` for the key-cap read).
- Appearance and Sounds section *content* is unchanged — restyled only as far as
  the shared shell/typography implies.
- Targeted cleanup: `exec/OrderSettingsModal.tsx` has not been a modal since the
  Task-11 unification — rename file to `exec/OrderSettingsSection.tsx` (test file
  follows).

## 2. Orders & hotkeys section

### Cheat-sheet strip (the signature element)

Pinned above the grid. Every bound template rendered as `[keycaps] label`,
grouped **Place** / **Manage**; the KILL entry's keycaps render in
`palette.danger`. The strip renders from the **draft** template state, so edits,
unbinds, and conflicts appear live before saving. Combos involved in a duplicate
binding render their keycaps in `palette.danger` in both the strip and the grid.

```
CHEAT SHEET
Place   [Ctrl 1] Buy $5k   [Ctrl 2] Buy 25% BP   [Ctrl 3] Sell ½   [Ctrl 4] Flatten
Manage  [Ctrl ⌫] Cancel Last   [Ctrl ⇧ ⌫] Cancel All   [Ctrl ⇧ K] KILL
```

### The grid

One aligned row per template (CSS grid, fixed column template — not today's
flex-wrap). Columns:

```
LABEL      SIDE  TYPE   TIF  PRICE  OFFSET      SIZE            KEY        ×
Buy $5k    BUY   LIMIT  DAY  Ask    +0.05 [$▾]  Dollar ▾  5000  [Ctrl 1]×  ×
Buy 25%BP  BUY   LIMIT  DAY  Ask    +0.10 [%▾]  BP % ▾      25  [Ctrl 2]×  ×
Sell ½     SELL  LIMIT  DAY  Bid    −0.05 [$▾]  Pos % ▾     50  [Ctrl 3]×  ×
Cancel Last     Cancel Last ▾ ──────(manage action spans)────── [Ctrl ⌫]×  ×
```

- **Place rows**: label input, side/type/tif/priceSource selects (as today),
  then the two new inputs:
  - **Offset** — signed decimal input + unit select (`$` | `%`).
  - **Size** — mode select + one adaptive value input: `Dollar` → dollar amount,
    `BP %` → percent 0–100, `Shares` → integer, `Pos %` → percent 0–100.
- **Manage rows**: label input + an **action select**
  (`CancelLast | CancelAllFocused | CancelAllEverything | KillSwitch`) spanning
  the parameter columns. (Today `kind` is fixed at creation and manage templates
  cannot be created at all.)
- **+ Add** becomes a two-option menu: **Order template** / **Management
  action**.
- **Hotkey cell**: the existing capture mechanism is kept verbatim — readOnly
  field, `normalizeCombo`, and the load-bearing `stopPropagation` (the settings
  screen must stay inert with zero order-safety authority; the comment moves with
  the code). Restyled as a keycap chip; bound combos gain an **unbind ×**.
- **Conflict detection**: duplicate normalized combos across draft templates mark
  both rows' KEY cells + cheat-sheet entries in `palette.danger` with a
  "duplicate binding" note, and **disable Save** until resolved. Scope is
  template-vs-template only (no app-reserved-combo registry in this pass).
- **Reset to defaults**: restores `DEFAULT_TEMPLATES` into the draft after an
  inline confirm (two-click: "Reset to defaults" → "Confirm reset"). Still needs
  Save to persist.
- The read-only gate-limits block moves to the Venues section (§4), where those
  values become editable.
- Save semantics unchanged: one `SetConfig` of the whole `orderConfig` blob;
  `OrderConfigProvider` remains the single source both the hotkey engine and the
  ticket read.
- Preserved `data-testid`s: `tmpl-label-*`, `tmpl-hotkey-*`, `add-template`
  (now the menu trigger), `save`.

## 3. Template model & resolution changes

`ui/src/chrome/exec/actionTemplate.ts`, `sizing.ts`, `priceSource.ts`:

- `PlaceOrderTemplate` gains `priceOffsetUnit?: "$" | "%"` (absent → `"$"`, so
  every persisted config is already valid).
  `resolvePrice(source, offset, unit, quote)`: `$` → `base + offset` (today's
  behavior); `%` → `base + base * offset / 100`. Offset stays signed.
- `SizingSpec` `PositionFraction` mode reads `pct` (0–100, shared field with
  `BuyingPowerPct` — modes are mutually exclusive).
  `resolveShares`: `floor(|positionQty| * pct / 100)`. The legacy
  `fraction?: "all" | "half"` field stays on the type as input-only.
- **One migration point**: `normalizeOrderConfig(config)` applied where the
  config enters the app (`OrderConfigProvider` after `GetConfig`, and to
  `DEFAULT_ORDER_CONFIG`): `fraction: "all"` → `pct: 100`, `"half"` → `pct: 50`,
  missing `priceOffsetUnit` → `"$"`. Everything downstream (hotkey engine, ticket
  presets, grid, AccountPanel flatten) sees only the normalized shape. Defaults
  in `DEFAULT_TEMPLATES` are updated to the new shape outright.
- The order ticket's `Pos` sizing mode today **ignores the amount input** and
  hardcodes `fraction: "all"` (`OrderTicketPanel.submitManual`). It now builds
  `{ mode, pct: Number(amount) || 0 }` like the other modes — the amount input
  is percent of position, 100 = flatten — consistent with the grid.

## 4. Venues & credentials section (new, `chrome/exec/VenuesSection.tsx`)

Three blocks, top to bottom:

```
⚠ Engine restart required — saved venue config differs from the running engine   (bronze banner, only when true)

VENUES                                                        [+ Add venue]
────────────────────────────────────────────────────────────────────────
tradezero    TradeZero   [LIVE]   acct TZxxxx   manual arm    edit  remove
alpaca-paper Alpaca      [PAPER]  —             auto-arm      edit  remove
  ▸ expanded edit form: id · broker ▾ · env ▾ · credentials key ▾ ·
    account id · auto-arm toggle (disabled+off when env=live) ·
    gate caps: max order value · max position value · max position
    shares · max open orders
GLOBAL LIMITS   max day loss [    ]  symbol value [    ]  symbol shares [    ]

CREDENTIALS                                                   [+ Add key]
────────────────────────────────────────────────────────────────────────
tradeZero     used by: tradezero            replace   delete(blocked)
alpaca        used by: alpaca-paper         replace   delete(blocked)
alpaca-live   used by: —                    replace   delete
  ▸ add/replace form: name · key id (masked) · secret key (masked) — write-only

                                            [Save venues & limits]
```

- **Venue rows**: mono venue id; broker name; env badge — `LIVE` chip in
  `palette.danger`, `PAPER` in muted; account id; auto-arm state; gate-caps
  summary. Edit expands the row inline; remove asks an inline confirm.
- **Env badge discipline**: LIVE rows carry the danger treatment everywhere they
  appear (list, edit form header) — this section is the one place in the app
  where you can point order flow at real money, and it should read that way.
- **Credentials rows**: key name + "used by" (computed from the venue list) only.
  **keyId and secretKey are never sent to the UI** — the add/replace form's
  masked fields are write-only and cleared after a successful save. Delete is
  disabled while any saved venue references the key.
- **Restart banner**: shown when the *file* state differs from the *running*
  state (both returned by `GetVenueSetup`). Venue/gate/credential edits never
  touch the running engine; they apply at next boot.
- Save = one `SetVenueSetup` for venues + gate. Credential add/replace/delete
  fire their own commands immediately (they edit a different file and shouldn't
  ride along with a possibly-invalid venue draft).
- Field-level validation errors from the engine ack render inline under the
  offending block; transport errors use the existing toast.

## 5. Engine: four new commands

New `wsmsg` args/result structs in `engine/internal/uihub/wsmsg`, TS types
regenerated via tygo (`make gen-ts`; drift-gated by `make gen-ts-check`).
Handlers in `uihub/commands.go` behind a new narrow interface (mirroring the
existing `cfg`/`exec` seams) implemented engine-side.

### `GetVenueSetup` → 

```
{ file:    { venues: []Venue, gate: Gate },      // parsed fresh from config.toml
  running: { venues: []Venue, gate: Gate },      // the Config the engine booted with
  credKeys: []string }                           // names only, from credentials.json
```

`Venue` is the `config.Venue` struct verbatim — it contains no secret material
(its `credentials` field is the key *name*). A missing/unreadable credentials
file yields `credKeys: []`, not an error.

### `SetVenueSetup { venues: []Venue, gate: Gate }`

Validation (reject with a field-naming error string; nothing written on any
failure):

| Rule | Why |
|---|---|
| venue ids non-empty, `[a-z0-9-]` only, unique | ids key gate map, events, workspace refs |
| `broker` ∈ tradezero · alpaca · moomoo · sim | adapter registry |
| `env` ∈ paper · live | |
| `env=live` ⇒ `auto_arm=false` (reject, and UI disables the toggle) | live venues keep the deliberate arm click — enforced server-side, not just UI |
| `broker` ∈ tradezero · alpaca ⇒ `credentials` names an existing key | adapter hard-errors at boot otherwise |
| `broker=tradezero` ⇒ `account_id` non-empty | TZ adapter requirement |
| gate caps ≥ 0; `gate.venue` keys ⊆ venue ids | 0 = off convention preserved |

Write path: the engine re-reads `config.toml`, decodes into the full `Config`,
replaces only `Venues` and `Gate`, re-encodes the **whole** file with
`BurntSushi/toml`'s encoder, and writes temp-file + rename (atomic). Before the
**first** UI-driven rewrite ever, copy the original to `config.toml.bak` (create
only if absent — the hand-written original, comments and all, is preserved
forever; subsequent saves don't touch the .bak). Accepted trade-off, decided
interactively: comments/ordering in `config.toml` are lost on the first UI save.
(Keys unknown to the `Config` struct are also dropped by the decode→encode
round-trip; the struct is the engine's entire config surface, so only unused
keys can be lost.)

### `PutCredential { name, keyId, secretKey }` / `DeleteCredential { name }`

- All three fields non-empty; put-with-existing-name = replace (the UI's
  "replace" flow).
- Read-modify-write of `credentials.json` via `map[string]json.RawMessage`:
  entries other than the target are preserved **byte-for-byte** (the file is
  shared with eJournal; its entries must survive untouched even if they grow
  fields eTape doesn't know). Atomic temp + rename, file mode 0600. Missing file
  on first put → created.
- `DeleteCredential` rejects while any venue in the current **file** config
  references the name.
- Secrets are never logged (existing `creds` package rule), never included in
  any ack/result, and never readable back over the WS.

## 6. Safety invariants

- **No runtime order authority is added.** Nothing in settings arms a venue,
  places, or cancels; venue/gate/credential edits are file-only and take effect
  at next boot. The running gate and arm state are untouched by every new
  command. KILL remains hotkey-only.
- The hotkey-capture field keeps its `stopPropagation` inertness (a combo typed
  while binding must never fire the live action).
- `live ⇒ no auto-arm` is enforced in engine validation, not just UI state.
- Secrets: entered masked, sent once over the localhost WS on save (accepted
  trade-off), write-only thereafter; UI state holding them is cleared on save.
- The engine never writes either file except through the validated commands.

## 7. Testing

**UI (vitest):**
- Grid: every place-row field editable and round-trips through Save (offset
  value + unit, each sizing mode's amount); manage-row action select; add menu
  creates both kinds; remove; reset-to-defaults confirm flow.
- `normalizeOrderConfig`: fraction all/half → pct 100/50; missing unit → `$`;
  idempotent on already-normalized configs.
- `resolvePrice` `%` branch (signed, both directions); `resolveShares` position
  pct (0/50/100, rounding).
- Conflicts: duplicate combo flags both rows + disables Save; unbind clears it;
  cheat-sheet reflects draft edits and conflict state.
- Venues: LIVE badge + disabled auto-arm on env=live; delete-credential blocked
  while referenced; credential inputs cleared after save and never rendered
  back; restart banner iff file ≠ running.
- SettingsModal: four nav sections route correctly.

**Engine (go test):**
- `SetVenueSetup` validation table (every rule above, accept + reject cases).
- TOML rewrite round-trip: load → set → re-load equals what was set, non-venue
  sections (opend/feed/md/store/uihub/scan/news/health/backfill) byte-equal
  values; `.bak` created once with original content, not overwritten on second
  save.
- Credentials merge: unknown sibling entries preserved byte-for-byte; replace
  overwrites only the target; 0600 mode; delete-referenced rejected.
- `GetVenueSetup` result contains no `secretKey`/`keyId` material (marshal the
  result, assert absence of the stored secret strings).

**E2E (Playwright smoke):** open settings → orders: change a template's dollar
amount, save, fire its hotkey against the replay engine, assert the flash/order
qty reflects the new amount. Venues: section renders, add-venue validation error
surfaces inline (no engine restart in E2E).

## 8. Out of scope

- Hot-applying venue changes at runtime (adapter spin-up/teardown) — restart
  remains the boundary.
- Editing the *running* gate; an app-reserved-hotkey registry; capture-UX
  overhaul (explicitly not selected).
- moomoo trade-unlock anything (standing rule: never in eTape).
- Filling in Earl's real TZ/Alpaca values — this spec builds the tool; the
  values themselves are still entered by Earl (Task 12 executes through this UI
  once it lands).
