package alpaca

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

// newTestAdapter is a Task 12 stand-in for the real constructor Task 15 will
// add alongside the full Adapter (REST client, WS client, wiring). Keep it
// consistent with the Adapter fields declared in alpaca.go.
func newTestAdapter(t *testing.T, v exec.VenueID) *Adapter {
	t.Helper()
	return &Adapter{venue: v, seenExecIDs: map[string]bool{}, sideByID: map[string]exec.Side{}, posBasis: map[string]posBasisEntry{}}
}

func loadUpdate(t *testing.T, name string) tradeUpdate {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var tu tradeUpdate
	if err := json.Unmarshal(b, &tu); err != nil {
		t.Fatal(err)
	}
	return tu
}

func fills(evs []exec.BrokerEvent) []exec.OrderFilled {
	var out []exec.OrderFilled
	for _, e := range evs {
		if f, ok := e.(exec.OrderFilled); ok {
			out = append(out, f)
		}
	}
	return out
}

func positions(evs []exec.BrokerEvent) []exec.BrokerPositions {
	var out []exec.BrokerPositions
	for _, e := range evs {
		if p, ok := e.(exec.BrokerPositions); ok {
			out = append(out, p)
		}
	}
	return out
}

func hasPosition(evs []exec.BrokerEvent, symbol string, qty float64) bool {
	for _, bp := range positions(evs) {
		for _, p := range bp.Positions {
			if p.Symbol == symbol && p.Qty == qty {
				return true
			}
		}
	}
	return false
}

func TestNormalizeUpdate_FillDedupOnExecutionID(t *testing.T) {
	a := newTestAdapter(t, "alpaca")
	tu := loadUpdate(t, "fill.json")
	evs := a.normalizeUpdate("alpaca", tu)
	if len(fills(evs)) != 1 {
		t.Fatalf("want 1 fill, got %d", len(fills(evs)))
	}
	f := fills(evs)[0]
	if f.F.Qty != 40 || f.AvgPrice != 190.48 || f.CumQty != 40 || f.F.OrderID != "ET01J0000000000000000000BB" {
		t.Fatalf("fill = %+v", f)
	}
	// the wire's bare "AAPL" must be re-prefixed to the domain "US.AAPL" convention.
	if f.F.Symbol != "US.AAPL" {
		t.Fatalf("fill symbol = %q, want US.AAPL", f.F.Symbol)
	}
	// same execution_id again -> no duplicate fill
	if len(fills(a.normalizeUpdate("alpaca", tu))) != 0 {
		t.Fatal("duplicate execution_id re-emitted a fill")
	}
	// a BrokerPositions with position_qty=40 accompanies the fill
	if !hasPosition(evs, "US.AAPL", 40) {
		t.Fatal("expected BrokerPositions position_qty=40")
	}
}

func TestNormalizeUpdate_PartialFillDistinctExecutionID(t *testing.T) {
	a := newTestAdapter(t, "alpaca")
	a.normalizeUpdate("alpaca", loadUpdate(t, "fill.json"))
	evs := a.normalizeUpdate("alpaca", loadUpdate(t, "partial_fill.json"))
	if len(fills(evs)) != 1 {
		t.Fatalf("want 1 fill for distinct execution_id, got %d", len(fills(evs)))
	}
	f := fills(evs)[0]
	if f.F.Qty != 20 || f.AvgPrice != 190.52 || f.CumQty != 60 {
		t.Fatalf("fill = %+v", f)
	}
	if !hasPosition(evs, "US.AAPL", 60) {
		t.Fatal("expected BrokerPositions position_qty=60")
	}
}

func TestNormalizeUpdate_New(t *testing.T) {
	a := newTestAdapter(t, "alpaca")
	evs := a.normalizeUpdate("alpaca", loadUpdate(t, "new.json"))
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d: %+v", len(evs), evs)
	}
	acc, ok := evs[0].(exec.OrderAccepted)
	if !ok {
		t.Fatalf("want OrderAccepted, got %T", evs[0])
	}
	if acc.OID != "ET01J0000000000000000000CC" || acc.BrokerOrderID != "b-2" {
		t.Fatalf("accepted = %+v", acc)
	}
}

func TestNormalizeUpdate_Canceled(t *testing.T) {
	a := newTestAdapter(t, "alpaca")
	evs := a.normalizeUpdate("alpaca", loadUpdate(t, "canceled.json"))
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	c, ok := evs[0].(exec.OrderCanceled)
	if !ok || c.OID != "ET01J0000000000000000000DD" {
		t.Fatalf("canceled = %+v (%T)", evs[0], evs[0])
	}
}

