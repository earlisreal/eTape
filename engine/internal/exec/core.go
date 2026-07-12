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
	Session    OrderSession
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
type Arm struct{}
type Disarm struct{}

// ResetBalance is sim-only: cancels resting orders, flattens positions, and
// reseeds the account to the venue's configured starting balance (Core's
// booted CoreConfig.StartingBalance, never a value from the command itself).
type ResetBalance struct{ Venue VenueID }

func (SubmitOrder) isCommand()  {}
func (CancelOrder) isCommand()  {}
func (ReplaceOrder) isCommand() {}
func (Flatten) isCommand()      {}
func (KillSwitch) isCommand()   {}
func (Arm) isCommand()          {}
func (Disarm) isCommand()       {}
func (ResetBalance) isCommand() {}

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
	venues          []VenueID
	gate            GateConfig
	store           EventStore
	brokers         map[VenueID]Broker
	clk             clock.Clock
	idgen           *OrderIDGen
	syslog          func(kind, detail string)
	startingBalance map[VenueID]float64

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
	// StartingBalance is the sim-only per-venue amount ResetBalance reseeds the
	// account to; baked in at boot, never read fresh from a command.
	StartingBalance map[VenueID]float64
}

func NewCore(cfg CoreConfig) *Core {
	sl := cfg.SysLog
	if sl == nil {
		sl = func(string, string) {}
	}
	c := &Core{
		venues:          cfg.Venues,
		gate:            cfg.Gate,
		store:           cfg.Store,
		brokers:         cfg.Brokers,
		clk:             cfg.Clock,
		idgen:           cfg.IDGen,
		syslog:          sl,
		startingBalance: cfg.StartingBalance,
		cmds:            make(chan cmdReq),
		bevents:         make(chan BrokerEvent, 1024),
		markCh:          make(chan Mark, 256),
		updates:         make(chan Update, 4096),
		state:           NewState(cfg.Venues),
		marks:           markState{},
		trades:          NewRoundTripAggregator(),
	}
	// Master always boots disarmed — Recover never touches arm state, so a
	// restart is fully disarmed until a deliberate arm click.
	return c
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
	c.seedTrades(ctx)
	return nil
}

// seedTrades rebuilds today's closed round-trips from persisted fills, so a
// restart doesn't lose Trade History for the current trading day (round trips
// themselves are derived, not persisted — only the underlying fills are).
// Scoped to the 20:00-ET pool day (session.PoolDay), NOT the ET-midnight
// boundary Recover's event replay above uses for orders — those are
// deliberately different windows. LIMITATION: a position opened before the
// 20:00-ET roll and closed today is misattributed (its opening fills fall
// outside this window) — acceptable for an intraday tool; a documented
// follow-up, not fixed here.
func (c *Core) seedTrades(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	fromMs := session.PoolDay(c.clk.Now()) * 1000
	fills, err := c.store.QueryFillsSince(ctx, fromMs)
	if err != nil {
		c.syslog("exec.recover", "seed trades: "+err.Error())
		return
	}
	for _, f := range fills {
		side, ok := sideFromString(f.Side)
		if !ok {
			c.syslog("exec.recover", "seed trades: unparseable Side "+f.Side+" for "+f.Symbol+"@"+string(f.Venue))
			continue
		}
		for _, t := range c.trades.Apply(VenueID(f.Venue), f.Symbol, side, f.Qty, f.Price, f.TsMs) {
			c.emit(TradeUpdate{Trade: t})
		}
	}
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
	case ResetBalance:
		return c.handleResetBalance(ctx, cm)
	case KillSwitch:
		return c.handleKill(ctx, cm)
	case Arm:
		return c.handleArm(true)
	case Disarm:
		return c.handleArm(false)
	default:
		return CmdAck{Accepted: false, Reason: "unknown command"}
	}
}

