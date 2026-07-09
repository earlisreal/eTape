package exec

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// Command is a UI→engine execution command. Sealed union.
type Command interface{ isCommand() }

type SubmitOrder struct {
	Venue      VenueID
	Symbol     string
	Side       Side
	Type       OrderType
	TIF        TIF
	Qty        float64
	LimitPrice float64
	StopPrice  float64
}
type CancelOrder struct {
	Venue   VenueID
	OrderID string
}
type ReplaceOrder struct {
	Venue      VenueID
	OrderID    string
	Qty        float64
	LimitPrice float64
	StopPrice  float64
}
type Flatten struct{ Venue VenueID }
type KillSwitch struct{ Venue VenueID }
type Arm struct{ Venue VenueID }
type Disarm struct{ Venue VenueID }

func (SubmitOrder) isCommand()  {}
func (CancelOrder) isCommand()  {}
func (ReplaceOrder) isCommand() {}
func (Flatten) isCommand()      {}
func (KillSwitch) isCommand()   {}
func (Arm) isCommand()          {}
func (Disarm) isCommand()       {}

// CmdAck is the synchronous accepted|blocked ack; order outcomes arrive later as
// Updates.
type CmdAck struct {
	Accepted bool
	Reason   string
	OrderID  string
}

type cmdReq struct {
	cmd   Command
	reply chan CmdAck
}

// markState is the Core's latest-mark map; implements MarkSource.
type markState map[string]float64

func (m markState) LastTrade(sym string) (float64, bool) { v, ok := m[sym]; return v, ok }

// Core is the single-writer execution coordinator.
type Core struct {
	venues  []VenueID
	gate    GateConfig
	store   EventStore
	brokers map[VenueID]Broker
	clk     clock.Clock
	idgen   *OrderIDGen
	syslog  func(kind, detail string)

	cmds    chan cmdReq
	bevents chan BrokerEvent
	markCh  chan Mark
	updates chan Update
	dropped atomic.Uint64

	state *State
	marks markState

	trades *RoundTripAggregator
}

// CoreConfig configures NewCore.
type CoreConfig struct {
	Venues  []VenueID
	Gate    GateConfig
	Store   EventStore
	Brokers map[VenueID]Broker
	Clock   clock.Clock
	IDGen   *OrderIDGen
	SysLog  func(kind, detail string) // optional; store.AppendSysEvent in prod
	AutoArm map[VenueID]bool          // venues that boot armed (paper); nil/false => disarmed
}

func NewCore(cfg CoreConfig) *Core {
	sl := cfg.SysLog
	if sl == nil {
		sl = func(string, string) {}
	}
	c := &Core{
		venues:  cfg.Venues,
		gate:    cfg.Gate,
		store:   cfg.Store,
		brokers: cfg.Brokers,
		clk:     cfg.Clock,
		idgen:   cfg.IDGen,
		syslog:  sl,
		cmds:    make(chan cmdReq),
		bevents: make(chan BrokerEvent, 1024),
		markCh:  make(chan Mark, 256),
		updates: make(chan Update, 4096),
		state:   NewState(cfg.Venues),
		marks:   markState{},
		trades:  NewRoundTripAggregator(),
	}
	// Auto-arm is the ONLY boot path that starts armed; Recover never touches
	// arm state, so a restart with no auto-arm venue is still fully disarmed.
	applyBootArm(c.state, cfg.Venues, cfg.AutoArm)
	return c
}

// applyBootArm seeds boot arm state: venues flagged auto_arm start armed, and
// master arms iff at least one venue auto-arms. Live venues (no auto_arm) stay
// disarmed and keep the deliberate arm click.
func applyBootArm(s *State, venues []VenueID, autoArm map[VenueID]bool) {
	for _, v := range venues {
		if autoArm[v] {
			s.SetVenueArmed(v, true)
			s.SetMasterArmed(true)
		}
	}
}

func (c *Core) Updates() <-chan Update { return c.updates }

func (c *Core) DroppedUpdates() uint64 { return c.dropped.Load() }

