package exec

import (
	"context"
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func rawProtectionPrint(t *testing.T, c *Core, ctx context.Context, p EligiblePrint) {
	t.Helper()
	done := make(chan struct{})
	select {
	case c.printCh <- executionInput{Print: p, Done: done}:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("raw print was not processed")
	}
}

func protectionHistory(t *testing.T, c *Core, id string) (HeldOrder, int64) {
	t.Helper()
	events, err := c.store.ReadExecEventsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	var held HeldOrder
	var activation int64
	for _, env := range events {
		if env.OrderID != id || env.Kind != (HeldOrderChanged{}).Kind() {
			continue
		}
		var e HeldOrderChanged
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			t.Fatal(err)
		}
		held = e.Held
		if held.Phase == HeldActivating && activation == 0 {
			activation = env.TsMs
		}
	}
	return held, activation
}

func fixedProtectionCore(t *testing.T) (*Core, *clock.Fake, *heldTestBroker, context.Context, string) {
	t.Helper()
	c, clk, b, ctx := newHeldCore(t, time.Date(2026, 10, 20, 4, 0, 0, 0, session.Loc()))
	b.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 1, AvgPrice: 6.19}}}
	waitPosition(t, c, "AAPL", 1)
	a := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideSell, Type: TypeStopLimit, TIF: TIFDay, Session: SessionExtended, Qty: 1, StopPrice: 6, LimitPrice: 5.94, RouteExpected: RouteEngineHeld})
	if !a.Accepted {
		t.Fatal(a)
	}
	waitHeldOrder(t, c, a.OrderID, HeldWaiting)
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(1, clk.Now(), 6.34))
	waitHeldOrder(t, c, a.OrderID, HeldArmed)
	return c, clk, b, ctx, a.OrderID
}

func TestProtectionTimeoutRetainsFirstCrossingDeadline(t *testing.T) {
	c, clk, b, ctx, id := fixedProtectionCore(t)
	clk.Advance(time.Millisecond)
	at := clk.Now()
	rawProtectionPrint(t, c, ctx, eligible(2, at, 5.23))
	c.PreviewEligiblePrint(ctx, "AAPL") // deadline registered before advancing fake time
	clk.Advance(50 * time.Millisecond)
	rawProtectionPrint(t, c, ctx, eligible(3, clk.Now(), 5.24))
	clk.Advance(49 * time.Millisecond)
	rawProtectionPrint(t, c, ctx, eligible(3, clk.Now(), 5.24)) // duplicate cannot extend the window
	if _, activation := protectionHistory(t, c, id); activation != 0 {
		t.Fatal("activated before the deadline")
	}
	c.PreviewEligiblePrint(ctx, "AAPL")
	clk.Advance(time.Millisecond)
	waitHeldOrder(t, c, id, HeldActivating) // no new market event is necessary
	if _, activation := protectionHistory(t, c, id); activation != at.Add(100*time.Millisecond).UnixMilli() {
		t.Fatalf("activation=%d", activation)
	}
	select {
	case child := <-b.submits:
		if child.LimitPrice != 5.94 || child.Qty != 1 {
			t.Fatal(child)
		}
	case <-time.After(time.Second):
		t.Fatal("no protective LIMIT child")
	}
}

func TestProtectionMissingBookRecoveryStillUsesAdverseFallback(t *testing.T) {
	c, clk, _, ctx, id := fixedProtectionCore(t)
	feedProtectionBook(t, c, ctx, TrailBook{Symbol: "AAPL", Generation: 1, Gap: true})
	clk.Advance(time.Millisecond)
	at := clk.Now()
	rawProtectionPrint(t, c, ctx, eligible(2, at, 5.23))
	c.PreviewEligiblePrint(ctx, "AAPL")
	clk.Advance(98 * time.Millisecond)
	rawProtectionPrint(t, c, ctx, eligible(3, clk.Now(), 6.2708)) // trade recovery alone is insufficient
	if _, activation := protectionHistory(t, c, id); activation != 0 {
		t.Fatal("activated before timeout")
	}
	c.PreviewEligiblePrint(ctx, "AAPL")
	clk.Advance(2 * time.Millisecond)
	waitHeldOrder(t, c, id, HeldActivating)
	if _, activation := protectionHistory(t, c, id); activation != at.Add(100*time.Millisecond).UnixMilli() {
		t.Fatalf("activation=%d", activation)
	}
}

