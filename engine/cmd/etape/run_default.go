//go:build !tray

// This file supplies the default (console) entrypoint for cmd/etape. It is
// excluded from tray builds (see run_tray.go, //go:build tray) so exactly
// one main() is compiled for any build-tag permutation.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(boot(ctx, nil))
}
