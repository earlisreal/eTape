package exec

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

type TrailSessions struct {
	Pre  bool `json:"pre"`
	RTH  bool `json:"rth"`
	Post bool `json:"post"`
}

func (s TrailSessions) allows(at time.Time) bool {
	switch session.PhaseAt(at) {
	case session.PreMarket:
		return s.Pre
	case session.RTH:
		return s.RTH
	case session.PostMarket:
		return s.Post
	default:
		return false
	}
}

func (s TrailSessions) deadline(at time.Time) time.Time {
	schedule := session.Schedule(at)
	if !schedule.TradingDay {
		return time.Time{}
	}
	if s.Post {
		return schedule.DataClose
	}
	if s.RTH {
		return schedule.Close
	}
	if s.Pre {
		return schedule.Open
	}
	return time.Time{}
}

type TrailConfig struct {
	Mode      string        `json:"mode"`
	Value     float64       `json:"value"`
	Sessions  TrailSessions `json:"sessions"`
	Pricing   string        `json:"pricing"`
	Cushion   LimitCushion  `json:"cushion"`
	QuoteSide string        `json:"quoteSide,omitempty"`
	Offset    LimitCushion  `json:"offset"`
	Timeframe string        `json:"timeframe,omitempty"`
	Buffer    LimitCushion  `json:"buffer"`
}

// TrailState belongs to the held parent and is persisted before any child POST.
type TrailState struct {
	References    map[string]TrailCandle `json:"references,omitempty"`
	PositionCycle string                 `json:"positionCycle,omitempty"`
	Floor         float64                `json:"floor,omitempty"`
	Config        TrailConfig            `json:"config"`
	Watermark     float64                `json:"watermark"`
	Stop          float64                `json:"stop"`
	Limit         float64                `json:"limit"`
	Triggered     bool                   `json:"triggered,omitempty"`
	Retired       bool                   `json:"retired,omitempty"`
	ReservedQty   float64                `json:"reservedQty,omitempty"`
	Reference     *TrailCandle           `json:"reference,omitempty"`
}

type TrailBook struct {
	Coverage   uint64
	Symbol     string
	Bid, Ask   float64
	Generation uint64
	Gap        bool
}

// Prints, books and candle evidence share one ordered, lossless execution lane.
type executionInput struct {
	Done   chan struct{}
	Print  EligiblePrint
	Book   *TrailBook
	Candle *TrailCandle
}

func (c *Core) FeedTrailBook(ctx context.Context, book TrailBook) {
	select {
	case c.printCh <- executionInput{Book: &book}:
	case <-ctx.Done():
	}
}

func (c *Core) handleTrailBook(ctx context.Context, book TrailBook) {
	if book.Generation < c.trailBookGeneration {
		return
	}
	if book.Generation > c.trailBookGeneration || book.Gap {
		clear(c.trailBooks)
	}
	c.trailBookGeneration = book.Generation
	if book.Gap {
		return
	}
	if c.trailBooks == nil {
		c.trailBooks = make(map[string]TrailBook)
	}
	c.trailBooks[book.Symbol] = book
	for _, id := range c.heldOrderIDs() {
		o := c.order(id)
		if o.Symbol == book.Symbol && o.Held.Phase == HeldWaitingQuote {
			c.activateTrailQuote(ctx, o)
		}
	}
}

type SubmitTrail struct {
	Venue         VenueID
	Symbol        string
	Side          Side
	PositionPct   float64
	Config        TrailConfig
	ClientOrderID string
}

func (SubmitTrail) isCommand() {}

