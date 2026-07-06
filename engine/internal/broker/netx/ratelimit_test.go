package netx

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func TestTokenBucket_BurstThenRefill(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	tb := NewTokenBucket(clk, 2, 2) // 2/sec, burst 2

	if !tb.Allow() || !tb.Allow() {
		t.Fatal("first two Allow() should succeed (full burst)")
	}
	if tb.Allow() {
		t.Fatal("third Allow() should fail (bucket empty)")
	}
	clk.Advance(500 * time.Millisecond) // +1 token at 2/sec
	if !tb.Allow() {
		t.Fatal("after 500ms one token should be available")
	}
	if tb.Allow() {
		t.Fatal("only one token refilled")
	}
}

func TestTokenBucket_CapsAtBurst(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	tb := NewTokenBucket(clk, 10, 3)
	clk.Advance(10 * time.Second) // would refill 100, but caps at burst=3
	n := 0
	for tb.Allow() {
		n++
	}
	if n != 3 {
		t.Fatalf("bucket should cap at burst 3, drained %d", n)
	}
}
