// Package venueadmin implements the settings-UI seam that reads and writes the
// two config files (config.toml venues+gate, credentials.json) behind the
// engine's WS commands. It captures the venue config the engine BOOTED with so
// the UI can show a "restart required" banner when the file drifts from it.
// Nothing here touches the running gate or arm state.
package venueadmin

import (
	"fmt"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/creds"
)

type Admin struct {
	cfgPath   string
	credsPath string
	booted    config.VenueConfig
}

func New(cfgPath, credsPath string, booted config.VenueConfig) *Admin {
	return &Admin{cfgPath: cfgPath, credsPath: credsPath, booted: booted}
}

// GetVenueSetup returns the file state (parsed fresh), the running state (what
// the engine booted with), and the credential key NAMES. A missing/unreadable
// credentials file yields no keys, not an error.
func (a *Admin) GetVenueSetup() (file, running config.VenueConfig, credKeys []string, err error) {
	file, err = config.ReadVenueConfig(a.cfgPath)
	if err != nil {
		return config.VenueConfig{}, config.VenueConfig{}, nil, err
	}
	keys, kerr := creds.Keys(a.credsPath)
	if kerr != nil {
		keys = nil // credentials are optional for read; never fail the setup fetch
	}
	return file, a.booted, keys, nil
}

// SetVenueSetup validates against the current credential keys, then rewrites
// config.toml. Nothing is written on any validation failure.
func (a *Admin) SetVenueSetup(vc config.VenueConfig) error {
	keys, _ := creds.Keys(a.credsPath)
	if err := config.ValidateVenueConfig(vc, keys); err != nil {
		return err
	}
	return config.WriteVenueConfig(a.cfgPath, vc)
}

func (a *Admin) PutCredential(name, keyID, secretKey string) error {
	return creds.Put(a.credsPath, name, keyID, secretKey)
}

// DeleteCredential refuses while any venue in the current FILE config
// references the name.
func (a *Admin) DeleteCredential(name string) error {
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
