package netx

import (
	"testing"
	"time"
)

func TestBackoff_ExponentialWithinBounds(t *testing.T) {
	b := Backoff{Min: time.Second, Max: 30 * time.Second}
	for i := 0; i < 10; i++ {
		d := b.Next()
		if d < b.Min || d > b.Max {
			t.Fatalf("delay %v out of [%v,%v]", d, b.Min, b.Max)
		}
	}
}

func TestBackoff_ResetReturnsToMin(t *testing.T) {
	b := Backoff{Min: time.Second, Max: 30 * time.Second}
	for i := 0; i < 5; i++ {
		b.Next()
	}
	b.Reset()
	if d := b.Next(); d != b.Min { // first Next after reset returns exactly Min (span==0)
		t.Fatalf("after reset first delay = %v, want %v", d, b.Min)
	}
}
