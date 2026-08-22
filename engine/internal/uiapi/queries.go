package uiapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/locates"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

var ErrQueriesUnavailable = errors.New("ui query service is unavailable")

type FillSource interface {
	QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error)
	ExportFills(ctx context.Context, venue string, fromMs, toMs int64) ([]exec.ExportFillRow, error)
	LoadCycleCheckpoint(exec.VenueID) (exec.CycleCheckpoint, bool, error)
	QueryVenueFillsSince(context.Context, string, int64) ([]exec.FillRow, error)
}

type ChartSource interface {
	QueryChartWindow(wsmsg.QueryChartWindowArgs) wsmsg.QueryChartWindowResult
}

type LocateSource interface {
	ProviderFor(exec.VenueID) (locates.Provider, bool)
}

type QuerySources struct {
	Fills   FillSource
	Charts  ChartSource
	Locates LocateSource
	Clock   clock.Clock
}

type ReadQueries struct {
	sources QuerySources
}

func NewReadQueries(sources QuerySources) *ReadQueries {
	return &ReadQueries{sources: sources}
}

func (q *ReadQueries) QueryChartWindow(_ context.Context, a QueryChartWindowArgs) (QueryChartWindowResult, error) {
	if q == nil || q.sources.Charts == nil {
		return QueryChartWindowResult{Symbol: a.Symbol, Timeframe: a.Timeframe, Bars: []Bar{}, Indicators: []IndicatorSeriesWindow{}}, ErrQueriesUnavailable
	}
	if a.Symbol == "" || a.Timeframe == "" || (a.TailBars > 0) == (a.FromMs < a.ToMs) {
		return emptyChart(a), nil
	}
	return chartFromWire(q.sources.Charts.QueryChartWindow(wsmsg.QueryChartWindowArgs{
		Symbol: a.Symbol, Timeframe: a.Timeframe, FromMs: a.FromMs, ToMs: a.ToMs,
		TailBars: a.TailBars, IndicatorSeriesKeys: a.IndicatorSeriesKeys, SkipBars: a.SkipBars,
	})), nil
}

func (q *ReadQueries) QueryFills(_ context.Context, a QueryFillsArgs) ([]Fill, error) {
	if q == nil || q.sources.Fills == nil {
		return nil, ErrQueriesUnavailable
	}
	rows, err := q.sources.Fills.QueryFills(a.Symbol, a.FromMs, a.ToMs)
	if err != nil {
		return nil, err
	}
	out := make([]Fill, 0, len(rows))
	for _, row := range rows {
		out = append(out, fillFromRow(row))
	}
	return out, nil
}

func (q *ReadQueries) QueryCycleFills(ctx context.Context, a QueryCycleFillsArgs) (QueryCycleFillsResult, error) {
	out := QueryCycleFillsResult{Carried: []CarriedPosition{}, Fills: []Fill{}}
	if q == nil || q.sources.Fills == nil || q.sources.Clock == nil {
		return out, ErrQueriesUnavailable
	}
	if a.Venue == "" {
		return out, nil
	}
	out.CycleStartMs = session.TradingCycleStart(q.sources.Clock.Now()).UnixMilli()
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := q.sources.Fills.QueryVenueFillsSince(ctx, a.Venue, out.CycleStartMs)
	if err != nil {
		return out, err
	}
	cp, found, err := q.sources.Fills.LoadCycleCheckpoint(exec.VenueID(a.Venue))
	if err != nil {
		return out, err
	}
	if found && cp.StartMs == out.CycleStartMs {
		for symbol, position := range cp.Positions {
			if position.Carried != 0 {
				out.Carried = append(out.Carried, CarriedPosition{Symbol: symbol, Qty: position.Carried})
			}
		}
	}
	for _, row := range rows {
		out.Fills = append(out.Fills, fillFromRow(row))
	}
	return out, nil
}

func (q *ReadQueries) ExportFills(ctx context.Context, a ExportFillsArgs) (ExportFillsResult, error) {
	if q == nil || q.sources.Fills == nil || q.sources.Clock == nil {
		return ExportFillsResult{}, ErrQueriesUnavailable
	}
	fromMs, toMs, err := exec.ResolveExportRange(a.Preset, a.From, a.To, q.sources.Clock.Now())
	if err != nil {
		return ExportFillsResult{Error: err.Error()}, nil
	}
	rows, err := q.sources.Fills.ExportFills(ctx, a.Venue, fromMs, toMs)
	if err != nil {
		return ExportFillsResult{}, err
	}
	csv, err := exec.BuildFillsCSV(rows)
	if err != nil {
		return ExportFillsResult{}, err
	}
	return ExportFillsResult{CSV: csv, Count: len(rows)}, nil
}

func (q *ReadQueries) QueryLocateEligibility(_ context.Context, a QueryLocateEligibilityArgs) (LocateEligibility, error) {
	if q == nil {
		return LocateEligibility{}, ErrQueriesUnavailable
	}
	if a.Venue == "" || strings.TrimSpace(a.Symbol) == "" {
		return LocateEligibility{Error: "bad args"}, nil
	}
	provider, ok := q.provider(a.Venue)
	if !ok {
		return LocateEligibility{Error: "locate unsupported for selected venue"}, nil
	}
	eligibility, found := provider.LocateEligibility(a.Symbol)
	return LocateEligibility{
		Supported: true, Found: found, BorrowStatus: eligibility.BorrowStatus,
		Shortable: eligibility.Shortable, Marginable: eligibility.Marginable, Tradable: eligibility.Tradable,
	}, nil
}

