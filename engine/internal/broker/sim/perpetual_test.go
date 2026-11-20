package sim

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

func TestPerpetualChartRiskRestingLimitCancelsRemainderAtStop(t *testing.T) {
	const symbol = "PERP.MEXC.BTC_USDT"
	clk := clock.NewFake(time.UnixMilli(1_795_041_491_205))
	rules := instrument.ContractRules{Base: "BTC", Settlement: "USDT", Multiplier: "0.0001", PriceTick: "0.1",
		QuantityStep: 1, Minimum: 1, Maximum: 1000, MinLeverage: 1, MaxLeverage: 10, TakerFee: "0.0005", TradingEnabled: true}
	b, err := NewPerpetual(PerpetualConfig{Venue: "paper", Exchange: "MEXC", Clock: clk, StartingUSDT: 100,
		Path: filepath.Join(t.TempDir(), "paper.json"), Rules: func(s string) (instrument.ContractRules, bool) { return rules, s == symbol }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	now := clk.Now().UnixMilli()
	book := feed.Book{Symbol: symbol, TsMs: now, Bids: []feed.BookLevel{{Price: 99_999, Volume: 10}},
		Asks: []feed.BookLevel{{Price: 100_000, Volume: 2}}}
	b.SetBook(symbol, book)
	b.SetFairQuote(feed.Quote{Symbol: symbol, Mark: 100_000, TsMs: now})
	ack, err := b.SubmitOrder(ctx, exec.OrderRequest{Venue: "paper", Symbol: symbol, ClientOrderID: "entry", Side: exec.SideBuy,
		Type: exec.TypeLimit, TIF: exec.TIFGTC, Qty: 5, LimitPrice: 100_000,
		Perpetual: &exec.PerpetualOrder{Leverage: 5, Multiplier: .0001, ProtectionPrice: 99_000, ProtectionID: "stop", RiskBudget: 2}})
	if err != nil || !ack.Accepted {
		t.Fatalf("entry was rejected: %+v, %v", ack, err)
	}
	if entry, stop := b.data.Orders["entry"], b.data.Orders["stop"]; entry.LeavesQty != 3 || !entry.Working() || stop.Qty != 2 {
		t.Fatalf("expected a resting remainder and protection for two fills: %+v, %+v", entry, stop)
	}
	b.SetFairQuote(feed.Quote{Symbol: symbol, Mark: 99_000, TsMs: now})
	book.Bids[0].Price, book.Asks[0].Price = 98_999, 99_000
	book.Asks[0].Volume = 10
	b.SetBook(symbol, book)
	_, positions, orders, err := b.Snapshot(ctx)
	if err != nil || len(positions) != 0 || len(orders) != 0 || b.data.Orders["entry"].ExecutedQty != 2 {
		t.Fatalf("the resting remainder reopened exposure after the stop: %+v, %+v, %v", positions, orders, err)
	}
}

func TestPerpetualPartialIOCPersistsPositionAndProtection(t *testing.T) {
	const symbol = "PERP.MEXC.BTC_USDT"
	for _, side := range []exec.Side{exec.SideBuy, exec.SideShort} {
		t.Run(side.String(), func(t *testing.T) {
			ctx := context.Background()
			clk := clock.NewFake(time.Date(2026, time.November, 21, 12, 0, 0, 0, time.UTC))
			rules := instrument.ContractRules{Base: "BTC", Settlement: "USDT", Multiplier: "0.0001", PriceTick: "0.1",
				QuantityStep: 1, Minimum: 1, Maximum: 1000, MinLeverage: 1, MaxLeverage: 10, TakerFee: "0.0005", TradingEnabled: true}
			cfg := PerpetualConfig{Venue: "mexc-paper", Exchange: "MEXC", Clock: clk, StartingUSDT: 100, Path: filepath.Join(t.TempDir(), "paper.json"),
				Rules: func(s string) (instrument.ContractRules, bool) { return rules, s == symbol }}
			b, err := NewPerpetual(cfg)
			if err != nil {
				t.Fatal(err)
			}
			clockMs := clk.Now().UnixMilli()
			book := feed.Book{Symbol: symbol, TsMs: clockMs, Bids: []feed.BookLevel{{Price: 100000, Volume: 4}},
				Asks: []feed.BookLevel{{Price: 100000, Volume: 4}}}
			b.SetBook(symbol, book)
			fundingAt := clockMs + int64(time.Second/time.Millisecond)
			b.SetFairQuote(feed.Quote{Symbol: symbol, Mark: 100000, TsMs: clockMs, NextFundingMs: fundingAt, FundingRate: .001})
			sign, closeSide := 1.0, exec.SideSell
			if side == exec.SideShort {
				sign, closeSide = -1, exec.SideCover
			}
			stop := 100000 - sign*1000
			if qty := rules.SizeRisk(1, 100, 100000, stop, 5); qty != 9 {
				t.Fatalf("expected nine contracts, got %d", qty)
			}
			ack, err := b.SubmitOrder(ctx, exec.OrderRequest{Venue: cfg.Venue, Symbol: symbol, ClientOrderID: "entry", Side: side,
				Type: exec.TypeLimit, TIF: exec.TIFGTC, Qty: 9, LimitPrice: 100000,
				Perpetual: &exec.PerpetualOrder{Leverage: 5, Multiplier: .0001, ProtectionPrice: stop, ProtectionID: "stop"}})
			if err != nil || !ack.Accepted {
				t.Fatalf("submit returned %+v, %v", ack, err)
			}
			newStop := stop - sign*50
			if err := b.ReplaceOrder(ctx, "stop", exec.ReplaceRequest{StopPrice: newStop, ExpectedStopPrice: stop}); err != nil {
				t.Fatal(err)
			}
			if err := b.ReplaceOrder(ctx, "stop", exec.ReplaceRequest{StopPrice: stop, ExpectedStopPrice: stop}); err == nil {
				t.Fatal("stale edit replaced the paper stop")
			}
			// Reload the durable paper venue before the first protective quote.
			b, err = NewPerpetual(cfg)
			if err != nil {
				t.Fatal(err)
			}
			acct, pos, orders, err := b.Snapshot(ctx)
			if err != nil || len(pos) != 1 || pos[0].Qty != 4*sign || pos[0].Leverage != 5 ||
				acct.MarginUsed != 8 || len(orders) != 1 || orders[0].ID != "stop" ||
				orders[0].Side != closeSide || orders[0].LeavesQty != 4 || orders[0].StopPrice != newStop {
				t.Fatalf("restored account=%+v positions=%+v orders=%+v err=%v", acct, pos, orders, err)
			}
			clk.Advance(time.Second)
			clockMs = clk.Now().UnixMilli()
			book = feed.Book{Symbol: symbol, TsMs: clockMs, Bids: []feed.BookLevel{{Price: stop, Volume: 10}},
				Asks: []feed.BookLevel{{Price: stop, Volume: 10}}}
			b.SetBook(symbol, book)
			b.SetFairQuote(feed.Quote{Symbol: symbol, Mark: 100000, TsMs: clockMs})
			acct, pos, _, _ = b.Snapshot(ctx)
			funding := -sign * .04
			if len(pos) != 1 || math.Abs(acct.FundingPnL-funding) > 1e-10 {
				t.Fatalf("fair-price stop or funding settled incorrectly: %+v %+v", acct, pos)
			}
			ack, err = b.SubmitOrder(ctx, exec.OrderRequest{Venue: cfg.Venue, Symbol: symbol, ClientOrderID: "resting-close",
				Side: closeSide, Type: exec.TypeLimit, TIF: exec.TIFGTC, Qty: 1, LimitPrice: stop + sign*2000,
				Perpetual: &exec.PerpetualOrder{Multiplier: .0001}})
			if err != nil || !ack.Accepted {
				t.Fatalf("resting close returned %+v, %v", ack, err)
			}
			b.SetFairQuote(feed.Quote{Symbol: symbol, Mark: stop, TsMs: clockMs})
			_, pos, _, _ = b.Snapshot(ctx)
			if len(pos) != 1 {
				t.Fatal("old stop triggered after replacement")
			}
			b.SetFairQuote(feed.Quote{Symbol: symbol, Mark: newStop, TsMs: clockMs})
			acct, pos, orders, err = b.Snapshot(ctx)
			want := 100 - .4 + funding - 4*.0001*(100000+stop)*.0005
			if err != nil || len(pos) != 0 || len(orders) != 0 || math.Abs(acct.Equity-want) > 1e-10 {
				t.Fatalf("after close account=%+v positions=%+v orders=%+v want equity %.8f", acct, pos, orders, want)
			}
			ack, err = b.SubmitOrder(ctx, exec.OrderRequest{Venue: cfg.Venue, Symbol: symbol, ClientOrderID: "duplicate-close",
				Side: closeSide, Type: exec.TypeMarket, Qty: 4, Perpetual: &exec.PerpetualOrder{Multiplier: .0001}})
			if err != nil || ack.Accepted {
				t.Fatalf("flat close returned %+v, %v", ack, err)
			}
			ack, err = b.SubmitOrder(ctx, exec.OrderRequest{Venue: cfg.Venue, Symbol: symbol, ClientOrderID: "unprotected",
				Side: side, Type: exec.TypeMarket, Qty: 1, Perpetual: &exec.PerpetualOrder{Leverage: 5, Multiplier: .0001}})
			if err != nil || !ack.Accepted {
				t.Fatalf("unprotected order returned %+v, %v", ack, err)
			}
			_, pos, orders, err = b.Snapshot(ctx)
			if err != nil || len(pos) != 1 || len(orders) != 0 {
				t.Fatalf("unprotected position=%+v, open orders=%+v, err=%v", pos, orders, err)
			}
		})
	}
}