func TestNormalizeUpdate_Replaced(t *testing.T) {
	a := newTestAdapter(t, "alpaca")
	evs := a.normalizeUpdate("alpaca", loadUpdate(t, "replaced.json"))
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	r, ok := evs[0].(exec.OrderReplaced)
	if !ok || r.OID != "ET01J0000000000000000000EE" {
		t.Fatalf("replaced = %+v (%T)", evs[0], evs[0])
	}
}

func TestNormalizeUpdate_Rejected(t *testing.T) {
	a := newTestAdapter(t, "alpaca")
	evs := a.normalizeUpdate("alpaca", loadUpdate(t, "rejected.json"))
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	r, ok := evs[0].(exec.OrderRejected)
	if !ok || r.OID != "ET01J0000000000000000000FF" || r.Reason == "" {
		t.Fatalf("rejected = %+v (%T)", evs[0], evs[0])
	}
}

func TestNormalizeUpdate_DoneForDayMapsToExpired(t *testing.T) {
	a := newTestAdapter(t, "alpaca")
	tu := loadUpdate(t, "canceled.json")
	tu.Event = "done_for_day"
	evs := a.normalizeUpdate("alpaca", tu)
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	if _, ok := evs[0].(exec.OrderExpired); !ok {
		t.Fatalf("want OrderExpired, got %T", evs[0])
	}
}

func TestNormalizeUpdate_RareEventsIgnored(t *testing.T) {
	a := newTestAdapter(t, "alpaca")
	for _, ev := range []string{
		"pending_new", "pending_cancel", "pending_replace",
		"stopped", "suspended", "calculated",
		"order_replace_rejected", "order_cancel_rejected",
	} {
		tu := loadUpdate(t, "new.json")
		tu.Event = ev
		if evs := a.normalizeUpdate("alpaca", tu); len(evs) != 0 {
			t.Fatalf("event %q: want 0 domain events, got %d: %+v", ev, len(evs), evs)
		}
	}
}

// TestNormalizeUpdate_Fill_AddsUSPrefixToSymbol proves trade_updates fill
// events (which carry Alpaca's bare symbol, e.g. "AAPL") get tagged with
// eTape's domain "US." prefix on both the Fill and the accompanying
// BrokerPositions — the same inbound half of the fix rest_test.go covers for
// the REST snapshot path.
func TestNormalizeUpdate_Fill_AddsUSPrefixToSymbol(t *testing.T) {
	a := newTestAdapter(t, "alpaca")
	tu := loadUpdate(t, "fill.json")
	evs := a.normalizeUpdate("alpaca", tu)
	if len(fills(evs)) != 1 || fills(evs)[0].F.Symbol != "US.AAPL" {
		t.Fatalf("fill symbol = %+v, want US.AAPL", fills(evs))
	}
	if !hasPosition(evs, "US.AAPL", 40) {
		t.Fatalf("expected BrokerPositions symbol US.AAPL, got %+v", positions(evs))
	}
}

func TestNormalizeUpdate_FillBasis_SameDirectionAdd(t *testing.T) {
	// Buy 40 @ 190.48 opens position (no cache entry → avg = fill price).
	// Buy 20 more @ 190.60 → same direction → weighted avg.
	a := newTestAdapter(t, "alpaca")

	// First fill: opens position.
	tu := loadUpdate(t, "fill.json") // buy 40, position_qty=40
	evs := a.normalizeUpdate("alpaca", tu)
	p := positions(evs)[0]
	if len(p.Positions) != 1 {
		t.Fatalf("want 1 position, got %d", len(p.Positions))
	}
	pos := p.Positions[0]
	if pos.Qty != 40 {
		t.Fatalf("qty = %v, want 40", pos.Qty)
	}
	if pos.AvgPrice != 190.48 {
		t.Fatalf("avgPrice = %v, want 190.48", pos.AvgPrice)
	}

	// Second fill: same direction add.
	tu2 := loadUpdate(t, "partial_fill.json") // buy 20, position_qty=60
	evs2 := a.normalizeUpdate("alpaca", tu2)
	p2 := positions(evs2)[0]
	pos2 := p2.Positions[0]
	if pos2.Qty != 60 {
		t.Fatalf("qty = %v, want 60", pos2.Qty)
	}
	expectedAvg := (40*190.48 + 20*190.60) / 60
	if pos2.AvgPrice != expectedAvg {
		t.Fatalf("avgPrice = %v, want %v", pos2.AvgPrice, expectedAvg)
	}
}

