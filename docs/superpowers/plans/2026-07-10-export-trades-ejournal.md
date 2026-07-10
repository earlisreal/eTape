# Export Trades → eJournal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an "Export" action to the Account panel that downloads a CSV of the
selected venue's fills (date-filtered), formatted for import into eJournal.

**Architecture:** A new venue+range-scoped store query (`Store.ExportFills`) feeds two
pure Go helpers (`exec.ResolveExportRange`, `exec.BuildFillsCSV`) wired behind a new
`ExportFills` WS query in uihub. The UI adds an `ExportTradesPopover` (presets + custom
date range + Download) triggered from a button in the Account panel header, downloading
the CSV via the existing Blob/anchor-click idiom.

**Tech Stack:** Go 1.26 (engine), TypeScript + React + Vite (`ui/`), stdlib
`encoding/csv`, `github.com/earlisreal/eTape/engine/internal/session` (ET calendar),
Vitest + Testing Library.

## Global Constraints

- CSV column order is fixed: `datetime,symbol,action,price,shares,fees,externalId`
  (see the design doc, `docs/superpowers/specs/2026-07-10-export-trades-ejournal-design.md`)
  — never reorder or rename these columns; eJournal's Generic importer reads columns
  0–5 positionally.
- `datetime` is ET wall-clock, ISO-local, **no timezone suffix**, seconds precision:
  Go layout `"2006-01-02T15:04:05"`.
- `fees` is always the literal `"0"`.
- `externalId` is always exactly `etape:{venue}:{fillId}`.
- No SQLite schema change — `fills.fill_id` already exists.
- ET time math always goes through `session.Loc()` / `session.BucketStartMs` —
  never call `time.LoadLocation` directly in new code.
- Every task ends with `go build ./...` (Go tasks) or the relevant `npx vitest run`
  (UI tasks) passing, then a commit.

---

### Task 1: Store layer — `ExportFillRow` + `Store.ExportFills`

**Files:**
- Modify: `engine/internal/exec/types.go` (add `ExportFillRow` after `FillRow`, ~line 337)
- Modify: `engine/internal/store/exec.go` (add `ExportFills` method)
- Test: `engine/internal/store/exec_test.go` (add `TestExportFillsByVenueAndRange`)

**Interfaces:**
- Produces: `exec.ExportFillRow{ FillID int64; Symbol, Side string; Qty, Price float64; TsMs int64; Venue string }` and `(*store.Store).ExportFills(ctx context.Context, venue string, fromMs, toMs int64) ([]exec.ExportFillRow, error)` — Task 2/3 (via `exec` package) and Task 5 (uihub) both depend on these exact names/signatures.

- [ ] **Step 1: Write the failing test**

Add to `engine/internal/store/exec_test.go`:

```go
// TestExportFillsByVenueAndRange exercises ExportFills' two distinguishing
// properties versus QueryFills/QueryFillsSince: it is scoped to ONE venue,
// and it is a closed [fromMs, toMs) range (not "since"), and it carries
// fill_id (the PK) for the exporter's externalId.
func TestExportFillsByVenueAndRange(t *testing.T) {
	s := openTestStore(t)
	mk := func(ts int64, oid, venue string) exec.EventEnvelope {
		return exec.EventEnvelope{TsMs: ts, Source: "ws", Venue: venue, OrderID: oid, Kind: "order_filled", Payload: []byte(`{}`)}
	}
	fill := func(oid, symbol, venue string, ts int64) *exec.FillRow {
		return &exec.FillRow{OrderID: oid, Symbol: symbol, Side: "BUY", Qty: 1, Price: 10, TsMs: ts, Venue: venue}
	}
	// Other venue: excluded regardless of ts.
	if _, err := s.AppendExecEvent(mk(2000, "other-venue", "sim-2"), fill("other-venue", "MSFT", "sim-2", 2000)); err != nil {
		t.Fatal(err)
	}
	// Same venue, before the range: excluded.
	if _, err := s.AppendExecEvent(mk(999, "before", "sim-1"), fill("before", "AAPL", "sim-1", 999)); err != nil {
		t.Fatal(err)
	}
	// Same venue, in range (out of insertion order): included, ascending by ts.
	if _, err := s.AppendExecEvent(mk(2000, "second", "sim-1"), fill("second", "AAPL", "sim-1", 2000)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendExecEvent(mk(1000, "first", "sim-1"), fill("first", "AAPL", "sim-1", 1000)); err != nil {
		t.Fatal(err)
	}
	// Same venue, AT the upper bound: excluded (toMs is exclusive).
	if _, err := s.AppendExecEvent(mk(3000, "at-upper", "sim-1"), fill("at-upper", "AAPL", "sim-1", 3000)); err != nil {
		t.Fatal(err)
	}
	got, err := s.ExportFills(context.Background(), "sim-1", 1000, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 fills, got %d: %+v", len(got), got)
	}
	if got[0].TsMs != 1000 || got[1].TsMs != 2000 {
		t.Fatalf("expected ts order [1000,2000], got [%d,%d]", got[0].TsMs, got[1].TsMs)
	}
	if got[0].Venue != "sim-1" || got[1].Venue != "sim-1" {
		t.Fatalf("venue filter leaked: %+v", got)
	}
	if got[0].FillID == 0 || got[1].FillID == 0 || got[0].FillID == got[1].FillID {
		t.Fatalf("expected distinct nonzero fill_id PKs, got %d and %d", got[0].FillID, got[1].FillID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/store/... -run TestExportFillsByVenueAndRange -v`
