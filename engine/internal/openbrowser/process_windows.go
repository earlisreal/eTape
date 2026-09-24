//go:build windows

package openbrowser

import (
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	showWindowAsync           = windows.NewLazySystemDLL("user32.dll").NewProc("ShowWindowAsync")
	showWindowAsyncCall       = showWindowAsync.Call
	isWindow                  = windows.NewLazySystemDLL("user32.dll").NewProc("IsWindow")
	isWindowCall              = isWindow.Call
	isIconic                  = windows.NewLazySystemDLL("user32.dll").NewProc("IsIconic")
	isIconicCall              = isIconic.Call
	setForegroundWindow       = windows.NewLazySystemDLL("user32.dll").NewProc("SetForegroundWindow")
	setForegroundWindowCall   = setForegroundWindow.Call
	flashWindowEx             = windows.NewLazySystemDLL("user32.dll").NewProc("FlashWindowEx")
	flashWindowExCall         = flashWindowEx.Call
	setWindowPos              = windows.NewLazySystemDLL("user32.dll").NewProc("SetWindowPos")
	setWindowPosCall          = setWindowPos.Call
	getWindowThreadProcessID  = windows.GetWindowThreadProcessId
	ownedProcessStateCall     = ownedProcessState
	visibleProcessWindowsCall = visibleProcessWindows
	startOwnedMainWindow      = func(cmd *exec.Cmd) error { return cmd.Start() }
	waitNewMainWindow         = waitNewVisibleProcessWindow
	processWindowLookup       struct {
		sync.Mutex
		pid     uint32
		handles []windows.HWND
	}
	enumProcessWindow = windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var pid uint32
		_, err := windows.GetWindowThreadProcessId(hwnd, &pid)
		if err == nil && pid == processWindowLookup.pid && windows.IsWindowVisible(hwnd) {
			processWindowLookup.handles = append(processWindowLookup.handles, hwnd)
		}
		return 1
	})
)

const (
	flashwTray      = 0x00000002
	flashwTimerNoFg = 0x0000000C
)

type flashWindowInfo struct {
	Size    uint32
	HWND    windows.HWND
	Flags   uint32
	Count   uint32
	Timeout uint32
}

func visibleProcessWindows(pid uint32) []windows.HWND {
	processWindowLookup.Lock()
	defer processWindowLookup.Unlock()
	processWindowLookup.pid = pid
	processWindowLookup.handles = processWindowLookup.handles[:0]
	_ = windows.EnumWindows(enumProcessWindow, nil)
	return append([]windows.HWND(nil), processWindowLookup.handles...)
}

func visibleProcessWindow(pid uint32) windows.HWND {
	handles := visibleProcessWindows(pid)
	if len(handles) == 0 {
		return 0
	}
	return handles[0]
}

