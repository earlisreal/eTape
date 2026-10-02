package exec

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// defaultRecoverSnapshotTimeout bounds each venue's Snapshot call during
// Recover when CoreConfig.RecoverSnapshotTimeout is unset. It is a short,
// fixed deadline, not the venue's own HTTP-client timeout (which can be
// 10-20+s) — one misconfigured/unreachable venue must not stall the whole
// boot sequence (Recover must fully complete before uihub starts listening).
const defaultRecoverSnapshotTimeout = 5 * time.Second

// Command is a UI→engine execution command. Sealed union.
type Command interface{ isCommand() }

type SubmitOrder struct {
	Venue               VenueID
	Symbol              string
	Side                Side
	Type                OrderType
	TIF                 TIF
	Session             OrderSession
	Qty                 float64
	DeferredPositionPct float64
	LimitPrice          float64
	StopPrice           float64
	RouteExpected       HeldRoute
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
type ConfigureHeldDemand struct{ Demand HeldDemandController }
type ResumeHeldOrder struct {
	Venue   VenueID
	OrderID string
}
type AcknowledgeHeldStopLimit struct {
	Venue    VenueID
	Identity string
}

// ResetBalance is sim-only: cancels resting orders, flattens positions, and
// reseeds the account to the venue's configured starting balance (Core's
// booted CoreConfig.StartingBalance, never a value from the command itself).
type ResetBalance struct{ Venue VenueID }
type SetActiveVenue struct{ Venue VenueID }

func (SubmitOrder) isCommand()              {}
func (CancelOrder) isCommand()              {}
func (ReplaceOrder) isCommand()             {}
func (Flatten) isCommand()                  {}
func (KillSwitch) isCommand()               {}
func (Arm) isCommand()                      {}
func (Disarm) isCommand()                   {}
func (ConfigureHeldDemand) isCommand()      {}
func (ResumeHeldOrder) isCommand()          {}
func (AcknowledgeHeldStopLimit) isCommand() {}
func (ResetBalance) isCommand()             {}
func (SetActiveVenue) isCommand()           {}

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

type eligiblePreviewReq struct {
	symbol string
	reply  chan EligiblePrintPreview
}

// markState is the Core's latest-mark map; implements MarkSource.
type markState map[string]float64

func (m markState) LastTrade(sym string) (float64, bool) { v, ok := m[sym]; return v, ok }

// Core is the single-writer execution coordinator.
type Core struct {
	venues                 []VenueID
	gate                   GateConfig
	store                  EventStore
	brokers                map[VenueID]Broker
	clk                    clock.Clock
	idgen                  *OrderIDGen
	syslog                 func(kind, detail string)
	startingBalance        map[VenueID]float64
	recoverSnapshotTimeout time.Duration

	cmds    chan cmdReq
	bevents chan BrokerEvent
	markCh  chan Mark
	printCh chan EligiblePrint
	preview chan eligiblePreviewReq
	updates chan Update
	dropped atomic.Uint64

	state *State
	marks markState

	trades           *RoundTripAggregator
	positionOpens    *RoundTripAggregator
	cycles           *cycleProjection
	closed           *closedOrders
	heldDemand       HeldDemandController
	printHealthy     bool
	lastPrintGapMs   int64
	lastPrintSeq     map[string]int64
	lastPrintDay     map[string]int64
	lastEligible     map[string]EligiblePrint
	heldLiveIdentity map[VenueID]string
	heldLiveAck      map[VenueID]string
	shutdownHeld     HeldShutdownSummary
	positionExecIDs  map[string]bool
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
	// RecoverSnapshotTimeout bounds each venue's Broker.Snapshot call during
	// Recover, so one misconfigured/unreachable venue can't stall the whole
	// boot past a short, fixed deadline. Zero means use
	// defaultRecoverSnapshotTimeout.
	RecoverSnapshotTimeout    time.Duration
	ActiveVenue               VenueID
	HeldStopLimitLiveIdentity map[VenueID]string
	HeldStopLimitAcknowledged map[VenueID]string
}

func NewCore(cfg CoreConfig) *Core {
	sl := cfg.SysLog
	if sl == nil {
		sl = func(string, string) {}
	}
	recoverSnapshotTimeout := cfg.RecoverSnapshotTimeout
	if recoverSnapshotTimeout <= 0 {
		recoverSnapshotTimeout = defaultRecoverSnapshotTimeout
	}
	c := &Core{
		venues:                 cfg.Venues,
		gate:                   cfg.Gate,
		store:                  cfg.Store,
		brokers:                cfg.Brokers,
		clk:                    cfg.Clock,
		idgen:                  cfg.IDGen,
		syslog:                 sl,
		startingBalance:        cfg.StartingBalance,
		recoverSnapshotTimeout: recoverSnapshotTimeout,
		cmds:                   make(chan cmdReq),
		bevents:                make(chan BrokerEvent, 1024),
		markCh:                 make(chan Mark, 256),
		printCh:                make(chan EligiblePrint, 8192),
		preview:                make(chan eligiblePreviewReq),
		updates:                make(chan Update, 4096),
		state:                  NewState(cfg.Venues),
		marks:                  markState{},
		trades:                 NewRoundTripAggregator(),
		positionOpens:          NewRoundTripAggregator(),
		cycles:                 newCycleProjection(),
		closed:                 newClosedOrders(),
		lastPrintSeq:           make(map[string]int64),
		lastPrintDay:           make(map[string]int64),
		lastEligible:           make(map[string]EligiblePrint),
		positionExecIDs:        make(map[string]bool),
		heldLiveIdentity:       cloneVenueStrings(cfg.HeldStopLimitLiveIdentity),
		heldLiveAck:            cloneVenueStrings(cfg.HeldStopLimitAcknowledged),
	}
	for v := range cfg.Gate.AccountRequired {
		// The stale transition is emitted only after the poller's five-failure
		// grace period; until then a venue is considered freshly armed.
		c.state.SetAccountFresh(v, true)
	}
	c.state.SetActiveVenue(cfg.ActiveVenue)
	// Master always boots disarmed — Recover never touches arm state, so a
	// restart is fully disarmed until a deliberate arm click.
	return c
}

func cloneVenueStrings(src map[VenueID]string) map[VenueID]string {
	dst := make(map[VenueID]string, len(src))
	for venue, value := range src {
		dst[venue] = value
	}
	return dst
}

func (c *Core) Updates() <-chan Update { return c.updates }

func (c *Core) DroppedUpdates() uint64 { return c.dropped.Load() }

// ShutdownHeldSummary is read after Run returns and summarizes the held-order
// safeguards performed during clean shutdown.
func (c *Core) ShutdownHeldSummary() HeldShutdownSummary { return c.shutdownHeld }

// Do submits a command and blocks for its accepted|blocked ack. Safe from any
// goroutine.
func (c *Core) Do(cmd Command) CmdAck {
	reply := make(chan CmdAck, 1)
	c.cmds <- cmdReq{cmd: cmd, reply: reply}
	return <-reply
}

// DoContext is the shutdown-safe form used by runtime observers that may be
// called while the top-level engine context is being canceled.
func (c *Core) DoContext(ctx context.Context, cmd Command) CmdAck {
	reply := make(chan CmdAck, 1)
	select {
	case c.cmds <- cmdReq{cmd: cmd, reply: reply}:
	case <-ctx.Done():
		return CmdAck{Accepted: false, Reason: "execution core stopped"}
	}
	select {
	case ack := <-reply:
		return ack
	case <-ctx.Done():
		return CmdAck{Accepted: false, Reason: "execution core stopped"}
	}
}

// FeedMark delivers a last-trade mark; keep-latest, drop-on-full (never blocks
// the caller — mirrors md.Core's mark path).
func (c *Core) FeedMark(m Mark) {
	select {
	case c.markCh <- m:
	default:
	}
}

// FeedEligiblePrint is intentionally lossless: dropping a trigger print would
// make locally-held custody unsafe, so this lane backpressures at capacity.
func (c *Core) FeedEligiblePrint(ctx context.Context, p EligiblePrint) {
	select {
	case c.printCh <- p:
	case <-ctx.Done():
	}
}

// PreviewEligiblePrint reads the same single-writer trust state used for
// trigger evaluation. It never treats cached quote/snapshot prices as live.
func (c *Core) PreviewEligiblePrint(ctx context.Context, symbol string) EligiblePrintPreview {
	reply := make(chan EligiblePrintPreview, 1)
	select {
	case c.preview <- eligiblePreviewReq{symbol: symbol, reply: reply}:
	case <-ctx.Done():
		return EligiblePrintPreview{}
	}
	select {
	case preview := <-reply:
		return preview
	case <-ctx.Done():
		return EligiblePrintPreview{}
	}
}

// PublishBrokerEvent is the observer seam used by account polling and other
// engine-owned sources that are not broker event channels.
func (c *Core) PublishBrokerEvent(be BrokerEvent) {
	select {
	case c.bevents <- be:
	default:
		// Account refreshes are periodic; a full queue is safer to drop than to
		// block the poller and stall all venue refreshes.
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

func isEngineOrderID(id string) bool { return strings.HasPrefix(id, "ET") }

// Recover rebuilds state at boot: replay today's persisted events, then seed
// account/positions/open-orders from each venue's broker snapshot. Call before
// Run.
func (c *Core) Recover(ctx context.Context) error {
	c.closed = newClosedOrders()
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
		if fill, ok := ev.(OrderFilled); ok && fill.PositionExecID != "" {
			c.positionExecIDs[positionExecKey(fill.F.Venue, fill.PositionExecID)] = true
		}
	}
	for v, vs := range c.state.Venues {
		for id, o := range vs.Orders {
			if !o.Working() || o.Action == nil || o.Action.Phase != ActionRequested {
				continue
			}
			action := *o.Action
			action.Phase, action.Reason = ActionUnknown, "engine restarted before venue confirmation"
			if err := c.appendAndFold(OrderActionChanged{V: v, OID: id, Action: action, Ts: c.now()}, SrcReconcile); err != nil {
				return err
			}
		}
	}
	cutoffMs := session.PoolDay(c.clk.Now()) * 1000
	var history []EventEnvelope
	if hs, ok := c.store.(closedHistoryStore); ok {
		history, err = hs.ReadExecOrderHistoriesSince(cutoffMs)
		if err != nil {
			return err
		}
	} else {
		history, err = c.store.ReadExecEventsSince(cutoffMs)
		if err != nil {
			return err
		}
	}
	for _, env := range history {
		ev, err := DecodeEvent(env.Kind, env.Payload)
		if err != nil {
			return err
		}
		c.closed.apply(ev, env.Seq)
	}
	c.closed.seedState(c.state)
	foundBrokerOrders := make(map[string]bool)
	for _, v := range c.venues {
		b, ok := c.brokers[v]
		if !ok {
			continue
		}
		snapCtx, cancel := context.WithTimeout(ctx, c.recoverSnapshotTimeout)
		acct, pos, orders, err := b.Snapshot(snapCtx)
		cancel()
		if err != nil {
			c.syslog("exec.recover", "snapshot "+string(v)+": "+err.Error())
			continue
		}
		c.state.ReconcileAccount(acct)
		c.positionOpens.reconcilePositions(v, pos)
		c.state.ReconcilePositions(v, pos)
		c.state.SetPositionsReady(v, true)
		externalOrders := make([]Order, 0, len(orders))
		for _, o := range orders {
			o.Venue = v
			foundBrokerOrders[o.ID] = true
			if _, exists := c.state.OrderVenue(o.ID); !exists && !c.closed.hasSeed(o.ID) {
				if isEngineOrderID(o.ID) {
					if err := c.appendAndFold(OrderSubmitted{Order: o}, SrcReconcile); err != nil {
						c.syslog("exec.recover", "persist adopted order "+o.ID+": "+err.Error())
						externalOrders = append(externalOrders, o)
					}
				} else {
					externalOrders = append(externalOrders, o)
				}
			} else {
				c.state.ReconcileOpenOrders(v, []Order{o})
				c.closed.adopt(o)
			}
		}
		c.state.ReconcileExternalOpenOrders(v, externalOrders)
		c.maybeClearFlattenPending(v)
	}
	if hs, ok := c.store.(orderHistoryByIDStore); ok {
		ids := make([]string, 0)
		for _, vs := range c.state.Venues {
			for id, o := range vs.Orders {
				if o.Working() {
					ids = append(ids, id)
				}
			}
		}
		sort.Strings(ids)
		envs, err := hs.ReadExecOrderHistoriesFor(ids)
		if err != nil {
			return err
		}
		actions := make(map[string]OrderAction)
		for _, env := range envs {
			ev, err := DecodeEvent(env.Kind, env.Payload)
			if err != nil {
				return err
			}
			if action, ok := ev.(OrderActionChanged); ok {
				actions[action.OID] = action.Action
			}
		}
		for _, id := range ids {
			venue, ok := c.state.OrderVenue(id)
			if !ok {
				continue
			}
			action, ok := actions[id]
			if !ok {
				continue
			}
			if action.Phase == ActionRequested {
				action.Phase, action.Reason = ActionUnknown, "engine restarted before venue confirmation"
			}
			current := c.order(id)
			if current.Action == nil || *current.Action != action {
				if err := c.appendAndFold(OrderActionChanged{V: venue, OID: id, Action: action, Ts: c.now()}, SrcReconcile); err != nil {
					return err
				}
			}
		}
	}
	for _, id := range c.heldOrderIDs() {
		o := c.order(id)
		if o.Held == nil {
			continue
		}
		h := *o.Held
		switch h.Phase {
		case HeldWaiting, HeldArmed:
			h.Phase, h.PausedReason = HeldPaused, "engine restarted; resume manually"
		case HeldActivating:
			if foundBrokerOrders[id] {
				h.Phase, h.PausedReason = HeldWorking, ""
			} else {
				h.Phase, h.PausedReason = HeldUnknown, "restart during activation; reconcile manually, no repost"
			}
		case HeldCancelRequested:
			h.CancelRequested = true
			if foundBrokerOrders[id] {
				h.CancelSent, h.PausedReason = true, "cancel resubmitted for reconciliation"
			} else {
				h.Phase, h.PausedReason = HeldUnknown, "cancel outcome unknown after restart"
			}
		default:
			continue
		}
		if err := c.appendAndFold(HeldOrderChanged{V: o.Venue, OID: o.ID, Held: h, Ts: c.now()}, SrcReconcile); err != nil {
			c.syslog("exec.recover", "persist held recovery "+id+": "+err.Error())
		}
		if h.Phase == HeldCancelRequested && foundBrokerOrders[id] {
			if b := c.brokers[o.Venue]; b != nil {
				c.startVenueAction(ctx, b, o.Venue, id, ActionCancel, ReplaceRequest{})
			}
		}
	}
	for v, vs := range c.state.Venues {
		for id, o := range vs.Orders {
			if !o.Working() || !foundBrokerOrders[id] || o.Action == nil || o.Action.Phase != ActionUnknown {
				continue
			}
			switch o.Action.Kind {
			case ActionSubmit:
				confirmed := *o.Action
				confirmed.Phase, confirmed.Reason = ActionConfirmed, ""
				if err := c.appendAndFold(OrderActionChanged{V: v, OID: id, Action: confirmed, Ts: c.now()}, SrcReconcile); err != nil {
					c.syslog("exec.recover", "persist submit confirmation "+id+": "+err.Error())
				}
			case ActionCancel:
				if o.Held == nil {
					if b := c.brokers[v]; b != nil {
						c.startVenueAction(ctx, b, v, id, ActionCancel, ReplaceRequest{})
					}
				}
			case ActionReplace:
				a := o.Action
				limitMatches := a.RequestedLimitPrice == 0 || math.Abs(o.LimitPrice-a.RequestedLimitPrice) < 1e-8
				stopMatches := a.RequestedStopPrice == 0 || math.Abs(o.StopPrice-a.RequestedStopPrice) < 1e-8
				qtyMatches := a.RequestedQty == 0 || math.Abs(o.Qty-a.RequestedQty) < 1e-8
				if limitMatches && stopMatches && qtyMatches {
					confirmed := *a
					confirmed.Phase, confirmed.Reason = ActionConfirmed, ""
					if err := c.appendAndFold(OrderActionChanged{V: v, OID: id, Action: confirmed, Ts: c.now()}, SrcReconcile); err != nil {
						c.syslog("exec.recover", "persist replace confirmation "+id+": "+err.Error())
					}
				}
			}
		}
	}
	for _, row := range c.closed.snapshotSince(cutoffMs) {
		c.emit(ClosedOrderUpdate{ClosedOrder: row})
	}
	for _, vs := range c.state.Venues {
		for _, o := range vs.Orders {
			if o.Working() {
				c.emit(OrderUpdate{Order: o})
			}
		}
	}
	c.recoverCycles(ctx)
	c.seedTrades(ctx)
	for _, v := range c.venues {
		if c.state.PositionsReady(v) {
			c.emit(PositionReadinessUpdate{Venue: v, Ready: true})
		}
	}
	for _, v := range c.venues {
		for _, p := range c.state.Venue(v).Positions {
			c.emitProjectedPosition(p)
		}
	}
	return nil
}

type cycleStore interface {
	SaveCycleCheckpoint(CycleCheckpoint) error
	LoadCycleCheckpoint(VenueID) (CycleCheckpoint, bool, error)
	QueryVenueFillsSince(context.Context, string, int64) ([]FillRow, error)
}

func (c *Core) recoverCycles(ctx context.Context) {
	start := session.TradingCycleStart(c.clk.Now()).UnixMilli()
	cs, _ := c.store.(cycleStore)
	for _, v := range c.venues {
		positions := make([]Position, 0, len(c.state.Venue(v).Positions))
		for _, p := range c.state.Venue(v).Positions {
			positions = append(positions, p)
		}
		if cs == nil {
			c.cycles.bootstrap(v, start, positions)
			continue
		}
		cp, ok, err := cs.LoadCycleCheckpoint(v)
		if err != nil || !ok || cp.StartMs != start {
			c.cycles.bootstrap(v, start, positions)
			continue
		}
		c.cycles.restore(cp)
		fills, err := cs.QueryVenueFillsSince(ctx, string(v), start)
		if err != nil {
			c.syslog("exec.recover", "cycle fills: "+err.Error())
			continue
		}
		for _, row := range fills {
			side, ok := sideFromString(row.Side)
			if !ok {
				continue
			}
			c.cycles.applyFill(Fill{Venue: v, OrderID: row.OrderID, Symbol: row.Symbol, Side: side, Qty: row.Qty, Price: row.Price, TsMs: row.TsMs})
		}
	}
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
	parsed := make([]Fill, 0, len(fills))
	for _, f := range fills {
		side, ok := sideFromString(f.Side)
		if !ok {
			c.syslog("exec.recover", "seed trades: unparseable Side "+f.Side+" for "+f.Symbol+"@"+string(f.Venue))
			continue
		}
		parsed = append(parsed, Fill{Venue: VenueID(f.Venue), OrderID: f.OrderID, Symbol: f.Symbol, Side: side, Qty: f.Qty, Price: f.Price, TsMs: f.TsMs})
	}
	positions := make([]Position, 0)
	for _, v := range c.venues {
		for _, p := range c.state.Venue(v).Positions {
			positions = append(positions, p)
		}
	}
	c.positionOpens.rebuildOpenPositions(positions, parsed)
	for _, f := range parsed {
		for _, t := range c.trades.Apply(f.Venue, f.Symbol, f.Side, f.Qty, f.Price, f.TsMs) {
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
	cycleTimer := c.clk.After(time.Until(session.NextTradingCycleStart(c.clk.Now())))
	heldTimer := c.clk.NewTicker(time.Second)
	defer heldTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			c.shutdownHeldOrders()
			return ctx.Err()
		case req := <-c.cmds:
			req.reply <- c.handleCmd(ctx, req.cmd)
		case be := <-c.bevents:
			c.handleBrokerEvent(ctx, be)
		case m := <-c.markCh:
			c.marks[m.Symbol] = m.Price
			c.cycles.mark(m.Symbol, m.Price)
			for _, v := range c.venues {
				c.emitProjectedAccount(v)
			}
		case p := <-c.printCh:
			c.handleEligiblePrint(ctx, p)
		case req := <-c.preview:
			p, ok := c.lastEligible[req.symbol]
			preview := EligiblePrintPreview{}
			if ok && c.printHealthy && c.freshEligiblePrint(p) {
				preview = EligiblePrintPreview{Price: p.Price, TsMs: p.TsMs, Trusted: true}
			}
			req.reply <- preview
		case <-heldTimer.C():
			c.expireHeldOrders(ctx)
		case <-cycleTimer:
			c.rollCycle()
			cycleTimer = c.clk.After(session.NextTradingCycleStart(c.clk.Now()).Sub(c.clk.Now()))
		}
	}
}

func (c *Core) rollCycle() {
	start := session.TradingCycleStart(c.clk.Now()).UnixMilli()
	cs, _ := c.store.(cycleStore)
	for _, v := range c.venues {
		vs := c.state.Venue(v)
		positions := make([]Position, 0, len(vs.Positions))
		for _, p := range vs.Positions {
			positions = append(positions, p)
		}
		c.cycles.reset(v, start, positions)
		if cs != nil {
			if err := cs.SaveCycleCheckpoint(*c.cycles.byVenue[v]); err != nil {
				c.syslog("exec.cycle", err.Error())
			}
		}
		c.emitProjectedAccount(v)
		for _, p := range positions {
			c.emitProjectedPosition(p)
		}
	}
}

func (c *Core) emitProjectedAccount(v VenueID) {
	acct := c.state.Venue(v).Account
	if acct.TsMs == 0 && acct.Equity == 0 && acct.BuyingPower == 0 && acct.AvailableCash == 0 && acct.Realized == 0 && acct.DayPnL == 0 {
		return
	}
	start, open, realized, day := c.cycles.account(v)
	if b := c.brokers[v]; b != nil && (b.Capabilities().AuthoritativeDayPnL || b.Capabilities().CalculatedDayPnL) {
		day = acct.DayPnL
	}
	c.emit(AccountUpdate{Account: acct, MasterArmed: c.state.MasterArmed,
		CycleStartMs: start, CycleRealized: realized, DisplayRealized: open, DisplayDayPnL: day})
}

func (c *Core) emitProjectedPosition(p Position) {
	p.DayBasis = c.cycles.position(p.Venue, p.Symbol).Basis
	if opened := c.positionOpens.OpenMs(p.Venue, p.Symbol); opened > 0 {
		p.OpenedMs = opened
	}
	c.emit(PositionUpdate{Position: p})
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
	seq, err := c.store.AppendExecEvent(env, fill)
	if err != nil {
		return err
	}
	var prior Order
	var hadPrior bool
	if filled, ok := ev.(OrderFilled); ok {
		prior = c.order(filled.F.OrderID)
		_, hadPrior = c.state.OrderVenue(filled.F.OrderID)
	}
	c.state.Apply(ev)
	if filled, ok := ev.(OrderFilled); ok {
		c.applyPositionFill(filled, prior, hadPrior)
	}
	for _, row := range c.closed.apply(ev, seq) {
		c.emit(ClosedOrderUpdate{ClosedOrder: row})
	}
	c.emitForEvent(ev)
	if filled, ok := ev.(OrderFilled); ok {
		if p, found := c.state.Venue(filled.F.Venue).Positions[filled.F.Symbol]; found {
			c.emitProjectedPosition(p)
		} else {
			c.emitProjectedPosition(Position{Venue: filled.F.Venue, Symbol: filled.F.Symbol})
		}
	}
	return nil
}

func positionExecKey(v VenueID, id string) string { return string(v) + "\x00" + id }

func (c *Core) applyPositionFill(e OrderFilled, prior Order, hadPrior bool) {
	if !c.state.PositionsReady(e.F.Venue) || e.F.Symbol == "" || !hadPrior || e.CumQty <= prior.ExecutedQty {
		return
	}
	if e.PositionExecID != "" {
		key := positionExecKey(e.F.Venue, e.PositionExecID)
		if c.positionExecIDs[key] {
			return
		}
		c.positionExecIDs[key] = true
	}
	// Use the order's cumulative delta, not the push's last-fill quantity. A
	// reconnect snapshot can already include a queued fill; the next fill push
	// then reports a cumulative quantity whose delta is smaller than F.Qty.
	c.applyPositionEffect(e.F.Venue, e.F.Symbol, e.F.Side, e.CumQty-prior.ExecutedQty, e.F.Price, "")
}

func (c *Core) applyPositionEffect(v VenueID, symbol string, side Side, qty, price float64, execID string) bool {
	if !c.state.PositionsReady(v) || symbol == "" || qty <= 0 {
		return false
	}
	if execID != "" {
		key := positionExecKey(v, execID)
		if c.positionExecIDs[key] {
			return false
		}
		c.positionExecIDs[key] = true
	}
	delta := qty
	if !longward(side) {
		delta = -delta
	}
	vs := c.state.Venue(v)
	p := vs.Positions[symbol]
	newQty := p.Qty + delta
	switch {
	case p.Qty == 0 || p.Qty*newQty < 0:
		p.AvgPrice = price
	case p.Qty*delta > 0:
		p.AvgPrice = (math.Abs(p.Qty)*p.AvgPrice + math.Abs(delta)*price) / math.Abs(newQty)
	}
	p.Venue, p.Symbol, p.Qty = v, symbol, newQty
	if newQty == 0 {
		delete(vs.Positions, symbol)
	} else {
		vs.Positions[symbol] = p
	}
	return true
}

// emitForEvent pushes the Update(s) an event implies.
func (c *Core) emitForEvent(ev Event) {
	if flatten, ok := ev.(FlattenPendingChanged); ok {
		c.emit(FlattenPendingUpdate{Venue: flatten.V, Pending: flatten.Pending})
	}
	if f, ok := ev.(OrderFilled); ok {
		c.positionOpens.Apply(f.F.Venue, f.F.Symbol, f.F.Side, f.F.Qty, f.F.Price, f.F.TsMs)
		c.cycles.applyFill(f.F)
		c.emit(FillUpdate{Fill: f.F})
		for _, t := range c.trades.Apply(f.F.Venue, f.F.Symbol, f.F.Side, f.F.Qty, f.F.Price, f.F.TsMs) {
			c.emit(TradeUpdate{Trade: t})
		}
		c.emitProjectedAccount(f.F.Venue)
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
	case SetActiveVenue:
		return c.handleSetActiveVenue(cm)
	case ConfigureHeldDemand:
		c.heldDemand = cm.Demand
		return CmdAck{Accepted: true}
	case ResumeHeldOrder:
		return c.handleResumeHeld(ctx, cm)
	case AcknowledgeHeldStopLimit:
		identity := c.heldLiveIdentity[cm.Venue]
		if identity == "" || identity != cm.Identity {
			return CmdAck{Accepted: false, Reason: "live account identity changed; restart before acknowledging"}
		}
		c.heldLiveAck[cm.Venue] = identity
		c.emit(HeldStopLimitAckUpdate{Venue: cm.Venue, Acknowledged: true})
		return CmdAck{Accepted: true}
	case KillSwitch:
		return c.handleKill(ctx, cm)
	case Arm:
		return c.handleArm(ctx, true)
	case Disarm:
		return c.handleArm(ctx, false)
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
	deferred := cm.DeferredPositionPct != 0
	if deferred {
		if !math.IsNaN(cm.DeferredPositionPct) && !math.IsInf(cm.DeferredPositionPct, 0) && cm.DeferredPositionPct > 0 && cm.DeferredPositionPct <= 100 &&
			cm.Side == SideSell && cm.Type == TypeStopLimit && cm.TIF == TIFDay && cm.Qty == 0 {
			// The held parent deliberately has no broker quantity until its trigger.
		} else {
			return CmdAck{Accepted: false, Reason: "deferred position sizing requires a zero-quantity DAY SELL stop-limit with position % in (0, 100]", OrderID: req.ClientOrderID}
		}
	} else if err := req.Validate(); err != nil {
		return CmdAck{Accepted: false, Reason: err.Error(), OrderID: req.ClientOrderID}
	}
	if deferred && !c.state.PositionsReady(req.Venue) {
		return CmdAck{Accepted: false, Reason: "position data unavailable; reconcile the venue before placing a deferred stop-sell", OrderID: req.ClientOrderID}
	}
	route := RouteNative
	var deadline time.Time
	if req.Type == TypeStopLimit {
		var effective OrderSession
		var routeReason string
		if deferred {
			route, effective, deadline, routeReason = ResolveDeferredStopSellRoute(c.clk.Now(), req.TIF, req.Session)
		} else {
			route, effective, deadline = ResolveStopLimitRoute(c.clk.Now(), req.TIF, req.Session)
		}
		if route == RouteUnsupported {
			return CmdAck{Accepted: false, Reason: routeReason, OrderID: req.ClientOrderID}
		}
		if cm.RouteExpected != "" && cm.RouteExpected != route {
			return CmdAck{Accepted: false, Reason: "order route changed; review custody and retry", OrderID: req.ClientOrderID}
		}
		if route == RouteEngineHeld {
			if identity := c.heldLiveIdentity[req.Venue]; identity != "" && c.heldLiveAck[req.Venue] != identity {
				return CmdAck{Accepted: false, Reason: "live engine-held stop-limit requires account acknowledgement"}
			}
			req.Session = effective
			switch req.Side {
			case SideBuy, SideCover:
				if req.LimitPrice < req.StopPrice {
					return CmdAck{Accepted: false, Reason: "buy stop-limit requires limit at or above trigger", OrderID: req.ClientOrderID}
				}
			case SideSell, SideShort:
				if req.LimitPrice > req.StopPrice {
					return CmdAck{Accepted: false, Reason: "sell stop-limit requires limit at or below trigger", OrderID: req.ClientOrderID}
				}
			}
		}
	}
	var ok bool
	var reason string
	if deferred {
		ok, reason = EvaluateDeferredAdmission(c.state, c.gate, req)
	} else {
		ok, reason = Evaluate(c.state, c.gate, req, c.marks)
	}
	if !ok {
		ev := OrderBlocked{V: req.Venue, OID: req.ClientOrderID, Req: req, Reason: reason, Ts: c.now()}
		if err := c.appendAndFold(ev, SrcLocal); err != nil {
			slog.Error("exec: append OrderBlocked failed", "err", err)
		}
		return CmdAck{Accepted: false, Reason: reason, OrderID: req.ClientOrderID}
	}
	if req.Side == SideSell && !deferred {
		if good, why := c.checkSellQuantity(req, ""); !good {
			return CmdAck{Accepted: false, Reason: why, OrderID: req.ClientOrderID}
		}
	}
	b := c.brokers[req.Venue]
	if b == nil {
		return CmdAck{Accepted: false, Reason: "unknown venue", OrderID: req.ClientOrderID}
	}
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
	// exempt (Capabilities.MarketOutsideRTH) so demo/practice at night fill.
	if req.Type == TypeMarket && session.PhaseAt(c.clk.Now()) != session.RTH &&
		(b == nil || !b.Capabilities().MarketOutsideRTH) {
		reason := "market order outside regular hours (UI converts these to marketable limits)"
		ev := OrderBlocked{V: req.Venue, OID: req.ClientOrderID, Req: req, Reason: reason, Ts: c.now()}
		if err := c.appendAndFold(ev, SrcLocal); err != nil {
			slog.Error("exec: append OrderBlocked failed", "err", err)
		}
		return CmdAck{Accepted: false, Reason: reason, OrderID: req.ClientOrderID}
	}
	if route == RouteEngineHeld {
		if c.heldDemand == nil {
			return CmdAck{Accepted: false, Reason: "execution ticker demand unavailable for held stop-limit", OrderID: req.ClientOrderID}
		}
		if err := c.heldDemand.Acquire(ctx, req.ClientOrderID, req.Symbol); err != nil {
			return CmdAck{Accepted: false, Reason: "execution ticker unavailable: " + err.Error(), OrderID: req.ClientOrderID}
		}
		o := newOrderFromRequest(req, c.now())
		if deferred {
			o.DeferredPositionPct = cm.DeferredPositionPct
		}
		o.Held = &HeldOrder{Phase: HeldWaiting, DeadlineMs: deadline.UnixMilli()}
		if err := c.appendAndFold(OrderSubmitted{Order: o}, SrcLocal); err != nil {
			c.heldDemand.Release(req.ClientOrderID)
			return CmdAck{Accepted: false, Reason: "event append failed: " + err.Error(), OrderID: req.ClientOrderID}
		}
		if last, ok := c.lastEligible[req.Symbol]; ok && c.freshEligiblePrint(last) && StopLimitTriggered(o.Side, last.Price, o.StopPrice) {
			c.activateHeld(ctx, c.order(o.ID))
		}
		return CmdAck{Accepted: true, OrderID: req.ClientOrderID}
	}
	// Append OrderSubmitted BEFORE the POST (crash-recovery rule). Append failure
	// blocks submission.
	o := newOrderFromRequest(req, c.now())
	if req.Side == SideSell {
		o.Action = &OrderAction{Kind: ActionSubmit, Phase: ActionRequested}
	}
	if err := c.appendAndFold(OrderSubmitted{Order: o}, SrcLocal); err != nil {
		return CmdAck{Accepted: false, Reason: "event append failed: " + err.Error(), OrderID: req.ClientOrderID}
	}
	go c.postSubmit(ctx, b, req)
	return CmdAck{Accepted: true, OrderID: req.ClientOrderID}
}

func newOrderFromRequest(req OrderRequest, now int64) Order {
	return Order{Venue: req.Venue, ID: req.ClientOrderID, Symbol: req.Symbol, Side: req.Side,
		Type: req.Type, TIF: req.TIF, Session: req.Session, Qty: req.Qty, LimitPrice: req.LimitPrice,
		StopPrice: req.StopPrice, Status: StatusSubmitted, LeavesQty: req.Qty, CreatedMs: now, UpdatedMs: now}
}

func (c *Core) checkSellQuantity(req OrderRequest, excludeID string) (bool, string) {
	if c.state.Venue(req.Venue).FlattenPending {
		return false, "venue flatten is awaiting authoritative reconciliation"
	}
	if !c.state.PositionsReady(req.Venue) {
		return false, "position data unavailable; reconcile the venue before selling"
	}
	long := c.state.VenuePositionShares(req.Venue, req.Symbol)
	if long <= 0 {
		return false, "no open long position to sell"
	}
	available := long - c.state.VenueSellCommittedShares(req.Venue, req.Symbol, excludeID)
	if available < 0 {
		available = 0
	}
	if req.Qty > available+1e-9 {
		return false, "sell quantity exceeds uncommitted long position"
	}
	return true, ""
}

func (c *Core) checkSellLeaves(req OrderRequest, leaves float64, excludeID string) (bool, string) {
	if c.state.Venue(req.Venue).FlattenPending {
		return false, "venue flatten is awaiting authoritative reconciliation"
	}
	if !c.state.PositionsReady(req.Venue) {
		return false, "position data unavailable; reconcile the venue before selling"
	}
	long := c.state.VenuePositionShares(req.Venue, req.Symbol)
	if long <= 0 {
		return false, "no open long position to sell"
	}
	available := long - c.state.VenueSellCommittedShares(req.Venue, req.Symbol, excludeID)
	if available < 0 {
		available = 0
	}
	if leaves > available+1e-9 {
		return false, "sell quantity exceeds uncommitted long position"
	}
	return true, ""
}

// postSubmit performs the broker POST off the writer loop. A SELL transport
// error is ambiguous, so its share reservation stays working until reconciliation.
func (c *Core) postSubmit(ctx context.Context, b Broker, req OrderRequest) {
	if b == nil {
		return
	}
	ack, err := b.SubmitOrder(ctx, req)
	if req.Side == SideSell {
		outcome := BrokerSubmitOutcome{V: req.Venue, OID: req.ClientOrderID, Accepted: err == nil && ack.Accepted}
		if err != nil {
			outcome.Reason = "transport: " + err.Error()
		} else if !ack.Accepted {
			outcome.Reason = "submit acknowledgement did not confirm acceptance"
		}
		select {
		case c.bevents <- outcome:
		case <-ctx.Done():
		}
		return
	}
	if err != nil {
		select {
		case c.bevents <- OrderRejected{V: req.Venue, OID: req.ClientOrderID, Reason: "transport: " + err.Error(), Ts: c.now()}:
		case <-ctx.Done():
		}
	}
}

func (c *Core) handleCancel(ctx context.Context, cm CancelOrder) CmdAck {
	v, ok := c.state.OrderVenue(cm.OrderID)
	if !ok || v != cm.Venue {
		return CmdAck{Accepted: false, Reason: "unknown order", OrderID: cm.OrderID}
	}
	o := c.state.Venue(v).Orders[cm.OrderID]
	if o.Action != nil && o.Action.Kind == ActionCancel &&
		(o.Action.Phase == ActionRequested || o.Action.Phase == ActionUnknown) {
		return CmdAck{Accepted: false, Reason: "cancel outcome is still unresolved", OrderID: o.ID}
	}
	if o.Action != nil && o.Action.Kind == ActionSubmit &&
		(o.Action.Phase == ActionRequested || o.Action.Phase == ActionUnknown) {
		return CmdAck{Accepted: false, Reason: "submit outcome is still unresolved", OrderID: o.ID}
	}
	if o.Held != nil {
		if o.Held.CancelRequested || o.Held.Phase == HeldCancelRequested {
			return CmdAck{Accepted: false, Reason: "cancel already requested", OrderID: o.ID}
		}
		switch o.Held.Phase {
		case HeldWaiting, HeldArmed, HeldPaused:
			if err := c.appendAndFold(OrderCanceled{V: o.Venue, OID: o.ID, Ts: c.now()}, SrcLocal); err != nil {
				return CmdAck{Accepted: false, Reason: "event append failed: " + err.Error(), OrderID: o.ID}
			}
			c.releaseHeld(o.ID)
			return CmdAck{Accepted: true, OrderID: o.ID}
		}
		c.requestHeldVenueCancel(ctx, o, "cancel requested")
		return CmdAck{Accepted: true, OrderID: o.ID}
	}
	b := c.brokers[cm.Venue]
	if b == nil {
		return CmdAck{Accepted: false, Reason: "unknown venue", OrderID: o.ID}
	}
	action := OrderAction{Kind: ActionCancel, Phase: ActionRequested, PreviousLimitPrice: o.LimitPrice, PreviousStopPrice: o.StopPrice}
	if err := c.appendAndFold(OrderActionChanged{V: o.Venue, OID: o.ID, Action: action, Ts: c.now()}, SrcLocal); err != nil {
		return CmdAck{Accepted: false, Reason: "event append failed: " + err.Error(), OrderID: o.ID}
	}
	c.startVenueAction(ctx, b, o.Venue, o.ID, ActionCancel, ReplaceRequest{})
	return CmdAck{Accepted: true, OrderID: cm.OrderID}
}

func (c *Core) handleReplace(ctx context.Context, cm ReplaceOrder) CmdAck {
	v, ok := c.state.OrderVenue(cm.OrderID)
	if !ok || v != cm.Venue {
		return CmdAck{Accepted: false, Reason: "unknown order", OrderID: cm.OrderID}
	}
	o := c.state.Venue(v).Orders[cm.OrderID]
	if o.Action != nil && (o.Action.Phase == ActionRequested || o.Action.Phase == ActionUnknown) {
		return CmdAck{Accepted: false, Reason: "order action outcome is still unresolved", OrderID: o.ID}
	}
	if o.Held != nil {
		if o.Held.Phase == HeldPaused || o.Held.CancelRequested || o.Held.Phase == HeldCancelRequested || o.Held.Phase == HeldUnknown {
			return CmdAck{Accepted: false, Reason: "held order is not modifiable in its current state", OrderID: o.ID}
		}
		if o.Held.Phase == HeldWaiting || o.Held.Phase == HeldArmed {
			stop := cm.StopPrice
			if stop <= 0 {
				stop = o.StopPrice
			}
			if (o.Side == SideBuy || o.Side == SideCover) && o.LimitPrice < stop || (o.Side == SideSell || o.Side == SideShort) && o.LimitPrice > stop {
				return CmdAck{Accepted: false, Reason: "stop change would put limit on the wrong side of trigger", OrderID: o.ID}
			}
			deferred := o.DeferredPositionPct > 0
			qty := cm.Qty
			if qty <= 0 && !deferred {
				qty = o.ExecutedQty + o.LeavesQty
			}
			if !deferred && qty <= o.ExecutedQty {
				return CmdAck{Accepted: false, Reason: "replacement quantity must exceed executed quantity", OrderID: o.ID}
			}
			if deferred && !c.state.PositionsReady(o.Venue) {
				return CmdAck{Accepted: false, Reason: "position data unavailable; reconcile the venue before editing this stop", OrderID: o.ID}
			}
			req := OrderRequest{Venue: o.Venue, Symbol: o.Symbol, Side: o.Side, Type: TypeStopLimit, TIF: o.TIF, Session: o.Session,
				Qty: qty, LimitPrice: o.LimitPrice, StopPrice: stop, ClientOrderID: o.ID}
			if good, reason := c.heldGate(req, o.ID); !good {
				return CmdAck{Accepted: false, Reason: reason, OrderID: o.ID}
			}
			if err := c.appendAndFold(OrderReplaced{V: o.Venue, OID: o.ID, NewQty: qty, NewStop: stop, Ts: c.now()}, SrcLocal); err != nil {
				return CmdAck{Accepted: false, Reason: "event append failed: " + err.Error(), OrderID: o.ID}
			}
			if last, ok := c.lastEligible[o.Symbol]; ok && c.freshEligiblePrint(last) && StopLimitTriggered(o.Side, last.Price, stop) {
				c.activateHeld(ctx, c.order(o.ID))
			}
			return CmdAck{Accepted: true, OrderID: o.ID}
		}
		if o.Held.Phase != HeldWorking {
			return CmdAck{Accepted: false, Reason: "held order is not modifiable in its current state", OrderID: o.ID}
		}
		if cm.Qty <= 0 {
			cm.Qty = o.ExecutedQty + o.LeavesQty
		}
		if cm.Qty <= o.ExecutedQty {
			return CmdAck{Accepted: false, Reason: "replacement quantity must exceed executed quantity", OrderID: o.ID}
		}
		cm.StopPrice = o.StopPrice // a triggered child is a LIMIT; never move its trigger.
	}
	b := c.brokers[cm.Venue]
	if b == nil {
		return CmdAck{Accepted: false, Reason: "unknown venue", OrderID: cm.OrderID}
	}
	if cm.Qty <= 0 {
		cm.Qty = o.ExecutedQty + o.LeavesQty
		if cm.Qty <= 0 {
			cm.Qty = o.Qty
		}
	}
	if cm.Qty <= o.ExecutedQty {
		return CmdAck{Accepted: false, Reason: "replacement quantity must exceed executed quantity", OrderID: o.ID}
	}
	if o.Side == SideSell && cm.Qty-o.ExecutedQty > o.LeavesQty+1e-9 {
		req := OrderRequest{Venue: o.Venue, Symbol: o.Symbol, Side: o.Side, Qty: cm.Qty}
		if good, reason := c.checkSellLeaves(req, cm.Qty-o.ExecutedQty, o.ID); !good {
			return CmdAck{Accepted: false, Reason: reason, OrderID: o.ID}
		}
	}
	if cm.LimitPrice <= 0 {
		cm.LimitPrice = o.LimitPrice
	}
	if cm.StopPrice <= 0 {
		cm.StopPrice = o.StopPrice
	}
	if cm.LimitPrice <= 0 && o.Type != TypeMarket {
		return CmdAck{Accepted: false, Reason: "replacement limit price must be positive", OrderID: o.ID}
	}
	rr := ReplaceRequest{Qty: cm.Qty, LimitPrice: cm.LimitPrice, StopPrice: cm.StopPrice}
	action := OrderAction{Kind: ActionReplace, Phase: ActionRequested, PreviousLimitPrice: o.LimitPrice,
		PreviousStopPrice: o.StopPrice, RequestedLimitPrice: cm.LimitPrice, RequestedStopPrice: cm.StopPrice, RequestedQty: cm.Qty}
	if err := c.appendAndFold(OrderActionChanged{V: o.Venue, OID: o.ID, Action: action, Ts: c.now()}, SrcLocal); err != nil {
		return CmdAck{Accepted: false, Reason: "event append failed: " + err.Error(), OrderID: o.ID}
	}
	c.startVenueAction(ctx, b, o.Venue, o.ID, ActionReplace, rr)
	return CmdAck{Accepted: true, OrderID: cm.OrderID}
}

func (c *Core) startVenueAction(ctx context.Context, b Broker, venue VenueID, orderID string, kind OrderActionKind, req ReplaceRequest) {
	go func() {
		var err error
		if kind == ActionCancel {
			err = b.CancelOrder(ctx, orderID)
		} else {
			err = b.ReplaceOrder(ctx, orderID, req)
		}
		if err == nil {
			return // only the authoritative broker order event confirms an action.
		}
		select {
		case c.bevents <- BrokerActionOutcome{V: venue, OID: orderID, Kind: kind, Reason: err.Error()}:
		case <-ctx.Done():
		}
	}()
}

func (c *Core) handleFlatten(ctx context.Context, cm Flatten) CmdAck {
	b := c.brokers[cm.Venue]
	if b == nil {
		return CmdAck{Accepted: false, Reason: "unknown venue"}
	}
	if !b.Capabilities().FlattenAll {
		return CmdAck{Accepted: false, Reason: "flatten unsupported on venue"}
	}
	if !c.state.PositionsReady(cm.Venue) {
		return CmdAck{Accepted: false, Reason: "position data unavailable; reconcile the venue before flattening"}
	}
	vs := c.state.Venue(cm.Venue)
	if vs.FlattenPending {
		return CmdAck{Accepted: false, Reason: "venue flatten is awaiting authoritative reconciliation"}
	}
	if c.state.VenueHasSellCommitments(cm.Venue) {
		return CmdAck{Accepted: false, Reason: "cancel or resolve working SELL orders before flattening"}
	}
	c.pauseHeldSellVenue(cm.Venue, "venue flatten requested; resume manually after reconciliation")
	if err := c.appendAndFold(FlattenPendingChanged{V: cm.Venue, Pending: true, Ts: c.now()}, SrcLocal); err != nil {
		return CmdAck{Accepted: false, Reason: "event append failed: " + err.Error()}
	}
	c.setPositionsReady(cm.Venue, false)
	go func() {
		if err := b.Flatten(ctx); err != nil {
			slog.Warn("exec: flatten failed", "venue", cm.Venue, "err", err)
		}
		reconcileCtx, cancel := context.WithTimeout(ctx, c.recoverSnapshotTimeout)
		defer cancel()
		account, positions, orders, err := b.Snapshot(reconcileCtx)
		if err != nil {
			slog.Warn("exec: flatten reconciliation failed", "venue", cm.Venue, "err", err)
			return
		}
		account.Venue = cm.Venue
		select {
		case c.bevents <- BrokerSnapshot{V: cm.Venue, Account: account, Positions: positions, Orders: orders}:
		case <-ctx.Done():
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
	// Kill never places orders: durably request cancellation for every working
	// order on the target venue(s), then disarm.
	// MasterArmed is global, so a venue-scoped kill still locks all trading.
	c.state.SetMasterArmed(false)
	c.disarmHeld(ctx, cm.Venue, true)
	targets := c.venues
	if cm.Venue != "" {
		targets = []VenueID{cm.Venue}
	}
	for _, v := range targets {
		ids := make([]string, 0, len(c.state.Venue(v).Orders))
		for id, o := range c.state.Venue(v).Orders {
			if o.Working() {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			c.handleCancel(ctx, CancelOrder{Venue: v, OrderID: id})
		}
	}
	c.syslog("exec.kill", "kill switch: venue="+string(cm.Venue))
	c.emitStatus()
	return CmdAck{Accepted: true}
}

func (c *Core) handleArm(ctx context.Context, on bool) CmdAck {
	c.state.SetMasterArmed(on)
	if !on {
		c.disarmHeld(ctx, "", false)
	}
	c.emitStatus()
	for _, vv := range c.venues {
		c.emitProjectedAccount(vv)
	}
	return CmdAck{Accepted: true}
}

func (c *Core) handleSetActiveVenue(cm SetActiveVenue) CmdAck {
	if cm.Venue != "" {
		if _, ok := c.state.Venues[cm.Venue]; !ok {
			return CmdAck{Accepted: false, Reason: "unknown venue"}
		}
	}
	c.state.SetActiveVenue(cm.Venue)
	if BreachedDayLoss(c.state, c.gate) && c.state.MasterArmed {
		c.state.SetMasterArmed(false)
		c.syslog("exec.autodisarm", "day-loss breach after active venue change: master disarmed")
		c.emitStatus()
	}
	return CmdAck{Accepted: true}
}

func (c *Core) emitStatus() {
	for _, v := range c.venues {
		c.emit(StatusUpdate{Venue: v, Connected: true, MasterArmed: c.state.MasterArmed})
	}
}

func (c *Core) handleBrokerEvent(ctx context.Context, be BrokerEvent) {
	switch e := be.(type) {
	case HeldActivationOutcome:
		before := c.order(e.OID)
		if before.Held == nil || before.Held.ChildClientID == "" ||
			(before.Held.Phase != HeldActivating && before.Held.Phase != HeldCancelRequested) {
			return
		}
		h := *before.Held
		h.Phase, h.PausedReason = HeldUnknown, "activation outcome unknown: "+e.Reason
		cancelNow := h.CancelRequested && !h.CancelSent
		if cancelNow {
			h.CancelSent = true
		}
		if err := c.appendAndFold(HeldOrderChanged{V: e.V, OID: e.OID, Held: h, Ts: c.now()}, SrcLocal); err != nil {
			c.syslog("exec.held", "persist activation uncertainty "+e.OID+": "+err.Error())
		}
		if cancelNow {
			if b := c.brokers[e.V]; b != nil {
				c.startVenueAction(ctx, b, e.V, e.OID, ActionCancel, ReplaceRequest{})
			}
		}
	case BrokerActionOutcome:
		o := c.order(e.OID)
		if o.Action == nil || o.Action.Kind != e.Kind || o.Action.Phase != ActionRequested {
			return
		}
		action := *o.Action
		action.Phase, action.Reason = ActionUnknown, e.Reason
		if err := c.appendAndFold(OrderActionChanged{V: e.V, OID: e.OID, Action: action, Ts: c.now()}, SrcLocal); err != nil {
			c.syslog("exec.order-action", "persist unknown outcome "+e.OID+": "+err.Error())
		}
	case BrokerSubmitOutcome:
		o := c.order(e.OID)
		if !o.Working() || o.Venue != e.V || o.Side != SideSell || o.Action == nil || o.Action.Kind != ActionSubmit ||
			(o.Action.Phase != ActionRequested && o.Action.Phase != ActionUnknown) {
			return
		}
		action := *o.Action
		if e.Accepted {
			action.Phase, action.Reason = ActionConfirmed, ""
		} else {
			action.Phase, action.Reason = ActionUnknown, e.Reason
		}
		if err := c.appendAndFold(OrderActionChanged{V: e.V, OID: e.OID, Action: action, Ts: c.now()}, SrcLocal); err != nil {
			c.syslog("exec.order-action", "persist submit outcome "+e.OID+": "+err.Error())
		}
	case Event: // order-lifecycle or StreamGap — persist + fold + emit
		before := c.order(e.OrderID())
		if err := c.appendAndFold(e, SrcWS); err != nil {
			slog.Error("exec: append broker event failed", "kind", e.Kind(), "err", err)
			return
		}
		if gap, ok := e.(StreamGap); ok {
			c.setPositionsReady(gap.V, false)
			c.pauseHeldVenue(gap.V, "broker stream gap; reconcile and resume manually")
		}
		if accepted, ok := e.(OrderAccepted); ok && before.Held != nil && before.Held.ChildClientID != "" &&
			(before.Held.Phase == HeldActivating || before.Held.Phase == HeldCancelRequested || before.Held.CancelRequested) {
			h := *before.Held
			wasCancelRequested := before.Held.CancelRequested || before.Held.Phase == HeldCancelRequested
			cancelUnknown := before.Action != nil && before.Action.Kind == ActionCancel && before.Action.Phase == ActionUnknown
			sendCancel := wasCancelRequested && (!before.Held.CancelSent || cancelUnknown)
			h.Phase, h.PausedReason, h.ChildBrokerID = HeldWorking, "", accepted.BrokerOrderID
			if wasCancelRequested {
				h.Phase, h.PausedReason, h.CancelRequested, h.CancelSent = HeldCancelRequested, "cancel requested", true, true
			}
			if err := c.appendAndFold(HeldOrderChanged{V: before.Venue, OID: before.ID, Held: h, Ts: c.now()}, SrcLocal); err != nil {
				c.syslog("exec.held", "persist child acceptance "+before.ID+": "+err.Error())
			}
			if sendCancel {
				if b := c.brokers[before.Venue]; b != nil {
					c.startVenueAction(ctx, b, before.Venue, before.ID, ActionCancel, ReplaceRequest{})
				}
			}
		}
		if id := e.OrderID(); id != "" {
			if _, rejected := e.(OrderRejected); !rejected {
				c.confirmSellSubmit(e.Venue(), id)
			}
			after := c.order(id)
			if before.Held != nil && (after.Status == StatusCanceled || after.Status == StatusRejected || after.Status == StatusExpired || after.Status == StatusFilled) {
				c.releaseHeld(id)
			}
		}
		c.maybeClearFlattenPending(e.Venue())
	case BrokerAccount:
		if b := c.brokers[e.Account.Venue]; b != nil && b.Capabilities().CalculatedDayPnL && e.Account.DayPnLSource == "" {
			previous := c.state.Venue(e.Account.Venue).Account
			e.Account.DayPnL = previous.DayPnL
			e.Account.DayPnLSource = previous.DayPnLSource
			e.Account.DayPnLProvisional = previous.DayPnLProvisional
		}
		c.state.ReconcileAccount(e.Account)
		if BreachedDayLoss(c.state, c.gate) && c.state.MasterArmed {
			c.state.SetMasterArmed(false)
			c.syslog("exec.autodisarm", "day-loss breach: master disarmed")
			c.emitStatus()
		}
		c.emitProjectedAccount(e.Account.Venue)
	case BrokerAccountFresh:
		c.state.SetAccountFresh(e.V, e.Fresh)
		if !e.Fresh && c.state.MasterArmed {
			c.state.SetMasterArmed(false)
			c.syslog("exec.autodisarm", "account data stale: master disarmed")
			c.emitStatus()
		}
	case BrokerPositions:
		previous := make(map[string]Position, len(c.state.Venue(e.V).Positions))
		for symbol, p := range c.state.Venue(e.V).Positions {
			previous[symbol] = p
		}
		c.positionOpens.reconcilePositions(e.V, e.Positions)
		c.state.ReconcilePositions(e.V, e.Positions)
		seen := make(map[string]bool, len(e.Positions))
		for _, p := range e.Positions {
			p.Venue = e.V
			seen[p.Symbol] = true
			c.emitProjectedPosition(p)
		}
		for symbol := range previous {
			if !seen[symbol] {
				c.emitProjectedPosition(Position{Venue: e.V, Symbol: symbol})
			}
		}
	case BrokerPosition:
		p := e.Position
		if p.Venue == "" || !c.state.PositionsReady(p.Venue) {
			return
		}
		c.state.ReconcilePosition(p)
		c.emitProjectedPosition(p)
		c.maybeClearFlattenPending(p.Venue)
	case BrokerPositionEffect:
		if c.applyPositionEffect(e.Venue, e.Symbol, e.Side, e.Qty, e.Price, e.ExecID) {
			position := c.state.Venue(e.Venue).Positions[e.Symbol]
			position.Venue, position.Symbol = e.Venue, e.Symbol
			c.emitProjectedPosition(position)
		}
	case BrokerExternalOrder:
		if e.Order.ID == "" {
			return
		}
		if _, known := c.state.OrderVenue(e.Order.ID); !known && !c.closed.hasSeed(e.Order.ID) && !isEngineOrderID(e.Order.ID) {
			c.state.SetExternalOrder(e.Venue, e.Order, e.Working)
		}
		c.maybeClearFlattenPending(e.Venue)
	case BrokerOpenOrders:
		local, external := make([]Order, 0, len(e.Orders)), make([]Order, 0, len(e.Orders))
		for _, o := range e.Orders {
			o.Venue = e.V
			if _, known := c.state.OrderVenue(o.ID); known || c.closed.hasSeed(o.ID) {
				local = append(local, o)
			} else if isEngineOrderID(o.ID) {
				if err := c.appendAndFold(OrderSubmitted{Order: o}, SrcReconcile); err != nil {
					c.syslog("exec.reconcile", "persist adopted order "+o.ID+": "+err.Error())
					external = append(external, o)
				}
			} else {
				external = append(external, o)
			}
		}
		c.state.ReconcileOpenOrders(e.V, local)
		c.state.ReconcileExternalOpenOrders(e.V, external)
		for _, o := range local {
			c.confirmSellSubmit(e.V, o.ID)
		}
		c.setPositionsReady(e.V, true)
		c.maybeClearFlattenPending(e.V)
	case BrokerSnapshot:
		v := e.V
		if v == "" {
			v = e.Account.Venue
		}
		acct := e.Account
		acct.Venue = v
		c.state.ReconcileAccount(acct)
		c.positionOpens.reconcilePositions(v, e.Positions)
		previous := make(map[string]Position, len(c.state.Venue(v).Positions))
		for symbol, p := range c.state.Venue(v).Positions {
			previous[symbol] = p
		}
		c.state.ReconcilePositions(v, e.Positions)
		local, external := make([]Order, 0, len(e.Orders)), make([]Order, 0, len(e.Orders))
		for _, o := range e.Orders {
			o.Venue = v
			if _, known := c.state.OrderVenue(o.ID); known || c.closed.hasSeed(o.ID) || isEngineOrderID(o.ID) {
				local = append(local, o)
				c.closed.adopt(o)
			} else {
				external = append(external, o)
			}
		}
		c.state.ReconcileOpenOrders(v, local)
		c.state.ReconcileExternalOpenOrders(v, external)
		for _, o := range local {
			c.confirmSellSubmit(v, o.ID)
		}
		for _, p := range e.Positions {
			p.Venue = v
			delete(previous, p.Symbol)
			c.emitProjectedPosition(p)
		}
		for symbol := range previous {
			c.emitProjectedPosition(Position{Venue: v, Symbol: symbol})
		}
		c.emitProjectedAccount(v)
		c.setPositionsReady(v, true)
		c.maybeClearFlattenPending(v)
	case BrokerConnUp:
		c.emit(StatusUpdate{Venue: e.V, Connected: true, MasterArmed: c.state.MasterArmed})
	case BrokerConnDown:
		c.printHealthy = false
		c.setPositionsReady(e.V, false)
		c.pauseHeldVenue(e.V, "venue connection lost; reconcile and resume manually")
		c.emit(StatusUpdate{Venue: e.V, Connected: false, MasterArmed: c.state.MasterArmed, Note: e.Note})
	}
}

func (c *Core) setPositionsReady(v VenueID, ready bool) {
	if c.state.PositionsReady(v) == ready {
		return
	}
	c.state.SetPositionsReady(v, ready)
	c.emit(PositionReadinessUpdate{Venue: v, Ready: ready})
}

func (c *Core) maybeClearFlattenPending(v VenueID) {
	if !c.state.Venue(v).FlattenPending || !c.state.FlattenReconciled(v) {
		return
	}
	if err := c.appendAndFold(FlattenPendingChanged{V: v, Pending: false, Ts: c.now()}, SrcReconcile); err != nil {
		c.syslog("exec.flatten", "persist reconciled flatten "+string(v)+": "+err.Error())
	}
}

func (c *Core) confirmSellSubmit(v VenueID, id string) {
	o := c.order(id)
	if !o.Working() || o.Venue != v || o.Side != SideSell || o.Action == nil || o.Action.Kind != ActionSubmit ||
		(o.Action.Phase != ActionRequested && o.Action.Phase != ActionUnknown) {
		return
	}
	action := *o.Action
	action.Phase, action.Reason = ActionConfirmed, ""
	if err := c.appendAndFold(OrderActionChanged{V: v, OID: id, Action: action, Ts: c.now()}, SrcReconcile); err != nil {
		c.syslog("exec.reconcile", "persist submit confirmation "+id+": "+err.Error())
	}
}
