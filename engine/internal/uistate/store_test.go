package uistate

import (
	"errors"
	"sync"
	"testing"
)

type configFake struct {
	mu     sync.Mutex
	values map[string]string
}

func newConfigFake() *configFake { return &configFake{values: map[string]string{}} }
func (f *configFake) GetConfig(key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.values[key]
	return v, ok, nil
}
func (f *configFake) SetConfig(key, value string) { f.mu.Lock(); f.values[key] = value; f.mu.Unlock() }
func (f *configFake) DeleteConfig(key string)     { f.mu.Lock(); delete(f.values, key); f.mu.Unlock() }

func TestStoreSerializesCatalogDocumentAndOpenWindowState(t *testing.T) {
	cfg := newConfigFake()
	state, err := NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}

	catalog, doc, err := state.CreateWorkspace("desk", "Desk", nil, 0)
	if err != nil || catalog.Revision != 1 || doc.Revision != 1 {
		t.Fatalf("create = catalog=%+v doc=%+v err=%v", catalog, doc, err)
	}
	loaded, err := state.LoadDocument("desk")
	if err != nil || !loaded.Exists || loaded.Revision != 1 {
		t.Fatalf("load = %+v err=%v", loaded, err)
	}

	if _, err := state.OpenWorkspace("desk", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := state.OpenWorkspace("desk", func() NativeWindow { t.Fatal("duplicate window created"); return nil }); err != nil {
		t.Fatal(err)
	}
	openedCatalog, err := state.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.DeleteWorkspace("desk", openedCatalog.Revision); !errors.Is(err, ErrWorkspaceConflict) {
		t.Fatalf("delete-open error = %v, want %v", err, ErrWorkspaceConflict)
	}
	if !state.CloseWorkspace("desk") {
		t.Fatal("close did not remove open identity")
	}
	current, err := state.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.DeleteWorkspace("desk", current.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRejectsStaleDocumentAndCatalogMutations(t *testing.T) {
	state, err := NewStore(newConfigFake())
	if err != nil {
		t.Fatal(err)
	}
	catalog, _, err := state.CreateWorkspace("desk", "Desk", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := state.LoadDocument("desk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.RenameWorkspace("desk", "Renamed", catalog.Revision+1); !errors.Is(err, ErrCatalogConflict) {
		t.Fatalf("stale rename error = %v", err)
	}
	if _, err := state.SaveDocument("desk", doc.Document, doc.Revision-1); !errors.Is(err, ErrWorkspaceConflict) {
		t.Fatalf("stale save error = %v", err)
	}
}

func TestStoreNotifiesCatalogAndDocumentRevision(t *testing.T) {
	state, err := NewStore(newConfigFake())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []Invalidation
	state.SetInvalidationNotifier(func(invalidation Invalidation) { mu.Lock(); got = append(got, invalidation); mu.Unlock() })
	if _, _, err := state.CreateWorkspace("desk", "Desk", nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := state.OpenWorkspace("desk", nil); err != nil {
		t.Fatal(err)
	}
	if !state.CloseWorkspace("desk") {
		t.Fatal("close did not remove open identity")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 4 || got[0].Kind != "catalog" || got[0].Revision != 1 || got[1].Kind != "document" || got[1].Revision != 1 || got[2].Revision != 2 || got[3].Revision != 3 {
		t.Fatalf("invalidations = %#v", got)
	}
}

func TestStoreBoundsOpaqueLayoutWithoutInterpretingIt(t *testing.T) {
	state, err := NewStore(newConfigFake())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.CreateWorkspace("desk", "Desk", []byte(`{"name":"other","layout":null}`), 0); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("identity mismatch = %v", err)
	}
	if _, _, err := state.CreateWorkspace("desk", "Desk", []byte(`{"name":"desk","layout":"`+string(make([]byte, MaxWorkspaceLayoutBytes))+`"}`), 0); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("oversized layout = %v", err)
	}
}
