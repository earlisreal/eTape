package tradezero

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

// newTestAdapter is a Task 7 stand-in for the real constructor Task 10 will
// add alongside the full Adapter (REST client, WS client, wiring). Keep it
// consistent with the Adapter fields declared in tradezero.go.
func newTestAdapter(t *testing.T, v exec.VenueID) *Adapter {
	t.Helper()
	return &Adapter{venue: v, seenExecuted: map[string]float64{}}
}

func loadOrder(t *testing.T, name string) tzOrder {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var o tzOrder
	if err := json.Unmarshal(b, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestSplitUserOrderID(t *testing.T) {
	acct, cid := splitUserOrderID("2TZ00001:ET01J000000000000000000001")
	if acct != "2TZ00001" || cid != "ET01J000000000000000000001" {
		t.Fatalf("split = %q,%q", acct, cid)
	}
	// clientOrderId may itself contain ':' in the derived replace suffix — split only on the first.
	_, cid = splitUserOrderID("2TZ00001:ET...-r1")
	if cid != "ET...-r1" {
		t.Fatalf("cid = %q", cid)
	}
}

func TestNormalizeOrder_PartialFillEmitsOneFill(t *testing.T) {
	a := newTestAdapter(t, "tz") // constructor stub; see Task 10 (or a minimal local one)
	evs := a.normalizeOrder("tz", loadOrder(t, "order_partial_fill.json"))
	var fills, updates int
	for _, e := range evs {
		switch f := e.(type) {
		case exec.OrderFilled:
			fills++
			if f.F.Qty != 40 || f.AvgPrice != 190.48 || f.CumQty != 40 || f.LeavesQty != 60 {
				t.Fatalf("fill fields wrong: %+v", f)
			}
			if f.F.Side != exec.SideBuy {
				t.Fatalf("side = %v", f.F.Side)
			}
		}
	}
	_ = updates
	if fills != 1 {
		t.Fatalf("want 1 fill, got %d", fills)
	}
	// Re-applying the same frame must NOT re-emit the fill (dedup on (id, executed)).
	if evs2 := a.normalizeOrder("tz", loadOrder(t, "order_partial_fill.json")); len(fills2(evs2)) != 0 {
		t.Fatal("duplicate frame re-emitted a fill")
	}
}

func TestNormalizeOrder_ShortUnenriched(t *testing.T) {
	a := newTestAdapter(t, "tz")
	o := loadOrder(t, "order_short_new.json")
	if got := sideDomain(o.Side, o.OpenClose); got != exec.SideShort {
		t.Fatalf("short side = %v", got)
	}
	_ = a
}

func fills2(evs []exec.BrokerEvent) []exec.OrderFilled {
	var out []exec.OrderFilled
	for _, e := range evs {
		if f, ok := e.(exec.OrderFilled); ok {
			out = append(out, f)
		}
	}
	return out
}

func TestStatusDomain_PendingCancelIsNonTerminal(t *testing.T) {
	// PendingCancel is a non-terminal state (TZ: PendingCancel -> Canceled).
	// statusDomain must return StatusSubmitted, NOT StatusCanceled, so that
	// normalizeOrder does not fire the terminal-cancel path. If statusDomain
	// incorrectly returned StatusCanceled, the emulated replace (Task 10)
	// would resubmit the new leg while the old leg still rests.
	status := statusDomain("PendingCancel")
	if status == exec.StatusCanceled {
		t.Fatal("PendingCancel must NOT map to StatusCanceled")
	}
	if status != exec.StatusSubmitted {
		t.Fatalf("PendingCancel must map to StatusSubmitted, got %v", status)
	}
}

func TestNormalizeOrder_PendingCancelDoesNotEmitCancelEvent(t *testing.T) {
	a := newTestAdapter(t, "tz")
	o := tzOrder{
		UserOrderID:   "2TZ00001:ET01J000000000000000000001",
		Symbol:        "AAPL",
		Side:          "BUY",
		OrderQuantity: 100,
		Executed:      0,
		LastQty:       0,
		Status:        "Submitted",
		OrderStatus:   "PendingCancel", // Non-terminal state
	}
	evs := a.normalizeOrder("tz", o)

	// Check that no OrderCanceled event was emitted. If statusDomain incorrectly
	// returned StatusCanceled, the switch in normalizeOrder would trigger
	// a.onCanceled(), which would emit cancel events.
	for _, e := range evs {
		if _, ok := e.(exec.OrderCanceled); ok {
			t.Fatalf("PendingCancel order must not emit OrderCanceled event; got %+v", e)
		}
	}
}