func waitVisibleProcessWindow(pid int, startToken uint64, timeout time.Duration) (windows.HWND, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		exists, err := ownedProcessExists(pid, startToken)
		if err != nil || !exists {
			return 0, false
		}
		if hwnd := visibleProcessWindow(uint32(pid)); hwnd != 0 {
			return hwnd, true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return 0, false
}

func restoreOwnedProcessWindows(pid int, startToken uint64, chrome, profile string, specs []WindowSpec, onMain func(uintptr)) {
	hwnd, ok := waitVisibleProcessWindow(pid, startToken, 10*time.Second)
	if !ok {
		slog.Warn("owned Chrome main window not found while restoring workspaces")
		return
	}
	onMain(uintptr(hwnd))
	_, _, _ = showWindowAsyncCall(uintptr(hwnd), windows.SW_MAXIMIZE)
	for _, spec := range specs {
		before := make(map[windows.HWND]struct{})
		for _, existing := range visibleProcessWindows(uint32(pid)) {
			before[existing] = struct{}{}
		}
		cmd := ownedChromeWindowCommand(chrome, spec.URL, profile, spec)
		if err := cmd.Start(); err != nil {
			slog.Warn("restore owned Chrome workspace", "url", spec.URL, "err", err)
			continue
		}
		go func() { _ = cmd.Wait() }()
		restored, ok := waitNewVisibleProcessWindow(pid, startToken, before, 10*time.Second)
		if !ok {
			slog.Warn("restored Chrome workspace window not found", "url", spec.URL)
			continue
		}
		_, _, _ = showWindowAsyncCall(uintptr(restored), windows.SW_RESTORE)
		if err := setRestoredWindowBounds(restored, spec); err != nil {
			slog.Warn("place restored Chrome workspace", "url", spec.URL, "err", err)
		}
	}
	// Reuse the original HWND so the mandatory main window finishes in front of
	// restored secondary windows when the OS permits foreground activation.
	_, _, _ = showWindowAsyncCall(uintptr(hwnd), windows.SW_SHOW)
	_, _, _ = setForegroundWindowCall(uintptr(hwnd))
}

func focusOwnedMainLocked(browser *OwnedBrowser) error {
	if err := verifyOwnedProcess(browser.pid, browser.startToken); err != nil {
		return err
	}
	hwnd := windows.HWND(browser.mainWindow)
	if !validOwnedWindow(hwnd, uint32(browser.pid)) {
		var err error
		hwnd, err = replaceMainWindowLocked(browser)
		if err != nil {
			return err
		}
		browser.mainWindow = uintptr(hwnd)
	}
	if err := activateMainWindow(hwnd); err != nil {
		return err
	}
	if validOwnedWindow(hwnd, uint32(browser.pid)) {
		return nil
	}
	hwnd, err := replaceMainWindowLocked(browser)
	if err != nil {
		return err
	}
	browser.mainWindow = uintptr(hwnd)
	return activateMainWindow(hwnd)
}

func validOwnedWindow(hwnd windows.HWND, pid uint32) bool {
	if hwnd == 0 {
		return false
	}
	if ok, _, _ := isWindowCall(uintptr(hwnd)); ok == 0 {
		return false
	}
	var owner uint32
	if _, err := getWindowThreadProcessID(hwnd, &owner); err != nil {
		return false
	}
	return owner == pid
}

func replaceMainWindowLocked(browser *OwnedBrowser) (windows.HWND, error) {
	chrome := browser.chrome
	if chrome == "" {
		chrome = findChrome()
	}
	if chrome == "" {
		return 0, errors.New("owned Chrome executable unavailable")
	}
	before := make(map[windows.HWND]struct{})
	for _, hwnd := range visibleProcessWindowsCall(uint32(browser.pid)) {
		before[hwnd] = struct{}{}
	}
	cmd := ownedChromeCommand(chrome, browser.url, browser.profileDir)
	if err := startOwnedMainWindow(cmd); err != nil {
		return 0, fmt.Errorf("start owned main workspace: %w", err)
	}
	if cmd.Process != nil {
		go func() { _ = cmd.Wait() }()
	}
	hwnd, ok := waitNewMainWindow(browser.pid, browser.startToken, before, 10*time.Second)
	if !ok {
		return 0, errors.New("owned Chrome main window did not appear")
	}
	return hwnd, nil
}

func activateMainWindow(hwnd windows.HWND) error {
	show := uintptr(windows.SW_SHOW)
	if iconic, _, _ := isIconicCall(uintptr(hwnd)); iconic != 0 {
		show = uintptr(windows.SW_RESTORE)
	}
	_, _, _ = showWindowAsyncCall(uintptr(hwnd), show)
	if foreground, _, _ := setForegroundWindowCall(uintptr(hwnd)); foreground == 0 {
		info := makeFlashWindowInfo(hwnd)
		_, _, _ = flashWindowExCall(uintptr(unsafe.Pointer(&info)))
	}
	return nil
}

func makeFlashWindowInfo(hwnd windows.HWND) flashWindowInfo {
	return flashWindowInfo{
		Size:    uint32(unsafe.Sizeof(flashWindowInfo{})),
		HWND:    hwnd,
		Flags:   flashwTray | flashwTimerNoFg,
		Count:   0,
		Timeout: 0,
	}
}

func waitNewVisibleProcessWindow(pid int, startToken uint64, before map[windows.HWND]struct{}, timeout time.Duration) (windows.HWND, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		exists, err := ownedProcessExists(pid, startToken)
		if err != nil || !exists {
			return 0, false
		}
		for _, hwnd := range visibleProcessWindows(uint32(pid)) {
			if _, existed := before[hwnd]; !existed {
				return hwnd, true
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return 0, false
}

func setRestoredWindowBounds(hwnd windows.HWND, spec WindowSpec) error {
	const swpNoZOrder = 0x0004
	const swpNoActivate = 0x0010
	ok, _, err := setWindowPosCall(
		uintptr(hwnd), 0, uintptr(spec.X), uintptr(spec.Y), uintptr(spec.Width), uintptr(spec.Height),
		swpNoZOrder|swpNoActivate,
	)
	if ok != 0 {
		return nil
	}
	if err != nil && !errors.Is(err, windows.ERROR_SUCCESS) {
		return err
	}
	return errors.New("SetWindowPos returned false")
}

func ownedProcessStartTime(pid int) (uint64, error) {
	token, exists, err := ownedProcessStateCall(pid)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, fmt.Errorf("process %d is not running", pid)
	}
	return token, nil
}

func verifyOwnedProcess(pid int, startToken uint64) error {
	token, exists, err := ownedProcessStateCall(pid)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("owned Chrome process %d is no longer running", pid)
	}
	if token != startToken {
		return fmt.Errorf("owned Chrome PID %d was reused", pid)
	}
	return nil
}

func ownedProcessExists(pid int, startToken uint64) (bool, error) {
	token, exists, err := ownedProcessStateCall(pid)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if token != startToken {
		return false, fmt.Errorf("owned Chrome PID %d was reused", pid)
	}
	return true, nil
}

func stopOwnedProcess(pid int, startToken uint64, force bool) error {
	if startToken != 0 {
		exists, err := ownedProcessExists(pid, startToken)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
	}
	return ownedProcessCommand(pid, force).Run()
}

func ownedProcessCommand(pid int, force bool) *exec.Cmd {
	args := []string{"/T"}
	if force {
		args = append(args, "/F")
	}
	args = append(args, "/PID", strconv.Itoa(pid))
	return exec.Command("taskkill", args...)
}

func ownedProcessState(pid int) (uint64, bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return 0, false, nil
		}
		return 0, false, err
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, false, err
	}
	return uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime), true, nil
}
