package tickstore_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/tickstore"
)

func closeArchive(t *testing.T, s *tickstore.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func waitArchive(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("archive condition timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func archiveFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "tick-*.sqlite"))
	if err != nil || len(files) == 0 {
		t.Fatalf("archive files: %v %v", files, err)
	}
	return files
}

func TestFailedAtomicSourceGroupPausesAndRecoversWithGap(t *testing.T) {
	dir := t.TempDir()
	s, err := tickstore.Open(tickstore.Options{Directory: dir, MinFreeBytes: 1, FlushInterval: 20 * time.Millisecond,
		Decode: func(r feed.Recording) ([]feed.Recording, error) {
			return []feed.Recording{r, {Kind: "print", Source: r.Source, Sequence: 7}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", archiveFiles(t, dir)[0])
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TRIGGER reject_print BEFORE INSERT ON observations WHEN NEW.kind='print' BEGIN SELECT RAISE(ABORT,'fixture disk failure'); END"); err != nil {
		t.Fatal(err)
	}
	s.Record(feed.Recording{Kind: "source", Body: []byte{1, 2, 3}, Source: feed.SourceRef{Ingress: 9}})
	waitArchive(t, func() bool { return s.Stats().Paused })
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM observations WHERE kind IN ('source','print')").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial group: %d %v", count, err)
	}
	if s.Record(feed.Recording{Kind: "processing"}) {
		t.Fatal("paused archive accepted work")
	}
	if _, err := db.Exec("DROP TRIGGER reject_print"); err != nil {
		t.Fatal(err)
	}
	waitArchive(t, func() bool { return !s.Stats().Paused && s.Stats().Committed > 0 })
	closeArchive(t, s)
	if err := db.QueryRow("SELECT COUNT(*) FROM observations WHERE kind='capture_gap'").Scan(&count); err != nil || count != 2 {
		t.Fatalf("independent failure gaps: %d %v", count, err)
	}
}

func TestFreeLookupFailureAndRootLockPreserveUnknownFiles(t *testing.T) {
	dir := t.TempDir()
	unknown := filepath.Join(dir, "investigation.sqlite")
	if err := os.WriteFile(unknown, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	var failed atomic.Bool
	options := tickstore.Options{Directory: dir, MinFreeBytes: 1, FlushInterval: 20 * time.Millisecond, FreeSpace: func(string) (uint64, error) {
		if failed.Load() {
			return 0, errors.New("fixture lookup failure")
		}
		return 1 << 40, nil
	}}
	s, err := tickstore.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := tickstore.Open(options); err == nil {
		closeArchive(t, other)
		t.Fatal("second writer acquired root")
	}
	failed.Store(true)
	s.Record(feed.Recording{Kind: "print"})
	waitArchive(t, func() bool { return s.Stats().Paused })
	failed.Store(false)
	waitArchive(t, func() bool { return !s.Stats().Paused })
	closeArchive(t, s)
	if got, err := os.ReadFile(unknown); err != nil || string(got) != "keep" {
		t.Fatalf("unknown file changed: %q %v", got, err)
	}
}

func TestETRetentionRotationCarriesActiveBasis(t *testing.T) {
	dir := t.TempDir()
	var now atomic.Int64
	now.Store(time.Date(2026, 10, 8, 23, 59, 0, 0, session.Loc()).UnixMilli())
	options := tickstore.Options{Directory: dir, MinFreeBytes: 1, RetentionDays: 2, FlushInterval: 20 * time.Millisecond, Now: func() time.Time { return time.UnixMilli(now.Load()) }}
	s, err := tickstore.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	s.Record(feed.Recording{Kind: "bucket_basis", Symbol: "US.AIXI", Timeframe: "10s", TimeMs: 1, Data: struct{ High float64 }{2.67}})
	waitArchive(t, func() bool { return s.Stats().Committed >= 1 })
	now.Add(int64(48 * time.Hour / time.Millisecond))
	s.Record(feed.Recording{Kind: "print", Symbol: "US.AIXI", Price: 2.37})
	waitArchive(t, func() bool { return s.Stats().Committed >= 2 })
	closeArchive(t, s)
	files := archiveFiles(t, dir)
	// The first segment is outside two ET dates; the new segment has its basis.
	if len(files) != 1 || !strings.Contains(files[0], "tick-20261010-") {
		t.Fatalf("retention files=%v", files)
	}
	db, err := sql.Open("sqlite", files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var data string
	if err := db.QueryRow("SELECT data FROM active_basis WHERE basis_key='US.AIXI/10s/1'").Scan(&data); err != nil || !strings.Contains(data, "2.67") {
		t.Fatalf("retained basis=%s %v", data, err)
	}
}

func TestCrashWALRetainsCommittedEvidenceAndMarksRestart(t *testing.T) {
	if dir := os.Getenv("ETAPE_TICKSTORE_CRASH_FIXTURE"); dir != "" {
		s, err := tickstore.Open(tickstore.Options{Directory: dir, MinFreeBytes: 1, FlushInterval: 10 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		s.Record(feed.Recording{Kind: "print", Sequence: 9007199254740993, Price: 1.2345})
		waitArchive(t, func() bool { return s.Stats().Committed >= 1 })
		os.Exit(19)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashWALRetainsCommittedEvidenceAndMarksRestart$")
	cmd.Env = append(os.Environ(), "ETAPE_TICKSTORE_CRASH_FIXTURE="+dir)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 19 {
		t.Fatalf("crash fixture: %v %s", err, output)
	}
	before := make(map[string]string)
	for _, path := range archiveFiles(t, dir) {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			data, err := os.ReadFile(path + suffix)
			if err != nil {
				t.Fatal(err)
			}
			before[path+suffix] = string(data)
		}
	}
	if rejected, err := tickstore.Open(tickstore.Options{Directory: dir, MinFreeBytes: 1, FreeSpace: func(string) (uint64, error) { return 1, nil }}); err == nil {
		closeArchive(t, rejected)
		t.Fatal("recovery accepted insufficient reserve")
	}
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("rejected recovery changed %s: %v", path, err)
		}
	}
	s, err := tickstore.Open(tickstore.Options{Directory: dir, MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	closeArchive(t, s)
	var prints, gaps int
	for _, file := range archiveFiles(t, dir) {
		db, err := sql.Open("sqlite", file)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM observations WHERE kind='print' AND sequence=9007199254740993 AND price=1.2345").Scan(&n); err != nil {
			t.Fatal(err)
		}
		prints += n
		if err := db.QueryRow("SELECT COUNT(*) FROM observations WHERE kind='restart_gap'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		gaps += n
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if prints != 1 || gaps != 1 {
		t.Fatalf("committed prints/restart gaps=%d/%d", prints, gaps)
	}
}

func TestPinnedReaderPausesRotationAndRetainsWAL(t *testing.T) {
	dir := t.TempDir()
	s, err := tickstore.Open(tickstore.Options{Directory: dir, MinFreeBytes: 1, MaxBytes: 8 << 20, SegmentBytes: 1, FlushInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	s.Record(feed.Recording{Kind: "print", Sequence: 1})
	waitArchive(t, func() bool { return s.Stats().Committed >= 1 })
	files := archiveFiles(t, dir)
	db, err := sql.Open("sqlite", files[len(files)-1])
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM observations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	s.Record(feed.Recording{Kind: "print", Sequence: 2})
	waitArchive(t, func() bool { return s.Stats().Paused })
	var physical int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		physical += info.Size()
	}
	if physical > 8<<20 {
		t.Fatalf("physical cap exceeded: %d", physical)
	}
	if _, err := os.Stat(files[len(files)-1] + "-wal"); err != nil {
		t.Fatalf("pinned WAL lost: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	waitArchive(t, func() bool { return !s.Stats().Paused })
	closeArchive(t, s)
}
