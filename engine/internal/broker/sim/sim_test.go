package sim

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

func newSim(t *testing.T) *Broker {
	t.Helper()
	b := New("sim-1", clock.NewFake(time.UnixMilli(1000)), 100_000, Options{})
	b.SetMark("AAPL", 100)
	return b
}

// newSimWithSlippage is newSim's Task 4 counterpart: a broker configured with
// a non-zero SlippageBps, for tests exercising the adverse-price knob.
func newSimWithSlippage(t *testing.T, bps float64) *Broker {
	t.Helper()
	b := New("sim-1", clock.NewFake(time.UnixMilli(1000)), 100_000, Options{SlippageBps: bps})
	b.SetMark("AAPL", 100)
	return b
}

// drain reads the next event within a timeout (events are emitted synchronously
// into a buffered channel, so this returns promptly).
func drain(t *testing.T, b *Broker) exec.BrokerEvent {
	t.Helper()
	select {
	case ev := <-b.Events():
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broker event")
		return nil
	}
}

// TestSimSetBookStoresSnapshot is a whitebox storage check (package sim):
// SetBook must just record the latest book per symbol. Task 2 adds the
// behavioral (fill-pricing) tests; this task only wires and stores it.
func TestSimSetBookStoresSnapshot(t *testing.T) {
	b := newSim(t)
	book := feed.Book{
		Symbol: "AAPL", TsMs: 1234,
		Bids: []feed.BookLevel{{Price: 99.5, Volume: 100}},
		Asks: []feed.BookLevel{{Price: 100.5, Volume: 150}},
	}
	b.SetBook("AAPL", book)

	got, ok := b.books["AAPL"]
	if !ok {
		t.Fatal("SetBook did not store a book for AAPL")
	}
	if got.Symbol != book.Symbol || got.TsMs != book.TsMs ||
		len(got.Bids) != 1 || got.Bids[0].Price != 99.5 ||
		len(got.Asks) != 1 || got.Asks[0].Price != 100.5 {
		t.Fatalf("stored book = %+v, want %+v", got, book)
	}
}

// --- fillAgainstBook: pure book-walk pricing, no broker/mutex machinery ---

func TestFillAgainstBook_MarketSweepsMultipleLevelsWeightedAverage(t *testing.T) {
	o := &exec.Order{Side: exec.SideBuy, Type: exec.TypeMarket, LeavesQty: 250}
	book := feed.Book{
		Asks: []feed.BookLevel{
			{Price: 100.0, Volume: 100},
			{Price: 100.1, Volume: 100},
			{Price: 100.2, Volume: 100},
		},
	}
	qty, px := fillAgainstBook(o, book, 0)
	if qty != 250 {
		t.Fatalf("qty = %v, want 250 (full sweep across 3 levels)", qty)
	}
	want := (100*100.0 + 100*100.1 + 50*100.2) / 250
	if math.Abs(px-want) > 1e-9 {
		t.Fatalf("avgPrice = %v, want %v", px, want)
	}
}

func TestFillAgainstBook_LimitStopsAtFirstLevelViolatingCap(t *testing.T) {
	o := &exec.Order{Side: exec.SideBuy, Type: exec.TypeLimit, LimitPrice: 100.1, LeavesQty: 250}
	book := feed.Book{
		Asks: []feed.BookLevel{
			{Price: 100.0, Volume: 100},
			{Price: 100.1, Volume: 100},
			{Price: 100.2, Volume: 100}, // violates the 100.1 cap; walk stops before this level
		},
	}
	qty, px := fillAgainstBook(o, book, 0)
	if qty != 200 {
		t.Fatalf("qty = %v, want 200 (depth exhausted at the cap, not the full 250)", qty)
	}
	want := (100*100.0 + 100*100.1) / 200
	if math.Abs(px-want) > 1e-9 {
		t.Fatalf("avgPrice = %v, want %v", px, want)
	}
}

func TestFillAgainstBook_SellConsumesBidsDescending(t *testing.T) {
	o := &exec.Order{Side: exec.SideSell, Type: exec.TypeMarket, LeavesQty: 30}
	book := feed.Book{
		Bids: []feed.BookLevel{
			{Price: 99.5, Volume: 20},
			{Price: 99.4, Volume: 50},
		},
	}
	qty, px := fillAgainstBook(o, book, 0)
	if qty != 30 {
		t.Fatalf("qty = %v, want 30", qty)
	}
	want := (20*99.5 + 10*99.4) / 30
	if math.Abs(px-want) > 1e-9 {
		t.Fatalf("avgPrice = %v, want %v", px, want)
	}
}

func TestFillAgainstBook_SellLimitCapsAtBidFloor(t *testing.T) {
	o := &exec.Order{Side: exec.SideSell, Type: exec.TypeLimit, LimitPrice: 99.45, LeavesQty: 30}
	book := feed.Book{
		Bids: []feed.BookLevel{
			{Price: 99.5, Volume: 20},
			{Price: 99.4, Volume: 50}, // below the 99.45 floor; walk stops before this level
		},
	}
	qty, px := fillAgainstBook(o, book, 0)
	if qty != 20 || px != 99.5 {
		t.Fatalf("qty=%v px=%v, want 20,99.5", qty, px)
	}
}

func TestFillAgainstBook_EmptyBookReturnsZero(t *testing.T) {
	o := &exec.Order{Side: exec.SideBuy, Type: exec.TypeMarket, LeavesQty: 10}
	qty, px := fillAgainstBook(o, feed.Book{}, 0)
	if qty != 0 || px != 0 {
		t.Fatalf("qty=%v px=%v, want 0,0 for an empty book", qty, px)
	}
}

func TestFillAgainstBook_ResumesFromLeavesQtyNotOriginalQty(t *testing.T) {
	// A previously-partially-filled order (Qty 100, already executed 60) must
	// only ask the book for its LeavesQty (40), not the original Qty.
	o := &exec.Order{Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 100, ExecutedQty: 60, LeavesQty: 40}
	book := feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 1000}}}
	qty, px := fillAgainstBook(o, book, 0)
	if qty != 40 || px != 100 {
		t.Fatalf("qty=%v px=%v, want 40,100", qty, px)
	}
}

// --- Task 4: slippageBps, the adverse-price knob ---

