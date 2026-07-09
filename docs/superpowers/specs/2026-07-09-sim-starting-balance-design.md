# Sim venue starting balance + reset action

Date: 2026-07-09
Status: approved

## Problem

The `sim` broker (`engine/internal/broker/sim/sim.go`) seeds every venue's
account snapshot at all-zero (`exec.AccountSnapshot{Venue: venue}`) and
nothing in the running system ever calls `SetAccount` outside tests. There is
no config knob and no UI control to fund a sim venue, so today its Account
panel always shows $0 equity/buying power/cash. There is also no way to reset
a sim venue back to a clean state once orders/positions have accumulated
during a session.

## Goals

- A per-venue config field that funds a `sim` venue with a starting balance at
  boot.
- A UI action that resets a running sim venue back to that starting balance,
  wiping its positions and resting orders, while leaving fill/order history
  (Trade History, persisted exec events) untouched.

## Non-goals

- No funding/reset support for real venues (TradeZero, Alpaca, moomoo) — the
  starting-balance field and reset action are meaningless for a broker backed
  by a real account and are structurally sim-only (see Capabilities below).
- Reset does not touch arm/master-arm state. If day-loss auto-disarm has
  tripped a venue, resetting its balance does not re-arm it — arming stays a
  deliberate, separate action, consistent with the rest of the exec design.
- No change to how Trade History / round-trip aggregation works. Reset is
  designed to be invisible to that layer by construction (see Data flow).

## Config

`config.Venue` (`engine/internal/config/config.go`) gains one field:

```go
type Venue struct {
    ID          string
    Broker      string
    Env         string
    Credentials string
    AccountID   string
    AutoArm     bool
    StartingBalance float64 `toml:"starting_balance"` // sim only; <=0 => 100_000 default
}
```

A resolver, e.g. `func (v Venue) EffectiveStartingBalance() float64`, returns
`v.StartingBalance` when positive, else the package default
`DefaultSimStartingBalance = 100_000.0`. `ValidateVenueConfig` rejects
negative values; zero/absent is valid and means "use the default."

The field is stored on every venue regardless of broker (simplest schema) but
is only ever read for `Broker == "sim"`.

## Engine: capability + broker interface

`exec.Capabilities` (`engine/internal/exec/broker.go`) gains:

```go
type Capabilities struct {
    NativeReplace    bool
    FlattenAll       bool
    OvernightSession bool
    ResetBalance     bool // sim only
}
```

`sim.Broker.Capabilities()` sets `ResetBalance: true`; TradeZero, Alpaca, and
the moomoo stub leave it at the zero value (`false`). This mirrors exactly how
`FlattenAll` already gates `Flatten` per-adapter.

`exec.Broker` gains one interface method:

```go
ResetBalance(ctx context.Context, startingCash float64) error
```

`sim.Broker.ResetBalance` composes existing primitives rather than
duplicating their logic:

```go
func (b *Broker) ResetBalance(ctx context.Context, startingCash float64) error {
    if err := b.CancelAll(ctx, ""); err != nil {
        return err
    }
    if err := b.Flatten(ctx); err != nil {
        return err
    }
    b.SetAccount(exec.AccountSnapshot{
        Equity: startingCash, BuyingPower: startingCash,
        AvailableCash: startingCash, SodEquity: startingCash,
    })
    return nil
}
```

TradeZero/Alpaca/stub each get a one-line `ResetBalance` that returns an
"unsupported" error, matching their existing unsupported-capability style.
Since `Core` checks `Capabilities().ResetBalance` before calling, these are
never actually invoked in practice — they exist only to satisfy the
interface.

## Engine: command + Core wiring

New sealed command:

```go
type ResetBalance struct{ Venue VenueID }
func (ResetBalance) isCommand() {}
```

`Core` gains a per-venue starting-balance table, baked in at construction —
same treatment as `GateConfig` and `AutoArm`:

```go
type CoreConfig struct {
    // ...existing fields
    StartingBalance map[VenueID]float64 // sim venues only
}
```

`handleCmd` dispatches to:

```go
func (c *Core) handleResetBalance(ctx context.Context, cm ResetBalance) CmdAck {
    b := c.brokers[cm.Venue]
    if b == nil {
        return CmdAck{Accepted: false, Reason: "unknown venue"}
    }
    if !b.Capabilities().ResetBalance {
        return CmdAck{Accepted: false, Reason: "reset balance unsupported on venue"}
    }
    amount := c.startingBalance[cm.Venue]
    go func() {
        if err := b.ResetBalance(ctx, amount); err != nil {
            slog.Warn("exec: reset balance failed", "venue", cm.Venue, "err", err)
        }
    }()
    return CmdAck{Accepted: true}
}
```

