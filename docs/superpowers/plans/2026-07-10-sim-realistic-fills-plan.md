# Sim broker: realistic fills + live equity — implementation plan

Design doc / context: see the approved plan summary below. This file is the
task-structured execution plan (subagent-driven-development); each `## Task N`
section is self-contained enough for a fresh implementer to work from.

## Context

The sim broker (`engine/internal/broker/sim/sim.go`) models a venue for replay,
E2E, and (v1.5) practice mode. Two real bugs:

1. **Fills ignore bid/ask.** The sim's only price input is the last-trade
   **mark** (`SetMark`). A marketable limit fills at `o.LimitPrice` — the exact
   price the trader typed (`actOnMarkLocked` → `fillLocked(o, o.LimitPrice)`,
   sim.go) — and a market/stop fills at the last trade. No spread, no price
   improvement, no partials. The full L2 book (`feed.Book`, 10 levels) already
   flows through `md.Core` (`BookUpdate`) in both live and replay, but is
   routed only to the UI, never to the broker.
2. **Equity never moves after a fill.** The exec Core and uihub mirror are pure
   pass-throughs of whatever `AccountSnapshot` the broker emits. `fillLocked`
   updates the position but never touches `b.acct` and never emits
   `exec.BrokerAccount`, so displayed equity/cash/buying-power/DayPnL stay
   pinned at the starting balance until a `ResetBalance`.

**Confirmed decisions:** depth-aware fills that walk the L2 book with
size-weighted average price and partial fills; rest-until-a-real-book-arrives
(no last-trade fallback for pricing marketable fills); configurable slippage;
configurable fill latency; fix equity to track realized + unrealized P&L like
a real broker's account snapshot.

## Global constraints (every task)

- Package `sim` "imports exec, never the reverse" (and now also imports
  `feed` for `feed.Book` — `feed` is dependency-free, bottom of the domain
  graph, no cycle risk).
- `sim.Broker`'s mutex (`b.mu`) guards all mutable state (`marks`, `orders`,
  `pos`, `acct`, and the new `books`). Every new field must be read/written
  only under `b.mu`, matching the existing `*Locked` helper convention
  (methods suffixed `Locked` assume the caller holds `b.mu`).
- Events are collected into a `[]exec.BrokerEvent` slice and returned/emitted
  by the caller *after* `b.mu.Unlock()` — never call `b.emit` while holding
  the lock (existing pattern in `SubmitOrder`/`SetMark`/`ReplaceOrder`).
- `exec.Order`, `exec.Fill`, `exec.Position`, `exec.AccountSnapshot`,
  `exec.OrderRequest`, `exec.BrokerEvent` variants (`OrderAccepted`,
  `OrderRejected`, `OrderFilled`, `OrderCanceled`, `OrderReplaced`,
  `BrokerPositions`, `BrokerAccount`) are defined in `engine/internal/exec/types.go`
  and `engine/internal/exec/events.go`/`broker.go` — reuse them; do not
  redefine.
- `feed.Book` / `feed.BookLevel` (`engine/internal/feed/feed.go`): `Book{Symbol
  string, TsMs int64, Bids, Asks []BookLevel}`, `BookLevel{Price float64,
  Volume int64, Orders int32}`. Levels are already best-first (`Bids[0]` = best
  bid, `Asks[0]` = best ask).
- Do not break any existing passing test. Run the full affected package's
  tests (not just new ones) before reporting DONE.
- Follow TDD: write the failing test first, then the implementation.

---

## Task 1: Wire the L2 order book from md.Core to the sim broker

**Goal:** plumbing only — get `feed.Book` snapshots flowing from `md.Core` to
`sim.Broker` the same way last-trade marks already flow, mirroring the
existing `Marks()`/`SetMark` path exactly. No fill-pricing behavior changes in
this task (Task 2 consumes the book; this task only wires and stores it).

**Files:**
- `engine/internal/md/core.go`
- `engine/internal/broker/sim/sim.go`
- `engine/internal/broker/sim/sim_test.go`
- `engine/cmd/etape/main.go`
- `engine/cmd/etape/main_test.go`

**Changes:**