func TestProtectionSupportedGenuineGapActivatesImmediately(t *testing.T) {
	c, clk, _, ctx, id := fixedProtectionCore(t)
	clk.Advance(time.Millisecond)
	feedProtectionBook(t, c, ctx, TrailBook{Symbol: "AAPL", Bid: 5.22, Ask: 5.24, Generation: 1, Coverage: 1})
	at := clk.Now()
	rawProtectionPrint(t, c, ctx, eligible(2, at, 5.23))
	waitHeldOrder(t, c, id, HeldActivating)
	if _, activation := protectionHistory(t, c, id); activation != at.UnixMilli() {
		t.Fatal("genuine gap was delayed")
	}
}

func TestProtectionBootstrapRequiresCorroboration(t *testing.T) {
	for _, viaBook := range []bool{false, true} {
		t.Run(map[bool]string{false: "second_trade", true: "book"}[viaBook], func(t *testing.T) {
			c, clk, b, ctx := newHeldCore(t, time.Date(2026, 10, 20, 4, 0, 0, 0, session.Loc()))
			b.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 1}}}
			waitPosition(t, c, "AAPL", 1)
			a := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100, Config: TrailConfig{Mode: "PRICE", Value: .3, Sessions: TrailSessions{Pre: true}, Pricing: "CUSHION"}})
			if !a.Accepted {
				t.Fatal(a)
			}
			waitHeldOrder(t, c, a.OrderID, HeldWaiting)
			clk.Advance(time.Millisecond)
			rawProtectionPrint(t, c, ctx, eligible(1, clk.Now(), 10))
			h, _ := protectionHistory(t, c, a.OrderID)
			if h.Trail.Watermark != 0 {
				t.Fatal("first unsupported print initialized a watermark")
			}
			clk.Advance(50 * time.Millisecond)
			if viaBook {
				feedProtectionBook(t, c, ctx, TrailBook{Symbol: "AAPL", Bid: 9.99, Ask: 10.01, Generation: 1, Coverage: 1})
			} else {
				rawProtectionPrint(t, c, ctx, eligible(2, clk.Now(), 10))
			}
			armed := waitHeldOrder(t, c, a.OrderID, HeldArmed)
			if armed.Held.Trail.Watermark != 10 || armed.StopPrice != 9.7 {
				t.Fatal(armed)
			}
		})
	}
}

func TestProtectionUnconfirmedFavorablePrintNeverRatchets(t *testing.T) {
	for _, side := range []Side{SideSell, SideCover} {
		t.Run(string(side), func(t *testing.T) {
			c, clk, b, ctx := newHeldCore(t, time.Date(2026, 10, 20, 4, 0, 0, 0, session.Loc()))
			qty, spike := 1.0, 12.0
			if side == SideCover {
				qty, spike = -1, 8
			}
			b.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: qty}}}
			waitPosition(t, c, "AAPL", qty)
			a := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: side, PositionPct: 100, Config: TrailConfig{Mode: "PRICE", Value: .3, Sessions: TrailSessions{Pre: true}, Pricing: "CUSHION"}})
			if !a.Accepted {
				t.Fatal(a)
			}
			clk.Advance(time.Millisecond)
			feedCorroboratedPrint(t, c, ctx, eligible(1, clk.Now(), 10))
			initial := waitHeldOrder(t, c, a.OrderID, HeldArmed)
			clk.Advance(time.Millisecond)
			rawProtectionPrint(t, c, ctx, eligible(2, clk.Now(), spike))
			c.PreviewEligiblePrint(ctx, "AAPL")
			clk.Advance(101 * time.Millisecond)
			c.PreviewEligiblePrint(ctx, "AAPL")
			h, activation := protectionHistory(t, c, a.OrderID)
			if h.Trail.Watermark != 10 || h.Trail.Stop != initial.StopPrice || activation != 0 {
				t.Fatalf("unconfirmed favorable price changed protection: %+v", h)
			}
		})
	}
}

