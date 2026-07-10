// Package sim is a deterministic in-memory exec.Broker used for tests, replay,
// and (v1.5) practice mode. Fill PRICING is book-walk based (fillAgainstBook):
// a market or marketable-limit order consumes price levels on the opposite
// side of its L2 book (SetBook), size-weighted across every level consumed,
// honoring a limit price as a per-level cap; an order with too little depth
// partially fills and rests until a later SetBook completes it. Fill
// TRIGGERING is still keyed off the last-trade mark (SetMark) for Stop/
// StopLimit orders only — stopTriggered decides *whether/when* a stop
// converts to a marketable order; fillAgainstBook then decides *at what
// price* it fills, exactly like any other Market/Limit order. A market or
// marketable-limit order with no book yet for its symbol does not reject —
// it rests (same as a non-marketable limit) until a real book arrives; there
// is no "reject for lack of a mark/book" case left in this broker. Fill
// TIMING can be additionally delayed by Options.FillLatencyMs (Task 5): a
// submit->fill delay implemented as deterministic EVENT-time gating (an
// order isn't considered for fillAgainstBook until b.now() clears its
// eligibility deadline), never a wall-clock timer — see eligibleLocked. It
// imports exec, never the reverse.
package sim

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

// Options configures optional fill-realism knobs on a Broker. The zero value
// turns every knob off, so New(venue, clk, startingCash, Options{}) behaves
// identically to the 3-arg-only broker that predates Task 4 — this and every
// later realism knob (Task 5: fill latency) is added as a new Options field
// rather than growing New's positional parameter list further.
type Options struct {
	// SlippageBps is extra adverse bps applied to every book level
	// fillAgainstBook consumes, modeling queue position/hidden liquidity
	// beyond the visible touch; <=0 => off. Mirrors config.Venue.SlippageBps's
	// doc-comment style.
	SlippageBps float64

	// FillLatencyMs (Task 5) is a submit->fill delay modeling round-trip time
	// to a venue: an order is not considered for fillAgainstBook at all until
	// b.now() has advanced at least this many ms past its submission time.
	// It is deterministic EVENT-time gating, not a wall-clock timer — see
	// Broker.eligibleLocked. <=0 => off (immediate first-attempt eligibility,
	// exactly Task 2's/Task 4's behavior). Mirrors config.Venue.FillLatencyMs.
	FillLatencyMs int
}

// Broker is a single-venue simulated broker.
type Broker struct {
	venue         exec.VenueID
	clk           clock.Clock
	slippageBps   float64 // Task 4: see Options.SlippageBps
	fillLatencyMs int64   // Task 5: see Options.FillLatencyMs (ms, matching b.now()'s unit)
	ev            chan exec.BrokerEvent

	mu     sync.Mutex
	marks  map[string]float64
	books  map[string]feed.Book   // latest L2 snapshot per symbol; fillAgainstBook prices fills off it
	orders map[string]*exec.Order // resting (working) orders
	// eligibleMs (Task 5) tracks, per order ID, the earliest b.now() at which
	// that order may be considered for fillAgainstBook — exec.Order itself
	// has no spare field for this, so it is tracked sim-side in this parallel
	// map. Only populated when fillLatencyMs>0 (see markEligibilityLocked);
	// entries are removed the moment an order leaves b.orders (see
	// clearEligibilityLocked and its call sites) so the map never grows
	// unbounded and never answers for an order ID that no longer exists.
	eligibleMs map[string]int64
	pos        map[string]*exec.Position
	acct       exec.AccountSnapshot
	bseq       int64 // broker order-id counter
}

var _ exec.Broker = (*Broker)(nil)

// New builds a SimBroker for a venue, funded with startingCash — the same
// seeding ResetBalance performs, so a fresh boot and a manual reset leave the
// account in an identical state instead of boot defaulting to all-zero. opts
// carries optional realism knobs (Task 4: SlippageBps); pass Options{} for
// the pre-Task-4 defaults.
func New(venue exec.VenueID, clk clock.Clock, startingCash float64, opts Options) *Broker {
	return &Broker{
		venue:         venue,
		clk:           clk,
		slippageBps:   opts.SlippageBps,
		fillLatencyMs: int64(opts.FillLatencyMs),
		ev:            make(chan exec.BrokerEvent, 256),
		marks:         map[string]float64{},
		books:         map[string]feed.Book{},
		orders:        map[string]*exec.Order{},
		eligibleMs:    map[string]int64{},
		pos:           map[string]*exec.Position{},
		acct: exec.AccountSnapshot{
			Venue: venue, Equity: startingCash, BuyingPower: startingCash,
			AvailableCash: startingCash, SodEquity: startingCash,
		},
	}
}

