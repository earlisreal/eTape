// Package sim is a deterministic in-memory exec.Broker used for tests, replay,
// and (v1.5) practice mode. It fills market and marketable-limit orders
// immediately and rests non-marketable limits until canceled, replaced, or
// crossed by a later SetMark. It imports exec, never the reverse.
package sim

import (
	"context"
	"fmt"
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
	return exec.Capabilities{NativeReplace: true, FlattenAll: true, OvernightSession: false}
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
	fillPx := req.LimitPrice
	// A limit order on a symbol with no mark yet cannot be judged marketable
	// (the zero-value mark would make almost any positive limit look
	// marketable) — it rests until a SetMark seeds a real price.
	doFill := req.Type == exec.TypeMarket || (hasMark && marketable(req.Side, req.LimitPrice, mark))
	if req.Type == exec.TypeMarket {
		fillPx = mark
	}
	if doFill {
		post = append(post, b.fillLocked(o, fillPx)...)
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
	if !(o.Side == exec.SideBuy || o.Side == exec.SideCover) {
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

// crossRestingLocked fills any resting orders on a symbol that the new mark makes
// marketable. Caller holds mu.
func (b *Broker) crossRestingLocked(symbol string, mark float64) []exec.BrokerEvent {
	var out []exec.BrokerEvent
	for _, o := range b.orders {
		if o.Symbol == symbol && marketable(o.Side, o.LimitPrice, mark) {
			out = append(out, b.fillLocked(o, o.LimitPrice)...)
		}
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
	// A replace into marketability fills immediately.
	if mark, ok := b.marks[o.Symbol]; ok && marketable(o.Side, o.LimitPrice, mark) {
		post = append(post, b.fillLocked(o, o.LimitPrice)...)
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
	for _, id := range ids {
		delete(b.orders, id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		b.emit(exec.OrderCanceled{V: b.venue, OID: id, Ts: b.now()})
	}
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
