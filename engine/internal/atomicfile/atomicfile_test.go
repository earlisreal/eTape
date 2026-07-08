package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesFileWithPerm(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.txt")
	if err := Write(p, []byte("hello"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "hello" {
		t.Fatalf("read back: %q err=%v", b, err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", fi.Mode().Perm())
	}
}

func TestWriteOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.txt")
	_ = os.WriteFile(p, []byte("old"), 0o644)
	if err := Write(p, []byte("new-and-longer"), 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "new-and-longer" {
		t.Fatalf("got %q", b)
	}
	// No temp files left behind in the dir.
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("expected 1 file, found %d", len(ents))
	}
}