func (b *Broker) Capabilities() exec.Capabilities {
	return exec.Capabilities{NativeReplace: true, FlattenAll: true, OvernightSession: false, ResetBalance: true}
}

func (b *Broker) Events() <-chan exec.BrokerEvent { return b.ev }

func (b *Broker) emit(e exec.BrokerEvent) { b.ev <- e }

// SetMark seeds/moves a symbol's price and crosses any resting orders it makes
// marketable.
func (b *Broker) SetMark(symbol string, price float64) {
	b.mu.Lock()
	b.marks[symbol] = price
	crossed := b.crossRestingLocked(symbol, price)
	crossed = append(crossed, b.markToMarketLocked()...)
	b.mu.Unlock()
	for _, ev := range crossed {
		b.emit(ev)
	}
}

// SetBook stores a symbol's latest L2 snapshot and crosses any resting
// Market/Limit orders it now prices fillable — the book-side analog of
// SetMark's mark-triggered crossing.
func (b *Broker) SetBook(symbol string, book feed.Book) {
	b.mu.Lock()
	b.books[symbol] = book
	crossed := b.crossRestingOnBookLocked(symbol, book)
	crossed = append(crossed, b.markToMarketLocked()...)
	b.mu.Unlock()
	for _, ev := range crossed {
		b.emit(ev)
	}
}

// markToMarketLocked recomputes the account snapshot from the current marks
// and returns a BrokerAccount event IF Equity actually moved as a result —
// e.g. a SetMark on a symbol the account holds. It is deliberately a no-op
// emit (not a no-op recompute) when nothing changed: recomputeAccountLocked
// still runs unconditionally (matching the brief's "recomputed after every
// SetMark/SetBook"), but comparing before/after Equity avoids spamming a
// BrokerAccount frame on every tick of a symbol the account doesn't hold, or
// a repeated mark that doesn't move the number.
//
// prevEquity is captured AFTER the caller's crossing pass (crossRestingLocked
// / crossRestingOnBookLocked), not before SetMark/SetBook started: if that
// crossing pass produced a fill, fillLocked already recomputed the account
// and returned its own BrokerAccount reflecting the fill's impact. Comparing
// against the pre-crossing Equity would make this function re-emit an
// identical duplicate of that frame; comparing against the post-crossing
// Equity means this only fires for a genuinely additional change (the
// mark-to-market effect of the new mark itself), never a repeat. Caller
// holds mu.
func (b *Broker) markToMarketLocked() []exec.BrokerEvent {
	prevEquity := b.acct.Equity
	b.recomputeAccountLocked()
	if len(b.pos) == 0 || b.acct.Equity == prevEquity {
		return nil
	}
	return []exec.BrokerEvent{exec.BrokerAccount{Account: b.acct}}
}

// recomputeAccountLocked derives Equity/BuyingPower/DayPnL from the current
// AvailableCash, open positions, and last-trade marks. It deliberately never
// touches AvailableCash, Realized, or SodEquity: those have their own
// dedicated update paths (fillLocked's cash debit/credit and realized-P&L
// accumulation; SodEquity is set once at boot/reset by New/ResetBalance and
// must stay fixed for the rest of the session no matter how many times this
// runs). Caller holds mu.
func (b *Broker) recomputeAccountLocked() {
	equity := b.acct.AvailableCash
	for _, p := range b.pos {
		mark, ok := b.marks[p.Symbol]
		if !ok {
			// No trade has printed for this symbol yet (e.g. a position
			// opened before any tick arrived) -- fall back to the position's
			// own average cost so it still contributes to equity instead of
			// silently reading as zero until the first tick shows up.
			mark = p.AvgPrice
		}
		equity += p.Qty * mark
	}
	b.acct.Equity = equity
	// v1: no margin/leverage multiple -- buying power is just available
	// cash. v1.5 should scale this by the account's Leverage once margin
	// rules are modeled.
	b.acct.BuyingPower = b.acct.AvailableCash
	b.acct.DayPnL = equity - b.acct.SodEquity
	b.acct.TsMs = b.now()
}

// SetAccount overwrites the venue account and emits a BrokerAccount reconcile
// (the test hook that drives day-loss auto-disarm deterministically).
func (b *Broker) SetAccount(a exec.AccountSnapshot) {
	a.Venue = b.venue
	b.mu.Lock()
	b.acct = a
	b.mu.Unlock()
	b.emit(exec.BrokerAccount{Account: a})
}

func (b *Broker) now() int64 { return b.clk.Now().UnixMilli() }

