package wailsruntime

import "testing"

func TestSessionRegistryBindsOpaqueTokenToWindowAndWorkspace(t *testing.T) {
	registry := NewSessionRegistry()
	token, err := registry.Issue(SessionOwner{WorkspaceID: "alpha", WindowID: 7})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if err := registry.Validate(token, "alpha", 7); err != nil {
		t.Fatalf("validate owner: %v", err)
	}
	if err := registry.Validate(token, "beta", 7); err == nil {
		t.Fatal("workspace text alone must not authorize a different session")
	}
	if err := registry.Validate(token, "alpha", 8); err == nil {
		t.Fatal("a token must not authorize a different native window")
	}
}

func TestSessionRegistrySupportsIsolatedServerIdentity(t *testing.T) {
	registry := NewSessionRegistry()
	token, err := registry.Issue(SessionOwner{WorkspaceID: "playwright", WindowID: 0})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if err := registry.Validate(token, "playwright", 0); err != nil {
		t.Fatalf("server identity: %v", err)
	}
	if err := registry.Validate(token, "playwright", 1); err == nil {
		t.Fatal("server identity must not be usable as a desktop window identity")
	}
}

func TestSessionRegistryRevokesWorkspaceSessionsTogether(t *testing.T) {
	registry := NewSessionRegistry()
	alpha, err := registry.Issue(SessionOwner{WorkspaceID: "alpha", WindowID: 1})
	if err != nil {
		t.Fatalf("issue alpha: %v", err)
	}
	beta, err := registry.Issue(SessionOwner{WorkspaceID: "beta", WindowID: 2})
	if err != nil {
		t.Fatalf("issue beta: %v", err)
	}

	registry.RevokeWorkspace("alpha")
	if err := registry.Validate(alpha, "alpha", 1); err == nil {
		t.Fatal("workspace close left an alpha session valid")
	}
	if err := registry.Validate(beta, "beta", 2); err != nil {
		t.Fatalf("workspace close revoked beta session: %v", err)
	}
}
