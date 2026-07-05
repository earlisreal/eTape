// Command etape is the eTape engine. In this plan it is the market-data
// harness: connect OpenD → feed adapter → md core, subscribe the watchlist
// (+ --focus symbols with depth), and log what the core emits. Plan 6
// replaces main with the full boot sequence (store → uihub → OpenD → exec).
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func main() {
	home, _ := os.UserHomeDir()
	cfgPath := flag.String("config", filepath.Join(home, ".eTape", "config.toml"), "path to config.toml")
	watch := flag.String("watch", "", "comma-separated symbols to watch (adds to config watchlist)")
	focus := flag.String("focus", "", "comma-separated symbols to focus (adds depth + quote)")
	verbose := flag.Bool("v", false, "log quotes/books/tape (noisy)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}
	anchorSecs, err := cfg.MD.AnchorSecs()
	if err != nil {
		log.Error("bad session_anchor", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := opend.New(opend.Options{Addr: cfg.OpenD.Addr(), Clock: clock.System{}})
	fd := opend.NewOpenDFeed(client, opend.FeedOptions{
		Budget:              cfg.Feed.QuotaSlots,
		Hysteresis:          time.Duration(cfg.Feed.UnsubHysteresisSecs) * time.Second,
		DisableExtendedTime: !cfg.Feed.ExtendedTime,
	})
	core := md.New(md.Config{TapeRing: cfg.MD.TapeRing, AnchorSecs: anchorSecs})

	go func() { _ = client.Run(ctx) }()
	go func() { _ = fd.Run(ctx) }()
	go func() { _ = core.Run(ctx) }()
	go func() { // the feed→core pipe; Plan 3's journal tee slots in here
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-fd.Events():
				core.Feed(ev)
			}
		}
	}()

	// Demands: config watchlist + --watch as watch profile, --focus focused.
	seen := 0
	for _, s := range append(cfg.Feed.Watchlist, splitCSV(*watch)...) {
		fd.Ensure(feed.WatchDemand("boot-watch-"+s, s))
		seen++
	}
	var firstFocus string
	for _, s := range splitCSV(*focus) {
		fd.Ensure(feed.FocusedDemand("boot-focus-"+s, s))
		if firstFocus == "" {
			firstFocus = s
		}
		seen++
	}
	if seen == 0 {
		log.Warn("no symbols demanded; pass --watch/--focus or set [feed].watchlist")
	}
	if firstFocus != "" { // prove the indicator pipeline end-to-end
		core.EnsureIndicator("harness-vwap", md.IndicatorSpec{Symbol: firstFocus, TF: session.TF1m, Type: md.IndVWAP})
		core.EnsureIndicator("harness-ema9", md.IndicatorSpec{Symbol: firstFocus, TF: session.TF1m, Type: md.IndEMA,
			Params: map[string]float64{"period": 9}})
	}

	go func() { // drain marks (exec's input in Plan 4)
		for {
			select {
			case <-ctx.Done():
				return
			case <-core.Marks():
			}
		}
	}()

	log.Info("engine up", "opend", cfg.OpenD.Addr(), "anchor", cfg.MD.SessionAnchor)
	for {
		select {
		case <-ctx.Done():
			log.Info("shutdown complete", "droppedUpdates", core.DroppedUpdates())
			return
		case u := <-core.Updates():
			switch v := u.(type) {
			case md.BarUpdate:
				if !v.Bar.InProgress {
					log.Info("bar", "sym", v.Bar.Symbol, "tf", v.Bar.TF, "bucket", v.Bar.BucketMs,
						"o", v.Bar.O, "h", v.Bar.H, "l", v.Bar.L, "c", v.Bar.C,
						"v", v.Bar.V, "delta", v.Bar.BuyV-v.Bar.SellV, "ticks", v.Bar.Ticks, "gap", v.Bar.Gap)
				}
			case md.IndicatorUpdate:
				if v.Snapshot {
					log.Info("indicator snapshot", "id", v.InstanceID, "key", v.SeriesKey, "points", len(v.Points))
				}
			case md.MismatchUpdate:
				log.Warn("1m mismatch", "sym", v.Symbol, "bucket", v.BucketMs, "detail", v.Detail)
			case md.ConnUpdate:
				log.Info("feed connection", "up", v.Up)
			case md.ResyncedUpdate:
				log.Info("feed resynced")
			case md.QuoteUpdate:
				if *verbose {
					log.Info("quote", "sym", v.Quote.Symbol, "last", v.Quote.Last)
				}
			case md.BookUpdate:
				if *verbose {
					log.Info("book", "sym", v.Book.Symbol, "bids", len(v.Book.Bids), "asks", len(v.Book.Asks))
				}
			case md.TapeUpdate:
				if *verbose {
					log.Info("tape", "sym", v.Symbol, "ticks", len(v.Ticks))
				}
			}
		}
	}
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