// markEligibilityLocked records o's Task-5 fill-latency deadline at the
// moment it starts resting in b.orders: submitMs + fillLatencyMs. It
// deliberately does NOT write an entry at all when fillLatencyMs<=0 (rather
// than writing submitMs+0), so eligibleLocked's "not gated" fast path is a
// genuine map-lookup miss — the zero-knob case never touches eligibleMs,
// which is how the regression requirement (byte-for-byte Task 4 behavior)
// holds. Every order gets this call regardless of type (Market/Limit/Stop/
// StopLimit alike): the delay models venue round-trip time for the ORDER
// itself, not "time since a stop trigger", so a stop that triggers later
// still owes the same deadline measured from its own submission. Caller
// holds mu.
func (b *Broker) markEligibilityLocked(o *exec.Order, submitMs int64) {
	if b.fillLatencyMs <= 0 {
		return
	}
	b.eligibleMs[o.ID] = submitMs + b.fillLatencyMs
}

// eligibleLocked reports whether o may be considered for fillAgainstBook as
// of eventMs. Absence from eligibleMs reads as eligible: that covers both
// fillLatencyMs<=0 (never populated) and an order that has already left
// b.orders (nothing left to gate) — either way there is no deadline to
// enforce. Time in this broker only ever moves forward (b.clk is either the
// real system clock or, in tests/replay, a clock.Fake advanced monotonically
// via Advance/AdvanceTo), so once this returns true for an order it stays
// true for the rest of that order's life; the entry is removed only when the
// order itself is removed (clearEligibilityLocked), not when it first
// becomes eligible. Caller holds mu.
func (b *Broker) eligibleLocked(o *exec.Order, eventMs int64) bool {
	deadline, gated := b.eligibleMs[o.ID]
	return !gated || eventMs >= deadline
}

// clearEligibilityLocked removes o's latency bookkeeping. Every code path
// that deletes an order from b.orders (full fill, FOK reject, IOC
// cancel-remainder, CancelOrder, CancelAll) has a matching call here, so
// eligibleMs never leaks an entry for an order that no longer exists. A
// no-op map-delete on an absent key when fillLatencyMs<=0 ever left nothing
// to clean up. Caller holds mu.
func (b *Broker) clearEligibilityLocked(id string) {
	delete(b.eligibleMs, id)
}

// marketable reports whether price satisfies limit's directional cap for
// side: buy/cover can pay up to limit (price <= limit), sell/short can give
// up down to limit (price >= limit). Originally the whole "is this resting
// order fillable against the mark" check; Task 2 replaced that role with
// book-walk pricing (fillAgainstBook), but the same directional comparison
// is exactly what a per-level price cap needs, so fillAgainstBook reuses it
// with price = the book level under consideration instead of the mark.
func marketable(side exec.Side, limit, price float64) bool {
	switch side {
	case exec.SideBuy, exec.SideCover:
		return limit >= price
	default: // Sell, Short
		return limit <= price
	}
}

// fillAgainstBook attempts to fill (or partially fill) o against book,
// consuming price levels on the opposite side of o's side (best-first, per
// feed.Book's contract), honoring o's limit (if any) as a per-level price
// cap. It is a pure pricing function: it reads o's Side/Type/LimitPrice/
// LeavesQty but never mutates o or any broker state — the caller applies
// the result (see fillLocked). Returns the qty filled and the
// size-weighted average fill price across every level consumed; qty is 0
// if nothing crossed (e.g. no book yet, or the very first level already
// violates the limit cap).
//
// TypeMarket sweeps levels uncapped until LeavesQty is satisfied or the
// side is exhausted. TypeLimit consumes a level only while it satisfies
// marketable(o.Side, o.LimitPrice, level.Price), stopping at the first
// level that would cross the limit (since the book is best-first, every
// level after that is worse). TypeStop/TypeStopLimit are never priced
// here directly: stopTriggered (keyed off the last-trade mark) gates
// whether/when they convert to TypeMarket/TypeLimit *before* reaching this
// function — see actOnMarkLocked.
//
// slippageBps (Task 4) applies extra adverse cost to each level BEFORE the
// cap decision and the size-weighted average, not as a flat scale of the
// final average afterward: this matters because it can make a level that
// would satisfy the raw limit cap actually violate it once its worse,
// slipped price is considered — a limit order must never execute worse than
// its limit, so the walk has to stop there rather than filling more at a
// price a naive "raw walk, then scale the average" implementation would
// wrongly allow. v1 applies it to every fill fillAgainstBook produces (not
// only a genuinely-aggressor-crossing one — e.g. a resting limit later
// crossed by an improving book pays it too); see the Task 4 report for that
// scope call.
func fillAgainstBook(o *exec.Order, book feed.Book, slippageBps float64) (filledQty, avgPrice float64) {
	levels := book.Asks
	if o.Side == exec.SideSell || o.Side == exec.SideShort {
		levels = book.Bids
	}
	capped := o.Type == exec.TypeLimit

	remaining := o.LeavesQty
	var sumPxQty, sumQty float64
	for _, lvl := range levels {
		if remaining <= 0 {
			break
		}
		px := slippedPrice(o.Side, lvl.Price, slippageBps)
		if capped && !marketable(o.Side, o.LimitPrice, px) {
			break // this level (and every level past it) violates the cap
		}
		take := remaining
		if v := float64(lvl.Volume); v < take {
			take = v
		}
		sumPxQty += take * px
		sumQty += take
		remaining -= take
	}
	if sumQty == 0 {
		return 0, 0
	}
	return sumQty, sumPxQty / sumQty
}