// TestFillAgainstBook_SlippageAdjustsBuyPriceAboveRawAsk: a buy fills
// strictly above the raw ask by the expected per-level amount.
func TestFillAgainstBook_SlippageAdjustsBuyPriceAboveRawAsk(t *testing.T) {
	o := &exec.Order{Side: exec.SideBuy, Type: exec.TypeMarket, LeavesQty: 10}
	book := feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}}
	qty, px := fillAgainstBook(o, book, 50) // 50 bps = 0.5%
	want := 100 * 1.005
	if qty != 10 || math.Abs(px-want) > 1e-9 {
		t.Fatalf("qty=%v px=%v, want 10,%v (strictly above the raw ask of 100)", qty, px, want)
	}
	if px <= 100 {
		t.Fatalf("px=%v must be strictly above the raw ask 100", px)
	}
}

// TestFillAgainstBook_SlippageAdjustsSellPriceBelowRawBid is the symmetric
// sell-side case: fills strictly below the raw bid.
func TestFillAgainstBook_SlippageAdjustsSellPriceBelowRawBid(t *testing.T) {
	o := &exec.Order{Side: exec.SideSell, Type: exec.TypeMarket, LeavesQty: 10}
	book := feed.Book{Bids: []feed.BookLevel{{Price: 100, Volume: 50}}}
	qty, px := fillAgainstBook(o, book, 50)
	want := 100 * 0.995
	if qty != 10 || math.Abs(px-want) > 1e-9 {
		t.Fatalf("qty=%v px=%v, want 10,%v (strictly below the raw bid of 100)", qty, px, want)
	}
	if px >= 100 {
		t.Fatalf("px=%v must be strictly below the raw bid 100", px)
	}
}

// TestFillAgainstBook_ZeroSlippageMatchesTask2Baseline is the explicit
// slippageBps=0 regression check: re-runs the Task 2 multi-level
// weighted-average case and asserts the exact same numbers, confirming the
// new parameter is a true no-op at its zero value.
func TestFillAgainstBook_ZeroSlippageMatchesTask2Baseline(t *testing.T) {
	o := &exec.Order{Side: exec.SideBuy, Type: exec.TypeMarket, LeavesQty: 250}
	book := feed.Book{
		Asks: []feed.BookLevel{
			{Price: 100.0, Volume: 100},
			{Price: 100.1, Volume: 100},
			{Price: 100.2, Volume: 100},
		},
	}
	qty, px := fillAgainstBook(o, book, 0)
	if qty != 250 {
		t.Fatalf("qty = %v, want 250", qty)
	}
	want := (100*100.0 + 100*100.1 + 50*100.2) / 250
	if math.Abs(px-want) > 1e-9 {
		t.Fatalf("avgPrice = %v, want %v (unchanged from Task 2 with slippage off)", px, want)
	}
}

// TestFillAgainstBook_SlippageAppliedPerLevelNotToFinalAverage is the key
// discriminator between "apply slippage per level, before the cap decision"
// (correct) and "walk the raw book, then flatly scale the final average by
// slippage" (wrong): the second level's raw price (100.90) satisfies the
// limit cap (101.00) on its own, but its SLIPPED price (with 50bps adverse)
// does not -- a limit order must never actually execute worse than its
// limit, so the walk must stop after level 1, filling only 5 shares, not the
// full 10 a raw-then-scale implementation would wrongly fill.
func TestFillAgainstBook_SlippageAppliedPerLevelNotToFinalAverage(t *testing.T) {
	o := &exec.Order{Side: exec.SideBuy, Type: exec.TypeLimit, LimitPrice: 101.00, LeavesQty: 10}
	book := feed.Book{
		Asks: []feed.BookLevel{
			{Price: 100.00, Volume: 5}, // slipped: 100.00*1.005 = 100.50 -- within the 101.00 cap
			{Price: 100.90, Volume: 5}, // slipped: 100.90*1.005 = 101.4045 -- violates the cap
		},
	}
	qty, px := fillAgainstBook(o, book, 50)
	if qty != 5 {
		t.Fatalf("qty = %v, want 5 (only level 1 -- slippage pushed level 2 past the limit)", qty)
	}
	want := 100.00 * 1.005
	if math.Abs(px-want) > 1e-9 {
		t.Fatalf("avgPrice = %v, want %v", px, want)
	}
}

// TestSimMarketableLimitFills is the basic sanity case: a marketable limit
// order fills against a book whose best level sits exactly at the entered
// limit. (The distinct price-improvement case — limit priced through the
// ask — is TestSimMarketableBuyLimitFillsAtAsk_PriceImprovement below.)
func TestSimMarketableLimitFills(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 100, ClientOrderID: "ET1"}
	ack, err := b.SubmitOrder(context.Background(), req)
	if err != nil || !ack.Accepted {
		t.Fatalf("submit: ack=%+v err=%v", ack, err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("first event should be OrderAccepted")
	}
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Qty != 10 || f.F.Price != 100 || f.LeavesQty != 0 {
		t.Fatalf("expected full fill at 100, got %+v ok=%v", f, ok)
	}
	if _, ok := drain(t, b).(exec.BrokerPositions); !ok {
		t.Fatal("fill should be followed by a BrokerPositions snapshot")
	}
}

// TestSimMarketableBuyLimitFillsAtAsk_PriceImprovement: a buy limit priced
// above the ask fills AT the ask (price improvement), not at the entered
// limit — book-walk pricing replaced "fill at the limit price" in Task 2.
func TestSimMarketableBuyLimitFillsAtAsk_PriceImprovement(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100.5, Volume: 50}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 102, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Price != 100.5 || f.F.Qty != 10 {
		t.Fatalf("buy limit above the ask should fill AT the ask (100.5), got %+v ok=%v", f, ok)
	}
}

