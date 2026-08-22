package uiapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/locates"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type querySourceFake struct {
	rows          []exec.FillRow
	fillErr       error
	exportRows    []exec.ExportFillRow
	exportErr     error
	cycleRows     []exec.FillRow
	cycleErr      error
	checkpoint    exec.CycleCheckpoint
	found         bool
	checkpointErr error
}

func (f *querySourceFake) QueryFills(string, int64, int64) ([]exec.FillRow, error) {
	return f.rows, f.fillErr
}

func (f *querySourceFake) ExportFills(context.Context, string, int64, int64) ([]exec.ExportFillRow, error) {
	return f.exportRows, f.exportErr
}

func (f *querySourceFake) LoadCycleCheckpoint(exec.VenueID) (exec.CycleCheckpoint, bool, error) {
	return f.checkpoint, f.found, f.checkpointErr
}

func (f *querySourceFake) QueryVenueFillsSince(context.Context, string, int64) ([]exec.FillRow, error) {
	return f.cycleRows, f.cycleErr
}

type chartSourceFake struct{ result wsmsg.QueryChartWindowResult }

func (f chartSourceFake) QueryChartWindow(wsmsg.QueryChartWindowArgs) wsmsg.QueryChartWindowResult {
	return f.result
}

type locateProviderFake struct {
	eligibility locates.Eligibility
	found       bool
	quotes      locates.QuoteResult
	page        locates.Page
	record      locates.Record
	err         error
	filter      locates.ListFilter
	locateID    string
}

func (f *locateProviderFake) LocateEligibility(string) (locates.Eligibility, bool) {
	return f.eligibility, f.found
}
func (f *locateProviderFake) QuoteLocates(context.Context, []string) (locates.QuoteResult, error) {
	return f.quotes, f.err
}
func (f *locateProviderFake) CreateLocate(context.Context, locates.Request) (locates.Record, error) {
	return f.record, f.err
}
func (f *locateProviderFake) ListLocates(_ context.Context, filter locates.ListFilter) (locates.Page, error) {
	f.filter = filter
	return f.page, f.err
}
func (f *locateProviderFake) GetLocate(_ context.Context, id string) (locates.Record, error) {
	f.locateID = id
	return f.record, f.err
}

func TestReadQueriesRoundTripValuesOptionalEnums(t *testing.T) {
	borrow := "hard_to_borrow"
	shortable := true
	provider := &locateProviderFake{
		eligibility: locates.Eligibility{BorrowStatus: &borrow, Shortable: &shortable}, found: true,
		quotes: locates.QuoteResult{
			Quotes: []locates.Quote{{Symbol: "US.AAPL", AvailableQty: 1200, Price: "0.012300", QuotedAt: time.Date(2026, 7, 6, 13, 30, 0, 123456000, time.UTC)}},
			Errors: []locates.QuoteError{{Symbol: "US.TSLA", Code: "not_quotable", Message: "no locate"}},
		},
		page:   locates.Page{Locates: []locates.Record{{ID: "loc-1", Symbol: "US.AAPL", RequestedQty: 500, Status: locates.StatusActive, CreatedAt: time.Unix(0, 0).UTC()}}, NextPageToken: "next"},
		record: locates.Record{ID: "loc-1", Symbol: "US.AAPL"},
	}
	registry := locates.NewRegistry()
	registry.Register(exec.VenueID("alpaca-paper"), provider)
	clk := clock.NewFake(time.Date(2026, 7, 6, 15, 0, 0, 0, time.UTC))
	source := &querySourceFake{rows: []exec.FillRow{{OrderID: "ET1", Symbol: "US.AAPL", Side: "SHORT", Qty: 100, Price: 3.47, TsMs: 5, Venue: "sim"}}}
	q := NewReadQueries(QuerySources{
		Fills: source, Clock: clk, Locates: registry,
		Charts: chartSourceFake{result: wsmsg.QueryChartWindowResult{
			Symbol: "US.AAPL", Timeframe: "1m", FromMs: 10, ToMs: 20,
			Bars:       []wsmsg.Bar{{Symbol: "US.AAPL", Timeframe: "1m", BucketStart: "2026-07-06T15:00:00Z", O: 1, H: 2, L: 0.5, C: 1.5, V: 7}},
			Indicators: []wsmsg.IndicatorSeriesWindow{{SeriesKey: "ema", Points: []wsmsg.IndicatorPoint{{TimeMs: 10, Value: 1.2}}}},
		}},
	})

	fills, err := q.QueryFills(context.Background(), QueryFillsArgs{Symbol: "US.AAPL", FromMs: 0, ToMs: 9})
	if err != nil || len(fills) != 1 || fills[0].Side != SideShort || fills[0].Qty != 100 {
		t.Fatalf("fills = %#v, err=%v", fills, err)
	}
	chart, err := q.QueryChartWindow(context.Background(), QueryChartWindowArgs{Symbol: "US.AAPL", Timeframe: "1m", FromMs: 10, ToMs: 20})
	if err != nil || len(chart.Bars) != 1 || len(chart.Indicators[0].Points) != 1 || chart.Bars[0].C != 1.5 {
		t.Fatalf("chart = %#v, err=%v", chart, err)
	}
	eligibility, err := q.QueryLocateEligibility(context.Background(), QueryLocateEligibilityArgs{Venue: "alpaca-paper", Symbol: "US.AAPL"})
	if err != nil || !eligibility.Supported || eligibility.BorrowStatus == nil || *eligibility.BorrowStatus != borrow || eligibility.Marginable != nil {
		t.Fatalf("eligibility = %#v, err=%v", eligibility, err)
	}
	quotes, err := q.QueryLocateQuotes(context.Background(), QueryLocateQuotesArgs{Venue: "alpaca-paper", Symbols: []string{"US.AAPL", "US.TSLA"}})
	if err != nil || len(quotes.Quotes) != 1 || quotes.Quotes[0].QuotedAt == "" || len(quotes.Errors) != 1 {
		t.Fatalf("quotes = %#v, err=%v", quotes, err)
	}
	page, err := q.QueryLocates(context.Background(), QueryLocatesArgs{Venue: "alpaca-paper", Status: "active", Symbol: "US.AAPL", Limit: 25, PageToken: "page-1"})
	if err != nil || len(page.Locates) != 1 || page.NextPageToken != "next" || provider.filter.PageToken != "page-1" {
		t.Fatalf("page = %#v, filter=%#v, err=%v", page, provider.filter, err)
	}
	record, err := q.QueryLocate(context.Background(), QueryLocateArgs{Venue: "alpaca-paper", LocateID: "loc-1"})
	if err != nil || record.ID != "loc-1" || provider.locateID != "loc-1" {
		t.Fatalf("record = %#v, err=%v", record, err)
	}

	encoded, err := json.Marshal(eligibility)
	if err != nil || !strings.Contains(string(encoded), `"marginable":null`) {
		t.Fatalf("optional model JSON = %s, err=%v", encoded, err)
	}
}