// slippedPrice adjusts one consumed book level's price by slippageBps of
// extra adverse cost (Task 4): buy/cover pays more, sell/short receives
// less. slippageBps <= 0 returns price unchanged, so a zero-value Options
// reproduces the pre-Task-4 fillAgainstBook exactly.
func slippedPrice(side exec.Side, price, slippageBps float64) float64 {
	if slippageBps <= 0 {
		return price
	}
	rate := slippageBps / 10_000
	switch side {
	case exec.SideBuy, exec.SideCover:
		return price * (1 + rate)
	default: // Sell, Short
		return price * (1 - rate)
	}
}

// stopTriggered reports whether a stop/stop-limit's trigger has been hit.
// Buy/Cover stops trigger at or above the stop; Sell/Short stops at or below.
func stopTriggered(side exec.Side, stop, mark float64) bool {
	switch side {
	case exec.SideBuy, exec.SideCover:
		return mark >= stop
	default: // Sell, Short
		return mark <= stop
	}
}

func (b *Broker) SubmitOrder(_ context.Context, req exec.OrderRequest) (exec.OrderAck, error) {
	if err := req.Validate(); err != nil {
		return exec.OrderAck{}, err
	}
	b.mu.Lock()
	b.bseq++
	brokerID := fmt.Sprintf("SIM-%d", b.bseq)
	o := &exec.Order{
		Venue: b.venue, ID: req.ClientOrderID, Symbol: req.Symbol, Side: req.Side,
		Type: req.Type, TIF: req.TIF, Session: req.Session, Qty: req.Qty, LimitPrice: req.LimitPrice,
		StopPrice: req.StopPrice, Status: exec.StatusAccepted, LeavesQty: req.Qty,
		CreatedMs: b.now(), UpdatedMs: b.now(),
	}
	b.orders[o.ID] = o
	// Task 5: every order (regardless of type) is stamped with its
	// submit->fill eligibility deadline before anything attempts to fill it
	// — a no-op when fillLatencyMs<=0 (see markEligibilityLocked).
	b.markEligibilityLocked(o, o.CreatedMs)
	mark, hasMark := b.marks[req.Symbol]
	var post []exec.BrokerEvent
	post = append(post, exec.OrderAccepted{V: b.venue, OID: o.ID, BrokerOrderID: brokerID, Ts: b.now()})

	switch o.Type {
	case exec.TypeMarket, exec.TypeLimit:
		// Book-priced from the moment of submission. There is no longer a
		// "market order + no mark -> reject" case: a market or
		// marketable-limit order with no book yet for its symbol simply
		// rests (fillAgainstBook against an empty/missing book returns 0),
		// exactly like a non-marketable limit always has — it fills
		// (fully or partially) the first time SetBook delivers a real book.
		// attemptBookFillLocked itself enforces the Task 5 eligibility gate
		// (o.CreatedMs == b.now() here, so fillLatencyMs>0 always blocks this
		// very first call — see its doc comment for why that's correct and
		// how TIF still ends up applying to whatever attempt DOES end up
		// being first).
		post = append(post, b.attemptBookFillLocked(o, b.books[o.Symbol])...)
	default: // TypeStop, TypeStopLimit
		// Trigger evaluation only, keyed off the mark (unchanged); whatever
		// doesn't trigger+fill stays resting until a later SetMark/SetBook
		// acts on it. The trigger comparison itself is never latency-gated
		// — only the book-fill attempt a trigger leads to is (inside
		// actOnMarkLocked -> attemptBookFillLocked).
		if hasMark {
			post = append(post, b.actOnMarkLocked(o, mark)...)
		}
	}
	b.mu.Unlock()
	for _, e := range post {
		b.emit(e)
	}
	return exec.OrderAck{OrderID: o.ID, Accepted: true, Message: brokerID}, nil
}

