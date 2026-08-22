//go:build wails

package uiapi

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/uistate"
	"github.com/earlisreal/eTape/engine/internal/wailsruntime"
)

func TestEngineServiceUsesSharedAdmissionGate(t *testing.T) {
	runtime := wailsruntime.New()
	service := NewEngineService(runtime)
	ConfigureEngineService(service, QuerySources{
		Fills: &querySourceFake{rows: []exec.FillRow{{OrderID: "ET1", Side: "BUY", Qty: 1, Venue: "sim"}}},
		Clock: clock.NewFake(time.UnixMilli(1)),
	})

	fills, err := service.QueryFills(context.Background(), QueryFillsArgs{Symbol: "US.AAPL"})
	if err != nil || len(fills) != 1 || fills[0].Side != SideBuy {
		t.Fatalf("fills = %#v, err=%v", fills, err)
	}
	if got := runtime.Gate().InFlight(); got != 0 {
		t.Fatalf("gate in-flight after completed binding = %d", got)
	}

	runtime.BeginStop()
	if _, err := service.QueryFills(context.Background(), QueryFillsArgs{}); !errors.Is(err, wailsruntime.ErrStopping) {
		t.Fatalf("post-stop query error = %v, want %v", err, wailsruntime.ErrStopping)
	}
}

type workspaceConfigFake struct {
	mu      sync.Mutex
	values  map[string]string
	flushes int
}

func newWorkspaceConfigFake() *workspaceConfigFake {
	return &workspaceConfigFake{values: map[string]string{}}
}
func (f *workspaceConfigFake) GetConfig(key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.values[key]
	return v, ok, nil
}
func (f *workspaceConfigFake) SetConfig(key, value string) {
	f.mu.Lock()
	f.values[key] = value
	f.mu.Unlock()
}
func (f *workspaceConfigFake) DeleteConfig(key string) {
	f.mu.Lock()
	delete(f.values, key)
	f.mu.Unlock()
}
func (f *workspaceConfigFake) Flush() { f.mu.Lock(); f.flushes++; f.mu.Unlock() }

func TestWorkspaceServiceOwnsCatalogDocumentsAndOpenSet(t *testing.T) {
	state, err := uistate.NewStore(newWorkspaceConfigFake())
	if err != nil {
		t.Fatal(err)
	}
	runtime := wailsruntime.New()
	service := NewWorkspaceService(runtime, state)

	created, err := service.CreateWorkspace(context.Background(), CreateWorkspaceArgs{WorkspaceID: "desk", Name: "Desk"})
	if err != nil || created.Status != WorkspaceAccepted || created.Revision != 1 || created.CatalogRevision != 1 {
		t.Fatalf("create = %+v err=%v", created, err)
	}
	loaded, err := service.LoadWorkspace(context.Background(), WorkspaceIDArgs{WorkspaceID: "desk"})
	if err != nil || loaded.Status != WorkspaceAccepted || loaded.Document == nil || loaded.Revision != 1 {
		t.Fatalf("load = %+v err=%v", loaded, err)
	}
	loaded.Document.Layout = map[string]any{"opaque": []any{1, "two"}}
	saved, err := service.SaveWorkspace(context.Background(), SaveWorkspaceArgs{WorkspaceID: "desk", Document: *loaded.Document, ExpectedRevision: loaded.Revision})
	if err != nil || saved.Status != WorkspaceAccepted || saved.Revision != 2 {
		t.Fatalf("save = %+v err=%v", saved, err)
	}
	stale, err := service.SaveWorkspace(context.Background(), SaveWorkspaceArgs{WorkspaceID: "desk", Document: *loaded.Document, ExpectedRevision: loaded.Revision})
	if err != nil || stale.Status != WorkspaceBlocked {
		t.Fatalf("stale save = %+v err=%v", stale, err)
	}

	if opened, err := service.OpenWorkspace(context.Background(), WorkspaceIDArgs{WorkspaceID: "desk"}); err != nil || opened.Status != WorkspaceAccepted || opened.Revision != 2 {
		t.Fatalf("open = %+v err=%v", opened, err)
	}
	if focused, err := service.FocusWorkspace(context.Background(), WorkspaceIDArgs{WorkspaceID: "desk"}); err != nil || focused.Status != WorkspaceAccepted || focused.Revision != 2 {
		t.Fatalf("focus = %+v err=%v", focused, err)
	}
	deleted, err := service.DeleteWorkspace(context.Background(), DeleteWorkspaceArgs{WorkspaceID: "desk", ExpectedCatalogRevision: created.CatalogRevision})
	if err != nil || deleted.Status != WorkspaceBlocked {
		t.Fatalf("delete-open = %+v err=%v", deleted, err)
	}
	closed, err := service.CloseWorkspace(context.Background(), WorkspaceIDArgs{WorkspaceID: "desk"})
	if err != nil || closed.Status != WorkspaceAccepted || closed.Revision != 3 {
		t.Fatal(err)
	}
	deleted, err = service.DeleteWorkspace(context.Background(), DeleteWorkspaceArgs{WorkspaceID: "desk", ExpectedCatalogRevision: closed.CatalogRevision})
	if err != nil || deleted.Status != WorkspaceAccepted {
		t.Fatalf("delete = %+v err=%v", deleted, err)
	}
	reserved, err := service.RenameWorkspace(context.Background(), RenameWorkspaceArgs{WorkspaceID: uistate.MonitoringWorkspaceID, Name: "Nope"})
	if err != nil || reserved.Status != WorkspaceBlocked {
		t.Fatalf("reserved rename = %+v err=%v", reserved, err)
	}
	unknown, err := service.CloseWorkspace(context.Background(), WorkspaceIDArgs{WorkspaceID: "missing"})
	if err != nil || unknown.Status != WorkspaceBlocked {
		t.Fatalf("unknown close = %+v err=%v", unknown, err)
	}
}

