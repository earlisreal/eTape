# Order ticket compact redesign, linked venue selection, auto-arm

**Date:** 2026-07-08 · **Status:** approved
**Revises:** UI design (`2026-07-03-ui-design.md`) order-ticket section; execution design
(`2026-07-03-portfolio-orders-design.md`) arm-at-boot behavior; multi-broker design
(`2026-07-04-multi-broker-execution-design.md`) moomoo-venue boot handling.

Three goals, decided in one design pass:

1. The order ticket shrinks to a ~5-row dense layout and loses its KILL / Cancel All
   buttons and armed-state chrome.
2. Venue (broker account) selection becomes a link-group concern: a dropdown in both
   the Account panel and the Order Ticket, synced through the color header, listing
   all configured venues — `alpaca-paper`, `alpaca-live`, `tradezero`, `moomoo`.
3. The arm ritual disappears for paper trading: per-venue `auto_arm` boots paper
   venues armed; live venues keep the deliberate arm click. The engine gate mechanism
   itself (two-layer arm, KILL, day-loss auto-disarm) is unchanged.

## 1. Order ticket — "dense strips" (`OrderTicketPanel.tsx`)

Five strips, top to bottom (~55% shorter than the current 11-block stack; panel
padding 6px, gap 3px):

```
┌──────────────────────────────┐
│ AAPL 189.54/189.56 [TZ▾]  ⚙  │  header blotter line
│ [BUY][SELL][SHORT][COVER]    │  side row (unchanged)
│ [LMT▾][DAY▾][189.54 ][stop ] │  type · tif · price · stop
│ [100 ][Sh▾][ BUY AAPL      ] │  qty · mode · submit
│ [100@Ask][Half][Flatten]     │  preset chips (when presets exist)
└──────────────────────────────┘
```

**Header blotter line** — the panel's signature. Serif bold bare symbol; live mono
bid/ask inline, bid in `palette.up`, ask in `palette.down`, muted `/` separator.
Clicking bid or ask fills the price input (same behavior as today's Bid/Ask button
row, which this replaces). No quote → `—/—`, clicks no-op. Right side: venue select
(see §3) and the ⚙ settings button. Keep `data-testid="bid"` / `"ask"`.

**Type/TIF/price/stop strip** — native selects keep full enum values but render
abbreviated option labels: order type via existing `abbrevType` (`LMT/MKT/STP/STPLMT`);
TIF is already short. Price input disabled when type is MARKET. **Stop input is
permanently rendered**, disabled + dimmed unless type is STOP or STOP_LIMIT — no row
reflow on type change.

**Qty/mode/submit strip** — amount input; sizing-mode select with short display
labels (`Sh` / `$` / `BP%` / `Pos`; full mode name in `title`); submit takes the
remaining width, `.btn-primary`, label `{side} {bareSymbol}`. **No armed indicator
anywhere in the ticket** (§4): a blocked submit surfaces as the engine's rejecting
ack toast, same as hotkeys today.

**Removed outright:** Cancel All button, KILL button, footer spacer, standalone
ARMED/DISARMED line (`data-testid="ticket-armed-state"` deleted), Bid/Ask button row,
labeled Price row, full-width Submit row. KILL stays reachable via `Ctrl+Shift+K`,
cancel-all via `Ctrl+Shift+Backspace` and OpenOrdersPanel's small Cancel All button.
This makes KILL hotkey-only — an accepted, deliberate tradeoff.

**Unchanged behavior:** submit pipeline (sizing → preCheck → toasts → flash text),
preset firing, link-group symbol following, throttled quote.

`registry.tsx` panel description: "Presets, sizing, kill switch" → "Compact entry,
presets, sizing".

## 2. Link groups carry a venue (`linkGroups.ts`)

Each color group gets a focused **venue** alongside its focused symbol:

- `LinkMsg` becomes `{ group, symbol?: string, venue?: VenueID }`; the bus handler
  applies whichever field is present.
- New `focusVenue(group, venue)` (setLocal + bus post — **no engine echo**; venue
  choice is UI-only state) and `venueFor(group): VenueID | undefined`.
- Persistence: a new `linkVenues` workspace-doc key beside the existing `linkGroups`
  symbol map (`hydrate`/`snapshot` extended). The symbol map's shape is untouched, so
  old workspace docs load with no migration.

**Resolution chain, used identically by the ticket, the Account panel, and hotkeys:**

```
venueFor(group) ?? orderCfg.activeVenue ?? status.venues[0] ?? ""
```