// fillLocked fills exactly qty of a resting order at price px: it updates
// the order's cumulative fill state, the position (weighted-average cost on
// an add/open, realized P&L on a reduce/close/flip), and the account's cash
// and mark-to-market snapshot, then returns the events to emit (OrderFilled
// + BrokerPositions + BrokerAccount) — every fill in this broker, partial or
// full, from any caller, flows through here, so this is the one place fill
// accounting lives. qty may be less than o.LeavesQty (a partial fill): the
// order is only deleted from b.orders and marked Filled once LeavesQty
// reaches 0; otherwise it is marked PartiallyFilled and stays resting so a
// later fill attempt (from another SetBook, etc.) can complete it. Caller
// holds mu.
func (b *Broker) fillLocked(o *exec.Order, qty, px float64) []exec.BrokerEvent {
	// AvgFillPrice is a running size-weighted average across every fill this
	// order has received so far, not just this one — an order can now fill
	// in multiple partial installments as the book changes.
	prevQty := o.ExecutedQty
	o.AvgFillPrice = (o.AvgFillPrice*prevQty + px*qty) / (prevQty + qty)
	o.ExecutedQty += qty
	o.LeavesQty -= qty
	o.UpdatedMs = b.now()
	if o.LeavesQty <= 0 {
		o.LeavesQty = 0
		o.Status = exec.StatusFilled
		delete(b.orders, o.ID)
		b.clearEligibilityLocked(o.ID) // Task 5: no longer resting, nothing left to gate
	} else {
		o.Status = exec.StatusPartiallyFilled
	}

	signed := qty
	if o.Side != exec.SideBuy && o.Side != exec.SideCover {
		signed = -qty
	}
	p := b.pos[o.Symbol]
	if p == nil {
		p = &exec.Position{Venue: b.venue, Symbol: o.Symbol}
		b.pos[o.Symbol] = p
	}

	prevPosQty := p.Qty
	if prevPosQty == 0 || (prevPosQty > 0) == (signed > 0) {
		// Adds to (or opens) a position: fold this fill into a size-weighted
		// average cost across the old and new shares.
		newAbs := math.Abs(prevPosQty) + qty
		p.AvgPrice = (math.Abs(prevPosQty)*p.AvgPrice + qty*px) / newAbs
		p.Qty = prevPosQty + signed
	} else {
		// Reduces, closes, or flips through flat: the closed portion
		// realizes P&L against the position's existing AvgPrice. longSign
		// flips the sign of (px - AvgPrice) so closing a long profits when
		// px > AvgPrice and closing a short profits when px < AvgPrice.
		longSign := 1.0
		if prevPosQty < 0 {
			longSign = -1.0
		}
		closedQty := math.Min(qty, math.Abs(prevPosQty))
		b.acct.Realized += (px - p.AvgPrice) * closedQty * longSign
		p.Qty = prevPosQty + signed
		if remainder := qty - closedQty; remainder > 0 {
			// Flip through flat: the excess beyond what closed the old
			// position opens a brand-new position on the other side, priced
			// at this fill -- there is nothing left of the old position to
			// average against.
			p.AvgPrice = px
		}
		// A pure reduce/close (remainder == 0, including the exact-flatten
		// case) leaves AvgPrice untouched: it only ever changes on the
		// add/open branch above, never while shrinking a position.
	}

	// Cash: buy/cover pay, sell/short receive -- same sign convention as
	// exec/roundtrip.go's cashSign (unexported there, and that package must
	// not import back into sim, so it's duplicated here rather than shared).
	b.acct.AvailableCash += cashSign(o.Side) * qty * px
	b.recomputeAccountLocked()

	fill := exec.Fill{Venue: b.venue, OrderID: o.ID, Symbol: o.Symbol, Side: o.Side, Qty: qty, Price: px, TsMs: b.now()}
	return []exec.BrokerEvent{
		exec.OrderFilled{F: fill, CumQty: o.ExecutedQty, LeavesQty: o.LeavesQty, AvgPrice: o.AvgFillPrice},
		exec.BrokerPositions{V: b.venue, Positions: b.positionsLocked()},
		exec.BrokerAccount{Account: b.acct},
	}
}

// cashSign is the fill's contribution sign to available cash: SELL/SHORT
// receive cash (+1), BUY/COVER pay cash (-1) -- the same convention as
// exec/roundtrip.go's (unexported) cashSign, duplicated here rather than
// imported since sim must not reach into exec's unexported helpers.
func cashSign(side exec.Side) float64 {
	if side == exec.SideSell || side == exec.SideShort {
		return 1
	}
	return -1
}