func (q *ReadQueries) QueryLocateQuotes(ctx context.Context, a QueryLocateQuotesArgs) (LocateQuoteResult, error) {
	out := LocateQuoteResult{Quotes: []LocateQuote{}, Errors: []LocateQuoteError{}}
	if q == nil {
		return out, ErrQueriesUnavailable
	}
	if a.Venue == "" || len(a.Symbols) == 0 {
		out.Error = "bad args"
		return out, nil
	}
	provider, ok := q.provider(a.Venue)
	if !ok {
		out.Error = "locate unsupported for selected venue"
		return out, nil
	}
	result, err := provider.QuoteLocates(ctx, a.Symbols)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	for _, quote := range result.Quotes {
		out.Quotes = append(out.Quotes, LocateQuote{Symbol: quote.Symbol, AvailableQty: quote.AvailableQty, Price: quote.Price, QuotedAt: formatTime(quote.QuotedAt)})
	}
	for _, item := range result.Errors {
		out.Errors = append(out.Errors, LocateQuoteError{Symbol: item.Symbol, Code: item.Code, Message: item.Message})
	}
	return out, nil
}

func (q *ReadQueries) QueryLocates(ctx context.Context, a QueryLocatesArgs) (LocateListResult, error) {
	out := LocateListResult{Locates: []LocateRecord{}}
	if q == nil {
		return out, ErrQueriesUnavailable
	}
	if a.Venue == "" {
		out.Error = "bad args"
		return out, nil
	}
	provider, ok := q.provider(a.Venue)
	if !ok {
		out.Error = "locate unsupported for selected venue"
		return out, nil
	}
	page, err := provider.ListLocates(ctx, locates.ListFilter{
		Status: a.Status, Symbol: a.Symbol, Start: a.Start, End: a.End, Limit: a.Limit, PageToken: a.PageToken,
	})
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.NextPageToken = page.NextPageToken
	for _, record := range page.Locates {
		out.Locates = append(out.Locates, recordFromLocate(record))
	}
	return out, nil
}

func (q *ReadQueries) QueryLocate(ctx context.Context, a QueryLocateArgs) (LocateRecord, error) {
	if q == nil {
		return LocateRecord{}, ErrQueriesUnavailable
	}
	if a.Venue == "" || a.LocateID == "" {
		return LocateRecord{Error: "bad args"}, nil
	}
	provider, ok := q.provider(a.Venue)
	if !ok {
		return LocateRecord{Error: "locate unsupported for selected venue"}, nil
	}
	record, err := provider.GetLocate(ctx, a.LocateID)
	if err != nil {
		return LocateRecord{Error: err.Error()}, nil
	}
	return recordFromLocate(record), nil
}

func (q *ReadQueries) provider(venue string) (locates.Provider, bool) {
	if q.sources.Locates == nil {
		return nil, false
	}
	provider, ok := q.sources.Locates.ProviderFor(exec.VenueID(venue))
	return provider, ok && provider != nil
}

func fillFromRow(row exec.FillRow) Fill {
	return Fill{Venue: row.Venue, OrderID: row.OrderID, Symbol: row.Symbol, Side: Side(row.Side), Qty: row.Qty, Price: row.Price, TsMs: row.TsMs}
}

func chartFromWire(result wsmsg.QueryChartWindowResult) QueryChartWindowResult {
	out := QueryChartWindowResult{
		Symbol: result.Symbol, Timeframe: result.Timeframe, FromMs: result.FromMs, ToMs: result.ToMs,
		Bars: make([]Bar, 0, len(result.Bars)), Indicators: make([]IndicatorSeriesWindow, 0, len(result.Indicators)), HistoryRevision: result.HistoryRevision,
	}
	for _, bar := range result.Bars {
		out.Bars = append(out.Bars, Bar{Symbol: bar.Symbol, Timeframe: bar.Timeframe, BucketStart: bar.BucketStart, O: bar.O, H: bar.H, L: bar.L, C: bar.C, V: bar.V, InProgress: bar.InProgress, Gap: bar.Gap, VolumeOnly: bar.VolumeOnly})
	}
	for _, series := range result.Indicators {
		window := IndicatorSeriesWindow{SeriesKey: series.SeriesKey, Points: make([]IndicatorPoint, 0, len(series.Points))}
		for _, point := range series.Points {
			window.Points = append(window.Points, IndicatorPoint{TimeMs: point.TimeMs, Value: point.Value})
		}
		out.Indicators = append(out.Indicators, window)
	}
	return out
}

func emptyChart(a QueryChartWindowArgs) QueryChartWindowResult {
	return QueryChartWindowResult{Symbol: a.Symbol, Timeframe: a.Timeframe, Bars: []Bar{}, Indicators: []IndicatorSeriesWindow{}}
}

func recordFromLocate(record locates.Record) LocateRecord {
	return LocateRecord{
		ID: record.ID, Symbol: record.Symbol, RequestedQty: record.RequestedQty, LimitPrice: record.LimitPrice,
		AllOrNone: record.AllOrNone, Status: record.Status, CreatedAt: formatTime(record.CreatedAt), LocatedQty: record.LocatedQty,
		LocatedPrice: record.LocatedPrice, TotalFee: record.TotalFee, ExpiresAt: formatTime(record.ExpiresAt),
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}
