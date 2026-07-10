//go:build windows

package singleinstance

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// acquire implements Acquire via LockFileEx. LOCKFILE_FAIL_IMMEDIATELY makes
// the call non-blocking: if another process already holds an exclusive lock
// on this file, LockFileEx returns ERROR_LOCK_VIOLATION immediately instead
// of waiting. bytesLow/bytesHigh cover the whole (empty) file, matching
// flock's whole-file semantics on the unix side.
func acquire(path string) (release func() error, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	var ov windows.Overlapped
	lockErr := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ov)
	if lockErr != nil {
		_ = f.Close()
		if errors.Is(lockErr, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrAlreadyRunning
		}
		return nil, lockErr
	}
	return func() error {
		var uov windows.Overlapped
		unlockErr := windows.UnlockFileEx(h, 0, 1, 0, &uov)
		closeErr := f.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}
