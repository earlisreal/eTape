// Command etape is the eTape engine. In this plan it is the market-data +
// persistence harness: connect OpenD → journal tee → md core, archive finalized
// bars, and (with --replay) reconstruct a recorded day from the journal. Plan 6
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
	"sync"
	"syscall"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/replay"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
)

func main() {
	home, _ := os.UserHomeDir()
	cfgPath := flag.String("config", filepath.Join(home, ".eTape", "config.toml"), "path to config.toml")
	watch := flag.String("watch", "", "comma-separated symbols to watch (adds to config watchlist)")
	focus := flag.String("focus", "", "comma-separated symbols to focus (adds depth + quote)")
	replayDay := flag.String("replay", "", "replay a recorded day (YYYY-MM-DD) instead of connecting to OpenD")
	speed := flag.Float64("speed", 0, "replay speed factor (>0: real-time x speed; <=0: as fast as possible)")
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
	dbPath := cfg.Store.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(home, ".eTape", "etape.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Error("make db dir", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(store.Options{
		Path:          dbPath,
		Clock:         clock.System{},
		FlushInterval: time.Duration(cfg.Store.FlushMs) * time.Millisecond,
	})
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	// NOTE: no `defer st.Close()`. The feed pipe writes to the store on a blocking
	// send, so the store must be closed ONLY after that goroutine has stopped —
	// otherwise RecordEvent races close(s.writes) (send on closed channel → panic).
	// We join pipeWG below, then Close explicitly.

	core := md.New(md.Config{TapeRing: cfg.MD.TapeRing, AnchorSecs: anchorSecs})
	go func() { _ = core.Run(ctx) }()
	go drainMarks(ctx, core)

	var pipeWG sync.WaitGroup // tracks the feed→core pipe goroutine(s)
	live := *replayDay == ""
	if live {
		if n, err := st.PruneJournal(cfg.Store.RetentionDays); err != nil {
			log.Warn("prune journal", "err", err)
		} else if n > 0 {
			log.Info("pruned journal", "rows", n)
		}
		st.AppendSysEvent("boot", "engine up")
		startLive(ctx, log, st, core, cfg, splitCSV(*watch), splitCSV(*focus), &pipeWG)
		log.Info("engine up (live)", "opend", cfg.OpenD.Addr(), "anchor", cfg.MD.SessionAnchor, "db", dbPath)
	}
	replayOK := true
	if !live {
		replayOK = startReplay(ctx, log, st, core, *replayDay, *speed, splitCSV(*focus), &pipeWG, stop)
	}

	// Consume updates until shutdown; tap finalized 1m/daily bars to the archive
	// (live only — replay must not rewrite the archive it reads from).
	var archive *store.Store
	if live {
		archive = st
	}
	drainUpdates(ctx, log, core, archive, *verbose)

	// ctx is done: join the pipe (no more store writes) BEFORE closing the store.
	pipeWG.Wait()
	if err := st.Close(); err != nil {
		log.Error("close store", "err", err)
	}
	log.Info("shutdown complete", "droppedUpdates", core.DroppedUpdates(), "droppedJournal", st.DroppedJournalRows())

	if !live && !replayOK {
		os.Exit(1)
	}
}

// startLive wires OpenD → journal tee → core and installs demands + indicators.
func startLive(ctx context.Context, log *slog.Logger, st *store.Store, core *md.Core, cfg config.Config, watch, focus []string, pipeWG *sync.WaitGroup) {
	client := opend.New(opend.Options{Addr: cfg.OpenD.Addr(), Clock: clock.System{}})
	fd := opend.NewOpenDFeed(client, opend.FeedOptions{
		Budget:              cfg.Feed.QuotaSlots,
		Hysteresis:          time.Duration(cfg.Feed.UnsubHysteresisSecs) * time.Second,
		DisableExtendedTime: !cfg.Feed.ExtendedTime,
	})
	go func() { _ = client.Run(ctx) }()
	go func() { _ = fd.Run(ctx) }()
	pipeWG.Add(1)
	go pipe(ctx, pipeWG, fd.Events(), core, st) // st != nil → journal tee active

	seen := 0
	for _, s := range append(cfg.Feed.Watchlist, watch...) {
		fd.Ensure(feed.WatchDemand("boot-watch-"+s, s))
		seen++
	}
	for _, s := range focus {
		fd.Ensure(feed.FocusedDemand("boot-focus-"+s, s))
		seen++
	}
	if seen == 0 {
		log.Warn("no symbols demanded; pass --watch/--focus or set [feed].watchlist")
	}
	setupIndicators(core, focus)
}