// Symmetric case: a sell limit priced below the bid fills AT the bid.
func TestSimMarketableSellLimitFillsAtBid_PriceImprovement(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Bids: []feed.BookLevel{{Price: 99.5, Volume: 50}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeLimit, Qty: 10, LimitPrice: 98, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Price != 99.5 || f.F.Qty != 10 {
		t.Fatalf("sell limit below the bid should fill AT the bid (99.5), got %+v ok=%v", f, ok)
	}
}

// TestSimOrderFillsAcrossMultipleBookLevels is the end-to-end (SubmitOrder,
// not the direct fillAgainstBook unit test above) confirmation that an
// order sized larger than the top level's Volume fills across multiple
// levels at the correct size-weighted average price.
func TestSimOrderFillsAcrossMultipleBookLevels(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{
		{Price: 100.0, Volume: 5},
		{Price: 100.1, Volume: 5},
	}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	f, ok := drain(t, b).(exec.OrderFilled)
	want := (5*100.0 + 5*100.1) / 10
	if !ok || f.F.Qty != 10 || math.Abs(f.F.Price-want) > 1e-9 || f.LeavesQty != 0 {
		t.Fatalf("expected full 10-share fill at weighted avg %v, got %+v ok=%v", want, f, ok)
	}
}

// TestSimPartialFillRestsThenCompletesOnLaterSetBook covers depth thinner
// than the order's qty: the first attempt partially fills and the order
// keeps resting (not deleted); a follow-up SetBook with more depth fills
// the remainder and the order disappears from working orders.
func TestSimPartialFillRestsThenCompletesOnLaterSetBook(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 4}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Qty != 4 || f.CumQty != 4 || f.LeavesQty != 6 {
		t.Fatalf("expected partial fill of 4, leaving 6, got %+v ok=%v", f, ok)
	}
	_ = drain(t, b) // BrokerPositions
	_ = drain(t, b) // BrokerAccount

	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Status != exec.StatusPartiallyFilled || orders[0].LeavesQty != 6 {
		t.Fatalf("partially-filled order should still be working: %+v", orders)
	}

	// More depth arrives — the remainder fills and the order stops working.
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100.2, Volume: 100}}})
	f2, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f2.F.Qty != 6 || f2.CumQty != 10 || f2.LeavesQty != 0 || f2.F.Price != 100.2 {
		t.Fatalf("expected the remaining 6 to fill at 100.2, got %+v ok=%v", f2, ok)
	}
	_ = drain(t, b) // BrokerPositions

	_, _, orders, err = b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 0 {
		t.Fatalf("fully-filled order should no longer be working: %+v", orders)
	}
}

// TestSimSetMarkDoesNotRefillPartiallyFilledOrderOffStaleBook is a regression
// guard for the actOnMarkLocked fall-through bug: a plain (never-was-a-stop)
// resting Market/Limit order that has already partially filled off the book
// must NOT be re-priced against that same stale book snapshot just because a
// new SetMark tick arrived with no accompanying SetBook. Only a genuinely
// new SetBook may consume more of the displayed depth.
func TestSimSetMarkDoesNotRefillPartiallyFilledOrderOffStaleBook(t *testing.T) {
	b := newSim(t) // seeds AAPL mark = 100
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 4}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Qty != 4 || f.LeavesQty != 6 {
		t.Fatalf("expected partial fill of 4, leaving 6, got %+v ok=%v", f, ok)
	}
	_ = drain(t, b) // BrokerPositions
	_ = drain(t, b) // BrokerAccount

	// A mark tick arrives with no new SetBook. The order must not touch the
	// already-consumed 4-share depth again.
	b.SetMark("AAPL", 100)
	select {
	case e := <-b.Events():
		t.Fatalf("SetMark alone must not re-fill a plain resting order off a stale book, got %+v", e)
	case <-time.After(100 * time.Millisecond):
	}

	_, positions, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].LeavesQty != 6 || orders[0].Status != exec.StatusPartiallyFilled {
		t.Fatalf("order should remain partially filled at LeavesQty 6, got %+v", orders)
	}
	if len(positions) != 1 || positions[0].Qty != 4 {
		t.Fatalf("position should still reflect only the original 4-share fill, got %+v", positions)
	}
}

func TestSimNonMarketableRestsThenCancel(t *testing.T) {
	b := newSim(t)
	// Buy limit 90 with mark 100 → not marketable → rests.
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 90, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("rested order should emit OrderAccepted only")
	}
	if err := b.CancelOrder(context.Background(), "ET1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := drain(t, b).(exec.OrderCanceled); !ok {
		t.Fatal("cancel should emit OrderCanceled")
	}
	// Canceling an unknown/terminal order errors.
	if err := b.CancelOrder(context.Background(), "ET1"); err == nil {
		t.Fatal("second cancel should error (order gone)")
	}
}

// TestSimSetMarkAloneDoesNotCrossRestingLimit_NoBook is a regression guard
// for the Task 2 pricing-model switch: Limit fills are now book-priced, so
// moving the mark through a resting limit's price no longer fills it by
// itself (the old behavior) when there is no book at all.
func TestSimSetMarkAloneDoesNotCrossRestingLimit_NoBook(t *testing.T) {
	b := newSim(t)
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 95, ClientOrderID: "ET1"}
	_, _ = b.SubmitOrder(context.Background(), req)
	_ = drain(t, b)       // OrderAccepted
	b.SetMark("AAPL", 94) // would have crossed a mark-priced limit; no book exists, so it must not fill
	select {
	case e := <-b.Events():
		t.Fatalf("resting limit with no book must not fill on a mark move alone, got %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 {
		t.Fatalf("order should still be resting: %+v", orders)
	}
}

// TestSimSetBookCrossesRestingLimitOrder is SetBook's crossing analog of the
// old SetMark-crossing test: a resting limit order that never had a book at
// submit time fills once a crossing book arrives via SetBook.
func TestSimSetBookCrossesRestingLimitOrder(t *testing.T) {
	b := newSim(t)
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 95, ClientOrderID: "ET1"}
	_, _ = b.SubmitOrder(context.Background(), req)
	_ = drain(t, b) // OrderAccepted

	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 94.8, Volume: 50}}})
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Price != 94.8 {
		t.Fatalf("SetBook should cross the resting limit at the ask 94.8, got %+v ok=%v", f, ok)
	}
	_ = drain(t, b) // BrokerPositions
}

// TestSimSubmitOrder_CarriesSessionIntoInternalOrder guards against a
// dropped-field regression: the internal *exec.Order sim tracks for its own
// bookkeeping (returned verbatim by Snapshot) must carry Session forward from
// the request, same as Core's own OrderSubmitted Order.
func TestSimSubmitOrder_CarriesSessionIntoInternalOrder(t *testing.T) {
	b := newSim(t)
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit,
		Session: exec.SessionExtended, Qty: 10, LimitPrice: 90, ClientOrderID: "ET1",
	})
	_ = drain(t, b) // OrderAccepted
	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Session != exec.SessionExtended {
		t.Fatalf("snapshot order dropped Session: %+v", orders)
	}
}

