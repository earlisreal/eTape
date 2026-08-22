package uistate

import (
	"errors"
	"fmt"
	"regexp"
	"sync"
)

var workspaceIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

var (
	ErrInvalidWorkspaceID = errors.New("uistate: invalid workspace id")
	ErrWindowClosed       = errors.New("uistate: workspace window is closed")
)

// ValidateWorkspaceID keeps the Native Window name and Workspace identity
// stable. Display names are separate catalog data and may contain spaces.
func ValidateWorkspaceID(id string) error {
	if !workspaceIDPattern.MatchString(id) {
		return fmt.Errorf("%w %q", ErrInvalidWorkspaceID, id)
	}
	return nil
}

// NativeWindow is the small lifecycle surface the canonical state store needs.
// The desktop package adapts the Wails window to this interface.
type NativeWindow interface {
	Show()
	Focus()
	Restore()
	IsMinimised() bool
	Close()
}

// WindowRegistry owns the one-Native-Window-per-Workspace invariant. A nil
// create function is the headless/server representation of an open identity.
type WindowRegistry struct {
	mu      sync.Mutex
	windows map[string]NativeWindow
	onEmpty func()
}

func NewWindowRegistry(onEmpty func()) *WindowRegistry {
	return &WindowRegistry{windows: make(map[string]NativeWindow), onEmpty: onEmpty}
}

func (r *WindowRegistry) SetOnEmpty(onEmpty func()) {
	r.mu.Lock()
	r.onEmpty = onEmpty
	r.mu.Unlock()
}

// Open returns the existing window when present and activates it. Creation is
// serialized so concurrent opens cannot produce duplicate native identities.
func (r *WindowRegistry) Open(id string, create func() NativeWindow) (NativeWindow, error) {
	window, _, err := r.OpenWithStatus(id, create)
	return window, err
}

func (r *WindowRegistry) OpenWithStatus(id string, create func() NativeWindow) (NativeWindow, bool, error) {
	if err := ValidateWorkspaceID(id); err != nil {
		return nil, false, err
	}

	r.mu.Lock()
	if existing, ok := r.windows[id]; ok {
		r.mu.Unlock()
		activate(existing)
		return existing, false, nil
	}
	var window NativeWindow
	if create != nil {
		window = create()
		if window == nil {
			r.mu.Unlock()
			return nil, false, errors.New("uistate: native window creation failed")
		}
	}
	r.windows[id] = window
	r.mu.Unlock()
	return window, true, nil
}

func (r *WindowRegistry) Get(id string) (NativeWindow, bool) {
	r.mu.Lock()
	window, ok := r.windows[id]
	r.mu.Unlock()
	return window, ok
}

func (r *WindowRegistry) Focus(id string) error {
	r.mu.Lock()
	window, ok := r.windows[id]
	r.mu.Unlock()
	if !ok {
		return ErrWindowClosed
	}
	activate(window)
	return nil
}

// Close removes a native identity. It never deletes the persisted Workspace.
func (r *WindowRegistry) Close(id string) bool {
	r.mu.Lock()
	_, removed := r.windows[id]
	if removed {
		delete(r.windows, id)
	}
	last := removed && len(r.windows) == 0
	onEmpty := r.onEmpty
	r.mu.Unlock()

	if last && onEmpty != nil {
		onEmpty()
	}
	return removed
}

func (r *WindowRegistry) IsOpen(id string) bool {
	r.mu.Lock()
	_, ok := r.windows[id]
	r.mu.Unlock()
	return ok
}

func (r *WindowRegistry) IDs() []string {
	r.mu.Lock()
	ids := make([]string, 0, len(r.windows))
	for id := range r.windows {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	return ids
}

func (r *WindowRegistry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.windows)
}

func activate(window NativeWindow) {
	if window == nil {
		return
	}
	if window.IsMinimised() {
		window.Restore()
	}
	window.Show()
	window.Focus()
}
