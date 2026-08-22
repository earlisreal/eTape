// Package venueadmin implements the settings-UI seam that reads and writes the
// two config files (config.toml venues+gate, credentials.json) behind the
// engine's WS commands. It captures the venue config the engine BOOTED with so
// the UI can show a "restart required" banner when the file drifts from it.
// Nothing here touches the running gate or arm state.
package venueadmin

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
)

// mu serializes every read-modify-write against the two config files
// (config.toml, credentials.json) so a second writer — the boot-time moomoo
// auto-seeder (venueseed) — can never race a settings-UI Save/PutCredential
// and tear the file. An in-process mutex is sufficient: singleinstance.Acquire
// already guarantees one live engine per store.
type Admin struct {
	mu        sync.Mutex
	cfgPath   string
	credsPath string
	booted    config.VenueConfig
	revision  uint64
}

type ValidationError struct{ Err error }

func (e ValidationError) Error() string       { return e.Err.Error() }
func (e ValidationError) Unwrap() error       { return e.Err }
func (e ValidationError) BusinessError() bool { return true }

type CredentialInUseError struct{ Err error }

func (e CredentialInUseError) Error() string       { return e.Err.Error() }
func (e CredentialInUseError) Unwrap() error       { return e.Err }
func (e CredentialInUseError) BusinessError() bool { return true }

func New(cfgPath, credsPath string, booted config.VenueConfig) *Admin {
	return &Admin{cfgPath: cfgPath, credsPath: credsPath, booted: booted}
}

func (a *Admin) Revision() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.revision
}

// GetVenueSetup returns the file state (parsed fresh), the running state (what
// the engine booted with), the credential key NAMES, and the file's [seed]
// moomoo-auto-config marker. moomooAttempted comes from the SAME fresh config
// read as file (config.Load once, not a second re-read) so the two stay a
// consistent snapshot. A missing/unreadable credentials file yields no keys,
// not an error.
func (a *Admin) GetVenueSetup() (file, running config.VenueConfig, credKeys []string, moomooAttempted bool, err error) {
	file, running, credKeys, moomooAttempted, _, err = a.GetVenueSetupSnapshot()
	return
}

// GetVenueSetupSnapshot returns the setup state and its exact revision from
// one lock acquisition so a typed binding cannot label an older file snapshot
// with a newer concurrent mutation.
func (a *Admin) GetVenueSetupSnapshot() (file, running config.VenueConfig, credKeys []string, moomooAttempted bool, revision uint64, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		return config.VenueConfig{}, config.VenueConfig{}, nil, false, a.revision, err
	}
	file = config.VenueConfig{Venues: cfg.Venues, Gate: cfg.Gate}
	keys, kerr := creds.Keys(a.credsPath)
	if kerr != nil {
		keys = nil // credentials are optional for read; never fail the setup fetch
	}
	return file, a.booted, keys, cfg.Seed.MoomooAttempted, a.revision, nil
}

// SetVenueSetup validates against the current credential keys, then rewrites
// config.toml. Nothing is written on any validation failure.
func (a *Admin) SetVenueSetup(vc config.VenueConfig) error {
	_, err := a.SetVenueSetupWithRevision(vc)
	return err
}

func (a *Admin) SetVenueSetupWithRevision(vc config.VenueConfig) (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	keys, _ := creds.Keys(a.credsPath)
	if err := config.ValidateVenueConfig(vc, keys); err != nil {
		return a.revision, ValidationError{Err: err}
	}
	if err := config.WriteVenueConfig(a.cfgPath, vc); err != nil {
		return a.revision, err
	}
	a.revision++
	return a.revision, nil
}

func (a *Admin) PutCredential(name, keyID, secretKey string) error {
	_, err := a.PutCredentialWithRevision(name, keyID, secretKey)
	return err
}

func (a *Admin) PutCredentialWithRevision(name, keyID, secretKey string) (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := creds.Put(a.credsPath, name, keyID, secretKey); err != nil {
		return a.revision, err
	}
	a.revision++
	return a.revision, nil
}