1. `md.Core`: add a `Books() <-chan feed.Book` channel, mirroring `Marks()
   <-chan Mark` exactly:
   - Field `books chan feed.Book` on `Core` (name it differently from the
     existing `books *bookStore` field — e.g. `bookOut chan feed.Book` — do not
     collide with the existing `c.books` bookStore field).
   - Constructed in `New` as `make(chan feed.Book, 1024)` (same buffer size as
     `marks`).
   - Accessor: `func (c *Core) Books() <-chan feed.Book { return c.bookOut }`.
   - Emit helper mirroring `func (c *Core) mark(m Mark)`:
     ```go
     func (c *Core) emitBook(b feed.Book) {
         select {
         case c.bookOut <- b:
         default: // keep-latest downstream; dropping a stale book is safe
         }
     }
     ```
   - In `applyEvent`'s `feed.BookEvent` case, call `c.emitBook(...)` with the
     *stored* book (the value returned by `c.books.set(e.Book)`) alongside the
     existing `c.emit(BookUpdate{...})` — same value, two destinations, do not
     compute it twice:
     ```go
     case feed.BookEvent:
         stored := c.books.set(e.Book)
         c.emit(BookUpdate{Book: stored})
         c.emitBook(stored)
     ```

2. `cmd/etape/main.go`: widen the sink contract and the bridge.
   - Extend the `markSink` interface (currently `SetMark(symbol string, price
     float64)`) to add `SetBook(symbol string, book feed.Book)`. Consider
     renaming the interface to something like `simSink` if `markSink` reads
     oddly once it carries two methods — your call, keep it consistent with
     surrounding comments.
   - `markBridge` currently `select`s only on `core.Marks()`; add a second case
     for `core.Books()` that calls `s.SetBook(m.Symbol, m)` on every sink (the
     book's own `Symbol` field, no need for a wrapper struct).
   - `simSinksOf` is a plain type-assertion loop — it does not need changes,
     but will now require every matched broker to implement the widened
     interface (which `sim.Broker` will after this task, and no other broker
     type-asserts to it today).

3. `broker/sim/sim.go`: add the book side of the interface.
   - New field `books map[string]feed.Book` on `Broker`, initialized in `New`
     alongside `marks`.
   - New method:
     ```go
     // SetBook stores a symbol's latest L2 snapshot. Task 2 makes resting
     // orders re-evaluate against it; for now this only updates the stored
     // book (no crossing side effects yet — SetMark still owns crossing).
     func (b *Broker) SetBook(symbol string, book feed.Book) {
         b.mu.Lock()
         b.books[symbol] = book
         b.mu.Unlock()
     }
     ```
   - Add `"github.com/earlisreal/eTape/engine/internal/feed"` to imports.
   - Update the package doc comment (sim.go top) to note the book is now
     tracked (keep it accurate to what Task 1 actually does — don't claim
     book-based fills yet).

**Tests:**
- `main_test.go`: the existing `recordingSink` (implements `SetMark`) must
  gain a `SetBook` method (mirror `SetMark`'s lock/store pattern) or the
  package will fail to compile once `markSink` widens — this is a required
  fix, not optional. Add a test analogous to `TestMarkBridgeForwardsToSinks`
  that feeds a `feed.BookEvent` via `core.Feed(...)` and asserts the sink's
  `SetBook` was called with the right book (poll with the same
  deadline-loop pattern used for marks).
- `sim_test.go`: a small test that calls `b.SetBook("AAPL", someBook)` and
  then reads back `b.books["AAPL"]` (whitebox — this test file is `package
  sim`) to confirm storage. Keep it minimal; Task 2 adds the behavioral tests.

**Verification:** `go build ./...` and `go test ./internal/md/... ./internal/broker/sim/... ./cmd/etape/...` all green.

**Report:** DONE with commit hash(es), test command + output summary, and note
whether you renamed `markSink` (and to what) so Task 2's dispatch can use the
correct name.

---

## Task 2: Depth-aware fill engine — walk the book, partial fills, TIF, rest-until-book

**Goal:** replace mark-price fills with book-price fills. This is the core of
"make the simulator realistic." Depends on Task 1's `SetBook`/`b.books` (read
Task 1's report for the exact field/method names it landed).

**Files:**
- `engine/internal/broker/sim/sim.go`
- `engine/internal/broker/sim/sim_test.go`

**Behavior to implement:**

1. **Book-walk pricing.** New function (replacing the role of `marketable` +
   the price-selection half of `actOnMarkLocked`/`fillLocked`):

   ```go
   // fillAgainstBook attempts to fill (or partially fill) o against book,
   // consuming price levels on the opposite side of o's side, honoring o's
   // limit (if any) as a per-level price cap. Returns the qty filled and the
   // size-weighted average fill price; qty is 0 if nothing crossed.
   func fillAgainstBook(o *exec.Order, book feed.Book) (filledQty, avgPrice float64)
   ```

   - Buy/Cover consume `book.Asks` (already best-first ascending); Sell/Short
     consume `book.Bids` (best-first descending).
   - For `TypeMarket`: no price cap, sweep levels until `o.LeavesQty` is
     satisfied or the book side is exhausted.
   - For `TypeLimit` (including a `TypeStopLimit` that has triggered and been
     converted, per existing `actOnMarkLocked` pattern): consume a level only
     while `level.Price <= o.LimitPrice` (buy/cover) or `level.Price >=
     o.LimitPrice` (sell/short); stop at the first level that violates the cap.
   - At each consumed level, take `min(remaining, level.Volume)`. Compute the
     size-weighted average price across all consumed levels
     (`Σ(qty_i * price_i) / Σqty_i`).
   - `TypeStop`: not priced here — stops still trigger off the last-trade mark
     (`stopTriggered`, unchanged) and, once triggered, become a marketable
     order that this function then prices like `TypeMarket`.

2. **Partial fills.** If `fillAgainstBook` returns `filledQty < o.LeavesQty`
   (depth ran out, or the next level breaks the limit before the order is
   fully filled):
   - Fill the partial quantity now (update `ExecutedQty`, `LeavesQty`,
     `AvgFillPrice` as a running weighted average across *all* fills for this
     order so far — not just this partial), set `Status =
     StatusPartiallyFilled`, and emit `OrderFilled{F: fill, CumQty:
     o.ExecutedQty, LeavesQty: o.LeavesQty, AvgPrice: o.AvgFillPrice}` —
     `exec/state.go`'s `applyFill` already maps `LeavesQty > 0` to
     `PartiallyFilled` for you; you just need to emit the event with the right
     numbers.
   - **Keep the order resting** (do not delete from `b.orders`) so a later
     `SetBook` can fill the remainder. Only `delete(b.orders, o.ID)` when
     `LeavesQty` reaches 0.
   - This changes `fillLocked`'s contract: today it always fully fills and
     always deletes. Split it — e.g. a `fillLocked(o, qty, px)` that fills
     exactly `qty` at `px`, updates cash/position (Task 3 extends this same
     function), and only deletes+marks-Filled when `LeavesQty` hits 0;
     otherwise marks `PartiallyFilled` and leaves it resting.

3. **Rest-until-book.** Replace the "market order + no mark → reject" branch
   in `SubmitOrder` (today: `if req.Type == exec.TypeMarket && !hasMark {
   reject }`). New rule: a market or marketable-limit order with **no book yet**
   for its symbol does not fill and is not rejected — it is Accepted and rests,
   same as a non-marketable limit today. It fills (fully or partially) the
   first time `SetBook` delivers a real book for that symbol. Only reject a
   market order if... (there is no longer a no-book rejection case — remove it;
   market orders always rest until a book shows up, however briefly).

4. **TIF.** `OrderRequest`/`Order` already carry `TIF` (parsed, currently
   unused). After the *first* fill attempt on submit (i.e., inside
   `SubmitOrder`, once — not on every later `SetBook`):
   - `TIFIOC`: if any quantity remains unfilled after that first attempt,
     cancel the remainder immediately (emit `OrderCanceled` for the order,
     remove from `b.orders`) instead of leaving it resting.
   - `TIFFOK`: all-or-none — if the *first* attempt would not fully fill the
     order, fill nothing (do not partially fill) and reject/cancel it instead.
     (Reuse `OrderRejected` with a reason like `"sim: FOK could not fill
     completely"` — check how the existing reject path in `SubmitOrder`
     structures its rejection event and match that shape.)
   - `TIFDay`/`TIFGTC`: unchanged — rest until filled/canceled (Day-expiry at
     session close is out of scope for this task).
   - IOC/FOK only apply to the *initial* attempt; they do not need special
     handling in `SetBook`'s later crossing pass (a resting IOC/FOK order
     should never exist after submit — either it fully filled, partially
     filled-then-canceled (IOC), or was rejected (FOK)).

5. **Stops sweep the book.** `stopTriggered` (unchanged, still keys off
   `b.marks`/last-trade). Once a `TypeStop` or triggered `TypeStopLimit`
   converts to a marketable order, route it through the same
   `fillAgainstBook` path (using the book if present; if absent, it rests like
   any other marketable order per point 3 above — a triggered stop is not a
   special case for "rest until book").

6. **Wire `SetBook` to trigger crossing**, mirroring `SetMark`'s
   `crossRestingLocked`: `SetBook` (from Task 1) should now call an analogous
   `crossRestingOnBookLocked(symbol, book)` that walks every resting order on
   that symbol (deterministic ID order, same `sort.Strings(ids)` pattern as
   `crossRestingLocked`) and attempts `fillAgainstBook` for each.

**What NOT to do in this task:** account/cash/equity accounting is Task 3.
Keep `fillLocked`'s position update as-is functionally (still last-fill-price
average — Task 3 upgrades it to weighted average) unless the split naturally
requires touching that line; if so, leave a comment that Task 3 will replace
it, don't half-implement Task 3's weighted-average logic here.