// startReplay wires replay.Feed → core (no journal tee) from a recorded day.
// It returns false if the day couldn't be replayed (no such journal, or empty),
// in which case main must not block waiting for a run that never started.
func startReplay(ctx context.Context, log *slog.Logger, st *store.Store, core *md.Core, day string, speed float64, focus []string, pipeWG *sync.WaitGroup, stop context.CancelFunc) bool {
	rows, err := st.ReadJournalDay(day)
	if err != nil {
		log.Error("read journal", "err", err, "day", day)
		return false
	}
	if len(rows) == 0 {
		log.Warn("no journal rows for day", "day", day)
		return false
	}
	// sim advances to each event's exchange timestamp but has no runtime consumer
	// yet (md.Core is clock-free by design); correctness is proven by
	// replay/clock_test.go alone until Plan 4's SimBroker consumes it.
	sim := replay.NewClock(time.UnixMilli(rows[0].TsExch))
	fd := replay.NewFeed(replay.FeedOptions{Rows: rows, Sim: sim, Pace: clock.System{}, Speed: speed})
	go func() { _ = fd.Run(ctx) }()
	pipeWG.Add(1)
	go pipe(ctx, pipeWG, fd.Events(), core, nil) // nil journal → no re-recording
	go func() { pipeWG.Wait(); stop() }()        // self-terminate once the journal is exhausted
	setupIndicators(core, focus)
	log.Info("engine up (replay)", "day", day, "rows", len(rows), "speed", speed)
	return true
}

// pipe forwards feed events into the core, journaling each first when journal != nil.
// It owns a pipeWG slot so main can join it before closing the store.
func pipe(ctx context.Context, wg *sync.WaitGroup, in <-chan feed.Event, core *md.Core, journal *store.Store) {
	defer wg.Done()
	sys := clock.System{}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-in:
			if !ok { // replay feed exhausted
				return
			}
			if journal != nil {
				journal.RecordEvent(ev, sys.Now().UnixMilli())
			}
			core.Feed(ev)
		}
	}
}

func setupIndicators(core *md.Core, focus []string) {
	if len(focus) == 0 {
		return
	}
	f := focus[0]
	core.EnsureIndicator("harness-vwap", md.IndicatorSpec{Symbol: f, TF: session.TF1m, Type: md.IndVWAP})
	core.EnsureIndicator("harness-ema9", md.IndicatorSpec{Symbol: f, TF: session.TF1m, Type: md.IndEMA,
		Params: map[string]float64{"period": 9}})
}

func drainMarks(ctx context.Context, core *md.Core) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-core.Marks():
		}
	}
}

func drainUpdates(ctx context.Context, log *slog.Logger, core *md.Core, archive *store.Store, verbose bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-core.Updates():
			switch v := u.(type) {
			case md.BarUpdate:
				if v.Bar.InProgress {
					continue
				}
				if archive != nil {
					b := feed.Bar{Symbol: v.Bar.Symbol, BucketMs: v.Bar.BucketMs,
						O: v.Bar.O, H: v.Bar.H, L: v.Bar.L, C: v.Bar.C, Volume: v.Bar.V}
					switch v.Bar.TF {
					case session.TF1m:
						archive.ArchiveBar1m(b)
					case session.TFDay:
						archive.ArchiveDaily(b)
					}
				}
				log.Info("bar", "sym", v.Bar.Symbol, "tf", v.Bar.TF, "bucket", v.Bar.BucketMs,
					"o", v.Bar.O, "h", v.Bar.H, "l", v.Bar.L, "c", v.Bar.C,
					"v", v.Bar.V, "delta", v.Bar.BuyV-v.Bar.SellV, "ticks", v.Bar.Ticks, "gap", v.Bar.Gap)
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
				if verbose {
					log.Info("quote", "sym", v.Quote.Symbol, "last", v.Quote.Last)
				}
			case md.BookUpdate:
				if verbose {
					log.Info("book", "sym", v.Book.Symbol, "bids", len(v.Book.Bids), "asks", len(v.Book.Asks))
				}
			case md.TapeUpdate:
				if verbose {
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
