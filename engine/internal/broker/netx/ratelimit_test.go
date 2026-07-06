package netx

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func TestTokenBucket_BurstThenRefill(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	tb := NewTokenBucket(clk, 2, 2) // 2/sec, burst 2

	ok1 := tb.Allow()
	ok2 := tb.Allow()
	if !ok1 || !ok2 {
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

// TestTokenBucket_TakeBlocksThenSucceeds drains the bucket, starts Take(ctx)
// in a goroutine (so it must block on clk.After), then advances the fake
// clock by exactly enough to refill one token and asserts Take returns nil.
// The select+time.After below only bounds how long this test itself waits
// for the goroutine to signal completion; it does not drive the bucket.
func TestTokenBucket_TakeBlocksThenSucceeds(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	tb := NewTokenBucket(clk, 2, 1) // 2/sec, burst 1
	if !tb.Allow() {
		t.Fatal("initial Allow() should succeed (full burst)")
	}
	// Bucket is empty; Take() must block until refill.
	done := make(chan error, 1)
	go func() { done <- tb.Take(context.Background()) }()
	runtime.Gosched() // give the goroutine a chance to register on clk.After before we advance

	clk.Advance(500 * time.Millisecond) // +1 token at 2/sec

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Take() = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Take() did not return after clock advanced past refill")
	}
}

// TestTokenBucket_TakeReturnsCtxErrOnCancel verifies Take(ctx) returns the
// context's error (not nil, not a hang) when ctx is canceled before a token
// becomes available. The clock is never advanced enough to refill, so the
// only way Take can return is via ctx.Done().
func TestTokenBucket_TakeReturnsCtxErrOnCancel(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	tb := NewTokenBucket(clk, 1, 1) // 1/sec, burst 1
	if !tb.Allow() {
		t.Fatal("initial Allow() should succeed (full burst)")
	}
	// Bucket is empty and stays empty (clock never advances).
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tb.Take(ctx) }()
	runtime.Gosched()

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Take() = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Take() did not return after ctx cancel")
	}
}

// TestTokenBucket_TakeDecrementsOnce verifies Take() consumes exactly one
// token per call (no double-decrement): with burst 2 and a token available
// from the initial fill, a single Take() must leave exactly one more token
// available via Allow(), and no more after that.
func TestTokenBucket_TakeDecrementsOnce(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(0))
	tb := NewTokenBucket(clk, 1, 2) // 1/sec, burst 2

	if err := tb.Take(context.Background()); err != nil {
		t.Fatalf("Take() = %v, want nil (token available from initial burst)", err)
	}
	if !tb.Allow() {
		t.Fatal("second token should still be available after one Take() call")
	}
	if tb.Allow() {
		t.Fatal("bucket should be empty after Take()+Allow() each consumed one token; Take() must not double-decrement")
	}
}
