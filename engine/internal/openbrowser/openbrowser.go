// Package openbrowser launches the OS default browser at a URL, so a boot
// of cmd/etape (in particular `-demo`) gives an immediate, no-terminal-
// typing smoke test of the running engine instead of requiring the user to
// copy an address into a browser manually.
package openbrowser

import (
	"os/exec"
	"runtime"
)

// Open launches the OS default browser at url in a detached process. It
// returns as soon as the launcher process has been spawned (via
// exec.Cmd.Start, not Run) — it never waits for the browser itself to exit.
// Errors are expected to be non-fatal to the caller: a machine without a
// default browser handler configured (e.g. a bare CI/container image)
// should still get a running engine.
func Open(url string) error {
	return command(runtime.GOOS, url).Start()
}

// command builds the OS-specific browser-launch command for goos. Split out
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
