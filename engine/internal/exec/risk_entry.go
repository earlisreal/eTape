package exec

import (
	"context"
	"fmt"
	"math"

	"github.com/earlisreal/eTape/engine/internal/session"
)

type LimitCushion struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// RiskEntry belongs to the entry; exits point back through RiskEntryID.
type RiskEntry struct {
	StopID             string       `json:"stopId"`
	Budget             float64      `json:"budget"`
	Mode               string       `json:"mode"`
	BuyCushion         LimitCushion `json:"buyCushion"`
	SellCushion        LimitCushion `json:"sellCushion"`
	Closing            bool         `json:"closing,omitempty"`
	ProtectionCanceled bool         `json:"protectionCanceled,omitempty"`
	Failure            string       `json:"failure,omitempty"`
}

type SubmitRiskEntry struct {
	Venue                   VenueID
	Symbol                  string
	BuyStop, SellStop       float64
	Mode                    string
	Value, MaxQty           float64
	BuyCushion, SellCushion LimitCushion
}

func (SubmitRiskEntry) isCommand() {}

// One log append admits/edits both local legs before either can activate.
type RiskEntryChanged struct{ Entry, Stop Order }

func (RiskEntryChanged) isExecEvent()      {}
func (RiskEntryChanged) Kind() string      { return "risk_entry_changed" }
func (e RiskEntryChanged) Venue() VenueID  { return e.Entry.Venue }
func (e RiskEntryChanged) OrderID() string { return e.Entry.ID }
func (e RiskEntryChanged) TsMs() int64     { return e.Entry.UpdatedMs }

func riskLimit(stop float64, cushion LimitCushion, buy bool) (float64, error) {
	if !validLimitIfTouchedPrice(stop) || math.IsNaN(cushion.Value) || math.IsInf(cushion.Value, 0) || cushion.Value < 0 ||
		(cushion.Unit != "" && cushion.Unit != "$" && cushion.Unit != "%") {
		return 0, fmt.Errorf("invalid trigger or Limit Cushion")
	}
	distance := cushion.Value
	if cushion.Unit == "%" {
		distance = stop * cushion.Value / 100
	}
	price := stop - distance
	if buy {
		price = stop + distance
	}
	tick := 0.0001
	if price >= 1 {
		tick = 0.01
	}
	units := price / tick
	if buy {
		price = math.Ceil(units-1e-9) * tick
	} else {
		price = math.Floor(units+1e-9) * tick
	}
	if !validLimitIfTouchedPrice(price) {
		return 0, fmt.Errorf("invalid execution limit")
	}
	return price, nil
}

func riskShares(budget, buyLimit, sellLimit, funds float64) float64 {
	distance := math.Round((buyLimit-sellLimit)*10000) / 10000
	if distance <= 0 || budget <= 0 || funds <= 0 {
		return 0
	}
	return math.Min(math.Floor(budget/distance+1e-9), math.Floor(funds/buyLimit+1e-9))
}

