package exec

import (
	"context"
	"encoding/json"
	"math"
	"time"
)

const (
	protectionWait    = 100 * time.Millisecond
	protectionBookAge = 250 * time.Millisecond
)

type protectionPrice struct {
	reference EligiblePrint
	latest    EligiblePrint
	candidate EligiblePrint
	accepted  bool
}

type pendingProtection struct {
	print, reference EligiblePrint
	resume           int64
	cycle            string
}

func sameProtectionPrint(a, b EligiblePrint) bool {
	return a.Symbol == b.Symbol && a.Seq == b.Seq && a.TsMs == b.TsMs
}

// This is an order-price tolerance floor, not a claim about trade tick size.
func protectionFloor(price float64) float64 {
	if price >= 1 {
		return .02
	}
	return .0002
}

func nearProtectionPrice(a, b float64) bool {
	return math.Abs(a-b) <= math.Max(protectionFloor(b), b*.0025)
}

func (c *Core) protectiveOrder(o Order) bool {
	if o.Held == nil || o.Type == TypeLimitIfTouched {
		return false
	}
	return o.Held.Trail != nil || o.Side == SideSell || o.Side == SideCover || o.RiskEntryID != "" || o.DeferredPositionPct > 0 ||
		reducesPosition(c.state, OrderRequest{Venue: o.Venue, Symbol: o.Symbol, Side: o.Side})
}

func (c *Core) protectionBook(symbol string) (TrailBook, bool) {
	b, ok := c.trailBooks[symbol]
	age := c.now() - b.ReceiptMs
	if !ok || b.clockInvalid || b.Generation == 0 || b.Coverage == 0 || b.ReceiptMs <= 0 || age < 0 || age > protectionBookAge.Milliseconds() ||
		b.bidReceiptMs <= 0 || b.askReceiptMs <= 0 || c.now()-b.bidReceiptMs > protectionBookAge.Milliseconds() || c.now()-b.askReceiptMs > protectionBookAge.Milliseconds() ||
		b.BidTsMs == nil || b.AskTsMs == nil || *b.BidTsMs <= 0 || *b.AskTsMs <= 0 ||
		!finitePositive(b.Bid) || !finitePositive(b.Ask) || b.Bid > b.Ask || b.Ask-b.Bid > (b.Bid+b.Ask)*.005 {
		return b, false
	}
	if d, yes := c.heldDemand.(BookCoverageController); yes {
		if revision := d.TrailBookCoverage(symbol); revision == 0 || revision != b.Coverage {
			return b, false
		}
	}
	return b, true
}

func protectionBookSupports(b TrailBook, price float64) bool {
	margin := math.Max(protectionFloor((b.Bid+b.Ask)/2), b.Ask-b.Bid)
	return price >= b.Bid-margin && price <= b.Ask+margin
}

func (c *Core) protectionPriceAccepted(p EligiblePrint) bool {
	if p.ReceiptMs <= 0 || c.now() < p.ReceiptMs || c.now()-p.ReceiptMs > eligiblePrintMaxAge.Milliseconds() {
		return false
	}
	s := c.protectionPrices[p.Symbol]
	if s == nil || !sameProtectionPrint(s.latest, p) {
		if !sameProtectionPrint(c.lastEligible[p.Symbol], p) {
			return false
		}
		c.observeProtectionPrice(p)
		s = c.protectionPrices[p.Symbol]
	}
	return s != nil && s.accepted && sameProtectionPrint(s.latest, p)
}

