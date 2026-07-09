package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Options{Path: filepath.Join(t.TempDir(), "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAppendExecEventSeqAndReadBack(t *testing.T) {
	s := openTestStore(t)
	mk := func(ts int64, oid string) exec.EventEnvelope {
		return exec.EventEnvelope{TsMs: ts, Source: "local", Venue: "sim-1", OrderID: oid, Kind: "order_submitted", Payload: []byte(`{"Order":{"ID":"` + oid + `"}}`)}
	}
	s1, err := s.AppendExecEvent(mk(1000, "ETa"), nil)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := s.AppendExecEvent(mk(1001, "ETb"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s2 <= s1 {
		t.Fatalf("seq not increasing: %d then %d", s1, s2)
	}
	got, err := s.ReadExecEventsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seq != s1 || got[1].Seq != s2 || got[0].OrderID != "ETa" || got[1].OrderID != "ETb" {
		t.Fatalf("read-back wrong: %+v", got)
	}
	// fromMs filter excludes older events.
	since, _ := s.ReadExecEventsSince(1001)
	if len(since) != 1 || since[0].OrderID != "ETb" {
		t.Fatalf("since filter wrong: %+v", since)
	}
}

func TestAppendExecEventFillProjection(t *testing.T) {
	s := openTestStore(t)
	env := exec.EventEnvelope{TsMs: 2000, Source: "ws", Venue: "sim-1", OrderID: "ETc", Kind: "order_filled", Payload: []byte(`{}`)}
	fill := &exec.FillRow{OrderID: "ETc", Symbol: "AAPL", Side: "BUY", Qty: 10, Price: 100, TsMs: 2000, Venue: "sim-1"}
	seq, err := s.AppendExecEvent(env, fill)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.QueryFills("AAPL", 0, 9999)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].OrderID != "ETc" || rows[0].Qty != 10 || rows[0].Price != 100 {
		t.Fatalf("fill query wrong: %+v", rows)
	}
	// The fill row references the event seq.
	var refSeq int64
	if err := s.db.QueryRow("SELECT seq FROM fills WHERE order_id='ETc'").Scan(&refSeq); err != nil {
		t.Fatal(err)
	}
	if refSeq != seq {
		t.Fatalf("fill seq FK = %d, want %d", refSeq, seq)
	}
}

// TestQueryFillsSinceAllSymbolsOrderedSinceBoundary exercises the seed query's
// two distinguishing properties versus QueryFills: it spans every
// symbol/venue (not one), and it is an open-ended "since" query (a lower
// bound only, no upper bound) ordered by ts.
func TestQueryFillsSinceAllSymbolsOrderedSinceBoundary(t *testing.T) {
	s := openTestStore(t)
	mk := func(ts int64, oid, venue string) exec.EventEnvelope {
		return exec.EventEnvelope{TsMs: ts, Source: "ws", Venue: venue, OrderID: oid, Kind: "order_filled", Payload: []byte(`{}`)}
	}
	fill := func(oid, symbol, venue string, ts int64) *exec.FillRow {
		return &exec.FillRow{OrderID: oid, Symbol: symbol, Side: "BUY", Qty: 1, Price: 10, TsMs: ts, Venue: venue}
	}
	// Before the fromMs boundary (5000): must be excluded.
	if _, err := s.AppendExecEvent(mk(1000, "before-1", "sim-1"), fill("before-1", "AAPL", "sim-1", 1000)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendExecEvent(mk(4999, "before-2", "sim-2"), fill("before-2", "MSFT", "sim-2", 4999)); err != nil {
		t.Fatal(err)
	}
	// At/after the boundary, across two symbols and venues: must be included.
	if _, err := s.AppendExecEvent(mk(6000, "after-1", "sim-2"), fill("after-1", "MSFT", "sim-2", 6000)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendExecEvent(mk(5000, "at-1", "sim-1"), fill("at-1", "AAPL", "sim-1", 5000)); err != nil {
		t.Fatal(err)
	}
	got, err := s.QueryFillsSince(context.Background(), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 fills at/after boundary, got %d: %+v", len(got), got)
	}
	// Ordered by ts ascending regardless of insertion order.
	if got[0].OrderID != "at-1" || got[0].Symbol != "AAPL" || got[0].Venue != "sim-1" || got[0].TsMs != 5000 {
		t.Fatalf("first fill wrong: %+v", got[0])
	}
	if got[1].OrderID != "after-1" || got[1].Symbol != "MSFT" || got[1].Venue != "sim-2" || got[1].TsMs != 6000 {
		t.Fatalf("second fill wrong: %+v", got[1])
	}
}
