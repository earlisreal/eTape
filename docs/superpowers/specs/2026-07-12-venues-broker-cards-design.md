# Venues & Credentials Redesign — broker cards, moomoo auto-config, button system

**Date:** 2026-07-12 (rev 2, same day — verified deltas from the second design panel)
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
- **Account discovery:** the existing `trdClient.getAccList` (trd.go:284) is an
  accID-keyed *validator* and cannot list, so `broker/moomoo` gains two exported
  helpers (shared by venueseed and the probe, so discovery and validation cannot
  drift):
  - `ListAccounts(ctx, addr, clientID, clk)` — dials a short-lived, read-only
    OpenD trade connection (throwaway `opend.Client`, same teardown guarantees as
    `VerifyAccount`), sends `Trd_GetAccList` once, returns the raw list;
  - `EligibleLiveUS(acc)` — the eligibility filter: `TrdEnv_Real`, not Master, not
    Disabled, US in TrdMarketAuthList (the same checks `getAccList` enforces for
    env="live"). **The decision rule runs on eligible accounts, not the raw
    list** — Earl's OpenD login exposes ≥3 accounts (real FUTUSG margin + paper HK
    + paper US); unfiltered, "exactly one account" would never fire.
  moomoo venues require a numeric `account_id` (ValidateVenueConfig), so discovery
  is mandatory — never write a placeholder. venueseed uses ClientID `etape-seed`,
  a ~10s probe budget, and runs the probe **outside** the config lock.
  - **Exactly one eligible account** → `venueadmin.SeedMoomooVenue(accID)`:
    re-acquire the mutex, **re-check** marker/venue-existence (TOCTOU close),
    validate, then write `{ID: "moomoo", Broker: "moomoo", Env: "live",
    AccountID: <discovered>}` **plus the marker in ONE atomic file write**
    (`config.WriteMoomooSeed` — a crash can never split "seeded" from "venue
    exists"). No live construction — the venue boots on next restart via the
    existing restart banner. `sys.events` notice (kind `venue.seeded`): "moomoo
    venue configured from OpenD (account NNNN) — restart to activate."
  - **Multiple eligible accounts** → marker-only write + notice ("moomoo: N live
    accounts found — pick one in Settings → Venues"); the moomoo card offers an
    account picker (served by the probe's discovery mode) and one click saves
    through the normal SetVenueSetup path. No silent pick on a real-money venue.
  - **Zero eligible accounts** (definitive successful response, e.g. paper-only
    login) → marker only; the card says "No live US-authorized account found on
    this OpenD login." and offers a re-probe.
  - **Any transport/login error** (including quote-only OpenD login) → **no
    marker, no write**; log warn only (no toast — a quote-only login would nag
    every boot); retry next boot; the card keeps its probe button.
  - **A moomoo venue already exists** (any id — e.g. hand-added before this
    feature) → marker-only write and stop: no duplicate is ever created, and that
    venue's later removal also sticks.
  - Validation failure on the seed write (e.g. a non-moomoo venue holds id
    `moomoo`) → log error, no marker, no write.
  - venueseed never sends `Trd_UnlockTrade` (standing rule) and never touches
    orders.
- **Write serialization (prerequisite):** `venueadmin` gains an internal
  `sync.Mutex` held across the full body of GetVenueSetup / SetVenueSetup /
  PutCredential / DeleteCredential and three new methods —
  `MoomooSeedState() (attempted, venueExists bool, err)`,
  `MarkMoomooSeedAttempted()`, `SeedMoomooVenue(accID) (created bool, err)` —
  closing the documented concurrent-writer gap. An in-process mutex is
  sufficient: `singleinstance.Acquire` already guarantees one live engine per
  store. New config writer: `config.WriteMoomooSeed(path, v *Venue)` — sets
  `Seed.MoomooAttempted = true`, appends `v` when non-nil (nil = marker-only),
  one encode + one atomic write, same `.bak` semantics as `WriteVenueConfig`.
- **Stale-draft race:** after venueseed writes, the hub broadcasts a `sys.events`
  notice; `VenuesSection` listens — with no unsaved edits it silently refreshes;
  with unsaved edits it shows "Venue config changed on disk — Reload" instead of
  letting a later Save clobber the seeded venue.
- **moomoo TestConnection probe:** `venueprobe` **already has** a validate-mode
  moomoo probe (`moomooVerify: moomoo.VerifyAccount`, ClientID
  `etape-trade-probe`) — extend it, don't duplicate it: an empty `accountID` now
  means **discovery mode** (`ListAccounts` + `EligibleLiveUS` →
  `Result{OK: true, Accounts[]}`, reusing the TradeZero multi-account shape); zero
  eligible → `OK: false, Message: "no live US-authorized account found on this
  OpenD login"`. Non-empty `accountID` stays validate mode, unchanged. Same 8s
  command timeout.
- **Status & surfacing:** the moomoo execution adapter populates
  `VenueStatus.note` on ConnDown ("OpenD unreachable"), cleared on ConnUp — the
  first and only writer of that currently-dead field. Because the seed write is
  silent and may sit unapplied for days, surfacing is two-layer: the toast at
  seed time, plus a persistent moomoo-card badge "Auto-configured — restart to
  activate" driven by the file≠running diff (a dismissed toast is never the only
  record).
- **Non-goals:** no mid-session venue construction (restart stays the single apply
  path); no arming changes; no DayPnL fix — the card carries the caveat as copy.
  The dropped `md.ConnUpdate` path is NOT wired into a new UI topic — before the
  venue exists the card sources OpenD reachability from the polled `sys.health`
  engine-moomoo link; two live sources for one fact would be a divergence risk.

## B. Wire contract (one tygo regen)

- `VenueSetup` gains `seed: { moomooAttempted: boolean }` — the moomoo card's state
  machine needs "never attempted" vs "attempted/declined".
- `TestConnection` with broker `"moomoo"` and empty accountID = discovery mode
  (behavioral change only; shapes unchanged).
- New `SysEvent` kinds (no schema change): `venue.seeded`, `venue.seed_declined`.
- Everything else rides the existing contract
  (GetVenueSetup/SetVenueSetup/PutCredential/DeleteCredential/ResetBalance/
  RestartEngine unchanged).

## C. UI — fixed-roster broker cards

`VenuesSection` becomes four fixed cards, single column, in order:

1. **Simulator** — always configured; starting balance, slippage bps, fill latency;
   Reset balance (two-click confirm, live command); no remove.
2. **moomoo** — never shows key/secret. State machine (inputs: file venue exists,
   `seed.moomooAttempted`, sys.health engine-moomoo link, `ExecStatus.Venues`,
   restart diff):
   - `waiting` (no venue, unattempted, link down): "Waiting for OpenD — start the
     OpenD gateway and moomoo configures itself."
   - `probe-ready` (no venue, unattempted, link up): same body + "Check OpenD"
     button (runs TestConnection discovery);
   - `picker` (probe returned >1 eligible account): "OpenD reports more than one
     live account. Pick the account eTape should trade through." + select +
     "Enable moomoo" (adds the venue to the draft; normal Save). A manual probe
     that finds exactly one account pre-fills but still requires the explicit
     Enable click — only the boot-time path writes without a click;
   - `declined` (no venue, attempted): "No live US-authorized account found on
     this OpenD login." + "Check OpenD" re-probe;
   - `pending restart` (file venue exists, not in running): account id (mono) +
     badge "Auto-configured — restart to activate";
   - `configured` (venue running): account id (mono), static LIVE chip, connection
     chip from `VenueStatus.connected`; on disconnect, the `note` text.
   Persistent caption on every configured/pending state — an inline muted-ink
   dashed-border band, deliberately neither bronze nor red (a caveat, not a state
   or a live signal; a flagged narrow palette exception), never a tooltip:
   "Day P&L unavailable — the max-day-loss breaker does not see moomoo losses."
   Remove = standard two-click; the card returns to `declined` (marker persists).
3. **Alpaca** — PAPER and LIVE slot groups (eyebrow labels), each an independent
   write-only key id + secret row + Test connection + status chip (`chip-set`
   "Key saved" when its venue exists) + two-click Remove. Paper caption, always
   visible: "Also powers 1-minute chart history — worth adding even if you never
   trade here." A live key typed into the paper slot errors with "This key belongs
   to a live account — paste it into the Live slot below." (and vice versa), via
   the existing dual-host env auto-detect. Live slot carries static copy (not a
   toast): "Real-money account. Orders require the master arm switch." Filling a
   slot creates its venue on Save (`alpaca` / `alpaca-live`); a live venue on the
   card triggers the danger top-stripe.
4. **TradeZero** — key id + secret + Test connection with detected env/account and
   the existing multi-account select; live gets the danger stripe.

- **Slot model:** cards are a projection of `venues[]` — each slot claims the first
  venue matching its (broker, env) predicate. Existing nonstandard ids (e.g.
  `sim-paper`) are claimed as-is and **never renamed** (fills, gate keys, and
  journal rows reference venue ids). New venues use canonical ids (`moomoo`,
  `alpaca`, `alpaca-live`, `tradezero`).
- **Legacy overflow:** venues beyond the roster render in a collapsed read-only
  "Other venues" list with a Remove button each. Nothing is silently dropped or
  rewritten. **Hard invariant (launch blocker):** the draft model carries every
  unclaimed venue and splices it back into `SetVenueSetup` unmodified — a unit
  test loads N legacy venues, edits one roster slot, saves, and asserts
  byte-for-byte survival of the others.
- **Simulator unconfigured state** (legacy config with no sim venue): body
  "Practice venue with simulated fills — no real money." + "Add simulator" button
  (appends the default sim venue to the draft). Global risk limits stay a
  **always-visible** section below the cards — it owns `MaxDayLoss`, the primary
  defense layer.
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
  `variant: "primary" | "neutral" | "danger" | "quiet"`,
  `size: "sm" | "md"` (sm: 11px / 4×10 padding — the current geometry; md: 12px /
  6×14 for modal CTAs), `confirm?: boolean` — encapsulating the two-click confirm
  + ~3s-timeout-revert pattern currently reimplemented per call site — plus
  `disabled`/`loading`/`iconOnly`. All variants: IBM Plex Sans, 1px border,
  radius 4, 2px bronze `:focus-visible` ring, 120ms transitions, reduced-motion
  respected. Rule: every `variant="danger"` call site uses `confirm` (or an
  equivalent external confirm flow) — enforced by a cheap grep-style UI test so a
  future AI-authored change can't casually recolor a benign button red. The skin
  change never removes or softens an existing confirm step, and no Switch-style
  toggle is generalized into the cards (guards against a per-venue arm creeping
  back).
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
| OpenD flaps | One attempt per process (atomic bool); marker prevents cross-boot re-attempts once definitive |
| GetAccList transport/login failure (incl. quote-only login) | No marker, no write; log warn only; retry next boot; card keeps probe button |
| Paper-only moomoo login | Marker written; card: "No live US-authorized account found…"; re-probe on demand |
| Multiple eligible REAL accounts | No silent pick; marker + picker |
| Earl's real account mix (real margin + 2 paper) | `EligibleLiveUS` filter → exactly one → auto-seeds (the primary-user path works) |
| Crash mid-seed | Venue + marker are one atomic write — never "seeded but no venue" or vice versa |
| User Save races auto-seed | venueadmin mutex + seeder TOCTOU re-check; UI reload guard prevents draft clobber |
| User removes moomoo venue | Marker persists → no re-seed; card offers manual re-probe |
| Pre-existing hand-added moomoo venue | Marker set on first ConnUp (no duplicate); its later removal also sticks |
| Seed id `moomoo` collides with a non-moomoo venue | Validation fails → log error, no write, no marker |
| Config restored from backup | Marker travels inside config.toml |
| Legacy/odd configs | Claimed by (broker, env); extras in read-only "Other venues"; byte-for-byte round-trip |
| Demo/replay | venueseed not constructed; cards render synthetic state, probe UI suppressed |

## Testing

- Go: venueseed table tests against injected Discover/Admin fakes (marker
  semantics, single/multi/zero-account and error paths, once-per-boot — two
  ConnUps → one probe, pre-existing venue → marker-only no-duplicate);
  `venueadmin` `-race` test (goroutines hammering SetVenueSetup interleaved with
  SeedMoomooVenue, no lost update); config `[seed]` round-trip through
  `WriteVenueConfig` + `WriteMoomooSeed` atomicity; `moomoo.ListAccounts` against
  the existing mock OpenD trade server + `EligibleLiveUS` filter table
  (Master/Disabled/non-US/Simulate all excluded) + connection-teardown property;
  venueprobe discovery vs validate mode.
- UI: `VenuesSection.test.tsx` rewritten around the card model (slot resolution
  incl. legacy ids, **legacy-overflow byte-for-byte round-trip — launch
  blocker**, all six moomoo states, Alpaca two-slot save creating both venues,
  stale-draft reload guard, secrets-never-echoed regression); `Button.tsx` tests
  (variants, sizes, confirm state machine + timeout, focus-visible, reduced
  motion, danger⇒confirm grep-style assertion).
- e2e: `settings-redesign.spec.ts` updated — first-run cards, Alpaca paper save →
  restart banner, moomoo picker with mocked TestConnection, auto-configured toast
  + pending-restart badge via injected sys.event.
