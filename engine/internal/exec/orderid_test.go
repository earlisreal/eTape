package exec

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func TestOrderIDFormat(t *testing.T) {
	g := NewOrderIDGen(clock.NewFake(time.UnixMilli(1_700_000_000_000)), rand.New(rand.NewSource(1)))
	id := g.Next()
	if !strings.HasPrefix(id, "ET") {
		t.Fatalf("id %q missing ET prefix", id)
	}
	if len(id) != 28 {
		t.Fatalf("id %q length %d, want 28", id, len(id))
	}
}

func TestOrderIDMonotonicUnique(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	g := NewOrderIDGen(clk, rand.New(rand.NewSource(1)))
	seen := map[string]bool{}
	prev := ""
	for i := 0; i < 1000; i++ {
		if i%100 == 0 {
			clk.Advance(time.Millisecond) // exercise both same-ms and advancing-ms paths
		}
		id := g.Next()
		if seen[id] {
			t.Fatalf("duplicate id %q at %d", id, i)
		}
		seen[id] = true
		if prev != "" && id <= prev {
			t.Fatalf("id %q not > prev %q (ULIDs are lexicographically time-ordered)", id, prev)
		}
		prev = id
	}
}