func TestNormalizeUpdate_FillBasis_PartialExit(t *testing.T) {
	// Cache: long 60 @ 190.50. Sell 20 → position_qty=40, same direction.
	// Partial exit → avg unchanged.
	a := newTestAdapter(t, "alpaca")
	a.posMu.Lock()
	a.posBasis["US.AAPL"] = posBasisEntry{qty: 60, avgAvg: 190.50}
	a.posMu.Unlock()

	// Sell 20, position_qty goes from 60→40.
	tu := tradeUpdate{
		Event:       "partial_fill",
		ExecutionID: "e-sell",
		Price:       numString(191.00),
		Qty:         numString(20),
		PositionQty: numString(40),
		Timestamp:   "2026-07-06T14:00:00Z",
		Order: auOrder{
			ClientOrderID: "ord-sell",
			Symbol:        "AAPL",
			Side:          "sell",
		},
	}
	evs := a.normalizeUpdate("alpaca", tu)
	p := positions(evs)[0]
	pos := p.Positions[0]
	if pos.Qty != 40 {
		t.Fatalf("qty = %v, want 40", pos.Qty)
	}
	if pos.AvgPrice != 190.50 {
		t.Fatalf("avgPrice = %v, want 190.50 (unchanged)", pos.AvgPrice)
	}
}

func TestNormalizeUpdate_FillBasis_DirectionReversal(t *testing.T) {
	// Cache: short 40 @ 190.50. Buy 60 → position_qty=20, reversal.
	// New basis = fill price.
	a := newTestAdapter(t, "alpaca")
	a.posMu.Lock()
	a.posBasis["US.AAPL"] = posBasisEntry{qty: -40, avgAvg: 190.50}
	a.posMu.Unlock()

	// Buy 60, position_qty goes from -40→20.
	tu := tradeUpdate{
		Event:       "fill",
		ExecutionID: "e-buy",
		Price:       numString(192.00),
		Qty:         numString(60),
		PositionQty: numString(20),
		Timestamp:   "2026-07-06T14:00:00Z",
		Order: auOrder{
			ClientOrderID: "ord-buy",
			Symbol:        "AAPL",
			Side:          "buy",
		},
	}
	evs := a.normalizeUpdate("alpaca", tu)
	p := positions(evs)[0]
	pos := p.Positions[0]
	if pos.Qty != 20 {
		t.Fatalf("qty = %v, want 20", pos.Qty)
	}
	if pos.AvgPrice != 192.00 {
		t.Fatalf("avgPrice = %v, want 192.00 (reversal)", pos.AvgPrice)
	}
}

func TestNormalizeUpdate_FillBasis_FlatDeletesEntry(t *testing.T) {
	// Cache: long 40 @ 190.48. Sell 40 → position_qty=0.
	// Delete cache entry, avgPrice=0.
	a := newTestAdapter(t, "alpaca")
	a.posMu.Lock()
	a.posBasis["US.AAPL"] = posBasisEntry{qty: 40, avgAvg: 190.48}
	a.posMu.Unlock()

	// Sell 40, position_qty goes from 40→0.
	tu := tradeUpdate{
		Event:       "fill",
		ExecutionID: "e-flat",
		Price:       numString(191.00),
		Qty:         numString(40),
		PositionQty: numString(0),
		Timestamp:   "2026-07-06T14:00:00Z",
		Order: auOrder{
			ClientOrderID: "ord-flat",
			Symbol:        "AAPL",
			Side:          "sell",
		},
	}
	evs := a.normalizeUpdate("alpaca", tu)
	p := positions(evs)[0]
	pos := p.Positions[0]
	if pos.Qty != 0 {
		t.Fatalf("qty = %v, want 0", pos.Qty)
	}
	if pos.AvgPrice != 0 {
		t.Fatalf("avgPrice = %v, want 0 (flat)", pos.AvgPrice)
	}
	a.posMu.Lock()
	if _, ok := a.posBasis["US.AAPL"]; ok {
		t.Fatal("cache entry should be deleted for flat position")
	}
	a.posMu.Unlock()
}

func TestNumString_ParsesQuotedAndBareNumbers(t *testing.T) {
	var n numString
	if err := json.Unmarshal([]byte(`"190.48"`), &n); err != nil || float64(n) != 190.48 {
		t.Fatalf("quoted: n=%v err=%v", n, err)
	}
	if err := json.Unmarshal([]byte(`190.48`), &n); err != nil || float64(n) != 190.48 {
		t.Fatalf("bare: n=%v err=%v", n, err)
	}
	if err := json.Unmarshal([]byte(`""`), &n); err != nil || float64(n) != 0 {
		t.Fatalf("empty: n=%v err=%v", n, err)
	}
}
