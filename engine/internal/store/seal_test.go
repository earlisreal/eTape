package store

import (
	"reflect"
	"testing"

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