func validateTrailConfig(config *TrailConfig) error {
	if config.Mode != "CANDLE" && ((config.Mode != "PRICE" && config.Mode != "PERCENT") || !finitePositive(config.Value)) {
		return fmt.Errorf("positive trail distance required")
	}
	if config.Mode == "CANDLE" {
		if config.Timeframe == "" {
			config.Timeframe = "1m"
		}
		if _, ok := session.IntradaySpanSecs(session.Timeframe(config.Timeframe)); !ok {
			return fmt.Errorf("intraday candle timeframe required")
		}
	}
	for _, amount := range []LimitCushion{config.Buffer, config.Cushion, config.Offset} {
		if math.IsNaN(amount.Value) || math.IsInf(amount.Value, 0) || amount.Unit != "" && amount.Unit != "$" && amount.Unit != "%" {
			return fmt.Errorf("invalid trail amount or unit")
		}
	}
	if config.Buffer.Value < 0 || config.Cushion.Value < 0 {
		return fmt.Errorf("nonnegative buffer and cushion required")
	}
	if config.Pricing != "CUSHION" && config.Pricing != "QUOTE" || config.Pricing == "QUOTE" && config.QuoteSide != "BID" && config.QuoteSide != "ASK" {
		return fmt.Errorf("invalid trail execution recipe")
	}
	return nil
}

func (c *Core) acquireHeldDemand(ctx context.Context, o Order) error {
	if o.Held != nil && o.Held.Trail != nil {
		if d, ok := c.heldDemand.(TrailDemandController); ok {
			return d.AcquireTrail(ctx, o.ID, o.Symbol, o.Held.Trail.Config)
		}
		if o.Held.Trail.Config.Mode == "CANDLE" || o.Held.Trail.Config.Pricing == "QUOTE" {
			return fmt.Errorf("execution trail demand unavailable")
		}
	}
	return c.heldDemand.Acquire(ctx, o.ID, o.Symbol)
}

func (c *Core) handleSubmitTrail(ctx context.Context, cm SubmitTrail) CmdAck {
	blocked := func(reason string) CmdAck { return CmdAck{Reason: reason, OrderID: cm.ClientOrderID} }
	if c.brokers[cm.Venue] == nil || cm.Symbol == "" {
		return blocked("unknown venue or missing symbol")
	}
	if cm.Side != SideSell && cm.Side != SideCover {
		return blocked("trail requires a reducing SELL or COVER")
	}
	if !c.state.PositionsReady(cm.Venue) {
		return blocked("position data unavailable")
	}
	if !finitePositive(cm.PositionPct) || cm.PositionPct > 100 {
		return blocked("position percentage must be in (0,100]")
	}
	if err := validateTrailConfig(&cm.Config); err != nil {
		return blocked(err.Error())
	}
	deadline := cm.Config.Sessions.deadline(c.clk.Now())
	if deadline.IsZero() || !deadline.After(c.clk.Now()) {
		return blocked("no remaining eligible session")
	}
	if identity := c.heldLiveIdentity[cm.Venue]; identity != "" && c.heldLiveAck[cm.Venue] != identity {
		return blocked("engine-held stop-limit acknowledgement required")
	}
	for _, o := range c.state.Venue(cm.Venue).Orders {
		if o.Symbol == cm.Symbol && o.Working() && (o.RiskEntryID != "" || o.Held != nil && o.Held.Trail != nil) {
			return blocked("position already has managed protection; cancel it before placing a new trail")
		}
	}
	shares := c.state.VenuePositionShares(cm.Venue, cm.Symbol)
	qty := math.Floor(math.Abs(shares) * cm.PositionPct / 100)
	req := OrderRequest{Venue: cm.Venue, Symbol: cm.Symbol, Side: cm.Side, Qty: qty, Type: TypeLimit, TIF: TIFDay, Session: SessionExtended}
	if ok, reason := c.checkSellLeaves(req, qty, ""); !ok {
		return blocked(reason)
	}
	if qty <= 0 {
		return blocked("position percentage rounds to zero shares")
	}
	admissionState := *c.state
	admissionState.MasterArmed = true
	admissionGate := c.gate
	admissionGate.Global.MaxDayLoss = 0
	admissionGate.AccountRequired = nil
	if ok, why := EvaluateDeferredAdmission(&admissionState, admissionGate, req); !ok {
		return blocked(why)
	}
	if cm.ClientOrderID == "" {
		cm.ClientOrderID = c.idgen.Next()
	}
	if _, exists := c.state.OrderVenue(cm.ClientOrderID); exists {
		return blocked("duplicate order identity")
	}
	if c.heldDemand == nil {
		return blocked("execution demand unavailable")
	}
	var demandErr error
	if d, ok := c.heldDemand.(TrailDemandController); ok {
		demandErr = d.AcquireTrail(ctx, cm.ClientOrderID, cm.Symbol, cm.Config)
	} else if cm.Config.Pricing == "QUOTE" || cm.Config.Mode == "CANDLE" {
		return blocked("execution Book demand unavailable")
	} else {
		demandErr = c.heldDemand.Acquire(ctx, cm.ClientOrderID, cm.Symbol)
	}
	if demandErr != nil {
		return blocked("execution market data unavailable: " + demandErr.Error())
	}
	if deadline.UnixMilli() <= c.now() {
		c.releaseHeld(cm.ClientOrderID)
		return blocked("trail session expired while acquiring market data")
	}
	o := newOrderFromRequest(OrderRequest{Venue: cm.Venue, Symbol: cm.Symbol, Side: cm.Side, Type: TypeTrailingStop, TIF: TIFDay, Session: SessionExtended, ClientOrderID: cm.ClientOrderID}, c.now())
	o.DeferredPositionPct = cm.PositionPct
	o.Held = &HeldOrder{Phase: HeldWaiting, DeadlineMs: deadline.UnixMilli(), PausedReason: "waiting for market data", Trail: &TrailState{Config: cm.Config, PositionCycle: c.trailPositionCycle(cm.Venue, cm.Symbol)}}
	if err := c.appendAndFold(OrderSubmitted{Order: o}, SrcLocal); err != nil {
		c.releaseHeld(o.ID)
		return blocked(err.Error())
	}
	if p, ok := c.lastEligible[o.Symbol]; ok && c.printHealthy && c.freshEligiblePrint(p) {
		c.advanceTrail(ctx, o, p, true)
	}
	return CmdAck{Accepted: true, OrderID: o.ID}
}

