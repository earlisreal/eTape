//go:build wails

package uiapi

import (
	"context"
	"sync"

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
}

func NewWorkspaceService(runtime *wailsruntime.Runtime) *WorkspaceService {
	return &WorkspaceService{runtime: runtime}
}

func (s *WorkspaceService) ServiceName() string { return "WorkspaceService" }