func TestSimReplaceAndSnapshot(t *testing.T) {
	b := newSim(t)
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 90, ClientOrderID: "ET1"})
	_ = drain(t, b) // OrderAccepted
	if err := b.ReplaceOrder(context.Background(), "ET1", exec.ReplaceRequest{Qty: 20, LimitPrice: 91}); err != nil {
		t.Fatal(err)
	}
	if r, ok := drain(t, b).(exec.OrderReplaced); !ok || r.NewQty != 20 || r.NewLimit != 91 {
		t.Fatalf("replace event wrong: %+v ok=%v", r, ok)
	}
	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Qty != 20 || orders[0].LimitPrice != 91 {
		t.Fatalf("snapshot orders wrong: %+v", orders)
	}
	if !b.Capabilities().NativeReplace || !b.Capabilities().FlattenAll {
		t.Fatal("SimBroker should advertise native replace + flatten")
	}
}

// TestSimReplaceOrder_RepricedLimitCrossesCurrentBook confirms ReplaceOrder
// still re-evaluates against the book via the same actOnMarkLocked path
// SubmitOrder/crossRestingLocked use — a limit whose new (replaced) price
// now crosses the standing book fills immediately on replace, at the book
// price (not the newly-entered limit).
func TestSimReplaceOrder_RepricedLimitCrossesCurrentBook(t *testing.T) {
	b := newSim(t) // seeds AAPL mark = 100 (replace's fill-attempt gate)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100.5, Volume: 50}}})
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 90, ClientOrderID: "ET1"})
	_ = drain(t, b) // OrderAccepted (90 doesn't cross the 100.5 ask)

	if err := b.ReplaceOrder(context.Background(), "ET1", exec.ReplaceRequest{Qty: 10, LimitPrice: 101}); err != nil {
		t.Fatal(err)
	}
	if r, ok := drain(t, b).(exec.OrderReplaced); !ok || r.NewLimit != 101 {
		t.Fatalf("replace event wrong: %+v ok=%v", r, ok)
	}
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Price != 100.5 || f.F.Qty != 10 {
		t.Fatalf("replace should cross the book at the ask (100.5), got %+v ok=%v", f, ok)
	}
}

// TestNew_SeedsAccountFromStartingCash guards against the boot-balance
// regression: New used to always zero-value acct, so a freshly booted engine
// (Core.Recover -> Broker.Snapshot) showed $0 equity/buying power regardless
// of the configured starting_balance, and only a manual "Reset balance" click
// ever funded the account.
func TestNew_SeedsAccountFromStartingCash(t *testing.T) {
	b := New("sim-1", clock.NewFake(time.UnixMilli(1000)), 75_000, Options{})
	acct, _, _, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acct.Equity != 75_000 || acct.BuyingPower != 75_000 || acct.AvailableCash != 75_000 || acct.SodEquity != 75_000 {
		t.Fatalf("New should seed the account with startingCash, got %+v", acct)
	}
}

