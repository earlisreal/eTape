package exec

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type finishPerpetualArm struct {
	orderID  string
	capacity int64
	err      error
}

func (finishPerpetualArm) isCommand() {}

func (c *Core) perpetualRiskIdentity(venue VenueID) string {
	if identity := c.heldLiveIdentity[venue]; identity != "" {
		return identity
	}
	if b, ok := c.brokers[venue].(wholePositionRiskBroker); ok && !c.gate.UnvaluedLive[venue] {
		return b.AccountIdentity()
	}
	return ""
}

func (c *Core) perpetualRiskAcknowledged(venue VenueID) bool {
	if live := c.heldLiveIdentity[venue]; live != "" {
		return c.heldLiveAck[venue] == live
	}
	return !c.gate.UnvaluedLive[venue]
}

func (c *Core) handleWholePositionRiskEntry(ctx context.Context, cm SubmitPerpetualRiskEntry, rules instrument.ContractRules, limit, budget, collateral float64) CmdAck {
	opening := cm.openingTerms
	side := cm.openingLimit
	if opening.Notional == nil || side.Leverage < 1 || side.MaxLeverage < 1 {
		return CmdAck{Reason: "BingX isolated opening terms are unavailable"}
	}
	position := c.state.Venue(cm.Venue).Positions[cm.Symbol]
	if position.BrokerPositionID != opening.PositionID || position.Qty != opening.Notional.Position.Qty ||
		(position.Qty != 0) != (cm.SharedStopID != "") {
		return CmdAck{Reason: "BingX position changed; reconcile and review the chart entry"}
	}
	first := cm.Leverage
	if cm.AutoLeverage && position.Qty == 0 {
		first = rules.MinLeverage
	} else if cm.Leverage != side.Leverage {
		return CmdAck{Reason: "use the verified BingX isolated leverage for this entry"}
	}
	qty, leverage := rules.SizeNotionalRisk(*opening.Notional, budget, collateral, limit, cm.Stop,
		first, min(cm.Leverage, side.MaxLeverage), side.Leverage, side.MaxContracts, int64(cm.MaxQty), cm.Side == SideBuy)
	if qty == 0 || leverage != cm.Leverage {
		return CmdAck{Reason: "BingX size or liquidation clearance changed; review the entry"}
	}
	protectionID := cm.SharedStopID
	if protectionID == "" {
		protectionID = c.idgen.Next()
	}
	options := &PerpetualOrder{Leverage: leverage, Multiplier: rules.ContractSize(), ProtectionPrice: cm.Stop, ProtectionID: protectionID,
		RiskBudget: budget, AutoLeverage: cm.AutoLeverage, EntryCushion: &cm.Cushion, OpeningMaxQty: qty,
		LocalProtection: true, WholePosition: true, PositionID: opening.PositionID, AccountIdentity: c.perpetualRiskIdentity(cm.Venue), Notional: opening.Notional}
	req := OrderRequest{Venue: cm.Venue, Symbol: cm.Symbol, Side: cm.Side, Type: TypeStopLimit, TIF: TIFIOC, Session: SessionAuto,
		Qty: float64(qty), StopPrice: cm.Trigger, LimitPrice: limit, ClientOrderID: c.idgen.Next(), Perpetual: options}
	if err := c.validatePerpetual(&req, ""); err != nil {
		return CmdAck{Reason: err.Error()}
	}
	if ok, reason := Evaluate(c.state, c.gate, req, c.marks); !ok {
		return CmdAck{Reason: reason}
	}
	if c.heldDemand == nil {
		return CmdAck{Reason: "perpetual market demand is unavailable"}
	}
	if err := c.heldDemand.Acquire(ctx, req.ClientOrderID, req.Symbol); err != nil {
		return CmdAck{Reason: err.Error()}
	}
	o := newOrderFromRequest(req, c.now())
	o.Held = &HeldOrder{Phase: HeldPaused, DeadlineMs: c.clk.Now().Add(24 * time.Hour).UnixMilli(),
		PausedReason: "verifying BingX opening terms", PerpetualArmPending: true}
	if err := c.appendAndFold(OrderSubmitted{Order: o}, SrcLocal); err != nil {
		c.releaseHeld(o.ID)
		return CmdAck{Reason: err.Error()}
	}
	return CmdAck{OrderID: o.ID, perpetualArm: &req}
}

