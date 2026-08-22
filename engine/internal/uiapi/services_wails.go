//go:build wails

package uiapi

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/earlisreal/eTape/engine/internal/uistate"
	"github.com/earlisreal/eTape/engine/internal/wailsruntime"
)

// EngineService is the concrete singleton for low-rate engine queries. The
// runtime gate is deliberately entered in this service, at the same boundary
// used by Workspace Stream handlers, so shutdown cannot race storage access.
type EngineService struct {
	runtime *wailsruntime.Runtime

	mu        sync.RWMutex
	queries   *ReadQueries
	mutations *Mutations
}

func NewEngineService(runtime *wailsruntime.Runtime) *EngineService {
	return &EngineService{runtime: runtime}
}

func (s *EngineService) ServiceName() string { return "EngineService" }

// ConfigureEngineService is kept as a package function so source wiring does
// not become another generated Wails method.
func ConfigureEngineService(service *EngineService, sources QuerySources) {
	if service == nil {
		return
	}
	service.mu.Lock()
	service.queries = NewReadQueries(sources)
	service.mu.Unlock()
}

func ConfigureEngineMutations(service *EngineService, sources MutationSources) {
	if service == nil {
		return
	}
	service.mu.Lock()
	service.mutations = NewMutations(sources)
	service.mu.Unlock()
}

