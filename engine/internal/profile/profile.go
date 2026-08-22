// Package profile resolves the files owned by one eTape runtime profile.
//
// Isolated profiles are the default for development, tests, prototypes,
// replay, demo, server, and migration work. The real user profile is
// available only when the caller explicitly opts in.
package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Kind string

const (
	KindDevelopment Kind = "development"
	KindTest        Kind = "test"
	KindPrototype   Kind = "prototype"
	KindReplay      Kind = "replay"
	KindDemo        Kind = "demo"
	KindServer      Kind = "server"
	KindUser        Kind = "user"
	KindMigration   Kind = "migration"
)

var (
	ErrRealProfileOptIn = errors.New("profile: real user profile requires explicit opt-in")
	ErrUnknownKind      = errors.New("profile: unknown runtime profile")
)

// Request describes the runtime profile to resolve. Root and ConfigPath are
// useful for fixtures and tests; when omitted, isolated kinds get a fresh
// temporary root, user requires AllowReal, and migration is isolated unless
// AllowReal is set.
type Request struct {
	Kind       Kind
	HomeDir    string
	Root       string
	ConfigPath string
	LogPath    string
	AllowReal  bool
}

// Paths is the complete default file set for one runtime profile.
type Paths struct {
	Kind            Kind
	Root            string
	ConfigPath      string
	CredentialsPath string
	DBPath          string
	LogPath         string
	Isolated        bool
}

func Resolve(req Request) (Paths, error) {
	kind := req.Kind
	if kind == "" {
		kind = KindDevelopment
	}
	if !knownKind(kind) {
		return Paths{}, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}

	home := req.HomeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil || home == "" {
			return Paths{}, fmt.Errorf("profile: resolve home: %w", err)
		}
	}
	home, err := absolutePath(home)
	if err != nil {
		return Paths{}, fmt.Errorf("profile: home: %w", err)
	}
	realRoot, err := absolutePath(filepath.Join(home, ".eTape"))
	if err != nil {
		return Paths{}, fmt.Errorf("profile: real root: %w", err)
	}
	realOptIn := req.AllowReal && (kind == KindUser || kind == KindMigration)

	root := strings.TrimSpace(req.Root)
	configPath := strings.TrimSpace(req.ConfigPath)
	if root == "" && configPath != "" {
		configPath, err = absolutePath(configPath)
		if err != nil {
			return Paths{}, fmt.Errorf("profile: config: %w", err)
		}
		root = filepath.Dir(configPath)
	}
	if root == "" {
		if kind == KindUser || (kind == KindMigration && req.AllowReal) {
			if !req.AllowReal {
				return Paths{}, ErrRealProfileOptIn
			}
			root = realRoot
		} else {
			root, err = os.MkdirTemp("", "etape-"+string(kind)+"-*")
			if err != nil {
				return Paths{}, fmt.Errorf("profile: create isolated root: %w", err)
			}
		}
	}
	root, err = absolutePath(root)
	if err != nil {
		return Paths{}, fmt.Errorf("profile: root: %w", err)
	}
	if pathsOverlap(realRoot, root) && !realOptIn {
		return Paths{}, ErrRealProfileOptIn
	}

	if configPath == "" {
		configPath = filepath.Join(root, "config.toml")
	} else {
		configPath, err = absolutePath(configPath)
		if err != nil {
			return Paths{}, fmt.Errorf("profile: config: %w", err)
		}
	}
	if !pathWithin(root, configPath) {
		return Paths{}, fmt.Errorf("profile: config path %q is outside root %q", configPath, root)
	}

	logPath := strings.TrimSpace(req.LogPath)
	if logPath == "" {
		logPath = filepath.Join(root, "etape.log")
	} else {
		logPath, err = absolutePath(logPath)
		if err != nil {
			return Paths{}, fmt.Errorf("profile: log: %w", err)
		}
		if pathWithin(realRoot, logPath) && !realOptIn {
			return Paths{}, ErrRealProfileOptIn
		}
	}

	return Paths{
		Kind:            kind,
		Root:            root,
		ConfigPath:      configPath,
		CredentialsPath: filepath.Join(root, "credentials.json"),
		DBPath:          filepath.Join(root, "etape.db"),
		LogPath:         logPath,
		Isolated:        !pathsOverlap(realRoot, root),
	}, nil
}

// ValidateDataPath rejects a store path that escapes an isolated profile.
// Configured paths are intentionally allowed to be outside the profile only
// for an explicitly opted-in user/migration run.
func (p Paths) ValidateDataPath(path string) error {
	if !p.Isolated {
		return nil
	}
	absolute, err := absolutePath(path)
	if err != nil {
		return fmt.Errorf("profile: data path: %w", err)
	}
	if !pathWithin(p.Root, absolute) {
		return fmt.Errorf("profile: data path %q is outside isolated root %q", absolute, p.Root)
	}
	return nil
}

func knownKind(kind Kind) bool {
	switch kind {
	case KindDevelopment, KindTest, KindPrototype, KindReplay, KindDemo, KindServer, KindUser, KindMigration:
		return true
	default:
		return false
	}
}

func absolutePath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	// Resolve the deepest existing ancestor plus any missing suffix so a
	// fixture cannot evade containment through a symlinked parent directory.
	candidate := abs
	var suffix []string
	for {
		if _, err := os.Lstat(candidate); err == nil {
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return abs, nil
		}
		suffix = append(suffix, filepath.Base(candidate))
		candidate = parent
	}
}

func pathWithin(parent, child string) bool {
	parent, child = filepath.Clean(parent), filepath.Clean(child)
	if runtime.GOOS == "windows" {
		parent, child = strings.ToLower(parent), strings.ToLower(child)
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func pathsOverlap(a, b string) bool {
	return pathWithin(a, b) || pathWithin(b, a)
}
