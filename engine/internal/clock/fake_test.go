package clock

import (
	"testing"
	"time"
)

func TestFakeAfterFiresOnAdvance(t *testing.T) {
	f := NewFake(time.Unix(1000, 0))
	ch := f.After(5 * time.Second)
	select {
	case <-ch:
		t.Fatal("fired before Advance")
	default:
	}
	f.Advance(4 * time.Second)
	select {
	case <-ch:
		t.Fatal("fired early")
	default:
	}
	f.Advance(time.Second)
	select {
	case ts := <-ch:
		if !ts.Equal(time.Unix(1005, 0)) {
			t.Fatalf("fired at %v, want %v", ts, time.Unix(1005, 0))
		}
	default:
		t.Fatal("did not fire at deadline")
	}
	if !f.Now().Equal(time.Unix(1005, 0)) {
		t.Fatalf("Now = %v, want 1005", f.Now())
	}
}

func TestFakeTickerRepeatsAndStops(t *testing.T) {
	f := NewFake(time.Unix(0, 0))
	tk := f.NewTicker(10 * time.Second)
	f.Advance(35 * time.Second)
	// Ticker channels have capacity 1 (matching time.Ticker): 3 ticks were due
	// but only the earliest undelivered one is buffered.
	select {
	case <-tk.C():
	default:
		t.Fatal("no tick buffered after 35s")
	}
	tk.Stop()
	f.Advance(time.Minute)
	select {
	case <-tk.C():
		t.Fatal("tick after Stop")
	default:
	}
}
