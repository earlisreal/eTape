package openbrowser

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// TestCommandPerOS verifies command dispatches to the right OS-specific
// launcher without ever starting a process (Open itself is not exercised
// here — that would actually pop open a browser window on the machine
// running the test).
func TestCommandPerOS(t *testing.T) {
	cases := []struct {
		goos string
		want string
	}{
		{"windows", "rundll32"},
		{"darwin", "open"},
		{"linux", "xdg-open"},
		{"freebsd", "xdg-open"}, // default fallback for anything unlisted
	}
	for _, c := range cases {
		cmd := command(c.goos, "http://127.0.0.1:8686")
		if got := cmd.Args[0]; got != c.want {
			t.Fatalf("goos=%s: command = %q, want %q", c.goos, got, c.want)
		}
		if got := cmd.Args[len(cmd.Args)-1]; got != "http://127.0.0.1:8686" {
			t.Fatalf("goos=%s: url arg = %q, want it passed through unchanged", c.goos, got)
		}
	}
}

func TestFindChromeFromPath(t *testing.T) {
	want := filepath.Join(t.TempDir(), "chrome.exe")
	got := findChromeWith(
		func(string) (string, error) { return want, nil },
		func(string) (os.FileInfo, error) { t.Fatal("stat should not run for a PATH hit"); return nil, nil },
		func(string) string { return "" },
	)
	if got != want {
		t.Fatalf("findChromeFromPath() = %q, want %q", got, want)
	}
}

func TestFindChromeFromEnvironmentLocations(t *testing.T) {
	locations := []string{"PROGRAMFILES", "PROGRAMFILES(X86)", "LOCALAPPDATA"}
	for _, envName := range locations {
		t.Run(envName, func(t *testing.T) {
			root := t.TempDir()
			want := filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe")
			if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(want, nil, 0o644); err != nil {
				t.Fatal(err)
			}

			got := findChromeWith(
				func(string) (string, error) { return "", errors.New("not on PATH") },
				os.Stat,
				func(name string) string {
					if name == envName {
						return root
					}
					return ""
				},
			)
			if got != want {
				t.Fatalf("findChromeWith() = %q, want %q", got, want)
			}
		})
	}
}

func TestFindChromeAbsent(t *testing.T) {
	root := t.TempDir()
	got := findChromeWith(
		func(string) (string, error) { return "", errors.New("not on PATH") },
		os.Stat,
		func(string) string { return root },
	)
	if got != "" {
		t.Fatalf("findChromeWith() = %q, want no Chrome", got)
	}
}

func TestChromeCommandPreservesExactURL(t *testing.T) {
	chrome := filepath.Join(t.TempDir(), "chrome.exe")
	url := "http://127.0.0.1:8686/?foo=bar"
	cmd := chromeCommand(chrome, url)

	want := []string{chrome, "--app=" + url, "--start-maximized"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("chromeCommand() args = %q, want %q", cmd.Args, want)
	}
}

func TestOwnedChromeCommandUsesPrivateProfile(t *testing.T) {
	chrome := filepath.Join(t.TempDir(), "chrome.exe")
	url := "http://127.0.0.1:8686/?foo=bar"
	profile := filepath.Join(t.TempDir(), "etape-chrome")
	cmd := ownedChromeCommand(chrome, url, profile)

	want := []string{
		chrome,
		"--app=" + url,
		"--start-maximized",
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
	}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("ownedChromeCommand() args = %q, want %q", cmd.Args, want)
	}
}

func TestOwnedBrowserRelaunchArgsPreserveIdentity(t *testing.T) {
	browser := &OwnedBrowser{pid: 1234, startToken: 5678, profileDir: `C:\\Temp\\etape-chrome`}
	want := []string{
		"-owned-browser-pid", "1234",
		"-owned-browser-start", "5678",
		"-owned-browser-profile", `C:\\Temp\\etape-chrome`,
	}
	if got := browser.RelaunchArgs(); !slices.Equal(got, want) {
		t.Fatalf("RelaunchArgs() = %q, want %q", got, want)
	}
}

func TestOpenWindowsFallsBackWhenChromeUnavailable(t *testing.T) {
	url := "http://127.0.0.1:8686/?foo=bar"
	var got *exec.Cmd
	err := open("windows", url, func() string { return "" }, func(cmd *exec.Cmd) error {
		got = cmd
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("open() did not start a fallback command")
	}
	want := []string{"rundll32", "url.dll,FileProtocolHandler", url}
	if !slices.Equal(got.Args, want) {
		t.Fatalf("fallback args = %q, want %q", got.Args, want)
	}
}

func TestOpenWindowsFallsBackWhenChromeStartFails(t *testing.T) {
	chrome := filepath.Join(t.TempDir(), "chrome.exe")
	url := "http://127.0.0.1:8686"
	var commands []*exec.Cmd
	err := open("windows", url, func() string { return chrome }, func(cmd *exec.Cmd) error {
		commands = append(commands, cmd)
		if len(commands) == 1 {
			return errors.New("Chrome failed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 {
		t.Fatalf("started %d commands, want Chrome then fallback", len(commands))
	}
	if got, want := commands[0].Args, []string{chrome, "--app=" + url, "--start-maximized"}; !slices.Equal(got, want) {
		t.Fatalf("Chrome args = %q, want %q", got, want)
	}
	if got, want := commands[1].Args, []string{"rundll32", "url.dll,FileProtocolHandler", url}; !slices.Equal(got, want) {
		t.Fatalf("fallback args = %q, want %q", got, want)
	}
}