**Tests (`sim_test.go`), at minimum:**
- Marketable buy limit priced above the ask fills at the ask (price
  improvement), not at the entered limit. Symmetric case for sell vs bid.
- An order sized larger than the top level's `Volume` fills across multiple
  levels at the correct size-weighted average price.
- Depth thinner than the order's qty → partial fill: `LeavesQty > 0`,
  `Status == StatusPartiallyFilled`, order still present in a `Snapshot()`
  call as working; a follow-up `SetBook` with more depth fills the remainder
  and the order disappears from working orders.
- Market order submitted with no book yet: `OrderAccepted`, no reject, no
  fill; stays working; a later `SetBook` fills it.
- `TIFIOC` order that only partially fills on submit: remainder is canceled,
  not left resting.
- `TIFFOK` order that cannot fully fill against the current book: no fill at
  all, order rejected/canceled, position/orders unaffected.
- A `TypeStop` still triggers off `SetMark` (not `SetBook`) and, once
  triggered, prices off the book.
- `ReplaceOrder` still works — a replace re-evaluates against the current book
  via the same path (keep the existing comment at sim.go about bare
  `TypeStop`/`TypeStopLimit` marketability gotchas; the reasoning still
  applies, just against `fillAgainstBook` instead of `marketable`).

**Verification:** `go test ./internal/broker/sim/... ./internal/exec/...` green (run the exec package too since `state.go`'s partial-fill fold is now exercised for real).

**Report:** DONE with commits, test summary, and explicitly confirm: (a) the
final field/method names for anything Task 3 will touch (`fillLocked`'s new
signature, the book/mark storage field names), (b) whether the no-mark-reject
branch was fully removed or retained for some other case.

---

## Task 3: Fix sim broker equity — cash, weighted-avg positions, realized/unrealized P&L

**Goal:** the second bug Earl found — equity/cash/buying-power/DayPnL never
move after a fill. Make the sim maintain its `AccountSnapshot` the way a real
broker does and emit it. Depends on Task 2's fill path (read Task 2's report
for the final `fillLocked`/equivalent signature).

