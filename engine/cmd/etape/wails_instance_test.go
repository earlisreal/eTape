//go:build wails && !server

package main

import (
	"os"
	"testing"
)

func TestPrepareWailsInstanceUsesDataLockIdentity(t *testing.T) {
	root := t.TempDir()
	previousArgs := os.Args
	os.Args = []string{"etape", "-profile", "test", "-data-root", root}
	defer func() { os.Args = previousArgs }()

	first, err := prepareWailsInstance(func() {})
	if err != nil {
		t.Fatal(err)
	}
	if first.release == nil {
		t.Fatal("first Wails instance did not acquire the data lock")
	}
	defer first.release()

	second, err := prepareWailsInstance(func() {})
	if err != nil {
		t.Fatal(err)
	}
	if second.release != nil {
		t.Fatal("second Wails instance acquired an already-held data lock")
	}
	if first.options.UniqueID != second.options.UniqueID {
		t.Fatalf("Wails identities differ for one data root: %q != %q", first.options.UniqueID, second.options.UniqueID)
	}
}
