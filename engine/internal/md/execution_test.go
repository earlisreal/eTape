package md

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestExecutionCandlePrecedesCausalPrintAndExcludesSeed(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(t0Ms))
	c := New(Config{Clock: clk, ExecutionData: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	tk := tick(1, 0, 10, 1, feed.Buy)
	tk.Delivery = feed.DeliveryRealtime
	tk.RecvTsMs = t0Ms
	coverage := feed.Coverage{Verified: true, Revision: 1, SinceMs: t0Ms}
	c.FeedContext(ctx, feed.TicksEvent{Ticks: []feed.Tick{tk}, Execution: coverage})
	c.FeedContext(ctx, feed.Bars1mEvent{Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: t0Ms, O: 10, H: 10, L: 9.8, C: 10, Volume: 100}}, Execution: coverage})
	c.FeedContext(ctx, feed.Bars1mEvent{Seed: true, Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: t0Ms - 60000, O: 10, H: 20, L: 1, C: 10, Volume: 100}}})
	clk.Advance(time.Minute)
	c.FeedContext(ctx, feed.Bars1mEvent{Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: t0Ms + 60000, O: 9.9, H: 9.9, L: 9.9, C: 9.9, Volume: 1}}, Execution: coverage})
	tk = tick(2, 60000, 9.9, 1, feed.Sell)
	tk.Delivery = feed.DeliveryRealtime
	tk.RecvTsMs = t0Ms + 60000
	c.FeedContext(ctx, feed.TicksEvent{Ticks: []feed.Tick{tk}, Execution: coverage})
	sawCandle := false
	for {
		select {
		case e := <-c.ExecutionEvents():
			if e.Candle != nil && e.Candle.Timeframe == "1m" {
				if e.Candle.StartMs != t0Ms || e.Candle.Low != 9.8 {
					t.Fatalf("wrong verified candle: %+v", e)
				}
				sawCandle = true
			}
			if e.Print != nil && e.Print.Seq == 2 {
				if !sawCandle {
					t.Fatal("causal print preceded candle")
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("execution evidence missing")
		}
	}
}

func TestExecutionMinuteWaitsForAuthoritativeRollover(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(t0Ms))
	c := New(Config{Clock: clk, ExecutionData: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	coverage := feed.Coverage{Verified: true, Revision: 1, SinceMs: t0Ms}
	tk := tick(1, 0, 10, 1, feed.Buy)
	tk.Delivery = feed.DeliveryRealtime
	tk.RecvTsMs = t0Ms
	c.FeedContext(ctx, feed.TicksEvent{Ticks: []feed.Tick{tk}, Execution: coverage})
	c.FeedContext(ctx, feed.Bars1mEvent{Bars: []feed.Bar{{Symbol: "US.AAPL", BucketMs: t0Ms, O: 10, H: 10, L: 9.8, C: 10, Volume: 100}}, Execution: coverage})
	clk.Advance(time.Minute)
	tk = tick(2, 60000, 9.9, 1, feed.Sell)
	tk.Delivery = feed.DeliveryRealtime
	tk.RecvTsMs = t0Ms + 60000
	c.FeedContext(ctx, feed.TicksEvent{Ticks: []feed.Tick{tk}, Execution: coverage})
	for {
		select {
		case e := <-c.ExecutionEvents():
			if e.Candle != nil && e.Candle.Timeframe == "1m" {
				t.Fatal("in-progress K-line treated as completed without provider rollover")
			}
			if e.Print != nil && e.Print.Seq == 2 {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("print missing")
		}
	}
}

func TestExecutionDecodeGapInvalidatesReadiness(t *testing.T) {
	c := New(Config{ExecutionData: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	c.FeedContext(ctx, feed.DataGapEvent{})
	select {
	case e := <-c.ExecutionEvents():
		if e.Print == nil || !e.Print.Gap {
			t.Fatal("decode loss retained execution readiness")
		}
	case <-time.After(time.Second):
		t.Fatal("missing execution gap")
	}
}
