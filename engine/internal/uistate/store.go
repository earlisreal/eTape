package uistate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

const (
	CatalogKey                = "windows.v1"
	CatalogRevisionKey        = "windows.v1.revision"
	CatalogVersion            = 1
	MainWorkspaceID           = "main"
	MonitoringWorkspaceID     = "monitoring"
	MonitoringWorkspaceName   = "Monitoring"
	MaxWorkspaceDocumentBytes = 4 << 20
	MaxWorkspaceLayoutBytes   = 2 << 20
)

var (
	ErrNotReady             = errors.New("uistate: persistence is not configured")
	ErrUnknownWorkspace     = errors.New("uistate: unknown workspace")
	ErrWorkspaceConflict    = errors.New("uistate: stale workspace revision")
	ErrCatalogConflict      = errors.New("uistate: stale catalog revision")
	ErrReservedWorkspace    = errors.New("uistate: reserved workspace")
	ErrDuplicateWorkspace   = errors.New("uistate: workspace already exists")
	ErrInvalidWorkspaceName = errors.New("uistate: invalid workspace name")
	ErrInvalidDocument      = errors.New("uistate: invalid workspace document")
	ErrRevisionOverflow     = errors.New("uistate: revision limit reached")
)

// Persistence is the existing config-store surface. Store deliberately does
// not replace SQLite; it serializes the in-memory authority above this seam.
type Persistence interface {
	GetConfig(key string) (string, bool, error)
	SetConfig(key, value string)
	DeleteConfig(key string)
}

type DurablePersistence interface {
	Persistence
	Flush()
}

type CatalogEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Open bool   `json:"open,omitempty"`
}

type CatalogSnapshot struct {
	Revision int64          `json:"revision"`
	Entries  []CatalogEntry `json:"entries"`
	OpenIDs  []string       `json:"openIds"`
}

type DocumentSnapshot struct {
	WorkspaceID string
	Revision    int64
	Exists      bool
	Document    []byte
}

type Invalidation struct {
	WorkspaceID string
	Kind        string
	Revision    int64
}

type Listener func(Invalidation)

type persistedCatalog struct {
	Version int            `json:"version"`
	Entries []CatalogEntry `json:"entries"`
}

type documentRecord struct {
	loaded   bool
	exists   bool
	revision int64
	document []byte
}

// Store is the canonical low-rate Workspace authority. Catalog and document
// mutations are serialized here; the frontend only holds projections.
type Store struct {
	mu          sync.Mutex
	persistence Persistence
	ready       bool
	catalog     []CatalogEntry
	catalogRev  int64
	documents   map[string]documentRecord
	listeners   map[string]map[uint64]Listener
	notifier    Listener
	nextListen  uint64
	windows     *WindowRegistry
}

func NewStore(persistence Persistence) (*Store, error) {
	s := &Store{
		persistence: persistence,
		documents:   make(map[string]documentRecord),
		listeners:   make(map[string]map[uint64]Listener),
		windows:     NewWindowRegistry(nil),
	}
	if persistence == nil {
		return s, nil
	}
	if err := s.ConfigurePersistence(persistence); err != nil {
		return nil, err
	}
	return s, nil
}

func NewRuntimeStore() *Store {
	s, _ := NewStore(nil)
	return s
}

// ConfigurePersistence is called once when the engine's existing Store opens.
// The catalog is loaded before the service exposes any mutable operation.
func (s *Store) ConfigurePersistence(persistence Persistence) error {
	if persistence == nil {
		return ErrNotReady
	}
	raw, found, err := persistence.GetConfig(CatalogKey)
	if err != nil {
		return err
	}
	entries := []CatalogEntry{}
	if found {
		var saved persistedCatalog
		if json.Unmarshal([]byte(raw), &saved) == nil && saved.Version == CatalogVersion {
			entries = validCatalogEntries(saved.Entries)
		}
	}
	revision := int64(0)
	rawRevision, ok, revisionErr := persistence.GetConfig(CatalogRevisionKey)
	if revisionErr != nil {
		return revisionErr
	}
	if ok {
		revision, _ = strconv.ParseInt(rawRevision, 10, 64)
	}
	if revision <= 0 && len(entries) > 0 {
		revision = 1
	}

	s.mu.Lock()
	if s.ready && s.persistence != persistence {
		s.mu.Unlock()
		return errors.New("uistate: persistence already configured")
	}
	s.persistence = persistence
	s.catalog = entries
	s.catalogRev = revision
	s.ready = true
	s.mu.Unlock()
	return nil
}