// DeleteCredential refuses while any venue in the current FILE config
// references the name.
func (a *Admin) DeleteCredential(name string) error {
	_, err := a.DeleteCredentialWithRevision(name)
	return err
}

func (a *Admin) DeleteCredentialWithRevision(name string) (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	file, err := config.ReadVenueConfig(a.cfgPath)
	if err != nil {
		return a.revision, err
	}
	for _, v := range file.Venues {
		if v.Credentials == name {
			return a.revision, CredentialInUseError{Err: fmt.Errorf("credential %q is in use by venue %q", name, v.ID)}
		}
	}
	if err := creds.Delete(a.credsPath, name); err != nil {
		return a.revision, err
	}
	a.revision++
	return a.revision, nil
}

// moomooSeedStateLocked re-reads the file fresh and reports the auto-config
// state. Callers must already hold mu.
func (a *Admin) moomooSeedStateLocked() (attempted, venueExists bool, err error) {
	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		return false, false, err
	}
	for _, v := range cfg.Venues {
		if v.Broker == "moomoo" {
			venueExists = true
			break
		}
	}
	return cfg.Seed.MoomooAttempted, venueExists, nil
}

// MoomooSeedState reports the file's auto-config state: whether the one-shot
// marker is set and whether any broker=="moomoo" venue exists (any id).
func (a *Admin) MoomooSeedState() (attempted, venueExists bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.moomooSeedStateLocked()
}

// MarkMoomooSeedAttempted sets the one-shot marker without touching venues
// (multi/zero-account outcomes, or a pre-existing hand-added moomoo venue —
// so that venue's later removal also sticks). Idempotent: a no-op write is
// skipped when the marker is already set.
func (a *Admin) MarkMoomooSeedAttempted() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	attempted, _, err := a.moomooSeedStateLocked()
	if err != nil {
		return err
	}
	if attempted {
		return nil
	}
	if err := config.WriteMoomooSeed(a.cfgPath, nil); err != nil {
		return err
	}
	a.revision++
	return nil
}

// SeedMoomooVenue appends {ID: "moomoo", Broker: "moomoo", Env: "live",
// AccountID: <accID>} plus the marker in one atomic write. It re-checks the
// file under the lock (the caller's earlier check may have raced a user
// save): marker already set → (false, nil); a moomoo venue already exists →
// marks attempted (if unset) and returns (false, nil); the resulting config
// failing ValidateVenueConfig (e.g. a non-moomoo venue already holds id
// "moomoo") → (false, err) with NO write and NO marker.
func (a *Admin) SeedMoomooVenue(accID uint64) (created bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	attempted, venueExists, err := a.moomooSeedStateLocked()
	if err != nil {
		return false, err
	}
	if attempted {
		return false, nil
	}
	if venueExists {
		// Marker is unset (checked above) — always write it here.
		if err := config.WriteMoomooSeed(a.cfgPath, nil); err != nil {
			return false, err
		}
		a.revision++
		return false, nil
	}

	file, err := config.ReadVenueConfig(a.cfgPath)
	if err != nil {
		return false, err
	}
	v := config.Venue{
		ID:        "moomoo",
		Broker:    "moomoo",
		Env:       "live",
		AccountID: strconv.FormatUint(accID, 10),
	}
	newVenues := make([]config.Venue, 0, len(file.Venues)+1)
	newVenues = append(newVenues, file.Venues...)
	newVenues = append(newVenues, v)
	newVC := config.VenueConfig{Venues: newVenues, Gate: file.Gate}

	keys, _ := creds.Keys(a.credsPath)
	if err := config.ValidateVenueConfig(newVC, keys); err != nil {
		return false, ValidationError{Err: err}
	}
	if err := config.WriteMoomooSeed(a.cfgPath, &v); err != nil {
		return false, err
	}
	a.revision++
	return true, nil
}
