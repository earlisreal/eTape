package exec

import (
	"strings"
	"testing"
	"time"
)

// export_test.go exposes Core's internal state to exec_test (the external test
// package) for whitebox assertions. It is compiled only for `go test` — never
// part of the production binary — and deliberately imports nothing beyond the
// standard library so it cannot introduce the broker/sim <-> exec import cycle
// that forced core_test.go (and core_lifecycle_test.go) into package exec_test.
//
// StateForTest returns Core's live *State. Callers must only read it, and only
// when no other goroutine is concurrently mutating it (e.g. right after Recover,
// before Run starts) to stay race-free.
func (c *Core) StateForTest() *State { return c.state }

func ms(iso string) int64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		panic(err)
	}
	return t.UnixMilli()
}

func TestResolveExportRangePresets(t *testing.T) {
	// 2026-07-08T18:00:00Z = 14:00 ET (EDT, UTC-4), a Wednesday. Same calendar
	// week/month as the vectors in session/session_test.go's TestBucketStartMs.
	now := time.UnixMilli(ms("2026-07-08T18:00:00Z"))
	cases := []struct {
		name       string
		preset     string
		wantFromMs int64
	}{
		{"today", "today", ms("2026-07-08T04:00:00Z")}, // ET wall-midnight
		{"week", "week", ms("2026-07-06T04:00:00Z")},   // Monday ET wall-midnight
		{"month", "month", ms("2026-07-01T04:00:00Z")}, // 1st-of-month ET wall-midnight
		{"all", "all", 0},
	}
	for _, c := range cases {
		fromMs, toMs, err := ResolveExportRange(c.preset, "", "", now)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if fromMs != c.wantFromMs {
			t.Errorf("%s: fromMs = %d, want %d", c.name, fromMs, c.wantFromMs)
		}
		if toMs != now.UnixMilli()+1 {
			t.Errorf("%s: toMs = %d, want now+1 = %d", c.name, toMs, now.UnixMilli()+1)
		}
	}
}

func TestResolveExportRangeCustom(t *testing.T) {
	now := time.UnixMilli(ms("2026-07-08T18:00:00Z"))
	fromMs, toMs, err := ResolveExportRange("custom", "2026-07-01", "2026-07-03", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := ms("2026-07-01T04:00:00Z"); fromMs != want { // ET midnight of "from"
		t.Errorf("fromMs = %d, want %d", fromMs, want)
	}
	if want := ms("2026-07-04T04:00:00Z"); toMs != want { // ET midnight of the day AFTER "to" (inclusive of "to")
		t.Errorf("toMs = %d, want %d", toMs, want)
	}
}

func TestResolveExportRangeCustomAcrossDST(t *testing.T) {
	// The "to" day (2026-03-08) is the day of the US spring-forward transition
	// (2am ET); the next-midnight upper bound must land on the correct EDT
	// wall-clock instant despite the offset change within the range.
	now := time.UnixMilli(ms("2026-03-10T00:00:00Z"))
	fromMs, toMs, err := ResolveExportRange("custom", "2026-03-06", "2026-03-08", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := ms("2026-03-06T05:00:00Z"); fromMs != want { // EST midnight, UTC-5
		t.Errorf("fromMs = %d, want %d", fromMs, want)
	}
	if want := ms("2026-03-09T04:00:00Z"); toMs != want { // EDT midnight, UTC-4 (DST already sprang forward)
		t.Errorf("toMs = %d, want %d", toMs, want)
	}
}

func TestResolveExportRangeCustomErrors(t *testing.T) {
	now := time.Now()
	if _, _, err := ResolveExportRange("custom", "not-a-date", "2026-07-03", now); err == nil {
		t.Error("expected error for unparseable from date")
	}
	if _, _, err := ResolveExportRange("custom", "2026-07-01", "nope", now); err == nil {
		t.Error("expected error for unparseable to date")
	}
	if _, _, err := ResolveExportRange("custom", "2026-07-05", "2026-07-01", now); err == nil {
		t.Error("expected error when from is after to")
	}
}

func TestResolveExportRangeUnknownPreset(t *testing.T) {
	if _, _, err := ResolveExportRange("bogus", "", "", time.Now()); err == nil {
		t.Error("expected error for unknown preset")
	}
}

func TestBuildFillsCSVSideMapping(t *testing.T) {
	cases := []struct{ side, wantAction string }{
		{"BUY", "BUY"}, {"COVER", "BUY"}, {"SELL", "SELL"}, {"SHORT", "SELL"},
	}
	for _, c := range cases {
		rows := []ExportFillRow{{FillID: 1, Symbol: "US.AAPL", Side: c.side, Qty: 10, Price: 100, TsMs: ms("2026-07-08T14:00:00Z"), Venue: "sim"}}
		got, err := BuildFillsCSV(rows)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.side, err)
		}
		lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("%s: expected header + 1 row, got %d lines: %q", c.side, len(lines), got)
		}
		fields := strings.Split(lines[1], ",")
		if fields[2] != c.wantAction {
			t.Errorf("%s: action = %q, want %q", c.side, fields[2], c.wantAction)
		}
	}
}

