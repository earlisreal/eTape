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