The command carries no amount — it is always the *booted* value from
`Core.startingBalance`, never a value read fresh from the UI's unsaved
Settings draft. This matters because `VenuesSection`'s draft can differ from
what's actually running (the file-vs-running split it already tracks via
`GetVenueSetup`); reset always targets live reality, not an edit in progress.

`boot.go`/`main.go` populate `CoreConfig.StartingBalance` from
`cfg.Venues` where `Broker == "sim"`, using `Venue.EffectiveStartingBalance()`.

## Data flow / why history survives untouched

- `CancelAll` cancels each resting order and emits `OrderCanceled` per order —
  these implement `Event` and get persisted via `appendAndFold`, same as any
  other cancel. This is correct: canceling resting orders as part of a reset
  is real history, not an erasure.
- `Flatten` zeroes positions and emits `BrokerPositions` — a transient
  reconcile signal, never persisted, exactly as today's manual Flatten button
  behaves.
- `SetAccount` overwrites the account snapshot and emits `BrokerAccount` —
  also transient, never persisted.
- None of these three touch the exec-event journal's `OrderFilled` records or
  `store.QueryFillsSince` (the Trade History / closed-round-trip seed). Trade
  History and the fill-derived closed-trades table are therefore unaffected
  by construction — no special-case code needed to "preserve" them.

## UI

`wsmsg.Venue` (wire type) and the TS `Venue` type gain `startingBalance:
number`, threaded through `GetVenueSetup`/`SetVenueSetup` alongside the
existing fields.

**Settings → Venues (`ui/src/chrome/exec/VenuesSection.tsx`):**

- A new "starting balance" number input in the Connection field group,
  rendered only when `v.broker === "sim"` (same conditional pattern as the
  existing `showCreds`).
- `addVenue()`'s draft literal gets `startingBalance: 100000` pre-filled for
  new sim rows.
- A "Reset balance" button in the venue card header (next to "Remove"),
  rendered only when `v.broker === "sim"` **and** the venue's `id` is present
  in `setup.running.venues` with `broker === "sim"` — i.e. it must actually be
  a live, booted sim venue, not a pending draft addition awaiting restart.
- Confirmation reuses this file's existing two-click pattern
  (`removeConfirmIdx`): "Reset balance" → "Reset to $100,000? / Confirm reset
  / Cancel", rather than a `window.confirm()`, for visual consistency with the
  adjacent Remove control.
- On confirm, sends the new `ResetBalance` command and toasts the outcome
  (success or the ack's rejection reason). No local account-state rendering
  is needed here since this panel doesn't display live balances.
- The file's header comment ("Edits are FILE-ONLY... nothing here arms a
  venue or changes the running gate") gets updated to carve out this one
  documented exception — Reset balance is a live command, not a file edit.

**Engine command plumbing:**

- `wsmsg.ResetBalanceArgs{Venue string}`, mirroring `FlattenArgs`.
- `commands.go` case `"ResetBalance"` → `cd.ex.Do(exec.ResetBalance{Venue:
  exec.VenueID(a.Venue)})`.
- `ui/src/chrome/exec/commands.ts` gets `resetBalance(venue): Promise<AckMsg>`
  (returns the ack, unlike `flatten()`'s void wrapper, so the Settings screen
  can show a rejection reason).

## Testing

- `sim` package: `ResetBalance` cancels resting orders (emits
  `OrderCanceled` per order), zeroes positions (emits `BrokerPositions`),
  and sets the account snapshot to the starting cash (emits `BrokerAccount`);
  a table/integration test confirms the exact event sequence and that a
  filled order beforehand does not reappear or get reversed.
- `exec` package: `Core.handleResetBalance` — unknown venue → blocked;
  non-sim venue (`Capabilities().ResetBalance == false`) → blocked with
  reason; sim venue → accepted, and the resulting `AccountUpdate`/
  `PositionUpdate` reflect the reset. A test confirms `QueryFillsSince` /
  seeded trades are unaffected by a reset (i.e. Trade History survives).
- `config` package: `EffectiveStartingBalance` defaults zero/absent to
  100,000; `ValidateVenueConfig` rejects negative values.
- UI: `VenuesSection.test.tsx` — starting-balance field only renders for
  `broker === "sim"`; reset button only renders for a running sim venue;
  two-click confirm flow sends `ResetBalance` with the right venue id; a
  rejection ack surfaces as a toast.