// Do submits a command and blocks for its accepted|blocked ack. Safe from any
// goroutine.
func (c *Core) Do(cmd Command) CmdAck {
	reply := make(chan CmdAck, 1)
	c.cmds <- cmdReq{cmd: cmd, reply: reply}
	return <-reply
}

// FeedMark delivers a last-trade mark; keep-latest, drop-on-full (never blocks
// the caller — mirrors md.Core's mark path).
func (c *Core) FeedMark(m Mark) {
	select {
	case c.markCh <- m:
	default:
	}
}

// emit sends an update; drop-and-count on overflow (uihub owns coalescing).
func (c *Core) emit(u Update) {
	select {
	case c.updates <- u:
	default:
		c.dropped.Add(1)
	}
}

func (c *Core) now() int64 { return c.clk.Now().UnixMilli() }

// Recover rebuilds state at boot: replay today's persisted events, then seed
// account/positions/open-orders from each venue's broker snapshot. Call before
// Run.
func (c *Core) Recover(ctx context.Context) error {
	fromMs := session.DayMs(c.now())
	envs, err := c.store.ReadExecEventsSince(fromMs)
	if err != nil {
		return err
	}
	for _, env := range envs {
		ev, err := DecodeEvent(env.Kind, env.Payload)
		if err != nil {
			return err
		}
		c.state.Apply(ev)
	}
	for _, v := range c.venues {
		b, ok := c.brokers[v]
		if !ok {
			continue
		}
		acct, pos, orders, err := b.Snapshot(ctx)
		if err != nil {
			c.syslog("exec.recover", "snapshot "+string(v)+": "+err.Error())
			continue
		}
		c.state.ReconcileAccount(acct)
		c.state.ReconcilePositions(v, pos)
		c.state.ReconcileOpenOrders(v, orders)
	}
	return nil
}

// Run is the single writer. It pumps every venue's broker events into the inbox
// and processes commands, broker events, and marks one at a time until ctx ends.
//
// Deliberately no panic recovery here: this plan's architecture treats process
// crash+restart as the safe recovery path — Recover reconstructs State
// deterministically from the durable event log plus each venue's broker
// snapshot on every boot. Catching a panic and continuing would risk running
// on in-memory state left inconsistent by whatever the panic interrupted
// mid-mutation, which is a worse failure mode than a visible crash.
func (c *Core) Run(ctx context.Context) error {
	for v, b := range c.brokers {
		go c.pump(ctx, v, b)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case req := <-c.cmds:
			req.reply <- c.handleCmd(ctx, req.cmd)
		case be := <-c.bevents:
			c.handleBrokerEvent(ctx, be)
		case m := <-c.markCh:
			c.marks[m.Symbol] = m.Price
		}
	}
}

// pump forwards one venue's broker events into the shared inbox.
func (c *Core) pump(ctx context.Context, _ VenueID, b Broker) {
	ch := b.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case be, ok := <-ch:
			if !ok {
				return
			}
			select {
			case c.bevents <- be:
			case <-ctx.Done():
				return
			}
		}
	}
}

// appendAndFold persists an event synchronously (append failure is returned so
// the submit path can block), then folds it and emits the matching Update.
func (c *Core) appendAndFold(ev Event, src Source) error {
	env := EnvelopeOf(ev, src, 0)
	fill, _ := FillRowOf(ev)
	if _, err := c.store.AppendExecEvent(env, fill); err != nil {
		return err
	}
	c.state.Apply(ev)
	c.emitForEvent(ev)
	return nil
}

// emitForEvent pushes the Update(s) an event implies.
func (c *Core) emitForEvent(ev Event) {
	if f, ok := ev.(OrderFilled); ok {
		c.emit(FillUpdate{Fill: f.F})
		for _, t := range c.trades.Apply(f.F.Venue, f.F.Symbol, f.F.Side, f.F.Qty, f.F.Price, f.F.TsMs) {
			c.emit(TradeUpdate{Trade: t})
		}
	}
	if v, ok := c.state.OrderVenue(ev.OrderID()); ok {
		if o, ok := c.state.Venue(v).Orders[ev.OrderID()]; ok {
			c.emit(OrderUpdate{Order: o})
		}
	}
}

