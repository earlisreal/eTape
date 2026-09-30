//go:build !tray

// This file supplies the default (console) entrypoint for cmd/etape. It is
// excluded from tray builds (see run_tray.go, //go:build tray) so exactly
// one main() is compiled for any build-tag permutation.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var held exec.HeldShutdownSummary
	code, restart, nextArgs := boot(ctx, nil, func(s exec.HeldShutdownSummary) { held = s })
	if restart {
		// On Unix relaunch() execs and never returns on success, so this
		// branch is only reached on failure. On Windows it spawns a new
		// process and returns, and os.Exit(code) below retires this one.
		if err := relaunch(nextArgs); err != nil {
			slog.Default().Error("relaunch failed", "err", err)
		}
	} else if held.Unconfirmed > 0 {
		fmt.Fprintf(os.Stderr, "eTape could not confirm cancellation for %d engine-held child order(s). Type FORCE to exit anyway; press Enter to restart eTape and reconcile. ", held.Unconfirmed)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.EqualFold(strings.TrimSpace(answer), "force") {
			slog.Default().Warn("force exit with unconfirmed engine-held child cancellations", "count", held.Unconfirmed)
		} else if err := relaunch(os.Args[1:]); err != nil {
			slog.Default().Error("restart for held-order reconciliation failed", "err", err)
		}
	}
	os.Exit(code)
}