func TestBuildFillsCSVExactFixture(t *testing.T) {
	rows := []ExportFillRow{
		{FillID: 12, Symbol: "US.NVDA", Side: "BUY", Qty: 100, Price: 120.5, TsMs: ms("2026-07-10T13:31:05Z"), Venue: "sim"},
		{FillID: 19, Symbol: "US.NVDA", Side: "SELL", Qty: 100, Price: 121.25, TsMs: ms("2026-07-10T13:44:02Z"), Venue: "sim"},
	}
	got, err := BuildFillsCSV(rows)
	if err != nil {
		t.Fatal(err)
	}
	want := "datetime,symbol,action,price,shares,fees,externalId\n" +
		"2026-07-10T09:31:05,NVDA,BUY,120.5,100,0,etape:sim:12\n" +
		"2026-07-10T09:44:02,NVDA,SELL,121.25,100,0,etape:sim:19\n"
	if got != want {
		t.Fatalf("CSV mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestBuildFillsCSVDatetimeAcrossDST(t *testing.T) {
	cases := []struct {
		name, tsIso, wantLocal string
	}{
		{"EST (winter)", "2026-01-15T14:30:00Z", "2026-01-15T09:30:00"}, // UTC-5
		{"EDT (summer)", "2026-07-15T14:30:00Z", "2026-07-15T10:30:00"}, // UTC-4
	}
	for _, c := range cases {
		rows := []ExportFillRow{{FillID: 1, Symbol: "US.AAPL", Side: "BUY", Qty: 1, Price: 1, TsMs: ms(c.tsIso), Venue: "sim"}}
		got, err := BuildFillsCSV(rows)
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Split(strings.Split(strings.TrimRight(got, "\n"), "\n")[1], ",")
		if fields[0] != c.wantLocal {
			t.Errorf("%s: datetime = %q, want %q", c.name, fields[0], c.wantLocal)
		}
	}
}

func TestBuildFillsCSVSymbolStrip(t *testing.T) {
	cases := []struct{ in, want string }{
		{"US.AAPL", "AAPL"},
		{"US.BRK.B", "BRK.B"},
	}
	for _, c := range cases {
		rows := []ExportFillRow{{FillID: 1, Symbol: c.in, Side: "BUY", Qty: 1, Price: 1, TsMs: ms("2026-07-08T14:00:00Z"), Venue: "sim"}}
		got, err := BuildFillsCSV(rows)
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Split(strings.Split(strings.TrimRight(got, "\n"), "\n")[1], ",")
		if fields[1] != c.want {
			t.Errorf("%s: symbol = %q, want %q", c.in, fields[1], c.want)
		}
	}
}

func TestBuildFillsCSVEmptyIsHeaderOnly(t *testing.T) {
	got, err := BuildFillsCSV(nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "datetime,symbol,action,price,shares,fees,externalId\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildFillsCSVQuotesFieldsContainingCommas(t *testing.T) {
	// Contrived: no real symbol contains a comma, but this proves
	// encoding/csv's quoting kicks in instead of producing a corrupt row.
	rows := []ExportFillRow{{FillID: 1, Symbol: "US.A,B", Side: "BUY", Qty: 1, Price: 1, TsMs: ms("2026-07-08T14:00:00Z"), Venue: "sim"}}
	got, err := BuildFillsCSV(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"A,B"`) {
		t.Fatalf("expected the comma-containing symbol to be quoted, got %q", got)
	}
}
