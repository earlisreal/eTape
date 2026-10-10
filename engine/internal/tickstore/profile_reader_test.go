package tickstore_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/tickstore"
)

func TestProfileReaderUsesCommittedNormalizedReportsAndSealedHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := tickstore.Open(tickstore.Options{Directory: dir, MinFreeBytes: 1, FlushInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeArchive(t, s) })
	const ts int64 = 1791540000000
	want := feed.Tick{Symbol: "US.TEST", Seq: 9007199254740993, TsMs: ts, Price: 1.2345, Volume: 17, Condition: feed.TradeConditionOddLot}
	if !s.Record(feed.Recording{Kind: "print", Symbol: want.Symbol, TimeMs: ts, Sequence: want.Seq, Condition: 999, Data: struct{ Normalized feed.Tick }{want}}) {
		t.Fatal("record rejected")
	}
	deadline := time.Now().Add(time.Second)
	for s.Stats().Committed < 1 {
		if time.Now().After(deadline) {
			t.Fatal("commit timeout")
		}
		time.Sleep(time.Millisecond)
	}
	reader := tickstore.NewProfileReader(dir, s)
	got, err := readProfileEventually(reader, want.Symbol, ts, ts+1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Prints) != 1 || got.Prints[0].Seq != want.Seq || got.Prints[0].Price != 1.2345 || got.Prints[0].Condition != feed.TradeConditionOddLot {
		t.Fatalf("normalized reports: %+v", got)
	}
	closeArchive(t, s)
	got, err = readProfileEventually(tickstore.NewProfileReader(dir, nil), want.Symbol, ts, ts+1000)
	if err != nil || len(got.Prints) != 1 {
		t.Fatalf("sealed history: %+v, %v", got, err)
	}
}

func TestProfileReaderConcurrentRotationAndPruningDoNotLoseRecording(t *testing.T) {
	dir := t.TempDir()
	const ts int64 = 1791540000000
	var now atomic.Int64
	now.Store(ts)
	s, err := tickstore.Open(tickstore.Options{Directory: dir, RetentionDays: 2, MinFreeBytes: 1, FlushInterval: time.Millisecond, Now: func() time.Time { return time.UnixMilli(now.Load()) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeArchive(t, s) })
	reader := tickstore.NewProfileReader(dir, s)
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				_, err := reader.Read(ctx, "US.TEST", ts, ts+100)
				if err != nil && !errors.Is(err, tickstore.ErrProfileBusy) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("concurrent read: %v", err)
					return
				}
			}
		}()
	}
	t.Cleanup(func() { cancel(); workers.Wait() })
	for i := range 8 {
		now.Add(24 * 60 * 60 * 1000)
		tick := feed.Tick{Symbol: "US.TEST", Seq: int64(i + 1), TsMs: ts + int64(i), Price: 10, Volume: 10, Condition: feed.TradeConditionAutomaticMatch}
		if !s.Record(feed.Recording{Kind: "print", Symbol: tick.Symbol, TimeMs: tick.TsMs, Data: struct{ Normalized feed.Tick }{tick}}) {
			t.Fatal("record rejected during reads")
		}
		waitArchive(t, func() bool { return s.Stats().Committed >= uint64(i+1) })
	}
	cancel()
	workers.Wait()
	if stats := s.Stats(); stats.Paused || stats.Lost != [feed.RecordingLaneCount]uint64{} {
		t.Fatalf("maintenance recording: %+v", stats)
	}
	got, err := readProfileEventually(reader, "US.TEST", ts, ts+100)
	if err != nil || len(got.Prints) == 0 || len(got.Prints) > 2 || !slices.Contains(got.Reasons, "coverage_unproven") {
		t.Fatalf("retained evidence after pruning: %+v %v", got, err)
	}
}

