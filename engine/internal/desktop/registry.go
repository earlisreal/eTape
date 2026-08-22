package desktop

import (
	"errors"
	"fmt"
	"regexp"
	"sync"
)

var workspaceIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

var ErrInvalidWorkspaceID = errors.New("desktop: invalid workspace id")

// ValidateWorkspaceID keeps the native window name and URL identity stable.
func ValidateWorkspaceID(id string) error {
	if !workspaceIDPattern.MatchString(id) {
		return fmt.Errorf("%w %q", ErrInvalidWorkspaceID, id)
	}
	return nil
}

func WindowName(workspaceID string) string { return "workspace:" + workspaceID }

// NativeWindow is the small lifecycle surface the registry needs. The Wails
// adapter lives beside the native host; tests can use a tiny fake instead.
type NativeWindow interface {
	Show()
	Focus()
	Restore()
	IsMinimised() bool
	Close()
}

// WorkspaceRegistry owns the one-native-window-per-workspace invariant.
type WorkspaceRegistry struct {
	mu      sync.Mutex
	windows map[string]NativeWindow
	onEmpty func()
}

func NewWorkspaceRegistry(onEmpty func()) *WorkspaceRegistry {
	return &WorkspaceRegistry{windows: make(map[string]NativeWindow), onEmpty: onEmpty}
}

// Open returns the existing window when present and activates it. Creation is
// serialized so concurrent opens cannot produce duplicate native identities.
func (r *WorkspaceRegistry) Open(id string, create func() NativeWindow) (NativeWindow, error) {
	if err := ValidateWorkspaceID(id); err != nil {
		return nil, err
	}

	r.mu.Lock()
	if existing := r.windows[id]; existing != nil {
		r.mu.Unlock()
		activate(existing)
		return existing, nil
	}
	window := create()
	if window == nil {
		r.mu.Unlock()
		return nil, errors.New("desktop: native window creation failed")
	}
	r.windows[id] = window
	r.mu.Unlock()
	return window, nil
}

func (r *WorkspaceRegistry) Get(id string) (NativeWindow, bool) {
	r.mu.Lock()
	window, ok := r.windows[id]
	r.mu.Unlock()
	return window, ok
}

// Close removes a native identity. The final close signals the tray policy;
// the registry never deletes the persisted Workspace document.
func (r *WorkspaceRegistry) Close(id string) bool {
	r.mu.Lock()
	_, removed := r.windows[id]
	if removed {
		delete(r.windows, id)
	}
	last := removed && len(r.windows) == 0
	r.mu.Unlock()

	if last && r.onEmpty != nil {
		r.onEmpty()
	}
	return removed
}

func (r *WorkspaceRegistry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.windows)
}

func activate(window NativeWindow) {
	if window.IsMinimised() {
		window.Restore()
	}
	window.Show()
	window.Focus()
}
