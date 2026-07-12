# Venues & Credentials Redesign — broker cards, moomoo auto-config, button system

**Date:** 2026-07-12
**Status:** Approved
**Supersedes:** the venue-list + "Add venue" flow from the settings redesign
(`2026-07-09-settings-redesign-design.md` §venues); everything else there stands.

## Goals

1. **moomoo auto-configuration.** The moomoo venue needs no key/secret in eTape —
   OpenD is the local, already-authenticated gateway. The engine auto-configures the
   moomoo live venue the first time it connects to OpenD successfully.
2. **Per-broker cards.** Replace "Add venue" with a fixed roster of dedicated broker
   cards (Simulator, moomoo, Alpaca, TradeZero), making Alpaca credential setup a
   first-class flow — the paper key also powers 1-minute chart history.
3. **Button refresh.** Replace the generic one-look `.btn` with a shared button
   primitive, applied app-wide.

Decisions locked with Earl: auto-config is **once, if absent** (manual removal
sticks; no re-seeding); Alpaca is **one card, two slots** (Paper + Live, each its
own venue); "Add venue" is **removed entirely**; buttons are a **shared primitive,
app-wide**. Arming stays master-only and manual — nothing here touches the gate.

Note: moomoo became a **live-only** execution venue on main (d728b48, 2026-07-12);
this design assumes it — the seeded venue is `env: "live"` and the moomoo card
shows a static LIVE chip, never an env selector.

## A. Engine — moomoo auto-config

New package `engine/internal/venueseed`, constructed in `main.go` on live boots
only (`-demo`/`-replay` skip it, same gate as `config.SeedDefaultIfMissing`).

