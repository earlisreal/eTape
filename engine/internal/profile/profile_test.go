package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultProfilesAreIsolatedFromUserProfile(t *testing.T) {
	home := t.TempDir()
	realRoot := filepath.Join(home, ".eTape")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []Kind{KindDevelopment, KindTest, KindPrototype, KindReplay, KindDemo, KindServer, KindMigration} {
		paths, err := Resolve(Request{Kind: kind, HomeDir: home})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", kind, err)
		}
		if !paths.Isolated || pathWithin(realRoot, paths.Root) {
			t.Fatalf("Resolve(%q) = %+v, want an isolated root outside %q", kind, paths, realRoot)
		}
		if err := paths.ValidateDataPath(paths.DBPath); err != nil {
			t.Fatalf("Resolve(%q) DBPath: %v", kind, err)
		}
	}
}

func TestDefaultProfileIsDevelopmentAndIsolated(t *testing.T) {
	paths, err := Resolve(Request{HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if paths.Kind != KindDevelopment || !paths.Isolated {
		t.Fatalf("default paths = %+v, want isolated development", paths)
	}
}

func TestRealProfileRequiresExplicitOptIn(t *testing.T) {
	home := t.TempDir()
	paths, err := Resolve(Request{Kind: KindMigration, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if !paths.Isolated || pathWithin(filepath.Join(home, ".eTape"), paths.Root) {
		t.Fatalf("migration defaults = %+v, want an isolated root", paths)
	}
	paths, err = Resolve(Request{Kind: KindMigration, HomeDir: home, AllowReal: true})
	if err != nil {
		t.Fatal(err)
	}
	if paths.Isolated || paths.Root != filepath.Join(home, ".eTape") {
		t.Fatalf("opted-in paths = %+v, want the real user root", paths)
	}
}

func TestRealProfileOverrideIsRejectedWithoutOptIn(t *testing.T) {
	home := t.TempDir()
	_, err := Resolve(Request{
		Kind:    KindDevelopment,
		HomeDir: home,
		Root:    filepath.Join(home, ".eTape", "fixture"),
	})
	if !errors.Is(err, ErrRealProfileOptIn) {
		t.Fatalf("real-root override error = %v, want %v", err, ErrRealProfileOptIn)
	}
}

func TestUserHomeAncestorCannotReachRealProfileWithoutOptIn(t *testing.T) {
	home := t.TempDir()
	_, err := Resolve(Request{Kind: KindDevelopment, HomeDir: home, Root: home})
	if !errors.Is(err, ErrRealProfileOptIn) {
		t.Fatalf("user-home root error = %v, want %v", err, ErrRealProfileOptIn)
	}
}

func TestRealProfileOptInIsLimitedToUserAndMigrationKinds(t *testing.T) {
	home := t.TempDir()
	_, err := Resolve(Request{Kind: KindDevelopment, HomeDir: home, Root: filepath.Join(home, ".eTape"), AllowReal: true})
	if !errors.Is(err, ErrRealProfileOptIn) {
		t.Fatalf("development real-root error = %v, want %v", err, ErrRealProfileOptIn)
	}
}

func TestSymlinkedParentCannotEscapeIsolation(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".eTape"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(filepath.Join(home, ".eTape"), link); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	_, err := Resolve(Request{Kind: KindTest, HomeDir: home, Root: filepath.Join(link, "fixture")})
	if !errors.Is(err, ErrRealProfileOptIn) {
		t.Fatalf("symlinked real-root error = %v, want %v", err, ErrRealProfileOptIn)
	}
}

func TestFixtureConfigAndStoreStayInsideRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fixture")
	paths, err := Resolve(Request{Kind: KindTest, HomeDir: t.TempDir(), Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if paths.ConfigPath != filepath.Join(root, "config.toml") || paths.DBPath != filepath.Join(root, "etape.db") {
		t.Fatalf("fixture paths = %+v, want files under %q", paths, root)
	}
	if err := paths.ValidateDataPath(filepath.Join(root, "nested", "fixture.db")); err != nil {
		t.Fatal(err)
	}
	if err := paths.ValidateDataPath(filepath.Join(t.TempDir(), "escaped.db")); err == nil {
		t.Fatal("escaped data path accepted")
	}
}

func TestExplicitConfigDerivesItsIsolatedRoot(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "fixture", "config.toml")
	paths, err := Resolve(Request{Kind: KindTest, HomeDir: home, ConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	if paths.Root != filepath.Dir(configPath) || paths.ConfigPath != configPath || !paths.Isolated {
		t.Fatalf("paths = %+v, want config directory as isolated root", paths)
	}
}

func TestRelativeRootAndConfigResolveTogether(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fixture")
	configPath := filepath.Join("fixture", "config.toml")
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Dir(root)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	paths, err := Resolve(Request{Kind: KindTest, HomeDir: t.TempDir(), Root: filepath.Base(root), ConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	if paths.ConfigPath != filepath.Join(root, "config.toml") {
		t.Fatalf("config path = %q, want %q", paths.ConfigPath, filepath.Join(root, "config.toml"))
	}
}
