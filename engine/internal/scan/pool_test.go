package scan

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

func et(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, session.Loc())
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestPoolAdmitsTopNAndBackfillsFirstTime(t *testing.T) {
	p := NewPool()
	d := p.Update([]string{"US.A", "US.B", "US.C"}, et(2026, 7, 8, 9, 30))
	want := []string{"US.A", "US.B", "US.C"}
	if !reflect.DeepEqual(d.Admitted, want) {
		t.Fatalf("Admitted=%v, want %v", d.Admitted, want)
	}
	if !reflect.DeepEqual(d.Backfill, want) {
		t.Fatalf("Backfill=%v, want %v (all first-time)", d.Backfill, want)
	}
	if len(d.Evicted) != 0 {
		t.Fatalf("Evicted=%v, want none", d.Evicted)
	}
}

func TestPoolStickyWhenSymbolDropsOffTopN(t *testing.T) {
	p := NewPool()
	base := et(2026, 7, 8, 9, 30)
	p.Update([]string{"US.A", "US.B"}, base)
	d := p.Update([]string{"US.B"}, base.Add(time.Minute)) // A drops out of the top-N
	if len(d.Admitted) != 0 || len(d.Evicted) != 0 {
		t.Fatalf("no delta expected when a member drops off-board: %+v", d)
	}
	if !containsStr(p.Symbols(), "US.A") {
		t.Fatalf("A must stay pooled (sticky): %v", p.Symbols())
	}
}

func TestPoolCapEvictsLongestOffBoardAndNeverEvictsTopN(t *testing.T) {
	p := NewPool()
	base := et(2026, 7, 8, 9, 30)
	// Fill exactly cap (30) members over 3 polls of 10 distinct symbols each,
	// with strictly increasing timestamps so off-board age is deterministic.
	for g := 0; g < 3; g++ {
		top := make([]string, 10)
		for i := range top {
			top[i] = fmt.Sprintf("US.S%02d", g*10+i)
		}
		p.Update(top, base.Add(time.Duration(g)*time.Minute))
	}
	if len(p.Symbols()) != poolCap {
		t.Fatalf("pool should be full at %d, got %d", poolCap, len(p.Symbols()))
	}
	// New poll: top-N = {S30 (new), S00 (oldest off-board, but now back on-board)}.
	// Admitting S30 forces one eviction. S00 is protected (in top-N this poll),
	// so the victim is the next-oldest off-board member: S01.
	d := p.Update([]string{"US.S30", "US.S00"}, base.Add(3*time.Minute))
	if !reflect.DeepEqual(d.Admitted, []string{"US.S30"}) {
		t.Fatalf("Admitted=%v, want [US.S30]", d.Admitted)
	}
	if !reflect.DeepEqual(d.Evicted, []string{"US.S01"}) {
		t.Fatalf("Evicted=%v, want [US.S01] (longest off-board, S00 protected)", d.Evicted)
	}
	if containsStr(d.Evicted, "US.S00") {
		t.Fatalf("a current top-N member must never be evicted: %v", d.Evicted)
	}
}

func TestPoolResetsAt2000ETBoundary(t *testing.T) {
	p := NewPool()
	p.Update([]string{"US.A", "US.B"}, et(2026, 7, 8, 19, 0)) // before 20:00 ET
	d := p.Update([]string{"US.C"}, et(2026, 7, 8, 20, 0))    // crosses the boundary
	if !reflect.DeepEqual(d.Evicted, []string{"US.A", "US.B"}) {
		t.Fatalf("Evicted=%v, want [US.A US.B] (whole prior pool day released)", d.Evicted)
	}
	if !reflect.DeepEqual(d.Admitted, []string{"US.C"}) {
		t.Fatalf("Admitted=%v, want [US.C]", d.Admitted)
	}
	if !reflect.DeepEqual(d.Backfill, []string{"US.C"}) {
		t.Fatalf("Backfill=%v, want [US.C] (fresh pool day)", d.Backfill)
	}
	if !reflect.DeepEqual(p.Symbols(), []string{"US.C"}) {
		t.Fatalf("pool after reset = %v, want [US.C]", p.Symbols())
	}
}