func (c *Core) handleSubmit(ctx context.Context, cm SubmitOrder) CmdAck {
	req := OrderRequest{
		Venue: cm.Venue, Symbol: cm.Symbol, Side: cm.Side, Type: cm.Type, TIF: cm.TIF,
		Session: cm.Session,
		Qty:     cm.Qty, LimitPrice: cm.LimitPrice, StopPrice: cm.StopPrice,
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
	b := c.brokers[req.Venue]
	// An explicit Overnight session requires the venue's broker to support it
	// natively (Alpaca's Blue Ocean ATS); TradeZero/sim do not. Block here rather
	// than let the adapter silently fall back to a different session than the
	// trader chose. Auto/RTH/Extended never hit this — only an explicit
	// Overnight choice can be capability-blocked.
	if req.Session == SessionOvernight && (b == nil || !b.Capabilities().OvernightSession) {
		reason := "venue does not support overnight session"
		ev := OrderBlocked{V: req.Venue, OID: req.ClientOrderID, Req: req, Reason: reason, Ts: c.now()}
		if err := c.appendAndFold(ev, SrcLocal); err != nil {
			slog.Error("exec: append OrderBlocked failed", "err", err)
		}
		return CmdAck{Accepted: false, Reason: reason, OrderID: req.ClientOrderID}
	}
	// A raw MARKET order outside regular hours cannot be placed on a real venue
	// (TradeZero hard-rejects with R78; Alpaca silently queues it to the next
	// open — worse). The UI converts these to marketable limits before they get
	// here; this is the backstop for a bug or a bypassing client. Sim venues are
	// exempt (Capabilities.MarketOutsideRTH) so replay/practice at night fill.
	if req.Type == TypeMarket && session.PhaseAt(c.clk.Now()) != session.RTH &&
		(b == nil || !b.Capabilities().MarketOutsideRTH) {
		reason := "market order outside regular hours (UI converts these to marketable limits)"
		ev := OrderBlocked{V: req.Venue, OID: req.ClientOrderID, Req: req, Reason: reason, Ts: c.now()}
		if err := c.appendAndFold(ev, SrcLocal); err != nil {
			slog.Error("exec: append OrderBlocked failed", "err", err)
		}
		return CmdAck{Accepted: false, Reason: reason, OrderID: req.ClientOrderID}
	}
	// Append OrderSubmitted BEFORE the POST (crash-recovery rule). Append failure
	// blocks submission.
	o := Order{Venue: req.Venue, ID: req.ClientOrderID, Symbol: req.Symbol, Side: req.Side,
		Type: req.Type, TIF: req.TIF, Session: req.Session, Qty: req.Qty, LimitPrice: req.LimitPrice,
		StopPrice: req.StopPrice, Status: StatusSubmitted, LeavesQty: req.Qty,
		CreatedMs: c.now(), UpdatedMs: c.now()}
	if err := c.appendAndFold(OrderSubmitted{Order: o}, SrcLocal); err != nil {
		return CmdAck{Accepted: false, Reason: "event append failed: " + err.Error(), OrderID: req.ClientOrderID}
	}
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

func (c *Core) handleKill(ctx context.Context, cm KillSwitch) CmdAck {
	// Kill never places orders: cancel-all on the targeted venue(s) + disarm.
	targets := c.venues
	if cm.Venue != "" {
		targets = []VenueID{cm.Venue}
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

func (c *Core) handleArm(on bool) CmdAck {
	c.state.SetMasterArmed(on)
	c.emitStatus()
	for _, vv := range c.venues {
		c.emit(AccountUpdate{Account: c.state.Venue(vv).Account, MasterArmed: c.state.MasterArmed})
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
		c.emit(AccountUpdate{Account: e.Account, MasterArmed: c.state.MasterArmed})
	case BrokerPositions:
		c.state.ReconcilePositions(e.V, e.Positions)
		for _, p := range e.Positions {
			c.emit(PositionUpdate{Position: p})
		}
	case BrokerConnUp:
		c.emit(StatusUpdate{Venue: e.V, Connected: true, MasterArmed: c.state.MasterArmed})
	case BrokerConnDown:
		c.emit(StatusUpdate{Venue: e.V, Connected: false, MasterArmed: c.state.MasterArmed, Note: e.Note})
	}
}
