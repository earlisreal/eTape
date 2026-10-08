package tickstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/tickstore"
)

func TestFullQueueKeepsLossAccountingAndDrainsAcceptedWork(t *testing.T) {
	dir := t.TempDir()
	s, err := tickstore.Open(tickstore.Options{Directory: dir, MinFreeBytes: 1, QueueItems: 2, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		accepted := s.Record(feed.Recording{Kind: "print", Symbol: "US.AIXI", Sequence: int64(i + 1)})
		if accepted != (i < 2) {
			t.Fatalf("accepted item %d = %v", i, accepted)
		}
	}
	if stats := s.Stats(); stats.WorkingItems != 2 || stats.Lost[feed.RecordingSource] != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "tick-*.sqlite"))
	db, err := sql.Open("sqlite", files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var prints int
	if err := db.QueryRow("SELECT COUNT(*) FROM observations WHERE kind='print'").Scan(&prints); err != nil {
		t.Fatal(err)
	}
	if prints != 2 {
		t.Fatalf("accepted prints = %d", prints)
	}
	var raw string
	if err := db.QueryRow("SELECT data FROM observations WHERE kind='capture_gap'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var gap struct{ Count uint64 }
	if err := json.Unmarshal([]byte(raw), &gap); err != nil {
		t.Fatal(err)
	}
	if gap.Count != 2 {
		t.Fatalf("gap = %s", raw)
	}
}

func TestByteReservationBoundsQueuedAndInFlightWork(t *testing.T) {
	s, err := tickstore.Open(tickstore.Options{Directory: t.TempDir(), MinFreeBytes: 1, QueueBytes: 32 << 10, FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	first := feed.Recording{Kind: "source", Source: feed.SourceRef{Ingress: 1}, Body: make([]byte, 100)}
	if !s.Record(first) {
		t.Fatal("first source rejected")
	}
	first.Source.Ingress = 2
	if s.Record(first) {
		t.Fatal("byte budget accepted a second source")
	}
	if !s.Record(feed.Recording{Kind: "processing"}) {
		t.Fatal("remaining byte reservation unusable")
	}
	stats := s.Stats()
	if stats.WorkingBytes > 32<<10 || stats.WorkingItems != 2 || stats.Lost[feed.RecordingSource] != 1 {
		t.Fatalf("bounded work=%+v", stats)
	}
	closeArchive(t, s)
}

func TestHealthyBurstCommitsWithinOneSecond(t *testing.T) {
	if testing.Short() {
		t.Skip("wall-clock capacity measurement; run without -short")
	}
	s, err := tickstore.Open(tickstore.Options{Directory: t.TempDir(), MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	for i := range 800 {
		if !s.Record(feed.Recording{Kind: "print", Symbol: "US.AIXI", Sequence: int64(i + 1), Price: 2.37, Volume: 10}) {
			t.Fatalf("healthy burst rejected %d", i)
		}
	}
	deadline := time.Now().Add(time.Second)
	for s.Stats().Committed < 800 {
		if time.Now().After(deadline) {
			t.Fatalf("healthy burst missed one-second commit target: %+v", s.Stats())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if stats := s.Stats(); stats.MaxCommitLagMs > 1000 || stats.Lost[feed.RecordingSource] != 0 {
		t.Fatalf("healthy commit stats = %+v", stats)
	}
}

func TestCommittedArchivePreservesDuplicateDeliveriesAndExactSequence(t *testing.T) {
	dir := t.TempDir()
	s, err := tickstore.Open(tickstore.Options{Directory: dir, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i, price := range []float64{1.2345, 1.2346} {
		if !s.Record(feed.Recording{Kind: "print", Source: feed.SourceRef{Run: s.RunID(), Connection: 1, Ingress: uint64(i + 1), ReceiptMs: time.Now().UnixMilli()}, Symbol: "US.AIXI", TimeMs: 123, Sequence: 9007199254740993, Price: price, Volume: 17}) {
			t.Fatal("record rejected")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "tick-*.sqlite"))
	if err != nil || len(files) != 1 {
		t.Fatalf("segments = %v, %v", files, err)
	}
	db, err := sql.Open("sqlite", files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT sequence, price FROM observations WHERE kind='print' ORDER BY ingress")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for _, want := range []float64{1.2345, 1.2346} {
		if !rows.Next() {
			t.Fatal("missing duplicate observation")
		}
		var sequence int64
		var price float64
		if err := rows.Scan(&sequence, &price); err != nil {
			t.Fatal(err)
		}
		if sequence != 9007199254740993 || price != want {
			t.Fatalf("sequence/price = %d/%v", sequence, price)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected report")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
