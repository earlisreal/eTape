package tickstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveWALDefersCheckpointUntilMaintenance(t *testing.T) {
	for _, name := range []string{"initial", "replacement"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "archive.sqlite")
			db, err := openDB(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if name == "replacement" {
				db.SetMaxIdleConns(0)
				db.SetMaxIdleConns(1)
			}
			if _, err := db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE archive_data (value BLOB)"); err != nil {
				t.Fatal(err)
			}
			var synchronous, pageSize int
			if err := db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
				t.Fatal(err)
			}
			if synchronous != 2 {
				t.Fatalf("archive commits must retain synchronous=FULL, got %d", synchronous)
			}
			if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			// Cross SQLite's default 1,000-page checkpoint threshold in one commit.
			if _, err := db.Exec("INSERT INTO archive_data VALUES (zeroblob(?))", pageSize*1100); err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if after.Size() != before.Size() {
				t.Fatalf("live commit checkpointed the database: size grew from %d to %d", before.Size(), after.Size())
			}
			var length int
			if err := db.QueryRow("SELECT length(value) FROM archive_data").Scan(&length); err != nil {
				t.Fatal(err)
			}
			if length != pageSize*1100 {
				t.Fatalf("committed WAL data unavailable: got %d bytes", length)
			}
			if err := checkpoint(db); err != nil {
				t.Fatal(err)
			}
			wal, err := os.Stat(path + "-wal")
			if err != nil {
				t.Fatal(err)
			}
			if wal.Size() != 0 {
				t.Fatalf("maintenance did not truncate WAL: %d bytes remain", wal.Size())
			}
		})
	}
}
