//go:build wails && server

package wailsruntime

import (
	"context"
	"errors"
	"testing"
)

func TestServerRuntimeUsesIsolatedWorkspaceIdentity(t *testing.T) {
	runtime := New()
	if _, err := runtime.OpenSession(context.Background(), "alpha"); !errors.Is(err, ErrUnknownWorkspace) {
		t.Fatalf("unregistered server workspace = %v, want %v", err, ErrUnknownWorkspace)
	}
	if err := runtime.RegisterWorkspace("alpha"); err != nil {
		t.Fatalf("register workspace: %v", err)
	}
	token, err := runtime.OpenSession(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("open server session: %v", err)
	}
	hello := StreamHello{Protocol: StreamProtocol, WorkspaceID: "alpha", Session: token}
	if err := runtime.ValidateSession(hello, 99); err != nil {
		t.Fatalf("server session with BrowserWindow identity: %v", err)
	}

	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("stop runtime: %v", err)
	}
	if err := runtime.ValidateSession(hello, 0); err == nil {
		t.Fatal("session remained valid after runtime stop")
	}
}
