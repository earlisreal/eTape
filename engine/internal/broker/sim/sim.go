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
// is no "reject for lack of a mark/book" case left in this broker. It
// imports exec, never the reverse.
package sim

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

// Broker is a single-venue simulated broker.
type Broker struct {
	venue exec.VenueID
	clk   clock.Clock
	ev    chan exec.BrokerEvent

	mu     sync.Mutex
	marks  map[string]float64
	books  map[string]feed.Book   // latest L2 snapshot per symbol; fillAgainstBook prices fills off it
	orders map[string]*exec.Order // resting (working) orders
	pos    map[string]*exec.Position
	acct   exec.AccountSnapshot
	bseq   int64 // broker order-id counter
}

var _ exec.Broker = (*Broker)(nil)

// New builds a SimBroker for a venue, funded with startingCash — the same
// seeding ResetBalance performs, so a fresh boot and a manual reset leave the
// account in an identical state instead of boot defaulting to all-zero.
func New(venue exec.VenueID, clk clock.Clock, startingCash float64) *Broker {
	return &Broker{
		venue:  venue,
		clk:    clk,
		ev:     make(chan exec.BrokerEvent, 256),
		marks:  map[string]float64{},
		books:  map[string]feed.Book{},
		orders: map[string]*exec.Order{},
		pos:    map[string]*exec.Position{},
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
	b.mu.Unlock()
	for _, ev := range crossed {
		b.emit(ev)
	}
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
func fillAgainstBook(o *exec.Order, book feed.Book) (filledQty, avgPrice float64) {
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
		if capped && !marketable(o.Side, o.LimitPrice, lvl.Price) {
			break // this level (and every level past it) violates the cap
		}
		take := remaining
		if v := float64(lvl.Volume); v < take {
			take = v
		}
		sumPxQty += take * lvl.Price
		sumQty += take
		remaining -= take
	}
	if sumQty == 0 {
		return 0, 0
	}
	return sumQty, sumPxQty / sumQty
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
		post = append(post, b.attemptInitialFillLocked(o)...)
	default: // TypeStop, TypeStopLimit
		// Trigger evaluation only, keyed off the mark (unchanged); whatever
		// doesn't trigger+fill stays resting until a later SetMark/SetBook
		// acts on it. TIF IOC/FOK deliberately do not apply on this branch
		// — see attemptInitialFillLocked's doc comment for why.
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

// attemptInitialFillLocked handles a freshly-submitted Market or Limit
// order's first — and, for IOC/FOK, only — fill attempt against the current
// book. It is deliberately distinct from attemptBookFillLocked: TIF governs
// only what happens to whatever is left over after this ONE attempt, and
// only here at submission time. SetBook's later crossing pass
// (crossRestingOnBookLocked) never re-applies TIF, so a resting IOC/FOK
// order should never exist after this function returns — it either fully
// filled, partially-filled-then-had-the-rest-canceled (IOC), or was
// rejected outright with no fill at all (FOK, which must never partially
// fill). Stop/StopLimit orders don't route through here at submission
// (SubmitOrder sends them to actOnMarkLocked instead): their "first attempt"
// is a trigger check that may not even fire yet, and applying IOC/FOK to an
// untriggered stop would trivially cancel/reject every stop+IOC/FOK
// combination on submission, which cannot be the intent — that combo is out
// of this task's scope. Caller holds mu.
func (b *Broker) attemptInitialFillLocked(o *exec.Order) []exec.BrokerEvent {
	book := b.books[o.Symbol]
	qty, px := fillAgainstBook(o, book) // pure: does not mutate o or book state

	if o.TIF == exec.TIFFOK && qty < o.LeavesQty {
		// All-or-none: fillAgainstBook hasn't mutated anything, so rejecting
		// here is a clean no-op on the order/position state.
		delete(b.orders, o.ID)
		return []exec.BrokerEvent{exec.OrderRejected{V: b.venue, OID: o.ID, Reason: "sim: FOK could not fill completely", Ts: b.now()}}
	}

	var out []exec.BrokerEvent
	if qty > 0 {
		out = append(out, b.fillLocked(o, qty, px)...)
	}
	if o.TIF == exec.TIFIOC && o.LeavesQty > 0 {
		// IOC never rests, even if nothing crossed at all: cancel whatever
		// this one attempt didn't fill instead of leaving it working.
		delete(b.orders, o.ID)
		out = append(out, exec.OrderCanceled{V: b.venue, OID: o.ID, Ts: b.now()})
	}
	return out
}

// fillLocked fills exactly qty of a resting order at price px, updates the
// order's cumulative fill state and position, and returns the events to
// emit (OrderFilled + BrokerPositions). qty may be less than o.LeavesQty (a
// partial fill): the order is only deleted from b.orders and marked Filled
// once LeavesQty reaches 0; otherwise it is marked PartiallyFilled and stays
// resting so a later fill attempt (from another SetBook, etc.) can complete
// it. Caller holds mu.
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
	p.Qty += signed
	p.AvgPrice = px // simplistic: last fill price (Task 3 replaces with weighted avg + cash/equity impact)

	fill := exec.Fill{Venue: b.venue, OrderID: o.ID, Symbol: o.Symbol, Side: o.Side, Qty: qty, Price: px, TsMs: b.now()}
	return []exec.BrokerEvent{
		exec.OrderFilled{F: fill, CumQty: o.ExecutedQty, LeavesQty: o.LeavesQty, AvgPrice: o.AvgFillPrice},
		exec.BrokerPositions{V: b.venue, Positions: b.positionsLocked()},
	}
}

// attemptBookFillLocked prices a resting order against book via
// fillAgainstBook and, if anything crossed, applies the fill through
// fillLocked. It is the shared "fill against the current book, no TIF"
// primitive used by actOnMarkLocked (once a Stop/StopLimit triggers and
// converts) and crossRestingOnBookLocked (SetBook's sweep) — unlike
// attemptInitialFillLocked, it never cancels/rejects a leftover: IOC/FOK
// only ever apply to the initial submit-time attempt. Returns nil if
// nothing crossed (order stays resting untouched). Caller holds mu.
func (b *Broker) attemptBookFillLocked(o *exec.Order, book feed.Book) []exec.BrokerEvent {
	qty, px := fillAgainstBook(o, book)
	if qty <= 0 {
		return nil
	}
	return b.fillLocked(o, qty, px)
}

// actOnMarkLocked applies a new mark to one resting order: it evaluates
// Stop/StopLimit triggers (still keyed off the last-trade mark, per
// stopTriggered — unchanged) and, for anything now marketable, prices the
// fill off the CURRENT BOOK via attemptBookFillLocked rather than the mark
// itself — book-walk pricing replaced mark-based marketable() pricing in
// Task 2. A triggered Stop converts to TypeMarket and a triggered StopLimit
// converts to TypeLimit (unchanged conversion pattern) so both, along with
// an already-plain Limit/Market order, fall through to the same uncapped/
// capped book walk. Returns the fill events produced (empty if it stays
// resting — including the "triggered but no book yet" case: a triggered
// stop is not a special case for SubmitOrder's rest-until-book rule).
// Caller holds mu.
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
	// Route the post-replace fill decision through actOnMarkLocked (the same
	// function crossRestingLocked/SubmitOrder use) rather than a raw
	// marketable(...) check: a bare TypeStop has LimitPrice == 0 (it prices
	// off StopPrice), so marketable(Sell/Short, 0, mark) is trivially true
	// for any positive mark and would fill the stop immediately at $0 without
	// its trigger ever being evaluated; a TypeStopLimit whose LimitPrice
	// happens to already be marketable would likewise fill without its stop
	// having triggered. actOnMarkLocked applies the correct Stop/StopLimit
	// trigger semantics before ever considering marketability.
	if mark, ok := b.marks[o.Symbol]; ok {
		post = append(post, b.actOnMarkLocked(o, mark)...)
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
	}
	b.mu.Unlock()
	for _, id := range ids {
		b.emit(exec.OrderCanceled{V: b.venue, OID: id, Ts: b.now()})
	}
	return nil
}

// Flatten zeroes every position and emits a reconcile. (Real brokers close via
// market orders that arrive back as fills; the sim shortcuts to a flat
// reconcile — sufficient for E2E/practice.)
func (b *Broker) Flatten(_ context.Context) error {
	b.mu.Lock()
	for _, p := range b.pos {
		p.Qty = 0
		p.AvgPrice = 0
	}
	post := []exec.BrokerEvent{exec.BrokerPositions{V: b.venue, Positions: b.positionsLocked()}}
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
