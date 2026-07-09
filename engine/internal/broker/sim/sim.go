// Package sim is a deterministic in-memory exec.Broker used for tests, replay,
// and (v1.5) practice mode. It fills market and marketable-limit orders
// immediately and rests non-marketable limits until canceled, replaced, or
// crossed by a later SetMark. It imports exec, never the reverse.
package sim

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

// Broker is a single-venue simulated broker.
type Broker struct {
	venue exec.VenueID
	clk   clock.Clock
	ev    chan exec.BrokerEvent

	mu     sync.Mutex
	marks  map[string]float64
	orders map[string]*exec.Order // resting (working) orders
	pos    map[string]*exec.Position
	acct   exec.AccountSnapshot
	bseq   int64 // broker order-id counter
}

var _ exec.Broker = (*Broker)(nil)

// New builds a SimBroker for a venue.
func New(venue exec.VenueID, clk clock.Clock) *Broker {
	return &Broker{
		venue:  venue,
		clk:    clk,
		ev:     make(chan exec.BrokerEvent, 256),
		marks:  map[string]float64{},
		orders: map[string]*exec.Order{},
		pos:    map[string]*exec.Position{},
		acct:   exec.AccountSnapshot{Venue: venue},
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

func marketable(side exec.Side, limit, mark float64) bool {
	switch side {
	case exec.SideBuy, exec.SideCover:
		return limit >= mark
	default: // Sell, Short
		return limit <= mark
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
		Type: req.Type, TIF: req.TIF, Qty: req.Qty, LimitPrice: req.LimitPrice,
		StopPrice: req.StopPrice, Status: exec.StatusAccepted, LeavesQty: req.Qty,
		CreatedMs: b.now(), UpdatedMs: b.now(),
	}
	b.orders[o.ID] = o
	mark, hasMark := b.marks[req.Symbol]
	var post []exec.BrokerEvent
	post = append(post, exec.OrderAccepted{V: b.venue, OID: o.ID, BrokerOrderID: brokerID, Ts: b.now()})
	// Market orders need a mark; without one they are rejected (mirrors gate).
	if req.Type == exec.TypeMarket && !hasMark {
		delete(b.orders, o.ID)
		post = append(post, exec.OrderRejected{V: b.venue, OID: o.ID, Reason: "sim: no mark for market order", Ts: b.now()})
		b.mu.Unlock()
		for _, e := range post {
			b.emit(e)
		}
		return exec.OrderAck{OrderID: o.ID, Accepted: true, Message: brokerID}, nil
	}
	// Market orders fill at the mark immediately.
	if req.Type == exec.TypeMarket {
		post = append(post, b.fillLocked(o, mark)...)
		b.mu.Unlock()
		for _, e := range post {
			b.emit(e)
		}
		return exec.OrderAck{OrderID: o.ID, Accepted: true, Message: brokerID}, nil
	}
	// Limit / Stop / StopLimit: apply the current mark if we have one; whatever
	// does not fill stays resting until a later SetMark acts on it.
	if hasMark {
		post = append(post, b.actOnMarkLocked(o, mark)...)
	}
	b.mu.Unlock()
	for _, e := range post {
		b.emit(e)
	}
	return exec.OrderAck{OrderID: o.ID, Accepted: true, Message: brokerID}, nil
}

// fillLocked fully fills a resting order at price px, updates position + account,
// and returns the events to emit (OrderFilled + BrokerPositions). Caller holds mu.
func (b *Broker) fillLocked(o *exec.Order, px float64) []exec.BrokerEvent {
	qty := o.LeavesQty
	o.ExecutedQty = o.Qty
	o.LeavesQty = 0
	o.AvgFillPrice = px
	o.Status = exec.StatusFilled
	o.UpdatedMs = b.now()
	delete(b.orders, o.ID)

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
	p.AvgPrice = px // simplistic: last fill price (v1.5 does weighted avg)

	fill := exec.Fill{Venue: b.venue, OrderID: o.ID, Symbol: o.Symbol, Side: o.Side, Qty: qty, Price: px, TsMs: b.now()}
	return []exec.BrokerEvent{
		exec.OrderFilled{F: fill, CumQty: o.ExecutedQty, LeavesQty: 0, AvgPrice: px},
		exec.BrokerPositions{V: b.venue, Positions: b.positionsLocked()},
	}
}

// actOnMarkLocked applies a new mark to one resting order and returns the fill
// events it produces (empty if it stays resting). Caller holds mu.
func (b *Broker) actOnMarkLocked(o *exec.Order, mark float64) []exec.BrokerEvent {
	switch o.Type {
	case exec.TypeStop:
		if stopTriggered(o.Side, o.StopPrice, mark) {
			return b.fillLocked(o, mark) // stop-market fills at the mark
		}
	case exec.TypeStopLimit:
		if stopTriggered(o.Side, o.StopPrice, mark) {
			o.Type = exec.TypeLimit // triggered: becomes a resting limit
			if marketable(o.Side, o.LimitPrice, mark) {
				return b.fillLocked(o, o.LimitPrice)
			}
		}
	default: // TypeLimit, TypeMarket(resting shouldn't happen)
		if marketable(o.Side, o.LimitPrice, mark) {
			return b.fillLocked(o, o.LimitPrice)
		}
	}
	return nil
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