Pinned panels (`group === null`) skip the first term and read/write the global
`orderCfg.activeVenue` as today. Grouped panels write `focusVenue` only, leaving
`activeVenue` untouched. `useHotkeys` (mounted on the green group) switches from
`activeVenue || venues[0]` to the full chain — closing today's latent mismatch where
a grouped ticket could display a different venue than the one hotkeys fire at.

## 3. Venue dropdown in both panels

Options come from `stores.exec.status().venues` (all configured venues, §5) —
venue IDs double as display labels; no wire-contract change.

- **Order ticket:** the header venue select becomes group-aware per §2.
- **Account panel:** a venue dropdown (`.ctl mono`, group-aware per §2) joins the
  left end of the stats strip. The stats cells (Equity / Buying Power / Day P&L / Realized) show the
  **selected venue's account row only** (missing row → "—"), and the positions table
  filters to that venue (NET aggregate rows disappear — summing paper and live
  dollars was meaningless anyway). Per-venue arm chips stay all-venue; the panel's
  duplicate master ARMED button is removed — the TopBar chip owns master arm.

## 4. Arm system: auto-arm paper, manual live

Engine mechanism unchanged: two-layer gate (master + venue, both required), KILL =
cancel-all + master disarm, day-loss breach auto-disarms master, flatten/cancel never
gated. What changes:

- `config.Venue` gains `AutoArm bool` (`auto_arm` in TOML). At exec-core boot, venues
  with `auto_arm = true` start with `venueArmed = true`; `masterArmed` starts true
  iff at least one venue auto-arms. Applies in replay mode too (all-SimBroker).
- After a KILL or day-loss disarm, re-arming master is the TopBar chip click —
  auto-arm applies at boot only, never re-arms a dropped switch.
- UI arm surfaces slim to two: TopBar master chip + AccountPanel per-venue chips.
  The ticket shows nothing.

## 5. Engine: moomoo stub venue

`boot.go`'s `case "moomoo"` currently hard-errors ("deferred to v1.x"), which would
prevent boot with moomoo configured. Replace the error with a **stub broker**: the
venue registers normally, appears in `exec.status.venues` with `connected = false`
and note `"execution v1.x"`, and rejects any submit/cancel with a clear message.
No Run loop. When the real moomoo adapter lands (v1.x), only the boot case changes.

## 6. Config: `~/.eTape/config.toml`

Add four venues plus explicit gate caps (gate zero-values mean **unenforced**, so
live venues must carry real numbers — starting values below are deliberately tiny,
matching the standing 1-share live-guardrail scale; edit to taste):

```toml
[[venue]]
id = "alpaca-paper"
broker = "alpaca"
env = "paper"
credentials = "alpaca"
auto_arm = true

[[venue]]
id = "alpaca-live"
broker = "alpaca"
env = "live"
credentials = "alpaca-live"

[[venue]]
id = "tradezero"
broker = "tradezero"
env = "live"
credentials = "tradeZero"
account_id = "<TZ accountId — required, adapter errors without it; verify via read-only accounts endpoint>"

[[venue]]
id = "moomoo"
broker = "moomoo"
auto_arm = true   # harmless: stub rejects submits regardless

[gate.global]
max_day_loss = 100.0

[gate.venue.alpaca-live]
max_order_value = 50.0
max_position_value = 50.0
max_position_shares = 5
max_open_orders = 2

[gate.venue.tradezero]
max_order_value = 50.0
max_position_value = 50.0
max_position_shares = 5
max_open_orders = 2
```

**Safety notes.** With live venues configured, the engine authenticates to TradeZero
and Alpaca-live at every boot — read-only account/position polling, which the
standing safety rule permits. Live venues boot disarmed; placing real orders still
requires a deliberate arm click per session plus the gate caps above, and the
standing rule (no live orders without explicit authorization in the current
conversation) is unaffected by this design.

## 7. Test impact

- `OrderTicketPanel.test.tsx`: delete kill / cancel-all / armed-state (incl. the
  ticket's color-discipline armed test — its subjects no longer exist); retarget
  bid/ask click-to-fill to the header; add stop-input-disabled coverage and a
  venue→`focusVenue` group-sync test.
- `AccountPanel` tests: master-button removal; venue-scoped stats/positions.
- `linkGroups` tests: `focusVenue`/`venueFor`, bus round-trip, `linkVenues`
  hydrate/snapshot, old-doc compatibility.
- Engine: `auto_arm` TOML parse; boot-time arm state; moomoo stub venue listed +
  submit rejected; boot no longer errors on a moomoo venue.
