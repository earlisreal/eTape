//go:build windows

package openbrowser

import (
	"os/exec"
	"slices"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestActivateMainWindowRestoresMinimizedAndFlashesWhenForegroundDenied(t *testing.T) {
	originalIconic := isIconicCall
	originalShow := showWindowAsyncCall
	originalForeground := setForegroundWindowCall
	originalFlash := flashWindowExCall
	defer func() {
		isIconicCall = originalIconic
		showWindowAsyncCall = originalShow
		setForegroundWindowCall = originalForeground
		flashWindowExCall = originalFlash
	}()
	var shown []uintptr
	var flashArgs []uintptr
	isIconicCall = func(args ...uintptr) (uintptr, uintptr, error) { return 1, 0, nil }
	showWindowAsyncCall = func(args ...uintptr) (uintptr, uintptr, error) { shown = slices.Clone(args); return 1, 0, nil }
	setForegroundWindowCall = func(args ...uintptr) (uintptr, uintptr, error) { return 0, 0, nil }
	flashWindowExCall = func(args ...uintptr) (uintptr, uintptr, error) {
		flashArgs = slices.Clone(args)
		return 1, 0, nil
	}

	if err := activateMainWindow(42); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(shown, []uintptr{42, windows.SW_RESTORE}) {
		t.Fatalf("ShowWindowAsync args = %v, want minimized restore", shown)
	}
	info := makeFlashWindowInfo(42)
	if info.HWND != windows.HWND(42) || info.Flags != flashwTray|flashwTimerNoFg {
		t.Fatalf("FlashWindowEx info = %+v, want hwnd 42 taskbar-until-foreground", info)
	}
	if len(flashArgs) != 1 || flashArgs[0] == 0 {
		t.Fatalf("FlashWindowEx args = %v, want non-nil FLASHWINFO", flashArgs)
	}
}

func TestValidOwnedWindowRejectsWindowFromAnotherProcess(t *testing.T) {
	originalWindow := isWindowCall
	originalPID := getWindowThreadProcessID
	defer func() { isWindowCall = originalWindow; getWindowThreadProcessID = originalPID }()
	isWindowCall = func(args ...uintptr) (uintptr, uintptr, error) { return 1, 0, nil }
	getWindowThreadProcessID = func(_ windows.HWND, pid *uint32) (uint32, error) { *pid = 999; return 1, nil }

	if validOwnedWindow(42, 123) {
		t.Fatal("validOwnedWindow accepted a handle owned by another process")
	}
}

func TestFocusMainReplacesStaleWindowOnceForConcurrentRequests(t *testing.T) {
	originalState := ownedProcessStateCall
	originalWindows := visibleProcessWindowsCall
	originalStart := startOwnedMainWindow
	originalWait := waitNewMainWindow
	originalWindow := isWindowCall
	originalPID := getWindowThreadProcessID
	originalIconic := isIconicCall
	originalShow := showWindowAsyncCall
	originalForeground := setForegroundWindowCall
	defer func() {
		ownedProcessStateCall = originalState
		visibleProcessWindowsCall = originalWindows
		startOwnedMainWindow = originalStart
		waitNewMainWindow = originalWait
		isWindowCall = originalWindow
		getWindowThreadProcessID = originalPID
		isIconicCall = originalIconic
		showWindowAsyncCall = originalShow
		setForegroundWindowCall = originalForeground
	}()
	ownedProcessStateCall = func(int) (uint64, bool, error) { return 5678, true, nil }
	visibleProcessWindowsCall = func(uint32) []windows.HWND { return []windows.HWND{7} }
	isWindowCall = func(args ...uintptr) (uintptr, uintptr, error) {
		if args[0] == 99 {
			return 1, 0, nil
		}
		return 0, 0, nil
	}
	getWindowThreadProcessID = func(_ windows.HWND, pid *uint32) (uint32, error) { *pid = 1234; return 1, nil }
	isIconicCall = func(args ...uintptr) (uintptr, uintptr, error) { return 0, 0, nil }
	showWindowAsyncCall = func(args ...uintptr) (uintptr, uintptr, error) { return 1, 0, nil }
	setForegroundWindowCall = func(args ...uintptr) (uintptr, uintptr, error) { return 1, 0, nil }
	started := make(chan struct{})
	release := make(chan struct{})
	var starts int
	var startMu sync.Mutex
	startOwnedMainWindow = func(cmd *exec.Cmd) error {
		startMu.Lock()
		starts++
		startMu.Unlock()
		close(started)
		<-release
		return nil
	}
	waitNewMainWindow = func(int, uint64, map[windows.HWND]struct{}, time.Duration) (windows.HWND, bool) {
		return windows.HWND(99), true
	}

	browser := &OwnedBrowser{pid: 1234, startToken: 5678, profileDir: `C:\\Temp\\etape-chrome`, url: "http://127.0.0.1:8686", chrome: "chrome.exe", mainWindow: 42}
	first := make(chan error, 1)
	go func() { first <- browser.FocusMain() }()
	<-started
	second := make(chan error, 1)
	go func() { second <- browser.FocusMain() }()
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	startMu.Lock()
	gotStarts := starts
	startMu.Unlock()
	if gotStarts != 1 {
		t.Fatalf("replacement launches = %d, want exactly one", gotStarts)
	}
	if browser.mainWindow != 99 {
		t.Fatalf("main window = %d, want replacement 99", browser.mainWindow)
	}
}

func TestOwnedProcessCommandTargetsOnlyTheProcessTree(t *testing.T) {
	for _, test := range []struct {
		name  string
		force bool
		want  []string
	}{
		{name: "graceful", want: []string{"taskkill", "/T", "/PID", "1234"}},
		{name: "force", force: true, want: []string{"taskkill", "/T", "/F", "/PID", "1234"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := ownedProcessCommand(1234, test.force)
			if !slices.Equal(cmd.Args, test.want) {
				t.Fatalf("ownedProcessCommand() = %q, want %q", cmd.Args, test.want)
			}
		})
	}
}

func TestSetRestoredWindowBoundsPreservesNegativeMonitorPosition(t *testing.T) {
	original := setWindowPosCall
	defer func() { setWindowPosCall = original }()
	var got []uintptr
	setWindowPosCall = func(args ...uintptr) (uintptr, uintptr, error) {
		got = slices.Clone(args)
		return 1, 0, nil
	}
	spec := WindowSpec{X: -1920, Y: 0, Width: 1920, Height: 1032}

	if err := setRestoredWindowBounds(windows.HWND(42), spec); err != nil {
		t.Fatal(err)
	}
	if len(got) != 7 || int(got[2]) != spec.X || int(got[3]) != spec.Y ||
		int(got[4]) != spec.Width || int(got[5]) != spec.Height {
		t.Fatalf("SetWindowPos args = %v, want saved bounds %+v", got, spec)
	}
}
