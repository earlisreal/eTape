package store

import (
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