func TestProtectionCancelAndGapInvalidatePending(t *testing.T) {
	for _, gap := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "gap"}[gap], func(t *testing.T) {
			c, clk, _, ctx, id := fixedProtectionCore(t)
			clk.Advance(time.Millisecond)
			rawProtectionPrint(t, c, ctx, eligible(2, clk.Now(), 5.23))
			if gap {
				rawProtectionPrint(t, c, ctx, EligiblePrint{Gap: true, RecvTsMs: clk.Now().UnixMilli()})
			} else if a := c.Do(CancelOrder{Venue: "v", OrderID: id}); !a.Accepted {
				t.Fatal(a)
			}
			c.PreviewEligiblePrint(ctx, "AAPL")
			clk.Advance(101 * time.Millisecond)
			c.PreviewEligiblePrint(ctx, "AAPL")
			if _, activation := protectionHistory(t, c, id); activation != 0 {
				t.Fatal("invalidated pending stop activated")
			}
		})
	}
}

func TestProtectionAgedQueuedPrintDoesNotGetNewWindow(t *testing.T) {
	c, clk, _, ctx, id := fixedProtectionCore(t)
	clk.Advance(time.Millisecond)
	p := eligible(2, clk.Now(), 5.23)
	clk.Advance(101 * time.Millisecond)
	rawProtectionPrint(t, c, ctx, p)
	waitHeldOrder(t, c, id, HeldActivating)
	if _, activation := protectionHistory(t, c, id); activation != clk.Now().UnixMilli() {
		t.Fatal("aged queue granted a new window")
	}
}

func TestProtectionAmendmentKeepsDeadlineAfterUnprovenRecovery(t *testing.T) {
	c, clk, _, ctx, id := fixedProtectionCore(t)
	feedProtectionBook(t, c, ctx, TrailBook{Symbol: "AAPL", Generation: 1, Gap: true})
	clk.Advance(time.Millisecond)
	at := clk.Now()
	rawProtectionPrint(t, c, ctx, eligible(2, at, 5.23))
	clk.Advance(50 * time.Millisecond)
	rawProtectionPrint(t, c, ctx, eligible(3, clk.Now(), 6.27))
	if a := c.Do(ReplaceOrder{Venue: "v", OrderID: id, Qty: 1, StopPrice: 6.05}); !a.Accepted {
		t.Fatal(a)
	}
	c.PreviewEligiblePrint(ctx, "AAPL")
	clk.Advance(50 * time.Millisecond)
	waitHeldOrder(t, c, id, HeldActivating)
	if _, activation := protectionHistory(t, c, id); activation != at.Add(100*time.Millisecond).UnixMilli() {
		t.Fatal("edit reset or canceled the unresolved crossing")
	}
}

func TestProtectionUnknownReceiptUsesImmediateAdverseFallback(t *testing.T) {
	c, clk, _, ctx, id := fixedProtectionCore(t)
	clk.Advance(time.Millisecond)
	p := eligible(2, clk.Now(), 5.23)
	p.ReceiptMs = 0
	rawProtectionPrint(t, c, ctx, p)
	waitHeldOrder(t, c, id, HeldActivating)
	if _, activation := protectionHistory(t, c, id); activation != clk.Now().UnixMilli() {
		t.Fatal("unknown receipt created a new wait")
	}
}

