package uihub

import (
	"time"

	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// isoMs renders an epoch-ms timestamp as an ISO-8601 UTC string with millisecond
// precision (the format md/scanner/news/sys.events topics use on the wire).
func isoMs(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func sideToWire(s exec.Side) wsmsg.Side {
	switch s {
	case exec.SideBuy:
		return wsmsg.SideBuy
	case exec.SideSell:
		return wsmsg.SideSell
	case exec.SideShort:
		return wsmsg.SideShort
	case exec.SideCover:
		return wsmsg.SideCover
	default:
		return wsmsg.SideBuy
	}
}

func orderTypeToWire(t exec.OrderType) wsmsg.OrderType {
	switch t {
	case exec.TypeMarket:
		return wsmsg.OrderMarket
	case exec.TypeLimit:
		return wsmsg.OrderLimit
	case exec.TypeStop:
		return wsmsg.OrderStop
	case exec.TypeStopLimit:
		return wsmsg.OrderStopLimit
	default:
		return wsmsg.OrderMarket
	}
}

func tifToWire(t exec.TIF) wsmsg.TIF {
	switch t {
	case exec.TIFDay:
		return wsmsg.TIFDay
	case exec.TIFGTC:
		return wsmsg.TIFGTC
	case exec.TIFIOC:
		return wsmsg.TIFIOC
	case exec.TIFFOK:
		return wsmsg.TIFFOK
	default:
		return wsmsg.TIFDay
	}
}

func statusToWire(s exec.OrderStatus) wsmsg.OrderStatus {
	switch s {
	case exec.StatusSubmitted:
		return wsmsg.StatusSubmitted
	case exec.StatusAccepted:
		return wsmsg.StatusAccepted
	case exec.StatusPartiallyFilled:
		return wsmsg.StatusPartiallyFilled
	case exec.StatusFilled:
		return wsmsg.StatusFilled
	case exec.StatusCanceled:
		return wsmsg.StatusCanceled
	case exec.StatusRejected:
		return wsmsg.StatusRejected
	case exec.StatusExpired:
		return wsmsg.StatusExpired
	case exec.StatusBlocked:
		return wsmsg.StatusBlocked
	case exec.StatusReplaced:
		return wsmsg.StatusReplaced
	default:
		return wsmsg.StatusSubmitted
	}
}

func dirToWire(d feed.Direction) wsmsg.TickDirection {
	switch d {
	case feed.Buy:
		return wsmsg.DirBuy
	case feed.Sell:
		return wsmsg.DirSell
	default:
		return wsmsg.DirNeutral
	}
}

func mapOrder(o exec.Order) wsmsg.Order {
	return wsmsg.Order{
		Venue: string(o.Venue), ID: o.ID, Symbol: o.Symbol,
		Side: sideToWire(o.Side), Type: orderTypeToWire(o.Type), TIF: tifToWire(o.TIF),
		Qty: o.Qty, LimitPrice: o.LimitPrice, StopPrice: o.StopPrice,
		Status: statusToWire(o.Status), ExecutedQty: o.ExecutedQty, LeavesQty: o.LeavesQty,
		AvgFillPrice: o.AvgFillPrice, RejectReason: o.RejectReason, ReplacesID: o.ReplacesID,
		CreatedMs: o.CreatedMs, UpdatedMs: o.UpdatedMs,
	}
}

func mapFill(f exec.Fill) wsmsg.Fill {
	return wsmsg.Fill{
		Venue: string(f.Venue), OrderID: f.OrderID, Symbol: f.Symbol,
		Side: sideToWire(f.Side), Qty: f.Qty, Price: f.Price, TsMs: f.TsMs,
	}
}

// mapPosition maps a venue-scoped position. mark is the latest last-trade price
// (0 if unknown); UnrealizedPnl = (mark - AvgPrice) * Qty with Qty signed.
func mapPosition(p exec.Position, mark float64) wsmsg.PositionRow {
	v := string(p.Venue)
	var upl float64
	if mark != 0 {
		upl = (mark - p.AvgPrice) * p.Qty
	}
	return wsmsg.PositionRow{Venue: &v, Symbol: p.Symbol, Qty: p.Qty, AvgPrice: p.AvgPrice, UnrealizedPnl: upl}
}

func mapAccount(a exec.AccountSnapshot) wsmsg.AccountRow {
	return wsmsg.AccountRow{
		Venue: string(a.Venue), Equity: a.Equity, BuyingPower: a.BuyingPower,
		AvailableCash: a.AvailableCash, SodEquity: a.SodEquity, Realized: a.Realized,
		DayPnl: a.DayPnL, Leverage: a.Leverage, TsMs: a.TsMs,
	}
}

func mapQuote(q feed.Quote, bid, ask float64) wsmsg.Quote {
	return wsmsg.Quote{Symbol: q.Symbol, Bid: bid, Ask: ask, Last: q.Last, Ts: isoMs(q.TsMs)}
}

func mapBook(b feed.Book) wsmsg.Book {
	bids := make([]wsmsg.BookLevel, len(b.Bids))
	for i, l := range b.Bids {
		bids[i] = wsmsg.BookLevel{Price: l.Price, Size: l.Volume}
	}
	asks := make([]wsmsg.BookLevel, len(b.Asks))
	for i, l := range b.Asks {
		asks[i] = wsmsg.BookLevel{Price: l.Price, Size: l.Volume}
	}
	return wsmsg.Book{Symbol: b.Symbol, Bids: bids, Asks: asks, Ts: isoMs(b.TsMs)}
}

func mapTick(t feed.Tick) wsmsg.Tick {
	return wsmsg.Tick{Symbol: t.Symbol, Price: t.Price, Size: t.Volume, Direction: dirToWire(t.Dir), Ts: isoMs(t.TsMs)}
}

func mapBar(b md.Bar) wsmsg.Bar {
	return wsmsg.Bar{
		Symbol: b.Symbol, Timeframe: string(b.TF), BucketStart: isoMs(b.BucketMs),
		O: b.O, H: b.H, L: b.L, C: b.C, V: b.V, InProgress: b.InProgress, Gap: b.Gap,
	}
}

func mapIndicatorPoint(p md.Point) wsmsg.IndicatorPoint {
	return wsmsg.IndicatorPoint{TimeMs: p.TimeMs, Value: p.Value}
}
