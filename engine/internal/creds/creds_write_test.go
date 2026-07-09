package creds

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPutPreservesSiblingsByteForByte(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	// A sibling entry Put doesn't touch, with an extra unknown field —
	// proves the raw-map round-trip preserves entries byte-for-byte.
	seed := `{"otherAppEntry":{"keyId":"K","secretKey":"S","futureField":42}}`
	if err := os.WriteFile(p, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Put(p, "alpaca", "AK", "AS"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	var raw map[string]json.RawMessage
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["otherAppEntry"]) != `{"keyId":"K","secretKey":"S","futureField":42}` {
		t.Fatalf("sibling mutated: %s", raw["otherAppEntry"])
	}
	got, _ := Load(p)
	if p := got["alpaca"]; p.KeyID != "AK" || p.SecretKey != "AS" {
		t.Fatalf("put entry wrong: %+v", p)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", fi.Mode().Perm())
	}
}

func TestPutReplacesOnlyTarget(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	_ = os.WriteFile(p, []byte(`{"alpaca":{"keyId":"old","secretKey":"old"}}`), 0o600)
	if err := Put(p, "alpaca", "new", "new"); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(p)
	if got["alpaca"].KeyID != "new" {
		t.Fatalf("replace failed: %+v", got["alpaca"])
	}
}

func TestPutCreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	if err := Put(p, "alpaca", "AK", "AS"); err != nil {
		t.Fatalf("Put on missing file: %v", err)
	}
	got, _ := Load(p)
	if got["alpaca"].KeyID != "AK" {
		t.Fatalf("not created: %+v", got)
	}
}

// TestPutCreatesMissingParentDir pins the writeRaw MkdirAll: the first Put
// for a fresh install must succeed even when the parent directory (e.g.
// ~/.eTape) doesn't exist yet, not just the file.
func TestPutCreatesMissingParentDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nested", "does-not-exist-yet", "credentials.json")
	if err := Put(p, "alpaca", "AK", "AS"); err != nil {
		t.Fatalf("Put with missing parent dir: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load after Put: %v", err)
	}
	if got["alpaca"].KeyID != "AK" {
		t.Fatalf("not created: %+v", got)
	}
}

func TestDeleteRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	_ = os.WriteFile(p, []byte(`{"a":{"keyId":"1","secretKey":"1"},"b":{"keyId":"2","secretKey":"2"}}`), 0o600)
	if err := Delete(p, "a"); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(p)
	if _, ok := got["a"]; ok {
		t.Fatalf("a not deleted")
	}
	if _, ok := got["b"]; !ok {
		t.Fatalf("b wrongly removed")
	}
}

func TestKeysSortedAndMissingFileEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials.json")
	if ks, err := Keys(p); err != nil || len(ks) != 0 {
		t.Fatalf("missing file: %v %v", ks, err)
	}
	_ = os.WriteFile(p, []byte(`{"z":{"keyId":"1","secretKey":"1"},"a":{"keyId":"2","secretKey":"2"}}`), 0o600)
	ks, err := Keys(p)
	if err != nil || len(ks) != 2 || ks[0] != "a" || ks[1] != "z" {
		t.Fatalf("keys: %v %v", ks, err)
	}
}
