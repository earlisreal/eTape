package clock

import (
	"testing"
	"time"
)

func TestSystemNowAdvances(t *testing.T) {
	var c System
	t0 := c.Now()
	time.Sleep(2 * time.Millisecond)
	if !c.Now().After(t0) {
		t.Fatal("System.Now did not advance")
	}
}

func TestSystemAfterFires(t *testing.T) {
	var c System
	select {
	case <-c.After(5 * time.Millisecond):
	case <-time.After(time.Second):
		t.Fatal("System.After did not fire within 1s")
	}
}

func TestSystemTickerFiresAndStops(t *testing.T) {
	var c System
	tk := c.NewTicker(5 * time.Millisecond)
	select {
	case <-tk.C():
	case <-time.After(time.Second):
		t.Fatal("ticker did not fire")
	}
	tk.Stop()
}
