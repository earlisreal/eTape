// Package creds reads eTape's broker credentials from
// ~/.eJournal/credentials.json (shared with eJournal). Values are secrets:
// never log them, never commit them.
package creds

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
