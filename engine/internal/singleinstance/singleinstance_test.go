package singleinstance

import (
	"errors"
	"os"
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