func (c *Core) handleCmd(ctx context.Context, cmd Command) CmdAck {
	switch cm := cmd.(type) {
	case SubmitOrder:
		return c.handleSubmit(ctx, cm)
	case CancelOrder:
		return c.handleCancel(ctx, cm)
	case ReplaceOrder:
		return c.handleReplace(ctx, cm)
	case Flatten:
		return c.handleFlatten(ctx, cm)
	case KillSwitch:
		return c.handleKill(ctx, cm)
	case Arm:
		return c.handleArm(cm.Venue, true)
	case Disarm:
		return c.handleArm(cm.Venue, false)
	default:
		return CmdAck{Accepted: false, Reason: "unknown command"}
	}
}

func (c *Core) handleSubmit(ctx context.Context, cm SubmitOrder) CmdAck {
	req := OrderRequest{
		Venue: cm.Venue, Symbol: cm.Symbol, Side: cm.Side, Type: cm.Type, TIF: cm.TIF,
		Qty: cm.Qty, LimitPrice: cm.LimitPrice, StopPrice: cm.StopPrice,
		ClientOrderID: c.idgen.Next(),
	}
	if err := req.Validate(); err != nil {
		return CmdAck{Accepted: false, Reason: err.Error(), OrderID: req.ClientOrderID}
	}
	if ok, reason := Evaluate(c.state, c.gate, req, c.marks); !ok {
		ev := OrderBlocked{V: req.Venue, OID: req.ClientOrderID, Req: req, Reason: reason, Ts: c.now()}
		if err := c.appendAndFold(ev, SrcLocal); err != nil {
			slog.Error("exec: append OrderBlocked failed", "err", err)
		}
		return CmdAck{Accepted: false, Reason: reason, OrderID: req.ClientOrderID}
	}
	// Append OrderSubmitted BEFORE the POST (crash-recovery rule). Append failure
	// blocks submission.
	o := Order{Venue: req.Venue, ID: req.ClientOrderID, Symbol: req.Symbol, Side: req.Side,
		Type: req.Type, TIF: req.TIF, Qty: req.Qty, LimitPrice: req.LimitPrice,
		StopPrice: req.StopPrice, Status: StatusSubmitted, LeavesQty: req.Qty,
		CreatedMs: c.now(), UpdatedMs: c.now()}
	if err := c.appendAndFold(OrderSubmitted{Order: o}, SrcLocal); err != nil {
		return CmdAck{Accepted: false, Reason: "event append failed: " + err.Error(), OrderID: req.ClientOrderID}
	}
	b := c.brokers[req.Venue]
	go c.postSubmit(ctx, b, req)
	return CmdAck{Accepted: true, OrderID: req.ClientOrderID}
}

// postSubmit performs the broker POST off the writer loop; a transport error is
// fed back as an OrderRejected event (Plan 5 adds the retry-once-same-ID probe).
func (c *Core) postSubmit(ctx context.Context, b Broker, req OrderRequest) {
	if b == nil {
		return
	}
	if _, err := b.SubmitOrder(ctx, req); err != nil {
		select {
		case c.bevents <- OrderRejected{V: req.Venue, OID: req.ClientOrderID, Reason: "transport: " + err.Error(), Ts: c.now()}:
		case <-ctx.Done():
			return
		}
	}
}

func (c *Core) handleCancel(ctx context.Context, cm CancelOrder) CmdAck {
	if _, ok := c.state.OrderVenue(cm.OrderID); !ok {
		return CmdAck{Accepted: false, Reason: "unknown order", OrderID: cm.OrderID}
	}
	b := c.brokers[cm.Venue]
	go func() {
		if b != nil {
			if err := b.CancelOrder(ctx, cm.OrderID); err != nil {
				slog.Warn("exec: cancel failed", "order", cm.OrderID, "err", err)
			}
		}
	}()
	return CmdAck{Accepted: true, OrderID: cm.OrderID}
}

