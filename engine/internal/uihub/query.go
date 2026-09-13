package uihub

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/buildinfo"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/locates"
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
	clk     clock.Clock
	locates LocateRegistry

	eligibility         EligibilityRegistry
	eligibilityMu       sync.Mutex
	eligibilityCache    map[venueEligibilityKey]venueEligibilityCacheEntry
	eligibilityInflight map[venueEligibilityKey]*venueEligibilityCall
}

const venueEligibilityTTL = time.Minute

type venueEligibilityKey struct {
	venue  exec.VenueID
	symbol string
}

type venueEligibilityCacheEntry struct {
	value     wsmsg.VenueInstrumentEligibility
	expiresAt time.Time
}

type venueEligibilityCall struct {
	done  chan struct{}
	value wsmsg.VenueInstrumentEligibility
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
	return q.handleContext(context.Background(), name, args)
}

func (q *queries) handleAsync(ctx context.Context, name string, args json.RawMessage, reply func(any)) bool {
	if !isAsyncQuery(name) {
		return false
	}
	go func() { reply(q.handleContext(ctx, name, args)) }()
	return true
}

func isAsyncQuery(name string) bool {
	switch name {
	case "QueryVenueInstrumentEligibility", "QueryLocateEligibility", "QueryLocateQuotes", "QueryLocates", "QueryLocate":
		return true
	default:
		return false
	}
}

func (q *queries) handleContext(ctx context.Context, name string, args json.RawMessage) any {
	switch name {
	case "QueryAppInfo":
		return wsmsg.AppInfo{Version: buildinfo.Version}
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
	case "QueryVenueInstrumentEligibility":
		var a wsmsg.QueryVenueInstrumentEligibilityArgs
		if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Venue) == "" || strings.TrimSpace(a.Symbol) == "" {
			return wsmsg.VenueInstrumentEligibility{Error: "bad args"}
		}
		return q.queryVenueInstrumentEligibility(ctx, a.Venue, a.Symbol)
	case "QueryLocateEligibility":
		var a wsmsg.QueryLocateEligibilityArgs
		if err := json.Unmarshal(args, &a); err != nil || a.Venue == "" || strings.TrimSpace(a.Symbol) == "" {
			return wsmsg.LocateEligibility{Error: "bad args"}
		}
		provider, ok := q.provider(a.Venue)
		if !ok {
			return wsmsg.LocateEligibility{Error: "locate unsupported for selected venue"}
		}
		eligibilityProvider, ok := provider.(interface {
			LocateEligibility(string) (locates.Eligibility, bool)
		})
		if !ok {
			return wsmsg.LocateEligibility{Error: "locate eligibility unsupported for selected venue"}
		}
		assetEligibility, found := eligibilityProvider.LocateEligibility(a.Symbol)
		return locateEligibilityToWire(true, found, assetEligibility, "")
	case "QueryLocateQuotes":
		var a wsmsg.QueryLocateQuotesArgs
		if err := json.Unmarshal(args, &a); err != nil || a.Venue == "" || len(a.Symbols) == 0 {
			return wsmsg.LocateQuoteResult{Quotes: []wsmsg.LocateQuote{}, Errors: []wsmsg.LocateQuoteError{}, Error: "bad args"}
		}
		provider, ok := q.provider(a.Venue)
		if !ok {
			return wsmsg.LocateQuoteResult{Quotes: []wsmsg.LocateQuote{}, Errors: []wsmsg.LocateQuoteError{}, Error: "locate unsupported for selected venue"}
		}
		result, err := provider.QuoteLocates(ctx, a.Symbols)
		if err != nil {
			return wsmsg.LocateQuoteResult{Quotes: []wsmsg.LocateQuote{}, Errors: []wsmsg.LocateQuoteError{}, Error: err.Error()}
		}
		return locateQuoteResultToWire(result)
	case "QueryLocates":
		var a wsmsg.QueryLocatesArgs
		if err := json.Unmarshal(args, &a); err != nil || a.Venue == "" {
			return wsmsg.LocateListResult{Locates: []wsmsg.LocateRecord{}, Error: "bad args"}
		}
		provider, ok := q.provider(a.Venue)
		if !ok {
			return wsmsg.LocateListResult{Locates: []wsmsg.LocateRecord{}, Error: "locate unsupported for selected venue"}
		}
		page, err := provider.ListLocates(ctx, locates.ListFilter{
			Status: a.Status, Symbol: a.Symbol, Start: a.Start, End: a.End,
			Limit: a.Limit, PageToken: a.PageToken,
		})
		if err != nil {
			return wsmsg.LocateListResult{Locates: []wsmsg.LocateRecord{}, Error: err.Error()}
		}
		return locatePageToWire(page)
	case "QueryLocate":
		var a wsmsg.QueryLocateArgs
		if err := json.Unmarshal(args, &a); err != nil || a.Venue == "" || a.LocateID == "" {
			return wsmsg.LocateRecord{}
		}
		provider, ok := q.provider(a.Venue)
		if !ok {
			return wsmsg.LocateRecord{}
		}
		record, err := provider.GetLocate(ctx, a.LocateID)
		if err != nil {
			return wsmsg.LocateRecord{}
		}
		return locateRecordToWire(record)
	default:
		return []any{}
	}
}

