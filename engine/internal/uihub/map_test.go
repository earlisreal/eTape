package uihub

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

func TestMapOrderEnumsAndTimestamps(t *testing.T) {
	o := exec.Order{
		Venue: "sim", ID: "ET1", Symbol: "US.AAPL",
		Side: exec.SideShort, Type: exec.TypeStopLimit, TIF: exec.TIFGTC,
		Qty: 80, LimitPrice: 3.55, StopPrice: 3.60, Status: exec.StatusPartiallyFilled,
		ExecutedQty: 30, LeavesQty: 50, AvgFillPrice: 3.54,
		ReplacesID: "ET0", CreatedMs: 1_700_000_000_000, UpdatedMs: 1_700_000_005_000,
	}
	w := mapOrder(o)
	if w.Side != wsmsg.SideShort || w.Type != wsmsg.OrderStopLimit || w.TIF != wsmsg.TIFGTC {
		t.Fatalf("enum map wrong: %+v", w)
	}
	if w.Status != wsmsg.StatusPartiallyFilled {
		t.Fatalf("status map wrong: %v", w.Status)
	}
	if w.CreatedMs != 1_700_000_000_000 || w.UpdatedMs != 1_700_000_005_000 {
		t.Fatalf("exec ms must pass through as numbers: %+v", w)
	}
	if w.ID != "ET1" || w.ReplacesID != "ET0" || w.LeavesQty != 50 {
		t.Fatalf("field copy wrong: %+v", w)
	}
}

func TestMapQuoteJoinsBidAskAndISOTime(t *testing.T) {
	q := feed.Quote{Symbol: "US.AAPL", Last: 3.47, TsMs: 1_783_344_660_000} // 2026-07-06T13:31:00Z
	w := mapQuote(q, 3.46, 3.48)
	if w.Bid != 3.46 || w.Ask != 3.48 || w.Last != 3.47 {
		t.Fatalf("quote join wrong: %+v", w)
	}
	if w.Ts != "2026-07-06T13:31:00.000Z" {
		t.Fatalf("md timestamp must be ISO-8601 UTC ms: %q", w.Ts)
	}
}

func TestMapPositionUnrealizedFromMark(t *testing.T) {
	p := exec.Position{Venue: "sim", Symbol: "US.AAPL", Qty: 100, AvgPrice: 3.50}
	w := mapPosition(p, 3.60) // long 100 @ 3.50, mark 3.60 => +10.00
	if w.Venue == nil || *w.Venue != "sim" {
		t.Fatalf("venue must be set for a venue-scoped row: %+v", w)
	}
	if w.UnrealizedPnl < 9.999 || w.UnrealizedPnl > 10.001 {
		t.Fatalf("unrealized pnl = %v, want ~10", w.UnrealizedPnl)
	}
}

func TestMapBarTimeframeAndBucket(t *testing.T) {
	b := md.Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: 1_783_344_660_000, // 2026-07-06T13:31:00Z
		O: 1, H: 2, L: 0.5, C: 1.5, V: 1000, InProgress: true}
	w := mapBar(b)
	if w.Timeframe != "1m" || w.BucketStart != "2026-07-06T13:31:00.000Z" {
		t.Fatalf("bar tf/bucket wrong: tf=%q bucket=%q", w.Timeframe, w.BucketStart)
	}
	if !w.InProgress || w.V != 1000 {
		t.Fatalf("bar fields wrong: %+v", w)
	}
}

func TestMapTickDirection(t *testing.T) {
	w := mapTick(feed.Tick{Symbol: "US.AAPL", Price: 3.47, Volume: 10, Dir: feed.Sell, TsMs: 1_783_344_660_000})
	if w.Direction != wsmsg.DirSell || w.Size != 10 {
		t.Fatalf("tick map wrong: %+v", w)
	}
}

func TestMapFillFieldsAndSide(t *testing.T) {
	f := exec.Fill{
		Venue: "tz", OrderID: "TZ123", Symbol: "US.AAPL",
		Side: exec.SideShort, Qty: 50, Price: 3.55, TsMs: 1_783_344_660_000,
	}
	w := mapFill(f)
	if w.Venue != "tz" || w.OrderID != "TZ123" || w.Symbol != "US.AAPL" {
		t.Fatalf("fill basic fields wrong: %+v", w)
	}
	if w.Side != wsmsg.SideShort {
		t.Fatalf("fill side must map via sideToWire: got %v, want SideShort", w.Side)
	}
	if w.Qty != 50 || w.Price != 3.55 || w.TsMs != 1_783_344_660_000 {
		t.Fatalf("fill qty/price/ts wrong: %+v", w)
	}
}

func TestMapAccountFields(t *testing.T) {
	a := exec.AccountSnapshot{
		Venue: "sim", Equity: 10000, BuyingPower: 20000,
		AvailableCash: 5000, SodEquity: 9500, Realized: 500,
		DayPnL: -250, Leverage: 1.5, TsMs: 1_783_344_660_000,
	}
	w := mapAccount(a)
	if w.Venue != "sim" {
		t.Fatalf("account venue wrong: %q", w.Venue)
	}
	if w.Equity != 10000 || w.BuyingPower != 20000 || w.AvailableCash != 5000 {
		t.Fatalf("account equity/bp/cash wrong: %+v", w)
	}
	if w.SodEquity != 9500 || w.Realized != 500 || w.DayPnl != -250 {
		t.Fatalf("account sod/realized/dayPnl wrong: %+v", w)
	}
	if w.Leverage != 1.5 || w.TsMs != 1_783_344_660_000 {
		t.Fatalf("account leverage/ts wrong: %+v", w)
	}
}

func TestMapBookLevelsAndTimestamp(t *testing.T) {
	b := feed.Book{
		Symbol: "US.AAPL",
		TsMs:   1_783_344_660_000, // 2026-07-06T13:31:00Z
		Bids: []feed.BookLevel{
			{Price: 3.48, Volume: 100, Orders: 2},
			{Price: 3.47, Volume: 50, Orders: 1},
		},
		Asks: []feed.BookLevel{
			{Price: 3.49, Volume: 75, Orders: 1},
			{Price: 3.50, Volume: 200, Orders: 3},
		},
	}
	w := mapBook(b)
	if w.Symbol != "US.AAPL" {
		t.Fatalf("book symbol wrong: %q", w.Symbol)
	}
	if w.Ts != "2026-07-06T13:31:00.000Z" {
		t.Fatalf("book ts must be ISO-8601 UTC ms: %q", w.Ts)
	}
	if len(w.Bids) != 2 || len(w.Asks) != 2 {
		t.Fatalf("book levels count wrong: bids=%d asks=%d", len(w.Bids), len(w.Asks))
	}
	if w.Bids[0].Price != 3.48 || w.Bids[0].Size != 100 {
		t.Fatalf("bid level 0 wrong: %+v", w.Bids[0])
	}
	if w.Asks[1].Price != 3.50 || w.Asks[1].Size != 200 {
		t.Fatalf("ask level 1 wrong: %+v", w.Asks[1])
	}
}

func TestMapIndicatorPointFields(t *testing.T) {
	p := md.Point{TimeMs: 1_783_344_660_000, Value: 42.5}
	w := mapIndicatorPoint(p)
	if w.TimeMs != 1_783_344_660_000 || w.Value != 42.5 {
		t.Fatalf("indicator point fields wrong: %+v", w)
	}
}