func (s *EngineService) read(ctx context.Context) (context.Context, *ReadQueries, func(), error) {
	if s == nil || s.runtime == nil {
		return nil, nil, nil, ErrQueriesUnavailable
	}
	workCtx, release, err := s.runtime.EnterContext(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	s.mu.RLock()
	queries := s.queries
	s.mu.RUnlock()
	if queries == nil {
		release()
		return nil, nil, nil, ErrQueriesUnavailable
	}
	return workCtx, queries, release, nil
}

func (s *EngineService) QueryChartWindow(ctx context.Context, args QueryChartWindowArgs) (QueryChartWindowResult, error) {
	workCtx, queries, release, err := s.read(ctx)
	if err != nil {
		return QueryChartWindowResult{}, err
	}
	defer release()
	return queries.QueryChartWindow(workCtx, args)
}

func (s *EngineService) QueryFills(ctx context.Context, args QueryFillsArgs) ([]Fill, error) {
	workCtx, queries, release, err := s.read(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return queries.QueryFills(workCtx, args)
}

func (s *EngineService) QueryCycleFills(ctx context.Context, args QueryCycleFillsArgs) (QueryCycleFillsResult, error) {
	workCtx, queries, release, err := s.read(ctx)
	if err != nil {
		return QueryCycleFillsResult{}, err
	}
	defer release()
	return queries.QueryCycleFills(workCtx, args)
}

func (s *EngineService) QueryLocateEligibility(ctx context.Context, args QueryLocateEligibilityArgs) (LocateEligibility, error) {
	workCtx, queries, release, err := s.read(ctx)
	if err != nil {
		return LocateEligibility{}, err
	}
	defer release()
	return queries.QueryLocateEligibility(workCtx, args)
}

func (s *EngineService) QueryLocateQuotes(ctx context.Context, args QueryLocateQuotesArgs) (LocateQuoteResult, error) {
	workCtx, queries, release, err := s.read(ctx)
	if err != nil {
		return LocateQuoteResult{}, err
	}
	defer release()
	return queries.QueryLocateQuotes(workCtx, args)
}

func (s *EngineService) QueryLocates(ctx context.Context, args QueryLocatesArgs) (LocateListResult, error) {
	workCtx, queries, release, err := s.read(ctx)
	if err != nil {
		return LocateListResult{}, err
	}
	defer release()
	return queries.QueryLocates(workCtx, args)
}

func (s *EngineService) QueryLocate(ctx context.Context, args QueryLocateArgs) (LocateRecord, error) {
	workCtx, queries, release, err := s.read(ctx)
	if err != nil {
		return LocateRecord{}, err
	}
	defer release()
	return queries.QueryLocate(workCtx, args)
}

func (s *EngineService) ExportFills(ctx context.Context, args ExportFillsArgs) (ExportFillsResult, error) {
	workCtx, queries, release, err := s.read(ctx)
	if err != nil {
		return ExportFillsResult{}, err
	}
	defer release()
	return queries.ExportFills(workCtx, args)
}

func (s *EngineService) mutate(ctx context.Context) (context.Context, *Mutations, func(), error) {
	if s == nil || s.runtime == nil {
		return nil, nil, nil, ErrMutationsUnavailable
	}
	workCtx, release, err := s.runtime.EnterContext(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	s.mu.RLock()
	mutations := s.mutations
	s.mu.RUnlock()
	if mutations == nil {
		release()
		return nil, nil, nil, ErrMutationsUnavailable
	}
	return workCtx, mutations, release, nil
}

func (s *EngineService) GetScannerFilters(ctx context.Context) (ScannerFiltersView, error) {
	workCtx, mutations, release, err := s.mutate(ctx)
	if err != nil {
		return ScannerFiltersView{}, err
	}
	defer release()
	return mutations.GetScannerFilters(workCtx)
}

func (s *EngineService) SetScannerFilters(ctx context.Context, args SetScannerFiltersArgs) (ScannerFiltersMutationResult, error) {
	workCtx, mutations, release, err := s.mutate(ctx)
	if err != nil {
		return ScannerFiltersMutationResult{}, err
	}
	defer release()
	return mutations.SetScannerFilters(workCtx, args)
}

func (s *EngineService) WatchlistAdd(ctx context.Context, args WatchlistMutationArgs) (WatchlistMutationResult, error) {
	workCtx, mutations, release, err := s.mutate(ctx)
	if err != nil {
		return WatchlistMutationResult{}, err
	}
	defer release()
	return mutations.WatchlistAdd(workCtx, args)
}

func (s *EngineService) WatchlistRemove(ctx context.Context, args WatchlistMutationArgs) (WatchlistMutationResult, error) {
	workCtx, mutations, release, err := s.mutate(ctx)
	if err != nil {
		return WatchlistMutationResult{}, err
	}
	defer release()
	return mutations.WatchlistRemove(workCtx, args)
}

func (s *EngineService) GetVenueSetup(ctx context.Context) (VenueSetup, error) {
	workCtx, mutations, release, err := s.mutate(ctx)
	if err != nil {
		return VenueSetup{}, err
	}
	defer release()
	return mutations.GetVenueSetup(workCtx)
}

func (s *EngineService) SetVenueSetup(ctx context.Context, args SetVenueSetupArgs) (MutationResult, error) {
	workCtx, mutations, release, err := s.mutate(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer release()
	return mutations.SetVenueSetup(workCtx, args)
}

func (s *EngineService) PutCredential(ctx context.Context, args PutCredentialArgs) (MutationResult, error) {
	workCtx, mutations, release, err := s.mutate(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer release()
	return mutations.PutCredential(workCtx, args)
}

func (s *EngineService) DeleteCredential(ctx context.Context, args DeleteCredentialArgs) (MutationResult, error) {
	workCtx, mutations, release, err := s.mutate(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer release()
	return mutations.DeleteCredential(workCtx, args)
}

func (s *EngineService) TestConnection(ctx context.Context, args TestConnectionArgs) (TestConnectionResult, error) {
	workCtx, mutations, release, err := s.mutate(ctx)
	if err != nil {
		return TestConnectionResult{}, err
	}
	defer release()
	return mutations.TestConnection(workCtx, args)
}

// WorkspaceService is the concrete singleton reserved for workspace-scoped
// low-rate operations. Stream subscriptions, demands, indicators, snapshots,
// and updates remain owned by the Workspace Stream in ticket 08.
type WorkspaceService struct {
	runtime *wailsruntime.Runtime
	state   *uistate.Store
	window  WorkspaceWindowController
}

// WorkspaceWindowController is the desktop adapter. The headless Wails
// server leaves it nil and uses the canonical open set without a Native Window.
type WorkspaceWindowController interface {
	OpenWorkspace(string) error
	FocusWorkspace(string) error
	CloseWorkspace(string) error
	CompleteWorkspaceClose(string, string) error
}

func NewWorkspaceService(runtime *wailsruntime.Runtime, state *uistate.Store, controllers ...WorkspaceWindowController) *WorkspaceService {
	service := &WorkspaceService{runtime: runtime, state: state}
	if len(controllers) > 0 {
		service.window = controllers[0]
	}
	return service
}

func (s *WorkspaceService) ServiceName() string { return "WorkspaceService" }

// ConfigureWorkspaceService attaches the existing SQLite-backed config store.
// It is called by the engine composition root after the store opens, before
// Workspace mutations can be admitted by Wails.
func ConfigureWorkspaceService(service *WorkspaceService, persistence uistate.Persistence) error {
	if service == nil || service.state == nil {
		return ErrWorkspaceUnavailable
	}
	if err := service.state.ConfigurePersistence(persistence); err != nil {
		return err
	}
	if service.runtime != nil {
		_ = service.runtime.RegisterWorkspace(uistate.MainWorkspaceID)
		_ = service.runtime.RegisterWorkspace(uistate.MonitoringWorkspaceID)
		if catalog, err := service.state.CatalogSnapshot(); err == nil {
			for _, entry := range catalog.Entries {
				_ = service.runtime.RegisterWorkspace(entry.ID)
			}
		}
	}
	return nil
}

func ConfigureWorkspaceNotifier(service *WorkspaceService, notifier uistate.Listener) {
	if service != nil {
		service.setInvalidationNotifier(notifier)
	}
}

func (s *WorkspaceService) setInvalidationNotifier(notifier uistate.Listener) {
	if s != nil && s.state != nil {
		s.state.SetInvalidationNotifier(notifier)
	}
}

var ErrWorkspaceUnavailable = errors.New("workspace service is unavailable")

func (s *WorkspaceService) enter(ctx context.Context) (func(), error) {
	if s == nil || s.runtime == nil || s.state == nil || !s.state.Ready() {
		return nil, ErrWorkspaceUnavailable
	}
	release, err := s.runtime.Enter(ctx)
	if err != nil {
		return nil, err
	}
	return release, nil
}

func (s *WorkspaceService) GetWorkspaceCatalog(ctx context.Context) (WorkspaceCatalogResult, error) {
	release, err := s.enter(ctx)
	if err != nil {
		return WorkspaceCatalogResult{}, err
	}
	defer release()
	catalog, err := s.state.CatalogSnapshot()
	if err != nil {
		return WorkspaceCatalogResult{}, err
	}
	return catalogResult(catalog), nil
}

func (s *WorkspaceService) CreateWorkspace(ctx context.Context, args CreateWorkspaceArgs) (WorkspaceMutationResult, error) {
	release, err := s.enter(ctx)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	defer release()
	var raw []byte
	if args.Document != nil {
		raw, err = json.Marshal(args.Document)
		if err != nil {
			return WorkspaceMutationResult{}, err
		}
	}
	catalog, document, err := s.state.CreateWorkspace(args.WorkspaceID, args.Name, raw, args.ExpectedCatalogRevision)
	if err != nil {
		if internal := businessError(err); internal != nil {
			return WorkspaceMutationResult{}, internal
		}
		return blockedMutation(args.WorkspaceID, s.catalogSnapshot(catalog), uistate.DocumentSnapshot{}, err), nil
	}
	if s.runtime != nil {
		_ = s.runtime.RegisterWorkspace(args.WorkspaceID)
	}
	return mutationResult(args.WorkspaceID, catalog, document), nil
}

func (s *WorkspaceService) RenameWorkspace(ctx context.Context, args RenameWorkspaceArgs) (WorkspaceMutationResult, error) {
	release, err := s.enter(ctx)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	defer release()
	catalog, err := s.state.RenameWorkspace(args.WorkspaceID, args.Name, args.ExpectedCatalogRevision)
	if err != nil {
		if internal := businessError(err); internal != nil {
			return WorkspaceMutationResult{}, internal
		}
		return blockedMutation(args.WorkspaceID, s.catalogSnapshot(catalog), uistate.DocumentSnapshot{}, err), nil
	}
	return mutationResult(args.WorkspaceID, catalog, uistate.DocumentSnapshot{}), nil
}

func (s *WorkspaceService) DeleteWorkspace(ctx context.Context, args DeleteWorkspaceArgs) (WorkspaceMutationResult, error) {
	release, err := s.enter(ctx)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	defer release()
	catalog, err := s.state.DeleteWorkspace(args.WorkspaceID, args.ExpectedCatalogRevision)
	if err != nil {
		if internal := businessError(err); internal != nil {
			return WorkspaceMutationResult{}, internal
		}
		return blockedMutation(args.WorkspaceID, s.catalogSnapshot(catalog), uistate.DocumentSnapshot{}, err), nil
	}
	if s.runtime != nil {
		s.runtime.UnregisterWorkspace(args.WorkspaceID)
	}
	return mutationResult(args.WorkspaceID, catalog, uistate.DocumentSnapshot{}), nil
}

func (s *WorkspaceService) LoadWorkspace(ctx context.Context, args WorkspaceIDArgs) (WorkspaceDocumentResult, error) {
	release, err := s.enter(ctx)
	if err != nil {
		return WorkspaceDocumentResult{}, err
	}
	defer release()
	snapshot, err := s.state.LoadDocument(args.WorkspaceID)
	if err != nil {
		if internal := businessError(err); internal != nil {
			return WorkspaceDocumentResult{}, internal
		}
		return blockedDocument(args.WorkspaceID, snapshot, err), nil
	}
	if !snapshot.Exists {
		result := documentResult(args.WorkspaceID, snapshot, nil)
		result.Status = WorkspaceBlocked
		result.Reason = "workspace document is missing"
		return result, nil
	}
	document, err := decodeWorkspaceDocument(snapshot.Document)
	if err != nil {
		result := documentResult(args.WorkspaceID, snapshot, nil)
		result.Status = WorkspaceBlocked
		result.Reason = "workspace document is invalid"
		return result, nil
	}
	return documentResult(args.WorkspaceID, snapshot, document), nil
}

func (s *WorkspaceService) SaveWorkspace(ctx context.Context, args SaveWorkspaceArgs) (WorkspaceDocumentResult, error) {
	release, err := s.enter(ctx)
	if err != nil {
		return WorkspaceDocumentResult{}, err
	}
	defer release()
	raw, err := json.Marshal(args.Document)
	if err != nil {
		return WorkspaceDocumentResult{}, err
	}
	snapshot, err := s.state.SaveDocument(args.WorkspaceID, raw, args.ExpectedRevision)
	if err != nil {
		if internal := businessError(err); internal != nil {
			return WorkspaceDocumentResult{}, internal
		}
		return blockedDocument(args.WorkspaceID, snapshot, err), nil
	}
	document, err := decodeWorkspaceDocument(snapshot.Document)
	if err != nil {
		return WorkspaceDocumentResult{}, err
	}
	return documentResult(args.WorkspaceID, snapshot, document), nil
}

func (s *WorkspaceService) FlushWorkspace(ctx context.Context) (WorkspaceFlushResult, error) {
	release, err := s.enter(ctx)
	if err != nil {
		return WorkspaceFlushResult{}, err
	}
	defer release()
	if err := s.state.Flush(); err != nil {
		return WorkspaceFlushResult{}, err
	}
	return WorkspaceFlushResult{Status: WorkspaceAccepted}, nil
}

func (s *WorkspaceService) OpenWorkspace(ctx context.Context, args WorkspaceIDArgs) (WorkspaceMutationResult, error) {
	release, err := s.enter(ctx)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	defer release()
	known, err := s.state.KnownWorkspace(args.WorkspaceID)
	if err != nil {
		if internal := businessError(err); internal != nil {
			return WorkspaceMutationResult{}, internal
		}
		return blockedMutation(args.WorkspaceID, uistate.CatalogSnapshot{}, uistate.DocumentSnapshot{}, err), nil
	}
	if !known {
		return blockedMutation(args.WorkspaceID, uistate.CatalogSnapshot{}, uistate.DocumentSnapshot{}, uistate.ErrUnknownWorkspace), nil
	}
	if s.window == nil || wailsruntime.ServerMode {
		if _, err := s.state.OpenWorkspace(args.WorkspaceID, nil); err != nil {
			if internal := businessError(err); internal != nil {
				return WorkspaceMutationResult{}, internal
			}
			return blockedMutation(args.WorkspaceID, uistate.CatalogSnapshot{}, uistate.DocumentSnapshot{}, err), nil
		}
	} else if err := s.window.OpenWorkspace(args.WorkspaceID); err != nil {
		if internal := businessError(err); internal != nil {
			return WorkspaceMutationResult{}, internal
		}
		return blockedMutation(args.WorkspaceID, uistate.CatalogSnapshot{}, uistate.DocumentSnapshot{}, err), nil
	}
	return s.windowResult(args.WorkspaceID, WorkspaceAccepted, ""), nil
}

func (s *WorkspaceService) FocusWorkspace(ctx context.Context, args WorkspaceIDArgs) (WorkspaceMutationResult, error) {
	release, err := s.enter(ctx)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	defer release()
	if s.window == nil || wailsruntime.ServerMode {
		err = s.state.FocusWorkspace(args.WorkspaceID)
	} else {
		err = s.window.FocusWorkspace(args.WorkspaceID)
	}
	if err != nil {
		if internal := businessError(err); internal != nil {
			return WorkspaceMutationResult{}, internal
		}
		return blockedMutation(args.WorkspaceID, uistate.CatalogSnapshot{}, uistate.DocumentSnapshot{}, err), nil
	}
	return s.windowResult(args.WorkspaceID, WorkspaceAccepted, ""), nil
}

func (s *WorkspaceService) CloseWorkspace(ctx context.Context, args WorkspaceIDArgs) (WorkspaceMutationResult, error) {
	release, err := s.enter(ctx)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	defer release()
	known, err := s.state.KnownWorkspace(args.WorkspaceID)
	if err != nil {
		if internal := businessError(err); internal != nil {
			return WorkspaceMutationResult{}, internal
		}
		return blockedMutation(args.WorkspaceID, uistate.CatalogSnapshot{}, uistate.DocumentSnapshot{}, err), nil
	}
	if !known {
		return blockedMutation(args.WorkspaceID, uistate.CatalogSnapshot{}, uistate.DocumentSnapshot{}, uistate.ErrUnknownWorkspace), nil
	}
	if s.window == nil || wailsruntime.ServerMode {
		s.state.CloseWorkspace(args.WorkspaceID)
	} else if err := s.window.CloseWorkspace(args.WorkspaceID); err != nil {
		if internal := businessError(err); internal != nil {
			return WorkspaceMutationResult{}, internal
		}
		return blockedMutation(args.WorkspaceID, uistate.CatalogSnapshot{}, uistate.DocumentSnapshot{}, err), nil
	}
	return s.windowResult(args.WorkspaceID, WorkspaceAccepted, ""), nil
}

// CompleteWorkspaceClose releases the native WindowClosing hook only after
// the renderer has serialized the live Dockview document and flushed storage.
func (s *WorkspaceService) CompleteWorkspaceClose(ctx context.Context, args WorkspaceCloseArgs) (WorkspaceMutationResult, error) {
	release, err := s.enter(ctx)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	defer release()
	known, err := s.state.KnownWorkspace(args.WorkspaceID)
	if err != nil {
		if internal := businessError(err); internal != nil {
			return WorkspaceMutationResult{}, internal
		}
		return blockedMutation(args.WorkspaceID, uistate.CatalogSnapshot{}, uistate.DocumentSnapshot{}, err), nil
	}
	if !known {
		return blockedMutation(args.WorkspaceID, uistate.CatalogSnapshot{}, uistate.DocumentSnapshot{}, uistate.ErrUnknownWorkspace), nil
	}
	if s.window == nil || wailsruntime.ServerMode {
		s.state.CloseWorkspace(args.WorkspaceID)
		return s.windowResult(args.WorkspaceID, WorkspaceAccepted, ""), nil
	}
	if err := s.window.CompleteWorkspaceClose(args.WorkspaceID, args.RequestID); err != nil {
		return blockedMutation(args.WorkspaceID, s.catalogSnapshot(uistate.CatalogSnapshot{}), uistate.DocumentSnapshot{}, err), nil
	}
	return s.windowResult(args.WorkspaceID, WorkspaceAccepted, ""), nil
}

func (s *WorkspaceService) windowResult(id string, status WorkspaceStatus, reason string) WorkspaceMutationResult {
	catalog, _ := s.state.CatalogSnapshot()
	result := mutationResult(id, catalog, uistate.DocumentSnapshot{})
	result.Revision = catalog.Revision
	result.Status, result.Reason = status, reason
	return result
}

func (s *WorkspaceService) catalogSnapshot(fallback uistate.CatalogSnapshot) uistate.CatalogSnapshot {
	if fallback.Revision != 0 || len(fallback.Entries) != 0 {
		return fallback
	}
	current, err := s.state.CatalogSnapshot()
	if err == nil {
		return current
	}
	return fallback
}

func catalogResult(catalog uistate.CatalogSnapshot) WorkspaceCatalogResult {
	entries := make([]WorkspaceCatalogEntry, 0, len(catalog.Entries)+1)
	entries = append(entries, WorkspaceCatalogEntry{WorkspaceID: uistate.MonitoringWorkspaceID, Name: uistate.MonitoringWorkspaceName, Open: containsWorkspace(catalog.OpenIDs, uistate.MonitoringWorkspaceID)})
	for _, entry := range catalog.Entries {
		entries = append(entries, WorkspaceCatalogEntry{WorkspaceID: entry.ID, Name: entry.Name, Open: entry.Open})
	}
	return WorkspaceCatalogResult{Status: WorkspaceAccepted, Revision: catalog.Revision, Entries: entries, OpenWorkspaceIDs: catalog.OpenIDs}
}

func containsWorkspace(ids []string, wanted string) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

func mutationResult(id string, catalog uistate.CatalogSnapshot, document uistate.DocumentSnapshot) WorkspaceMutationResult {
	result := WorkspaceMutationResult{Status: WorkspaceAccepted, WorkspaceID: id, Revision: document.Revision, CatalogRevision: catalog.Revision, OpenWorkspaceIDs: catalog.OpenIDs}
	result.Entries = catalogResult(catalog).Entries
	return result
}

func documentResult(id string, snapshot uistate.DocumentSnapshot, document *WorkspaceDocument) WorkspaceDocumentResult {
	result := WorkspaceDocumentResult{Status: WorkspaceAccepted, WorkspaceID: id, Revision: snapshot.Revision, Document: document}
	return result
}

func blockedMutation(id string, catalog uistate.CatalogSnapshot, document uistate.DocumentSnapshot, err error) WorkspaceMutationResult {
	result := mutationResult(id, catalog, document)
	result.Status = WorkspaceBlocked
	result.Reason = err.Error()
	return result
}

func blockedDocument(id string, snapshot uistate.DocumentSnapshot, err error) WorkspaceDocumentResult {
	result := documentResult(id, snapshot, nil)
	result.Status = WorkspaceBlocked
	result.Reason = err.Error()
	return result
}

func decodeWorkspaceDocument(raw []byte) (*WorkspaceDocument, error) {
	var document WorkspaceDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	return &document, nil
}

func businessError(err error) error {
	switch {
	case errors.Is(err, uistate.ErrInvalidWorkspaceID), errors.Is(err, uistate.ErrUnknownWorkspace),
		errors.Is(err, uistate.ErrWorkspaceConflict), errors.Is(err, uistate.ErrCatalogConflict),
		errors.Is(err, uistate.ErrReservedWorkspace), errors.Is(err, uistate.ErrDuplicateWorkspace),
		errors.Is(err, uistate.ErrInvalidWorkspaceName), errors.Is(err, uistate.ErrInvalidDocument),
		errors.Is(err, uistate.ErrWindowClosed):
		return nil
	default:
		return err
	}
}
