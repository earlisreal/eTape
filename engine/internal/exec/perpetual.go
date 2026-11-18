package exec

import (
	"context"
	"errors"
	"time"

	"github.com/earlisreal/eTape/engine/internal/instrument"
)

// PerpetualOrder carries the admitted terms and optional closing instruction.
// The core derives the multiplier from the selected adapter's catalog.
type PerpetualOrder struct {
	Leverage        int     `json:"leverage"`
	Multiplier      float64 `json:"multiplier"`
	ProtectionPrice float64 `json:"protectionPrice,omitempty"`
	ProtectionID    string  `json:"protectionId,omitempty"`
	RiskBudget      float64 `json:"riskBudget,omitempty"`
}

type SubmitPerpetualRiskEntry struct {
	Venue          VenueID
	Symbol         string
	Side           Side
	Trigger, Stop  float64
	Leverage       int
	Cushion        LimitCushion
	Budget, MaxQty float64
	CollateralPct  bool
}

func (SubmitPerpetualRiskEntry) isCommand() {}

func (c *Core) perpetualRoute(venue VenueID, symbol string) (PerpetualBroker, instrument.ContractRules, error) {
	b, ok := c.brokers[venue].(PerpetualBroker)
	if !ok || !instrument.IsPerpetual(symbol) {
		return nil, instrument.ContractRules{}, errors.New("select a perpetual Execution Venue for this exchange")
	}
	rules, ok := b.ContractRules(symbol)
	if !ok || !rules.Valid() {
		return nil, rules, errors.New("perpetual contract rules are unavailable")
	}
	return b, rules, nil
}

func (c *Core) validatePerpetual(req *OrderRequest, ownOrder string) error {
	broker, rules, err := c.perpetualRoute(req.Venue, req.Symbol)
	if err != nil {
		return err
	}
	if req.Perpetual == nil || !rules.ValidQuantity(req.Qty) ||
		(req.Type != TypeMarket && (req.Type != TypeLimit || !rules.ValidPrice(req.LimitPrice))) {
		return errors.New("use a valid contract count and market or tick-aligned limit price")
	}
	o := req.Perpetual
	if o.Multiplier != 0 && o.Multiplier != rules.ContractSize() {
		return errors.New("the contract multiplier changed; review the order")
	}
	o.Multiplier = rules.ContractSize()
	if !c.state.PositionsReady(req.Venue) {
		return errors.New("reconcile positions before trading perpetuals")
	}
	position := c.state.VenuePositionShares(req.Venue, req.Symbol)
	switch req.Side {
	case SideSell, SideCover:
		if o.ProtectionPrice != 0 || o.RiskBudget != 0 || !reducesPosition(c.state, *req) ||
			position > 0 && req.Qty > position || position < 0 && req.Qty > -position {
			return errors.New("a close must reduce the confirmed position")
		}
		return nil
	case SideBuy, SideShort:
		if !rules.TradingEnabled || o.Leverage < rules.MinLeverage || o.Leverage > rules.MaxLeverage {
			return errors.New("contract or leverage is unavailable")
		}
		if (position < 0 && req.Side == SideBuy) || (position > 0 && req.Side == SideShort) {
			return errors.New("close the existing position before reversing")
		}
		for _, order := range c.state.Venue(req.Venue).Orders {
			if order.ID != ownOrder && order.Symbol == req.Symbol && order.Working() && (order.Side == SideBuy || order.Side == SideShort) {
				return errors.New("resolve the working perpetual entry first")
			}
		}
		price, ok := broker.FairPrice(req.Symbol)
		if !ok {
			return errors.New("waiting for a fresh fair price")
		}
		if o.ProtectionPrice != 0 {
			validSide := req.Side == SideBuy && price > o.ProtectionPrice || req.Side == SideShort && price < o.ProtectionPrice
			if !rules.ValidPrice(o.ProtectionPrice) || !validSide || position != 0 {
				return errors.New("protection requires a flat symbol and a stop on the loss side of fair price")
			}
			req.TIF = TIFIOC
		}
		if o.RiskBudget > 0 {
			if req.Qty > float64(rules.SizeRisk(o.RiskBudget, c.state.Venue(req.Venue).Account.AvailableCash, req.LimitPrice, o.ProtectionPrice, o.Leverage)) {
				return errors.New("review the risk entry after margin or contract rules changed")
			}
		}
		return nil
	default:
		return errors.New("choose a perpetual direction")
	}
}

func (c *Core) handlePerpetualRiskEntry(ctx context.Context, cm SubmitPerpetualRiskEntry) CmdAck {
	deny := func(err string) CmdAck { return CmdAck{Reason: err} }
	_, rules, err := c.perpetualRoute(cm.Venue, cm.Symbol)
	if err != nil {
		return deny(err.Error())
	}
	long := cm.Side == SideBuy
	if (!long && cm.Side != SideShort) || (long && cm.Stop >= cm.Trigger) || (!long && cm.Stop <= cm.Trigger) {
		return deny("place the stop on the loss side of the entry trigger")
	}
	limit, ok := rules.EntryLimit(cm.Trigger, cm.Cushion.Value, cm.Cushion.Unit == "%", long)
	if !ok || !rules.ValidPrice(cm.Stop) || !rules.ValidQuantity(cm.MaxQty) {
		return deny("review a tick-aligned price and contract quantity")
	}
	budget := cm.Budget
	if cm.CollateralPct {
		if cm.Budget <= 0 || cm.Budget > 100 {
			return deny("enter a collateral percentage from 0 to 100")
		}
		budget = c.state.Venue(cm.Venue).Account.AvailableCash * cm.Budget / 100
	}
	size := min(cm.MaxQty, float64(rules.SizeRisk(budget, c.state.Venue(cm.Venue).Account.AvailableCash, limit, cm.Stop, cm.Leverage)))
	options := &PerpetualOrder{Leverage: cm.Leverage, Multiplier: rules.ContractSize(), ProtectionPrice: cm.Stop, ProtectionID: c.idgen.Next(), RiskBudget: budget}
	req := OrderRequest{Venue: cm.Venue, Symbol: cm.Symbol, Side: cm.Side, Type: TypeLimit, TIF: TIFIOC, Session: SessionAuto,
		Qty: size, LimitPrice: limit, ClientOrderID: c.idgen.Next(), Perpetual: options}
	if err = c.validatePerpetual(&req, ""); err != nil {
		return deny(err.Error())
	}
	if pass, reason := Evaluate(c.state, c.gate, req, c.marks); !pass {
		return deny(reason)
	}
	if c.heldDemand == nil {
		return deny("perpetual market demand is unavailable")
	}
	if err = c.heldDemand.Acquire(ctx, req.ClientOrderID, req.Symbol); err != nil {
		return deny(err.Error())
	}
	order := newOrderFromRequest(req, c.now())
	order.Type, order.StopPrice = TypeStopLimit, cm.Trigger
	order.Held = &HeldOrder{Phase: HeldWaiting, DeadlineMs: c.clk.Now().Add(24 * time.Hour).UnixMilli()}
	if err = c.appendAndFold(OrderSubmitted{Order: order}, SrcLocal); err != nil {
		c.releaseHeld(order.ID)
		return deny(err.Error())
	}
	if p, ok := c.lastEligible[order.Symbol]; ok && c.freshEligiblePrint(p) && HeldOrderTriggered(order.Type, order.Side, p.Price, order.StopPrice) {
		c.activateHeld(ctx, order)
	}
	return CmdAck{Accepted: true, OrderID: order.ID}
}
