// Package openbrowser launches the local UI in the OS browser, so a boot of
// cmd/etape (in particular `-demo`) gives an immediate, no-terminal-typing
// smoke test of the running engine instead of requiring the user to copy an
// address into a browser manually.
package openbrowser

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
)

const (
	ownedChromeProfilePrefix = "etape-chrome-"
	ownedChromeCloseTimeout  = 2 * time.Second
)

// OwnedBrowser is the auto-opened Windows Chrome app and its private profile.
// The process identity is carried across Windows engine restarts so the same
// browser window remains owned until the final clean shutdown.
type OwnedBrowser struct {
	pid        int
	startToken uint64
	profileDir string
	done       <-chan struct{}

	closeOnce sync.Once
	closeErr  error
	cleanOnce sync.Once
}

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

// OpenOwned opens the startup UI in an isolated Windows Chrome app. If Chrome
// is unavailable, it preserves Open's default-browser fallback and returns no
// owned handle. Non-Windows launches remain unchanged.
func OpenOwned(url string) (*OwnedBrowser, error) {
	if runtime.GOOS != "windows" {
		return nil, Open(url)
	}
	chrome := findChrome()
	if chrome == "" {
		return nil, Open(url)
	}
	profileDir, err := os.MkdirTemp("", ownedChromeProfilePrefix)
	if err != nil {
		return nil, openDefault(url)
	}
	cmd := ownedChromeCommand(chrome, url, profileDir)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, openDefault(url)
	}
	startToken, err := ownedProcessStartTime(cmd.Process.Pid)
	if err != nil {
		_ = stopOwnedProcess(cmd.Process.Pid, 0, true)
		_ = os.RemoveAll(profileDir)
		return nil, openDefault(url)
	}
	done := make(chan struct{})
	owned := &OwnedBrowser{pid: cmd.Process.Pid, startToken: startToken, profileDir: profileDir, done: done}
	go func() {
		_ = cmd.Wait()
		close(done)
		owned.cleanup()
	}()
	return owned, nil
}

// AdoptOwned reconnects a Windows engine restart to the startup Chrome app
// launched by the previous engine process.
func AdoptOwned(pid int, startToken uint64, profileDir string) (*OwnedBrowser, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("owned Chrome is supported only on Windows")
	}
	if pid <= 0 || startToken == 0 || profileDir == "" {
		return nil, fmt.Errorf("invalid owned Chrome identity")
	}
	if err := verifyOwnedProcess(pid, startToken); err != nil {
		return nil, err
	}
	return &OwnedBrowser{pid: pid, startToken: startToken, profileDir: profileDir}, nil
}

// RelaunchArgs carries ownership to the replacement Windows engine process.
func (b *OwnedBrowser) RelaunchArgs() []string {
	if b == nil {
		return nil
	}
	return []string{
		"-owned-browser-pid", strconv.Itoa(b.pid),
		"-owned-browser-start", strconv.FormatUint(b.startToken, 10),
		"-owned-browser-profile", b.profileDir,
	}
}

// Close stops only the owned Chrome process tree and removes its temporary
// profile. The identity check prevents a reused PID from touching another
// process.
func (b *OwnedBrowser) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		b.closeErr = b.close()
	})
	return b.closeErr
}

func (b *OwnedBrowser) close() error {
	if done, err := ownedProcessExited(b.pid, b.startToken, b.done); err != nil {
		return err
	} else if !done {
		_ = stopOwnedProcess(b.pid, b.startToken, false)
		if done, err = waitOwnedProcessExit(b.pid, b.startToken, b.done, ownedChromeCloseTimeout); err != nil {
			return err
		} else if !done {
			if err := stopOwnedProcess(b.pid, b.startToken, true); err != nil {
				return err
			}
			if done, err = waitOwnedProcessExit(b.pid, b.startToken, b.done, ownedChromeCloseTimeout); err != nil {
				return err
			} else if !done {
				return fmt.Errorf("owned Chrome process %d did not exit", b.pid)
			}
		}
	}
	b.cleanup()
	return nil
}

func (b *OwnedBrowser) cleanup() {
	if b == nil || b.profileDir == "" {
		return
	}
	b.cleanOnce.Do(func() { _ = os.RemoveAll(b.profileDir) })
}

func ownedProcessExited(pid int, startToken uint64, done <-chan struct{}) (bool, error) {
	if done != nil {
		select {
		case <-done:
			return true, nil
		default:
		}
	}
	exists, err := ownedProcessExists(pid, startToken)
	return !exists, err
}

func waitOwnedProcessExit(pid int, startToken uint64, done <-chan struct{}, timeout time.Duration) (bool, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		exited, err := ownedProcessExited(pid, startToken, done)
		if err != nil || exited {
			return exited, err
		}
		select {
		case <-timer.C:
			return false, nil
		case <-ticker.C:
		}
	}
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

func openDefault(url string) error {
	return command(runtime.GOOS, url).Start()
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
	return exec.Command(chrome, "--app="+url, "--start-maximized")
}

func ownedChromeCommand(chrome, url, profileDir string) *exec.Cmd {
	return exec.Command(chrome,
		"--app="+url,
		"--start-maximized",
		"--user-data-dir="+profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	)
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
