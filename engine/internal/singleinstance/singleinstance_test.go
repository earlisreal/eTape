package singleinstance

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestAcquireBlocksSecond verifies the core guarantee: a second Acquire on
// the same path fails with ErrAlreadyRunning while the first is held, and
// succeeds again once release() runs -- mirroring one eTape process locking
// out a second launch pointed at the same DB path, then a clean shutdown
// letting a later launch through.
func TestAcquireBlocksSecond(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etape.db.lock")

	release, err := Acquire(path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	if _, err := Acquire(path); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Acquire while held: err = %v, want ErrAlreadyRunning", err)
	}

	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	release2, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if err := release2(); err != nil {
		t.Fatalf("release2: %v", err)
	}
}

// TestAcquireCreatesMissingFile verifies Acquire works against a path whose
// file doesn't exist yet -- the real caller's dbPath+".lock" never
// pre-exists on a fresh ~/.eTape.
func TestAcquireCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "etape.db.lock")
	// The parent dir must already exist -- callers create it (os.MkdirAll on
	// the DB dir) before calling Acquire; this test mirrors that contract
	// rather than asserting Acquire creates directories itself.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}

	release, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire on missing file: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// TestHelperProcess is not itself a test of singleinstance -- it exists so
// TestAcquireCrossProcessAndAutoRelease can re-exec the compiled test binary
// as a genuinely separate OS process holding the lock (Go's standard
// self-exec helper-process pattern, as used by net/http's and os/exec's own
// tests). Run directly by `go test` (GO_WANT_HELPER_PROCESS unset) it is a
// no-op; it only does anything when the parent test below sets that env var.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	path := os.Getenv("HELPER_LOCK_PATH")
	if _, err := Acquire(path); err != nil {
		fmt.Fprintf(os.Stderr, "helper: Acquire(%q): %v\n", path, err)
		os.Exit(1)
	}
	// Ready marker: the parent blocks on this line before asserting anything,
	// so the two processes never race on who calls Acquire first.
	fmt.Println("locked")
	// Block forever holding the lock -- the parent kills this process
	// (simulating a crash) rather than signalling a graceful shutdown, since
	// crash-safety (the OS auto-releasing the lock on process death, per
	// singleinstance.go's doc comment) is the property this test exists to
	// prove; the clean release() path is already covered in-process by
	// TestAcquireBlocksSecond above.
	select {}
}

// TestAcquireCrossProcessAndAutoRelease covers the guarantee the two tests
// above don't reach: mutual exclusion across two REAL OS processes (mirroring
// two `etape` launches pointed at the same DB path, the actual bug ea37d2c
// fixed), and that the lock is released automatically when the holder is
// killed WITHOUT ever calling release() (mirroring a crashed engine, not a
// clean shutdown) -- the exact crash-safety claim in singleinstance.go's
// package doc comment ("released automatically when the holding process
// exits or crashes").
func TestAcquireCrossProcessAndAutoRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etape.db.lock")

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "HELPER_LOCK_PATH="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	// Always reap the child, even on an early t.Fatalf below -- Kill/Wait are
	// harmless no-ops once the process has already been killed and reaped by
	// the explicit steps further down.
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	ready := bufio.NewScanner(stdout)
	if !ready.Scan() {
		t.Fatalf("helper process exited before signalling ready; stderr: %s", stderr.String())
	}
	if line := ready.Text(); line != "locked" {
		t.Fatalf("helper process: got %q, want %q; stderr: %s", line, "locked", stderr.String())
	}

	// Cross-process exclusion: a second Acquire from THIS process, while the
	// helper -- a genuinely separate process -- holds the lock, must fail
	// exactly like the in-process case TestAcquireBlocksSecond already covers.
	if _, err := Acquire(path); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Acquire while helper holds lock: err = %v, want ErrAlreadyRunning", err)
	}

	// Simulate a crash: kill the helper without giving it any chance to call
	// release(). Wait() reaps it; its (expected) kill-signal exit error is
	// not itself under test here.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper process: %v", err)
	}
	_ = cmd.Wait()

	// Auto-release on crash: with the killed process no longer holding any
	// fd/handle, the OS lock must be gone -- Acquire must succeed again with
	// no manual cleanup, exactly as singleinstance.go's doc comment promises.
	release, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after helper was killed: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}