func (s *Store) Ready() bool {
	s.mu.Lock()
	ready := s.ready
	s.mu.Unlock()
	return ready
}

func (s *Store) Windows() *WindowRegistry { return s.windows }

func (s *Store) SetInvalidationNotifier(notifier Listener) {
	s.mu.Lock()
	s.notifier = notifier
	s.mu.Unlock()
}

func (s *Store) Subscribe(workspaceID string, listener Listener) func() {
	if listener == nil {
		return func() {}
	}
	s.mu.Lock()
	s.nextListen++
	id := s.nextListen
	listeners := s.listeners[workspaceID]
	if listeners == nil {
		listeners = make(map[uint64]Listener)
		s.listeners[workspaceID] = listeners
	}
	listeners[id] = listener
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if listeners := s.listeners[workspaceID]; listeners != nil {
			delete(listeners, id)
			if len(listeners) == 0 {
				delete(s.listeners, workspaceID)
			}
		}
		s.mu.Unlock()
	}
}

func (s *Store) CatalogSnapshot() (CatalogSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return CatalogSnapshot{}, ErrNotReady
	}
	return s.catalogSnapshotLocked(), nil
}

func (s *Store) KnownWorkspace(id string) (bool, error) {
	if err := ValidateWorkspaceID(id); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return false, ErrNotReady
	}
	return knownWorkspaceLocked(id, s.catalog), nil
}

func (s *Store) LoadDocument(id string) (DocumentSnapshot, error) {
	if err := ValidateWorkspaceID(id); err != nil {
		return DocumentSnapshot{WorkspaceID: id}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return DocumentSnapshot{WorkspaceID: id}, ErrNotReady
	}
	if !knownWorkspaceLocked(id, s.catalog) {
		return DocumentSnapshot{WorkspaceID: id}, ErrUnknownWorkspace
	}
	record, err := s.loadDocumentLocked(id)
	if err != nil {
		return DocumentSnapshot{WorkspaceID: id}, err
	}
	return DocumentSnapshot{WorkspaceID: id, Revision: record.revision, Exists: record.exists, Document: clone(record.document)}, nil
}