**Files:**
- `engine/internal/broker/sim/sim.go`
- `engine/internal/broker/sim/sim_test.go`

**Behavior:**

1. **Cash on every fill (partial or full).** For the quantity just filled at
   price `px`: `AvailableCash += cashSign(side) * qty * px`, where buy/cover
   pay cash (`-1`) and sell/short receive cash (`+1`) — same convention as
   `exec/roundtrip.go`'s (unexported) `cashSign`; add an equivalent local
   helper in `sim` rather than importing the unexported one:
   ```go
   func cashSign(side exec.Side) float64 {
       if side == exec.SideSell || side == exec.SideShort {
           return 1
       }
       return -1
   }
   ```

2. **Weighted-average positions + realized P&L.** Replace the "simplistic:
   last fill price" position update (the line `p.AvgPrice = px // simplistic:
   last fill price`) with:
   - If the fill **adds to or opens** a position (same sign as existing
     `p.Qty`, or `p.Qty == 0`): new `AvgPrice = (|p.Qty|*p.AvgPrice +
     qty*px) / (|p.Qty|+qty)`, `p.Qty += signed`.
   - If the fill **reduces or closes/flips** a position (opposite sign, i.e.
     covering/closing): the closed portion realizes P&L —
     `closedQty = min(qty, |p.Qty|)`; `realized += (px - p.AvgPrice) *
     closedQty * longSign` where `longSign` is `+1` if the *existing* position
     was long, `-1` if short (closing a long profits when `px > AvgPrice`;
     closing a short profits when `px < AvgPrice`). Accumulate `realized` into
     `b.acct.Realized`. If the fill's qty exceeds `|p.Qty|` (a flip through
     flat), the remainder opens a new position on the other side at `px` (avg
     price = px, qty = the excess, opposite sign) — do this after realizing
     P&L on the closed portion, don't double-count.
   - `p.AvgPrice` should never change on a fill that purely reduces a position
     (it only changes when adding to/opening); confirm your implementation
     doesn't drift it during a partial-close.

3. **Mark-to-market equity**, recomputed after every fill and after every
   `SetMark`/`SetBook`:
   - `Equity = AvailableCash + Σ over positions(pos.Qty * lastMark[pos.Symbol])`
     using `b.marks` (last-trade; if a symbol has no mark yet, treat its
     position's contribution as `pos.Qty * pos.AvgPrice` so equity isn't
     understated to zero for a position opened before any tick arrived — note
     this fallback in a comment).
   - `BuyingPower = AvailableCash` (v1 — no leverage multiple; note this as a
     v1 simplification in a comment, matching the existing "v1.5 does X"
     comment style in this file).
   - `SodEquity`: set once at boot/reset to the starting balance (already
     happens in `New`/`ResetBalance`; just don't let the new equity-recompute
     logic overwrite it — `SodEquity` is fixed for the session/day).
   - `DayPnL = Equity - SodEquity`.

4. **Emit `exec.BrokerAccount{Account: b.acct}`** appended to the returned
   event slice:
   - On every fill (partial or full) — alongside the existing `OrderFilled` +
     `BrokerPositions`.
   - On every `SetMark` and `SetBook` call, *if* the recomputed `Equity`
     differs from the previous snapshot (avoid spamming identical account
     frames on marks/books that don't touch a held symbol — cheap guard:
     skip the emit if there are no open positions, or if the new Equity ==
     old Equity). The hub already coalesces account frames
     (`acctTick`/`acctPend` in `uihub/hub.go`) so exact frequency isn't
     critical, but avoid a pointless emit on every tick of an untraded symbol.

5. Do not double-emit/double-count with `Flatten`/`ResetBalance` — those
   already set `b.acct` directly (`ResetBalance` reseeds `Equity`,
   `BuyingPower`, `AvailableCash`, `SodEquity`; `Flatten` zeroes positions).
   Your new mark-to-market recompute must not run *inside* those two methods
   in a way that immediately overwrites their reseeded values — they already
   emit their own `BrokerAccount`/`BrokerPositions`; leave them as the
   authoritative reset path.

**Tests (`sim_test.go`):**
- Buy N shares at the ask: `AvailableCash` drops by `N*fillPrice`; a
  `BrokerAccount` event is emitted with the new numbers.
- While holding, moving the mark up/down changes `Equity` (MTM) without
  touching `AvailableCash`.
- Sell to close a profitable long: `Realized` increases by the correct
  amount, `DayPnL` reflects it, position flattens to `Qty == 0`.
- Add to an existing position at a different price: `AvgPrice` becomes the
  correct size-weighted average (not the latest fill price).
- Flip a position through flat (e.g. long 10 → sell 15): realized P&L on the
  10 closed, then a new short 5 opened at the fill price with that as its
  `AvgPrice`.
- A position opened before any mark exists still contributes to `Equity` via
  the `AvgPrice` fallback (doesn't silently read as zero).

**Verification:** `go test ./internal/broker/sim/... ./internal/exec/...` green.

**Report:** DONE with commits, test summary, and the final `Equity`/`DayPnL`
formula as implemented (so Task 6's integration test can assert against it
precisely).

---

## Task 4: Configurable slippage

**Goal:** an adverse-price knob applied to marketable fills, modeling queue
position / hidden liquidity beyond the visible touch. Depends on Task 2's
`fillAgainstBook`.

**Files:**
- `engine/internal/config/config.go`
- `engine/internal/broker/sim/sim.go`
- `engine/cmd/etape/boot.go`
- test files for each

**Changes:**

1. `config.Venue` (alongside the existing `StartingBalance float64
   `toml:"starting_balance"`` field, which documents itself as "sim only;
   <=0 => DefaultSimStartingBalance" — follow that same doc-comment style):
   ```go
   SlippageBps float64 `toml:"slippage_bps"` // sim only; extra adverse bps applied to marketable fills; <=0 => off
   ```
   Add validation near the existing starting-balance negative-value check
   (config.go, search for where `starting_balance` is validated, e.g. around
   the`Validate` method) rejecting `SlippageBps < 0` with a clear error
   message in the same style as the neighboring check.

2. `sim.Broker`: accept a slippage rate at construction. Extend `sim.New`'s
   signature — check Task 1/2's final `New` signature first (it should be
   unchanged from today: `New(venue exec.VenueID, clk clock.Clock, startingCash
   float64)`) and add a parameter, e.g. `slippageBps float64`, OR (preferred,
   to avoid a long positional parameter list growing further in Task 5) change
   to an options/config struct — your call, but if you introduce an options
   struct here, make it additive and keep zero-value = "all knobs off" so
   existing call sites requiring only venue/clk/startingCash keep compiling
   with a `sim.Options{}`/`sim.Config{}` zero value if you go that route.
   Store it on `Broker` (e.g. `slippageBps float64`).
   Apply it inside `fillAgainstBook` (or the caller): for a *marketable*
   fill (i.e., aggressor pays the spread — not for a fully passive
   already-resting limit that simply got crossed by an improving book,
   though for v1 it's acceptable to apply it to every fill price produced by
   `fillAgainstBook`; document whichever choice you make), adjust each
   consumed level's price:
   - Buy/Cover: `effectivePrice = price * (1 + slippageBps/10_000)`
   - Sell/Short: `effectivePrice = price * (1 - slippageBps/10_000)`
   Apply before computing the size-weighted average, not after (per-level,
   not a flat adjustment to the final average — matters when levels differ in
   price).

3. `cmd/etape/boot.go`: pass `v.SlippageBps` (or your chosen zero-value-safe
   equivalent) through to `sim.New(...)` at the `buildBrokers` call site.

**Tests:**
- With `slippageBps = 0`, fills are unchanged from Task 2's behavior
  (regression check — re-run/assert one of Task 2's price-improvement tests
  still holds exactly).
- With `slippageBps > 0`, a buy fills strictly above the raw ask price by the
  expected amount; a sell fills strictly below the raw bid by the expected
  amount.
- Config validation rejects a negative `slippage_bps` with a clear error.

**Verification:** `go test ./internal/broker/sim/... ./internal/config/... ./cmd/etape/...` green.

**Report:** DONE with commits, test summary, and the final `sim.New`
signature / options-struct shape (Task 5 will extend the same construction
path).

---

## Task 5: Configurable fill latency

**Goal:** a submit→fill delay so fills aren't instantaneous, implemented
deterministically (event-time gating, not wall-clock timers) so it works
identically in replay. Depends on Task 4's construction-path shape (read its
report first).

**Files:**
- `engine/internal/config/config.go`
- `engine/internal/broker/sim/sim.go`
- `engine/cmd/etape/boot.go`
- test files for each

**Changes:**

1. `config.Venue`: add
   ```go
   FillLatencyMs int `toml:"fill_latency_ms"` // sim only; submit->fill delay, event-time gated; <=0 => off
   ```
   with the same negative-value validation pattern as Task 4's `SlippageBps`.

2. `sim.Broker`: thread the latency through the same construction path Task 4
   established. On `SubmitOrder`, stamp the order with an eligibility time:
   `eligibleMs = b.now() + fillLatencyMs` (store on the order — `exec.Order`
   has no spare field for this, so track it sim-side in a parallel map, e.g.
   `eligibleMs map[string]int64` keyed by order ID, cleaned up when the order
   leaves `b.orders`).
   - An order is only eligible for `fillAgainstBook` consideration (in
     `SubmitOrder`'s first attempt, in `crossRestingOnBookLocked`, and in the
     mark-based stop-trigger path) once the *triggering event's* `TsMs` is
     `>= eligibleMs`. If the initial submit attempt is itself blocked by
     latency, the order simply rests (Accepted) until a later
     `SetBook`/`SetMark` event crosses the eligibility threshold — this
     reuses the existing resting/crossing machinery, no new state machine
     needed.
   - `TIFIOC`/`TIFFOK` (Task 2) evaluate "the first attempt" as the first
     *eligible* attempt, not literally the submit call — i.e., if latency
     defers the first real evaluation to a later book event, that later
     event's outcome is what IOC/FOK react to, not submit time itself. State
     this in a code comment since it's a subtle interaction between Tasks 2
     and 5.
   - `fillLatencyMs <= 0` must be exactly Task 2's/Task 4's behavior
     (immediate first-attempt eligibility) — this is the regression case to
     test.

3. `cmd/etape/boot.go`: pass `v.FillLatencyMs` through alongside Task 4's
   slippage parameter.

**Tests:**
- `fillLatencyMs = 0`: unchanged from Task 4's behavior (regression).
- `fillLatencyMs > 0`: an order submitted against an already-marketable book
  does NOT fill immediately; it fills once a `SetBook`/`SetMark` call is fed
  with `TsMs >= submitTs + fillLatencyMs` (use `clock.NewFake` — already used
  throughout `sim_test.go` — to control `b.now()` deterministically and feed
  events with explicit `TsMs`).
- The `eligibleMs` bookkeeping map does not leak entries for orders that have
  fully filled or been canceled (check its size/absence of the key after).

**Verification:** `go test ./internal/broker/sim/... ./internal/config/... ./cmd/etape/...` green.

**Report:** DONE with commits, test summary, and a short note on the final
shape of the construction path (venue → `sim.New`) for the controller to
verify `boot.go`'s call site end-to-end in Task 6.

---

## Task 6: Integration coverage + replay-mode verification

**Goal:** prove the whole feature works together (book wiring → depth fill →
equity update → slippage/latency knobs), not just each task's unit tests in
isolation, and exercise it against the real replay path.

**Files:**
- `engine/internal/broker/sim/sim_test.go` (or a new `sim_integration_test.go`
  in the same package)
- Manual/scripted verification against `cmd/etape` replay (see below)

**Work:**

1. **One integration test** in the `sim` package that, without any mocking
   beyond `clock.NewFake`, drives a realistic sequence end-to-end:
   - Construct a `Broker` with non-zero `slippageBps` and `fillLatencyMs`
     (using whatever construction shape Tasks 4/5 landed on).
   - Feed an initial `SetBook` (bid/ask with multiple levels) and `SetMark`.
   - Submit a limit buy larger than top-of-book depth → assert partial fill,
     `PartiallyFilled` status, `AvailableCash` decremented for the filled
     portion only, `BrokerAccount` emitted.
   - Feed a deeper `SetBook` after the latency window elapses → assert the
     remainder fills, order becomes `Filled`, cash/equity reflect the full
     position.
   - Move the mark and confirm `Equity` (MTM) changes without touching
     `AvailableCash`.
   - Submit a sell that closes the position → assert `Realized`/`DayPnL`
     update correctly and match the formula Task 3's report documented.
   - This test doubles as living documentation of the feature — comment each
     phase.

2. **Replay-mode smoke check** (manual, reported in prose — not a Go test):
   run the engine in replay mode against a recorded journal day that
   contains book data (check `docs/` or existing test fixtures for a
   suitable journal — if none exists locally, state that clearly rather than
   fabricating a run) and confirm, via logs or a WS client, that:
   - Fills print at bid/ask levels, not at the submitted limit price.
   - A large test order partial-fills.
   - `exec.account`/`exec.positions` WS frames show equity/cash/DayPnL moving
     as positions open, mark, and close.
   If no suitable replay fixture is available in this environment, say so
   explicitly in the report and rely on the integration test + full unit
   suite as the verification evidence instead of skipping silently.

3. Run the **full engine test suite** (`cd engine && go test ./...`) and
   report the complete pass/fail summary, not just the sim package.

**Verification:** the integration test above, plus `go test ./...` from the
`engine` module root, all green.

**Report:** DONE with commits, the integration test's assertions summarized,
the full `go test ./...` output summary, and the replay-smoke-check outcome
(ran it / fixture unavailable — say which).
