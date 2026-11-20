package exec

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/earlisreal/eTape/engine/internal/instrument"
)

var errPerpetualFairPrice = errors.New("waiting for a fresh fair price")

// PerpetualOrder carries the admitted terms and optional closing instruction.
// The core derives the multiplier from the selected adapter's catalog.
type PerpetualOrder struct {
	Leverage        int                      `json:"leverage"`
	Multiplier      float64                  `json:"multiplier"`
	ProtectionPrice float64                  `json:"protectionPrice,omitempty"`
	ProtectionID    string                   `json:"protectionId,omitempty"`
	RiskBudget      float64                  `json:"riskBudget,omitempty"`
	AutoLeverage    bool                     `json:"autoLeverage,omitempty"`
	OpeningMaxQty   int64                    `json:"openingMaxQty,omitempty"`
	LocalProtection bool                     `json:"localProtection,omitempty"`
	WholePosition   bool                     `json:"wholePosition,omitempty"`
	LateAddClose    bool                     `json:"lateAddClose,omitempty"`
	Notional        *instrument.NotionalRisk `json:"notional,omitempty"`
	PositionID      string                   `json:"positionId,omitempty"`
	AccountIdentity string                   `json:"accountIdentity,omitempty"`
	EntryCushion    *LimitCushion            `json:"entryCushion,omitempty"`
}

func (o *PerpetualOrder) IsRiskEntry() bool {
	return o != nil && o.ProtectionID != "" && finitePositive(o.ProtectionPrice) && finitePositive(o.RiskBudget)
}

// RiskEntryCushion preserves the saved recipe. Older orders can retain their
// original absolute trigger-to-limit distance because it is already tick aligned.
func (o Order) RiskEntryCushion() LimitCushion {
	if o.Perpetual != nil && o.Perpetual.EntryCushion != nil {
		return *o.Perpetual.EntryCushion
	}
	return LimitCushion{Value: math.Abs(o.LimitPrice - o.StopPrice), Unit: "$"}
}

type SubmitPerpetualRiskEntry struct {
	Venue          VenueID
	Symbol         string
	Side           Side
	Trigger, Stop  float64
	Leverage       int
	AutoLeverage   bool
	Cushion        LimitCushion
	Budget, MaxQty float64
	CollateralPct  bool
	SharedStopID   string
	openingLimit   PerpetualOpeningLimit
	openingTerms   PerpetualOpeningTerms
}

func (SubmitPerpetualRiskEntry) isCommand() {}

