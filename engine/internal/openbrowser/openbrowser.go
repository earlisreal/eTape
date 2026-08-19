// Package openbrowser launches the local UI in the OS browser, so a boot of
// cmd/etape (in particular `-demo`) gives an immediate, no-terminal-typing
// smoke test of the running engine instead of requiring the user to copy an
// address into a browser manually.
package openbrowser

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Open launches url in a detached browser process. Windows tries Chrome
// application mode first and falls back to the OS default handler; other
// platforms use their existing default-browser command. It returns as soon
// as the launcher process has been spawned (via exec.Cmd.Start, not Run) — it
// never waits for the browser itself to exit. Errors are expected to be
// non-fatal to the caller: a machine without a browser handler configured
// should still get a running engine.
func Open(url string) error {
	return open(runtime.GOOS, url, findChrome, (*exec.Cmd).Start)
}

func open(goos, url string, discoverChrome func() string, start func(*exec.Cmd) error) error {
	if goos == "windows" {
		if chrome := discoverChrome(); chrome != "" {
			if err := start(chromeCommand(chrome, url)); err == nil {
				return nil
			}
		}
	}
	return start(command(goos, url))
}

// command builds the OS-specific default-browser command for goos. Split out
// from Open so it can be unit-tested without actually spawning a browser
// process.
func command(goos, url string) *exec.Cmd {
	switch goos {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		return exec.Command("open", url)
	default: // linux and everything else
		return exec.Command("xdg-open", url)
	}
}

func chromeCommand(chrome, url string) *exec.Cmd {
	return exec.Command(chrome, "--app="+url)
}

func findChrome() string {
	return findChromeWith(exec.LookPath, os.Stat, os.Getenv)
}

func findChromeWith(
	lookPath func(string) (string, error),
	stat func(string) (os.FileInfo, error),
	getenv func(string) string,
) string {
	if path, err := lookPath("chrome.exe"); err == nil && path != "" {
		return path
	}

	for _, envName := range []string{"PROGRAMFILES", "PROGRAMFILES(X86)", "LOCALAPPDATA"} {
		root := getenv(envName)
		if root == "" {
			continue
		}
		path := filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe")
		if info, err := stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
