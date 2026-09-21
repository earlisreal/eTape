package main

import (
	"encoding/json"
	"log/slog"
	"net/url"

	"github.com/earlisreal/eTape/engine/internal/openbrowser"
	"github.com/earlisreal/eTape/engine/internal/uihub"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type windowRestoreStore interface {
	GetConfig(key string) (string, bool, error)
	SetConfig(key, value string)
}

type windowCatalogDocument struct {
	Version int `json:"version"`
	Entries []struct {
		ID string `json:"id"`
	} `json:"entries"`
}

// restoredWindowSpecs reads only the machine-local open set. Missing or bad
// state deliberately degrades to main-only startup.
func restoredWindowSpecs(st windowRestoreStore, addr string, debug bool) []openbrowser.WindowSpec {
	log := slog.Default()
	allowed := map[string]bool{"main": true, "monitoring": true}
	if raw, ok, err := st.GetConfig("windows.v1"); err != nil {
		log.Warn("load workspace catalog for window restore", "err", err)
	} else if ok {
		var catalog windowCatalogDocument
		if err := json.Unmarshal([]byte(raw), &catalog); err == nil && catalog.Version == 1 {
			for _, entry := range catalog.Entries {
				allowed[entry.ID] = true
			}
		}
	}

	raw, ok, err := st.GetConfig(uihub.WindowStateConfigKey)
	if err != nil || !ok {
		if err != nil {
			log.Warn("load window state for restore", "err", err)
		}
		return nil
	}
	doc, err := uihub.DecodeWindowState(raw)
	if err != nil {
		log.Warn("reset invalid window state", "err", err)
		empty := wsmsg.WindowStateV1{Version: 1, Entries: []wsmsg.WindowStateEntry{}}
		st.SetConfig(uihub.WindowStateConfigKey, uihub.EncodeWindowState(empty))
		return nil
	}
	doc, dropped := uihub.NormalizeWindowState(doc, allowed)
	encoded := uihub.EncodeWindowState(doc)
	if len(dropped) > 0 || encoded != raw {
		log.Warn("discard invalid window state entries", "count", len(dropped))
		st.SetConfig(uihub.WindowStateConfigKey, encoded)
	}

	result := make([]openbrowser.WindowSpec, 0, len(doc.Entries))
	for _, entry := range doc.Entries {
		if entry.WorkspaceID == "main" {
			continue
		}
		result = append(result, openbrowser.WindowSpec{
			URL: workspaceBrowserURL(addr, debug, entry.WorkspaceID),
			X:   entry.X, Y: entry.Y, Width: entry.Width, Height: entry.Height,
		})
	}
	return result
}

func workspaceBrowserURL(addr string, debug bool, workspaceID string) string {
	base := browserURL(addr, debug)
	parsed, err := url.Parse(base)
	if err != nil {
		return base
	}
	query := parsed.Query()
	query.Set("workspace", workspaceID)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