// PreviewPerpetualOpening reads account terms from the configured venue
// outside the execution loop. The broker map is fixed at startup.
func (c *Core) PreviewPerpetualOpening(ctx context.Context, venue VenueID, symbol string) (PerpetualOpeningTerms, error) {
	broker, _, err := c.perpetualRoute(venue, symbol)
	if err != nil {
		return PerpetualOpeningTerms{}, err
	}
	if reader, ok := broker.(perpetualOpeningTermReader); ok {
		return reader.OpeningTerms(ctx, symbol)
	}
	return PerpetualOpeningTerms{}, errors.New("live perpetual opening terms are unavailable")
}

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
	heldEntry := req.Type == TypeStopLimit && req.Perpetual != nil && req.Perpetual.IsRiskEntry()
	validOrder := req.Type == TypeMarket || (req.Type == TypeLimit || heldEntry) && rules.ValidPrice(req.LimitPrice)
	qtyRules := rules
	if p := req.Perpetual; p != nil && p.WholePosition && p.LocalProtection && !p.IsRiskEntry() && req.Type == TypeMarket {
		qtyRules.Minimum = qtyRules.QuantityStep
	}
	if req.Perpetual == nil || !qtyRules.ValidQuantity(req.Qty) || !validOrder {
		return errors.New("use a valid contract count and market or tick-aligned limit price")
	}
	o := req.Perpetual
	caps := broker.Capabilities()
	if caps.ReconciledPerpetual && (o.ProtectionPrice != 0 || o.RiskBudget != 0 || o.ProtectionID != "" || o.AutoLeverage) {
		if !caps.EngineHeldPerpetualRiskEntry || !o.LocalProtection || !o.IsRiskEntry() {
			return errors.New("attached protection on a live perpetual requires a supported Chart Risk Entry")
		}
		identity := c.perpetualRiskIdentity(req.Venue)
		if identity == "" || o.AccountIdentity != identity || !c.perpetualRiskAcknowledged(req.Venue) {
			return errors.New("enable Stop-Limit for this live account in Orders & hotkeys")
		}
	}
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
			if o.WholePosition && order.ID == o.ProtectionID && o.PositionID != "" {
				continue // a linked add shares this stop
			}
			if order.ID != ownOrder && order.Symbol == req.Symbol && order.Working() &&
				(o.IsRiskEntry() || order.Side == SideBuy || order.Side == SideShort ||
					order.isPerpetualRiskStop() && order.Perpetual.WholePosition && order.Held.PerpetualTriggered) {
				return errors.New("resolve the working perpetual entry first")
			}
		}
		if caps.EngineHeldPerpetualRiskEntry && o.IsRiskEntry() {
			for _, order := range c.state.Venue(req.Venue).ExternalOrders {
				if order.Symbol == req.Symbol && order.Working() {
					return errors.New("resolve the working exchange order first")
				}
			}
		}
		var price float64
		if !heldEntry {
			var ok bool
			price, ok = broker.FairPrice(req.Symbol)
			if !ok {
				return errPerpetualFairPrice
			}
		}
		if o.ProtectionPrice != 0 {
			if !rules.ValidPrice(o.ProtectionPrice) || position != 0 && !o.WholePosition {
				return errors.New("protection requires a flat symbol and a tick-aligned stop")
			}
			validSide := req.Side == SideBuy && price > o.ProtectionPrice || req.Side == SideShort && price < o.ProtectionPrice
			// A pending breakout can leave the stop between fair price and the entry trigger.
			if !heldEntry && !validSide {
				return errors.New("protective stop is already crossed by the current fair price")
			}
			req.TIF = TIFIOC
		}
		if o.WholePosition {
			return c.validateWholePositionEntry(*req, rules)
		}
		if o.RiskBudget > 0 {
			if req.Qty > float64(rules.SizeRisk(o.RiskBudget, c.state.Venue(req.Venue).Account.AvailableCash, req.LimitPrice, o.ProtectionPrice, o.Leverage)) {
				return errors.New("review the risk entry after margin or contract rules changed")
			}
		}
		if o.AutoLeverage && !rules.AutoRiskAllowed(req.Qty, req.LimitPrice, o.ProtectionPrice, o.Leverage, req.Side == SideBuy) {
			return errors.New("review the risk entry after the leverage tier or liquidation buffer changed")
		}
		return nil
	default:
		return errors.New("choose a perpetual direction")
	}
}

