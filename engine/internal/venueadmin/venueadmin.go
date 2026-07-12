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
}

func New(cfgPath, credsPath string, booted config.VenueConfig) *Admin {
	return &Admin{cfgPath: cfgPath, credsPath: credsPath, booted: booted}
}

// GetVenueSetup returns the file state (parsed fresh), the running state (what
// the engine booted with), the credential key NAMES, and the file's [seed]
// moomoo-auto-config marker. moomooAttempted comes from the SAME fresh config
// read as file (config.Load once, not a second re-read) so the two stay a
// consistent snapshot. A missing/unreadable credentials file yields no keys,
// not an error.
func (a *Admin) GetVenueSetup() (file, running config.VenueConfig, credKeys []string, moomooAttempted bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		return config.VenueConfig{}, config.VenueConfig{}, nil, false, err
	}
	file = config.VenueConfig{Venues: cfg.Venues, Gate: cfg.Gate}
	keys, kerr := creds.Keys(a.credsPath)
	if kerr != nil {
		keys = nil // credentials are optional for read; never fail the setup fetch
	}
	return file, a.booted, keys, cfg.Seed.MoomooAttempted, nil
}

// SetVenueSetup validates against the current credential keys, then rewrites
// config.toml. Nothing is written on any validation failure.
func (a *Admin) SetVenueSetup(vc config.VenueConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	keys, _ := creds.Keys(a.credsPath)
	if err := config.ValidateVenueConfig(vc, keys); err != nil {
		return err
	}
	return config.WriteVenueConfig(a.cfgPath, vc)
}

func (a *Admin) PutCredential(name, keyID, secretKey string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return creds.Put(a.credsPath, name, keyID, secretKey)
}

// DeleteCredential refuses while any venue in the current FILE config
// references the name.
func (a *Admin) DeleteCredential(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	file, err := config.ReadVenueConfig(a.cfgPath)
	if err != nil {
		return err
	}
	for _, v := range file.Venues {
		if v.Credentials == name {
			return fmt.Errorf("credential %q is in use by venue %q", name, v.ID)
		}
	}
	return creds.Delete(a.credsPath, name)
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
	return config.WriteMoomooSeed(a.cfgPath, nil)
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
		return false, config.WriteMoomooSeed(a.cfgPath, nil)
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
		return false, err
	}
	if err := config.WriteMoomooSeed(a.cfgPath, &v); err != nil {
		return false, err
	}
	return true, nil
}
