package sim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/atomicfile"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type PerpetualConfig struct {
	Venue        exec.VenueID
	Exchange     string
	Clock        clock.Clock
	StartingUSDT float64
	Path         string
	Rules        func(string) (instrument.ContractRules, bool)
}

// PerpetualSimulator uses an independent USDT ledger and MEXC depth. Only the
// broker interface and event projection are shared with cash equities.
type PerpetualSimulator struct {
	cfg    PerpetualConfig
	mu     sync.Mutex
	data   perpetualLedger
	books  map[string]feed.Book
	fair   map[string]feed.Quote
	out    chan exec.BrokerEvent
	demand func(string, bool)
}

type perpetualLedger struct {
	Wallet     float64
	Realized   float64
	Initial    float64
	FundingPnL float64
	Sequence   uint64
	Positions  map[string]perpetualPosition
	Orders     map[string]exec.Order
}

type perpetualPosition struct {
	Position     exec.Position
	Leverage     int
	Multiplier   float64
	FeeRate      float64
	ProtectionID string
	FundingAt    int64
	FundingRate  float64
}

var _ exec.PerpetualBroker = (*PerpetualSimulator)(nil)

func NewPerpetual(cfg PerpetualConfig) (*PerpetualSimulator, error) {
	ledger := perpetualLedger{
		Wallet: cfg.StartingUSDT, Initial: cfg.StartingUSDT,
		Positions: make(map[string]perpetualPosition),
		Orders:    make(map[string]exec.Order),
	}
	if cfg.Path != "" {
		body, err := os.ReadFile(cfg.Path)
		switch {
		case err == nil:
			if err := json.Unmarshal(body, &ledger); err != nil {
				return nil, fmt.Errorf("read perpetual simulator ledger: %w", err)
			}
			if ledger.Positions == nil || ledger.Orders == nil || !finite(ledger.Wallet) {
				return nil, errors.New("invalid perpetual simulator ledger")
			}
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
	}
	return &PerpetualSimulator{cfg: cfg, data: ledger, books: make(map[string]feed.Book),
		fair: make(map[string]feed.Quote), out: make(chan exec.BrokerEvent, 4096)}, nil
}

func (b *PerpetualSimulator) Capabilities() exec.Capabilities {
	return exec.Capabilities{MarketOutsideRTH: true, AuthoritativeDayPnL: true}
}

func (b *PerpetualSimulator) Events() <-chan exec.BrokerEvent { return b.out }

func (b *PerpetualSimulator) ContractRules(symbol string) (instrument.ContractRules, bool) {
	id, ok := instrument.ParsePerpetual(symbol)
	if !ok || id.Exchange != b.cfg.Exchange {
		return instrument.ContractRules{}, false
	}
	return b.cfg.Rules(symbol)
}

func (b *PerpetualSimulator) FairPrice(symbol string) (float64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	q := b.fair[symbol]
	return q.Mark, finite(q.Mark) && q.Mark > 0 && b.fresh(q.TsMs)
}

func (b *PerpetualSimulator) fresh(ts int64) bool {
	age := b.cfg.Clock.Now().Sub(time.UnixMilli(ts))
	return ts > 0 && age >= -time.Second && age <= 5*time.Second
}

func (b *PerpetualSimulator) commit(next perpetualLedger, events []exec.BrokerEvent) error {
	if b.cfg.Path != "" {
		body, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if err = atomicfile.Write(b.cfg.Path, body, 0600); err != nil {
			return err
		}
	}
	before := demandSymbols(b.data)
	b.data = next
	if b.demand != nil {
		after := demandSymbols(next)
		for symbol := range before {
			if !after[symbol] {
				b.demand(symbol, false)
			}
		}
		for symbol := range after {
			if !before[symbol] {
				b.demand(symbol, true)
			}
		}
	}
	for _, event := range events {
		b.out <- event
	}
	if len(events) != 0 {
		b.out <- exec.BrokerPositions{V: b.cfg.Venue, Positions: positions(next)}
		b.out <- exec.BrokerAccount{Account: b.account(next)}
	}
	return nil
}

func (b *PerpetualSimulator) AttachDemand(fn func(string, bool)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.demand = fn
	for symbol := range demandSymbols(b.data) {
		fn(symbol, true)
	}
}

func demandSymbols(state perpetualLedger) map[string]bool {
	symbols := make(map[string]bool)
	for symbol := range state.Positions {
		symbols[symbol] = true
	}
	for _, order := range state.Orders {
		if order.Working() {
			symbols[order.Symbol] = true
		}
	}
	return symbols
}

func (b *PerpetualSimulator) copyLedger() perpetualLedger {
	copy := b.data
	copy.Positions = maps.Clone(copy.Positions)
	copy.Orders = maps.Clone(copy.Orders)
	return copy
}

func (b *PerpetualSimulator) SubmitOrder(_ context.Context, req exec.OrderRequest) (exec.OrderAck, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.cfg.Clock.Now().UnixMilli()
	reject := func(reason string) (exec.OrderAck, error) {
		b.out <- exec.OrderRejected{V: b.cfg.Venue, OID: req.ClientOrderID, Reason: reason, Ts: now}
		return exec.OrderAck{OrderID: req.ClientOrderID, Message: reason}, nil
	}
	if seen, ok := b.data.Orders[req.ClientOrderID]; ok {
		return exec.OrderAck{OrderID: seen.ID, Accepted: true}, nil
	}
	rules, ok := b.ContractRules(req.Symbol)
	options := req.Perpetual
	if !ok || req.Venue != b.cfg.Venue || options == nil || !rules.Valid() ||
		!rules.ValidQuantity(req.Qty) || options.Multiplier != rules.ContractSize() ||
		(req.Type != exec.TypeMarket && (req.Type != exec.TypeLimit || !rules.ValidPrice(req.LimitPrice))) {
		return reject("perpetual simulator rejected the instrument, tick or quantity")
	}
	p := b.data.Positions[req.Symbol]
	isOpen := opening(req.Side)
	quote := b.fair[req.Symbol]
	book := b.books[req.Symbol]
	if !b.fresh(book.TsMs) || len(book.Bids) == 0 || len(book.Asks) == 0 {
		return reject("waiting for a current perpetual order book")
	}
	if isOpen {
		if !rules.TradingEnabled || options.Leverage < rules.MinLeverage || options.Leverage > rules.MaxLeverage ||
			!b.fresh(quote.TsMs) || quote.Mark <= 0 {
			return reject("perpetual opening requires current leverage rules and fair price")
		}
		if p.Position.Qty != 0 && (p.Leverage != options.Leverage || p.ProtectionID != "" || (p.Position.Qty > 0) != buys(req.Side)) {
			return reject("resolve the existing position or its protection before adding")
		}
		if options.ProtectionPrice > 0 {
			if options.ProtectionID == "" || p.Position.Qty != 0 || !rules.ValidPrice(options.ProtectionPrice) ||
				buys(req.Side) && quote.Mark <= options.ProtectionPrice || !buys(req.Side) && quote.Mark >= options.ProtectionPrice {
				return reject("perpetual protection is incompatible with the position or fair price")
			}
			req.TIF = exec.TIFIOC
		}
		price := req.LimitPrice
		if req.Type == exec.TypeMarket {
			// Value against the least favorable quoted level consumed.
			levels := book.Bids
			if buys(req.Side) {
				levels = book.Asks
			}
			for _, level := range levels {
				price = max(price, level.Price)
			}
		}
		// A short limit can fill higher than its limit and consumes margin
		// at the actual execution price. Reserve a current price floor.
		price = max(price, quote.Mark, book.Asks[0].Price)
		need := req.Qty * price * options.Multiplier * (1/float64(options.Leverage) + 2*rules.FeeRate())
		if need > b.account(b.data).AvailableCash {
			return reject("insufficient USDT margin")
		}
	} else if !reduces(req.Side, p.Position.Qty) || req.Qty > math.Abs(p.Position.Qty) {
		return reject("perpetual close would exceed the confirmed position")
	}
	o := exec.Order{ID: req.ClientOrderID, Venue: b.cfg.Venue, Symbol: req.Symbol, Side: req.Side,
		Type: req.Type, TIF: req.TIF, Session: req.Session, Qty: req.Qty, LeavesQty: req.Qty, LimitPrice: req.LimitPrice,
		Perpetual: options, Status: exec.StatusAccepted, CreatedMs: now, UpdatedMs: now}
	if o.Type == exec.TypeMarket {
		o.TIF = exec.TIFIOC
	}
	next := b.copyLedger()
	next.Orders[o.ID] = o
	event := exec.OrderAccepted{V: b.cfg.Venue, OID: o.ID, BrokerOrderID: o.ID, Ts: now}
	book = copyBook(book)
	events := append([]exec.BrokerEvent{event}, b.match(&next, o.ID, &book, rules)...)
	if err := b.commit(next, events); err != nil {
		return exec.OrderAck{}, err
	}
	b.books[req.Symbol] = book
	return exec.OrderAck{OrderID: o.ID, Accepted: true}, nil
}

func (b *PerpetualSimulator) SetMark(string, float64) {}

func (b *PerpetualSimulator) SetFairQuote(q feed.Quote) {
	if id, ok := instrument.ParsePerpetual(q.Symbol); !ok || id.Exchange != b.cfg.Exchange {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	previous := b.fair[q.Symbol]
	b.fair[q.Symbol] = q
	b.settleFunding(q, previous)
	b.cross(q.Symbol)
}

func (b *PerpetualSimulator) settleFunding(q, previous feed.Quote) {
	p, ok := b.data.Positions[q.Symbol]
	if !ok || q.Mark <= 0 || !b.fresh(q.TsMs) {
		return
	}
	due := p.FundingAt != 0 && q.TsMs >= p.FundingAt
	newSchedule := q.NextFundingMs > q.TsMs && q.NextFundingMs != p.FundingAt
	if !due && !newSchedule {
		return
	}
	next := b.copyLedger()
	if due {
		rate := p.FundingRate
		if previous.NextFundingMs == p.FundingAt && previous.TsMs < p.FundingAt {
			rate = previous.FundingRate
		}
		cashFlow := -p.Position.Qty * p.Multiplier * q.Mark * rate
		next.Wallet += cashFlow
		next.FundingPnL += cashFlow
		p.FundingAt = 0
	}
	if newSchedule {
		p.FundingAt, p.FundingRate = q.NextFundingMs, q.FundingRate
	}
	next.Positions[q.Symbol] = p
	if err := b.commit(next, nil); err == nil {
		b.out <- exec.BrokerAccount{Account: b.account(next)}
	}
}

func (b *PerpetualSimulator) SetBook(symbol string, book feed.Book) {
	if id, ok := instrument.ParsePerpetual(symbol); !ok || id.Exchange != b.cfg.Exchange {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	prior := b.books[symbol]
	if book.TsMs == prior.TsMs && len(book.Bids) != 0 && len(book.Asks) != 0 {
		return
	}
	b.books[symbol] = copyBook(book)
	b.cross(symbol)
}

func (b *PerpetualSimulator) cross(symbol string) {
	book := b.books[symbol]
	if !b.fresh(book.TsMs) {
		return
	}
	rules, ok := b.ContractRules(symbol)
	p := b.data.Positions[symbol]
	if !ok && p.Position.Qty == 0 {
		return
	}
	if !ok {
		rules.TakerFee = fmt.Sprint(p.FeeRate)
	}
	q := b.fair[symbol]
	ids := make([]string, 0)
	for id, order := range b.data.Orders {
		if order.Symbol == symbol && order.Working() {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	sort.Strings(ids)
	next := b.copyLedger()
	book = copyBook(book)
	var events []exec.BrokerEvent
	for _, id := range ids {
		o := next.Orders[id]
		if !o.Working() || opening(o.Side) && (!ok || !b.fresh(q.TsMs) || q.Mark <= 0) {
			continue
		}
		if o.Type == exec.TypeStop {
			if !b.fresh(q.TsMs) || q.Mark <= 0 || !stopTriggered(o.Side, o.StopPrice, q.Mark) {
				continue
			}
			o.Type = exec.TypeMarket
			next.Orders[id] = o
			events = append(events, exec.OrderSubmitted{Order: o})
		}
		events = append(events, b.match(&next, id, &book, rules)...)
	}
	if len(events) != 0 {
		if err := b.commit(next, events); err == nil {
			b.books[symbol] = book
		}
	}
}

func (b *PerpetualSimulator) match(next *perpetualLedger, id string, book *feed.Book, rules instrument.ContractRules) []exec.BrokerEvent {
	o := next.Orders[id]
	p := next.Positions[o.Symbol]
	if opening(o.Side) && (!rules.Valid() || !rules.TradingEnabled || (p.Position.Qty != 0 && (p.Position.Qty > 0) != buys(o.Side))) {
		return b.cancel(next, o)
	}
	attempt := o
	if !opening(o.Side) {
		if !reduces(o.Side, p.Position.Qty) {
			return b.cancel(next, o)
		}
		attempt.LeavesQty = min(attempt.LeavesQty, math.Abs(p.Position.Qty))
	}
	qty, price := fillAgainstBook(&attempt, *book, 0)
	if o.TIF == exec.TIFFOK && qty < o.LeavesQty {
		return b.cancel(next, o)
	}
	var events []exec.BrokerEvent
	if opening(o.Side) && qty > 0 {
		notional := qty * price * o.Perpetual.Multiplier
		required := notional * (1/float64(o.Perpetual.Leverage) + rules.FeeRate())
		// Exclude this order's own reservation before testing the actual fill.
		available := b.account(*next).AvailableCash + b.reserved(o)
		if required > available {
			return b.cancel(next, o)
		}
	}
	if qty > 0 {
		consumeBook(book, o.Side, qty)
		events = b.fill(next, o, qty, price, rules.FeeRate())
		o = next.Orders[id]
	}
	if o.TIF == exec.TIFIOC && o.Working() {
		events = append(events, b.cancel(next, o)...)
	}
	return events
}

func (b *PerpetualSimulator) fill(next *perpetualLedger, o exec.Order, qty, price, feeRate float64) []exec.BrokerEvent {
	now := b.cfg.Clock.Now().UnixMilli()
	p := next.Positions[o.Symbol]
	if opening(o.Side) && p.Position.Qty == 0 {
		q := b.fair[o.Symbol]
		p = perpetualPosition{Position: exec.Position{Venue: b.cfg.Venue, Symbol: o.Symbol, OpenedMs: now},
			Multiplier: o.Perpetual.Multiplier, Leverage: o.Perpetual.Leverage, FeeRate: feeRate, ProtectionID: o.Perpetual.ProtectionID}
		if q.NextFundingMs > now {
			p.FundingAt, p.FundingRate = q.NextFundingMs, q.FundingRate
		}
	}
	delta := qty
	if !buys(o.Side) {
		delta = -qty
	}
	net := -qty * p.Multiplier * price * feeRate
	if opening(o.Side) {
		prior := math.Abs(p.Position.Qty)
		p.Position.AvgPrice = (p.Position.AvgPrice*prior + price*qty) / (prior + qty)
	} else {
		net += math.Copysign(qty, p.Position.Qty) * p.Multiplier * (price - p.Position.AvgPrice)
	}
	p.Position.Qty += delta
	next.Positions[o.Symbol] = p
	next.Wallet += net
	next.Realized += net
	next.Sequence++
	o.AvgFillPrice = (o.AvgFillPrice*o.ExecutedQty + price*qty) / (o.ExecutedQty + qty)
	o.ExecutedQty += qty
	o.LeavesQty -= qty
	o.UpdatedMs = now
	o.Status = exec.StatusPartiallyFilled
	if o.LeavesQty == 0 {
		o.Status = exec.StatusFilled
	}
	next.Orders[o.ID] = o
	f := exec.Fill{Venue: b.cfg.Venue, OrderID: o.ID, Symbol: o.Symbol, Side: o.Side, Qty: qty, Price: price, TsMs: now, Multiplier: p.Multiplier}
	events := []exec.BrokerEvent{exec.OrderFilled{F: f, CumQty: o.ExecutedQty, LeavesQty: o.LeavesQty,
		AvgPrice: o.AvgFillPrice, PositionExecID: fmt.Sprintf("perpetual-paper-%d", next.Sequence)}}
	if opening(o.Side) && p.ProtectionID != "" {
		stop := exec.Order{Venue: b.cfg.Venue, ID: p.ProtectionID, Symbol: o.Symbol, Side: exec.SideSell, Type: exec.TypeStop,
			TIF: exec.TIFGTC, Qty: math.Abs(p.Position.Qty), LeavesQty: math.Abs(p.Position.Qty), StopPrice: o.Perpetual.ProtectionPrice,
			Status: exec.StatusAccepted, CreatedMs: now, UpdatedMs: now}
		if p.Position.Qty < 0 {
			stop.Side = exec.SideCover
		}
		next.Orders[stop.ID] = stop
		events = append(events, exec.OrderSubmitted{Order: stop})
	} else if p.Position.Qty == 0 {
		delete(next.Positions, o.Symbol)
		for _, remaining := range next.Orders {
			if remaining.Symbol == o.Symbol && remaining.Working() && !opening(remaining.Side) {
				events = append(events, b.cancel(next, remaining)...)
			}
		}
	} else if stop := next.Orders[p.ProtectionID]; stop.Working() && stop.ID != o.ID {
		stop.LeavesQty = math.Abs(p.Position.Qty)
		stop.Qty = stop.ExecutedQty + stop.LeavesQty
		stop.UpdatedMs = now
		next.Orders[stop.ID] = stop
		events = append(events, exec.OrderSubmitted{Order: stop})
	}
	return events
}

func (b *PerpetualSimulator) cancel(next *perpetualLedger, o exec.Order) []exec.BrokerEvent {
	o.Status, o.UpdatedMs = exec.StatusCanceled, b.cfg.Clock.Now().UnixMilli()
	next.Orders[o.ID] = o
	return []exec.BrokerEvent{exec.OrderCanceled{V: o.Venue, OID: o.ID, Ts: o.UpdatedMs}}
}

func (b *PerpetualSimulator) reserved(o exec.Order) float64 {
	if !o.Working() || !opening(o.Side) || o.Perpetual == nil {
		return 0
	}
	return o.LeavesQty * max(o.LimitPrice, b.fair[o.Symbol].Mark) * o.Perpetual.Multiplier / float64(o.Perpetual.Leverage)
}

func (b *PerpetualSimulator) account(state perpetualLedger) exec.AccountSnapshot {
	margin, unrealized := 0.0, 0.0
	for symbol, pos := range state.Positions {
		p := pos.Position
		mark := b.fair[symbol].Mark
		if mark <= 0 {
			mark = p.AvgPrice
		}
		margin += math.Abs(p.Qty) * pos.Multiplier * p.AvgPrice / float64(pos.Leverage)
		unrealized += p.Qty * pos.Multiplier * (mark - p.AvgPrice)
	}
	for _, order := range state.Orders {
		margin += b.reserved(order)
	}
	equity := state.Wallet + unrealized
	available := max(0, min(state.Wallet, equity)-margin)
	return exec.AccountSnapshot{Venue: b.cfg.Venue, Equity: equity, AvailableCash: available, BuyingPower: available,
		SodEquity: state.Initial, Realized: state.Realized, DayPnL: equity - state.Initial, DayPnLSource: "paper",
		TsMs: b.cfg.Clock.Now().UnixMilli(), Currency: "USDT", MarginUsed: margin, FundingPnL: state.FundingPnL}
}

func (b *PerpetualSimulator) Snapshot(context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var orders []exec.Order
	for _, order := range b.data.Orders {
		if order.Working() {
			orders = append(orders, order)
		}
	}
	return b.account(b.data), positions(b.data), orders, nil
}

func (b *PerpetualSimulator) CancelOrder(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	o := b.data.Orders[id]
	if !o.Working() {
		return errors.New("no working perpetual order")
	}
	next := b.copyLedger()
	return b.commit(next, b.cancel(&next, o))
}

func (b *PerpetualSimulator) CancelAll(_ context.Context, symbol string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	next := b.copyLedger()
	var events []exec.BrokerEvent
	for _, order := range next.Orders {
		if order.Working() && (symbol == "" || order.Symbol == symbol) {
			events = append(events, b.cancel(&next, order)...)
		}
	}
	return b.commit(next, events)
}

func (b *PerpetualSimulator) ReplaceOrder(context.Context, string, exec.ReplaceRequest) error {
	return errors.New("perpetual paper orders require cancel and resubmit")
}

func (b *PerpetualSimulator) Flatten(context.Context) error {
	return errors.New("close each perpetual position explicitly")
}

func (b *PerpetualSimulator) ResetBalance(context.Context, float64) error {
	return errors.New("perpetual collateral cannot be reset")
}

func positions(state perpetualLedger) []exec.Position {
	out := make([]exec.Position, 0, len(state.Positions))
	for _, pos := range state.Positions {
		p := pos.Position
		p.Leverage, p.Multiplier = pos.Leverage, pos.Multiplier
		p.MarginUsed = math.Abs(p.Qty) * p.AvgPrice * pos.Multiplier / float64(pos.Leverage)
		if stop := state.Orders[pos.ProtectionID]; stop.Working() && stop.Type == exec.TypeStop && stop.LeavesQty >= math.Abs(p.Qty) {
			p.ProtectionPrice = stop.StopPrice
		}
		out = append(out, p)
	}
	return out
}

func opening(s exec.Side) bool { return s == exec.SideBuy || s == exec.SideShort }
func buys(s exec.Side) bool    { return s == exec.SideBuy || s == exec.SideCover }
func reduces(s exec.Side, qty float64) bool {
	return s == exec.SideSell && qty > 0 || s == exec.SideCover && qty < 0
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func copyBook(book feed.Book) feed.Book {
	book.Bids = append([]feed.BookLevel(nil), book.Bids...)
	book.Asks = append([]feed.BookLevel(nil), book.Asks...)
	return book
}

func consumeBook(book *feed.Book, side exec.Side, qty float64) {
	levels := book.Bids
	if buys(side) {
		levels = book.Asks
	}
	remaining := int64(qty)
	for i := range levels {
		used := min(levels[i].Volume, remaining)
		levels[i].Volume -= used
		remaining -= used
		if remaining == 0 {
			break
		}
	}
}