// attemptBookFillLocked is the SHARED "consider this order against a book"
// primitive for every caller: SubmitOrder's own first attempt (Market/Limit,
// called directly), the stop-trigger conversion branch in actOnMarkLocked,
// crossRestingOnBookLocked's SetBook sweep, and ReplaceOrder's Market/Limit
// branch. Prior to Task 5 there were two separate functions here
// (attemptInitialFillLocked applied TIF and only ran once at submission;
// this one never applied TIF and ran on every later re-evaluation) because
// IOC/FOK were guaranteed to resolve — fill, partial-then-cancel, or reject —
// at that single submit-time call, so a resting IOC/FOK order could never
// reach a later caller. Task 5 breaks that guarantee: fillLatencyMs>0 can
// defer a submit-time attempt into doing nothing at all (see the gate
// below), leaving an IOC/FOK order genuinely resting until a LATER call —
// through any of the paths above — becomes its first real evaluation. So
// this single function now owns both concerns:
//
//  1. The Task 5 eligibility gate: if o is not yet eligible (see
//     eligibleLocked), return nil immediately WITHOUT calling
//     fillAgainstBook at all. This is what "the order simply rests until a
//     later SetBook/SetMark event crosses the threshold" means in code —
//     no state changes, no partial consumption of the book, nothing to undo
//     later. Because b.now() only moves forward, once this gate opens for an
//     order it never closes again for that same order.
//  2. TIF, applied unconditionally on every eligible call: this is safe for
//     every caller. A GTC/Day order (o.TIF is neither IOC nor FOK) never hits
//     either TIF branch below, so repeated eligible calls behave exactly as
//     attemptBookFillLocked always has (partial fills accumulate across
//     multiple SetBook events). An IOC/FOK order, once it reaches an
//     eligible call, is ALWAYS removed from b.orders by the end of it (full
//     fill, partial-fill-then-cancel-remainder, or reject-with-no-fill) —
//     so it can never reach this function a second time and never gets TIF
//     misapplied to an already-resolved order. This is precisely the "TIF's
//     first attempt is the first ELIGIBLE attempt" rule the plan calls for,
//     with no new state machine: eligibility + "IOC/FOK always terminate on
//     their one evaluation" together are enough.
//
// Returns nil if the order isn't eligible yet, or is eligible but nothing in
// the book crossed it (order stays resting untouched either way). Caller
// holds mu.
func (b *Broker) attemptBookFillLocked(o *exec.Order, book feed.Book) []exec.BrokerEvent {
	if !b.eligibleLocked(o, b.now()) {
		return nil
	}
	qty, px := fillAgainstBook(o, book, b.slippageBps) // pure: does not mutate o or book state

	if o.TIF == exec.TIFFOK && qty < o.LeavesQty {
		// All-or-none: fillAgainstBook hasn't mutated anything, so rejecting
		// here is a clean no-op on the order/position state.
		delete(b.orders, o.ID)
		b.clearEligibilityLocked(o.ID)
		return []exec.BrokerEvent{exec.OrderRejected{V: b.venue, OID: o.ID, Reason: "sim: FOK could not fill completely", Ts: b.now()}}
	}

	var out []exec.BrokerEvent
	if qty > 0 {
		out = append(out, b.fillLocked(o, qty, px)...)
	}
	if o.TIF == exec.TIFIOC && o.LeavesQty > 0 {
		// IOC never rests, even if nothing crossed at all: cancel whatever
		// this one (eligible) attempt didn't fill instead of leaving it
		// working. o may already be gone from b.orders (fillLocked deleted
		// it) if that fill happened to be complete -- LeavesQty>0 guards
		// against canceling a just-fully-filled order.
		delete(b.orders, o.ID)
		b.clearEligibilityLocked(o.ID)
		out = append(out, exec.OrderCanceled{V: b.venue, OID: o.ID, Ts: b.now()})
	}
	return out
}

