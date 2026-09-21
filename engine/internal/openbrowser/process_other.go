//go:build !windows

package openbrowser

import "fmt"

func restoreOwnedProcessWindows(int, uint64, string, string, []WindowSpec) {}

func ownedProcessStartTime(int) (uint64, error) {
	return 0, fmt.Errorf("owned Chrome is supported only on Windows")
}

func verifyOwnedProcess(int, uint64) error {
	return fmt.Errorf("owned Chrome is supported only on Windows")
}

func ownedProcessExists(int, uint64) (bool, error) {
	return false, fmt.Errorf("owned Chrome is supported only on Windows")
}

func stopOwnedProcess(int, uint64, bool) error {
	return fmt.Errorf("owned Chrome is supported only on Windows")
}