func (c *Core) handleRiskEntry(ctx context.Context, cm SubmitRiskEntry) CmdAck {
	blocked := func(reason string) CmdAck { return CmdAck{Reason: reason} }
	if c.brokers[cm.Venue] == nil || cm.Symbol == "" {
		return blocked("missing symbol or execution venue")
	}
	phase := session.PhaseAt(c.clk.Now())
	if phase != session.PreMarket && phase != session.RTH && phase != session.PostMarket {
		return blocked("risk entry is unavailable outside PRE/RTH/POST")
	}
	if !c.state.PositionsReady(cm.Venue) || c.state.Venue(cm.Venue).FlattenPending {
		return blocked("position data unavailable or flatten pending")
	}
	vs := c.state.Venue(cm.Venue)
	if vs.Positions[cm.Symbol].Qty != 0 {
		return blocked("risk entry requires a flat symbol")
	}
	for _, orders := range []map[string]Order{vs.Orders, vs.ExternalOrders} {
		for _, o := range orders {
			if o.Symbol == cm.Symbol && o.Working() {
				return blocked("risk entry requires no other working orders")
			}
		}
	}
	account := vs.Account
	if !riskAccountFresh(c.state, cm.Venue) || account.TsMs <= 0 || c.now()-account.TsMs > 30_000 || account.TsMs > c.now()+1000 {
		return blocked("fresh account data required")
	}
	if !c.printHealthy {
		return blocked("fresh eligible market data required")
	}
	last, hasLast := c.lastEligible[cm.Symbol]
	if !hasLast || !c.freshEligiblePrint(last) {
		return blocked("fresh eligible market data required")
	}
	if identity := c.heldLiveIdentity[cm.Venue]; identity != "" && c.heldLiveAck[cm.Venue] != identity {
		return blocked("live engine-held stop-limit requires account acknowledgement")
	}
	if math.IsNaN(cm.Value) || math.IsInf(cm.Value, 0) || cm.Value <= 0 || !finitePositive(cm.MaxQty) {
		return blocked("positive risk value and preview quantity required")
	}
	budget, funds := cm.Value, account.BuyingPower
	switch cm.Mode {
	case "Dollar":
	case "CashPct":
		budget = account.AvailableCash * cm.Value / 100
		funds = account.AvailableCash
	case "BuyingPowerPct":
		budget = account.BuyingPower * cm.Value / 100
	default:
		return blocked("invalid risk mode")
	}
	if cm.Mode != "Dollar" && cm.Value > 100 {
		return blocked("risk percentage must be at most 100")
	}
	buyLimit, err := riskLimit(cm.BuyStop, cm.BuyCushion, true)
	if err != nil {
		return blocked(err.Error())
	}
	sellLimit, err := riskLimit(cm.SellStop, cm.SellCushion, false)
	if err != nil || cm.SellStop >= cm.BuyStop {
		return blocked("sell trigger must be below buy trigger with valid execution limits")
	}
	qty := math.Min(riskShares(budget, buyLimit, sellLimit, funds), math.Floor(cm.MaxQty))
	if !finitePositive(qty) {
		return blocked("risk or funding budget rounds to zero shares")
	}
	req := OrderRequest{Venue: cm.Venue, Symbol: cm.Symbol, Side: SideBuy, Type: TypeStopLimit, TIF: TIFDay, Session: SessionExtended, Qty: qty, LimitPrice: buyLimit, StopPrice: cm.BuyStop, ClientOrderID: c.idgen.Next()}
	if good, why := Evaluate(c.state, c.gate, req, c.marks); !good {
		return blocked(why)
	}
	if cap := c.gate.Venue[cm.Venue].MaxOpenOrders; cap > 0 && workingCount(vs, cm.Symbol)+2 > cap {
		return blocked("insufficient open-order capacity for pair")
	}
	if c.heldDemand == nil {
		return blocked("execution ticker demand unavailable")
	}
	entry := newOrderFromRequest(req, c.now())
	stopReq := req
	stopReq.Side = SideSell
	stopReq.Qty = 0
	stopReq.LimitPrice = sellLimit
	stopReq.StopPrice = cm.SellStop
	stopReq.ClientOrderID = c.idgen.Next()
	stop := newOrderFromRequest(stopReq, c.now())
	deadline := session.Schedule(c.clk.Now()).DataClose.UnixMilli()
	entry.Held = &HeldOrder{Phase: HeldWaiting, DeadlineMs: deadline}
	stop.Held = &HeldOrder{Phase: HeldWaiting, DeadlineMs: deadline, PausedReason: "awaiting entry fills"}
	entry.RiskEntry = &RiskEntry{StopID: stop.ID, Budget: budget, Mode: cm.Mode, BuyCushion: cm.BuyCushion, SellCushion: cm.SellCushion}
	stop.RiskEntryID = entry.ID
	for _, o := range []Order{entry, stop} {
		if err := c.heldDemand.Acquire(ctx, o.ID, o.Symbol); err != nil {
			c.releaseHeld(entry.ID)
			c.releaseHeld(stop.ID)
			return blocked("execution ticker unavailable: " + err.Error())
		}
	}
	if err := c.appendAndFold(RiskEntryChanged{Entry: entry, Stop: stop}, SrcLocal); err != nil {
		c.releaseHeld(entry.ID)
		c.releaseHeld(stop.ID)
		return blocked("event append failed: " + err.Error())
	}
	if StopLimitTriggered(SideBuy, last.Price, entry.StopPrice) {
		c.activateHeld(ctx, entry)
	}
	return CmdAck{Accepted: true, OrderID: entry.ID}
}

