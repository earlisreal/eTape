//go:build tray && windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

func confirmForceExitHeldOrders(summary exec.HeldShutdownSummary) bool {
	text, _ := syscall.UTF16PtrFromString(fmt.Sprintf(
		"eTape could not confirm cancellation for %d engine-held child order(s).\n\nYes: FORCE EXIT despite this risk.\nNo: Restart eTape and reconcile before continuing.",
		summary.Unconfirmed,
	))
	title, _ := syscall.UTF16PtrFromString("Unconfirmed held-order cancellation")
	result, _, _ := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(
		0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x00000004|0x00000030|0x00000100,
	)
	return result == 6 // IDYES; every other result keeps eTape running via restart.
}
