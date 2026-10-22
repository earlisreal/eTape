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
	pid := int(windows.GetCurrentProcessId())
	token, err := ownedProcessStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	return &taskbarIcons{pid: pid, startToken: token, data: data,
		icons: make(map[int]uintptr), touched: make(map[windows.HWND]struct{})}
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
	icons.release()
	if got, err := iconMessage(hwnd, wmGetIcon, iconBig, 0); err != nil || got != small {
		t.Fatalf("cleanup changed a foreign icon: %v, %v", got, err)
	}
}

func TestTaskbarIconMonitorCanStopConcurrently(t *testing.T) {
	icons := testTaskbarIcons(t, taskbarTestData(t))
	browser := &OwnedBrowser{pid: icons.pid, startToken: icons.startToken}
	release, err := browser.SetTaskbarIcon(icons.data)
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
