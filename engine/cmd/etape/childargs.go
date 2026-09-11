package main

// baseFlags are the launch flags a demo relaunch must preserve.
type baseFlags struct {
	ConfigPath string
	DistDir    string
	LogPath    string
}

// childArgs builds the flag list for a self-triggered demo relaunch (see
// relaunch_unix.go/relaunch_windows.go, which prepend the executable path). It
// rebuilds from known flag values rather than editing os.Args, because -demo
// mutates flag values in place at boot. -no-open is always included: the user
// is mid-session in an open browser tab, so a relaunch must never pop a new
// one.
func childArgs(base baseFlags, demo bool) []string {
	argv := []string{"-config", base.ConfigPath}
	if base.DistDir != "" {
		argv = append(argv, "-dist", base.DistDir)
	}
	if base.LogPath != "" {
		argv = append(argv, "-log", base.LogPath)
	}
	argv = append(argv, "-no-open")
	if demo {
		argv = append(argv, "-demo")
	}
	return argv
}
