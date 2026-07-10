//go:build tray

// This file supplies the log-destination policy for the tray (windowsgui)
// build. -H=windowsgui (see Makefile) links against the Windows GUI
// subsystem, which has no console attached -- so os.Stderr is a dead handle
// and anything written to it is silently discarded. Without this file, the
// released .exe would produce no logs at all unless launched with -log,
// which a tray-clicked binary never is. defaultLogPath gives boot() a
// fallback destination so every tray build is debuggable out of the box.
package main

import (
	"os"
	"path/filepath"
)

// logToStderr is false here: writing to stderr in a windowsgui process is a
// no-op, and worse, a dead writer placed in an io.MultiWriter alongside the
// log file would abort the whole write on first error, dropping file writes
// too. See openLogFile's caller in boot() for how this is used.
const logToStderr = false

// defaultLogPath returns the log file boot() falls back to when -log is not
// given. Returns "" if the home directory can't be resolved, in which case
// boot() runs with no log destination at all rather than failing to start.
func defaultLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".eTape", "etape.log")
}
