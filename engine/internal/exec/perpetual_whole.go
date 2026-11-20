package exec

import (
	"context"
	"errors"
	"math"

	"github.com/earlisreal/eTape/engine/internal/instrument"
)

func (c *Core) perpetualRiskIdentity(venue VenueID) string {
	if identity := c.heldLiveIdentity[venue]; identity != "" {
		return identity
	}
	if b, ok := c.brokers[venue].(interface{ AccountIdentity() string }); ok &&
		c.brokers[venue].Capabilities().WholePositionPerpetualRiskEntry && !c.gate.UnvaluedLive[venue] {
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

func (c *Core) handleWholePositionRiskEntry(ctx context.Context, cm SubmitPerpetualRiskEntry, rules instrument.ContractRules, limit, budget, collateral float64, opening PerpetualOpeningTerms, side PerpetualOpeningLimit) CmdAck {
	if opening.Notional == nil || side.Leverage < 1 || side.MaxLeverage < 1 {
		return CmdAck{Reason: "BingX isolated opening terms are unavailable"}
	}
	position := c.state.Venue(cm.Venue).Positions[cm.Symbol]
	if position.BrokerPositionID != opening.PositionID || position.Qty != opening.Notional.Position.Qty ||
		(position.Qty != 0) != (cm.SharedStopID != "") {
		return CmdAck{Reason: "BingX position changed; reconcile and review the chart entry"}
	}
	if cm.Leverage != side.Leverage || cm.Leverage > side.MaxLeverage {
		return CmdAck{Reason: "use the verified BingX isolated leverage for this entry"}
	}
	qty, leverage := rules.SizeNotionalRisk(*opening.Notional, budget, collateral, limit, cm.Stop,
		cm.Leverage, cm.Leverage, side.Leverage, side.MaxContracts, int64(cm.MaxQty), cm.Side == SideBuy)
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
	req := OrderRequest{Venue: cm.Venue, Symbol: cm.Symbol, Side: cm.Side, Type: TypeStopLimit, TIF: TIFGTC, Session: SessionAuto,
		Qty: float64(qty), StopPrice: cm.Trigger, LimitPrice: limit, ClientOrderID: c.idgen.Next(), Perpetual: options}
	if err := c.validatePerpetual(&req, ""); err != nil {
		return CmdAck{Reason: err.Error()}
	}
	if ok, reason := Evaluate(c.state, c.gate, req, c.marks); !ok {
		return CmdAck{Reason: reason}
	}
	return c.armPerpetualEntry(ctx, req)
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
