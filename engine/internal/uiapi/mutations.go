package uiapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/scan"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
	"github.com/earlisreal/eTape/engine/internal/venueprobe"
	"github.com/earlisreal/eTape/engine/internal/watchlist"
)

var ErrMutationsUnavailable = errors.New("ui mutation service unavailable")

type ConfigSource interface {
	SetConfig(key, value string)
}

type ScannerSource interface {
	FilterSnapshot() (wsmsg.ScannerFilters, uint64)
	SetFiltersWithRevision(wsmsg.ScannerFilters) (uint64, error)
}

type WatchlistSource interface {
	AddWithRevision(string) (bool, []string, uint64, error)
	RemoveWithRevision(string) (bool, []string, uint64)
	Snapshot() ([]string, uint64)
	Poke()
}

type VenueSource interface {
	GetVenueSetupSnapshot() (file, running config.VenueConfig, credKeys []string, moomooAttempted bool, revision uint64, err error)
	SetVenueSetupWithRevision(config.VenueConfig) (uint64, error)
	PutCredentialWithRevision(name, keyID, secretKey string) (uint64, error)
	DeleteCredentialWithRevision(name string) (uint64, error)
}

type ConnectionSource interface {
	TestConnection(ctx context.Context, broker, env, credName, keyID, secretKey, accountID string) (venueprobe.Result, error)
}

type SymbolValidator interface {
	ValidateSymbol(context.Context, string) error
}

type MutationSources struct {
	Config    ConfigSource
	Scanner   ScannerSource
	Watchlist WatchlistSource
	Venues    VenueSource
	Connect   ConnectionSource
	Symbols   SymbolValidator
}

type Mutations struct {
	sources MutationSources
}

func NewMutations(sources MutationSources) *Mutations {
	return &Mutations{sources: sources}
}

func (m *Mutations) GetScannerFilters(context.Context) (ScannerFiltersView, error) {
	if m == nil || m.sources.Scanner == nil {
		return ScannerFiltersView{}, ErrMutationsUnavailable
	}
	filters, revision := m.sources.Scanner.FilterSnapshot()
	return ScannerFiltersView{
		Filters:  scannerFiltersFromWire(filters),
		Revision: revision,
	}, nil
}

func (m *Mutations) SetScannerFilters(_ context.Context, args SetScannerFiltersArgs) (ScannerFiltersMutationResult, error) {
	filters := scannerFiltersToWire(args.Filters)
	if err := scan.ValidateFilters(filters); err != nil {
		return ScannerFiltersMutationResult{Status: MutationBlocked, Reason: err.Error(), Filters: args.Filters}, nil
	}
	if m == nil || m.sources.Scanner == nil {
		return ScannerFiltersMutationResult{}, ErrMutationsUnavailable
	}
	if m.sources.Config == nil {
		return ScannerFiltersMutationResult{}, ErrMutationsUnavailable
	}
	revision, err := m.sources.Scanner.SetFiltersWithRevision(filters)
	if err != nil {
		if isBusinessError(err) {
			return ScannerFiltersMutationResult{Status: MutationBlocked, Reason: err.Error(), Filters: args.Filters, Revision: revision}, nil
		}
		return ScannerFiltersMutationResult{}, err
	}
	raw, err := json.Marshal(filters)
	if err != nil {
		return ScannerFiltersMutationResult{}, err
	}
	m.sources.Config.SetConfig("scanner.filters.v1", string(raw))
	return ScannerFiltersMutationResult{
		Status:   MutationAccepted,
		Filters:  args.Filters,
		Revision: revision,
	}, nil
}

func (m *Mutations) WatchlistAdd(ctx context.Context, args WatchlistMutationArgs) (WatchlistMutationResult, error) {
	symbol, reason, err := m.validateWatchlistSymbol(ctx, args.Symbol)
	if err != nil {
		return WatchlistMutationResult{}, err
	}
	if reason != "" {
		return WatchlistMutationResult{Status: MutationBlocked, Reason: reason}, nil
	}
	if m == nil || m.sources.Watchlist == nil {
		return WatchlistMutationResult{}, ErrMutationsUnavailable
	}
	_, symbols, revision, err := m.sources.Watchlist.AddWithRevision(symbol)
	if err != nil {
		if errors.Is(err, watchlist.ErrFull) {
			return WatchlistMutationResult{Status: MutationBlocked, Reason: "watchlist full (400)", Revision: revision}, nil
		}
		return WatchlistMutationResult{}, err
	}
	m.sources.Watchlist.Poke()
	return watchlistAccepted(symbols, revision), nil
}