func TestProfileReaderBoundsBusySymbolWithoutPresentingTruncatedVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("large archive fixture")
	}
	dir := t.TempDir()
	s, err := tickstore.Open(tickstore.Options{Directory: dir, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	closeArchive(t, s)
	paths, err := filepath.Glob(filepath.Join(dir, "tick-*.sqlite"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("archive paths: %v %v", paths, err)
	}
	db, err := sql.Open("sqlite", paths[0])
	if err != nil {
		t.Fatal(err)
	}
	// Populate a sealed test fixture without paying producer scheduling costs.
	_, err = db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<96001)
	 INSERT INTO observations(kind,run_id,connection,ingress,list_index,receipt_ms,symbol,timeframe,time_ms,sequence,price,volume,direction,condition,processing_ordinal,observation_ms,data)
	 SELECT 'print','fixture',0,0,0,1791540000000,'US.BUSY','',1791540000000,i,10,1,0,0,0,1791540000000,
	 '{"Normalized":{"Symbol":"US.BUSY","Seq":'||i||',"TsMs":1791540000000,"Price":10,"Volume":1,"Condition":0}}' FROM n`)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, err := tickstore.NewProfileReader(dir, nil).Read(context.Background(), "US.BUSY", 1791540000000, 1791540000001)
	if !errors.Is(err, md.ErrVolumeProfileTooLarge) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("large fixture must fail explicitly: %d prints %v", len(got.Prints), err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("deadline exceeded by reader: %s", time.Since(start))
	}
}

func TestProfileReaderLateReceiptRotationAndCancellation(t *testing.T) {
	dir := t.TempDir()
	const ts int64 = 1791540000000
	var now atomic.Int64
	now.Store(ts)
	s, err := tickstore.Open(tickstore.Options{Directory: dir, MinFreeBytes: 1, FlushInterval: time.Millisecond, Now: func() time.Time { return time.UnixMilli(now.Load()) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeArchive(t, s) })
	record := func(seq, exchangeTime int64) {
		tick := feed.Tick{Symbol: "US.TEST", Seq: seq, TsMs: exchangeTime, Price: 10, Volume: 10, Condition: feed.TradeConditionAutomaticMatch}
		if !s.Record(feed.Recording{Kind: "print", Symbol: tick.Symbol, TimeMs: exchangeTime, Sequence: seq, Data: struct{ Normalized feed.Tick }{tick}}) {
			t.Fatal("record rejected")
		}
	}
	record(1, ts)
	waitArchive(t, func() bool { return s.Stats().Committed >= 1 })
	now.Add(24 * 60 * 60 * 1000)
	record(2, ts+1)
	waitArchive(t, func() bool { return s.Stats().Committed >= 2 })
	reader := tickstore.NewProfileReader(dir, s)
	got, err := readProfileEventually(reader, "US.TEST", ts, ts+100)
	if err != nil || len(got.Prints) != 2 || !slices.Contains(got.Reasons, "coverage_unproven") {
		t.Fatalf("cross-receipt-day read: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.Read(ctx, "US.TEST", ts, ts+100); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
	if _, err := tickstore.NewProfileReader(dir, nil).Read(context.Background(), "US.TEST", ts, ts+100); !errors.Is(err, tickstore.ErrProfileBusy) {
		t.Fatalf("uncoordinated active writer: %v", err)
	}
	closeArchive(t, s)
	got, err = readProfileEventually(tickstore.NewProfileReader(dir, nil), "US.TEST", ts, ts+100)
	if err != nil || len(got.Prints) != 2 {
		t.Fatalf("sealed cross-segment read: %+v %v", got, err)
	}
}

// Race instrumentation and simultaneous package tests may exhaust a 50 ms
// chunk; retry the same public read as the production controller does.
func readProfileEventually(reader *tickstore.ProfileReader, symbol string, from, to int64) (tickstore.ProfileRead, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		result, err := reader.Read(context.Background(), symbol, from, to)
		if (!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, tickstore.ErrProfileBusy)) || time.Now().After(deadline) {
			return result, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