// TestSimMarketOrderNoBookRestsThenFillsOnSetBook replaces the old "no mark
// -> reject" behavior: Task 2 removed that rejection entirely. A market
// order with no book yet for its symbol is Accepted and rests (same as a
// non-marketable limit) — not rejected, not filled — until a real book
// arrives.
func TestSimMarketOrderNoBookRestsThenFillsOnSetBook(t *testing.T) {
	b := New("sim-1", clock.NewFake(time.UnixMilli(1000)), 100_000, Options{}) // no SetMark/SetBook — "MSFT" has neither
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "MSFT", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	ack, err := b.SubmitOrder(context.Background(), req)
	if err != nil || !ack.Accepted {
		t.Fatalf("submit: ack=%+v err=%v", ack, err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("first event should be OrderAccepted")
	}
	select {
	case e := <-b.Events():
		t.Fatalf("market order with no book must not reject or fill, got %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Status != exec.StatusAccepted {
		t.Fatalf("market order should still be working while no book exists: %+v", orders)
	}

	b.SetBook("MSFT", feed.Book{Asks: []feed.BookLevel{{Price: 50, Volume: 20}}})
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Price != 50 || f.F.Qty != 10 || f.LeavesQty != 0 {
		t.Fatalf("first SetBook should fill the resting market order, got %+v ok=%v", f, ok)
	}
}

// TestSimMarketOrderFillsAgainstBook confirms a market order prices off the
// book (not a flat "mark"), even when a mark also exists for the symbol.
func TestSimMarketOrderFillsAgainstBook(t *testing.T) {
	b := newSim(t) // seeds AAPL mark = 100 (irrelevant to pricing now)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100.25, Volume: 50}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	ack, err := b.SubmitOrder(context.Background(), req)
	if err != nil || !ack.Accepted {
		t.Fatalf("submit: ack=%+v err=%v", ack, err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("first event should be OrderAccepted")
	}
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Price != 100.25 || f.F.Qty != 10 {
		t.Fatalf("market order should fill at the book ask (100.25), not the mark (100), got %+v ok=%v", f, ok)
	}
	if _, ok := drain(t, b).(exec.BrokerPositions); !ok {
		t.Fatal("fill should be followed by a BrokerPositions snapshot")
	}
}

// --- TIF: IOC and FOK only govern the first fill attempt on submit ---

// TestSimTIFIOC_PartialFillCancelsRemainder: depth thinner than qty fills
// what it can immediately, then IOC cancels the remainder instead of
// resting it.
func TestSimTIFIOC_PartialFillCancelsRemainder(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 4}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFIOC, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Qty != 4 || f.LeavesQty != 6 {
		t.Fatalf("IOC should still fill the immediately-available 4, got %+v ok=%v", f, ok)
	}
	_ = drain(t, b) // BrokerPositions
	_ = drain(t, b) // BrokerAccount
	c, ok := drain(t, b).(exec.OrderCanceled)
	if !ok || c.OID != "ET1" {
		t.Fatalf("IOC should cancel the unfilled remainder instead of resting it, got %+v ok=%v", c, ok)
	}
	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 0 {
		t.Fatalf("IOC order must not be left working after the cancel: %+v", orders)
	}
}

// TestSimTIFIOC_NoBookCancelsImmediately: IOC never rests, even if nothing
// crossed at all.
func TestSimTIFIOC_NoBookCancelsImmediately(t *testing.T) {
	b := New("sim-1", clock.NewFake(time.UnixMilli(1000)), 100_000, Options{})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "MSFT", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFIOC, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	c, ok := drain(t, b).(exec.OrderCanceled)
	if !ok || c.OID != "ET1" {
		t.Fatalf("IOC with no book should cancel immediately (never rest), got %+v ok=%v", c, ok)
	}
}

// TestSimTIFFOK_CannotFillCompletely_RejectsWithNoFill: FOK is all-or-none —
// if the book can't fill the whole order right now, nothing fills at all and
// the order is rejected, leaving orders/positions untouched.
func TestSimTIFFOK_CannotFillCompletely_RejectsWithNoFill(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 4}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFFOK, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	r, ok := drain(t, b).(exec.OrderRejected)
	if !ok || r.OID != "ET1" {
		t.Fatalf("FOK unable to fill completely should reject with no fill, got %+v ok=%v", r, ok)
	}
	_, pos, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 0 {
		t.Fatalf("rejected FOK order must not remain working: %+v", orders)
	}
	for _, p := range pos {
		if p.Qty != 0 {
			t.Fatalf("rejected FOK order must not affect positions: %+v", pos)
		}
	}
}

// TestSimTIFFOK_FullyFillableFillsNormally: FOK that CAN fill completely
// against the current book fills exactly like a Day/GTC order would.
func TestSimTIFFOK_FullyFillableFillsNormally(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, TIF: exec.TIFFOK, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Qty != 10 || f.LeavesQty != 0 {
		t.Fatalf("fully-fillable FOK should fill normally, got %+v ok=%v", f, ok)
	}
}

// drainAll reads all currently-buffered broker events without blocking. Named
// distinctly from the existing single-event drain(t, b) helper above, which
// blocks for exactly one event.
func drainAll(ch <-chan exec.BrokerEvent) []exec.BrokerEvent {
	var out []exec.BrokerEvent
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func filledAt(t *testing.T, evs []exec.BrokerEvent) (exec.OrderFilled, bool) {
	t.Helper()
	for _, e := range evs {
		if f, ok := e.(exec.OrderFilled); ok {
			return f, true
		}
	}
	return exec.OrderFilled{}, false
}

func TestSim_Flatten_ZeroesPositions(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	drain(t, b) // OrderAccepted
	drain(t, b) // OrderFilled
	drain(t, b) // BrokerPositions (from the fill)
	drain(t, b) // BrokerAccount (from the fill)
	if err := b.Flatten(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := drain(t, b).(exec.BrokerPositions); !ok {
		t.Fatal("Flatten should emit a BrokerPositions reconcile")
	}
	_, pos, _, _ := b.Snapshot(context.Background())
	for _, p := range pos {
		if p.Qty != 0 {
			t.Fatalf("Flatten should zero %s, got %v", p.Symbol, p.Qty)
		}
	}
}

// TestSim_BuyStop_TriggersOnMarkAtOrAboveStop covers point 5/7 of the Task 2
// brief: a stop's trigger still keys off the last-trade mark (SetMark), but
// once triggered it prices off the book, not the mark — the ask (101.25) is
// deliberately different from the triggering mark (101) so a test that
// asserted "fills at the mark" would fail here.
func TestSim_BuyStop_TriggersOnMarkAtOrAboveStop(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	b := New("v", clk, 100_000, Options{})
	b.SetMark("AAPL", 95)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 101.25, Volume: 50}}})
	drainAll(b.Events())
	_, err := b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeStop,
		Qty: 10, StopPrice: 100, ClientOrderID: "ET-bstop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := filledAt(t, drainAll(b.Events())); ok {
		t.Fatal("buy stop must rest while mark (95) < stop (100)")
	}
	b.SetMark("AAPL", 101) // crosses the stop -- triggers off the mark
	f, ok := filledAt(t, drainAll(b.Events()))
	if !ok {
		t.Fatal("buy stop must fill once mark reaches the stop")
	}
	if f.AvgPrice != 101.25 {
		t.Fatalf("triggered stop-market prices off the book (101.25), not the mark: got %v", f.AvgPrice)
	}
}

// TestSim_BuyStop_TriggersButRestsWithNoBook: the trigger check itself never
// needs a book (it only compares mark to StopPrice), but once triggered the
// converted market order still follows "rest until book" like any other
// marketable order — a triggered stop is not a special case for that rule.
func TestSim_BuyStop_TriggersButRestsWithNoBook(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	b := New("v", clk, 100_000, Options{})
	b.SetMark("AAPL", 95)
	drainAll(b.Events())
	_, err := b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeStop,
		Qty: 10, StopPrice: 100, ClientOrderID: "ET-bstop2",
	})
	if err != nil {
		t.Fatal(err)
	}
	drainAll(b.Events())
	b.SetMark("AAPL", 101) // triggers, but there is still no book for AAPL
	if _, ok := filledAt(t, drainAll(b.Events())); ok {
		t.Fatal("a triggered stop with no book must rest, not fill")
	}
	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 {
		t.Fatalf("triggered-but-unpriced stop should still be working: %+v", orders)
	}
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 102, Volume: 50}}})
	f, ok := filledAt(t, drainAll(b.Events()))
	if !ok || f.AvgPrice != 102 {
		t.Fatalf("first SetBook after trigger should fill the resting order at the ask, got ok=%v px=%v", ok, f.AvgPrice)
	}
}

func TestSim_SellStop_TriggersOnMarkAtOrBelowStop(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	b := New("v", clk, 100_000, Options{})
	b.SetMark("AAPL", 105)
	b.SetBook("AAPL", feed.Book{Bids: []feed.BookLevel{{Price: 98.75, Volume: 50}}})
	drainAll(b.Events())
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeStop,
		Qty: 10, StopPrice: 100, ClientOrderID: "ET-sstop",
	})
	if _, ok := filledAt(t, drainAll(b.Events())); ok {
		t.Fatal("sell stop must rest while mark (105) > stop (100)")
	}
	b.SetMark("AAPL", 99)
	if f, ok := filledAt(t, drainAll(b.Events())); !ok || f.AvgPrice != 98.75 {
		t.Fatalf("sell stop prices off the book bid (98.75), not the mark (99); ok=%v px=%v", ok, f.AvgPrice)
	}
}

