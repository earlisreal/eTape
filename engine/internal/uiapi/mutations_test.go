package uiapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
	"github.com/earlisreal/eTape/engine/internal/venueprobe"
	"github.com/earlisreal/eTape/engine/internal/watchlist"
)

type mutationConfig struct{ key, value string }

func (c *mutationConfig) SetConfig(key, value string) { c.key, c.value = key, value }

type mutationScanner struct {
	filters wsmsg.ScannerFilters
	rev     uint64
	err     error
}

func (s *mutationScanner) Filters() wsmsg.ScannerFilters { return s.filters }
func (s *mutationScanner) SetFilters(f wsmsg.ScannerFilters) error {
	if s.err != nil {
		return s.err
	}
	s.filters, s.rev = f, s.rev+1
	return nil
}
func (s *mutationScanner) Revision() uint64                               { return s.rev }
func (s *mutationScanner) FilterSnapshot() (wsmsg.ScannerFilters, uint64) { return s.filters, s.rev }
func (s *mutationScanner) SetFiltersWithRevision(f wsmsg.ScannerFilters) (uint64, error) {
	if err := s.SetFilters(f); err != nil {
		return s.rev, err
	}
	return s.rev, nil
}

type mutationWatchlist struct {
	syms   []string
	rev    uint64
	err    error
	addErr error
}

func (w *mutationWatchlist) Add(symbol string) (bool, error) {
	if w.err != nil {
		return false, w.err
	}
	if w.addErr != nil {
		return false, w.addErr
	}
	w.syms, w.rev = append(w.syms, symbol), w.rev+1
	return true, nil
}
func (w *mutationWatchlist) AddWithRevision(symbol string) (bool, []string, uint64, error) {
	added, err := w.Add(symbol)
	return added, w.Symbols(), w.rev, err
}
func (w *mutationWatchlist) Remove(symbol string) bool {
	for i, s := range w.syms {
		if s == symbol {
			w.syms, w.rev = append(w.syms[:i], w.syms[i+1:]...), w.rev+1
			return true
		}
	}
	return false
}
func (w *mutationWatchlist) RemoveWithRevision(symbol string) (bool, []string, uint64) {
	removed := w.Remove(symbol)
	return removed, w.Symbols(), w.rev
}
func (w *mutationWatchlist) Symbols() []string            { return append([]string(nil), w.syms...) }
func (w *mutationWatchlist) Revision() uint64             { return w.rev }
func (w *mutationWatchlist) Snapshot() ([]string, uint64) { return w.Symbols(), w.rev }
func (w *mutationWatchlist) Poke()                        {}

type mutationVenue struct {
	file, running config.VenueConfig
	keys          []string
	attempted     bool
	rev           uint64
	err           error
}

func (v *mutationVenue) GetVenueSetup() (config.VenueConfig, config.VenueConfig, []string, bool, error) {
	return v.file, v.running, append([]string(nil), v.keys...), v.attempted, nil
}
func (v *mutationVenue) GetVenueSetupSnapshot() (config.VenueConfig, config.VenueConfig, []string, bool, uint64, error) {
	file, running, keys, attempted, err := v.GetVenueSetup()
	return file, running, keys, attempted, v.rev, err
}
func (v *mutationVenue) SetVenueSetup(config.VenueConfig) error     { return v.err }
func (v *mutationVenue) PutCredential(string, string, string) error { return v.err }
func (v *mutationVenue) DeleteCredential(string) error              { return v.err }
func (v *mutationVenue) Revision() uint64                           { return v.rev }
func (v *mutationVenue) SetVenueSetupWithRevision(config.VenueConfig) (uint64, error) {
	return v.rev, v.err
}
func (v *mutationVenue) PutCredentialWithRevision(string, string, string) (uint64, error) {
	return v.rev, v.err
}
func (v *mutationVenue) DeleteCredentialWithRevision(string) (uint64, error) {
	return v.rev, v.err
}

type mutationConnection struct {
	result venueprobe.Result
	err    error
}

func (c mutationConnection) TestConnection(context.Context, string, string, string, string, string, string) (venueprobe.Result, error) {
	return c.result, c.err
}

type mutationValidator struct{ err error }

func (v mutationValidator) ValidateSymbol(context.Context, string) error { return v.err }

type mutationBusinessError struct{ reason string }

func (e mutationBusinessError) Error() string       { return e.reason }
func (e mutationBusinessError) BusinessError() bool { return true }

func validScannerFilters() ScannerFilters {
	return ScannerFilters{Mode: "gainers", FloatUnit: "M", VolumeUnit: "K"}
}

func TestMutationsSetScannerFiltersPersistsRevision(t *testing.T) {
	cfg := &mutationConfig{}
	scanner := &mutationScanner{}
	m := NewMutations(MutationSources{Config: cfg, Scanner: scanner})
	result, err := m.SetScannerFilters(context.Background(), SetScannerFiltersArgs{Filters: validScannerFilters()})
	if err != nil || result.Status != MutationAccepted || result.Revision != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if cfg.key != "scanner.filters.v1" || cfg.value == "" {
		t.Fatalf("scanner config was not persisted: %+v", cfg)
	}
}

