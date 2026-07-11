package store

import (
	"testing"
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
