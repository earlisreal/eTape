package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/klauspost/compress/zstd"
)

func TestSchemaHasJournalChunks(t *testing.T) {
	s := open(t) // helper in store_test.go: temp DB, fake clock, hourly flush
	var name string
	row := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='journal_chunks'")
	if err := row.Scan(&name); err != nil {
		t.Fatalf("journal_chunks table missing: %v", err)
	}
	// Columns are queryable (empty result, no error).
	if _, err := s.db.Query("SELECT day, chunk_no, first_seq, last_seq, n_rows, body FROM journal_chunks"); err != nil {
		t.Fatalf("journal_chunks columns wrong: %v", err)
	}
}

func TestChunkCodecRoundTrip(t *testing.T) {
	crs := []chunkRow{
		{Seq: 1, TsExch: 1_700_000_000_000, TsRecv: 1_700_000_000_050, Symbol: "US.AAPL", Kind: "ticks", Seed: 0, Payload: `{"Ticks":[{"Symbol":"US.AAPL","Price":100.1}],"Seed":false}`},
		{Seq: 2, TsExch: 1_700_000_001_000, TsRecv: 1_700_000_001_010, Symbol: "US.AAPL", Kind: "book", Seed: 1, Payload: `{"Book":{"Symbol":"US.AAPL"},"Seed":true}`},
		{Seq: 3, TsExch: 1_700_000_002_000, TsRecv: 1_700_000_002_000, Symbol: "", Kind: "conn_up", Seed: 0, Payload: `{}`},
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer enc.Close()
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer dec.Close()

	body, rawLen, err := encodeChunkRows(enc, crs)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if rawLen <= 0 || len(body) == 0 {
		t.Fatalf("empty encode: rawLen=%d body=%d", rawLen, len(body))
	}
	got, err := decodeChunk(dec, body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(crs, got) {
		t.Fatalf("round-trip mismatch:\n in: %#v\nout: %#v", crs, got)
	}
}

// openAtClock opens a temp store whose clock is `now`, so tests can put the
// recorded day strictly in the past (sealable). Mirrors open() in store_test.go.
func openAtClock(t *testing.T, now time.Time) *Store {
	t.Helper()
	s, err := Open(Options{Path: t.TempDir() + "/seal.db", Clock: clock.NewFake(now), FlushInterval: time.Hour})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// insertChunk hand-writes one chunk so read-path tests don't depend on sealing.
func insertChunk(t *testing.T, s *Store, day string, chunkNo int, crs []chunkRow) {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	body, _, err := encodeChunkRows(enc, crs)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(
		`INSERT INTO journal_chunks (day, chunk_no, first_seq, last_seq, n_rows, body) VALUES (?,?,?,?,?,?)`,
		day, chunkNo, crs[0].Seq, crs[len(crs)-1].Seq, int64(len(crs)), body)
	if err != nil {
		t.Fatalf("insert chunk: %v", err)
	}
}

func TestReadJournalDayNoChunksReturnsRaw(t *testing.T) {
	s := open(t)
	evs := sampleEvents() // codec_test.go
	for i, ev := range evs {
		s.RecordEvent(ev, recvBase+int64(i)) // recvBase = 2026-07-06 09:30 ET (journal_test.go)
	}
	s.Flush()
	rows, err := s.ReadJournalDay("2026-07-06")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(evs) {
		t.Fatalf("rows = %d, want %d", len(rows), len(evs))
	}
	for i := range rows {
		if !reflect.DeepEqual(rows[i].Event, evs[i]) {
			t.Fatalf("row %d event mismatch:\n in: %#v\nout: %#v", i, evs[i], rows[i].Event)
		}
		if rows[i].Day != "2026-07-06" {
			t.Fatalf("row %d day = %q", i, rows[i].Day)
		}
	}
}

func TestReadJournalDayMergesChunksThenRaw(t *testing.T) {
	s := open(t)
	// Two sealed rows (seq 1,2) + one raw row (seq 3) for the same day.
	insertChunk(t, s, "2026-07-06", 0, []chunkRow{
		{Seq: 1, TsExch: 10, TsRecv: 10, Symbol: "US.AAPL", Kind: "conn_up", Seed: 0, Payload: `{}`},
		{Seq: 2, TsExch: 20, TsRecv: 20, Symbol: "US.AAPL", Kind: "conn_down", Seed: 0, Payload: `{}`},
	})
	_, err := s.db.Exec(
		`INSERT INTO journal (day, seq, ts_exch, ts_recv, symbol, kind, seed, payload) VALUES (?,?,?,?,?,?,?,?)`,
		"2026-07-06", 3, 30, 30, "US.AAPL", "resynced", 0, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ReadJournalDay("2026-07-06")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	wantSeq := []int64{1, 2, 3}
	for i := range rows {
		if rows[i].Seq != wantSeq[i] {
			t.Fatalf("row %d seq = %d, want %d", i, rows[i].Seq, wantSeq[i])
		}
	}
}

func TestJournalDaysUnionsChunksAndRaw(t *testing.T) {
	s := open(t)
	// 2026-07-05 exists only as a chunk; 2026-07-06 only as a raw row.
	insertChunk(t, s, "2026-07-05", 0, []chunkRow{
		{Seq: 1, TsExch: 1, TsRecv: 1, Symbol: "", Kind: "conn_up", Seed: 0, Payload: `{}`},
	})
	s.RecordEvent(feed.ConnUpEvent{}, recvBase) // 2026-07-06
	s.Flush()
	days, err := s.JournalDays()
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0] != "2026-07-05" || days[1] != "2026-07-06" {
		t.Fatalf("days = %v, want [2026-07-05 2026-07-06]", days)
	}
}
