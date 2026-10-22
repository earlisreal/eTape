//go:build windows

package openbrowser

import (
	"bytes"
	"encoding/binary"
	"image"
	_ "image/png"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func taskbarTestData(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../cmd/etape/assets/etape.ico")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testTaskbarIcons(t *testing.T, data []byte) *taskbarIcons {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	pid := int(windows.GetCurrentProcessId())
	token, err := ownedProcessStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	shell, err := newTaskbarShell(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &taskbarIcons{pid: pid, startToken: token, data: data,
		icons: make(map[int]uintptr), touched: make(map[windows.HWND]struct{}), shell: shell}
}

func readTaskbarProperty(t *testing.T, hwnd windows.HWND, id uint32) string {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, 0); err != nil && err != windows.Errno(1) {
		t.Fatal(err)
	}
	defer windows.CoUninitialize()
	iid, _ := windows.GUIDFromString("{886D8EEB-8CF2-4446-8D02-CDBA1DBDCF99}")
	var store *struct{ Vtable *[8]uintptr }
	hr, _, _ := windows.NewLazySystemDLL("shell32.dll").NewProc("SHGetPropertyStoreForWindow").Call(
		uintptr(hwnd), uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&store)))
	if int32(hr) < 0 {
		t.Fatalf("read taskbar property store: HRESULT %#x", hr)
	}
	defer func() { _, _, _ = syscall.SyscallN(store.Vtable[2], uintptr(unsafe.Pointer(store))) }()
	format, _ := windows.GUIDFromString("{9F4C2855-9F79-4B39-A8D0-E1D42DE1D5F3}")
	key := struct {
		Format windows.GUID
		ID     uint32
	}{format, id}
	var value struct {
		Type     uint16
		Reserved [3]uint16
		Text     *uint16
		Extra    uintptr
	}
	hr, _, _ = syscall.SyscallN(store.Vtable[5], uintptr(unsafe.Pointer(store)), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&value)))
	defer func() {
		_, _, _ = windows.NewLazySystemDLL("ole32.dll").NewProc("PropVariantClear").Call(uintptr(unsafe.Pointer(&value)))
	}()
	if int32(hr) < 0 {
		t.Fatalf("read taskbar property %d: HRESULT %#x", id, hr)
	}
	if value.Type == 0 {
		return ""
	}
	if value.Type != 31 {
		t.Fatalf("taskbar property %d has unexpected type %d", id, value.Type)
	}
	return windows.UTF16PtrToString(value.Text)
}

func TestTaskbarIconNativeSizes(t *testing.T) {
	data := taskbarTestData(t)
	for _, size := range []int{16, 20, 24, 32, 40, 48, 64, 96, 128, 256} {
		frame, err := icoImage(data, size)
		if err != nil {
			t.Fatal(err)
		}
		decoded, _, err := image.Decode(bytes.NewReader(frame))
		if err != nil {
			// ICO entries may be PNGs or BMP DIBs (whose height includes the mask).
			if len(frame) < 40 || binary.LittleEndian.Uint32(frame) != 40 ||
				int(binary.LittleEndian.Uint32(frame[4:])) != size || int(binary.LittleEndian.Uint32(frame[8:])) != size*2 {
				t.Fatalf("%dpx ICO entry is not a native PNG or DIB", size)
			}
		} else if decoded.Bounds().Dx() != size || decoded.Bounds().Dy() != size {
			t.Fatalf("%dpx icon was sourced from %v; need native DPI entries", size, decoded.Bounds())
		}
	}
	if _, err := icoImage(data, 0); err == nil {
		t.Fatal("accepted zero icon size")
	}
	for _, bad := range [][]byte{nil, data[:5], append([]byte(nil), data...)} {
		if len(bad) == len(data) {
			binary.LittleEndian.PutUint32(bad[18:], uint32(len(data)))
		}
		if _, err := icoImage(bad, 32); err == nil {
			t.Fatal("accepted invalid ICO image bounds")
		}
	}
}

