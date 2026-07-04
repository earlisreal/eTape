package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if got := cfg.OpenD.Addr(); got != "127.0.0.1:11111" {
		t.Fatalf("default OpenD addr = %q, want 127.0.0.1:11111", got)
	}
}

func TestLoadOverridesOpenD(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[opend]\nhost = \"10.0.0.5\"\nport = 22222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.OpenD.Addr(); got != "10.0.0.5:22222" {
		t.Fatalf("OpenD addr = %q, want 10.0.0.5:22222", got)
	}
}

func TestLoadMalformedFileErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(p, []byte("[opend\nhost = "), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("Load: expected error for malformed TOML, got nil")
	}
}
