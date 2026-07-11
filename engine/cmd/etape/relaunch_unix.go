//go:build !windows

package main

import (
	"os"
	"syscall"
)

// relaunch replaces the current process image in place (same PID) so a
// UI-triggered "Restart engine now" applies fresh config without leaving a
// stale process around. It is only ever called from an entrypoint (main) once
// boot has already returned -- at that point every deferred cleanup inside
// boot (releaseLock, st.Close, httpSrv shutdown, ...) has already run, so the
// single-instance lock and uihub port are free before the re-exec'd image
// re-acquires them.
//
// syscall.Exec keeps the same PID, which matters under `go run`: the compiled
// temp binary stays on disk for the life of the process, and a same-PID exec
// stays under the original `go run` supervision (a spawned child process
// would not). os.Args is reused as-is so CLI flags survive the restart
// unchanged (flag.Parse runs again inside the re-exec'd boot).
//
// Never returns on success -- the process image is replaced.
func relaunch() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
