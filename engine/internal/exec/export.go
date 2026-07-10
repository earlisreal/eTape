package exec

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
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