// actOnMarkLocked applies a new mark to one resting order: it evaluates
// Stop/StopLimit triggers (still keyed off the last-trade mark, per
// stopTriggered — unchanged) and, only for a stop that JUST triggered on
// this mark, prices the resulting fill off the CURRENT BOOK via
// attemptBookFillLocked rather than the mark itself — book-walk pricing
// replaced mark-based marketable() pricing in Task 2. A triggered Stop
// converts to TypeMarket and a triggered StopLimit converts to TypeLimit
// (unchanged conversion pattern), and only THEN reaches the book walk.
// A plain Market/Limit order — one that was never a stop — must NOT be
// re-priced here at all: SetMark/crossRestingLocked fires far more often
// than SetBook in real feeds, and re-attempting attemptBookFillLocked
// against a book that hasn't changed since a previous fill would consume
// the same displayed depth twice (bounded by LeavesQty, so not an
// overfill, but a phantom fill off stale liquidity). The book itself
// (SetBook/crossRestingOnBookLocked) is the sole crossing trigger for
// plain orders. Returns the fill events produced (empty if it stays
// resting — including the "triggered but no book yet" case: a triggered
// stop is not a special case for SubmitOrder's rest-until-book rule).
// Caller holds mu.
//
// Task 5: the stopTriggered comparison above is NEVER latency-gated — a stop
// converts from Stop/StopLimit to Market/Limit purely off the mark, on
// schedule, regardless of fillLatencyMs. Only the attemptBookFillLocked call
// below is gated: if the order's eligibility deadline (set at its ORIGINAL
// submission, not at trigger time) hasn't elapsed yet, it returns nil and
// the now-converted order simply rests until a later SetBook/SetMark call
// clears the gate — the exact same "rest until book" behavior a triggered
// stop with no book at all already has.
func (b *Broker) actOnMarkLocked(o *exec.Order, mark float64) []exec.BrokerEvent {
	switch o.Type {
	case exec.TypeStop:
		if !stopTriggered(o.Side, o.StopPrice, mark) {
			return nil
		}
		o.Type = exec.TypeMarket // triggered: becomes a plain marketable order
	case exec.TypeStopLimit:
		if !stopTriggered(o.Side, o.StopPrice, mark) {
			return nil
		}
		o.Type = exec.TypeLimit // triggered: becomes a resting limit
	default:
		// A plain (never-was-a-stop) Market/Limit order: marks never
		// re-price it, only the book does.
		return nil
	}
	return b.attemptBookFillLocked(o, b.books[o.Symbol])
}