func TestProtectionUnknownRecoveryReceiptCannotRefuteCrossing(t *testing.T) {
	c, clk, _, ctx, id := fixedProtectionCore(t)
	clk.Advance(time.Millisecond)
	rawProtectionPrint(t, c, ctx, eligible(2, clk.Now(), 5.23))
	clk.Advance(50 * time.Millisecond)
	p := eligible(3, clk.Now(), 6.34)
	p.ReceiptMs = 0
	rawProtectionPrint(t, c, ctx, p)
	c.PreviewEligiblePrint(ctx, "AAPL")
	clk.Advance(50 * time.Millisecond)
	c.PreviewEligiblePrint(ctx, "AAPL")
	if _, activation := protectionHistory(t, c, id); activation == 0 {
		t.Fatal("unknown recovery receipt refuted the unresolved crossing")
	}
}

func TestProtectionTimeoutDoesNotRatchetOppositeVenue(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 20, 4, 0, 0, 0, session.Loc()))
	longBroker, shortBroker := newHeldTestBroker(), newHeldTestBroker()
	c := NewCore(CoreConfig{Venues: []VenueID{"v", "w"}, Clock: clk, IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(1))), Store: &heldTestStore{}, Brokers: map[VenueID]Broker{"v": longBroker, "w": shortBroker}, Gate: GateConfig{Global: GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionValue: 1_000_000, MaxSymbolPositionShares: 1000}, Venue: map[VenueID]VenueLimits{"v": {MaxOrderValue: 100_000, MaxPositionValue: 1_000_000, MaxPositionShares: 1000, MaxOpenOrders: 10}, "w": {MaxOrderValue: 100_000, MaxPositionValue: 1_000_000, MaxPositionShares: 1000, MaxOpenOrders: 10}}}})
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Recover(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	c.Do(ConfigureHeldDemand{Demand: &heldDemandStub{}})
	c.Do(Arm{})
	longBroker.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 1}}}
	waitPosition(t, c, "AAPL", 1)
	shortBroker.ev <- BrokerPositions{V: "w", Positions: []Position{{Venue: "w", Symbol: "AAPL", Qty: -1}}}
	waitPosition(t, c, "AAPL", -1)
	config := TrailConfig{Mode: "PRICE", Value: .3, Sessions: TrailSessions{Pre: true}, Pricing: "CUSHION"}
	long := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100, Config: config})
	short := c.Do(SubmitTrail{Venue: "w", Symbol: "AAPL", Side: SideCover, PositionPct: 100, Config: config})
	if !long.Accepted || !short.Accepted {
		t.Fatal(long, short)
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(1, clk.Now(), 6.34))
	clk.Advance(time.Millisecond)
	rawProtectionPrint(t, c, ctx, eligible(2, clk.Now(), 5.23))
	c.PreviewEligiblePrint(ctx, "AAPL")
	clk.Advance(100 * time.Millisecond)
	waitHeldOrder(t, c, long.OrderID, HeldActivating)
	h, activation := protectionHistory(t, c, short.OrderID)
	if h.Trail.Watermark != 6.34 || h.Trail.Stop != 6.64 || activation != 0 {
		t.Fatalf("long adverse timeout trusted a favorable short price: %+v", h)
	}
}

func TestProtectionBookRejectsUnknownStaleRegressingAndWideEvidence(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1000))
	c := NewCore(CoreConfig{Clock: clk})
	clockValue := int64(3200) // deliberately unsynchronized provider clock
	book := TrailBook{Symbol: "AAPL", Generation: 1, Coverage: 1, ReceiptMs: 1000, Bid: 10, Ask: 10.01, BidTsMs: &clockValue, AskTsMs: &clockValue}
	c.handleTrailBook(context.Background(), book)
	if _, ok := c.protectionBook("AAPL"); !ok {
		t.Fatal("coherent book rejected")
	}
	clk.Advance(251 * time.Millisecond)
	book.ReceiptMs = clk.Now().UnixMilli()
	c.handleTrailBook(context.Background(), book)
	if _, ok := c.protectionBook("AAPL"); ok {
		t.Fatal("unchanged side clocks refreshed stale evidence")
	}
	for _, change := range []func(*TrailBook){
		func(b *TrailBook) { b.BidTsMs = nil },
		func(b *TrailBook) { z := int64(0); b.AskTsMs = &z },
		func(b *TrailBook) { b.Ask = 11 },
		func(b *TrailBook) { b.Bid = 11 },
		func(b *TrailBook) { z := int64(3100); b.BidTsMs = &z },
		func(b *TrailBook) { z := int64(3451); b.AskTsMs = &z },
	} {
		clear(c.trailBooks)
		c.handleTrailBook(context.Background(), book)
		bad := book
		change(&bad)
		c.handleTrailBook(context.Background(), bad)
		if _, ok := c.protectionBook("AAPL"); ok {
			t.Fatalf("invalid book accepted: %+v", bad)
		}
	}
}

