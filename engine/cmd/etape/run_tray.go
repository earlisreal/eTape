//go:build tray

// This file supplies the Windows release entrypoint for cmd/etape: a system-
// tray icon (no console window) instead of a plain console process.
// systray.Run owns the OS thread, so all real work (boot's full sequence,
// and reacting to menu clicks) happens in goroutines started from onReady.
// Quit is the only shutdown path in this build -- there is no OS-signal
// handling here, unlike run_default.go's signal.NotifyContext, because a
// tray-only process has no controlling terminal to send it a Ctrl-C from.
package main

import (
	"context"
	_ "embed"
	"log/slog"
	"sync"

	"fyne.io/systray"

	"github.com/earlisreal/eTape/engine/internal/openbrowser"
)

//go:embed assets/etape.ico
var trayIcon []byte

func main() {
	systray.Run(onReady, onExit)
}

// addrMu/addr capture the uihub listening address reported by boot's
// onListening callback, so the "Open eTape" menu action knows where to
// point the browser. Guarded by a mutex since it's written from boot's
// goroutine and read from the menu-click loop.
var (
	addrMu sync.Mutex
	addr   string
)

func captureAddr(a string) {
	addrMu.Lock()
	addr = a
	addrMu.Unlock()
}

func currentAddr() string {
	addrMu.Lock()
	defer addrMu.Unlock()
	return addr
}

func onReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("eTape")
	systray.SetTooltip("eTape")

	open := systray.AddMenuItem("Open eTape", "Open the eTape UI in your browser")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Quit eTape")

	// No OS-signal handling: Quit (below) is the only shutdown path in this
	// build.
	ctx, cancel := context.WithCancel(context.Background())

	// This goroutine is the sole owner of systray.Quit(): it only tears down
	// the tray after boot has actually returned, whether that's because Quit
	// (below) cancelled ctx and boot ran its ordered shutdown, boot
	// self-terminated on its own (non-`-replay-hold` replay mode), or boot
	// failed outright. That keeps shutdown to exactly one path, so the
	// process never exits mid-shutdown and never leaves a ghost tray icon
	// behind on a failure exit.
	go func() {
		code, restart := boot(ctx, captureAddr)
		if code != 0 {
			// boot() already called slog.SetDefault with a handler writing
			// to stderr (and the -log file, if one was given) before
			// returning from almost every path, including error returns --
			// so slog.Default() here lands in the same "-log file
			// mechanism from Task 3" boot itself uses. There's nothing
			// meaningful left to show in a tray with no engine behind it.
			slog.Default().Error("boot failed", "code", code)
		}
		if restart {
			// Spawn the replacement process BEFORE systray.Quit() below --
			// boot has already returned here, so its deferred cleanup
			// (releaseLock, st.Close, ...) has already released the
			// single-instance lock and uihub port the new process needs.
			// Ordering it before Quit avoids a window where this process
			// could exit before the new one is spawned; the brief two-icon
			// overlap while the new tray starts up is an accepted tradeoff.
			if err := relaunch(); err != nil {
				slog.Default().Error("relaunch failed", "err", err)
			}
		}
		systray.Quit()
	}()

	go func() {
		for {
			select {
			case <-open.ClickedCh:
				a := currentAddr()
				if a == "" {
					slog.Default().Warn("open eTape: engine is not listening yet")
					continue
				}
				if err := openbrowser.Open("http://" + a); err != nil {
					slog.Default().Warn("open browser", "err", err)
				}
			case <-quit.ClickedCh:
				cancel()
				return
			}
		}
	}()
}

// onExit runs after systray.Quit() tears down the tray. systray.Quit() is
// only ever called from the boot goroutine above, after boot(ctx,
// captureAddr) has returned -- so boot's own shutdown sequence has already
// blocked until every goroutine is joined and the store is closed by the
// time onExit runs. There is nothing left to do here.
func onExit() {}
