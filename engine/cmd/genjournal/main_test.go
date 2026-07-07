package main

import (
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/store"
)

func TestGenerateWritesReplayableDay(t *testing.T) {
	db := filepath.Join(t.TempDir(), "gen.db")
	const day = "2026-01-02"

	if err := generate(db, day); err != nil {
		t.Fatalf("generate: %v", err)
	}

	st, err := store.Open(store.Options{Path: db, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rows, err := st.ReadJournalDay(day)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows written")
	}

	kinds := map[string]map[string]int{"US.AAPL": {}, "US.NVDA": {}}
	for _, r := range rows {
		if m, ok := kinds[r.Symbol]; ok {
			m[r.Kind]++
		}
	}
	for sym, m := range kinds {
		for _, k := range []string{"ticks", "bars1m", "book", "quote"} {
			if m[k] == 0 {
				t.Errorf("%s: missing %q events (%v)", sym, k, m)
			}
		}
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Seq <= rows[i-1].Seq {
			t.Fatalf("seq not increasing at %d", i)
		}
	}
	var sawTick bool
	for _, r := range rows {
		if te, ok := r.Event.(feed.TicksEvent); ok && len(te.Ticks) > 0 && te.Ticks[0].Price > 0 {
			sawTick = true
			break
		}
	}
	if !sawTick {
		t.Fatal("no decodable tick with a positive price")
	}
}
