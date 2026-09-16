package scan

import (
	"testing"
	"time"
)

func TestRollingHistoryUsesBoundedBaseAndRejectsGaps(t *testing.T) {
	start := time.Unix(0, 0)
	h := &rollingHistory{silent: true}
	h.observe(start, 100)
	h.observe(start.Add(5*time.Second), 110)
	if got, ok := h.pct(start.Add(10*time.Second), 5*time.Second); !ok || got != 0 {
		t.Fatalf("exactly five-second base offset got %v/%v", got, ok)
	}
	h.observe(start.Add(11*time.Second), 111) // six seconds after the prior sample: new segment
	if _, ok := h.pct(start.Add(11*time.Second), 5*time.Second); ok {
		t.Fatal("gap bridged by an old sample")
	}
	if !h.silent {
		t.Fatal("gap did not re-arm silent recovery")
	}
}