func (c *Core) observeProtectionPrice(p EligiblePrint) {
	if c.protectionPrices == nil {
		c.protectionPrices = make(map[string]*protectionPrice)
	}
	s := c.protectionPrices[p.Symbol]
	if s == nil {
		s = &protectionPrice{}
		c.protectionPrices[p.Symbol] = s
	}
	if sameProtectionPrint(s.latest, p) {
		return
	}
	b, book := c.protectionBook(p.Symbol)
	localAge := c.now() - p.ReceiptMs
	known := p.ReceiptMs > 0 && localAge >= 0 && localAge <= eligiblePrintMaxAge.Milliseconds()
	supported := book && protectionBookSupports(b, p.Price)
	ordinary := s.reference.ReceiptMs > 0 && p.ReceiptMs-s.reference.ReceiptMs >= 0 && p.ReceiptMs-s.reference.ReceiptMs <= eligiblePrintMaxAge.Milliseconds() &&
		math.Abs(p.Price-s.reference.Price) <= math.Max(protectionFloor(s.reference.Price), s.reference.Price*.01)
	second := s.candidate.Seq > 0 && p.Seq > s.candidate.Seq && s.candidate.ReceiptMs > 0 &&
		p.ReceiptMs-s.candidate.ReceiptMs >= 0 && p.ReceiptMs-s.candidate.ReceiptMs < protectionWait.Milliseconds() && nearProtectionPrice(p.Price, s.candidate.Price)
	s.latest = p
	s.accepted = known && (supported || !book && (ordinary || second))
	if s.accepted {
		if s.candidate.Seq > 0 {
			c.logProtection(Order{Symbol: p.Symbol}, pendingProtection{print: s.candidate, reference: s.reference}, "price_confirmed")
		}
		s.reference, s.candidate = p, EligiblePrint{}
	} else if s.candidate.Seq == 0 || p.ReceiptMs >= s.candidate.ReceiptMs+protectionWait.Milliseconds() || !nearProtectionPrice(p.Price, s.candidate.Price) {
		s.candidate = p
		c.logProtection(Order{Symbol: p.Symbol}, pendingProtection{print: p, reference: s.reference}, "price_pending")
	}
}

func (c *Core) requestProtectionBook(o Order) {
	if !c.protectiveOrder(o) || c.protectionBooks[o.ID] {
		return
	}
	if d, ok := c.heldDemand.(ProtectionBookDemandController); ok {
		if c.protectionBooks == nil {
			c.protectionBooks = make(map[string]bool)
		}
		d.EnsureProtectionBook(o.ID, o.Symbol)
		c.protectionBooks[o.ID] = true
	}
}

func (c *Core) activateProtectedStop(ctx context.Context, o Order, p EligiblePrint) {
	if !c.protectiveOrder(o) {
		c.activateHeld(ctx, o)
		return
	}
	if !c.protectionCanWait(o) {
		return
	}
	if c.protectionPriceAccepted(p) {
		c.activateHeld(ctx, o)
		return
	}
	c.deferProtection(o, p)
}

func (c *Core) protectionCanWait(o Order) bool {
	if !o.Working() || o.Held == nil || o.Held.CancelRequested || (o.Held.Phase != HeldWaiting && o.Held.Phase != HeldArmed) {
		return false
	}
	if o.RiskEntryID != "" {
		e := c.riskOwner(o)
		if e.RiskEntry == nil || e.RiskEntry.ProtectionCanceled || e.RiskEntry.Failure != "" || o.Qty <= 0 {
			return false
		}
	}
	return o.Held.Trail == nil || !o.Held.Trail.Retired && !o.Held.Trail.Triggered
}

func (c *Core) deferProtection(o Order, p EligiblePrint) {
	if !c.protectionCanWait(o) || !StopLimitTriggered(o.Side, p.Price, o.StopPrice) {
		return
	}
	if c.protectionPending == nil {
		c.protectionPending = make(map[string]pendingProtection)
	}
	if a, ok := c.protectionPending[o.ID]; ok && a.resume == o.Held.ResumeAfterMs && StopLimitTriggered(o.Side, a.print.Price, o.StopPrice) {
		return
	}
	a := pendingProtection{print: p, resume: o.Held.ResumeAfterMs, cycle: c.trailPositionCycle(o.Venue, o.Symbol)}
	if s := c.protectionPrices[o.Symbol]; s != nil {
		a.reference = s.reference
	}
	c.protectionPending[o.ID] = a
	c.logProtection(o, a, "pending")
}