func (s *Store) CreateWorkspace(id, name string, document []byte, expectedCatalogRevision int64) (CatalogSnapshot, DocumentSnapshot, error) {
	if err := ValidateWorkspaceID(id); err != nil {
		return CatalogSnapshot{}, DocumentSnapshot{WorkspaceID: id}, err
	}
	if id == MainWorkspaceID || id == MonitoringWorkspaceID {
		return CatalogSnapshot{}, DocumentSnapshot{WorkspaceID: id}, ErrReservedWorkspace
	}
	if err := validateWorkspaceName(name); err != nil {
		return CatalogSnapshot{}, DocumentSnapshot{WorkspaceID: id}, err
	}
	if len(document) == 0 {
		document = []byte(fmt.Sprintf(`{"name":%q,"layoutVersion":8,"panels":[],"layout":null}`, id))
	}
	if err := validateDocument(id, document); err != nil {
		return CatalogSnapshot{}, DocumentSnapshot{WorkspaceID: id}, err
	}

	s.mu.Lock()
	if err := s.requireReadyLocked(); err != nil {
		s.mu.Unlock()
		return CatalogSnapshot{}, DocumentSnapshot{WorkspaceID: id}, err
	}
	if expectedCatalogRevision != 0 && expectedCatalogRevision != s.catalogRev {
		s.mu.Unlock()
		return CatalogSnapshot{}, DocumentSnapshot{WorkspaceID: id}, ErrCatalogConflict
	}
	if knownWorkspaceLocked(id, s.catalog) {
		s.mu.Unlock()
		return CatalogSnapshot{}, DocumentSnapshot{WorkspaceID: id}, ErrDuplicateWorkspace
	}
	if catalogNameTaken(name, s.catalog) {
		s.mu.Unlock()
		return CatalogSnapshot{}, DocumentSnapshot{WorkspaceID: id}, ErrDuplicateWorkspace
	}
	entry := CatalogEntry{ID: id, Name: name}
	s.catalog = append(s.catalog, entry)
	var err error
	s.catalogRev, err = nextRevision(s.catalogRev)
	if err != nil {
		s.catalog = s.catalog[:len(s.catalog)-1]
		s.mu.Unlock()
		return CatalogSnapshot{}, DocumentSnapshot{WorkspaceID: id}, err
	}
	s.documents[id] = documentRecord{loaded: true, exists: true, revision: 1, document: clone(document)}
	persistence := s.persistence
	persistence.SetConfig(CatalogKey, marshalCatalog(s.catalog))
	persistence.SetConfig(CatalogRevisionKey, strconv.FormatInt(s.catalogRev, 10))
	persistence.SetConfig(documentKey(id), string(document))
	persistence.SetConfig(documentRevisionKey(id), "1")
	catalog := s.catalogSnapshotLocked()
	doc := DocumentSnapshot{WorkspaceID: id, Revision: 1, Exists: true, Document: clone(document)}
	s.mu.Unlock()
	s.emit(Invalidation{Kind: "catalog", Revision: catalog.Revision})
	s.emit(Invalidation{WorkspaceID: id, Kind: "document", Revision: doc.Revision})
	return catalog, doc, nil
}

func (s *Store) RenameWorkspace(id, name string, expectedCatalogRevision int64) (CatalogSnapshot, error) {
	if err := ValidateWorkspaceID(id); err != nil {
		return CatalogSnapshot{}, err
	}
	if id == MainWorkspaceID || id == MonitoringWorkspaceID {
		return CatalogSnapshot{}, ErrReservedWorkspace
	}
	if err := validateWorkspaceName(name); err != nil {
		return CatalogSnapshot{}, err
	}
	s.mu.Lock()
	if err := s.requireReadyLocked(); err != nil {
		s.mu.Unlock()
		return CatalogSnapshot{}, err
	}
	if expectedCatalogRevision != 0 && expectedCatalogRevision != s.catalogRev {
		s.mu.Unlock()
		return CatalogSnapshot{}, ErrCatalogConflict
	}
	index := catalogIndex(id, s.catalog)
	if index < 0 {
		s.mu.Unlock()
		return CatalogSnapshot{}, ErrUnknownWorkspace
	}
	if catalogNameTakenExcept(name, id, s.catalog) {
		s.mu.Unlock()
		return CatalogSnapshot{}, ErrDuplicateWorkspace
	}
	if s.catalog[index].Name == name {
		catalog := s.catalogSnapshotLocked()
		s.mu.Unlock()
		return catalog, nil
	}
	s.catalog[index].Name = name
	var err error
	s.catalogRev, err = nextRevision(s.catalogRev)
	if err != nil {
		s.mu.Unlock()
		return CatalogSnapshot{}, err
	}
	s.persistence.SetConfig(CatalogKey, marshalCatalog(s.catalog))
	s.persistence.SetConfig(CatalogRevisionKey, strconv.FormatInt(s.catalogRev, 10))
	catalog := s.catalogSnapshotLocked()
	s.mu.Unlock()
	s.emit(Invalidation{Kind: "catalog", Revision: catalog.Revision})
	return catalog, nil
}

