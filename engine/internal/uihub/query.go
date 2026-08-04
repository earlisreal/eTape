package uihub

import (
	"context"
	"encoding/json"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type fillsQuerier interface {
	QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error)
	ExportFills(ctx context.Context, venue string, fromMs, toMs int64) ([]exec.ExportFillRow, error)
}

type cycleFillsQuerier interface {
	LoadCycleCheckpoint(exec.VenueID) (exec.CycleCheckpoint, bool, error)
	QueryVenueFillsSince(context.Context, string, int64) ([]exec.FillRow, error)
}

type queries struct {
	fills  fillsQuerier
	charts interface {
		QueryChartWindow(wsmsg.QueryChartWindowArgs) wsmsg.QueryChartWindowResult
	}
	clk clock.Clock
}

func newQueries(f fillsQuerier, clk clock.Clock, charts ...interface {
	QueryChartWindow(wsmsg.QueryChartWindowArgs) wsmsg.QueryChartWindowResult
}) *queries {
	q := &queries{fills: f, clk: clk}
	if len(charts) > 0 {
		q.charts = charts[0]
	}
	return q
}

func fillRowToWire(r exec.FillRow) wsmsg.Fill {
	return wsmsg.Fill{
		Venue: r.Venue, OrderID: r.OrderID, Symbol: r.Symbol,
		Side: wsmsg.Side(r.Side), Qty: r.Qty, Price: r.Price, TsMs: r.TsMs,
	}
}

func (q *queries) handle(name string, args json.RawMessage) any {
	switch name {
	case "QueryChartWindow":
		var a wsmsg.QueryChartWindowArgs
		if json.Unmarshal(args, &a) != nil || q.charts == nil || a.Symbol == "" || a.Timeframe == "" || (a.TailBars > 0) == (a.FromMs < a.ToMs) {
			return wsmsg.QueryChartWindowResult{Symbol: a.Symbol, Timeframe: a.Timeframe, Bars: []wsmsg.Bar{}, Indicators: []wsmsg.IndicatorSeriesWindow{}}
		}
		return q.charts.QueryChartWindow(a)
	case "QueryFills":
		var a wsmsg.QueryFillsArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return []wsmsg.Fill{}
		}
		rows, err := q.fills.QueryFills(a.Symbol, a.FromMs, a.ToMs)
		if err != nil {
			return []wsmsg.Fill{}
		}
		out := make([]wsmsg.Fill, 0, len(rows))
		for _, r := range rows {
			out = append(out, fillRowToWire(r))
		}
		return out
	case "QueryCycleFills":
		var a wsmsg.QueryCycleFillsArgs
		cq, ok := q.fills.(cycleFillsQuerier)
		if json.Unmarshal(args, &a) != nil || a.Venue == "" || !ok {
			return wsmsg.QueryCycleFillsResult{Carried: []wsmsg.CarriedPosition{}, Fills: []wsmsg.Fill{}}
		}
		start := session.TradingCycleStart(q.clk.Now()).UnixMilli()
		rows, err := cq.QueryVenueFillsSince(context.Background(), a.Venue, start)
		if err != nil {
			return wsmsg.QueryCycleFillsResult{CycleStartMs: start, Carried: []wsmsg.CarriedPosition{}, Fills: []wsmsg.Fill{}}
		}
		out := wsmsg.QueryCycleFillsResult{CycleStartMs: start, Carried: []wsmsg.CarriedPosition{}, Fills: make([]wsmsg.Fill, 0, len(rows))}
		if cp, found, _ := cq.LoadCycleCheckpoint(exec.VenueID(a.Venue)); found && cp.StartMs == start {
			for symbol, p := range cp.Positions {
				if p.Carried != 0 {
					out.Carried = append(out.Carried, wsmsg.CarriedPosition{Symbol: symbol, Qty: p.Carried})
				}
			}
		}
		for _, row := range rows {
			out.Fills = append(out.Fills, fillRowToWire(row))
		}
		return out
	case "ExportFills":
		var a wsmsg.ExportFillsArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return wsmsg.ExportFillsResult{}
		}
		fromMs, toMs, err := exec.ResolveExportRange(a.Preset, a.From, a.To, q.clk.Now())
		if err != nil {
			return wsmsg.ExportFillsResult{}
		}
		rows, err := q.fills.ExportFills(context.Background(), a.Venue, fromMs, toMs)
		if err != nil {
			return wsmsg.ExportFillsResult{}
		}
		csvStr, err := exec.BuildFillsCSV(rows)
		if err != nil {
			return wsmsg.ExportFillsResult{}
		}
		return wsmsg.ExportFillsResult{CSV: csvStr, Count: len(rows)}
	default:
		return []any{}
	}
}