// TestSim_BuyStopLimit_TriggersThenRestsAsLimit: the trigger is still a mark
// comparison (StopPrice vs mark), but once triggered its marketability is a
// BOOK comparison (LimitPrice vs the ask) — a further mark move no longer
// changes anything; only a book update can fill it.
func TestSim_BuyStopLimit_TriggersThenRestsAsLimit(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	b := New("v", clk, 100_000, Options{})
	b.SetMark("AAPL", 95)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 102, Volume: 50}}})
	drainAll(b.Events())
	// stop 100, limit 100.5 buy: on trigger it is a limit buy @100.5.
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeStopLimit,
		Qty: 10, StopPrice: 100, LimitPrice: 100.5, ClientOrderID: "ET-bsl",
	})
	b.SetMark("AAPL", 102) // triggers (>=100), but the book ask (102) is above the 100.5 limit -> rests
	if _, ok := filledAt(t, drainAll(b.Events())); ok {
		t.Fatal("stop-limit must not fill above its limit")
	}
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100.5, Volume: 50}}}) // ask now at the limit
	if f, ok := filledAt(t, drainAll(b.Events())); !ok || f.AvgPrice != 100.5 {
		t.Fatalf("stop-limit should fill at its limit 100.5 once the book crosses it; ok=%v px=%v", ok, f.AvgPrice)
	}
}

// TestSim_ReplaceOrder_StopNotTriggered_DoesNotFillAtZero reproduces the
// final whole-branch review finding: ReplaceOrder's post-replace fill
// decision used to call the raw marketable(o.Side, o.LimitPrice, mark) check
// instead of actOnMarkLocked. A bare TypeStop has LimitPrice == 0 (it prices
// off StopPrice, not LimitPrice), so marketable(Sell, 0, mark) evaluated as
// 0 <= mark, which is true for any positive mark -- a resting sell stop that
// got replaced would fill IMMEDIATELY at price $0, regardless of whether the
// stop had actually triggered. This asserts a resting stop that has NOT
// triggered stays resting (and unfilled) across a replace.
func TestSim_ReplaceOrder_StopNotTriggered_DoesNotFillAtZero(t *testing.T) {
	b := newSim(t) // seeds AAPL mark = 100
	_, err := b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "sim-1", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeStop,
		Qty: 10, StopPrice: 90, ClientOrderID: "ET-stop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("resting stop (mark 100 > stop 90, not triggered) should emit OrderAccepted only")
	}

	if err := b.ReplaceOrder(context.Background(), "ET-stop", exec.ReplaceRequest{Qty: 20}); err != nil {
		t.Fatal(err)
	}
	r, ok := drain(t, b).(exec.OrderReplaced)
	if !ok || r.NewQty != 20 {
		t.Fatalf("replace event wrong: %+v ok=%v", r, ok)
	}
	select {
	case e := <-b.Events():
		t.Fatalf("stop order that hasn't triggered must not fill on replace, got %+v", e)
	case <-time.After(100 * time.Millisecond):
	}

	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Qty != 20 || orders[0].Type != exec.TypeStop {
		t.Fatalf("stop order should remain resting with the new qty and its type: %+v", orders)
	}
}

// TestSim_ReplaceOrder_StopLimitNotTriggered_RawMarketableWouldWronglyFill
// covers the StopLimit half of the same finding: a resting stop-limit whose
// limit price already happens to be "marketable" against the current mark
// must still NOT fill on replace if its stop has not actually triggered.
// The raw marketable(...) check the old code used cannot see the stop at
// all; only actOnMarkLocked's Stop/StopLimit branch evaluates the trigger
// before ever considering the limit.
func TestSim_ReplaceOrder_StopLimitNotTriggered_RawMarketableWouldWronglyFill(t *testing.T) {
	b := newSim(t) // seeds AAPL mark = 100
	_, err := b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "sim-1", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeStopLimit,
		Qty: 10, StopPrice: 40, LimitPrice: 50, ClientOrderID: "ET-sl",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("resting stop-limit (mark 100 > stop 40, not triggered) should emit OrderAccepted only")
	}

	if err := b.ReplaceOrder(context.Background(), "ET-sl", exec.ReplaceRequest{Qty: 15, LimitPrice: 50}); err != nil {
		t.Fatal(err)
	}
	r, ok := drain(t, b).(exec.OrderReplaced)
	if !ok || r.NewQty != 15 {
		t.Fatalf("replace event wrong: %+v ok=%v", r, ok)
	}
	// Raw marketable(Sell, limit=50, mark=100) is true (50 <= 100) -- exactly
	// the buggy shortcut this fix removes -- but the stop (40) has not
	// actually triggered at mark 100, so the order must stay resting.
	select {
	case e := <-b.Events():
		t.Fatalf("stop-limit whose stop hasn't triggered must not fill just because its limit is marketable, got %+v", e)
	case <-time.After(100 * time.Millisecond):
	}

	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Type != exec.TypeStopLimit {
		t.Fatalf("stop-limit order should remain resting as StopLimit (not triggered): %+v", orders)
	}
}

func TestSim_ResetBalance_CancelsFlattensAndSetsAccount(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
	// A resting order that should be canceled.
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "MSFT", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 5, LimitPrice: 90, ClientOrderID: "ET1"})
	drain(t, b) // OrderAccepted (rests: no book for MSFT, so nothing crosses)

	// A filled position that should be flattened.
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET2"})
	drain(t, b) // OrderAccepted
	drain(t, b) // OrderFilled
	drain(t, b) // BrokerPositions (from the fill)
	drain(t, b) // BrokerAccount (from the fill)

	if err := b.ResetBalance(context.Background(), 50_000); err != nil {
		t.Fatal(err)
	}

	evs := drainAll(b.Events())
	var sawCancel, sawPositions, sawAccount bool
	for _, e := range evs {
		switch ev := e.(type) {
		case exec.OrderCanceled:
			if ev.OID != "ET1" {
				t.Fatalf("unexpected cancel: %+v", ev)
			}
			sawCancel = true
		case exec.BrokerPositions:
			sawPositions = true
		case exec.BrokerAccount:
			sawAccount = true
			a := ev.Account
			if a.Equity != 50_000 || a.BuyingPower != 50_000 || a.AvailableCash != 50_000 || a.SodEquity != 50_000 {
				t.Fatalf("account not reset to starting cash: %+v", a)
			}
			if a.Realized != 0 || a.DayPnL != 0 {
				t.Fatalf("realized/day-pnl should reset to zero: %+v", a)
			}
		}
	}
	if !sawCancel || !sawPositions || !sawAccount {
		t.Fatalf("expected cancel+positions+account events, got %+v", evs)
	}

	_, pos, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 0 {
		t.Fatalf("expected no resting orders after reset, got %+v", orders)
	}
	for _, p := range pos {
		if p.Qty != 0 {
			t.Fatalf("expected flat positions after reset, got %+v", p)
		}
	}
}

