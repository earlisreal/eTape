package uihub

import (
	"context"
	"encoding/json"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type fillsQuerier interface {
	QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error)
	ExportFills(ctx context.Context, venue string, fromMs, toMs int64) ([]exec.ExportFillRow, error)
}

type queries struct {
	fills fillsQuerier
	clk   clock.Clock
}

func newQueries(f fillsQuerier, clk clock.Clock) *queries { return &queries{fills: f, clk: clk} }

func fillRowToWire(r exec.FillRow) wsmsg.Fill {
	return wsmsg.Fill{
		Venue: r.Venue, OrderID: r.OrderID, Symbol: r.Symbol,
		Side: wsmsg.Side(r.Side), Qty: r.Qty, Price: r.Price, TsMs: r.TsMs,
	}
}

func (q *queries) handle(name string, args json.RawMessage) any {
	switch name {
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
		return []any{} // unknown query -> resolves to [] on the UI, never hangs
	}
}
