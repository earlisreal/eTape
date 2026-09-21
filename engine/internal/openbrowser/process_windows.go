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

	"golang.org/x/sys/windows"
)

var (
	showWindowAsync     = windows.NewLazySystemDLL("user32.dll").NewProc("ShowWindowAsync")
	setForegroundWindow = windows.NewLazySystemDLL("user32.dll").NewProc("SetForegroundWindow")
	processWindowLookup struct {
		sync.Mutex
		pid   uint32
		hwnd  windows.HWND
		count int
	}
	enumProcessWindow = windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var pid uint32
		_, err := windows.GetWindowThreadProcessId(hwnd, &pid)
		if err == nil && pid == processWindowLookup.pid && windows.IsWindowVisible(hwnd) {
			if processWindowLookup.hwnd == 0 {
				processWindowLookup.hwnd = hwnd
			}
			processWindowLookup.count++
		}
		return 1
	})
)

func visibleProcessWindows(pid uint32) (windows.HWND, int) {
	processWindowLookup.Lock()
	defer processWindowLookup.Unlock()
	processWindowLookup.pid = pid
	processWindowLookup.hwnd = 0
	processWindowLookup.count = 0
	_ = windows.EnumWindows(enumProcessWindow, nil)
	return processWindowLookup.hwnd, processWindowLookup.count
}

func visibleProcessWindow(pid uint32) windows.HWND {
	hwnd, _ := visibleProcessWindows(pid)
	return hwnd
}

func visibleProcessWindowCount(pid uint32) int {
	_, count := visibleProcessWindows(pid)
	return count
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

func restoreOwnedProcessWindows(pid int, startToken uint64, chrome, profile string, specs []WindowSpec) {
	hwnd, ok := waitVisibleProcessWindow(pid, startToken, 10*time.Second)
	if !ok {
		slog.Warn("owned Chrome main window not found while restoring workspaces")
		return
	}
	_, _, _ = showWindowAsync.Call(uintptr(hwnd), windows.SW_MAXIMIZE)
	launched := 0
	for _, spec := range specs {
		cmd := ownedChromeWindowCommand(chrome, spec.URL, profile, spec)
		if err := cmd.Start(); err != nil {
			slog.Warn("restore owned Chrome workspace", "url", spec.URL, "err", err)
			continue
		}
		launched++
		go func() { _ = cmd.Wait() }()
	}
	if expected := launched + 1; expected > 1 {
		waitVisibleProcessWindows(pid, startToken, expected, 2*time.Second)
	}
	// Reuse the original HWND so the mandatory main window finishes in front of
	// restored secondary windows when the OS permits foreground activation.
	_, _, _ = showWindowAsync.Call(uintptr(hwnd), windows.SW_SHOW)
	_, _, _ = setForegroundWindow.Call(uintptr(hwnd))
}

func waitVisibleProcessWindows(pid int, startToken uint64, expected int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		exists, err := ownedProcessExists(pid, startToken)
		if err != nil || !exists || visibleProcessWindowCount(uint32(pid)) >= expected {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func ownedProcessStartTime(pid int) (uint64, error) {
	token, exists, err := ownedProcessState(pid)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, fmt.Errorf("process %d is not running", pid)
	}
	return token, nil
}

func verifyOwnedProcess(pid int, startToken uint64) error {
	token, exists, err := ownedProcessState(pid)
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
	token, exists, err := ownedProcessState(pid)
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