func TestTaskbarIconPreservesSmallIconAndHandlesChromeReplacement(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		t.Fatal(err)
	}
	handle, _, err := windows.NewLazySystemDLL("user32.dll").NewProc("CreateWindowExW").Call(
		0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 100, 100, 0, 0, 0, 0)
	if handle == 0 {
		t.Fatal(err)
	}
	hwnd := windows.HWND(handle)
	icons := testTaskbarIcons(t, taskbarTestData(t))
	title := testTaskbarIcons(t, taskbarTestData(t))
	defer func() {
		_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("DestroyWindow").Call(handle)
		icons.release()
		title.release()
	}()
	small, err := title.icon(16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iconMessage(hwnd, wmSetIcon, iconSmall, small); err != nil {
		t.Fatal(err)
	}
	if err := icons.apply(hwnd); err != nil {
		t.Fatal(err)
	}
	if got := readTaskbarProperty(t, hwnd, 5); got != "eTape.Desktop" {
		t.Fatalf("taskbar identity = %q; a BIG icon alone leaves Explorer using the transparent favicon", got)
	}
	if got := readTaskbarProperty(t, hwnd, 3); got == "" {
		t.Fatal("Explorer has no explicit taskbar icon resource")
	}
	big, err := iconMessage(hwnd, wmGetIcon, iconBig, 0)
	if err != nil || big == 0 || big == small {
		t.Fatalf("BIG icon = %v, error %v; want separate native taskbar icon", big, err)
	}
	if _, err := iconMessage(hwnd, wmSetIcon, iconBig, small); err != nil {
		t.Fatal(err)
	}
	if err := icons.apply(hwnd); err != nil {
		t.Fatal(err)
	}
	if got, err := iconMessage(hwnd, wmGetIcon, iconBig, 0); err != nil || got != big {
		t.Fatalf("Chrome replacement was not corrected: %v, %v", got, err)
	}
	originalDpi := getDpiForWindowCall
	defer func() { getDpiForWindowCall = originalDpi }()
	getDpiForWindowCall = func(...uintptr) (uintptr, uintptr, error) { return 120, 0, nil }
	if err := icons.apply(hwnd); err != nil {
		t.Fatal(err)
	}
	if got, err := iconMessage(hwnd, wmGetIcon, iconBig, 0); err != nil || got == 0 || got != icons.icons[40] {
		t.Fatalf("125%% DPI did not select the native 40px icon: %v, %v", got, err)
	}
	getDpiForWindowCall = originalDpi
	icons.release()
	for _, id := range []uint32{2, 3, 4, 5} {
		if got := readTaskbarProperty(t, hwnd, id); got != "" {
			t.Fatalf("cleanup retained taskbar property %d: %q", id, got)
		}
	}
	if got, err := iconMessage(hwnd, wmGetIcon, iconBig, 0); err != nil || got != 0 {
		t.Fatalf("restart cleanup retained an engine-owned BIG handle: %v, %v", got, err)
	}
	if got, err := iconMessage(hwnd, wmGetIcon, iconSmall, 0); err != nil || got != small {
		t.Fatalf("SMALL title icon changed: %v, %v", got, err)
	}
	if err := icons.apply(hwnd); err != nil {
		t.Fatal(err)
	}
	if _, err := iconMessage(hwnd, wmSetIcon, iconBig, small); err != nil {
		t.Fatal(err)
	}
	store, releaseStore, err := openTaskbarStore(hwnd)
	if err != nil {
		t.Fatal(err)
	}
	err = store.set(3, "foreign.ico,0")
	releaseStore()
	if err != nil {
		t.Fatal(err)
	}
	icons.release()
	if got := readTaskbarProperty(t, hwnd, 3); got != "foreign.ico,0" {
		t.Fatalf("cleanup replaced a foreign taskbar resource: %q", got)
	}
	if got, err := iconMessage(hwnd, wmGetIcon, iconBig, 0); err != nil || got != small {
		t.Fatalf("cleanup changed a foreign icon: %v, %v", got, err)
	}
}

func TestTaskbarShellCacheAndLaunchCommand(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	data := taskbarTestData(t)
	shell, err := newTaskbarShell(data, []string{"-config", `C:\with spaces\config.toml`, "-demo"})
	if err != nil {
		t.Fatal(err)
	}
	args, err := windows.DecomposeCommandLine(shell.values[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 4 || args[1] != "-config" || args[2] != `C:\with spaces\config.toml` || args[3] != "-demo" {
		t.Fatalf("taskbar command = %q", args)
	}
	again, err := newTaskbarShell(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.values[1] != shell.values[1] {
		t.Fatal("same icon data did not reuse the durable resource")
	}
	path := shell.values[1][:len(shell.values[1])-2]
	if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newTaskbarShell(data, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("cache was not repaired: %v", err)
	}
}

func TestTaskbarIconMonitorCanStopConcurrently(t *testing.T) {
	icons := testTaskbarIcons(t, taskbarTestData(t))
	browser := &OwnedBrowser{pid: icons.pid, startToken: icons.startToken}
	release, err := browser.SetTaskbarIcon(icons.data, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(release)
	}
	wg.Wait()
}

func TestIconMessageTimeoutIsBounded(t *testing.T) {
	original := sendIconMessageCall
	defer func() { sendIconMessageCall = original }()
	sendIconMessageCall = func(_, _, _, _, flags, timeout uintptr, _ *uintptr) (uintptr, uintptr, error) {
		if flags != 0x22 || timeout != 200 {
			t.Fatalf("unbounded icon message: flags %v, timeout %v", flags, timeout)
		}
		return 0, 0, windows.ERROR_TIMEOUT
	}
	if _, err := iconMessage(42, wmSetIcon, iconBig, 1); err == nil {
		t.Fatal("ignored a hung window")
	}
}
