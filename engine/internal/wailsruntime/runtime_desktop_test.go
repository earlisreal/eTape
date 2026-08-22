//go:build wails && !server

package wailsruntime

import (
	"context"
	"testing"
)

func TestDesktopRuntimeRejectsWindowlessSessionValidation(t *testing.T) {
	runtime := New()
	token, err := runtime.sessions.Issue(SessionOwner{WorkspaceID: "alpha", WindowID: 7})
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	if err := runtime.ValidateSession(StreamHello{
		Protocol:    StreamProtocol,
		WorkspaceID: "alpha",
		Session:     token,
	}, 0); err == nil {
		t.Fatal("desktop runtime accepted a windowless session")
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("stop runtime: %v", err)
	}
}