func (c *Core) validateWholePositionEntry(req OrderRequest, rules instrument.ContractRules) error {
	p := req.Perpetual
	venue := c.state.Venue(req.Venue)
	position := venue.Positions[req.Symbol]
	if p.Notional == nil || p.PositionID != position.BrokerPositionID || req.Qty > float64(p.OpeningMaxQty) {
		return errors.New("BingX entry identity or size changed; review the entry")
	}
	long := req.Side == SideBuy
	if position.Qty != 0 {
		stop := venue.Orders[p.ProtectionID]
		if p.PositionID == "" || stop.Symbol != req.Symbol || !stop.isPerpetualRiskStop() || !stop.Perpetual.WholePosition ||
			!editablePerpetualHeld(stop) || !c.ownsPerpetualPosition(stop, position) || stop.LeavesQty != math.Abs(position.Qty) {
			return errors.New("BingX add requires the original position and its healthy shared stop")
		}
		if long && p.ProtectionPrice < stop.StopPrice || !long && p.ProtectionPrice > stop.StopPrice {
			return errors.New("the add must keep or tighten the shared stop")
		}
	} else if p.PositionID != "" {
		return errors.New("the BingX position closed; review a new chart entry")
	}
	terms := *p.Notional
	terms.Position = instrument.RiskPosition{Qty: position.Qty, AvgPrice: position.AvgPrice,
		Margin: position.IsolatedMargin, LiquidationPrice: position.LiquidationPrice, Leverage: position.Leverage}
	if mark, ok := c.brokers[req.Venue].(PerpetualBroker).FairPrice(req.Symbol); ok {
		terms.Mark = mark
	}
	qty, _ := rules.SizeNotionalRisk(terms, p.RiskBudget, venue.Account.AvailableCash, req.LimitPrice, p.ProtectionPrice,
		p.Leverage, p.Leverage, p.Leverage, p.OpeningMaxQty, int64(req.Qty), long)
	if float64(qty) != req.Qty {
		return errors.New("BingX position, margin or liquidation clearance changed; review the entry")
	}
	return nil
}

func (c *Core) armWholePositionEntry(ctx context.Context, req OrderRequest, reply chan<- CmdAck) {
	armCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	b := c.brokers[req.Venue].(wholePositionRiskBroker)
	capacity, err := b.ArmPerpetualRisk(armCtx, req)
	reply <- c.DoContext(ctx, finishPerpetualArm{orderID: req.ClientOrderID, capacity: capacity, err: err})
}

func (c *Core) finishWholePositionArm(ctx context.Context, cm finishPerpetualArm) CmdAck {
	o := c.order(cm.orderID)
	if !o.Working() || o.Held == nil || !o.Held.PerpetualArmPending {
		return CmdAck{OrderID: cm.orderID, Reason: "the BingX entry changed while arming"}
	}
	deny := func(reason string) CmdAck {
		if err := c.appendAndFold(OrderRejected{V: o.Venue, OID: o.ID, Ts: c.now(), Reason: reason}, SrcLocal); err != nil {
			return CmdAck{OrderID: o.ID, Reason: err.Error()}
		}
		c.releaseHeld(o.ID)
		return CmdAck{OrderID: o.ID, Reason: reason}
	}
	if cm.err != nil {
		return deny(cm.err.Error())
	}
	if !c.perpetualRiskAccountFresh(o.Venue) || float64(cm.capacity) < o.Qty {
		return deny("fresh BingX account and opening capacity required")
	}
	p := *o.Perpetual
	p.OpeningMaxQty = min(p.OpeningMaxQty, cm.capacity)
	req := OrderRequest{Venue: o.Venue, Symbol: o.Symbol, Side: o.Side, Type: o.Type, TIF: o.TIF, Session: o.Session,
		Qty: o.Qty, LimitPrice: o.LimitPrice, StopPrice: o.StopPrice, ClientOrderID: o.ID, Perpetual: &p}
	if err := c.validatePerpetual(&req, o.ID); err != nil {
		return deny(err.Error())
	}
	if ok, reason := c.heldGate(req, o.ID); !ok {
		return deny(reason)
	}
	held := *o.Held
	held.Phase, held.PausedReason, held.PerpetualArmPending = HeldWaiting, "", false
	o.Held, o.Perpetual, o.UpdatedMs = &held, &p, c.now()
	if err := c.appendAndFold(RiskEntryChanged{Entry: o}, SrcLocal); err != nil {
		return CmdAck{OrderID: o.ID, Reason: err.Error()}
	}
	c.checkPerpetualEntryTrigger(ctx, o)
	return CmdAck{OrderID: o.ID, Accepted: true}
}

func (c *Core) applyPerpetualAddFill(entry Order) error {
	p := entry.Perpetual
	if p == nil || !p.WholePosition || !p.IsRiskEntry() || p.PositionID == "" || p.LateAddClose ||
		entry.ExecutedQty == 0 || entry.Held == nil || entry.Held.PerpetualAddApplied || entry.perpetualPositionID() != p.PositionID {
		return nil
	}
	stop := c.order(p.ProtectionID)
	if !stop.isPerpetualRiskStop() || stop.Venue != entry.Venue || stop.Symbol != entry.Symbol ||
		stop.Perpetual.PositionID != p.PositionID || !stop.Working() || stop.Held.PerpetualTriggered {
		return nil
	}
	if stop.Side == SideSell {
		stop.StopPrice = max(stop.StopPrice, p.ProtectionPrice)
	} else {
		stop.StopPrice = min(stop.StopPrice, p.ProtectionPrice)
	}
	held := *entry.Held
	held.PerpetualAddApplied = true
	entry.Held, entry.UpdatedMs, stop.UpdatedMs = &held, c.now(), c.now()
	if err := c.appendAndFold(RiskEntryChanged{Entry: entry, Stop: stop}, SrcReconcile); err != nil {
		return err
	}
	c.emit(OrderUpdate{Order: stop})
	return nil
}
