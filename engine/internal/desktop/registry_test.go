package desktop

import (
	"errors"
	"testing"
)

type fakeWindow struct {
	minimised bool
	shows     int
	focuses   int
	restores  int
}

func (w *fakeWindow) Show()             { w.shows++ }
func (w *fakeWindow) Focus()            { w.focuses++ }
func (w *fakeWindow) Restore()          { w.restores++; w.minimised = false }
func (w *fakeWindow) IsMinimised() bool { return w.minimised }
func (w *fakeWindow) Close()            {}

func TestWorkspaceRegistryOpenIsIdempotentAndActivates(t *testing.T) {
	registry := NewWorkspaceRegistry(nil)
	created := 0
	first := &fakeWindow{minimised: true}
	open := func() NativeWindow { created++; return first }

	got, err := registry.Open("desk", open)
	if err != nil || got != first {
		t.Fatalf("first Open = (%v, %v), want first window", got, err)
	}
	got, err = registry.Open("desk", func() NativeWindow { created++; return &fakeWindow{} })
	if err != nil || got != first {
		t.Fatalf("second Open = (%v, %v), want first window", got, err)
	}
	if created != 1 || first.restores != 1 || first.shows != 1 || first.focuses != 1 {
		t.Fatalf("created=%d restores=%d shows=%d focuses=%d, want 1,1,1,1", created, first.restores, first.shows, first.focuses)
	}
}

func TestWorkspaceRegistryCloseSignalsOnlyFinalWindow(t *testing.T) {
	lastCloseSignals := 0
	registry := NewWorkspaceRegistry(func() { lastCloseSignals++ })
	for _, id := range []string{"main", "desk"} {
		if _, err := registry.Open(id, func() NativeWindow { return &fakeWindow{} }); err != nil {
			t.Fatal(err)
		}
	}
	removed := registry.Close("unknown")
	if removed || lastCloseSignals != 0 {
		t.Fatalf("unknown close changed registry: removed=%v signals=%d", removed, lastCloseSignals)
	}
	if !registry.Close("main") || registry.Len() != 1 || lastCloseSignals != 0 {
		t.Fatalf("first close: len=%d signals=%d", registry.Len(), lastCloseSignals)
	}
	if !registry.Close("desk") || registry.Len() != 0 || lastCloseSignals != 1 {
		t.Fatalf("final close: len=%d signals=%d", registry.Len(), lastCloseSignals)
	}
	if registry.Close("desk") || lastCloseSignals != 1 {
		t.Fatalf("duplicate close changed state: signals=%d", lastCloseSignals)
	}
}

func TestValidateWorkspaceID(t *testing.T) {
	if err := ValidateWorkspaceID("abc-123"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ValidateWorkspaceID("Workspace:main"), ErrInvalidWorkspaceID) {
		t.Fatal("invalid native name accepted")
	}
}