func TestSim_ResetBalance_AdvertisedCapability(t *testing.T) {
	b := newSim(t)
	if !b.Capabilities().ResetBalance {
		t.Fatal("SimBroker should advertise ResetBalance capability")
	}
}

func TestSimCancelAll(t *testing.T) {
	b := newSim(t)
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 1, LimitPrice: 90, ClientOrderID: "ET1"})
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "MSFT", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 1, LimitPrice: 90, ClientOrderID: "ET2"})
	_, _ = drain(t, b), drain(t, b) // two OrderAccepted
	if err := b.CancelAll(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	got[drain(t, b).(exec.OrderCanceled).OID] = true
	got[drain(t, b).(exec.OrderCanceled).OID] = true
	if !got["ET1"] || !got["ET2"] {
		t.Fatalf("cancel-all should cancel both, got %v", got)
	}
}

// --- Task 3: cash, weighted-average positions, realized/unrealized P&L ---

// lastAccountEvent scans (rather than requiring a specific position) for the
// most recent exec.BrokerAccount in evs, since a fill's cash/position/account
// bookkeeping can be interleaved with other events (OrderFilled,
// BrokerPositions) whose exact count isn't this helper's concern.
func lastAccountEvent(evs []exec.BrokerEvent) (exec.BrokerAccount, bool) {
	var out exec.BrokerAccount
	found := false
	for _, e := range evs {
		if a, ok := e.(exec.BrokerAccount); ok {
			out, found = a, true
		}
	}
	return out, found
}

// TestSimFill_DebitsCashAndEmitsAccount covers the basic cash-on-fill case: a
// buy pays cash (AvailableCash drops by qty*fillPrice) and the fill is
// followed by a BrokerAccount event carrying the updated numbers.
func TestSimFill_DebitsCashAndEmitsAccount(t *testing.T) {
	b := newSim(t) // AAPL mark 100, starting cash 100_000
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	evs := drainAll(b.Events())
	acctEv, ok := lastAccountEvent(evs)
	if !ok {
		t.Fatalf("expected a BrokerAccount event after the fill, got %+v", evs)
	}
	wantCash := 100_000.0 - 10*100
	if acctEv.Account.AvailableCash != wantCash {
		t.Fatalf("AvailableCash = %v, want %v", acctEv.Account.AvailableCash, wantCash)
	}
	// Bought at the same price as the standing mark: cash spent equals the
	// new position's mark-to-market value, so equity is unchanged from
	// starting cash -- exactly what a real fill at cost should do.
	if acctEv.Account.Equity != 100_000 {
		t.Fatalf("Equity = %v, want 100000 (fill at cost shouldn't move equity)", acctEv.Account.Equity)
	}
	acct, _, _, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acct.AvailableCash != wantCash {
		t.Fatalf("Snapshot AvailableCash = %v, want %v", acct.AvailableCash, wantCash)
	}
}

// TestSimSetMark_UpdatesEquityWithoutTouchingCash: while holding a position,
// moving the mark changes Equity (mark-to-market) but must never touch
// AvailableCash -- only a fill moves cash.
func TestSimSetMark_UpdatesEquityWithoutTouchingCash(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"})
	drainAll(b.Events())

	acct, _, _, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cashAfterFill := acct.AvailableCash

	b.SetMark("AAPL", 110)
	evs := drainAll(b.Events())
	acctEv, ok := lastAccountEvent(evs)
	if !ok {
		t.Fatalf("expected a BrokerAccount event when the mark moves for a held symbol, got %+v", evs)
	}
	if acctEv.Account.AvailableCash != cashAfterFill {
		t.Fatalf("AvailableCash changed on a mark move alone: got %v, want %v", acctEv.Account.AvailableCash, cashAfterFill)
	}
	wantEquity := cashAfterFill + 10*110
	if acctEv.Account.Equity != wantEquity {
		t.Fatalf("Equity = %v, want %v (cash + qty*newMark)", acctEv.Account.Equity, wantEquity)
	}

	b.SetMark("AAPL", 90)
	evs = drainAll(b.Events())
	acctEv, ok = lastAccountEvent(evs)
	if !ok {
		t.Fatalf("expected a BrokerAccount event on the down move too, got %+v", evs)
	}
	wantEquity = cashAfterFill + 10*90
	if acctEv.Account.Equity != wantEquity {
		t.Fatalf("Equity = %v, want %v after the down move", acctEv.Account.Equity, wantEquity)
	}
}

// TestSimFill_ClosingProfitableLongRealizesPnL: selling a profitable long to
// flat realizes the correct P&L, DayPnL reflects it, and the position
// flattens exactly to zero.
func TestSimFill_ClosingProfitableLongRealizesPnL(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"})
	drainAll(b.Events())

	b.SetBook("AAPL", feed.Book{Bids: []feed.BookLevel{{Price: 110, Volume: 50}}})
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET2"})
	evs := drainAll(b.Events())
	acctEv, ok := lastAccountEvent(evs)
	if !ok {
		t.Fatalf("expected a BrokerAccount event after the closing fill, got %+v", evs)
	}
	if acctEv.Account.Realized != 100 {
		t.Fatalf("Realized = %v, want 100 ((110-100)*10)", acctEv.Account.Realized)
	}
	wantEquity := 100_000.0 + 100 // starting cash + realized profit, flat position
	if acctEv.Account.Equity != wantEquity {
		t.Fatalf("Equity = %v, want %v", acctEv.Account.Equity, wantEquity)
	}
	if acctEv.Account.DayPnL != 100 {
		t.Fatalf("DayPnL = %v, want 100", acctEv.Account.DayPnL)
	}

	_, pos, _, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pos {
		if p.Symbol == "AAPL" && p.Qty != 0 {
			t.Fatalf("AAPL position should be flat after closing the whole long, got %+v", p)
		}
	}
}