func (m *Mutations) WatchlistRemove(ctx context.Context, args WatchlistMutationArgs) (WatchlistMutationResult, error) {
	symbol, reason := normalizeWatchlistSymbol(args.Symbol)
	if reason != "" {
		return WatchlistMutationResult{Status: MutationBlocked, Reason: reason}, nil
	}
	if m == nil || m.sources.Watchlist == nil {
		return WatchlistMutationResult{}, ErrMutationsUnavailable
	}
	_, symbols, revision := m.sources.Watchlist.RemoveWithRevision(symbol)
	m.sources.Watchlist.Poke()
	return watchlistAccepted(symbols, revision), nil
}

func (m *Mutations) GetVenueSetup(context.Context) (VenueSetup, error) {
	if m == nil || m.sources.Venues == nil {
		return VenueSetup{}, ErrMutationsUnavailable
	}
	file, running, keys, attempted, revision, err := m.sources.Venues.GetVenueSetupSnapshot()
	if err != nil {
		return VenueSetup{}, err
	}
	return VenueSetup{
		File:     venueConfigFromConfig(file),
		Running:  venueConfigFromConfig(running),
		CredKeys: append([]string(nil), keys...),
		Seed:     SeedView{MoomooAttempted: attempted},
		Revision: revision,
	}, nil
}

func (m *Mutations) SetVenueSetup(_ context.Context, args SetVenueSetupArgs) (MutationResult, error) {
	if m == nil || m.sources.Venues == nil {
		return MutationResult{}, ErrMutationsUnavailable
	}
	revision, err := m.sources.Venues.SetVenueSetupWithRevision(venueConfigToConfig(args.Venues, args.Gate))
	if err != nil {
		if isBusinessError(err) {
			return MutationResult{Status: MutationBlocked, Reason: err.Error(), Revision: revision}, nil
		}
		return MutationResult{}, err
	}
	return MutationResult{Status: MutationAccepted, Revision: revision}, nil
}

func (m *Mutations) PutCredential(_ context.Context, args PutCredentialArgs) (MutationResult, error) {
	if strings.TrimSpace(args.Name) == "" || args.KeyID == "" || args.SecretKey == "" {
		return MutationResult{Status: MutationBlocked, Reason: "name, keyId, and secretKey are required"}, nil
	}
	if m == nil || m.sources.Venues == nil {
		return MutationResult{}, ErrMutationsUnavailable
	}
	revision, err := m.sources.Venues.PutCredentialWithRevision(args.Name, args.KeyID, args.SecretKey)
	if err != nil {
		if isBusinessError(err) {
			return MutationResult{Status: MutationBlocked, Reason: err.Error(), Revision: revision}, nil
		}
		return MutationResult{}, err
	}
	return MutationResult{Status: MutationAccepted, Revision: revision}, nil
}

func (m *Mutations) DeleteCredential(_ context.Context, args DeleteCredentialArgs) (MutationResult, error) {
	if strings.TrimSpace(args.Name) == "" {
		return MutationResult{Status: MutationBlocked, Reason: "name is required"}, nil
	}
	if m == nil || m.sources.Venues == nil {
		return MutationResult{}, ErrMutationsUnavailable
	}
	revision, err := m.sources.Venues.DeleteCredentialWithRevision(args.Name)
	if err != nil {
		if isBusinessError(err) {
			return MutationResult{Status: MutationBlocked, Reason: err.Error(), Revision: revision}, nil
		}
		return MutationResult{}, err
	}
	return MutationResult{Status: MutationAccepted, Revision: revision}, nil
}