func TestReadQueriesBusinessOutcomesAndInternalFailures(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1789000000000))
	source := &querySourceFake{
		exportRows: []exec.ExportFillRow{{FillID: 12, Symbol: "US.NVDA", Side: "BUY", Qty: 100, Price: 120.5, TsMs: 1789000000000, Venue: "sim"}},
		checkpoint: exec.CycleCheckpoint{StartMs: session.TradingCycleStart(clk.Now()).UnixMilli(), Positions: map[string]exec.CyclePosition{"US.AAPL": {Carried: 4}}}, found: true,
		cycleRows: []exec.FillRow{{Symbol: "US.AAPL", Side: "BUY", Qty: 1, Venue: "sim"}},
	}
	q := NewReadQueries(QuerySources{Fills: source, Clock: clk})
	export, err := q.ExportFills(context.Background(), ExportFillsArgs{Venue: "sim", Preset: "all"})
	if err != nil || export.Count != 1 || !strings.Contains(export.CSV, "etape:sim:12") {
		t.Fatalf("export = %#v, err=%v", export, err)
	}
	invalid, err := q.ExportFills(context.Background(), ExportFillsArgs{Preset: "custom", From: "2026-07-10", To: "2026-07-01"})
	if err != nil || invalid.Error == "" || invalid.Count != 0 {
		t.Fatalf("invalid export = %#v, err=%v", invalid, err)
	}
	cycle, err := q.QueryCycleFills(context.Background(), QueryCycleFillsArgs{Venue: "sim"})
	if err != nil || cycle.CycleStartMs == 0 || len(cycle.Carried) != 1 || len(cycle.Fills) != 1 {
		t.Fatalf("cycle = %#v, err=%v", cycle, err)
	}
	unsupported, err := q.QueryLocateQuotes(context.Background(), QueryLocateQuotesArgs{Venue: "sim", Symbols: []string{"US.AAPL"}})
	if err != nil || unsupported.Error == "" {
		t.Fatalf("unsupported locate = %#v, err=%v", unsupported, err)
	}

	source.fillErr = errors.New("store unavailable")
	if _, err := q.QueryFills(context.Background(), QueryFillsArgs{}); !errors.Is(err, source.fillErr) {
		t.Fatalf("storage error = %v, want %v", err, source.fillErr)
	}
	source.exportErr = errors.New("export unavailable")
	if _, err := q.ExportFills(context.Background(), ExportFillsArgs{Preset: "all"}); !errors.Is(err, source.exportErr) {
		t.Fatalf("export storage error = %v, want %v", err, source.exportErr)
	}
}