// Synthetic incident trace: real Core, fake broker/time, no runtime connection.
// The prior book is explicitly usable; historical capture coverage is partial.
func TestProtectivePriceSAIQCluster(t *testing.T) {
	start := time.Date(2026, 10, 20, 4, 1, 1, 418000000, session.Loc())
	c, clk, broker, ctx := newHeldCore(t, start)
	broker.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 1, AvgPrice: 6.19}}}
	waitPosition(t, c, "AAPL", 1)
	ack := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideSell, Type: TypeStopLimit, TIF: TIFDay, Session: SessionExtended, Qty: 1, StopPrice: 6.00, LimitPrice: 5.94, RouteExpected: RouteEngineHeld})
	if !ack.Accepted {
		t.Fatalf("setup stop: %+v", ack)
	}
	waitHeldOrder(t, c, ack.OrderID, HeldWaiting)
	feed := func(seq int64, price float64) {
		p := eligible(seq, clk.Now(), price)
		p.ReceiptMs = clk.Now().UnixMilli()
		c.FeedEligiblePrint(ctx, p)
	}

	awaitPrice := func(price float64) {
		t.Helper()
		until := time.Now().Add(time.Second)
		for time.Now().Before(until) {
			p := c.PreviewEligiblePrint(ctx, "AAPL")
			if p.Trusted && p.Price == price && p.TsMs == clk.Now().UnixMilli() {
				return
			}
		}
		t.Fatalf("price %v not processed", price)
	}
	clk.Advance(time.Millisecond)
	feed(1, 6.34)
	waitHeldOrder(t, c, ack.OrderID, HeldArmed)
	clk.Advance(8 * time.Millisecond)
	providerTime := clk.Now().Add(2200 * time.Millisecond).UnixMilli()
	book := TrailBook{Symbol: "AAPL", Bid: 6.30, Ask: 6.34, Generation: 1, Coverage: 1, ReceiptMs: clk.Now().UnixMilli(), BidTsMs: &providerTime, AskTsMs: &providerTime}
	bookDone := make(chan struct{})
	c.printCh <- executionInput{Book: &book, Done: bookDone}
	<-bookDone
	clk.Advance(145 * time.Millisecond)
	candidateAt := clk.Now()
	feed(2, 5.23)
	awaitPrice(5.23)
	feed(3, 5.24)
	awaitPrice(5.24)
	clk.Advance(55 * time.Millisecond)
	feed(4, 5.24)
	awaitPrice(5.24)
	clk.Advance(43 * time.Millisecond)
	feed(5, 5.24)
	feed(6, 6.2708)
	awaitPrice(6.2708)
	clk.Advance(2 * time.Millisecond)
	c.PreviewEligiblePrint(ctx, "AAPL")

	events, err := c.store.(*heldTestStore).ReadExecEventsSince(start.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range events {
		if env.OrderID != ack.OrderID || env.Kind != (HeldOrderChanged{}).Kind() {
			continue
		}
		var change HeldOrderChanged
		if err := json.Unmarshal(env.Payload, &change); err != nil {
			t.Fatal(err)
		}
		if change.Held.Phase == HeldActivating {
			t.Fatalf("unsupported $5.23 candidate activated $6 stop after %d ms; book was $6.30/$6.34 and recovery reached $6.2708 at +98 ms; want no activation when that book remains usable", env.TsMs-candidateAt.UnixMilli())
		}
	}
}
