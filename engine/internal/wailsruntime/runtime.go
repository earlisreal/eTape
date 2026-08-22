//go:build wails

package wailsruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const StreamProtocol = 1

type StreamHello struct {
	Protocol    int    `json:"protocol"`
	WorkspaceID string `json:"workspaceId"`
	Session     string `json:"session"`
}

type StreamReply struct {
	Type  string `json:"type"`
	Error string `json:"error,omitempty"`
}

type Runtime struct {
	gate       *Gate
	sessions   *SessionRegistry
	workspaces *WorkspaceRegistry
	hints      *HintQueue
	streamsMu  sync.Mutex
	streams    map[*application.StreamConn]struct{}
	stopOnce   sync.Once
}

func New() *Runtime {
	return &Runtime{
		gate:       NewGate(),
		sessions:   NewSessionRegistry(),
		workspaces: NewWorkspaceRegistry(),
		hints:      NewHintQueue(256),
		streams:    make(map[*application.StreamConn]struct{}),
	}
}

func (r *Runtime) Gate() *Gate { return r.gate }

func (r *Runtime) Enter(ctx context.Context) (func(), error) {
	return r.gate.Enter(ctx)
}

func (r *Runtime) EnterContext(ctx context.Context) (context.Context, func(), error) {
	return r.gate.EnterContext(ctx)
}

// BeginStop synchronously closes the runtime admission boundary and revokes
// all ephemeral transport capabilities. The lifecycle owner calls this from
// Wails' non-blocking shutdown hook before it waits for admitted work.
func (r *Runtime) BeginStop() {
	r.stopOnce.Do(func() {
		r.gate.BeginStop()
		r.sessions.RevokeAll()
		r.streamsMu.Lock()
		streams := make([]*application.StreamConn, 0, len(r.streams))
		for stream := range r.streams {
			streams = append(streams, stream)
		}
		r.streamsMu.Unlock()
		for _, stream := range streams {
			_ = stream.Close()
		}
	})
}

func (r *Runtime) Stop(ctx context.Context) error {
	r.BeginStop()
	return r.gate.Stop(ctx)
}

func (r *Runtime) RegisterWorkspace(workspaceID string) error {
	return r.workspaces.Register(workspaceID)
}

func (r *Runtime) EnqueueHint(hint Hint) bool {
	if !EventAllowed(hint.Class) || r.gate.Stopping() {
		return false
	}
	return r.hints.Push(hint)
}

func (r *Runtime) PopHint() (Hint, bool) { return r.hints.Pop() }

func (r *Runtime) HintWake() <-chan struct{} { return r.hints.Wake() }

func (r *Runtime) CallerWindowID(ctx context.Context) uint64 {
	if ServerMode || ctx == nil {
		return 0
	}
	window, _ := ctx.Value(application.WindowKey).(application.Window)
	if window == nil {
		return 0
	}
	return uint64(window.ID())
}

func CallerWorkspaceID(window application.Window) string {
	if ServerMode || window == nil {
		return ""
	}
	const prefix = "workspace:"
	name := window.Name()
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	return strings.TrimPrefix(name, prefix)
}

func (r *Runtime) OpenSession(ctx context.Context, workspaceID string) (string, error) {
	_, release, err := r.EnterContext(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	if workspaceID == "" {
		return "", ErrInvalidSession
	}
	windowID := r.CallerWindowID(ctx)
	if ServerMode {
		if !r.workspaces.Contains(workspaceID) {
			return "", ErrUnknownWorkspace
		}
	} else if windowID == 0 || CallerWorkspaceID(r.callerWindow(ctx)) != workspaceID {
		return "", ErrSessionOwner
	}
	return r.sessions.Issue(SessionOwner{
		WorkspaceID: workspaceID,
		WindowID:    windowID,
	})
}

func (r *Runtime) ValidateSession(hello StreamHello, windowID uint64) error {
	if hello.Protocol != StreamProtocol {
		return fmt.Errorf("unsupported stream protocol %d", hello.Protocol)
	}
	if !ServerMode && windowID == 0 {
		return ErrSessionOwner
	}
	if ServerMode {
		windowID = 0
	}
	return r.sessions.Validate(hello.Session, hello.WorkspaceID, windowID)
}

func (r *Runtime) ValidateStream(c *application.StreamConn, hello StreamHello) error {
	var windowID uint64
	if window := c.Window(); window != nil {
		if !ServerMode && CallerWorkspaceID(window) != hello.WorkspaceID {
			return ErrSessionOwner
		}
		windowID = uint64(window.ID())
	}
	return r.ValidateSession(hello, windowID)
}

func (r *Runtime) HandleStream(c *application.StreamConn) {
	_, release, err := r.EnterContext(c.Context())
	if err != nil {
		return
	}
	defer release()
	if !r.trackStream(c) {
		return
	}
	defer r.untrackStream(c)

	var hello StreamHello
	if err := c.ReceiveJSON(&hello); err != nil {
		return
	}
	defer r.sessions.Revoke(hello.Session)
	if err := r.ValidateStream(c, hello); err != nil {
		_ = c.SendJSON(StreamReply{Type: "rejected", Error: err.Error()})
		return
	}
	if err := c.SendJSON(StreamReply{Type: "accepted"}); err != nil {
		return
	}

	for {
		frame, err := c.Receive()
		if err != nil {
			return
		}
		if err := c.TrySend(frame); err != nil {
			return
		}
	}
}

func (r *Runtime) callerWindow(ctx context.Context) application.Window {
	if ctx == nil {
		return nil
	}
	window, _ := ctx.Value(application.WindowKey).(application.Window)
	return window
}

func (r *Runtime) trackStream(c *application.StreamConn) bool {
	r.streamsMu.Lock()
	if r.gate.Stopping() {
		r.streamsMu.Unlock()
		_ = c.Close()
		return false
	}
	r.streams[c] = struct{}{}
	r.streamsMu.Unlock()
	return true
}

func (r *Runtime) untrackStream(c *application.StreamConn) {
	r.streamsMu.Lock()
	delete(r.streams, c)
	r.streamsMu.Unlock()
}