func (s *Store) DeleteWorkspace(id string, expectedCatalogRevision int64) (CatalogSnapshot, error) {
	if err := ValidateWorkspaceID(id); err != nil {
		return CatalogSnapshot{}, err
	}
	if id == MainWorkspaceID || id == MonitoringWorkspaceID {
		return CatalogSnapshot{}, ErrReservedWorkspace
	}
	s.mu.Lock()
	if err := s.requireReadyLocked(); err != nil {
		s.mu.Unlock()
		return CatalogSnapshot{}, err
	}
	if expectedCatalogRevision != 0 && expectedCatalogRevision != s.catalogRev {
		s.mu.Unlock()
		return CatalogSnapshot{}, ErrCatalogConflict
	}
	if s.windows.IsOpen(id) {
		s.mu.Unlock()
		return CatalogSnapshot{}, ErrWorkspaceConflict
	}
	index := catalogIndex(id, s.catalog)
	if index < 0 {
		s.mu.Unlock()
		return CatalogSnapshot{}, ErrUnknownWorkspace
	}
	s.catalog = append(s.catalog[:index], s.catalog[index+1:]...)
	var err error
	s.catalogRev, err = nextRevision(s.catalogRev)
	if err != nil {
		s.mu.Unlock()
		return CatalogSnapshot{}, err
	}
	delete(s.documents, id)
	s.persistence.SetConfig(CatalogKey, marshalCatalog(s.catalog))
	s.persistence.SetConfig(CatalogRevisionKey, strconv.FormatInt(s.catalogRev, 10))
	s.persistence.DeleteConfig(documentKey(id))
	s.persistence.DeleteConfig(documentRevisionKey(id))
	catalog := s.catalogSnapshotLocked()
	s.mu.Unlock()
	s.emit(Invalidation{Kind: "catalog", Revision: catalog.Revision})
	return catalog, nil
}

func (s *Store) SaveDocument(id string, document []byte, expectedRevision int64) (DocumentSnapshot, error) {
	if err := ValidateWorkspaceID(id); err != nil {
		return DocumentSnapshot{WorkspaceID: id}, err
	}
	if err := validateDocument(id, document); err != nil {
		return DocumentSnapshot{WorkspaceID: id}, err
	}
	s.mu.Lock()
	if err := s.requireReadyLocked(); err != nil {
		s.mu.Unlock()
		return DocumentSnapshot{WorkspaceID: id}, err
	}
	if !knownWorkspaceLocked(id, s.catalog) {
		s.mu.Unlock()
		return DocumentSnapshot{WorkspaceID: id}, ErrUnknownWorkspace
	}
	record, err := s.loadDocumentLocked(id)
	if err != nil {
		s.mu.Unlock()
		return DocumentSnapshot{WorkspaceID: id}, err
	}
	if expectedRevision != record.revision {
		s.mu.Unlock()
		return DocumentSnapshot{WorkspaceID: id, Revision: record.revision, Exists: record.exists, Document: clone(record.document)}, ErrWorkspaceConflict
	}
	if record.exists && bytes.Equal(record.document, document) {
		s.mu.Unlock()
		return DocumentSnapshot{WorkspaceID: id, Revision: record.revision, Exists: true, Document: clone(record.document)}, nil
	}
	revision, err := nextRevision(record.revision)
	if err != nil {
		s.mu.Unlock()
		return DocumentSnapshot{WorkspaceID: id, Revision: record.revision}, err
	}
	s.documents[id] = documentRecord{loaded: true, exists: true, revision: revision, document: clone(document)}
	s.persistence.SetConfig(documentKey(id), string(document))
	s.persistence.SetConfig(documentRevisionKey(id), strconv.FormatInt(revision, 10))
	s.mu.Unlock()
	s.emit(Invalidation{WorkspaceID: id, Kind: "document", Revision: revision})
	return DocumentSnapshot{WorkspaceID: id, Revision: revision, Exists: true, Document: clone(document)}, nil
}

