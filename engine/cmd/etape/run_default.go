//go:build !tray

// This file supplies the default (console) entrypoint for cmd/etape. It is
// excluded from tray builds (see run_tray.go, //go:build tray) so exactly
// one main() is compiled for any build-tag permutation.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code, restart := boot(ctx, nil)
	if restart {
		// On Unix relaunch() execs and never returns on success, so this
		// branch is only reached on failure. On Windows it spawns a new
		// process and returns, and os.Exit(code) below retires this one.
		if err := relaunch(); err != nil {
			slog.Default().Error("relaunch failed", "err", err)
		}
	}
	os.Exit(code)
}