func (c *Core) saveTrail(o Order, trail TrailState, phase HeldPhase, reason string) error {
	h := *o.Held
	h.Trail, h.Phase, h.PausedReason = &trail, phase, reason
	return c.appendAndFold(HeldOrderChanged{V: o.Venue, OID: o.ID, Held: h, Ts: c.now()}, SrcLocal)
}

func (c *Core) advanceTrail(ctx context.Context, o Order, p EligiblePrint, deliberate bool) {
	t := *o.Held.Trail
	if o.RiskEntryID != "" && o.Qty <= 0 {
		return
	}
	if t.PositionCycle == "" {
		t.PositionCycle = c.trailPositionCycle(o.Venue, o.Symbol)
	}
	if t.PositionCycle == "" || t.PositionCycle != c.trailPositionCycle(o.Venue, o.Symbol) {
		c.retireTrail(ctx, o, "position cycle changed")
		return
	}
	if t.Retired || t.Triggered || !t.Config.Sessions.allows(c.clk.Now()) || !t.Config.Sessions.allows(time.UnixMilli(p.TsMs)) {
		return
	}
	if t.Watermark == 0 || o.Side == SideSell && p.Price > t.Watermark || o.Side == SideCover && p.Price < t.Watermark {
		t.Watermark = p.Price
	}
	distance := t.Config.Value
	if t.Config.Mode == "PERCENT" {
		distance = t.Watermark * distance / 100
	}
	candidate := t.Watermark - distance
	if o.Side == SideCover {
		candidate = t.Watermark + distance
	}
	if t.Config.Mode == "CANDLE" {
		latest := c.trailCandles[o.Symbol+"/"+t.Config.Timeframe]
		if !deliberate && latest.afterSeq >= p.Seq && (t.Reference == nil || latest.StartMs > t.Reference.StartMs) {
			candidate = t.Stop
		} else {
			candidate = c.candleTrailCandidate(o.Symbol, o.Side, &t)
		}
		if candidate == 0 && t.Stop == 0 {
			if err := c.saveTrail(o, t, HeldWaiting, "waiting for a verified completed candle"); err != nil {
				c.syslog("exec.trail", err.Error())
			}
			return
		}
	}
	candidate = roundTrailPrice(candidate, o.Side)
	if o.Side == SideCover {
		if t.Stop == 0 || candidate < t.Stop {
			t.Stop = candidate
		}
	} else {
		t.Stop = math.Max(t.Stop, candidate)
	}
	if !finitePositive(t.Stop) {
		c.rejectTrail(ctx, o, "invalid derived trailing price")
		return
	}
	if t.Config.Pricing == "CUSHION" {
		limit, err := riskLimit(t.Stop, t.Config.Cushion, o.Side == SideCover)
		if err != nil {
			c.rejectTrail(ctx, o, err.Error())
			return
		}
		t.Limit = limit
	}
	t.Triggered = StopLimitTriggered(o.Side, p.Price, t.Stop)
	phase, reason := HeldArmed, ""
	if t.Config.Mode == "CANDLE" && t.Reference == nil && t.Floor > 0 {
		reason = "fixed fallback; waiting for a verified completed candle"
	}
	if t.Triggered {
		t.ReservedQty = o.LeavesQty
		if o.DeferredPositionPct > 0 {
			t.ReservedQty = math.Floor(math.Abs(c.state.VenuePositionShares(o.Venue, o.Symbol)) * o.DeferredPositionPct / 100)
		}
		if t.ReservedQty <= 0 {
			c.rejectTrail(ctx, o, "position percentage rounds to zero shares")
			return
		}
		if ok, why := c.checkSellLeaves(OrderRequest{Venue: o.Venue, Symbol: o.Symbol, Side: o.Side}, t.ReservedQty, o.ID); !ok {
			c.rejectTrail(ctx, o, why)
			return
		}
		if t.Config.Pricing == "QUOTE" {
			phase, reason = HeldWaitingQuote, "waiting for a valid execution quote"
		}
	}
	if err := c.saveTrail(o, t, phase, reason); err != nil {
		c.syslog("exec.trail", err.Error())
		return
	}
	if t.Triggered {
		if o.RiskEntryID != "" && !c.beginRiskExit(ctx, c.riskOwner(o)) {
			return
		}
		if t.Config.Pricing == "QUOTE" {
			c.activateTrailQuote(ctx, c.order(o.ID))
		} else {
			c.activateHeld(ctx, c.order(o.ID))
		}
	}
}