func (s *Store) OpenWorkspace(id string, create func() NativeWindow) (NativeWindow, error) {
	if err := ValidateWorkspaceID(id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if id != MainWorkspaceID && id != MonitoringWorkspaceID {
		if err := s.requireReadyLocked(); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		if !knownWorkspaceLocked(id, s.catalog) {
			s.mu.Unlock()
			return nil, ErrUnknownWorkspace
		}
	}
	if s.ready {
		if _, err := nextRevision(s.catalogRev); err != nil {
			s.mu.Unlock()
			return nil, err
		}
	}
	window, created, err := s.windows.OpenWithStatus(id, create)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	var revision int64
	if created && s.ready {
		revision, _ = nextRevision(s.catalogRev)
		s.catalogRev = revision
		s.persistence.SetConfig(CatalogRevisionKey, strconv.FormatInt(revision, 10))
	}
	s.mu.Unlock()
	if revision != 0 {
		s.emit(Invalidation{Kind: "catalog", Revision: revision})
	}
	return window, nil
}

// Flush waits for the underlying persistence writer when it exposes the
// existing durable barrier. Test and headless adapters may omit that optional
// method; their synchronous SetConfig is already durable at this seam.
func (s *Store) Flush() error {
	s.mu.Lock()
	if err := s.requireReadyLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	persistence := s.persistence
	s.mu.Unlock()
	if durable, ok := persistence.(DurablePersistence); ok {
		durable.Flush()
	}
	return nil
}

func (s *Store) FocusWorkspace(id string) error {
	if err := s.ensureKnown(id); err != nil {
		return err
	}
	return s.windows.Focus(id)
}

func (s *Store) CloseWorkspace(id string) bool {
	if ValidateWorkspaceID(id) != nil {
		return false
	}
	s.mu.Lock()
	removed := s.windows.Close(id)
	var revision int64
	if removed && s.ready {
		if next, err := nextRevision(s.catalogRev); err == nil {
			s.catalogRev = next
			revision = next
			s.persistence.SetConfig(CatalogRevisionKey, strconv.FormatInt(next, 10))
		}
	}
	s.mu.Unlock()
	if revision != 0 {
		s.emit(Invalidation{Kind: "catalog", Revision: revision})
	}
	return removed
}

func (s *Store) OpenWorkspaceIDs() []string { return s.windows.IDs() }

func (s *Store) ensureKnown(id string) error {
	if err := ValidateWorkspaceID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == MainWorkspaceID || id == MonitoringWorkspaceID {
		return nil
	}
	if err := s.requireReadyLocked(); err != nil {
		return err
	}
	if !knownWorkspaceLocked(id, s.catalog) {
		return ErrUnknownWorkspace
	}
	return nil
}

func (s *Store) emit(invalidation Invalidation) {
	s.mu.Lock()
	listeners := listenersFor(s.listeners[invalidation.WorkspaceID])
	if invalidation.WorkspaceID != "" {
		listeners = append(listeners, listenersFor(s.listeners[""])...)
	}
	notifier := s.notifier
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(invalidation)
	}
	if notifier != nil {
		notifier(invalidation)
	}
}

func listenersFor(listeners map[uint64]Listener) []Listener {
	if len(listeners) == 0 {
		return nil
	}
	out := make([]Listener, 0, len(listeners))
	for _, listener := range listeners {
		out = append(out, listener)
	}
	return out
}

func (s *Store) requireReadyLocked() error {
	if !s.ready || s.persistence == nil {
		return ErrNotReady
	}
	return nil
}

func (s *Store) loadDocumentLocked(id string) (documentRecord, error) {
	if record, ok := s.documents[id]; ok && record.loaded {
		return record, nil
	}
	raw, found, err := s.persistence.GetConfig(documentKey(id))
	if err != nil {
		return documentRecord{}, err
	}
	record := documentRecord{loaded: true, exists: found}
	if found {
		record.document = []byte(raw)
		if err := validateDocument(id, record.document); err != nil {
			return documentRecord{}, err
		}
		record.revision = 1
		rawRevision, ok, revisionErr := s.persistence.GetConfig(documentRevisionKey(id))
		if revisionErr != nil {
			return documentRecord{}, revisionErr
		}
		if ok {
			if parsed, parseErr := strconv.ParseInt(rawRevision, 10, 64); parseErr == nil && parsed > 0 {
				record.revision = parsed
			}
		}
	}
	s.documents[id] = record
	return record, nil
}

