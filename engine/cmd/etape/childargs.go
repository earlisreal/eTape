package main

import "strconv"

// baseFlags are the launch flags a relaunch must preserve across a mode switch.
type baseFlags struct {
	ConfigPath string
	DistDir    string
	LogPath    string
}

// replayMode selects what the relaunched process boots into.
type replayMode struct {
	Live  bool
	Day   string
	Speed float64
}

// childArgs builds the flag list for a self-triggered relaunch into a
// different mode (see relaunch_unix.go/relaunch_windows.go, which prepend the
// executable path). It rebuilds from known flag values rather than editing
// os.Args, because -demo mutates flag values in place at boot. -no-open is
// always included: the user is mid-session in an open browser tab (they just
// clicked a UI control), so a relaunch must never pop a new one.
func childArgs(base baseFlags, mode replayMode) []string {
	argv := []string{"-config", base.ConfigPath}
	if base.DistDir != "" {
		argv = append(argv, "-dist", base.DistDir)
	}
	if base.LogPath != "" {
		argv = append(argv, "-log", base.LogPath)
	}
	argv = append(argv, "-no-open")
	if !mode.Live {
		argv = append(argv, "-replay", mode.Day,
			"-speed", strconv.FormatFloat(mode.Speed, 'f', -1, 64), "-replay-hold")
	}
	return argv
}
