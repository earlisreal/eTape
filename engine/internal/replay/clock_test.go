package replay

import (
	"testing"
	"time"
)

func TestClockAdvanceToForwardOnly(t *testing.T) {
	start := time.UnixMilli(1000)
	c := NewClock(start)
	if !c.Now().Equal(start) {
		t.Fatalf("Now = %v, want %v", c.Now(), start)
	}
	c.AdvanceTo(time.UnixMilli(500)) // backwards → no-op
	if c.Now().UnixMilli() != 1000 {
		t.Fatalf("backwards AdvanceTo moved clock: %d", c.Now().UnixMilli())
	}
	c.AdvanceTo(time.UnixMilli(3000))
	if c.Now().UnixMilli() != 3000 {
		t.Fatalf("Now = %d, want 3000", c.Now().UnixMilli())
	}
}

func TestClockTickerFiresOnAdvance(t *testing.T) {
	c := NewClock(time.UnixMilli(0))
	tk := c.NewTicker(time.Second)
	defer tk.Stop()
	c.AdvanceTo(time.UnixMilli(2500)) // crosses 1s and 2s
	select {
	case <-tk.C():
	default:
		t.Fatal("ticker did not fire after AdvanceTo crossed its interval")
	}
}