func (c *Core) validProtection(o Order, a pendingProtection) bool {
	return c.protectionCanWait(o) &&
		!o.Held.CancelRequested && o.Held.ResumeAfterMs == a.resume && StopLimitTriggered(o.Side, a.print.Price, o.StopPrice) &&
		c.trailPositionCycle(o.Venue, o.Symbol) == a.cycle && c.protectiveOrder(o)
}

func (c *Core) authorizeProtection(ctx context.Context, o Order, a pendingProtection, reason string) {
	delete(c.protectionPending, o.ID)
	c.logProtection(o, a, reason)
	if o.Held.Trail != nil {
		c.advanceTrailPrice(ctx, o, a.print, false, true)
	} else {
		c.activateHeld(ctx, o)
	}
}

func (c *Core) resolveProtection(ctx context.Context, symbol string) {
	p := c.lastEligible[symbol]
	b, book := c.protectionBook(symbol)
	for _, id := range c.heldOrderIDs() {
		a, pending := c.protectionPending[id]
		if !pending {
			continue
		}
		o := c.order(id)
		if o.Symbol != symbol {
			continue
		}
		if !c.validProtection(o, a) {
			delete(c.protectionPending, id)
			continue
		}
		if c.now() >= a.print.ReceiptMs+protectionWait.Milliseconds() {
			continue
		}
		if book && protectionBookSupports(b, a.print.Price) {
			c.authorizeProtection(ctx, o, a, "book_confirmed")
		} else if p.Seq > a.print.Seq && book && a.reference.Seq > 0 &&
			protectionBookSupports(b, a.reference.Price) && protectionBookSupports(b, p.Price) {
			delete(c.protectionPending, id)
			c.logProtection(o, a, "refuted")
		} else if p.Seq > a.print.Seq && nearProtectionPrice(p.Price, a.print.Price) && (!book || protectionBookSupports(b, p.Price)) {
			c.authorizeProtection(ctx, o, a, "print_confirmed")
		}
	}
}

func (c *Core) protectionDeadline(ctx context.Context) int64 {
	var first int64
	for _, id := range c.heldOrderIDs() {
		a, pending := c.protectionPending[id]
		if !pending {
			continue
		}
		o := c.order(id)
		if !c.printHealthy || !c.validProtection(o, a) {
			delete(c.protectionPending, id)
			continue
		}
		due := a.print.ReceiptMs + protectionWait.Milliseconds()
		if due <= c.now() {
			if c.freshEligiblePrint(a.print) {
				c.authorizeProtection(ctx, o, a, "timeout")
			} else {
				delete(c.protectionPending, id)
			}
		} else if first == 0 || due < first {
			first = due
		}
	}
	for symbol, s := range c.protectionPrices {
		if s.candidate.Seq == 0 {
			continue
		}
		due := s.candidate.ReceiptMs + protectionWait.Milliseconds()
		if due <= c.now() {
			c.logProtection(Order{Symbol: symbol}, pendingProtection{print: s.candidate, reference: s.reference}, "price_unconfirmed")
			s.candidate = EligiblePrint{}
		} else if first == 0 || due < first {
			first = due
		}
	}
	return first
}

func (c *Core) logProtection(o Order, a pendingProtection, verdict string) {
	b, _ := c.protectionBook(o.Symbol)
	deadline, elapsed := int64(0), int64(0)
	if a.print.ReceiptMs > 0 {
		deadline, elapsed = a.print.ReceiptMs+protectionWait.Milliseconds(), c.now()-a.print.ReceiptMs
	}
	detail, _ := json.Marshal(struct {
		Order, Symbol, Verdict                                    string
		Price, Reference, Bid, Ask                                float64
		Sequence, ReceiptMs, BookReceiptMs, DeadlineMs, ElapsedMs int64
	}{o.ID, o.Symbol, verdict, a.print.Price, a.reference.Price, b.Bid, b.Ask, a.print.Seq, a.print.ReceiptMs, b.ReceiptMs, deadline, elapsed})
	c.syslog("exec.protection_price", string(detail))
}
