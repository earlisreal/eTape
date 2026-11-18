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
			// Reload the durable paper venue before the first protective quote.
			b, err = NewPerpetual(cfg)
			if err != nil {
				t.Fatal(err)
			}
			acct, pos, orders, err := b.Snapshot(ctx)
			if err != nil || len(pos) != 1 || pos[0].Qty != 4*sign || pos[0].Leverage != 5 ||
				acct.MarginUsed != 8 || len(orders) != 1 || orders[0].ID != "stop" ||
				orders[0].Side != closeSide || orders[0].LeavesQty != 4 {
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
		})
	}
}