func (c *Core) activateTrailQuote(ctx context.Context, o Order) {
	if o.Held == nil || o.Held.Trail == nil || o.Held.Trail.Retired || o.Held.Phase != HeldWaitingQuote {
		return
	}
	t := *o.Held.Trail
	if !t.Config.Sessions.allows(c.clk.Now()) || o.Held.DeadlineMs <= c.now() {
		return
	}
	b, ok := c.trailBooks[o.Symbol]
	if d, verified := c.heldDemand.(BookCoverageController); verified {
		coverage := d.TrailBookCoverage(o.Symbol)
		if coverage == 0 || b.Coverage != coverage {
			return
		}
	}
	if !ok || b.Generation == 0 || !finitePositive(b.Bid) || !finitePositive(b.Ask) || b.Bid > b.Ask {
		return
	}
	base := b.Bid
	if t.Config.QuoteSide == "ASK" {
		base = b.Ask
	}
	distance := t.Config.Offset.Value
	if t.Config.Offset.Unit == "%" {
		distance = base * distance / 100
	}
	price := base + distance
	if !finitePositive(price) {
		c.rejectTrail(ctx, o, "invalid quote-derived limit")
		return
	}
	t.Limit = roundTrailPrice(price, o.Side)
	if err := c.saveTrail(o, t, HeldWaitingQuote, ""); err != nil {
		c.syslog("exec.trail", err.Error())
		return
	}
	c.activateHeld(ctx, c.order(o.ID))
}

