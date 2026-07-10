package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenLogFileCreatesParentDir covers the case boot() relies on: logging
// is set up before the store's own db-dir MkdirAll, so the default log
// path's parent directory may not exist yet.
func TestOpenLogFileCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "etape.log")

	f, err := openLogFile(path)
	if err != nil {
		t.Fatalf("openLogFile: %v", err)
	}
	defer f.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected log file to exist: %v", err)
	}
}

// TestOpenLogFileAppends covers -log's documented append semantics: a
// second open (e.g. across process restarts) must not truncate prior
// content.
func TestOpenLogFileAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etape.log")

	f1, err := openLogFile(path)
	if err != nil {
		t.Fatalf("openLogFile (first): %v", err)
	}
	if _, err := f1.WriteString("first\n"); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := f1.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	f2, err := openLogFile(path)
	if err != nil {
		t.Fatalf("openLogFile (second): %v", err)
	}
	if _, err := f2.WriteString("second\n"); err != nil {
		t.Fatalf("write second: %v", err)
	}
	if err := f2.Close(); err != nil {
		t.Fatalf("close second: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if want := "first\nsecond\n"; string(got) != want {
		t.Fatalf("got %q, want %q (second open must append, not truncate)", got, want)
	}
}

// TestConsoleBuildLogPolicy documents the console (!tray) build's contract:
// it always has a usable stderr and never picks a default log file, so its
// behavior is unchanged from before the tray/console split existed. The
// tray build's opposite contract (logToStderr==false, defaultLogPath()!="")
// can't be exercised from this test binary (built without the tray tag);
// it's covered by the release-windows manual smoke test instead.
func TestConsoleBuildLogPolicy(t *testing.T) {
	if !logToStderr {
		t.Error("console build must log to stderr")
	}
	if got := defaultLogPath(); got != "" {
		t.Errorf("console build must have no default log path, got %q", got)
	}
}
