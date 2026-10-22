//go:build windows && taskbar_smoke

package openbrowser

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsExecutableIcon(t *testing.T) {
	path, err := filepath.Abs("../../../dist/etape-windows-amd64.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("build the Windows release before checking its PE icon")
	} else if err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	var big, small uintptr
	count, _, err := windows.NewLazySystemDLL("shell32.dll").NewProc("ExtractIconExW").Call(
		uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&big)), uintptr(unsafe.Pointer(&small)), 1)
	if count == 0 || uint32(count) == ^uint32(0) || big == 0 || small == 0 {
		t.Fatalf("extract executable icon: count %d, error %v", count, err)
	}
	defer func() { _, _, _ = destroyIcon.Call(big); _, _, _ = destroyIcon.Call(small) }()
	actual, err := nativeIconBitmap(big)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := icoImage(taskbarTestData(t), actual.Bounds().Dx())
	if err != nil {
		t.Fatal(err)
	}
	expected, _, err := image.Decode(bytes.NewReader(frame))
	if err != nil {
		t.Fatal(err)
	}
	for y := range actual.Bounds().Dy() {
		for x := range actual.Bounds().Dx() {
			r, g, b, a := expected.At(x, y).RGBA()
			if a == 0xffff {
				p := actual.RGBAAt(x, y)
				if uint32(p.R)*257 != r || uint32(p.G)*257 != g || uint32(p.B)*257 != b || p.A != 255 {
					t.Fatalf("executable icon differs from approved tiled asset at %d,%d", x, y)
				}
			}
		}
	}
	t.Logf("Windows extracts the approved %dpx tiled icon from the built executable", actual.Bounds().Dx())
}

func useBuiltTaskbarExecutable(t *testing.T) {
	t.Helper()
	path, err := filepath.Abs("../../../dist/etape-windows-amd64.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("build the Windows release before running native taskbar checks: %v", err)
	}
	original := taskbarExecutable
	taskbarExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { taskbarExecutable = original })
}

func TestWindowsTaskbarResourceIcon(t *testing.T) {
	useBuiltTaskbarExecutable(t)
	shell, err := newTaskbarShell(nil)
	if err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(shell.values[1][:len(shell.values[1])-2])
	if err != nil {
		t.Fatal(err)
	}
	var big, small uintptr
	hr, _, _ := windows.NewLazySystemDLL("shell32.dll").NewProc("SHDefExtractIconW").Call(
		uintptr(unsafe.Pointer(path)), 0, 0, uintptr(unsafe.Pointer(&big)), uintptr(unsafe.Pointer(&small)), 32|16<<16)
	if int32(hr) < 0 || big == 0 {
		t.Fatalf("extract shell taskbar resource: HRESULT %#x, BIG %x", hr, big)
	}
	defer func() { _, _, _ = destroyIcon.Call(big); _, _, _ = destroyIcon.Call(small) }()
	bitmap, err := nativeIconBitmap(big)
	if err != nil {
		t.Fatal(err)
	}
	pale, green, red := 0, 0, 0
	for y := range bitmap.Bounds().Dy() {
		for x := range bitmap.Bounds().Dx() {
			p := bitmap.RGBAAt(x, y)
			if p.A == 255 && p.R == 242 && p.G == 243 && p.B == 248 {
				pale++
			}
			if p.A == 255 && p.R == 23 && p.G == 122 && p.B == 88 {
				green++
			}
			if p.A == 255 && p.R == 194 && p.G == 51 && p.B == 77 {
				red++
			}
		}
	}
	if pale < 100 || green < 10 || red < 2 {
		t.Fatalf("shell resource is not the tiled mark: pale=%d green=%d red=%d", pale, green, red)
	}
	t.Logf("shell extracts tiled mark: pale=%d green=%d red=%d", pale, green, red)
}

