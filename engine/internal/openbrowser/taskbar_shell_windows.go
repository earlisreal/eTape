//go:build windows

package openbrowser

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const taskbarAppID = "eTape.ChromeApp"

var (
	getTaskbarStore   = windows.NewLazySystemDLL("shell32.dll").NewProc("SHGetPropertyStoreForWindow")
	clearTaskbarValue = windows.NewLazySystemDLL("ole32.dll").NewProc("PropVariantClear")
	taskbarStoreIID   = windows.GUID{Data1: 0x886d8eeb, Data2: 0x8cf2, Data3: 0x4446, Data4: [8]byte{0x8d, 0x02, 0xcd, 0xba, 0x1d, 0xbd, 0xcf, 0x99}}
	taskbarKeyFormat  = windows.GUID{Data1: 0x9f4c2855, Data2: 0x9f79, Data3: 0x4b39, Data4: [8]byte{0xa8, 0xd0, 0xe1, 0xd4, 0x2d, 0xe1, 0xd5, 0xf3}}
	taskbarExecutable = os.Executable
)

type taskbarShell struct {
	values   [4]string // relaunch command, icon resource, display name, AppUserModelID
	previous map[windows.HWND][4]string
}

func newTaskbarShell(launchArgs []string) (*taskbarShell, error) {
	exe, err := taskbarExecutable()
	if err != nil {
		return nil, err
	}
	return &taskbarShell{
		// Explorer displays the PE resource reliably; a standalone ICO can fall back to a generic file icon.
		values:   [4]string{windows.ComposeCommandLine(append([]string{exe}, launchArgs...)), exe + ",0", "eTape", taskbarAppID},
		previous: make(map[windows.HWND][4]string),
	}, nil
}

func (shell *taskbarShell) apply(hwnd windows.HWND) error {
	store, release, err := openTaskbarStore(hwnd)
	if err != nil {
		return err
	}
	defer release()
	var current [4]string
	for i := range current {
		current[i], err = store.get(uint32(i + 2))
		if err != nil {
			return err
		}
	}
	if _, found := shell.previous[hwnd]; !found {
		shell.previous[hwnd] = current
	}
	// Set the identity last so Explorer creates the group with its icon already present.
	for i, value := range shell.values {
		if current[i] != value {
			if err := store.set(uint32(i+2), value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (shell *taskbarShell) restore(hwnd windows.HWND) error {
	previous, found := shell.previous[hwnd]
	if !found {
		return nil
	}
	store, release, err := openTaskbarStore(hwnd)
	if err != nil {
		return err
	}
	defer release()
	for i, value := range shell.values {
		current, err := store.get(uint32(i + 2))
		if err != nil {
			return err
		}
		if current == value {
			if err := store.set(uint32(i+2), previous[i]); err != nil {
				return err
			}
		}
	}
	delete(shell.previous, hwnd)
	return nil
}

type taskbarStore struct{ vtable *[8]uintptr }
type taskbarPropertyKey struct {
	format windows.GUID
	id     uint32
}
type taskbarPropertyValue struct {
	kind     uint16
	reserved [3]uint16
	text     *uint16
	extra    uintptr
}

func openTaskbarStore(hwnd windows.HWND) (*taskbarStore, func(), error) {
	runtime.LockOSThread()
	if err := windows.CoInitializeEx(0, 0); err != nil && err != windows.Errno(1) { // S_FALSE is also success.
		runtime.UnlockOSThread()
		return nil, nil, fmt.Errorf("initialize taskbar COM: %w", err)
	}
	var store *taskbarStore
	hr, _, _ := getTaskbarStore.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&taskbarStoreIID)), uintptr(unsafe.Pointer(&store)))
	if int32(hr) < 0 {
		windows.CoUninitialize()
		runtime.UnlockOSThread()
		return nil, nil, fmt.Errorf("open taskbar property store: HRESULT %#x", hr)
	}
	return store, func() {
		_, _, _ = syscall.SyscallN(store.vtable[2], uintptr(unsafe.Pointer(store)))
		windows.CoUninitialize()
		runtime.UnlockOSThread()
	}, nil
}

func (store *taskbarStore) get(id uint32) (string, error) {
	key := taskbarPropertyKey{taskbarKeyFormat, id}
	var value taskbarPropertyValue
	hr, _, _ := syscall.SyscallN(store.vtable[5], uintptr(unsafe.Pointer(store)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&value)))
	defer func() { _, _, _ = clearTaskbarValue.Call(uintptr(unsafe.Pointer(&value))) }()
	if int32(hr) < 0 {
		return "", fmt.Errorf("read taskbar property %d: HRESULT %#x", id, hr)
	}
	if value.kind == 0 {
		return "", nil
	}
	if value.kind != 31 {
		return "", fmt.Errorf("taskbar property %d has type %d", id, value.kind)
	}
	return windows.UTF16PtrToString(value.text), nil
}

func (store *taskbarStore) set(id uint32, text string) error {
	key := taskbarPropertyKey{taskbarKeyFormat, id}
	var value taskbarPropertyValue
	if text != "" {
		ptr, err := windows.UTF16PtrFromString(text)
		if err != nil {
			return err
		}
		value.kind, value.text = 31, ptr
	}
	hr, _, _ := syscall.SyscallN(store.vtable[6], uintptr(unsafe.Pointer(store)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&value)))
	runtime.KeepAlive(value)
	if int32(hr) < 0 {
		return fmt.Errorf("set taskbar property %d: HRESULT %#x", id, hr)
	}
	return nil
}
