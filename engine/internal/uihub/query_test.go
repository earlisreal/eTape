package uihub

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type spyFills struct {
	rows []exec.FillRow
	err  error
	sym  string

	exportRowsMulti          []exec.ExportFillRow
	exportErr                error
	exportVenue              string
	exportFromMs, exportToMs int64
}

func (s *spyFills) QueryFills(symbol string, _, _ int64) ([]exec.FillRow, error) {
	s.sym = symbol
	return s.rows, s.err
}

func (s *spyFills) ExportFills(_ context.Context, venue string, fromMs, toMs int64) ([]exec.ExportFillRow, error) {
	s.exportVenue, s.exportFromMs, s.exportToMs = venue, fromMs, toMs
	return s.exportRowsMulti, s.exportErr
}

type emptyJournal struct{}

func (e *emptyJournal) JournalDays() ([]string, error) { return []string{}, nil }

func TestQueryFillsReturnsFills(t *testing.T) {
	f := &spyFills{rows: []exec.FillRow{{OrderID: "ET1", Symbol: "US.AAPL", Side: "BUY", Qty: 100, Price: 3.47, TsMs: 5, Venue: "sim"}}}
	q := newQueries(f, &emptyJournal{}, clock.NewFake(time.Now()))
	out := q.handle("QueryFills", json.RawMessage(`{"symbol":"US.AAPL","fromMs":0,"toMs":9}`))
	fills, ok := out.([]wsmsg.Fill)
	if !ok || len(fills) != 1 {
		t.Fatalf("expected []wsmsg.Fill of len 1, got %T %v", out, out)
	}
	if fills[0].Side != wsmsg.SideBuy || fills[0].OrderID != "ET1" || f.sym != "US.AAPL" {
		t.Fatalf("fill map wrong: %+v (queried %q)", fills[0], f.sym)
	}
}

func TestQueryFillsEmptyOnError(t *testing.T) {
	q := newQueries(&spyFills{err: errors.New("boom")}, &emptyJournal{}, clock.NewFake(time.Now()))
	out := q.handle("QueryFills", json.RawMessage(`{"symbol":"X","fromMs":0,"toMs":1}`))
	// must marshal to "[]", not null, so the UI promise resolves to []
	b, _ := json.Marshal(out)
	if string(b) != "[]" {
		t.Fatalf("error must yield empty []wsmsg.Fill (never nil/hang); marshaled to %s", b)
	}
}

func TestQueryFillsMalformedArgsReturnsEmptySlice(t *testing.T) {
	q := newQueries(&spyFills{}, &emptyJournal{}, clock.NewFake(time.Now()))
	out := q.handle("QueryFills", json.RawMessage(`invalid-json`))
	// malformed args must marshal to "[]", not null, so the UI promise resolves to []
	b, _ := json.Marshal(out)
	if string(b) != "[]" {
		t.Fatalf("malformed args must yield empty []wsmsg.Fill (never nil/hang); marshaled to %s", b)
	}
}

func TestQueryUnknownReturnsEmptySlice(t *testing.T) {
	q := newQueries(&spyFills{}, &emptyJournal{}, clock.NewFake(time.Now()))
	out := q.handle("Nope", json.RawMessage(`{}`))
	// must be a non-nil, JSON-marshals-to-[] value so the UI promise resolves to []
	b, _ := json.Marshal(out)
	if string(b) != "[]" {
		t.Fatalf("unknown query must resolve to []; marshaled to %s", b)
	}
}

func TestExportFillsReturnsCSV(t *testing.T) {
	f := &spyFills{exportRowsMulti: []exec.ExportFillRow{
		{FillID: 12, Symbol: "US.NVDA", Side: "BUY", Qty: 100, Price: 120.5, TsMs: 1789000000000, Venue: "sim"},
	}}
	clk := clock.NewFake(time.UnixMilli(1789000000000))
	q := newQueries(f, &emptyJournal{}, clk)
	out := q.handle("ExportFills", json.RawMessage(`{"venue":"sim","preset":"all"}`))
	res, ok := out.(wsmsg.ExportFillsResult)
	if !ok {
		t.Fatalf("expected wsmsg.ExportFillsResult, got %T %v", out, out)
	}
	if res.Count != 1 {
		t.Fatalf("Count = %d, want 1", res.Count)
	}
	if !strings.Contains(res.CSV, "datetime,symbol,action,price,shares,fees,externalId") {
		t.Fatalf("CSV missing header: %q", res.CSV)
	}
	if !strings.Contains(res.CSV, "etape:sim:12") {
		t.Fatalf("CSV missing mapped row: %q", res.CSV)
	}
	if f.exportVenue != "sim" {
		t.Fatalf("ExportFills called with venue %q, want %q", f.exportVenue, "sim")
	}
}

func TestExportFillsMalformedArgsReturnsEmptyResult(t *testing.T) {
	q := newQueries(&spyFills{}, &emptyJournal{}, clock.NewFake(time.Now()))
	out := q.handle("ExportFills", json.RawMessage(`invalid-json`))
	b, _ := json.Marshal(out)
	if string(b) != `{"csv":"","count":0}` {
		t.Fatalf("malformed args must yield empty ExportFillsResult (never nil/hang); marshaled to %s", b)
	}
}

func TestExportFillsEmptyOnStoreError(t *testing.T) {
	q := newQueries(&spyFills{exportErr: errors.New("boom")}, &emptyJournal{}, clock.NewFake(time.Now()))
	out := q.handle("ExportFills", json.RawMessage(`{"venue":"sim","preset":"all"}`))
	b, _ := json.Marshal(out)
	if string(b) != `{"csv":"","count":0}` {
		t.Fatalf("store error must yield empty ExportFillsResult; marshaled to %s", b)
	}
}

func TestExportFillsInvalidCustomRangeReturnsEmptyResult(t *testing.T) {
	q := newQueries(&spyFills{}, &emptyJournal{}, clock.NewFake(time.Now()))
	out := q.handle("ExportFills", json.RawMessage(`{"venue":"sim","preset":"custom","from":"2026-07-10","to":"2026-07-01"}`))
	b, _ := json.Marshal(out)
	if string(b) != `{"csv":"","count":0}` {
		t.Fatalf("invalid custom range (from after to) must yield empty ExportFillsResult; marshaled to %s", b)
	}
}

type spyJournal struct{ days []string }

func (s *spyJournal) JournalDays() ([]string, error) { return s.days, nil }

func TestListReplayDays(t *testing.T) {
	q := newQueries(nil, &spyJournal{days: []string{"2026-07-06", "2026-07-05"}}, clock.System{})
	got := q.handle("ListReplayDays", json.RawMessage(`{}`))
	if !reflect.DeepEqual(got, []string{"2026-07-06", "2026-07-05"}) {
		t.Fatalf("got %#v", got)
	}
}
