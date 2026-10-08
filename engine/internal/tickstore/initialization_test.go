package tickstore

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFailedInitializationRemovesOnlyNewSegment(t *testing.T) {
	dir := t.TempDir()
	unknown := filepath.Join(dir, "investigation.sqlite")
	if err := os.WriteFile(unknown, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Store{run: "0123456789abcdef0123456789abcdef", basis: map[string]string{"fixture": "{}"}, segments: make(map[string]int64)}
	calls := 0
	s.opt = Options{Directory: dir, MaxBytes: 10 << 30, MinFreeBytes: 1, RetentionDays: 30, FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }, Now: func() time.Time {
		calls++
		files, _ := filepath.Glob(filepath.Join(dir, "tick-*.sqlite"))
		if len(files) == 1 {
			db, err := sql.Open("sqlite", files[0])
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec("DROP TABLE active_basis")
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		}
		return time.UnixMilli(1)
	}}
	if err := s.newSegment(); err == nil {
		t.Fatal("fixture initialization unexpectedly succeeded")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "investigation.sqlite" {
		t.Fatalf("orphan entries=%v err=%v calls=%d", entries, err, calls)
	}
	got, err := os.ReadFile(unknown)
	if err != nil || string(got) != "keep" {
		t.Fatal("previous evidence changed")
	}
}