func finitePositive(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func (c *Core) riskOwner(o Order) Order {
	if o.RiskEntry != nil {
		return o
	}
	if o.RiskEntryID != "" {
		return c.order(o.RiskEntryID)
	}
	return Order{}
}

func (c *Core) syncRiskVenue(ctx context.Context, venue VenueID) {
	for _, vs := range c.state.Venues {
		for _, entry := range vs.Orders {
			if entry.RiskEntry != nil && (venue == "" || entry.Venue == venue) {
				c.syncRiskProtection(ctx, entry, true)
			}
		}
	}
}

func (c *Core) persistRisk(entry Order, risk RiskEntry) error {
	entry.RiskEntry = &risk
	entry.UpdatedMs = c.now()
	return c.appendAndFold(RiskEntryChanged{Entry: entry, Stop: c.order(risk.StopID)}, SrcLocal)
}

// Protection is scoped to confirmed fills from this entry, never the whole position.
func (c *Core) syncRiskProtection(ctx context.Context, entry Order, reconciling bool) {
	if entry.RiskEntry == nil {
		return
	}
	risk := *entry.RiskEntry
	stop := c.order(risk.StopID)
	if risk.ProtectionCanceled || stop.Held == nil || stop.Held.DeadlineMs <= c.now() {
		return
	}
	if !risk.Closing {
		if stop.Held.ChildClientID != "" {
			return
		}
		stop.Qty, stop.LeavesQty = entry.ExecutedQty, entry.ExecutedQty
		stop.UpdatedMs = c.now()
		if stop.Qty == 0 {
			stop.Held = &HeldOrder{Phase: stop.Held.Phase, DeadlineMs: stop.Held.DeadlineMs, ResumeAfterMs: stop.Held.ResumeAfterMs, PausedReason: "awaiting entry fills"}
		}
		if !entry.Working() && entry.ExecutedQty == 0 {
			stop.Status = StatusCanceled
			c.releaseHeld(stop.ID)
		}
		if err := c.appendAndFold(RiskEntryChanged{Entry: entry, Stop: stop}, SrcLocal); err != nil {
			c.syslog("exec.risk", err.Error())
			return
		}
		if stop.Qty > 0 && (stop.Held.Phase == HeldWaiting || stop.Held.Phase == HeldArmed) {
			if last, ok := c.lastEligible[stop.Symbol]; ok && c.freshEligiblePrint(last) && StopLimitTriggered(SideSell, last.Price, stop.StopPrice) {
				c.activateHeld(ctx, stop)
			}
		}
		return
	}
	covered := 0.0
	for _, exit := range c.state.Venue(entry.Venue).Orders {
		if exit.RiskEntryID != entry.ID {
			continue
		}
		covered += exit.ExecutedQty
		if exit.Working() {
			covered += exit.LeavesQty
		}
	}
	qty := entry.ExecutedQty - covered
	if qty <= 1e-9 || risk.Failure != "" {
		return
	}
	// Late fills get their own durable child; never resize an uncertain/working exit.
	late := newOrderFromRequest(OrderRequest{Venue: entry.Venue, Symbol: entry.Symbol, Side: SideSell, Type: TypeStopLimit, TIF: TIFDay, Session: SessionExtended,
		Qty: qty, LimitPrice: stop.LimitPrice, StopPrice: stop.StopPrice, ClientOrderID: c.idgen.Next()}, c.now())
	late.RiskEntryID = entry.ID
	late.Held = &HeldOrder{Phase: HeldWaiting, DeadlineMs: stop.Held.DeadlineMs}
	if reconciling || stop.Held.Phase == HeldPaused || !c.printHealthy || !c.state.PositionsReady(entry.Venue) {
		late.Held.Phase = HeldPaused
		late.Held.PausedReason = "late entry fill; reconcile and resume protection"
	}
	if c.heldDemand == nil {
		return
	}
	if err := c.heldDemand.Acquire(ctx, late.ID, late.Symbol); err != nil {
		c.failRisk(ctx, entry, "late fill protection unavailable: "+err.Error())
		return
	}
	if err := c.appendAndFold(OrderSubmitted{Order: late}, SrcLocal); err != nil {
		c.releaseHeld(late.ID)
		c.failRisk(ctx, entry, "late fill protection persistence failed: "+err.Error())
		return
	}
	if late.Held.Phase == HeldWaiting {
		c.activateHeld(ctx, late)
	}
}

func (c *Core) failRisk(ctx context.Context, entry Order, reason string) {
	if entry.RiskEntry == nil {
		return
	}
	risk := *entry.RiskEntry
	risk.Failure = reason
	if err := c.persistRisk(entry, risk); err != nil {
		c.syslog("exec.risk", err.Error())
		return
	}
	if entry.Working() {
		_ = c.handleCancel(ctx, CancelOrder{Venue: entry.Venue, OrderID: entry.ID})
	}
}

func (c *Core) riskBrokerEvent(ctx context.Context, id string) {
	o := c.order(id)
	entry := c.riskOwner(o)
	if entry.RiskEntry == nil {
		return
	}
	if o.RiskEntryID != "" && (o.Status == StatusRejected || o.Status == StatusCanceled && !entry.RiskEntry.ProtectionCanceled || o.Held != nil && o.Held.Phase == HeldUnknown || o.Action != nil && (o.Action.Phase == ActionUnknown || o.Action.Phase == ActionFailed)) {
		c.failRisk(ctx, entry, "protection interrupted; verify "+o.ID)
	}
	c.syncRiskProtection(ctx, c.order(entry.ID), false)
}

func (c *Core) cancelRiskProtection(ctx context.Context, entry Order) error {
	if entry.RiskEntry == nil {
		return nil
	}
	risk := *entry.RiskEntry
	risk.ProtectionCanceled = true
	if err := c.persistRisk(entry, risk); err != nil {
		return err
	}
	if entry.Working() {
		if entry.Held.ChildClientID == "" {
			_ = c.handleCancel(ctx, CancelOrder{Venue: entry.Venue, OrderID: entry.ID})
		} else {
			c.requestHeldVenueCancel(ctx, entry, "protection canceled")
		}
	}
	for _, exit := range c.state.Venue(entry.Venue).Orders {
		if exit.RiskEntryID == entry.ID && exit.Working() {
			_ = c.handleCancel(ctx, CancelOrder{Venue: exit.Venue, OrderID: exit.ID})
		}
	}
	return nil
}

func (c *Core) replaceRisk(ctx context.Context, cm ReplaceOrder, o Order) CmdAck {
	blocked := func(reason string) CmdAck { return CmdAck{Reason: reason, OrderID: o.ID} }
	entry := c.riskOwner(o)
	if entry.RiskEntry == nil || o.Held == nil || !o.Working() {
		return blocked("linked order unavailable")
	}
	entryPhase := "WAITING"
	if entry.Held.ChildClientID != "" {
		entryPhase = "ACTIVATED"
	}
	if cm.ExpectedRiskEntryPhase != entryPhase {
		return blocked("entry activated or changed during drag; review current risk sizing")
	}
	pretrigger := o.Held.Phase == HeldWaiting || o.Held.Phase == HeldArmed
	expectedPre := cm.ExpectedHeldPhase == HeldWaiting || cm.ExpectedHeldPhase == HeldArmed
	if cm.ExpectedHeldPhase == "" || pretrigger != expectedPre || !pretrigger && cm.ExpectedHeldPhase != o.Held.Phase {
		return blocked("order activated or changed during drag; review current order")
	}
	if o.Held.CancelRequested || o.Held.Phase == HeldPaused || o.Held.Phase == HeldUnknown || o.Action != nil && (o.Action.Phase == ActionRequested || o.Action.Phase == ActionUnknown) {
		return blocked("linked order action unresolved or paused")
	}
	if !pretrigger {
		if o.Held.Phase != HeldWorking || !validLimitIfTouchedPrice(cm.LimitPrice) {
			return blocked("invalid working limit modification")
		}
		if cm.Qty != 0 && cm.Qty != o.Qty {
			return blocked("linked quantity locks at buy activation")
		}
		if o.Side == SideBuy {
			req := OrderRequest{Venue: o.Venue, Symbol: o.Symbol, Side: o.Side, Type: TypeLimit, TIF: o.TIF, Session: o.Session, Qty: o.LeavesQty, LimitPrice: cm.LimitPrice, ClientOrderID: o.ID}
			if good, reason := c.heldGate(req, o.ID); !good {
				return blocked(reason)
			}
		}
		return c.handleReplaceSingle(ctx, cm)
	}
	stop := c.order(entry.RiskEntry.StopID)
	if o.RiskEntryID != "" && o.ID != stop.ID {
		return blocked("late-fill protection trigger is locked; adjust its working limit")
	}
	if !validLimitIfTouchedPrice(cm.StopPrice) {
		return blocked("invalid trigger price")
	}
	risk := *entry.RiskEntry
	if o.ID == entry.ID {
		entry.StopPrice = cm.StopPrice
		price, err := riskLimit(cm.StopPrice, risk.BuyCushion, true)
		if err != nil {
			return blocked(err.Error())
		}
		entry.LimitPrice = price
	} else {
		stop.StopPrice = cm.StopPrice
		price, err := riskLimit(cm.StopPrice, risk.SellCushion, false)
		if err != nil {
			return blocked(err.Error())
		}
		stop.LimitPrice = price
	}
	if entry.Held.ChildClientID == "" {
		if !finitePositive(cm.Qty) {
			return blocked("positive reviewed share quantity required before buy activation")
		}
		if stop.StopPrice >= entry.StopPrice {
			return blocked("sell trigger must be below buy trigger")
		}
		account := c.state.Venue(entry.Venue).Account
		if !riskAccountFresh(c.state, entry.Venue) || account.TsMs <= 0 || c.now()-account.TsMs > 30_000 {
			return blocked("fresh account data required")
		}
		funds := account.BuyingPower
		if risk.Mode == "CashPct" {
			funds = account.AvailableCash
		}
		qty := math.Min(riskShares(risk.Budget, entry.LimitPrice, stop.LimitPrice, funds), math.Floor(cm.Qty))
		if !finitePositive(qty) {
			return blocked("risk or funding budget rounds to zero shares")
		}
		req := OrderRequest{Venue: entry.Venue, Symbol: entry.Symbol, Side: SideBuy, Type: TypeStopLimit, TIF: TIFDay, Session: SessionExtended, Qty: qty, LimitPrice: entry.LimitPrice, StopPrice: entry.StopPrice, ClientOrderID: entry.ID}
		if good, reason := c.heldGate(req, entry.ID); !good {
			return blocked(reason)
		}
		entry.Qty, entry.LeavesQty = qty, qty
	} else if o.ID != entry.ID && !c.state.PositionsReady(entry.Venue) {
		return blocked("position data unavailable")
	}
	entry.UpdatedMs = c.now()
	stop.UpdatedMs = c.now()
	if err := c.appendAndFold(RiskEntryChanged{Entry: entry, Stop: stop}, SrcLocal); err != nil {
		return blocked("event append failed: " + err.Error())
	}
	if last, ok := c.lastEligible[o.Symbol]; ok && c.freshEligiblePrint(last) {
		current := entry
		if o.ID != entry.ID {
			current = stop
		}
		if StopLimitTriggered(current.Side, last.Price, current.StopPrice) {
			c.activateHeld(ctx, current)
		}
	}
	return CmdAck{Accepted: true, OrderID: o.ID}
}

func (c *Core) disarmRiskEntries(ctx context.Context, venue VenueID, protection bool) {
	for _, vs := range c.state.Venues {
		for _, entry := range vs.Orders {
			if entry.RiskEntry == nil || venue != "" && entry.Venue != venue {
				continue
			}
			if protection {
				if err := c.cancelRiskProtection(ctx, entry); err != nil {
					c.syslog("exec.risk", err.Error())
				}
			} else if entry.Working() {
				if entry.Held.ChildClientID == "" {
					_ = c.handleCancel(ctx, CancelOrder{Venue: entry.Venue, OrderID: entry.ID})
				} else {
					c.requestHeldVenueCancel(ctx, entry, "master disarmed; cancel remaining buy")
				}
			}
		}
	}
}

func riskAccountFresh(s *State, v VenueID) bool { fresh, ok := s.AccountFresh[v]; return !ok || fresh }
