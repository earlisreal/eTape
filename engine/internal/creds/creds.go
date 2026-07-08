// Package creds reads eTape's broker credentials from
// ~/.eJournal/credentials.json (shared with eJournal). Values are secrets:
// never log them, never commit them.
package creds

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/earlisreal/eTape/engine/internal/atomicfile"
)

type Pair struct {
	KeyID     string `json:"keyId"`
	SecretKey string `json:"secretKey"`
}

type File map[string]Pair

// DefaultPath is ~/.eJournal/credentials.json.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "credentials.json"
	}
	return filepath.Join(home, ".eJournal", "credentials.json")
}

// Load reads and parses the credentials file. A missing file IS an error here
// (unlike bootstrap config): an adapter asked for creds because it needs them.
func Load(path string) (File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("creds: %w", err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("creds %s: %w", path, err)
	}
	return f, nil
}

func (f File) Get(key string) (Pair, error) {
	p, ok := f[key]
	if !ok || p.KeyID == "" || p.SecretKey == "" {
		return Pair{}, fmt.Errorf("creds: no usable %q entry", key)
	}
	return p, nil
}

// readRaw reads the file as an order-agnostic name→raw-entry map, preserving
// each entry's exact JSON bytes. A missing file yields an empty map.
func readRaw(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("creds: %w", err)
	}
	m := map[string]json.RawMessage{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("creds %s: %w", path, err)
		}
	}
	return m, nil
}

func writeRaw(path string, m map[string]json.RawMessage) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("creds: marshal: %w", err)
	}
	return atomicfile.Write(path, b, 0o600)
}

// Put upserts one credential entry, preserving every sibling entry
// byte-for-byte (the file is shared with eJournal). The file is created 0600 if
// absent. Secrets are never logged.
func Put(path, name, keyID, secretKey string) error {
	m, err := readRaw(path)
	if err != nil {
		return err
	}
	entry, err := json.Marshal(Pair{KeyID: keyID, SecretKey: secretKey})
	if err != nil {
		return fmt.Errorf("creds: marshal entry: %w", err)
	}
	m[name] = entry
	return writeRaw(path, m)
}

// Delete removes one entry, preserving all siblings. A missing entry or missing
// file is a no-op success.
func Delete(path, name string) error {
	m, err := readRaw(path)
	if err != nil {
		return err
	}
	delete(m, name)
	return writeRaw(path, m)
}

// Keys returns the sorted credential names. A missing file yields nil, nil.
func Keys(path string) ([]string, error) {
	m, err := readRaw(path)
	if err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, nil
	}
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks, nil
}