func (q *queries) provider(venue string) (locates.Provider, bool) {
	if q.locates == nil {
		return nil, false
	}
	provider, ok := q.locates.ProviderFor(exec.VenueID(venue))
	return provider, ok && provider != nil
}

func canonicalEligibilitySymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

func (q *queries) queryVenueInstrumentEligibility(ctx context.Context, venue, symbol string) wsmsg.VenueInstrumentEligibility {
	key := venueEligibilityKey{venue: exec.VenueID(venue), symbol: canonicalEligibilitySymbol(symbol)}
	now := q.clk.Now()
	q.eligibilityMu.Lock()
	if entry, ok := q.eligibilityCache[key]; ok && now.Before(entry.expiresAt) {
		q.eligibilityMu.Unlock()
		return entry.value
	}
	if call, ok := q.eligibilityInflight[key]; ok {
		q.eligibilityMu.Unlock()
		select {
		case <-call.done:
			return call.value
		case <-ctx.Done():
			return wsmsg.VenueInstrumentEligibility{Error: ctx.Err().Error()}
		}
	}
	if q.eligibilityCache == nil {
		q.eligibilityCache = map[venueEligibilityKey]venueEligibilityCacheEntry{}
	}
	if q.eligibilityInflight == nil {
		q.eligibilityInflight = map[venueEligibilityKey]*venueEligibilityCall{}
	}
	call := &venueEligibilityCall{done: make(chan struct{})}
	q.eligibilityInflight[key] = call
	q.eligibilityMu.Unlock()

	value := q.fetchVenueInstrumentEligibility(ctx, key)
	fetchedAt := q.clk.Now()
	q.eligibilityMu.Lock()
	delete(q.eligibilityInflight, key)
	call.value = value
	if value.Error == "" {
		q.eligibilityCache[key] = venueEligibilityCacheEntry{value: value, expiresAt: fetchedAt.Add(venueEligibilityTTL)}
	}
	close(call.done)
	q.eligibilityMu.Unlock()
	return value
}

func (q *queries) fetchVenueInstrumentEligibility(ctx context.Context, key venueEligibilityKey) wsmsg.VenueInstrumentEligibility {
	if q.eligibility == nil {
		return wsmsg.VenueInstrumentEligibility{Error: "venue instrument eligibility unsupported for selected venue"}
	}
	provider, ok := q.eligibility.ProviderFor(key.venue)
	if !ok || provider == nil {
		return wsmsg.VenueInstrumentEligibility{Error: "venue instrument eligibility unsupported for selected venue"}
	}
	value, found, err := provider.VenueInstrumentEligibility(ctx, key.symbol)
	if err != nil {
		slog.Warn("venue instrument eligibility lookup failed", "venue", key.venue, "symbol", key.symbol, "err", err)
		return wsmsg.VenueInstrumentEligibility{Supported: true, Error: err.Error()}
	}
	return wsmsg.VenueInstrumentEligibility{
		Supported: true, Found: found,
		Shortable: value.Shortable, Marginable: value.Marginable, Tradable: value.Tradable,
	}
}

func locateEligibilityToWire(supported, found bool, e locates.Eligibility, errText string) wsmsg.LocateEligibility {
	return wsmsg.LocateEligibility{
		Supported: supported, Found: found, BorrowStatus: e.BorrowStatus,
		Shortable: e.Shortable, Marginable: e.Marginable, Tradable: e.Tradable,
		Error: errText,
	}
}

func locateQuoteResultToWire(result locates.QuoteResult) wsmsg.LocateQuoteResult {
	out := wsmsg.LocateQuoteResult{
		Quotes: make([]wsmsg.LocateQuote, 0, len(result.Quotes)),
		Errors: make([]wsmsg.LocateQuoteError, 0, len(result.Errors)),
	}
	for _, quote := range result.Quotes {
		out.Quotes = append(out.Quotes, wsmsg.LocateQuote{
			Symbol: quote.Symbol, AvailableQty: quote.AvailableQty, Price: quote.Price,
			QuotedAt: formatLocateTime(quote.QuotedAt),
		})
	}
	for _, item := range result.Errors {
		out.Errors = append(out.Errors, wsmsg.LocateQuoteError{Symbol: item.Symbol, Code: item.Code, Message: item.Message})
	}
	return out
}

func locateRecordToWire(record locates.Record) wsmsg.LocateRecord {
	return wsmsg.LocateRecord{
		ID: record.ID, Symbol: record.Symbol, RequestedQty: record.RequestedQty,
		LimitPrice: record.LimitPrice, AllOrNone: record.AllOrNone, Status: record.Status,
		CreatedAt: formatLocateTime(record.CreatedAt), LocatedQty: record.LocatedQty,
		LocatedPrice: record.LocatedPrice, TotalFee: record.TotalFee, ExpiresAt: formatLocateTime(record.ExpiresAt),
	}
}

func locatePageToWire(page locates.Page) wsmsg.LocateListResult {
	out := wsmsg.LocateListResult{Locates: make([]wsmsg.LocateRecord, 0, len(page.Locates)), NextPageToken: page.NextPageToken}
	for _, record := range page.Locates {
		out.Locates = append(out.Locates, locateRecordToWire(record))
	}
	return out
}

func formatLocateTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}
