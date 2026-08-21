// Package openbrowser launches the local UI in the OS browser, so a boot of
// cmd/etape (in particular `-demo`) gives an immediate, no-terminal-typing
// smoke test of the running engine instead of requiring the user to copy an
// address into a browser manually.
package openbrowser

import (
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ownedChromeProfilePrefix = "etape-chrome-"
	devToolsHTTPTimeout      = time.Second
)

// OwnedBrowser is the auto-opened Windows Chrome app and its private profile.
// The process identity is carried across Windows engine restarts so the same
// browser window remains owned until the final clean shutdown.
type OwnedBrowser struct {
	pid        int
	startToken uint64
	profileDir string
	url        string
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
	owned := &OwnedBrowser{pid: cmd.Process.Pid, startToken: startToken, profileDir: profileDir, url: url, done: done}
	go func() {
		_ = cmd.Wait()
		close(done)
		owned.cleanup()
	}()
	return owned, nil
}

// AdoptOwned reconnects a Windows engine restart to the startup Chrome app
// launched by the previous engine process.
func AdoptOwned(pid int, startToken uint64, profileDir, url string) (*OwnedBrowser, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("owned Chrome is supported only on Windows")
	}
	if pid <= 0 || startToken == 0 || profileDir == "" || url == "" {
		return nil, fmt.Errorf("invalid owned Chrome identity")
	}
	if err := verifyOwnedProcess(pid, startToken); err != nil {
		return nil, err
	}
	return &OwnedBrowser{pid: pid, startToken: startToken, profileDir: profileDir, url: url}, nil
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
		"-owned-browser-url", b.url,
	}
}

// Close closes only the startup page. Child workspace pages share the private
// Chrome process and must remain open; the process and profile clean up after
// the last owned page is closed.
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
		port, err := devToolsPort(b.profileDir)
		if err != nil {
			return fmt.Errorf("read owned Chrome DevTools port: %w", err)
		}
		targets, err := devToolsTargets(port)
		if err != nil {
			return fmt.Errorf("list owned Chrome pages: %w", err)
		}
		target, ok := ownedDevToolsTarget(targets, b.url)
		if !ok {
			return fmt.Errorf("owned Chrome startup page %q not found", b.url)
		}
		if err := closeDevToolsTarget(port, target.ID); err != nil {
			return fmt.Errorf("close owned Chrome startup page: %w", err)
		}
	}
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

type devToolsTarget struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

func ownedDevToolsTarget(targets []devToolsTarget, startupURL string) (devToolsTarget, bool) {
	for _, target := range targets {
		if target.Type == "page" && sameDevToolsURL(target.URL, startupURL) {
			return target, true
		}
	}
	return devToolsTarget{}, false
}

func sameDevToolsURL(left, right string) bool {
	a, err := neturl.Parse(left)
	if err != nil {
		return false
	}
	b, err := neturl.Parse(right)
	if err != nil {
		return false
	}
	if a.Path == "" {
		a.Path = "/"
	}
	if b.Path == "" {
		b.Path = "/"
	}
	return a.Scheme == b.Scheme && a.Host == b.Host && a.Path == b.Path && a.RawQuery == b.RawQuery
}

func devToolsPort(profileDir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(profileDir, "DevToolsActivePort"))
	if err != nil {
		return 0, err
	}
	portText := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", portText)
	}
	return port, nil
}

func devToolsTargets(port int) ([]devToolsTarget, error) {
	client := &http.Client{Timeout: devToolsHTTPTimeout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	var targets []devToolsTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func closeDevToolsTarget(port int, targetID string) error {
	client := &http.Client{Timeout: devToolsHTTPTimeout}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/json/close/%s", port, neturl.PathEscape(targetID))
	resp, err := client.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return nil
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
		"--remote-debugging-port=0",
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
