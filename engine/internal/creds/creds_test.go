package creds_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/creds"
)

func TestLoadAndGet(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(p, []byte(`{"tradeZero":{"keyId":"K1","secretKey":"S1"},"alpaca":{"keyId":"K2","secretKey":"S2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := creds.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	tz, err := f.Get("tradeZero")
	if err != nil || tz.KeyID != "K1" || tz.SecretKey != "S1" {
		t.Fatalf("tradeZero pair wrong: %+v err=%v", tz, err)
	}
	if _, err := f.Get("nope"); err == nil {
		t.Fatal("Get of missing key should error")
	}
}

func TestLoad_MissingFileErrors(t *testing.T) {
	if _, err := creds.Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("missing credentials file should error")
	}
}