// Opt-in: launches only a temporary Chrome profile, never the running eTape session.
func TestChromeTaskbarIcons(t *testing.T) {
	useBuiltTaskbarExecutable(t)
	chrome := findChrome()
	if chrome == "" {
		t.Fatal("Chrome is required for the taskbar smoke test")
	}
	favicon, err := os.ReadFile("../../../ui/public/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	reloaded := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			if r.URL.RawQuery == "reload" {
				select {
				case reloaded <- struct{}{}:
				default:
				}
			}
			w.Header().Set("Content-Type", "image/x-icon")
			_, _ = w.Write(favicon)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<title>eTape icon smoke test</title><link rel="icon" href="/favicon.ico"><script>setTimeout(() => {document.querySelector('link').href='/favicon.ico?reload';document.title+=' updated';}, 2500)</script>`))
	}))
	defer server.Close()
	profile := t.TempDir()
	cmd := ownedChromeCommand(chrome, server.URL, profile)
	cmd.Args = append(cmd.Args, "--window-position=-10000,-10000", "--window-size=500,300")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	token, err := ownedProcessStartTime(pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	defer func() { _ = stopOwnedProcess(pid, token, true); _ = cmd.Wait() }()
	var hwnd windows.HWND
	callback := windows.NewCallback(func(handle windows.HWND, _ uintptr) uintptr {
		var owner uint32
		_, _ = windows.GetWindowThreadProcessId(handle, &owner)
		if owner == uint32(pid) {
			var class [64]uint16
			_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("GetClassNameW").Call(
				uintptr(handle), uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
			if windows.UTF16ToString(class[:]) == "Chrome_WidgetWin_1" {
				hwnd = handle
				return 0
			}
		}
		return 1
	})
	deadline := time.Now().Add(10 * time.Second)
	for hwnd == 0 && time.Now().Before(deadline) {
		_ = windows.EnumWindows(callback, nil)
		time.Sleep(25 * time.Millisecond)
	}
	if hwnd == 0 {
		t.Fatal("temporary Chrome app window not found")
	}
	_, _, _ = showWindowAsyncCall(uintptr(hwnd), 7) // SW_SHOWMINNOACTIVE
	waitIcon := func(kind uintptr, tiled bool) *image.RGBA {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			handle, err := iconMessage(hwnd, wmGetIcon, kind, 0)
			if err == nil && handle != 0 {
				bitmap, err := nativeIconBitmap(handle)
				if err == nil {
					x, y := 0, bitmap.Bounds().Dy()/2
					pixel := bitmap.RGBAAt(x, y)
					if tiled && pixel.A == 255 && pixel.R == 242 && pixel.G == 243 && pixel.B == 248 {
						return bitmap
					}
					if !tiled && pixel.A == 0 {
						for y := range bitmap.Bounds().Dy() {
							for x := range bitmap.Bounds().Dx() {
								p := bitmap.RGBAAt(x, y)
								if p.R == 23 && p.G == 122 && p.B == 88 && p.A == 255 {
									return bitmap
								}
							}
						}
					}
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("native icon kind %d did not become tiled=%v", kind, tiled)
		return nil
	}
	waitIcon(iconSmall, false)
	originalAppID := readTaskbarProperty(t, hwnd, 5)
	browser := &OwnedBrowser{pid: pid, startToken: token}
	release, err := browser.SetTaskbarIcon(taskbarTestData(t), []string{"-demo"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	big := waitIcon(iconBig, true)
	small := waitIcon(iconSmall, false)
	checkShell := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for readTaskbarProperty(t, hwnd, 5) != taskbarAppID && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
		if got := readTaskbarProperty(t, hwnd, 5); got != taskbarAppID {
			t.Fatalf("Explorer taskbar identity = %q", got)
		}
		resource := readTaskbarProperty(t, hwnd, 3)
		if len(resource) < 3 || resource[len(resource)-2:] != ",0" {
			t.Fatalf("Explorer taskbar resource = %q", resource)
		}
		exe, err := taskbarExecutable()
		if err != nil || resource != exe+",0" {
			t.Fatalf("Explorer must use the verified executable resource: %q, %v", resource, err)
		}
		if got := readTaskbarProperty(t, hwnd, 4); got != "eTape" {
			t.Fatalf("taskbar display name = %q", got)
		}
	}
	checkShell()
	dpi, _, _ := getDpiForWindowCall(uintptr(hwnd))
	wantSize, _, _ := getMetricsForDpi.Call(11, dpi)
	if big.Bounds().Dx() != int(wantSize) {
		t.Fatalf("native BIG icon is %dpx, want %dpx for DPI %d", big.Bounds().Dx(), wantSize, dpi)
	}
	select {
	case <-reloaded:
	case <-time.After(10 * time.Second):
		t.Fatal("favicon reload not observed")
	}
	time.Sleep(time.Second)
	checkShell()
	chromeSmall, err := iconMessage(hwnd, wmGetIcon, iconSmall, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iconMessage(hwnd, wmSetIcon, iconBig, chromeSmall); err != nil {
		t.Fatal(err)
	}
	waitIcon(iconBig, true)
	waitIcon(iconSmall, false)
	release()
	if got := readTaskbarProperty(t, hwnd, 5); got != originalAppID {
		t.Fatalf("handoff did not restore Chrome's taskbar identity: %q, want %q", got, originalAppID)
	}
	if got, err := iconMessage(hwnd, wmGetIcon, iconBig, 0); err != nil || got != 0 {
		t.Fatalf("handoff left an engine-owned icon: %v, %v", got, err)
	}
	adopted, err := AdoptOwned(pid, token, profile, server.URL, uintptr(hwnd))
	if err != nil {
		t.Fatal(err)
	}
	releaseAdopted, err := adopted.SetTaskbarIcon(taskbarTestData(t), []string{"-demo"})
	if err != nil {
		t.Fatal(err)
	}
	defer releaseAdopted()
	waitIcon(iconBig, true)
	waitIcon(iconSmall, false)
	checkShell()
	if dir := os.Getenv("ETAPE_ICON_CAPTURE_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, bitmap := range map[string]*image.RGBA{"taskbar": big, "titlebar": small} {
			file, err := os.Create(filepath.Join(dir, name+"-native.png"))
			if err != nil {
				t.Fatal(err)
			}
			encodeErr := png.Encode(file, bitmap)
			closeErr := file.Close()
			if encodeErr != nil || closeErr != nil {
				t.Fatalf("capture %s: encode %v, close %v", name, encodeErr, closeErr)
			}
		}
	}
	t.Logf("native %dpx BIG icon is tiled; %dpx SMALL is transparent; refresh and adoption pass", big.Bounds().Dx(), small.Bounds().Dx())
}

func nativeIconBitmap(icon uintptr) (*image.RGBA, error) {
	user32, gdi32 := windows.NewLazySystemDLL("user32.dll"), windows.NewLazySystemDLL("gdi32.dll")
	var info struct {
		Icon, XHotspot, YHotspot uint32
		Mask, Color              uintptr
	}
	if ok, _, err := user32.NewProc("GetIconInfo").Call(icon, uintptr(unsafe.Pointer(&info))); ok == 0 {
		return nil, err
	}
	defer func() {
		_, _, _ = gdi32.NewProc("DeleteObject").Call(info.Mask)
		_, _, _ = gdi32.NewProc("DeleteObject").Call(info.Color)
	}()
	var bitmap struct {
		Type, Width, Height, WidthBytes int32
		Planes, BitsPixel               uint16
		Bits                            uintptr
	}
	if ok, _, err := gdi32.NewProc("GetObjectW").Call(info.Color, unsafe.Sizeof(bitmap), uintptr(unsafe.Pointer(&bitmap))); ok == 0 {
		return nil, err
	}
	var header struct {
		Size               uint32
		Width, Height      int32
		Planes, Bits       uint16
		Compression, Bytes uint32
		XMeter, YMeter     int32
		Colors, Important  uint32
	}
	header.Size, header.Width, header.Height, header.Planes, header.Bits = 40, bitmap.Width, -bitmap.Height, 1, 32
	pixels := make([]byte, int(bitmap.Width*bitmap.Height*4))
	dc, _, _ := gdi32.NewProc("CreateCompatibleDC").Call(0)
	defer func() { _, _, _ = gdi32.NewProc("DeleteDC").Call(dc) }()
	if ok, _, err := gdi32.NewProc("GetDIBits").Call(dc, info.Color, 0, uintptr(bitmap.Height), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&header)), 0); ok == 0 {
		return nil, err
	}
	for i := 0; i < len(pixels); i += 4 {
		pixels[i], pixels[i+2] = pixels[i+2], pixels[i]
	}
	return &image.RGBA{Pix: pixels, Stride: int(bitmap.Width) * 4, Rect: image.Rect(0, 0, int(bitmap.Width), int(bitmap.Height))}, nil
}
