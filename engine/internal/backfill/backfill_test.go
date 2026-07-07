package backfill

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

func TestIntradayFromSkipsWeekends(t *testing.T) {
	// Wednesday 2026-07-08 12:00 ET.
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, session.Loc())
	// 1 trading day back = Tuesday 2026-07-07 00:00 ET.
	got := intradayFrom(now, 1)
	want := time.Date(2026, 7, 7, 0, 0, 0, 0, session.Loc())
	if !got.Equal(want) {
		t.Fatalf("intradayFrom(1) = %s, want %s", got, want)
	}
	// 3 trading days back from Wed spans the weekend: Tue, Mon, Fri 2026-07-03.
	got = intradayFrom(now, 3)
	want = time.Date(2026, 7, 3, 0, 0, 0, 0, session.Loc())
	if !got.Equal(want) {
		t.Fatalf("intradayFrom(3) = %s, want %s", got, want)
	}
}
