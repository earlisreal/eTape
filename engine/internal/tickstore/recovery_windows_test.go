//go:build windows

package tickstore_test

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/tickstore"
	"golang.org/x/sys/windows"
)

func TestFailedWindowsDeletionRemainsCountedUnderPressure(t *testing.T) {
	dir := t.TempDir()
	var now atomic.Int64
	now.Store(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC).UnixMilli())
	options := tickstore.Options{Directory: dir, MinFreeBytes: 1, MaxBytes: 8 << 20, RetentionDays: 2, FlushInterval: 20 * time.Millisecond, Now: func() time.Time { return time.UnixMilli(now.Load()) }}
	s, err := tickstore.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	s.Record(feed.Recording{Kind: "print", Sequence: 1})
	closeArchive(t, s)
	old := archiveFiles(t, dir)[0]
	path, err := windows.UTF16PtrFromString(old)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if handle != windows.InvalidHandle {
			_ = windows.CloseHandle(handle)
		}
	}()
	now.Add(int64(48 * time.Hour / time.Millisecond))
	s, err = tickstore.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	padding := filepath.Join(dir, "fixture-padding.bin")
	if err := os.WriteFile(padding, make([]byte, 6<<20), 0600); err != nil {
		t.Fatal(err)
	}
	s.Record(feed.Recording{Kind: "print", Sequence: 2})
	waitArchive(t, func() bool { return s.Stats().Paused })
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("failed deletion lost evidence: %v", err)
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	handle = windows.InvalidHandle
	if err := os.Remove(padding); err != nil {
		t.Fatal(err)
	}
	waitArchive(t, func() bool { return !s.Stats().Paused })
	closeArchive(t, s)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("retired segment remained after reader released: %v", err)
	}
}
