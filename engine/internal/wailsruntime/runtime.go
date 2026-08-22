//go:build wails

package wailsruntime

import (
	"context"
	"fmt"

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
	gate     *Gate
	sessions *SessionRegistry
}

func New() *Runtime {
	return &Runtime{
		gate:     NewGate(),
		sessions: NewSessionRegistry(),
	}
}

func (r *Runtime) Gate() *Gate { return r.gate }

func (r *Runtime) Enter(ctx context.Context) (func(), error) {
	return r.gate.Enter(ctx)
}

func (r *Runtime) Stop(ctx context.Context) error { return r.gate.Stop(ctx) }

func (r *Runtime) CallerWindowID(ctx context.Context) uint64 {
	if ctx == nil {
		return 0
	}
	window, _ := ctx.Value(application.WindowKey).(application.Window)
	if window == nil {
		return 0
	}
	return uint64(window.ID())
}

func (r *Runtime) OpenSession(ctx context.Context, workspaceID string) (string, error) {
	release, err := r.Enter(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	if workspaceID == "" {
		return "", ErrInvalidSession
	}
	return r.sessions.Issue(SessionOwner{
		WorkspaceID: workspaceID,
		WindowID:    r.CallerWindowID(ctx),
	})
}

func (r *Runtime) ValidateSession(hello StreamHello, windowID uint64) error {
	if hello.Protocol != StreamProtocol {
		return fmt.Errorf("unsupported stream protocol %d", hello.Protocol)
	}
	return r.sessions.Validate(hello.Session, hello.WorkspaceID, windowID)
}

func (r *Runtime) ValidateStream(c *application.StreamConn, hello StreamHello) error {
	var windowID uint64
	if window := c.Window(); window != nil {
		windowID = uint64(window.ID())
	}
	return r.ValidateSession(hello, windowID)
}

func (r *Runtime) HandleStream(c *application.StreamConn) {
	release, err := r.Enter(c.Context())
	if err != nil {
		return
	}
	defer release()

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