func (c *Core) handleReplace(ctx context.Context, cm ReplaceOrder) CmdAck {
	if _, ok := c.state.OrderVenue(cm.OrderID); !ok {
		return CmdAck{Accepted: false, Reason: "unknown order", OrderID: cm.OrderID}
	}
	b := c.brokers[cm.Venue]
	rr := ReplaceRequest{Qty: cm.Qty, LimitPrice: cm.LimitPrice, StopPrice: cm.StopPrice}
	go func() {
		if b != nil {
			if err := b.ReplaceOrder(ctx, cm.OrderID, rr); err != nil {
				slog.Warn("exec: replace failed", "order", cm.OrderID, "err", err)
			}
		}
	}()
	return CmdAck{Accepted: true, OrderID: cm.OrderID}
}

func (c *Core) handleFlatten(ctx context.Context, cm Flatten) CmdAck {
	b := c.brokers[cm.Venue]
	if b == nil {
		return CmdAck{Accepted: false, Reason: "unknown venue"}
	}
	if !b.Capabilities().FlattenAll {
		return CmdAck{Accepted: false, Reason: "flatten unsupported on venue"}
	}
	go func() {
		if err := b.Flatten(ctx); err != nil {
			slog.Warn("exec: flatten failed", "venue", cm.Venue, "err", err)
		}
	}()
	return CmdAck{Accepted: true}
}

func (c *Core) handleKill(ctx context.Context, cm KillSwitch) CmdAck {
	// Kill never places orders: cancel-all on the targeted venue(s) + disarm.
	targets := c.venues
	if cm.Venue != "" {
		targets = []VenueID{cm.Venue}
		c.state.SetVenueArmed(cm.Venue, false)
	} else {
		c.state.SetMasterArmed(false)
	}
	for _, v := range targets {
		b := c.brokers[v]
		if b == nil {
			continue
		}
		go func(b Broker, v VenueID) {
			if err := b.CancelAll(ctx, ""); err != nil {
				slog.Warn("exec: kill cancel-all failed", "venue", v, "err", err)
			}
		}(b, v)
	}
	c.syslog("exec.kill", "kill switch: venue="+string(cm.Venue))
	c.emitStatus()
	return CmdAck{Accepted: true}
}

func (c *Core) handleArm(v VenueID, on bool) CmdAck {
	if v == "" {
		c.state.SetMasterArmed(on)
	} else {
		if _, ok := c.state.Venues[v]; !ok {
			return CmdAck{Accepted: false, Reason: "unknown venue"}
		}
		c.state.SetVenueArmed(v, on)
	}
	c.emitStatus()
	for _, vv := range c.venues {
		c.emit(AccountUpdate{Account: c.state.Venue(vv).Account, VenueArmed: c.state.Venue(vv).Armed, MasterArmed: c.state.MasterArmed})
	}
	return CmdAck{Accepted: true}
}

func (c *Core) emitStatus() {
	for _, v := range c.venues {
		c.emit(StatusUpdate{Venue: v, Connected: true, MasterArmed: c.state.MasterArmed})
	}
}

func (c *Core) handleBrokerEvent(_ context.Context, be BrokerEvent) {
	switch e := be.(type) {
	case Event: // order-lifecycle or StreamGap — persist + fold + emit
		if err := c.appendAndFold(e, SrcWS); err != nil {
			slog.Error("exec: append broker event failed", "kind", e.Kind(), "err", err)
		}
	case BrokerAccount:
		c.state.ReconcileAccount(e.Account)
		if BreachedDayLoss(c.state, c.gate) && c.state.MasterArmed {
			c.state.SetMasterArmed(false)
			c.syslog("exec.autodisarm", "day-loss breach: master disarmed")
			c.emitStatus()
		}
		vs := c.state.Venue(e.Account.Venue)
		c.emit(AccountUpdate{Account: e.Account, VenueArmed: vs.Armed, MasterArmed: c.state.MasterArmed})
	case BrokerPositions:
		c.state.ReconcilePositions(e.V, e.Positions)
		for _, p := range e.Positions {
			c.emit(PositionUpdate{Position: p})
		}
	case BrokerConnUp:
		c.emit(StatusUpdate{Venue: e.V, Connected: true, MasterArmed: c.state.MasterArmed})
	case BrokerConnDown:
		c.emit(StatusUpdate{Venue: e.V, Connected: false, MasterArmed: c.state.MasterArmed})
	}
}