func (s *Store) catalogSnapshotLocked() CatalogSnapshot {
	entries := make([]CatalogEntry, len(s.catalog))
	copy(entries, s.catalog)
	for i := range entries {
		entries[i].Open = s.windows.IsOpen(entries[i].ID)
	}
	return CatalogSnapshot{Revision: s.catalogRev, Entries: entries, OpenIDs: s.windows.IDs()}
}

func nextRevision(current int64) (int64, error) {
	if current == int64(^uint64(0)>>1) {
		return current, ErrRevisionOverflow
	}
	return current + 1, nil
}

func validCatalogEntries(entries []CatalogEntry) []CatalogEntry {
	out := make([]CatalogEntry, 0, len(entries))
	seenIDs := map[string]bool{}
	seenNames := map[string]bool{}
	for _, entry := range entries {
		if entry.ID == MainWorkspaceID || entry.ID == MonitoringWorkspaceID || ValidateWorkspaceID(entry.ID) != nil || seenIDs[entry.ID] {
			continue
		}
		if validateWorkspaceName(entry.Name) != nil || strings.EqualFold(entry.Name, MonitoringWorkspaceName) || seenNames[strings.ToLower(entry.Name)] {
			continue
		}
		seenIDs[entry.ID] = true
		seenNames[strings.ToLower(entry.Name)] = true
		out = append(out, CatalogEntry{ID: entry.ID, Name: entry.Name})
	}
	return out
}

func knownWorkspaceLocked(id string, entries []CatalogEntry) bool {
	if id == MainWorkspaceID || id == MonitoringWorkspaceID {
		return true
	}
	return catalogIndex(id, entries) >= 0
}

func catalogIndex(id string, entries []CatalogEntry) int {
	for i, entry := range entries {
		if entry.ID == id {
			return i
		}
	}
	return -1
}

func catalogNameTaken(name string, entries []CatalogEntry) bool {
	return strings.EqualFold(name, MainWorkspaceID) || strings.EqualFold(name, MonitoringWorkspaceName) || catalogNameTakenExcept(name, "", entries)
}

func catalogNameTakenExcept(name, exceptID string, entries []CatalogEntry) bool {
	for _, entry := range entries {
		if entry.ID != exceptID && strings.EqualFold(entry.Name, name) {
			return true
		}
	}
	return false
}

func validateWorkspaceName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 || strings.EqualFold(name, MainWorkspaceID) || strings.EqualFold(name, MonitoringWorkspaceName) {
		return ErrInvalidWorkspaceName
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return ErrInvalidWorkspaceName
		}
	}
	return nil
}

func validateDocument(id string, document []byte) error {
	if len(document) == 0 || len(document) > MaxWorkspaceDocumentBytes {
		return ErrInvalidDocument
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(document, &object); err != nil || object == nil {
		return ErrInvalidDocument
	}
	var name string
	if err := json.Unmarshal(object["name"], &name); err != nil || name != id {
		return ErrInvalidDocument
	}
	if layout, ok := object["layout"]; ok && len(layout) > MaxWorkspaceLayoutBytes {
		return ErrInvalidDocument
	}
	return nil
}

func marshalCatalog(entries []CatalogEntry) string {
	saved := persistedCatalog{Version: CatalogVersion, Entries: entries}
	b, _ := json.Marshal(saved)
	return string(b)
}

func documentKey(id string) string         { return "workspace." + id }
func documentRevisionKey(id string) string { return "workspace." + id + ".revision" }

func clone(value []byte) []byte { return append([]byte(nil), value...) }