- **Trigger:** a hook in `forwardMD` (`engine/cmd/etape/main.go`) — on
  `md.ConnUpdate{Up: true}`, call `venueseed.OnFeedUp(ctx)` (non-blocking; work runs
  on venueseed's own goroutine). Covers boot-time connect and mid-session OpenD
  arrival; `forwardMD` drains every md update, verified.
- **Once-only, two layers:**
  - process-level atomic bool — one attempt per boot; OpenD flapping cannot
    re-trigger it;
  - config-lifetime marker — a **typed section on `config.Config`**:
    `Seed SeedConfig` (`toml:"seed"`), `SeedConfig{MoomooAttempted bool}`
    (`toml:"moomoo_attempted"`). It must be a typed field: `WriteVenueConfig`
    decode→encodes the typed `Config`, dropping unknown keys, so a raw TOML table
    would not survive the next save.
  - Skip immediately if the marker is set OR any `broker == "moomoo"` venue exists
    in the file config.
- **Account discovery:** dial a short-lived `opend.Client` (ClientID `etape-seed`,
  same address as the feed), send `Trd_GetAccList`, filter REAL securities
  accounts. moomoo venues require a numeric `account_id` (ValidateVenueConfig), so
  discovery is mandatory — never write a placeholder.
  - **Exactly one REAL account** → write
    `{ID: "moomoo", Broker: "moomoo", Env: "live", AccountID: <discovered>}` plus
    the marker in one locked venueadmin write. No live construction — the venue
    boots on next restart via the existing restart banner. Push a `sys.events`
    notice: "moomoo venue configured from OpenD — restart to activate".
  - **Multiple REAL accounts** → write the marker only + a notice; the moomoo card
    offers an account picker (served by the new moomoo TestConnection probe) and
    one click saves through the normal SetVenueSetup path.
  - **Zero REAL accounts** (definitive successful response, e.g. paper-only login)
    → marker only; the card says "no live account found" and Enable re-probes on
    demand.
  - **Any transport/login error** (including quote-only OpenD login) → **no
    marker**; retry next boot; the card keeps its probe button.
  - venueseed never sends `Trd_UnlockTrade` (standing rule) and never touches
    orders.
- **Write serialization (prerequisite):** `venueadmin` gains an internal
  `sync.Mutex` held across every read-modify-write, closing the documented
  concurrent-writer gap; venueseed mutates only through a new
  `venueadmin.SeedMoomooVenue(...)` (venue append + marker, one locked write).
- **Stale-draft race:** after venueseed writes, the hub broadcasts a `sys.events`
  notice; `VenuesSection` listens — with no unsaved edits it silently refreshes;
  with unsaved edits it shows "Venue config changed on disk — Reload" instead of
  letting a later Save clobber the seeded venue.
- **moomoo TestConnection probe:** `venueprobe` gains `case "moomoo"`: dial OpenD
  (2s budget inside the existing 8s command timeout) → InitConnect →
  `Trd_GetAccList` → `TestConnectionResult{OK, Env: "live", AccountID, Accounts[]}`
  — reuses the TradeZero multi-account shape; no wire-shape change.
- **Status:** the moomoo execution adapter populates `VenueStatus.note` on ConnDown
  ("OpenD unreachable") — the first and only writer of that currently-dead field.
- **Non-goals:** no mid-session venue construction (restart stays the single apply
  path); no arming changes; no DayPnL fix — the card carries the caveat as copy.

## B. Wire contract (one tygo regen)

- `VenueSetup` gains `seed: { moomooAttempted: boolean }` — the moomoo card's state
  machine needs "never attempted" vs "attempted/declined".
- `TestConnectionArgs.broker` accepts `"moomoo"` (no shape change).
- Everything else rides the existing contract
  (GetVenueSetup/SetVenueSetup/PutCredential/DeleteCredential/ResetBalance/
  RestartEngine unchanged).

## C. UI — fixed-roster broker cards

`VenuesSection` becomes four fixed cards, single column, in order:

1. **Simulator** — always configured; starting balance, slippage bps, fill latency;
   Reset balance (two-click confirm, live command); no remove.
2. **moomoo** — never shows key/secret. State machine:
   - `unconfigured + unattempted`: "Waiting for OpenD — start the OpenD gateway to
     configure moomoo automatically." when the feed is down; a "Check OpenD" button
     when it's up;
   - `picker`: multiple REAL accounts found — account select + Enable;
   - `configured`: account id, static LIVE chip, connection status;
   - `declined`: attempted but no venue — Enable re-probes.
   Status sources: `VenueStatus.connected` once the venue is running; the
   `sys.health` engine-moomoo link before that. Persistent caption: "Day P&L
   unavailable — the max-day-loss breaker does not see moomoo losses."
3. **Alpaca** — PAPER and LIVE slot groups (eyebrow labels), each an independent
   write-only key id + secret row + Test connection. Paper caption: "Also powers
   1-minute chart history." Filling a slot creates its venue on Save
   (`alpaca` / `alpaca-live`); the live slot gets the danger top-stripe treatment.
4. **TradeZero** — key id + secret + Test connection with detected env/account and
   the existing multi-account select; live gets the danger stripe.

- **Slot model:** cards are a projection of `venues[]` — each slot claims the first
  venue matching its (broker, env) predicate. Existing nonstandard ids (e.g.
  `sim-paper`) are claimed as-is and **never renamed** (fills, gate keys, and
  journal rows reference venue ids). New venues use canonical ids (`moomoo`,
  `alpaca`, `alpaca-live`, `tradezero`).
- **Legacy overflow:** venues beyond the roster render in a collapsed read-only
  "Other venues" list with a Remove button each. Nothing is silently dropped or
  rewritten.
- **Remove semantics:** unchanged — venue removed from the draft, credential
  best-effort deleted on Save; the card returns to its unconfigured state. No
  Enabled flag is added to config.
- **Save flow unchanged:** draft edits → Save = PutCredential(s) → SetVenueSetup →
  best-effort DeleteCredential → refresh; restart banner when file ≠ running.
  Credential names stay minted-opaque (`key-<uuid8>`). The paper slot creating the
  paper venue is what makes the 1m-data promise hold —
  `resolveBackfillAlpacaCreds` resolves via the first non-live alpaca venue, so no
  engine creds-resolution change is needed.
- Per-venue risk caps stay inside each card behind the existing collapsible; global
  caps stay a section below the cards. `VenueSetupPrompt.tsx` copy points at the
  cards. Demo/replay boots skip venueseed; cards render the synthetic state with
  probe UI suppressed.

## D. Visual system

- **`ui/src/chrome/controls/Button.tsx`** (wrapping HoverButton's hover mechanics)
  + a refreshed `.btn` variant family in `global.css`. API:
  `variant: "primary" | "neutral" | "danger" | "quiet"`, `size: "sm" | "md"`,
  `confirm?: boolean` — encapsulating the two-click confirm + timeout pattern
  currently reimplemented per call site. All variants: IBM Plex Sans, 1px border,
  radius 4, 2px bronze `:focus-visible` ring, 120ms transitions, reduced-motion
  respected.
  - **primary** — bronze-filled (`accent` bg): the ONLY filled button, one per view
    maximum.
  - **neutral** — the refined current `.btn`: transparent bg, strong border; hover
    = surface bg.
  - **danger** — neutral silhouette with danger border+text, never filled (red
    stays a signal, not a surface); destructive confirms only.
  - **quiet** — borderless, textMuted; disclosure toggles and tertiary actions.
  - Migration: the near-black `.btn-primary` fill is retired; OrderSettingsSection's
    bespoke accent Save converges on the primitive; all ~38 `.btn` call sites are
    swept. TVDialog's chart-dialog buttons stay as-is (separate system by design).
- **`.broker-card` shell CSS** (no shared React card component — card internals
  differ too much): 1px border, radius 6, the `.venue-card` mount animation, and
  the 3px top-stripe as the card's status voice — **bronze = configured + healthy,
  danger = live env, none = unconfigured**. Header: broker name in IBM Plex Serif
  600 over the card's own hairline rule (a quiet echo of the ledger-header double
  rule), status chips right-aligned. Broker identity stays typographic — no logos,
  no per-broker colors; the bronze/red/magenta semantic-color rules stay intact.

## Failure modes → expected behavior

| Scenario | Behavior |
|---|---|
| OpenD never connects | No seed attempt; moomoo card waits; no marker |
| OpenD flaps | One attempt per process; marker prevents cross-boot re-attempts once definitive |
| GetAccList transport/login failure | No marker; retry next boot; card keeps probe button |
| Paper-only moomoo login | Marker written; card: "no live account found"; Enable re-probes |
| Multiple REAL accounts | No silent pick; marker + picker |
| User Save races auto-seed | venueadmin mutex serializes; UI reload guard prevents draft clobber |
| User removes moomoo venue | Marker persists → no re-seed; card offers manual Enable |
| Config restored from backup | Marker travels inside config.toml |
| Legacy/odd configs | Read-only "Other venues" list; no silent rewrites |
| Demo/replay | venueseed not constructed; cards render synthetic state |

## Testing

- Go: venueseed unit tests (marker semantics, single/multi/zero-account and
  error paths, once-per-boot) against a stub opend server; venueadmin race test
  (concurrent SetVenueSetup vs seed); config `[seed]` round-trip through
  `WriteVenueConfig`; venueprobe moomoo case.
- UI: `VenuesSection.test.tsx` rewritten around the card model (slot resolution
  incl. legacy ids, moomoo state machine, Alpaca two-slot save, Other-venues,
  reload guard); `Button.tsx` tests (variants, confirm state machine,
  focus-visible).
- e2e: `settings-redesign.spec.ts` updated — first-run cards, Alpaca paper save →
  restart banner, moomoo picker with mocked TestConnection.