// crossRestingLocked applies a new mark to every resting order on a symbol,
// in deterministic id order. Caller holds mu.
func (b *Broker) crossRestingLocked(symbol string, mark float64) []exec.BrokerEvent {
	var ids []string
	for id, o := range b.orders {
		if o.Symbol == symbol {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []exec.BrokerEvent
	for _, id := range ids {
		o, ok := b.orders[id]
		if !ok { // filled earlier in this pass
			continue
		}
		out = append(out, b.actOnMarkLocked(o, mark)...)
	}
	return out
}

// crossRestingOnBookLocked applies a new book snapshot to every resting
// order on that symbol, in deterministic id order — SetBook's analog of
// crossRestingLocked. It only ever attempts Market/Limit orders: a resting
// Stop/StopLimit that has not yet triggered is deliberately skipped here,
// since fillAgainstBook has no concept of a stop trigger — calling it
// directly on an untriggered bare Stop (LimitPrice == 0) would treat it as
// an uncapped marketable order and fill it immediately, ignoring its
// trigger entirely. A Stop/StopLimit that HAS already triggered has, by
// then, been converted to TypeMarket/TypeLimit by actOnMarkLocked, so it is
// swept here like any other resting order. Caller holds mu.
func (b *Broker) crossRestingOnBookLocked(symbol string, book feed.Book) []exec.BrokerEvent {
	var ids []string
	for id, o := range b.orders {
		if o.Symbol == symbol && (o.Type == exec.TypeMarket || o.Type == exec.TypeLimit) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []exec.BrokerEvent
	for _, id := range ids {
		o, ok := b.orders[id]
		if !ok { // filled earlier in this pass
			continue
		}
		out = append(out, b.attemptBookFillLocked(o, book)...)
	}
	return out
}

func (b *Broker) positionsLocked() []exec.Position {
	out := make([]exec.Position, 0, len(b.pos))
	for _, p := range b.pos {
		out = append(out, *p)
	}
	return out
}

func (b *Broker) ReplaceOrder(_ context.Context, orderID string, req exec.ReplaceRequest) error {
	b.mu.Lock()
	o, ok := b.orders[orderID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("sim: replace: order %s not working", orderID)
	}
	o.Qty = req.Qty
	if req.LimitPrice > 0 {
		o.LimitPrice = req.LimitPrice
	}
	if req.StopPrice > 0 {
		o.StopPrice = req.StopPrice
	}
	o.LeavesQty = req.Qty - o.ExecutedQty
	o.UpdatedMs = b.now()
	post := []exec.BrokerEvent{exec.OrderReplaced{V: b.venue, OID: orderID, NewQty: req.Qty, NewLimit: req.LimitPrice, NewStop: req.StopPrice, Ts: b.now()}}
	// Route the post-replace fill decision by order type, since
	// actOnMarkLocked no longer falls through for plain orders (it now only
	// ever prices a Stop/StopLimit that triggers on this call):
	//   - Stop/StopLimit: still needs the mark to evaluate its trigger (a
	//     bare TypeStop has LimitPrice == 0 -- it prices off StopPrice --
	//     so a raw marketable(...) check would fill it immediately at $0
	//     without the trigger ever being evaluated; actOnMarkLocked applies
	//     the correct trigger semantics first). Gated on a mark existing,
	//     same as before.
	//   - Market/Limit: was never a stop, so there is no trigger to
	//     evaluate -- go straight to the book via attemptBookFillLocked,
	//     the same primitive SetBook's crossing sweep uses. Deliberately
	//     NOT gated on a mark existing: a symbol with a book but no mark
	//     yet should still be able to re-cross a replaced limit order.
	//
	// Task 5: both branches route through attemptBookFillLocked (directly, or
	// via actOnMarkLocked), so both are subject to the SAME fill-latency
	// eligibility deadline set at o's ORIGINAL SubmitOrder call -- replacing
	// an order does not reset or grant a fresh latency window. If that
	// deadline hasn't elapsed yet, this replace's fill attempt is a no-op
	// (order rests with its new qty/price) exactly like any other blocked
	// attempt, and a later SetBook/SetMark can still complete it once eligible.
	switch o.Type {
	case exec.TypeStop, exec.TypeStopLimit:
		if mark, ok := b.marks[o.Symbol]; ok {
			post = append(post, b.actOnMarkLocked(o, mark)...)
		}
	default:
		post = append(post, b.attemptBookFillLocked(o, b.books[o.Symbol])...)
	}
	b.mu.Unlock()
	for _, e := range post {
		b.emit(e)
	}
	return nil
}

func (b *Broker) CancelOrder(_ context.Context, orderID string) error {
	b.mu.Lock()
	_, ok := b.orders[orderID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("sim: cancel: order %s not working", orderID)
	}
	delete(b.orders, orderID)
	b.clearEligibilityLocked(orderID)
	b.mu.Unlock()
	b.emit(exec.OrderCanceled{V: b.venue, OID: orderID, Ts: b.now()})
	return nil
}

func (b *Broker) CancelAll(_ context.Context, symbol string) error {
	b.mu.Lock()
	var ids []string
	for id, o := range b.orders {
		if symbol == "" || o.Symbol == symbol {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		delete(b.orders, id)
		b.clearEligibilityLocked(id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		b.emit(exec.OrderCanceled{V: b.venue, OID: id, Ts: b.now()})
	}
	return nil
}

// Flatten zeroes every position and emits a reconcile. (Real brokers close via
// market orders that arrive back as fills; the sim shortcuts to a flat
// reconcile — sufficient for E2E/practice.) Unlike a real close-out order,
// this never touches the book/mark-crossing machinery, but it still must
// realize the same P&L and cash effect a real closing fill would have
// produced — otherwise the very next SetMark/SetBook recomputes Equity from
// AvailableCash with zero positions and manufactures a phantom gain/loss
// that never happened, while Realized silently stays behind. So each open
// position is closed here using the exact fillLocked conventions: the
// symbol's last-trade mark if one exists, else the position's own AvgPrice
// (same "no mark yet" fallback recomputeAccountLocked already documents);
// longSign for the realized-P&L sign; cashSign for the cash sign, keyed off
// the SIDE that economically closes the position (closing a long is a sell,
// closing a short is a cover) rather than the position's raw Qty sign.
func (b *Broker) Flatten(_ context.Context) error {
	b.mu.Lock()
	for _, p := range b.pos {
		if p.Qty == 0 {
			continue
		}
		closePrice, ok := b.marks[p.Symbol]
		if !ok {
			closePrice = p.AvgPrice
		}
		longSign := 1.0
		closeSide := exec.SideSell
		if p.Qty < 0 {
			longSign = -1.0
			closeSide = exec.SideCover
		}
		qty := math.Abs(p.Qty)
		b.acct.Realized += (closePrice - p.AvgPrice) * qty * longSign
		b.acct.AvailableCash += cashSign(closeSide) * qty * closePrice
		p.Qty = 0
		p.AvgPrice = 0
	}
	b.recomputeAccountLocked()
	post := []exec.BrokerEvent{
		exec.BrokerPositions{V: b.venue, Positions: b.positionsLocked()},
		exec.BrokerAccount{Account: b.acct},
	}
	b.mu.Unlock()
	for _, e := range post {
		b.emit(e)
	}
	return nil
}

// ResetBalance cancels every resting order, flattens all positions, and
// reseeds the account snapshot to startingCash — composed from the existing
// CancelAll/Flatten/SetAccount primitives rather than duplicating their
// locking/event logic. CancelAll's OrderCanceled events are persisted (real
// cancel history); Flatten's BrokerPositions and SetAccount's BrokerAccount
// are transient reconciles, same as a manual Flatten click, so neither the
// exec-event journal nor Trade History is touched by a reset.
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

func (b *Broker) Snapshot(_ context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	orders := make([]exec.Order, 0, len(b.orders))
	for _, o := range b.orders {
		orders = append(orders, *o)
	}
	return b.acct, b.positionsLocked(), orders, nil
}
