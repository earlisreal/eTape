package main

import (
	"encoding/json"
	"fmt"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/scan"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

const legacyScannerDefaultsKey = "scanner.toml-defaults.v1"

type scannerMigrationStore interface {
	GetConfig(string) (string, bool, error)
	SetConfig(string, string)
	Flush()
}

// Persist old TOML defaults before any settings or seed write can drop them.
// Existing saved scanner filters remain authoritative over these defaults.
func scannerDefaultsAtBoot(st scannerMigrationStore, cfg config.Scan, demo bool) (wsmsg.ScannerFilters, *config.Scan, error) {
	defaults := scan.Defaults(cfg)
	if demo {
		return defaults, nil, nil
	}
	if cfg.LegacyMinChangePct != nil || cfg.LegacyMaxFloatShares != nil || cfg.LegacyMinVolume != nil {
		if err := scan.ValidateFilters(defaults); err != nil {
			return defaults, nil, fmt.Errorf("invalid legacy scanner defaults: %w", err)
		}
		raw, err := json.Marshal(defaults)
		if err != nil {
			return defaults, nil, err
		}
		st.SetConfig(legacyScannerDefaultsKey, string(raw))
		st.Flush()
		stored, ok, err := st.GetConfig(legacyScannerDefaultsKey)
		if err != nil || !ok || stored != string(raw) {
			return defaults, nil, fmt.Errorf("could not persist legacy scanner defaults: %v", err)
		}
		return defaults, &cfg, nil
	}
	raw, ok, err := st.GetConfig(legacyScannerDefaultsKey)
	if err != nil {
		return defaults, nil, err
	}
	if ok {
		if saved, valid := decodeScannerFiltersV2(raw); valid {
			defaults = saved
		}
	}
	return defaults, nil, nil
}