func roundTrailPrice(price float64, side Side) float64 {
	tick := .0001
	if price >= 1 {
		tick = .01
	}
	if side == SideCover {
		price = math.Ceil(price/tick-1e-9) * tick
	} else {
		price = math.Floor(price/tick+1e-9) * tick
	}
	return math.Round(price*10000) / 10000
}

func (c *Core) rejectTrail(ctx context.Context, o Order, reason string) {
	if err := c.appendAndFold(OrderRejected{V: o.Venue, OID: o.ID, Reason: fmt.Sprintf("trailing protection: %s", reason), Ts: c.now()}, SrcLocal); err != nil {
		c.syslog("exec.trail", err.Error())
		return
	}
	c.releaseHeld(o.ID)
	if entry := c.riskOwner(o); entry.RiskEntry != nil {
		c.failRisk(ctx, entry, "trailing protection rejected: "+reason)
	}
}

func (c *Core) checkTrailPositions(ctx context.Context) {
	c.syncTrailPositionCycles()
	for _, id := range c.heldOrderIDs() {
		o := c.order(id)
		if o.Held.Trail == nil || o.Held.Trail.Retired {
			continue
		}
		qty := c.state.VenuePositionShares(o.Venue, o.Symbol)
		if !c.state.PositionsReady(o.Venue) {
			c.retireTrail(ctx, o, "position continuity lost")
		} else if entry := c.riskOwner(o); entry.RiskEntry != nil && entry.ExecutedQty == 0 && entry.Working() {
			continue
		} else if o.Side == SideSell && qty <= 0 || o.Side == SideCover && qty >= 0 {
			c.retireTrail(ctx, o, "position closed or reversed")
		} else if o.Held.Trail.PositionCycle != "" && o.Held.Trail.PositionCycle != c.trailPositionCycle(o.Venue, o.Symbol) {
			c.retireTrail(ctx, o, "position cycle changed")
		}
	}
}

func (c *Core) retireTrail(ctx context.Context, o Order, reason string) {
	t := *o.Held.Trail
	t.Retired = true
	if err := c.saveTrail(o, t, o.Held.Phase, reason); err != nil {
		c.syslog("exec.trail", err.Error())
		return
	}
	o = c.order(o.ID)
	if entry := c.riskOwner(o); entry.RiskEntry != nil && entry.Working() {
		_ = c.handleCancel(ctx, CancelOrder{Venue: entry.Venue, OrderID: entry.ID})
	}
	if o.Held.ChildClientID != "" {
		c.requestHeldVenueCancel(ctx, o, reason)
		return
	}
	if err := c.appendAndFold(OrderCanceled{V: o.Venue, OID: o.ID, Ts: c.now()}, SrcLocal); err != nil {
		c.syslog("exec.trail", err.Error())
		return
	}
	c.releaseHeld(o.ID)
}

func (c *Core) recoverTrail(ctx context.Context, o Order, found bool) {
	if o.Held.ChildClientID == "" {
		c.retireTrail(ctx, o, "prior engine run expired")
		return
	}
	t := *o.Held.Trail
	t.Retired = true
	phase := HeldUnknown
	if found {
		phase = HeldWorking
	}
	if err := c.saveTrail(o, t, phase, "prior engine run; reconcile child cancellation"); err != nil {
		c.syslog("exec.trail", err.Error())
		return
	}
	o = c.order(o.ID)
	if found {
		c.requestHeldVenueCancel(ctx, o, "prior engine run expired")
		return
	}
	action := OrderAction{Kind: ActionCancel, Phase: ActionUnknown, Reason: "prior engine run; broker child outcome unknown"}
	if err := c.appendAndFold(OrderActionChanged{V: o.Venue, OID: o.ID, Action: action, Ts: c.now()}, SrcReconcile); err != nil {
		c.syslog("exec.trail", err.Error())
	}
}
