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

// TestSimIntegration_BookWiredDepthFillLatencySlippageEquityAndPnL is Task
// 6's end-to-end proof that Tasks 1-5's pieces work together as ONE broker,
// not just in isolation (each task's own unit tests already cover that in
// sim_test.go): L2 book wiring (SetBook), depth-aware partial fills that
// rest until a deeper book completes them, submit->fill latency gating,
// adverse slippage pricing, mark-to-market equity vs. cash, and a closing
// fill's realized P&L. No mocking beyond clock.NewFake -- every other piece
// (Broker, fillAgainstBook, slippedPrice, the account/position bookkeeping)
// is the real production code.
//
// Every "want" value below is derived from the SAME formulas sim.go itself
// uses (slippedPrice for a level's adverse-adjusted price, a size-weighted
// average across levels/fills for cost basis, and fillLocked's
// (px-AvgPrice)*qty*longSign realized-P&L formula) rather than a
// hand-computed literal, so this test doubles as a worked numeric example
// of the whole feature -- comment blocks below narrate each phase.
func TestSimIntegration_BookWiredDepthFillLatencySlippageEquityAndPnL(t *testing.T) {
	const slippageBps = 50.0 // 0.50% adverse cost applied to every consumed level
	const latencyMs = 300    // submit->fill delay, gating EVERY order's first eligible attempt
	const startingCash = 100_000.0

	clk := clock.NewFake(time.UnixMilli(1_000))
	b := New("sim-1", clk, startingCash, Options{SlippageBps: slippageBps, FillLatencyMs: latencyMs})

	// --- Phase 1: seed the book (bid/ask, multiple levels each) and mark ---
	// The ask side is deliberately thin (2 levels, 6 shares total) relative
	// to the 20-share buy submitted in Phase 2, so it can only partially
	// fill until a deeper ask book arrives in Phase 4. The bid side (2
	// levels, 100 shares total) is seeded now too, matching a real full-book
	// snapshot, even though it stays unused until the closing sell (Phase 6).
	thinAsks := []feed.BookLevel{{Price: 100.00, Volume: 4}, {Price: 100.25, Volume: 2}}
	seedBids := []feed.BookLevel{{Price: 99.50, Volume: 50}, {Price: 99.00, Volume: 50}}
	b.SetBook("AAPL", feed.Book{Symbol: "AAPL", Asks: thinAsks, Bids: seedBids})
	b.SetMark("AAPL", 100)
	if evs := drainAll(b.Events()); len(evs) != 0 {
		t.Fatalf("seeding a book/mark with no resting orders or positions yet should emit nothing, got %+v", evs)
	}

	// --- Phase 2: submit a limit buy larger than the visible ask depth ---
	buyReq := exec.OrderRequest{
		Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit,
		Qty: 20, LimitPrice: 102, ClientOrderID: "ET-BUY",
	}
	if _, err := b.SubmitOrder(context.Background(), buyReq); err != nil {
		t.Fatal(err)
	}
	// FillLatencyMs=300 blocks the submit-time attempt entirely: b.now() ==
	// the order's own CreatedMs here, strictly before its own deadline, so
	// attemptBookFillLocked's eligibility gate returns nil without even
	// calling fillAgainstBook -- only OrderAccepted is emitted, proving
	// latency gating applies even to an otherwise-immediately-marketable
	// order on its very first attempt.
	evs := drainAll(b.Events())
	if len(evs) != 1 {
		t.Fatalf("expected only OrderAccepted while the latency window is open, got %+v", evs)
	}
	if _, ok := evs[0].(exec.OrderAccepted); !ok {
		t.Fatalf("expected OrderAccepted, got %+v", evs[0])
	}

	// --- Phase 3: the latency window elapses; the SAME thin book now
	// yields a genuine partial fill (only the 6 visible shares) ---
	clk.Advance(latencyMs * time.Millisecond)
	b.SetBook("AAPL", feed.Book{Symbol: "AAPL", Asks: thinAsks, Bids: seedBids})
	evs = drainAll(b.Events())
	fill1, ok := filledAt(t, evs)
	if !ok {
		t.Fatalf("expected a partial fill once eligible, got %+v", evs)
	}
	wantPx1 := (4*slippedPrice(exec.SideBuy, 100.00, slippageBps) + 2*slippedPrice(exec.SideBuy, 100.25, slippageBps)) / 6
	if fill1.F.Qty != 6 || math.Abs(fill1.F.Price-wantPx1) > 1e-9 {
		t.Fatalf("fill1 = %+v, want qty=6 price=%v", fill1, wantPx1)
	}
	if fill1.CumQty != 6 || fill1.LeavesQty != 14 {
		t.Fatalf("fill1 cum/leaves = %v/%v, want 6/14", fill1.CumQty, fill1.LeavesQty)
	}
	acctEv1, ok := lastAccountEvent(evs)
	if !ok {
		t.Fatal("expected a BrokerAccount event after the partial fill")
	}
	wantCash1 := startingCash - 6*wantPx1
	if math.Abs(acctEv1.Account.AvailableCash-wantCash1) > 1e-9 {
		t.Fatalf("AvailableCash after partial fill = %v, want %v (only the filled 6 shares charged, not all 20)", acctEv1.Account.AvailableCash, wantCash1)
	}
	if _, _, orders, err := b.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	} else if len(orders) != 1 || orders[0].Status != exec.StatusPartiallyFilled || orders[0].LeavesQty != 14 {
		t.Fatalf("order should still be resting PartiallyFilled with 14 left, got %+v", orders)
	}

	// --- Phase 4: a deeper book arrives. Latency never re-gates an already-
	// eligible order (b.now() only moves forward), so this fills the
	// remainder immediately ---
	deepAsk := feed.BookLevel{Price: 100.50, Volume: 100}
	b.SetBook("AAPL", feed.Book{Symbol: "AAPL", Asks: []feed.BookLevel{deepAsk}})
	evs = drainAll(b.Events())
	fill2, ok := filledAt(t, evs)
	if !ok {
		t.Fatalf("expected the remainder to fill against the deeper book, got %+v", evs)
	}
	wantPx2 := slippedPrice(exec.SideBuy, deepAsk.Price, slippageBps)
	if fill2.F.Qty != 14 || math.Abs(fill2.F.Price-wantPx2) > 1e-9 {
		t.Fatalf("fill2 = %+v, want qty=14 price=%v", fill2, wantPx2)
	}
	if fill2.CumQty != 20 || fill2.LeavesQty != 0 {
		t.Fatalf("fill2 cum/leaves = %v/%v, want 20/0 (order fully filled)", fill2.CumQty, fill2.LeavesQty)
	}
	_, positions, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 0 {
		t.Fatalf("fully-filled order should no longer be resting, got %+v", orders)
	}
	var pos exec.Position
	for _, p := range positions {
		if p.Symbol == "AAPL" {
			pos = p
		}
	}
	wantAvgCost := (6*wantPx1 + 14*wantPx2) / 20
	if pos.Qty != 20 || math.Abs(pos.AvgPrice-wantAvgCost) > 1e-9 {
		t.Fatalf("position = %+v, want qty=20 avgPrice=%v", pos, wantAvgCost)
	}
	acctEv2, ok := lastAccountEvent(evs)
	if !ok {
		t.Fatal("expected a BrokerAccount event after the completing fill")
	}
	wantCashAfterBuy := startingCash - 6*wantPx1 - 14*wantPx2
	if math.Abs(acctEv2.Account.AvailableCash-wantCashAfterBuy) > 1e-9 {
		t.Fatalf("AvailableCash after full fill = %v, want %v", acctEv2.Account.AvailableCash, wantCashAfterBuy)
	}

	// --- Phase 5: move the mark -- equity (MTM) must move, cash must not ---
	b.SetMark("AAPL", 110)
	evs = drainAll(b.Events())
	acctEv3, ok := lastAccountEvent(evs)
	if !ok {
		t.Fatal("expected a BrokerAccount event when the mark moves for a held symbol")
	}
	if math.Abs(acctEv3.Account.AvailableCash-wantCashAfterBuy) > 1e-9 {
		t.Fatalf("AvailableCash moved on a mark-only update: got %v, want unchanged %v", acctEv3.Account.AvailableCash, wantCashAfterBuy)
	}
	wantEquityAfterMark := wantCashAfterBuy + 20*110
	if math.Abs(acctEv3.Account.Equity-wantEquityAfterMark) > 1e-9 {
		t.Fatalf("Equity after mark move = %v, want %v (cash + qty*newMark)", acctEv3.Account.Equity, wantEquityAfterMark)
	}

	// --- Phase 6: a closing sell realizes P&L ---
	sellReq := exec.OrderRequest{
		Venue: "sim-1", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeLimit,
		Qty: 20, LimitPrice: 95, ClientOrderID: "ET-SELL",
	}
	if _, err := b.SubmitOrder(context.Background(), sellReq); err != nil {
		t.Fatal(err)
	}
	// Same latency proof as Phase 2, now on the CLOSING order: its
	// eligibility deadline is measured from ITS OWN submission time, not
	// inherited or reused from the buy -- so it too must wait out its own
	// window before it can fill.
	evs = drainAll(b.Events())
	if len(evs) != 1 {
		t.Fatalf("closing sell should also be latency-gated on submission, got %+v", evs)
	}
	if _, ok := evs[0].(exec.OrderAccepted); !ok {
		t.Fatalf("expected OrderAccepted, got %+v", evs[0])
	}

	clk.Advance(latencyMs * time.Millisecond)
	closingBid := feed.BookLevel{Price: 105, Volume: 50}
	b.SetBook("AAPL", feed.Book{Symbol: "AAPL", Bids: []feed.BookLevel{closingBid}})
	evs = drainAll(b.Events())
	closeFill, ok := filledAt(t, evs)
	if !ok {
		t.Fatalf("expected the closing sell to fill once eligible, got %+v", evs)
	}
	wantClosePx := slippedPrice(exec.SideSell, closingBid.Price, slippageBps)
	if closeFill.F.Qty != 20 || math.Abs(closeFill.F.Price-wantClosePx) > 1e-9 {
		t.Fatalf("closing fill = %+v, want qty=20 price=%v", closeFill, wantClosePx)
	}
	acctEv4, ok := lastAccountEvent(evs)
	if !ok {
		t.Fatal("expected a BrokerAccount event after the closing fill")
	}
	wantRealized := (wantClosePx - wantAvgCost) * 20
	if math.Abs(acctEv4.Account.Realized-wantRealized) > 1e-9 {
		t.Fatalf("Realized = %v, want %v ((closePx-avgCost)*20)", acctEv4.Account.Realized, wantRealized)
	}
	if math.Abs(acctEv4.Account.DayPnL-wantRealized) > 1e-9 {
		t.Fatalf("DayPnL = %v, want %v (flat position, so DayPnL == Realized)", acctEv4.Account.DayPnL, wantRealized)
	}
	wantFinalCash := wantCashAfterBuy + 20*wantClosePx
	if math.Abs(acctEv4.Account.AvailableCash-wantFinalCash) > 1e-9 {
		t.Fatalf("AvailableCash after close = %v, want %v", acctEv4.Account.AvailableCash, wantFinalCash)
	}
	// Algebraic identity check independent of the two intermediate fill
	// prices: starting cash + realized P&L, since the position ends flat --
	// exactly the invariant TestSimFill_ClosingProfitableLongRealizesPnL
	// checks for a single fill, now holding across a multi-fill buy and a
	// separately-latency-gated closing sell.
	wantFinalEquity := startingCash + wantRealized
	if math.Abs(acctEv4.Account.Equity-wantFinalEquity) > 1e-9 {
		t.Fatalf("Equity after close = %v, want %v (starting cash + realized P&L, flat position)", acctEv4.Account.Equity, wantFinalEquity)
	}
	_, positions, orders, err = b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 0 {
		t.Fatalf("closing order should no longer be resting, got %+v", orders)
	}
	for _, p := range positions {
		if p.Symbol == "AAPL" && p.Qty != 0 {
			t.Fatalf("AAPL position should be flat after the closing sell, got %+v", p)
		}
	}
}