func (m *Mutations) TestConnection(ctx context.Context, args TestConnectionArgs) (TestConnectionResult, error) {
	if strings.TrimSpace(args.Broker) == "" {
		return TestConnectionResult{Status: MutationBlocked, Reason: "broker is required", Message: "broker is required"}, nil
	}
	if m == nil || m.sources.Connect == nil {
		return TestConnectionResult{}, ErrMutationsUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	r, err := m.sources.Connect.TestConnection(probeCtx, args.Broker, args.Env, args.Credentials, args.KeyID, args.SecretKey, args.AccountID)
	if err != nil {
		return TestConnectionResult{}, err
	}
	accounts := make([]TestAccount, 0, len(r.Accounts))
	for _, account := range r.Accounts {
		accounts = append(accounts, TestAccount{AccountID: account.AccountID, AccountType: account.AccountType, Env: account.Env})
	}
	return TestConnectionResult{
		Status: MutationAccepted, OK: r.OK, Env: r.Env, AccountID: r.AccountID, AccountType: r.AccountType,
		Message: r.Message, Accounts: accounts,
	}, nil
}

func (m *Mutations) validateWatchlistSymbol(ctx context.Context, raw string) (string, string, error) {
	symbol, reason := normalizeWatchlistSymbol(raw)
	if reason != "" {
		return "", reason, nil
	}
	if m != nil && m.sources.Symbols != nil {
		if err := m.sources.Symbols.ValidateSymbol(ctx, symbol); err != nil {
			if isBusinessError(err) {
				return "", err.Error(), nil
			}
			return "", "", err
		}
	}
	return symbol, "", nil
}

func normalizeWatchlistSymbol(raw string) (string, string) {
	symbol := watchlist.Normalize(raw)
	if symbol == "" {
		return "", "symbol is required"
	}
	if !strings.HasPrefix(symbol, "US.") {
		return "", "watchlist accepts US symbols only"
	}
	return symbol, ""
}

func watchlistAccepted(symbols []string, revision uint64) WatchlistMutationResult {
	return WatchlistMutationResult{
		Status:   MutationAccepted,
		Symbols:  append([]string(nil), symbols...),
		Revision: revision,
	}
}

func isBusinessError(err error) bool {
	var business interface{ BusinessError() bool }
	return errors.As(err, &business) && business.BusinessError()
}

func scannerFiltersToWire(f ScannerFilters) wsmsg.ScannerFilters {
	return wsmsg.ScannerFilters{Mode: f.Mode, MinChangePct: f.MinChangePct, MaxFloatShares: f.MaxFloatShares, MinVolume: f.MinVolume, MinVolumeRatio: f.MinVolumeRatio, FloatUnit: f.FloatUnit, VolumeUnit: f.VolumeUnit}
}

func scannerFiltersFromWire(f wsmsg.ScannerFilters) ScannerFilters {
	return ScannerFilters{Mode: f.Mode, MinChangePct: f.MinChangePct, MaxFloatShares: f.MaxFloatShares, MinVolume: f.MinVolume, MinVolumeRatio: f.MinVolumeRatio, FloatUnit: f.FloatUnit, VolumeUnit: f.VolumeUnit}
}

func venueConfigFromConfig(vc config.VenueConfig) VenueConfig {
	venues := make([]Venue, 0, len(vc.Venues))
	for _, v := range vc.Venues {
		venues = append(venues, Venue{ID: v.ID, Broker: v.Broker, Env: v.Env, Credentials: v.Credentials, AccountID: v.AccountID, StartingBalance: v.StartingBalance, SlippageBps: v.SlippageBps, FillLatencyMs: v.FillLatencyMs})
	}
	venueLimits := make(map[string]GateLimitsView, len(vc.Gate.Venue))
	for id, v := range vc.Gate.Venue {
		venueLimits[id] = GateLimitsView{MaxOrderValue: v.MaxOrderValue, MaxPositionValue: v.MaxPositionValue, MaxPositionShares: v.MaxPositionShares, MaxOpenOrders: v.MaxOpenOrders}
	}
	return VenueConfig{
		Venues: venues,
		Gate: Gate{
			Global: GlobalLimitsView{MaxDayLoss: vc.Gate.Global.MaxDayLoss, MaxSymbolPositionValue: vc.Gate.Global.MaxSymbolPositionValue, MaxSymbolPositionShares: vc.Gate.Global.MaxSymbolPositionShares},
			Venue:  venueLimits,
		},
	}
}

func venueConfigToConfig(venues []Venue, gate Gate) config.VenueConfig {
	configVenues := make([]config.Venue, 0, len(venues))
	for _, v := range venues {
		configVenues = append(configVenues, config.Venue{ID: v.ID, Broker: v.Broker, Env: v.Env, Credentials: v.Credentials, AccountID: v.AccountID, StartingBalance: v.StartingBalance, SlippageBps: v.SlippageBps, FillLatencyMs: v.FillLatencyMs})
	}
	venueLimits := make(map[string]config.GateVenue, len(gate.Venue))
	for id, v := range gate.Venue {
		venueLimits[id] = config.GateVenue{MaxOrderValue: v.MaxOrderValue, MaxPositionValue: v.MaxPositionValue, MaxPositionShares: v.MaxPositionShares, MaxOpenOrders: v.MaxOpenOrders}
	}
	return config.VenueConfig{
		Venues: configVenues,
		Gate: config.Gate{
			Global: config.GateGlobal{MaxDayLoss: gate.Global.MaxDayLoss, MaxSymbolPositionValue: gate.Global.MaxSymbolPositionValue, MaxSymbolPositionShares: gate.Global.MaxSymbolPositionShares},
			Venue:  venueLimits,
		},
	}
}