func TestWorkspaceServiceConcurrentCreatesSerializeCatalogRevision(t *testing.T) {
	state, err := uistate.NewStore(newWorkspaceConfigFake())
	if err != nil {
		t.Fatal(err)
	}
	service := NewWorkspaceService(wailsruntime.New(), state)
	var wg sync.WaitGroup
	results := make(chan WorkspaceMutationResult, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := service.CreateWorkspace(context.Background(), CreateWorkspaceArgs{WorkspaceID: fmt.Sprintf("desk-%d", i), Name: fmt.Sprintf("Desk %d", i)})
			if err == nil {
				results <- result
			}
		}(i)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for result := range results {
		if result.Status == WorkspaceAccepted {
			accepted++
		}
	}
	if accepted != 8 {
		t.Fatalf("accepted concurrent creates = %d, want 8", accepted)
	}
	catalog, err := service.GetWorkspaceCatalog(context.Background())
	if err != nil || len(catalog.Entries) != 9 || catalog.Revision != 8 {
		t.Fatalf("catalog = %+v err=%v", catalog, err)
	}
	for i := 0; i < 4; i++ {
		result, err := service.OpenWorkspace(context.Background(), WorkspaceIDArgs{WorkspaceID: fmt.Sprintf("desk-%d", i)})
		if err != nil || result.Status != WorkspaceAccepted {
			t.Fatalf("open desk-%d = %+v err=%v", i, result, err)
		}
		result, err = service.OpenWorkspace(context.Background(), WorkspaceIDArgs{WorkspaceID: fmt.Sprintf("desk-%d", i)})
		if err != nil || result.Status != WorkspaceAccepted {
			t.Fatalf("idempotent open desk-%d = %+v err=%v", i, result, err)
		}
	}
	if state.Windows().Len() != 4 {
		t.Fatalf("open window count = %d, want 4", state.Windows().Len())
	}
}

func TestWorkspaceServiceFlushAcknowledgesDurabilityBarrier(t *testing.T) {
	config := newWorkspaceConfigFake()
	state, err := uistate.NewStore(config)
	if err != nil {
		t.Fatal(err)
	}
	service := NewWorkspaceService(wailsruntime.New(), state)
	result, err := service.FlushWorkspace(context.Background())
	if err != nil || result.Status != WorkspaceAccepted {
		t.Fatalf("flush = %+v err=%v", result, err)
	}
	config.mu.Lock()
	flushes := config.flushes
	config.mu.Unlock()
	if flushes != 1 {
		t.Fatalf("flush calls = %d, want 1", flushes)
	}
}

func TestWorkspaceServiceCatalogConflictsReturnCurrentSnapshot(t *testing.T) {
	state, err := uistate.NewStore(newWorkspaceConfigFake())
	if err != nil {
		t.Fatal(err)
	}
	service := NewWorkspaceService(wailsruntime.New(), state)
	first, err := service.CreateWorkspace(context.Background(), CreateWorkspaceArgs{WorkspaceID: "desk", Name: "Desk"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateWorkspace(context.Background(), CreateWorkspaceArgs{WorkspaceID: "other", Name: "Other", ExpectedCatalogRevision: first.CatalogRevision}); err != nil {
		t.Fatal(err)
	}
	stale, err := service.RenameWorkspace(context.Background(), RenameWorkspaceArgs{WorkspaceID: "desk", Name: "Stale", ExpectedCatalogRevision: first.CatalogRevision})
	if err != nil || stale.Status != WorkspaceBlocked || stale.CatalogRevision != 2 || len(stale.Entries) != 3 {
		t.Fatalf("stale rename = %+v err=%v", stale, err)
	}
	stale, err = service.DeleteWorkspace(context.Background(), DeleteWorkspaceArgs{WorkspaceID: "desk", ExpectedCatalogRevision: first.CatalogRevision})
	if err != nil || stale.Status != WorkspaceBlocked || stale.CatalogRevision != 2 || len(stale.Entries) != 3 {
		t.Fatalf("stale delete = %+v err=%v", stale, err)
	}
}