func (c *Core) handlePerpetualRiskEntry(ctx context.Context, cm SubmitPerpetualRiskEntry) CmdAck {
	deny := func(err string) CmdAck { return CmdAck{Reason: err} }
	broker, rules, err := c.perpetualRoute(cm.Venue, cm.Symbol)
	if err != nil {
		return deny(err.Error())
	}
	caps := broker.Capabilities()
	if caps.ReconciledPerpetual && !caps.EngineHeldPerpetualRiskEntry {
		return deny("this perpetual execution venue does not support Chart Risk Entry")
	}
	if !c.perpetualRiskAccountFresh(cm.Venue) {
		return deny("fresh account data required")
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
	collateral := c.state.Venue(cm.Venue).Account.AvailableCash
	_, whole := broker.(wholePositionRiskBroker)
	if whole {
		collateral = min(collateral, cm.openingTerms.Collateral)
	}
	if cm.CollateralPct {
		if cm.Budget <= 0 || cm.Budget > 100 {
			return deny("enter a collateral percentage from 0 to 100")
		}
		budget = collateral * cm.Budget / 100
	}
	leverage := cm.Leverage
	if whole {
		return c.handleWholePositionRiskEntry(ctx, cm, rules, limit, budget, collateral)
	}
	if caps.EngineHeldPerpetualRiskEntry {
		switch {
		case cm.openingLimit.Leverage <= 0:
			return deny("MEXC isolated leverage is unavailable for this direction")
		case cm.openingLimit.Leverage != leverage:
			return deny(fmt.Sprintf("MEXC isolated leverage is %d×. Review the entry at that leverage or change it on MEXC.", cm.openingLimit.Leverage))
		case cm.openingLimit.MaxContracts < rules.Minimum:
			return deny("MEXC isolated risk tier cannot support the minimum contract size")
		}
		rules.Maximum = min(rules.Maximum, cm.openingLimit.MaxContracts)
		if cm.AutoLeverage {
			// Auto uses the reviewed account leverage on live MEXC. The
			// exchange setting is never changed by submitting the entry.
			rules.MinLeverage = leverage
		}
	}
	var size float64
	if cm.AutoLeverage {
		// The preview's leverage and quantity are ceilings. Once armed, retain
		// the admitted terms and pause if those terms become unsafe.
		qty, chosen := rules.AutoRiskSize(budget, collateral, limit, cm.Stop, leverage, int64(cm.MaxQty), long)
		if qty == 0 {
			return deny("available margin and the liquidation buffer cannot support this risk entry")
		}
		size, leverage = float64(qty), chosen
	} else {
		size = min(cm.MaxQty, float64(rules.SizeRisk(budget, collateral, limit, cm.Stop, leverage)))
	}
	options := &PerpetualOrder{Leverage: leverage, Multiplier: rules.ContractSize(), ProtectionPrice: cm.Stop, ProtectionID: c.idgen.Next(), RiskBudget: budget, AutoLeverage: cm.AutoLeverage, EntryCushion: &cm.Cushion}
	if caps.EngineHeldPerpetualRiskEntry {
		options.LocalProtection = true
		options.AccountIdentity = c.heldLiveIdentity[cm.Venue]
		options.OpeningMaxQty = cm.openingLimit.MaxContracts
	}
	req := OrderRequest{Venue: cm.Venue, Symbol: cm.Symbol, Side: cm.Side, Type: TypeStopLimit, TIF: TIFIOC, Session: SessionAuto,
		Qty: size, StopPrice: cm.Trigger, LimitPrice: limit, ClientOrderID: c.idgen.Next(), Perpetual: options}
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
	order.Held = &HeldOrder{Phase: HeldWaiting, DeadlineMs: c.clk.Now().Add(24 * time.Hour).UnixMilli()}
	if err = c.appendAndFold(OrderSubmitted{Order: order}, SrcLocal); err != nil {
		c.releaseHeld(order.ID)
		return deny(err.Error())
	}
	c.checkPerpetualEntryTrigger(ctx, order)
	return CmdAck{Accepted: true, OrderID: order.ID}
}

func (c *Core) perpetualRiskAccountFresh(venue VenueID) bool {
	ts := c.state.Venue(venue).Account.TsMs
	return riskAccountFresh(c.state, venue) && ts > 0 && c.now()-ts <= 30_000 && ts-c.now() <= 1000
}

func (c *Core) checkPerpetualEntryTrigger(ctx context.Context, order Order) {
	if p, ok := c.lastEligible[order.Symbol]; ok && p.RecvTsMs > order.Held.ResumeAfterMs &&
		c.freshEligiblePrint(p) && HeldOrderTriggered(order.Type, order.Side, p.Price, order.StopPrice) {
		c.activateHeld(ctx, order)
	}
}
