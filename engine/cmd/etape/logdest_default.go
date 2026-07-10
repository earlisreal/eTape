//go:build !tray

// This file supplies the log-destination policy for the default (console)
// build. It has a real, usable stderr (a terminal, or whatever the caller
// redirected it to), so unlike the tray build (logdest_tray.go) it needs no
// default log file -- a file is written only if -log is explicitly given.
package main

// logToStderr is true: the console build always has a usable stderr.
const logToStderr = true

// defaultLogPath returns "" -- the console build only writes a log file when
// -log is explicitly passed, matching its behavior before this file existed.
func defaultLogPath() string {
	return ""
}