func TestMutationsWatchlistBlocksValidationAndReturnsAcceptedRevision(t *testing.T) {
	watchlist := &mutationWatchlist{}
	m := NewMutations(MutationSources{Watchlist: watchlist})
	blocked, err := m.WatchlistAdd(context.Background(), WatchlistMutationArgs{Symbol: "HK.700"})
	if err != nil || blocked.Status != MutationBlocked {
		t.Fatalf("blocked=%+v err=%v", blocked, err)
	}
	accepted, err := m.WatchlistAdd(context.Background(), WatchlistMutationArgs{Symbol: "aapl"})
	if err != nil || accepted.Status != MutationAccepted || accepted.Revision != 1 || len(accepted.Symbols) != 1 || accepted.Symbols[0] != "US.AAPL" {
		t.Fatalf("accepted=%+v err=%v", accepted, err)
	}
}

func TestMutationsWatchlistProbeErrorsPreserveBusinessAndInternalOutcomes(t *testing.T) {
	business := NewMutations(MutationSources{Watchlist: &mutationWatchlist{}, Symbols: mutationValidator{err: mutationBusinessError{reason: "unknown symbol"}}})
	blocked, err := business.WatchlistAdd(context.Background(), WatchlistMutationArgs{Symbol: "US.NOPE"})
	if err != nil || blocked.Status != MutationBlocked || blocked.Reason != "unknown symbol" {
		t.Fatalf("business probe = %+v, err=%v", blocked, err)
	}

	failure := errors.New("feed unavailable")
	internal := NewMutations(MutationSources{Watchlist: &mutationWatchlist{}, Symbols: mutationValidator{err: failure}})
	if _, err := internal.WatchlistAdd(context.Background(), WatchlistMutationArgs{Symbol: "US.AAPL"}); !errors.Is(err, failure) {
		t.Fatalf("internal probe error = %v", err)
	}
}

func TestMutationsWatchlistFullIsBlocked(t *testing.T) {
	m := NewMutations(MutationSources{Watchlist: &mutationWatchlist{addErr: watchlist.ErrFull}})
	result, err := m.WatchlistAdd(context.Background(), WatchlistMutationArgs{Symbol: "US.AAPL"})
	if err != nil || result.Status != MutationBlocked || result.Reason != "watchlist full (400)" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestMutationsVenueSetupAndConnectionAreTypedAndSecretFree(t *testing.T) {
	venue := &mutationVenue{
		file:    config.VenueConfig{Venues: []config.Venue{{ID: "sim", Broker: "sim", Credentials: "cred"}}},
		running: config.VenueConfig{}, keys: []string{"cred"}, attempted: true, rev: 3,
	}
	m := NewMutations(MutationSources{Venues: venue, Connect: mutationConnection{result: venueprobe.Result{
		OK: true, Env: "live", AccountID: "acct", AccountType: "margin", Message: "connected",
		Accounts: []venueprobe.Account{{AccountID: "acct2", AccountType: "paper", Env: "paper"}},
	}}})
	setup, err := m.GetVenueSetup(context.Background())
	if err != nil || setup.Revision != 3 || !setup.Seed.MoomooAttempted || len(setup.CredKeys) != 1 {
		t.Fatalf("setup=%+v err=%v", setup, err)
	}
	encoded, _ := json.Marshal(setup)
	if strings.Contains(string(encoded), "secretKey") || strings.Contains(string(encoded), "keyId") {
		t.Fatalf("setup leaked secret material: %s", encoded)
	}
	result, err := m.TestConnection(context.Background(), TestConnectionArgs{Broker: "alpaca"})
	if err != nil || result.Status != MutationAccepted || !result.OK || result.AccountID != "acct" || len(result.Accounts) != 1 {
		t.Fatalf("connection=%+v err=%v", result, err)
	}
}

func TestMutationsRejectsInvalidCredentialArgsAndBlocksBusinessErrors(t *testing.T) {
	venue := &mutationVenue{err: mutationBusinessError{reason: "credential in use"}, rev: 2}
	m := NewMutations(MutationSources{Venues: venue})
	invalid, err := m.PutCredential(context.Background(), PutCredentialArgs{Name: "cred", KeyID: "", SecretKey: "secret"})
	if err != nil || invalid.Status != MutationBlocked {
		t.Fatalf("invalid=%+v err=%v", invalid, err)
	}
	blocked, err := m.DeleteCredential(context.Background(), DeleteCredentialArgs{Name: "cred"})
	if err != nil || blocked.Status != MutationBlocked || blocked.Reason != "credential in use" || blocked.Revision != 2 {
		t.Fatalf("blocked=%+v err=%v", blocked, err)
	}
}

func TestMutationsInternalFailuresReject(t *testing.T) {
	failure := errors.New("disk failure")
	_, err := NewMutations(MutationSources{Config: &mutationConfig{}, Scanner: &mutationScanner{err: failure}}).SetScannerFilters(context.Background(), SetScannerFiltersArgs{Filters: validScannerFilters()})
	if !errors.Is(err, failure) {
		t.Fatalf("scanner error=%v", err)
	}
	_, err = NewMutations(MutationSources{Watchlist: &mutationWatchlist{err: failure}}).WatchlistAdd(context.Background(), WatchlistMutationArgs{Symbol: "US.AAPL"})
	if !errors.Is(err, failure) {
		t.Fatalf("watchlist error=%v", err)
	}
	_, err = NewMutations(MutationSources{Connect: mutationConnection{err: failure}}).TestConnection(context.Background(), TestConnectionArgs{Broker: "alpaca"})
	if !errors.Is(err, failure) {
		t.Fatalf("connection error=%v", err)
	}
	_, err = NewMutations(MutationSources{Venues: &mutationVenue{err: failure}}).PutCredential(context.Background(), PutCredentialArgs{Name: "cred", KeyID: "key", SecretKey: "secret"})
	if !errors.Is(err, failure) {
		t.Fatalf("venue error=%v", err)
	}
}
