//go:build windows

package openbrowser

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	createIconFromResource = windows.NewLazySystemDLL("user32.dll").NewProc("CreateIconFromResourceEx")
	destroyIcon            = windows.NewLazySystemDLL("user32.dll").NewProc("DestroyIcon")
	sendIconMessage        = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	getDpiForWindowCall    = windows.NewLazySystemDLL("user32.dll").NewProc("GetDpiForWindow").Call
	getMetricsForDpi       = windows.NewLazySystemDLL("user32.dll").NewProc("GetSystemMetricsForDpi")
	// Keep the result pointer typed until the direct syscall so it can escape the Go stack.
	sendIconMessageCall = func(hwnd, message, kind, icon, flags, timeout uintptr, result *uintptr) (uintptr, uintptr, error) {
		return sendIconMessage.Call(hwnd, message, kind, icon, flags, timeout, uintptr(unsafe.Pointer(result)))
	}
)

const (
	wmGetIcon = 0x007F
	wmSetIcon = 0x0080
	iconSmall = 0
	iconBig   = 1
)

type taskbarIcons struct {
	pid        int
	startToken uint64
	data       []byte
	icons      map[int]uintptr
	touched    map[windows.HWND]struct{}
	shell      *taskbarShell
}

// SetTaskbarIcon sets owned Chrome windows' shell identity, taskbar resource,
// and BIG icons separately from transparent SMALL title icons. Release must run
// before engine exit, including a restart that leaves Chrome alive for adoption.
func (b *OwnedBrowser) SetTaskbarIcon(data []byte, launchArgs []string) (func(), error) {
	if err := verifyOwnedProcess(b.pid, b.startToken); err != nil {
		return nil, err
	}
	icons := &taskbarIcons{
		pid: b.pid, startToken: b.startToken, data: data,
		icons: make(map[int]uintptr), touched: make(map[windows.HWND]struct{}),
	}
	if _, err := icons.icon(32); err != nil {
		return nil, err
	}
	shell, err := newTaskbarShell(launchArgs)
	if err != nil {
		icons.release()
		return nil, err
	}
	icons.shell = shell
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		defer icons.release()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			alive, err := ownedProcessExists(b.pid, b.startToken)
			if err != nil || !alive {
				return
			}
			for _, hwnd := range visibleProcessWindowsCall(uint32(b.pid)) {
				if err := icons.apply(hwnd); err != nil {
					slog.Debug("refresh owned Chrome taskbar icon", "err", err)
				}
			}
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }); <-done }, nil
}

func (icons *taskbarIcons) icon(size int) (uintptr, error) {
	if icon := icons.icons[size]; icon != 0 {
		return icon, nil
	}
	image, err := icoImage(icons.data, size)
	if err != nil {
		return 0, err
	}
	icon, _, err := createIconFromResource.Call(
		uintptr(unsafe.Pointer(&image[0])), uintptr(len(image)), 1, 0x00030000,
		uintptr(size), uintptr(size), 0,
	)
	runtime.KeepAlive(image)
	if icon == 0 {
		if err == nil || errors.Is(err, windows.ERROR_SUCCESS) {
			err = errors.New("CreateIconFromResourceEx returned no icon")
		}
		return 0, fmt.Errorf("create %dpx taskbar icon: %w", size, err)
	}
	icons.icons[size] = icon
	return icon, nil
}

func (icons *taskbarIcons) apply(hwnd windows.HWND) error {
	if !validOwnedWindow(hwnd, uint32(icons.pid)) {
		return nil
	}
	icons.touched[hwnd] = struct{}{}
	if icons.shell != nil {
		if err := icons.shell.apply(hwnd); err != nil {
			return err
		}
	}
	dpi, _, _ := getDpiForWindowCall(uintptr(hwnd))
	if dpi == 0 {
		dpi = 96
	}
	size, _, _ := getMetricsForDpi.Call(11, dpi) // SM_CXICON
	icon, err := icons.icon(int(size))
	if err != nil {
		return err
	}
	current, err := iconMessage(hwnd, wmGetIcon, iconBig, 0)
	if err != nil || current == icon || !validOwnedWindow(hwnd, uint32(icons.pid)) {
		return err
	}
	_, err = iconMessage(hwnd, wmSetIcon, iconBig, icon)
	return err
}

func (icons *taskbarIcons) release() {
	alive, err := ownedProcessExists(icons.pid, icons.startToken)
	if err != nil {
		slog.Warn("verify Chrome while releasing taskbar icons", "err", err)
		return
	}
	if alive {
		for hwnd := range icons.touched {
			if !validOwnedWindow(hwnd, uint32(icons.pid)) {
				continue
			}
			if icons.shell != nil {
				if err := icons.shell.restore(hwnd); err != nil {
					slog.Warn("restore Chrome taskbar properties", "err", err)
				}
			}
			current, err := iconMessage(hwnd, wmGetIcon, iconBig, 0)
			if err == nil {
				for _, icon := range icons.icons {
					if current == icon {
						if validOwnedWindow(hwnd, uint32(icons.pid)) {
							_, err = iconMessage(hwnd, wmSetIcon, iconBig, 0)
						}
						break
					}
				}
			}
			if err != nil {
				// Keep handles alive if a hung window might still reference them.
				slog.Warn("release Chrome taskbar icon", "err", err)
				return
			}
		}
	}
	for _, icon := range icons.icons {
		_, _, _ = destroyIcon.Call(icon)
	}
	clear(icons.icons)
}

func iconMessage(hwnd windows.HWND, message, kind, icon uintptr) (uintptr, error) {
	var result uintptr
	ok, _, err := sendIconMessageCall(uintptr(hwnd), message, kind, icon,
		0x0022, 200, &result) // abort hung/destroyed windows
	if ok != 0 {
		return result, nil
	}
	if err != nil && !errors.Is(err, windows.ERROR_SUCCESS) {
		return 0, err
	}
	return 0, errors.New("window icon message failed or timed out")
}

func icoImage(data []byte, size int) ([]byte, error) {
	if len(data) < 6 || binary.LittleEndian.Uint16(data) != 0 || binary.LittleEndian.Uint16(data[2:]) != 1 {
		return nil, errors.New("invalid ICO header")
	}
	count := int(binary.LittleEndian.Uint16(data[4:]))
	if count == 0 || len(data) < 6+16*count || size <= 0 {
		return nil, errors.New("invalid ICO directory or size")
	}
	var best []byte
	bestSize := 0
	for i := range count {
		entry := data[6+16*i : 6+16*(i+1)]
		width := int(entry[0])
		if width == 0 {
			width = 256
		}
		length := uint64(binary.LittleEndian.Uint32(entry[8:]))
		offset := uint64(binary.LittleEndian.Uint32(entry[12:]))
		if length == 0 || offset < uint64(6+16*count) || offset+length > uint64(len(data)) {
			return nil, errors.New("invalid ICO image bounds")
		}
		if best == nil || (width >= size && (bestSize < size || width < bestSize)) || (width < size && width > bestSize) {
			best, bestSize = data[offset:offset+length], width
		}
	}
	return best, nil
}