Expected: FAIL — `s.ExportFills undefined (type *Store has no field or method ExportFills)` (also a compile error on `exec.ExportFillRow` not existing, but the type isn't referenced directly in this test, so the failure is specifically the missing method).

- [ ] **Step 3: Add `ExportFillRow` to `engine/internal/exec/types.go`**

Insert immediately after the `FillRow` struct (currently ends at line 337):

```go
// ExportFillRow is a fills row enriched with its table PK (fill_id), the
// stable key the trade exporter mints externalIds from. Separate from
// FillRow (which has no PK) so the export path is the only reader of
// fill_id.
type ExportFillRow struct {
	FillID int64
	Symbol string
	Side   string
	Qty    float64
	Price  float64
	TsMs   int64
	Venue  string
}
```

- [ ] **Step 4: Add `Store.ExportFills` to `engine/internal/store/exec.go`**

Append after `QueryFillsSince` (end of file):

```go

// ExportFills returns fills for one venue in [fromMs, toMs), ascending by
// (ts, fill_id) — the trade-export input. Unlike QueryFills (single-symbol)
// and QueryFillsSince (all-venues, no upper bound, no fill_id), it is
// venue-scoped, range-bounded, and carries fill_id so the exporter can mint
// stable externalIds. ctx-aware so a slow/canceled export doesn't hang.
func (s *Store) ExportFills(ctx context.Context, venue string, fromMs, toMs int64) ([]exec.ExportFillRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT fill_id, symbol, side, qty, price, ts, venue
         FROM fills WHERE venue = ? AND ts >= ? AND ts < ? ORDER BY ts, fill_id`, venue, fromMs, toMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exec.ExportFillRow
	for rows.Next() {
		var f exec.ExportFillRow
		if err := rows.Scan(&f.FillID, &f.Symbol, &f.Side, &f.Qty, &f.Price, &f.TsMs, &f.Venue); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd engine && go test ./internal/store/... -run TestExportFillsByVenueAndRange -v`
Expected: PASS

- [ ] **Step 6: Run the full store package + build to check for regressions**

Run: `cd engine && go build ./... && go test ./internal/store/... ./internal/exec/...`
Expected: `ok` for both packages, build succeeds.

- [ ] **Step 7: Commit**

```bash
git add engine/internal/exec/types.go engine/internal/store/exec.go engine/internal/store/exec_test.go
git commit -m "feat(store): add venue+range scoped ExportFills query"
```

---

### Task 2: Engine — `exec.ResolveExportRange` (pure range resolver)

**Files:**
- Create: `engine/internal/exec/export.go`
- Test: Create `engine/internal/exec/export_test.go`

**Interfaces:**
- Consumes: nothing from other tasks (uses `session.Loc()`/`session.BucketStartMs` from the existing `session` package).
- Produces: `ResolveExportRange(preset, from, to string, now time.Time) (fromMs, toMs int64, err error)` — Task 5's query handler calls this exact signature.

- [ ] **Step 1: Write the failing tests**

Create `engine/internal/exec/export_test.go`:

```go
package exec

import (
	"testing"
	"time"
)

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/exec/... -run TestResolveExportRange -v`
Expected: FAIL — `undefined: ResolveExportRange`

- [ ] **Step 3: Write the minimal implementation**

Create `engine/internal/exec/export.go`:

```go
package exec

import (
	"fmt"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// exportDateLayout is the custom-range wire format: "YYYY-MM-DD", an ET
// calendar date with no zone — matches an <input type="date">'s value.
const exportDateLayout = "2006-01-02"

// ResolveExportRange turns a preset (or an explicit custom from/to) into the
// [fromMs, toMs) window ExportFills queries. Takes `now` explicitly so it is
// clock-free and deterministic under test; callers pass clk.Now(). ET
// calendar boundaries (session.BucketStartMs) keep "today/week/month" in
// agreement with the rest of the engine's session logic.
func ResolveExportRange(preset, from, to string, now time.Time) (fromMs, toMs int64, err error) {
	nowMs := now.UnixMilli()
	switch preset {
	case "today":
		return session.BucketStartMs(nowMs, session.TFDay), nowMs + 1, nil
	case "week":
		return session.BucketStartMs(nowMs, session.TFWeek), nowMs + 1, nil
	case "month":
		return session.BucketStartMs(nowMs, session.TFMonth), nowMs + 1, nil
	case "all", "":
		return 0, nowMs + 1, nil
	case "custom":
		return resolveCustomRange(from, to)
	default:
		return 0, 0, fmt.Errorf("exec: unknown export preset %q", preset)
	}
}

func resolveCustomRange(from, to string) (fromMs, toMs int64, err error) {
	loc := session.Loc()
	fromDay, err := time.ParseInLocation(exportDateLayout, from, loc)
	if err != nil {
		return 0, 0, fmt.Errorf("exec: invalid export from date %q: %w", from, err)
	}
	toDay, err := time.ParseInLocation(exportDateLayout, to, loc)
	if err != nil {
		return 0, 0, fmt.Errorf("exec: invalid export to date %q: %w", to, err)
	}
	if fromDay.After(toDay) {
		return 0, 0, fmt.Errorf("exec: export from date %q is after to date %q", from, to)
	}
	// toDay's whole ET calendar day is inclusive: the exclusive upper bound is
	// the NEXT day's ET midnight (AddDate is DST-safe — time.Time normalizes
	// the wall clock across the transition, see TestResolveExportRangeCustomAcrossDST).
	return fromDay.UnixMilli(), toDay.AddDate(0, 0, 1).UnixMilli(), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/exec/... -run TestResolveExportRange -v`
Expected: PASS (5 tests)

- [ ] **Step 5: Build check**

Run: `cd engine && go build ./...`
Expected: succeeds

- [ ] **Step 6: Commit**

```bash
git add engine/internal/exec/export.go engine/internal/exec/export_test.go
git commit -m "feat(exec): add ResolveExportRange for trade-export date filtering"
```

---

### Task 3: Engine — `exec.BuildFillsCSV` (pure CSV builder)

**Files:**
- Modify: `engine/internal/exec/export.go` (append `BuildFillsCSV` + `exportAction` + `exportHeader`)
- Modify: `engine/internal/exec/export_test.go` (append tests; remove the Task 2 placeholder import line)

**Interfaces:**
- Consumes: `ExportFillRow` (Task 1).
- Produces: `BuildFillsCSV(rows []ExportFillRow) (string, error)` — Task 5's query handler calls this exact signature.

- [ ] **Step 1: Write the failing tests**

In `engine/internal/exec/export_test.go`, add `"strings"` to the import block (it currently has only `"testing"` and `"time"`):

```go
import (
	"strings"
	"testing"
	"time"
)
```

Then append these tests to the end of the file:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/exec/... -run TestBuildFillsCSV -v`
Expected: FAIL — `undefined: BuildFillsCSV`

- [ ] **Step 3: Write the minimal implementation**

Append to `engine/internal/exec/export.go` (add `"encoding/csv"`, `"strconv"`, `"strings"` to the import block):

```go
import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)
```

```go

// exportHeader is the eJournal Generic-importer column order (indices 0-5)
// plus externalId appended at index 6 — see the CSV contract in
// docs/superpowers/specs/2026-07-10-export-trades-ejournal-design.md.
var exportHeader = []string{"datetime", "symbol", "action", "price", "shares", "fees", "externalId"}

// BuildFillsCSV renders export rows as the eJournal CSV contract. Header row
// always emitted; empty input yields a header-only document. Times are
// America/New_York wall-clock, ISO local (no zone), seconds precision.
func BuildFillsCSV(rows []ExportFillRow) (string, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.Write(exportHeader); err != nil {
		return "", err
	}
	loc := session.Loc()
	for _, r := range rows {
		rec := []string{
			time.UnixMilli(r.TsMs).In(loc).Format("2006-01-02T15:04:05"),
			strings.TrimPrefix(r.Symbol, "US."),
			exportAction(r.Side),
			strconv.FormatFloat(r.Price, 'f', -1, 64),
			strconv.FormatFloat(r.Qty, 'f', -1, 64),
			"0",
			"etape:" + r.Venue + ":" + strconv.FormatInt(r.FillID, 10),
		}
		if err := w.Write(rec); err != nil {
			return "", err
		}
	}
	w.Flush()
	return b.String(), w.Error()
}

// exportAction folds the four exec sides into eJournal's BUY/SELL: BUY and
// COVER (opening/adding to a long, or covering a short) => BUY; SELL and
// SHORT => SELL.
func exportAction(side string) string {
	switch side {
	case "BUY", "COVER":
		return "BUY"
	default:
		return "SELL"
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd engine && go test ./internal/exec/... -run "TestBuildFillsCSV|TestResolveExportRange" -v`
Expected: PASS (all 9 tests from Tasks 2+3)

- [ ] **Step 5: Full package + build check**

Run: `cd engine && go build ./... && go test ./internal/exec/...`
Expected: `ok`

- [ ] **Step 6: Commit**

```bash
git add engine/internal/exec/export.go engine/internal/exec/export_test.go
git commit -m "feat(exec): add BuildFillsCSV rendering the eJournal export contract"
```

---

### Task 4: Wire types — `ExportFillsArgs`/`ExportFillsResult` + tygo regen

**Files:**
- Modify: `engine/internal/uihub/wsmsg/payloads.go` (add two structs beside `QueryFillsArgs`)
- Generated (do not hand-edit): `ui/src/gen/wsmsg.ts`

**Interfaces:**
- Produces: `wsmsg.ExportFillsArgs{ Venue, Preset, From, To string }` and `wsmsg.ExportFillsResult{ CSV string; Count int }`, plus their generated TS equivalents `ExportFillsArgs`/`ExportFillsResult` in `ui/src/wire/contract` — Task 5 (Go) and Task 6 (TS) both depend on these exact field names.

- [ ] **Step 1: Add the structs**

In `engine/internal/uihub/wsmsg/payloads.go`, insert immediately after `QueryFillsArgs` (currently lines 276–280):

```go

// ExportFillsArgs selects one venue's fills for the trade-export CSV.
// Preset is one of "today"|"week"|"month"|"all"|"custom"; From/To are
// "YYYY-MM-DD" ET calendar dates, used only when Preset is "custom".
type ExportFillsArgs struct {
	Venue  string `json:"venue"`
	Preset string `json:"preset"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
}

// ExportFillsResult carries the generated CSV (engine is the content source
// of truth) plus a row count for a UI empty-state/toast check.
type ExportFillsResult struct {
	CSV   string `json:"csv"`
	Count int    `json:"count"`
}
```

- [ ] **Step 2: Regenerate the TS contract**

Run: `cd engine && make gen-ts`
Expected: exits 0; `git status` shows `ui/src/gen/wsmsg.ts` modified with new `ExportFillsArgs`/`ExportFillsResult` TS interfaces.

- [ ] **Step 3: Verify the drift gate passes**

Run: `cd engine && make gen-ts-check`
Expected: exits 0 (the file just regenerated matches what's about to be committed).

- [ ] **Step 4: Build check**

Run: `cd engine && go build ./...`
Expected: succeeds

- [ ] **Step 5: Commit**

```bash
git add engine/internal/uihub/wsmsg/payloads.go ui/src/gen/wsmsg.ts
git commit -m "feat(wsmsg): add ExportFills wire args/result and regenerate TS contract"
```

---

### Task 5: uihub — `ExportFills` query handler + clock injection

**Files:**
- Modify: `engine/internal/uihub/query.go` (inject clock, widen `fillsQuerier`, add `case "ExportFills"`)
- Modify: `engine/internal/uihub/query_test.go` (extend `spyFills`, update existing `newQueries` call sites, add new tests)
- Modify: `engine/internal/uihub/api.go` (widen `Stores` interface, pass `clk` into `newQueries`)
- Modify: `engine/internal/uihub/api_test.go` (add `ExportFills` to the `apiStores` test fake — required or the package fails to compile once `Stores` widens)

**Interfaces:**
- Consumes: `exec.ExportFillRow`/`Store.ExportFills` (Task 1), `exec.ResolveExportRange`/`exec.BuildFillsCSV` (Tasks 2/3), `wsmsg.ExportFillsArgs`/`wsmsg.ExportFillsResult` (Task 4).
- Produces: the `"ExportFills"` WS query name, callable via `commands.sendQuery("ExportFills", {venue, preset, from, to})` — Task 6/7 (UI) depend on this exact name and arg shape.

- [ ] **Step 1: Write the failing tests**

In `engine/internal/uihub/query_test.go`, update the imports and `spyFills` fake, and update every existing `newQueries(f)` call to `newQueries(f, clock.NewFake(time.Now()))` (the constructor signature is changing in Step 3 below):

```go
package uihub

import (
	"context"
	"encoding/json"
	"errors"
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
```

Update the 4 existing tests' `newQueries(...)` calls to pass a fake clock (add `, clock.NewFake(time.Now())` as the second argument to each of `TestQueryFillsReturnsFills`, `TestQueryFillsEmptyOnError`, `TestQueryFillsMalformedArgsReturnsEmptySlice`, `TestQueryUnknownReturnsEmptySlice`), e.g.:

```go
func TestQueryFillsReturnsFills(t *testing.T) {
	f := &spyFills{rows: []exec.FillRow{{OrderID: "ET1", Symbol: "US.AAPL", Side: "BUY", Qty: 100, Price: 3.47, TsMs: 5, Venue: "sim"}}}
	q := newQueries(f, clock.NewFake(time.Now()))
	// ...unchanged body...
```

(Apply the same `, clock.NewFake(time.Now())` addition to the other three `newQueries(&spyFills{...})` / `newQueries(f)` call sites in the file.)

Then append the new tests:

```go
func TestExportFillsReturnsCSV(t *testing.T) {
	f := &spyFills{exportRowsMulti: []exec.ExportFillRow{
		{FillID: 12, Symbol: "US.NVDA", Side: "BUY", Qty: 100, Price: 120.5, TsMs: 1789000000000, Venue: "sim"},
	}}
	clk := clock.NewFake(time.UnixMilli(1789000000000))
	q := newQueries(f, clk)
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
	q := newQueries(&spyFills{}, clock.NewFake(time.Now()))
	out := q.handle("ExportFills", json.RawMessage(`invalid-json`))
	b, _ := json.Marshal(out)
	if string(b) != `{"csv":"","count":0}` {
		t.Fatalf("malformed args must yield empty ExportFillsResult (never nil/hang); marshaled to %s", b)
	}
}

func TestExportFillsEmptyOnStoreError(t *testing.T) {
	q := newQueries(&spyFills{exportErr: errors.New("boom")}, clock.NewFake(time.Now()))
	out := q.handle("ExportFills", json.RawMessage(`{"venue":"sim","preset":"all"}`))
	b, _ := json.Marshal(out)
	if string(b) != `{"csv":"","count":0}` {
		t.Fatalf("store error must yield empty ExportFillsResult; marshaled to %s", b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd engine && go test ./internal/uihub/... -run "TestExportFills|TestQueryFills|TestQueryUnknown" -v`
Expected: FAIL to compile — `newQueries` takes 1 argument, and `ExportFills` is not a method on `fillsQuerier`.

- [ ] **Step 3: Update `engine/internal/uihub/query.go`**

Replace the file's contents with:

```go
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
```

- [ ] **Step 4: Widen `Stores` and wire the clock in `engine/internal/uihub/api.go`**

In `api.go`, change the `Stores` interface (currently lines 19–24):

```go
// Stores is the store surface uihub needs (satisfied by *store.Store).
type Stores interface {
	GetConfig(key string) (string, bool, error)
	SetConfig(key, value string)
	QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error)
	ExportFills(ctx context.Context, venue string, fromMs, toMs int64) ([]exec.ExportFillRow, error)
}
```

And change the `newQueries(st)` call (currently line 95):

```go
	qry := newQueries(st, clk)
```

(`clk` is already the function's first parameter, already in scope at that line.)

- [ ] **Step 5: Fix the `apiStores` test fake in `engine/internal/uihub/api_test.go`**

Add a matching method so `apiStores` still satisfies the widened `Stores` interface (append after the existing `QueryFills` method, currently line 22):

```go
func (apiStores) ExportFills(context.Context, string, int64, int64) ([]exec.ExportFillRow, error) {
	return nil, nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd engine && go test ./internal/uihub/... -v`
Expected: PASS — all `TestQueryFills*`, `TestQueryUnknown*`, and the 3 new `TestExportFills*` tests, plus `TestUIHubNewBuildsRunnableHubAndServer` (which now compiles against the widened interface).

- [ ] **Step 7: Full build + broader regression check**

Run: `cd engine && go build ./... && go test ./internal/uihub/... ./internal/uihubtest/... ./internal/store/... ./internal/exec/...`
Expected: `ok` for all four packages (uihubtest exercises the real `*store.Store` against `uihub.New`, catching any interface-satisfaction issue Step 5 didn't).

- [ ] **Step 8: Commit**

```bash
git add engine/internal/uihub/query.go engine/internal/uihub/query_test.go engine/internal/uihub/api.go engine/internal/uihub/api_test.go
git commit -m "feat(uihub): wire ExportFills query with clock-resolved date presets"
```

---

### Task 6: UI — `ExportTradesPopover` component

**Files:**
- Create: `ui/src/chrome/panels/ExportTradesPopover.tsx`
- Create: `ui/src/chrome/panels/ExportTradesPopover.test.tsx`

**Interfaces:**
- Consumes: `ExportFillsResult` (generated, Task 4), `Palette` (`ui/src/render/palette`), `ToastApi` (`ui/src/chrome/Toast`), `HoverButton` (`ui/src/chrome/controls/HoverButton`).
- Produces: `ExportTradesPopover({ palette, anchor, venue, commands, toast, onClose })` — Task 7 (`AccountPanel.tsx`) renders this exact component with these exact prop names.

- [ ] **Step 1: Write the failing tests**

Create `ui/src/chrome/panels/ExportTradesPopover.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { LIGHT } from "../../render/palette";
import { ToastProvider, useToasts } from "../Toast";
import { ExportTradesPopover } from "./ExportTradesPopover";

function Harness({ commands, onClose = () => {} }: { commands: { sendQuery: (name: string, args: unknown) => Promise<unknown> }; onClose?: () => void }) {
  const toast = useToasts();
  const anchor = document.createElement("button");
  document.body.appendChild(anchor);
  return <ExportTradesPopover palette={LIGHT} anchor={anchor} venue="sim" commands={commands} toast={toast} onClose={onClose} />;
}

function wrap(commands: { sendQuery: (name: string, args: unknown) => Promise<unknown> }, onClose?: () => void) {
  return render(<ToastProvider><Harness commands={commands} onClose={onClose} /></ToastProvider>);
}

describe("ExportTradesPopover", () => {
  beforeEach(() => {
    (URL as unknown as { createObjectURL: (b: Blob) => string }).createObjectURL = vi.fn(() => "blob:mock");
    (URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = vi.fn();
  });

  it("defaults to All time and downloads a CSV via an anchor click", async () => {
    const csv = "datetime,symbol,action,price,shares,fees,externalId\n2026-07-10T09:31:05,NVDA,BUY,120.5,100,0,etape:sim:12\n";
    const calls: Array<{ name: string; args: unknown }> = [];
    const sendQuery = vi.fn(async (name: string, args: unknown) => { calls.push({ name, args }); return { csv, count: 1 }; });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const onClose = vi.fn();
    wrap({ sendQuery }, onClose);

    fireEvent.click(screen.getByTestId("export-download"));

    await waitFor(() => expect(clickSpy).toHaveBeenCalledTimes(1));
    expect(calls).toEqual([{ name: "ExportFills", args: { venue: "sim", preset: "all", from: "", to: "" } }]);
    const anchor = clickSpy.mock.instances[0] as HTMLAnchorElement;
    expect(anchor.download).toBe("etape-sim-all.csv");
    expect(onClose).toHaveBeenCalled();
    clickSpy.mockRestore();
  });

  it("shows an info toast and does not download when there are no fills", async () => {
    const sendQuery = vi.fn(async () => ({ csv: "datetime,symbol,action,price,shares,fees,externalId\n", count: 0 }));
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const onClose = vi.fn();
    wrap({ sendQuery }, onClose);

    fireEvent.click(screen.getByTestId("export-download"));

    await waitFor(() => expect(screen.getByText(/No fills to export/)).toBeTruthy());
    expect(clickSpy).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    clickSpy.mockRestore();
  });

  it("Custom preset reveals date inputs, disables Download until both are set, and forwards from/to", async () => {
    const calls: Array<{ name: string; args: unknown }> = [];
    const sendQuery = vi.fn(async (name: string, args: unknown) => { calls.push({ name, args }); return { csv: "h\n", count: 1 }; });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    wrap({ sendQuery });

    fireEvent.change(screen.getByTestId("export-preset"), { target: { value: "custom" } });
    expect((screen.getByTestId("export-download") as HTMLButtonElement).disabled).toBe(true);

    fireEvent.change(screen.getByTestId("export-from"), { target: { value: "2026-07-01" } });
    expect((screen.getByTestId("export-download") as HTMLButtonElement).disabled).toBe(true);

    fireEvent.change(screen.getByTestId("export-to"), { target: { value: "2026-07-03" } });
    expect((screen.getByTestId("export-download") as HTMLButtonElement).disabled).toBe(false);

    fireEvent.click(screen.getByTestId("export-download"));
    await waitFor(() => expect(clickSpy).toHaveBeenCalledTimes(1));
    expect(calls).toEqual([{ name: "ExportFills", args: { venue: "sim", preset: "custom", from: "2026-07-01", to: "2026-07-03" } }]);
    clickSpy.mockRestore();
  });

  it("closes on Escape without downloading", () => {
    const sendQuery = vi.fn(async () => ({ csv: "h\n", count: 1 }));
    const onClose = vi.fn();
    wrap({ sendQuery }, onClose);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/ExportTradesPopover.test.tsx`
Expected: FAIL — `Failed to resolve import "./ExportTradesPopover"`

- [ ] **Step 3: Write the minimal implementation**

Create `ui/src/chrome/panels/ExportTradesPopover.tsx`:

```tsx
// ui/src/chrome/panels/ExportTradesPopover.tsx
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { Palette } from "../../render/palette";
import type { ExportFillsResult } from "../../wire/contract";
import type { ToastApi } from "../Toast";
import { HoverButton } from "../controls/HoverButton";

type Preset = "today" | "week" | "month" | "all" | "custom";

const PRESETS: Array<{ value: Preset; label: string }> = [
  { value: "today", label: "Today" },
  { value: "week", label: "This week" },
  { value: "month", label: "This month" },
  { value: "all", label: "All time" },
  { value: "custom", label: "Custom" },
];

export interface ExportTradesPopoverProps {
  palette: Palette;
  anchor: HTMLElement | null;
  venue: string;
  commands: { sendQuery(name: string, args: unknown): Promise<unknown> };
  toast: ToastApi;
  onClose: () => void;
}

const WIDTH = 220;

// Anchored dropdown for the Account panel's Export action. Same portal +
// fixed-position pattern as IndicatorPickerPopover (see that file's
// comment): the trigger sits inside PanelFrame's overflow:hidden header
// slot, so an absolutely-positioned child would be clipped.
export function ExportTradesPopover(
  { palette, anchor, venue, commands, toast, onClose }: ExportTradesPopoverProps,
): JSX.Element | null {
  const ref = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const [preset, setPreset] = useState<Preset>("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");

  useLayoutEffect(() => {
    if (!anchor) { setPos(null); return; }
    const place = () => {
      const rect = anchor.getBoundingClientRect();
      const left = Math.min(Math.max(rect.left, 8), window.innerWidth - WIDTH - 8);
      setPos({ top: rect.bottom + 4, left });
    };
    place();
    window.addEventListener("resize", place);
    return () => window.removeEventListener("resize", place);
  }, [anchor]);

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node;
      if (ref.current && !ref.current.contains(t) && !(anchor && anchor.contains(t))) onClose();
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => { document.removeEventListener("mousedown", onDown); window.removeEventListener("keydown", onKey); };
  }, [anchor, onClose]);

  if (!pos && anchor) return null; // first-tick guard: position not measured yet

  const download = () => {
    void commands.sendQuery("ExportFills", {
      venue, preset, from: preset === "custom" ? from : "", to: preset === "custom" ? to : "",
    }).then((payload) => {
      const { csv, count } = payload as ExportFillsResult;
      if (!count) { toast.push({ level: "info", text: `No fills to export for ${venue}` }); return; }
      const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      const label = preset === "custom" ? `${from}_${to}` : preset;
      a.download = `etape-${venue}-${label}.csv`;
      a.click();
      URL.revokeObjectURL(url);
      onClose();
    });
  };

  const labelStyle = { fontSize: 11, color: palette.textMuted };
  const inputStyle = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, borderRadius: 4, padding: "3px 6px", fontSize: 12, width: "100%" };

  return createPortal(
    <div ref={ref} className="popover" role="menu" style={{
      position: "fixed", top: pos?.top ?? 0, left: pos?.left ?? 0, width: WIDTH, zIndex: 10001,
      background: palette.bg, color: palette.text, fontFamily: '"IBM Plex Sans", system-ui, sans-serif',
      fontVariantNumeric: "tabular-nums",
    }}>
      <div style={{ display: "flex", flexDirection: "column", gap: 6, padding: "6px 10px" }}>
        <span style={labelStyle}>Export — {venue}</span>
        <select data-testid="export-preset" value={preset} onChange={(e) => setPreset(e.target.value as Preset)} style={inputStyle}>
          {PRESETS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
        </select>
        {preset === "custom" && (
          <>
            <label style={labelStyle}>From
              <input data-testid="export-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} style={{ ...inputStyle, marginTop: 2 }} />
            </label>
            <label style={labelStyle}>To
              <input data-testid="export-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} style={{ ...inputStyle, marginTop: 2 }} />
            </label>
          </>
        )}
        <HoverButton data-testid="export-download" onClick={download}
          disabled={preset === "custom" && (!from || !to)}
          style={{ marginTop: 4, padding: "5px 8px", border: `1px solid ${palette.border}`, borderRadius: 4, background: "transparent", color: palette.text, cursor: "pointer", fontSize: 12 }}
          hoverStyle={{ background: palette.surface }}>
          Download CSV
        </HoverButton>
      </div>
    </div>,
    document.body,
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/panels/ExportTradesPopover.test.tsx`
Expected: PASS (4 tests)

- [ ] **Step 5: Typecheck**

Run: `cd ui && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add ui/src/chrome/panels/ExportTradesPopover.tsx ui/src/chrome/panels/ExportTradesPopover.test.tsx
git commit -m "feat(ui): add ExportTradesPopover with preset and custom date-range export"
```

---

### Task 7: UI — Account panel wiring (trigger button + portal)

**Files:**
- Modify: `ui/src/chrome/panels/AccountPanel.tsx`
- Modify: `ui/src/chrome/panels/AccountPanel.test.tsx`

**Interfaces:**
- Consumes: `ExportTradesPopover` (Task 6).
- Produces: nothing new for later tasks — this is the final integration point.

- [ ] **Step 1: Write the failing test**

Append to `ui/src/chrome/panels/AccountPanel.test.tsx` (add `waitFor` to the existing `@testing-library/react` import on line 3, so it reads `import { render, screen, act, fireEvent, waitFor } from "@testing-library/react";`), then add:

```tsx
describe("Export trades (Task 7 wiring)", () => {
  beforeEach(() => {
    (URL as unknown as { createObjectURL: (b: Blob) => string }).createObjectURL = vi.fn(() => "blob:mock");
    (URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = vi.fn();
  });

  it("opens the Export popover from the header and downloads for the panel's selected venue", async () => {
    const { props, stores, linkGroups } = mkProps("green");
    const calls: Array<{ name: string; args: unknown }> = [];
    props.commands.sendQuery = vi.fn(async (name: string, args: unknown) => {
      calls.push({ name, args });
      return { csv: "datetime,symbol,action,price,shares,fees,externalId\n2026-07-10T09:31:05,NVDA,BUY,120.5,100,0,etape:alpaca-paper:1\n", count: 1 };
    });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(false, "alpaca-paper") });
      linkGroups.focusVenue("green", "alpaca-paper");
    });
    wrap(props);

    expect(screen.queryByTestId("export-download")).toBeNull(); // popover closed by default
    fireEvent.click(screen.getByTestId("acct-export"));
    expect(screen.getByTestId("export-download")).toBeTruthy(); // popover opened

    fireEvent.click(screen.getByTestId("export-download"));
    await waitFor(() => expect(clickSpy).toHaveBeenCalledTimes(1));
    expect(calls).toEqual([{ name: "ExportFills", args: { venue: "alpaca-paper", preset: "all", from: "", to: "" } }]);
    clickSpy.mockRestore();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/AccountPanel.test.tsx -t "Export trades"`
Expected: FAIL — `Unable to find an element by: [data-testid="acct-export"]`

- [ ] **Step 3: Wire the trigger + popover in `AccountPanel.tsx`**

Add to the top-of-file imports (after the existing `import { PanelHeaderActionsSlotContext } from "./headerSlot";` line):

```tsx
import { ExportTradesPopover } from "./ExportTradesPopover";
```

Change the react import (currently `import { useContext, useMemo, useState, useSyncExternalStore } from "react";`) to add `useRef`:

```tsx
import { useContext, useMemo, useRef, useState, useSyncExternalStore } from "react";
```

Replace the `venueSelect`/`actionsSlot` block (the block starting at `const actionsSlot = useContext(PanelHeaderActionsSlotContext);` through the `venueSelect` JSX close):

```tsx
  const actionsSlot = useContext(PanelHeaderActionsSlotContext);
  const [exportOpen, setExportOpen] = useState(false);
  const exportBtnRef = useRef<HTMLButtonElement | null>(null);
  const venueSelect = (
    <select data-testid="acct-venue" className="ctl mono" value={venue} onChange={(e) => selectVenue(e.target.value)}>
      {venues.map((v) => <option key={v} value={v}>{v}</option>)}
    </select>
  );
  const headerActions = (
    <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
      {venueSelect}
      <HoverButton ref={exportBtnRef} data-testid="acct-export" className="ctl mono" aria-haspopup="menu" aria-expanded={exportOpen}
        onClick={() => setExportOpen((v) => !v)} style={{ background: "transparent" }}>
        Export
      </HoverButton>
      {exportOpen && (
        <ExportTradesPopover palette={palette} anchor={exportBtnRef.current} venue={venue} commands={commands} toast={toast}
          onClose={() => setExportOpen(false)} />
      )}
    </div>
  );
```

Change the render line (currently `{actionsSlot === undefined ? venueSelect : actionsSlot ? createPortal(venueSelect, actionsSlot) : null}`):

```tsx
      {actionsSlot === undefined ? headerActions : actionsSlot ? createPortal(headerActions, actionsSlot) : null}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/panels/AccountPanel.test.tsx`
Expected: PASS — all existing tests plus the new "Export trades" test (27 → 28 tests)

- [ ] **Step 5: Typecheck**

Run: `cd ui && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add ui/src/chrome/panels/AccountPanel.tsx ui/src/chrome/panels/AccountPanel.test.tsx
git commit -m "feat(ui): wire Export trigger into the Account panel header"
```

---

### Task 8: Whole-stack verification (no code changes)

**Files:** none modified — this task only runs commands.

- [ ] **Step 1: Full Go build + test suite**

Run: `cd engine && go build ./... && go test -race ./...`
Expected: build succeeds; all packages `ok` (this also re-runs the full baseline that passed before Task 1, confirming no regressions across the whole engine).

- [ ] **Step 2: gen-ts drift gate (belt-and-suspenders after all Go edits)**

Run: `cd engine && make gen-ts-check`
Expected: exits 0 — no drift between `wsmsg/payloads.go` and `ui/src/gen/wsmsg.ts`.

- [ ] **Step 3: Full UI test suite + typecheck**

Run: `cd ui && npx vitest run && npx tsc --noEmit`
Expected: all test files pass; no type errors.

- [ ] **Step 4: Manual end-to-end walkthrough (no live OpenD)**

1. Boot the replay harness: `cd ui && ./e2e/serve.sh` (genjournal synthetic day → config venue `sim-paper` → `etape -replay 2026-01-02 -speed 0 -replay-hold -dist ui/dist` on `127.0.0.1:8686`).
2. Open `http://127.0.0.1:8686` in a browser. Apply the Trading preset, arm master, select **sim-paper** in the Account panel's venue dropdown.
3. Submit a marketable-LIMIT **buy then sell** on `US.NVDA` (mirror `ui/e2e/trade-history.spec.ts`'s order-submission helper) to create at least 2 fills.
4. Click **Export** in the Account panel header → the popover opens next to the button.
5. Leave the preset on **All time**, click **Download CSV** → confirm a file named `etape-sim-paper-all.csv` downloads. Open it and check: header is exactly `datetime,symbol,action,price,shares,fees,externalId`; `symbol` is `NVDA` (no `US.` prefix); `action` matches the sides submitted; `datetime` values are in ET; `fees` is `0`; `externalId` is `etape:sim-paper:<fillId>` with distinct fill ids per row.
6. Switch the preset to **Custom**, pick a From/To range that excludes both fills (e.g. a date before today) → Download → confirm a header-only CSV.
7. Pick a Custom range that includes today → Download → confirm both rows reappear.
8. **eJournal smoke test**: open eJournal (`~/Projects/eJournal`), use its existing **Generic CSV** importer, and import the `etape-sim-paper-all.csv` file from step 5. Confirm the trades appear (columns 0–5 line up positionally; eJournal ignores the trailing `externalId` column since it has no dedicated eTape parser yet — that's the follow-up session's job). Re-import the same file and confirm eJournal creates duplicate rows (expected today, since the Generic importer never dedups — this is exactly the gap the future eTape-specific parser closes).

- [ ] **Step 5: Report results**

No commit for this task (nothing changed). Summarize: build/test status from Steps 1–3, and the manual walkthrough outcome from Step 4 (what was verified, and confirm the eJournal Generic-import smoke test in Step 4.8 succeeded).
