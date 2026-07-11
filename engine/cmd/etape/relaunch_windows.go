//go:build windows

package main

import "os"

// relaunch spawns a fresh, detached process running the same binary with the
// same args and environment, then releases the *os.Process handle without
// waiting on it. Windows has no exec-that-replaces-the-current-image
// primitive (unlike syscall.Exec on Unix), so a restart here means: spawn the
// new process, then let the caller (main, or run_tray.go's boot goroutine)
// exit/Quit the old one right after.
//
// Like relaunch_unix.go, this is only ever called once boot has already
// returned, so the single-instance lock and uihub port are already free
// before the child calls singleinstance.Acquire.
//
// Files is left with three nil entries (no inherited stdin/stdout/stderr) so
// this is safe to call from the windowsgui (tray) build, which has no console
// handles to inherit in the first place.
func relaunch() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	proc, err := os.StartProcess(exe, os.Args, &os.ProcAttr{
		Env:   os.Environ(),
		Files: []*os.File{nil, nil, nil},
	})
	if err != nil {
		return err
	}
	return proc.Release()
}
