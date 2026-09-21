package uihub

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"sync"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

const WindowStateConfigKey = "window-state.v1"

var workspaceIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// window-state.v1 is intentionally small and machine-local: the config store
// already belongs to one eTape database, while browser coordinates do not make
// sense as portable workspace content.
func decodeWindowState(raw string) (wsmsg.WindowStateV1, error) {
	if raw == "" {
		return wsmsg.WindowStateV1{Version: 1, Entries: []wsmsg.WindowStateEntry{}}, nil
	}
	var doc wsmsg.WindowStateV1
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return wsmsg.WindowStateV1{}, fmt.Errorf("decode window state: %w", err)
	}
	if doc.Version != 1 {
		return wsmsg.WindowStateV1{}, fmt.Errorf("unsupported window state version %d", doc.Version)
	}
	if doc.Entries == nil {
		doc.Entries = []wsmsg.WindowStateEntry{}
	}
	return doc, nil
}

// DecodeWindowState validates the persisted machine-local window document.
func DecodeWindowState(raw string) (wsmsg.WindowStateV1, error) { return decodeWindowState(raw) }

func encodeWindowState(doc wsmsg.WindowStateV1) string {
	b, _ := json.Marshal(doc)
	return string(b)
}

// EncodeWindowState serializes a validated window document for config storage.
func EncodeWindowState(doc wsmsg.WindowStateV1) string { return encodeWindowState(doc) }

func validWindowStateEntry(e wsmsg.WindowStateEntry) bool {
	return workspaceIDPattern.MatchString(e.WorkspaceID) &&
		e.X >= -1_000_000 && e.X <= 1_000_000 &&
		e.Y >= -1_000_000 && e.Y <= 1_000_000 &&
		e.Width >= 100 && e.Width <= 100_000 &&
		e.Height >= 100 && e.Height <= 100_000
}

// normalizeWindowState removes malformed/deleted entries, keeps the newest
// bounds for duplicate ids, and sorts the result for deterministic writes.
func normalizeWindowState(doc wsmsg.WindowStateV1, allowed map[string]bool) (wsmsg.WindowStateV1, []string) {
	latest := make(map[string]wsmsg.WindowStateEntry, len(doc.Entries))
	dropped := make([]string, 0)
	for _, entry := range doc.Entries {
		if !validWindowStateEntry(entry) || (allowed != nil && !allowed[entry.WorkspaceID]) {
			dropped = append(dropped, entry.WorkspaceID)
			continue
		}
		latest[entry.WorkspaceID] = entry
	}
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := wsmsg.WindowStateV1{Version: 1, Entries: make([]wsmsg.WindowStateEntry, 0, len(ids))}
	for _, id := range ids {
		out.Entries = append(out.Entries, latest[id])
	}
	return out, dropped
}

// NormalizeWindowState removes malformed or unavailable entries and sorts the result.
func NormalizeWindowState(doc wsmsg.WindowStateV1, allowed map[string]bool) (wsmsg.WindowStateV1, []string) {
	return normalizeWindowState(doc, allowed)
}

type windowStateRegistry struct {
	cfg     configStore
	mu      sync.Mutex
	entries map[string]wsmsg.WindowStateEntry
	owners  map[uint64]string
}

func newWindowStateRegistry(cfg configStore) *windowStateRegistry {
	r := &windowStateRegistry{cfg: cfg, entries: map[string]wsmsg.WindowStateEntry{}, owners: map[uint64]string{}}
	if cfg == nil {
		return r
	}
	raw, ok, err := cfg.GetConfig(WindowStateConfigKey)
	if err != nil {
		slog.Warn("load window state", "err", err)
		return r
	}
	if !ok {
		return r
	}
	doc, err := decodeWindowState(raw)
	if err != nil {
		slog.Warn("reset invalid window state", "err", err)
		r.persistLocked()
		return r
	}
	doc, dropped := normalizeWindowState(doc, nil)
	for _, entry := range doc.Entries {
		r.entries[entry.WorkspaceID] = entry
	}
	if len(dropped) > 0 || raw != encodeWindowState(doc) {
		slog.Warn("discard invalid window state entries", "count", len(dropped))
		r.persistLocked()
	}
	return r
}

func (r *windowStateRegistry) set(connID uint64, args wsmsg.SetWindowStateArgs) wsmsg.AckMsg {
	entry := wsmsg.WindowStateEntry{
		WorkspaceID: args.WorkspaceID,
		X:           args.X, Y: args.Y, Width: args.Width, Height: args.Height,
	}
	if !validWindowStateEntry(entry) {
		return wsmsg.AckMsg{Status: wsmsg.AckBlocked, Reason: "invalid workspace window state"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	if oldID := r.owners[connID]; oldID != "" && oldID != entry.WorkspaceID {
		delete(r.owners, connID)
		if !r.hasOwnerLocked(oldID) {
			if _, ok := r.entries[oldID]; ok {
				delete(r.entries, oldID)
				changed = true
			}
		}
	}
	r.owners[connID] = entry.WorkspaceID
	if old, ok := r.entries[entry.WorkspaceID]; !ok || old != entry {
		r.entries[entry.WorkspaceID] = entry
		changed = true
	}
	if changed {
		r.persistLocked()
	}
	return wsmsg.AckMsg{Status: wsmsg.AckAccepted}
}

func (r *windowStateRegistry) release(connID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.owners[connID]
	if id == "" {
		return
	}
	delete(r.owners, connID)
	if r.hasOwnerLocked(id) {
		return
	}
	if _, ok := r.entries[id]; ok {
		delete(r.entries, id)
		r.persistLocked()
	}
}

func (r *windowStateRegistry) hasOwnerLocked(id string) bool {
	for _, ownerID := range r.owners {
		if ownerID == id {
			return true
		}
	}
	return false
}

func (r *windowStateRegistry) persistLocked() {
	if r.cfg == nil {
		return
	}
	ids := make([]string, 0, len(r.entries))
	for id := range r.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	doc := wsmsg.WindowStateV1{Version: 1, Entries: make([]wsmsg.WindowStateEntry, 0, len(ids))}
	for _, id := range ids {
		doc.Entries = append(doc.Entries, r.entries[id])
	}
	r.cfg.SetConfig(WindowStateConfigKey, encodeWindowState(doc))
}
