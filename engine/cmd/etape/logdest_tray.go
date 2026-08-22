//go:build tray

// This file supplies the log-destination policy for the tray (windowsgui)
// build. -H=windowsgui (see Makefile) links against the Windows GUI
// subsystem, which has no console attached -- so os.Stderr is a dead handle
// and anything written to it is silently discarded. The resolved runtime
// profile supplies the fallback file so a tray build remains diagnosable
// without ever inventing a second data-root policy here.
package main

// logToStderr is false here: writing to stderr in a windowsgui process is a
// no-op, and worse, a dead writer placed in an io.MultiWriter alongside the
// log file would abort the whole write on first error, dropping file writes
// too. See openLogFile's caller in boot() for how this is used.
const logToStderr = false

// defaultLogPath returns the resolved profile log file when -log is not given.
func defaultLogPath(profileLogPath string) string {
	return profileLogPath
}