func TestPoolReadmitAfterEvictionNoRebackfill(t *testing.T) {
	p := NewPool()
	base := et(2026, 7, 8, 9, 30)
	d0 := p.Update([]string{"US.A"}, base) // first admission -> backfill
	if !reflect.DeepEqual(d0.Backfill, []string{"US.A"}) {
		t.Fatalf("A should backfill on first admission: %v", d0.Backfill)
	}
	// Fill 30 fresh symbols over 3 polls, always ranked without A, so cap
	// pressure evicts A (the oldest off-board member).
	for g := 0; g < 3; g++ {
		top := make([]string, 10)
		for i := range top {
			top[i] = fmt.Sprintf("US.F%02d", g*10+i)
		}
		p.Update(top, base.Add(time.Duration(g+1)*time.Minute))
	}
	if containsStr(p.Symbols(), "US.A") {
		t.Fatalf("A should have been cap-evicted: %v", p.Symbols())
	}
	// Re-admit A within the SAME pool day.
	d := p.Update([]string{"US.A"}, base.Add(5*time.Minute))
	if !containsStr(d.Admitted, "US.A") {
		t.Fatalf("A should be re-admitted: %v", d.Admitted)
	}
	if containsStr(d.Backfill, "US.A") {
		t.Fatalf("A must NOT re-backfill within the same pool day: %v", d.Backfill)
	}
}

func TestPoolMultipleCapEvictionsAreSorted(t *testing.T) {
	p := NewPool()
	base := et(2026, 7, 8, 9, 30)
	// Fill pool to cap (30) with symbols in a specific order
	// so that eviction order (oldest-first) is NOT alphabetically sorted.
	// Use: B00, A00, C00, B01, A01, C01, ... (interleaved by letter)
	// This gives timestamps: B00->T0, A00->T1, C00->T2, B01->T3, A01->T4, C01->T5, ...
	symbols := []string{}
	for i := 0; i < 10; i++ {
		symbols = append(symbols, fmt.Sprintf("US.B%02d", i))
		symbols = append(symbols, fmt.Sprintf("US.A%02d", i))
		symbols = append(symbols, fmt.Sprintf("US.C%02d", i))
	}
	// symbols is now [B00, A00, C00, B01, A01, C01, ..., B09, A09, C09]
	// length = 30
	// Fill pool by processing each symbol individually (each gets its own timestamp).
	for i, sym := range symbols {
		p.Update([]string{sym}, base.Add(time.Duration(i)*time.Minute))
	}
	if len(p.Symbols()) != poolCap {
		t.Fatalf("pool should be full at %d, got %d", poolCap, len(p.Symbols()))
	}
	// Now admit 3 new symbols in one Update call, triggering 3 cap-evictions.
	// Off-board members in oldest-first order: B00 (T0), A00 (T1), C00 (T2)
	// New symbols: D00, E00, F00
	// Processing each new symbol (within the same Update call):
	// - D00 admitted, B00 (oldest off-board) evicted
	// - E00 admitted, A00 (next oldest) evicted
	// - F00 admitted, C00 (next oldest) evicted
	// Eviction order (without the fix): [B00, A00, C00] - NOT sorted
	// Eviction order (with the fix): [A00, B00, C00] - sorted
	d := p.Update([]string{"US.D00", "US.E00", "US.F00"}, base.Add(40*time.Minute))
	wantEvicted := []string{"US.A00", "US.B00", "US.C00"} // sorted ascending
	if !reflect.DeepEqual(d.Evicted, wantEvicted) {
		t.Fatalf("Evicted=%v, want %v (must be sorted despite reverse-order victim selection)", d.Evicted, wantEvicted)
	}
}