// TestSimFill_AddingToPositionWeightsAveragePrice: scaling into an existing
// long at a different price produces a size-weighted average cost, not the
// latest fill price.
func TestSimFill_AddingToPositionWeightsAveragePrice(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"})
	drainAll(b.Events())

	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 110, Volume: 50}}})
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET2"})
	drainAll(b.Events())

	_, pos, _, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var p exec.Position
	for _, pp := range pos {
		if pp.Symbol == "AAPL" {
			p = pp
		}
	}
	if p.Qty != 20 {
		t.Fatalf("Qty = %v, want 20", p.Qty)
	}
	want := (10*100.0 + 10*110.0) / 20
	if math.Abs(p.AvgPrice-want) > 1e-9 {
		t.Fatalf("AvgPrice = %v, want %v (size-weighted average, not the latest fill price 110)", p.AvgPrice, want)
	}
}

// TestSimFill_FlipThroughFlatRealizesThenOpensOpposite: selling more than a
// long position holds realizes P&L on the closed portion, then opens a new
// short with the excess at the fill price as its AvgPrice.
func TestSimFill_FlipThroughFlatRealizesThenOpensOpposite(t *testing.T) {
	b := newSim(t)
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"})
	drainAll(b.Events())

	b.SetBook("AAPL", feed.Book{Bids: []feed.BookLevel{{Price: 120, Volume: 50}}})
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeMarket, Qty: 15, ClientOrderID: "ET2"})
	evs := drainAll(b.Events())
	acctEv, ok := lastAccountEvent(evs)
	if !ok {
		t.Fatalf("expected a BrokerAccount event after the flip, got %+v", evs)
	}
	wantRealized := (120.0 - 100.0) * 10
	if acctEv.Account.Realized != wantRealized {
		t.Fatalf("Realized = %v, want %v (only the 10 closed shares realize)", acctEv.Account.Realized, wantRealized)
	}

	_, pos, _, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var p exec.Position
	for _, pp := range pos {
		if pp.Symbol == "AAPL" {
			p = pp
		}
	}
	if p.Qty != -5 {
		t.Fatalf("Qty = %v, want -5 (short the 5-share excess)", p.Qty)
	}
	if p.AvgPrice != 120 {
		t.Fatalf("AvgPrice = %v, want 120 (the flip's fill price, not blended with the old long's cost)", p.AvgPrice)
	}
}

// TestSimFill_PositionWithNoMarkFallsBackToAvgPriceForEquity: a symbol that
// has never had a mark (SetMark never called) must still contribute to
// Equity via its position's AvgPrice, not read as a silent zero.
func TestSimFill_PositionWithNoMarkFallsBackToAvgPriceForEquity(t *testing.T) {
	b := New("sim-1", clock.NewFake(time.UnixMilli(1000)), 100_000, Options{}) // no SetMark for TSLA anywhere
	b.SetBook("TSLA", feed.Book{Asks: []feed.BookLevel{{Price: 200, Volume: 50}}})
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "TSLA", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 5, ClientOrderID: "ET1"})
	evs := drainAll(b.Events())
	acctEv, ok := lastAccountEvent(evs)
	if !ok {
		t.Fatalf("expected a BrokerAccount event after the fill, got %+v", evs)
	}
	gotContribution := acctEv.Account.Equity - acctEv.Account.AvailableCash
	wantContribution := 5 * 200.0
	if gotContribution != wantContribution {
		t.Fatalf("position MTM contribution = %v, want %v -- Equity must fall back to qty*AvgPrice when no mark exists, not read as zero", gotContribution, wantContribution)
	}
}

// --- Task 4: end-to-end SubmitOrder/New coverage for SlippageBps ---

// TestSim_ZeroSlippageOptions_MatchesPreTask4Behavior is the broker-level
// regression check: a Broker built with the zero-value Options{} (as every
// pre-Task-4 call site now passes) reproduces the exact price-improvement
// numbers Task 2 established, byte-for-byte.
func TestSim_ZeroSlippageOptions_MatchesPreTask4Behavior(t *testing.T) {
	b := newSim(t) // Options{} -- slippage off
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100.5, Volume: 50}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 102, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Price != 100.5 || f.F.Qty != 10 {
		t.Fatalf("slippage off should fill AT the ask (100.5), exactly as before Task 4, got %+v ok=%v", f, ok)
	}
}

// TestSim_SlippageBps_BuyMarketFillsAboveAsk is the SubmitOrder-level
// end-to-end confirmation of the pure fillAgainstBook slippage tests above.
func TestSim_SlippageBps_BuyMarketFillsAboveAsk(t *testing.T) {
	b := newSimWithSlippage(t, 25) // 25 bps = 0.25%
	b.SetBook("AAPL", feed.Book{Asks: []feed.BookLevel{{Price: 100, Volume: 50}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	f, ok := drain(t, b).(exec.OrderFilled)
	want := 100 * 1.0025
	if !ok || math.Abs(f.F.Price-want) > 1e-9 {
		t.Fatalf("buy fill = %+v ok=%v, want price %v (strictly above the raw ask 100)", f, ok, want)
	}
	if f.F.Price <= 100 {
		t.Fatalf("fill price %v must be strictly above the raw ask 100", f.F.Price)
	}
}

// TestSim_SlippageBps_SellMarketFillsBelowBid is the symmetric sell case.
func TestSim_SlippageBps_SellMarketFillsBelowBid(t *testing.T) {
	b := newSimWithSlippage(t, 25)
	b.SetBook("AAPL", feed.Book{Bids: []feed.BookLevel{{Price: 100, Volume: 50}}})
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	_ = drain(t, b) // OrderAccepted
	f, ok := drain(t, b).(exec.OrderFilled)
	want := 100 * 0.9975
	if !ok || math.Abs(f.F.Price-want) > 1e-9 {
		t.Fatalf("sell fill = %+v ok=%v, want price %v (strictly below the raw bid 100)", f, ok, want)
	}
	if f.F.Price >= 100 {
		t.Fatalf("fill price %v must be strictly below the raw bid 100", f.F.Price)
	}
}
