// Command etape is the eTape engine. In this plan it is a minimal harness that
// connects to OpenD and logs connection state + pushes; Plan 6 replaces main with
// the full boot sequence (store → uihub → OpenD → pre-subscribe → exec).
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
)

func main() {
	home, _ := os.UserHomeDir()
	cfgPath := flag.String("config", filepath.Join(home, ".eTape", "config.toml"), "path to config.toml")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := opend.New(opend.Options{
		Addr:      cfg.OpenD.Addr(),
		ClientID:  "etape-engine",
		ClientVer: 100,
		Clock:     clock.System{},
	})

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case st := <-client.State():
				log.Info("opend connection", "state", st)
			case f := <-client.Pushes():
				log.Info("opend push", "protoID", f.ProtoID, "serialNo", f.SerialNo, "bodyLen", len(f.Body))
			}
		}
	}()

	log.Info("connecting to OpenD", "addr", cfg.OpenD.Addr())
	if err := client.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("opend client stopped", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}
