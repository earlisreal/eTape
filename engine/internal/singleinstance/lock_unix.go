//go:build !windows

package singleinstance

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// acquire implements Acquire via flock(2). LOCK_NB makes the call
// non-blocking: if another process already holds LOCK_EX on this fd's
// file, Flock returns EWOULDBLOCK immediately instead of waiting.
func acquire(path string) (release func() error, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	return func() error {
		unlockErr := unix.Flock(int(f.Fd()), unix.LOCK_UN)
		closeErr := f.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}
